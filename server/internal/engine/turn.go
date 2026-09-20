package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// Say는 사용자가 한 말 하나다.
type Say struct {
	// ClientMessageID는 클라이언트가 글마다 새로 만드는 식별자다. 같은 값의 글은 한 번만 저장된다.
	ClientMessageID uuid.UUID
	Text            string
	// Modality가 비어 있으면 대화를 시작할 때의 방식이다.
	Modality string
	// STTMinConfidence는 음성 인식이 이 발화에서 가장 낮게 준 확신도다. 글로 쓴 말에는 nil이다.
	STTMinConfidence *float32
}

// Turn은 한 턴이 어떻게 끝났는지다. 말의 내용은 담지 않으므로 통째로 로그에 남겨도 된다.
//
// 단계는 화면에 보내지 않는다. 부르는 쪽이 로그와 지표에만 쓴다.
type Turn struct {
	// Stage는 관문의 최종 단계다.
	Stage crisis.Stage
	// Origin은 나간 말의 출처다(store.Origin*). 말이 나가지 않았으면 비어 있다.
	Origin string
	// Seq는 사용자의 글에 붙은 순번이다.
	Seq int32
	// Duplicate는 같은 글이 이미 저장되어 있었다는 뜻이다.
	Duplicate bool
	// Replayed는 이미 끝난 턴의 답을 다시 내보냈다는 뜻이다.
	Replayed bool
	// ResourcesPinned는 이 턴이 끝난 뒤 도움 자원이 화면에 고정되어 있는지다.
	ResourcesPinned bool
}

// LogValue는 턴을 통째로 로그에 넘겨도 말의 내용이 나가지 않게 한다.
func (t Turn) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("stage", int(t.Stage)),
		slog.String("origin", t.Origin),
		slog.Int("seq", int(t.Seq)),
		slog.Bool("duplicate", t.Duplicate),
		slog.Bool("replayed", t.Replayed),
		slog.Bool("resources_pinned", t.ResourcesPinned),
	)
}

// outgoing은 이번에 나갈 말이다.
type outgoing struct {
	text   string
	speech string
	origin string
	phrase phrases.ID
}

// Handle은 사용자의 말 하나로 한 턴을 돌린다.
//
// 한 대화의 턴은 줄을 선다. 앞선 턴이 끝나기 전에 들어온 글은 기다린다.
// ctx를 취소하면 돌고 있던 모델 호출이 모두 멈춘다. 이미 저장된 것은 되돌리지 않는다.
func (e *Engine) Handle(ctx context.Context, s *Session, in Say) (Turn, error) {
	if s == nil {
		return Turn{}, errors.New("engine: session is required")
	}
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return Turn{}, ErrEmptyText
	case in.ClientMessageID == uuid.Nil:
		return Turn{}, ErrNoClientMessageID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return Turn{}, ErrConversationEnded
	}

	sealer, err := e.sealer(ctx, s.user.ID)
	if err != nil {
		return Turn{}, err
	}

	// 한 턴은 컨텍스트 하나다. 돌아갈 때 아직 돌고 있는 모델 호출은 여기서 멈춘다.
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	said, err := e.persistSaid(turnCtx, s, in, text, sealer)
	if err != nil {
		return Turn{}, err
	}
	if err := s.sink.Emit(turnCtx, Accepted{ClientMessageID: in.ClientMessageID, Seq: said.stored.Seq}); err != nil {
		return Turn{}, fmt.Errorf("engine: emit accepted: %w", err)
	}
	switch {
	case said.replay != nil:
		// 먼저 저장된 글의 턴이 이미 끝나 있었다. 그때 나간 말을 다시 내보내고 끝낸다.
		if err := s.sink.Emit(turnCtx, *said.replay); err != nil {
			return Turn{}, fmt.Errorf("engine: emit ai text: %w", err)
		}
		return Turn{
			Seq: said.stored.Seq, Origin: said.replay.Origin,
			Duplicate: true, Replayed: true, ResourcesPinned: s.crisis,
		}, nil
	case said.stale:
		// 그 글 뒤로 대화가 이미 흘러갔다. 다시 답하면 순서가 어긋난다.
		return Turn{Seq: said.stored.Seq, Duplicate: true, ResourcesPinned: s.crisis}, nil
	}

	return e.runTurn(turnCtx, s, said, sealer)
}

