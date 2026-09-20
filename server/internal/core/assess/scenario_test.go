package assess

import (
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

// 이 파일의 시험은 사람의 이야기처럼 읽히도록 썼다. 기록은 모두 2026년 6월 1일에 시작하고 한 줄이 하루다.
// 기대값은 규칙을 기본 조정 값으로 돌려서 실제로 나온 값이다. 규칙이나 기본값을 바꾸면 여기가 먼저 깨져서
// 그 변화가 사람의 이야기에서 무엇을 바꾸는지 보게 된다.

// steadyWeek는 별일 없이 지내는 사람의 한 주다. 이틀은 잠을 설쳤고 나머지는 대체로 괜찮다고 말한다.
// 두 주를 이렇게 지내면 평소의 하루 평균 신호 수는 14분의 4다.
var steadyWeek = []string{"xxxx....", "..O.x...", "xx..x.x.", "...x....", "xxO.....", "....xx..", "xx.x...."}

// hardWeek는 크게 힘든 한 주다. 날마다 네댓 항목이 관찰된다.
var hardWeek = []string{"OOOO..O.", "OOOOx...", "OOOO.O..", "OOOOO...", "OOOO..O.", "OOOO.O..", "OOOOO.O."}

func TestScenarioSteadyAndWell(t *testing.T) {
	days := diary(t, "2026-06-01", weeks(6, steadyWeek...)...)

	got := evaluate(t, days, "2026-07-12")

	t.Run("여섯 주 내내 0단계이고 변화 감지도 묶임도 없다", func(t *testing.T) {
		require.Len(t, got.Stage.Series, 42)
		for _, pt := range got.Stage.Series {
			assert.Equal(t, stage.Everyday, pt.Stage, pt.Date.String())
			assert.False(t, pt.Detected, pt.Date.String())
			assert.False(t, pt.Held, pt.Date.String())
		}
	})

	t.Run("추정 점수는 1이고 신뢰도는 높음이다", func(t *testing.T) {
		total, ok := got.Score.Score()

		require.True(t, ok)
		assert.Equal(t, 1, total, "잠만 14일 중 4일 관찰됐다")
		assert.Equal(t, confidence.High, got.Confidence.Level)
	})

	t.Run("점 달력의 세 줄 모두 평소와 비슷하다", func(t *testing.T) {
		for _, row := range got.Trend.Rows {
			assert.Equal(t, Similar, row.Comparison, row.Row.String())
		}
		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 4, sleep.ObservedDays)
		assert.Equal(t, 14, sleep.ConversationDays)
	})

	t.Run("위기 관문에 알릴 나쁜 상태가 없다", func(t *testing.T) {
		assert.Equal(t, crisis.State{}, got.GateState())
	})
}

// asWritten은 변화 탐지의 하루 상한과 누적값의 천장을 끈 조정 값이다. 두 장치가 이야기에서 무엇을 바꾸는지 견줄 때 쓴다.
func asWritten() params.Params {
	p := params.Default()
	p.CUSUM.MaxStep, p.CUSUM.MaxS = 0, 0
	return p
}

