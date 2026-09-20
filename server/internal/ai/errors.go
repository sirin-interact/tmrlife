package ai

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// 실패 종류다. errors.Is로 가린다.
//
// 다시 부를 만한지는 실패가 어디서 났는지로 갈린다. 입력 때문에 거절된 요청은 다시 보내도 같은 답이 온다.
// 출력은 같은 입력에도 부를 때마다 달라지므로(temperature를 0으로 두어도 그렇다),
// 출력 쪽에서 난 실패는 다시 부르거나 다른 모델에 보내면 풀릴 수 있다.
var (
	// ErrBlocked는 공급자가 요청 자체를 거절한 경우다.
	ErrBlocked = errors.New("ai: request blocked")
	// ErrNoCandidate는 거절 사유도 없이 답이 하나도 오지 않은 경우다.
	ErrNoCandidate = errors.New("ai: no candidate")
	// ErrAbnormalFinish는 답이 정상 종료가 아닌 사유로 끝난 경우다(안전 필터, 인용 제한 등).
	ErrAbnormalFinish = errors.New("ai: abnormal finish")
	// ErrEmpty는 정상 종료인데 글이 빈 경우다.
	ErrEmpty = errors.New("ai: empty response")
	// ErrTruncated는 출력 한도에 걸려 답이 잘린 경우다.
	// 생각 토큰의 양은 부를 때마다 달라서 다시 부르면 들어맞을 수 있다. 되풀이되면 한도를 올려야 한다.
	ErrTruncated = errors.New("ai: response truncated")
	// ErrTimeout은 정해진 시간 안에 답이 오지 않은 경우다.
	ErrTimeout = errors.New("ai: timeout")
	// ErrProvider는 공급자까지 가지 못했거나 공급자가 잠시 받지 못하는 경우다(연결 실패, 5xx, 429).
	ErrProvider = errors.New("ai: provider error")
	// ErrInvalidRequest는 요청이 잘못되어 몇 번을 보내도 받아들여지지 않는 경우다(잘못된 요청, 인증 실패, 없는 모델).
	ErrInvalidRequest = errors.New("ai: invalid request")
	// ErrInvalidJSON은 JSON으로 답하라고 했는데 JSON이 아니거나 약속한 모양으로 풀리지 않는 답이 온 경우다.
	ErrInvalidJSON = errors.New("ai: invalid json response")
)

// Kind는 실패 종류의 고정된 이름이다. 로그와 지표에 그대로 쓸 수 있다.
type Kind string

const (
	KindNone           Kind = ""
	KindBlocked        Kind = "blocked"
	KindNoCandidate    Kind = "no_candidate"
	KindAbnormalFinish Kind = "abnormal_finish"
	KindEmpty          Kind = "empty"
	KindTruncated      Kind = "truncated"
	KindTimeout        Kind = "timeout"
	KindProvider       Kind = "provider"
	KindInvalidRequest Kind = "invalid_request"
	KindInvalidJSON    Kind = "invalid_json"
	// KindCanceled는 부른 쪽이 그만둔 경우다. 사용자가 끼어들어 그 턴을 취소했을 때처럼 실패가 아닌 경우가 많다.
	KindCanceled Kind = "canceled"
	KindUnknown  Kind = "unknown"
)

type Failure struct {
	Kind Kind
	// Retryable은 같은 요청을 다시 보내거나 다른 모델에 보내 볼 만한지다.
	Retryable bool
}

var failureBySentinel = []struct {
	sentinel error
	failure  Failure
}{
	{ErrBlocked, Failure{Kind: KindBlocked}},
	{ErrInvalidRequest, Failure{Kind: KindInvalidRequest}},
	{ErrNoCandidate, Failure{Kind: KindNoCandidate, Retryable: true}},
	{ErrAbnormalFinish, Failure{Kind: KindAbnormalFinish, Retryable: true}},
	{ErrEmpty, Failure{Kind: KindEmpty, Retryable: true}},
	{ErrTruncated, Failure{Kind: KindTruncated, Retryable: true}},
	{ErrInvalidJSON, Failure{Kind: KindInvalidJSON, Retryable: true}},
	{ErrTimeout, Failure{Kind: KindTimeout, Retryable: true}},
	{ErrProvider, Failure{Kind: KindProvider, Retryable: true}},
}

