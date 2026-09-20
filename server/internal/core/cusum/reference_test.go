package cusum_test

// 이 파일은 변화 탐지의 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 참조 구현은 분수로 정확하게 쌓아서, 한계값을 넘었는지를 오차 없이 안다. 실제 구현이 돌려주는 누적값은
// 보여주기 위해 소수(float64)로 옮긴 값이라 아주 작은 차이는 봐주지만, 감지 여부는 한 치도 봐주지 않는다.
// 지난 날짜의 상태도 이어 쓰지 않고 그 날짜를 기준일로 삼아 기준선부터 다시 구한다. 느리지만 틀리기 어렵다.
// 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ---------------------------------------------------------------------------
// 가상 기록
// ---------------------------------------------------------------------------

// 참조 구현은 판단을 패키지의 타입이 아니라 맨 숫자로 든다.
// 같은 타입과 같은 크기 비교를 빌려 쓰면 두 구현이 같은 실수를 함께 하게 된다.
const (
	refNotMentioned = 0
	refNotObserved  = 1
	refObserved     = 2

	refItems = 8
)

// refRow는 대화 하나에서 항목 하나에 대해 나온 판단이다.
type refRow struct {
	item      int // 0부터 7, 항목의 정해진 순서
	status    int
	direct    bool // 언급 없음에는 뜻이 없다
	cancelled bool
}

// refDay는 하루로 합친 판단이다. 날짜는 기록의 첫날부터 센 날 수로만 든다.
type refDay struct {
	offset int
	status [refItems]int
	direct [refItems]bool
}

// refHistory는 가상 인물 한 사람의 기록이다.
type refHistory struct {
	base recorddate.Date
	// span은 기록이 놓일 수 있는 날 수다(1부터 120).
	span int
	// rows의 열쇠는 base부터 센 날 수다. 행이 없는 날짜도 열쇠로는 있을 수 있다(분석을 꺼 둔 날).
	rows map[int][]refRow
}

func (h refHistory) date(offset int) recorddate.Date {
	return h.base.AddDays(offset)
}

func (h refHistory) offsets() []int {
	offsets := make([]int, 0, len(h.rows))
	for offset := range h.rows {
		offsets = append(offsets, offset)
	}
	slices.Sort(offsets)
	return offsets
}

func (h refHistory) clone() refHistory {
	out := refHistory{base: h.base, span: h.span, rows: make(map[int][]refRow, len(h.rows))}
	for offset, rows := range h.rows {
		out.rows[offset] = slices.Clone(rows)
	}
	return out
}

