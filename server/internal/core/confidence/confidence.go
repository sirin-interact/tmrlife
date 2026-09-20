// Package confidence는 추정 점수에 기대어 움직여도 되는지를 기록의 양과 질로 계산한다.
//
// 신뢰도는 AI가 스스로 말하는 확신이 아니다. 점수와 같은 창(기준일을 포함한 최근 기간)의 기록을 세어서 구한다.
//
//   - 기록 충실도: 창 안에서 대화한 일수 ÷ 창의 길이.
//     사흘만 기록한 두 주와 열이틀 기록한 두 주는 같은 점수가 나와도 다르게 다뤄야 한다.
//   - 항목 충족도: 여덟 항목 가운데 창 안에서 한 번이라도 이야기가 나온(관찰됨이든 관찰되지 않음이든) 항목의 비율.
//     이야기가 나오지 않은 항목은 "없었다"가 아니라 "모른다"인데 점수에는 0점으로 들어간다.
//     이 비율이 낮으면 점수가 실제보다 낮게 나왔을 가능성이 크다.
//   - 근거 명시성: 창 안의 관찰됨 판단 가운데 사용자가 직접 말한 것에 근거한 판단의 비율.
//     미루어 짐작한 판단만으로 쌓인 점수는 덜 믿는다.
//
// 최종 값은 세 요소의 평균이 아니라 가장 작은 값이다. 평균을 내면 한 요소가 아주 나빠도 다른 요소에 묻힌다.
// 신뢰도는 섣부른 개입을 막는 장치이므로 가장 약한 고리가 결과를 정하게 한다.
// 말수가 적은 사용자는 날마다 대화해도 항목 충족도가 낮아 신뢰도가 낮음으로 나온다.
// 그러면 개입 단계를 올리는 대신 빠진 항목을 자연스럽게 묻거나 자가 점검을 제안한다.
//
// 창 안에서 대화한 날이 정해진 일수에 못 미치면(기록 부족) 점수와 마찬가지로 최종 값을 계산하지 않고
// 낮음으로 다룬다. 세 요소는 그때도 채워서 돌려준다. 내부 확인 화면에서 무엇이 모자란지 볼 수 있어야 한다.
//
// 기간과 경계는 모두 params에서 받는다. 이 패키지는 숫자를 들고 있지 않다.
package confidence

