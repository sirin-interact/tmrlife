package assess_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func date(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

// diary는 first부터 달력의 하루에 한 줄씩 적은 기록을 하루의 목록으로 옮긴다.
//
// 한 줄은 여덟 글자이고 자리는 흥미, 기분, 수면, 피로, 식욕, 자기 비난, 집중, 움직임 순이다.
// O는 관찰됨(직접 언급), o는 관찰됨(간접 추론), X는 관찰되지 않음(직접), x는 관찰되지 않음(간접), 점은 언급 없음이다.
// 빈 줄은 대화하지 않은 날이라 하루를 만들지 않는다.
func diary(t *testing.T, first string, lines ...string) []signal.Day {
	t.Helper()
	start := date(t, first)
	days := make([]signal.Day, 0, len(lines))
	for offset, line := range lines {
		if line == "" {
			continue
		}
		require.Len(t, line, signal.ItemCount, "하루는 여덟 글자로 적는다")
		day := signal.Day{Date: start.AddDays(offset)}
		for idx := range signal.ItemCount {
			switch line[idx] {
			case 'O':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			case 'o':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
			case 'X':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
			case 'x':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect}
			case '.':
			default:
				require.Failf(t, "모르는 글자", "%q", line[idx])
			}
		}
		days = append(days, day)
	}
	require.NoError(t, signal.ValidateDays(days))
	return days
}

func repeat(n int, line string) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = line
	}
	return lines
}

func join(parts ...[]string) []string {
	var lines []string
	for _, part := range parts {
		lines = append(lines, part...)
	}
	return lines
}

// countsDiary는 연속한 n일의 기록을 만든다. 항목마다 앞에서부터 counts[항목]일 동안 관찰됨이고 나머지 날은 관찰되지 않음이다.
func countsDiary(n int, counts [signal.ItemCount]int) []string {
	lines := make([]string, n)
	for i := range lines {
		line := make([]byte, signal.ItemCount)
		for idx := range line {
			line[idx] = 'X'
			if i < counts[idx] {
				line[idx] = 'O'
			}
		}
		lines[i] = string(line)
	}
	return lines
}

func evaluate(t *testing.T, days []signal.Day, asOf string) assess.Evaluation {
	t.Helper()
	got, err := assess.Evaluate(days, date(t, asOf), params.Default())
	require.NoError(t, err)
	return got
}

// weekOfRows는 3월 1일~7일의 신호 행이다.
//   - 날마다 대화 c1: 기분 관찰됨(직접), 수면 관찰되지 않음(직접)
//   - 3월 1일~4일 대화 c1: 흥미 관찰됨(직접)
//   - 3월 5일 밤의 두 번째 대화 c2: 수면 관찰됨(간접)
func weekOfRows(t *testing.T) map[recorddate.Date][]signal.Row {
	t.Helper()
	rows := make(map[recorddate.Date][]signal.Row)
	first := date(t, "2026-03-01")
	for offset := range 7 {
		day := first.AddDays(offset)
		rows[day] = []signal.Row{
			{ConversationID: "c1", Item: signal.Mood, Status: signal.Observed, Explicitness: signal.Direct},
			{ConversationID: "c1", Item: signal.Sleep, Status: signal.NotObserved, Explicitness: signal.Direct},
		}
		if offset < 4 {
			rows[day] = append(rows[day],
				signal.Row{ConversationID: "c1", Item: signal.Interest, Status: signal.Observed, Explicitness: signal.Direct})
		}
	}
	night := date(t, "2026-03-05")
	rows[night] = append(rows[night],
		signal.Row{ConversationID: "c2", Item: signal.Sleep, Status: signal.Observed, Explicitness: signal.Indirect})
	return rows
}

// cancel은 그 날짜에서 그 대화의 그 항목 행을 취소한다.
func cancel(t *testing.T, rows map[recorddate.Date][]signal.Row, day, conversationID string, item signal.Item) {
	t.Helper()
	found := false
	for i, row := range rows[date(t, day)] {
		if row.ConversationID == conversationID && row.Item == item {
			rows[date(t, day)][i].Cancelled = true
			found = true
		}
	}
	require.True(t, found, "취소할 행이 있어야 한다")
}

