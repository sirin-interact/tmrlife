// Package reply는 대화 모델에게 다음 말을 받아 와서, 나가도 되는 말인지 검사한 뒤에 돌려준다.
//
// 모델의 글은 그대로 나가지 않는다. 한두 문장인지, 질문이 하나인지, 전화번호나 기관 안내를 끼워 넣지 않았는지,
// 허락을 구하거나 진단하는 말이 없는지, 다른 문자가 섞이지 않았는지를 Check로 본다.
// 걸리면 무엇이 걸렸는지 알려주고 한 번 다시 만들게 하고, 그래도 걸리면 미리 써 둔 말로 바꾼다.
// 그래서 Generate는 모델이 멈췄을 때도 언제나 나갈 수 있는 말을 돌려준다.
//
// 이 패키지는 위기 관문을 모른다. 어느 방식(Mode)으로 말할지는 관문의 판정을 아는 쪽이 정해서 넘기고,
// 여기서 받은 말을 언제 내보낼지도 그쪽이 정한다.
//
// 사용자의 말과 모델의 답은 로그에 남기지 않는다. Result와 Violation은 통째로 로그에 넘겨도 내용이 나가지 않는다.
package reply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
)

// Mode는 이번 답을 어떤 방식으로 말할지다.
type Mode string

const (
	// ModeNormal은 평소의 대화다.
	ModeNormal Mode = "normal"
	// ModeCheck는 여러 뜻으로 읽히는 말을 들었을 때의 첫 걸음이다. 사용자의 표현을 그대로 받아 무슨 일이 있었는지 묻는다.
	ModeCheck Mode = "check"
	// ModeCrisisFollow는 미리 써 둔 위기 응답이 나간 뒤의 대화다. 듣는 쪽에 머물고, 조언하지 않고, 번호를 다시 꺼내지 않는다.
	ModeCrisisFollow Mode = "crisis_follow"
)

// 방식마다 쓰는 지시문의 ID다.
const (
	TaskNormal       = "conversation"
	TaskCheck        = "conversation_check"
	TaskCrisisFollow = "conversation_crisis"
)

// Tasks는 이 패키지가 쓰는 지시문 ID다. 서버가 뜰 때 지시문이 모두 있는지 확인하는 데 쓴다.
func Tasks() []string {
	return []string{TaskNormal, TaskCheck, TaskCrisisFollow}
}

// Speaker는 말한 쪽이다. 값은 발화를 저장할 때 쓰는 값과 같다.
type Speaker string

const (
	SpeakerUser Speaker = "user"
	SpeakerAI   Speaker = "ai"
)

// Turn은 대화에서 오간 말 하나다.
type Turn struct {
	Speaker Speaker
	Text    string
}

// Input은 답을 만들 턴 하나다.
type Input struct {
	Mode Mode
	// Turns는 이 대화에서 오간 말이다. 오래된 것이 앞에 오고, 마지막은 지금 답할 사용자의 말이어야 한다.
	// 미리 써 둔 말(첫 안부, 위기 응답)도 AI의 말로 넣는다. 직전에 연달아 물었는지를 여기서 센다.
	Turns []Turn
	// UserWords는 ModeCheck에서 받아 되물을 사용자의 표현이다(관문이 근거로 고른 말).
	// 비워 두면 사용자의 마지막 말을 쓴다.
	UserWords string
}

// Origin은 나가는 말이 어디서 왔는지다. 값은 발화를 저장할 때 쓰는 값과 같다.
type Origin string

const (
	// OriginModel은 모델이 만들었고 출력 검사를 통과한 말이다.
	OriginModel Origin = "model"
	// OriginFixed는 미리 써 둔 말이다.
	OriginFixed Origin = "fixed"
	// OriginTemplate은 미리 써 둔 문형에 사용자의 표현을 넣은 말이다.
	OriginTemplate Origin = "template"
)

// Attempt는 모델을 한 번 부른 결과다.
type Attempt struct {
	// Model은 답한 모델이다. 호출이 실패했으면 비어 있다.
	Model string
	// Violations는 그 답이 걸린 출력 검사다. 비어 있고 Failure도 없으면 통과한 답이다.
	Violations []Violation
	// Failure는 호출이 실패했을 때의 종류다.
	Failure ai.Kind
	Usage   ai.Usage
}

