package assess_test

// 이 파일은 규칙을 한 번 더, 다른 방식으로 옮긴 두 번째 단순 구현과 실제 구현을 견준다.
//
// 같은 패키지의 참조 구현은 소수로 계산하고, 누적값이 한계값과 딱 같은 날의 변화 감지는 실제 구현의 답을 받아 쓴다.
// 그 자리에서 두 구현이 함께 틀리면 드러나지 않는다. 여기서는 모든 값을 분수로 정확하게 계산해서 그런 날도 스스로 답한다.
// 조정 값도 0.3이나 4.1처럼 이진 소수로 딱 떨어지지 않는 값을 섞는다. 사람이 적은 십진수 그대로 읽혀야 한다.
//
// 날짜마다 점수, 신뢰도, 기준선, 누적값을 그 날짜까지의 기록만으로 처음부터 다시 구한다.
// 이어 쓰는 것은 전날의 단계와 이어진 일수뿐이다. 느리지만 "지난 계산을 잘못 이어 쓴" 실수가 끼어들 자리가 없다.
//
// 가상 기록도 따로 만든다. 며칠 말하고 며칠 쉬는 리듬, 몇 주씩 이어지는 나쁜 시기와 회복, 몇 항목만 이야기하는 사람,
// 간접 추론뿐인 시기를 일부러 넣어서 기록 부족으로 단계를 이어 가는 길, 누적값이 천장에 닿았다 풀리는 길,
// 창이 밀려 점수가 오르는 대화 없는 날, 신뢰도가 낮아 묶이는 날을 자주 지나가게 한다.

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ---------------------------------------------------------------------------
// 두 번째 단순 구현
// ---------------------------------------------------------------------------

// difDay는 하루로 합친 기록이다. 항목마다 참과 거짓만 든다.
type difDay struct {
	observed  [refItems]bool
	direct    [refItems]bool // 관찰된 행 가운데 직접 언급이 하나라도 있었는가
	mentioned [refItems]bool // 관찰됨이든 관찰되지 않음이든 이야기가 나왔는가
}

func (d difDay) observedCount() int {
	count := 0
	for _, observed := range d.observed {
		if observed {
			count++
		}
	}
	return count
}

// difMerge는 행을 하루로 합친다. 취소된 행은 없는 것과 같고, 한 번이라도 관찰되면 관찰됨이다.
// 행이 하나도 없는 날짜는 대화하지 않은 날이라 결과에 없다. 행이 모두 취소된 날은 대화한 날로 남는다.
func difMerge(h refHistory) map[int]difDay {
	out := map[int]difDay{}
	for offset, rows := range h.rows {
		if len(rows) == 0 {
			continue
		}
		var day difDay
		for _, row := range rows {
			if row.cancelled {
				continue
			}
			switch row.status {
			case refObserved:
				day.observed[row.item], day.mentioned[row.item] = true, true
				if row.direct {
					day.direct[row.item] = true
				}
			case refNotObserved:
				day.mentioned[row.item] = true
			}
		}
		out[offset] = day
	}
	return out
}

// difDecimal은 조정 값을 사람이 적은 십진수 그대로 분수로 읽는다. 0.3은 10분의 3이다.
func difDecimal(t *testing.T, v float64) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	require.True(t, ok, "분수로 읽을 수 없는 조정 값 %v", v)
	return r
}

// difKnobs는 소수로 적힌 조정 값을 분수로 옮겨 둔 것이다. 상한과 천장은 두지 않으면 nil이다.
type difKnobs struct {
	k, h       *big.Rat
	maxStep    *big.Rat
	ceiling    *big.Rat
	mediumMin  *big.Rat
	highMin    *big.Rat
	windowDays int
}

func difKnobsOf(t *testing.T, p params.Params) difKnobs {
	t.Helper()
	knobs := difKnobs{
		k: difDecimal(t, p.CUSUM.K), h: difDecimal(t, p.CUSUM.H),
		mediumMin: difDecimal(t, p.Confidence.MediumMin), highMin: difDecimal(t, p.Confidence.HighMin),
		windowDays: p.Window.Days,
	}
	if p.CUSUM.MaxStep > 0 {
		knobs.maxStep = difDecimal(t, p.CUSUM.MaxStep)
	}
	if p.CUSUM.MaxS > 0 {
		knobs.ceiling = new(big.Rat).Mul(difDecimal(t, p.CUSUM.MaxS), knobs.h)
	}
	return knobs
}

// difWindow는 기준일을 포함해 거슬러 센 창 안의 대화한 날들이다.
func difWindow(days map[int]difDay, date int, knobs difKnobs) []difDay {
	var in []difDay
	for offset := date - knobs.windowDays + 1; offset <= date; offset++ {
		if day, ok := days[offset]; ok {
			in = append(in, day)
		}
	}
	return in
}

// difScore는 그 날짜의 추정 점수다. 환산 일수는 분수로 구해 0.5를 더하고 내림한다.
func difScore(days map[int]difDay, date int, p params.Params, knobs difKnobs) (n int, sufficient bool, total int) {
	window := difWindow(days, date, knobs)
	n = len(window)
	if n < p.Window.MinConversationDays {
		return n, false, 0
	}
	half := big.NewRat(1, 2)
	for item := range refItems {
		observedDays := 0
		for _, day := range window {
			if day.observed[item] {
				observedDays++
			}
		}
		converted := big.NewRat(int64(p.Window.Days*observedDays), int64(n))
		converted.Add(converted, half)
		rounded := int(new(big.Int).Quo(converted.Num(), converted.Denom()).Int64())
		switch {
		case rounded >= p.Score.ItemScore3MinDays:
			total += 3
		case rounded >= p.Score.ItemScore2MinDays:
			total += 2
		case rounded >= p.Score.ItemScore1MinDays:
			total++
		}
	}
	return n, true, total
}

