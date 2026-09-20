package cusum_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부르고, 평소의 값도 손으로 넣지 않고 기록에서 구한다.
//
// 누적값이 한계값과 "딱 같은" 경우를 보려고 평소의 하루 평균이 0, 0.5, 1, 2처럼 이진 소수로 떨어지는 기록을 쓴다.
// 그러면 누적값에 오차가 실리지 않아 같은지 다른지를 정확히 말할 수 있다.
//
// 기본값: 여유 0.5, 한계값 4, 하루에 늘 수 있는 양 2, 누적값의 천장은 한계값의 2배인 8.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
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
// O는 관찰됨(직접 언급), X는 관찰되지 않음(직접), 점은 언급 없음이다. 빈 줄은 대화하지 않은 날이라 하루를 만들지 않는다.
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
			case 'X':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
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

func join(parts ...[]string) []string {
	var lines []string
	for _, part := range parts {
		lines = append(lines, part...)
	}
	return lines
}

// x는 count개 항목이 관찰된 하루다.
func x(count int) string {
	line := []byte("XXXXXXXX")
	for idx := range count {
		line[idx] = 'O'
	}
	return string(line)
}

// xs는 하루에 하나씩, 적어 준 수만큼 관찰된 날들이다. 음수는 대화하지 않은 날이다.
func xs(counts ...int) []string {
	lines := make([]string, 0, len(counts))
	for _, count := range counts {
		if count < 0 {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, x(count))
	}
	return lines
}

// usualHalf는 3월 1일~14일 날마다 대화한 기준선 기간이다. 하루 걸러 한 항목씩 관찰돼 평소가 7 ÷ 14 = 0.5다.
// 기간은 3월 14일에 끝나고 누적은 3월 15일부터다.
func usualHalf() []string {
	return xs(1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0)
}

// usualOne은 같은 기간에 날마다 한 항목씩 관찰돼 평소가 14 ÷ 14 = 1이다.
func usualOne() []string {
	return xs(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
}

func run(t *testing.T, days []signal.Day, asOf string, p params.Params) cusum.Result {
	t.Helper()
	base, err := baseline.Compute(days, date(t, asOf), p)
	require.NoError(t, err)
	got, err := cusum.Run(days, base, p)
	require.NoError(t, err)
	return got
}

type wantPoint struct {
	date     string
	observed int
	s        float64
	detected bool
}

func assertSeries(t *testing.T, want []wantPoint, got []cusum.Point) {
	t.Helper()
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, date(t, w.date), got[i].Date)
		assert.Equal(t, w.observed, got[i].Observed, w.date)
		assert.InDelta(t, w.s, got[i].S, 0, w.date)
		assert.Equal(t, w.detected, got[i].Detected, w.date)
	}
}

// 누적값이 한계값을 "넘어야" 감지다. 딱 같으면 아직 아니다.
func TestOracleExactlyAtThreshold(t *testing.T) {
	// 평소 0.5, 여유 0.5 → 하루의 증가량은 x − 1.
	//   3월 15일 x=3 → +2 → S = 2
	//   3월 16일 x=3 → +2 → S = 4   한계값 4와 같다 → 감지 아님
	//   3월 17일 x=2 → +1 → S = 5   → 감지
	days := diary(t, "2026-03-01", join(usualHalf(), xs(3, 3, 2))...)
	got := run(t, days, "2026-03-17", params.Default())

	assert.Equal(t, date(t, "2026-03-15"), got.From)
	assertSeries(t, []wantPoint{
		{"2026-03-15", 3, 2, false},
		{"2026-03-16", 3, 4, false},
		{"2026-03-17", 2, 5, true},
	}, got.Series)
	assert.True(t, got.State.Running)
	assert.True(t, got.State.Detected)
	assert.InDelta(t, 5.0, got.State.S, 0)
}

