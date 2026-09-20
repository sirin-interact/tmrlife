// Package classifier는 위기 관문의 둘째 겹이다. 대화를 만드는 모델과 다른 모델, 다른 지시문, 다른 호출로
// 사용자의 마지막 발화가 어느 단계인지 판별한다.
//
// 규칙 겹이 읽지 못하는 것을 맡는다. 앞의 질문에 대한 짧은 답("응", "그 정도는 아니야"),
// 같은 말의 다른 쓰임(창피해서 나온 과장과 가라앉은 이야기 끝의 말), 죽음을 가리키는 낱말 없이 속뜻으로만 읽히는 말이다.
//
// 이 겹은 실패할 수 있다. 모델의 응답 시간은 가끔 크게 늘어지고, 답이 막히거나 비거나 잘리기도 한다.
// 어떤 실패든 "답하지 못함"으로 돌려준다. 읽지 못한 답을 "해당 없음"으로 읽으면 걸러야 할 말을 조용히 통과시키게 된다.
// 답하지 못한 턴을 어떻게 다룰지는 core/crisis가 정한다.
//
// 요청과 답에는 사용자의 말이 들어 있다. 이 패키지는 로그를 남기지 않고, Result는 로그에 넘겨도 말이 나가지 않는다.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
)

// Task는 판별 지시문의 ID다.
const Task = "gate_classifier"

const (
	// DefaultContextTurns는 마지막 발화와 함께 보내는 직전 말의 수다.
	DefaultContextTurns = 3
	// DefaultMaxAttempts는 한 번의 판별에서 모델을 부르는 최대 횟수다.
	DefaultMaxAttempts = 2
	// maxContextRunes는 직전 말 하나에서 보내는 최대 글자 수다. 넘으면 뒤쪽을 남긴다.
	// 문맥은 뜻을 읽는 데만 쓰이고, 마지막 발화에 가까운 쪽이 더 쓸모 있다. 마지막 발화는 자르지 않는다.
	maxContextRunes = 400
)

// Speaker는 말한 쪽이다.
type Speaker string

const (
	SpeakerUser Speaker = "user"
	SpeakerAI   Speaker = "ai"
)

// Turn은 앞선 대화의 말 하나다.
type Turn struct {
	Speaker Speaker
	Text    string
}

// Input은 판별 한 번에 필요한 것이다.
type Input struct {
	// Context는 마지막 발화보다 앞선 말들이다. 오래된 말부터 넣는다. 몇 개를 넣든 뒤에서 정해진 수만 쓴다.
	Context []Turn
	// Utterance는 판별할 사용자의 마지막 발화다.
	Utterance string
}

// Failure는 판별이 답하지 못한 까닭이다. 로그와 지표에 그대로 쓸 수 있는 고정된 이름이다.
type Failure string

const (
	FailureNone Failure = ""
	// 아래는 ai 패키지의 실패 종류와 이름이 같다.
	FailureTimeout        Failure = "timeout"
	FailureCanceled       Failure = "canceled"
	FailureBlocked        Failure = "blocked"
	FailureNoCandidate    Failure = "no_candidate"
	FailureAbnormalFinish Failure = "abnormal_finish"
	FailureEmpty          Failure = "empty"
	FailureTruncated      Failure = "truncated"
	FailureInvalidJSON    Failure = "invalid_json"
	FailureProvider       Failure = "provider"
	FailureInvalidRequest Failure = "invalid_request"
	FailureUnknown        Failure = "unknown"
	// FailureStageMissing은 JSON은 맞지만 단계가 없는 답이다.
	FailureStageMissing Failure = "stage_missing"
	// FailureStageOutOfRange는 단계가 0부터 3이 아닌 답이다.
	FailureStageOutOfRange Failure = "stage_out_of_range"
	// FailurePanic은 모델을 부르는 구현 안에서 패닉이 난 경우다.
	FailurePanic Failure = "panic"
)

// Result는 판별의 결과다. Evidence에는 사용자의 말이 들어 있으므로 통째로 로그에 넘기지 않는다.
// 넘기더라도 LogValue가 단계, 실패 종류, 걸린 시간만 남긴다.
type Result struct {
	// Answered는 쓸 수 있는 답을 제때 받았는지다. false면 Stage와 Evidence는 비어 있다.
	Answered bool
	Stage    crisis.Stage
	// Evidence는 모델이 근거로 든 말이다. 마지막 발화에 글자 그대로 있는 것만 남긴다.
	Evidence string
	// EvidenceDropped는 모델이 든 근거가 마지막 발화에 없어서 버렸는지다. 단계는 그대로 쓴다.
	EvidenceDropped bool
	// Failure는 답하지 못한 까닭이다. 여러 번 불렀으면 마지막 실패다.
	Failure Failure
	// Attempts는 모델을 부른 횟수다.
	Attempts int
	// Latency는 판별 전체에 걸린 시간이다.
	Latency time.Duration
	// Model은 답한 모델이다. 답하지 못했으면 비어 있을 수 있다.
	Model string
	// PromptVersion은 판별에 쓴 지시문의 버전 표시다.
	PromptVersion string
}

