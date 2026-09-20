package assess_test

// 이 파일은 평가 전체를 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 참조 구현은 신호 행에서 시작해 하루로 합치기, 추정 점수, 신뢰도, 개인 기준선, 변화 탐지, 개입 단계, 점 달력을
// 모두 규칙 문장 그대로 다시 계산한다. 정수로 하는 반올림은 소수로, 이어 쓰는 계산은 날짜마다 처음부터 다시 한다.
// 느리지만 틀리기 어렵다. 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
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

func refToSignalRows(h refHistory) map[recorddate.Date][]signal.Row {
	out := make(map[recorddate.Date][]signal.Row, len(h.rows))
	for offset, rows := range h.rows {
		converted := make([]signal.Row, 0, len(rows))
		for i, row := range rows {
			r := signal.Row{
				ConversationID: fmt.Sprintf("c%d", i),
				Item:           signal.AllItems()[row.item],
				Cancelled:      row.cancelled,
			}
			switch row.status {
			case refObserved:
				r.Status = signal.Observed
			case refNotObserved:
				r.Status = signal.NotObserved
			}
			switch {
			case row.status == refNotMentioned:
			case row.direct:
				r.Explicitness = signal.Direct
			default:
				r.Explicitness = signal.Indirect
			}
			converted = append(converted, r)
		}
		out[h.date(offset)] = converted
	}
	return out
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
		if ceiling := p.CUSUM.MaxS * p.CUSUM.H; ceiling > 0 && state.s > ceiling {
			state.s = ceiling
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
// 오르려던 만큼 오르지 못한 까닭은 하나만 적는다. 겹치면 기록 부족, 낮은 신뢰도, 그날의 기록 없음, 하루에 한 단계의 순서다.
//
// onLimitDetected는 누적값이 한계값과 같은 날에 변화 감지를 어느 쪽으로 볼지 답한다.
// 그런 날의 판정은 변화 탐지 쪽 시험이 따로 본다. 여기서는 단계의 규칙만 가려내려고 실제 구현의 답을 그대로 받는다.
func refStage(days []refDay, asOf int, p params.Params, onLimitDetected func(offset int) bool) []refStagePoint {
	first, found := 0, false
	talked := map[int]bool{}
	for _, day := range days {
		if day.offset > asOf {
			continue
		}
		talked[day.offset] = true
		if !found || day.offset < first {
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
			offset: date, hasRecord: talked[date], n: sc.n, insufficient: sc.insufficient, score: sc.total,
			level: refConfidence(days, date, p).level, detected: change.detected, onLimit: change.onLimit,
			reasons: []string{},
		}
		if pt.onLimit {
			pt.detected = onLimitDetected(date)
		}

		if pt.n == 0 {
			pt.reasons = append(pt.reasons, "no_recent_records")
			previousStage, previousElevated = 0, 0
			points = append(points, pt)
			continue
		}

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

		// 오를 수 있는 가장 높은 단계를 먼저 정하고, 그날의 값으로 본 단계를 거기에 맞춘다.
		ceiling, blockedBy := previousStage+1, "held_one_step_per_day"
		if !pt.hasRecord {
			ceiling, blockedBy = previousStage, "held_no_record_today"
		}
		if pt.level == "low" {
			ceiling, blockedBy = previousStage, "held_low_confidence"
		}
		if pt.insufficient {
			ceiling, blockedBy = previousStage, "held_insufficient_records"
		}

		pt.stage = pt.raw
		if pt.raw > ceiling {
			pt.stage, pt.held = ceiling, true
			pt.reasons = append(pt.reasons, blockedBy)
		}
		if pt.insufficient && pt.raw < previousStage {
			pt.stage = previousStage
			pt.reasons = append(pt.reasons, "carried_insufficient_records")
		}

		switch {
		case pt.insufficient:
			pt.elevatedDays = previousElevated
		case pt.stage >= 2:
			pt.elevatedDays = previousElevated + 1
		}
		previousStage, previousElevated = pt.stage, pt.elevatedDays
		points = append(points, pt)
	}
	return points
}

// ---------------------------------------------------------------------------
// 참조 구현: 점 달력
// ---------------------------------------------------------------------------

type refTrendRow struct {
	marks        []string
	observedDays int
	usualDays    int
	usualTotal   int
	comparison   string
}

type refTrend struct {
	from, to int
	n        int
	rows     [3]refTrendRow
}

// refTrendItems는 점 달력의 세 줄이 보는 항목이다: 기분(흥미 저하, 우울감 가운데 하나라도), 수면, 에너지(피로).
var refTrendItems = [3][]int{{0, 1}, {2}, {3}}

// refTrendOf는 점 달력을 규칙 그대로 그린다.
//
//   - 칸은 점수가 보는 창의 날짜마다 하나다.
//   - 그 줄의 항목 가운데 하나라도 관찰됐으면 찬 점, 아니고 하나라도 이야기가 나왔으면 빈 점,
//     대화는 했지만 이야기가 없었으면 작은 점, 대화하지 않은 날은 빈칸이다.
//   - 기준선이 잡혀 있고 창 안의 기록이 모자라지 않으면, 창 안의 관찰 비율을 기준선 기간의 관찰 비율과 견준다.
//     퍼센트포인트로 Trend.MinDifferencePercent 이상 벌어지면 잦음이나 드묾이고, 아니면 비슷함이다.
func refTrendOf(days []refDay, asOf int, p params.Params) refTrend {
	tr := refTrend{from: asOf - (p.Window.Days - 1), to: asOf}
	base := refBaseline(days, asOf, p)

	for row, items := range refTrendItems {
		r := refTrendRow{comparison: "none"}
		for date := tr.from; date <= tr.to; date++ {
			mark := "no_conversation"
			for _, day := range days {
				if day.offset != date {
					continue
				}
				if row == 0 {
					tr.n++
				}
				mark = "not_mentioned"
				for _, item := range items {
					if day.status[item] == refNotObserved {
						mark = "not_observed"
					}
				}
				for _, item := range items {
					if day.status[item] == refObserved {
						mark = "observed"
					}
				}
			}
			if mark == "observed" {
				r.observedDays++
			}
			r.marks = append(r.marks, mark)
		}
		if base.established {
			r.usualDays, r.usualTotal = base.days, base.trendObserved[row]
		}
		tr.rows[row] = r
	}

	for row := range tr.rows {
		r := &tr.rows[row]
		if !base.established || tr.n < p.Window.MinConversationDays {
			continue
		}
		recent := 100 * float64(r.observedDays) / float64(tr.n)
		usual := 100 * float64(r.usualTotal) / float64(r.usualDays)
		switch limit := float64(p.Trend.MinDifferencePercent) - refFloatSlack; {
		case recent-usual >= limit:
			r.comparison = "more_often"
		case usual-recent >= limit:
			r.comparison = "less_often"
		default:
			r.comparison = "similar"
		}
	}
	return tr
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

// refStagePointDiff는 실제 구현의 하루를 참조 구현의 하루와 견줘 다른 점을 돌려준다.
func refStagePointDiff(h refHistory, want refStagePoint, got stage.Point) []string {
	var diffs []string
	name := "단계 " + h.date(want.offset).String()
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

// refEvaluationDiff는 실제 구현의 평가 하나를 참조 구현으로 처음부터 다시 계산한 값과 견준다.
func refEvaluationDiff(h refHistory, asOf int, p params.Params, got assess.Evaluation) string {
	days := refMerge(h)
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
	note("평가에 쓴 조정 값", p, got.Params)

	// 추정 점수
	sc := refScore(days, asOf, p)
	note("점수: 창의 첫날", h.date(sc.from), got.Score.Window.From)
	note("점수: 창의 마지막 날", h.date(sc.to), got.Score.Window.To)
	note("점수: 대화한 일수", sc.n, got.Score.ConversationDays)
	note("점수: 기록 부족", sc.insufficient, got.Score.Insufficient)
	note("점수: 추정 점수", sc.total, got.Score.Total)
	note("점수: 구간", sc.band, got.Score.Band.String())
	for item, id := range signal.AllItems() {
		res := got.Score.Item(id)
		note("점수: "+id.String()+" 관찰된 일수", sc.observed[item], res.ObservedDays)
		note("점수: "+id.String()+" 환산 일수", sc.converted[item], res.ConvertedDays)
		note("점수: "+id.String()+" 항목 점수", sc.points[item], res.Points)
	}

	// 신뢰도
	conf := refConfidence(days, asOf, p)
	note("신뢰도: 기준일", h.date(asOf), got.Confidence.AsOf)
	note("신뢰도: 대화한 일수", conf.n, got.Confidence.ConversationDays)
	note("신뢰도: 기록 부족", conf.insufficient, got.Confidence.Insufficient)
	note("신뢰도: 이야기가 나온 항목", conf.mentioned, got.Confidence.Mentioned)
	note("신뢰도: 관찰됨 판단 수", conf.observedJudgements, got.Confidence.ObservedJudgements)
	note("신뢰도: 직접 언급 판단 수", conf.directJudgements, got.Confidence.DirectJudgements)
	note("신뢰도: 기록 충실도", conf.record, got.Confidence.RecordCoverage.Float64())
	note("신뢰도: 항목 충족도", conf.item, got.Confidence.ItemCoverage.Float64())
	note("신뢰도: 근거 명시성", conf.explicitness, got.Confidence.Explicitness.Float64())
	note("신뢰도: 최종 값", conf.value, got.Confidence.Value.Float64())
	note("신뢰도: 가장 약한 요소", conf.limiting, got.Confidence.Limiting.String())
	note("신뢰도: 구간", conf.level, got.Confidence.Level.String())

	// 개인 기준선
	base := refBaseline(days, asOf, p)
	note("기준선: 기준일", h.date(asOf), got.Baseline.AsOf)
	note("기준선: 첫날", dateOrZero(base.hasStart, base.start), got.Baseline.Start)
	note("기준선: 마지막 날", dateOrZero(base.hasEnd, base.end), got.Baseline.End)
	note("기준선: 잡혔는가", base.established, got.Baseline.Established)
	note("기준선: 기간을 늘렸는가", base.extended, got.Baseline.Extended)
	note("기준선: 쓴 대화 일수", base.days, got.Baseline.Days)
	note("기준선: 관찰된 항목 수의 합", base.observedTotal, got.Baseline.ObservedTotal)
	for item, id := range signal.AllItems() {
		note("기준선: "+id.String()+" 관찰 비율", baseline.Rate{ObservedDays: base.itemObserved[item], Days: base.days}, got.Baseline.ItemRate(id))
	}

	// 변화 탐지
	change := refChange(days, asOf, p)
	note("변화 탐지: 기준일", h.date(asOf), got.Change.AsOf)
	note("변화 탐지: 돌고 있는가", change.running, got.Change.State.Running)
	note("변화 탐지: 쌓기 시작하는 날", dateOrZero(change.running, base.end+1), got.Change.From)
	if math.Abs(change.s-got.Change.State.S) > refFloatSlack {
		diffs = append(diffs, fmt.Sprintf("변화 탐지: 누적값: 참조 %v, 실제 %v", change.s, got.Change.State.S))
	}
	if change.onLimit {
		// 누적값이 한계값과 같은 날의 판정은 변화 탐지 쪽 시험이 따로 본다. 여기서는 실제 구현의 답을 받는다.
		change.detected = got.Change.State.Detected
	}
	note("변화 탐지: 감지", change.detected, got.Change.State.Detected)
	accumulated := 0
	for _, day := range days {
		if change.running && day.offset > base.end && day.offset <= asOf {
			accumulated++
		}
	}
	if got.Change.Series == nil {
		diffs = append(diffs, "변화 탐지: 흐름이 nil이다")
	}
	note("변화 탐지: 쌓은 날 수", accumulated, len(got.Change.Series))

	// 개입 단계
	wantStage := refStage(days, asOf, p, func(offset int) bool {
		return got.Change.StateAt(h.date(offset)).Detected
	})
	note("단계: 기준일", h.date(asOf), got.Stage.AsOf)
	note("단계: 흐름의 길이", len(wantStage), len(got.Stage.Series))
	if len(wantStage) == len(got.Stage.Series) {
		for i := range wantStage {
			diffs = append(diffs, refStagePointDiff(h, wantStage[i], got.Stage.Series[i])...)
			if len(diffs) > 12 {
				break
			}
		}
	}
	last := refStagePoint{offset: asOf, insufficient: true, level: "low", reasons: []string{"no_recent_records"}}
	if len(wantStage) > 0 {
		last = wantStage[len(wantStage)-1]
		note("단계: 첫날", h.date(wantStage[0].offset), got.Stage.From)
	}
	diffs = append(diffs, refStagePointDiff(h, last, got.Stage.State)...)

	// 점 달력
	tr := refTrendOf(days, asOf, p)
	note("점 달력: 첫날", h.date(tr.from), got.Trend.From)
	note("점 달력: 마지막 날", h.date(tr.to), got.Trend.To)
	note("점 달력: 대화한 일수", tr.n, got.Trend.ConversationDays)
	note("점 달력: 칸 수", p.Window.Days, len(got.Trend.Dates))
	for i, date := range got.Trend.Dates {
		note(fmt.Sprintf("점 달력: %d번째 칸의 날짜", i), h.date(tr.from+i), date)
	}
	for row, id := range signal.AllTrendRows() {
		gotRow := got.Trend.Row(id)
		name := "점 달력: " + id.String() + " 줄"
		note(name, id, gotRow.Row)
		marks := make([]string, 0, len(gotRow.Marks))
		for _, mark := range gotRow.Marks {
			marks = append(marks, mark.String())
		}
		if !slices.Equal(tr.rows[row].marks, marks) {
			diffs = append(diffs, fmt.Sprintf("%s의 표시: 참조 %v, 실제 %v", name, tr.rows[row].marks, marks))
		}
		note(name+"의 관찰된 일수", tr.rows[row].observedDays, gotRow.ObservedDays)
		note(name+"의 대화한 일수", tr.n, gotRow.ConversationDays)
		note(name+"의 평소", baseline.Rate{ObservedDays: tr.rows[row].usualTotal, Days: tr.rows[row].usualDays}, gotRow.Usual)
		note(name+"의 비교", tr.rows[row].comparison, gotRow.Comparison.String())
	}

	// 위기 관문에 넘기는 상태
	gate := got.GateState()
	note("관문: 점수가 기준 이상인가", !sc.insufficient && sc.total >= p.Crisis.EscalationMinScore, gate.ScoreElevated)
	// 창 안에 대화한 날이 하나도 없으면 변화 감지는 꺼진 것으로 전한다.
	note("관문: 변화 감지", change.detected && sc.n > 0, gate.ChangeDetected)

	return strings.Join(diffs, "\n")
}

// refAssessDisagreement는 신호 행에서 시작해 두 구현으로 평가를 구하고 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refAssessDisagreement(h refHistory, asOf int, p params.Params) string {
	got, err := assess.EvaluateRows(refToSignalRows(h), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	return refEvaluationDiff(h, asOf, p, got)
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return refAssessDisagreement(c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p, refAssessDisagreement(small, smallAsOf, p), small.date(smallAsOf), small.describe())
}

// refLivelyParams는 단계가 자주 움직이도록 경계를 낮춘 조정 값이다.
// 기본값에서는 가상 기록 대부분이 0단계와 1단계에 머물러서 위쪽 규칙을 거의 지나가지 않는다.
func refLivelyParams(t *testing.T, rng *rand.Rand) params.Params {
	t.Helper()
	p := params.Default()
	p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore = 2, 4, 9
	p.Stage.SustainedStage2Days = 2 + rng.IntN(6)
	p.Confidence.MediumMin, p.Confidence.HighMin = 0.5, 0.8
	p.Crisis.EscalationMinScore = 4
	p.Trend.MinDifferencePercent = 10
	require.NoError(t, p.Validate())
	return p
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

func TestReferenceEvaluate(t *testing.T) {
	t.Run("가상 기록에서 기본값으로 참조 구현과 같은 평가가 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			for _, asOf := range refAsOfPicks(rng, h) {
				if refAssessDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 평가가 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			if seed%3 == 0 {
				p = refLivelyParams(t, rng)
			}
			for _, asOf := range []int{h.span - 1, rng.IntN(h.span), h.span + rng.IntN(40)} {
				if refAssessDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("하루를 지우거나 신호 하나를 취소한 뒤에도 남은 기록으로 처음부터 계산한 값과 같다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			offsets := h.offsets()
			if len(offsets) == 0 {
				continue
			}
			rng := rand.New(rand.NewPCG(seed, 0xde1e7e))
			p := refLivelyParams(t, rng)
			asOf := h.span - 1 + rng.IntN(10)

			// 앞쪽 날일수록 기준선 기간에 들 가능성이 높다. 기준선이 다시 정해지는 길을 자주 지나가게 한다.
			deleted := h.clone()
			delete(deleted.rows, offsets[rng.IntN(min(len(offsets), 10))])
			if refAssessDisagreement(deleted, asOf, p) != "" {
				refFailShrunk(t, seed, deleted, asOf, p)
			}

			cancelled := h.clone()
			offset := offsets[rng.IntN(len(offsets))]
			if len(cancelled.rows[offset]) > 0 {
				cancelled.rows[offset][rng.IntN(len(cancelled.rows[offset]))].cancelled = true
			}
			if refAssessDisagreement(cancelled, asOf, p) != "" {
				refFailShrunk(t, seed, cancelled, asOf, p)
			}
		}
	})
}

func TestReferenceEvaluateProperties(t *testing.T) {
	evaluate := func(t *testing.T, h refHistory, asOf int, p params.Params) assess.Evaluation {
		t.Helper()
		got, err := assess.EvaluateRows(refToSignalRows(h), h.date(asOf), p)
		require.NoError(t, err)
		return got
	}

	t.Run("같은 기록에서는 몇 번을 계산해도 같은 평가다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xd1ce))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span + 20)
			first := evaluate(t, h, asOf, p)
			for range 2 {
				// 맵을 새로 만들어 도는 순서가 달라져도 결과가 같아야 한다.
				require.Equal(t, first, evaluate(t, h, asOf, p), "씨앗 %d", seed)
			}
		}
	})

	t.Run("행의 순서와 하루의 순서를 섞어도 같은 평가다", func(t *testing.T) {
		for seed := range refCount(500) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x5aff1e))
			p := refLivelyParams(t, rng)
			asOf := h.span - 1
			want := evaluate(t, h, asOf, p)

			shuffled := h.clone()
			for _, rows := range shuffled.rows {
				rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
			}
			require.Equal(t, want, evaluate(t, shuffled, asOf, p), "씨앗 %d: 행을 섞었다", seed)

			// 참조 구현이 합친 하루를 그대로 넘겨도 같은 평가다. 신호 행에서 시작하는 입구와 하루에서 시작하는 입구가 같은 말을 한다.
			days := refToSignalDays(h, refMerge(h))
			fromDays, err := assess.Evaluate(days, h.date(asOf), p)
			require.NoError(t, err)
			require.Equal(t, want, fromDays, "씨앗 %d: 하루의 목록으로 넘겼다", seed)

			// 하루의 목록으로 넘길 때는 날짜순이어야 한다. 섞인 목록은 정렬을 거치면 같은 평가가 된다.
			mixed := slices.Clone(days)
			rng.Shuffle(len(mixed), func(i, j int) { mixed[i], mixed[j] = mixed[j], mixed[i] })
			sorted, err := signal.SortDays(mixed)
			require.NoError(t, err)
			got, err := assess.Evaluate(sorted, h.date(asOf), p)
			require.NoError(t, err)
			require.Equal(t, want, got, "씨앗 %d: 하루를 섞었다가 정렬했다", seed)

			if len(days) >= 2 {
				slices.Reverse(days)
				failed, err := assess.Evaluate(days, h.date(asOf), p)
				require.ErrorIs(t, err, signal.ErrUnsortedDays, "씨앗 %d", seed)
				require.Equal(t, assess.Evaluation{}, failed, "씨앗 %d: 오류인데 일부가 계산돼 있다", seed)
			}
		}
	})

	t.Run("기준일의 평가는 기준일보다 뒤의 날에 기대지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xf00d))
			p := refGenParams(t, rng)
			if seed%2 == 0 {
				p = refLivelyParams(t, rng)
			}
			asOf := rng.IntN(h.span)

			past := h.clone()
			for offset := range past.rows {
				if offset > asOf {
					delete(past.rows, offset)
				}
			}
			// 뒤의 날을 아예 다른 기록으로 바꿔도 마찬가지다.
			rewritten := past.clone()
			other := refGenHistory(t, seed+1_000_000)
			for offset, rows := range other.rows {
				if offset > asOf {
					rewritten.rows[offset] = rows
				}
			}

			want := evaluate(t, h, asOf, p)
			require.Equal(t, want, evaluate(t, past, asOf, p), "씨앗 %d: 뒤의 날을 지웠다", seed)
			require.Equal(t, want, evaluate(t, rewritten, asOf, p), "씨앗 %d: 뒤의 날을 다른 기록으로 바꿨다", seed)
		}
	})

	t.Run("관찰됨 판단을 더하면 그 항목의 관찰된 일수가 줄지 않는다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xadd))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span + 10)
			item := rng.IntN(refItems)
			id := signal.AllItems()[item]

			// 이미 대화한 날에 더하기도 하고, 대화하지 않은 날에 새로 더하기도 한다.
			added := h.clone()
			offset := rng.IntN(h.span)
			added.rows[offset] = append(added.rows[offset], refRow{item: item, status: refObserved, direct: rng.IntN(2) == 0})

			before, after := evaluate(t, h, asOf, p), evaluate(t, added, asOf, p)
			require.GreaterOrEqual(t, after.Score.Item(id).ObservedDays, before.Score.Item(id).ObservedDays,
				"씨앗 %d: %s에 %s 관찰됨을 더했다", seed, h.date(offset), id)
			for _, other := range signal.AllItems() {
				if other != id {
					require.Equal(t, before.Score.Item(other).ObservedDays, after.Score.Item(other).ObservedDays,
						"씨앗 %d: 다른 항목 %s의 관찰된 일수가 달라졌다", seed, other)
				}
			}
			for _, row := range signal.AllTrendRows() {
				require.GreaterOrEqual(t, after.Trend.Row(row).ObservedDays, before.Trend.Row(row).ObservedDays, "씨앗 %d, %s", seed, row)
			}
			if !before.Score.Insufficient {
				require.GreaterOrEqual(t, after.Score.Item(id).Points, before.Score.Item(id).Points, "씨앗 %d", seed)
			}
		}
	})

	t.Run("단계는 대화한 날에만, 하루에 한 단계씩, 신뢰도가 낮지 않고 기록이 충분한 날에만 오른다", func(t *testing.T) {
		rises := 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x10ad))
			p := refLivelyParams(t, rng)
			got := evaluate(t, h, h.span+20, p)
			days := refMerge(h)
			talked := map[int]bool{}
			for _, day := range days {
				talked[day.offset] = true
			}

			previous := stage.Everyday
			for i, pt := range got.Stage.Series {
				// 실제 구현이 적어 둔 값이 아니라, 그 날짜를 기준일로 따로 구한 점수와 신뢰도로 확인한다.
				offset := i + got.Stage.From.DaysSince(h.date(0))
				sc := refScore(days, offset, p)
				if sc.insufficient || refConfidence(days, offset, p).level == "low" || !talked[offset] {
					require.LessOrEqual(t, pt.Stage, previous, "씨앗 %d: %s에 단계가 올랐다\n%s", seed, pt.Date, refLazy(h.describe))
				}
				require.LessOrEqual(t, pt.Stage, previous+1, "씨앗 %d: %s에 두 단계 넘게 올랐다\n%s", seed, pt.Date, refLazy(h.describe))
				if sc.insufficient && sc.n > 0 {
					require.Equal(t, previous, pt.Stage, "씨앗 %d: 기록 부족인 %s에 단계가 움직였다\n%s", seed, pt.Date, refLazy(h.describe))
				}
				if pt.Stage > previous {
					rises++
				}
				previous = pt.Stage
			}
		}
		require.Positive(t, rises, "단계가 한 번도 오르지 않았다면 위의 확인은 아무것도 보지 않은 것이다")
	})

	t.Run("창 안에 대화한 날이 없으면 관문에는 변화 감지를 꺼진 것으로 전한다", func(t *testing.T) {
		silenced := 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x6a7e))
			p := refGenParams(t, rng)
			got := evaluate(t, h, h.span+rng.IntN(40), p)

			if got.Score.ConversationDays == 0 {
				require.False(t, got.GateState().ChangeDetected, "씨앗 %d", seed)
				require.False(t, got.GateState().ScoreElevated, "씨앗 %d", seed)
				if got.Change.State.Detected {
					silenced++
				}
			} else {
				require.Equal(t, got.Change.State.Detected, got.GateState().ChangeDetected, "씨앗 %d", seed)
			}
		}
		require.Positive(t, silenced, "감지가 켜진 채로 창이 빈 기록이 하나도 없었다면 위의 확인은 아무것도 보지 않은 것이다")
	})

	t.Run("대화 도중에 읽는 평가는 오늘의 기록이 있으면 오늘을, 없으면 어제를 기준일로 한 평가다", func(t *testing.T) {
		// 오늘의 기록이 있는지는 실제 구현이 합친 하루가 아니라 가상 기록의 행에서 바로 읽는다.
		usedToday, usedYesterday, keptScore := 0, 0, 0
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x11fe))
			p := refLivelyParams(t, rng)
			days := refToSignalDays(h, refMerge(h))
			today := 1 + rng.IntN(h.span+20)

			live, err := assess.EvaluateLive(days, h.date(today), p)
			require.NoError(t, err)

			asOf := today - 1
			if len(h.rows[today]) > 0 {
				asOf = today
				usedToday++
			} else {
				usedYesterday++
			}
			want, err := assess.Evaluate(days, h.date(asOf), p)
			require.NoError(t, err)
			require.Equal(t, want, live, "씨앗 %d, 오늘 %s", seed, h.date(today))

			// 오늘을 그대로 기준일로 삼았다면 창에서 하루가 밀려나 점수가 사라졌을 자리다.
			if asOf != today && !live.Score.Insufficient && refScore(refMerge(h), today, p).insufficient {
				keptScore++
			}
		}
		require.Positive(t, usedToday)
		require.Positive(t, usedYesterday)
		require.Positive(t, keptScore, "어제를 기준일로 삼아서 점수를 지킨 경우가 하나도 없었다")
	})

	t.Run("어떤 기록의 평가든 JSON으로 적을 수 있다", func(t *testing.T) {
		// 기준선이 아직 없거나 기록이 하나도 없을 때처럼 빈 날짜가 섞이는 평가도 내부 확인 화면까지 나가야 한다.
		for seed := range refCount(500) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x150d))
			got := evaluate(t, h, rng.IntN(h.span+30), refGenParams(t, rng))
			encoded, err := json.Marshal(got)
			require.NoError(t, err, "씨앗 %d", seed)
			require.True(t, json.Valid(encoded), "씨앗 %d", seed)
		}
	})

	t.Run("평가의 부분들이 같은 날, 같은 기록을 말한다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := refGenParams(t, rng)
			got := evaluate(t, h, rng.IntN(h.span+30), p)

			require.Equal(t, got.Score.ConversationDays, got.Confidence.ConversationDays, "씨앗 %d", seed)
			require.Equal(t, got.Score.ConversationDays, got.Trend.ConversationDays, "씨앗 %d", seed)
			require.Equal(t, got.Score.ConversationDays, got.Stage.State.ConversationDays, "씨앗 %d", seed)
			require.Equal(t, got.Score.Insufficient, got.Confidence.Insufficient, "씨앗 %d", seed)
			require.Equal(t, got.Score.Insufficient, got.Stage.State.Insufficient, "씨앗 %d", seed)
			require.Equal(t, got.Score.Total, got.Stage.State.Score, "씨앗 %d", seed)
			require.Equal(t, got.Confidence.Level, got.Stage.State.Confidence, "씨앗 %d", seed)
			require.Equal(t, got.Change.State.Detected, got.Stage.State.Detected, "씨앗 %d", seed)
			require.Equal(t, got.Baseline.Established, got.Change.State.Running, "씨앗 %d", seed)
			require.Equal(t, got.Score.Window.From, got.Trend.From, "씨앗 %d", seed)
			for _, pt := range got.Stage.Series {
				require.Equal(t, got.Change.StateAt(pt.Date).Detected, pt.Detected, "씨앗 %d, %s", seed, pt.Date)
			}
		}
	})
}

