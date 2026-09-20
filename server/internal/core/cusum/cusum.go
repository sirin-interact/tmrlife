// Package cusum은 평소에서 벗어난 정도를 날마다 쌓아서 변화를 알아챈다.
//
// 하루하루를 기준값 하나와 견주기만 하면, 하루 크게 나빴다가 회복한 날에는 울리고
// 조금씩 꾸준히 나빠지는 흐름은 놓친다. 그래서 그날 관찰된 항목 수가 평소보다 얼마나 많은지를
// 날마다 더해 가고, 평소 이하인 날에는 누적값이 줄어들게 한다.
//
//	S = min(MaxS × H, max(0, S + step)),  step = min(MaxStep, x − μ − K)
//
// 대화한 날마다 이 식을 한 번씩 적용한다.
//
//   - x는 그날 관찰된 항목 수, μ는 평소의 하루 평균 신호 수, K는 허용 여유다.
//     평소보다 K만큼 많은 것까지는 흔한 기복으로 보고 쌓지 않는다.
//   - 누적값은 0 아래로 내려가지 않는다. 좋았던 날을 저축해 두었다가 나빠지는 흐름을 가리는 일이 없게 한다.
//   - 하루에 늘어나는 양은 MaxStep(기본 2)까지만 인정한다. 줄어드는 쪽은 묶지 않는다.
//     하루만 크게 나빴다가 회복한 날 하나로 한계값을 넘지 않게 하려는 것이다. 0이면 묶지 않는다.
//   - 누적값은 H의 MaxS배(기본 2배)를 넘지 못한다. 그날의 증가량을 더한 다음에 천장에 맞춘다.
//     천장이 없으면 힘든 몇 주 동안 쌓인 값을 다 덜어 낼 때까지, 회복한 뒤에도 몇 달 동안 감지가 켜져 있다. 0이면 천장이 없다.
//   - 누적값이 H를 넘으면 변화 감지다. H와 같으면 아직 아니다.
//   - 넘은 뒤에도 누적값을 0으로 되돌리지 않는다. 되돌리면 나쁜 상태가 이어지는 동안에도 감지가 꺼졌다 켜지기를 되풀이한다.
//     나아지면 누적값이 줄어 H 이하로 내려가고, 그때 감지가 풀린다.
//   - 대화하지 않은 날은 건너뛰고 누적값을 그대로 둔다. 기록이 없는 날을 좋은 날로도 나쁜 날로도 치지 않는다.
//     대화는 했지만 여덟 항목이 모두 언급 없음인 날은 대화한 날이다. x = 0으로 쌓여 누적값을 줄인다.
//
// # 시작하는 날
//
// 기준선 기간의 마지막 날 다음 날부터 쌓는다. 기준선이 잡히기 전에는 돌리지 않는다.
// 평소를 정하는 데 쓴 날을 다시 평소와 견주면 같은 날을 두 번 쓰는 셈이고,
// 평소가 아직 없는 동안에는 견줄 대상이 없다. 그동안에는 추정 점수의 절대값만 본다.
//
// # 정확한 계산
//
// 누적값은 소수(float64)가 아니라 분수로 쌓는다. μ는 "관찰된 항목 수의 합 ÷ 대화한 일수"라서
// 7분의 9처럼 이진 소수로 딱 떨어지지 않는 값이 흔하다. 그런 μ를 소수로 빼 가며 쌓으면 정확히 4여야 할 누적값이
// 4.000000000000001이 되고, "H와 같으면 아직 아니다"가 끝자리 오차에 따라 뒤집힌다.
// 기본값에서는 누적값이 언제나 1/(2×대화한 일수)의 배수라 H와 딱 같아지는 날이 드물지 않고,
// 그때마다 감지가 하루 일찍 켜지거나 하루 늦게 풀린다. 감지 여부는 개입 단계와 위기 관문의 판정을 바꾸므로
// 끝자리 오차에 맡기지 않는다.
//
//   - μ는 기준선의 ObservedTotal ÷ Days를 분수 그대로 쓴다. Mu(소수)는 쓰지 않는다.
//   - K, H, MaxStep, MaxS는 사람이 십진수로 적는 값이다. 0.1은 이진 소수로 딱 떨어지지 않지만 적은 사람의 뜻은 10분의 1이다.
//     그래서 float64를 가장 짧은 십진 표기로 되돌린 다음 분수로 읽는다. 손으로 셈한 값과 끝까지 맞는다.
//   - 감지 여부(Detected), 상한에 걸렸는지(Capped), 천장에 걸렸는지(AtCeiling)는 분수끼리 견준 결과다.
//   - Point와 State의 Step, S는 보여주기 위한 값이다. 분수를 가장 가까운 float64로 옮겼다.
//     판정을 다시 하려고 S를 H와 견주지 말고 Detected를 읽는다.
package cusum

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ErrInvalidBaseline은 넘겨받은 기준선으로는 계산할 수 없다는 뜻이다.
// baseline.Compute가 돌려준 값은 걸리지 않는다. 손으로 채운 값이 앞뒤가 맞지 않을 때 나온다.
// 평소의 하루 평균(Mu)이 ObservedTotal ÷ Days와 다른 경우도 여기에 든다.
var ErrInvalidBaseline = errors.New("cusum: baseline is not usable")

