// Package ai는 언어 모델을 부르는 쪽과 공급자 사이의 약속이다.
//
// 공급자마다 다른 것은 구현 안에 가두고, 부르는 쪽은 Request를 주고 Response나 실패 종류 하나를 받는다.
// 모델은 구현을 만들 때 정한다. 대화, 위기 판별, 대화 뒤 분석처럼 일마다 모델이 다르므로
// 일마다 LLM을 하나씩 만들어 쓴다.
//
// Request와 Response에는 사용자의 말이 들어 있다. 이 패키지가 만드는 오류에는 그 내용을 넣지 않는다.
// 오류에는 지시문 ID, 모델 이름, 공급자가 준 사유 코드, 토큰 수만 담는다.
package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleModel Role = "model"
)

type Message struct {
	Role Role
	Text string
}

// 생각하기 수준이다. 모델마다 받는 수준이 달라서, 받지 않는 수준을 어떻게 할지는 구현이 정한다.
const (
	ThinkingMinimal = "minimal"
	ThinkingLow     = "low"
	ThinkingMedium  = "medium"
	ThinkingHigh    = "high"
)

type Request struct {
	// Task는 지시문 ID다. 오류와 로그에서 어느 일의 호출인지 가리는 데 쓴다.
	Task string
	// System은 지시문 본문이다.
	System   string
	Messages []Message
	// MaxOutputTokens가 0이면 공급자의 기본 한도를 쓴다.
	// 겉으로 보이지 않는 생각 토큰도 이 한도에서 빠지므로, 답의 길이보다 넉넉하게 잡아야 한다.
	MaxOutputTokens int
	// Thinking이 비어 있으면 모델의 기본 수준을 쓴다.
	Thinking string
	// JSONSchema를 주면 답은 이 스키마를 따르는 JSON이어야 한다.
	JSONSchema json.RawMessage
}

type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishMaxTokens FinishReason = "max_tokens"
	// FinishSafety는 만들던 답이 공급자의 안전 필터에 걸려 끊긴 경우다.
	FinishSafety FinishReason = "safety"
	FinishOther  FinishReason = "other"
)

// Usage의 ReasoningTokens는 답에 보이지 않는 생각 토큰이다. 출력 한도는 이 값과 OutputTokens가 함께 쓴다.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

// Response는 쓸 수 있는 답이다. 오류 없이 돌아온 Response는 CheckResponse를 통과한 것이다.
type Response struct {
	Text         string
	FinishReason FinishReason
	Usage        Usage
	// Model은 실제로 답한 모델이다. 예비 모델이 답했는지는 이 값으로 안다.
	Model string
}

// LLM은 요청 하나에 답 하나를 돌려준다.
//
// 구현이 지킬 것:
//   - 실패는 이 패키지의 실패 종류(ErrBlocked 등)로 감싸 돌려준다. 쓸 수 없는 답은 CheckResponse로 걸러낸다.
//   - ctx가 끝나면 바로 돌아오고, 그 오류는 ContextError로 만든다.
//   - req를 고치지 않는다. 같은 요청이 두 모델에 동시에 갈 수 있다.
//   - 오류에 요청이나 답의 내용을 넣지 않는다.
type LLM interface {
	Generate(ctx context.Context, req Request) (Response, error)
}

// DeltaFunc는 답의 조각을 순서대로 받는다. 오류를 돌려주면 생성을 멈춘다.
type DeltaFunc func(delta string) error

// StreamLLM은 답을 만들어지는 대로 조각내어 넘긴다.
//
// onDelta는 한 번에 하나씩, GenerateStream이 돌아오기 전에만 불린다.
// 돌려주는 Response.Text는 넘긴 조각을 모두 이은 것이다.
// 조각을 이미 넘긴 뒤에도 실패할 수 있다(답이 중간에 잘린 경우 등). 그때 받은 조각은 부른 쪽이 버린다.
type StreamLLM interface {
	GenerateStream(ctx context.Context, req Request, onDelta DeltaFunc) (Response, error)
}

var taskIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidTaskID는 지시문 ID로 쓸 수 있는 이름인지 본다.
// 디렉터리 이름이 되고 오류 문구와 로그에도 그대로 들어가므로, 소문자와 숫자, '_', '-'만 받는다.
func ValidTaskID(id string) bool {
	return taskIDPattern.MatchString(id)
}