func TestScenarioSlowWorsening(t *testing.T) {
	// 두 주는 평소대로 지내고, 그 뒤로 한 주마다 조금씩 나빠진다.
	records := join(
		weeks(2, steadyWeek...),
		[]string{"xxOx....", "xx.x....", "x.Ox....", "xxO.....", "xxxO....", "..O.....", "xxOx...."},
		[]string{"xxOO....", "x.OO....", "xOOx....", "..OO....", "xxOO.x..", "xOO.....", "x.OO...."},
		[]string{"xOOO....", "OOO.....", ".OOOx...", "OOOO....", "xOOO....", "OOO..x..", "OOOO...."},
		weeks(3, hardWeek...),
	)
	days := diary(t, "2026-06-01", records...)

	got := evaluate(t, days, "2026-07-12")

	t.Run("단계는 0, 1, 2의 순서로 올라간다", func(t *testing.T) {
		assert.Equal(t, []stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion}, stageChanges(got.Stage.Series))
	})

	t.Run("변화 탐지가 점수보다 나흘 먼저 반응한다", func(t *testing.T) {
		assert.Equal(t, mustDate(t, "2026-06-24"), firstDetected(got.Stage.Series))

		first := stageOn(t, got, "2026-06-24")
		assert.Equal(t, 4, first.Score, "점수만 보면 아직 0단계다")
		assert.Equal(t, stage.Reflection, first.Stage)
		assert.Equal(t, []stage.Reason{stage.ReasonChangeDetected}, first.Reasons)

		byScore := stageOn(t, got, "2026-06-28")
		assert.Equal(t, 5, byScore.Score)
		assert.Equal(t, []stage.Reason{stage.ReasonScore}, byScore.Reasons)
	})

	t.Run("점수가 10을 넘은 7월 8일에 2단계가 된다", func(t *testing.T) {
		assert.Equal(t, mustDate(t, "2026-07-08"), firstDateAt(got.Stage.Series, stage.Suggestion))
		assert.Equal(t, 11, stageOn(t, got, "2026-07-08").Score)
	})

	t.Run("기준일에는 점 달력의 세 줄 모두 평소보다 잦다", func(t *testing.T) {
		for _, row := range got.Trend.Rows {
			assert.Equal(t, MoreOften, row.Comparison, row.Row.String())
		}
	})

	t.Run("위기 관문에는 점수와 변화 감지를 둘 다 알린다", func(t *testing.T) {
		assert.Equal(t, crisis.State{ScoreElevated: true, ChangeDetected: true}, got.GateState())
	})

	t.Run("두 주를 더 그렇게 지내면 점수 15로 3단계가 된다", func(t *testing.T) {
		later := evaluate(t, days, "2026-07-26")

		assert.Equal(t,
			[]stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion, stage.Recommendation},
			stageChanges(later.Stage.Series))
		assert.Equal(t, mustDate(t, "2026-07-13"), firstDateAt(later.Stage.Series, stage.Recommendation))
		assert.Equal(t, 15, stageOn(t, later, "2026-07-13").Score)
	})

	t.Run("감지는 6월 24일, 1단계는 6월 24일, 2단계는 7월 8일, 3단계는 7월 13일이다", func(t *testing.T) {
		later := evaluate(t, days, "2026-07-26")

		assert.Equal(t, mustDate(t, "2026-06-24"), firstDetected(later.Stage.Series))
		assert.Equal(t, mustDate(t, "2026-06-24"), firstDateAt(later.Stage.Series, stage.Reflection))
		assert.Equal(t, mustDate(t, "2026-07-08"), firstDateAt(later.Stage.Series, stage.Suggestion))
		assert.Equal(t, mustDate(t, "2026-07-13"), firstDateAt(later.Stage.Series, stage.Recommendation))
		// 날마다 대화하면서 서서히 나빠지는 사람은 한 번에 한 단계씩만 오르려 하므로 어느 날도 묶이지 않는다.
		for _, pt := range later.Stage.Series {
			assert.False(t, pt.Held, pt.Date.String())
		}
	})

	t.Run("하루 증가량의 상한과 누적값의 천장은 서서히 나빠지는 흐름을 잡는 날을 하루도 바꾸지 않는다", func(t *testing.T) {
		written := evaluateWith(t, days, "2026-07-26", asWritten())
		later := evaluate(t, days, "2026-07-26")

		assert.Equal(t, mustDate(t, "2026-06-24"), firstDetected(written.Stage.Series))
		assert.Equal(t, stagesOf(written.Stage.Series), stagesOf(later.Stage.Series))
		// 달라지는 것은 누적값이 어디까지 쌓이느냐다. 천장이 없으면 끝없이 쌓이고, 있으면 8에서 멈춘다.
		assert.InDelta(t, 8.0, later.Change.State.S, 0)
		assert.Greater(t, written.Change.State.S, 50.0)
	})
}