// Result는 나갈 말이다.
type Result struct {
	// Text는 화면에 보이고 발화로 저장되는 글이다.
	Text string
	// Speech는 음성 합성에 넘기는 글이다. 미리 써 둔 말은 숫자가 한글로 풀려 있다. 모델의 말은 Text와 같다.
	Speech string
	Origin Origin
	// Phrase는 미리 써 둔 말이 나갔을 때 그 문구의 이름이다.
	Phrase phrases.ID
	// NoQuestion은 이번 턴에 질문을 막았는지다.
	NoQuestion bool
	// Attempts는 모델을 부른 기록이다. 많아야 둘이다.
	Attempts []Attempt
	// PromptTask와 PromptVersion은 어느 지시문의 어느 판으로 만들었는지다.
	PromptTask    string
	PromptVersion string
}

// Violations는 모든 시도에서 걸린 출력 검사를 시도 순서대로 모은 것이다.
func (r Result) Violations() []Violation {
	var out []Violation
	for _, a := range r.Attempts {
		out = append(out, a.Violations...)
	}
	return out
}

// LogValue는 말의 내용 대신 출처, 길이, 걸린 검사의 이름만 남긴다.
func (r Result) LogValue() slog.Value {
	attempts := make([]any, 0, len(r.Attempts))
	for i, a := range r.Attempts {
		rules := make([]string, 0, len(a.Violations))
		for _, v := range a.Violations {
			rules = append(rules, v.String())
		}
		attempts = append(attempts, slog.Group(fmt.Sprintf("attempt_%d", i+1),
			slog.String("model", a.Model),
			slog.String("failure", string(a.Failure)),
			slog.Any("violations", rules),
			slog.Int("output_tokens", a.Usage.OutputTokens),
			slog.Int("reasoning_tokens", a.Usage.ReasoningTokens),
		))
	}
	return slog.GroupValue(
		slog.String("origin", string(r.Origin)),
		slog.String("phrase", string(r.Phrase)),
		slog.Int("chars", utf8.RuneCountInString(r.Text)),
		slog.Bool("no_question", r.NoQuestion),
		slog.String("prompt_task", r.PromptTask),
		slog.String("prompt_version", r.PromptVersion),
		slog.Group("attempts", attempts...),
	)
}

// Prompts는 방식마다 쓰는 지시문이다.
type Prompts struct {
	Normal       prompts.Prompt
	Check        prompts.Prompt
	CrisisFollow prompts.Prompt
}

// PromptsFrom은 읽어 둔 지시문에서 이 패키지가 쓰는 셋을 꺼낸다.
func PromptsFrom(reg *prompts.Registry) (Prompts, error) {
	if reg == nil {
		return Prompts{}, errors.New("reply: prompt registry is required")
	}
	var p Prompts
	for _, slot := range []struct {
		task string
		dst  *prompts.Prompt
	}{{TaskNormal, &p.Normal}, {TaskCheck, &p.Check}, {TaskCrisisFollow, &p.CrisisFollow}} {
		got, err := reg.Get(slot.task)
		if err != nil {
			return Prompts{}, fmt.Errorf("reply: %w", err)
		}
		*slot.dst = got
	}
	return p, nil
}

func (p Prompts) forMode(mode Mode) (prompts.Prompt, bool) {
	switch mode {
	case ModeNormal:
		return p.Normal, true
	case ModeCheck:
		return p.Check, true
	case ModeCrisisFollow:
		return p.CrisisFollow, true
	default:
		return prompts.Prompt{}, false
	}
}

const (
	// DefaultMaxTurns는 모델에 보내는 지난 말의 수다. 저녁의 대화 하나는 대개 이 안에 든다.
	DefaultMaxTurns = 40
	// maxAttempts는 모델을 부르는 횟수의 상한이다. 한 번 만들고, 걸리면 한 번만 다시 만든다.
	maxAttempts = 2
)

