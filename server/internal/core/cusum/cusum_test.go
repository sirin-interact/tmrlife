package cusum

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// skip은 대화하지 않은 날이다.
const skip = -1

func mustDate(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

// dayWith는 앞 항목부터 observed개가 관찰된 하루를 만든다.
func dayWith(date recorddate.Date, observed int) signal.Day {
	day := signal.Day{Date: date}
	for i := range observed {
		day.Judgements[i] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
	}
	return day
}

// after는 기준선 기간의 마지막 날 다음 날부터 하루에 하나씩 관찰된 항목 수를 놓는다. skip은 대화하지 않은 날이다.
func after(end recorddate.Date, observed ...int) []signal.Day {
	days := make([]signal.Day, 0, len(observed))
	for i, x := range observed {
		if x == skip {
			continue
		}
		days = append(days, dayWith(end.AddDays(i+1), x))
	}
	return days
}

// established는 end에서 기간이 끝났고 평소의 하루 평균이 mu인, 이미 잡힌 기준선이다.
// 누적 식을 손으로 따라가기 쉽게 평균을 직접 적는다.
//
// 누적은 소수인 평균이 아니라 "관찰된 항목 수의 합 ÷ 대화한 일수"로 하므로, 그 평균이 나오는 가장 작은 일수와 합을 찾아 채운다.
// 1.0은 1일에 1개, 1.25는 4일에 5개, 9/7은 7일에 9개다. 28일 안에서 찾지 못하면 합을 비워 두는데,
// 그러면 평균과 합이 어긋난 기준선이 되어 Run이 ErrInvalidBaseline으로 알려준다.
func established(end, asOf recorddate.Date, mu float64) baseline.Baseline {
	base := baseline.Baseline{
		AsOf:        asOf,
		Established: true,
		Start:       end.AddDays(-13),
		End:         end,
		Days:        14,
		Mu:          mu,
	}
	for days := 1; days <= 28; days++ {
		total := math.Round(mu * float64(days))
		if total/float64(days) == mu {
			base.Days, base.ObservedTotal = days, int(total)
			break
		}
	}
	return base
}

// withCUSUM은 변화 탐지의 조정 값만 바꾼 Params를 만든다. 나머지는 기본값이다.
func withCUSUM(c params.CUSUM) params.Params {
	p := params.Default()
	p.CUSUM = c
	return p
}

// asWritten은 하루 증가량의 상한과 누적값의 천장을 모두 끈 값이다. 누적 식 그대로를 볼 때 쓴다.
func asWritten() params.Params {
	return withCUSUM(params.CUSUM{K: 0.5, H: 4.0})
}

// values는 흐름에서 누적값만 뽑는다.
func values(series []Point) []float64 {
	out := make([]float64, 0, len(series))
	for _, pt := range series {
		out = append(out, pt.S)
	}
	return out
}

// detections는 흐름에서 감지 여부만 뽑는다.
func detections(series []Point) []bool {
	out := make([]bool, 0, len(series))
	for _, pt := range series {
		out = append(out, pt.Detected)
	}
	return out
}

// firstDetected는 처음으로 변화 감지가 된 날이 흐름의 몇 번째(1부터)인지다. 없으면 0이다.
func firstDetected(series []Point) int {
	for i, pt := range series {
		if pt.Detected {
			return i + 1
		}
	}
	return 0
}

func repeat(x, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = x
	}
	return out
}

func TestRunRecurrence(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")

	t.Run("상한과 천장을 끄고 평소 1.0, 여유 0.5, 한계 4.0으로 손으로 따라간 흐름", func(t *testing.T) {
		days := after(end, 3, 0, 2, 4, 1, 0, 0, 5, 3)

		got, err := Run(days, established(end, asOf, 1.0), asWritten())
		require.NoError(t, err)

		date := func(n int) recorddate.Date { return end.AddDays(n) }
		want := []Point{
			{Date: date(1), Observed: 3, Step: 1.5, S: 1.5},
			// 1.5 − 1.5 = 0
			{Date: date(2), Observed: 0, Step: -1.5, S: 0},
			{Date: date(3), Observed: 2, Step: 0.5, S: 0.5},
			{Date: date(4), Observed: 4, Step: 2.5, S: 3.0},
			// 평소와 같은 날에도 여유만큼 줄어든다.
			{Date: date(5), Observed: 1, Step: -0.5, S: 2.5},
			{Date: date(6), Observed: 0, Step: -1.5, S: 1.0},
			// 1.0 − 1.5 = −0.5지만 0 아래로는 내려가지 않는다.
			{Date: date(7), Observed: 0, Step: -1.5, S: 0},
			{Date: date(8), Observed: 5, Step: 3.5, S: 3.5},
			{Date: date(9), Observed: 3, Step: 1.5, S: 5.0, Detected: true},
		}
		assert.Equal(t, want, got.Series)
		assert.Equal(t, State{Running: true, S: 5.0, Detected: true}, got.State)
		assert.Equal(t, date(1), got.From)
		assert.Equal(t, asOf, got.AsOf)
	})

	t.Run("기본값으로 같은 기록을 따라가면 하루에 2까지만 는다", func(t *testing.T) {
		// 위와 같은 아흐레에 세 항목인 날이 하루 더 붙었다. 넷과 다섯이 관찰된 날의 2.5와 3.5가 2로 묶인다.
		days := after(end, 3, 0, 2, 4, 1, 0, 0, 5, 3, 3)

		got, err := Run(days, established(end, asOf, 1.0), params.Default())
		require.NoError(t, err)

		date := func(n int) recorddate.Date { return end.AddDays(n) }
		want := []Point{
			{Date: date(1), Observed: 3, Step: 1.5, S: 1.5},
			{Date: date(2), Observed: 0, Step: -1.5, S: 0},
			{Date: date(3), Observed: 2, Step: 0.5, S: 0.5},
			// 4 − 1 − 0.5 = 2.5지만 하루에 2까지만 쌓는다.
			{Date: date(4), Observed: 4, Step: 2.0, Capped: true, S: 2.5},
			{Date: date(5), Observed: 1, Step: -0.5, S: 2.0},
			{Date: date(6), Observed: 0, Step: -1.5, S: 0.5},
			{Date: date(7), Observed: 0, Step: -1.5, S: 0},
			{Date: date(8), Observed: 5, Step: 2.0, Capped: true, S: 2.0},
			{Date: date(9), Observed: 3, Step: 1.5, S: 3.5},
			{Date: date(10), Observed: 3, Step: 1.5, S: 5.0, Detected: true},
		}
		assert.Equal(t, want, got.Series)
		assert.Equal(t, State{Running: true, S: 5.0, Detected: true}, got.State)
	})

	t.Run("좋은 날이 이어져도 누적값은 0 아래로 쌓이지 않는다", func(t *testing.T) {
		// 0 아래로 쌓아 두면 그 뒤의 나쁜 날들이 한동안 가려진다.
		days := after(end, 0, 0, 0, 0, 0, 0, 4, 4, 4)

		got, err := Run(days, established(end, asOf, 1.0), params.Default())
		require.NoError(t, err)

		// 4 − 1 − 0.5 = 2.5는 2로 묶인다. 사흘째에 한계값을 넘는다.
		assert.Equal(t, []float64{0, 0, 0, 0, 0, 0, 2, 4, 6}, values(got.Series))
		assert.True(t, got.State.Detected)
	})

	t.Run("평소가 높은 사람은 같은 날도 덜 쌓인다", func(t *testing.T) {
		days := after(end, 4, 4, 4)

		low, err := Run(days, established(end, asOf, 1.0), params.Default())
		require.NoError(t, err)
		high, err := Run(days, established(end, asOf, 3.5), params.Default())
		require.NoError(t, err)

		// 4 − 1 − 0.5 = 2.5는 하루 상한 2로 묶인다.
		assert.Equal(t, []float64{2, 4, 6}, values(low.Series))
		// 4 − 3.5 − 0.5 = 0
		assert.Equal(t, []float64{0, 0, 0}, values(high.Series))
	})

	t.Run("여유를 0으로 두면 평소와 같은 날에는 그대로다", func(t *testing.T) {
		days := after(end, 2, 1, 1, 0)

		got, err := Run(days, established(end, asOf, 1.0), withCUSUM(params.CUSUM{K: 0, H: 4}))
		require.NoError(t, err)

		assert.Equal(t, []float64{1, 1, 1, 0}, values(got.Series))
	})

	t.Run("딱 떨어지지 않는 평균으로도 식대로 쌓인다", func(t *testing.T) {
		// 7일 동안 모두 9개가 관찰된 사람이다. 평소는 9/7이다.
		first := mustDate(t, "2026-09-01")
		days := []signal.Day{
			dayWith(first, 3), dayWith(first.AddDays(1), 1), dayWith(first.AddDays(2), 0),
			dayWith(first.AddDays(3), 0), dayWith(first.AddDays(4), 2), dayWith(first.AddDays(5), 1),
			dayWith(first.AddDays(6), 2),
		}
		days = append(days, after(end, 3, 3, 3, 3)...)

		base, err := baseline.Compute(days, asOf, params.Default())
		require.NoError(t, err)
		require.True(t, base.Established)
		require.Equal(t, end, base.End)

		got, err := Run(days, base, params.Default())
		require.NoError(t, err)

		step := 3 - 9.0/7.0 - 0.5
		require.Len(t, got.Series, 4)
		for i, pt := range got.Series {
			assert.InDelta(t, step, pt.Step, 1e-12)
			assert.InDelta(t, step*float64(i+1), pt.S, 1e-12)
		}
		// 사흘째 누적값은 약 3.64, 나흘째는 약 4.86이라 나흘째에 한계값을 넘는다.
		assert.Equal(t, 4, firstDetected(got.Series))
	})
}