// runTurn은 저장된 발화 하나로 관문과 답 만들기를 돌리고 말을 내보낸다.
func (e *Engine) runTurn(ctx context.Context, s *Session, said storedSaid, sealer *crypto.Sealer) (Turn, error) {
	conversation, err := e.store.Queries().GetConversation(ctx, db.GetConversationParams{ID: s.id, UserID: s.user.ID})
	if err != nil {
		return Turn{}, fmt.Errorf("engine: get conversation: %w", err)
	}
	checkState := conversation.Conversation.CheckState

	turns, err := e.recentTurns(ctx, s, sealer)
	if err != nil {
		return Turn{}, err
	}

	// 관문과 답 만들기를 동시에 시작한다. 만든 답은 버퍼에 두고 내보내지 않는다.
	ruleResult := e.gate.Rule(said.text)
	mode, buffered := bufferMode(ruleResult.Stage, checkState, s.crisis)
	pending := e.startReply(ctx, buffered, reply.Input{Mode: mode, Turns: turns, UserWords: ruleResult.Evidence})
	defer pending.close()

	decision := said.decision
	if decision == nil {
		decided, detection, err := e.decide(ctx, s, decideInput{
			said: said.text, turns: turns, checkState: checkState,
		})
		if err != nil {
			return Turn{}, err
		}
		if err := e.persistGateEvent(ctx, s, said.stored.ID, decided, detection, sealer); err != nil {
			return Turn{}, err
		}
		decision = &decided
	}

	out, nextCheckState, err := e.respond(ctx, s, respondInput{
		stage:      decision.Stage,
		checkState: checkState,
		said:       said.text,
		turns:      turns,
		pending:    pending,
	})
	if err != nil {
		return Turn{}, err
	}
	if err := e.emitReply(ctx, s, out, nextCheckState); err != nil {
		return Turn{}, err
	}
	if decision.Stage >= crisis.StageRespond {
		s.crisis = true
		items := e.phrases.Resources()
		if decision.Stage >= crisis.StageUrgent {
			// 가장 급한 순간에는 바로 걸어야 하는 번호가 앞에 온다.
			items = e.phrases.UrgentResources()
		}
		if err := s.emitResources(ctx, items); err != nil {
			return Turn{}, err
		}
	}

	turn := Turn{
		Stage:           decision.Stage,
		Origin:          out.origin,
		Seq:             said.stored.Seq,
		Duplicate:       said.duplicate,
		ResourcesPinned: s.crisis,
	}
	e.logger.LogAttrs(ctx, slog.LevelInfo, "conversation turn finished",
		slog.Any("conversation", s), slog.Any("turn", turn))
	return turn, nil
}

// storedSaid는 저장된(또는 이미 저장되어 있던) 사용자 발화와, 그 글의 처리가 어디까지 갔는지다.
type storedSaid struct {
	stored db.Utterance
	text   string
	// duplicate는 같은 글이 이미 저장되어 있었다는 뜻이다.
	duplicate bool
	// decision이 있으면 관문의 판정은 이미 남아 있다. 답만 다시 만든다.
	decision *crisis.Decision
	// replay가 있으면 그 턴은 이미 끝났다. 그때 나간 말을 다시 내보내기만 한다.
	replay *AIText
	// stale은 다시 보낸 글이고 그 뒤로 대화가 이미 흘러간 경우다.
	stale bool
}

