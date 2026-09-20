// Package baseline은 그 사람의 "평소"를 구한다.
//
// 절대 점수만 보면 원래 말수가 적거나 표현이 건조한 사람과 실제로 가라앉은 사람을 가르기 어렵다.
// 그래서 처음 얼마 동안의 기록으로 그 사람의 평소를 잡아 두고, 그 뒤의 날들은 평소와 견주어 본다.
//
// # 기간
//
//   - 첫 대화 날부터 달력 날짜로 WindowDays일(첫날 포함, 기본 14일)이 기준선 기간이다.
//     대화하지 않은 날도 달력 날짜로는 센다.
//   - 그 안에 대화한 날이 MinConversationDays일(기본 7일)이 안 되면 그만큼 찰 때까지 기간을 늘린다.
//     이때 기간의 마지막 날은 MinConversationDays번째 대화 날이다.
//     며칠 안 되는 기록으로 평소를 정하면 평소가 0에 가깝게 잡혀서 그 뒤의 평범한 날이 모두 나빠 보인다.
//
// # 잡히는 시점
//
// 기준선은 기간의 마지막 날이 지난 뒤에 잡힌다. 여기서 "지났다"는 기준일이 마지막 날보다 뒤라는 뜻이다.
// 기준일이 마지막 날과 같으면 아직 잡히지 않은 것이다.
//
// 기준일 당일은 아직 끝나지 않은 하루다. 밤에 한 번 더 대화하면 그날의 판단이 달라질 수 있다.
// 끝나지 않은 날까지 넣어 평소를 정하면 같은 날 안에서 평소가 흔들리고, 고정한다는 말이 맞지 않게 된다.
// 기간을 늘린 경우도 같다. 마지막 대화 날이 기준일이면 다음 날부터 잡힌 것으로 본다.
//
// 첫날이 9월 1일이면 다음과 같다.
//
//	9월 1일~14일에 대화한 날이 7일 이상  → 기간은 9월 14일까지, 9월 15일부터 잡힌 상태
//	9월 1일~14일에 6일, 일곱 번째가 9월 21일 → 기간은 9월 21일까지, 9월 22일부터 잡힌 상태
//
// # 고정과 다시 계산
//
// 한번 잡힌 기준선은 그 뒤에 기록이 쌓여도 달라지지 않는다. 기간이 끝난 뒤의 날은 계산에 쓰지 않기 때문이다.
// 달라지는 것은 사용자가 기간 안의 날을 지웠을 때뿐이다. 결과를 저장해 두지 않고 남은 기록으로
// 매번 처음부터 구하므로, 지운 날은 저절로 빠지고 기간과 평균이 그에 맞게 다시 정해진다.
// 첫날을 지우면 시작이 다음 대화 날로 옮겨 가고, 지워서 대화한 날이 모자라게 되면 기간이 늘어난다.
package baseline