// 서서히 나빠지는 흐름을 며칠째에 잡는지.
func TestOracleSlowWorsening(t *testing.T) {
	t.Run("평소 0.5에서 날마다 두 항목이면 닷새째에 감지한다", func(t *testing.T) {
		// 하루 +1씩: 1, 2, 3, 4, 5. 나흘째(3월 18일)의 4는 한계값과 같아 아직이고 닷새째(3월 19일)에 넘는다.
		days := diary(t, "2026-03-01", join(usualHalf(), xs(2, 2, 2, 2, 2, 2))...)
		got := run(t, days, "2026-03-20", params.Default())

		assertSeries(t, []wantPoint{
			{"2026-03-15", 2, 1, false},
			{"2026-03-16", 2, 2, false},
			{"2026-03-17", 2, 3, false},
			{"2026-03-18", 2, 4, false},
			{"2026-03-19", 2, 5, true},
			{"2026-03-20", 2, 6, true},
		}, got.Series)
	})

	t.Run("평소 1에서 날마다 두 항목이면 아흐레째에 감지한다", func(t *testing.T) {
		// 하루 +0.5씩(2 − 1 − 0.5). 여드레째(3월 22일) 4.0은 한계값과 같고, 아흐레째(3월 23일) 4.5에 넘는다.
		days := diary(t, "2026-03-01", join(usualOne(), xs(2, 2, 2, 2, 2, 2, 2, 2, 2, 2))...)
		got := run(t, days, "2026-03-24", params.Default())

		require.Len(t, got.Series, 10)
		assert.InDelta(t, 4.0, got.Series[7].S, 0)
		assert.False(t, got.Series[7].Detected, "3월 22일")
		assert.Equal(t, date(t, "2026-03-23"), got.Series[8].Date)
		assert.InDelta(t, 4.5, got.Series[8].S, 0)
		assert.True(t, got.Series[8].Detected, "3월 23일")

		// 지난 날짜의 상태를 물으면 그날을 기준일로 다시 돌린 것과 같아야 한다.
		assert.False(t, got.StateAt(date(t, "2026-03-22")).Detected)
		assert.True(t, got.StateAt(date(t, "2026-03-23")).Detected)
		replay := run(t, days, "2026-03-22", params.Default())
		assert.False(t, replay.State.Detected)
		assert.InDelta(t, 4.0, replay.State.S, 0)
	})

	t.Run("평균이 이진 소수로 떨어지지 않아도 감지하는 날은 같다", func(t *testing.T) {
		// 3월 1, 3, 5, …, 13일 이레 대화, 관찰된 수 2, 1, 1, 1, 1, 1, 2 → 평소 9/7.
		// 기간은 3월 14일까지. 그 뒤 날마다 세 항목: 하루 +(3 − 9/7 − 1/2) = +17/14 ≈ 1.2143.
		//   1.214, 2.429, 3.643, 4.857 → 나흘째(3월 18일)에 감지.
		days := diary(t, "2026-03-01", join(xs(2, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 2, -1), xs(3, 3, 3, 3))...)
		got := run(t, days, "2026-03-18", params.Default())

		require.Len(t, got.Series, 4)
		assert.InDelta(t, 17.0/14.0, got.Series[0].S, 1e-9)
		assert.InDelta(t, 51.0/14.0, got.Series[2].S, 1e-9)
		assert.False(t, got.Series[2].Detected)
		assert.InDelta(t, 68.0/14.0, got.Series[3].S, 1e-9)
		assert.True(t, got.Series[3].Detected)
	})
}

// 대화하지 않은 날은 건너뛰고 누적값을 그대로 둔다.
func TestOracleSilentDaysKeepTheSum(t *testing.T) {
	// 평소 0.5. 3월 15일 x=4 → +3이 하루 상한 2로 묶여 S = 2. 3월 16일~24일은 대화가 없다.
	// 3월 25일 x=3 → +2 → S = 4(한계값과 같아 아직). 3월 26일 x=2 → +1 → S = 5 → 감지.
	days := diary(t, "2026-03-01", join(usualHalf(), xs(4, -1, -1, -1, -1, -1, -1, -1, -1, -1, 3, 2))...)
	got := run(t, days, "2026-03-26", params.Default())

	assertSeries(t, []wantPoint{
		{"2026-03-15", 4, 2, false},
		{"2026-03-25", 3, 4, false},
		{"2026-03-26", 2, 5, true},
	}, got.Series)

	quiet := got.StateAt(date(t, "2026-03-20"))
	assert.True(t, quiet.Running)
	assert.InDelta(t, 2.0, quiet.S, 0, "대화하지 않은 날에는 줄지도 늘지도 않는다")
	assert.False(t, quiet.Detected)

	before := run(t, days, "2026-03-24", params.Default())
	assert.InDelta(t, 2.0, before.State.S, 0)
}

