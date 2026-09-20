package signal_test

// 이 파일은 하루로 합치는 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 참조 구현은 규칙 문장을 그대로 옮긴다. 우선순위를 숫자 크기로 비교하는 대신 "관찰된 행이 하나라도 있는가"를
// 항목마다 처음부터 끝까지 훑어 확인한다. 느리지만 틀리기 어렵다.
// 가상 기록은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 기록이 나온다.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func refToSignalRow(row refRow, conversation int) signal.Row {
	out := signal.Row{
		ConversationID: fmt.Sprintf("c%d", conversation),
		Item:           signal.AllItems()[row.item],
		Cancelled:      row.cancelled,
	}
	switch row.status {
	case refObserved:
		out.Status = signal.Observed
	case refNotObserved:
		out.Status = signal.NotObserved
	default:
		out.Status = signal.NotMentioned
	}
	switch {
	case row.status == refNotMentioned:
		out.Explicitness = signal.None
	case row.direct:
		out.Explicitness = signal.Direct
	default:
		out.Explicitness = signal.Indirect
	}
	return out
}

func refToSignalRows(h refHistory) map[recorddate.Date][]signal.Row {
	out := make(map[recorddate.Date][]signal.Row, len(h.rows))
	for offset, rows := range h.rows {
		converted := make([]signal.Row, 0, len(rows))
		for i, row := range rows {
			converted = append(converted, refToSignalRow(row, i))
		}
		out[h.date(offset)] = converted
	}
	return out
}

// refSameDay는 실제 구현의 하루가 참조 구현의 하루와 같은 말을 하는지 본다. 다르면 무엇이 다른지 돌려준다.
func refSameDay(h refHistory, want refDay, got signal.Day) string {
	if got.Date != h.date(want.offset) {
		return fmt.Sprintf("날짜가 다르다: 참조 %s, 실제 %s", h.date(want.offset), got.Date)
	}
	for item, id := range signal.AllItems() {
		wantStatus := [...]string{"not_mentioned", "not_observed", "observed"}[want.status[item]]
		wantExplicitness := "none"
		if want.status[item] != refNotMentioned {
			wantExplicitness = "indirect"
			if want.direct[item] {
				wantExplicitness = "direct"
			}
		}
		j := got.Judgement(id)
		if j.Status.String() != wantStatus || j.Explicitness.String() != wantExplicitness {
			return fmt.Sprintf("%s %s: 참조 %s/%s, 실제 %s/%s",
				got.Date, id, wantStatus, wantExplicitness, j.Status, j.Explicitness)
		}
	}
	return ""
}

// refMergeDisagreement는 기록 하나를 두 구현으로 합쳐 보고 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refMergeDisagreement(h refHistory) string {
	want := refMerge(h)
	got, err := signal.MergeDays(refToSignalRows(h))
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}
	if len(got) != len(want) {
		return fmt.Sprintf("대화한 일수가 다르다: 참조 %d, 실제 %d", len(want), len(got))
	}
	for i := range want {
		if diff := refSameDay(h, want[i], got[i]); diff != "" {
			return diff
		}
	}
	return ""
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

func TestReferenceMergeDays(t *testing.T) {
	t.Run("가상 기록 수천 개에서 참조 구현과 같은 하루가 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount) {
			h := refGenHistory(t, seed)
			if refMergeDisagreement(h) == "" {
				continue
			}
			small, _ := refShrink(h, 0, func(c refHistory, _ int) bool { return refMergeDisagreement(c) != "" })
			require.Failf(t, "두 구현이 어긋난다", "씨앗 %d\n%s\n가장 작게 줄인 기록:\n%s",
				seed, refMergeDisagreement(small), small.describe())
		}
	})

	t.Run("날짜 하나씩 따로 합쳐도 같은 하루가 나온다", func(t *testing.T) {
		for seed := range refCount(refHistoryCount / 10) {
			h := refGenHistory(t, seed)
			rowsByDate := refToSignalRows(h)
			for _, want := range refMerge(h) {
				got, err := signal.MergeDay(h.date(want.offset), rowsByDate[h.date(want.offset)])
				require.NoError(t, err)
				require.Empty(t, refSameDay(h, want, got), "씨앗 %d", seed)
			}
		}
	})

	t.Run("기록이 하나도 없으면 하루도 없다", func(t *testing.T) {
		days, err := signal.MergeDays(nil)
		require.NoError(t, err)
		assert.Empty(t, days)

		days, err = signal.MergeDays(map[recorddate.Date][]signal.Row{})
		require.NoError(t, err)
		assert.Empty(t, days)
	})
}

