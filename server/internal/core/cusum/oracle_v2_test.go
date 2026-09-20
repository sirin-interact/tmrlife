package cusum_test

// 하루 증가량의 상한과 누적값의 천장을 손으로 셈한 값과 견주는 시험이다.
// 기대값은 코드를 돌려서 얻지 않았다. 경우마다 위에 셈을 적었으니, 깨지면 셈과 코드 가운데 어느 쪽이 틀렸는지 따져 볼 수 있다.
// 공개된 함수만 부르고, 평소의 하루 평균도 손으로 넣지 않고 기록에서 구하게 한다.
//
// 손으로 따라간 식은 이것 하나다. 대화한 날마다 한 번씩 적용한다.
//
//	더할 값 = min(2, 그날 관찰된 항목 수 − 평소의 하루 평균 − 0.5)
//	누적값 = min(8, max(0, 누적값 + 더할 값)),  누적값이 4를 넘으면(같으면 아니다) 변화 감지
//
// 첫 14일이 평소를 정하는 기간이고, 15일째부터 쌓는다.

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

// 첫 대화 날이다. 3월은 31일까지라 35일째쯤까지 가도 날짜를 머리로 따라가기 쉽다.
const ov2First = "2026-03-01"

// ov2Nth는 첫 대화 날을 1일째로 센 n일째 날짜다.
func ov2Nth(t *testing.T, n int) recorddate.Date {
	t.Helper()
	first, err := recorddate.Parse(ov2First)
	require.NoError(t, err)
	return first.AddDays(n - 1)
}

// ov2Silent는 대화하지 않은 날이다.
const ov2Silent = -1

// ov2Days는 첫 대화 날부터 달력의 하루에 하나씩 적은 "그날 관찰된 항목 수"를 하루의 목록으로 옮긴다.
// 관찰되지 않은 나머지 항목은 "관찰되지 않음"으로 채운다. 누적에는 관찰된 수만 쓰이므로 결과와 상관없다.
func ov2Days(t *testing.T, counts ...int) []signal.Day {
	t.Helper()
	days := make([]signal.Day, 0, len(counts))
	for offset, count := range counts {
		if count == ov2Silent {
			continue
		}
		require.GreaterOrEqual(t, count, 0)
		require.LessOrEqual(t, count, signal.ItemCount)
		day := signal.Day{Date: ov2Nth(t, offset+1)}
		for idx := range signal.ItemCount {
			status := signal.NotObserved
			if idx < count {
				status = signal.Observed
			}
			day.Judgements[idx] = signal.Judgement{Status: status, Explicitness: signal.Direct}
		}
		days = append(days, day)
	}
	require.NoError(t, signal.ValidateDays(days))
	return days
}

func ov2Cat(parts ...[]int) []int {
	var out []int
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func ov2Rep(n, count int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = count
	}
	return out
}

// 평소를 정하는 첫 14일의 기록 세 가지다. 모두 날마다 대화한 기록이라 기간은 14일째에 끝난다.
//
//	ov2UsualHalf: 1과 0이 번갈아 → 합 7 ÷ 14일 = 0.5
//	ov2Usual(c):  날마다 c개   → 합 14c ÷ 14일 = c
func ov2UsualHalf() []int {
	return []int{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0}
}

func ov2Usual(count int) []int {
	return ov2Rep(14, count)
}

// ov2Run은 기록의 마지막 날을 기준일로 삼아 기준선을 구하고 누적을 돌린다.
func ov2Run(t *testing.T, p params.Params, counts []int) (baseline.Baseline, cusum.Result) {
	t.Helper()
	days := ov2Days(t, counts...)
	asOf := ov2Nth(t, len(counts))
	base, err := baseline.Compute(days, asOf, p)
	require.NoError(t, err)
	require.True(t, base.Established, "15일째부터는 기준선이 잡혀 있어야 셈의 전제가 맞는다")
	result, err := cusum.Run(days, base, p)
	require.NoError(t, err)
	return base, result
}

type ov2Point struct {
	nth       int
	observed  int
	step      float64
	capped    bool
	atCeiling bool
	s         float64
	detected  bool
}