// describe는 어긋난 기록을 사람이 읽을 수 있게 적는다. 한 줄이 하루이고, 행은 "항목=판단" 꼴이다.
// O 관찰됨(직접), o 관찰됨(간접), X 관찰되지 않음(직접), x 관찰되지 않음(간접), . 언급 없음, 뒤의 !는 취소된 행이다.
func (h refHistory) describe() string {
	var b strings.Builder
	for _, offset := range h.offsets() {
		fmt.Fprintf(&b, "  %s (첫날+%d):", h.date(offset), offset)
		for _, row := range h.rows[offset] {
			mark := "."
			switch {
			case row.status == refObserved && row.direct:
				mark = "O"
			case row.status == refObserved:
				mark = "o"
			case row.status == refNotObserved && row.direct:
				mark = "X"
			case row.status == refNotObserved:
				mark = "x"
			}
			if row.cancelled {
				mark += "!"
			}
			fmt.Fprintf(&b, " %s=%s", signal.AllItems()[row.item], mark)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// refGenHistory는 씨앗 하나에서 가상 인물 한 사람의 기록을 만든다.
// 대화 빈도, 항목별 빈도, 취소, 긴 침묵, 서서히 나빠짐, 하루만 크게 나쁨을 씨앗마다 다르게 섞는다.
func refGenHistory(t *testing.T, seed uint64) refHistory {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0x5eed))

	// 달과 해가 바뀌는 곳, 윤일을 지나가도록 첫날을 여러 곳에 둔다.
	bases := [...]string{"2026-03-01", "2024-02-20", "2025-12-10", "2027-06-15", "2026-10-25"}
	base, err := recorddate.Parse(bases[rng.IntN(len(bases))])
	require.NoError(t, err)

	h := refHistory{base: base, span: 1 + rng.IntN(120), rows: map[int][]refRow{}}

	density := [...]float64{0.12, 0.35, 0.55, 0.8, 1}[rng.IntN(5)]
	mention := rng.Float64() * 0.7
	direct := [...]float64{0, 0.3, 0.6, 0.9, 1}[rng.IntN(5)]
	cancel := [...]float64{0, 0, 0.05, 0.3}[rng.IntN(4)]

	// 사람마다 전반적인 수준이 다르고, 그 안에서 항목마다 또 다르다.
	level := rng.Float64()
	var observe [refItems]float64
	for item := range observe {
		observe[item] = level * rng.Float64()
	}
	slope := 0.0
	if rng.IntN(3) == 0 {
		slope = rng.Float64()
	}
	badDay := -1
	if rng.IntN(4) == 0 {
		badDay = rng.IntN(h.span)
	}
	var silentFrom, silentTo []int
	for range rng.IntN(3) {
		from := rng.IntN(h.span)
		silentFrom = append(silentFrom, from)
		silentTo = append(silentTo, from+5+rng.IntN(41))
	}

	for offset := range h.span {
		silent := false
		for i := range silentFrom {
			if offset >= silentFrom[i] && offset < silentTo[i] {
				silent = true
			}
		}
		if silent {
			continue
		}
		if offset != badDay && rng.Float64() >= density {
			continue
		}
		if rng.IntN(40) == 0 {
			// 분석을 꺼 둔 날: 날짜는 있지만 신호 행이 없다.
			h.rows[offset] = nil
			continue
		}

		conversations := 1
		if rng.IntN(4) == 0 {
			conversations += 1 + rng.IntN(2)
		}
		var rows []refRow
		for range conversations {
			for item := range refItems {
				p := observe[item] + slope*float64(offset)/float64(h.span)
				if offset == badDay {
					p = 0.9
				}
				row := refRow{item: item, direct: rng.Float64() < direct, cancelled: rng.Float64() < cancel}
				roll, mentionRoll, blankRoll := rng.Float64(), rng.Float64(), rng.IntN(10)
				switch {
				case roll < p:
					row.status = refObserved
				case mentionRoll < mention:
					row.status = refNotObserved
				case blankRoll == 0:
					// 언급 없음을 행으로 남기는 경우도 있다.
					row.status = refNotMentioned
					row.direct = false
				default:
					continue
				}
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			// 대화는 했지만 어느 항목도 나오지 않은 날이다.
			rows = append(rows, refRow{item: rng.IntN(refItems), status: refNotMentioned})
		}
		h.rows[offset] = rows
	}
	return h
}

// refShrink는 두 구현이 어긋나는 기록을, 어긋남이 남아 있는 한 가장 작게 줄인다.
// 날을 빼 보고, 행을 빼 보고, 판단을 약하게 바꿔 보고, 기준일과 날짜를 앞으로 당겨 본다.
// 받아들인 변경은 언제나 기록을 더 작게 만들기 때문에 반드시 끝난다.
func refShrink(h refHistory, asOf int, disagrees func(refHistory, int) bool) (refHistory, int) {
	for changed := true; changed; {
		changed = false

		for _, offset := range h.offsets() {
			candidate := h.clone()
			delete(candidate.rows, offset)
			if disagrees(candidate, asOf) {
				h, changed = candidate, true
			}
		}

		for _, offset := range h.offsets() {
			for i := len(h.rows[offset]) - 1; i >= 0 && len(h.rows[offset]) > 1; i-- {
				candidate := h.clone()
				candidate.rows[offset] = slices.Delete(candidate.rows[offset], i, i+1)
				if disagrees(candidate, asOf) {
					h, changed = candidate, true
				}
			}
		}

		for _, offset := range h.offsets() {
			for i := range h.rows[offset] {
				for _, weaken := range []func(*refRow) bool{
					func(r *refRow) bool { was := r.cancelled; r.cancelled = false; return was },
					func(r *refRow) bool { was := r.direct; r.direct = false; return was },
					func(r *refRow) bool {
						if r.status == refNotMentioned {
							return false
						}
						r.status--
						if r.status == refNotMentioned {
							r.direct = false
						}
						return true
					},
				} {
					candidate := h.clone()
					if weaken(&candidate.rows[offset][i]) && disagrees(candidate, asOf) {
						h, changed = candidate, true
					}
				}
			}
		}

		if asOf > 0 && disagrees(h, asOf-1) {
			asOf, changed = asOf-1, true
		}

		if offsets := h.offsets(); len(offsets) > 0 && offsets[0] > 0 && asOf > 0 {
			candidate := refHistory{base: h.base, span: h.span, rows: map[int][]refRow{}}
			for offset, rows := range h.rows {
				candidate.rows[offset-1] = slices.Clone(rows)
			}
			if disagrees(candidate, asOf-1) {
				h, asOf, changed = candidate, asOf-1, true
			}
		}
	}
	return h, asOf
}

// ---------------------------------------------------------------------------
// 참조 구현: 하루로 합치기
// ---------------------------------------------------------------------------

// refMerge는 날짜를 첫날부터 하나씩 짚어 가며 그날의 행을 항목별 판단 하나로 합친다.
//
//   - 취소된 행은 없는 것으로 친다.
//   - 관찰된 행이 하나라도 있으면 관찰됨이고, 그중 하나라도 직접 언급이면 직접 언급이다.
//   - 관찰된 행이 없고 관찰되지 않은 행이 있으면 관찰되지 않음이다.
//   - 아무 행도 남지 않은 항목은 언급 없음이다.
//   - 행이 하나도 없는 날짜는 대화하지 않은 날이다. 행이 모두 취소된 날은 대화한 날이다.
func refMerge(h refHistory) []refDay {
	days := []refDay{}
	for _, offset := range h.offsets() {
		rows := h.rows[offset]
		if len(rows) == 0 {
			continue
		}
		day := refDay{offset: offset}
		for item := range refItems {
			anyObserved, observedDirect := false, false
			anyNotObserved, notObservedDirect := false, false
			for _, row := range rows {
				if row.cancelled || row.item != item {
					continue
				}
				if row.status == refObserved {
					anyObserved = true
					observedDirect = observedDirect || row.direct
				}
				if row.status == refNotObserved {
					anyNotObserved = true
					notObservedDirect = notObservedDirect || row.direct
				}
			}
			switch {
			case anyObserved:
				day.status[item], day.direct[item] = refObserved, observedDirect
			case anyNotObserved:
				day.status[item], day.direct[item] = refNotObserved, notObservedDirect
			}
		}
		days = append(days, day)
	}
	return days
}

// ---------------------------------------------------------------------------
// 두 구현을 잇는 다리
// ---------------------------------------------------------------------------

// refToSignalDays는 참조 구현이 합친 하루를 실제 구현의 입력으로 옮긴다.
// 실제 구현의 합치기를 거치지 않으므로, 합치기가 틀려도 여기 시험은 영향받지 않는다.
func refToSignalDays(h refHistory, days []refDay) []signal.Day {
	out := make([]signal.Day, 0, len(days))
	for _, day := range days {
		converted := signal.Day{Date: h.date(day.offset)}
		for item, id := range signal.AllItems() {
			var j signal.Judgement
			switch day.status[item] {
			case refObserved:
				j.Status = signal.Observed
			case refNotObserved:
				j.Status = signal.NotObserved
			}
			switch {
			case day.status[item] == refNotMentioned:
			case day.direct[item]:
				j.Explicitness = signal.Direct
			default:
				j.Explicitness = signal.Indirect
			}
			converted.Judgements[id.Index()] = j
		}
		out = append(out, converted)
	}
	return out
}

// refGenParams는 절반은 기본값을, 절반은 허용 범위 안에서 아무렇게나 고른 조정 값을 돌려준다.
// 기본값에서만 맞는 구현(14나 7을 어딘가에 직접 적어 둔 구현)을 잡아내려는 것이다.
func refGenParams(t *testing.T, rng *rand.Rand) params.Params {
	t.Helper()
	p := params.Default()
	if rng.IntN(2) == 0 {
		return p
	}

	// 1부터 limit까지에서 서로 다른 값을 count개 골라 작은 것부터 돌려준다.
	ascending := func(count, limit int) []int {
		picks := rng.Perm(limit)[:count]
		slices.Sort(picks)
		for i := range picks {
			picks[i]++
		}
		return picks
	}

	p.Window.Days = 3 + rng.IntN(19)
	p.Window.MinConversationDays = 1 + rng.IntN(p.Window.Days)
	item := ascending(3, p.Window.Days)
	p.Score.ItemScore1MinDays, p.Score.ItemScore2MinDays, p.Score.ItemScore3MinDays = item[0], item[1], item[2]
	band := ascending(4, 24)
	p.Score.MildMin, p.Score.ModerateMin, p.Score.ModeratelySevereMin, p.Score.SevereMin = band[0], band[1], band[2], band[3]

	p.Confidence.MediumMin = [...]float64{0.2, 0.25, 0.3, 0.4, 0.5}[rng.IntN(5)]
	p.Confidence.HighMin = [...]float64{0.6, 0.7, 0.75, 0.8, 1}[rng.IntN(5)]

	p.Baseline.WindowDays = 1 + rng.IntN(20)
	p.Baseline.MinConversationDays = 1 + rng.IntN(p.Baseline.WindowDays)

	p.CUSUM.K = [...]float64{0, 0.25, 0.5, 1}[rng.IntN(4)]
	p.CUSUM.H = [...]float64{1, 2.5, 4, 7}[rng.IntN(4)]
	p.CUSUM.MaxStep = [...]float64{0, 0, 0.75, 1, 2}[rng.IntN(5)]
	p.CUSUM.MaxS = [...]float64{0, 0, 1.25, 1.5, 2, 3}[rng.IntN(6)]

	stage := ascending(3, 24)
	p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = stage[0], stage[1], stage[2]
	p.Stage.SustainedStage2Days = 1 + rng.IntN(20)

	p.Crisis.EscalationMinScore = 1 + rng.IntN(24)
	p.Trend.MinDifferencePercent = 1 + rng.IntN(60)

	require.NoError(t, p.Validate())
	return p
}

// refAsOfPicks는 기록 하나에서 견줘 볼 기준일을 고른다.
// 기록의 마지막 날, 기록 안의 아무 날 둘, 그리고 기록이 끝난 뒤 길게 말이 없는 어느 날이다.
func refAsOfPicks(rng *rand.Rand, h refHistory) []int {
	return []int{h.span - 1, rng.IntN(h.span), rng.IntN(h.span), h.span + rng.IntN(50)}
}

// ---------------------------------------------------------------------------
// 참조 구현: 개인 기준선
// ---------------------------------------------------------------------------

type refBaselineResult struct {
	hasStart bool
	start    int
	hasEnd   bool
	end      int

	established bool
	extended    bool

	days          int
	observedTotal int
	itemObserved  [refItems]int
	// trendObserved는 추세 화면의 세 줄이다: 기분(흥미 저하나 우울감 가운데 하나라도), 수면, 에너지(피로).
	trendObserved [3]int
}

// refBaseline은 규칙을 글자 그대로 옮긴다.
//
//   - 기준일까지 알 수 있었던 날만 본다.
//   - 기간은 첫 대화 날부터 달력으로 Baseline.WindowDays일이다(첫날 포함).
//   - 그 안에 대화한 날이 Baseline.MinConversationDays일이 안 되면, 그만큼째 대화 날까지 기간을 늘린다.
//   - 기간의 마지막 날이 지난 뒤(기준일이 마지막 날보다 뒤)에 기준선이 잡힌다.
//   - 평소의 하루 평균은 기간 안의 대화한 날들에서 "그날 관찰된 항목 수"의 평균이다. 항목별 관찰 비율도 같은 날들로 센다.
func refBaseline(days []refDay, asOf int, p params.Params) refBaselineResult {
	var known []refDay
	for _, day := range days {
		if day.offset <= asOf {
			known = append(known, day)
		}
	}
	slices.SortFunc(known, func(a, b refDay) int { return a.offset - b.offset })

	var r refBaselineResult
	if len(known) == 0 {
		return r
	}
	r.hasStart, r.start = true, known[0].offset

	windowEnd := r.start + p.Baseline.WindowDays - 1
	inWindow := 0
	for _, day := range known {
		if day.offset <= windowEnd {
			inWindow++
		}
	}
	switch {
	case inWindow >= p.Baseline.MinConversationDays:
		r.hasEnd, r.end = true, windowEnd
	case len(known) >= p.Baseline.MinConversationDays:
		r.hasEnd, r.end, r.extended = true, known[p.Baseline.MinConversationDays-1].offset, true
	default:
		// 마지막 날은 아직 모른다. 처음 기간이 다 지났다면 늘어난다는 것만은 정해졌다.
		r.extended = asOf > windowEnd
	}
	r.established = r.hasEnd && asOf > r.end

	for _, day := range known {
		if r.hasEnd && day.offset > r.end {
			continue
		}
		r.days++
		for item := range refItems {
			if day.status[item] == refObserved {
				r.observedTotal++
				r.itemObserved[item]++
			}
		}
		if day.status[0] == refObserved || day.status[1] == refObserved {
			r.trendObserved[0]++
		}
		if day.status[2] == refObserved {
			r.trendObserved[1]++
		}
		if day.status[3] == refObserved {
			r.trendObserved[2]++
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// 참조 구현: 변화 탐지
// ---------------------------------------------------------------------------

type refCusumPoint struct {
	offset  int
	x       int
	rawStep *big.Rat // 상한을 적용하기 전의 x − μ − K
	step    *big.Rat
	uncut   *big.Rat // 천장에 맞추기 전의 누적값
	s       *big.Rat
}

type refCusumResult struct {
	running bool
	from    int
	points  []refCusumPoint
	s       *big.Rat
}

// refCusum은 규칙을 글자 그대로, 분수로 정확하게 옮긴다.
//
//   - 기준일에 기준선이 잡혀 있지 않으면 돌리지 않는다.
//   - 기준선 기간의 마지막 날 다음 날부터, 대화한 날마다 S = max(0, S + (x − μ − K))로 쌓는다.
//   - 대화하지 않은 날은 건너뛰고 S를 그대로 둔다.
//   - MaxStep이 0보다 크면 하루에 더하는 값을 MaxStep까지만 인정한다.
//   - MaxS가 0보다 크면 그날의 값을 더한 뒤의 S는 MaxS × H를 넘지 못한다.
//   - S가 H를 넘으면(같으면 아니다) 변화 감지다. 넘은 뒤에도 S를 되돌리지 않는다.
func refCusum(days []refDay, asOf int, p params.Params) refCusumResult {
	base := refBaseline(days, asOf, p)
	r := refCusumResult{s: new(big.Rat)}
	if !base.established {
		return r
	}
	r.running, r.from = true, base.end+1

	mu := big.NewRat(int64(base.observedTotal), int64(base.days))
	k := new(big.Rat).SetFloat64(p.CUSUM.K)
	maxStep := new(big.Rat).SetFloat64(p.CUSUM.MaxStep)
	ceiling := refCeiling(p)

	for date := r.from; date <= asOf; date++ {
		for _, day := range days {
			if day.offset != date {
				continue
			}
			x := 0
			for item := range refItems {
				if day.status[item] == refObserved {
					x++
				}
			}
			raw := new(big.Rat).SetInt64(int64(x))
			raw.Sub(raw, mu).Sub(raw, k)
			step := new(big.Rat).Set(raw)
			if maxStep.Sign() > 0 && step.Cmp(maxStep) > 0 {
				step.Set(maxStep)
			}
			uncut := new(big.Rat).Add(r.s, step)
			if uncut.Sign() < 0 {
				uncut.SetInt64(0)
			}
			s := new(big.Rat).Set(uncut)
			if ceiling.Sign() > 0 && s.Cmp(ceiling) > 0 {
				s.Set(ceiling)
			}
			r.s = s
			r.points = append(r.points, refCusumPoint{offset: date, x: x, rawStep: raw, step: step, uncut: uncut, s: s})
		}
	}
	return r
}

// refCeiling은 누적값의 천장(MaxS × H)이다. 천장을 두지 않으면 0이다.
// 시험에서 고르는 MaxS와 H는 모두 이진 소수로 딱 떨어지는 값이라 소수에서 곧바로 분수로 옮겨도 정확하다.
func refCeiling(p params.Params) *big.Rat {
	ceiling := new(big.Rat).SetFloat64(p.CUSUM.MaxS)
	return ceiling.Mul(ceiling, new(big.Rat).SetFloat64(p.CUSUM.H))
}

// 소수 계산의 오차로 볼 수 있는 가장 큰 차이다. 분모가 백 남짓인 분수끼리는 같지 않으면 이보다 훨씬 크게 벌어진다.
const refFloatSlack = 1e-9

func refNear(exact *big.Rat, got float64) bool {
	want, _ := exact.Float64()
	return math.Abs(want-got) <= refFloatSlack
}

// refStateDiff는 어느 날짜의 상태를 견준다. 누적값이 한계값과 정확히 같은 날의 감지 여부는 onBoundary로 따로 넘긴다.
func refStateDiff(name string, want refCusumResult, got cusum.State, p params.Params, onBoundary func(detected bool)) []string {
	var diffs []string
	if want.running != got.Running {
		diffs = append(diffs, fmt.Sprintf("%s 돌고 있는가: 참조 %v, 실제 %v", name, want.running, got.Running))
	}
	if !refNear(want.s, got.S) {
		diffs = append(diffs, fmt.Sprintf("%s 누적값: 참조 %s, 실제 %v", name, want.s.FloatString(12), got.S))
	}
	h := new(big.Rat).SetFloat64(p.CUSUM.H)
	switch cmp := want.s.Cmp(h); {
	case cmp == 0:
		onBoundary(got.Detected)
	case (cmp > 0) != got.Detected:
		diffs = append(diffs, fmt.Sprintf("%s 변화 감지: 참조 %v, 실제 %v (누적값 %s)", name, cmp > 0, got.Detected, want.s.FloatString(12)))
	}
	return diffs
}

// refRunProduction은 실제 구현을 쓰는 쪽과 같은 순서로 부른다: 같은 기록으로 기준선을 구하고, 그 기준선으로 흐름을 구한다.
func refRunProduction(h refHistory, days []refDay, asOf int, p params.Params) (cusum.Result, error) {
	converted := refToSignalDays(h, days)
	base, err := baseline.Compute(converted, h.date(asOf), p)
	if err != nil {
		return cusum.Result{}, err
	}
	return cusum.Run(converted, base, p)
}

// refCusumDisagreement는 기록 하나, 기준일 하나의 흐름을 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refCusumDisagreement(h refHistory, asOf int, p params.Params, onBoundary func(bool)) string {
	days := refMerge(h)
	want := refCusum(days, asOf, p)
	got, err := refRunProduction(h, days, asOf, p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}

	var diffs []string
	if got.AsOf != h.date(asOf) {
		diffs = append(diffs, fmt.Sprintf("기준일: 참조 %s, 실제 %s", h.date(asOf), got.AsOf))
	}
	wantFrom := recorddate.Date{}
	if want.running {
		wantFrom = h.date(want.from)
	}
	if got.From != wantFrom {
		diffs = append(diffs, fmt.Sprintf("쌓기 시작하는 날: 참조 %s, 실제 %s", wantFrom, got.From))
	}
	if got.Series == nil {
		diffs = append(diffs, "흐름이 nil이다. 쌓은 날이 없어도 길이 0인 목록이어야 한다")
	}
	if len(got.Series) != len(want.points) {
		diffs = append(diffs, fmt.Sprintf("쌓은 날 수: 참조 %d, 실제 %d", len(want.points), len(got.Series)))
		return strings.Join(diffs, "\n")
	}

	maxStep := new(big.Rat).SetFloat64(p.CUSUM.MaxStep)
	ceiling := refCeiling(p)
	hLimit := new(big.Rat).SetFloat64(p.CUSUM.H)
	for i, wantPoint := range want.points {
		gotPoint := got.Series[i]
		name := h.date(wantPoint.offset).String()
		if gotPoint.Date != h.date(wantPoint.offset) || gotPoint.Observed != wantPoint.x {
			diffs = append(diffs, fmt.Sprintf("%s: 참조 x=%d, 실제 %s x=%d", name, wantPoint.x, gotPoint.Date, gotPoint.Observed))
		}
		if !refNear(wantPoint.step, gotPoint.Step) {
			diffs = append(diffs, fmt.Sprintf("%s 더한 값: 참조 %s, 실제 %v", name, wantPoint.step.FloatString(12), gotPoint.Step))
		}
		if !refNear(wantPoint.s, gotPoint.S) {
			diffs = append(diffs, fmt.Sprintf("%s 누적값: 참조 %s, 실제 %v", name, wantPoint.s.FloatString(12), gotPoint.S))
		}
		// 상한과 정확히 같은 값은 묶인 것이 아니다. 소수 오차로 갈릴 수 있는 자리라 여기서는 견주지 않는다.
		if capped := maxStep.Sign() > 0 && wantPoint.rawStep.Cmp(maxStep) > 0; wantPoint.rawStep.Cmp(maxStep) != 0 && capped != gotPoint.Capped {
			diffs = append(diffs, fmt.Sprintf("%s 상한에 걸렸는가: 참조 %v, 실제 %v", name, capped, gotPoint.Capped))
		}
		// 천장과 정확히 같은 값은 잘린 것이 아니다. 분수로 견주므로 같은 날도 그대로 견준다.
		if atCeiling := ceiling.Sign() > 0 && wantPoint.uncut.Cmp(ceiling) > 0; atCeiling != gotPoint.AtCeiling {
			diffs = append(diffs, fmt.Sprintf("%s 천장에 걸렸는가: 참조 %v, 실제 %v", name, atCeiling, gotPoint.AtCeiling))
		}
		switch cmp := wantPoint.s.Cmp(hLimit); {
		case cmp == 0:
			onBoundary(gotPoint.Detected)
		case (cmp > 0) != gotPoint.Detected:
			diffs = append(diffs, fmt.Sprintf("%s 변화 감지: 참조 %v, 실제 %v", name, cmp > 0, gotPoint.Detected))
		}
	}
	diffs = append(diffs, refStateDiff("기준일", want, got.State, p, func(bool) {})...)
	if got.State != got.StateAt(h.date(asOf)) {
		diffs = append(diffs, "State가 StateAt(AsOf)와 다르다")
	}
	return strings.Join(diffs, "\n")
}

// refReplayDisagreement는 지난 날짜의 상태를 물은 값이, 그 날짜를 기준일로 삼아 처음부터 다시 구한 값과 같은지 본다.
func refReplayDisagreement(h refHistory, asOf int, p params.Params, onBoundary func(bool)) string {
	days := refMerge(h)
	got, err := refRunProduction(h, days, asOf, p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	var diffs []string
	for date := 0; date <= asOf; date++ {
		diffs = append(diffs, refStateDiff(h.date(date).String(), refCusum(days, date, p), got.StateAt(h.date(date)), p, onBoundary)...)
	}
	// 기준일보다 뒤를 물으면 기준일의 상태다. 빈 날짜와 첫 기록보다 앞선 날짜는 돌기 전이다.
	if got.StateAt(h.date(asOf+5)) != got.State {
		diffs = append(diffs, "기준일 뒤의 날짜를 물었는데 기준일의 상태가 아니다")
	}
	if got.StateAt(recorddate.Date{}) != (cusum.State{}) || got.StateAt(h.date(-1)) != (cusum.State{}) {
		diffs = append(diffs, "기록보다 앞선 날짜인데 빈 상태가 아니다")
	}
	return strings.Join(diffs, "\n")
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params, disagreement func(refHistory, int, params.Params, func(bool)) string) {
	t.Helper()
	ignore := func(bool) {}
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return disagreement(c, a, p, ignore) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p.Baseline, p.CUSUM, disagreement(small, smallAsOf, p, ignore), small.date(smallAsOf), small.describe())
}

const refHistoryCount = 3000

// refCount는 -short로 돌릴 때 돌려 볼 수를 십분의 일로 줄인다. 손으로 자주 돌릴 때를 위한 것이고, 평소에는 전부 돌린다.
func refCount(n int) uint64 {
	if testing.Short() {
		n = max(n/10, 1)
	}
	return uint64(n)
}

// ---------------------------------------------------------------------------
// 시험
// ---------------------------------------------------------------------------

func TestReferenceCusum(t *testing.T) {
	// 누적값이 한계값과 정확히 같았던 날에 실제 구현이 감지라고 답한 횟수를 센다.
	// 규칙은 "넘으면"이므로 같은 날은 감지가 아니어야 한다.
	boundaryDays, boundaryDetected := 0, 0
	onBoundary := func(detected bool) {
		boundaryDays++
		if detected {
			boundaryDetected++
		}
	}

	t.Run("가상 기록 수천 개에서 기본값으로 참조 구현과 같은 흐름이 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			for _, asOf := range refAsOfPicks(rng, h) {
				if refCusumDisagreement(h, asOf, p, onBoundary) != "" {
					refFailShrunk(t, seed, h, asOf, p, refCusumDisagreement)
				}
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 흐름이 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			for _, asOf := range refAsOfPicks(rng, h) {
				if refCusumDisagreement(h, asOf, p, onBoundary) != "" {
					refFailShrunk(t, seed, h, asOf, p, refCusumDisagreement)
				}
			}
		}
	})

	t.Run("지난 날짜의 상태는 그 날짜를 기준일로 처음부터 다시 구한 상태와 같다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount / 10) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x5e71))
			p := refGenParams(t, rng)
			asOf := h.span - 1 + rng.IntN(20)
			if refReplayDisagreement(h, asOf, p, onBoundary) != "" {
				refFailShrunk(t, seed, h, asOf, p, refReplayDisagreement)
			}
		}
	})

	// 아무렇게나 만든 기록에서도 같은 날은 드물지 않다. 일부러 만든 기록은 TestReferenceCusumBoundary가 따로 본다.
	t.Logf("가상 기록에서 누적값이 한계값과 정확히 같았던 날: %d번, 그중 실제 구현이 감지로 답한 날: %d번", boundaryDays, boundaryDetected)
	assert.Zero(t, boundaryDetected, "누적값이 한계값과 같은 날은 감지가 아니다")
}