// difLevel은 그 날짜의 신뢰도 구간이다. 세 요소 가운데 가장 작은 값을 경계와 분수로 견준다.
func difLevel(days map[int]difDay, date int, p params.Params, knobs difKnobs) string {
	window := difWindow(days, date, knobs)
	if len(window) < p.Window.MinConversationDays {
		return "low"
	}
	mentionedItems, observedJudgements, directJudgements := 0, 0, 0
	for item := range refItems {
		mentioned := false
		for _, day := range window {
			mentioned = mentioned || day.mentioned[item]
			if day.observed[item] {
				observedJudgements++
				if day.direct[item] {
					directJudgements++
				}
			}
		}
		if mentioned {
			mentionedItems++
		}
	}
	value := big.NewRat(int64(len(window)), int64(p.Window.Days))
	if itemCoverage := big.NewRat(int64(mentionedItems), refItems); itemCoverage.Cmp(value) < 0 {
		value = itemCoverage
	}
	if observedJudgements > 0 {
		if explicitness := big.NewRat(int64(directJudgements), int64(observedJudgements)); explicitness.Cmp(value) < 0 {
			value = explicitness
		}
	}
	switch {
	case value.Cmp(knobs.mediumMin) < 0:
		return "low"
	case value.Cmp(knobs.highMin) < 0:
		return "medium"
	default:
		return "high"
	}
}

type difBaseline struct {
	hasStart, hasEnd bool
	start, end       int
	established      bool
	count, total     int
}

// difBaselineAt은 그 날짜에 알 수 있었던 기록만으로 기준선을 구한다.
// 첫 대화 날부터 정해진 일수(첫날 포함)가 기간이고, 그 안에 대화한 날이 모자라면 모자란 수만큼째 대화 날까지 늘린다.
// 기간의 마지막 날이 지난 뒤에야 기준선이 잡힌다.
func difBaselineAt(days map[int]difDay, date int, p params.Params) difBaseline {
	var known []int
	for offset := range days {
		if offset <= date {
			known = append(known, offset)
		}
	}
	slices.Sort(known)
	var b difBaseline
	if len(known) == 0 {
		return b
	}
	b.hasStart, b.start = true, known[0]

	calendarEnd := b.start + p.Baseline.WindowDays - 1
	inCalendar := 0
	for _, offset := range known {
		if offset <= calendarEnd {
			inCalendar++
		}
	}
	switch {
	case inCalendar >= p.Baseline.MinConversationDays:
		b.hasEnd, b.end = true, calendarEnd
	case len(known) >= p.Baseline.MinConversationDays:
		b.hasEnd, b.end = true, known[p.Baseline.MinConversationDays-1]
	}
	b.established = b.hasEnd && date > b.end

	for _, offset := range known {
		if b.hasEnd && offset > b.end {
			continue
		}
		b.count++
		b.total += days[offset].observedCount()
	}
	return b
}

type difChangeDay struct {
	offset, x int
	// step과 s는 그 흐름의 공통 분모를 곱한 정수다. 분수로는 difChange.exact로 되돌린다.
	step, s  int64
	capped   bool
	cut      bool
	detected bool
}

type difChange struct {
	running bool
	from    int
	series  []difChangeDay
	// unit은 이 흐름의 공통 분모다. s와 series의 값은 모두 여기에 맞춘 정수다. 돌고 있지 않은 흐름에서는 0이다.
	unit     int64
	s        int64
	detected bool
}

// exact는 공통 분모를 곱해 둔 정수를 분수로 되돌린다.
func (c difChange) exact(scaled int64) *big.Rat {
	if c.unit == 0 {
		return new(big.Rat)
	}
	return big.NewRat(scaled, c.unit)
}

// difMaxUnit은 공통 분모로 받아 주는 가장 큰 값이다. 하루에 더하는 값은 공통 분모의 여덟 배를 넘지 못하므로,
// 공통 분모가 이보다 작으면 수만 일을 쌓아도 int64를 넘치지 않는다.
const difMaxUnit = int64(1) << 40

// difCommonUnit은 넘겨받은 분수들의 분모를 모두 나누는 가장 작은 수다. nil은 두지 않은 조정 값이라 건너뛴다.
func difCommonUnit(t *testing.T, values ...*big.Rat) *big.Int {
	t.Helper()
	unit := big.NewInt(1)
	for _, v := range values {
		if v == nil {
			continue
		}
		shared := new(big.Int).GCD(nil, nil, unit, v.Denom())
		unit.Mul(unit, new(big.Int).Quo(v.Denom(), shared))
	}
	require.True(t, unit.IsInt64() && unit.Int64() <= difMaxUnit, "공통 분모 %s가 정수로 쌓기에 너무 크다", unit)
	return unit
}

// difScaled는 분수에 공통 분모를 곱한 정수다. 공통 분모가 그 분수의 분모로 나누어떨어지므로 버려지는 것이 없다.
func difScaled(t *testing.T, v *big.Rat, unit *big.Int) int64 {
	t.Helper()
	// 분자 × (공통 분모 ÷ 분모). 분수끼리 곱하면 곱할 때마다 약분을 다시 해서, 날짜마다 부르기에는 느리다.
	times, left := new(big.Int).QuoRem(unit, v.Denom(), new(big.Int))
	scaled := times.Mul(times, v.Num())
	require.True(t, left.Sign() == 0 && scaled.IsInt64(), "%s에 공통 분모 %s를 곱해도 정수가 되지 않는다", v, unit)
	return scaled.Int64()
}

