package stage_test

// 이 파일은 개입 단계의 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 실제 구현은 기준선과 변화 탐지를 한 번만 구해 두고 날짜마다 창을 밀어 가며 돌린다.
// 참조 구현은 날짜마다 점수, 신뢰도, 기준선, 변화 탐지를 그 날짜를 기준일로 삼아 처음부터 다시 구한다.
// 점수의 반올림도 소수로 한다. 느리지만 틀리기 어렵다.
// 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
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

// refLazy는 시험이 실패해서 메시지를 만들 때에만 글로 옮겨지는 값이다.
// 기록 하나를 글로 옮기는 데는 행마다 품이 든다. 날짜마다 미리 옮겨 두면 시험 시간의 대부분이 읽히지도 않을 글을 만드는 데 들어간다.
type refLazy func() string

func (l refLazy) String() string { return l() }

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
// 참조 구현: 추정 점수
// ---------------------------------------------------------------------------

type refScoreResult struct {
	from, to     int
	n            int
	insufficient bool
	observed     [refItems]int
	converted    [refItems]int
	points       [refItems]int
	total        int
	band         string
}

// refScore는 규칙을 글자 그대로 옮긴다.
//
//   - 창은 기준일을 포함해 거슬러 Window.Days일이다.
//   - n은 창 안에서 대화한 일수, o는 항목이 관찰된 일수다.
//   - n이 Window.MinConversationDays보다 적으면 점수를 내지 않는다.
//   - 환산 일수는 Window.Days × o ÷ n을 0.5에서 올리는 반올림이다. 여기서는 소수로 그대로 계산한다.
//     딱 0.5로 끝나는 몫은 소수로도 정확히 적히고, 나머지는 0.5에서 1/(2n) 넘게 떨어져 있어 오차가 끼어들 틈이 없다.
//   - 환산 일수를 경계에 견줘 항목 점수(0부터 3)로 옮기고, 여덟 항목을 더한 값을 구간에 견준다.
func refScore(days []refDay, asOf int, p params.Params) refScoreResult {
	r := refScoreResult{from: asOf - (p.Window.Days - 1), to: asOf, band: "none"}
	for date := r.from; date <= r.to; date++ {
		for _, day := range days {
			if day.offset != date {
				continue
			}
			r.n++
			for item := range refItems {
				if day.status[item] == refObserved {
					r.observed[item]++
				}
			}
		}
	}
	if r.n < p.Window.MinConversationDays {
		r.insufficient = true
		return r
	}

	for item := range refItems {
		exact := float64(p.Window.Days) * float64(r.observed[item]) / float64(r.n)
		r.converted[item] = int(math.Floor(exact + 0.5))
		switch d := r.converted[item]; {
		case d >= p.Score.ItemScore3MinDays:
			r.points[item] = 3
		case d >= p.Score.ItemScore2MinDays:
			r.points[item] = 2
		case d >= p.Score.ItemScore1MinDays:
			r.points[item] = 1
		}
		r.total += r.points[item]
	}
	switch {
	case r.total >= p.Score.SevereMin:
		r.band = "severe"
	case r.total >= p.Score.ModeratelySevereMin:
		r.band = "moderately_severe"
	case r.total >= p.Score.ModerateMin:
		r.band = "moderate"
	case r.total >= p.Score.MildMin:
		r.band = "mild"
	default:
		r.band = "minimal"
	}
	return r
}

// ---------------------------------------------------------------------------
// 참조 구현: 신뢰도
// ---------------------------------------------------------------------------

type refConfidenceResult struct {
	n                  int
	insufficient       bool
	mentioned          [refItems]bool
	mentionedItems     int
	observedJudgements int
	directJudgements   int

	record, item, explicitness float64
	value                      float64
	limiting                   string
	level                      string
}