// 신호 행에서 출발해 하루로 합치고 평가 전부를 구한다.
func TestOracleFromRowsToEvaluation(t *testing.T) {
	// 기준일 3월 7일, n = 7(2배로 환산).
	//   기분 o=7 → 14일 → 3점
	//   흥미 o=4 → 8일  → 2점
	//   수면 o=1 → 2일  → 1점  (3월 5일: 아침에는 "잘 잤다"였지만 밤 대화에서 관찰됐으므로 그날은 관찰됨이다)
	// 합 6점, 가벼움.
	// 신뢰도: 기록 7/14, 항목은 흥미·기분·수면 3/8 = 0.375, 관찰됨 7+4+1 = 12개 가운데 직접 11개 → 11/12.
	//         가장 작은 값 0.375 → 낮음.
	// 단계: 6점이면 1단계로 오르려 하지만 신뢰도가 낮아 0단계에 묶인다.
	// 기준선은 3월 15일에야 잡히므로 변화 탐지는 돌지 않는다. 관문에 넘기는 상태는 둘 다 거짓이다.
	got, err := assess.EvaluateRows(weekOfRows(t), date(t, "2026-03-07"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, date(t, "2026-03-07"), got.AsOf)

	assert.Equal(t, 7, got.Score.ConversationDays)
	assert.Equal(t, 3, got.Score.Item(signal.Mood).Points)
	assert.Equal(t, 2, got.Score.Item(signal.Interest).Points)
	assert.Equal(t, 1, got.Score.Item(signal.Sleep).ObservedDays)
	assert.Equal(t, 1, got.Score.Item(signal.Sleep).Points)
	assert.Equal(t, 6, got.Score.Total)
	assert.Equal(t, score.Mild, got.Score.Band)

	assert.Equal(t, 7, got.Confidence.ConversationDays)
	assert.Equal(t, confidence.Ratio{Num: 3, Den: 8}, got.Confidence.ItemCoverage)
	assert.Equal(t, confidence.Ratio{Num: 11, Den: 12}, got.Confidence.Explicitness)
	assert.Equal(t, confidence.Low, got.Confidence.Level)

	assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
	assert.Equal(t, stage.Reflection, got.Stage.State.Raw)
	assert.True(t, got.Stage.State.Held)

	assert.False(t, got.Baseline.Established)
	assert.False(t, got.Change.State.Running)
	assert.Empty(t, got.Change.Series)
	assert.Equal(t, crisis.State{}, got.GateState())

	assert.Equal(t, 7, got.Trend.ConversationDays)
}

// 신호를 취소하거나 하루를 지우면 남은 기록으로 처음부터 다시 구한다.
func TestOracleRecalculationAfterCancelAndDelete(t *testing.T) {
	t.Run("흥미 신호 하나를 취소하면 그 항목의 점수가 내려간다", func(t *testing.T) {
		// 3월 4일의 흥미를 취소 → 흥미 o=3 → 6일 → 1점. 합 3+1+1 = 5점, 여전히 가벼움.
		rows := weekOfRows(t)
		cancel(t, rows, "2026-03-04", "c1", signal.Interest)
		got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.Score.ConversationDays, "취소해도 그날은 대화한 날이다")
		assert.Equal(t, 3, got.Score.Item(signal.Interest).ObservedDays)
		assert.Equal(t, 1, got.Score.Item(signal.Interest).Points)
		assert.Equal(t, 5, got.Score.Total)
		assert.Equal(t, score.Mild, got.Score.Band)
	})

	t.Run("밤 대화의 수면 신호까지 취소하면 그날의 수면은 아침의 판단으로 돌아간다", func(t *testing.T) {
		// 수면 o=0 → 0점. 합 3+1+0 = 4점 → 최소 구간, 점수로 본 단계도 0단계라 묶일 것이 없다.
		// 관찰됨은 기분 7 + 흥미 3 = 10개, 모두 직접 언급 → 10/10.
		// 수면은 "관찰되지 않음"으로 여전히 언급된 항목이라 항목 충족도는 3/8 그대로다.
		rows := weekOfRows(t)
		cancel(t, rows, "2026-03-04", "c1", signal.Interest)
		cancel(t, rows, "2026-03-05", "c2", signal.Sleep)
		got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 0, got.Score.Item(signal.Sleep).ObservedDays)
		assert.Equal(t, 4, got.Score.Total)
		assert.Equal(t, score.Minimal, got.Score.Band)
		assert.Equal(t, confidence.Ratio{Num: 10, Den: 10}, got.Confidence.Explicitness)
		assert.Equal(t, confidence.Ratio{Num: 3, Den: 8}, got.Confidence.ItemCoverage)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.False(t, got.Stage.State.Held)
		assert.Equal(t, assess.MarkNotObserved, got.Trend.Row(signal.TrendSleep).Marks[got.Trend.To.DaysSince(got.Trend.From)-2])
	})

	t.Run("하루의 신호를 모두 취소해도 그날은 대화한 날로 남는다", func(t *testing.T) {
		// 3월 2일의 세 행을 모두 취소 → 그날은 여덟 항목이 모두 언급 없음인 하루다. n = 7 그대로.
		// 기분 o=6 → 12일 → 3점, 흥미 o=3 → 6일 → 1점, 수면 o=1 → 1점. 합 5점.
		rows := weekOfRows(t)
		cancel(t, rows, "2026-03-02", "c1", signal.Mood)
		cancel(t, rows, "2026-03-02", "c1", signal.Sleep)
		cancel(t, rows, "2026-03-02", "c1", signal.Interest)
		got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.Score.ConversationDays)
		assert.False(t, got.Score.Insufficient)
		assert.Equal(t, 12, got.Score.Item(signal.Mood).ConvertedDays)
		assert.Equal(t, 5, got.Score.Total)
	})

	t.Run("하루를 지우면 대화한 날이 6일이 되어 점수를 내지 않는다", func(t *testing.T) {
		rows := weekOfRows(t)
		delete(rows, date(t, "2026-03-02"))
		got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 6, got.Score.ConversationDays)
		assert.True(t, got.Score.Insufficient)
		assert.Equal(t, score.NoBand, got.Score.Band)
		assert.True(t, got.Confidence.Insufficient)
		assert.Equal(t, confidence.Low, got.Confidence.Level)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.Equal(t, crisis.State{}, got.GateState())
	})

	t.Run("행이 하나도 없는 날짜는 대화하지 않은 날과 같다", func(t *testing.T) {
		rows := weekOfRows(t)
		rows[date(t, "2026-03-02")] = nil
		got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 6, got.Score.ConversationDays)
		assert.True(t, got.Score.Insufficient)
	})
}