func TestRunSingleVeryBadDay(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")
	base := established(end, asOf, 1.0)
	// 평소와 같은 날들 사이에 항목 여섯 개가 관찰된 하루가 끼어 있다.
	days := after(end, 1, 1, 6, 1, 1, 1, 1, 1)

	t.Run("상한이 없으면 그 하루로 한계값을 넘는다", func(t *testing.T) {
		got, err := Run(days, base, withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 0}))
		require.NoError(t, err)

		// 6 − 1 − 0.5 = 4.5
		assert.Equal(t, []float64{0, 0, 4.5, 4.0, 3.5, 3.0, 2.5, 2.0}, values(got.Series))
		bad := got.Series[2]
		assert.InDelta(t, 4.5, bad.Step, 0)
		assert.False(t, bad.Capped)
		assert.True(t, bad.Detected)
		// 다음 날 4.0으로 내려오면 한계값과 같아져서 바로 풀린다.
		assert.False(t, got.Series[3].Detected)
	})

	t.Run("기본값에서는 그 하루로 넘지 않는다", func(t *testing.T) {
		got, err := Run(days, base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{0, 0, 2.0, 1.5, 1.0, 0.5, 0, 0}, values(got.Series))
		assert.True(t, got.Series[2].Capped)
		assert.Zero(t, firstDetected(got.Series))
	})

	t.Run("평소가 0.3인 사람에게 여섯 항목이 관찰된 하루도 기본값에서는 울리지 않는다", func(t *testing.T) {
		// 상한이 없으면 6 − 0.3 − 0.5 = 5.2가 한 번에 쌓인다. 그 뒤로 아무것도 관찰되지 않아도 하루에 0.8씩만 줄어서
		// 그날과 다음 날(5.2, 4.4) 감지가 켜져 있다가 3.6이 되는 날에야 풀린다.
		quiet := established(end, asOf, 0.3)
		spike := after(end, 0, 6, 0, 0, 0)

		written, err := Run(spike, quiet, asWritten())
		require.NoError(t, err)
		assert.Equal(t, []bool{false, true, true, false, false}, detections(written.Series))

		got, err := Run(spike, quiet, params.Default())
		require.NoError(t, err)
		assert.Equal(t, []bool{false, false, false, false, false}, detections(got.Series))
		assert.InDelta(t, 2.0, got.Series[1].S, 0)
	})

	t.Run("하루 증가량을 2로 묶으면 그 하루로는 넘지 않는다", func(t *testing.T) {
		got, err := Run(days, base, withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}))
		require.NoError(t, err)

		assert.Equal(t, []float64{0, 0, 2.0, 1.5, 1.0, 0.5, 0, 0}, values(got.Series))
		bad := got.Series[2]
		assert.InDelta(t, 2.0, bad.Step, 0)
		assert.True(t, bad.Capped)
		assert.Equal(t, 6, bad.Observed)
		assert.Zero(t, firstDetected(got.Series))
	})

	t.Run("상한이 있어도 크게 나쁜 날이 사흘 이어지면 넘는다", func(t *testing.T) {
		got, err := Run(after(end, 6, 6, 6), base, withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}))
		require.NoError(t, err)

		// 이틀째의 4.0은 한계값과 같아서 아직 아니다.
		assert.Equal(t, []float64{2, 4, 6}, values(got.Series))
		assert.Equal(t, 3, firstDetected(got.Series))
	})

	t.Run("상한 없이 한계값을 7로 올려도 그 하루에는 울리지 않지만 서서히 나빠지는 흐름을 늦게 잡는다", func(t *testing.T) {
		raised := withCUSUM(params.CUSUM{K: 0.5, H: 7.0, MaxStep: 0})

		spike, err := Run(days, base, raised)
		require.NoError(t, err)
		assert.Zero(t, firstDetected(spike.Series))

		// 평소보다 하나씩 많은 날이 이어지면 하루에 0.5씩 쌓여 15일째에야 7을 넘는다.
		slow, err := Run(after(end, repeat(2, 20)...), base, raised)
		require.NoError(t, err)
		assert.Equal(t, 15, firstDetected(slow.Series))
	})

	t.Run("상한과 같은 증가량은 묶인 것이 아니다", func(t *testing.T) {
		got, err := Run(after(end, 3), established(end, asOf, 0.5), withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}))
		require.NoError(t, err)

		assert.Equal(t, []Point{{Date: end.AddDays(1), Observed: 3, Step: 2.0, S: 2.0}}, got.Series)
	})

	t.Run("줄어드는 쪽은 묶지 않는다", func(t *testing.T) {
		got, err := Run(after(end, 8, 8, 8, 8, 0), established(end, asOf, 5.0), withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}))
		require.NoError(t, err)

		// 8 − 5 − 0.5 = 2.5는 2로 묶이고, 0 − 5 − 0.5 = −5.5는 그대로 빠진다.
		assert.Equal(t, []float64{2, 4, 6, 8, 2.5}, values(got.Series))
		last := got.Series[4]
		assert.InDelta(t, -5.5, last.Step, 0)
		assert.False(t, last.Capped)
	})
}

