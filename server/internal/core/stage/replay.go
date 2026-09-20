package stage

import (
	"errors"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ErrNoAsOf는 기준일이 빠졌다는 뜻이다. 현재 시각을 읽지 않으므로 기준일은 언제나 받아야 한다.
var ErrNoAsOf = errors.New("stage: as-of date is missing")

// Point는 달력 날짜 하루의 개입 단계와 그 단계가 나온 과정이다.
// 내부 확인 화면이 "왜 이 단계인가"를 그대로 보여줄 수 있도록 규칙이 본 값을 함께 담는다.
type Point struct {
	Date recorddate.Date

	// HasRecord는 그날 대화한 기록이 있는지다. 단계는 이 값이 참인 날에만 오른다.
	HasRecord bool
	// ConversationDays는 그날을 기준일로 한 창 안에서 대화한 일수다.
	ConversationDays int
	// Insufficient는 기록 부족이어서 그날의 추정 점수를 내지 않았다는 뜻이다.
	Insufficient bool
	// Score는 그날의 추정 점수다. Insufficient가 참이면 구하지 않은 값(0)이므로 읽지 않는다.
	Score int
	// Confidence는 그날의 신뢰도 구간이다. 기록 부족이면 낮음이다.
	Confidence confidence.Level
	// Detected는 그날 변화 감지 상태였는지다. 기준선이 잡히기 전에는 언제나 거짓이다.
	Detected bool

	// Raw는 그날의 값(점수, 변화 감지, 이어진 일수)만으로 정한 단계다. 전날의 단계와 견줘 묶거나 이어 가기 전의 값이다.
	// 기록 부족인 날에는 점수가 없으므로 변화 감지 상태면 1단계, 아니면 0단계다.
	Raw Stage
	// Stage는 그날의 개입 단계다. 기록 부족이어서 전날의 단계를 이어 간 날에는 Raw보다 높을 수 있다.
	Stage Stage
	// Held는 단계가 오르려던 만큼 오르지 못했다는 뜻이다. Raw가 Stage보다 큰 것과 같다.
	// 까닭은 HeldBy로 읽는다. 신뢰도가 낮거나 기록 부족이어서 묶인 날은
	// 단계를 올리는 대신 빠진 항목을 묻거나 자가 점검을 제안할 근거가 된다.
	Held bool

	// ElevatedDays는 2단계 이상이 그날까지 달력 날짜로 며칠째 이어졌는지다. 그날이 2단계 미만이면 0이다.
	// 기록 부족이어서 단계를 이어 간 날은 세지 않고 끊지도 않는다. 그런 날에는 전날의 값이 그대로 남는다.
	ElevatedDays int

	// Reasons는 단계를 실제로 움직인 조건을 규칙의 순서대로 담는다. 없으면 길이 0이고 nil은 아니다.
	Reasons []Reason
}

// HeldBy는 단계가 오르려던 만큼 오르지 못한 까닭을 돌려준다. 묶인 날이 아니면 빈 값(0)이다.
// 까닭은 하루에 하나다. 여럿이 겹친 날에 어느 것을 적는지는 패키지 설명에 있다.
func (pt Point) HeldBy() Reason {
	for _, reason := range pt.Reasons {
		if reason.held() {
			return reason
		}
	}
	return 0
}

// Result는 첫 대화 날부터 기준일까지의 흐름과 기준일의 상태다.
type Result struct {
	// AsOf는 기준일이다.
	AsOf recorddate.Date
	// From은 흐름의 첫날, 곧 첫 대화 날이다. 기준일까지 대화한 날이 없으면 빈 값이다.
	// 빈 날짜는 JSON으로 적을 수 없어서, 비어 있으면 JSON에서 빠지게 한다.
	From recorddate.Date `json:",omitzero"`
	// Series는 From부터 AsOf까지 달력의 하루하루다. 대화하지 않은 날도 들어 있다. 날짜순이고, 없으면 길이 0이다.
	Series []Point
	// State는 기준일의 상태다. Series의 마지막 값과 같다.
	// 기준일까지 대화한 날이 없으면 0단계이고 ReasonNoRecentRecords가 적힌다.
	State Point
}

// At은 그 날짜의 Point를 돌려준다. 흐름에 없는 날짜(첫 대화 날보다 앞이거나 기준일보다 뒤)면 ok가 거짓이다.
func (r Result) At(date recorddate.Date) (pt Point, ok bool) {
	if r.From.IsZero() || date.IsZero() {
		return Point{}, false
	}
	offset := date.DaysSince(r.From)
	if offset < 0 || offset >= len(r.Series) {
		return Point{}, false
	}
	return r.Series[offset], true
}

// Changed는 기준일의 단계가 전날과 다른지, 전날의 단계는 무엇이었는지 알려준다.
// 단계에 따라 말을 만드는 쪽이 "오늘 새로 이 단계가 되었는가"를 흐름의 끝 두 칸을 직접 견주지 않고 묻는다.
// 첫 대화 날의 전날과 기록이 없는 때의 전날은 0단계다.
func (r Result) Changed() (previous Stage, changed bool) {
	if n := len(r.Series); n >= 2 {
		previous = r.Series[n-2].Stage
	}
	return previous, previous != r.State.Stage
}

// Compute는 첫 대화 날부터 asOf까지 하루씩 다시 돌려 개입 단계를 구한다.
//
// days는 대화한 날 전부를 날짜순으로 넘긴다(signal.ValidateDays를 통과해야 한다). 고치지 않는다.
// asOf보다 뒤의 날은 쓰지 않는다. 지난 날짜를 기준일로 넣으면 그날 알 수 있었던 기록만으로 구한 값이 나온다.
//
// 기준일까지 대화한 날이 없으면 빈 흐름과 0단계 상태를 돌려준다. 오류가 아니다.
// 오류는 조정 값이 틀렸을 때(*params.FieldError), 기준일이 비었을 때(ErrNoAsOf),
// days가 날짜순이 아니거나 틀린 하루가 있을 때(*signal.DayError),
// 어느 날짜의 창을 만들 수 없을 때(score.ErrInvalidWindow)다.
func Compute(days []signal.Day, asOf recorddate.Date, p params.Params) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, fmt.Errorf("stage: %w", err)
	}
	if asOf.IsZero() {
		return Result{}, ErrNoAsOf
	}
	if err := signal.ValidateDays(days); err != nil {
		return Result{}, fmt.Errorf("stage: %w", err)
	}

	known := upTo(days, asOf)
	if len(known) == 0 {
		return Result{
			AsOf:   asOf,
			Series: []Point{},
			State:  step(Point{}, input{date: asOf}, p.Stage),
		}, nil
	}

	// 변화 감지는 한 번만 돌리고 날짜마다 그날의 상태를 물어본다.
	// 기준선은 기간이 끝난 뒤의 기록에 영향받지 않으므로, 지난 날짜를 기준일로 다시 구한 것과 같은 값이 나온다.
	base, err := baseline.Compute(known, asOf, p)
	if err != nil {
		return Result{}, fmt.Errorf("stage: %w", err)
	}
	change, err := cusum.Run(known, base, p)
	if err != nil {
		return Result{}, fmt.Errorf("stage: %w", err)
	}

	first := known[0].Date
	count := asOf.DaysSince(first) + 1
	result := Result{AsOf: asOf, From: first, Series: make([]Point, 0, count)}

	// 빈 값에서 시작한다. 첫 대화 날의 "전날"은 0단계이고 이어진 일수도 0이다.
	var previous Point
	// known[lo:hi]는 그날의 창 안에 드는 대화한 날이다. 날짜가 하루씩 나아가므로 두 자리 모두 앞으로만 움직인다.
	lo, hi := 0, 0
	for offset := range count {
		date := first.AddDays(offset)
		for hi < len(known) && !known[hi].Date.After(date) {
			hi++
		}
		for lo < hi && date.DaysSince(known[lo].Date) >= p.Window.Days {
			lo++
		}

		// 점수와 신뢰도에는 창 안의 날만 잘라 넘긴다. 창 밖의 날은 어차피 보지 않으므로 결과는 전부를 넘긴 것과 같고,
		// 하루를 돌릴 때마다 기록 전체를 다시 훑지 않아도 된다. 같은 조각을 넘기므로 두 계산이 보는 날도 언제나 같다.
		window := known[lo:hi]
		// 날짜가 하루씩 나아가므로, 그날의 기록이 있다면 지금까지 지나온 날 가운데 마지막 것이다.
		hasRecord := hi > 0 && known[hi-1].Date == date
		estimated, err := score.Compute(window, date, p)
		if err != nil {
			return Result{}, fmt.Errorf("stage: %w", err)
		}
		reliability, err := confidence.Compute(window, date, p)
		if err != nil {
			return Result{}, fmt.Errorf("stage: %w", err)
		}
		total, hasScore := estimated.Score()

		previous = step(previous, input{
			date:             date,
			hasRecord:        hasRecord,
			conversationDays: estimated.ConversationDays,
			score:            total,
			hasScore:         hasScore,
			confidence:       reliability.Level,
			detected:         change.StateAt(date).Detected,
		}, p.Stage)
		result.Series = append(result.Series, previous)
	}

	result.State = previous
	return result, nil
}

