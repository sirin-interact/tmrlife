package diary

import (
	"context"

	"github.com/sirin-interact/tmrlife/server/internal/logging"
)

// WorkAttempt는 큐가 작업을 attempt번째로 집어 실행한 것처럼 한 번 돌린다.
// 시도 번호에 따른 갈림길(다시 시도, 포기, 미루기)을 큐의 다시 시도 간격을 기다리지 않고 보려는 것이다.
// 큐를 끝까지 거치는 길은 TestQueueRunsDraftJob이 따로 본다.
func (w *Worker) WorkAttempt(ctx context.Context, args DraftArgs, number, maxAttempts int) error {
	return w.work(ctx, args, attempt{jobID: 1, number: number, max: maxAttempts})
}

// PromptLabels는 모델에 보내는 글의 뼈대가 되는 낱말이다. 지시문이 같은 낱말로 입력을 설명하는지 시험이 견준다.
func PromptLabels() []string {
	return []string{labelModeFirst, labelModeAppend, "방식", "사용자가 한 말"}
}

// JoinEntry는 이미 있는 글 뒤에 새 단락을 붙이는 규칙을 시험에 내준다.
func JoinEntry(base, entry string) string {
	return joinEntry(logging.Redacted(base), logging.Redacted(entry))
}