// TestReferenceDefaults는 조정 값의 기본값을 숫자 그대로 적어 둔 것과 견준다.
// 두 구현이 같은 기본값을 읽어 쓰기 때문에, 기본값 자체가 틀리면 위의 견주기로는 드러나지 않는다.
func TestReferenceDefaults(t *testing.T) {
	p := params.Default()
	require.NoError(t, p.Validate())

	t.Run("창은 기준일을 포함한 14일이고 대화한 날이 7일 미만이면 기록 부족이다", func(t *testing.T) {
		assert.Equal(t, params.Window{Days: 14, MinConversationDays: 7}, p.Window)
	})
	t.Run("항목 점수의 경계는 1일, 7일, 12일이고 구간의 경계는 5, 10, 15, 20점이다", func(t *testing.T) {
		assert.Equal(t, params.Score{
			ItemScore1MinDays: 1, ItemScore2MinDays: 7, ItemScore3MinDays: 12,
			MildMin: 5, ModerateMin: 10, ModeratelySevereMin: 15, SevereMin: 20,
		}, p.Score)
	})
	t.Run("신뢰도는 0.4 미만이 낮음이고 0.7 이상이 높음이다", func(t *testing.T) {
		assert.Equal(t, params.Confidence{MediumMin: 0.4, HighMin: 0.7}, p.Confidence)
	})
	t.Run("기준선은 첫 14일이고 대화한 날이 7일은 있어야 한다", func(t *testing.T) {
		assert.Equal(t, params.Baseline{WindowDays: 14, MinConversationDays: 7}, p.Baseline)
	})
	t.Run("변화 탐지의 허용 여유는 0.5, 한계값은 4이고 하루에 2까지만 늘며 누적값은 한계값의 2배를 넘지 못한다", func(t *testing.T) {
		assert.Equal(t, params.CUSUM{K: 0.5, H: 4, MaxStep: 2, MaxS: 2}, p.CUSUM)
	})
	t.Run("단계의 경계는 5, 10, 15점이고 2단계 이상이 14일째 이어지는 날부터 3단계다", func(t *testing.T) {
		assert.Equal(t, params.Stage{Stage1MinScore: 5, Stage2MinScore: 10, Stage3MinScore: 15, SustainedStage2Days: 14}, p.Stage)
	})
	t.Run("관문은 10점 이상에서 올리고, 14일 안의 세 번째 대화와 7일의 민감 기간을 본다", func(t *testing.T) {
		assert.Equal(t, params.Crisis{EscalationMinScore: 10, RepeatWindowDays: 14, RepeatCount: 3, SensitiveWindowDays: 7}, p.Crisis)
	})
}

