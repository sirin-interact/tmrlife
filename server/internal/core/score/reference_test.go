package score_test

// 이 파일은 추정 점수의 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 실제 구현은 반올림까지 정수로 한다. 참조 구현은 규칙 문장 그대로 소수로 나누고 0.5를 더해 내림한다.
// 창도 날짜를 하루씩 짚어 가며 기록을 처음부터 찾는다. 느리지만 틀리기 어렵다.
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

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
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

// refDefaultPoints와 refDefaultBand는 기본값의 표를 숫자 그대로 적은 것이다.
// 조정 값의 기본값이 표와 어긋나 있으면 refScore는 함께 어긋나므로, 기본값일 때는 이 표로 한 번 더 본다.
func refDefaultPoints(convertedDays int) int {
	switch {
	case convertedDays == 0:
		return 0
	case convertedDays <= 6:
		return 1
	case convertedDays <= 11:
		return 2
	default:
		return 3
	}
}

func refDefaultBand(total int) string {
	switch {
	case total <= 4:
		return "minimal"
	case total <= 9:
		return "mild"
	case total <= 14:
		return "moderate"
	case total <= 19:
		return "moderately_severe"
	default:
		return "severe"
	}
}

// refScoreDisagreement는 기록 하나, 기준일 하나를 두 구현으로 계산해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refScoreDisagreement(h refHistory, asOf int, p params.Params) string {
	days := refMerge(h)
	want := refScore(days, asOf, p)
	got, err := score.Compute(refToSignalDays(h, days), h.date(asOf), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}

	var diffs []string
	note := func(name string, want, got any) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s: 참조 %v, 실제 %v", name, want, got))
		}
	}
	note("창의 첫날", h.date(want.from), got.Window.From)
	note("창의 마지막 날", h.date(want.to), got.Window.To)
	note("대화한 일수", want.n, got.ConversationDays)
	note("기록 부족", want.insufficient, got.Insufficient)
	note("추정 점수", want.total, got.Total)
	note("구간", want.band, got.Band.String())
	total, ok := got.Score()
	note("Score()의 점수", want.total, total)
	note("Score()의 있음", !want.insufficient, ok)
	for item, id := range signal.AllItems() {
		res := got.Item(id)
		note(id.String()+" 항목", id, res.Item)
		note(id.String()+" 관찰된 일수", want.observed[item], res.ObservedDays)
		note(id.String()+" 환산 일수", want.converted[item], res.ConvertedDays)
		note(id.String()+" 항목 점수", want.points[item], res.Points)
	}
	return strings.Join(diffs, "\n")
}

// refFailShrunk는 어긋난 기록을 가장 작게 줄여서 보여주고 시험을 멈춘다.
func refFailShrunk(t *testing.T, seed uint64, h refHistory, asOf int, p params.Params) {
	t.Helper()
	small, smallAsOf := refShrink(h, asOf, func(c refHistory, a int) bool { return refScoreDisagreement(c, a, p) != "" })
	require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 기록 (기준일 %s):\n%s",
		seed, p, refScoreDisagreement(small, smallAsOf, p), small.date(smallAsOf), small.describe())
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

func TestReferenceScore(t *testing.T) {
	t.Run("가상 기록 수천 개에서 기본값으로 참조 구현과 같은 점수가 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			for _, asOf := range refAsOfPicks(rng, h) {
				if refScoreDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("조정 값을 바꿔도 참조 구현과 같은 점수가 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xa50f))
			p := refGenParams(t, rng)
			for _, asOf := range refAsOfPicks(rng, h) {
				if refScoreDisagreement(h, asOf, p) != "" {
					refFailShrunk(t, seed, h, asOf, p)
				}
			}
		}
	})

	t.Run("기본값에서는 적힌 표 그대로의 항목 점수와 구간이 나온다", func(t *testing.T) {
		p := params.Default()
		for seed := range refCount(refHistoryCount / 3) {
			h := refGenHistory(t, seed)
			days := refToSignalDays(h, refMerge(h))
			got, err := score.Compute(days, h.date(h.span-1), p)
			require.NoError(t, err)
			if got.Insufficient {
				continue
			}
			sum := 0
			for _, item := range got.Items {
				require.Equal(t, refDefaultPoints(item.ConvertedDays), item.Points, "씨앗 %d, %s", seed, item.Item)
				sum += item.Points
			}
			require.Equal(t, sum, got.Total, "씨앗 %d", seed)
			require.Equal(t, refDefaultBand(got.Total), got.Band.String(), "씨앗 %d", seed)
		}
	})

	t.Run("대화한 일수와 관찰된 일수의 모든 조합에서 환산 일수가 0.5에서 올리는 반올림이다", func(t *testing.T) {
		// 분수로 정확히 계산한 값과 견준다. 소수도 정수 나눗셈도 쓰지 않는 세 번째 길이다.
		p := params.Default()
		base, err := recorddate.Parse("2026-05-01")
		require.NoError(t, err)
		for n := 1; n <= 14; n++ {
			for o := 0; o <= n; o++ {
				h := refHistory{base: base, span: 14, rows: map[int][]refRow{}}
				for offset := range n {
					row := refRow{item: 2, status: refNotObserved, direct: true}
					if offset < o {
						row.status = refObserved
					}
					h.rows[offset] = []refRow{row}
				}
				got, err := score.Compute(refToSignalDays(h, refMerge(h)), h.date(13), p)
				require.NoError(t, err)
				require.Equal(t, n < 7, got.Insufficient, "n=%d", n)
				if got.Insufficient {
					continue
				}

				half := big.NewRat(1, 2)
				exact := new(big.Rat).Add(big.NewRat(int64(14*o), int64(n)), half)
				rounded := new(big.Int).Quo(exact.Num(), exact.Denom())
				require.Equal(t, rounded.Int64(), int64(got.Item(signal.Sleep).ConvertedDays), "n=%d, o=%d", n, o)
			}
		}
	})
}

