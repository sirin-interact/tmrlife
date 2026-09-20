package engine

import (
	"context"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/reply"
)

// pendingReply는 관문이 끝나기 전에 미리 만들기 시작한 답이다.
//
// 만든 답은 여기 머물고, 단계가 정해지기 전에는 밖으로 나가지 않는다.
// 대응 단계 이상이면 close가 돌고 있는 호출을 멈추고 만들어진 답을 버린다.
type pendingReply struct {
	cancel context.CancelFunc
	// done은 만들기가 끝나면 닫힌다. 미리 만들지 않은 턴에서는 처음부터 닫혀 있다.
	done chan struct{}
	mode reply.Mode
	// started가 거짓이면 대화 모델을 부르지 않은 턴이다.
	started bool

	result reply.Result
	err    error
}

// startReply는 답 만들기를 띄운다. buffered가 거짓이면 모델을 부르지 않는다.
//
// 돌려받은 값은 반드시 close로 닫는다. 닫아야 돌던 고루틴이 멈추고, 멈춘 것을 확인하고 돌아간다.
func (e *Engine) startReply(ctx context.Context, buffered bool, in reply.Input) *pendingReply {
	p := &pendingReply{done: make(chan struct{}), mode: in.Mode, started: buffered}
	if !buffered {
		close(p.done)
		p.cancel = func() {}
		return p
	}

	callCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go func() {
		defer close(p.done)
		p.result, p.err = e.reply.Generate(callCtx, in)
	}()
	return p
}

// take는 미리 만든 답이 이번에 쓸 방식으로 만들어진 것이면 그것을 돌려준다.
// 방식이 다르거나 미리 만들지 않았으면 두 번째 값이 거짓이다. 그때 부르는 쪽은 새로 만든다.
func (p *pendingReply) take(ctx context.Context, mode reply.Mode) (reply.Result, bool, error) {
	if !p.started || p.mode != mode {
		return reply.Result{}, false, nil
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return reply.Result{}, false, fmt.Errorf("engine: %w", ctx.Err())
	}
	if p.err != nil {
		return reply.Result{}, false, fmt.Errorf("engine: generate reply: %w", p.err)
	}
	return p.result, true, nil
}

// close는 돌고 있는 호출을 멈추고 끝날 때까지 기다린다. 여러 번 불러도 된다.
//
// 기다리는 이유: 취소한 호출의 고루틴이 남아 있으면 턴이 끝난 뒤에도 모델 응답을 붙들고 있게 된다.
// 취소를 받은 구현은 바로 돌아오므로 기다리는 시간은 없다시피 하고, 미리 써 둔 말은 이미 나간 뒤다.
func (p *pendingReply) close() {
	p.cancel()
	<-p.done
}
