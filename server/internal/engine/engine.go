package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// DefaultTimezone은 시간대를 읽을 수 없는 사용자의 시간대다. 가입할 때 정하는 기본값과 같아야 한다.
// 기록 날짜의 새벽 경계가 이 값으로 정해지므로, 읽지 못했다고 서버가 도는 곳의 시간대로 떨어지게 두지 않는다.
const DefaultTimezone = "Asia/Seoul"

// openAttempts는 대화를 여는 일을 다시 해 보는 횟수다.
// 두 기기가 동시에 시작하면 한쪽이 유일 제약에 걸리고, 진 쪽은 열린 대화를 다시 읽어 이어간다.
// 그 사이에 그 대화가 또 끝났을 수 있어 한 번 더 돈다.
const openAttempts = 3

var (
	// ErrConversationEnded는 이미 끝난 대화에 말을 더하려 했다는 뜻이다. 새로 시작해야 한다.
	ErrConversationEnded = errors.New("engine: conversation is already ended")
	// ErrEmptyText는 빈 글이 들어왔다는 뜻이다. 공백만 있는 글도 여기에 든다.
	ErrEmptyText = errors.New("engine: utterance text is empty")
	// ErrNoClientMessageID는 사용자의 글에 클라이언트가 붙이는 식별자가 없다는 뜻이다.
	// 그 식별자가 없으면 다시 보낸 글을 가릴 수 없어 같은 말이 두 번 저장된다.
	ErrNoClientMessageID = errors.New("engine: client message id is required")
	// ErrUnsupportedMode는 아직 받지 않는 대화 방식이라는 뜻이다.
	ErrUnsupportedMode = errors.New("engine: unsupported conversation mode")
	// ErrGone은 계정이 지워져 그 사람의 글을 더는 잠그거나 열 수 없다는 뜻이다.
	ErrGone = errors.New("engine: user is gone")
)

// DiaryEnqueuer는 대화를 끝내는 트랜잭션 안에서 일기 초안 작업을 등록한다. *diary.Enqueuer가 이것을 채운다.
//
// 주지 않으면 초안 작업을 넣지 않고, 끝난 대화의 진행 상태도 열어 두지 않는다(none).
// 언어 모델이나 작업 큐를 아직 붙이지 않은 실행을 위해 남겨 둔 길이다.
type DiaryEnqueuer interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, args diary.DraftArgs) error
}

// AnalysisEnqueuer는 대화를 끝내는 트랜잭션 안에서 마음 신호 추출 작업을 등록한다. *analysis.Enqueuer가 이것을 채운다.
//
// 초안 쪽과 달리 그 트랜잭션의 쿼리도 함께 받는다. 추출 쪽은 작업을 넣으면서 대화의 분석 상태를 pending으로 적어서,
// "pending인 대화에는 언제나 작업이 있다"가 부르는 쪽의 기억이 아니라 구조로 지켜진다.
//
// 주지 않으면 추출 작업을 넣지 않고 분석 상태는 none으로 남는다. 그 대화는 계산에서 그냥 빠진다.
type AnalysisEnqueuer interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, q *db.Queries, args analysis.ExtractArgs) error
}

// Options는 엔진을 만드는 데 필요한 것이다.
type Options struct {
	Store   *store.Store
	Sealers *sealing.Sealers
	// Gate는 위기 관문의 두 겹이다.
	Gate *gate.Detector
	// Reply는 대화 모델에게 답을 받아 출력 검사를 거치는 쪽이다.
	Reply *reply.Generator
	// Phrases는 모델을 거치지 않고 나가는 말과 도움 자원이다.
	Phrases *phrases.Catalogue
	// Diary는 비워 둘 수 있다. 위의 DiaryEnqueuer 설명을 본다.
	Diary DiaryEnqueuer
	// Analysis는 비워 둘 수 있다. 위의 AnalysisEnqueuer 설명을 본다.
	Analysis AnalysisEnqueuer
	Clock    clock.Clock
	Logger   *slog.Logger
	// Params가 빈 값이면 params.Default다. 관문의 판정에 쓰는 조정 값이 여기서 온다.
	Params params.Params
	// ContextTurns가 0이면 classifier.DefaultContextTurns다. AI 판별에 함께 보내는 직전 말의 수다.
	ContextTurns int
	// ReplyTurns가 0이면 reply.DefaultMaxTurns다. 답을 만들 때 보는 지난 말의 수다.
	ReplyTurns int
}