// 점 달력: 세 줄(기분, 수면, 에너지), 최근 14일, 하루에 한 칸.
func TestOracleDotCalendar(t *testing.T) {
	// 기준일 3월 14일 → 칸은 3월 1일~14일.
	//   3월 1일 흥미 관찰됨                        → 기분 줄 찬 점
	//   3월 2일 기분 관찰되지 않음                 → 기분 줄 빈 점
	//   3월 3일 대화 없음                          → 세 줄 모두 빈칸
	//   3월 4일 수면 관찰됨                        → 기분 줄 작은 점(언급 없음), 수면 줄 찬 점
	//   3월 5일 흥미 관찰되지 않음 + 기분 관찰됨   → 기분 줄 찬 점(하나라도 관찰되면 관찰됨), 에너지 줄 빈 점
	//   3월 6일 피로 관찰됨(간접)                  → 에너지 줄 찬 점
	//   3월 7일~14일 대화 없음
	days := diary(t, "2026-03-01",
		"O.......",
		".X......",
		"",
		"..O.....",
		"XO.X....",
		"...o....",
	)
	got := evaluate(t, days, "2026-03-14")
	trend := got.Trend

	assert.Equal(t, date(t, "2026-03-01"), trend.From)
	assert.Equal(t, date(t, "2026-03-14"), trend.To)
	require.Len(t, trend.Dates, 14)
	assert.Equal(t, date(t, "2026-03-01"), trend.Dates[0])
	assert.Equal(t, date(t, "2026-03-14"), trend.Dates[13])
	assert.Equal(t, 5, trend.ConversationDays)

	blank := assess.MarkNoConversation
	quiet := assess.MarkNotMentioned
	empty := assess.MarkNotObserved
	filled := assess.MarkObserved

	mood := trend.Row(signal.TrendMood)
	assert.Equal(t, []assess.Mark{filled, empty, blank, quiet, filled, quiet, blank, blank, blank, blank, blank, blank, blank, blank}, mood.Marks)
	assert.Equal(t, 2, mood.ObservedDays)
	assert.Equal(t, 5, mood.ConversationDays)

	sleep := trend.Row(signal.TrendSleep)
	assert.Equal(t, []assess.Mark{quiet, quiet, blank, filled, quiet, quiet, blank, blank, blank, blank, blank, blank, blank, blank}, sleep.Marks)
	assert.Equal(t, 1, sleep.ObservedDays)

	energy := trend.Row(signal.TrendEnergy)
	assert.Equal(t, []assess.Mark{quiet, quiet, blank, quiet, empty, filled, blank, blank, blank, blank, blank, blank, blank, blank}, energy.Marks)
	assert.Equal(t, 1, energy.ObservedDays)

	for _, row := range signal.AllTrendRows() {
		assert.Equal(t, assess.ComparisonNone, trend.Row(row).Comparison, "기준선이 없으면 평소와 견주지 않는다")
	}
}