// difChangeAt은 그 날짜를 기준일로 삼아 누적값을 처음부터 다시 쌓는다.
// 기준선 기간의 다음 날부터 대화한 날마다, 그날 관찰된 항목 수에서 평소의 평균과 여유를 뺀 값을 더한다.
// 하루에 더하는 값은 상한까지만, 누적값은 0과 천장 사이에서만 움직인다. 한계값을 넘어야(같으면 아니다) 감지다.
//
// 분수를 날마다 더하고 견주면, 날짜마다 처음부터 다시 쌓는 이 구현에서는 시험 시간의 대부분이 분수 계산에 들어간다.
// 그래서 평소의 평균과 조정 값의 분모를 모두 나누는 공통 분모를 먼저 구하고, 모든 값에 그것을 곱해 정수로 쌓는다.
// 정수끼리 더하고 견주는 것이라 분수로 쌓은 것과 똑같이 정확하다. 소수는 어디에도 끼어들지 않는다.
func difChangeAt(t *testing.T, days map[int]difDay, date int, p params.Params, knobs difKnobs) difChange {
	t.Helper()
	base := difBaselineAt(days, date, p)
	var change difChange
	if !base.established {
		return change
	}
	change.running, change.from = true, base.end+1

	mu := big.NewRat(int64(base.total), int64(base.count))
	common := difCommonUnit(t, mu, knobs.k, knobs.h, knobs.maxStep, knobs.ceiling)
	change.unit = common.Int64()
	usual, allowance, limit := difScaled(t, mu, common), difScaled(t, knobs.k, common), difScaled(t, knobs.h, common)
	var maxStep, ceiling int64
	if knobs.maxStep != nil {
		maxStep = difScaled(t, knobs.maxStep, common)
	}
	if knobs.ceiling != nil {
		ceiling = difScaled(t, knobs.ceiling, common)
	}

	change.series = make([]difChangeDay, 0, date-change.from+1)
	for offset := change.from; offset <= date; offset++ {
		day, ok := days[offset]
		if !ok {
			continue
		}
		point := difChangeDay{offset: offset, x: day.observedCount()}
		point.step = int64(point.x)*change.unit - usual - allowance
		if knobs.maxStep != nil && point.step > maxStep {
			point.step, point.capped = maxStep, true
		}
		next := max(change.s+point.step, 0)
		if knobs.ceiling != nil && next > ceiling {
			next, point.cut = ceiling, true
		}
		point.s, point.detected = next, next > limit
		change.s, change.detected = next, point.detected
		change.series = append(change.series, point)
	}
	return change
}

type difPoint struct {
	offset     int
	hasRecord  bool
	n          int
	sufficient bool
	score      int
	level      string
	change     difChange

	// wanted는 그날의 점수, 변화 감지, 이어진 일수만으로 본 단계다. stage는 전날과 견줘 묶거나 이어 간 뒤의 단계다.
	wanted, stage int
	run           int
	// blockers는 오르려던 단계를 막은 규칙들이다. 둘 이상이 함께 막은 날에는 모두 든다.
	blockers []string
}

// difReplay는 첫 대화 날부터 기준일까지 달력의 하루하루를 돌린다.
//
//   - 창 안에 대화한 날이 하나도 없으면 0단계이고 이어진 일수도 0이다.
//   - 기록 부족이면 전날의 단계와 이어진 일수를 그대로 둔다.
//   - 아니면 점수로 본 단계에서 시작해, 변화 감지면 적어도 1단계, 2단계 이상이 정해진 일수째 이어지면 3단계다.
//   - 오르는 것은 그날의 기록이 있고 신뢰도가 낮지 않은 날에만, 한 단계씩이다. 내려가는 것은 그날 바로다.
func difReplay(t *testing.T, days map[int]difDay, asOf int, p params.Params, knobs difKnobs) []difPoint {
	t.Helper()
	first, found := 0, false
	for offset := range days {
		if offset <= asOf && (!found || offset < first) {
			first, found = offset, true
		}
	}
	if !found {
		return nil
	}

	var out []difPoint
	previousStage, previousRun := 0, 0
	for date := first; date <= asOf; date++ {
		pt := difPoint{offset: date, level: difLevel(days, date, p, knobs), change: difChangeAt(t, days, date, p, knobs)}
		_, pt.hasRecord = days[date]
		pt.n, pt.sufficient, pt.score = difScore(days, date, p, knobs)

		switch {
		case pt.n == 0:
			pt.stage, pt.run = 0, 0

		case !pt.sufficient:
			if pt.change.detected {
				pt.wanted = 1
			}
			pt.stage, pt.run = previousStage, previousRun
			if pt.wanted > previousStage {
				pt.blockers = append(pt.blockers, "held_insufficient_records")
			}

		default:
			switch {
			case pt.score >= p.Stage.Stage3MinScore:
				pt.wanted = 3
			case pt.score >= p.Stage.Stage2MinScore:
				pt.wanted = 2
			case pt.score >= p.Stage.Stage1MinScore:
				pt.wanted = 1
			}
			if pt.change.detected && pt.wanted < 1 {
				pt.wanted = 1
			}
			if pt.wanted >= 2 && previousRun+1 >= p.Stage.SustainedStage2Days {
				pt.wanted = 3
			}

			pt.stage = pt.wanted
			if pt.wanted > previousStage {
				if !pt.hasRecord {
					pt.blockers = append(pt.blockers, "held_no_record_today")
				}
				if pt.level == "low" {
					pt.blockers = append(pt.blockers, "held_low_confidence")
				}
				switch {
				case len(pt.blockers) > 0:
					pt.stage = previousStage
				case pt.wanted > previousStage+1:
					pt.stage = previousStage + 1
					pt.blockers = append(pt.blockers, "held_one_step_per_day")
				}
			}
			if pt.stage >= 2 {
				pt.run = previousRun + 1
			}
		}

		previousStage, previousRun = pt.stage, pt.run
		out = append(out, pt)
	}
	return out
}