// Validate는 어느 공급자에서도 받아들여지지 않을 요청을 보내기 전에 걸러낸다.
func (r Request) Validate() error {
	invalid := func(reason string) error {
		task := r.Task
		if !ValidTaskID(task) {
			task = ""
		}
		return NewError(ErrInvalidRequest, Detail{Task: task, Reason: reason})
	}

	if !ValidTaskID(r.Task) {
		return invalid("bad_task_id")
	}
	if len(r.Messages) == 0 {
		return invalid("no_messages")
	}
	for _, m := range r.Messages {
		if m.Role != RoleUser && m.Role != RoleModel {
			return invalid("bad_role")
		}
		if strings.TrimSpace(m.Text) == "" {
			return invalid("blank_message")
		}
	}
	if r.MaxOutputTokens < 0 {
		return invalid("negative_max_output_tokens")
	}
	switch r.Thinking {
	case "", ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh:
	default:
		return invalid("bad_thinking_level")
	}
	if r.JSONSchema != nil && !json.Valid(r.JSONSchema) {
		return invalid("bad_json_schema")
	}
	return nil
}

// CheckResponse는 공급자가 오류 없이 돌려준 답이 실제로 쓸 수 있는 답인지 본다.
// 정상 응답의 모양을 하고도 쓸 수 없는 경우가 있어서, 모든 구현이 같은 기준으로 거르도록 여기에 둔다.
//
// 막힌 요청(ErrBlocked)과 후보 없음(ErrNoCandidate)은 Response가 만들어지기 전의 일이라 구현이 직접 돌려준다.
// providerReason은 공급자가 준 종료 사유 코드이고, 오류의 설명에만 쓰인다.
func CheckResponse(req Request, resp Response, providerReason string) error {
	detail := Detail{Task: req.Task, Model: resp.Model, Reason: providerReason, Usage: resp.Usage}
	if detail.Reason == "" {
		detail.Reason = string(resp.FinishReason)
	}

	switch {
	case resp.FinishReason == FinishMaxTokens:
		// 생각 토큰이 한도를 먼저 써 버리면 답이 비거나 문장 중간에서 끊긴다. 어느 쪽이든 쓸 수 없다.
		return NewError(ErrTruncated, detail)
	case resp.FinishReason != FinishStop:
		return NewError(ErrAbnormalFinish, detail)
	case strings.TrimSpace(resp.Text) == "":
		return NewError(ErrEmpty, detail)
	case req.JSONSchema != nil && !json.Valid([]byte(resp.Text)):
		return NewError(ErrInvalidJSON, detail)
	}
	return nil
}

// DecodeJSON은 JSON으로 받은 답을 dst에 푼다.
// encoding/json의 오류에는 답의 글자가 섞여 나올 수 있어서, 원래 오류를 감싸지 않고 종류만 알린다.
func DecodeJSON(req Request, resp Response, dst any) error {
	if err := json.Unmarshal([]byte(resp.Text), dst); err != nil {
		return NewError(ErrInvalidJSON, Detail{Task: req.Task, Model: resp.Model, Reason: "decode_failed", Usage: resp.Usage})
	}
	return nil
}

// LogValue는 요청을 통째로 로그에 넘겨도 사용자의 말이 나가지 않게 한다.
func (r Request) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("task", safeToken(r.Task)),
		slog.Int("system_chars", utf8.RuneCountInString(r.System)),
		slog.Int("message_count", len(r.Messages)),
		slog.Int("max_output_tokens", r.MaxOutputTokens),
		slog.String("thinking", safeToken(r.Thinking)),
		slog.Bool("json", r.JSONSchema != nil),
	)
}

// LogValue는 말의 내용 대신 역할과 길이만 남긴다.
func (m Message) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("role", safeToken(string(m.Role))),
		slog.Int("chars", utf8.RuneCountInString(m.Text)),
	)
}

// LogValue는 답의 내용 대신 길이와 토큰 수만 남긴다.
func (r Response) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", safeToken(r.Model)),
		slog.String("finish_reason", safeToken(string(r.FinishReason))),
		slog.Int("chars", utf8.RuneCountInString(r.Text)),
		slog.Int("input_tokens", r.Usage.InputTokens),
		slog.Int("output_tokens", r.Usage.OutputTokens),
		slog.Int("reasoning_tokens", r.Usage.ReasoningTokens),
	)
}
