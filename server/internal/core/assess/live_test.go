package assess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// 이틀에 한 번, 홀수 날에만 대화하는 사람이다. 대화한 날마다 네 항목이 관찰된다.
// 어느 14일을 잘라도 대화한 날이 딱 7일이라 기록 부족의 경계에 서 있다.
// 점수는 12점(2단계)이다. 6월 13일에 1단계, 다음에 대화한 6월 15일에 2단계가 된다.
// 2단계의 14일째인 6월 28일은 대화하지 않은 날이라 오르지 않고, 6월 29일의 기록이 생겨야 3단계가 된다.
func everyOtherDay(t *testing.T, conversationDays int) []signal.Day {
	t.Helper()
	var lines []string
	for range conversationDays {
		lines = append(lines, "OOOO....", "")
	}
	return diary(t, "2026-06-01", lines...)
}

func TestEvaluateLive(t *testing.T) {
	p := params.Default()
	today := mustDate(t, "2026-06-29")
	yesterday := mustDate(t, "2026-06-28")

	// 6월 27일까지 열네 번 대화했다. 6월 29일의 대화는 지금 하고 있어서 아직 기록이 없다.
	before := everyOtherDay(t, 14)
	// 6월 29일의 대화가 끝나고 분석까지 된 뒤다.
	after := everyOtherDay(t, 15)
	require.Equal(t, today, after[len(after)-1].Date)

	t.Run("오늘의 기록이 아직 없으면 어제를 기준일로 삼는다", func(t *testing.T) {
		got, err := EvaluateLive(before, today, p)
		require.NoError(t, err)

		closed, err := Evaluate(before, yesterday, p)
		require.NoError(t, err)
		assert.Equal(t, closed, got, "어제 하루가 끝난 뒤에 구한 평가와 같다")

		assert.Equal(t, yesterday, got.AsOf)
		assert.Equal(t, 7, got.Score.ConversationDays)
		// 어제는 대화하지 않은 날이라 3단계로 오르지 못했다. 오늘의 대화가 분석된 뒤에 오른다.
		assert.Equal(t, stage.Suggestion, got.Stage.State.Stage)
		assert.Equal(t, stage.ReasonHeldNoRecordToday, got.Stage.State.HeldBy())
		assert.Equal(t, crisis.State{ScoreElevated: true}, got.GateState())
	})

	t.Run("같은 순간에 오늘을 기준일로 넣으면 하루가 빠져 기록 부족이 된다", func(t *testing.T) {
		// Evaluate는 받은 기준일을 그대로 쓴다. 대화 도중에 이렇게 부르면 관문에 전할 점수 상태가 사라진다.
		// 그래서 대화 도중에 읽는 쪽은 EvaluateLive를 쓴다. 개입 단계는 기록 부족인 날에 전날의 값을 이어 가므로 그대로다.
		got, err := Evaluate(before, today, p)
		require.NoError(t, err)

		assert.Equal(t, 6, got.Score.ConversationDays)
		assert.True(t, got.Score.Insufficient)
		assert.Equal(t, stage.Suggestion, got.Stage.State.Stage)
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	t.Run("오늘의 기록이 있으면 오늘을 기준일로 삼는다", func(t *testing.T) {
		got, err := EvaluateLive(after, today, p)
		require.NoError(t, err)

		closed, err := Evaluate(after, today, p)
		require.NoError(t, err)
		assert.Equal(t, closed, got)

		assert.Equal(t, today, got.AsOf)
		assert.Equal(t, 7, got.Score.ConversationDays)
		assert.Equal(t, stage.Recommendation, got.Stage.State.Stage)
	})

	t.Run("오늘보다 뒤의 기록은 어느 쪽에서도 쓰지 않는다", func(t *testing.T) {
		got, err := EvaluateLive(after, yesterday, p)
		require.NoError(t, err)

		want, err := EvaluateLive(before, yesterday, p)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, mustDate(t, "2026-06-27"), got.AsOf, "6월 28일에는 기록이 없으므로 그 전날이 기준일이다")
	})

	t.Run("기록이 하나도 없는 첫 대화 도중에도 오류가 아니다", func(t *testing.T) {
		got, err := EvaluateLive(nil, today, p)
		require.NoError(t, err)

		assert.Equal(t, yesterday, got.AsOf)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	t.Run("지원하는 날짜 범위의 첫날에는 어제가 없어서 오늘을 그대로 쓴다", func(t *testing.T) {
		first, err := recorddate.New(1, 1, 1)
		require.NoError(t, err)
		require.True(t, first.AddDays(-1).IsZero())

		assert.Equal(t, first, liveAsOf(nil, first))
	})

	t.Run("오늘이 비어 있으면 오류다", func(t *testing.T) {
		got, err := EvaluateLive(before, recorddate.Date{}, p)

		require.ErrorIs(t, err, ErrNoAsOf)
		assert.Equal(t, Evaluation{}, got)
	})

	t.Run("기록이 틀렸으면 Evaluate와 같은 오류다", func(t *testing.T) {
		unsorted := []signal.Day{before[1], before[0]}

		_, err := EvaluateLive(unsorted, today, p)

		var dayErr *signal.DayError
		require.ErrorAs(t, err, &dayErr)
	})
}

// 두 주는 평온했고 석 주는 힘들었다. 그러고는 40일 동안 말이 없다가 8월 15일에 돌아와 날마다 평온한 대화를 한다.
// 누적값은 대화한 날에만 움직이므로 쉬는 동안에도 천장인 8에 머물러 있다.
func TestScenarioReturnsAfterFortyDays(t *testing.T) {
	away := join(times(14, "x.x....."), times(21, "OOOO.O.."), silence(40))
	back := join(away, times(10, "x.x....."))

	check := func(t *testing.T, state crisis.State, today string) crisis.Decision {
		t.Helper()
		decision, err := crisis.Decide(crisis.Input{
			Rule:           crisis.RuleResult{Stage: crisis.StageCheck, Matched: true},
			AI:             crisis.AIAnswered(crisis.StageCheck),
			State:          state,
			ConversationID: "conversation-" + today,
			Now:            mustDate(t, today).UTCMidnight(),
		}, params.Default().Crisis)
		require.NoError(t, err)
		return decision
	}

	t.Run("떠난 뒤에도 창에 기록이 남아 있는 두 주 동안은 3단계를 이어 가다가, 창이 비는 7월 19일에 0단계로 돌아간다", func(t *testing.T) {
		got := evaluate(t, diary(t, "2026-06-01", away...), "2026-08-14")

		assert.Equal(t, stage.Recommendation, stageOn(t, got, "2026-07-12").Stage)
		carried := stageOn(t, got, "2026-07-13")
		assert.True(t, carried.Insufficient)
		assert.Equal(t, stage.Recommendation, carried.Stage)
		assert.Equal(t, stage.Recommendation, stageOn(t, got, "2026-07-18").Stage)
		assert.Equal(t, stage.Everyday, stageOn(t, got, "2026-07-19").Stage)
	})

	t.Run("돌아온 날의 대화 도중에는 0단계이고, 위기 관문에도 옛 기록의 변화 감지를 전하지 않는다", func(t *testing.T) {
		got, err := EvaluateLive(diary(t, "2026-06-01", away...), mustDate(t, "2026-08-15"), params.Default())
		require.NoError(t, err)
		require.Equal(t, mustDate(t, "2026-08-14"), got.AsOf)

		assert.Equal(t, 0, got.Stage.State.ConversationDays)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, []stage.Reason{stage.ReasonNoRecentRecords}, got.Stage.State.Reasons)

		// 누적값은 마지막 대화 날의 값 그대로 남아 있다. 힘든 석 주 동안 하루에 2씩 쌓여 천장 8에 닿은 값이다.
		last := got.Change.Series[len(got.Change.Series)-1]
		assert.Equal(t, mustDate(t, "2026-07-05"), last.Date)
		assert.True(t, got.Change.State.Detected)
		assert.InDelta(t, 8.0, got.Change.State.S, 0)

		// 그래도 창 안에 대화한 날이 하나도 없으므로 관문에는 꺼진 것으로 전한다. 돌아온 날의 애매한 표현은 평소대로 되묻는다.
		assert.Equal(t, crisis.State{}, got.GateState())
		decision := check(t, got.GateState(), "2026-08-15")
		assert.Equal(t, crisis.StageCheck, decision.Stage)
		assert.Empty(t, decision.Adjustments)
	})

	t.Run("돌아온 이튿날부터는 남아 있던 누적값이 다시 보인다. 평온한 대화가 여덟 번 쌓이는 8월 22일에 풀린다", func(t *testing.T) {
		days := diary(t, "2026-06-01", back...)

		// 8월 16일의 대화 도중에는 8월 15일까지의 기록으로 본다. 누적값은 8에서 0.5 줄어 7.5라 아직 변화 감지다.
		second, err := EvaluateLive(days[:len(days)-9], mustDate(t, "2026-08-16"), params.Default())
		require.NoError(t, err)
		require.Equal(t, mustDate(t, "2026-08-15"), second.AsOf)
		assert.InDelta(t, 7.5, second.Change.State.S, 0)
		assert.Equal(t, crisis.State{ChangeDetected: true}, second.GateState())
		assert.Equal(t, crisis.StageRespond, check(t, second.GateState(), "2026-08-16").Stage)
		// 개입 단계는 기록 부족이라 오르지 못하고, 오르려다 묶였다는 표시만 남는다.
		assert.Equal(t, stage.Everyday, second.Stage.State.Stage)
		assert.Equal(t, stage.ReasonHeldInsufficientRecords, second.Stage.State.HeldBy())

		got := evaluate(t, days, "2026-08-24")
		detected := detectedDates(got.Stage.Series)
		assert.Equal(t, mustDate(t, "2026-08-21"), detected[len(detected)-1], "돌아온 뒤 일곱 번째 대화 날까지 켜져 있다")
		assert.InDelta(t, 4.0, got.Change.StateAt(mustDate(t, "2026-08-22")).S, 0, "여덟 번째 대화 날에 한계값과 같아져 풀린다")
		assert.Equal(t, crisis.State{}, evaluate(t, days, "2026-08-22").GateState())

		// 돌아온 뒤로 단계는 한 번도 오르지 않는다. 점수가 0이고, 기록이 찬 뒤에는 두 항목만 이야기해 신뢰도가 낮다.
		for _, pt := range got.Stage.Series {
			if !pt.Date.Before(mustDate(t, "2026-07-19")) {
				assert.Equal(t, stage.Everyday, pt.Stage, pt.Date.String())
			}
		}
		assert.Equal(t, stage.ReasonHeldLowConfidence, stageOn(t, got, "2026-08-21").HeldBy())
	})
}