// Core는 core/crisis가 받는 꼴로 바꾼다. 답하지 못한 판별은 빈 값, 곧 실패 표시가 된다.
func (r Result) Core() crisis.AIResult {
	if !r.Answered {
		return crisis.AIFailed()
	}
	return crisis.AIAnswered(r.Stage)
}

// LogValue는 결과를 통째로 로그에 넘겨도 사용자의 말이 나가지 않게 한다.
func (r Result) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("answered", r.Answered),
		slog.Int("stage", int(r.Stage)),
		slog.String("failure", string(r.Failure)),
		slog.Int("attempts", r.Attempts),
		slog.Duration("latency", r.Latency),
		slog.Int("evidence_chars", utf8.RuneCountInString(r.Evidence)),
		slog.Bool("evidence_dropped", r.EvidenceDropped),
		slog.String("model", r.Model),
		slog.String("prompt_version", r.PromptVersion),
	)
}

// Config는 판별기의 조정 값이다. 모델 이름은 넘겨받는 LLM이 이미 정하고 있다.
type Config struct {
	// Timeout은 판별 전체를 기다리는 시간이다. 다시 부르는 것까지 이 안에서 끝나야 한다.
	// 관문이 끝나야 답이 나가므로 이 값이 곧 사용자가 기다리는 시간의 상한이다.
	Timeout time.Duration
	// Thinking은 생각하기 수준이다. 비워 두면 LLM의 기본 수준이다.
	Thinking string
	// MaxOutputTokens가 0이면 LLM의 기본 한도다. 생각 토큰도 이 한도에서 빠지므로 답의 길이에 맞춰 줄이지 않는다.
	MaxOutputTokens int
	// ContextTurns가 0이면 DefaultContextTurns다.
	ContextTurns int
	// MaxAttempts가 0이면 DefaultMaxAttempts다. 1이면 다시 부르지 않는다.
	MaxAttempts int
}

// Classifier는 판별 모델을 부른다. 여러 고루틴에서 함께 써도 된다.
type Classifier struct {
	llm    ai.LLM
	prompt prompts.Prompt
	clock  clock.Clock
	cfg    Config
}

