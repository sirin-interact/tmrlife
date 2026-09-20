package assess

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// worsening은 두 주를 평소대로 지내고 석 주를 크게 앓는 기록이다. 모든 계산이 값을 내는 기록이라 묶음을 확인하기 좋다.
func worsening(t *testing.T) []signal.Day {
	t.Helper()
	return diary(t, "2026-06-01", join(weeks(2, steadyWeek...), weeks(3, hardWeek...))...)
}

func TestEvaluateBundlesEveryCalculation(t *testing.T) {
	days := worsening(t)
	asOf := mustDate(t, "2026-07-05")
	p := params.Default()

	got, err := Evaluate(days, asOf, p)
	require.NoError(t, err)

	t.Run("기준일과 쓴 조정 값을 결과에 남긴다", func(t *testing.T) {
		assert.Equal(t, asOf, got.AsOf)
		assert.Equal(t, p, got.Params)
	})

	t.Run("추정 점수는 점수 계산을 따로 부른 것과 같다", func(t *testing.T) {
		want, err := score.Compute(days, asOf, p)
		require.NoError(t, err)

		assert.Equal(t, want, got.Score)
		assert.Equal(t, 15, got.Score.Total)
	})

	t.Run("신뢰도는 신뢰도 계산을 따로 부른 것과 같다", func(t *testing.T) {
		want, err := confidence.Compute(days, asOf, p)
		require.NoError(t, err)

		assert.Equal(t, want, got.Confidence)
		assert.Equal(t, confidence.High, got.Confidence.Level)
	})

	t.Run("기준선과 변화 탐지는 같은 기록에서 차례로 구한 것과 같다", func(t *testing.T) {
		wantBase, err := baseline.Compute(days, asOf, p)
		require.NoError(t, err)
		wantChange, err := cusum.Run(days, wantBase, p)
		require.NoError(t, err)

		assert.Equal(t, wantBase, got.Baseline)
		assert.Equal(t, wantChange, got.Change)
		assert.True(t, got.Baseline.Established)
		assert.True(t, got.Change.State.Detected)
	})

	t.Run("개입 단계는 단계 계산을 따로 부른 것과 같다", func(t *testing.T) {
		want, err := stage.Compute(days, asOf, p)
		require.NoError(t, err)

		assert.Equal(t, want, got.Stage)
		assert.Equal(t, stage.Recommendation, got.Stage.State.Stage)
	})

	t.Run("묶인 값들이 서로 같은 날과 같은 상태를 말한다", func(t *testing.T) {
		assert.Equal(t, asOf, got.Score.Window.To)
		assert.Equal(t, asOf, got.Confidence.AsOf)
		assert.Equal(t, asOf, got.Baseline.AsOf)
		assert.Equal(t, asOf, got.Change.AsOf)
		assert.Equal(t, asOf, got.Stage.AsOf)
		assert.Equal(t, asOf, got.Trend.To)

		assert.Equal(t, got.Score.ConversationDays, got.Confidence.ConversationDays)
		assert.Equal(t, got.Score.ConversationDays, got.Trend.ConversationDays)
		assert.Equal(t, got.Score.ConversationDays, got.Stage.State.ConversationDays)
		assert.Equal(t, got.Score.Total, got.Stage.State.Score)
		assert.Equal(t, got.Confidence.Level, got.Stage.State.Confidence)

		for _, pt := range got.Stage.Series {
			assert.Equal(t, got.Change.StateAt(pt.Date).Detected, pt.Detected, pt.Date.String())
		}
	})

	t.Run("결과를 JSON으로 옮길 수 있다", func(t *testing.T) {
		// 내부 확인 화면이 이 값을 그대로 받아 보여준다.
		encoded, err := json.Marshal(got)

		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"more_often"`)
	})
}

func TestEvaluateWithoutRecords(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	for name, days := range map[string][]signal.Day{
		"기록이 하나도 없다":       nil,
		"첫 대화 날이 기준일보다 뒤다": diary(t, "2026-09-21", "OOOO...."),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Evaluate(days, asOf, params.Default())
			require.NoError(t, err)

			_, hasScore := got.Score.Score()
			assert.False(t, hasScore)
			assert.Equal(t, score.NoBand, got.Score.Band)
			assert.True(t, got.Confidence.Insufficient)
			assert.Equal(t, confidence.Low, got.Confidence.Level)
			assert.False(t, got.Baseline.Established)
			assert.Empty(t, got.Change.Series)
			assert.Empty(t, got.Stage.Series)
			assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
			assert.Equal(t, crisis.State{}, got.GateState())

			assert.Len(t, got.Trend.Dates, 14)
			assert.Zero(t, got.Trend.ConversationDays)
			for _, row := range got.Trend.Rows {
				assert.Equal(t, slices.Repeat([]Mark{MarkNoConversation}, 14), row.Marks)
				assert.Equal(t, ComparisonNone, row.Comparison)
			}

			_, err = json.Marshal(got)
			require.NoError(t, err, "빈 평가도 JSON으로 옮길 수 있어야 한다")
		})
	}
}

// 지난 날짜를 기준일로 넣으면 그날 알 수 있었던 기록만으로 구한 평가가 나와야 한다.
func TestEvaluateIgnoresRecordsAfterAsOf(t *testing.T) {
	days := worsening(t)
	asOf := mustDate(t, "2026-06-20")

	upToAsOf := make([]signal.Day, 0, len(days))
	for _, day := range days {
		if !day.Date.After(asOf) {
			upToAsOf = append(upToAsOf, day)
		}
	}
	require.Less(t, len(upToAsOf), len(days))

	withFuture, err := Evaluate(days, asOf, params.Default())
	require.NoError(t, err)
	withoutFuture, err := Evaluate(upToAsOf, asOf, params.Default())
	require.NoError(t, err)

	assert.Equal(t, withoutFuture, withFuture)
}

func TestEvaluateLeavesInputUntouched(t *testing.T) {
	days := worsening(t)
	before := slices.Clone(days)

	_, err := Evaluate(days, mustDate(t, "2026-07-05"), params.Default())

	require.NoError(t, err)
	assert.Equal(t, before, days)
}

func TestEvaluateIsDeterministic(t *testing.T) {
	days := worsening(t)
	asOf := mustDate(t, "2026-07-05")

	first, err := Evaluate(days, asOf, params.Default())
	require.NoError(t, err)
	second, err := Evaluate(days, asOf, params.Default())
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

func TestEvaluateErrors(t *testing.T) {
	days := worsening(t)
	asOf := mustDate(t, "2026-07-05")

	t.Run("기준일이 비었다", func(t *testing.T) {
		got, err := Evaluate(days, recorddate.Date{}, params.Default())

		require.ErrorIs(t, err, ErrNoAsOf)
		assert.Equal(t, Evaluation{}, got)
	})

	t.Run("조정 값이 틀렸으면 어느 계산도 돌리지 않는다", func(t *testing.T) {
		p := params.Default()
		p.Trend.MinDifferencePercent = 0

		got, err := Evaluate(days, asOf, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Trend.MinDifferencePercent", fieldErr.Field)
		assert.Equal(t, Evaluation{}, got)
	})

	t.Run("날짜순이 아니다", func(t *testing.T) {
		unsorted := slices.Clone(days)
		slices.Reverse(unsorted)

		got, err := Evaluate(unsorted, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrUnsortedDays)
		var dayErr *signal.DayError
		require.ErrorAs(t, err, &dayErr)
		assert.Equal(t, 1, dayErr.Index)
		assert.Equal(t, Evaluation{}, got)
	})

	t.Run("판단과 명시성이 맞지 않는 하루가 있다", func(t *testing.T) {
		broken := slices.Clone(days)
		broken[3].Judgements[0] = signal.Judgement{Status: signal.Observed, Explicitness: signal.None}

		_, err := Evaluate(broken, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})

	t.Run("창의 첫날이 다룰 수 있는 날짜 범위를 벗어난다", func(t *testing.T) {
		_, err := Evaluate(nil, mustDate(t, "0001-01-05"), params.Default())

		require.ErrorIs(t, err, score.ErrInvalidWindow)
	})
}

func TestEvaluateRows(t *testing.T) {
	asOf := mustDate(t, "2026-06-10")
	lines := times(10, "OOOxx...")

	t.Run("행을 하루씩 합친 다음 평가한 것과 같다", func(t *testing.T) {
		got, err := EvaluateRows(rowsOf(t, "2026-06-01", lines...), asOf, params.Default())
		require.NoError(t, err)

		want, err := Evaluate(diary(t, "2026-06-01", lines...), asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, 9, got.Score.Total)
	})

	t.Run("행이 하나도 없는 날짜는 대화하지 않은 날이다", func(t *testing.T) {
		rows := rowsOf(t, "2026-06-01", lines...)
		rows[mustDate(t, "2026-06-04")] = nil

		got, err := EvaluateRows(rows, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, 9, got.Score.ConversationDays)
	})

	t.Run("분석은 했지만 아무 항목도 나오지 않은 날은 언급 없음 행이 있어야 대화한 날로 센다", func(t *testing.T) {
		quiet := mustDate(t, "2026-06-04")
		notMentioned := make([]signal.Row, 0, signal.ItemCount)
		for _, item := range signal.AllItems() {
			notMentioned = append(notMentioned, signal.Row{
				ConversationID: "conversation-quiet", Item: item, Status: signal.NotMentioned, Explicitness: signal.None,
			})
		}

		// 언급 없음 행을 남긴 경우: 열흘 가운데 아흐레에 세 항목이 관찰됐다. 14 × 9 ÷ 10 = 12.6 → 13일 → 3점씩 9점.
		rows := rowsOf(t, "2026-06-01", lines...)
		rows[quiet] = notMentioned
		kept, err := EvaluateRows(rows, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, 10, kept.Score.ConversationDays)
		assert.Equal(t, 9, kept.Score.Item(signal.Interest).ObservedDays)
		assert.Equal(t, 13, kept.Score.Item(signal.Interest).ConvertedDays)

		// 행을 남기지 않은 경우: 그날이 통째로 빠져 아흐레 가운데 아흐레가 된다. 14 × 9 ÷ 9 = 14일.
		// 조용했던 하루가 사라지면서 환산 일수가 올라간다. 행을 만드는 쪽이 언급 없음 행을 남겨야 하는 까닭이다.
		rows[quiet] = nil
		dropped, err := EvaluateRows(rows, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, 9, dropped.Score.ConversationDays)
		assert.Equal(t, 14, dropped.Score.Item(signal.Interest).ConvertedDays)
	})

	t.Run("기록이 없어도 된다", func(t *testing.T) {
		got, err := EvaluateRows(nil, asOf, params.Default())

		require.NoError(t, err)
		assert.Empty(t, got.Stage.Series)
	})

	t.Run("틀린 행이 있으면 오류이고 어느 대화의 행인지는 메시지에 담지 않는다", func(t *testing.T) {
		rows := rowsOf(t, "2026-06-01", lines...)
		date := mustDate(t, "2026-06-04")
		rows[date] = append(rows[date], signal.Row{
			ConversationID: "conversation-with-a-secret-name",
			Item:           signal.Sleep,
			Status:         signal.Observed,
			Explicitness:   signal.None,
		})

		got, err := EvaluateRows(rows, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
		var rowErr *signal.RowError
		require.ErrorAs(t, err, &rowErr)
		assert.NotContains(t, err.Error(), "secret")
		assert.Equal(t, Evaluation{}, got)
	})
}

func TestGateState(t *testing.T) {
	t.Run("추정 점수가 10 이상이면 점수 쪽이 참이다", func(t *testing.T) {
		// 네 항목이 날마다 관찰되면 12점이다. 처음부터 그랬으므로 변화 감지는 울리지 않는다.
		got := evaluate(t, diary(t, "2026-06-01", times(20, "OOOO.x..")...), "2026-06-20")

		require.Equal(t, 12, got.Score.Total)
		assert.Equal(t, crisis.State{ScoreElevated: true}, got.GateState())
	})

	t.Run("추정 점수가 9면 점수 쪽은 거짓이다", func(t *testing.T) {
		got := evaluate(t, diary(t, "2026-06-01", times(20, "OOOxx...")...), "2026-06-20")

		require.Equal(t, 9, got.Score.Total)
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	t.Run("기준은 평가에 쓴 조정 값에서 가져온다", func(t *testing.T) {
		p := params.Default()
		p.Crisis.EscalationMinScore = 9

		got := evaluateWith(t, diary(t, "2026-06-01", times(20, "OOOxx...")...), "2026-06-20", p)

		assert.Equal(t, crisis.State{ScoreElevated: true}, got.GateState())
	})

	t.Run("변화 감지 상태면 점수가 낮아도 변화 쪽이 참이다", func(t *testing.T) {
		records := join(times(14, "xxxx...."), times(9, "xxxO...."))

		got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-23")

		require.Equal(t, 2, got.Score.Total)
		assert.Equal(t, crisis.State{ChangeDetected: true}, got.GateState())
	})

	t.Run("기록 부족이면 점수가 없으므로 점수 쪽은 거짓이고 변화 감지는 그대로 전한다", func(t *testing.T) {
		// 두 주 뒤로 여드레를 쉬고 돌아와 사흘 동안 여러 항목이 관찰된다. 창 안에서 대화한 날은 6일뿐이다.
		records := join(times(14, "xxxx...."), silence(8), times(3, "OOOOOO.."))

		got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-25")

		_, hasScore := got.Score.Score()
		require.False(t, hasScore)
		assert.Equal(t, crisis.State{ChangeDetected: true}, got.GateState())
	})

	t.Run("창 안에 대화한 날이 하나도 없으면 변화 감지가 켜진 채여도 꺼진 것으로 전한다", func(t *testing.T) {
		// 6월 15일부터 열흘 동안 피로가 관찰돼 누적값이 5.0이 되고, 6월 24일을 끝으로 말이 없다.
		records := join(times(14, "xxxx...."), times(10, "xxxO...."))
		days := diary(t, "2026-06-01", records...)

		// 7월 7일의 창에는 6월 24일 하루가 남아 있다. 7월 8일부터 창이 빈다.
		lastDayInWindow := evaluate(t, days, "2026-07-07")
		require.Equal(t, 1, lastDayInWindow.Score.ConversationDays)
		assert.Equal(t, crisis.State{ChangeDetected: true}, lastDayInWindow.GateState())

		empty := evaluate(t, days, "2026-07-08")
		require.Zero(t, empty.Score.ConversationDays)
		require.True(t, empty.Change.State.Detected, "누적값은 대화하지 않는 동안 그대로 남는다")
		assert.Equal(t, crisis.State{}, empty.GateState())
	})

	t.Run("평가를 거치지 않은 빈 값은 둘 다 거짓이다", func(t *testing.T) {
		// 빈 조정 값에서는 점수 기준이 0이라, 막지 않으면 0점이 기준 이상으로 읽힌다.
		assert.Equal(t, crisis.State{}, Evaluation{}.GateState())
	})

	t.Run("위기 관문의 판정에 그대로 넘길 수 있다", func(t *testing.T) {
		got := evaluate(t, diary(t, "2026-06-01", times(20, "OOOO.x..")...), "2026-06-20")

		decision, err := crisis.Decide(crisis.Input{
			Rule:           crisis.RuleResult{Stage: crisis.StageCheck, Matched: true},
			AI:             crisis.AIAnswered(crisis.StageCheck),
			State:          got.GateState(),
			ConversationID: "conversation-2026-06-20",
			Now:            mustDate(t, "2026-06-20").UTCMidnight(),
		}, got.Params.Crisis)

		require.NoError(t, err)
		assert.Equal(t, crisis.StageRespond, decision.Stage, "상태가 나쁜 동안에는 같은 말도 한 단계 올려 듣는다")
	})
}