// persistSaid는 사용자의 말을 잠가 저장한다.
//
// 같은 글이 이미 저장되어 있으면 그 글의 처리가 어디까지 갔는지 본다. 발화만 저장하고 죽었을 수 있기 때문이다.
//   - 판정도 답도 없으면 그 발화로 턴을 처음부터 돌린다.
//   - 판정은 있고 답이 없으면 저장된 판정으로 답만 다시 만든다.
//   - 답까지 있으면 그 말을 다시 내보낸다.
func (e *Engine) persistSaid(
	ctx context.Context, s *Session, in Say, text string, sealer *crypto.Sealer,
) (storedSaid, error) {
	id, err := store.NewID()
	if err != nil {
		return storedSaid{}, fmt.Errorf("engine: %w", err)
	}
	sealed, err := sealer.SealString(text, sealing.UtteranceText(id))
	if err != nil {
		return storedSaid{}, fmt.Errorf("engine: seal utterance: %w", err)
	}
	modality := in.Modality
	if modality == "" {
		modality = s.mode
	}

	clientMessageID := in.ClientMessageID
	appended, err := e.store.AppendUtterance(ctx, store.NewUtterance{
		ID: id, ConversationID: s.id, UserID: s.user.ID,
		Speaker: store.SpeakerUser, Modality: modality, Origin: store.OriginUser,
		TextEnc: sealed, STTMinConfidence: in.STTMinConfidence, ClientMessageID: &clientMessageID,
		Now: e.clock.Now(),
	})
	switch {
	case errors.Is(err, store.ErrConversationNotActive):
		return storedSaid{}, ErrConversationEnded
	case err != nil:
		return storedSaid{}, fmt.Errorf("engine: append utterance: %w", err)
	}

	said := storedSaid{stored: appended.Utterance, text: text, duplicate: appended.Duplicate}
	if !appended.Duplicate {
		return said, nil
	}
	// 먼저 저장된 글이다. 그 암호문은 이번에 만든 ID가 아니라 저장된 발화의 ID에 묶여 있다.
	said.text, err = sealer.OpenString(said.stored.TextEnc, sealing.UtteranceText(said.stored.ID))
	if err != nil {
		return storedSaid{}, fmt.Errorf("%w: open utterance: %w", ErrGone, err)
	}

	event, err := e.store.Queries().GetGateEventByUtterance(ctx, db.GetGateEventByUtteranceParams{
		UtteranceID: said.stored.ID, UserID: s.user.ID,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		// 발화만 저장하고 판정을 남기기 전에 끊긴 턴이다. 관문부터 다시 돈다.
		return said, nil
	case err != nil:
		return storedSaid{}, fmt.Errorf("engine: get gate event: %w", err)
	}

	next, found, err := e.nextUtterance(ctx, s, said.stored.Seq)
	switch {
	case err != nil:
		return storedSaid{}, err
	case !found:
		// 판정은 남았는데 답이 없다. 그 판정 그대로 답만 다시 만든다.
		stage, err := crisis.StageFromInt(int(event.FinalStage))
		if err != nil {
			return storedSaid{}, fmt.Errorf("engine: stored gate event: %w", err)
		}
		said.decision = &crisis.Decision{Stage: stage}
		return said, nil
	case next.Speaker != store.SpeakerAI:
		said.stale = true
		return said, nil
	}

	text, err = sealer.OpenString(next.TextEnc, sealing.UtteranceText(next.ID))
	if err != nil {
		return storedSaid{}, fmt.Errorf("%w: open utterance: %w", ErrGone, err)
	}
	said.replay = &AIText{Seq: next.Seq, Text: text, Speech: text, Origin: next.Origin}
	return said, nil
}

// nextUtterance는 seq 바로 다음의 말을 돌려준다. 최근 몇 개 안에 없으면 없는 것으로 본다.
// 한참 지난 글을 다시 보낸 경우인데, 그때는 뒤이은 말이 이미 여럿이라 어느 쪽으로 읽어도 다시 답하지 않는다.
func (e *Engine) nextUtterance(ctx context.Context, s *Session, seq int32) (db.Utterance, bool, error) {
	rows, err := e.lastUtterances(ctx, s)
	if err != nil {
		return db.Utterance{}, false, err
	}
	var (
		best  db.Utterance
		found bool
	)
	for _, row := range rows {
		if row.Seq > seq && (!found || row.Seq < best.Seq) {
			best, found = row, true
		}
	}
	return best, found, nil
}

func (e *Engine) lastUtterances(ctx context.Context, s *Session) ([]db.Utterance, error) {
	rows, err := e.store.Queries().ListLastUtterances(ctx, db.ListLastUtterancesParams{
		ConversationID: s.id, UserID: s.user.ID,
		//nolint:gosec // G115: 위에서 int32의 상한으로 자른다.
		MaxRows: int32(min(e.maxTurns, math.MaxInt32)),
	})
	if err != nil {
		return nil, fmt.Errorf("engine: list utterances: %w", err)
	}
	return rows, nil
}

// recentTurns는 모델에 보낼 지난 말을 오래된 것부터 돌려준다. 마지막은 방금 저장한 사용자의 말이다.
func (e *Engine) recentTurns(ctx context.Context, s *Session, sealer *crypto.Sealer) ([]reply.Turn, error) {
	rows, err := e.lastUtterances(ctx, s)
	if err != nil {
		return nil, err
	}

	turns := make([]reply.Turn, 0, len(rows))
	// 쿼리는 가장 최근 발화부터 돌려준다. 모델에는 오래된 것부터 보낸다.
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		text, err := sealer.OpenString(row.TextEnc, sealing.UtteranceText(row.ID))
		if err != nil {
			// 열리지 않는 말은 빼고 이어간다. 방금 저장한 글은 부르는 쪽이 이미 열어 봤다.
			continue
		}
		speaker := reply.SpeakerUser
		if row.Speaker == store.SpeakerAI {
			speaker = reply.SpeakerAI
		}
		turns = append(turns, reply.Turn{Speaker: speaker, Text: text})
	}
	return turns, nil
}