// 평소와의 비교는 기준선 기간의 빈도와 최근 14일의 빈도를 견준다.
func TestOracleComparisonWithUsual(t *testing.T) {
	t.Run("평소 14일 중 0일이던 수면이 최근 14일 중 14일이면 평소보다 잦다", func(t *testing.T) {
		// 3월 1일~14일: 아무것도 관찰되지 않음(기준선). 3월 15일~28일: 날마다 수면 관찰됨. 기준일 3월 28일.
		days := diary(t, "2026-03-01", join(repeat(14, "XXXXXXXX"), repeat(14, "XXOXXXXX"))...)
		got := evaluate(t, days, "2026-03-28")

		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 14, sleep.ObservedDays)
		assert.Equal(t, 14, sleep.ConversationDays)
		assert.Equal(t, baseline.Rate{ObservedDays: 0, Days: 14}, sleep.Usual)
		assert.Equal(t, assess.MoreOften, sleep.Comparison)

		// 기분 줄은 평소에도 최근에도 0일이다.
		assert.Equal(t, assess.Similar, got.Trend.Row(signal.TrendMood).Comparison)
	})

	t.Run("평소 날마다 관찰되던 수면이 최근에는 한 번도 없으면 평소보다 드물다", func(t *testing.T) {
		days := diary(t, "2026-03-01", join(repeat(14, "XXOXXXXX"), repeat(14, "XXXXXXXX"))...)
		got := evaluate(t, days, "2026-03-28")

		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 0, sleep.ObservedDays)
		assert.Equal(t, baseline.Rate{ObservedDays: 14, Days: 14}, sleep.Usual)
		assert.Equal(t, assess.LessOften, sleep.Comparison)
	})

	t.Run("기준선 기간의 마지막 날에는 아직 견주지 않는다", func(t *testing.T) {
		days := diary(t, "2026-03-01", repeat(14, "XXOXXXXX")...)
		got := evaluate(t, days, "2026-03-14")

		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 14, sleep.ObservedDays)
		assert.Equal(t, assess.ComparisonNone, sleep.Comparison)
		assert.Equal(t, baseline.Rate{}, sleep.Usual, "모으는 중인 값은 평소로 내보내지 않는다")
	})
}

