package confidence_test

// 이 파일은 신뢰도의 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 실제 구현은 세 요소를 분수로 들고 정수 곱셈으로 크기를 견준다. 참조 구현은 규칙 문장 그대로 소수로 나누고
// 소수끼리 견준다. 창도 날짜를 하루씩 짚어 가며 기록을 처음부터 찾는다. 느리지만 틀리기 어렵다.
// 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
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

// refResultDiff는 실제 구현의 결과 하나를 참조 구현의 결과와 견줘 다른 점을 돌려준다.
func refResultDiff(h refHistory, asOf int, p params.Params, want refConfidenceResult, got confidence.Result) string {
	var diffs []string
	note := func(name string, want, got any) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s %s: 참조 %v, 실제 %v", h.date(asOf), name, want, got))
		}
	}
	note("기준일", h.date(asOf), got.AsOf)
	note("대화한 일수", want.n, got.ConversationDays)
	note("기록 부족", want.insufficient, got.Insufficient)
	note("이야기가 나온 항목", want.mentioned, got.Mentioned)
	note("이야기가 나온 항목 수", want.mentionedItems, got.MentionedItems)
	note("관찰됨 판단 수", want.observedJudgements, got.ObservedJudgements)
	note("직접 언급 판단 수", want.directJudgements, got.DirectJudgements)

	note("기록 충실도", want.record, got.RecordCoverage.Float64())
	note("기록 충실도의 분수", confidence.Ratio{Num: want.n, Den: p.Window.Days}, got.RecordCoverage)
	note("항목 충족도", want.item, got.ItemCoverage.Float64())
	note("항목 충족도의 분수", confidence.Ratio{Num: want.mentionedItems, Den: refItems}, got.ItemCoverage)
	note("근거 명시성", want.explicitness, got.Explicitness.Float64())
	if want.observedJudgements == 0 {
		note("근거 명시성의 분수", confidence.Ratio{Num: 1, Den: 1}, got.Explicitness)
	} else {
		note("근거 명시성의 분수", confidence.Ratio{Num: want.directJudgements, Den: want.observedJudgements}, got.Explicitness)
	}

	note("최종 값", want.value, got.Value.Float64())
	note("최종 값이 비었는가", want.insufficient, got.Value.IsZero())
	note("가장 약한 요소", want.limiting, got.Limiting.String())
	note("구간", want.level, got.Level.String())

	var missing []signal.Item
	for item, id := range signal.AllItems() {
		if !want.mentioned[item] {
			missing = append(missing, id)
		}
	}
	if !slices.Equal(missing, got.MissingItems()) {
		diffs = append(diffs, fmt.Sprintf("%s 빠진 항목: 참조 %v, 실제 %v", h.date(asOf), missing, got.MissingItems()))
	}
	return strings.Join(diffs, "\n")
}

// refConfidenceDisagreement는 기록 하나, 기준일 하나를 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refConfidenceDisagreement(h refHistory, asOf int, p params.Params) string {
	days := refMerge(h)
	got, err := confidence.Compute(refToSignalDays(h, days), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	return refResultDiff(h, asOf, p, refConfidence(days, asOf, p), got)
}