func TestReferenceCusumProperties(t *testing.T) {
	run := func(t *testing.T, h refHistory, days []refDay, asOf int, p params.Params) cusum.Result {
		t.Helper()
		got, err := refRunProduction(h, days, asOf, p)
		require.NoError(t, err)
		return got
	}

	t.Run("같은 기록에서는 몇 번을 계산해도 같은 값이고 받은 목록을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xd1ce))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span + 20)
			days := refToSignalDays(h, refMerge(h))
			before := slices.Clone(days)
			base, err := baseline.Compute(days, h.date(asOf), p)
			require.NoError(t, err)

			first, err := cusum.Run(days, base, p)
			require.NoError(t, err)
			for range 3 {
				again, err := cusum.Run(days, base, p)
				require.NoError(t, err)
				require.Equal(t, first, again, "씨앗 %d", seed)
			}
			require.Equal(t, before, days, "씨앗 %d", seed)
		}
	})

	t.Run("기준일보다 뒤의 날은 결과를 바꾸지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xf00d))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span)
			all := refMerge(h)
			var upToAsOf []refDay
			for _, day := range all {
				if day.offset <= asOf {
					upToAsOf = append(upToAsOf, day)
				}
			}
			require.Equal(t, run(t, h, all, asOf, p), run(t, h, upToAsOf, asOf, p), "씨앗 %d", seed)
		}
	})

	t.Run("기준일을 뒤로 옮기면 앞의 흐름은 그대로이고 뒤에 이어 붙기만 한다", func(t *testing.T) {
		for seed := range refCount(500) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x9fe))
			p := refGenParams(t, rng)
			all := refMerge(h)
			early := rng.IntN(h.span)
			short, long := run(t, h, all, early, p), run(t, h, all, h.span+10, p)
			if !short.State.Running {
				continue
			}
			require.Equal(t, short.From, long.From, "씨앗 %d", seed)
			require.LessOrEqual(t, len(short.Series), len(long.Series), "씨앗 %d", seed)
			require.Equal(t, short.Series, long.Series[:len(short.Series)], "씨앗 %d", seed)
			require.Equal(t, short.State, long.StateAt(h.date(early)), "씨앗 %d", seed)
		}
	})

	t.Run("기준선이 잡히기 전과 기준선 기간의 날은 쌓지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span + 20)
			days := refToSignalDays(h, refMerge(h))
			base, err := baseline.Compute(days, h.date(asOf), p)
			require.NoError(t, err)
			got, err := cusum.Run(days, base, p)
			require.NoError(t, err)

			if !base.Established {
				require.Equal(t, cusum.State{}, got.State, "씨앗 %d", seed)
				require.Empty(t, got.Series, "씨앗 %d", seed)
				require.True(t, got.From.IsZero(), "씨앗 %d", seed)
				continue
			}
			for _, point := range got.Series {
				require.True(t, point.Date.After(base.End), "씨앗 %d: 기준선 기간의 날 %s을 쌓았다", seed, point.Date)
				require.False(t, point.Date.After(base.AsOf), "씨앗 %d: 기준일 뒤의 날 %s을 쌓았다", seed, point.Date)
				require.GreaterOrEqual(t, point.S, 0.0, "씨앗 %d", seed)
				require.Equal(t, point.S > p.CUSUM.H, point.Detected, "씨앗 %d", seed)
			}
		}
	})

	t.Run("가상 기록이 하루 상한과 누적값의 천장을 실제로 지나간다", func(t *testing.T) {
		// 한 번도 지나가지 않았다면 참조 구현과 견준 것이 그 두 장치에 대해서는 아무것도 보지 않은 것이다.
		cappedDays, ceilingDays := 0, 0
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			got := run(t, h, refMerge(h), h.span+5, p)
			for _, point := range got.Series {
				if point.Capped {
					cappedDays++
				}
				if point.AtCeiling {
					ceilingDays++
				}
				require.LessOrEqual(t, point.Step, p.CUSUM.MaxStep, "씨앗 %d, %s", seed, point.Date)
				require.LessOrEqual(t, point.S, p.CUSUM.MaxS*p.CUSUM.H, "씨앗 %d, %s", seed, point.Date)
			}
		}
		require.Positive(t, cappedDays)
		require.Positive(t, ceilingDays)
	})

	t.Run("조정 값을 바꿔도 하루에 더한 값은 상한을, 누적값은 천장을 넘지 않는다", func(t *testing.T) {
		// 시험에서 고르는 MaxStep, MaxS, H는 이진 소수로 딱 떨어지는 값이다. 상한과 천장도 소수로 정확히 적히고,
		// 그보다 크지 않은 분수를 가장 가까운 소수로 옮긴 값은 그 소수를 넘지 못한다. 그래서 여유 없이 견준다.
		cappedDays, ceilingDays := 0, 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xcea1))
			p := refGenParams(t, rng)
			got := run(t, h, refMerge(h), h.span+5, p)

			stepLimit, ceiling := p.CUSUM.MaxStep, p.CUSUM.MaxS*p.CUSUM.H
			before := 0.0
			for _, point := range got.Series {
				if stepLimit > 0 {
					require.LessOrEqual(t, point.Step, stepLimit, "씨앗 %d, %s: 하루에 더한 값이 상한을 넘었다", seed, point.Date)
					require.LessOrEqual(t, point.S-before, stepLimit+refFloatSlack, "씨앗 %d, %s: 누적값이 하루에 상한보다 많이 늘었다", seed, point.Date)
				}
				if ceiling > 0 {
					require.LessOrEqual(t, point.S, ceiling, "씨앗 %d, %s: 누적값이 천장을 넘었다", seed, point.Date)
				}
				if point.Capped {
					cappedDays++
					require.Positive(t, stepLimit, "씨앗 %d, %s: 상한이 없는데 상한에 걸렸다고 적혔다", seed, point.Date)
					require.InDelta(t, stepLimit, point.Step, refFloatSlack, "씨앗 %d, %s: 상한에 걸린 날에 더한 값은 상한이다", seed, point.Date)
				}
				if point.AtCeiling {
					ceilingDays++
					require.Positive(t, ceiling, "씨앗 %d, %s: 천장이 없는데 천장에 걸렸다고 적혔다", seed, point.Date)
					require.InDelta(t, ceiling, point.S, refFloatSlack, "씨앗 %d, %s: 천장에 걸린 날의 누적값은 천장이다", seed, point.Date)
				}
				before = point.S
			}
		}
		require.Positive(t, cappedDays)
		require.Positive(t, ceilingDays)
	})

	t.Run("상한과 천장은 누적값을 낮추기만 하고, 처음 걸리는 날 전까지는 아무것도 바꾸지 않는다", func(t *testing.T) {
		lowered := 0
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x10e4))
			p := refGenParams(t, rng)
			if p.CUSUM.MaxStep == 0 && p.CUSUM.MaxS == 0 {
				p.CUSUM.MaxStep, p.CUSUM.MaxS = 2, 2
			}
			asWritten := p
			asWritten.CUSUM.MaxStep, asWritten.CUSUM.MaxS = 0, 0

			days := refMerge(h)
			bound, free := run(t, h, days, h.span+5, p), run(t, h, days, h.span+5, asWritten)
			require.Len(t, bound.Series, len(free.Series), "씨앗 %d", seed)
			touched := false
			for i, point := range bound.Series {
				touched = touched || point.Capped || point.AtCeiling
				if !touched {
					require.Equal(t, free.Series[i], point, "씨앗 %d, %s: 상한에도 천장에도 걸리기 전인데 값이 다르다", seed, point.Date)
				}
				require.LessOrEqual(t, point.S, free.Series[i].S+refFloatSlack, "씨앗 %d, %s: 상한과 천장을 뒀더니 누적값이 더 커졌다", seed, point.Date)
				require.False(t, point.Detected && !free.Series[i].Detected, "씨앗 %d, %s: 상한과 천장을 뒀더니 없던 감지가 생겼다", seed, point.Date)
				require.False(t, free.Series[i].Capped || free.Series[i].AtCeiling, "씨앗 %d, %s", seed, point.Date)
				if point.S < free.Series[i].S-refFloatSlack {
					lowered++
				}
			}
		}
		require.Positive(t, lowered, "상한이나 천장이 누적값을 낮춘 날이 하나도 없었다면 위의 확인은 아무것도 보지 않은 것이다")
	})

	t.Run("기본값에서는 누적값이 2 이하이던 사람이 하루 크게 나빠도 감지가 켜지지 않는다", func(t *testing.T) {
		// 하루에 2까지만 늘고 한계값 4는 넘어야 감지이므로, 누적값이 2 이하이던 사람은 그 하루에 아무리 많이 관찰돼도 4를 넘지 못한다.
		p := params.Default()
		badDays := 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			days := refMerge(h)
			calm := run(t, h, days, h.span+5, p)
			if len(calm.Series) == 0 {
				continue
			}
			// 쌓기 시작한 뒤의 아무 날 하나를 여덟 항목이 모두 관찰된 날로 바꾼다. 기준선 기간 밖이라 평소는 그대로다.
			rng := rand.New(rand.NewPCG(seed, 0xbad1))
			target := rng.IntN(len(calm.Series))
			worse := slices.Clone(days)
			for i := range worse {
				if h.date(worse[i].offset) == calm.Series[target].Date {
					for item := range refItems {
						worse[i].status[item], worse[i].direct[item] = refObserved, true
					}
				}
			}
			got := run(t, h, worse, h.span+5, p)
			before := 0.0
			if target > 0 {
				before = got.Series[target-1].S
			}
			if before <= p.CUSUM.H-p.CUSUM.MaxStep+refFloatSlack {
				badDays++
				require.False(t, got.Series[target].Detected, "씨앗 %d, %s: 누적값 %v에서 하루 만에 감지가 켜졌다", seed, got.Series[target].Date, before)
			}
		}
		require.Positive(t, badDays)
	})

	t.Run("기본값에서는 나빴던 기간이 아무리 길어도 평온한 대화 여덟 번이면 감지가 풀린다", func(t *testing.T) {
		// 누적값은 한계값의 2배인 8을 넘지 못하고, 아무 항목도 관찰되지 않은 날에는 적어도 허용 여유 0.5만큼 줄어든다.
		// 여덟 번이면 4 이하가 되고, 4는 넘은 것이 아니다. 천장을 끄면 같은 기록에서 이 성질이 깨지는 것도 함께 본다.
		p := params.Default()
		noCeiling := params.Default()
		noCeiling.CUSUM.MaxS = 0
		const calmTalks = 8

		released, stuckWithoutCeiling := 0, 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			days := refMerge(h)
			last := run(t, h, days, h.span-1, p)
			if !last.State.Running {
				continue
			}
			// 기준선이 잡힌 뒤이므로 뒤에 붙이는 날은 평소를 바꾸지 않는다.
			for i := range calmTalks {
				days = append(days, refDay{offset: h.span + 2*i})
			}
			asOf := h.span + 2*calmTalks
			got := run(t, h, days, asOf, p)
			require.False(t, got.State.Detected, "씨앗 %d: 평온한 대화 %d번 뒤에도 감지가 켜져 있다 (누적값 %v)", seed, calmTalks, got.State.S)
			if last.State.Detected {
				released++
			}
			if run(t, h, days, asOf, noCeiling).State.Detected {
				stuckWithoutCeiling++
			}
		}
		require.Positive(t, released, "감지가 켜져 있다가 풀린 기록이 하나도 없었다")
		require.Positive(t, stuckWithoutCeiling, "천장을 꺼도 모두 풀렸다면 천장이 하는 일을 보지 못한 것이다")
	})

	t.Run("관찰된 항목이 더 많은 날로 바꾸면 그날부터의 누적값이 줄지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xadd))
			p := refGenParams(t, rng)
			asOf := h.span + 5
			all := refMerge(h)
			before := run(t, h, all, asOf, p)
			if len(before.Series) == 0 {
				continue
			}
			// 기준선이 달라지지 않도록, 쌓기 시작한 뒤의 날 하나에만 관찰됨을 더한다.
			target := before.Series[rng.IntN(len(before.Series))].Date
			worse := slices.Clone(all)
			for i := range worse {
				if h.date(worse[i].offset) == target {
					item := rng.IntN(refItems)
					worse[i].status[item], worse[i].direct[item] = refObserved, true
				}
			}
			after := run(t, h, worse, asOf, p)
			require.Len(t, after.Series, len(before.Series), "씨앗 %d", seed)
			for i := range before.Series {
				require.GreaterOrEqual(t, after.Series[i].S, before.Series[i].S-refFloatSlack, "씨앗 %d, %s", seed, before.Series[i].Date)
			}
		}
	})
}