// Options는 조정 값이다. 모두 비워 두어도 된다.
type Options struct {
	// MaxOutputTokens가 0이면 모델 쪽의 기본 한도를 쓴다.
	// 답은 두 문장이지만 보이지 않는 생각 토큰도 이 한도에서 빠지므로, 정한다면 넉넉하게 잡는다.
	MaxOutputTokens int
	// Thinking이 비어 있으면 모델 쪽의 기본 수준을 쓴다.
	Thinking string
	// Budget은 모델을 기다리는 시간의 상한이다. 두 번의 시도를 합친 시간이다. 넘기면 미리 써 둔 말로 바꾼다.
	// 0이면 부른 쪽의 컨텍스트만 따른다.
	Budget time.Duration
	// MaxTurns가 0이면 DefaultMaxTurns다.
	MaxTurns int
	// Limits의 빈 값은 DefaultLimits로 채운다.
	Limits Limits
}

// ErrInvalidInput은 부른 쪽이 잘못 부른 경우다. 모델의 실패가 아니어서 미리 써 둔 말로 덮지 않는다.
var ErrInvalidInput = errors.New("reply: invalid input")

// Generator는 답을 만든다. 여러 고루틴에서 함께 써도 된다.
type Generator struct {
	llm     ai.LLM
	prompts Prompts
	phrases *phrases.Catalogue
	opts    Options
}

// New는 Generator를 만든다. llm은 대화 모델이다. 늦은 응답에 대비한 예비 모델을 쓰려면 그것을 감싼 LLM을 넘긴다.
func New(llm ai.LLM, p Prompts, catalogue *phrases.Catalogue, opts Options) (*Generator, error) {
	switch {
	case llm == nil:
		return nil, errors.New("reply: llm is required")
	case catalogue == nil:
		return nil, errors.New("reply: phrase catalogue is required")
	case opts.MaxOutputTokens < 0 || opts.Budget < 0 || opts.MaxTurns < 0:
		return nil, errors.New("reply: options must not be negative")
	}
	for _, mode := range []Mode{ModeNormal, ModeCheck, ModeCrisisFollow} {
		if prompt, _ := p.forMode(mode); strings.TrimSpace(prompt.System) == "" || !ai.ValidTaskID(prompt.Task) {
			return nil, fmt.Errorf("reply: prompt for mode %s is missing", mode)
		}
	}
	if opts.MaxTurns == 0 {
		opts.MaxTurns = DefaultMaxTurns
	}
	opts.Limits = opts.Limits.withDefaults()
	return &Generator{llm: llm, prompts: p, phrases: catalogue, opts: opts}, nil
}

// Generate는 다음에 나갈 말을 돌려준다.
//
// 모델이 실패하거나 답이 출력 검사를 두 번 통과하지 못해도 오류가 아니다. 그때는 미리 써 둔 말을 돌려주고,
// 무슨 일이 있었는지는 Result.Attempts에 남긴다. 오류는 두 경우뿐이다. 잘못 부른 경우(ErrInvalidInput)와,
// 부른 쪽의 컨텍스트가 끝난 경우다(사용자가 끼어들었거나 연결이 끊긴 것이므로 아무 말도 나가면 안 된다).
func (g *Generator) Generate(ctx context.Context, in Input) (Result, error) {
	t, err := g.prepare(in)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("reply: %w", err)
	}

	callCtx := ctx
	if g.opts.Budget > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, g.opts.Budget)
		defer cancel()
	}

	result := Result{NoQuestion: t.noQuestion, PromptTask: t.prompt.Task, PromptVersion: t.prompt.Version}
	var rejected []Violation
	for len(result.Attempts) < maxAttempts {
		resp, err := g.llm.Generate(callCtx, g.request(t, rejected))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Result{}, fmt.Errorf("reply: %w", ctxErr)
			}
			failure := ai.Classify(err)
			result.Attempts = append(result.Attempts, Attempt{Failure: failure.Kind})
			// 시간을 다 썼으면 다시 불러도 같은 자리에서 끊긴다.
			if !failure.Retryable || callCtx.Err() != nil {
				break
			}
			continue
		}

		text := strings.TrimSpace(resp.Text)
		violations := Check(Draft{
			Mode:       in.Mode,
			Text:       text,
			NoQuestion: t.noQuestion,
			UserTexts:  t.userTexts,
			UserWords:  t.userWords,
		}, g.opts.Limits)
		result.Attempts = append(result.Attempts, Attempt{Model: resp.Model, Violations: violations, Usage: resp.Usage})
		if len(violations) == 0 {
			result.Text, result.Speech, result.Origin = text, text, OriginModel
			return result, nil
		}
		rejected = violations
	}

	if in.Mode == ModeCheck {
		// 되물어야 하는 턴을 "듣고 있어요"로 넘기면 무거울 수도 있는 말을 흘려보내는 셈이다. 사용자의 표현을 넣은 문형으로 되묻는다.
		g.fallback(&result, g.phrases.Reflect(t.userWords), OriginTemplate)
		return result, nil
	}
	g.fallback(&result, g.phrases.SafeReply(t.lastAIText), OriginFixed)
	return result, nil
}