func TestRunSlowSteadyWorsening(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")
	base := established(end, asOf, 1.0)

	t.Run("평소보다 하나씩 많은 날이 이어지면 9일째 대화 날에 감지된다", func(t *testing.T) {
		got, err := Run(after(end, repeat(2, 12)...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t,
			[]float64{0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 3.5, 4.0, 4.5, 5.0, 5.5, 6.0},
			values(got.Series))
		assert.Equal(t, 9, firstDetected(got.Series))
		assert.Equal(t, mustDate(t, "2026-09-23"), got.Series[8].Date)
		// 8일째의 4.0은 한계값과 같아서 아직 아니다.
		assert.False(t, got.Series[7].Detected)
	})

	t.Run("하루 증가량을 2로 묶어도 서서히 나빠지는 흐름은 같은 날에 감지된다", func(t *testing.T) {
		got, err := Run(after(end, repeat(2, 12)...), base, withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}))
		require.NoError(t, err)

		assert.Equal(t, 9, firstDetected(got.Series))
		for _, pt := range got.Series {
			assert.False(t, pt.Capped)
		}
	})

	t.Run("이틀에 한 번 대화하면 9번째 대화 날은 17일 뒤다", func(t *testing.T) {
		pattern := make([]int, 0, 24)
		for range 12 {
			pattern = append(pattern, 2, skip)
		}

		got, err := Run(after(end, pattern...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, 9, firstDetected(got.Series))
		assert.Equal(t, end.AddDays(17), got.Series[8].Date)
	})

	t.Run("평소보다 둘씩 많은 날이 이어지면 3일째에 감지된다", func(t *testing.T) {
		got, err := Run(after(end, repeat(3, 5)...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{1.5, 3.0, 4.5, 6.0, 7.5}, values(got.Series))
		assert.Equal(t, 3, firstDetected(got.Series))
	})

	t.Run("평소와 같은 날만 이어지면 쌓이지 않는다", func(t *testing.T) {
		got, err := Run(after(end, repeat(1, 30)...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, make([]float64, 30), values(got.Series))
		assert.False(t, got.State.Detected)
	})
}

func TestRunRecovery(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")
	base := established(end, asOf, 1.0)

	// 네 항목인 날은 4 − 1 − 0.5 = 2.5가 2로 묶여 쌓이고, 한 항목인 날은 0.5, 없는 날은 1.5가 빠진다.
	days := after(end, 4, 4, 4, 4, 0, 0, 1, 1, 0, 4, 4)
	got, err := Run(days, base, params.Default())
	require.NoError(t, err)

	tests := []struct {
		name         string
		n            int
		wantS        float64
		wantDetected bool
	}{
		{"이틀째의 4.0은 한계값과 같아서 아직 아니다", 2, 4.0, false},
		{"사흘째에 한계값을 넘는다", 3, 6.0, true},
		{"넘은 뒤에도 0으로 되돌리지 않고 계속 쌓는다", 4, 8.0, true},
		{"나아진 첫날에는 줄었지만 아직 한계값 위다", 5, 6.5, true},
		{"이틀째에도 아직 위다", 6, 5.0, true},
		{"사흘째에도 아직 위다", 7, 4.5, true},
		{"한계값과 같아지는 날 감지가 풀린다", 8, 4.0, false},
		{"그 뒤로는 계속 줄어든다", 9, 2.5, false},
		{"다시 나빠지면 남아 있던 값 위에 쌓여 또 감지된다", 10, 4.5, true},
		{"이어지면 감지 상태도 이어진다", 11, 6.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := got.Series[tt.n-1]
			assert.InDelta(t, tt.wantS, pt.S, 0)
			assert.Equal(t, tt.wantDetected, pt.Detected)
		})
	}

	t.Run("기준일의 상태는 마지막 날의 상태다", func(t *testing.T) {
		assert.Equal(t, State{Running: true, S: 6.5, Detected: true}, got.State)
	})

	t.Run("기준일이 나아진 뒤라면 감지가 풀린 상태다", func(t *testing.T) {
		recovered, err := Run(days, established(end, end.AddDays(9), 1.0), params.Default())
		require.NoError(t, err)
		assert.Equal(t, State{Running: true, S: 2.5, Detected: false}, recovered.State)
	})
}

// 누적값은 한계값의 정해진 배수(기본 2배, 곧 8)를 넘지 못한다.
// 천장이 없으면 힘든 기간이 길수록 회복한 뒤에도 그만큼 오래 감지가 켜져 있다.
func TestRunCeiling(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-12-31")
	base := established(end, asOf, 1.0)

	t.Run("더한 뒤에 천장을 넘으면 천장에 맞추고, 걸렸다고 표시한다", func(t *testing.T) {
		// 세 항목인 날은 하루에 1.5씩 쌓인다: 1.5, 3, 4.5, 6, 7.5, 그다음 9가 8로 잘린다.
		got, err := Run(after(end, repeat(3, 8)...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{1.5, 3, 4.5, 6, 7.5, 8, 8, 8}, values(got.Series))
		for i, pt := range got.Series {
			assert.Equal(t, i >= 5, pt.AtCeiling, "%d번째 날", i+1)
			assert.InDelta(t, 1.5, pt.Step, 0, "천장에 걸린 날에도 Step은 잘리기 전의 값이다")
			assert.False(t, pt.Capped)
		}
	})

	t.Run("천장과 딱 같아진 날은 잘린 것이 없으므로 걸린 것이 아니다", func(t *testing.T) {
		// 네 항목인 날은 2씩(상한) 쌓여 나흘째에 정확히 8이 된다. 닷새째에 비로소 잘린다.
		got, err := Run(after(end, repeat(4, 5)...), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{2, 4, 6, 8, 8}, values(got.Series))
		assert.False(t, got.Series[3].AtCeiling)
		assert.True(t, got.Series[4].AtCeiling)
	})

	t.Run("힘든 3주 뒤에 회복하면 대화 몇 번 만에 감지가 풀린다", func(t *testing.T) {
		hard := repeat(6, 21)
		// 회복한 뒤에는 평소와 같은 날(한 항목)이 이어진다. 하루에 0.5씩 줄어든다.
		pattern := append(hard, repeat(1, 8)...)

		got, err := Run(after(end, pattern...), base, params.Default())
		require.NoError(t, err)

		assert.InDelta(t, 8.0, got.Series[20].S, 0, "3주 동안 쌓여도 8이다")
		// 8에서 0.5씩: 7.5, 7, 6.5, 6, 5.5, 5, 4.5, 4.0. 여덟 번째 대화 날에 한계값과 같아져 풀린다.
		assert.Equal(t, []bool{true, true, true, true, true, true, true, false}, detections(got.Series[21:]))

		// 아무 항목도 관찰되지 않는 날이 이어지면 1.5씩 줄어 세 번째 대화 날에 풀린다: 6.5, 5, 3.5.
		better, err := Run(after(end, append(repeat(6, 21), repeat(0, 3)...)...), base, params.Default())
		require.NoError(t, err)
		assert.Equal(t, []bool{true, true, false}, detections(better.Series[21:]))
	})

	t.Run("천장을 끄면 같은 3주가 42까지 쌓여 회복한 뒤에도 오래 켜져 있다", func(t *testing.T) {
		noCeiling := withCUSUM(params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2})
		pattern := append(repeat(6, 21), repeat(1, 8)...)

		got, err := Run(after(end, pattern...), base, noCeiling)
		require.NoError(t, err)

		assert.InDelta(t, 42.0, got.Series[20].S, 0)
		assert.InDelta(t, 38.0, got.State.S, 0)
		assert.True(t, got.State.Detected)
		for _, pt := range got.Series {
			assert.False(t, pt.AtCeiling)
		}
	})

	t.Run("천장은 한계값의 배수라서 한계값을 바꾸면 함께 움직인다", func(t *testing.T) {
		// 한계값 2.5의 1.5배는 3.75다. 0.1처럼 이진 소수로 떨어지지 않는 값이어도 적은 십진수 그대로 곱한다.
		got, err := Run(after(end, repeat(4, 3)...), base, withCUSUM(params.CUSUM{K: 0.5, H: 2.5, MaxStep: 2, MaxS: 1.5}))
		require.NoError(t, err)

		assert.Equal(t, []float64{2, 3.75, 3.75}, values(got.Series))
		assert.Equal(t, []bool{false, true, true}, detections(got.Series))
		assert.True(t, got.Series[1].AtCeiling)
	})

	t.Run("천장에 머물던 누적값도 줄어드는 쪽은 그대로 줄어든다", func(t *testing.T) {
		got, err := Run(after(end, 4, 4, 4, 4, 4, 0), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{2, 4, 6, 8, 8, 6.5}, values(got.Series))
		assert.False(t, got.Series[5].AtCeiling)
	})
}

func TestRunSkipsDaysWithoutConversation(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-12-31")
	base := established(end, asOf, 1.0)

	t.Run("대화하지 않은 날은 흐름에 없고 누적값은 그대로 이어진다", func(t *testing.T) {
		got, err := Run(after(end, 4, skip, skip, 4, 4, skip, 0, 0), base, params.Default())
		require.NoError(t, err)

		// 네 항목인 날의 2.5는 하루 상한 2로 묶인다.
		want := []Point{
			{Date: end.AddDays(1), Observed: 4, Step: 2.0, Capped: true, S: 2.0},
			{Date: end.AddDays(4), Observed: 4, Step: 2.0, Capped: true, S: 4.0},
			{Date: end.AddDays(5), Observed: 4, Step: 2.0, Capped: true, S: 6.0, Detected: true},
			{Date: end.AddDays(7), Observed: 0, Step: -1.5, S: 4.5, Detected: true},
			{Date: end.AddDays(8), Observed: 0, Step: -1.5, S: 3.0},
		}
		assert.Equal(t, want, got.Series)

		assert.Equal(t, State{Running: true, S: 2.0}, got.StateAt(end.AddDays(2)), "쉬는 동안")
		assert.Equal(t, State{Running: true, S: 2.0}, got.StateAt(end.AddDays(3)), "쉬는 동안")
		assert.Equal(t, State{Running: true, S: 6.0, Detected: true}, got.StateAt(end.AddDays(6)), "감지된 채로 쉰 날")
	})

	t.Run("사이에 쉰 날이 있든 없든 누적값은 같다", func(t *testing.T) {
		withGaps, err := Run(after(end, 3, skip, 2, skip, skip, skip, 4, 0, skip, 5), base, params.Default())
		require.NoError(t, err)
		withoutGaps, err := Run(after(end, 3, 2, 4, 0, 5), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, values(withoutGaps.Series), values(withGaps.Series))
	})

	t.Run("감지된 채로 오래 쉬어도 저절로 풀리지 않는다", func(t *testing.T) {
		got, err := Run(after(end, 4, 4, 4), base, params.Default())
		require.NoError(t, err)

		require.Len(t, got.Series, 3)
		assert.Equal(t, State{Running: true, S: 6.0, Detected: true}, got.State)
		assert.Equal(t, got.State, got.StateAt(end.AddDays(60)))
	})

	t.Run("여덟 항목이 모두 언급 없음인 날은 대화한 날이다. 관찰 0개로 쌓는다", func(t *testing.T) {
		got, err := Run(after(end, 4, 0), base, params.Default())
		require.NoError(t, err)

		assert.Equal(t, []float64{2.0, 0.5}, values(got.Series))
	})
}

func TestRunThresholdIsStrict(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")

	tests := []struct {
		name         string
		mu           float64
		p            params.CUSUM
		observed     []int
		wantS        float64
		wantDetected bool
	}{
		{
			name: "누적값이 한계값과 같으면 아직 아니다",
			mu:   0, p: params.CUSUM{K: 0.5, H: 4.0},
			observed: repeat(1, 8), wantS: 4.0, wantDetected: false,
		},
		{
			name: "한 번 더 쌓여 한계값을 넘으면 감지다",
			mu:   0, p: params.CUSUM{K: 0.5, H: 4.0},
			observed: repeat(1, 9), wantS: 4.5, wantDetected: true,
		},
		{
			name: "하루 만에 한계값과 같아져도 아니다",
			mu:   0.5, p: params.CUSUM{K: 0.5, H: 4.0},
			observed: []int{5}, wantS: 4.0, wantDetected: false,
		},
		{
			name: "한계값이 아주 조금만 낮아도 같은 누적값이 감지다",
			mu:   0.5, p: params.CUSUM{K: 0.5, H: 3.999},
			observed: []int{5}, wantS: 4.0, wantDetected: true,
		},
		{
			name: "한계값 바로 아래는 아니다",
			mu:   0.5, p: params.CUSUM{K: 0.5, H: 4.0},
			observed: []int{4}, wantS: 3.0, wantDetected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(after(end, tt.observed...), established(end, asOf, tt.mu), withCUSUM(tt.p))
			require.NoError(t, err)

			assert.InDelta(t, tt.wantS, got.State.S, 0)
			assert.Equal(t, tt.wantDetected, got.State.Detected)
		})
	}
}