// New는 판별기를 만든다. prompt는 지시문 등록소에서 Task로 꺼낸 것이다.
func New(llm ai.LLM, prompt prompts.Prompt, clk clock.Clock, cfg Config) (*Classifier, error) {
	switch {
	case llm == nil:
		return nil, errors.New("classifier: llm is required")
	case clk == nil:
		return nil, errors.New("classifier: clock is required")
	case strings.TrimSpace(prompt.System) == "":
		return nil, errors.New("classifier: prompt has no system text")
	case len(prompt.Schema) == 0:
		// 스키마 없이 부르면 모델이 글로 답하고, 그 답은 전부 실패로 읽힌다. 뜰 때 드러나게 한다.
		return nil, errors.New("classifier: prompt has no json schema")
	case cfg.Timeout <= 0:
		return nil, errors.New("classifier: timeout must be greater than zero")
	case cfg.MaxOutputTokens < 0:
		return nil, errors.New("classifier: max output tokens must not be negative")
	case cfg.ContextTurns < 0:
		return nil, errors.New("classifier: context turns must not be negative")
	case cfg.MaxAttempts < 0:
		return nil, errors.New("classifier: max attempts must not be negative")
	}
	if cfg.ContextTurns == 0 {
		cfg.ContextTurns = DefaultContextTurns
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	return &Classifier{llm: llm, prompt: prompt, clock: clk, cfg: cfg}, nil
}

// Classify는 마지막 발화를 판별한다. 오류를 돌려주지 않는다. 실패는 모두 Result.Failure에 담긴 "답하지 못함"이다.
//
// ctx가 끝나면 진행 중인 호출도 멈춘다. 사용자가 연결을 끊었을 때 부르는 쪽이 ctx를 취소하면 된다.
func (c *Classifier) Classify(ctx context.Context, in Input) (result Result) {
	start := c.clock.Now()
	result = Result{PromptVersion: c.prompt.Version}
	defer func() { result.Latency = c.clock.Now().Sub(start) }()

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := c.Request(in)
	if err != nil {
		result.Failure = FailureInvalidRequest
		return result
	}

	for result.Attempts < c.cfg.MaxAttempts {
		result.Attempts++
		verdict, retry := c.attempt(ctx, req, in.Utterance)
		verdict.Attempts, verdict.PromptVersion = result.Attempts, result.PromptVersion
		result = verdict
		if result.Answered || !retry || ctx.Err() != nil {
			break
		}
	}
	return result
}

// Request는 판별 모델에 보낼 요청을 만든다. 실제 모델을 부르는 평가에서 요청의 모양을 볼 수 있게 드러내 둔다.
func (c *Classifier) Request(in Input) (ai.Request, error) {
	message, err := encodeInput(in, c.cfg.ContextTurns)
	if err != nil {
		return ai.Request{}, err
	}
	req := c.prompt.Request([]ai.Message{{Role: ai.RoleUser, Text: message}})
	req.Thinking = c.cfg.Thinking
	req.MaxOutputTokens = c.cfg.MaxOutputTokens
	return req, nil
}

// attempt는 모델을 한 번 부르고 답을 읽는다. retry는 다시 불러 볼 만한 실패인지다.
func (c *Classifier) attempt(ctx context.Context, req ai.Request, utterance string) (result Result, retry bool) {
	defer func() {
		// 구현 안의 패닉이 이 고루틴을 죽이면 그 턴의 관문이 통째로 사라진다. 답하지 못한 것으로 바꿔 규칙의 판정이라도 쓰이게 한다.
		if r := recover(); r != nil {
			result, retry = Result{Failure: FailurePanic}, false
		}
	}()

	resp, err := c.llm.Generate(ctx, req)
	if err != nil {
		failure := ai.Classify(err)
		return Result{Failure: failureOf(failure.Kind)}, failure.Retryable
	}

	var answer struct {
		Stage    *int   `json:"stage"`
		Evidence string `json:"evidence"`
	}
	if err := ai.DecodeJSON(req, resp, &answer); err != nil {
		return Result{Failure: FailureInvalidJSON, Model: resp.Model}, true
	}
	if answer.Stage == nil {
		return Result{Failure: FailureStageMissing, Model: resp.Model}, true
	}
	stage, err := crisis.StageFromInt(*answer.Stage)
	if err != nil {
		return Result{Failure: FailureStageOutOfRange, Model: resp.Model}, true
	}

	result = Result{Answered: true, Stage: stage, Model: resp.Model}
	evidence := strings.TrimSpace(answer.Evidence)
	switch {
	case stage == crisis.StageNone || evidence == "":
		// 해당 없음에는 근거를 남기지 않는다.
	case strings.Contains(utterance, evidence):
		result.Evidence = evidence
	default:
		// 모델이 말을 고쳐 옮겼거나 문맥의 말을 옮긴 것이다. 사용자가 하지 않은 말을 근거로 남길 수는 없다.
		// 단계까지 버리면 멀쩡한 판정을 실패로 만들게 되므로 근거만 버린다.
		result.EvidenceDropped = true
	}
	return result, false
}

func failureOf(kind ai.Kind) Failure {
	switch kind {
	case ai.KindTimeout:
		return FailureTimeout
	case ai.KindCanceled:
		return FailureCanceled
	case ai.KindBlocked:
		return FailureBlocked
	case ai.KindNoCandidate:
		return FailureNoCandidate
	case ai.KindAbnormalFinish:
		return FailureAbnormalFinish
	case ai.KindEmpty:
		return FailureEmpty
	case ai.KindTruncated:
		return FailureTruncated
	case ai.KindInvalidJSON:
		return FailureInvalidJSON
	case ai.KindProvider:
		return FailureProvider
	case ai.KindInvalidRequest:
		return FailureInvalidRequest
	default:
		return FailureUnknown
	}
}

type inputMessage struct {
	Context           []inputTurn `json:"context"`
	LastUserUtterance string      `json:"last_user_utterance"`
}

type inputTurn struct {
	Speaker Speaker `json:"speaker"`
	Text    string  `json:"text"`
}

// encodeInput은 대화를 JSON 하나로 묶는다.
//
// 글로 이어 붙이지 않는 이유: 사용자의 말에 줄바꿈이나 "마지막 발화:" 같은 글이 들어 있으면 어디까지가 누구의 말인지 흐려진다.
// JSON으로 묶으면 그런 글자가 모두 이스케이프되어 말의 경계가 바뀌지 않는다.
func encodeInput(in Input, contextTurns int) (string, error) {
	if strings.TrimSpace(in.Utterance) == "" {
		return "", errors.New("classifier: utterance is blank")
	}

	msg := inputMessage{Context: []inputTurn{}, LastUserUtterance: in.Utterance}
	turns := make([]Turn, 0, len(in.Context))
	for _, turn := range in.Context {
		if strings.TrimSpace(turn.Text) == "" {
			continue
		}
		if turn.Speaker != SpeakerUser && turn.Speaker != SpeakerAI {
			return "", errors.New("classifier: unknown speaker")
		}
		turns = append(turns, turn)
	}
	if len(turns) > contextTurns {
		turns = turns[len(turns)-contextTurns:]
	}
	for _, turn := range turns {
		msg.Context = append(msg.Context, inputTurn{Speaker: turn.Speaker, Text: tail(turn.Text, maxContextRunes)})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// 한글과 부등호를 \u 꼴로 바꾸지 않는다. 모델이 근거를 글자 그대로 옮기려면 글이 읽히는 꼴이어야 한다.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		return "", errors.New("classifier: input could not be encoded")
	}
	return strings.TrimSpace(buf.String()), nil
}

// tail은 글이 길면 뒤쪽 limit 글자만 남긴다.
func tail(text string, limit int) string {
	count := utf8.RuneCountInString(text)
	if count <= limit {
		return text
	}
	skip := count - limit
	for i := range text {
		if skip == 0 {
			return "…" + text[i:]
		}
		skip--
	}
	return text
}