// bufferMode는 관문이 끝나기 전에 미리 만들어 둘 답의 방식을 고른다.
//
// 두 번째 값이 거짓이면 미리 만들지 않는다. 규칙 겹에 걸린 말의 최종 단계는 규칙의 단계보다 낮아지지 않으므로,
// 그 버퍼가 반드시 버려질 것을 여기서 미리 알 수 있다. 버려질 답을 만들지 않으면 가장 무거운 순간에
// 미리 써 둔 말이 대화 모델을 기다리는 일이 아예 없다.
func bufferMode(ruleStage crisis.Stage, checkState string, crisisFollow bool) (reply.Mode, bool) {
	switch {
	case ruleStage >= crisis.StageRespond:
		return "", false
	case ruleStage >= crisis.StageCheck && checkState != store.CheckStateNone:
		// 되물은 뒤의 확인 단계는 미리 써 둔 직접 묻기로 가고, 직접 물은 뒤라면 코어의 규칙이 대응 단계로 올린다.
		return "", false
	case crisisFollow:
		return reply.ModeCrisisFollow, true
	case ruleStage >= crisis.StageCheck:
		return reply.ModeCheck, true
	default:
		return reply.ModeNormal, true
	}
}

// respondInput은 단계에 따라 나갈 말을 고르는 데 필요한 것이다.
type respondInput struct {
	stage      crisis.Stage
	checkState string
	said       string
	turns      []reply.Turn
	pending    *pendingReply
}

