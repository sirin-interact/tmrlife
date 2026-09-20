package crisis

import (
	"errors"
	"fmt"
)

// 입력이 틀렸을 때 돌려주는 오류다. errors.Is로 가려낸다.
// 이 패키지는 사용자의 말을 받지 않으므로 메시지에 담길 것도 없다. 자리와 고정된 설명만 담는다.
var (
	ErrInvalidStage      = errors.New("crisis: invalid stage")
	ErrInvalidDetectedBy = errors.New("crisis: invalid detected-by value")
	ErrInvalidAdjustment = errors.New("crisis: invalid adjustment")

	// ErrInconsistentAI는 AI 판별이 답하지 못했다고 하면서 단계는 채워져 있다는 뜻이다.
	// Answered를 빠뜨린 것일 수 있다. 그 단계를 조용히 버리면 잡아야 할 말을 놓치므로 오류로 알린다.
	ErrInconsistentAI = errors.New("crisis: ai result carries a stage but is not marked as answered")

	// ErrMissingNow는 판정 시각이 비어 있다는 뜻이다.
	// 빈 시각을 그대로 쓰면 지난 기록이 모두 "미래"가 되어 쌓임을 보는 규칙이 조용히 꺼진다.
	ErrMissingNow = errors.New("crisis: current instant is missing")

	// ErrMissingEventTime은 지난 판정 가운데 시각이 빈 것이 있다는 뜻이다.
	ErrMissingEventTime = errors.New("crisis: event instant is missing")

	// ErrMissingConversationID는 이번 대화나 지난 판정의 대화 식별자가 비어 있다는 뜻이다.
	// 비어 있는 식별자끼리 같은 대화로 읽으면 되풀이를 세는 규칙이 오류 없이 덜 세게 된다.
	ErrMissingConversationID = errors.New("crisis: conversation id is missing")

	// ErrInvalidParams는 조정 값이 규칙을 돌릴 수 없는 범위라는 뜻이다. 안에 *params.FieldError가 들어 있다.
	ErrInvalidParams = errors.New("crisis: invalid params")
)

// EventError는 지난 판정 가운데 어느 것이 왜 틀렸는지 알려준다.
type EventError struct {
	// Index는 넘겨받은 슬라이스에서의 자리다.
	Index int
	Err   error
}

func (e *EventError) Error() string {
	return fmt.Sprintf("crisis event %d: %v", e.Index, e.Err)
}

func (e *EventError) Unwrap() error { return e.Err }