// 누적값이 한계값과 딱 같은 날은 감지가 아니다. 평소의 하루 평균이 이진 소수로 떨어지지 않아도,
// 조정 값이 0.3처럼 이진 소수로 떨어지지 않아도 그래야 한다. 소수로 쌓으면 4가 4.000000000000001이 되어
// 감지가 하루 일찍 켜지거나 하루 늦게 풀린다. 기대값은 모두 분수로 손으로 셈한 것이다.
func TestRunThresholdIsExact(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")

	tests := []struct {
		name     string
		mu       float64
		p        params.CUSUM
		observed []int
		// wantDetected는 대화한 날마다의 감지 여부다.
		wantDetected []bool
		// wantS는 마지막 날의 누적값이다.
		wantS float64
	}{
		{
			// 평소 11/6, 하루에 빠지는 값은 11/6 + 1/2 = 7/3. 3개, 4개, 4개면 2/3 + 5/3 + 5/3 = 4.
			name: "평소 6분의 11에서 사흘 만에 딱 4가 되면 아직 아니다",
			mu:   11.0 / 6.0, p: params.CUSUM{K: 0.5, H: 4},
			observed:     []int{3, 4, 4},
			wantDetected: []bool{false, false, false},
			wantS:        4,
		},
		{
			// 위의 기록에 3개인 날이 하루 더 붙으면 4 + 2/3이다.
			name: "그다음 날 조금이라도 더 쌓이면 감지다",
			mu:   11.0 / 6.0, p: params.CUSUM{K: 0.5, H: 4},
			observed:     []int{3, 4, 4, 3},
			wantDetected: []bool{false, false, false, true},
			wantS:        4 + 2.0/3.0,
		},
		{
			// 평소 3/10, 하루에 빠지는 값은 4/5. 하루 1개씩이면 날마다 1/5가 쌓여 스무 날째에 딱 4다.
			name: "평소 10분의 3에서 스무 날 만에 딱 4가 되면 아직 아니다",
			mu:   0.3, p: params.CUSUM{K: 0.5, H: 4},
			observed:     repeat(1, 21),
			wantDetected: append(make([]bool, 20), true),
			wantS:        4.2,
		},
		{
			// 평소 1/3, 하루에 빠지는 값은 5/6. 3, 3, 1, 1, 1개면 13/6, 26/6, 27/6, 28/6, 29/6이고
			// 하나도 관찰되지 않은 날 5/6가 빠져 24/6 = 4가 된다. 한계값과 같아졌으므로 감지가 풀린다.
			name: "감지된 뒤 누적값이 줄어 딱 한계값이 되면 풀린다",
			mu:   1.0 / 3.0, p: params.CUSUM{K: 0.5, H: 4},
			observed:     []int{3, 3, 1, 1, 1, 0},
			wantDetected: []bool{false, true, true, true, true, false},
			wantS:        4,
		},
		{
			// 허용 여유 0.3은 이진 소수로 딱 떨어지지 않지만 적은 사람의 뜻은 10분의 3이다.
			// 하루 1개씩이면 날마다 7/10이 쌓여 열흘째에 딱 7이고, 열하루째에 넘는다.
			name: "조정 값은 적은 십진수 그대로 읽는다",
			mu:   0, p: params.CUSUM{K: 0.3, H: 7},
			observed:     repeat(1, 11),
			wantDetected: append(make([]bool, 10), true),
			wantS:        7.7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(after(end, tt.observed...), established(end, asOf, tt.mu), withCUSUM(tt.p))
			require.NoError(t, err)
			require.Len(t, got.Series, len(tt.wantDetected))

			for i, pt := range got.Series {
				assert.Equal(t, tt.wantDetected[i], pt.Detected, "%d번째 대화 날 (누적값 %v)", i+1, pt.S)
			}
			assert.InDelta(t, tt.wantS, got.State.S, 1e-12)
			assert.Equal(t, tt.wantDetected[len(tt.wantDetected)-1], got.State.Detected)
		})
	}

	t.Run("상한과 딱 같은 날은 Capped가 거짓이고 넘는 날만 참이다", func(t *testing.T) {
		// 평소 1/4, 여유 1/2, 상한 1/4. 1개인 날의 증가량이 1 − 1/4 − 1/2 = 1/4로 딱 상한이다. 줄어든 것이 없으므로 걸린 것이 아니다.
		// 2개인 날은 5/4라서 상한에 걸려 1/4만 쌓인다.
		got, err := Run(after(end, 1, 2), established(end, asOf, 0.25), withCUSUM(params.CUSUM{K: 0.5, H: 4, MaxStep: 0.25}))
		require.NoError(t, err)
		require.Len(t, got.Series, 2)

		assert.False(t, got.Series[0].Capped)
		assert.InDelta(t, 0.25, got.Series[0].Step, 0)
		assert.True(t, got.Series[1].Capped)
		assert.InDelta(t, 0.25, got.Series[1].Step, 0)
		assert.InDelta(t, 0.5, got.State.S, 0)
	})
}