// respond는 최종 단계에 따라 나갈 말과 다음 확인 상태를 정한다.
func (e *Engine) respond(ctx context.Context, s *Session, in respondInput) (outgoing, string, error) {
	switch {
	case in.stage >= crisis.StageRespond:
		// 가장 무거운 순간의 말은 모델의 출력이나 장애에 맡기지 않는다. 만들던 답은 버린다.
		in.pending.cancel()
		phrase := e.phrases.CrisisRespond()
		if in.stage >= crisis.StageUrgent {
			phrase = e.phrases.CrisisUrgent()
		}
		return fixed(phrase), in.checkState, nil

	case in.stage == crisis.StageCheck && in.checkState == store.CheckStateNone:
		// 첫 걸음은 사용자의 말을 그대로 받아 되묻는 것이다.
		out, err := e.replyIn(ctx, reply.ModeCheck, in, in.said)
		return out, store.CheckStateReflected, err

	case in.stage == crisis.StageCheck:
		// 둘째 걸음은 돌려 말하지 않고 묻는다. 한 대화에서 한 번뿐이다.
		in.pending.cancel()
		return fixed(e.phrases.DirectAsk()), store.CheckStateAsked, nil

	default:
		mode := reply.ModeNormal
		if s.crisis {
			// 미리 써 둔 위기 응답이 나간 뒤의 대화다. 듣는 쪽에 머물고 조언하지 않는다.
			mode = reply.ModeCrisisFollow
		}
		out, err := e.replyIn(ctx, mode, in, "")
		return out, in.checkState, err
	}
}

func fixed(p phrases.Phrase) outgoing {
	return outgoing{text: p.Display, speech: p.Speech, origin: store.OriginFixed, phrase: p.ID}
}

// replyIn은 버퍼의 답이 이번에 쓸 방식으로 만들어졌으면 그것을 쓰고, 아니면 그 방식으로 새로 만든다.
func (e *Engine) replyIn(ctx context.Context, mode reply.Mode, in respondInput, userWords string) (outgoing, error) {
	result, ok, err := in.pending.take(ctx, mode)
	if err != nil {
		return outgoing{}, err
	}
	if !ok {
		// 규칙 겹은 놓쳤는데 AI 판별이 잡은 경우다. 버퍼의 답은 이번에 쓸 방식으로 만든 것이 아니다.
		in.pending.cancel()
		result, err = e.reply.Generate(ctx, reply.Input{Mode: mode, Turns: in.turns, UserWords: userWords})
		if err != nil {
			return outgoing{}, fmt.Errorf("engine: generate reply: %w", err)
		}
	}
	return outgoing{
		text:   result.Text,
		speech: result.Speech,
		origin: string(result.Origin),
		phrase: result.Phrase,
	}, nil
}

// emitReply는 나가는 말을 잠가 저장하고, 확인 상태를 옮기고, 그다음에 내보낸다.
//
// 엔진 밖으로 AI의 말이 나가는 길은 여기 하나뿐이다. 저장이 먼저인 이유는 두 가지다.
// 사용자가 본 말이 대화 기록에 없으면 다음 턴의 문맥과 일기의 재료가 어긋나고,
// 확인 상태를 답과 함께 옮겨야 직접 묻기가 한 대화에서 한 번으로 묶인다.
func (e *Engine) emitReply(ctx context.Context, s *Session, out outgoing, nextCheckState string) error {
	if strings.TrimSpace(out.text) == "" {
		return errors.New("engine: refusing to emit an empty reply")
	}
	sealer, err := e.sealer(ctx, s.user.ID)
	if err != nil {
		return err
	}
	id, err := store.NewID()
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	sealed, err := sealer.SealString(out.text, sealing.UtteranceText(id))
	if err != nil {
		return fmt.Errorf("engine: seal utterance: %w", err)
	}

	var saved db.Utterance
	err = e.store.InTx(ctx, func(q *db.Queries) error {
		appended, err := store.AppendUtterance(ctx, q, store.NewUtterance{
			ID: id, ConversationID: s.id, UserID: s.user.ID,
			Speaker: store.SpeakerAI, Modality: s.mode, Origin: out.origin,
			TextEnc: sealed, Now: e.clock.Now(),
		})
		if err != nil {
			return err
		}
		saved = appended.Utterance
		if nextCheckState == "" || nextCheckState == store.CheckStateNone {
			return nil
		}
		_, err = q.AdvanceConversationCheckState(ctx, db.AdvanceConversationCheckStateParams{
			CheckState: nextCheckState, ID: s.id, UserID: s.user.ID,
		})
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			// 찾지 못함은 상태가 이미 더 앞서 있다는 뜻이다. 뒤로 돌리지 않는 것이 그 쿼리의 약속이다.
			return fmt.Errorf("advance check state: %w", err)
		}
		return nil
	})
	switch {
	case errors.Is(err, store.ErrConversationNotActive):
		return ErrConversationEnded
	case err != nil:
		return fmt.Errorf("engine: append utterance: %w", err)
	}

	if err := s.sink.Emit(ctx, AIText{
		Seq:    saved.Seq,
		Text:   out.text,
		Speech: out.speech,
		Origin: out.origin,
		Phrase: out.phrase,
	}); err != nil {
		return fmt.Errorf("engine: emit ai text: %w", err)
	}
	return nil
}

