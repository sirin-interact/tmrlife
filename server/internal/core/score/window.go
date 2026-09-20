package score

import (
	"errors"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ErrInvalidWindow는 창을 만들 수 없다는 뜻이다.
// 기준일이 비었거나, 길이가 1보다 작거나, 창의 첫날이 다룰 수 있는 날짜 범위를 벗어난 경우다.
var ErrInvalidWindow = errors.New("score: window cannot be built")

// Window는 점수가 들여다보는 기간이다. From과 To를 모두 포함한다.
//
// 기준일(To)을 포함해 거슬러 센다. 길이가 14이면 기준일과 그 앞의 13일이다.
// 오늘 저녁의 대화가 오늘의 점수에 바로 들어가야 하므로 기준일을 뺀 "어제까지"로 잡지 않는다.
type Window struct {
	From recorddate.Date
	To   recorddate.Date
}

// NewWindow는 asOf에서 끝나는 length일짜리 창을 만든다.
//
// 기준일은 언제나 인자로 받는다. 지난 날짜를 기준일로 넘기면 그날의 점수를 다시 구할 수 있고,
// 첫 대화 날부터 하루씩 다시 돌리는 계산이 여기에 기대고 있다.
func NewWindow(asOf recorddate.Date, length int) (Window, error) {
	if asOf.IsZero() {
		return Window{}, fmt.Errorf("%w: as-of date is missing", ErrInvalidWindow)
	}
	if length < 1 {
		return Window{}, fmt.Errorf("%w: length must be at least 1, got %d", ErrInvalidWindow, length)
	}
	from := asOf.AddDays(-(length - 1))
	// 빈 날짜는 모든 날짜보다 앞선 것으로 비교된다. 그대로 두면 창이 끝없이 과거로 열린다.
	if from.IsZero() {
		return Window{}, fmt.Errorf("%w: first day is outside the supported date range", ErrInvalidWindow)
	}
	return Window{From: from, To: asOf}, nil
}

// Contains는 그 날짜가 창 안에 드는지 알려준다. 빈 날짜와 빈 창은 언제나 거짓이다.
func (w Window) Contains(date recorddate.Date) bool {
	if date.IsZero() || w.From.IsZero() || w.To.IsZero() {
		return false
	}
	return !date.Before(w.From) && !date.After(w.To)
}

// Length는 창의 날 수다. 빈 창은 0이다.
func (w Window) Length() int {
	if w.From.IsZero() || w.To.IsZero() {
		return 0
	}
	return w.To.DaysSince(w.From) + 1
}

// DaysIn은 창 안에 드는 하루만 골라 새 슬라이스로 돌려준다. 받은 순서를 지키고, 받은 슬라이스는 고치지 않는다.
//
// 점수와 함께 같은 기간을 보는 다른 계산이 "창 안의 기록"을 똑같이 고르도록 둔 것이다.
// 기준일 뒤의 날짜도 여기서 빠진다. 지난 날짜를 기준일로 다시 계산할 때 그 뒤의 기록이 섞이면 안 된다.
func (w Window) DaysIn(days []signal.Day) []signal.Day {
	var inside []signal.Day
	for _, day := range days {
		if w.Contains(day.Date) {
			inside = append(inside, day)
		}
	}
	return inside
}