// 관문에 넘기는 상태: 추정 점수가 10 이상인지, 변화 감지 상태인지.
func TestOracleGateState(t *testing.T) {
	t.Run("점수의 경계는 10점이다", func(t *testing.T) {
		// 3월 1일~14일 날마다 대화(n = 14, 환산 일수 = 관찰된 일수). 기준선은 아직 잡히지 않아 변화 감지는 거짓이다.
		//   2점 하나 + 1점 일곱 = 9점  → 거짓
		//   2점 둘 + 1점 여섯 = 10점   → 참
		nine := evaluate(t, diary(t, "2026-03-01", countsDiary(14, [signal.ItemCount]int{7, 1, 1, 1, 1, 1, 1, 1})...), "2026-03-14")
		assert.Equal(t, 9, nine.Score.Total)
		assert.Equal(t, crisis.State{}, nine.GateState())

		ten := evaluate(t, diary(t, "2026-03-01", countsDiary(14, [signal.ItemCount]int{7, 7, 1, 1, 1, 1, 1, 1})...), "2026-03-14")
		assert.Equal(t, 10, ten.Score.Total)
		assert.Equal(t, crisis.State{ScoreElevated: true}, ten.GateState())
	})

	// 아래 경우들은 같은 기록이다.
	// 3월 1일~14일: 관찰된 것 없음(평소 0). 3월 15일~21일: 날마다 네 항목 관찰(하루 +3.5가 2로 묶인다). 그 뒤로 대화 없음.
	//   3월 15일 4점, S = 2                  → 둘 다 거짓
	//   3월 16일 4점, S = 4 한계값과 같다    → 둘 다 거짓
	//   3월 17일 4점, S = 6 > 4              → 변화 감지만 참
	//   3월 28일 창에 3월 15일~21일만 남아 n = 7, 12점, S = 8(천장) → 둘 다 참
	//   3월 29일 n = 6 기록 부족 → 점수 쪽은 거짓, 변화 감지는 그대로 참
	//   4월 3일  n = 1(3월 21일만 남는다) → 변화 감지는 그대로 참
	//   4월 4일  n = 0 → 둘 다 거짓. 누적값은 8 그대로지만, 창 안에 대화한 날이 없으면 변화 감지를 꺼진 것으로 전한다.
	days := diary(t, "2026-03-01", join(repeat(14, "XXXXXXXX"), repeat(7, "OOOOXXXX"))...)
	tests := []struct {
		name string
		asOf string
		want crisis.State
	}{
		{"3월 15일: 4점이고 누적값 2라 둘 다 아니다", "2026-03-15", crisis.State{}},
		{"3월 16일: 누적값이 한계값과 같아 아직 아니다", "2026-03-16", crisis.State{}},
		{"3월 17일: 점수는 낮아도 변화 감지 상태다", "2026-03-17", crisis.State{ChangeDetected: true}},
		{"3월 28일: 12점이고 변화 감지 상태다", "2026-03-28", crisis.State{ScoreElevated: true, ChangeDetected: true}},
		{"3월 29일: 기록 부족이면 점수 쪽은 거짓이고 변화 감지는 그대로 전한다", "2026-03-29", crisis.State{ChangeDetected: true}},
		{"4월 3일: 창에 하루라도 남아 있으면 변화 감지는 그대로 전한다", "2026-04-03", crisis.State{ChangeDetected: true}},
		{"4월 4일: 창이 비면 변화 감지도 꺼진 것으로 전한다", "2026-04-04", crisis.State{}},
		{"4월 10일: 그 뒤로도 대화가 없는 동안은 꺼진 채다", "2026-04-10", crisis.State{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evaluate(t, days, tt.asOf).GateState())
		})
	}

	t.Run("창이 빈 동안에도 누적값과 감지 상태 자체는 평가에 그대로 남아 있다", func(t *testing.T) {
		got := evaluate(t, days, "2026-04-10")
		assert.InDelta(t, 8.0, got.Change.State.S, 0)
		assert.True(t, got.Change.State.Detected)
		assert.Equal(t, 0, got.Score.ConversationDays)
	})

	t.Run("평가가 없으면 둘 다 거짓이다", func(t *testing.T) {
		assert.Equal(t, crisis.State{}, assess.Evaluation{}.GateState())
	})
}