func ov2AssertSeries(t *testing.T, want []ov2Point, got []cusum.Point) {
	t.Helper()
	require.Len(t, got, len(want), "대화한 날마다 한 줄이어야 한다")
	for i, w := range want {
		g := got[i]
		assert.Equal(t, ov2Nth(t, w.nth), g.Date, "%d번째 줄의 날짜", i+1)
		assert.Equal(t, w.observed, g.Observed, "%d일째 관찰된 항목 수", w.nth)
		assert.InDelta(t, w.step, g.Step, 1e-9, "%d일째 더한 값", w.nth)
		assert.Equal(t, w.capped, g.Capped, "%d일째 상한에 걸렸는지", w.nth)
		assert.Equal(t, w.atCeiling, g.AtCeiling, "%d일째 천장에 걸렸는지", w.nth)
		assert.InDelta(t, w.s, g.S, 1e-9, "%d일째 누적값", w.nth)
		assert.Equal(t, w.detected, g.Detected, "%d일째 변화 감지", w.nth)
	}
}

// 평소 0.5.
//
//	15일째 3개: 3 − 0.5 − 0.5 = 2.0  상한과 딱 같다. 줄어든 것이 없으므로 걸린 것이 아니다. 누적 2.0
//	16일째 4개: 4 − 0.5 − 0.5 = 3.0  → 2.0으로 묶임. 누적 4.0. 한계값과 같으므로 아직 감지가 아니다
//	17일째 1개: 1 − 0.5 − 0.5 = 0    누적 4.0 그대로. 여전히 감지가 아니다
//	18일째 2개: 2 − 0.5 − 0.5 = 1.0  누적 5.0 > 4 → 감지
func TestOracleV2StepExactlyAtCap(t *testing.T) {
	base, result := ov2Run(t, params.Default(), ov2Cat(ov2UsualHalf(), []int{3, 4, 1, 2}))

	require.Equal(t, 14, base.Days)
	require.Equal(t, 7, base.ObservedTotal)
	assert.Equal(t, ov2Nth(t, 15), result.From, "기준선 기간의 다음 날부터 쌓는다")

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 3, step: 2.0, capped: false, s: 2.0, detected: false},
		{nth: 16, observed: 4, step: 2.0, capped: true, s: 4.0, detected: false},
		{nth: 17, observed: 1, step: 0.0, capped: false, s: 4.0, detected: false},
		{nth: 18, observed: 2, step: 1.0, capped: false, s: 5.0, detected: true},
	}, result.Series)
	assert.True(t, result.State.Detected)
}

// 평소 0.3: 첫 14일 가운데 열흘 대화했고(4, 8, 12, 14일째는 쉼) 그중 사흘만 하나씩 관찰됐다. 합 3 ÷ 10일 = 0.3.
//
// 하루만 크게 나빴다가 회복한 기복에는 울리지 않아야 한다.
//
//	15일째 6개: 6 − 0.3 − 0.5 = 5.2 → 2.0으로 묶임. 누적 2.0. 4를 넘지 않아 감지가 아니다
//	16일째 0개: 0 − 0.3 − 0.5 = −0.8  누적 1.2
//	17일째 0개: 누적 0.4
//	18일째 0개: 0.4 − 0.8 = −0.4 → 0에서 멈춘다
//
// 상한을 끄면(0) 같은 기록에서 15일째에 5.2로 곧바로 감지가 켜지고 16일째(4.4)까지 이어진다. 17일째 3.6에서 풀린다.
// 그 하루 때문에 울리는 것을 막는 것이 상한이라는 것을 두 결과의 차이로 확인한다.
func TestOracleV2SingleBadDayDoesNotDetect(t *testing.T) {
	counts := ov2Cat(
		[]int{1, 0, 0, ov2Silent, 1, 0, 0, ov2Silent, 1, 0, 0, ov2Silent, 0, ov2Silent},
		[]int{6, 0, 0, 0},
	)

	t.Run("상한이 있으면 하루의 급등으로는 감지되지 않는다", func(t *testing.T) {
		base, result := ov2Run(t, params.Default(), counts)
		require.Equal(t, 10, base.Days)
		require.Equal(t, 3, base.ObservedTotal)
		require.Equal(t, ov2Nth(t, 14), base.End, "첫 14일 안에 열흘 대화했으므로 기간은 14일째에 끝난다")

		ov2AssertSeries(t, []ov2Point{
			{nth: 15, observed: 6, step: 2.0, capped: true, s: 2.0},
			{nth: 16, observed: 0, step: -0.8, s: 1.2},
			{nth: 17, observed: 0, step: -0.8, s: 0.4},
			{nth: 18, observed: 0, step: -0.8, s: 0.0},
		}, result.Series)
	})

	t.Run("상한을 끄면 같은 하루로 감지가 켜진다", func(t *testing.T) {
		p := params.Default()
		p.CUSUM.MaxStep = 0
		_, result := ov2Run(t, p, counts)

		ov2AssertSeries(t, []ov2Point{
			{nth: 15, observed: 6, step: 5.2, s: 5.2, detected: true},
			{nth: 16, observed: 0, step: -0.8, s: 4.4, detected: true},
			{nth: 17, observed: 0, step: -0.8, s: 3.6, detected: false},
			{nth: 18, observed: 0, step: -0.8, s: 2.8, detected: false},
		}, result.Series)
	})
}