// Engine은 대화 한 바퀴를 돌린다. 여러 고루틴에서 함께 써도 된다. 대화 하나의 턴은 Session이 줄을 세운다.
type Engine struct {
	closer

	sealers  *sealing.Sealers
	gate     *gate.Detector
	reply    *reply.Generator
	phrases  *phrases.Catalogue
	logger   *slog.Logger
	params   params.Params
	ctxTurns int
	maxTurns int
}

// New는 엔진을 만든다.
func New(opts Options) (*Engine, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("engine: store is required")
	case opts.Sealers == nil:
		return nil, errors.New("engine: sealers are required")
	case opts.Gate == nil:
		return nil, errors.New("engine: gate is required")
	case opts.Reply == nil:
		return nil, errors.New("engine: reply generator is required")
	case opts.Phrases == nil:
		return nil, errors.New("engine: phrase catalogue is required")
	case opts.Clock == nil:
		return nil, errors.New("engine: clock is required")
	case opts.Logger == nil:
		return nil, errors.New("engine: logger is required")
	case opts.ContextTurns < 0 || opts.ReplyTurns < 0:
		return nil, errors.New("engine: turn counts must not be negative")
	}

	p := opts.Params
	if p == (params.Params{}) {
		p = params.Default()
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}

	e := &Engine{
		closer:   closer{store: opts.Store, diary: opts.Diary, analysis: opts.Analysis, clock: opts.Clock},
		sealers:  opts.Sealers,
		gate:     opts.Gate,
		reply:    opts.Reply,
		phrases:  opts.Phrases,
		logger:   opts.Logger,
		params:   p,
		ctxTurns: opts.ContextTurns,
		maxTurns: opts.ReplyTurns,
	}
	if e.ctxTurns == 0 {
		e.ctxTurns = classifier.DefaultContextTurns
	}
	if e.maxTurns == 0 {
		e.maxTurns = reply.DefaultMaxTurns
	}
	return e, nil
}

// failureName은 오류를 로그에 남길 짧은 이름으로 바꾼다. 오류의 문구는 남기지 않는다.
//
// 감싼 오류의 문구에는 사용자의 글이나 쿼리 조각이 섞여 들어올 수 있다. 로그를 거르는 그물은 이름으로 거르는데
// "error"는 그 목록에 없다. 그래서 여기서 정해 둔 낱말로 바꿔 남긴다.
func failureName(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrGone):
		return "gone"
	case errors.Is(err, ErrConversationEnded):
		return "conversation_ended"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	default:
		return "internal"
	}
}

// Participant는 대화하는 사람이다.
type Participant struct {
	ID uuid.UUID
	// Timezone은 IANA 시간대 이름이다. 비었거나 모르는 이름이면 DefaultTimezone으로 본다.
	Timezone string
}

// StartInput은 대화를 시작하거나 이어가는 데 필요한 것이다.
type StartInput struct {
	User Participant
	// Mode는 store.ModeChat이나 store.ModeVoice다. 비워 두면 store.ModeChat이다.
	Mode string
	// Sink는 이 연결로 나가는 사건을 받는다.
	Sink Sink
}