import (
	"errors"
	"fmt"
	"sort"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

var (
	// ErrNoAsOf는 기준일이 빠졌다는 뜻이다. 현재 시각을 읽지 않으므로 기준일은 언제나 받아야 한다.
	ErrNoAsOf = errors.New("confidence: as-of date is missing")
	// ErrInvalidRange는 Series에 넘긴 기간의 끝이 시작보다 앞선다는 뜻이다.
	ErrInvalidRange = errors.New("confidence: range ends before it starts")
)

// Result는 기준일 하루의 신뢰도와 그 값이 나온 과정이다.
// 내부 확인 화면이 이 값을 그대로 보여줄 수 있도록 센 값과 세 요소를 모두 담는다.
type Result struct {
	// AsOf는 기준일이다. 창은 이날을 포함해 거슬러 센다.
	AsOf recorddate.Date

	// ConversationDays는 창 안에서 대화한 일수다.
	ConversationDays int
	// Mentioned는 항목별로 창 안에서 한 번이라도 이야기가 나왔는지다. 자리는 signal.Item.Index다.
	Mentioned [signal.ItemCount]bool
	// MentionedItems는 Mentioned에서 참인 항목의 수다.
	MentionedItems int
	// ObservedJudgements는 창 안의 관찰됨 판단 수다. 하루의 항목 하나가 판단 하나다.
	ObservedJudgements int
	// DirectJudgements는 그 가운데 직접 언급에 근거한 판단 수다.
	DirectJudgements int

	// RecordCoverage는 기록 충실도다: ConversationDays ÷ 창의 길이.
	RecordCoverage Ratio
	// ItemCoverage는 항목 충족도다: MentionedItems ÷ 항목 수.
	ItemCoverage Ratio
	// Explicitness는 근거 명시성이다: DirectJudgements ÷ ObservedJudgements.
	// 관찰됨이 하나도 없으면 1/1이다. 따질 판단이 없는 것이지 근거가 흐린 것이 아니다.
	// 0으로 두면 별일 없이 지낸 사용자의 신뢰도가 언제나 낮음이 되어 "잘 지내고 있다"는 기록까지 믿지 못하게 된다.
	Explicitness Ratio

	// Insufficient는 기록 부족이라는 뜻이다. 창 안에서 대화한 날이 정해진 일수에 못 미쳤다.
	// 이때 Value는 빈 값, Limiting은 ComponentNone, Level은 Low다.
	Insufficient bool
	// Value는 최종 값이다. 세 요소 가운데 가장 작은 값을 센 값 그대로 담는다.
	Value Ratio
	// Limiting은 Value를 정한 가장 약한 요소다. 값이 같은 요소가 여럿이면
	// 기록 충실도, 항목 충족도, 근거 명시성 순으로 앞의 것을 적는다.
	Limiting Component
	// Level은 구간이다. 개입 단계를 올려도 되는지는 이 값으로 본다.
	Level Level
}

// MissingItems는 창 안에서 한 번도 이야기가 나오지 않은 항목을 정해진 항목 순서로 돌려준다.
// 신뢰도가 낮을 때 다음 대화에서 자연스럽게 물어볼 항목을 고르는 데 쓴다. 없으면 nil이다.
func (r Result) MissingItems() []signal.Item {
	var missing []signal.Item
	for _, item := range signal.AllItems() {
		if !r.Mentioned[item.Index()] {
			missing = append(missing, item)
		}
	}
	return missing
}

// Compute는 기준일의 신뢰도를 계산한다.
//
// days는 분석이 끝난 대화가 있는 날만 담은, 날짜순으로 정렬된 목록이다(signal.ValidateDays를 통과해야 한다).
// 기준일 뒤의 하루는 보지 않는다. 지난 날짜를 기준으로 다시 돌려 볼 때 그날까지의 기록만으로 계산해야 하기 때문이다.
// days는 고치지 않는다.
//
// 오류: 기준일이 빠졌으면 ErrNoAsOf, 조정 값이 틀렸으면 *params.FieldError를 감싼 오류,
// days가 틀렸으면 *signal.DayError를 감싼 오류다. 기록 부족은 오류가 아니라 Result.Insufficient다.
func Compute(days []signal.Day, asOf recorddate.Date, p params.Params) (Result, error) {
	if asOf.IsZero() {
		return Result{}, ErrNoAsOf
	}
	if err := checkInputs(days, p); err != nil {
		return Result{}, err
	}
	return compute(days, asOf, p), nil
}

// Series는 from부터 to까지(둘 다 포함) 달력의 하루하루를 기준일로 삼은 신뢰도를 날짜순으로 돌려준다.
// 대화하지 않은 날도 하루로 센다. 창이 움직이면 대화한 일수가 바뀌기 때문이다.
//
// 개입 단계처럼 첫날부터 하루씩 다시 돌리는 계산과 날짜별 흐름을 보여주는 화면을 위한 것이다.
// 결과는 날짜마다 Compute를 부른 것과 같고, 입력 검사를 한 번만 한다.
func Series(days []signal.Day, from, to recorddate.Date, p params.Params) ([]Result, error) {
	if from.IsZero() || to.IsZero() {
		return nil, ErrNoAsOf
	}
	if to.Before(from) {
		return nil, ErrInvalidRange
	}
	if err := checkInputs(days, p); err != nil {
		return nil, err
	}

	count := to.DaysSince(from) + 1
	results := make([]Result, 0, count)
	// 날짜를 하루씩 더해 가며 to와 견주지 않고 횟수로 돈다.
	// 지원 범위의 마지막 날에서 하루를 더하면 빈 날짜가 나오는데, 빈 날짜는 모든 날짜보다 앞서서 반복이 끝나지 않는다.
	for offset := range count {
		results = append(results, compute(days, from.AddDays(offset), p))
	}
	return results, nil
}

func checkInputs(days []signal.Day, p params.Params) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	if err := signal.ValidateDays(days); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	return nil
}