// 서서히 나빠지는 흐름은 하루 증가량이 작아서 상한에 닿지 않는다. 상한이 있든 없든 같은 날에 감지된다.
//
// 평소 1, 날마다 2개: 2 − 1 − 0.5 = 0.5씩 쌓인다.
//
//	15일째 0.5, 16일째 1.0, …, 22일째(여덟 번째) 4.0 → 한계값과 같으므로 아직 아니다
//	23일째(아홉 번째) 4.5 → 감지
func TestOracleV2GradualWorseningUnaffectedByCap(t *testing.T) {
	counts := ov2Cat(ov2Usual(1), ov2Rep(9, 2))

	tests := []struct {
		name    string
		maxStep float64
	}{
		{name: "상한이 2일 때", maxStep: 2.0},
		{name: "상한을 껐을 때", maxStep: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := params.Default()
			p.CUSUM.MaxStep = tt.maxStep
			_, result := ov2Run(t, p, counts)

			require.Len(t, result.Series, 9)
			for i, pt := range result.Series {
				assert.InDelta(t, 0.5*float64(i+1), pt.S, 1e-9, "%d번째 대화한 날의 누적값", i+1)
				assert.False(t, pt.Capped)
				assert.Equal(t, i == 8, pt.Detected, "아홉 번째 날에만 감지여야 한다(%d번째)", i+1)
			}
			assert.Equal(t, ov2Nth(t, 23), result.Series[8].Date)
		})
	}
}

