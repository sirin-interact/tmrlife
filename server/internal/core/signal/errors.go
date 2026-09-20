package signal

import (
	"errors"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// 입력이 틀렸을 때 돌려주는 오류다. errors.Is로 가려낸다.
// 메시지에는 입력받은 글자를 담지 않는다. 위치와 날짜, 고정된 설명만 담는다.
var (
	ErrInvalidItem           = errors.New("signal: invalid item")
	ErrInvalidStatus         = errors.New("signal: invalid status")
	ErrInvalidExplicitness   = errors.New("signal: invalid explicitness")
	ErrInconsistentJudgement = errors.New("signal: status and explicitness do not match")

	// ErrNoRows는 신호 행이 하나도 없는 날을 하루로 합치려 했다는 뜻이다.
	// 그런 날은 대화하지 않은 날과 똑같이 입력에서 빠져야 한다.
	ErrNoRows = errors.New("signal: no rows for the day")

	ErrZeroDate      = errors.New("signal: record date is missing")
	ErrDuplicateDate = errors.New("signal: record date appears more than once")
	ErrUnsortedDays  = errors.New("signal: days are not in ascending date order")
)

// RowError는 MergeDay에 넘긴 신호 행 가운데 어느 것이 왜 틀렸는지 알려준다.
type RowError struct {
	// Index는 넘겨받은 슬라이스에서의 자리다.
	Index int
	// ConversationID는 그 행이 나온 대화다. 어디서 온 행인지 찾을 때 쓴다. 메시지에는 넣지 않는다.
	ConversationID string
	Err            error
}

func (e *RowError) Error() string {
	return fmt.Sprintf("signal row %d: %v", e.Index, e.Err)
}

func (e *RowError) Unwrap() error { return e.Err }

// DayError는 하루 하나가 왜 틀렸는지 알려준다.
type DayError struct {
	// Index는 넘겨받은 슬라이스에서의 자리다. 슬라이스로 받지 않은 경우(MergeDay, MergeDays)에는 -1이다.
	Index int
	// Date는 문제가 된 날짜다. 날짜가 빠진 경우에는 빈 값이다.
	Date recorddate.Date
	Err  error
}

func (e *DayError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("signal day %s: %v", e.Date, e.Err)
	}
	return fmt.Sprintf("signal day %d (%s): %v", e.Index, e.Date, e.Err)
}

func (e *DayError) Unwrap() error { return e.Err }