// ---------------------------------------------------------------------------
// 가상 기록과 조정 값
// ---------------------------------------------------------------------------

// difGenHistory는 씨앗 하나에서 기록 하나를 만든다. 리듬과 시기를 먼저 정하고 그 위에 행을 뿌린다.
func difGenHistory(t *testing.T, seed uint64) refHistory {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0xd1ff))
	bases := [...]string{"2026-01-20", "2024-02-10", "2025-12-20", "2027-08-25"}
	base, err := recorddate.Parse(bases[rng.IntN(len(bases))])
	require.NoError(t, err)
	h := refHistory{base: base, span: 25 + rng.IntN(120), rows: map[int][]refRow{}}

	// 리듬: 날마다, 며칠 말하고 며칠 쉬기, 드문드문.
	talks := func(int) bool { return true }
	switch rng.IntN(4) {
	case 0:
		on, off := 1+rng.IntN(5), 1+rng.IntN(6)
		talks = func(offset int) bool { return offset%(on+off) < on }
	case 1:
		density := 0.3 + 0.6*rng.Float64()
		talks = func(int) bool { return rng.Float64() < density }
	case 2:
		on, off := 2+rng.IntN(3), 2+rng.IntN(3)
		skip := 0.15 * rng.Float64()
		talks = func(offset int) bool { return offset%(on+off) < on && rng.Float64() >= skip }
	}

	// 시기: 기록을 두세 토막으로 나누고 토막마다 나쁜 정도, 직접 언급의 비율, 이야기하는 항목의 폭을 따로 고른다.
	type phase struct {
		until    int
		severity float64
		direct   float64
		breadth  int
	}
	var phases []phase
	for cut := 0; cut < h.span; {
		cut += 10 + rng.IntN(45)
		phases = append(phases, phase{
			until:    cut,
			severity: [...]float64{0, 0.08, 0.25, 0.5, 0.8, 0.97}[rng.IntN(6)],
			direct:   [...]float64{0, 0.2, 0.5, 0.9, 1}[rng.IntN(5)],
			breadth:  [...]int{2, 3, 5, 8, 8}[rng.IntN(5)],
		})
	}
	silenceFrom, silenceDays := -1, 0
	if rng.IntN(3) == 0 {
		silenceFrom, silenceDays = rng.IntN(h.span), 8+rng.IntN(40)
	}
	cancel := [...]float64{0, 0, 0.04, 0.2}[rng.IntN(4)]
	var bias [refItems]float64
	for item := range bias {
		bias[item] = 0.6 + 0.8*rng.Float64()
	}

	for offset := range h.span {
		if offset >= silenceFrom && offset < silenceFrom+silenceDays && silenceFrom >= 0 {
			continue
		}
		if !talks(offset) {
			continue
		}
		if rng.IntN(50) == 0 {
			h.rows[offset] = nil // 분석을 꺼 둔 날
			continue
		}
		ph := phases[len(phases)-1]
		for _, candidate := range phases {
			if offset < candidate.until {
				ph = candidate
				break
			}
		}
		var rows []refRow
		for range 1 + rng.IntN(5)/4 {
			for item := range ph.breadth {
				row := refRow{item: item, status: refNotObserved, direct: rng.Float64() < ph.direct, cancelled: rng.Float64() < cancel}
				switch roll := rng.Float64(); {
				case roll < ph.severity*bias[item]:
					row.status = refObserved
				case rng.IntN(3) == 0:
					row.status, row.direct = refNotMentioned, false
				}
				rows = append(rows, row)
			}
		}
		h.rows[offset] = rows
	}
	return h
}