func TestRunWithoutEstablishedBaseline(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	p := params.Default()

	// 첫 14일 동안 7일 대화했고, 그 뒤로 날마다 여덟 항목이 모두 관찰됐다.
	days := []signal.Day{
		dayWith(first, 1), dayWith(first.AddDays(2), 1), dayWith(first.AddDays(4), 1),
		dayWith(first.AddDays(6), 1), dayWith(first.AddDays(8), 1), dayWith(first.AddDays(10), 1),
		dayWith(first.AddDays(12), 1),
	}
	end := first.AddDays(13)
	days = append(days, after(end, 8, 8, 8)...)

	run := func(t *testing.T, asOf recorddate.Date) Result {
		t.Helper()
		base, err := baseline.Compute(days, asOf, p)
		require.NoError(t, err)
		got, err := Run(days, base, p)
		require.NoError(t, err)
		return got
	}

	t.Run("기준선 기간의 마지막 날이 기준일이면 돌리지 않는다", func(t *testing.T) {
		got := run(t, end)

		assert.Equal(t, end, got.AsOf)
		assert.True(t, got.From.IsZero())
		assert.NotNil(t, got.Series)
		assert.Empty(t, got.Series)
		assert.Equal(t, State{}, got.State)
	})

	t.Run("마지막 날의 다음 날부터 돌고, 그날의 대화부터 쌓는다", func(t *testing.T) {
		got := run(t, end.AddDays(1))

		assert.Equal(t, end.AddDays(1), got.From)
		// 8 − 1 − 0.5 = 6.5지만 하루에 2까지만 쌓는다. 여덟 항목이 모두 관찰된 날이어도 하루로는 울리지 않는다.
		assert.Equal(t, []Point{{Date: end.AddDays(1), Observed: 8, Step: 2.0, Capped: true, S: 2.0}}, got.Series)
		assert.Equal(t, State{Running: true, S: 2.0}, got.State)
	})

	t.Run("대화한 날이 모자라 기준선이 잡히지 않았으면 아무리 나빠도 감지가 아니다", func(t *testing.T) {
		few := []signal.Day{dayWith(first, 8), dayWith(first.AddDays(20), 8), dayWith(first.AddDays(21), 8)}
		base, err := baseline.Compute(few, first.AddDays(30), p)
		require.NoError(t, err)
		require.False(t, base.Established)

		got, err := Run(few, base, p)
		require.NoError(t, err)

		assert.Empty(t, got.Series)
		assert.Equal(t, State{}, got.State)
		assert.Equal(t, State{}, got.StateAt(first.AddDays(25)))
	})

	t.Run("대화한 날이 하나도 없어도 오류가 아니다", func(t *testing.T) {
		base, err := baseline.Compute(nil, first, p)
		require.NoError(t, err)

		got, err := Run(nil, base, p)
		require.NoError(t, err)

		assert.Empty(t, got.Series)
		assert.Equal(t, State{}, got.State)
	})

	t.Run("기준선은 잡혔지만 그 뒤로 대화가 없으면 누적값 0으로 돌고 있는 상태다", func(t *testing.T) {
		got, err := Run(days[:7], established(end, end.AddDays(10), 1.0), p)
		require.NoError(t, err)

		assert.Equal(t, end.AddDays(1), got.From)
		assert.Empty(t, got.Series)
		assert.Equal(t, State{Running: true}, got.State)
	})
}