// 힘든 3주 동안 누적값은 8에서 멈춘다. 그 뒤 회복해서 감지가 풀리기까지 대화한 날이 며칠 필요한지를 센다.
//
// 나쁜 날은 여덟 항목이 모두 관찰된 날이다. 8 − 평소 − 0.5는 어느 평소에서든 2보다 커서 날마다 2씩만 쌓인다.
//
//	15일째 2.0, 16일째 4.0(같으므로 아직 아니다), 17일째 6.0(감지), 18일째 8.0(천장과 딱 같다. 잘린 것은 없다),
//	19일째부터 10.0이 될 것이 8.0으로 잘린다. 21일 동안 나빴어도 35일째의 누적값은 8.0이다(천장이 없으면 42.0).
//
// 회복하는 날의 더할 값을 −d라 하면 8 − n×d ≤ 4가 되는 가장 작은 n이 필요한 일수다(4와 같아지면 감지가 풀린다).
//
//	평소 1,   0개: d = 1.5 → 6.5, 5.0, 3.5                          → 3일
//	평소 1,   1개: d = 0.5 → 7.5, 7.0, 6.5, 6.0, 5.5, 5.0, 4.5, 4.0 → 8일(여덟째 날에 딱 4.0)
//	평소 0.5, 0개: d = 1.0 → 7.0, 6.0, 5.0, 4.0                     → 4일(넷째 날에 딱 4.0)
//	평소 0,   0개: d = 0.5 →                                          8일
//	평소 2,   0개: d = 2.5 → 5.5, 3.0                                → 2일
//	평소 2,   1개: d = 1.5 → 6.5, 5.0, 3.5                          → 3일
//	평소 2,   2개: d = 0.5 →                                          8일
func TestOracleV2RecoveryDaysFromCeiling(t *testing.T) {
	const badDays = 21
	const recoveryDays = 10

	tests := []struct {
		name      string
		usual     []int
		recoverAt int
		drop      float64
		wantDays  int
	}{
		{name: "평소 1에서 아무것도 관찰되지 않는 날로 회복하면 사흘", usual: ov2Usual(1), recoverAt: 0, drop: 1.5, wantDays: 3},
		{name: "평소 1에서 평소와 같은 날로 회복하면 여드레", usual: ov2Usual(1), recoverAt: 1, drop: 0.5, wantDays: 8},
		{name: "평소 0.5에서 아무것도 관찰되지 않는 날로 회복하면 나흘", usual: ov2UsualHalf(), recoverAt: 0, drop: 1.0, wantDays: 4},
		{name: "평소 0에서 아무것도 관찰되지 않는 날로 회복하면 여드레", usual: ov2Usual(0), recoverAt: 0, drop: 0.5, wantDays: 8},
		{name: "평소 2에서 아무것도 관찰되지 않는 날로 회복하면 이틀", usual: ov2Usual(2), recoverAt: 0, drop: 2.5, wantDays: 2},
		{name: "평소 2에서 하나만 관찰되는 날로 회복하면 사흘", usual: ov2Usual(2), recoverAt: 1, drop: 1.5, wantDays: 3},
		{name: "평소 2에서 평소와 같은 날로 회복하면 여드레", usual: ov2Usual(2), recoverAt: 2, drop: 0.5, wantDays: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, result := ov2Run(t, params.Default(),
				ov2Cat(tt.usual, ov2Rep(badDays, 8), ov2Rep(recoveryDays, tt.recoverAt)))
			require.Len(t, result.Series, badDays+recoveryDays)

			bad := result.Series[:badDays]
			for i, pt := range bad {
				// 2.0, 4.0, 6.0, 8.0, 그 뒤로는 8.0
				want := min(8.0, 2.0*float64(i+1))
				assert.InDelta(t, want, pt.S, 1e-9, "나쁜 날 %d번째의 누적값", i+1)
				assert.True(t, pt.Capped, "나쁜 날의 증가량은 상한에 걸린다")
				assert.Equal(t, i >= 4, pt.AtCeiling, "다섯 번째 나쁜 날부터 천장에 잘린다(%d번째)", i+1)
				assert.Equal(t, i >= 2, pt.Detected, "세 번째 나쁜 날(6.0)부터 감지다(%d번째)", i+1)
			}

			recovery := result.Series[badDays:]
			cleared := 0
			for i, pt := range recovery {
				want := max(0.0, 8.0-tt.drop*float64(i+1))
				assert.InDelta(t, want, pt.S, 1e-9, "회복 %d일째의 누적값", i+1)
				if cleared == 0 && !pt.Detected {
					cleared = i + 1
				}
			}
			assert.Equal(t, tt.wantDays, cleared, "감지가 풀리기까지 회복한 뒤 대화한 날 수")
			for i, pt := range recovery {
				assert.Equal(t, i+1 < tt.wantDays, pt.Detected, "회복 %d일째의 변화 감지", i+1)
			}
		})
	}
}

// 상한과 천장이 따로 걸리는 것을 본다. 평소 0.5.
//
//	15~17일째 3개씩: 2.0씩(상한과 같을 뿐 걸린 것은 아니다) → 2.0, 4.0, 6.0
//	18일째 2개: 1.0 → 7.0
//	19일째 3개: 2.0 → 9.0이 될 것이 8.0으로 잘린다. 천장에만 걸렸다
//	20일째 1개: 0   → 8.0 그대로. 넘은 것이 없으므로 천장에 걸린 것이 아니다
//	21일째 4개: 3.0 → 2.0으로 묶이고, 10.0이 될 것이 8.0으로 잘린다. 둘 다 걸렸다
//	22일째 0개: −1.0 → 7.0. 천장에서도 내려가는 것은 그대로 내려간다
func TestOracleV2CapAndCeilingFlags(t *testing.T) {
	_, result := ov2Run(t, params.Default(), ov2Cat(ov2UsualHalf(), []int{3, 3, 3, 2, 3, 1, 4, 0}))

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 3, step: 2.0, s: 2.0, detected: false},
		{nth: 16, observed: 3, step: 2.0, s: 4.0, detected: false},
		{nth: 17, observed: 3, step: 2.0, s: 6.0, detected: true},
		{nth: 18, observed: 2, step: 1.0, s: 7.0, detected: true},
		{nth: 19, observed: 3, step: 2.0, atCeiling: true, s: 8.0, detected: true},
		{nth: 20, observed: 1, step: 0.0, s: 8.0, detected: true},
		{nth: 21, observed: 4, step: 2.0, capped: true, atCeiling: true, s: 8.0, detected: true},
		{nth: 22, observed: 0, step: -1.0, s: 7.0, detected: true},
	}, result.Series)
}