// 누적값은 0 아래로 내려가지 않는다. 좋았던 날을 저축해 두지 않는다.
func TestOracleFloorAtZero(t *testing.T) {
	// 평소 1, 여유 0.5 → 증가량 x − 1.5.
	//   3월 15일 x=0 → −1.5 → 0에서 멈춤
	//   3월 16일 x=3 → +1.5 → 1.5
	//   3월 17일 x=0 → −1.5 → 0
	//   3월 18일 x=0 → −1.5 → 0 (음수로 쌓이지 않는다)
	//   3월 19일 x=6 → +4.5가 2로 묶여 2
	//   3월 20일 x=6 → 4 (한계값과 같아 아직)
	//   3월 21일 x=6 → 6 → 감지. 앞의 좋은 날들이 깎아 주지 않는다. 저축해 두었다면(−4.5에서 출발) 이날 1.5였을 것이다.
	days := diary(t, "2026-03-01", join(usualOne(), xs(0, 3, 0, 0, 6, 6, 6))...)
	got := run(t, days, "2026-03-21", params.Default())

	assertSeries(t, []wantPoint{
		{"2026-03-15", 0, 0, false},
		{"2026-03-16", 3, 1.5, false},
		{"2026-03-17", 0, 0, false},
		{"2026-03-18", 0, 0, false},
		{"2026-03-19", 6, 2, false},
		{"2026-03-20", 6, 4, false},
		{"2026-03-21", 6, 6, true},
	}, got.Series)
}

// 한계값을 넘은 뒤에도 누적값을 0으로 되돌리지 않는다. 나아지면 줄어들고, 한계값 이하로 내려오면 감지가 풀린다.
func TestOracleNoResetAndRelease(t *testing.T) {
	// 평소 0.5 → 증가량 x − 1. 여섯 항목인 날의 +5는 2로 묶인다.
	//   3월 15일 x=6 → +2 → 2
	//   3월 16일 x=6 → +2 → 4  한계값과 같아 아직
	//   3월 17일 x=6 → +2 → 6  감지
	//   3월 18일 x=2 → +1 → 7  감지(0에서 다시 시작했다면 1이었을 것이다)
	//   3월 19일 x=0 → −1 → 6  감지
	//   3월 20일 x=0 → −1 → 5  감지
	//   3월 21일 x=0 → −1 → 4  한계값과 같다 → 감지가 풀린다
	//   3월 22일 x=0 → −1 → 3
	days := diary(t, "2026-03-01", join(usualHalf(), xs(6, 6, 6, 2, 0, 0, 0, 0))...)
	got := run(t, days, "2026-03-22", params.Default())

	assertSeries(t, []wantPoint{
		{"2026-03-15", 6, 2, false},
		{"2026-03-16", 6, 4, false},
		{"2026-03-17", 6, 6, true},
		{"2026-03-18", 2, 7, true},
		{"2026-03-19", 0, 6, true},
		{"2026-03-20", 0, 5, true},
		{"2026-03-21", 0, 4, false},
		{"2026-03-22", 0, 3, false},
	}, got.Series)
	assert.False(t, got.State.Detected)
}