// Point는 누적한 날 하루의 기록이다. 내부에서 흐름을 되짚어 볼 때 이 값을 날짜순으로 그대로 보여준다.
type Point struct {
	// Date는 대화한 날이다. 대화하지 않은 날은 Point가 없다.
	Date recorddate.Date
	// Observed는 그날 관찰된 항목 수(x)다.
	Observed int
	// Step은 그날 누적값에 더한 값이다. x − μ − K이고, 상한에 걸렸으면 MaxStep이다.
	// 누적값이 0이나 천장에서 멈춘 날에도 Step은 멈추기 전의 값이다. 보여주기 위한 값이라 가장 가까운 소수로 옮긴 것이다.
	Step float64
	// Capped는 하루 증가량의 상한에 걸려 Step이 줄었는지다. 상한과 딱 같은 날은 줄어든 것이 없으므로 false다.
	Capped bool
	// AtCeiling은 누적값이 천장에 걸려 그날의 Step이 다 쌓이지 못했는지다.
	// 더한 값이 천장과 딱 같은 날은 잘린 것이 없으므로 false다.
	AtCeiling bool
	// S는 그날까지의 누적값이다. 보여주기 위한 값이라 가장 가까운 소수로 옮긴 것이다.
	S float64
	// Detected는 그날 누적값이 한계값을 넘어 있는지다. 소수로 옮기기 전의 정확한 값으로 견준 결과다.
	Detected bool
}

// State는 어느 날짜에 본 변화 탐지의 상태다.
type State struct {
	// Running은 그 날짜에 변화 탐지가 돌고 있는지다. 기준선이 잡히기 전에는 false이고, 그때 S는 0, Detected는 false다.
	Running bool
	// S는 그 날짜까지의 누적값이다. 그날 대화하지 않았으면 마지막으로 대화한 날의 값이 이어진다.
	// 보여주기 위한 값이다. 감지 여부는 이 값을 H와 다시 견주지 말고 Detected에서 읽는다.
	S float64
	// Detected는 변화 감지 상태인지다.
	Detected bool
}

// Result는 기준일까지의 흐름과 기준일의 상태다.
type Result struct {
	// AsOf는 기준일이다. 넘겨받은 기준선의 기준일과 같다.
	AsOf recorddate.Date
	// From은 누적을 시작하는 날짜, 곧 기준선 기간의 마지막 날 다음 날이다. 기준선이 잡히지 않았으면 빈 값이다.
	// 빈 날짜는 JSON으로 적을 수 없어서, 비어 있으면 JSON에서 빠지게 한다.
	From recorddate.Date `json:",omitzero"`
	// Series는 From부터 기준일까지 대화한 날마다의 기록이다. 날짜순이고, 없으면 길이 0이다.
	Series []Point
	// State는 기준일의 상태다. StateAt(AsOf)와 같다.
	State State
}

