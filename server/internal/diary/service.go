package diary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// PromptTask는 일기 초안 지시문의 ID다.
const PromptTask = "diary_draft"

const (
	// DefaultMaxOutputTokens는 답의 출력 한도다. 겉으로 보이지 않는 생각 토큰도 이 한도에서 빠지므로
	// 몇 문장짜리 일기보다 훨씬 넉넉하게 잡는다. 빠듯하면 답이 문장 중간에서 잘리고, 잘린 답은 쓰지 못한다.
	DefaultMaxOutputTokens = 4096
	// DefaultCallTimeout은 모델의 답을 기다리는 시간이다. 대화가 끝난 뒤의 일이라 느린 답도 기다릴 수 있지만,
	// 가끔 나오는 아주 느린 응답에 작업자 한 자리가 오래 묶이지 않게 끝을 둔다.
	DefaultCallTimeout = 40 * time.Second
	// DefaultMaxEntryRunes는 모델이 한 번에 쓴 글의 최대 글자 수다. 초안은 짧아야 한다. 넘으면 지시를 따르지 않은 답으로 본다.
	DefaultMaxEntryRunes = 1200

	// maxRounds는 초안을 만드는 동안 그날의 기록이 바뀌었을 때 처음부터 다시 해 보는 횟수다.
	maxRounds = 3
)

// DefaultRetryDelays는 실패한 시도 뒤에 다음 시도까지 기다리는 시간이다. n번째 값은 n번째 시도가 실패한 뒤에 쓴다.
// 사용자는 대화를 끝내고 초안을 기다리고 있다. 작업 큐의 기본 간격(1초, 16초, 81초)으로는 빈 초안이 나오기까지 1분 반이 넘게 걸린다.
// 모델의 실패는 대개 그 답 하나의 문제(잘림, 빈 답, 느린 응답)라서 오래 물러설 까닭이 없다.
func DefaultRetryDelays() []time.Duration {
	return []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
}

var (
	// ErrRejected는 모델의 답이 출력 검사를 통과하지 못했다는 뜻이다. 답은 부를 때마다 달라지므로 다시 시도할 만하다.
	ErrRejected = errors.New("diary: draft rejected by output check")
	// ErrUnreadable은 저장된 글이 열리지 않는다는 뜻이다. 다시 읽어도 열리지 않으므로 다시 시도하지 않는다.
	ErrUnreadable = errors.New("diary: stored text cannot be opened")
	// ErrBusy는 초안을 만드는 동안 그날의 기록이 거듭 바뀌어 저장하지 못했다는 뜻이다.
	ErrBusy = errors.New("diary: day kept changing while drafting")

	// errGone은 하루나 계정이 지워졌다는 뜻이다. 오류가 아니라 결과(OutcomeGone)로 나간다.
	errGone = errors.New("diary: day or account is gone")
	// errStale은 읽은 뒤에 그날의 기록이 바뀌었다는 뜻이다. 다시 읽고 처음부터 한다.
	errStale = errors.New("diary: day changed since it was read")
)

// RejectError는 출력 검사에 걸린 이유를 담는다. 이유는 고정된 이름이고 글의 내용은 담지 않는다.
type RejectError struct {
	Reason string
}

func (e *RejectError) Error() string { return ErrRejected.Error() + " (" + e.Reason + ")" }

func (e *RejectError) Is(target error) bool { return target == ErrRejected }

// Permanent는 다시 시도해도 같은 결과가 나올 실패인지 알려준다.
// 무엇인지 모르는 오류(DB 연결 실패 등)는 다시 시도하는 쪽으로 본다.
func Permanent(err error) bool {
	if errors.Is(err, ErrUnreadable) {
		return true
	}
	switch failure := ai.Classify(err); failure.Kind {
	case ai.KindNone, ai.KindUnknown, ai.KindCanceled:
		return false
	default:
		return !failure.Retryable
	}
}

// Mode는 초안을 어떻게 쓰는지다.
type Mode string

const (
	// ModeFirst는 그날의 일기를 처음부터 쓴다.
	ModeFirst Mode = "first"
	// ModeAppend는 이미 있는 글 뒤에 붙일 단락만 쓴다.
	ModeAppend Mode = "append"
)

// Outcome은 한 번의 실행이 어떻게 끝났는지다.
type Outcome string