func TestReferenceScoreProperties(t *testing.T) {
	compute := func(t *testing.T, days []signal.Day, asOf recorddate.Date, p params.Params) score.Result {
		t.Helper()
		got, err := score.Compute(days, asOf, p)
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

	t.Run("기록 전체를 같은 날 수만큼 옮기면 날짜만 바뀌고 값은 같다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xbeef))
			asOf := rng.IntN(h.span + 20)
			moved := h.clone()
			moved.base = h.base.AddDays(1 + rng.IntN(400))

			a := compute(t, refToSignalDays(h, refMerge(h)), h.date(asOf), params.Default())
			b := compute(t, refToSignalDays(moved, refMerge(moved)), moved.date(asOf), params.Default())
			a.Window, b.Window = score.Window{}, score.Window{}
			require.Equal(t, a, b, "씨앗 %d", seed)
		}
	})

	t.Run("관찰됨 판단을 더하면 그 항목의 관찰된 일수, 환산 일수, 항목 점수가 줄지 않는다", func(t *testing.T) {
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

			before := compute(t, refToSignalDays(h, refMerge(h)), h.date(asOf), p)
			after := compute(t, refToSignalDays(added, refMerge(added)), added.date(asOf), p)

			require.GreaterOrEqual(t, after.Item(id).ObservedDays, before.Item(id).ObservedDays,
				"씨앗 %d: %s에 %s 관찰됨을 더했다\n%s", seed, h.date(offset), id, h.describe())
			require.GreaterOrEqual(t, after.ConversationDays, before.ConversationDays, "씨앗 %d", seed)
			if !before.Insufficient {
				require.False(t, after.Insufficient, "씨앗 %d: 기록을 더했는데 기록 부족이 됐다", seed)
				require.GreaterOrEqual(t, after.Item(id).ConvertedDays, before.Item(id).ConvertedDays, "씨앗 %d", seed)
				require.GreaterOrEqual(t, after.Item(id).Points, before.Item(id).Points, "씨앗 %d", seed)
			}
		}
	})

	t.Run("결과의 값들이 서로 앞뒤가 맞는다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			rng := rand.New(rand.NewPCG(seed, 0xc0de))
			p := refGenParams(t, rng)
			got := compute(t, refToSignalDays(h, refMerge(h)), h.date(rng.IntN(h.span+20)), p)

			require.Equal(t, p.Window.Days, got.Window.Length(), "씨앗 %d", seed)
			require.LessOrEqual(t, got.ConversationDays, p.Window.Days, "씨앗 %d", seed)
			require.Equal(t, got.ConversationDays < p.Window.MinConversationDays, got.Insufficient, "씨앗 %d", seed)

			sum := 0
			for _, item := range got.Items {
				require.LessOrEqual(t, item.ObservedDays, got.ConversationDays, "씨앗 %d", seed)
				require.LessOrEqual(t, item.ConvertedDays, p.Window.Days, "씨앗 %d", seed)
				require.LessOrEqual(t, item.Points, score.MaxItemPoints, "씨앗 %d", seed)
				if !got.Insufficient && item.ObservedDays > 0 {
					// 한 번이라도 관찰된 항목의 환산 일수가 반올림으로 0이 되는 일은 없다.
					require.Positive(t, item.ConvertedDays, "씨앗 %d, %s", seed, item.Item)
				}
				if item.ObservedDays == 0 {
					require.Zero(t, item.Points, "씨앗 %d, %s", seed, item.Item)
				}
				sum += item.Points
			}
			require.Equal(t, sum, got.Total, "씨앗 %d", seed)
			require.LessOrEqual(t, got.Total, score.MaxTotal, "씨앗 %d", seed)
			if got.Insufficient {
				require.Zero(t, got.Total, "씨앗 %d", seed)
				require.Equal(t, score.NoBand, got.Band, "씨앗 %d", seed)
			} else {
				require.NotEqual(t, score.NoBand, got.Band, "씨앗 %d", seed)
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

			shuffled := slices.Clone(days)
			slices.Reverse(shuffled)
			_, err := score.Compute(shuffled, h.date(h.span-1), params.Default())
			require.ErrorIs(t, err, signal.ErrUnsortedDays, "씨앗 %d", seed)

			sorted, err := signal.SortDays(shuffled)
			require.NoError(t, err)
			assert.Equal(t, want, compute(t, sorted, h.date(h.span-1), params.Default()), "씨앗 %d", seed)
		}
	})
}