func TestRunStartsAfterBaselinePeriod(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	p := params.Default()

	t.Run("기준선 기간 안의 날은 아무리 나빠도 쌓지 않는다", func(t *testing.T) {
		// 14일째 날(기간의 마지막 날)에 여덟 항목이 모두 관찰됐다. 이날은 평소를 정하는 데 들어간다.
		days := []signal.Day{
			dayWith(first, 0), dayWith(first.AddDays(1), 0), dayWith(first.AddDays(2), 0),
			dayWith(first.AddDays(3), 0), dayWith(first.AddDays(4), 0), dayWith(first.AddDays(5), 0),
			dayWith(first.AddDays(12), 0), dayWith(first.AddDays(13), 8),
			dayWith(first.AddDays(14), 2),
		}
		base, err := baseline.Compute(days, first.AddDays(20), p)
		require.NoError(t, err)
		require.True(t, base.Established)
		require.InDelta(t, 1.0, base.Mu, 0)

		got, err := Run(days, base, p)
		require.NoError(t, err)

		assert.Equal(t, []Point{{Date: first.AddDays(14), Observed: 2, Step: 0.5, S: 0.5}}, got.Series)
	})

	t.Run("기간을 늘린 기준선은 일곱 번째 대화 날의 다음 날부터 쌓는다", func(t *testing.T) {
		days := []signal.Day{
			dayWith(first, 1), dayWith(first.AddDays(1), 1), dayWith(first.AddDays(2), 1),
			dayWith(first.AddDays(3), 1), dayWith(first.AddDays(4), 1), dayWith(first.AddDays(5), 1),
			// 첫 14일이 지난 뒤의 날이지만 일곱 번째 대화 날이라 기준선에 들어간다.
			dayWith(first.AddDays(20), 1),
			dayWith(first.AddDays(22), 4),
		}
		base, err := baseline.Compute(days, first.AddDays(25), p)
		require.NoError(t, err)
		require.True(t, base.Established)
		require.True(t, base.Extended)

		got, err := Run(days, base, p)
		require.NoError(t, err)

		assert.Equal(t, first.AddDays(21), got.From)
		assert.Equal(t, []Point{{Date: first.AddDays(22), Observed: 4, Step: 2.0, Capped: true, S: 2.0}}, got.Series)
	})

	t.Run("기준선 기간의 날을 지우면 평소가 달라져서 흐름도 달라진다", func(t *testing.T) {
		early := []signal.Day{
			dayWith(first, 4), dayWith(first.AddDays(1), 0), dayWith(first.AddDays(2), 0),
			dayWith(first.AddDays(3), 0), dayWith(first.AddDays(4), 0), dayWith(first.AddDays(5), 0),
			dayWith(first.AddDays(6), 0), dayWith(first.AddDays(7), 0),
		}
		later := after(first.AddDays(13), 2, 2, 2)
		asOf := first.AddDays(30)

		run := func(days []signal.Day) Result {
			base, err := baseline.Compute(days, asOf, p)
			require.NoError(t, err)
			got, err := Run(days, base, p)
			require.NoError(t, err)
			return got
		}

		// 지우기 전: 평소는 4/8 = 0.5이고 15일째 날부터 하루에 2 − 0.5 − 0.5 = 1.0씩 쌓인다.
		before := run(slices.Concat(early, later))
		assert.Equal(t, first.AddDays(14), before.From)
		assert.Equal(t, []float64{1, 2, 3}, values(before.Series))

		// 넷이 관찰된 첫날을 지우면 시작이 하루 밀리고 기간의 마지막 날도 하루 밀린다.
		// 그래서 쌓던 첫날(2개 관찰)이 이제는 기준선에 들어가 평소가 2/8 = 0.25가 되고,
		// 남은 이틀만 하루에 2 − 0.25 − 0.5 = 1.25씩 쌓인다.
		afterDeletion := run(slices.Concat(early[1:], later))
		assert.Equal(t, first.AddDays(15), afterDeletion.From)
		assert.Equal(t, []float64{1.25, 2.5}, values(afterDeletion.Series))
	})
}

func TestRunIgnoresDaysAfterAsOf(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	days := after(end, 4, 4, 4, 4, 4)

	got, err := Run(days, established(end, end.AddDays(3), 1.0), params.Default())
	require.NoError(t, err)

	assert.Equal(t, []float64{2, 4, 6}, values(got.Series))
	assert.Equal(t, State{Running: true, S: 6.0, Detected: true}, got.State)
}

func TestStateAt(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := end.AddDays(10)
	days := after(end, skip, 4, skip, 4, 4, 0, 0)

	got, err := Run(days, established(end, asOf, 1.0), params.Default())
	require.NoError(t, err)

	tests := []struct {
		name string
		date recorddate.Date
		want State
	}{
		{"빈 날짜", recorddate.Date{}, State{}},
		{"기준선 기간 안의 날에는 돌고 있지 않다", end.AddDays(-5), State{}},
		{"기준선 기간의 마지막 날에도 돌고 있지 않다", end, State{}},
		{"시작한 날에 대화가 없었으면 누적값 0으로 돌고 있다", end.AddDays(1), State{Running: true}},
		{"첫 대화 날", end.AddDays(2), State{Running: true, S: 2.0}},
		{"쉰 날은 앞 날의 값", end.AddDays(3), State{Running: true, S: 2.0}},
		{"한계값과 같아진 날은 아직 아니다", end.AddDays(4), State{Running: true, S: 4.0}},
		{"감지된 날", end.AddDays(5), State{Running: true, S: 6.0, Detected: true}},
		{"줄었지만 아직 한계값 위인 날", end.AddDays(6), State{Running: true, S: 4.5, Detected: true}},
		{"풀린 날", end.AddDays(7), State{Running: true, S: 3.0}},
		{"기준일", asOf, State{Running: true, S: 3.0}},
		{"기준일보다 뒤를 물으면 기준일의 상태", asOf.AddDays(30), State{Running: true, S: 3.0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, got.StateAt(tt.date))
		})
	}

	t.Run("기준일의 상태와 같다", func(t *testing.T) {
		assert.Equal(t, got.State, got.StateAt(got.AsOf))
	})

	t.Run("빈 결과에 물어도 돌고 있지 않음이다", func(t *testing.T) {
		assert.Equal(t, State{}, Result{}.StateAt(asOf))
	})
}

