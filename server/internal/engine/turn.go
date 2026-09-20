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
	// crisisStage가 0보다 크면 그 단계의 위기 고정 문구다. 이 말은 저장에 실패해도 반드시 화면에 닿아야 한다.
	crisisStage crisis.Stage
	// fallbackSeq는 저장하지 못한 채 내보낼 때 쓸 순번이다. 사용자의 글 바로 뒤에 놓인다.
	// 0으로 두면 화면이 첫 안부보다 위에 놓는다.
	fallbackSeq int32
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
//
// 대응 단계 이상에서 지키는 것이 하나 있다. 그 단계의 고정 문구와 도움 자원은 반드시 화면에 닿는다.
// 판정을 남기지 못해도, 나가는 말을 저장하지 못해도 나간다. 가장 무거운 순간의 응답을 두 번째 쓰기의 성패에 맡기지 않는다.
func (e *Engine) runTurn(ctx context.Context, s *Session, said storedSaid, sealer *crypto.Sealer) (Turn, error) {
	// 관문은 연결이 끊겨도 끝까지 간다. 판별 모델에는 이미 짧은 기한이 걸려 있고, 그 판정이 없으면
	// 무거운 말이 오간 대화가 자동 일기의 재료로 흘러간다(일기는 위기 기록이 있는 대화를 빼고 만든다).
	// 멈춰야 하는 것은 몇십 초씩 걸리는 대화 모델이지 몇 초짜리 판별이 아니다.
	// 관문에 필요한 읽기까지 여기에 든다. 그것이 끊기면 판정 자체를 내리지 못한다.
	gateCtx, cancelGate := context.WithTimeout(context.WithoutCancel(ctx), gateWindow)
	defer cancelGate()

	conversation, err := e.store.Queries().GetConversation(gateCtx, db.GetConversationParams{ID: s.id, UserID: s.user.ID})
	if err != nil {
		return Turn{}, fmt.Errorf("engine: get conversation: %w", err)
	}
	checkState := conversation.Conversation.CheckState
	spoken, err := crisis.StageFromInt(int(conversation.Conversation.CrisisSpokenStage))
	if err != nil {
		return Turn{}, fmt.Errorf("engine: stored crisis stage: %w", err)
	}

	turns, err := e.recentTurns(gateCtx, s, sealer)
	if err != nil {
		return Turn{}, err
	}

	// 관문과 답 만들기를 동시에 시작한다. 만든 답은 버퍼에 두고 내보내지 않는다.
	ruleResult := e.gate.Rule(said.text)
	mode, buffered := bufferMode(ruleResult.Stage, checkState, spoken, s.crisis)
	pending := e.startReply(ctx, buffered, reply.Input{Mode: mode, Turns: turns, UserWords: ruleResult.Evidence})
	defer pending.close()

	decision := said.decision
	if decision == nil {
		decided, detection, err := e.decide(gateCtx, s, decideInput{
			said: said.text, turns: turns, checkState: checkState,
		})
		if err != nil {
			return Turn{}, err
		}
		// 직접 묻기의 자리는 대화 행이 내준다. 진 쪽은 코어가 다시 판정한다.
		if decided.Stage == crisis.StageCheck && checkState == store.CheckStateReflected {
			claimed, err := e.claimDirectAsk(gateCtx, s)
			if err != nil {
				return Turn{}, err
			}
			if !claimed {
				checkState = store.CheckStateAsked
				decided = e.redecideAfterDirectAsk(gateCtx, s, detection)
			}
		}
		if err := e.persistGateEvent(gateCtx, s, said.stored.ID, decided, detection.detection, sealer); err != nil {
			if decided.Stage < crisis.StageRespond {
				return Turn{}, err
			}
			// 판정을 남기지 못했다. 그래도 이 단계의 말은 나가야 한다. 남기지 못한 사실만 따로 적는다.
			e.logger.LogAttrs(ctx, slog.LevelError, "gate event cannot be stored",
				slog.Any("conversation", s),
				slog.String("utterance_id", said.stored.ID.String()),
				slog.Int("final_stage", int(decided.Stage)),
				slog.String("failure", failureName(err)),
			)
		}
		decision = &decided
	}

	// 도움 자원은 나가는 말을 저장하기 전에 고정한다. 내보내는 일뿐이라 DB에 기댈 것이 없고,
	// 발화를 저장하는 트랜잭션에 매달아 두면 그 쓰기가 실패할 때 번호까지 함께 사라진다.
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

	out, nextCheckState, err := e.respond(ctx, s, respondInput{
		stage:      decision.Stage,
		checkState: checkState,
		spoken:     spoken,
		seq:        said.stored.Seq,
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

// claimDirectAsk는 이 대화의 직접 묻기 자리를 차지한다. 차지했으면 참이다.
//
// 자리를 말보다 먼저 차지하는 까닭: 뒤에 오는 일이 실패해 이 턴이 아무 말도 못 하고 끝나면, 자리는 쓰였는데
// 질문은 나가지 않은 상태가 된다. 그때 다음 턴은 "이미 물었다"로 보고 대응 단계로 올라간다.
// 어긋나는 방향이 무거운 쪽이라 안전하다. 반대로 두면 같은 질문이 두 번 나갈 수 있다.
func (e *Engine) claimDirectAsk(ctx context.Context, s *Session) (bool, error) {
	_, err := e.store.Queries().ClaimDirectAsk(ctx, db.ClaimDirectAskParams{ID: s.id, UserID: s.user.ID})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, store.ErrNotFound):
		// 다른 연결이 먼저 물었거나 대화가 그 사이에 끝났다. 어느 쪽이든 여기서 다시 묻지 않는다.
		return false, nil
	default:
		return false, fmt.Errorf("engine: claim direct ask: %w", err)
	}
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
	appended, err := e.appendSaid(ctx, s, storeSaid{
		id: id, text: sealed, modality: modality,
		clientMessageID: &clientMessageID, sttMinConfidence: in.STTMinConfidence,
	})
	if err != nil {
		return storedSaid{}, err
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
	if said.text != text {
		// 같은 식별자에 다른 글이 실려 왔다. 다시 보내기는 언제나 같은 글을 싣기 때문에, 이 길로 오는 것은
		// 식별자를 다시 쓴 클라이언트뿐이다. 그 글을 먼저 저장된 글의 답으로 갈음하면 사용자가 방금 한 말이
		// 관문을 거치지 않고 사라진다. 식별자 없이 새 발화로 저장해 관문을 그대로 거치게 한다.
		e.logger.LogAttrs(ctx, slog.LevelWarn, "client message id was reused for different text",
			slog.Any("conversation", s),
			slog.String("utterance_id", said.stored.ID.String()),
		)
		fresh, err := e.appendSaid(ctx, s, storeSaid{
			id: id, text: sealed, modality: modality, sttMinConfidence: in.STTMinConfidence,
		})
		if err != nil {
			return storedSaid{}, err
		}
		return storedSaid{stored: fresh.Utterance, text: text}, nil
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
	replay := AIText{Seq: next.Seq, Text: text, Speech: text, Origin: next.Origin}
	if next.Origin == store.OriginFixed {
		// 다시 내보내는 고정 문구는 처음 나갔을 때와 글자 그대로 같아야 한다.
		// 음성으로 읽을 글은 번호를 한글로 풀어 둔 것이고, 대화 기록에는 화면에 보일 글만 남는다.
		if p, ok := e.phrases.ByDisplay(text); ok {
			replay.Speech, replay.Phrase = p.Speech, p.ID
		}
	}
	said.replay = &replay
	return said, nil
}

// storeSaid는 사용자의 말 하나를 저장하는 데 필요한 값이다. 글은 이미 잠긴 뒤다.
type storeSaid struct {
	id               uuid.UUID
	text             []byte
	modality         string
	clientMessageID  *uuid.UUID
	sttMinConfidence *float32
}

func (e *Engine) appendSaid(ctx context.Context, s *Session, in storeSaid) (store.AppendedUtterance, error) {
	appended, err := e.store.AppendUtterance(ctx, store.NewUtterance{
		ID: in.id, ConversationID: s.id, UserID: s.user.ID,
		Speaker: store.SpeakerUser, Modality: in.modality, Origin: store.OriginUser,
		TextEnc: in.text, STTMinConfidence: in.sttMinConfidence, ClientMessageID: in.clientMessageID,
		Now: e.clock.Now(),
	})
	switch {
	case errors.Is(err, store.ErrConversationNotActive), errors.Is(err, store.ErrNotFound):
		// 찾지 못함은 그 사이에 사용자가 그날의 기록을 통째로 지웠다는 뜻이다. 대화 행은 함께 사라진다.
		// 대화의 식별자는 서버가 들고 있으므로 남의 대화나 없는 대화를 가리킬 일은 없다.
		return store.AppendedUtterance{}, ErrConversationEnded
	case err != nil:
		return store.AppendedUtterance{}, fmt.Errorf("engine: append utterance: %w", err)
	}
	return appended, nil
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
//
// spoken은 이 대화에서 고정 문구가 이미 나간 가장 높은 단계다. 규칙이 대응 단계로 본 말이라도 그 단계의 문구가
// 이미 나갔으면 이번에 나갈 말은 이어가는 대화이므로 미리 만들어 둔다. 그러지 않으면 가장 무거운 대화에서만
// 관문이 끝난 뒤에야 모델을 부르게 되어 답이 늦어진다.
func bufferMode(ruleStage crisis.Stage, checkState string, spoken crisis.Stage, crisisFollow bool) (reply.Mode, bool) {
	switch {
	case ruleStage >= crisis.StageRespond && ruleStage > spoken:
		return "", false
	case ruleStage >= crisis.StageRespond:
		return reply.ModeCrisisFollow, true
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
	// spoken은 이 대화에서 위기 고정 문구가 이미 나간 가장 높은 단계다.
	spoken crisis.Stage
	// seq는 방금 저장한 사용자 발화의 순번이다. 저장하지 못한 채 내보낼 때 그 바로 뒤에 놓는다.
	seq     int32
	said    string
	turns   []reply.Turn
	pending *pendingReply
}

// respond는 최종 단계에 따라 나갈 말과 다음 확인 상태를 정한다.
//
// 두 번째 값이 비어 있으면 확인 상태를 이 턴에서 옮기지 않는다는 뜻이다.
func (e *Engine) respond(ctx context.Context, s *Session, in respondInput) (outgoing, string, error) {
	switch {
	case in.stage >= crisis.StageRespond && in.stage > in.spoken:
		// 이 단계의 첫 응답이다. 가장 무거운 순간의 말은 모델의 출력이나 장애에 맡기지 않는다. 만들던 답은 버린다.
		in.pending.cancel()
		phrase := e.phrases.CrisisRespond()
		if in.stage >= crisis.StageUrgent {
			phrase = e.phrases.CrisisUrgent()
		}
		out := fixed(phrase)
		out.crisisStage, out.fallbackSeq = in.stage, in.seq+1
		return out, "", nil

	case in.stage >= crisis.StageRespond:
		// 이 단계의 고정 문구는 이 대화에서 이미 나갔다. 같은 글을 글자 그대로 다시 읽어 주지 않는다.
		// 번호를 다시 꺼내지 않고 듣는 쪽에 머물며 이어간다.
		out, err := e.replyIn(ctx, reply.ModeCrisisFollow, in, "")
		return out, "", err

	case in.stage == crisis.StageCheck && in.checkState == store.CheckStateNone:
		// 첫 걸음은 사용자의 말을 그대로 받아 되묻는 것이다.
		out, err := e.replyIn(ctx, reply.ModeCheck, in, in.said)
		return out, store.CheckStateReflected, err

	case in.stage == crisis.StageCheck && in.checkState == store.CheckStateReflected:
		// 둘째 걸음은 돌려 말하지 않고 묻는다. 한 대화에서 한 번뿐이고, 그 자리는 이미 차지해 두었다.
		in.pending.cancel()
		return fixed(e.phrases.DirectAsk()), "", nil

	default:
		// 직접 물은 뒤에 다시 온 확인 단계는 코어가 대응 단계로 올리므로 여기까지 오지 않는다.
		// 저장된 판정으로 답만 다시 만드는 길에서만, 그 사이에 다른 연결이 물었을 때 여기로 온다.
		// 그때 같은 질문을 또 하지 않고 듣는 쪽에 머문다.
		mode := reply.ModeNormal
		if s.crisis {
			// 미리 써 둔 위기 응답이 나간 뒤의 대화다. 듣는 쪽에 머물고 조언하지 않는다.
			mode = reply.ModeCrisisFollow
		}
		out, err := e.replyIn(ctx, mode, in, "")
		return out, "", err
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

// emitReply는 나가는 말을 잠가 저장하고, 확인 상태와 고정 문구의 단계를 옮기고, 그다음에 내보낸다.
//
// 엔진 밖으로 AI의 말이 나가는 길은 여기 하나뿐이다. 저장이 먼저인 이유는 두 가지다.
// 사용자가 본 말이 대화 기록에 없으면 다음 턴의 문맥과 일기의 재료가 어긋나고,
// 고정 문구의 단계를 말과 함께 옮겨야 같은 글이 한 대화에서 두 번 나가지 않는다.
//
// 딱 하나 예외가 있다. 대응 단계 이상의 고정 문구는 저장하지 못해도 내보낸다. 가장 무거운 순간에 사람이 받는 말이
// 두 번째 쓰기의 성패에 걸려서는 안 된다. 저장하지 못한 사실은 따로 남긴다.
func (e *Engine) emitReply(ctx context.Context, s *Session, out outgoing, nextCheckState string) error {
	if strings.TrimSpace(out.text) == "" {
		return errors.New("engine: refusing to emit an empty reply")
	}
	// 연결이 이미 끊겼으면 닿을 화면이 없다. 그때는 저장에 실패한 것을 위기 응답을 잃은 일로 적지 않는다.
	mustDeliver := out.crisisStage >= crisis.StageRespond && ctx.Err() == nil

	saved, err := e.storeReply(ctx, s, out, nextCheckState)
	if err != nil && mustDeliver && !errors.Is(err, ErrConversationEnded) {
		// 접속 풀이 잠깐 흔들린 것일 수 있다. 한 번만 다시 해 본다.
		saved, err = e.storeReply(ctx, s, out, nextCheckState)
	}
	if err != nil {
		if !mustDeliver {
			return err
		}
		e.logger.LogAttrs(ctx, slog.LevelError, "crisis reply cannot be stored",
			slog.Any("conversation", s),
			slog.Int("stage", int(out.crisisStage)),
			slog.String("failure", failureName(err)),
		)
		// 저장되지 않았으니 순번이 없다. 사용자의 글 바로 뒤에 놓아 화면의 순서가 어긋나지 않게 한다.
		saved = db.Utterance{Seq: out.fallbackSeq}
	}

	if emitErr := s.sink.Emit(ctx, AIText{
		Seq:    saved.Seq,
		Text:   out.text,
		Speech: out.speech,
		Origin: out.origin,
		Phrase: out.phrase,
	}); emitErr != nil {
		return fmt.Errorf("engine: emit ai text: %w", emitErr)
	}
	if errors.Is(err, ErrConversationEnded) {
		// 말은 내보냈다. 대화가 끝났다는 사실은 부르는 쪽이 알아야 한다.
		return err
	}
	return nil
}

// storeReply는 나가는 말을 한 트랜잭션에 저장하고, 확인 상태와 고정 문구의 단계를 함께 옮긴다.
func (e *Engine) storeReply(
	ctx context.Context, s *Session, out outgoing, nextCheckState string,
) (db.Utterance, error) {
	sealer, err := e.sealer(ctx, s.user.ID)
	if err != nil {
		return db.Utterance{}, err
	}
	id, err := store.NewID()
	if err != nil {
		return db.Utterance{}, fmt.Errorf("engine: %w", err)
	}
	sealed, err := sealer.SealString(out.text, sealing.UtteranceText(id))
	if err != nil {
		return db.Utterance{}, fmt.Errorf("engine: seal utterance: %w", err)
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
		if out.crisisStage > 0 {
			//nolint:gosec // G115: 단계는 0부터 3까지다.
			_, err = q.AdvanceConversationCrisisStage(ctx, db.AdvanceConversationCrisisStageParams{
				CrisisSpokenStage: int16(out.crisisStage), ID: s.id, UserID: s.user.ID,
			})
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("advance crisis stage: %w", err)
			}
		}
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
	case errors.Is(err, store.ErrConversationNotActive), errors.Is(err, store.ErrNotFound):
		// 찾지 못함은 그 사이에 사용자가 그날의 기록을 통째로 지웠다는 뜻이다. 대화 행은 함께 사라진다.
		return db.Utterance{}, ErrConversationEnded
	case err != nil:
		return db.Utterance{}, fmt.Errorf("engine: append utterance: %w", err)
	}
	return saved, nil
}

// decideInput은 최종 단계를 정하는 데 필요한 것이다.
type decideInput struct {
	said       string
	turns      []reply.Turn
	checkState string
}

// gateOutcome은 관문이 본 것과, 그것을 코어에 넘길 때 쓴 입력이다.
//
// 입력을 들고 있는 까닭은 직접 묻기의 자리를 놓쳤을 때다. 그때 "이미 물었다"는 값만 바꿔 코어에 다시 물어본다.
// 단계를 엔진이 고쳐 잡지 않고 코어가 다시 계산하게 하려는 것이다.
type gateOutcome struct {
	detection gate.Detection
	input     crisis.Input
}

// redecideAfterDirectAsk는 직접 묻기가 이미 끝난 것으로 보고 다시 판정한다.
func (e *Engine) redecideAfterDirectAsk(ctx context.Context, s *Session, o gateOutcome) crisis.Decision {
	in := o.input
	in.DirectAskDone = true
	decision, err := crisis.Decide(in, e.params.Crisis)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "gate decision has invalid input",
			slog.Any("conversation", s),
			slog.Int("stage", int(decision.Stage)),
			slog.String("failure", failureName(err)),
		)
	}
	return decision
}

// decide는 관문의 두 겹을 돌리고 코어의 규칙으로 최종 단계를 정한다.
//
// 규칙 겹은 답을 미리 만들지 정할 때 이미 한 번 돌았다. 같은 글에는 언제나 같은 판정이 나오는 순수한 검사라
// 여기서 다시 돌려도 결과가 달라지지 않고, 두 겹의 판정을 한 자리에서 받는 쪽이 어긋날 틈이 없다.
func (e *Engine) decide(ctx context.Context, s *Session, in decideInput) (crisis.Decision, gateOutcome, error) {
	detection := e.gate.Detect(ctx, classifier.Input{
		Context:   classifierContext(in.turns, e.ctxTurns),
		Utterance: in.said,
	})
	outcome := gateOutcome{detection: detection}
	if err := ctx.Err(); err != nil {
		// 사용자가 연결을 끊었거나 더 새로운 턴이 들어왔다. 판정을 남기지 않고 그만둔다.
		return crisis.Decision{}, outcome, fmt.Errorf("engine: %w", err)
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
			slog.Any("conversation", s), slog.String("failure", failureName(err)))
	}
	input.History = history
	outcome.input = input

	decision, err := crisis.Decide(input, e.params.Crisis)
	if err != nil {
		// Decide는 오류와 함께 돌려주는 판정도 채워서 준다. 읽지 못한 입력은 단계를 낮추는 쪽으로 쓰이지 않는다.
		e.logger.LogAttrs(ctx, slog.LevelError, "gate decision has invalid input",
			slog.Any("conversation", s),
			slog.Int("stage", int(decision.Stage)),
			slog.String("failure", failureName(err)),
		)
	}
	return decision, outcome, nil
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

// gateWindow는 부르는 쪽의 연결이 끊긴 뒤에도 관문을 끝까지 돌리는 데 주는 시간이다.
// 판별 모델에는 이미 그보다 짧은 기한이 걸려 있으므로 여기에 걸리는 일은 거의 없다.
const gateWindow = 10 * time.Second

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