// 줄어드는 쪽은 묶지 않는다. 평소 4.
//
//	15~18일째 8개씩: 8 − 4 − 0.5 = 3.5 → 2.0으로 묶임 → 2.0, 4.0, 6.0, 8.0
//	19일째 0개: 0 − 4 − 0.5 = −4.5 → 3.5. 하루 만에 한계값 아래로 내려가 감지가 풀린다
//
// 줄어드는 쪽도 2로 묶였다면 6.0이어서 감지가 남았을 것이다.
func TestOracleV2DecreaseIsNotCapped(t *testing.T) {
	_, result := ov2Run(t, params.Default(), ov2Cat(ov2Usual(4), []int{8, 8, 8, 8, 0}))

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 8, step: 2.0, capped: true, s: 2.0, detected: false},
		{nth: 16, observed: 8, step: 2.0, capped: true, s: 4.0, detected: false},
		{nth: 17, observed: 8, step: 2.0, capped: true, s: 6.0, detected: true},
		{nth: 18, observed: 8, step: 2.0, capped: true, s: 8.0, detected: true},
		{nth: 19, observed: 0, step: -4.5, s: 3.5, detected: false},
	}, result.Series)
}

// 대화하지 않은 날은 건너뛰고 누적값을 그대로 둔다. 평소 1.
//
//	15일째 8개: 2.0
//	16, 17일째 쉼: 2.0 그대로
//	18일째 8개: 4.0(아직 아니다), 19일째 8개: 6.0(감지)
//	20~25일째 쉼: 6.0과 감지가 그대로 이어진다. 쉰다고 저절로 줄지 않는다
func TestOracleV2SilentDaysKeepTheSum(t *testing.T) {
	_, result := ov2Run(t, params.Default(), ov2Cat(
		ov2Usual(1),
		[]int{8, ov2Silent, ov2Silent, 8, 8},
		ov2Rep(6, ov2Silent),
	))

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 8, step: 2.0, capped: true, s: 2.0, detected: false},
		{nth: 18, observed: 8, step: 2.0, capped: true, s: 4.0, detected: false},
		{nth: 19, observed: 8, step: 2.0, capped: true, s: 6.0, detected: true},
	}, result.Series)

	tests := []struct {
		name     string
		nth      int
		running  bool
		s        float64
		detected bool
	}{
		{name: "기준선 기간의 마지막 날에는 아직 돌지 않는다", nth: 14},
		{name: "쉰 17일째에는 15일째의 값이 이어진다", nth: 17, running: true, s: 2.0},
		{name: "18일째에는 한계값과 같아 아직 감지가 아니다", nth: 18, running: true, s: 4.0},
		{name: "쉰 22일째에도 감지가 이어진다", nth: 22, running: true, s: 6.0, detected: true},
		{name: "기준일인 25일째", nth: 25, running: true, s: 6.0, detected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := result.StateAt(ov2Nth(t, tt.nth))
			assert.Equal(t, tt.running, got.Running)
			assert.InDelta(t, tt.s, got.S, 1e-9)
			assert.Equal(t, tt.detected, got.Detected)
		})
	}
	assert.Equal(t, result.StateAt(ov2Nth(t, 25)), result.State, "기준일의 상태는 그날을 물어본 것과 같다")
}