// Classify는 오류가 어떤 실패이고 다시 부를 만한지 알려준다.
// 이 패키지의 실패 종류가 아닌 오류는 무엇인지 모르므로 다시 부르지 않는 쪽으로 본다.
func Classify(err error) Failure {
	if err == nil {
		return Failure{}
	}

	var hedge *HedgeError
	if errors.As(err, &hedge) {
		// 주 모델의 실패가 고쳐야 할 쪽이라 종류는 그쪽을 따른다.
		// 어느 한쪽이라도 일시적인 실패였다면 다시 불렀을 때 그 길로 풀릴 수 있다.
		primary, fallback := Classify(hedge.Primary), Classify(hedge.Fallback)
		return Failure{Kind: primary.Kind, Retryable: primary.Retryable || fallback.Retryable}
	}

	if errors.Is(err, context.Canceled) {
		return Failure{Kind: KindCanceled}
	}
	for _, c := range failureBySentinel {
		if errors.Is(err, c.sentinel) {
			return c.failure
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Failure{Kind: KindTimeout, Retryable: true}
	}
	return Failure{Kind: KindUnknown}
}

// Detail은 실패에 붙는 설명이다. 요청이나 답의 내용은 담지 않는다.
type Detail struct {
	// Task는 지시문 ID다.
	Task  string
	Model string
	// Reason은 공급자가 준 사유 코드다(예: MAX_TOKENS, SAFETY). 사람이 읽는 설명 문장은 넣지 않는다.
	Reason string
	// Status는 HTTP 상태 코드다. 없으면 0이다.
	Status int
	Usage  Usage
}

// Error는 실패 종류에 Detail을 붙인 오류다.
type Error struct {
	// Kind는 이 패키지의 실패 종류 가운데 하나이거나 context.Canceled다.
	Kind   error
	Detail Detail
	// deadline은 ctx의 기한이 지나서 난 시간 초과인지다.
	deadline bool
}

func NewError(kind error, detail Detail) *Error {
	return &Error{Kind: kind, Detail: detail}
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(kindMessage(e.Kind))

	sep := " ("
	field := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(sep + name + "=" + value)
		sep = " "
	}
	count := func(name string, n int) {
		if n != 0 {
			field(name, strconv.Itoa(n))
		}
	}

	field("task", safeToken(e.Detail.Task))
	field("model", safeToken(e.Detail.Model))
	field("reason", safeToken(e.Detail.Reason))
	count("status", e.Detail.Status)
	count("input_tokens", e.Detail.Usage.InputTokens)
	count("output_tokens", e.Detail.Usage.OutputTokens)
	count("reasoning_tokens", e.Detail.Usage.ReasoningTokens)
	if sep == " " {
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap은 실패 종류를 드러낸다. ctx의 기한 때문에 난 시간 초과는 context.DeadlineExceeded로도 잡힌다.
func (e *Error) Unwrap() []error {
	if e.Kind == nil {
		return nil
	}
	if e.deadline {
		return []error{e.Kind, context.DeadlineExceeded}
	}
	return []error{e.Kind}
}

// ContextError는 ctx가 끝나서 난 오류를 실패 종류로 바꾼다.
// 기한이 지난 것은 ErrTimeout이 되고, 부른 쪽이 취소한 것은 context.Canceled로 남는다.
// ctx와 상관없는 오류에는 nil을 돌려준다.
func ContextError(err error, detail Detail) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Kind: ErrTimeout, Detail: detail, deadline: true}
	case errors.Is(err, context.Canceled):
		return &Error{Kind: context.Canceled, Detail: detail}
	default:
		return nil
	}
}

// StatusError는 공급자가 돌려준 HTTP 오류 상태를 실패 종류로 바꾼다.
func StatusError(status int, detail Detail) error {
	detail.Status = status
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusGatewayTimeout:
		return NewError(ErrTimeout, detail)
	case status == http.StatusTooManyRequests, status >= 500:
		return NewError(ErrProvider, detail)
	default:
		// 나머지 4xx는 요청, 키, 모델 이름이 틀린 것이다. 다시 보내도 같은 답이 온다.
		return NewError(ErrInvalidRequest, detail)
	}
}

// HedgeError는 주 모델과 예비 모델이 모두 실패한 경우다. errors.Is는 두 쪽의 실패 종류를 모두 본다.
type HedgeError struct {
	Primary  error
	Fallback error
}

func (e *HedgeError) Error() string {
	return "ai: primary and fallback both failed: primary: " + safeMessage(e.Primary) + "; fallback: " + safeMessage(e.Fallback)
}

func (e *HedgeError) Unwrap() []error {
	return []error{e.Primary, e.Fallback}
}

// kindMessage는 실패 종류의 고정 문구만 돌려준다. 알 수 없는 오류가 Kind로 들어와도 그 문구를 옮기지 않는다.
func kindMessage(kind error) string {
	if kind == nil {
		return "ai: error"
	}
	if errors.Is(kind, context.Canceled) {
		return "ai: canceled"
	}
	for _, c := range failureBySentinel {
		if errors.Is(kind, c.sentinel) {
			return c.sentinel.Error()
		}
	}
	if errors.Is(kind, context.DeadlineExceeded) {
		return ErrTimeout.Error()
	}
	return "ai: error"
}

// safeMessage는 이 패키지가 만든 문구만 옮긴다.
// 구현이 약속을 어기고 다른 오류를 그대로 돌려줬을 때 그 문구가 섞여 나가지 않게 한다.
func safeMessage(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Error()
	}
	return kindMessage(err)
}

const maxTokenLength = 64

// safeToken은 오류 문구와 로그에 넣어도 되는 꼴의 값만 통과시킨다.
// 모델 이름이나 사유 코드 자리에 실수로 문장이 들어와도 밖으로 나가지 않게 하는 안전망이다.
func safeToken(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > maxTokenLength {
		return "invalid"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.', r == ':', r == '/':
		default:
			return "invalid"
		}
	}
	return s
}