// decideInput은 최종 단계를 정하는 데 필요한 것이다.
type decideInput struct {
	said       string
	turns      []reply.Turn
	checkState string
}

// decide는 관문의 두 겹을 돌리고 코어의 규칙으로 최종 단계를 정한다.
//
// 규칙 겹은 답을 미리 만들지 정할 때 이미 한 번 돌았다. 같은 글에는 언제나 같은 판정이 나오는 순수한 검사라
// 여기서 다시 돌려도 결과가 달라지지 않고, 두 겹의 판정을 한 자리에서 받는 쪽이 어긋날 틈이 없다.
func (e *Engine) decide(ctx context.Context, s *Session, in decideInput) (crisis.Decision, gate.Detection, error) {
	detection := e.gate.Detect(ctx, classifier.Input{
		Context:   classifierContext(in.turns, e.ctxTurns),
		Utterance: in.said,
	})
	if err := ctx.Err(); err != nil {
		// 사용자가 연결을 끊었거나 더 새로운 턴이 들어왔다. 판정을 남기지 않고 그만둔다.
		return crisis.Decision{}, detection, fmt.Errorf("engine: %w", err)
	}

	now := e.clock.Now()
	input := detection.Input()
	input.State = e.gateState(ctx, s, now)
	input.DirectAskDone = in.checkState == store.CheckStateAsked
	input.ConversationID = s.id.String()
	input.Now = now

	history, err := e.gateHistory(ctx, s, now)
	if err != nil {
		// 지난 판정을 읽지 못했다고 관문을 멈추지 않는다. 쌓임을 보는 규칙만 이번 턴에 꺼진다.
		e.logger.LogAttrs(ctx, slog.LevelError, "gate history cannot be read",
			slog.Any("conversation", s), slog.String("error", err.Error()))
	}
	input.History = history

	decision, err := crisis.Decide(input, e.params.Crisis)
	if err != nil {
		// Decide는 오류와 함께 돌려주는 판정도 채워서 준다. 읽지 못한 입력은 단계를 낮추는 쪽으로 쓰이지 않는다.
		e.logger.LogAttrs(ctx, slog.LevelError, "gate decision has invalid input",
			slog.Any("conversation", s),
			slog.Int("stage", int(decision.Stage)),
			slog.String("error", err.Error()),
		)
	}
	return decision, detection, nil
}

// classifierContext는 판별에 함께 보낼 직전 말을 고른다. 마지막 발화는 Input.Utterance로 따로 간다.
func classifierContext(turns []reply.Turn, maxTurns int) []classifier.Turn {
	if len(turns) <= 1 || maxTurns <= 0 {
		return nil
	}
	previous := turns[:len(turns)-1]
	if len(previous) > maxTurns {
		previous = previous[len(previous)-maxTurns:]
	}
	out := make([]classifier.Turn, 0, len(previous))
	for _, t := range previous {
		speaker := classifier.SpeakerUser
		if t.Speaker == reply.SpeakerAI {
			speaker = classifier.SpeakerAI
		}
		out = append(out, classifier.Turn{Speaker: speaker, Text: t.Text})
	}
	return out
}