// 평소가 이진 소수로 딱 떨어지지 않아도 누적값은 분수로 정확히 쌓인다. 천장에서 내려와 한계값과 딱 같아지는 날에 감지가 풀려야 한다.
//
// 평소 5/6: 첫 14일 가운데 열이틀 대화했고(6, 12일째는 쉼) 그중 열흘에 하나씩 관찰됐다. 합 10 ÷ 12일 = 5/6.
//
//	나쁜 날(8개): 8 − 5/6 − 1/2 = 6과 2/3 → 2로 묶임 → 15~18일째 2, 4, 6, 8. 19일째는 10이 될 것이 8로 잘린다
//	0개로 회복:   더할 값은 −(5/6 + 1/2) = −4/3 → 20/3(6.67), 16/3(5.33), 12/3 = 4, 8/3(2.67). 셋째 날에 딱 4가 되어 감지가 풀린다
//	1개로 회복:   더할 값은 1 − 4/3 = −1/3 → 8 − n/3 ≤ 4가 되는 가장 작은 n은 12. 열두째 날에 딱 4가 되어 감지가 풀린다
//
// 소수로 쌓았다면 1.333…을 세 번 뺀 자리에 4보다 아주 조금 큰 값이 남아 하루 늦게 풀렸을 것이다.
func TestOracleV2ExactThirdsFromCeiling(t *testing.T) {
	usual := []int{1, 1, 1, 1, 1, ov2Silent, 1, 1, 1, 1, 1, ov2Silent, 0, 0}
	const badDays = 5

	tests := []struct {
		name         string
		recoverAt    int
		recoveryDays int
		wantCleared  int
	}{
		{name: "아무것도 관찰되지 않는 날로 회복하면 셋째 날에 딱 4가 되어 풀린다", recoverAt: 0, recoveryDays: 4, wantCleared: 3},
		{name: "하나만 관찰되는 날로 회복하면 열두째 날에 딱 4가 되어 풀린다", recoverAt: 1, recoveryDays: 13, wantCleared: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, result := ov2Run(t, params.Default(),
				ov2Cat(usual, ov2Rep(badDays, 8), ov2Rep(tt.recoveryDays, tt.recoverAt)))
			require.Equal(t, 12, base.Days)
			require.Equal(t, 10, base.ObservedTotal)
			require.Equal(t, ov2Nth(t, 14), base.End)
			require.Len(t, result.Series, badDays+tt.recoveryDays)

			last := result.Series[badDays-1]
			assert.InDelta(t, 8.0, last.S, 1e-9)
			assert.True(t, last.AtCeiling, "다섯째 나쁜 날은 천장에 잘린다")
			assert.True(t, last.Detected)

			drop := 4.0 / 3.0
			if tt.recoverAt == 1 {
				drop = 1.0 / 3.0
			}
			for i, pt := range result.Series[badDays:] {
				nthDay := i + 1
				assert.InDelta(t, 8.0-drop*float64(nthDay), pt.S, 1e-9, "회복 %d일째의 누적값", nthDay)
				assert.Equal(t, nthDay < tt.wantCleared, pt.Detected, "회복 %d일째의 변화 감지", nthDay)
				assert.False(t, pt.AtCeiling, "회복 %d일째", nthDay)
			}
		})
	}
}

// 조정 값은 적은 십진수 그대로 읽힌다. 허용 여유를 0.3으로 두고 평소가 0.7이면, 세 항목이 관찰된 날의 더할 값은 3 − 0.7 − 0.3 = 딱 2다.
//
// 평소 0.7: 첫 14일 가운데 열흘 대화했고(11~14일째는 쉼) 그중 이레에 하나씩 관찰됐다. 합 7 ÷ 10일.
//
//	15일째 3개: 2.0. 상한과 같을 뿐 묶인 것은 아니다. 누적 2.0
//	16일째 3개: 누적 4.0. 한계값과 같으므로 아직 감지가 아니다
//	17일째 1개: 1 − 0.7 − 0.3 = 0. 그대로 4.0
//	18일째 2개: 1.0 → 5.0 감지
func TestOracleV2DecimalAllowanceStaysExact(t *testing.T) {
	p := params.Default()
	p.CUSUM.K = 0.3

	usual := []int{1, 1, 1, 1, 1, 1, 1, 0, 0, 0, ov2Silent, ov2Silent, ov2Silent, ov2Silent}
	base, result := ov2Run(t, p, ov2Cat(usual, []int{3, 3, 1, 2}))
	require.Equal(t, 10, base.Days)
	require.Equal(t, 7, base.ObservedTotal)
	require.Equal(t, ov2Nth(t, 14), base.End)

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 3, step: 2.0, capped: false, s: 2.0, detected: false},
		{nth: 16, observed: 3, step: 2.0, capped: false, s: 4.0, detected: false},
		{nth: 17, observed: 1, step: 0.0, s: 4.0, detected: false},
		{nth: 18, observed: 2, step: 1.0, s: 5.0, detected: true},
	}, result.Series)
}