// refConfidence는 규칙을 글자 그대로 옮긴다.
//
//   - 기록 충실도는 창 안에서 대화한 일수 ÷ 창의 길이다.
//   - 항목 충족도는 여덟 항목 가운데 창 안에서 한 번이라도 이야기가 나온(관찰됨 또는 관찰되지 않음) 항목의 비율이다.
//   - 근거 명시성은 창 안의 관찰됨 판단 가운데 직접 언급의 비율이다. 관찰됨이 하나도 없으면 1이다.
//   - 최종 값은 셋 가운데 가장 작은 값이다. MediumMin 미만이면 낮음, HighMin 이상이면 높음, 그 사이는 보통이다.
//   - 기록 부족이면 최종 값을 내지 않고 낮음으로 다룬다.
//
// 분자와 분모가 작은 정수라서 소수로 나눠도 같은 분수는 같은 소수가 되고, 다른 분수는 다른 소수가 된다.
func refConfidence(days []refDay, asOf int, p params.Params) refConfidenceResult {
	r := refConfidenceResult{limiting: "none", level: "low"}
	for date := asOf - (p.Window.Days - 1); date <= asOf; date++ {
		for _, day := range days {
			if day.offset != date {
				continue
			}
			r.n++
			for item := range refItems {
				if day.status[item] != refNotMentioned {
					r.mentioned[item] = true
				}
				if day.status[item] == refObserved {
					r.observedJudgements++
					if day.direct[item] {
						r.directJudgements++
					}
				}
			}
		}
	}
	for _, mentioned := range r.mentioned {
		if mentioned {
			r.mentionedItems++
		}
	}

	r.record = float64(r.n) / float64(p.Window.Days)
	r.item = float64(r.mentionedItems) / refItems
	r.explicitness = 1
	if r.observedJudgements > 0 {
		r.explicitness = float64(r.directJudgements) / float64(r.observedJudgements)
	}

	if r.n < p.Window.MinConversationDays {
		r.insufficient = true
		return r
	}

	// 값이 같으면 앞에 적힌 요소를 가장 약한 요소로 적는다.
	r.value, r.limiting = r.record, "record_coverage"
	if r.item < r.value {
		r.value, r.limiting = r.item, "item_coverage"
	}
	if r.explicitness < r.value {
		r.value, r.limiting = r.explicitness, "explicitness"
	}
	switch {
	case r.value < p.Confidence.MediumMin:
		r.level = "low"
	case r.value < p.Confidence.HighMin:
		r.level = "medium"
	default:
		r.level = "high"
	}
	return r
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

// 소수 계산의 오차로 볼 수 있는 가장 큰 차이다. 분모가 백 남짓인 분수끼리는 같지 않으면 이보다 훨씬 크게 벌어진다.
const refFloatSlack = 1e-9

type refChangeState struct {
	running  bool
	s        float64
	detected bool
	// onLimit은 누적값이 한계값과 소수 오차 안에서 같다는 뜻이다. 규칙으로는 "넘지 않았다"이므로 detected는 거짓이다.
	onLimit bool
}

// refChange는 그 날짜를 기준일로 삼아 기준선부터 처음부터 다시 구한 변화 탐지의 상태다.
//
//   - 기준일에 기준선이 잡혀 있지 않으면 돌리지 않는다.
//   - 기준선 기간의 마지막 날 다음 날부터, 대화한 날마다 S = max(0, S + (x − μ − K))로 쌓는다.
//   - 대화하지 않은 날은 건너뛰고 S를 그대로 둔다.
//   - MaxStep이 0보다 크면 하루에 더하는 값을 MaxStep까지만 인정한다.
//   - MaxS가 0보다 크면 그날의 값을 더한 뒤의 S는 MaxS × H를 넘지 못한다.
//   - S가 H를 넘으면(같으면 아니다) 변화 감지다. 넘은 뒤에도 S를 되돌리지 않는다.
func refChange(days []refDay, asOf int, p params.Params) refChangeState {
	base := refBaseline(days, asOf, p)
	if !base.established {
		return refChangeState{}
	}
	mu := float64(base.observedTotal) / float64(base.days)

	state := refChangeState{running: true}
	for _, day := range days {
		if day.offset <= base.end || day.offset > asOf {
			continue
		}
		x := 0
		for item := range refItems {
			if day.status[item] == refObserved {
				x++
			}
		}
		step := float64(x) - mu - p.CUSUM.K
		if p.CUSUM.MaxStep > 0 && step > p.CUSUM.MaxStep {
			step = p.CUSUM.MaxStep
		}
		state.s = math.Max(0, state.s+step)
		if p.CUSUM.MaxS > 0 {
			state.s = math.Min(state.s, p.CUSUM.MaxS*p.CUSUM.H)
		}
	}
	state.onLimit = math.Abs(state.s-p.CUSUM.H) <= refFloatSlack
	state.detected = !state.onLimit && state.s > p.CUSUM.H
	return state
}

// ---------------------------------------------------------------------------
// 참조 구현: 개입 단계
// ---------------------------------------------------------------------------

type refStagePoint struct {
	offset       int
	hasRecord    bool
	n            int
	insufficient bool
	score        int
	level        string
	detected     bool
	onLimit      bool

	raw, stage   int
	held         bool
	elevatedDays int
	reasons      []string
}

// refStage는 첫 대화 날부터 기준일까지 달력의 하루하루를 차례로 돌린다.
// 날마다 점수, 신뢰도, 변화 감지를 그 날짜를 기준일로 삼아 처음부터 다시 구한다. 지난 계산을 이어 쓰는 것은 어제의 단계와 이어진 일수뿐이다.
//
//  1. 점수로 본 단계. 기록이 모자라 점수가 없는 날은 0단계에서 시작한다.
//  2. 변화 감지 상태면 적어도 1단계.
//  3. 2단계 이상이 Stage.SustainedStage2Days일째 이어지는 날부터 3단계. 달력 날짜로 세고 그날을 포함한다.
//  4. 그날 대화한 기록이 없으면 전날보다 오르지 않는다.
//  5. 하루에 한 단계만 오른다.
//  6. 신뢰도가 낮으면 전날보다 오르지 않는다. 내려가는 것은 막지 않는다.
//  7. 기록 부족이면 전날의 단계와 이어진 일수를 그대로 이어 간다.
//  8. 창 안에 대화한 날이 하나도 없으면 0단계이고 이어진 일수도 0이다.
//
// 묶인 까닭은 하나만 적는다. 겹치면 기록 부족, 낮은 신뢰도, 그날의 기록 없음, 하루에 한 단계의 순서다.
//
// onLimitDetected는 누적값이 한계값과 같은 날에 변화 감지를 어느 쪽으로 볼지 답한다.
// 그런 날의 판정은 변화 탐지 쪽 시험이 따로 본다. 여기서는 단계의 규칙만 가려내려고 실제 구현의 답을 그대로 받는다.
func refStage(days []refDay, asOf int, p params.Params, onLimitDetected func(offset int) bool) []refStagePoint {
	first, found := 0, false
	for _, day := range days {
		if day.offset <= asOf && (!found || day.offset < first) {
			first, found = day.offset, true
		}
	}
	if !found {
		return nil
	}

	var points []refStagePoint
	previousStage, previousElevated := 0, 0
	for date := first; date <= asOf; date++ {
		sc := refScore(days, date, p)
		change := refChange(days, date, p)
		pt := refStagePoint{
			offset: date, n: sc.n, insufficient: sc.insufficient, score: sc.total,
			level: refConfidence(days, date, p).level, detected: change.detected, onLimit: change.onLimit,
			reasons: []string{},
		}
		for _, day := range days {
			if day.offset == date {
				pt.hasRecord = true
			}
		}
		if pt.onLimit {
			pt.detected = onLimitDetected(date)
		}

		if pt.n == 0 {
			// 8번. 단계도 이어진 일수도 0이다.
			pt.reasons = append(pt.reasons, "no_recent_records")
			previousStage, previousElevated = 0, 0
			points = append(points, pt)
			continue
		}

		// 1~3번
		if !pt.insufficient {
			switch {
			case pt.score >= p.Stage.Stage3MinScore:
				pt.raw = 3
			case pt.score >= p.Stage.Stage2MinScore:
				pt.raw = 2
			case pt.score >= p.Stage.Stage1MinScore:
				pt.raw = 1
			}
		}
		if pt.raw >= 1 {
			pt.reasons = append(pt.reasons, "score")
		}
		if pt.detected && pt.raw < 1 {
			pt.raw = 1
			pt.reasons = append(pt.reasons, "change_detected")
		}
		if pt.raw == 2 && previousElevated+1 >= p.Stage.SustainedStage2Days {
			pt.raw = 3
			pt.reasons = append(pt.reasons, "sustained")
		}

		switch {
		case pt.insufficient:
			// 7번
			pt.stage, pt.elevatedDays = previousStage, previousElevated
			if pt.raw > previousStage {
				pt.held = true
				pt.reasons = append(pt.reasons, "held_insufficient_records")
			}
			if pt.raw < previousStage {
				pt.reasons = append(pt.reasons, "carried_insufficient_records")
			}
		case pt.raw <= previousStage:
			// 같거나 내려가는 것은 어느 규칙도 막지 않는다.
			pt.stage = pt.raw
		case pt.level == "low":
			// 6번
			pt.stage, pt.held = previousStage, true
			pt.reasons = append(pt.reasons, "held_low_confidence")
		case !pt.hasRecord:
			// 4번
			pt.stage, pt.held = previousStage, true
			pt.reasons = append(pt.reasons, "held_no_record_today")
		case pt.raw > previousStage+1:
			// 5번
			pt.stage, pt.held = previousStage+1, true
			pt.reasons = append(pt.reasons, "held_one_step_per_day")
		default:
			pt.stage = pt.raw
		}

		if !pt.insufficient && pt.stage >= 2 {
			pt.elevatedDays = previousElevated + 1
		}
		previousStage, previousElevated = pt.stage, pt.elevatedDays
		points = append(points, pt)
	}
	return points
}

// ---------------------------------------------------------------------------
// 견주기
// ---------------------------------------------------------------------------

func refReasonIDs(reasons []stage.Reason) []string {
	ids := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		ids = append(ids, reason.String())
	}
	return ids
}