// StateAt은 그 날짜에 본 상태를 돌려준다. 그 날짜까지 누적한 마지막 날의 값이다.
//
// 개입 단계처럼 첫날부터 하루씩 다시 돌려 보는 계산이 지난 날짜의 상태를 물을 때 쓴다.
// 기준일 이전의 날짜라면, 그 날짜를 기준일로 삼아 처음부터 다시 구한 상태와 같다.
// 기준선은 그 기간이 끝난 뒤의 기록에 영향받지 않기 때문이다.
//
// From보다 앞선 날짜는 아직 돌리기 전이다. 기준일보다 뒤의 날짜를 물으면 기준일의 상태를 돌려준다.
// 그 뒤의 기록은 이 결과에 들어 있지 않다.
func (r Result) StateAt(date recorddate.Date) State {
	if r.From.IsZero() || date.Before(r.From) {
		return State{}
	}
	n := sort.Search(len(r.Series), func(i int) bool {
		return r.Series[i].Date.After(date)
	})
	if n == 0 {
		return State{Running: true}
	}
	last := r.Series[n-1]
	return State{Running: true, S: last.S, Detected: last.Detected}
}

// Run은 기준선 기간의 마지막 날 다음 날부터 기준일까지, 대화한 날마다 누적값을 구한다.
//
// days는 기준선을 구할 때 넘긴 것과 같은 목록이어야 한다(날짜순, signal.ValidateDays를 통과). 고치지 않는다.
// 기준일은 base.AsOf다. 기준일을 따로 받지 않는 것은 기준선을 본 날과 흐름을 보는 날이 어긋나지 않게 하기 위해서다.
// 기준일보다 뒤의 날은 쓰지 않는다.
//
// 기준선이 잡히지 않았으면 빈 흐름과 "돌고 있지 않음" 상태를 돌려준다. 오류가 아니다.
// 오류는 조정 값이 틀렸을 때(*params.FieldError), 기준선의 앞뒤가 맞지 않을 때(ErrInvalidBaseline),
// days가 날짜순이 아니거나 틀린 하루가 있을 때(*signal.DayError)다.
//
// 조정 값은 p.CUSUM만 쓰지만 검사는 p 전체에 한다. NaN 같은 값은 분수로 옮길 수 없고
// 어떤 비교에서도 거짓이라 감지가 영영 울리지 않으므로, 검사를 통과한 값으로만 돌린다.
func Run(days []signal.Day, base baseline.Baseline, p params.Params) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, fmt.Errorf("cusum: %w", err)
	}
	if err := checkBaseline(base); err != nil {
		return Result{}, err
	}
	if err := signal.ValidateDays(days); err != nil {
		return Result{}, fmt.Errorf("cusum: %w", err)
	}

	result := Result{AsOf: base.AsOf, Series: []Point{}}
	if !base.Established {
		return result, nil
	}
	result.From = base.End.AddDays(1)

	table := newStepTable(base, p.CUSUM)
	s := new(big.Rat)
	for _, day := range days {
		if !day.Date.After(base.End) {
			continue
		}
		if day.Date.After(base.AsOf) {
			break
		}

		observed := day.ObservedCount()
		s.Add(s, table.step[observed])
		if s.Sign() < 0 {
			s.SetInt64(0)
		}
		// 천장은 그날의 증가량을 더한 다음에 맞춘다. 천장과 딱 같은 값은 잘린 것이 없으므로 걸린 것으로 치지 않는다.
		atCeiling := table.ceiling != nil && s.Cmp(table.ceiling) > 0
		if atCeiling {
			s.Set(table.ceiling)
		}

		result.Series = append(result.Series, Point{
			Date:      day.Date,
			Observed:  observed,
			Step:      table.shown[observed],
			Capped:    table.capped[observed],
			AtCeiling: atCeiling,
			S:         nearest(s),
			Detected:  s.Cmp(table.limit) > 0,
		})
	}

	result.State = result.StateAt(base.AsOf)
	return result, nil
}

