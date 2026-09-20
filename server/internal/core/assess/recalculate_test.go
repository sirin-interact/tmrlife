package assess

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// 다시 계산하는 길은 따로 없다. 하루를 지우거나 신호를 취소한 뒤에는 남은 행으로 평가를 다시 부른다.
// 아래 시험은 그렇게만 해도 지운 기록의 흔적이 어디에도 남지 않는다는 것을 확인한다:
// 지운 뒤의 평가는 그 기록이 처음부터 없었던 사람의 평가와 글자 하나 다르지 않아야 한다.

// without은 그 날짜의 행을 모두 뺀 사본을 돌려준다. 하루의 일기를 지우면 그날의 신호가 함께 지워진다.
func without(rows map[recorddate.Date][]signal.Row, date recorddate.Date) map[recorddate.Date][]signal.Row {
	remaining := maps.Clone(rows)
	delete(remaining, date)
	return remaining
}

// cancelling은 그 날짜 그 항목의 행을 모두 취소한 사본을 돌려준다. 행은 남기고 취소했다는 표시만 한다.
func cancelling(rows map[recorddate.Date][]signal.Row, date recorddate.Date, item signal.Item) map[recorddate.Date][]signal.Row {
	changed := maps.Clone(rows)
	changed[date] = nil
	for _, row := range rows[date] {
		if row.Item == item {
			row.Cancelled = true
		}
		changed[date] = append(changed[date], row)
	}
	return changed
}

func evaluateRows(t *testing.T, rows map[recorddate.Date][]signal.Row, asOf string) Evaluation {
	t.Helper()
	got, err := EvaluateRows(rows, mustDate(t, asOf), params.Default())
	require.NoError(t, err)
	return got
}

func TestRecalculateAfterDeletingABaselineDay(t *testing.T) {
	// 두 주의 기준선 기간 가운데 6월 5일 하루가 크게 나빴다. 그 뒤로는 피로가 날마다 관찰된다.
	records := join(
		times(4, "xxxx...."), []string{"OOOOOO.."}, times(9, "xxxx...."),
		times(9, "xxxO...."),
	)
	rows := rowsOf(t, "2026-06-01", records...)
	heavyDay := mustDate(t, "2026-06-05")

	before := evaluateRows(t, rows, "2026-06-23")
	after := evaluateRows(t, without(rows, heavyDay), "2026-06-23")

	t.Run("지우기 전에는 그날이 평소를 끌어올려 변화 감지가 울리지 않는다", func(t *testing.T) {
		assert.True(t, before.Baseline.Contains(heavyDay), "지우면 평소가 다시 정해지는 날이다")
		assert.Equal(t, 14, before.Baseline.Days)
		assert.InDelta(t, 6.0/14.0, before.Baseline.Mu, 1e-12)
		assert.False(t, before.Change.State.Detected)
		assert.Equal(t, stage.Everyday, before.Stage.State.Stage)
	})

	t.Run("지우면 평소가 0으로 다시 잡히고, 같은 기록에서 변화 감지가 울려 1단계가 된다", func(t *testing.T) {
		assert.Equal(t, 13, after.Baseline.Days)
		assert.Zero(t, after.Baseline.ObservedTotal)
		assert.InDelta(t, 0.0, after.Baseline.Mu, 0)
		assert.Equal(t, mustDate(t, "2026-06-14"), after.Baseline.End, "대화한 날이 아직 7일 이상이라 기간은 그대로다")

		assert.InDelta(t, 4.5, after.Change.State.S, 0)
		assert.True(t, after.Change.State.Detected)
		assert.Equal(t, stage.Reflection, after.Stage.State.Stage)
		assert.Equal(t, []stage.Reason{stage.ReasonChangeDetected}, after.Stage.State.Reasons)
		assert.Equal(t, crisis.State{ChangeDetected: true}, after.GateState())
	})

	t.Run("창 밖의 날이라 추정 점수는 달라지지 않는다", func(t *testing.T) {
		assert.Equal(t, before.Score, after.Score)
		assert.Equal(t, 2, after.Score.Total)
	})

	t.Run("지운 뒤의 평가는 그날이 처음부터 없었던 기록의 평가와 같다", func(t *testing.T) {
		never := join(
			times(4, "xxxx...."), silence(1), times(9, "xxxx...."),
			times(9, "xxxO...."),
		)

		assert.Equal(t, evaluate(t, diary(t, "2026-06-01", never...), "2026-06-23"), after)
	})

	t.Run("첫 대화 날을 지우면 기준선 기간이 다음 대화 날에서 다시 시작한다", func(t *testing.T) {
		shifted := evaluateRows(t, without(rows, mustDate(t, "2026-06-01")), "2026-06-23")

		assert.Equal(t, mustDate(t, "2026-06-02"), shifted.Baseline.Start)
		assert.Equal(t, mustDate(t, "2026-06-15"), shifted.Baseline.End)
		assert.Equal(t, mustDate(t, "2026-06-16"), shifted.Change.From)
		assert.Equal(t, mustDate(t, "2026-06-02"), shifted.Stage.From)
	})

	t.Run("지워서 기간 안의 대화한 날이 7일에 못 미치면 기간이 늘어난다", func(t *testing.T) {
		// 첫 두 주에 이레만 대화했고 그 뒤로는 날마다 대화했다.
		sparse := join(
			[]string{"xxxx....", "", "xxxx....", "", "xxxx....", "", "xxxx....", "", "xxxx....", "", "xxxx....", "", "xxxx....", ""},
			times(7, "xxxx...."),
		)
		sparseRows := rowsOf(t, "2026-06-01", sparse...)

		full := evaluateRows(t, sparseRows, "2026-06-21")
		assert.False(t, full.Baseline.Extended)
		assert.Equal(t, mustDate(t, "2026-06-14"), full.Baseline.End)

		shorter := evaluateRows(t, without(sparseRows, mustDate(t, "2026-06-07")), "2026-06-21")
		assert.True(t, shorter.Baseline.Extended)
		assert.Equal(t, mustDate(t, "2026-06-15"), shorter.Baseline.End, "일곱 번째 대화 날까지다")
		assert.Equal(t, 7, shorter.Baseline.Days)
	})
}