const (
	// OutcomeDrafted는 초안을 새로 썼거나 이어 붙여 저장했다는 뜻이다.
	OutcomeDrafted Outcome = "drafted"
	// OutcomeEmpty는 옮길 말이 없어 빈 초안만 만들었다는 뜻이다. 사용자가 직접 쓸 자리다.
	OutcomeEmpty Outcome = "empty"
	// OutcomeNothing은 일기를 건드리지 않았다는 뜻이다. 이미 담긴 대화뿐이었거나, 위기 대응이 있었던 대화뿐이었거나,
	// 있는 글에 보탤 말이 없었다.
	OutcomeNothing Outcome = "nothing"
	// OutcomeGone은 하루나 계정이 지워져 그만뒀다는 뜻이다.
	OutcomeGone Outcome = "gone"
	// OutcomeGaveUp은 초안을 만들지 못해 포기했다는 뜻이다. 그날에 글이 없었으면 빈 초안을 남겼다.
	OutcomeGaveUp Outcome = "gave_up"
)

// Target은 초안을 만들 하루다. 하루의 ID는 (사용자, 기록 날짜)마다 하나다.
type Target struct {
	UserID uuid.UUID
	DayID  uuid.UUID
}

// Result는 실행 결과다. 글의 내용은 담지 않으므로 통째로 로그에 남겨도 된다.
type Result struct {
	Outcome Outcome
	Mode    Mode
	// Conversations는 이번 초안의 재료가 된 대화의 수다.
	Conversations int
	// CrisisSkipped는 위기 대응이 있어서 재료에서 뺀 대화의 수다.
	CrisisSkipped int
	// UserUtterances는 모델에 보낸 사용자 발화의 수다.
	UserUtterances int
	// UnreadableUtterances는 열리지 않아 뺀 발화의 수다.
	UnreadableUtterances int
	// EntryRunes는 모델이 쓴 글의 글자 수다. 이어 붙인 경우 앞의 글은 세지 않는다.
	EntryRunes int
	// Model은 실제로 답한 모델이다. 모델을 부르지 않았으면 비어 있다.
	Model         string
	PromptVersion string
	// Rounds는 읽고, 쓰고, 저장하기를 몇 번 돌았는지다. 1보다 크면 그동안 그날의 기록이 바뀐 것이다.
	Rounds int
}

// LogValue는 결과를 로그 속성 묶음으로 바꾼다.
func (r Result) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("outcome", string(r.Outcome)),
		slog.String("mode", string(r.Mode)),
		slog.Int("conversations", r.Conversations),
		slog.Int("crisis_skipped", r.CrisisSkipped),
		slog.Int("user_utterances", r.UserUtterances),
		slog.Int("unreadable_utterances", r.UnreadableUtterances),
		slog.Int("entry_chars", r.EntryRunes),
		slog.String("model", r.Model),
		slog.String("prompt_version", r.PromptVersion),
		slog.Int("rounds", r.Rounds),
	)
}

// Sealers는 사용자의 Sealer를 내준다. *sealing.Sealers가 이 모양을 만족한다.
type Sealers interface {
	For(ctx context.Context, userID uuid.UUID) (*crypto.Sealer, error)
}

// Options는 Service를 만드는 데 필요한 것들이다.
type Options struct {
	Store   *store.Store
	Sealers Sealers
	// LLM은 대화 뒤 분석에 쓰는 모델이다.
	LLM     ai.LLM
	Prompts *prompts.Registry
	Clock   clock.Clock
	Logger  *slog.Logger
	// Thinking은 생각하기 수준이다. 비워 두면 모델의 기본 수준을 쓴다.
	Thinking string
	// 아래 셋은 0이면 기본값을 쓴다.
	MaxOutputTokens int
	CallTimeout     time.Duration
	MaxEntryRunes   int
	// RetryDelays가 비어 있으면 DefaultRetryDelays를 쓴다.
	RetryDelays []time.Duration
}

// Service는 하루의 일기 초안을 만든다. 여러 고루틴이 함께 써도 된다.
type Service struct {
	store           *store.Store
	sealers         Sealers
	llm             ai.LLM
	prompt          prompts.Prompt
	clock           clock.Clock
	logger          *slog.Logger
	thinking        string
	maxOutputTokens int
	callTimeout     time.Duration
	maxEntryRunes   int
	retryDelays     []time.Duration
}