// Session은 열린 대화 하나를 가리키는 손잡이다. 연결 하나가 하나를 들고 있다.
//
// 한 대화의 턴은 여기서 줄을 선다. 앞선 턴이 끝나기 전에 들어온 글은 기다린다.
// 여러 연결이 같은 대화를 다루면 Session도 여럿이 되고, 이 잠금은 그들 사이에서 아무것도 지켜 주지 않는다.
// 그때 순서를 지키는 것은 DB의 잠금이다. 직접 묻기를 한 대화에서 한 번으로 묶는 것은 대화 행의 확인 상태인데,
// 읽고 나서 옮기는 것으로는 모자라 옮기는 쪽이 자리를 차지하게 되어 있다(ClaimDirectAsk).
// 위기 고정 문구를 한 단계에 한 번으로 묶는 것도 같은 까닭으로 대화 행에 있다(crisis_spoken_stage).
type Session struct {
	engine *Engine
	sink   Sink

	user       Participant
	loc        *time.Location
	id         uuid.UUID
	dayID      uuid.UUID
	recordDate recorddate.Date
	mode       string
	resumed    bool

	// mu는 이 대화의 턴을 줄 세운다. 아래 값들도 이 잠금이 지킨다.
	mu sync.Mutex
	// crisis는 이 대화에 대응 단계 이상의 판정이 있었는지다. 그런 대화는 위기 상황용 지시문으로 이어가고,
	// 일기 초안을 자동으로 만들지 않는다.
	crisis bool
	// resourcesSent는 이 연결로 도움 자원을 이미 내보냈는지다.
	resourcesSent bool
	ended         bool
}

// ConversationID는 이 대화의 식별자다.
func (s *Session) ConversationID() uuid.UUID { return s.id }

// DayID는 이 대화가 매달린 하루의 식별자다.
func (s *Session) DayID() uuid.UUID { return s.dayID }

// RecordDate는 이 대화의 기록 날짜다.
func (s *Session) RecordDate() recorddate.Date { return s.recordDate }

// Resumed는 열려 있던 대화를 이어받았는지다.
func (s *Session) Resumed() bool { return s.resumed }

// UserID는 이 대화의 주인이다.
func (s *Session) UserID() uuid.UUID { return s.user.ID }

// Location은 기록 날짜를 정하는 데 쓴 시간대다.
func (s *Session) Location() *time.Location { return s.loc }

// ResourcesPinned는 이 대화에서 도움 자원을 화면에 고정해 두었는지다.
func (s *Session) ResourcesPinned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crisis
}

// Ended는 이 손잡이로 대화를 끝냈는지다. 다른 연결이나 주기 작업이 끝낸 것은 여기에 보이지 않는다.
func (s *Session) Ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// LogValue는 대화를 통째로 로그에 넘겨도 식별자와 상태만 남게 한다.
func (s *Session) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user_id", s.user.ID.String()),
		slog.String("conversation_id", s.id.String()),
		slog.String("record_date", s.recordDate.String()),
		slog.Bool("resumed", s.resumed),
	)
}

