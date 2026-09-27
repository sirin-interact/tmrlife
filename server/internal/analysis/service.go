package analysis

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
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// PromptTask는 신호 추출 지시문의 ID다.
const PromptTask = "signal_extract"

const (
	// DefaultMaxOutputTokens는 답의 출력 한도다. 겉으로 보이지 않는 생각 토큰도 이 한도에서 빠지므로
	// 여덟 항목의 JSON보다 훨씬 넉넉하게 잡는다. 빠듯하면 답이 중간에서 잘리고, 잘린 답은 쓰지 못한다.
	// 설정의 기본값(LLM_MAX_OUTPUT_ANALYSIS)과 같다.
	DefaultMaxOutputTokens = 8192
	// DefaultCallTimeout은 모델의 답을 기다리는 시간이다. 대화가 끝난 뒤의 일이라 느린 답도 기다릴 수 있지만,
	// 가끔 나오는 아주 느린 응답에 작업자 한 자리가 오래 묶이지 않게 끝을 둔다. 설정의 기본값(LLM_ANALYSIS_BUDGET)과 같다.
	DefaultCallTimeout = 60 * time.Second
)

// DefaultRetryDelays는 실패한 시도 뒤에 다음 시도까지 기다리는 시간이다. n번째 값은 n번째 시도가 실패한 뒤에 쓴다.
// 사용자가 화면에서 기다리는 결과가 아니라 일기 초안보다 느긋하게 물러서지만, 대화를 끝낸 김에 추세를 열어 보는 사람이 있으므로
// 큐의 기본 간격(1초, 16초, 81초)만큼 오래 끌지는 않는다.
func DefaultRetryDelays() []time.Duration {
	return []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}
}

var (
	// ErrRejected는 모델의 답이 정해진 꼴을 따르지 않았다는 뜻이다. 답은 부를 때마다 달라지므로 다시 시도할 만하다.
	ErrRejected = errors.New("analysis: model answer rejected")
	// ErrUnreadable은 저장된 발화가 열리지 않는다는 뜻이다. 다시 읽어도 열리지 않으므로 다시 시도하지 않는다.
	ErrUnreadable = errors.New("analysis: stored utterance cannot be opened")
	// ErrBusy는 다른 실행이 그 대화를 맡고 있다는 뜻이다. 조금 뒤에 다시 해 본다.
	ErrBusy = errors.New("analysis: another run holds the conversation")
	// ErrStillActive는 대화가 아직 열려 있다는 뜻이다. 열린 대화를 뽑으면 일부만 본 결과가 그날의 판단으로 굳는다.
	ErrStillActive = errors.New("analysis: conversation is still active")

	// errGone은 대화나 계정이 지워졌다는 뜻이다. 오류가 아니라 결과(OutcomeGone)로 나간다.
	errGone = errors.New("analysis: conversation or account is gone")
)

// RejectError는 답이 왜 쓸 수 없었는지다. 이유는 고정된 이름이고 답의 내용은 담지 않는다.
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

// Outcome은 한 번의 실행이 어떻게 끝났는지다.
type Outcome string

const (
	// OutcomeSaved는 여덟 항목의 행을 저장하고 분석을 닫았다는 뜻이다.
	OutcomeSaved Outcome = "saved"
	// OutcomeAlreadyDone은 그 대화의 분석이 이미 끝나 있었다는 뜻이다. 아무것도 저장하지 않았다.
	OutcomeAlreadyDone Outcome = "already_done"
	// OutcomeDisabled는 사용자가 분석을 꺼 두었다는 뜻이다.
	OutcomeDisabled Outcome = "disabled"
	// OutcomeNothingSaid는 사용자가 한 말이 없어 뽑을 것이 없었다는 뜻이다.
	OutcomeNothingSaid Outcome = "nothing_said"
	// OutcomeGone은 대화나 계정이 지워져 그만뒀다는 뜻이다.
	OutcomeGone Outcome = "gone"
	// OutcomeGaveUp은 다 시도하고도 뽑지 못해 분석을 failed로 닫았다는 뜻이다. 행은 하나도 남기지 않았다.
	OutcomeGaveUp Outcome = "gave_up"
)