// 하루만 크게 나빴던 사람이다. 석 주를 평소대로 지내다가 6월 22일 하루에 여섯 항목이 관찰되고, 다음 날부터 다시 평소대로다.
func TestScenarioOneTerribleDay(t *testing.T) {
	records := join(weeks(3, steadyWeek...), []string{"OOOOOO.."}, weeks(3, steadyWeek...))
	days := diary(t, "2026-06-01", records...)
	uncapped := params.Default()
	uncapped.CUSUM.MaxStep = 0

	byDefault := evaluateWith(t, days, "2026-07-13", params.Default())
	withoutCap := evaluateWith(t, days, "2026-07-13", uncapped)

	t.Run("기본값에서는 그 하루로 변화 감지가 울리지 않는다", func(t *testing.T) {
		// 평소의 하루 평균이 14분의 4라 그날의 증가량은 6 − 0.29 − 0.5 = 5.21이지만 하루에 2까지만 쌓인다.
		assert.Empty(t, detectedDates(byDefault.Stage.Series))
		assert.InDelta(t, 2.0, byDefault.Change.StateAt(mustDate(t, "2026-06-22")).S, 0)
		// 평소로 돌아오면 나흘 만에 누적값이 0이 된다.
		assert.InDelta(t, 0.0, byDefault.Change.StateAt(mustDate(t, "2026-06-26")).S, 0)
	})

	t.Run("하루 상한을 끄면 그 하루로 울리고 사흘 동안 이어진다", func(t *testing.T) {
		assert.Equal(t,
			[]recorddate.Date{mustDate(t, "2026-06-22"), mustDate(t, "2026-06-23"), mustDate(t, "2026-06-24")},
			detectedDates(withoutCap.Stage.Series))
		assert.InDelta(t, 5.21, withoutCap.Change.StateAt(mustDate(t, "2026-06-22")).S, 0.01)
	})

	t.Run("어느 쪽이든 그 하루는 점수를 1에서 6으로 올리고, 창을 벗어날 때까지 14일 동안 1단계다", func(t *testing.T) {
		// 한 번이라도 관찰된 항목은 환산 일수가 1일 이상이라 1점을 받는다. 새로 관찰된 다섯 항목이 5점을 보탠다.
		// 하루에 변화 감지를 울릴 만큼 항목이 많이 관찰된 날은 그것만으로 점수 5를 넘기므로,
		// 하루 증가량의 상한은 이 사람의 개입 단계를 바꾸지 못한다. 바뀌는 것은 변화 감지 여부뿐이다.
		for _, got := range []Evaluation{byDefault, withoutCap} {
			assert.Equal(t, 1, stageOn(t, got, "2026-06-21").Score)
			assert.Equal(t, stage.Everyday, stageOn(t, got, "2026-06-21").Stage)

			for offset := range 14 {
				pt, ok := got.Stage.At(mustDate(t, "2026-06-22").AddDays(offset))
				require.True(t, ok)
				assert.Equal(t, 6, pt.Score, pt.Date.String())
				assert.Equal(t, stage.Reflection, pt.Stage, pt.Date.String())
				assert.Equal(t, []stage.Reason{stage.ReasonScore}, pt.Reasons, pt.Date.String())
			}

			assert.Equal(t, 1, stageOn(t, got, "2026-07-06").Score)
			assert.Equal(t, stage.Everyday, stageOn(t, got, "2026-07-06").Stage)
		}
		assert.Equal(t, stagesOf(byDefault.Stage.Series), stagesOf(withoutCap.Stage.Series))
	})

	t.Run("그날 위기 관문에 알리는 상태는 상한에 따라 갈린다", func(t *testing.T) {
		thatDay := func(p params.Params) crisis.State {
			return evaluateWith(t, days, "2026-06-22", p).GateState()
		}

		assert.Equal(t, crisis.State{}, thatDay(params.Default()), "기본값에서는 하루의 기복으로 같은 말을 무겁게 듣지 않는다")
		assert.Equal(t, crisis.State{ChangeDetected: true}, thatDay(uncapped))
	})
}