func TestRecalculateAfterDeletingARecentDay(t *testing.T) {
	// 석 주를 평소대로 지내다가 6월 22일 하루가 크게 나빴고, 다음 날부터 다시 평소대로다.
	records := join(weeks(3, steadyWeek...), []string{"OOOOOO.."}, weeks(1, steadyWeek...))
	rows := rowsOf(t, "2026-06-01", records...)
	badDay := mustDate(t, "2026-06-22")

	before := evaluateRows(t, rows, "2026-06-29")
	after := evaluateRows(t, without(rows, badDay), "2026-06-29")

	t.Run("지우기 전에는 그 하루 때문에 점수가 6이고 1단계다", func(t *testing.T) {
		assert.Equal(t, 6, before.Score.Total)
		assert.Equal(t, 14, before.Score.ConversationDays)
		assert.Equal(t, stage.Reflection, before.Stage.State.Stage)
		assert.Equal(t, MarkObserved, markOn(t, before, signal.TrendMood, badDay))
		// 하루에 2까지만 쌓이므로 그 하루로 변화 감지가 울리지는 않았다.
		assert.InDelta(t, 2.0, before.Change.StateAt(badDay).S, 0)
		assert.Empty(t, detectedDates(before.Stage.Series))
	})

	t.Run("지우면 점수가 1로 돌아가고 0단계가 된다", func(t *testing.T) {
		assert.Equal(t, 1, after.Score.Total)
		assert.Equal(t, 13, after.Score.ConversationDays, "대화한 일수에서도 빠진다")
		assert.Equal(t, 13, after.Confidence.ConversationDays)
		assert.Equal(t, stage.Everyday, after.Stage.State.Stage)
		assert.InDelta(t, 0.0, after.Change.StateAt(badDay).S, 0, "그날 쌓였던 누적값도 없던 일이 된다")
		assert.Empty(t, detectedDates(after.Stage.Series))
	})

	t.Run("점 달력에서 그날은 대화하지 않은 날의 빈칸이 된다", func(t *testing.T) {
		for _, row := range signal.AllTrendRows() {
			assert.Equal(t, MarkNoConversation, markOn(t, after, row, badDay), row.String())
		}
		assert.Equal(t, 13, after.Trend.ConversationDays)
	})

	t.Run("그날보다 앞선 날들의 단계는 달라지지 않는다", func(t *testing.T) {
		untouched := badDay.DaysSince(before.Stage.From)

		assert.Equal(t, before.Stage.Series[:untouched], after.Stage.Series[:untouched])
	})

	t.Run("지운 뒤의 평가는 그날이 처음부터 없었던 기록의 평가와 같다", func(t *testing.T) {
		never := join(weeks(3, steadyWeek...), silence(1), weeks(1, steadyWeek...))

		assert.Equal(t, evaluate(t, diary(t, "2026-06-01", never...), "2026-06-29"), after)
	})

	t.Run("지워서 창 안의 대화한 날이 7일에 못 미치면 점수 자체가 없어진다", func(t *testing.T) {
		// 이레를 대화해 점수 12가 나온 날(하루에 한 단계라 1단계다), 그 가운데 하루를 지운다.
		shortRows := rowsOf(t, "2026-06-01", times(7, "OOOO.x..")...)

		full := evaluateRows(t, shortRows, "2026-06-07")
		assert.Equal(t, 12, full.Score.Total)
		assert.Equal(t, stage.Reflection, full.Stage.State.Stage)
		assert.Equal(t, crisis.State{ScoreElevated: true}, full.GateState())

		shorter := evaluateRows(t, without(shortRows, mustDate(t, "2026-06-03")), "2026-06-07")
		_, hasScore := shorter.Score.Score()
		assert.False(t, hasScore)
		assert.Equal(t, confidence.Low, shorter.Confidence.Level)
		assert.Equal(t, stage.Everyday, shorter.Stage.State.Stage)
		assert.Equal(t, crisis.State{}, shorter.GateState())
	})
}

