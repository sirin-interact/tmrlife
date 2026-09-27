package analysis

import "context"

// WorkAttempt는 큐가 작업을 attempt번째로 집어 실행한 것처럼 한 번 돌린다.
// 시도 번호에 따른 갈림길(다시 시도, 포기)을 큐의 다시 시도 간격을 기다리지 않고 보려는 것이다.
// 큐를 끝까지 거치는 길은 TestQueueRunsExtractJob이 따로 본다.
func (w *Worker) WorkAttempt(ctx context.Context, args ExtractArgs, number, maxAttempts int) error {
	return w.work(ctx, args, attempt{jobID: 1, number: number, max: maxAttempts})
}

// PromptLabels는 모델에 보내는 글의 뼈대가 되는 낱말이다. 지시문이 같은 낱말로 입력을 설명하는지 시험이 견준다.
func PromptLabels() []string {
	return []string{labelTranscript, labelAssistant}
}

// DropReasons는 근거를 받아들이지 못한 까닭의 고정된 이름이다.
func DropReasons() []string {
	return append([]string(nil), allDropReasons...)
}