// 서서히 나빠지는 사용자를 처음부터 끝까지 따라간다.
func TestOracleSlowWorseningEndToEnd(t *testing.T) {
	// 3월 1일~14일: 날마다 기분만 관찰됨, 나머지 일곱 항목은 관찰되지 않음 → 평소 1.0, 기간은 3월 14일까지.
	// 3월 15일부터: 기분과 수면이 관찰됨(x = 2) → 하루 +0.5.
	//   점수(n = 14): 기분 o=14 → 3점. 수면은 관찰된 일수가 그대로 환산 일수다.
	//     3월 20일 수면 o=6 → 1점 → 합 4점 → 0단계
	//     3월 21일 수면 o=7 → 2점 → 합 5점 → 1단계(신뢰도 높음)
	//   변화 탐지: 3월 22일 S = 4.0(한계값과 같아 아직), 3월 23일 S = 4.5 → 감지
	days := diary(t, "2026-03-01", join(repeat(14, "XOXXXXXX"), repeat(11, "XOOXXXXX"))...)

	t.Run("3월 20일: 4점, 0단계", func(t *testing.T) {
		got := evaluate(t, days, "2026-03-20")
		assert.Equal(t, 4, got.Score.Total)
		assert.Equal(t, score.Minimal, got.Score.Band)
		assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
		assert.InDelta(t, 3.0, got.Change.State.S, 0)
	})

	t.Run("3월 21일: 5점, 1단계", func(t *testing.T) {
		got := evaluate(t, days, "2026-03-21")
		assert.Equal(t, 5, got.Score.Total)
		assert.Equal(t, score.Mild, got.Score.Band)
		assert.Equal(t, confidence.High, got.Confidence.Level)
		assert.Equal(t, stage.Reflection, got.Stage.State.Stage)
		assert.False(t, got.Change.State.Detected)
	})

	t.Run("3월 22일: 누적값이 한계값과 같아 아직 감지가 아니다", func(t *testing.T) {
		got := evaluate(t, days, "2026-03-22")
		assert.InDelta(t, 4.0, got.Change.State.S, 0)
		assert.False(t, got.Change.State.Detected)
		assert.False(t, got.GateState().ChangeDetected)
	})

	t.Run("3월 23일: 감지", func(t *testing.T) {
		got := evaluate(t, days, "2026-03-23")

		assert.True(t, got.Baseline.Established)
		assert.Equal(t, date(t, "2026-03-14"), got.Baseline.End)
		assert.InDelta(t, 1.0, got.Baseline.Mu, 0)

		assert.InDelta(t, 4.5, got.Change.State.S, 0)
		assert.True(t, got.Change.State.Detected)
		require.Len(t, got.Change.Series, 9)

		// 창 3월 10일~23일: 기분 14일 → 3점, 수면 9일 → 2점.
		assert.Equal(t, 5, got.Score.Total)
		assert.Equal(t, stage.Reflection, got.Stage.State.Stage)
		assert.Equal(t, crisis.State{ChangeDetected: true}, got.GateState())

		// 추세 화면: 수면은 최근 14일 중 9일, 평소 14일 중 0일 → 평소보다 잦음. 기분은 14일 중 14일로 평소와 같다.
		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 9, sleep.ObservedDays)
		assert.Equal(t, assess.MoreOften, sleep.Comparison)
		assert.Equal(t, assess.Similar, got.Trend.Row(signal.TrendMood).Comparison)
	})
}