// refPointDiff는 실제 구현의 하루를 참조 구현의 하루와 견줘 다른 점을 돌려준다.
func refPointDiff(h refHistory, want refStagePoint, got stage.Point) []string {
	var diffs []string
	name := h.date(want.offset).String()
	note := func(field string, want, got any) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s %s: 참조 %v, 실제 %v", name, field, want, got))
		}
	}
	note("날짜", h.date(want.offset), got.Date)
	note("그날의 기록이 있는가", want.hasRecord, got.HasRecord)
	note("대화한 일수", want.n, got.ConversationDays)
	note("기록 부족", want.insufficient, got.Insufficient)
	note("추정 점수", want.score, got.Score)
	note("신뢰도 구간", want.level, got.Confidence.String())
	note("변화 감지", want.detected, got.Detected)
	note("그날의 값으로 본 단계", want.raw, int(got.Raw))
	note("단계", want.stage, int(got.Stage))
	note("묶였는가", want.held, got.Held)
	note("2단계 이상이 이어진 일수", want.elevatedDays, got.ElevatedDays)
	if got.Reasons == nil || !slices.Equal(want.reasons, refReasonIDs(got.Reasons)) {
		diffs = append(diffs, fmt.Sprintf("%s 단계를 움직인 조건: 참조 %v, 실제 %v", name, want.reasons, refReasonIDs(got.Reasons)))
	}
	return diffs
}