// Target은 신호를 뽑을 대화다.
type Target struct {
	UserID         uuid.UUID
	ConversationID uuid.UUID
}

// Counts는 여덟 항목의 판단이 어떻게 갈렸는지다. 내용이 없으므로 통째로 로그에 남겨도 된다.
type Counts struct {
	Observed     int
	NotObserved  int
	NotMentioned int
	Direct       int
	Indirect     int
}

func (c Counts) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("observed", c.Observed),
		slog.Int("not_observed", c.NotObserved),
		slog.Int("not_mentioned", c.NotMentioned),
		slog.Int("direct", c.Direct),
		slog.Int("indirect", c.Indirect),
	)
}

// Result는 실행 결과다. 발화도 근거 토막도 담지 않으므로 통째로 로그에 남겨도 된다.
type Result struct {
	Outcome Outcome
	// UserUtterances는 모델에 보낸 사용자 발화의 수다.
	UserUtterances int
	// UnreadableUtterances는 열리지 않아 뺀 발화의 수다.
	UnreadableUtterances int
	// Counts는 저장한 판단의 갈래다. 저장하지 않았으면 모두 0이다.
	Counts Counts
	// Dropped는 근거가 대조를 통과하지 못해 언급 없음으로 되돌린 항목의 수다.
	Dropped int
	// DropReasons는 되돌린 까닭마다 몇 항목이었는지다. 이름은 고정되어 있다.
	DropReasons map[string]int
	// Repaired는 근거는 맞았지만 명시성이 앞뒤가 맞지 않아 간접 추론으로 고친 항목의 수다.
	Repaired int
	// LineMismatch는 모델이 가리킨 줄에는 없고 다른 줄에 있던 근거의 수다. 지시문이 얼마나 지켜지는지 보는 값이다.
	LineMismatch int
	// Unanchored는 같은 글자가 여러 발화에 있어 어느 발화에서 왔는지 가리지 못한 근거의 수다.
	// 판단과 근거는 저장하고 발화만 가리키지 않은 항목이다.
	Unanchored int
	// Model은 실제로 답한 모델이다. 모델을 부르지 않았으면 비어 있다.
	Model         string
	PromptVersion string
	// ExtractorVersion은 신호 행에 함께 저장한 표시다.
	ExtractorVersion string
	// Takeover는 다른 실행이 맡아 둔 채로 멈춘 대화를 이어받았는지다.
	Takeover bool
}

// LogValue는 결과를 로그 속성 묶음으로 바꾼다.
func (r Result) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("outcome", string(r.Outcome)),
		slog.Int("user_utterances", r.UserUtterances),
		slog.Int("unreadable_utterances", r.UnreadableUtterances),
		slog.Any("counts", r.Counts),
		slog.Int("dropped", r.Dropped),
		slog.Int("repaired", r.Repaired),
		slog.Int("line_mismatch", r.LineMismatch),
		slog.Int("unanchored", r.Unanchored),
		slog.String("model", r.Model),
		slog.String("prompt_version", r.PromptVersion),
		slog.Bool("takeover", r.Takeover),
	}
	if len(r.DropReasons) > 0 {
		reasons := make([]slog.Attr, 0, len(r.DropReasons))
		for _, reason := range allDropReasons {
			if n := r.DropReasons[reason]; n > 0 {
				reasons = append(reasons, slog.Int(reason, n))
			}
		}
		attrs = append(attrs, slog.Attr{Key: "drop_reasons", Value: slog.GroupValue(reasons...)})
	}
	return slog.GroupValue(attrs...)
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
	// 아래 둘은 0이면 기본값을 쓴다.
	MaxOutputTokens int
	CallTimeout     time.Duration
	// RetryDelays가 비어 있으면 DefaultRetryDelays를 쓴다.
	RetryDelays []time.Duration
}