// difGenUnevenHistory는 고친 규칙이 걸리는 자리를 일부러 자주 지나가는 기록을 만든다.
//
// 다른 두 만들기 함수의 기록은 대화 빈도가 기록 내내 고르다. 그러면 창 안의 대화한 일수가 기준을 한두 번 넘고 끝나서,
// 기록 부족으로 단계를 이어 가다가 다시 세기 시작하는 자리와 2단계 이상이 그 사이를 건너 이어지는 자리를 드물게만 지나간다.
// 여기서는 대화한 일수가 기준 언저리를 오르내리게 하거나, 길이가 매번 다른 토막으로 말하고 쉬게 한다.
// 나쁜 정도도 짧은 토막마다 바꾸고, 하루만 뒤집힌 날을 섞는다.
func difGenUnevenHistory(t *testing.T, seed uint64) refHistory {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0x0e7e))
	start, err := recorddate.Parse("2025-11-01")
	require.NoError(t, err)
	h := refHistory{base: start.AddDays(rng.IntN(900)), span: 30 + rng.IntN(220), rows: map[int][]refRow{}}

	talked := make([]bool, h.span)
	rhythm := rng.IntN(5)
	target := 6 + rng.IntN(3)
	talking, left := true, 1+rng.IntN(8)
	density := 0.15 + 0.5*rng.Float64()
	silenceFrom, silenceDays := -1, 0
	if rng.IntN(2) == 0 {
		silenceFrom, silenceDays = rng.IntN(h.span), 10+rng.IntN(50)
	}

	type phase struct {
		until    int
		severity float64
		direct   float64
		breadth  int
	}
	var phases []phase
	for cut := 0; cut < h.span; {
		cut += 4 + rng.IntN(40)
		phases = append(phases, phase{
			until:    cut,
			severity: [...]float64{0, 0, 0.1, 0.3, 0.6, 0.9, 1}[rng.IntN(7)],
			direct:   [...]float64{0, 0.3, 0.5, 1, 1}[rng.IntN(5)],
			breadth:  [...]int{1, 2, 3, 4, 8, 8}[rng.IntN(6)],
		})
	}
	cancel := [...]float64{0, 0, 0.05, 0.25}[rng.IntN(4)]

	for offset := range h.span {
		var talk bool
		switch rhythm {
		case 0:
			// 지난 13일에 대화한 날이 목표보다 적을 때만 말한다. 창 안의 대화한 일수가 목표 언저리에서 오르내린다.
			recent := 0
			for back := max(offset-13, 0); back < offset; back++ {
				if talked[back] {
					recent++
				}
			}
			talk = recent < target
			if rng.IntN(7) == 0 {
				talk = !talk
			}
			if rng.IntN(25) == 0 {
				target = 5 + rng.IntN(5)
			}
		case 1:
			// 말하는 토막과 쉬는 토막의 길이를 토막마다 새로 고른다.
			talk = talking
			if left--; left == 0 {
				talking = !talking
				left = 1 + rng.IntN(18)
				if talking {
					left = 1 + rng.IntN(8)
				}
			}
		case 2:
			talk = rng.IntN(12) != 0
		case 3:
			talk = rng.Float64() < density
		default:
			talk = offset%6 < 3
		}
		if silenceFrom >= 0 && offset >= silenceFrom && offset < silenceFrom+silenceDays {
			talk = false
		}
		if !talk {
			continue
		}
		if rng.IntN(60) == 0 {
			h.rows[offset] = nil // 분석을 꺼 둔 날
			continue
		}
		talked[offset] = true

		ph := phases[len(phases)-1]
		for _, candidate := range phases {
			if offset < candidate.until {
				ph = candidate
				break
			}
		}
		severity := ph.severity
		if rng.IntN(30) == 0 {
			severity = 1 - severity // 하루만 뒤집힌 날
		}
		var rows []refRow
		for range 1 + rng.IntN(3)/2 {
			for item := range ph.breadth {
				row := refRow{item: item, status: refNotObserved, direct: rng.Float64() < ph.direct, cancelled: rng.Float64() < cancel}
				switch {
				case rng.Float64() < severity:
					row.status = refObserved
				case rng.IntN(4) == 0:
					row.status, row.direct = refNotMentioned, false
				}
				rows = append(rows, row)
			}
		}
		h.rows[offset] = rows
	}
	return h
}

// difGenParams는 셋 가운데 하나는 기본값을, 나머지는 허용 범위 안에서 고른 값을 돌려준다.
// 변화 탐지의 값에는 이진 소수로 딱 떨어지지 않는 십진수를 섞는다.
func difGenParams(t *testing.T, rng *rand.Rand) params.Params {
	t.Helper()
	p := params.Default()
	switch rng.IntN(3) {
	case 0:
		return p
	case 1:
		// 기본값의 틀은 두고 단계가 자주 움직이게만 한다.
		p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = 2, 4, 9
		p.Stage.SustainedStage2Days = 1 + rng.IntN(8)
		p.Crisis.EscalationMinScore = 4
	default:
		p.Window.Days = 4 + rng.IntN(18)
		p.Window.MinConversationDays = 1 + rng.IntN(p.Window.Days)
		picks := rng.Perm(p.Window.Days)[:3]
		slices.Sort(picks)
		p.Score.ItemScore1MinDays, p.Score.ItemScore2MinDays, p.Score.ItemScore3MinDays = picks[0]+1, picks[1]+1, picks[2]+1
		stages := rng.Perm(16)[:3]
		slices.Sort(stages)
		p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = stages[0]+1, stages[1]+1, stages[2]+1
		p.Stage.SustainedStage2Days = 1 + rng.IntN(16)
		p.Baseline.WindowDays = 1 + rng.IntN(20)
		p.Baseline.MinConversationDays = 1 + rng.IntN(p.Baseline.WindowDays)
		p.Crisis.EscalationMinScore = 1 + rng.IntN(16)
	}
	p.Confidence.MediumMin = [...]float64{0.2, 0.3, 0.4, 0.5}[rng.IntN(4)]
	p.Confidence.HighMin = [...]float64{0.6, 0.7, 0.8, 0.9}[rng.IntN(4)]
	p.CUSUM.K = [...]float64{0, 0.1, 0.3, 0.5, 0.7, 1}[rng.IntN(6)]
	p.CUSUM.H = [...]float64{1, 2.5, 3.3, 4, 4.1, 7}[rng.IntN(6)]
	p.CUSUM.MaxStep = [...]float64{0, 0.7, 1, 1.3, 2, 2.5}[rng.IntN(6)]
	p.CUSUM.MaxS = [...]float64{0, 1.1, 1.5, 2, 2, 3}[rng.IntN(6)]
	require.NoError(t, p.Validate())
	return p
}

// ---------------------------------------------------------------------------
// 견주기
// ---------------------------------------------------------------------------

const difSlack = 1e-9

func difNear(exact *big.Rat, got float64) bool {
	want, _ := exact.Float64()
	return math.Abs(want-got) <= difSlack
}

