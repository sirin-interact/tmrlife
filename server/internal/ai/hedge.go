package ai

import (
	"context"
	"errors"
	"time"
)

// Hedged는 주 모델이 늦거나 실패하면 같은 요청을 예비 모델에도 보내고, 먼저 성공한 답을 쓴다.
//
// 모델의 응답 시간은 대부분 짧지만 가끔 몇 배로 늘어진다. 그 드문 경우를 기다리는 대신
// 정해 둔 시간이 지나면 두 번째 요청을 함께 띄워, 늦은 쪽이 아니라 빠른 쪽이 응답 시간을 정하게 한다.
type Hedged struct {
	primary  LLM
	fallback LLM
	delay    time.Duration
}

var _ LLM = (*Hedged)(nil)

// NewHedged의 delay는 주 모델의 답을 혼자 기다리는 시간이다.
//
// 재는 것은 답 전체가 돌아올 때까지의 시간이다. 첫 글자가 나올 때까지의 시간이 아니다.
// 그래서 설정의 LLM.FallbackAfter를 여기에 넘기면 안 된다. 그 값은 실시간 대화에서 첫 글자를 기다리는 시간(기본 3초)인데,
// 글자를 흘려 받지 않는 생성은 멀쩡할 때도 그만큼은 걸린다. 그대로 넘기면 거의 매번 예비 모델이 함께 떠서
// 비용이 두 배가 되고, 어느 모델의 답이 쓰일지가 요청마다 달라진다.
// 실시간 대화에는 글자를 흘려 받는 쪽(StreamLLM)에서 첫 글자를 기준으로 예비 모델을 띄우는 구현이 따로 있어야 한다. 아직 없다.
// 이 타입은 답 전체를 한 번에 받는 작업에 쓰고, delay는 그 작업의 평소 응답 시간보다 넉넉히 길게 잡는다.
func NewHedged(primary, fallback LLM, delay time.Duration) (*Hedged, error) {
	if primary == nil || fallback == nil {
		return nil, errors.New("ai: hedged llm needs both a primary and a fallback")
	}
	if delay <= 0 {
		return nil, errors.New("ai: hedge delay must be greater than zero")
	}
	return &Hedged{primary: primary, fallback: fallback, delay: delay}, nil
}

type hedgeSide int

const (
	sidePrimary hedgeSide = iota
	sideFallback
)

type hedgeResult struct {
	side     hedgeSide
	resp     Response
	err      error
	panicked any
}

// Generate는 예비 모델을 두 경우에 띄운다. delay가 지나도록 주 모델의 답이 없을 때와,
// 주 모델이 다시 부를 만한 실패로 일찍 끝났을 때다. 다시 불러도 소용없는 실패는 그대로 돌려준다.
// 예비 모델이 이미 떠 있으면 한쪽이 실패해도 다른 쪽을 끝까지 기다린다.
// 돌아갈 때 아직 돌고 있는 쪽은 컨텍스트로 취소한다.
func (h *Hedged) Generate(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, ContextError(err, Detail{Task: req.Task})
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 진 쪽은 아무도 받지 않는 결과를 남기고 끝난다. 그 고루틴이 막히지 않도록 자리를 둘 다 마련해 둔다.
	results := make(chan hedgeResult, 2)
	launch := func(side hedgeSide, llm LLM) {
		go func() {
			result := hedgeResult{side: side}
			defer func() {
				// 구현 안에서 난 패닉을 여기서 죽게 두면 프로세스 전체가 내려간다.
				// 부른 쪽의 고루틴으로 옮겨, 나란히 띄우지 않았을 때와 같은 자리에서 다뤄지게 한다.
				if r := recover(); r != nil {
					result.panicked = r
				}
				results <- result
			}()
			result.resp, result.err = llm.Generate(attemptCtx, req)
		}()
	}

	launch(sidePrimary, h.primary)
	running := 1

	timer := time.NewTimer(h.delay)
	defer timer.Stop()

	fallbackStarted := false
	startFallback := func() {
		if fallbackStarted {
			return
		}
		fallbackStarted = true
		timer.Stop()
		launch(sideFallback, h.fallback)
		running++
	}

	var failures [2]error
	for {
		select {
		case <-ctx.Done():
			return Response{}, ContextError(ctx.Err(), Detail{Task: req.Task})

		case <-timer.C:
			startFallback()

		case result := <-results:
			running--
			if result.panicked != nil {
				panic(result.panicked)
			}
			if result.err == nil {
				return result.resp, nil
			}
			// 부른 쪽이 그만둔 것이라면 시도가 남긴 오류보다 그 사실이 먼저다.
			if err := ctx.Err(); err != nil {
				return Response{}, ContextError(err, Detail{Task: req.Task})
			}

			failures[result.side] = result.err
			switch {
			case !fallbackStarted && Classify(result.err).Retryable:
				startFallback()
			case !fallbackStarted:
				return Response{}, result.err
			case running == 0:
				return Response{}, &HedgeError{Primary: failures[sidePrimary], Fallback: failures[sideFallback]}
			}
		}
	}
}