import (
	"errors"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ErrZeroAsOf는 기준일이 비어 있다는 뜻이다.
// 기준일 없이는 기준선이 잡혔는지를 말할 수 없고, 오늘 날짜를 대신 읽어 오지도 않는다.
var ErrZeroAsOf = errors.New("baseline: as-of date is missing")

// Rate는 기준선 기간의 대화한 날 가운데 며칠에서 관찰됐는지다.
//
// 비율을 소수 하나로만 두지 않고 일수 둘로 둔다. 화면에서 "9일 중 3일"처럼 일수로 말하고,
// 최근 기간의 빈도와 견줄 때도 정수끼리 비교할 수 있게 하기 위해서다.
type Rate struct {
	// ObservedDays는 관찰된 날 수다.
	ObservedDays int
	// Days는 기준선에 쓴 대화한 날 수다. Baseline.Days와 같다.
	Days int
}

// Value는 비율(0부터 1)이다. 대화한 날이 없으면 0이다.
func (r Rate) Value() float64 {
	if r.Days <= 0 {
		return 0
	}
	return float64(r.ObservedDays) / float64(r.Days)
}

// Baseline은 기준일에 본 개인 기준선이다.
//
// Established가 false인 동안의 Days, Mu, 비율은 "지금까지 모인 값"이다. 평소로 쓰면 안 된다.
// 얼마나 모였는지 내부에서 확인하는 용도로만 채워 둔다.
type Baseline struct {
	// AsOf는 이 결과를 구한 기준일이다. 기준일보다 뒤의 기록은 아직 없는 것으로 보고 쓰지 않았다.
	AsOf recorddate.Date

	// Established는 기준일에 기준선이 잡혀 있는지다. End가 정해졌고 기준일이 End보다 뒤일 때만 true다.
	// 기준일이 End와 같은 날이면 false다.
	Established bool

	// Start는 기간의 첫날, 곧 첫 대화 날이다. 기준일까지 대화한 날이 없으면 빈 값이다.
	// 빈 날짜는 JSON으로 적을 수 없어서, 비어 있으면 JSON에서 빠지게 한다. End도 같다.
	Start recorddate.Date `json:",omitzero"`

	// End는 기간의 마지막 날이다. 기준일까지의 기록으로 아직 정할 수 없으면 빈 값이다.
	//
	//   - 첫 WindowDays일 안에 대화한 날이 이미 MinConversationDays일 이상이면 그 기간의 마지막 달력 날짜다.
	//     기준일이 그보다 앞이어도 정해진다. 남은 날에 대화를 더 해도 마지막 날은 달라지지 않기 때문이다.
	//   - 아니면 MinConversationDays번째 대화 날이다. 그날이 아직 오지 않았으면 빈 값이다.
	End recorddate.Date `json:",omitzero"`

	// Extended는 첫 WindowDays일 안에 대화한 날이 모자라서 기간을 늘렸는지다.
	// 첫 WindowDays일이 다 지났는데 대화한 날이 모자라면, 마지막 날이 아직 정해지지 않았어도 true다.
	Extended bool

	// Days는 기준선에 쓴 대화한 날 수다. 기간 안에 있고 기준일을 넘지 않는 날만 센다.
	Days int

	// ObservedTotal은 그 날들의 "그날 관찰된 항목 수"를 모두 더한 값이다. Mu의 분자다.
	ObservedTotal int

	// Mu는 평소의 하루 평균 신호 수다. ObservedTotal을 Days로 나눈 값이고, Days가 0이면 0이다.
	// 대화하지 않은 날은 분모에 들지 않는다. 기록이 없는 날을 "신호 없음"으로 세면 평소가 실제보다 낮게 잡힌다.
	Mu float64

	// ItemRates는 항목별 관찰 비율이다. 자리는 signal.Item.Index다.
	ItemRates [signal.ItemCount]Rate

	// TrendRates는 추세 화면의 줄별 관찰 비율이다. 자리는 signal.TrendRow.Index다.
	//
	// 기분 줄은 두 항목 가운데 하나라도 관찰된 날을 센다. 두 항목의 비율을 더하거나 큰 쪽을 골라서는
	// 이 값을 만들 수 없어서(같은 날 둘 다 관찰되면 겹친다) 줄 단위로 따로 센다.
	TrendRates [signal.TrendRowCount]Rate
}

// ItemRate는 그 항목의 관찰 비율이다. 항목이 아닌 값을 넘기면 빈 값을 돌려준다.
func (b Baseline) ItemRate(item signal.Item) Rate {
	idx := item.Index()
	if idx < 0 {
		return Rate{}
	}
	return b.ItemRates[idx]
}

// TrendRate는 추세 화면의 그 줄의 관찰 비율이다. 줄이 아닌 값을 넘기면 빈 값을 돌려준다.
// 추세 화면이 최근 기간의 빈도에 "평소보다 잦음" 같은 말을 붙일 때 이 값과 견준다.
// 기준선이 잡히기 전(Established가 false)의 값은 아직 평소가 아니므로 견주는 데 쓰지 않는다.
func (b Baseline) TrendRate(row signal.TrendRow) Rate {
	idx := row.Index()
	if idx < 0 {
		return Rate{}
	}
	return b.TrendRates[idx]
}

// Contains는 그 날짜가 기준선 기간에 드는지 알려준다.
// 그날의 기록을 지우면 평소가 다시 정해진다는 것을 미리 알아야 할 때 쓴다.
//
// 마지막 날이 아직 정해지지 않았으면 첫날부터 기준일까지를 기간으로 본다.
// 그 사이의 날은 모두 지금 모으고 있는 기준선에 들어가 있다.
func (b Baseline) Contains(date recorddate.Date) bool {
	if b.Start.IsZero() || date.Before(b.Start) {
		return false
	}
	last := b.End
	if last.IsZero() {
		last = b.AsOf
	}
	return !date.After(last)
}

// Compute는 기준일 asOf에 본 개인 기준선을 구한다.
//
// days는 대화한 날 전부를 날짜순으로 넘긴다(signal.ValidateDays를 통과해야 한다). 고치지 않는다.
// asOf보다 뒤의 날은 쓰지 않는다. 지난 날짜를 기준일로 넣으면 그날 알 수 있었던 기록만으로 구한 값이 나온다.
// 기준선이 잡힌 뒤에는 기준일을 뒤로 옮겨도 AsOf 말고는 달라지지 않는다.
//
// 기준일까지 대화한 날이 하나도 없으면 AsOf만 채운 빈 결과를 돌려준다. 오류가 아니다.
// 오류는 조정 값이 틀렸을 때(*params.FieldError), 기준일이 비었을 때(ErrZeroAsOf),
// days가 날짜순이 아니거나 틀린 하루가 있을 때(*signal.DayError)다.
//
// 조정 값은 p.Baseline만 쓰지만 검사는 p 전체에 한다. 틀린 값이 섞인 조정 값으로는
// 어느 계산도 돌리지 않아야, 한 번의 평가 안에서 일부만 계산되는 일이 없다.
func Compute(days []signal.Day, asOf recorddate.Date, p params.Params) (Baseline, error) {
	if err := p.Validate(); err != nil {
		return Baseline{}, fmt.Errorf("baseline: %w", err)
	}
	if asOf.IsZero() {
		return Baseline{}, ErrZeroAsOf
	}
	if err := signal.ValidateDays(days); err != nil {
		return Baseline{}, fmt.Errorf("baseline: %w", err)
	}

	known := upTo(days, asOf)
	b := Baseline{AsOf: asOf}
	if len(known) == 0 {
		return b, nil
	}
	b.Start = known[0].Date

	// 지원하는 날짜 범위(9999년) 밖으로 나가는 기간은 마지막 날을 적을 수 없다.
	// 그런 기간은 끝나지 않는 것으로 보고, 기준선을 잡지 않은 채 모인 값만 돌려준다.
	windowEnd := b.Start.AddDays(p.Baseline.WindowDays - 1)
	if windowEnd.IsZero() {
		b.fill(known)
		return b, nil
	}

	inWindow := 0
	for _, day := range known {
		if day.Date.After(windowEnd) {
			break
		}
		inWindow++
	}

	needed := p.Baseline.MinConversationDays
	used := len(known)
	switch {
	case inWindow >= needed:
		b.End = windowEnd
		used = inWindow
	case len(known) >= needed:
		// 첫 기간 안의 날이 모자랐으므로 이 자리의 날은 첫 기간보다 뒤에 있다.
		b.End = known[needed-1].Date
		b.Extended = true
		used = needed
	default:
		// 첫 기간의 마지막 날이 기준일이면 아직 늘린다고 말하지 않는다.
		// 그날 대화를 하면 대화한 날이 하루 늘어 모자라지 않게 될 수 있다.
		b.Extended = asOf.After(windowEnd)
	}

	b.fill(known[:used])
	b.Established = !b.End.IsZero() && asOf.After(b.End)
	return b, nil
}

// fill은 기준선에 쓰는 날들로 하루 평균과 비율을 채운다.
func (b *Baseline) fill(used []signal.Day) {
	b.Days = len(used)
	for _, day := range used {
		b.ObservedTotal += day.ObservedCount()
		for _, item := range signal.AllItems() {
			if day.Judgement(item).Status == signal.Observed {
				b.ItemRates[item.Index()].ObservedDays++
			}
		}
		for _, row := range signal.AllTrendRows() {
			if day.TrendStatus(row) == signal.Observed {
				b.TrendRates[row.Index()].ObservedDays++
			}
		}
	}
	for i := range b.ItemRates {
		b.ItemRates[i].Days = b.Days
	}
	for i := range b.TrendRates {
		b.TrendRates[i].Days = b.Days
	}
	if b.Days > 0 {
		b.Mu = float64(b.ObservedTotal) / float64(b.Days)
	}
}

// upTo는 기준일까지의 날만 남긴다. days가 날짜순이라 앞부분을 그대로 잘라 쓴다.
func upTo(days []signal.Day, asOf recorddate.Date) []signal.Day {
	n := 0
	for n < len(days) && !days[n].Date.After(asOf) {
		n++
	}
	return days[:n]
}