// difDisagreement는 기록 하나, 기준일 하나를 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
// 실제 구현에는 신호 행을 그대로 넘기므로 하루로 합치는 것부터 개입 단계까지 한 번에 견준다.
func difDisagreement(t *testing.T, h refHistory, asOf int, p params.Params) string {
	t.Helper()
	got, err := assess.EvaluateRows(refToSignalRows(h), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	knobs := difKnobsOf(t, p)
	days := difMerge(h)
	want := difReplay(t, days, asOf, p, knobs)

	var diffs []string
	note := func(where, field string, want, got any) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s %s: 분수로 구한 값 %v, 실제 %v", where, field, want, got))
		}
	}

	// 기준일의 점수, 신뢰도, 기준선
	n, sufficient, total := difScore(days, asOf, p, knobs)
	note("기준일", "대화한 일수", n, got.Score.ConversationDays)
	note("기준일", "기록 부족", !sufficient, got.Score.Insufficient)
	if sufficient {
		note("기준일", "추정 점수", total, got.Score.Total)
	}
	note("기준일", "신뢰도 구간", difLevel(days, asOf, p, knobs), got.Confidence.Level.String())

	base := difBaselineAt(days, asOf, p)
	note("기준선", "잡혔는가", base.established, got.Baseline.Established)
	note("기준선", "첫날이 있는가", base.hasStart, !got.Baseline.Start.IsZero())
	note("기준선", "마지막 날이 있는가", base.hasEnd, !got.Baseline.End.IsZero())
	if base.hasStart {
		note("기준선", "첫날", h.date(base.start), got.Baseline.Start)
	}
	if base.hasEnd {
		note("기준선", "마지막 날", h.date(base.end), got.Baseline.End)
	}
	note("기준선", "대화한 날 수", base.count, got.Baseline.Days)
	note("기준선", "관찰된 항목 수의 합", base.total, got.Baseline.ObservedTotal)

	// 변화 탐지의 날짜별 흐름
	change := difChangeAt(t, days, asOf, p, knobs)
	note("변화 탐지", "돌고 있는가", change.running, got.Change.State.Running)
	note("변화 탐지", "기준일의 감지", change.detected, got.Change.State.Detected)
	if !difNear(change.exact(change.s), got.Change.State.S) {
		diffs = append(diffs, fmt.Sprintf("변화 탐지 기준일의 누적값: 분수로 구한 값 %s, 실제 %v", change.exact(change.s).FloatString(12), got.Change.State.S))
	}
	if len(change.series) != len(got.Change.Series) {
		diffs = append(diffs, fmt.Sprintf("변화 탐지 쌓은 날 수: 분수로 구한 값 %d, 실제 %d", len(change.series), len(got.Change.Series)))
	} else {
		for i, wantDay := range change.series {
			gotDay, where := got.Change.Series[i], "변화 탐지 "+h.date(wantDay.offset).String()
			note(where, "날짜", h.date(wantDay.offset), gotDay.Date)
			note(where, "관찰된 항목 수", wantDay.x, gotDay.Observed)
			note(where, "상한에 걸렸는가", wantDay.capped, gotDay.Capped)
			note(where, "천장에 걸렸는가", wantDay.cut, gotDay.AtCeiling)
			note(where, "감지", wantDay.detected, gotDay.Detected)
			wantStep, wantS := change.exact(wantDay.step), change.exact(wantDay.s)
			if !difNear(wantStep, gotDay.Step) || !difNear(wantS, gotDay.S) {
				diffs = append(diffs, fmt.Sprintf("%s: 분수로 구한 값 더한 값 %s 누적값 %s, 실제 %v %v",
					where, wantStep.FloatString(12), wantS.FloatString(12), gotDay.Step, gotDay.S))
			}
		}
	}

	// 개입 단계의 날짜별 흐름
	if len(want) != len(got.Stage.Series) {
		diffs = append(diffs, fmt.Sprintf("단계 흐름의 길이: 분수로 구한 값 %d, 실제 %d", len(want), len(got.Stage.Series)))
		return strings.Join(diffs, "\n")
	}
	for i, wantPt := range want {
		gotPt, where := got.Stage.Series[i], h.date(wantPt.offset).String()
		note(where, "날짜", h.date(wantPt.offset), gotPt.Date)
		note(where, "그날의 기록이 있는가", wantPt.hasRecord, gotPt.HasRecord)
		note(where, "대화한 일수", wantPt.n, gotPt.ConversationDays)
		note(where, "기록 부족", !wantPt.sufficient, gotPt.Insufficient)
		if wantPt.sufficient {
			note(where, "추정 점수", wantPt.score, gotPt.Score)
		}
		note(where, "신뢰도 구간", wantPt.level, gotPt.Confidence.String())
		note(where, "변화 감지", wantPt.change.detected, gotPt.Detected)
		note(where, "단계", wantPt.stage, int(gotPt.Stage))
		note(where, "2단계 이상이 이어진 일수", wantPt.run, gotPt.ElevatedDays)
		if wantPt.n > 0 {
			note(where, "그날의 값으로 본 단계", wantPt.wanted, int(gotPt.Raw))
		}
		note(where, "묶였는가", len(wantPt.blockers) > 0, gotPt.Held)
		if gotPt.Held && !slices.Contains(wantPt.blockers, gotPt.HeldBy().String()) {
			diffs = append(diffs, fmt.Sprintf("%s 묶인 까닭: 막은 규칙은 %v인데 실제 구현은 %s라고 적었다", where, wantPt.blockers, gotPt.HeldBy()))
		}

		// 지난 날짜의 변화 탐지 상태를 물어도 그 날짜를 기준일로 처음부터 구한 값과 같아야 한다.
		state := got.Change.StateAt(h.date(wantPt.offset))
		note(where, "그 날짜에 변화 탐지가 돌고 있었는가", wantPt.change.running, state.Running)
		note(where, "그 날짜의 감지", wantPt.change.detected, state.Detected)
		if wantS := wantPt.change.exact(wantPt.change.s); !difNear(wantS, state.S) {
			diffs = append(diffs, fmt.Sprintf("%s 그 날짜의 누적값: 분수로 구한 값 %s, 실제 %v", where, wantS.FloatString(12), state.S))
		}
		if len(diffs) > 12 {
			break
		}
	}
	if len(want) > 0 {
		last := want[len(want)-1]
		note("기준일", "단계", last.stage, int(got.Stage.State.Stage))
		note("기준일", "2단계 이상이 이어진 일수", last.run, got.Stage.State.ElevatedDays)
	} else {
		note("기준일", "기록이 없을 때의 단계", 0, int(got.Stage.State.Stage))
	}

	// 위기 관문에 전하는 상태: 점수는 기록이 충분할 때만, 변화 감지는 창 안에 대화한 날이 있을 때만 켜진다.
	gate := got.GateState()
	note("관문에 전하는 상태", "점수가 기준 이상", sufficient && total >= p.Crisis.EscalationMinScore, gate.ScoreElevated)
	note("관문에 전하는 상태", "변화 감지", change.detected && n > 0, gate.ChangeDetected)
	return strings.Join(diffs, "\n")
}

func difFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return difDisagreement(t, c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p, difDisagreement(t, small, smallAsOf, p), small.date(smallAsOf), small.describe())
}

const difHistoryCount = 2000

// ---------------------------------------------------------------------------
// 시험
// ---------------------------------------------------------------------------

func TestDifferentialEvaluate(t *testing.T) {
	t.Run("리듬과 시기가 있는 가상 기록에서 기본값으로 분수 계산과 같은 평가가 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(difHistoryCount) {
			h := difGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa5a5))
			asOf := h.span - 1 + rng.IntN(45)
			if difDisagreement(t, h, asOf, p) != "" {
				difFailShrunk(t, seed, h, asOf, p)
			}
		}
	})

	t.Run("십진수로 적은 조정 값을 섞어도 분수 계산과 같은 평가가 나온다", func(t *testing.T) {
		for seed := range refCount(difHistoryCount) {
			h := difGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x5a5a))
			p := difGenParams(t, rng)
			for _, asOf := range []int{h.span - 1 + rng.IntN(45), rng.IntN(h.span)} {
				if difDisagreement(t, h, asOf, p) != "" {
					difFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("대화한 일수가 기준을 오르내리는 가상 기록에서는 분수 계산과도, 참조 구현과도 같은 평가가 나온다", func(t *testing.T) {
		// 지나간 길은 실제 구현이 적어 둔 값으로 센다. 맞는지를 보는 데 쓰는 것이 아니라 그 자리를 지나갔는지만 본다.
		carriedElevated, sustainedAcrossCarried, emptyWindows := 0, 0, 0
		for seed := range refCount(difHistoryCount / 8) {
			h := difGenUnevenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x0e7f))
			var p params.Params
			switch rng.IntN(4) {
			case 0:
				p = params.Default()
			case 1:
				p = refLivelyParams(t, rng)
			case 2:
				p = refGenParams(t, rng)
			default:
				p = difGenParams(t, rng)
			}
			asOf := h.span - 1 + rng.IntN(40)
			if difDisagreement(t, h, asOf, p) != "" {
				difFailShrunk(t, seed, h, asOf, p)
			}
			if refAssessDisagreement(h, asOf, p) != "" {
				refFailShrunk(t, seed, h, asOf, p)
			}

			got, err := assess.EvaluateRows(refToSignalRows(h), h.date(asOf), p)
			require.NoError(t, err)
			crossedCarried := false
			for _, pt := range got.Stage.Series {
				carried := pt.Insufficient && pt.ConversationDays > 0
				switch {
				case pt.ConversationDays == 0:
					emptyWindows++
					crossedCarried = false
				case carried && pt.Stage >= stage.Suggestion:
					carriedElevated++
					crossedCarried = true
				case pt.Stage < stage.Suggestion:
					crossedCarried = false
				case crossedCarried && slices.Contains(pt.Reasons, stage.ReasonSustained):
					sustainedAcrossCarried++
					crossedCarried = false
				}
			}
		}
		require.Positive(t, carriedElevated, "기록 부족인 날에 2단계 이상을 이어 간 경우")
		require.Positive(t, sustainedAcrossCarried, "기록 부족인 날들을 건너 이어진 일수로 3단계가 된 경우")
		require.Positive(t, emptyWindows, "창이 빈 날")
	})

	t.Run("아무렇게나 흩뿌린 가상 기록에서도 분수 계산과 같은 평가가 나온다", func(t *testing.T) {
		for seed := range refCount(difHistoryCount / 2) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x77aa))
			p := difGenParams(t, rng)
			asOf := h.span - 1 + rng.IntN(30)
			if difDisagreement(t, h, asOf, p) != "" {
				difFailShrunk(t, seed, h, asOf, p)
			}
		}
	})
}