// stepTable은 그날 관찰된 항목 수(0부터 8)마다 누적값에 더할 값을 미리 구해 둔 것이다.
// μ, K, MaxStep은 흐름 내내 같으므로 하루 증가량은 아홉 가지뿐이다. 한계값과 천장도 함께 둔다.
type stepTable struct {
	// step은 상한까지 적용한 하루 증가량이다.
	step [signal.ItemCount + 1]*big.Rat
	// shown은 step을 가장 가까운 소수로 옮긴 값이다.
	shown [signal.ItemCount + 1]float64
	// capped는 상한에 걸려 줄어든 값인지다.
	capped [signal.ItemCount + 1]bool
	// limit은 한계값 H다.
	limit *big.Rat
	// ceiling은 누적값의 천장(MaxS × H)이다. 천장을 두지 않으면 nil이다.
	ceiling *big.Rat
}

func newStepTable(base baseline.Baseline, p params.CUSUM) stepTable {
	// 하루에 빠지는 값(μ + K)이다. μ는 소수로 나눈 Mu가 아니라 정수 둘의 비 그대로 쓴다.
	drift := big.NewRat(int64(base.ObservedTotal), int64(base.Days))
	drift.Add(drift, exactOf(p.K))
	maxStep := exactOf(p.MaxStep)

	table := stepTable{limit: exactOf(p.H)}
	if multiple := exactOf(p.MaxS); multiple.Sign() > 0 {
		table.ceiling = multiple.Mul(multiple, table.limit)
	}
	for x := range table.step {
		step := new(big.Rat).SetInt64(int64(x))
		step.Sub(step, drift)
		// 상한과 딱 같은 값은 줄어드는 것이 없으므로 걸린 것으로 치지 않는다.
		if maxStep.Sign() > 0 && step.Cmp(maxStep) > 0 {
			step.Set(maxStep)
			table.capped[x] = true
		}
		table.step[x] = step
		table.shown[x] = nearest(step)
	}
	return table
}

// exactOf는 조정 값을 사람이 적은 십진수 그대로의 분수로 되돌린다.
// float64의 가장 짧은 십진 표기는 같은 값에서 언제나 같으므로 결과도 언제나 같다.
func exactOf(v float64) *big.Rat {
	if r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'g', -1, 64)); ok {
		return r
	}
	// 유한한 값의 표기는 언제나 읽힌다. 그래도 읽지 못하면 소수에 담긴 값 그대로 쓴다.
	// 조정 값 검사를 통과한 값은 유한하므로 여기서 0이 남는 일은 없다.
	r := new(big.Rat)
	r.SetFloat64(v)
	return r
}

// nearest는 분수를 가장 가까운 소수로 옮긴다. 보여주는 값에만 쓴다.
func nearest(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// checkBaseline은 기준선의 값이 서로 맞는지 본다.
// 잡히지 않은 기준선은 기준일만 있으면 된다. 나머지 값은 쓰지 않는다.
func checkBaseline(base baseline.Baseline) error {
	if base.AsOf.IsZero() {
		return fmt.Errorf("%w: as-of date is missing", ErrInvalidBaseline)
	}
	if !base.Established {
		return nil
	}
	switch {
	case base.End.IsZero():
		return fmt.Errorf("%w: established without an end date", ErrInvalidBaseline)
	case !base.AsOf.After(base.End):
		return fmt.Errorf("%w: established but the as-of date is not after the end date", ErrInvalidBaseline)
	case base.Days < 1:
		return fmt.Errorf("%w: established without conversation days", ErrInvalidBaseline)
	case !finite(base.Mu) || base.Mu < 0 || base.Mu > signal.ItemCount:
		// 하루에 관찰될 수 있는 항목은 0개부터 여덟 개까지다. 평균도 그 밖으로 나갈 수 없다.
		return fmt.Errorf("%w: mean is outside 0..%d", ErrInvalidBaseline, signal.ItemCount)
	case base.Mu != float64(base.ObservedTotal)/float64(base.Days):
		// 누적은 합과 일수로 정확하게 하고 Mu는 보여주기만 한다. 둘이 어긋난 기준선을 받아 주면
		// 화면에 보이는 평소와 계산에 쓴 평소가 달라진다. baseline.Compute와 같은 나눗셈이라 맞는 값은 끝자리까지 같다.
		return fmt.Errorf("%w: mean does not match the observed total over the conversation days", ErrInvalidBaseline)
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