// refStageDisagreement는 기록 하나, 기준일 하나의 흐름을 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refStageDisagreement(h refHistory, asOf int, p params.Params) string {
	days := refMerge(h)
	got, err := stage.Compute(refToSignalDays(h, days), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}

	// 누적값이 한계값과 같은 날의 변화 감지는 실제 구현의 답을 받아서 단계의 규칙만 가려낸다.
	want := refStage(days, asOf, p, func(offset int) bool {
		pt, _ := got.At(h.date(offset))
		return pt.Detected
	})

	var diffs []string
	if got.AsOf != h.date(asOf) {
		diffs = append(diffs, fmt.Sprintf("기준일: 참조 %s, 실제 %s", h.date(asOf), got.AsOf))
	}
	if got.Series == nil {
		diffs = append(diffs, "흐름이 nil이다. 기록이 없어도 길이 0인 목록이어야 한다")
	}
	if len(want) != len(got.Series) {
		diffs = append(diffs, fmt.Sprintf("흐름의 길이: 참조 %d, 실제 %d", len(want), len(got.Series)))
		return strings.Join(diffs, "\n")
	}

	if len(want) == 0 {
		// 기준일까지 대화한 날이 없다. 0단계이고, 창에 기록이 없다는 조건만 적힌다.
		if !got.From.IsZero() {
			diffs = append(diffs, "기록이 없는데 첫날이 채워져 있다")
		}
		empty := refStagePoint{offset: asOf, insufficient: true, level: "low", reasons: []string{"no_recent_records"}}
		diffs = append(diffs, refPointDiff(h, empty, got.State)...)
		return strings.Join(diffs, "\n")
	}

	if got.From != h.date(want[0].offset) {
		diffs = append(diffs, fmt.Sprintf("첫날: 참조 %s, 실제 %s", h.date(want[0].offset), got.From))
	}
	for i := range want {
		diffs = append(diffs, refPointDiff(h, want[i], got.Series[i])...)
		if len(diffs) > 12 {
			break
		}
	}
	diffs = append(diffs, refPointDiff(h, want[len(want)-1], got.State)...)
	return strings.Join(diffs, "\n")
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return refStageDisagreement(c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p, refStageDisagreement(small, smallAsOf, p), small.date(smallAsOf), small.describe())
}