// 천장은 한계값의 배수라서 한계값을 바꾸면 함께 따라간다. 한계값 3, 배수 1.5면 천장은 4.5다. 평소 1.
//
//	15일째 8개: 2로 묶임 → 2.0. 3을 넘지 않는다
//	16일째 8개: 4.0 > 3 → 감지. 4.5를 넘지 않았으므로 잘린 것은 없다
//	17일째 8개: 6.0이 될 것이 4.5로 잘린다
//	18일째 8개: 6.5가 될 것이 4.5로 잘린다
//	19일째 0개: 0 − 1 − 0.5 = −1.5 → 3.0. 한계값과 같으므로 하루 만에 감지가 풀린다
func TestOracleV2CeilingFollowsLimit(t *testing.T) {
	p := params.Default()
	p.CUSUM.H = 3.0
	p.CUSUM.MaxS = 1.5

	_, result := ov2Run(t, p, ov2Cat(ov2Usual(1), []int{8, 8, 8, 8, 0}))

	ov2AssertSeries(t, []ov2Point{
		{nth: 15, observed: 8, step: 2.0, capped: true, s: 2.0, detected: false},
		{nth: 16, observed: 8, step: 2.0, capped: true, s: 4.0, detected: true},
		{nth: 17, observed: 8, step: 2.0, capped: true, atCeiling: true, s: 4.5, detected: true},
		{nth: 18, observed: 8, step: 2.0, capped: true, atCeiling: true, s: 4.5, detected: true},
		{nth: 19, observed: 0, step: -1.5, s: 3.0, detected: false},
	}, result.Series)
}

// 두 장치가 없을 때와 있을 때, 힘든 3주 뒤에 감지가 풀리기까지 걸리는 날 수를 견준다.
//
// 평소 3. 나쁜 날은 여덟 항목이 모두 관찰된 날(8 − 3 − 0.5 = 4.5), 회복한 날은 평소와 같은 3개(3 − 3 − 0.5 = −0.5).
//
//	상한도 천장도 없으면: 21일 × 4.5 = 94.5. 94.5 − 0.5n ≤ 4가 되는 가장 작은 n은 181 → 반년 가까이 감지가 남는다
//	상한만 있으면:       21일 × 2 = 42.   42 − 0.5n ≤ 4 → n = 76
//	둘 다 있으면(기본값): 8에서 멈춘다.   8 − 0.5n ≤ 4 → n = 8
func TestOracleV2RecoveryWithAndWithoutTheTwoLimits(t *testing.T) {
	const badDays = 21
	const recoveryDays = 185

	tests := []struct {
		name        string
		maxStep     float64
		maxS        float64
		wantPeak    float64
		wantCleared int
	}{
		{name: "상한도 천장도 없으면 94.5까지 쌓이고 181일째에야 풀린다", maxStep: 0, maxS: 0, wantPeak: 94.5, wantCleared: 181},
		{name: "상한만 있으면 42까지 쌓이고 76일째에 풀린다", maxStep: 2.0, maxS: 0, wantPeak: 42.0, wantCleared: 76},
		{name: "상한과 천장이 모두 있으면 8에서 멈추고 8일째에 풀린다", maxStep: 2.0, maxS: 2.0, wantPeak: 8.0, wantCleared: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := params.Default()
			p.CUSUM.MaxStep = tt.maxStep
			p.CUSUM.MaxS = tt.maxS
			_, result := ov2Run(t, p, ov2Cat(ov2Usual(3), ov2Rep(badDays, 8), ov2Rep(recoveryDays, 3)))
			require.Len(t, result.Series, badDays+recoveryDays)

			peak := result.Series[badDays-1]
			assert.InDelta(t, tt.wantPeak, peak.S, 1e-9, "나쁜 3주가 끝난 날의 누적값")
			assert.True(t, peak.Detected)

			for i, pt := range result.Series[badDays:] {
				nthDay := i + 1
				assert.InDelta(t, max(0.0, tt.wantPeak-0.5*float64(nthDay)), pt.S, 1e-9, "회복 %d일째의 누적값", nthDay)
				assert.Equal(t, nthDay < tt.wantCleared, pt.Detected, "회복 %d일째의 변화 감지", nthDay)
			}
		})
	}
}