// Service는 끝난 대화에서 신호를 뽑는다. 여러 고루틴이 함께 써도 된다.
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
	retryDelays     []time.Duration
}

// NewService는 Service를 만든다. 지시문이 없으면 첫 작업이 아니라 여기서 실패한다.
func NewService(opts Options) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("analysis: store is required")
	case opts.Sealers == nil:
		return nil, errors.New("analysis: sealers are required")
	case opts.LLM == nil:
		return nil, errors.New("analysis: language model is required")
	case opts.Prompts == nil:
		return nil, errors.New("analysis: prompt registry is required")
	case opts.Clock == nil:
		return nil, errors.New("analysis: clock is required")
	case opts.Logger == nil:
		return nil, errors.New("analysis: logger is required")
	case opts.MaxOutputTokens < 0, opts.CallTimeout < 0:
		return nil, errors.New("analysis: limits must not be negative")
	}
	for _, delay := range opts.RetryDelays {
		if delay < 0 {
			return nil, errors.New("analysis: retry delays must not be negative")
		}
	}
	prompt, err := opts.Prompts.Get(PromptTask)
	if err != nil {
		return nil, fmt.Errorf("analysis: %w", err)
	}
	if prompt.Schema == nil {
		// 스키마가 없으면 모델은 JSON이 아닌 글로 답하고, 그 답은 모두 풀리지 않는 답으로 버려진다.
		return nil, errors.New("analysis: prompt " + PromptTask + " needs a response schema")
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
	return s, nil
}

// MaxDuration은 Extract 한 번이 가장 오래 걸릴 때의 시간이다. 작업의 제한 시간을 정하는 데 쓴다.
func (s *Service) MaxDuration() time.Duration {
	return s.callTimeout + 30*time.Second
}

// NextRetry는 attempt번째 시도가 실패한 뒤에 다음 시도를 할 시각이다.
func (s *Service) NextRetry(attempt int) time.Time {
	i := min(max(attempt, 1), len(s.retryDelays)) - 1
	return s.clock.Now().Add(s.retryDelays[i])
}

// PromptVersion은 지금 쓰는 지시문의 판이다.
func (s *Service) PromptVersion() string { return s.prompt.Version }

// ExtractorVersion은 신호 행에 남길 표시다. 지시문의 판과 실제로 답한 모델을 함께 담는다.
// 어느 지시문에서 나온 판단인지만으로는 예비 모델이 답한 행을 가릴 수 없다.
func ExtractorVersion(promptVersion, model string) string {
	if model == "" {
		return promptVersion
	}
	return promptVersion + "/" + model
}