// Start는 대화를 시작하거나 열려 있던 대화를 이어간다.
//
// 열린 대화는 사용자마다 하나다. 기록 날짜가 지나도록 열려 있던 대화는 먼저 끝내고 새 대화를 연다.
// 돌아오기 전에 Ready를 내보내고, 이어가는 대화에 도움 자원이 고정되어 있었으면 Resources를,
// 새 대화면 첫 안부(AIText)를 이어서 내보낸다.
func (e *Engine) Start(ctx context.Context, in StartInput) (*Session, error) {
	if in.User.ID == uuid.Nil {
		return nil, errors.New("engine: user id is required")
	}
	if in.Sink == nil {
		return nil, errors.New("engine: sink is required")
	}
	mode := in.Mode
	if mode == "" {
		mode = store.ModeChat
	}
	if mode != store.ModeChat && mode != store.ModeVoice {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedMode, mode)
	}

	loc := e.location(ctx, in.User)
	s := &Session{engine: e, sink: in.Sink, user: in.User, loc: loc, mode: mode}

	row, err := e.openOrResume(ctx, s)
	if err != nil {
		return nil, err
	}
	s.id, s.dayID = row.ID, row.DayID
	spoken, err := crisis.StageFromInt(int(row.CrisisSpokenStage))
	if err != nil {
		return nil, fmt.Errorf("engine: stored crisis stage: %w", err)
	}

	var history []Utterance
	if s.resumed {
		history, err = e.history(ctx, s)
		if err != nil {
			return nil, err
		}
		s.crisis, err = e.store.Queries().ConversationHasCrisisGateEvent(ctx, db.ConversationHasCrisisGateEventParams{
			ConversationID: s.id, UserID: s.user.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: read crisis flag: %w", err)
		}
	}

	if err := s.sink.Emit(ctx, Ready{
		ConversationID:  s.id,
		RecordDate:      s.recordDate,
		Resumed:         s.resumed,
		Utterances:      history,
		ResourcesPinned: s.crisis,
	}); err != nil {
		return nil, fmt.Errorf("engine: emit ready: %w", err)
	}
	e.logger.LogAttrs(ctx, slog.LevelInfo, "conversation started", slog.Any("conversation", s))

	if s.crisis {
		// 자원 고정은 그 대화가 끝날 때까지 유지한다. 연결이 새로 붙었으면 화면도 비어 있으므로 다시 내보낸다.
		// 순서도 그대로 이어간다. 가장 급한 판정을 받은 사람이 화면을 새로 고쳤다고 보통의 차례로 돌아가지 않는다.
		items := e.phrases.Resources()
		if spoken >= crisis.StageUrgent {
			items = e.phrases.UrgentResources()
		}
		if err := s.emitResources(ctx, items); err != nil {
			return nil, err
		}
	}
	if !s.resumed {
		if err := e.emitOpening(ctx, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// openOrResume은 이어갈 대화를 찾거나 새로 연다. 기록 날짜가 지난 대화는 먼저 끝낸다.
func (e *Engine) openOrResume(ctx context.Context, s *Session) (db.Conversation, error) {
	for range openAttempts {
		now := e.clock.Now()
		today := recorddate.Of(now, s.loc)
		if today.IsZero() {
			return db.Conversation{}, errors.New("engine: cannot resolve the record date")
		}
		s.recordDate = today

		active, err := e.store.Queries().GetActiveConversation(ctx, s.user.ID)
		switch {
		case err == nil:
			date, err := store.RecordDate(active.RecordDate)
			if err != nil {
				return db.Conversation{}, fmt.Errorf("engine: read record date: %w", err)
			}
			if date == today {
				s.resumed = true
				return active.Conversation, nil
			}
			// 새벽의 경계를 넘긴 채 열려 있던 대화다. 이어가면 어제 한 말이 오늘의 일기로 간다.
			ended, err := e.end(ctx, endTarget{
				userID: s.user.ID, conversationID: active.Conversation.ID, dayID: active.Conversation.DayID,
			}, store.EndReasonIdle, nil)
			if err != nil && !errors.Is(err, errNotActive) {
				return db.Conversation{}, err
			}
			e.logger.LogAttrs(ctx, slog.LevelInfo, "stale conversation ended before a new one",
				slog.String("user_id", s.user.ID.String()),
				slog.String("conversation_id", active.Conversation.ID.String()),
				slog.String("record_date", date.String()),
				slog.Bool("diary_expected", ended.DiaryExpected),
			)
		case !errors.Is(err, store.ErrNotFound):
			return db.Conversation{}, fmt.Errorf("engine: get active conversation: %w", err)
		}

		conversationID, err := store.NewID()
		if err != nil {
			return db.Conversation{}, fmt.Errorf("engine: %w", err)
		}
		dayID, err := store.NewID()
		if err != nil {
			return db.Conversation{}, fmt.Errorf("engine: %w", err)
		}
		opened, err := e.store.OpenConversation(ctx, store.NewConversation{
			ID: conversationID, NewDayID: dayID, UserID: s.user.ID,
			RecordDate: today, StartedMode: s.mode, Now: now,
		})
		switch {
		case err == nil:
			return opened, nil
		case errors.Is(err, store.ErrActiveConversationExists):
			// 그 사이에 다른 연결이 대화를 열었다. 다시 읽어 그 대화를 이어간다.
			continue
		default:
			return db.Conversation{}, fmt.Errorf("engine: open conversation: %w", err)
		}
	}
	return db.Conversation{}, errors.New("engine: could not open or resume a conversation")
}

// history는 이어가는 대화의 지난 말을 순번대로 열어 돌려준다.
func (e *Engine) history(ctx context.Context, s *Session) ([]Utterance, error) {
	rows, err := e.store.Queries().ListUtterancesByConversation(ctx, db.ListUtterancesByConversationParams{
		ConversationID: s.id, UserID: s.user.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: list utterances: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	sealer, err := e.sealer(ctx, s.user.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Utterance, 0, len(rows))
	unreadable := 0
	for _, row := range rows {
		text, err := sealer.OpenString(row.TextEnc, sealing.UtteranceText(row.ID))
		if err != nil {
			// 말 하나가 열리지 않는다고 대화를 통째로 막지 않는다. 그 말만 빼고 이어간다.
			unreadable++
			continue
		}
		out = append(out, Utterance{
			Seq:             row.Seq,
			Speaker:         row.Speaker,
			Origin:          row.Origin,
			Text:            text,
			ClientMessageID: row.ClientMessageID,
			CreatedAt:       row.CreatedAt,
		})
	}
	if unreadable > 0 {
		e.logger.LogAttrs(ctx, slog.LevelError, "some utterances cannot be opened",
			slog.String("user_id", s.user.ID.String()),
			slog.String("conversation_id", s.id.String()),
			slog.Int("unreadable", unreadable),
		)
	}
	return out, nil
}

// emitOpening은 대화를 여는 첫 안부를 저장하고 내보낸다. 고정된 것은 이 말뿐이다.
func (e *Engine) emitOpening(ctx context.Context, s *Session) error {
	opening := e.phrases.Opening()
	out := outgoing{
		text:   opening.Display,
		speech: opening.Speech,
		origin: store.OriginFixed,
		phrase: opening.ID,
	}
	return e.emitReply(ctx, s, out, store.CheckStateNone)
}

// emitResources는 도움 자원을 한 연결에서 한 번만 내보낸다.
//
// 한 번 나갔다는 표시는 실제로 나간 뒤에 남긴다. 나가기 전에 남기면 내보내기가 실패했을 때 그 연결은
// 번호를 영영 다시 내주지 않는다. 반드시 닿아야 하는 것을 지키는 빗장이 그 자체를 막아서는 안 된다.
func (s *Session) emitResources(ctx context.Context, items []phrases.Resource) error {
	if s.resourcesSent {
		return nil
	}
	if err := s.sink.Emit(ctx, Resources{Items: items}); err != nil {
		return fmt.Errorf("engine: emit resources: %w", err)
	}
	s.resourcesSent = true
	return nil
}

// location은 사용자의 시간대를 읽는다. 읽을 수 없으면 기본 시간대를 쓰고 그 사실을 남긴다.
func (e *Engine) location(ctx context.Context, user Participant) *time.Location {
	if user.Timezone != "" {
		if loc, err := time.LoadLocation(user.Timezone); err == nil && user.Timezone != "Local" {
			return loc
		}
		// 시간대 이름은 사용자의 글이 아니지만, 가입할 때 확인한 값이라 여기서 다시 적을 까닭이 없다.
		e.logger.LogAttrs(ctx, slog.LevelWarn, "unknown user timezone, falling back to the default",
			slog.String("user_id", user.ID.String()),
			slog.String("fallback", DefaultTimezone),
		)
	}
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		// 실행 파일에 시간대 자료를 함께 묶으므로(cmd의 tzdata) 여기까지 오지 않는다.
		return time.UTC
	}
	return loc
}

// sealer는 그 사용자의 Sealer를 받아 온다. 계정이 지워졌으면 ErrGone이다.
func (e *Engine) sealer(ctx context.Context, userID uuid.UUID) (*crypto.Sealer, error) {
	sealer, err := e.sealers.For(ctx, userID)
	switch {
	case errors.Is(err, sealing.ErrNoKey):
		return nil, fmt.Errorf("%w: %w", ErrGone, err)
	case err != nil:
		return nil, fmt.Errorf("engine: %w", err)
	}
	return sealer, nil
}