// input은 규칙이 날짜 하루에 대해 보는 값 전부다.
type input struct {
	date recorddate.Date
	// hasRecord는 그날 대화한 기록이 있는지다.
	hasRecord        bool
	conversationDays int
	score            int
	hasScore         bool
	confidence       confidence.Level
	detected         bool
}

// step은 전날의 결과와 그날의 값으로 그날의 단계를 정한다. 규칙은 패키지 설명에 적은 순서 그대로다.
// 전날에서 넘겨받는 것은 단계와 2단계 이상이 이어진 일수뿐이다. 첫날에는 빈 Point를 넘긴다.
func step(previous Point, in input, p params.Stage) Point {
	pt := Point{
		Date:             in.date,
		HasRecord:        in.hasRecord,
		ConversationDays: in.conversationDays,
		Insufficient:     !in.hasScore,
		Confidence:       in.confidence,
		Detected:         in.detected,
		Reasons:          []Reason{},
	}
	if in.hasScore {
		pt.Score = in.score
	}

	// 8. 창 안에 대화한 날이 없으면 나머지 규칙을 따지지 않는다. 누적값은 대화하지 않는 동안 그대로 남아서
	// 변화 감지가 켜진 채일 수 있는데, 그 흔적만으로 한참 만에 돌아온 사용자를 1단계로 맞지 않는다.
	// 이어진 일수도 0에서 다시 센다.
	if in.conversationDays == 0 {
		pt.Reasons = append(pt.Reasons, ReasonNoRecentRecords)
		return pt
	}

	// 1~3. 그날의 값만으로 본 단계다. 점수가 없는 날은 점수로 받칠 단계가 없어 0단계에서 시작한다.
	raw := Everyday
	if in.hasScore {
		raw = FromScore(in.score, p)
	}
	if raw > Everyday {
		pt.Reasons = append(pt.Reasons, ReasonScore)
	}
	if in.detected && raw < Reflection {
		raw = Reflection
		pt.Reasons = append(pt.Reasons, ReasonChangeDetected)
	}
	// 그날까지 포함해 센 일수가 정해진 일수에 닿는 날부터다. 전날까지 하루라도 이어졌다면 전날이 2단계 이상이었으므로,
	// 점수로 2단계인 오늘은 아래의 어느 규칙에 묶이든 2단계 이상으로 남아 그 일수에 든다.
	// 점수만으로 이미 3단계인 날에는 바꿀 것이 없다.
	if raw == Suggestion && previous.ElevatedDays+1 >= p.SustainedStage2Days {
		raw = Recommendation
		pt.Reasons = append(pt.Reasons, ReasonSustained)
	}
	pt.Raw = raw

	// 7. 기록 부족인 날에는 전날의 단계와 이어진 일수를 그대로 이어 간다.
	// 점수가 없는 날은 나아졌다는 근거도 나빠졌다는 근거도 없는 날이다.
	if !in.hasScore {
		pt.Stage, pt.ElevatedDays = previous.Stage, previous.ElevatedDays
		switch {
		case raw > previous.Stage:
			pt.Held = true
			pt.Reasons = append(pt.Reasons, ReasonHeldInsufficientRecords)
		case raw < previous.Stage:
			pt.Reasons = append(pt.Reasons, ReasonCarriedInsufficientRecords)
		}
		return pt
	}

	// 4~6. 오르는 것만 막는다. 까닭이 겹치면 기록이 더 쌓여야 풀리는 쪽(낮은 신뢰도)을 먼저 적는다.
	// 그날의 기록이 없어서, 또는 하루에 한 단계라서 덜 오른 것은 다음에 대화한 날이면 풀린다.
	pt.Stage = raw
	if raw > previous.Stage {
		switch {
		case in.confidence == confidence.Low:
			pt.Stage = previous.Stage
			pt.Reasons = append(pt.Reasons, ReasonHeldLowConfidence)
		case !in.hasRecord:
			pt.Stage = previous.Stage
			pt.Reasons = append(pt.Reasons, ReasonHeldNoRecordToday)
		case raw > previous.Stage+1:
			pt.Stage = previous.Stage + 1
			pt.Reasons = append(pt.Reasons, ReasonHeldOneStepPerDay)
		}
		pt.Held = pt.Stage < raw
	}

	if pt.Stage >= Suggestion {
		pt.ElevatedDays = previous.ElevatedDays + 1
	}
	return pt
}

// upTo는 기준일까지의 날만 남긴다. days가 날짜순이라 앞부분을 그대로 잘라 쓴다.
func upTo(days []signal.Day, asOf recorddate.Date) []signal.Day {
	n := 0
	for n < len(days) && !days[n].Date.After(asOf) {
		n++
	}
	return days[:n]
}