// 기록이 없어도 평가는 나온다. 모든 값이 "아직 모른다" 쪽이다.
func TestOracleNoRecords(t *testing.T) {
	got := evaluate(t, nil, "2026-03-14")

	assert.True(t, got.Score.Insufficient)
	assert.Equal(t, score.NoBand, got.Score.Band)
	assert.True(t, got.Confidence.Insufficient)
	assert.Equal(t, confidence.Low, got.Confidence.Level)
	assert.False(t, got.Baseline.Established)
	assert.Empty(t, got.Change.Series)
	assert.False(t, got.Change.State.Detected)
	assert.Equal(t, stage.Everyday, got.Stage.State.Stage)
	assert.Equal(t, crisis.State{}, got.GateState())

	require.Len(t, got.Trend.Dates, 14)
	for _, row := range signal.AllTrendRows() {
		for _, mark := range got.Trend.Row(row).Marks {
			assert.Equal(t, assess.MarkNoConversation, mark)
		}
	}
}

// 지난 날짜를 기준일로 넣으면 그날 알 수 있었던 기록만으로 구한다. 틀린 입력은 오류다.
func TestOracleAsOfAndErrors(t *testing.T) {
	days := diary(t, "2026-03-01", repeat(20, "OOOOXXXX")...)

	t.Run("3월 7일을 기준일로 넣으면 뒤의 13일은 없는 것과 같다", func(t *testing.T) {
		got := evaluate(t, days, "2026-03-07")
		assert.Equal(t, 7, got.Score.ConversationDays)
		assert.Equal(t, 12, got.Score.Total)
		assert.False(t, got.Baseline.Established)
		require.Len(t, got.Stage.Series, 7)
		// 점수로는 2단계지만 하루에 한 단계만 오른다. 첫 엿새는 기록 부족이라 0단계였다.
		assert.Equal(t, stage.Suggestion, got.Stage.State.Raw)
		assert.Equal(t, stage.Reflection, got.Stage.State.Stage)
	})

	t.Run("기준일이 비어 있으면 오류다", func(t *testing.T) {
		_, err := assess.Evaluate(days, recorddate.Date{}, params.Default())
		require.ErrorIs(t, err, assess.ErrNoAsOf)
	})

	t.Run("날짜순이 아닌 기록은 오류다", func(t *testing.T) {
		reversed := []signal.Day{days[1], days[0]}
		_, err := assess.Evaluate(reversed, date(t, "2026-03-07"), params.Default())
		require.ErrorIs(t, err, signal.ErrUnsortedDays)
	})

	t.Run("판단과 명시성이 맞지 않는 행은 오류다", func(t *testing.T) {
		rows := map[recorddate.Date][]signal.Row{
			date(t, "2026-03-01"): {{ConversationID: "c1", Item: signal.Mood, Status: signal.Observed, Explicitness: signal.None}},
		}
		_, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})
}

