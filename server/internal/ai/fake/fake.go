// Package fake는 대본대로 답하는 가짜 언어 모델이다.
//
// 시험에서는 모델의 답, 지연, 실패를 정해 두고 부르는 쪽의 동작을 본다.
// 키 없이 앱을 띄울 때는 Handler로 요청마다 정해진 답을 돌려준다.
// 실제 구현과 같은 기준(ai.Request.Validate, ai.CheckResponse)으로 요청과 답을 거르므로,
// 빈 답이나 잘린 답 같은 공급자의 동작을 답의 모양만으로 흉내 낼 수 있다.
package fake

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// ErrNoScript는 대본이 바닥났고 Handler도 없는 경우다.
// ai의 실패 종류로 감싸지 않는다. 시험의 준비가 잘못된 것이지 모델의 실패가 아니어서,
// 부르는 쪽의 실패 대응에 묻히지 않고 드러나야 한다.
var ErrNoScript = errors.New("fake: no scripted step left and no handler set")

// Step은 호출 한 번에 대한 대본이다.
type Step struct {
	// Text는 답 전체다. 비워 두고 Chunks만 주면 조각을 이은 것이 답이 된다.
	Text string
	// Chunks는 스트리밍으로 넘길 조각이다. 비워 두면 Text를 한 조각으로 넘긴다.
	Chunks []string
	// FinishReason을 비워 두면 정상 종료다.
	FinishReason ai.FinishReason
	Usage        ai.Usage
	// Model을 비워 두면 가짜를 만들 때 준 이름을 쓴다.
	Model string
	// Err를 주면 호출이 이 오류로 끝난다. 스트리밍에서는 조각을 모두 넘긴 뒤에 실패한다.
	Err error
	// Latency는 답(스트리밍에서는 첫 조각)이 나오기까지의 시간이다.
	Latency time.Duration
	// ChunkInterval은 조각 사이의 간격이다.
	ChunkInterval time.Duration
}

func Reply(text string) Step { return Step{Text: text} }

func Fail(err error) Step { return Step{Err: err} }

// Handler는 대본이 비었을 때 요청을 보고 답을 정한다.
type Handler func(ctx context.Context, req ai.Request) Step

type Option func(*LLM)

// WithRecordLimit은 받은 요청을 가장 최근 n개만 남긴다. 0이면 아무것도 남기지 않는다.
// 요청에는 사용자의 말이 들어 있다. 시험이 아니라 앱에 붙여 오래 띄울 때는 한도를 둔다.
func WithRecordLimit(n int) Option {
	return func(f *LLM) {
		if n < 0 {
			n = 0
		}
		f.recordLimit = n
	}
}

const unlimitedRecords = -1

// LLM은 ai.LLM과 ai.StreamLLM을 구현한다. 여러 고루틴에서 함께 불러도 된다.
type LLM struct {
	model       string
	recordLimit int

	mu          sync.Mutex
	queue       []Step
	handler     Handler
	requests    []ai.Request
	calls       int
	interrupted int
}

var (
	_ ai.LLM       = (*LLM)(nil)
	_ ai.StreamLLM = (*LLM)(nil)
)

// New의 model은 답에 찍히는 모델 이름이다.
func New(model string, opts ...Option) *LLM {
	f := &LLM{model: model, recordLimit: unlimitedRecords}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Enqueue는 대본을 뒤에 잇는다. 호출마다 앞에서부터 하나씩 쓴다.
func (f *LLM) Enqueue(steps ...Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, steps...)
}

// SetHandler는 대본이 비었을 때 쓸 Handler를 정한다.
func (f *LLM) SetHandler(h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = h
}

// Requests는 받은 요청을 받은 순서대로 돌려준다. 돌려준 값을 고쳐도 기록은 바뀌지 않는다.
func (f *LLM) Requests() []ai.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ai.Request, len(f.requests))
	for i, r := range f.requests {
		out[i] = cloneRequest(r)
	}
	return out
}

func (f *LLM) LastRequest() (ai.Request, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return ai.Request{}, false
	}
	return cloneRequest(f.requests[len(f.requests)-1]), true
}