const refHistoryCount = 1500

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

func TestReferenceStage(t *testing.T) {
	t.Run("가상 기록에서 기본값으로 참조 구현과 같은 흐름이 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			// 기록이 끝난 뒤 말이 없는 날들도 돌려 본다. 창이 비어 0단계로 돌아가는 자리다.
			asOf := h.span - 1 + rng.IntN(40)
			if refStageDisagreement(h, asOf, p) != "" {
				refFailShrunk(t, seed, h, asOf, p)
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 흐름이 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			for _, asOf := range refAsOfPicks(rng, h) {
				if refStageDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("단계가 잘 오르는 조정 값에서도 같다", func(t *testing.T) {
		// 기본값에서는 가상 기록 대부분이 0단계와 1단계에 머문다. 경계를 낮추고 이어진 일수를 줄여서
		// 2단계가 이어져 3단계가 되는 길과, 올라가려다 묶이는 길을 자주 지나가게 한다.
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x10ad))
			asOf := h.span - 1 + rng.IntN(40)
			p := params.Default()
			p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = 2, 4, 9
			p.Stage.SustainedStage2Days = 2 + rng.IntN(6)
			p.Confidence.MediumMin, p.Confidence.HighMin = 0.5, 0.8
			require.NoError(t, p.Validate())
			if refStageDisagreement(h, asOf, p) != "" {
				refFailShrunk(t, seed, h, asOf, p)
			}
		}
	})
}