// 하루에 늘 수 있는 양은 기본으로 2다. 하루만 크게 나빴던 날 하나로는 울리지 않는다.
func TestOracleDailyCap(t *testing.T) {
	// 평소 0.5. 하루만 크게 나빴던 사람: 3월 15일 x=6, 그 뒤로 x=0.
	oneBadDay := diary(t, "2026-03-01", join(usualHalf(), xs(6, 0, 0))...)

	t.Run("상한을 끄면 여섯 항목이 관찰된 하루로 한계값을 넘는다", func(t *testing.T) {
		// +5 → 5 감지, −1 → 4 풀림, −1 → 3.
		p := params.Default()
		p.CUSUM.MaxStep = 0
		got := run(t, oneBadDay, "2026-03-17", p)
		assertSeries(t, []wantPoint{
			{"2026-03-15", 6, 5, true},
			{"2026-03-16", 0, 4, false},
			{"2026-03-17", 0, 3, false},
		}, got.Series)
		assert.False(t, got.Series[0].Capped)
	})

	t.Run("기본값에서는 그 하루로는 울리지 않는다", func(t *testing.T) {
		// +5가 +2로 묶인다 → 2, −1 → 1, −1 → 0.
		got := run(t, oneBadDay, "2026-03-17", params.Default())
		assertSeries(t, []wantPoint{
			{"2026-03-15", 6, 2, false},
			{"2026-03-16", 0, 1, false},
			{"2026-03-17", 0, 0, false},
		}, got.Series)
		assert.True(t, got.Series[0].Capped)
		assert.InDelta(t, 2.0, got.Series[0].Step, 0)
	})

	t.Run("나쁜 날이 이어지면 사흘째에 울리고 줄어드는 쪽은 묶지 않는다", func(t *testing.T) {
		// x=6이 사흘: 2, 4(같음, 아직), 6(감지). 나흘째 x=0 → −1은 그대로 → 5.
		days := diary(t, "2026-03-01", join(usualHalf(), xs(6, 6, 6, 0))...)
		got := run(t, days, "2026-03-18", params.Default())
		assertSeries(t, []wantPoint{
			{"2026-03-15", 6, 2, false},
			{"2026-03-16", 6, 4, false},
			{"2026-03-17", 6, 6, true},
			{"2026-03-18", 0, 5, true},
		}, got.Series)
		assert.False(t, got.Series[3].Capped)
		assert.InDelta(t, -1.0, got.Series[3].Step, 0)
	})
}

// 누적값은 한계값의 2배인 8을 넘지 못한다. 그날의 증가량을 더한 다음에 천장에 맞춘다.
func TestOracleCeiling(t *testing.T) {
	// 평소 0.5 → 증가량 x − 1.
	//   3월 15일~17일 x=6(+5가 2로 묶임) → 2, 4, 6
	//   3월 18일 x=2 → +1 → 7
	//   3월 19일 x=6 → +2 → 9 → 천장 8에 맞춘다
	//   3월 20일~24일 x=6 → 8에 머문다
	//   3월 25일부터 x=0 → −1씩: 7, 6, 5, 4. 나아진 지 나흘째(3월 28일)에 한계값과 같아져 감지가 풀린다.
	lines := join(usualHalf(), xs(6, 6, 6, 2, 6, 6, 6, 6, 6, 6, 0, 0, 0, 0))

	t.Run("기본값에서는 8에서 멈추고 나아진 지 나흘째에 풀린다", func(t *testing.T) {
		got := run(t, diary(t, "2026-03-01", lines...), "2026-03-28", params.Default())
		assertSeries(t, []wantPoint{
			{"2026-03-15", 6, 2, false},
			{"2026-03-16", 6, 4, false},
			{"2026-03-17", 6, 6, true},
			{"2026-03-18", 2, 7, true},
			{"2026-03-19", 6, 8, true},
			{"2026-03-20", 6, 8, true},
			{"2026-03-21", 6, 8, true},
			{"2026-03-22", 6, 8, true},
			{"2026-03-23", 6, 8, true},
			{"2026-03-24", 6, 8, true},
			{"2026-03-25", 0, 7, true},
			{"2026-03-26", 0, 6, true},
			{"2026-03-27", 0, 5, true},
			{"2026-03-28", 0, 4, false},
		}, got.Series)

		assert.False(t, got.Series[3].AtCeiling, "3월 18일의 7은 천장 아래다")
		assert.True(t, got.Series[4].AtCeiling, "3월 19일은 9가 8로 잘렸다")
		assert.InDelta(t, 2.0, got.Series[4].Step, 0, "잘린 날에도 그날 더하려던 값은 그대로 적는다")
		assert.True(t, got.Series[9].AtCeiling)
		assert.False(t, got.Series[10].AtCeiling)
	})

	t.Run("천장을 끄면 19까지 쌓여서 나아진 지 나흘이 지나도 감지가 켜져 있다", func(t *testing.T) {
		// 2, 4, 6, 7, 9, 11, 13, 15, 17, 19 → 18, 17, 16, 15.
		p := params.Default()
		p.CUSUM.MaxS = 0
		got := run(t, diary(t, "2026-03-01", lines...), "2026-03-28", p)

		require.Len(t, got.Series, 14)
		assert.InDelta(t, 19.0, got.Series[9].S, 0)
		assert.InDelta(t, 15.0, got.State.S, 0)
		assert.True(t, got.State.Detected)
	})

	t.Run("천장을 한계값의 1.5배로 낮추면 6에서 멈춘다", func(t *testing.T) {
		p := params.Default()
		p.CUSUM.MaxS = 1.5
		got := run(t, diary(t, "2026-03-01", lines...), "2026-03-19", p)

		assertSeries(t, []wantPoint{
			{"2026-03-15", 6, 2, false},
			{"2026-03-16", 6, 4, false},
			{"2026-03-17", 6, 6, true},
			{"2026-03-18", 2, 6, true},
			{"2026-03-19", 6, 6, true},
		}, got.Series)
		assert.False(t, got.Series[2].AtCeiling, "3월 17일은 딱 6이라 잘린 것이 없다")
		assert.True(t, got.Series[3].AtCeiling)
	})
}