// Calls는 지금까지 불린 횟수다. 기록 한도와 상관없이 센다.
func (f *LLM) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Interrupted는 답을 다 내기 전에 컨텍스트가 끝나서(취소, 기한) 중단된 호출의 수다.
func (f *LLM) Interrupted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interrupted
}

// Pending은 아직 쓰지 않은 대본의 수다.
func (f *LLM) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}

func (f *LLM) Generate(ctx context.Context, req ai.Request) (ai.Response, error) {
	return f.run(ctx, req, nil)
}

func (f *LLM) GenerateStream(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc) (ai.Response, error) {
	if onDelta == nil {
		return ai.Response{}, ai.NewError(ai.ErrInvalidRequest, ai.Detail{Task: req.Task, Model: f.model, Reason: "nil_delta_func"})
	}
	return f.run(ctx, req, onDelta)
}

func (f *LLM) run(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc) (ai.Response, error) {
	// 잘못된 요청도 기록한다. 무엇을 보내려 했는지가 시험에서 확인하려는 것이기 때문이다.
	f.record(req)
	if err := req.Validate(); err != nil {
		return ai.Response{}, err
	}

	step, handler, scripted := f.next()
	if !scripted {
		if handler == nil {
			return ai.Response{}, ErrNoScript
		}
		step = handler(ctx, req)
	}

	model := step.Model
	if model == "" {
		model = f.model
	}
	detail := ai.Detail{Task: req.Task, Model: model}

	if err := f.wait(ctx, step.Latency, detail); err != nil {
		return ai.Response{}, err
	}

	chunks := step.Chunks
	text := step.Text
	switch {
	case text == "":
		text = strings.Join(chunks, "")
	case len(chunks) == 0:
		chunks = []string{text}
	}

	if onDelta != nil {
		for i, chunk := range chunks {
			if i > 0 {
				if err := f.wait(ctx, step.ChunkInterval, detail); err != nil {
					return ai.Response{}, err
				}
			}
			if chunk == "" {
				continue
			}
			if err := onDelta(chunk); err != nil {
				return ai.Response{}, err
			}
		}
	}

	if step.Err != nil {
		return ai.Response{}, step.Err
	}

	resp := ai.Response{Text: text, FinishReason: step.FinishReason, Usage: step.Usage, Model: model}
	if resp.FinishReason == "" {
		resp.FinishReason = ai.FinishStop
	}
	if err := ai.CheckResponse(req, resp, ""); err != nil {
		return ai.Response{}, err
	}
	return resp, nil
}

func (f *LLM) record(req ai.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.recordLimit == 0 {
		return
	}
	f.requests = append(f.requests, cloneRequest(req))
	if f.recordLimit > 0 && len(f.requests) > f.recordLimit {
		f.requests = append([]ai.Request(nil), f.requests[len(f.requests)-f.recordLimit:]...)
	}
}

// next는 이번 호출의 대본을 꺼낸다. 대본이 비었으면 Handler를 돌려준다.
func (f *LLM) next() (step Step, handler Handler, scripted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.queue) == 0 {
		return Step{}, f.handler, false
	}
	step = f.queue[0]
	f.queue = f.queue[1:]
	return step, nil, true
}

// wait는 d만큼 기다리되 컨텍스트가 끝나면 바로 돌아온다.
func (f *LLM) wait(ctx context.Context, d time.Duration, detail ai.Detail) error {
	if d <= 0 {
		if err := ctx.Err(); err != nil {
			return f.interrupt(err, detail)
		}
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return f.interrupt(ctx.Err(), detail)
	}
}

func (f *LLM) interrupt(err error, detail ai.Detail) error {
	f.mu.Lock()
	f.interrupted++
	f.mu.Unlock()
	return ai.ContextError(err, detail)
}

func cloneRequest(r ai.Request) ai.Request {
	out := r
	if r.Messages != nil {
		out.Messages = append([]ai.Message(nil), r.Messages...)
	}
	if r.JSONSchema != nil {
		out.JSONSchema = append([]byte(nil), r.JSONSchema...)
	}
	return out
}