func TestScenarioRecovery(t *testing.T) {
	t.Run("여드레쯤 가볍게 나빠졌다가 나아지면 단계가 1로 올랐다가 0으로 돌아온다", func(t *testing.T) {
		records := join(weeks(2, steadyWeek...), times(8, "xxOO...."), weeks(4, steadyWeek...))
		days := diary(t, "2026-06-01", records...)

		got := evaluate(t, days, "2026-07-20")

		assert.Equal(t, []stage.Stage{stage.Everyday, stage.Reflection, stage.Everyday}, stageChanges(got.Stage.Series))
		// 나흘째에 울리고, 평소로 돌아온 지 여드레째에 누적값이 한계값 아래로 내려가 풀린다.
		detected := detectedDates(got.Stage.Series)
		require.NotEmpty(t, detected)
		assert.Equal(t, mustDate(t, "2026-06-18"), detected[0])
		assert.Equal(t, mustDate(t, "2026-06-29"), detected[len(detected)-1])
		assert.Equal(t, mustDate(t, "2026-06-30"), lastChangeTo(got.Stage.Series, stage.Everyday))
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	// 석 주를 크게 앓고(6월 15일~7월 5일), 한 주에 걸쳐 나아지고(7월 6일~12일), 7월 13일부터는 평소대로 지낸다.
	easing := []string{"xOOO....", "OxOO....", "xxOO....", "xOOx....", "xxOO....", "xxOx....", "xxxO...."}
	records := join(weeks(2, steadyWeek...), weeks(3, hardWeek...), easing, weeks(40, steadyWeek...))
	days := diary(t, "2026-06-01", records...)

	t.Run("석 주를 크게 앓고 나아지면 평소로 돌아온 지 여덟 번째 대화 날에 변화 감지가 풀린다", func(t *testing.T) {
		got := evaluate(t, days, "2026-09-06")

		// 점수는 창이 움직이는 대로 내려온다. 7월 21일부터는 점수만 보면 0단계다.
		assert.Equal(t,
			[]stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion, stage.Recommendation, stage.Reflection, stage.Everyday},
			stageChanges(got.Stage.Series))
		assert.Equal(t, mustDate(t, "2026-06-26"), firstDateAt(got.Stage.Series, stage.Recommendation))
		assert.Equal(t, 4, stageOn(t, got, "2026-07-21").Score)
		assert.Equal(t, mustDate(t, "2026-07-21"), lastChangeTo(got.Stage.Series, stage.Everyday))

		// 누적값은 앓는 동안 천장 8에 닿아 거기서 멈춘다. 나아지는 한 주에도 평소보다는 많아서 8에 머문다.
		assert.InDelta(t, 8.0, got.Change.StateAt(mustDate(t, "2026-07-05")).S, 0)
		assert.InDelta(t, 8.0, got.Change.StateAt(mustDate(t, "2026-07-12")).S, 0)

		// 평소로 돌아온 7월 13일부터 대화한 날을 센다. 일곱 번째인 7월 19일까지 감지가 켜져 있고 여덟 번째인 7월 20일에 풀린다.
		detected := detectedDates(got.Stage.Series)
		require.NotEmpty(t, detected)
		assert.Equal(t, mustDate(t, "2026-06-17"), detected[0])
		assert.Equal(t, mustDate(t, "2026-07-19"), detected[len(detected)-1])
		assert.InDelta(t, 3.71, got.Change.StateAt(mustDate(t, "2026-07-20")).S, 0.01)

		// 기준일에는 아무 흔적도 남아 있지 않다. 위기 관문도 평소대로 듣는다.
		assert.InDelta(t, 0.0, got.Change.State.S, 0)
		assert.Equal(t, 1, got.Stage.State.Score)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	t.Run("누적값의 천장을 끄면 같은 사람의 변화 감지가 평소로 돌아온 뒤에도 석 달을 간다", func(t *testing.T) {
		noCeiling := params.Default()
		noCeiling.CUSUM.MaxS = 0

		got := evaluateWith(t, days, "2027-03-01", noCeiling)

		// 하루 상한 2는 그대로여도 앓는 석 주와 나아지는 한 주 동안 50까지 쌓이고, 평소대로 지내는 한 주에 3.5씩만 줄어든다.
		assert.InDelta(t, 50.07, got.Change.StateAt(mustDate(t, "2026-07-12")).S, 0.01)
		detected := detectedDates(got.Stage.Series)
		require.NotEmpty(t, detected)
		assert.Equal(t, mustDate(t, "2026-10-11"), detected[len(detected)-1])
	})
}

// 처음부터 힘든 상태로 시작한 사람이다.
func TestScenarioStartedAlreadyHigh(t *testing.T) {
	// 네 항목이 첫날부터 날마다 관찰된다. 점수는 12점이다.
	days := diary(t, "2026-06-01", times(28, "OOOO.x..")...)

	got := evaluate(t, days, "2026-06-28")

	t.Run("평소가 높게 잡혀서 변화 감지는 한 번도 울리지 않는다", func(t *testing.T) {
		assert.True(t, got.Baseline.Established)
		assert.InDelta(t, 4.0, got.Baseline.Mu, 0)
		assert.Empty(t, detectedDates(got.Stage.Series))
		for _, row := range got.Trend.Rows {
			assert.Equal(t, Similar, row.Comparison, "점 달력으로는 언제나 평소와 같아 보인다: %s", row.Row)
		}
	})

	t.Run("그래도 점수의 절대값이 받쳐서 기록이 7일 쌓인 날 1단계, 다음 날 2단계가 된다", func(t *testing.T) {
		first := stageOn(t, got, "2026-06-07")
		assert.Equal(t, 12, first.Score)
		assert.Equal(t, confidence.Medium, first.Confidence)
		assert.Equal(t, stage.Suggestion, first.Raw)
		assert.Equal(t, stage.Reflection, first.Stage)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, first.HeldBy())

		second := stageOn(t, got, "2026-06-08")
		assert.Equal(t, stage.Suggestion, second.Stage)
		assert.False(t, second.Held)
	})

	t.Run("2단계의 14일째인 6월 21일에 3단계가 된다", func(t *testing.T) {
		assert.Equal(t,
			[]stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion, stage.Recommendation},
			stageChanges(got.Stage.Series))
		assert.Equal(t, 13, stageOn(t, got, "2026-06-20").ElevatedDays)
		assert.Equal(t, mustDate(t, "2026-06-21"), firstDateAt(got.Stage.Series, stage.Recommendation))
		assert.Equal(t, []stage.Reason{stage.ReasonScore, stage.ReasonSustained}, got.Stage.State.Reasons)
	})

	t.Run("위기 관문에는 점수가 높다고 알린다", func(t *testing.T) {
		assert.Equal(t, crisis.State{ScoreElevated: true}, got.GateState())
	})

	t.Run("점수가 처음부터 3단계 구간인 사람도 대화한 날마다 한 단계씩 0, 1, 2, 3을 거친다", func(t *testing.T) {
		// 여섯 항목이 첫날부터 날마다 관찰된다. 점수가 처음 나오는 6월 7일부터 18점이다.
		severeDays := diary(t, "2026-06-01", times(14, "OOOOOO..")...)
		severe := evaluate(t, severeDays, "2026-06-14")

		assert.Equal(t, 18, stageOn(t, severe, "2026-06-07").Score)
		wants := map[string]stage.Stage{
			"2026-06-06": stage.Everyday,
			"2026-06-07": stage.Reflection,
			"2026-06-08": stage.Suggestion,
			"2026-06-09": stage.Recommendation,
		}
		for date, want := range wants {
			assert.Equal(t, want, stageOn(t, severe, date).Stage, date)
		}
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, stageOn(t, severe, "2026-06-07").HeldBy())
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, stageOn(t, severe, "2026-06-08").HeldBy())
		assert.False(t, stageOn(t, severe, "2026-06-09").Held)
		// 단계와 상관없이 위기 관문에는 점수가 처음 나온 날부터 상태가 나쁘다고 알린다.
		assert.Equal(t, crisis.State{ScoreElevated: true}, evaluate(t, severeDays, "2026-06-07").GateState())
	})

	t.Run("이틀에 한 번 대화하는 사람은 대화한 날에만 한 단계씩 오른다", func(t *testing.T) {
		var lines []string
		for range 12 {
			lines = append(lines, "OOOOOO..", "")
		}
		everyOther := evaluate(t, diary(t, "2026-06-01", lines...), "2026-06-24")

		// 일곱 번째 대화 날인 6월 13일에 점수가 나온다. 사이의 대화하지 않은 날에는 오르지 않는다.
		wants := []struct {
			date   string
			stage  stage.Stage
			heldBy stage.Reason
		}{
			{"2026-06-12", stage.Everyday, 0},
			{"2026-06-13", stage.Reflection, stage.ReasonHeldOneStepPerDay},
			{"2026-06-14", stage.Reflection, stage.ReasonHeldNoRecordToday},
			{"2026-06-15", stage.Suggestion, stage.ReasonHeldOneStepPerDay},
			{"2026-06-16", stage.Suggestion, stage.ReasonHeldNoRecordToday},
			{"2026-06-17", stage.Recommendation, 0},
		}
		for _, want := range wants {
			pt := stageOn(t, everyOther, want.date)
			assert.Equal(t, want.stage, pt.Stage, want.date)
			assert.Equal(t, want.heldBy, pt.HeldBy(), want.date)
		}
	})
}