func TestReferenceStageProperties(t *testing.T) {
	compute := func(t *testing.T, days []signal.Day, asOf recorddate.Date, p params.Params) stage.Result {
		t.Helper()
		got, err := stage.Compute(days, asOf, p)
		require.NoError(t, err)
		return got
	}
	// 단계가 자주 움직이도록 경계를 낮춘 조정 값과 기본값을 번갈아 쓴다.
	paramsFor := func(t *testing.T, seed uint64, rng *rand.Rand) params.Params {
		t.Helper()
		if seed%2 == 0 {
			return refGenParams(t, rng)
		}
		p := params.Default()
		p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = 2, 4, 9
		p.Stage.SustainedStage2Days = 2 + rng.IntN(6)
		p.Confidence.MediumMin, p.Confidence.HighMin = 0.5, 0.8
		require.NoError(t, p.Validate())
		return p
	}

	t.Run("단계는 대화한 날에만, 하루에 한 단계씩, 신뢰도가 낮지 않고 기록이 충분한 날에만 오른다", func(t *testing.T) {
		rises := 0
		heldBy := map[stage.Reason]int{}
		carriedDays := 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x10ad))
			p := paramsFor(t, seed, rng)
			days := refMerge(h)
			got := compute(t, refToSignalDays(h, days), h.date(h.span+20), p)

			recorded := map[int]bool{}
			for _, day := range days {
				recorded[day.offset] = true
			}

			previous := stage.Everyday
			for i, pt := range got.Series {
				// 실제 구현이 적어 둔 값이 아니라, 그 날짜를 기준일로 따로 구한 점수와 신뢰도로 확인한다.
				offset := i + got.From.DaysSince(h.date(0))
				sc := refScore(days, offset, p)
				low := refConfidence(days, offset, p).level == "low"
				if sc.insufficient || low || !recorded[offset] {
					require.LessOrEqual(t, pt.Stage, previous,
						"씨앗 %d: %s에 단계가 올랐다 (기록 부족 %v, 신뢰도 낮음 %v, 그날의 기록 %v)\n%s",
						seed, pt.Date, sc.insufficient, low, recorded[offset], refLazy(h.describe))
				}
				require.LessOrEqual(t, pt.Stage, previous+1, "씨앗 %d: %s에 두 단계 넘게 올랐다\n%s", seed, pt.Date, refLazy(h.describe))
				if sc.insufficient && sc.n > 0 {
					require.Equal(t, previous, pt.Stage, "씨앗 %d: 기록 부족인 %s에 단계가 움직였다\n%s", seed, pt.Date, refLazy(h.describe))
					if pt.Raw < pt.Stage {
						carriedDays++
					}
				}
				if pt.Stage > previous {
					rises++
				}
				if pt.Held {
					heldBy[pt.HeldBy()]++
				}
				previous = pt.Stage
			}
		}
		// 단계가 한 번도 오르지 않았거나 묶인 날이 없었다면 위의 확인은 아무것도 보지 않은 것이다.
		require.Positive(t, rises)
		require.Positive(t, carriedDays, "기록 부족이어서 단계를 이어 간 날이 한 번도 없었다")
		for _, reason := range []stage.Reason{
			stage.ReasonHeldNoRecordToday, stage.ReasonHeldOneStepPerDay,
			stage.ReasonHeldLowConfidence, stage.ReasonHeldInsufficientRecords,
		} {
			require.Positive(t, heldBy[reason], "%s 때문에 묶인 날이 한 번도 없었다", reason)
		}
	})

	t.Run("점수가 닿지 않은 3단계는 2단계 이상이 기록 부족인 날을 빼고 정해진 일수째 이어진 날에만 나오고, 그날이 오면 반드시 나온다", func(t *testing.T) {
		// 실제 구현이 적어 둔 이어진 일수와 그날의 값으로 본 단계는 읽지 않는다.
		// 단계의 흐름을 거꾸로 짚어 가며 따로 세고, 점수와 신뢰도도 그 날짜를 기준일로 따로 구한다.
		reached, reachedAcrossCarriedDays, due := 0, 0, 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x57a1))
			p := paramsFor(t, seed, rng)
			days := refMerge(h)
			got := compute(t, refToSignalDays(h, days), h.date(h.span+20), p)
			if len(got.Series) == 0 {
				continue
			}

			recorded := map[int]bool{}
			for _, day := range days {
				recorded[day.offset] = true
			}
			first := got.From.DaysSince(h.date(0))
			scores := make([]refScoreResult, len(got.Series))
			for i := range got.Series {
				scores[i] = refScore(days, first+i, p)
			}

			// streak은 i번째 날까지 2단계 이상이 며칠째 이어졌는지를 센다.
			// 기록 부족인 날은 세지도 끊지도 않고, 창이 빈 날과 2단계 미만인 날에서 끊긴다.
			streak := func(i int) (run int, acrossCarried bool) {
				passedCarried := false
				for j := i; j >= 0; j-- {
					switch {
					case scores[j].n == 0:
						return run, acrossCarried
					case scores[j].insufficient:
						passedCarried = run > 0
					case got.Series[j].Stage >= stage.Suggestion:
						run++
						acrossCarried = acrossCarried || passedCarried
					default:
						return run, acrossCarried
					}
				}
				return run, acrossCarried
			}

			for i, pt := range got.Series {
				if scores[i].insufficient {
					continue
				}
				where := refLazy(func() string { return fmt.Sprintf("씨앗 %d, %s\n%s", seed, pt.Date, h.describe()) })
				if pt.Stage == stage.Recommendation && scores[i].total < p.Stage.Stage3MinScore {
					run, acrossCarried := streak(i)
					require.GreaterOrEqual(t, run, p.Stage.SustainedStage2Days, "점수가 닿지 않았고 이어진 일수도 모자란데 3단계다: %s", where)
					reached++
					if acrossCarried {
						reachedAcrossCarriedDays++
					}
				}
				if i == 0 || got.Series[i-1].Stage < stage.Suggestion {
					continue
				}
				before, _ := streak(i - 1)
				low := refConfidence(days, first+i, p).level == "low"
				if before+1 >= p.Stage.SustainedStage2Days && scores[i].total >= p.Stage.Stage2MinScore && recorded[first+i] && !low {
					require.Equal(t, stage.Recommendation, pt.Stage, "2단계 이상이 정해진 일수째 이어졌고 막는 것이 없는데 3단계가 아니다: %s", where)
					due++
				}
			}
		}
		require.Positive(t, reached, "이어진 일수로 3단계가 된 날")
		require.Positive(t, reachedAcrossCarriedDays, "기록 부족인 날을 사이에 두고 이어져 3단계가 된 날")
		require.Positive(t, due, "정해진 일수째가 되어 3단계여야 했던 날")
	})

	t.Run("하루의 값들이 서로 앞뒤가 맞는다", func(t *testing.T) {
		sustained := 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := paramsFor(t, seed, rng)
			got := compute(t, refToSignalDays(h, refMerge(h)), h.date(h.span+20), p)

			previous := stage.Point{}
			for _, pt := range got.Series {
				require.True(t, pt.Stage.Valid() && pt.Raw.Valid(), "씨앗 %d, %s", seed, pt.Date)
				require.Equal(t, pt.Raw > pt.Stage, pt.Held, "씨앗 %d, %s", seed, pt.Date)
				require.Equal(t, pt.Held, pt.HeldBy() != 0, "씨앗 %d, %s: 묶인 날에는 까닭이 하나 적힌다", seed, pt.Date)
				carried := pt.Insufficient && pt.ConversationDays > 0
				if !carried {
					require.LessOrEqual(t, pt.Stage, pt.Raw, "씨앗 %d, %s: 단계가 그날의 값으로 본 단계보다 높다", seed, pt.Date)
				}
				switch pt.HeldBy() {
				case stage.ReasonHeldOneStepPerDay:
					require.Equal(t, previous.Stage+1, pt.Stage, "씨앗 %d, %s: 하루에 한 단계는 올랐어야 한다", seed, pt.Date)
					require.True(t, pt.HasRecord && !pt.Insufficient && pt.Confidence != confidence.Low, "씨앗 %d, %s", seed, pt.Date)
				case stage.ReasonHeldNoRecordToday:
					require.Equal(t, previous.Stage, pt.Stage, "씨앗 %d, %s: 묶인 날은 전날의 단계다", seed, pt.Date)
					require.True(t, !pt.HasRecord && !pt.Insufficient && pt.Confidence != confidence.Low, "씨앗 %d, %s", seed, pt.Date)
				case stage.ReasonHeldLowConfidence:
					require.Equal(t, previous.Stage, pt.Stage, "씨앗 %d, %s: 묶인 날은 전날의 단계다", seed, pt.Date)
					require.True(t, !pt.Insufficient && pt.Confidence == confidence.Low, "씨앗 %d, %s", seed, pt.Date)
				case stage.ReasonHeldInsufficientRecords:
					require.Equal(t, previous.Stage, pt.Stage, "씨앗 %d, %s: 묶인 날은 전날의 단계다", seed, pt.Date)
					require.True(t, pt.Insufficient, "씨앗 %d, %s", seed, pt.Date)
				}
				if pt.ConversationDays == 0 {
					require.Equal(t, stage.Everyday, pt.Stage, "씨앗 %d, %s: 창이 비었는데 0단계가 아니다", seed, pt.Date)
					require.Zero(t, pt.ElevatedDays, "씨앗 %d, %s: 창이 비었는데 이어진 일수가 남아 있다", seed, pt.Date)
				}
				if pt.Insufficient {
					require.Equal(t, confidence.Low, pt.Confidence, "씨앗 %d, %s", seed, pt.Date)
					require.Zero(t, pt.Score, "씨앗 %d, %s", seed, pt.Date)
				}
				if pt.Detected && pt.ConversationDays > 0 {
					require.GreaterOrEqual(t, pt.Raw, stage.Reflection, "씨앗 %d, %s: 변화 감지인데 1단계가 안 된다", seed, pt.Date)
				}

				wantElevated := 0
				switch {
				case carried:
					wantElevated = previous.ElevatedDays
				case pt.Stage >= stage.Suggestion:
					wantElevated = previous.ElevatedDays + 1
				}
				require.Equal(t, wantElevated, pt.ElevatedDays, "씨앗 %d, %s", seed, pt.Date)
				require.Equal(t, pt.Stage >= stage.Suggestion, pt.ElevatedDays > 0, "씨앗 %d, %s", seed, pt.Date)
				if slices.Contains(pt.Reasons, stage.ReasonSustained) {
					sustained++
					require.GreaterOrEqual(t, previous.ElevatedDays+1, p.Stage.SustainedStage2Days, "씨앗 %d, %s", seed, pt.Date)
				}
				if !pt.Insufficient && pt.Score < p.Stage.Stage3MinScore && pt.Stage == stage.Recommendation {
					// 점수로는 3단계가 아닌데 3단계라면 2단계 이상이 정해진 일수째 이어진 것이어야 한다.
					require.GreaterOrEqual(t, pt.ElevatedDays, p.Stage.SustainedStage2Days, "씨앗 %d, %s", seed, pt.Date)
				}
				previous = pt
			}
		}
		require.Positive(t, sustained, "2단계가 이어져 3단계가 되는 길을 한 번도 지나가지 않았다")
	})

	t.Run("같은 기록에서는 몇 번을 계산해도 같은 값이고 받은 목록을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xd1ce))
			p := paramsFor(t, seed, rng)
			days := refToSignalDays(h, refMerge(h))
			before := slices.Clone(days)
			asOf := h.date(rng.IntN(h.span + 20))
			first := compute(t, days, asOf, p)
			for range 2 {
				require.Equal(t, first, compute(t, slices.Clone(days), asOf, p), "씨앗 %d", seed)
			}
			require.Equal(t, before, days, "씨앗 %d", seed)
		}
	})

	t.Run("기준일보다 뒤의 날은 결과를 바꾸지 않고, 앞선 기준일의 흐름은 뒤 기준일의 흐름의 앞부분이다", func(t *testing.T) {
		for seed := range refCount(500) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xf00d))
			p := paramsFor(t, seed, rng)
			asOf := rng.IntN(h.span)
			all := refMerge(h)
			var upToAsOf []refDay
			for _, day := range all {
				if day.offset <= asOf {
					upToAsOf = append(upToAsOf, day)
				}
			}
			early := compute(t, refToSignalDays(h, all), h.date(asOf), p)
			require.Equal(t, early, compute(t, refToSignalDays(h, upToAsOf), h.date(asOf), p), "씨앗 %d", seed)

			late := compute(t, refToSignalDays(h, all), h.date(h.span+15), p)
			require.Equal(t, early.Series, late.Series[:len(early.Series)], "씨앗 %d", seed)
			if len(early.Series) > 0 {
				require.Equal(t, early.From, late.From, "씨앗 %d", seed)
				pt, ok := late.At(h.date(asOf))
				require.True(t, ok, "씨앗 %d", seed)
				require.Equal(t, early.State, pt, "씨앗 %d", seed)
			}
		}
	})

	t.Run("흐름에 없는 날짜를 물으면 없다고 답한다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			got := compute(t, refToSignalDays(h, refMerge(h)), h.date(h.span-1), params.Default())
			_, ok := got.At(h.date(h.span))
			assert.False(t, ok, "씨앗 %d: 기준일 뒤", seed)
			_, ok = got.At(recorddate.Date{})
			assert.False(t, ok, "씨앗 %d: 빈 날짜", seed)
			if !got.From.IsZero() {
				_, ok = got.At(got.From.AddDays(-1))
				assert.False(t, ok, "씨앗 %d: 첫날의 전날", seed)
				first, ok := got.At(got.From)
				assert.True(t, ok, "씨앗 %d", seed)
				assert.Equal(t, got.Series[0], first, "씨앗 %d", seed)
			}
		}
	})

	t.Run("날을 모두 지우면 0단계이고 흐름이 비어 있다", func(t *testing.T) {
		asOf, err := recorddate.Parse("2026-07-01")
		require.NoError(t, err)
		for _, days := range [][]signal.Day{nil, {}} {
			got := compute(t, days, asOf, params.Default())
			assert.Equal(t, stage.Result{
				AsOf:   asOf,
				Series: []stage.Point{},
				State: stage.Point{
					Date: asOf, Insufficient: true, Confidence: confidence.Low,
					Reasons: []stage.Reason{stage.ReasonNoRecentRecords},
				},
			}, got)
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
			_, err := stage.Compute(days, h.date(h.span-1), params.Default())
			assert.ErrorIs(t, err, signal.ErrUnsortedDays, "씨앗 %d", seed)
		}
	})
}