func (g *Generator) fallback(result *Result, p phrases.Phrase, origin Origin) {
	result.Text, result.Speech, result.Origin, result.Phrase = p.Display, p.Speech, origin, p.ID
}

// turn은 요청과 검사에 함께 쓰는, 이번 턴에서 뽑아 둔 값이다.
type turn struct {
	prompt     prompts.Prompt
	messages   []ai.Message
	noQuestion bool
	userTexts  []string
	userWords  string
	lastAIText string
}

func (g *Generator) prepare(in Input) (turn, error) {
	prompt, ok := g.prompts.forMode(in.Mode)
	if !ok {
		return turn{}, fmt.Errorf("%w: unknown mode", ErrInvalidInput)
	}

	turns := make([]Turn, 0, len(in.Turns))
	for _, tn := range in.Turns {
		if tn.Speaker != SpeakerUser && tn.Speaker != SpeakerAI {
			return turn{}, fmt.Errorf("%w: unknown speaker", ErrInvalidInput)
		}
		// 빈 말은 모델이 받지 않는다. 저장된 대화에 섞여 있어도 턴 전체를 실패시킬 일은 아니다.
		if text := strings.TrimSpace(tn.Text); text != "" {
			turns = append(turns, Turn{Speaker: tn.Speaker, Text: text})
		}
	}
	if len(turns) == 0 || turns[len(turns)-1].Speaker != SpeakerUser {
		return turn{}, fmt.Errorf("%w: the last turn must be a non-empty user turn", ErrInvalidInput)
	}

	t := turn{prompt: prompt}
	var aiTexts []string
	for _, tn := range turns {
		if tn.Speaker == SpeakerUser {
			t.userTexts = append(t.userTexts, tn.Text)
		} else {
			aiTexts = append(aiTexts, tn.Text)
		}
	}
	if n := len(aiTexts); n > 0 {
		t.lastAIText = aiTexts[n-1]
	}
	// 되물어야 하는 턴은 직전에 몇 번을 물었든 물어야 한다. 무거울 수도 있는 말을 묻지 않고 넘기는 쪽이 더 나쁘다.
	t.noQuestion = in.Mode != ModeCheck && askedTwiceInARow(aiTexts)

	t.userWords = strings.TrimSpace(in.UserWords)
	if t.userWords == "" {
		t.userWords = turns[len(turns)-1].Text
	}

	if len(turns) > g.opts.MaxTurns {
		turns = turns[len(turns)-g.opts.MaxTurns:]
	}
	t.messages = toMessages(turns)
	return t, nil
}

// askedTwiceInARow는 직전 두 번의 AI 말이 모두 질문으로 끝났는지 본다.
// 질문이 연달아 이어지면 대화가 아니라 면담이 된다. 두 번 물었으면 다음은 묻지 않고 반응만 한다.
func askedTwiceInARow(aiTexts []string) bool {
	n := len(aiTexts)
	return n >= 2 && EndsWithQuestion(aiTexts[n-1]) && EndsWithQuestion(aiTexts[n-2])
}