// 사흘 대화하고 사흘 쉬기를 되풀이하는 사람이다. 대화한 날마다 다섯 항목이 관찰된다.
// 창 안의 대화한 일수가 7, 8, 8, 7, 6, 6일로 돌아서, 엿새마다 이틀씩 기록 부족이 된다.
func TestScenarioThreeDaysOnThreeDaysOff(t *testing.T) {
	var records []string
	for range 10 {
		records = append(records, "OOOOOx..", "OOOOOx..", "OOOOOx..", "", "", "")
	}
	days := diary(t, "2026-06-01", records...)

	got := evaluate(t, days, "2026-07-30")

	t.Run("점수가 처음 나오는 6월 13일부터 사흘에 걸쳐 3단계가 된다", func(t *testing.T) {
		assert.Equal(t, 15, stageOn(t, got, "2026-06-13").Score)
		assert.Equal(t, stage.Reflection, stageOn(t, got, "2026-06-13").Stage)
		assert.Equal(t, stage.Suggestion, stageOn(t, got, "2026-06-14").Stage)
		assert.Equal(t, stage.Recommendation, stageOn(t, got, "2026-06-15").Stage)
	})

	t.Run("그 뒤로 기록 부족이 열여섯 번 끼어도 단계는 한 번도 오르내리지 않는다", func(t *testing.T) {
		assert.Equal(t,
			[]stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion, stage.Recommendation},
			stageChanges(got.Stage.Series))

		insufficientDays := 0
		for _, pt := range got.Stage.Series {
			if pt.Date.Before(mustDate(t, "2026-06-15")) {
				continue
			}
			assert.Equal(t, stage.Recommendation, pt.Stage, pt.Date.String())
			if pt.Insufficient {
				insufficientDays++
				assert.Equal(t, []stage.Reason{stage.ReasonCarriedInsufficientRecords}, pt.Reasons, pt.Date.String())
			}
		}
		assert.Equal(t, 16, insufficientDays)
	})

	t.Run("2단계 이상이 이어진 일수는 기록 부족인 날만 빼고 계속 센다", func(t *testing.T) {
		// 6월 14일부터 7월 30일까지 47일 가운데 기록 부족인 16일을 뺀 31일이다.
		assert.Equal(t, 31, got.Stage.State.ElevatedDays)
	})

	t.Run("위기 관문에 알리는 점수 상태는 기록 부족인 날에 꺼진다", func(t *testing.T) {
		// 개입 단계는 이어 가지만 점수 자체가 없는 날이다. 없는 점수를 높은 점수로 읽지 않는다.
		assert.Equal(t, crisis.State{ScoreElevated: true}, evaluate(t, days, "2026-07-28").GateState())
		assert.Equal(t, crisis.State{}, evaluate(t, days, "2026-07-29").GateState())
	})
}