// 기준선이 잡히기 전에는 돌리지 않고, 기준선 기간의 날은 아무리 나빴어도 쌓지 않는다.
func TestOracleStartsAfterBaselinePeriod(t *testing.T) {
	// 3월 10일 하루만 일곱 항목이 관찰됐고 나머지 13일은 0이다. 평소 = 7 ÷ 14 = 0.5.
	// 3월 15일 x=1 → 1 − 0.5 − 0.5 = 0 → S = 0.
	lines := join(xs(0, 0, 0, 0, 0, 0, 0, 0, 0, 7, 0, 0, 0, 0), xs(1))
	days := diary(t, "2026-03-01", lines...)

	t.Run("기간의 마지막 날인 3월 14일에는 아직 돌지 않는다", func(t *testing.T) {
		got := run(t, days, "2026-03-14", params.Default())
		assert.Empty(t, got.Series)
		assert.False(t, got.State.Running)
		assert.False(t, got.State.Detected)
		assert.InDelta(t, 0.0, got.State.S, 0)
	})

	t.Run("3월 15일이 첫 누적이고 3월 10일의 일곱 항목은 쌓이지 않았다", func(t *testing.T) {
		got := run(t, days, "2026-03-15", params.Default())
		assert.Equal(t, date(t, "2026-03-15"), got.From)
		assertSeries(t, []wantPoint{{"2026-03-15", 1, 0, false}}, got.Series)
		assert.True(t, got.State.Running)
		assert.False(t, got.StateAt(date(t, "2026-03-14")).Running)
	})
}

// 기간을 늘린 기준선에서는 일곱 번째 대화 날의 다음 날부터 쌓는다.
func TestOracleStartsAfterExtendedBaseline(t *testing.T) {
	// 사흘에 한 번 대화: 3월 1, 4, 7, 10, 13일(각 1개), 16일(8개), 19일(1개). 14일 안에 5일뿐이라 3월 19일까지가 기간이다.
	// 평소 = (5 + 8 + 1) ÷ 7 = 2. 3월 16일의 여덟 항목은 기간 안이라 쌓이지 않는다.
	//   3월 20일 x=4 → 4 − 2 − 0.5 = +1.5 → 1.5
	//   3월 22일 x=7 → +4.5가 2로 묶여 3.5
	//   3월 23일 x=7 → 5.5 → 감지
	lines := xs(1, -1, -1, 1, -1, -1, 1, -1, -1, 1, -1, -1, 1, -1, -1, 8, -1, -1, 1, 4, -1, 7, 7)
	days := diary(t, "2026-03-01", lines...)

	t.Run("일곱 번째 대화 날인 3월 19일에는 돌지 않는다", func(t *testing.T) {
		got := run(t, days, "2026-03-19", params.Default())
		assert.Empty(t, got.Series)
		assert.False(t, got.State.Running)
	})

	t.Run("3월 20일부터 쌓는다", func(t *testing.T) {
		got := run(t, days, "2026-03-23", params.Default())
		assert.Equal(t, date(t, "2026-03-20"), got.From)
		assertSeries(t, []wantPoint{
			{"2026-03-20", 4, 1.5, false},
			{"2026-03-22", 7, 3.5, false},
			{"2026-03-23", 7, 5.5, true},
		}, got.Series)
	})
}