// gateHistory는 쌓임을 보는 규칙이 필요한 만큼 지난 판정을 읽는다.
func (e *Engine) gateHistory(ctx context.Context, s *Session, now time.Time) ([]crisis.Event, error) {
	rows, err := e.store.Queries().ListFlaggedGateEventsSince(ctx, db.ListFlaggedGateEventsSinceParams{
		UserID: s.user.ID,
		Since:  now.Add(-crisis.HistoryHorizon(e.params.Crisis)),
	})
	if err != nil {
		return nil, fmt.Errorf("engine: list gate events: %w", err)
	}
	events := make([]crisis.Event, 0, len(rows))
	for _, row := range rows {
		stage, err := crisis.StageFromInt(int(row.FinalStage))
		if err != nil {
			// 읽지 못한 판정 하나 때문에 나머지를 버리지 않는다. Decide도 읽을 수 있는 것만으로 끝까지 판정한다.
			continue
		}
		events = append(events, crisis.Event{At: row.CreatedAt, Stage: stage, ConversationID: row.ConversationID.String()})
	}
	return events, nil
}

// persistGateEvent는 판정을 저장한다. 해당 없음도 저장한다. 근거 발화는 확인 단계 이상일 때만 남긴다.
func (e *Engine) persistGateEvent(
	ctx context.Context, s *Session, utteranceID uuid.UUID,
	decision crisis.Decision, detection gate.Detection, sealer *crypto.Sealer,
) error {
	id, err := store.NewID()
	if err != nil {
		return fmt.Errorf("engine: %w", err)
	}

	// 단계는 0부터 3까지다. 그 밖의 값은 crisis.StageFromInt가 이미 걸러 낸다.
	ruleStage := int16(detection.Rule.Stage) //nolint:gosec // G115: 단계는 0부터 3까지다.
	args := db.InsertGateEventParams{
		ID: id, UserID: s.user.ID, ConversationID: s.id, UtteranceID: utteranceID,
		RuleStage:   &ruleStage,
		FinalStage:  int16(decision.Stage), //nolint:gosec // G115: 단계는 0부터 3까지다.
		DetectedBy:  decision.DetectedBy.String(),
		Adjustments: decision.AdjustmentIDs(),
		AIFailed:    !detection.AI.Answered,
		Now:         e.clock.Now(),
	}
	if detection.AI.Answered {
		aiStage := int16(detection.AI.Stage) //nolint:gosec // G115: 단계는 0부터 3까지다.
		args.AIStage = &aiStage
	}
	if ms := detection.AI.Latency.Milliseconds(); ms > 0 {
		latency := int32(min(ms, int64(math.MaxInt32)))
		args.AILatencyMs = &latency
	}
	if decision.Stage >= crisis.StageCheck {
		if evidence := detection.Evidence(); evidence != "" {
			sealed, err := sealer.SealString(evidence, sealing.GateEvidence(id))
			if err != nil {
				return fmt.Errorf("engine: seal gate evidence: %w", err)
			}
			args.EvidenceEnc = sealed
		}
	}

	if _, err := e.store.Queries().InsertGateEvent(ctx, args); err != nil {
		return fmt.Errorf("engine: insert gate event: %w", err)
	}
	e.logger.LogAttrs(ctx, slog.LevelInfo, "gate decision recorded",
		slog.Any("conversation", s),
		slog.String("gate_event_id", id.String()),
		slog.Int("final_stage", int(decision.Stage)),
		slog.String("detected_by", decision.DetectedBy.String()),
		slog.Any("adjustments", decision.AdjustmentIDs()),
		slog.Any("detection", detection),
	)
	return nil
}