func TestScenarioSparseTalker(t *testing.T) {
	t.Run("한 주에 사흘만 대화하는 사람은 무슨 말을 해도 점수가 나오지 않아 0단계에 머문다", func(t *testing.T) {
		// 월, 수, 금에만 대화한다. 처음 석 주는 잠 이야기뿐이고 그 뒤 다섯 주는 날마다 다섯 항목이 관찰된다.
		records := join(
			weeks(3, "..O.....", "", "x.O.....", "", "..Ox....", "", ""),
			weeks(5, "OOOO.O..", "", "OOOOO...", "", "OOOO.O..", "", ""),
		)
		days := diary(t, "2026-06-01", records...)

		got := evaluate(t, days, "2026-07-26")

		for _, pt := range got.Stage.Series {
			assert.True(t, pt.Insufficient, "창 안에서 대화한 날은 많아야 6일이다: %s", pt.Date)
			assert.Equal(t, stage.Everyday, pt.Stage, pt.Date.String())
		}

		// 기준선은 일곱 번째 대화 날인 6월 15일까지로 늘어난다.
		assert.True(t, got.Baseline.Extended)
		assert.Equal(t, mustDate(t, "2026-06-15"), got.Baseline.End)

		// 나빠진 셋째 대화 날에 변화 감지가 울리지만(하루에 2씩 쌓여 2, 4, 6) 기록 부족이라 단계는 오르지 못한다.
		// 그날부터 묶였다는 표시가 켜진다.
		assert.Equal(t, mustDate(t, "2026-06-26"), firstDetected(got.Stage.Series))
		for _, pt := range got.Stage.Series {
			assert.Equal(t, !pt.Date.Before(mustDate(t, "2026-06-26")), pt.Held, pt.Date.String())
		}
		assert.Equal(t, stage.Reflection, got.Stage.State.Raw)
		assert.Equal(t,
			[]stage.Reason{stage.ReasonChangeDetected, stage.ReasonHeldInsufficientRecords},
			got.Stage.State.Reasons)

		// 점수도 점 달력의 비교도 나오지 않는다. 위기 관문에는 변화 감지만 전해진다.
		_, ok := got.Score.Score()
		assert.False(t, ok)
		for _, row := range got.Trend.Rows {
			assert.Equal(t, ComparisonNone, row.Comparison, row.Row.String())
		}
		assert.Equal(t, crisis.State{ChangeDetected: true}, got.GateState())
	})

	t.Run("날마다 대화해도 잠과 기운 이야기만 하는 사람은 신뢰도가 낮아 묶인다", func(t *testing.T) {
		days := diary(t, "2026-06-01", times(28, "..OO....")...)

		got := evaluate(t, days, "2026-06-28")

		for _, pt := range got.Stage.Series {
			assert.Equal(t, stage.Everyday, pt.Stage, pt.Date.String())
		}
		want := stage.Point{
			Date:             mustDate(t, "2026-06-28"),
			HasRecord:        true,
			ConversationDays: 14,
			Score:            6,
			Confidence:       confidence.Low,
			Raw:              stage.Reflection,
			Stage:            stage.Everyday,
			Held:             true,
			Reasons:          []stage.Reason{stage.ReasonScore, stage.ReasonHeldLowConfidence},
		}
		assert.Equal(t, want, got.Stage.State)

		// 묶인 까닭과 다음 대화에서 자연스럽게 물어볼 항목을 평가에서 바로 읽을 수 있다.
		assert.Equal(t, confidence.ComponentItemCoverage, got.Confidence.Limiting)
		assert.Equal(t,
			[]signal.Item{signal.Interest, signal.Mood, signal.Appetite, signal.SelfBlame, signal.Concentration, signal.Psychomotor},
			got.Confidence.MissingItems())
	})
}