func TestReferenceMergeProperties(t *testing.T) {
	t.Run("같은 기록은 몇 번을 합쳐도 같은 결과다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			first, err := signal.MergeDays(refToSignalRows(h))
			require.NoError(t, err)
			for range 3 {
				// 맵을 새로 만들어 도는 순서가 달라져도 결과가 같아야 한다.
				again, err := signal.MergeDays(refToSignalRows(h))
				require.NoError(t, err)
				require.Equal(t, first, again, "씨앗 %d", seed)
			}
		}
	})

	t.Run("행의 순서를 섞어도 결과가 같다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			want, err := signal.MergeDays(refToSignalRows(h))
			require.NoError(t, err)

			rng := rand.New(rand.NewPCG(seed, 0xabc))
			shuffled := h.clone()
			for _, rows := range shuffled.rows {
				rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
			}
			got, err := signal.MergeDays(refToSignalRows(shuffled))
			require.NoError(t, err)
			require.Equal(t, want, got, "씨앗 %d\n%s", seed, h.describe())
		}
	})

	t.Run("합치는 동안 받은 행을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(200) {
			h := refGenHistory(t, seed)
			rowsByDate := refToSignalRows(h)
			before := refToSignalRows(h)
			_, err := signal.MergeDays(rowsByDate)
			require.NoError(t, err)
			require.Equal(t, before, rowsByDate, "씨앗 %d", seed)
		}
	})

	t.Run("하루의 순서를 섞었다가 정렬하면 처음과 같다", func(t *testing.T) {
		for seed := range refCount(500) {
			h := refGenHistory(t, seed)
			want, err := signal.MergeDays(refToSignalRows(h))
			require.NoError(t, err)
			require.NoError(t, signal.ValidateDays(want))

			shuffled := slices.Clone(want)
			rng := rand.New(rand.NewPCG(seed, 0xdef))
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			input := slices.Clone(shuffled)

			got, err := signal.SortDays(shuffled)
			require.NoError(t, err)
			require.Equal(t, want, got, "씨앗 %d", seed)
			require.Equal(t, input, shuffled, "정렬이 받은 목록을 고쳤다 (씨앗 %d)", seed)
		}
	})

	t.Run("관찰됨 행을 하나 더하면 그 항목은 관찰됨이고 다른 항목은 그대로다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			offsets := h.offsets()
			if len(offsets) == 0 {
				continue
			}
			rng := rand.New(rand.NewPCG(seed, 0x123))
			offset := offsets[rng.IntN(len(offsets))]
			item := rng.IntN(refItems)
			if len(h.rows[offset]) == 0 {
				continue
			}

			before, err := signal.MergeDay(h.date(offset), refToSignalRows(h)[h.date(offset)])
			require.NoError(t, err)

			added := h.clone()
			added.rows[offset] = append(added.rows[offset], refRow{item: item, status: refObserved, direct: rng.IntN(2) == 0})
			after, err := signal.MergeDay(h.date(offset), refToSignalRows(added)[h.date(offset)])
			require.NoError(t, err)

			id := signal.AllItems()[item]
			require.Equal(t, signal.Observed, after.Judgement(id).Status, "씨앗 %d", seed)
			require.GreaterOrEqual(t, after.ObservedCount(), before.ObservedCount(), "씨앗 %d", seed)
			for _, other := range signal.AllItems() {
				if other != id {
					require.Equal(t, before.Judgement(other), after.Judgement(other), "씨앗 %d, %s", seed, other)
				}
			}
		}
	})

	t.Run("행을 취소하면 그 항목의 판단이 올라가지 않는다", func(t *testing.T) {
		for seed := range refCount(1000) {
			h := refGenHistory(t, seed)
			offsets := h.offsets()
			if len(offsets) == 0 {
				continue
			}
			rng := rand.New(rand.NewPCG(seed, 0x456))
			offset := offsets[rng.IntN(len(offsets))]
			if len(h.rows[offset]) == 0 {
				continue
			}
			i := rng.IntN(len(h.rows[offset]))

			before, err := signal.MergeDay(h.date(offset), refToSignalRows(h)[h.date(offset)])
			require.NoError(t, err)

			cancelled := h.clone()
			cancelled.rows[offset][i].cancelled = true
			after, err := signal.MergeDay(h.date(offset), refToSignalRows(cancelled)[h.date(offset)])
			require.NoError(t, err)

			for _, id := range signal.AllItems() {
				require.LessOrEqual(t, after.Judgement(id).Status, before.Judgement(id).Status, "씨앗 %d, %s", seed, id)
			}
			require.LessOrEqual(t, after.ObservedCount(), before.ObservedCount(), "씨앗 %d", seed)
		}
	})

	t.Run("행을 모두 취소해도 그날은 대화한 날로 남고 여덟 항목이 언급 없음이다", func(t *testing.T) {
		for seed := range refCount(300) {
			h := refGenHistory(t, seed)
			conversationDays := len(refMerge(h))
			for _, rows := range h.rows {
				for i := range rows {
					rows[i].cancelled = true
				}
			}
			days, err := signal.MergeDays(refToSignalRows(h))
			require.NoError(t, err)
			require.Len(t, days, conversationDays, "씨앗 %d", seed)
			for _, day := range days {
				require.Equal(t, signal.Day{Date: day.Date}, day, "씨앗 %d", seed)
			}
		}
	})
}
