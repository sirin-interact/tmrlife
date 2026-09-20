package baseline_test

// 이 파일은 개인 기준선의 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 참조 구현은 기준일마다 "그날 알 수 있었던 기록"만 남기고 처음부터 다시 센다.
// 지난 계산을 이어 쓰지 않으므로 느리지만, 기준일을 옮겨 가며 견주면 고정된다는 약속이 지켜지는지 드러난다.
// 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
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

// refBaselineDisagreement는 기록 하나, 기준일 하나를 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refBaselineDisagreement(h refHistory, asOf int, p params.Params) string {
	days := refMerge(h)
	want := refBaseline(days, asOf, p)
	got, err := baseline.Compute(refToSignalDays(h, days), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}

	var diffs []string
	note := func(name string, want, got any) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s: 참조 %v, 실제 %v", name, want, got))
		}
	}
	dateOrZero := func(has bool, offset int) recorddate.Date {
		if !has {
			return recorddate.Date{}
		}
		return h.date(offset)
	}
	note("기준일", h.date(asOf), got.AsOf)
	note("첫날", dateOrZero(want.hasStart, want.start), got.Start)
	note("마지막 날", dateOrZero(want.hasEnd, want.end), got.End)
	note("잡혔는가", want.established, got.Established)
	note("기간을 늘렸는가", want.extended, got.Extended)
	note("쓴 대화 일수", want.days, got.Days)
	note("관찰된 항목 수의 합", want.observedTotal, got.ObservedTotal)

	mu := 0.0
	if want.days > 0 {
		mu = float64(want.observedTotal) / float64(want.days)
	}
	if diff := mu - got.Mu; diff > 1e-12 || diff < -1e-12 {
		diffs = append(diffs, fmt.Sprintf("하루 평균: 참조 %v, 실제 %v", mu, got.Mu))
	}
	for item, id := range signal.AllItems() {
		note(id.String()+" 관찰 비율", baseline.Rate{ObservedDays: want.itemObserved[item], Days: want.days}, got.ItemRate(id))
	}
	for row, id := range signal.AllTrendRows() {
		note(id.String()+" 줄의 관찰 비율", baseline.Rate{ObservedDays: want.trendObserved[row], Days: want.days}, got.TrendRate(id))
	}

	// 기간에 드는 날짜인지도 하루씩 물어본다. 마지막 날을 아직 모르면 첫날부터 기준일까지가 기간이다.
	last := asOf
	if want.hasEnd {
		last = want.end
	}
	for date := -2; date <= asOf+3; date++ {
		inside := want.hasStart && date >= want.start && date <= last
		if h.date(date).IsZero() {
			continue
		}
		note(fmt.Sprintf("%s이 기간에 드는가", h.date(date)), inside, got.Contains(h.date(date)))
	}
	return strings.Join(diffs, "\n")
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return refBaselineDisagreement(c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p.Baseline, refBaselineDisagreement(small, smallAsOf, p), small.date(smallAsOf), small.describe())
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