// 3단계까지 간 사람이 6월 21일을 끝으로 한 달 동안 말이 없다가 7월 22일에 돌아온다.
func TestScenarioLongSilence(t *testing.T) {
	records := join(times(21, "OOOO.x.."), silence(30), []string{"xxxx.x.."})
	days := diary(t, "2026-06-01", records...)

	got := evaluate(t, days, "2026-07-22")

	t.Run("창 안에 대화한 날이 7일 남아 있는 동안은 3단계가 이어진다", func(t *testing.T) {
		pt := stageOn(t, got, "2026-06-28")

		assert.Equal(t, 7, pt.ConversationDays)
		assert.Equal(t, stage.Recommendation, pt.Stage)
	})

	t.Run("기록 부족이 되어도 창에 기록이 남아 있는 동안은 3단계를 그대로 이어 간다", func(t *testing.T) {
		first := stageOn(t, got, "2026-06-29")
		assert.Equal(t, 6, first.ConversationDays)
		assert.True(t, first.Insufficient)
		assert.Equal(t, stage.Recommendation, first.Stage)
		assert.False(t, first.Held)
		assert.Equal(t, []stage.Reason{stage.ReasonCarriedInsufficientRecords}, first.Reasons)

		last := stageOn(t, got, "2026-07-04")
		assert.Equal(t, 1, last.ConversationDays)
		assert.Equal(t, stage.Recommendation, last.Stage)
	})

	t.Run("창이 비는 7월 5일부터는 대화한 날이 없어서 0단계라고 적힌다", func(t *testing.T) {
		for date := mustDate(t, "2026-07-05"); date.Before(mustDate(t, "2026-07-22")); date = date.AddDays(1) {
			pt, ok := got.Stage.At(date)
			require.True(t, ok)
			assert.Equal(t, stage.Everyday, pt.Stage, date.String())
			assert.Zero(t, pt.ElevatedDays, date.String())
			assert.Equal(t, []stage.Reason{stage.ReasonNoRecentRecords}, pt.Reasons, date.String())
		}
	})

	t.Run("돌아온 날에는 한 달 전의 기록으로 말을 걸지 않는다", func(t *testing.T) {
		assert.Equal(t,
			[]stage.Stage{stage.Everyday, stage.Reflection, stage.Suggestion, stage.Recommendation, stage.Everyday},
			stageChanges(got.Stage.Series))
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, 1, got.Stage.State.ConversationDays)
		assert.False(t, got.Stage.State.Held)
		assert.Equal(t, crisis.State{}, got.GateState())

		mood := got.Trend.Row(signal.TrendMood)
		assert.Equal(t, MarkNotObserved, mood.Marks[len(mood.Marks)-1])
		for _, mark := range mood.Marks[:len(mood.Marks)-1] {
			assert.Equal(t, MarkNoConversation, mark)
		}
		assert.Equal(t, ComparisonNone, mood.Comparison, "하루치 기록으로는 평소와 견주지 않는다")
	})
}

// detectedDates는 변화 감지 상태였던 날짜를 모두 돌려준다.
func detectedDates(series []stage.Point) []recorddate.Date {
	var out []recorddate.Date
	for _, pt := range series {
		if pt.Detected {
			out = append(out, pt.Date)
		}
	}
	return out
}

// stagesOf는 흐름에서 단계만 뽑는다.
func stagesOf(series []stage.Point) []stage.Stage {
	out := make([]stage.Stage, 0, len(series))
	for _, pt := range series {
		out = append(out, pt.Stage)
	}
	return out
}

// lastChangeTo는 그 단계로 마지막으로 바뀐 날짜다. 없으면 빈 날짜다.
func lastChangeTo(series []stage.Point, want stage.Stage) recorddate.Date {
	var date recorddate.Date
	for i, pt := range series {
		if pt.Stage == want && i > 0 && series[i-1].Stage != want {
			date = pt.Date
		}
	}
	return date
}