// refSeriesDisagreement는 첫날부터 기준일까지 날마다의 신뢰도를 두 구현으로 계산해 어긋난 점을 돌려준다.
func refSeriesDisagreement(h refHistory, asOf int, p params.Params) string {
	days := refMerge(h)
	got, err := confidence.Series(refToSignalDays(h, days), h.date(0), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	if len(got) != asOf+1 {
		return fmt.Sprintf("날짜 수가 다르다: 참조 %d, 실제 %d", asOf+1, len(got))
	}
	for date := 0; date <= asOf; date++ {
		if diff := refResultDiff(h, date, p, refConfidence(days, date, p), got[date]); diff != "" {
			return diff
		}
	}
	return ""
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params, disagreement func(refHistory, int, params.Params) string) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return disagreement(c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p, disagreement(small, smallAsOf, p), small.date(smallAsOf), small.describe())
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

func TestReferenceConfidence(t *testing.T) {
	t.Run("가상 기록 수천 개에서 기본값으로 참조 구현과 같은 신뢰도가 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			for _, asOf := range refAsOfPicks(rng, h) {
				if refConfidenceDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p, refConfidenceDisagreement)
				}
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 신뢰도가 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			for _, asOf := range refAsOfPicks(rng, h) {
				if refConfidenceDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p, refConfidenceDisagreement)
				}
			}
		}
	})

	t.Run("날마다의 흐름도 날짜 하나하나를 처음부터 계산한 값과 같다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount / 5) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0x5e71))
			p := refGenParams(t, rng)
			asOf := h.span - 1 + rng.IntN(30)
			if refSeriesDisagreement(h, asOf, p) != "" {
				refFailShrunk(t, seed, h, asOf, p, refSeriesDisagreement)
			}
		}
	})

	t.Run("구간은 분수로 정확히 견줘도 같다", func(t *testing.T) {
		// 경계는 소수로 적혀 있다(0.4, 0.7). 적힌 글자 그대로를 분수로 읽어서 최종 값과 정확히 견준다.
		// 소수 나눗셈의 오차가 구간을 바꾸는 일이 없는지 보는 것이다.
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xe4ac7))
			p := refGenParams(t, rng)
			got, err := confidence.Compute(refToSignalDays(h, refMerge(h)), h.date(rng.IntN(h.span+10)), p)
			require.NoError(t, err)
			if got.Insufficient {
				continue
			}

			medium, ok := new(big.Rat).SetString(strconv.FormatFloat(p.Confidence.MediumMin, 'g', -1, 64))
			require.True(t, ok)
			high, ok := new(big.Rat).SetString(strconv.FormatFloat(p.Confidence.HighMin, 'g', -1, 64))
			require.True(t, ok)

			value := big.NewRat(int64(got.RecordCoverage.Num), int64(got.RecordCoverage.Den))
			for _, component := range []confidence.Ratio{got.ItemCoverage, got.Explicitness} {
				if c := big.NewRat(int64(component.Num), int64(component.Den)); c.Cmp(value) < 0 {
					value = c
				}
			}
			want := "high"
			switch {
			case value.Cmp(medium) < 0:
				want = "low"
			case value.Cmp(high) < 0:
				want = "medium"
			}
			require.Equal(t, want, got.Level.String(), "씨앗 %d: 최종 값 %s, 경계 %v와 %v", seed, value, p.Confidence.MediumMin, p.Confidence.HighMin)
			require.Zero(t, value.Cmp(big.NewRat(int64(got.Value.Num), int64(got.Value.Den))), "씨앗 %d: 최종 값이 가장 작은 요소가 아니다", seed)
		}
	})

	t.Run("경계에 딱 걸린 값은 위 구간이다", func(t *testing.T) {
		// 5분의 2는 0.4 이상이라 보통이고, 10분의 7은 0.7 이상이라 높음이다. 근거 명시성으로 그 값을 만든다.
		base, err := recorddate.Parse("2026-05-01")
		require.NoError(t, err)
		cases := []struct {
			name             string
			direct, indirect int
			want             string
		}{
			{"직접 언급이 다섯 가운데 둘이면 보통이다", 2, 3, "medium"},
			{"직접 언급이 다섯 가운데 하나면 낮음이다", 1, 4, "low"},
			{"직접 언급이 열 가운데 일곱이면 높음이다", 7, 3, "high"},
			{"직접 언급이 열 가운데 여섯이면 보통이다", 6, 4, "medium"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := refHistory{base: base, span: 14, rows: map[int][]refRow{}}
				for offset := range 14 {
					// 여덟 항목이 모두 나오고 열나흘을 모두 대화해서, 근거 명시성만 최종 값을 정한다.
					for item := range refItems {
						h.rows[offset] = append(h.rows[offset], refRow{item: item, status: refNotObserved, direct: true})
					}
				}
				for i := range tc.direct + tc.indirect {
					h.rows[i][0] = refRow{item: 0, status: refObserved, direct: i < tc.direct}
				}
				require.Empty(t, refConfidenceDisagreement(h, 13, params.Default()))

				got, err := confidence.Compute(refToSignalDays(h, refMerge(h)), h.date(13), params.Default())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Level.String())
				assert.Equal(t, confidence.ComponentExplicitness, got.Limiting)
			})
		}
	})
}