// TestDifferentialProperties는 실제 구현의 결과만 놓고, 기록에서 직접 센 값으로 규칙을 확인한다.
// 두 번째 구현의 단계 계산에는 기대지 않는다. 그쪽이 틀려도 여기서는 드러난다.
func TestDifferentialProperties(t *testing.T) {
	type tally struct {
		rises, risesAfterQuietDays        int
		quietDaysWantingToRise            int
		carriedAboveZero, carriedAboveRaw int
		emptyWindowResets                 int
		cappedDays, ceilingDays           int
		releasedAfterCeiling              int
	}
	var seen tally

	check := func(t *testing.T, seed uint64, h refHistory, p params.Params) {
		t.Helper()
		asOf := h.span + 20
		got, err := assess.EvaluateRows(refToSignalRows(h), h.date(asOf), p)
		require.NoError(t, err)

		recorded := func(offset int) bool { return len(h.rows[offset]) > 0 }
		conversationDays := func(date int) int {
			n := 0
			for offset := date - p.Window.Days + 1; offset <= date; offset++ {
				if recorded(offset) {
					n++
				}
			}
			return n
		}

		previous, previousRun, quietStreak := stage.Everyday, 0, 0
		for i, pt := range got.Stage.Series {
			offset := got.Stage.From.DaysSince(h.date(0)) + i
			n := conversationDays(offset)
			// 기록을 글로 옮기는 것은 실패했을 때뿐이다. 날짜마다 미리 옮기면 이 시험의 시간 대부분이 거기에 들어간다.
			where := refLazy(func() string { return fmt.Sprintf("씨앗 %d, %s\n%s", seed, pt.Date, h.describe()) })

			if !recorded(offset) {
				require.LessOrEqual(t, pt.Stage, previous, "그날의 기록이 없는 날에 단계가 올랐다: %s", where)
				if pt.Raw > previous && n >= p.Window.MinConversationDays {
					seen.quietDaysWantingToRise++
				}
				quietStreak++
			}
			require.LessOrEqual(t, pt.Stage, previous+1, "하루에 두 단계 넘게 올랐다: %s", where)

			switch {
			case n == 0:
				require.Equal(t, stage.Everyday, pt.Stage, "창이 비었는데 0단계가 아니다: %s", where)
				require.Zero(t, pt.ElevatedDays, "창이 비었는데 이어진 일수가 남았다: %s", where)
				if previous > stage.Everyday {
					seen.emptyWindowResets++
				}
			case n < p.Window.MinConversationDays:
				require.Equal(t, previous, pt.Stage, "기록 부족인 날에 단계가 전날과 다르다: %s", where)
				require.Equal(t, previousRun, pt.ElevatedDays, "기록 부족인 날에 이어진 일수가 움직였다: %s", where)
				if pt.Stage > stage.Everyday {
					seen.carriedAboveZero++
				}
				if pt.Stage > pt.Raw {
					seen.carriedAboveRaw++
				}
			}

			if pt.Stage > previous {
				seen.rises++
				if quietStreak > 0 {
					seen.risesAfterQuietDays++
				}
			}
			if recorded(offset) {
				quietStreak = 0
			}
			previous, previousRun = pt.Stage, pt.ElevatedDays
		}

		// 누적값은 천장을 넘지 못하고, 하루에 상한보다 많이 늘지 못한다.
		// 보여주는 값은 가장 가까운 소수라서, 십진수로 읽은 경계와 견줄 때 끝자리만큼은 봐준다.
		knobs := difKnobsOf(t, p)
		before, wasAtCeiling := 0.0, false
		for _, point := range got.Change.Series {
			where := refLazy(func() string { return fmt.Sprintf("씨앗 %d, %s", seed, point.Date) })
			if knobs.maxStep != nil {
				limit, _ := knobs.maxStep.Float64()
				require.LessOrEqual(t, point.Step, limit+difSlack, "하루에 더한 값이 상한을 넘었다: %s", where)
				require.LessOrEqual(t, point.S-before, limit+difSlack, "누적값이 하루에 상한보다 많이 늘었다: %s", where)
			}
			if knobs.ceiling != nil {
				limit, _ := knobs.ceiling.Float64()
				require.LessOrEqual(t, point.S, limit+difSlack, "누적값이 천장을 넘었다: %s", where)
			}
			require.GreaterOrEqual(t, point.S, 0.0, "누적값이 0 아래로 내려갔다: %s", where)
			if point.Capped {
				seen.cappedDays++
			}
			if point.AtCeiling {
				seen.ceilingDays++
				wasAtCeiling = true
			}
			if wasAtCeiling && !point.Detected {
				seen.releasedAfterCeiling++
				wasAtCeiling = false
			}
			before = point.S
		}
	}

	t.Run("단계는 기록 없는 날에 오르지 않고, 하루에 한 단계만 오르고, 기록 부족인 날에는 전날과 같고, 누적값은 상한과 천장을 지킨다", func(t *testing.T) {
		for seed := range refCount(difHistoryCount) {
			rng := rand.New(rand.NewPCG(seed, 0x9e37))
			check(t, seed, difGenHistory(t, seed), difGenParams(t, rng))
			check(t, seed, difGenHistory(t, seed), params.Default())
		}
		for seed := range refCount(difHistoryCount / 2) {
			rng := rand.New(rand.NewPCG(seed, 0x9e38))
			check(t, seed, refGenHistory(t, seed), difGenParams(t, rng))
		}
		for seed := range refCount(difHistoryCount / 2) {
			rng := rand.New(rand.NewPCG(seed, 0x9e39))
			check(t, seed, difGenUnevenHistory(t, seed), difGenParams(t, rng))
		}

		// 한 번도 지나가지 않은 길이 있으면 위의 확인은 그 규칙에 대해 아무것도 보지 않은 것이다.
		t.Logf("지나간 길: %+v", seen)
		require.Positive(t, seen.rises, "단계가 오른 날")
		require.Positive(t, seen.risesAfterQuietDays, "대화 없는 날들을 지나 다음 대화 날에 오른 경우")
		require.Positive(t, seen.quietDaysWantingToRise, "대화 없는 날에 그날의 값으로는 오를 만했던 경우")
		require.Positive(t, seen.carriedAboveZero, "기록 부족인 날에 0단계보다 높은 단계를 이어 간 경우")
		require.Positive(t, seen.carriedAboveRaw, "기록 부족이 아니었다면 내려갔을 단계를 이어 간 경우")
		require.Positive(t, seen.emptyWindowResets, "창이 비어 0단계로 돌아간 경우")
		require.Positive(t, seen.cappedDays, "하루 상한에 걸린 날")
		require.Positive(t, seen.ceilingDays, "천장에 걸린 날")
		require.Positive(t, seen.releasedAfterCeiling, "천장에 닿았다가 감지가 풀린 경우")
	})
}