// 지난 날짜의 상태를 한 번 구한 결과에서 꺼낸 값은, 그 날짜를 기준일로 처음부터 다시 구한 값과 같아야 한다.
// 첫날부터 하루씩 다시 돌리는 계산이 이 성질에 기대고 있다.
func TestStateAtMatchesRecomputation(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	p := params.Default()

	histories := []struct {
		name string
		days []signal.Day
	}{
		{
			name: "첫 14일에 7일을 채운 기록",
			days: slices.Concat(
				[]signal.Day{
					dayWith(first, 1), dayWith(first.AddDays(2), 2), dayWith(first.AddDays(3), 0),
					dayWith(first.AddDays(5), 1), dayWith(first.AddDays(8), 3), dayWith(first.AddDays(9), 0),
					dayWith(first.AddDays(13), 1),
				},
				after(first.AddDays(13), 2, 3, skip, 4, 5, skip, skip, 0, 0, 1, 6, 0, skip, 3, 3, 3),
			),
		},
		{
			name: "기간을 늘린 기록",
			days: slices.Concat(
				[]signal.Day{
					dayWith(first, 0), dayWith(first.AddDays(3), 1), dayWith(first.AddDays(6), 0),
					dayWith(first.AddDays(9), 2), dayWith(first.AddDays(12), 1), dayWith(first.AddDays(15), 0),
					dayWith(first.AddDays(18), 1),
				},
				after(first.AddDays(18), 4, 4, skip, 0, 3, 3, 3, skip, 0, 0, 0),
			),
		},
	}

	for _, h := range histories {
		t.Run(h.name, func(t *testing.T) {
			asOf := first.AddDays(45)
			base, err := baseline.Compute(h.days, asOf, p)
			require.NoError(t, err)
			full, err := Run(h.days, base, p)
			require.NoError(t, err)
			require.NotEmpty(t, full.Series)

			for offset := 0; offset <= 45; offset++ {
				date := first.AddDays(offset)
				baseAt, err := baseline.Compute(h.days, date, p)
				require.NoError(t, err)
				at, err := Run(h.days, baseAt, p)
				require.NoError(t, err)

				assert.Equal(t, at.State, full.StateAt(date), "기준일 %s", date)
			}
		})
	}
}