// Extract는 끝난 대화 하나에서 여덟 항목의 신호를 뽑아 저장한다.
//
// 몇 번을 불러도 행이 두 번 들어가지 않는다. 오류를 돌려줬다면 신호 행은 하나도 들어가지 않았다.
// 오류가 다시 시도할 만한 것인지는 Permanent로 가린다. 시도를 다 썼으면 GiveUp을 부른다.
func (s *Service) Extract(ctx context.Context, target Target) (Result, error) {
	result := Result{PromptVersion: s.prompt.Version}

	material, err := s.load(ctx, target)
	switch {
	case errors.Is(err, errGone):
		result.Outcome = OutcomeGone
		return result, nil
	case err != nil:
		return result, err
	}
	result.Takeover = material.takeover
	result.UserUtterances = len(material.lines)
	result.UnreadableUtterances = material.unreadable

	switch {
	case material.alreadyDone:
		result.Outcome = OutcomeAlreadyDone
		return result, nil
	case material.disabled:
		result.Outcome = OutcomeDisabled
		_, err := s.close(ctx, target, store.AnalysisNone)
		return result, err
	case len(material.lines) == 0:
		// 사용자가 아무 말도 하지 않은 대화다. 여덟 항목을 언급 없음으로 남기면 그날이 "대화한 날"로 세어져
		// 점수를 나누는 일수만 늘어난다. 말이 없었던 자리는 기록하지 않는다.
		result.Outcome = OutcomeNothingSaid
		_, err := s.close(ctx, target, store.AnalysisNone)
		return result, err
	}

	extracted, err := s.extract(ctx, material)
	if err != nil {
		return result, err
	}
	result.Model = extracted.model
	result.Counts = extracted.counts
	result.Dropped = extracted.dropped
	result.DropReasons = extracted.dropReasons
	result.Repaired = extracted.repaired
	result.LineMismatch = extracted.lineMismatch
	result.Unanchored = extracted.unanchored
	result.ExtractorVersion = ExtractorVersion(s.prompt.Version, extracted.model)

	err = s.store.SaveConversationSignals(ctx, store.ConversationAnalysis{
		UserID:           target.UserID,
		DayID:            material.dayID,
		ConversationID:   target.ConversationID,
		Judgements:       extracted.judgements,
		ExtractorVersion: result.ExtractorVersion,
		Now:              s.clock.Now(),
	})
	switch {
	case errors.Is(err, store.ErrSignalsAlreadySaved):
		// 맡은 뒤에 다른 실행이 먼저 저장했다. 먼저 들어간 여덟 행이 그대로 남는다.
		result.Outcome = OutcomeAlreadyDone
		result.Counts = Counts{}
		return result, nil
	case errors.Is(err, store.ErrNotFound):
		result.Outcome = OutcomeGone
		return result, nil
	case err != nil:
		return result, fmt.Errorf("analysis: save signals: %w", err)
	}
	result.Outcome = OutcomeSaved
	return result, nil
}

// GiveUp은 뽑기를 포기하고 분석을 failed로 닫는다. 신호 행은 하나도 남기지 않는다.
// 절반만 남은 행은 모든 숫자를 망치지만, 행이 없는 대화는 계산에서 그냥 빠진다.
func (s *Service) GiveUp(ctx context.Context, target Target) (Result, error) {
	result := Result{Outcome: OutcomeGaveUp, PromptVersion: s.prompt.Version}
	closed, err := s.close(ctx, target, store.AnalysisFailed)
	if err != nil {
		return result, err
	}
	if !closed {
		// 적을 곳이 없었다. 대화가 지워졌거나, 이미 분석이 끝났거나, 아직 열려 있다.
		result.Outcome = OutcomeGone
	}
	return result, nil
}

// close는 분석 상태만 적는다. 이미 done인 대화는 건드리지 않는다.
// 대화가 없거나 아직 열려 있으면 적을 곳이 없다(closed가 false). 어느 쪽이든 행은 하나도 들어가지 않으므로 계산은 온전하다.
func (s *Service) close(ctx context.Context, target Target, status string) (closed bool, err error) {
	_, err = s.store.Queries().SetConversationAnalysisStatus(ctx, db.SetConversationAnalysisStatusParams{
		AnalysisStatus: status, ID: target.ConversationID, UserID: target.UserID,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("analysis: set analysis status: %w", err)
	}
	return true, nil
}

// judgementCounts는 저장할 판단을 갈래별로 센다.
func judgementCounts(judgements [signal.ItemCount]store.SignalJudgement) Counts {
	var counts Counts
	for _, item := range signal.AllItems() {
		j := judgements[item.Index()].Judgement
		switch j.Status {
		case signal.Observed:
			counts.Observed++
		case signal.NotObserved:
			counts.NotObserved++
		case signal.NotMentioned:
			counts.NotMentioned++
		}
		switch j.Explicitness {
		case signal.Direct:
			counts.Direct++
		case signal.Indirect:
			counts.Indirect++
		case signal.None:
		}
	}
	return counts
}