// 여유와 한계값은 조정 값이다.
func TestOracleCustomAllowanceAndLimit(t *testing.T) {
	t.Run("여유 0, 한계값 1: 첫날 1은 같아서 아직이고 둘째 날 2에 감지한다", func(t *testing.T) {
		// 평소 1, x=2 → +1씩.
		p := params.Default()
		p.CUSUM.K, p.CUSUM.H = 0, 1
		got := run(t, diary(t, "2026-03-01", join(usualOne(), xs(2, 2))...), "2026-03-16", p)
		assertSeries(t, []wantPoint{
			{"2026-03-15", 2, 1, false},
			{"2026-03-16", 2, 2, true},
		}, got.Series)
	})

	t.Run("여유 1이면 평소보다 하나 많은 날은 쌓이지 않는다", func(t *testing.T) {
		// 평소 1, x=2 → 2 − 1 − 1 = 0.
		p := params.Default()
		p.CUSUM.K = 1
		got := run(t, diary(t, "2026-03-01", join(usualOne(), xs(2, 2, 2))...), "2026-03-17", p)
		assertSeries(t, []wantPoint{
			{"2026-03-15", 2, 0, false},
			{"2026-03-16", 2, 0, false},
			{"2026-03-17", 2, 0, false},
		}, got.Series)
	})
}

// 평소의 하루 평균이 이진 소수로 떨어지지 않을 때도 "한계값과 딱 같으면 아직 아니다"가 지켜져야 한다.
// 기대값은 분수로 정확하게 셈한 것이다. 소수 계산의 오차로 4가 4.000000000000001이 되어 감지로 뒤집히면 안 된다.
func TestOracleExactlyAtThresholdWithFractionalMean(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		// atLimit은 누적값이 정확히 4가 되는 날, next는 그다음 대화 날이다.
		atLimit string
		next    string
	}{
		{
			// 3월 1일~14일 날마다 대화, 관찰된 수의 합 3 → 평소 3/14. 하루의 증가량은 x − 3/14 − 1/2 = x − 5/7.
			// 그 뒤 x = 2, 1, 1, 1, 1, 1, 2:
			//   9/7, 11/7, 13/7, 15/7, 17/7, 19/7, 28/7 = 4 (3월 21일). 0에서 멈춘 날은 없다.
			// 3월 22일 x = 1 → 4 + 2/7 → 감지.
			name:    "평소 14분의 3",
			lines:   join(xs(1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0), xs(2, 1, 1, 1, 1, 1, 2, 1)),
			atLimit: "2026-03-21", next: "2026-03-22",
		},
		{
			// 3월 1, 3, …, 13일 이레 대화, 합 2 → 평소 2/7. 증가량은 x − 2/7 − 1/2 = x − 11/14.
			// 그 뒤 x = 1이 13일(+3/14씩 39/14), 이어서 x = 2 하루(+17/14) → 56/14 = 4 (3월 28일).
			// 3월 29일 x = 1 → 4 + 3/14 → 감지.
			name: "평소 7분의 2",
			lines: join(xs(1, -1, 1, -1, 0, -1, 0, -1, 0, -1, 0, -1, 0, -1),
				xs(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 1)),
			atLimit: "2026-03-28", next: "2026-03-29",
		},
		{
			// 3월 1, 3, …, 13일 이레 대화, 합 9 → 평소 9/7. 증가량은 x − 9/7 − 1/2 = x − 25/14.
			// 그 뒤 x = 3 하루(+17/14), 이어서 x = 2가 13일(+3/14씩 39/14) → 56/14 = 4 (3월 28일).
			// 3월 29일 x = 2 → 4 + 3/14 → 감지.
			name: "평소 7분의 9",
			lines: join(xs(2, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 2, -1),
				xs(3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2)),
			atLimit: "2026-03-28", next: "2026-03-29",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := diary(t, "2026-03-01", tt.lines...)
			got := run(t, days, tt.next, params.Default())

			limit := got.StateAt(date(t, tt.atLimit))
			assert.InDelta(t, 4.0, limit.S, 1e-9)
			assert.False(t, limit.Detected, "%s의 누적값은 정확히 4라서 아직 감지가 아니다 (구한 값 %v)", tt.atLimit, limit.S)

			after := got.StateAt(date(t, tt.next))
			assert.True(t, after.Detected, tt.next)
		})
	}
}