// 긴 기록에서도 지켜져야 하는 성질을 본다. 난수 대신 0부터 8까지가 고르게 섞인 고정된 수열을 쓴다.
func TestRunInvariants(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := end.AddDays(250)

	observed := make([]int, 0, 200)
	for i := range 200 {
		x := (i*7 + i/9*5 + 3) % 9
		if i%11 == 4 {
			x = skip
		}
		observed = append(observed, x)
	}
	days := after(end, observed...)

	tests := []struct {
		name string
		mu   float64
		p    params.CUSUM
	}{
		{"평소 0, 상한 없음", 0, params.CUSUM{K: 0.5, H: 4.0}},
		{"평소 1.25, 상한 없음", 1.25, params.CUSUM{K: 0.5, H: 4.0}},
		{"평소 9/7, 상한 2", 9.0 / 7.0, params.CUSUM{K: 0.5, H: 4.0, MaxStep: 2}},
		{"평소 4, 상한 0.75", 4, params.CUSUM{K: 0.25, H: 2.5, MaxStep: 0.75}},
		{"평소 8, 여유 0", 8, params.CUSUM{K: 0, H: 1}},
		{"평소 1.25, 기본값(상한 2, 천장 2배)", 1.25, params.Default().CUSUM},
		{"평소 0, 상한 없이 천장 1.5배", 0, params.CUSUM{K: 0.5, H: 4.0, MaxS: 1.5}},
		{"평소 9/7, 상한 1, 천장 3배", 9.0 / 7.0, params.CUSUM{K: 0.25, H: 2.5, MaxStep: 1, MaxS: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(days, established(end, asOf, tt.mu), withCUSUM(tt.p))
			require.NoError(t, err)
			require.Len(t, got.Series, len(days))

			previousS := 0.0
			previousDate := end
			for i, pt := range got.Series {
				raw := float64(pt.Observed) - tt.mu - tt.p.K

				assert.True(t, pt.Date.After(previousDate), "날짜순이고 기준선 기간보다 뒤다")
				assert.False(t, pt.Date.After(asOf), "기준일을 넘지 않는다")
				assert.Equal(t, days[i].ObservedCount(), pt.Observed)
				assert.GreaterOrEqual(t, pt.S, 0.0, "누적값은 0 아래로 내려가지 않는다")
				assert.Equal(t, pt.S > tt.p.H, pt.Detected, "감지는 누적값이 한계값을 넘었는지와 같다")
				if tt.p.MaxStep > 0 {
					assert.LessOrEqual(t, pt.Step, tt.p.MaxStep, "하루 증가량은 상한을 넘지 않는다")
					assert.Equal(t, raw > tt.p.MaxStep, pt.Capped)
				} else {
					assert.False(t, pt.Capped)
				}
				// Step과 S는 분수로 쌓은 값을 가장 가까운 소수로 옮긴 것이다. 소수끼리 다시 셈한 값과는 끝자리가 다를 수 있다.
				if !pt.Capped {
					assert.InDelta(t, raw, pt.Step, 1e-12)
				}
				added := math.Max(0, previousS+pt.Step)
				if tt.p.MaxS > 0 {
					ceiling := tt.p.MaxS * tt.p.H
					assert.LessOrEqual(t, pt.S, ceiling, "누적값은 천장을 넘지 않는다")
					assert.Equal(t, added > ceiling+1e-9, pt.AtCeiling, "천장에 걸린 날만 표시한다")
					added = math.Min(added, ceiling)
				} else {
					assert.False(t, pt.AtCeiling)
				}
				assert.InDelta(t, added, pt.S, 1e-9, "앞 날의 누적값에 그날의 값을 더하고 천장에 맞춘 것이다")

				previousS = pt.S
				previousDate = pt.Date
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	end := mustDate(t, "2026-09-14")
	asOf := mustDate(t, "2026-10-31")
	days := after(end, 1, 2, 3)
	valid := established(end, asOf, 1.0)

	paramTests := []struct {
		name      string
		p         params.CUSUM
		wantField string
	}{
		{"허용 여유가 음수다", params.CUSUM{K: -0.1, H: 4}, "CUSUM.K"},
		{"허용 여유가 NaN이다", params.CUSUM{K: math.NaN(), H: 4}, "CUSUM.K"},
		{"허용 여유가 무한대다", params.CUSUM{K: math.Inf(1), H: 4}, "CUSUM.K"},
		{"한계값이 0이다", params.CUSUM{K: 0.5, H: 0}, "CUSUM.H"},
		{"한계값이 음수다", params.CUSUM{K: 0.5, H: -4}, "CUSUM.H"},
		{"한계값이 NaN이다", params.CUSUM{K: 0.5, H: math.NaN()}, "CUSUM.H"},
		{"한계값이 무한대다", params.CUSUM{K: 0.5, H: math.Inf(1)}, "CUSUM.H"},
		{"하루 증가량의 상한이 음수다", params.CUSUM{K: 0.5, H: 4, MaxStep: -1}, "CUSUM.MaxStep"},
		{"하루 증가량의 상한이 NaN이다", params.CUSUM{K: 0.5, H: 4, MaxStep: math.NaN()}, "CUSUM.MaxStep"},
		{"누적값의 천장이 한계값과 같다", params.CUSUM{K: 0.5, H: 4, MaxS: 1}, "CUSUM.MaxS"},
		{"누적값의 천장이 음수다", params.CUSUM{K: 0.5, H: 4, MaxS: -2}, "CUSUM.MaxS"},
		{"누적값의 천장이 NaN이다", params.CUSUM{K: 0.5, H: 4, MaxS: math.NaN()}, "CUSUM.MaxS"},
		{"빈 조정 값이다", params.CUSUM{}, "CUSUM.H"},
	}
	for _, tt := range paramTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(days, valid, withCUSUM(tt.p))

			var fieldErr *params.FieldError
			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, tt.wantField, fieldErr.Field)
			assert.Equal(t, Result{}, got)
		})
	}

	t.Run("다른 계산에 쓰는 조정 값이 틀려도 돌리지 않는다", func(t *testing.T) {
		p := params.Default()
		p.Baseline.WindowDays = 0

		_, err := Run(days, valid, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Baseline.WindowDays", fieldErr.Field)
	})

	baselineTests := []struct {
		name   string
		change func(b *baseline.Baseline)
	}{
		{"기준일이 비어 있다", func(b *baseline.Baseline) { b.AsOf = recorddate.Date{} }},
		{"잡혔다면서 마지막 날이 없다", func(b *baseline.Baseline) { b.End = recorddate.Date{} }},
		{"잡혔다면서 기준일이 마지막 날과 같다", func(b *baseline.Baseline) { b.AsOf = b.End }},
		{"잡혔다면서 기준일이 마지막 날보다 앞이다", func(b *baseline.Baseline) { b.AsOf = b.End.AddDays(-1) }},
		{"잡혔다면서 대화한 날이 없다", func(b *baseline.Baseline) { b.Days = 0 }},
		{"평균이 NaN이다", func(b *baseline.Baseline) { b.Mu = math.NaN() }},
		{"평균이 무한대다", func(b *baseline.Baseline) { b.Mu = math.Inf(1) }},
		{"평균이 음수다", func(b *baseline.Baseline) { b.Mu = -0.5 }},
		{"평균이 항목 수보다 크다", func(b *baseline.Baseline) { b.Mu = 8.5 }},
		// 누적은 합과 일수로 하고 평균은 보여주기만 한다. 둘이 어긋나면 보이는 평소와 계산에 쓴 평소가 달라진다.
		{"평균이 합을 일수로 나눈 값과 다르다", func(b *baseline.Baseline) { b.Mu = 1.5 }},
		{"합이 비어 있는데 평균만 채워져 있다", func(b *baseline.Baseline) { b.ObservedTotal = 0 }},
		{"합이 음수다", func(b *baseline.Baseline) { b.ObservedTotal, b.Mu = -1, -1 }},
	}
	for _, tt := range baselineTests {
		t.Run(tt.name, func(t *testing.T) {
			base := valid
			tt.change(&base)

			got, err := Run(days, base, params.Default())
			require.ErrorIs(t, err, ErrInvalidBaseline)
			assert.Equal(t, Result{}, got)
		})
	}

	t.Run("빈 기준선은 기준일이 없어서 오류다", func(t *testing.T) {
		_, err := Run(days, baseline.Baseline{}, params.Default())
		assert.ErrorIs(t, err, ErrInvalidBaseline)
	})

	t.Run("잡히지 않은 기준선은 기준일만 있으면 된다", func(t *testing.T) {
		got, err := Run(days, baseline.Baseline{AsOf: asOf}, params.Default())
		require.NoError(t, err)
		assert.Empty(t, got.Series)
		assert.Equal(t, State{}, got.State)
	})

	dayTests := []struct {
		name    string
		days    []signal.Day
		wantErr error
	}{
		{"날짜순이 아니다", []signal.Day{dayWith(end.AddDays(2), 1), dayWith(end.AddDays(1), 1)}, signal.ErrUnsortedDays},
		{"같은 날짜가 두 번 나온다", []signal.Day{dayWith(end.AddDays(1), 1), dayWith(end.AddDays(1), 2)}, signal.ErrDuplicateDate},
		{"날짜가 빠진 하루가 있다", []signal.Day{{}}, signal.ErrZeroDate},
		{
			"판단과 명시성이 맞지 않는 하루가 있다",
			[]signal.Day{{Date: end.AddDays(1), Judgements: [signal.ItemCount]signal.Judgement{{Status: signal.Observed}}}},
			signal.ErrInconsistentJudgement,
		},
	}
	for _, tt := range dayTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(tt.days, valid, params.Default())
			require.ErrorIs(t, err, tt.wantErr)

			var dayErr *signal.DayError
			assert.ErrorAs(t, err, &dayErr)
		})
	}
}

func TestRunIsPure(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	p := params.Default()
	days := slices.Concat(
		[]signal.Day{
			dayWith(first, 3), dayWith(first.AddDays(1), 1), dayWith(first.AddDays(2), 0),
			dayWith(first.AddDays(3), 0), dayWith(first.AddDays(4), 2), dayWith(first.AddDays(5), 1),
			dayWith(first.AddDays(6), 2),
		},
		after(first.AddDays(13), 2, 3, skip, 4, 5, 0, 0, 6, 1),
	)
	original := slices.Clone(days)
	asOf := first.AddDays(40)

	base, err := baseline.Compute(days, asOf, p)
	require.NoError(t, err)

	a, err := Run(days, base, p)
	require.NoError(t, err)
	b, err := Run(days, base, p)
	require.NoError(t, err)

	t.Run("같은 기록에서는 소수 끝자리까지 같은 값이 나온다", func(t *testing.T) {
		// 평소가 9/7이라 누적값이 이진 소수로 딱 떨어지지 않지만, 보여주는 값은 끝자리까지 매번 같아야 한다.
		assert.Equal(t, a, b)
		for i := range a.Series {
			assert.Equal(t, math.Float64bits(a.Series[i].S), math.Float64bits(b.Series[i].S))
		}
	})

	t.Run("받은 기록을 고치지 않는다", func(t *testing.T) {
		assert.Equal(t, original, days)
	})

	t.Run("돌려받은 흐름을 고쳐도 다음 계산에 영향이 없다", func(t *testing.T) {
		a.Series[0].S = 999
		c, err := Run(days, base, p)
		require.NoError(t, err)
		assert.Equal(t, b, c)
	})
}