func TestReferenceBaseline(t *testing.T) {
	t.Run("가상 기록 수천 개에서 기본값으로 참조 구현과 같은 기준선이 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			for _, asOf := range refAsOfPicks(rng, h) {
				if refBaselineDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 기준선이 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			for _, asOf := range refAsOfPicks(rng, h) {
				if refBaselineDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("기준일을 첫날부터 하루씩 옮겨 가며 견줘도 같다", func(t *testing.T) {
		// 기간의 마지막 날, 그 전날, 그 다음 날처럼 하루 차이로 갈리는 자리를 빠짐없이 지나간다.
		for seed := range refCount(refHistoryCount / 10) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x5e71))
			p := refGenParams(t, rng)
			for asOf := 0; asOf < h.span+3; asOf++ {
				if refBaselineDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})
}

func TestReferenceBaselineProperties(t *testing.T) {
	compute := func(t *testing.T, days []signal.Day, asOf recorddate.Date, p params.Params) baseline.Baseline {
		t.Helper()
		got, err := baseline.Compute(days, asOf, p)
		require.NoError(t, err)
		return got
	}

	t.Run("같은 기록에서는 몇 번을 계산해도 같은 값이고 받은 목록을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xd1ce))
			p := refGenParams(t, rng)
			days := refToSignalDays(h, refMerge(h))
			before := slices.Clone(days)
			asOf := h.date(rng.IntN(h.span + 20))
			first := compute(t, days, asOf, p)
			for range 3 {
				require.Equal(t, first, compute(t, slices.Clone(days), asOf, p), "씨앗 %d", seed)
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
			require.Equal(t,
				compute(t, refToSignalDays(h, all), h.date(asOf), p),
				compute(t, refToSignalDays(h, upToAsOf), h.date(asOf), p), "씨앗 %d", seed)
		}
	})

	t.Run("한번 잡힌 기준선은 기준일을 뒤로 옮겨도, 뒤의 기록을 지워도 달라지지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xf1fed))
			p := refGenParams(t, rng)
			all := refMerge(h)
			days := refToSignalDays(h, all)

			var fixed baseline.Baseline
			for asOf := 0; asOf < h.span+20; asOf++ {
				got := compute(t, days, h.date(asOf), p)
				if !fixed.Established {
					fixed = got
					continue
				}
				require.True(t, got.Established, "씨앗 %d: 잡혔던 기준선이 %s에 풀렸다", seed, h.date(asOf))
				got.AsOf = fixed.AsOf
				require.Equal(t, fixed, got, "씨앗 %d: 기준일 %s", seed, h.date(asOf))
			}
			if !fixed.Established {
				continue
			}

			// 기간이 끝난 뒤의 날을 모두 지워도 평소는 그대로다.
			var insidePeriod []refDay
			for _, day := range all {
				if !h.date(day.offset).After(fixed.End) {
					insidePeriod = append(insidePeriod, day)
				}
			}
			got := compute(t, refToSignalDays(h, insidePeriod), h.date(h.span+19), p)
			got.AsOf = fixed.AsOf
			require.Equal(t, fixed, got, "씨앗 %d", seed)
		}
	})

	t.Run("기간 안의 날을 지우면 남은 기록으로 처음부터 다시 정해진다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xde1e7e))
			p := refGenParams(t, rng)
			offsets := h.offsets()
			if len(offsets) == 0 {
				continue
			}
			// 앞쪽 날일수록 기간 안에 들 가능성이 높다.
			deleted := h.clone()
			delete(deleted.rows, offsets[rng.IntN(min(len(offsets), 8))])
			asOf := h.span + 5
			if refBaselineDisagreement(deleted, asOf, p) != "" {
				refFailShrunk(t, seed, deleted, asOf, p)
			}
		}
	})

	t.Run("결과의 값들이 서로 앞뒤가 맞는다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := refGenParams(t, rng)
			got := compute(t, refToSignalDays(h, refMerge(h)), h.date(rng.IntN(h.span+20)), p)

			if got.Established {
				require.False(t, got.End.IsZero(), "씨앗 %d", seed)
				require.True(t, got.AsOf.After(got.End), "씨앗 %d", seed)
				require.GreaterOrEqual(t, got.Days, p.Baseline.MinConversationDays, "씨앗 %d", seed)
				require.GreaterOrEqual(t, got.End.DaysSince(got.Start)+1, p.Baseline.WindowDays, "씨앗 %d", seed)
				require.Equal(t, got.End.DaysSince(got.Start)+1 > p.Baseline.WindowDays, got.Extended, "씨앗 %d", seed)
			}
			sum := 0
			for _, rate := range got.ItemRates {
				require.Equal(t, got.Days, rate.Days, "씨앗 %d", seed)
				require.LessOrEqual(t, rate.ObservedDays, rate.Days, "씨앗 %d", seed)
				sum += rate.ObservedDays
			}
			require.Equal(t, got.ObservedTotal, sum, "씨앗 %d", seed)
			require.GreaterOrEqual(t, got.Mu, 0.0, "씨앗 %d", seed)
			require.LessOrEqual(t, got.Mu, float64(refItems), "씨앗 %d", seed)
		}
	})

	t.Run("날짜순이 아닌 기록은 받지 않는다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			days := refToSignalDays(h, refMerge(h))
			if len(days) < 2 {
				continue
			}
			slices.Reverse(days)
			_, err := baseline.Compute(days, h.date(h.span-1), params.Default())
			assert.ErrorIs(t, err, signal.ErrUnsortedDays, "씨앗 %d", seed)
		}
	})
}
