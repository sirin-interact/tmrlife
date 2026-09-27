// Package scripted는 공급자를 부르지 않고 정해진 규칙으로 답하는 언어 모델이다.
//
// 키가 없어도 서버가 끝에서 끝까지 돌게 하려고 둔다. 브라우저 흐름 테스트와 키 없이 돌려 보는 로컬 시연에 쓴다.
// 같은 요청에는 언제나 같은 답을 낸다. 상태를 갖지 않으므로 같은 요청이 동시에 두 번 와도 답이 같다.
//
// 실제 모델을 흉내 내는 것이 아니다. 대화의 답은 사용자의 말을 이해하지 않고 목록에서 고른 짧은 문장이고,
// 위기 판별은 낱말 몇 개를 찾는 표일 뿐 문맥도 관용 표현도 가리지 못한다. 운영에 쓰면 안 된다.
// 그래서 설정의 AI_PROVIDER가 scripted일 때만 만들어지고(설정은 운영 환경에서 그 값을 거부한다),
// 답에는 모델 이름으로 "scripted"가 찍혀 어디서 나온 글인지 저장된 기록에서도 가릴 수 있다.
//
// 시험에서 모델의 답, 지연, 실패를 하나하나 정하고 싶을 때는 이 패키지가 아니라 ai/fake를 쓴다.
package scripted

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/config"
)

// ModelName은 답에 찍히는 모델 이름이다.
const ModelName = "scripted"

// Behavior는 지시문 하나에 어떤 규칙으로 답할지다.
type Behavior int

const (
	// Conversation은 짧은 존댓말 답을 목록에서 차례로 고른다.
	Conversation Behavior = iota + 1
	// Reflect는 사용자의 마지막 말을 그대로 받아 무슨 일이 있었는지 하나만 묻는다.
	Reflect
	// CrisisFollow는 Conversation과 같되 대화를 닫는 말을 하지 않는 목록에서 고른다.
	CrisisFollow
	// Gate는 마지막 발화에서 낱말을 찾아 위기 단계를 JSON으로 답한다.
	Gate
	// Diary는 사용자의 발화를 이어 붙여 1인칭 글을 만든다.
	Diary
	// SignalExtract는 번호가 붙은 사용자의 줄에서 낱말을 찾아 여덟 항목의 판단과 근거를 JSON으로 답한다.
	SignalExtract
)

// DefaultTasks는 Options.Tasks를 비워 두었을 때 쓰는 표다. 서버의 지시문 ID에 맞춰 둔다.
// "diary_draft"처럼 표의 ID에 이름을 덧붙인 ID는 앞머리의 규칙을 따른다.
func DefaultTasks() map[string]Behavior {
	return map[string]Behavior{
		"conversation":        Conversation,
		"conversation_check":  Reflect,
		"conversation_crisis": CrisisFollow,
		"gate":                Gate,
		"diary":               Diary,
		"signal_extract":      SignalExtract,
	}
}

// DefaultNoQuestionHints는 Options.NoQuestionHints를 비워 두었을 때 쓰는 글귀다.
// 서버가 질문을 막는 턴에 지시문 끝에 덧붙이는 말, 그리고 물어서 버려진 답을 다시 만들게 할 때 덧붙이는 말에서 따왔다.
func DefaultNoQuestionHints() []string {
	return []string{"이번에는 묻지 않는다", "묻지 않아야 하는데 물었다"}
}

// DefaultUtteranceMarker는 위기 판별 요청에서 판정할 발화가 시작되는 자리를 알리는 표시다.
const DefaultUtteranceMarker = "마지막 발화:"

// Options는 서버의 지시문과 요청 모양에 이 패키지를 맞추는 값이다.
type Options struct {
	// Tasks는 지시문 ID마다 어떤 규칙으로 답할지다. 비워 두면 DefaultTasks다.
	//
	// 표에 없는 ID는 표의 ID 뒤에 '-'나 '_'로 이름을 덧붙인 꼴일 때 그 규칙을 따른다
	// ("diary_draft"는 "diary"의 규칙). 그래도 없으면 ai.ErrInvalidRequest다.
	// 모르는 일에 아무 글이나 돌려주면 시험이 엉뚱한 자리에서 깨진다.
	Tasks map[string]Behavior

	// NoQuestionHints는 "이번에는 질문으로 끝내지 말라"는 지시를 알아보는 글귀다. 비워 두면 DefaultNoQuestionHints다.
	// 지시문이나 마지막 메시지에 이 가운데 하나가 들어 있으면 질문 없는 답을 고른다.
	// 글귀가 하나도 맞지 않아도 직전 두 번의 AI 말이 모두 질문으로 끝났으면 질문 없는 답을 고른다.
	// 평소의 지시문에도 들어 있는 말을 글귀로 주면 언제나 질문 없는 답만 나온다.
	NoQuestionHints []string

	// UtteranceMarkers는 위기 판별 요청에서 판정할 발화 앞에 붙는 표시다. 비워 두면 DefaultUtteranceMarker다.
	// 마지막 메시지에 표시가 있으면 그 뒤만 판정하고, 앞부분은 문맥으로만 쓴다. 표시가 없으면 마지막 메시지 전체를 판정한다.
	UtteranceMarkers []string

	// Latency는 답이 나오기까지 기다리는 시간이다. 시연에서 "답을 준비하는 중" 표시가 보이게 할 때 쓴다.
	Latency time.Duration
}