// compute는 검사를 마친 입력으로 계산한다. days는 날짜순이어야 한다.
func compute(days []signal.Day, asOf recorddate.Date, p params.Params) Result {
	result := Result{AsOf: asOf}

	// 창은 기준일을 포함한 Window.Days일이다. 길이가 14이면 13일 전이 창의 첫날이고 14일 전은 창 밖이다.
	// 창의 첫날을 날짜로 만들지 않고 기준일과의 거리로 따진다. 날짜를 거슬러 가다 지원 범위를 벗어나면
	// 빈 날짜가 나오는데, 빈 날짜는 모든 날짜보다 앞서서 창이 끝없이 과거로 열린다.
	first := sort.Search(len(days), func(i int) bool {
		return asOf.DaysSince(days[i].Date) < p.Window.Days
	})
	for _, day := range days[first:] {
		if day.Date.After(asOf) {
			break
		}
		result.ConversationDays++
		for idx, judgement := range day.Judgements {
			if judgement.Status.Mentioned() {
				result.Mentioned[idx] = true
			}
			if judgement.Status == signal.Observed {
				result.ObservedJudgements++
				if judgement.Explicitness == signal.Direct {
					result.DirectJudgements++
				}
			}
		}
	}
	for _, mentioned := range result.Mentioned {
		if mentioned {
			result.MentionedItems++
		}
	}

	result.RecordCoverage = Ratio{Num: result.ConversationDays, Den: p.Window.Days}
	result.ItemCoverage = Ratio{Num: result.MentionedItems, Den: signal.ItemCount}
	result.Explicitness = Ratio{Num: 1, Den: 1}
	if result.ObservedJudgements > 0 {
		result.Explicitness = Ratio{Num: result.DirectJudgements, Den: result.ObservedJudgements}
	}

	// 기록 부족이면 최종 값을 내지 않는다. 엿새 치 기록이 고르기만 하면 6/14로 보통 구간에 드는데,
	// 점수조차 계산하지 않는 기록을 두고 신뢰도가 보통이라고 말하면 안 된다.
	if result.ConversationDays < p.Window.MinConversationDays {
		result.Insufficient = true
		result.Level = Low
		return result
	}

	result.Value, result.Limiting = weakest(result)
	result.Level = levelOf(result.Value, p.Confidence)
	return result
}

// weakest는 세 요소 가운데 가장 작은 값과 그 요소를 돌려준다.
// 값이 같으면 앞의 요소가 남는다. 순서를 고정해 두어야 같은 기록에서 언제나 같은 요소가 적힌다.
func weakest(r Result) (Ratio, Component) {
	value, limiting := r.RecordCoverage, ComponentRecordCoverage
	if r.ItemCoverage.Compare(value) < 0 {
		value, limiting = r.ItemCoverage, ComponentItemCoverage
	}
	if r.Explicitness.Compare(value) < 0 {
		value, limiting = r.Explicitness, ComponentExplicitness
	}
	return value, limiting
}

// levelOf는 최종 값을 구간으로 옮긴다. 경계는 "이상"이다: MediumMin 미만이면 낮음, HighMin 이상이면 높음.
//
// 경계가 소수로 주어지므로 여기서만 소수끼리 견준다. 경계에 딱 걸린 값(2/5와 0.4, 7/10과 0.7)이
// 오차 때문에 아래 구간으로 떨어지지 않는 까닭은 Ratio.Float64에 적었다.
// 분수와 경계가 같지 않을 때는 걱정할 것이 없다. 분모가 백 남짓이고 경계는 소수 몇 자리로 적은 값이라,
// 둘의 차이가 소수 오차(1e-16쯤)와는 견줄 수 없이 크다.
func levelOf(value Ratio, cut params.Confidence) Level {
	v := value.Float64()
	switch {
	case v >= cut.HighMin:
		return High
	case v >= cut.MediumMin:
		return Medium
	default:
		return Low
	}
}