// TestReferenceCusumBoundary는 누적값이 한계값과 정확히 같아지는 기록을 일부러 만들어 본다.
//
// 규칙은 "누적값이 한계값을 넘으면 변화 감지"다. 같은 날은 감지가 아니다.
// 평소의 하루 평균이 6분의 11처럼 이진 소수로 딱 떨어지지 않으면, 소수로 쌓을 때 아주 작은 오차가 실린다.
// 정확히 계산하면 한계값과 같은 날에, 그 오차가 위쪽으로 실려 감지로 답하는 구현을 잡아낸다.
func TestReferenceCusumBoundary(t *testing.T) {
	p := params.Default()
	base, err := recorddate.Parse("2026-03-01")
	require.NoError(t, err)

	// observedOn은 관찰된 항목이 x개인 하루의 행을 만든다.
	observedOn := func(x int) []refRow {
		rows := []refRow{{item: refItems - 1, status: refNotMentioned}}
		for item := range x {
			rows = append(rows, refRow{item: item, status: refObserved, direct: true})
		}
		return rows
	}

	t.Run("가장 작은 기록: 평소가 6분의 11이고 그 뒤 사흘이 3개, 4개, 4개", func(t *testing.T) {
		// 기준선 기간(3월 1일~14일)에 열이틀 대화했고 관찰된 항목은 모두 22개다. 평소의 하루 평균은 22 ÷ 12 = 11/6이다.
		// 그 뒤 사흘에 더하는 값은 3 − 11/6 − 1/2 = 2/3, 4 − 11/6 − 1/2 = 5/3, 5/3이고 합은 정확히 4다.
		// 한계값 4를 넘지 않았으므로 감지가 아니어야 한다.
		h := refHistory{base: base, span: 17, rows: map[int][]refRow{}}
		for offset := range 12 {
			x := 2
			if offset >= 10 {
				x = 1
			}
			h.rows[offset] = observedOn(x)
		}
		h.rows[14], h.rows[15], h.rows[16] = observedOn(3), observedOn(4), observedOn(4)

		days := refMerge(h)
		want := refCusum(days, 16, p)
		require.Zero(t, want.s.Cmp(big.NewRat(4, 1)), "정확히 계산한 누적값이 4가 아니다")

		got, err := refRunProduction(h, days, 16, p)
		require.NoError(t, err)
		require.Len(t, got.Series, 3)
		assert.InDelta(t, 4.0, got.State.S, refFloatSlack)
		assert.False(t, got.State.Detected, "누적값이 한계값과 같은데 감지로 답했다 (소수로 쌓은 값 %.17g)", got.State.S)
	})

	t.Run("누적값이 정확히 한계값이 되는 기록을 빠짐없이 만들어 본다", func(t *testing.T) {
		type miss struct {
			conversationDays, observedTotal, days, low, highDays int
			s                                                    float64
		}

		// 식 그대로(상한과 천장 없음)와 기본값으로 한 번씩 돌린다. 기본값에서는 하루 상한에 걸리는 조합을 뺀다.
		// 걸리면 누적값이 덜 쌓여 m일째에 한계값과 같아지지 않는다. 누적값은 4까지만 오르므로 천장 8에는 닿지 않는다.
		asWritten := params.Default()
		asWritten.CUSUM.MaxStep, asWritten.CUSUM.MaxS = 0, 0
		settings := []struct {
			name string
			p    params.Params
		}{
			{"상한과 천장 없음", asWritten},
			{"기본값", p},
		}

		for _, setting := range settings {
			var misses []miss
			probes := 0

			// 기준선: 열나흘 가운데 b일 대화하고 관찰된 항목이 모두 a개. 그 뒤 m일 동안 날마다 c개 또는 c+1개가 관찰된다.
			// 하루에 더하는 값이 언제나 0보다 커서 누적값이 0에서 멈추지 않고, m일째에 정확히 한계값 4가 되는 조합만 고른다.
			for b := 7; b <= 14; b++ {
				for a := 0; a <= 3*b; a++ {
					for c := range refItems {
						if 2*b*c-2*a-b <= 0 {
							continue
						}
						for m := 1; m <= 40; m++ {
							// 관찰된 항목의 합이 c·m + r일 때 2b·(c·m + r) − m(2a + b) = 8b여야 한다.
							numerator := 8*b + m*(2*a+b) - 2*b*c*m
							if numerator < 0 || numerator%(2*b) != 0 || numerator/(2*b) > m {
								continue
							}
							r := numerator / (2 * b)

							// 가장 많이 관찰된 날에 더하는 값은 top − a/b − 1/2이다. 상한이 있으면 그 값이 상한 이하인 조합만 남긴다.
							top := c
							if r > 0 {
								top = c + 1
							}
							if limit := setting.p.CUSUM.MaxStep; limit > 0 && float64(2*b*top-2*a-b) > limit*float64(2*b) {
								continue
							}

							h := refHistory{base: base, span: 14 + m, rows: map[int][]refRow{}}
							left := a
							for offset := range b {
								x := min(left, 3)
								if offset == b-1 {
									x = left
								}
								left -= x
								h.rows[offset] = observedOn(x)
							}
							for i := range m {
								x := c
								if i >= m-r {
									x = c + 1
								}
								h.rows[14+i] = observedOn(x)
							}

							days := refMerge(h)
							want := refCusum(days, 13+m, setting.p)
							require.Zero(t, want.s.Cmp(big.NewRat(4, 1)), "%s: 만든 기록의 누적값이 한계값과 같지 않다 (b=%d a=%d c=%d m=%d)", setting.name, b, a, c, m)

							got, err := refRunProduction(h, days, 13+m, setting.p)
							require.NoError(t, err)
							probes++
							if got.State.Detected {
								misses = append(misses, miss{b, a, m, c, r, got.State.S})
							}
						}
					}
				}
			}

			slices.SortStableFunc(misses, func(x, y miss) int { return x.days - y.days })
			t.Logf("%s: 누적값이 정확히 한계값과 같아지는 기록 %d개 가운데 %d개에서 감지로 답했다", setting.name, probes, len(misses))
			for i, m := range misses {
				if i >= 5 {
					break
				}
				t.Logf("  기준선: 대화 %d일에 관찰된 항목 합 %d / 그 뒤 %d일: 하루 %d개, 마지막 %d일은 %d개 → 소수로 쌓은 값 %.17g",
					m.conversationDays, m.observedTotal, m.days, m.low, m.highDays, m.low+1, m.s)
			}
			require.Positive(t, probes, "%s: 만들어 본 기록이 하나도 없다", setting.name)
			assert.Empty(t, misses, "%s: 정확히 계산하면 한계값과 같아서 감지가 아닌데 감지로 답한 기록이 있다", setting.name)
		}
	})
}