func TestRecalculateAfterCancellingASignal(t *testing.T) {
	// 열흘 동안 잠은 날마다, 피로는 이틀 관찰됐다. 흥미 저하는 6월 8일 하루에만 관찰됐다.
	records := []string{
		"..Ox....", "..OO....", "..Ox....", "..Ox....", "..OO....",
		"..Ox....", "..Ox....", "OxOx....", "..Ox....", "..Ox....",
	}
	rows := rowsOf(t, "2026-06-01", records...)
	thatDay := mustDate(t, "2026-06-08")

	before := evaluateRows(t, rows, "2026-06-10")
	after := evaluateRows(t, cancelling(rows, thatDay, signal.Interest), "2026-06-10")

	t.Run("취소하기 전에는 점수 5로 1단계다", func(t *testing.T) {
		// 잠 3점, 피로 1점, 흥미 저하 1점이다.
		assert.Equal(t, 5, before.Score.Total)
		assert.Equal(t, 1, before.Score.Item(signal.Interest).Points)
		assert.Equal(t, stage.Reflection, before.Stage.State.Stage)
		assert.Equal(t, MarkObserved, markOn(t, before, signal.TrendMood, thatDay))
	})

	t.Run("하나뿐인 관찰을 취소하면 그 항목의 점수가 사라져 0단계로 돌아간다", func(t *testing.T) {
		interest := after.Score.Item(signal.Interest)
		assert.Zero(t, interest.ObservedDays)
		assert.Zero(t, interest.Points)
		assert.Equal(t, 4, after.Score.Total)
		assert.Equal(t, stage.Everyday, after.Stage.State.Stage)
		assert.Equal(t, 10, after.Score.ConversationDays, "그날의 대화는 그대로 대화한 날이다")
	})

	t.Run("취소한 자리는 언급 없음과 같아서 신뢰도의 항목 충족도에서도 빠진다", func(t *testing.T) {
		assert.Equal(t, 4, before.Confidence.MentionedItems)
		assert.Equal(t, 3, after.Confidence.MentionedItems)
		assert.Contains(t, after.Confidence.MissingItems(), signal.Interest)
	})

	t.Run("점 달력의 기분 줄은 같은 날 남아 있는 우울감의 판단을 따른다", func(t *testing.T) {
		assert.Equal(t, MarkNotObserved, markOn(t, after, signal.TrendMood, thatDay))
		assert.Zero(t, after.Trend.Row(signal.TrendMood).ObservedDays)
	})

	t.Run("취소한 뒤의 평가는 그 신호가 처음부터 없었던 기록의 평가와 같다", func(t *testing.T) {
		never := []string{
			"..Ox....", "..OO....", "..Ox....", "..Ox....", "..OO....",
			"..Ox....", "..Ox....", ".xOx....", "..Ox....", "..Ox....",
		}

		assert.Equal(t, evaluate(t, diary(t, "2026-06-01", never...), "2026-06-10"), after)
	})

	t.Run("같은 날 다른 대화에서도 관찰됐다면 하나를 취소해도 관찰됨이 남는다", func(t *testing.T) {
		twice := maps.Clone(rows)
		twice[thatDay] = append(append([]signal.Row{}, rows[thatDay]...), signal.Row{
			ConversationID: "late-night-conversation",
			Item:           signal.Interest,
			Status:         signal.Observed,
			Explicitness:   signal.Indirect,
		})
		// 저녁 대화의 행만 취소한다.
		for i, row := range twice[thatDay] {
			if row.Item == signal.Interest && row.ConversationID != "late-night-conversation" {
				twice[thatDay][i].Cancelled = true
			}
		}

		got := evaluateRows(t, twice, "2026-06-10")

		assert.Equal(t, 1, got.Score.Item(signal.Interest).ObservedDays)
		assert.Equal(t, 5, got.Score.Total)
	})

	t.Run("그날의 행을 모두 취소해도 그날은 대화한 날로 남는다", func(t *testing.T) {
		all := maps.Clone(rows)
		all[thatDay] = nil
		for _, row := range rows[thatDay] {
			row.Cancelled = true
			all[thatDay] = append(all[thatDay], row)
		}

		got := evaluateRows(t, all, "2026-06-10")

		assert.Equal(t, 10, got.Score.ConversationDays)
		assert.Equal(t, MarkNotMentioned, markOn(t, got, signal.TrendSleep, thatDay))
	})
}

// markOn은 점 달력에서 그 줄 그 날짜의 표시를 꺼낸다.
func markOn(t *testing.T, e Evaluation, row signal.TrendRow, date recorddate.Date) Mark {
	t.Helper()
	offset := date.DaysSince(e.Trend.From)
	require.GreaterOrEqual(t, offset, 0, "창 안의 날짜여야 한다")
	require.Less(t, offset, len(e.Trend.Dates), "창 안의 날짜여야 한다")
	return e.Trend.Row(row).Marks[offset]
}