func TestReferenceConfidenceProperties(t *testing.T) {
	compute := func(t *testing.T, days []signal.Day, asOf recorddate.Date, p params.Params) confidence.Result {
		t.Helper()
		got, err := confidence.Compute(days, asOf, p)
		require.NoError(t, err)
		return got
	}

	t.Run("같은 기록에서는 몇 번을 계산해도 같은 값이다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xd1ce))
			p := refGenParams(t, rng)
			days := refToSignalDays(h, refMerge(h))
			asOf := h.date(rng.IntN(h.span + 20))
			first := compute(t, days, asOf, p)
			for range 3 {
				require.Equal(t, first, compute(t, slices.Clone(days), asOf, p), "씨앗 %d", seed)
			}
		}
	})

	t.Run("받은 목록을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			days := refToSignalDays(h, refMerge(h))
			before := slices.Clone(days)
			compute(t, days, h.date(h.span-1), params.Default())
			_, err := confidence.Series(days, h.date(0), h.date(h.span-1), params.Default())
			require.NoError(t, err)
			require.Equal(t, before, days, "씨앗 %d", seed)
		}
	})

	t.Run("기준일보다 뒤의 날과 창보다 앞의 날은 결과를 바꾸지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xf00d))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span)
			all := refMerge(h)

			var upToAsOf, insideWindow []refDay
			for _, day := range all {
				if day.offset <= asOf {
					upToAsOf = append(upToAsOf, day)
				}
				if day.offset <= asOf && day.offset > asOf-p.Window.Days {
					insideWindow = append(insideWindow, day)
				}
			}
			want := compute(t, refToSignalDays(h, all), h.date(asOf), p)
			require.Equal(t, want, compute(t, refToSignalDays(h, upToAsOf), h.date(asOf), p), "씨앗 %d: 뒤의 날을 뺐다", seed)
			require.Equal(t, want, compute(t, refToSignalDays(h, insideWindow), h.date(asOf), p), "씨앗 %d: 창 밖의 날을 뺐다", seed)
		}
	})

	t.Run("기록 부족이면 언제나 낮음이고 최종 값이 비어 있다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := refGenParams(t, rng)
			got := compute(t, refToSignalDays(h, refMerge(h)), h.date(rng.IntN(h.span+40)), p)

			require.Equal(t, got.ConversationDays < p.Window.MinConversationDays, got.Insufficient, "씨앗 %d", seed)
			if got.Insufficient {
				require.Equal(t, confidence.Low, got.Level, "씨앗 %d", seed)
				require.True(t, got.Value.IsZero(), "씨앗 %d", seed)
				require.Equal(t, confidence.ComponentNone, got.Limiting, "씨앗 %d", seed)
				continue
			}
			// 최종 값은 세 요소 어느 것보다도 크지 않다.
			for _, component := range []confidence.Ratio{got.RecordCoverage, got.ItemCoverage, got.Explicitness} {
				require.LessOrEqual(t, got.Value.Compare(component), 0, "씨앗 %d", seed)
			}
		}
	})

	t.Run("관찰됨 판단을 직접 언급으로 더하면 기록 충실도와 항목 충족도가 줄지 않는다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xadd))
			p := refGenParams(t, rng)
			asOf := rng.IntN(h.span + 10)

			added := h.clone()
			offset := rng.IntN(h.span)
			added.rows[offset] = append(added.rows[offset], refRow{item: rng.IntN(refItems), status: refObserved, direct: true})

			before := compute(t, refToSignalDays(h, refMerge(h)), h.date(asOf), p)
			after := compute(t, refToSignalDays(added, refMerge(added)), added.date(asOf), p)

			require.GreaterOrEqual(t, after.RecordCoverage.Compare(before.RecordCoverage), 0, "씨앗 %d", seed)
			require.GreaterOrEqual(t, after.ItemCoverage.Compare(before.ItemCoverage), 0, "씨앗 %d", seed)
			require.GreaterOrEqual(t, after.Explicitness.Compare(before.Explicitness), 0, "씨앗 %d", seed)
			if !before.Insufficient {
				require.GreaterOrEqual(t, after.Level, before.Level, "씨앗 %d", seed)
			}
		}
	})

	t.Run("날짜순이 아닌 기록은 받지 않고 정렬해서 넣으면 같은 값이다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			days := refToSignalDays(h, refMerge(h))
			if len(days) < 2 {
				continue
			}
			want := compute(t, days, h.date(h.span-1), params.Default())

			reversed := slices.Clone(days)
			slices.Reverse(reversed)
			_, err := confidence.Compute(reversed, h.date(h.span-1), params.Default())
			require.ErrorIs(t, err, signal.ErrUnsortedDays, "씨앗 %d", seed)

			sorted, err := signal.SortDays(reversed)
			require.NoError(t, err)
			assert.Equal(t, want, compute(t, sorted, h.date(h.span-1), params.Default()), "씨앗 %d", seed)
		}
	})
}