// NewService는 Service를 만든다. 지시문이 없으면 첫 작업이 아니라 여기서 실패한다.
func NewService(opts Options) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("diary: store is required")
	case opts.Sealers == nil:
		return nil, errors.New("diary: sealers are required")
	case opts.LLM == nil:
		return nil, errors.New("diary: language model is required")
	case opts.Prompts == nil:
		return nil, errors.New("diary: prompt registry is required")
	case opts.Clock == nil:
		return nil, errors.New("diary: clock is required")
	case opts.Logger == nil:
		return nil, errors.New("diary: logger is required")
	case opts.MaxOutputTokens < 0, opts.CallTimeout < 0, opts.MaxEntryRunes < 0:
		return nil, errors.New("diary: limits must not be negative")
	}
	for _, delay := range opts.RetryDelays {
		if delay < 0 {
			return nil, errors.New("diary: retry delays must not be negative")
		}
	}
	prompt, err := opts.Prompts.Get(PromptTask)
	if err != nil {
		return nil, fmt.Errorf("diary: %w", err)
	}
	if prompt.Schema == nil {
		// 스키마가 없으면 모델은 JSON이 아닌 글로 답하고, 그 답은 모두 풀리지 않는 답으로 버려진다.
		return nil, errors.New("diary: prompt " + PromptTask + " needs a response schema")
	}

	s := &Service{
		store:           opts.Store,
		sealers:         opts.Sealers,
		llm:             opts.LLM,
		prompt:          prompt,
		clock:           opts.Clock,
		logger:          opts.Logger,
		thinking:        opts.Thinking,
		maxOutputTokens: opts.MaxOutputTokens,
		callTimeout:     opts.CallTimeout,
		maxEntryRunes:   opts.MaxEntryRunes,
		retryDelays:     append([]time.Duration(nil), opts.RetryDelays...),
	}
	if len(s.retryDelays) == 0 {
		s.retryDelays = DefaultRetryDelays()
	}
	if s.maxOutputTokens == 0 {
		s.maxOutputTokens = DefaultMaxOutputTokens
	}
	if s.callTimeout == 0 {
		s.callTimeout = DefaultCallTimeout
	}
	if s.maxEntryRunes == 0 {
		s.maxEntryRunes = DefaultMaxEntryRunes
	}
	return s, nil
}

// MaxDuration은 Draft 한 번이 가장 오래 걸릴 때의 시간이다. 작업의 제한 시간을 정하는 데 쓴다.
func (s *Service) MaxDuration() time.Duration {
	return time.Duration(maxRounds)*s.callTimeout + 30*time.Second
}

// NextRetry는 attempt번째 시도가 실패한 뒤에 다음 시도를 할 시각이다.
func (s *Service) NextRetry(attempt int) time.Time {
	i := min(max(attempt, 1), len(s.retryDelays)) - 1
	return s.clock.Now().Add(s.retryDelays[i])
}

// DraftByDate는 기록 날짜로 하루를 찾아 Draft를 부른다. 그날의 기록이 없으면 OutcomeGone이다.
func (s *Service) DraftByDate(ctx context.Context, userID uuid.UUID, date recorddate.Date) (Result, error) {
	target, found, err := s.targetByDate(ctx, userID, date)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{Outcome: OutcomeGone, PromptVersion: s.prompt.Version}, nil
	}
	return s.Draft(ctx, target)
}

// Draft는 그날의 끝난 대화 가운데 아직 일기에 담기지 않은 것을 초안으로 옮겨 저장한다.
//
// 몇 번을 불러도 같은 대화가 두 번 담기지 않는다. 오류를 돌려줬다면 일기와 대화의 상태는 부르기 전 그대로다.
// 오류가 다시 시도할 만한 것인지는 Permanent로 가린다. 시도를 다 썼으면 GiveUp을 부른다.
func (s *Service) Draft(ctx context.Context, target Target) (Result, error) {
	result := Result{PromptVersion: s.prompt.Version}

	// 모델은 이미 있는 글을 보지 않으므로, 쓴 글은 재료(방식과 대화)가 같으면 그대로 다시 쓸 수 있다.
	// 저장하려는 순간 사용자가 일기를 고쳐서 다시 도는 경우에 모델을 또 부르지 않는다.
	var written composed
	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round

		snap, err := s.load(ctx, target)
		if errors.Is(err, errGone) {
			result.Outcome = OutcomeGone
			return result, nil
		}
		if err != nil {
			return result, err
		}
		result.Mode = snap.mode
		result.Conversations = len(snap.included)
		result.CrisisSkipped = len(snap.considered) - len(snap.included)
		result.UserUtterances = snap.lineCount()
		result.UnreadableUtterances = snap.unreadable
		if len(snap.considered) == 0 {
			result.Outcome = OutcomeNothing
			return result, nil
		}

		if key := snap.materialKey(); key != written.key {
			written, err = s.compose(ctx, snap)
			if err != nil {
				return result, err
			}
			written.key = key
		}
		result.Model = written.model
		result.EntryRunes = written.runes

		outcome, err := s.save(ctx, target, snap, written.entry)
		switch {
		case errors.Is(err, errStale):
			continue
		case errors.Is(err, errGone):
			result.Outcome = OutcomeGone
			return result, nil
		case err != nil:
			return result, err
		}
		result.Outcome = outcome
		return result, nil
	}
	return result, ErrBusy
}

// composed는 모델이 쓴 글과 그 글이 어떤 재료에서 나왔는지다.
type composed struct {
	key   string
	entry logging.Redacted
	runes int
	model string
}