// 하루에 대화가 여럿이면 항목마다 하나로 합친다. 관찰됨의 명시성은 관찰된 행만 보고 정한다.
func TestOracleMergedExplicitness(t *testing.T) {
	// 3월 1일~7일 날마다 대화 c1에서 기분이 간접 추론으로 관찰됐다. 몇몇 날에는 두 번째 대화 c2가 있다.
	//   3월 2일 c2: 기분 관찰됨(직접)             → 그날은 관찰됨, 직접 언급
	//   3월 3일 c2: 기분 관찰되지 않음(직접)      → 그날은 관찰됨이 이기고, 명시성은 관찰된 행의 것인 간접 추론
	//   3월 4일 c2: 기분 관찰됨(직접)이지만 취소 → 그날은 간접 추론
	// 관찰됨 판단 7개 가운데 직접 언급은 3월 2일 하나 → 명시성 1/7.
	rows := make(map[recorddate.Date][]signal.Row)
	first := date(t, "2026-03-01")
	for offset := range 7 {
		rows[first.AddDays(offset)] = []signal.Row{
			{ConversationID: "c1", Item: signal.Mood, Status: signal.Observed, Explicitness: signal.Indirect},
		}
	}
	second := func(day string, status signal.Status, cancelled bool) {
		rows[date(t, day)] = append(rows[date(t, day)], signal.Row{
			ConversationID: "c2", Item: signal.Mood, Status: status, Explicitness: signal.Direct, Cancelled: cancelled,
		})
	}
	second("2026-03-02", signal.Observed, false)
	second("2026-03-03", signal.NotObserved, false)
	second("2026-03-04", signal.Observed, true)

	got, err := assess.EvaluateRows(rows, date(t, "2026-03-07"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, 7, got.Score.Item(signal.Mood).ObservedDays)
	assert.Equal(t, 3, got.Score.Total)
	assert.Equal(t, 7, got.Confidence.ObservedJudgements)
	assert.Equal(t, 1, got.Confidence.DirectJudgements)
	assert.Equal(t, confidence.Ratio{Num: 1, Den: 7}, got.Confidence.Explicitness)
}

// 기준선 기간의 날을 지우면 평소가 다시 정해지고, 그 뒤의 변화 탐지도 처음부터 다시 돈다.
func TestOracleBaselineDeletionMovesDetection(t *testing.T) {
	// 3월 1일~14일: 하루 걸러 한 항목(1, 0, 1, 0, …) → 평소 7 ÷ 14 = 0.5.
	// 3월 15일~20일: 날마다 두 항목.
	lines := func() []string {
		usual := make([]string, 0, 14)
		for range 7 {
			usual = append(usual, "OXXXXXXX", "XXXXXXXX")
		}
		return join(usual, repeat(6, "OOXXXXXX"))
	}

	firstDetected := func(t *testing.T, e assess.Evaluation) recorddate.Date {
		t.Helper()
		for _, pt := range e.Change.Series {
			if pt.Detected {
				return pt.Date
			}
		}
		return recorddate.Date{}
	}

	t.Run("지우기 전: 하루 +1씩 쌓여 닷새째인 3월 19일에 감지한다", func(t *testing.T) {
		got := evaluate(t, diary(t, "2026-03-01", lines()...), "2026-03-20")
		assert.InDelta(t, 0.5, got.Baseline.Mu, 0)
		assert.Equal(t, date(t, "2026-03-19"), firstDetected(t, got))
	})

	t.Run("기간 가운데의 하루(3월 3일, 한 항목)를 지우면 하루 일찍 감지한다", func(t *testing.T) {
		// 기간은 그대로 3월 14일까지이고 대화한 날은 13일, 합 6 → 평소 6/13.
		// 하루 +(2 − 6/13 − 1/2) = 27/26 ≈ 1.038. 사흘째 3.12, 나흘째(3월 18일) 4.15 > 4.
		l := lines()
		l[2] = ""
		got := evaluate(t, diary(t, "2026-03-01", l...), "2026-03-20")

		assert.Equal(t, date(t, "2026-03-14"), got.Baseline.End)
		assert.Equal(t, 13, got.Baseline.Days)
		assert.InDelta(t, 6.0/13.0, got.Baseline.Mu, 1e-12)
		assert.Equal(t, date(t, "2026-03-18"), firstDetected(t, got))
	})

	t.Run("첫날(3월 1일)을 지우면 기간이 하루 밀려 하루 늦게 감지한다", func(t *testing.T) {
		// 시작이 3월 2일로 옮겨 가 기간은 3월 15일까지다. 3월 15일의 두 항목이 평소에 들어간다: 합 6 + 2 = 8 → 8/14.
		// 누적은 3월 16일부터, 하루 +(2 − 8/14 − 1/2) = 13/14 ≈ 0.929. 나흘째(3월 19일) 3.71, 닷새째(3월 20일) 4.64 > 4.
		l := lines()
		l[0] = ""
		got := evaluate(t, diary(t, "2026-03-01", l...), "2026-03-20")

		assert.Equal(t, date(t, "2026-03-02"), got.Baseline.Start)
		assert.Equal(t, date(t, "2026-03-15"), got.Baseline.End)
		assert.InDelta(t, 8.0/14.0, got.Baseline.Mu, 1e-12)
		assert.Equal(t, date(t, "2026-03-16"), got.Change.From)
		assert.Equal(t, date(t, "2026-03-20"), firstDetected(t, got))
	})
}