func TestReferenceEmptyEvaluation(t *testing.T) {
	asOf, err := recorddate.Parse("2026-07-01")
	require.NoError(t, err)
	p := params.Default()

	check := func(t *testing.T, got assess.Evaluation) {
		t.Helper()
		require.Equal(t, asOf, got.AsOf)
		require.Equal(t, p, got.Params)

		_, hasScore := got.Score.Score()
		assert.False(t, hasScore, "기록이 없는데 점수가 있다")
		assert.True(t, got.Score.Insufficient)
		assert.Zero(t, got.Score.ConversationDays)
		for _, item := range got.Score.Items {
			assert.Zero(t, item.ObservedDays)
		}

		assert.True(t, got.Confidence.Insufficient)
		assert.Equal(t, confidence.Low, got.Confidence.Level)
		assert.True(t, got.Confidence.Value.IsZero())

		assert.Equal(t, baseline.Baseline{AsOf: asOf}, got.Baseline)

		assert.Equal(t, asOf, got.Change.AsOf)
		assert.Empty(t, got.Change.Series)
		assert.False(t, got.Change.State.Running)
		assert.False(t, got.Change.State.Detected)

		assert.Empty(t, got.Stage.Series)
		assert.True(t, got.Stage.From.IsZero())
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, []stage.Reason{stage.ReasonNoRecentRecords}, got.Stage.State.Reasons)

		assert.Zero(t, got.Trend.ConversationDays)
		assert.Len(t, got.Trend.Dates, p.Window.Days)
		for _, row := range got.Trend.Rows {
			assert.Zero(t, row.ObservedDays)
			assert.Equal(t, assess.ComparisonNone, row.Comparison)
			for _, mark := range row.Marks {
				assert.Equal(t, assess.MarkNoConversation, mark)
			}
		}

		assert.Equal(t, crisis.State{}, got.GateState())
	}

	t.Run("기록이 하나도 없으면 빈 평가다", func(t *testing.T) {
		got, err := assess.Evaluate(nil, asOf, p)
		require.NoError(t, err)
		check(t, got)
	})

	t.Run("날을 모두 지운 평가는 기록이 처음부터 없던 평가와 똑같다", func(t *testing.T) {
		want, err := assess.Evaluate(nil, asOf, p)
		require.NoError(t, err)

		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			h.base = asOf.AddDays(-h.span)

			// 열쇠째로 지운 경우와, 날짜는 남았지만 행이 하나도 없는 경우 둘 다 본다.
			emptied := refToSignalRows(h)
			for date := range emptied {
				emptied[date] = nil
			}
			for _, rows := range []map[recorddate.Date][]signal.Row{{}, nil, emptied} {
				got, err := assess.EvaluateRows(rows, asOf, p)
				require.NoError(t, err)
				require.Equal(t, want, got, "씨앗 %d", seed)
			}
		}
		check(t, want)
	})

	t.Run("기준일이 첫 기록보다 앞이면 빈 평가다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			h.base = asOf.AddDays(1)
			got, err := assess.EvaluateRows(refToSignalRows(h), asOf, p)
			require.NoError(t, err)
			check(t, got)
		}
	})

	t.Run("빈 Evaluation은 관문에 빈 상태를 넘긴다", func(t *testing.T) {
		assert.Equal(t, crisis.State{}, assess.Evaluation{}.GateState())
	})
}