// LLM은 ai.LLM과 ai.StreamLLM을 구현한다. 여러 고루틴에서 함께 불러도 된다.
type LLM struct {
	tasks            map[string]Behavior
	noQuestionHints  []string
	utteranceMarkers []string
	latency          time.Duration
}

var (
	_ ai.LLM       = (*LLM)(nil)
	_ ai.StreamLLM = (*LLM)(nil)
)

// New는 설정이 고른 공급자가 scripted일 때만 LLM을 만든다.
// 설정값을 받는 이유: 이 패키지를 쓰는 길을 AI_PROVIDER 하나로 묶어, 다른 조건으로 슬쩍 끼워 넣지 못하게 한다.
func New(provider config.AIProvider, opts Options) (*LLM, error) {
	if provider != config.AIProviderScripted {
		return nil, errors.New("scripted: only available when AI_PROVIDER is scripted")
	}
	if opts.Latency < 0 {
		return nil, errors.New("scripted: latency must not be negative")
	}

	tasks := opts.Tasks
	if len(tasks) == 0 {
		tasks = DefaultTasks()
	}
	l := &LLM{
		tasks:            make(map[string]Behavior, len(tasks)),
		noQuestionHints:  nonBlank(opts.NoQuestionHints),
		utteranceMarkers: nonBlank(opts.UtteranceMarkers),
		latency:          opts.Latency,
	}
	for task, behavior := range tasks {
		if !ai.ValidTaskID(task) {
			return nil, errors.New("scripted: task ids must use only lowercase letters, digits, '_' and '-'")
		}
		switch behavior {
		case Conversation, Reflect, CrisisFollow, Gate, Diary, SignalExtract:
		default:
			return nil, errors.New("scripted: unknown behavior")
		}
		l.tasks[task] = behavior
	}
	if len(l.noQuestionHints) == 0 {
		l.noQuestionHints = DefaultNoQuestionHints()
	}
	if len(l.utteranceMarkers) == 0 {
		l.utteranceMarkers = []string{DefaultUtteranceMarker}
	}
	return l, nil
}

func nonBlank(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func (l *LLM) Generate(ctx context.Context, req ai.Request) (ai.Response, error) {
	return l.run(ctx, req, nil)
}

func (l *LLM) GenerateStream(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc) (ai.Response, error) {
	if onDelta == nil {
		return ai.Response{}, ai.NewError(ai.ErrInvalidRequest, ai.Detail{Task: safeTask(req), Model: ModelName, Reason: "nil_delta_func"})
	}
	return l.run(ctx, req, onDelta)
}

func (l *LLM) run(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc) (ai.Response, error) {
	if err := req.Validate(); err != nil {
		return ai.Response{}, err
	}
	detail := ai.Detail{Task: req.Task, Model: ModelName}

	behavior, ok := l.behaviorFor(req.Task)
	if !ok {
		detail.Reason = "unknown_task"
		return ai.Response{}, ai.NewError(ai.ErrInvalidRequest, detail)
	}
	if err := wait(ctx, l.latency); err != nil {
		return ai.Response{}, ai.ContextError(err, detail)
	}

	var text string
	switch behavior {
	case Conversation:
		text = l.conversationReply(req, questionReplies, statementReplies)
	case Reflect:
		text = reflectReply(req)
	case CrisisFollow:
		text = l.conversationReply(req, crisisQuestionReplies, crisisStatementReplies)
	case Gate:
		text = l.gateReply(req)
	case Diary:
		text = diaryReply(req)
	case SignalExtract:
		text = signalReply(req)
	}

	if onDelta != nil {
		for _, piece := range pieces(text) {
			if err := ctx.Err(); err != nil {
				return ai.Response{}, ai.ContextError(err, detail)
			}
			if err := onDelta(piece); err != nil {
				return ai.Response{}, err
			}
		}
	}

	resp := ai.Response{Text: text, FinishReason: ai.FinishStop, Model: ModelName}
	// 실제 구현과 같은 기준으로 거른다. 스키마에 맞춘 JSON을 만들지 못했다면 여기서 드러난다.
	if err := ai.CheckResponse(req, resp, ""); err != nil {
		return ai.Response{}, err
	}
	return resp, nil
}

func (l *LLM) behaviorFor(task string) (Behavior, bool) {
	if b, ok := l.tasks[task]; ok {
		return b, true
	}
	// 가장 길게 맞는 앞머리를 따른다. "conversation"과 "conversation_crisis"가 함께 표에 있을 때를 위해서다.
	best, found := "", false
	for prefix := range l.tasks {
		if len(prefix) <= len(best) || len(task) <= len(prefix) || !strings.HasPrefix(task, prefix) {
			continue
		}
		if sep := task[len(prefix)]; sep == '-' || sep == '_' {
			best, found = prefix, true
		}
	}
	return l.tasks[best], found
}

// wait는 d만큼 기다리되 컨텍스트가 끝나면 바로 돌아온다.
func wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pieces는 글을 낱말 단위로 나눈다. 이으면 원래 글과 같다.
func pieces(text string) []string {
	var out []string
	for text != "" {
		i := strings.IndexByte(text, ' ')
		if i < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:i+1])
		text = text[i+1:]
	}
	return out
}

func safeTask(req ai.Request) string {
	if ai.ValidTaskID(req.Task) {
		return req.Task
	}
	return ""
}
