// Package score는 최근 기간의 기록을 여덟 항목 척도의 추정 점수(0부터 24)로 옮긴다.
//
// 척도는 "지난 2주 동안 얼마나 자주"를 항목마다 0부터 3으로 묻는다.
// 기록에서는 항목이 관찰된 일수를 세어 같은 물음에 답한다.
// 다만 대화하지 않은 날을 "신호 없음"으로 세면 점수가 구조적으로 낮아진다.
// 그래서 관찰된 일수를 대화한 일수로 나눠 창의 길이에 맞춰 환산한 다음 점수로 옮긴다.
//
// 이 값은 정식 자가 점검의 결과가 아니라 대화 기록에서 추정한 값이다.
// 대화한 날에도 사용자가 꺼내지 않은 항목은 없는 것처럼 세어지므로 실제보다 낮을 수 있다.
// 사용자 화면에는 숫자로 보여주지 않고, 개입 단계와 위기 관문의 판정, 개발용 화면에서만 쓴다.
//
// 소수를 쓰지 않는다. 반올림까지 정수로 하기 때문에 같은 기록에서는 어디서 돌려도 같은 값이 나온다.
package score

import (
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

const (
	// MaxItemPoints는 항목 하나가 받을 수 있는 가장 높은 점수다.
	// 척도가 항목마다 0부터 3까지로 정해져 있어서 조정 값이 아니다.
	MaxItemPoints = 3
	// MaxTotal은 추정 점수가 닿을 수 있는 가장 높은 값(24)이다.
	MaxTotal = signal.ItemCount * MaxItemPoints
)

// ItemResult는 항목 하나가 어떻게 점수가 되었는지를 단계별로 담는다.
// 점수가 왜 그렇게 나왔는지 되짚어 볼 수 있도록 중간값을 버리지 않는다.
type ItemResult struct {
	Item signal.Item
	// ObservedDays는 창 안에서 그 항목이 관찰된 일수다. 기록 부족이어도 센다.
	ObservedDays int
	// ConvertedDays는 관찰된 일수를 창의 길이에 맞춰 환산한 일수다. 기록 부족이면 구하지 않고 0으로 둔다.
	ConvertedDays int
	// Points는 항목 점수(0부터 3)다. 기록 부족이면 구하지 않고 0으로 둔다.
	Points int
}

// Result는 기준일 하루의 추정 점수와 그 값이 나온 과정이다.
type Result struct {
	// Window는 들여다본 기간이다. To가 기준일이다.
	Window Window
	// ConversationDays는 창 안에서 대화한 일수다.
	ConversationDays int

	// Insufficient는 대화한 날이 모자라 점수를 내지 않았다는 뜻이다.
	//
	// 0점과는 다른 상태다. 0점은 "충분히 들었고 신호가 없었다"이고, 기록 부족은 "모른다"이다.
	// 참이면 Total, Band, 항목별 환산 일수와 점수는 구하지 않은 값이므로 읽지 않는다.
	// 점수가 필요한 쪽은 Score를 써서 두 경우를 나눠 받는다.
	Insufficient bool

	// Items의 자리는 signal.Item.Index다.
	Items [signal.ItemCount]ItemResult
	// Total은 여덟 항목 점수의 합(0부터 24)이다.
	Total int
	// Band는 Total이 드는 구간이다. 기록 부족이면 NoBand다.
	Band Band
}

// Score는 추정 점수를 돌려준다. 기록 부족이면 ok가 거짓이다.
// 기록 부족을 0점으로 잘못 읽지 않도록, 점수만 필요한 쪽은 Total 대신 이 메서드를 쓴다.
func (r Result) Score() (total int, ok bool) {
	if r.Insufficient {
		return 0, false
	}
	return r.Total, true
}

// Item은 그 항목의 결과다. 항목이 아닌 값을 넘기면 빈 값을 돌려준다.
func (r Result) Item(item signal.Item) ItemResult {
	idx := item.Index()
	if idx < 0 {
		return ItemResult{}
	}
	return r.Items[idx]
}

// Compute는 asOf를 기준일로 하는 추정 점수를 구한다.
//
//   - 창은 기준일을 포함한 최근 p.Window.Days일이다.
//   - 창 안에서 대화한 일수 n이 p.Window.MinConversationDays보다 적으면 기록 부족이다.
//     사흘 치 기록을 2주로 늘려 읽으면 하루의 기복이 2주의 상태처럼 보이기 때문에 점수를 내지 않는다.
//   - 항목마다 창 안에서 관찰된 일수 o를 세고, 창의 길이 × o ÷ n을 반올림해 환산 일수로 삼는다.
//     관찰되지 않음과 언급 없음은 o에 들지 않지만, 그런 날도 대화한 날이므로 n에는 든다.
//   - 환산 일수를 항목 점수(0부터 3)로 옮기고, 여덟 항목을 더한 값이 추정 점수다.
//
// days는 날짜순이어야 하고 같은 날짜가 둘이면 안 된다(signal.ValidateDays). 순서를 모르면 signal.SortDays를 먼저 거친다.
// 창 밖의 날짜가 섞여 있어도 된다. 창보다 앞선 날과 기준일 뒤의 날은 보지 않는다.
// 기준일 뒤를 버리는 것은 지난 날짜의 점수를 그날 알 수 있었던 기록만으로 다시 구하기 위해서다.
//
// days는 고치지 않는다.
func Compute(days []signal.Day, asOf recorddate.Date, p params.Params) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, fmt.Errorf("score: %w", err)
	}
	window, err := NewWindow(asOf, p.Window.Days)
	if err != nil {
		return Result{}, err
	}
	// 같은 날짜가 둘이면 대화한 일수와 관찰된 일수가 조용히 부풀려진다. 세기 전에 거른다.
	if err := signal.ValidateDays(days); err != nil {
		return Result{}, fmt.Errorf("score: %w", err)
	}

	result := Result{Window: window}
	for idx, item := range signal.AllItems() {
		result.Items[idx].Item = item
	}

	for _, day := range days {
		if day.Date.After(window.To) {
			// 날짜순이므로 이 뒤는 모두 기준일 뒤다.
			break
		}
		if !window.Contains(day.Date) {
			continue
		}
		result.ConversationDays++
		for idx, judgement := range day.Judgements {
			if judgement.Status == signal.Observed {
				result.Items[idx].ObservedDays++
			}
		}
	}

	if result.ConversationDays < p.Window.MinConversationDays {
		result.Insufficient = true
		return result, nil
	}

	for idx := range result.Items {
		item := &result.Items[idx]
		item.ConvertedDays = convertedDays(item.ObservedDays, result.ConversationDays, p.Window.Days)
		item.Points = itemPoints(item.ConvertedDays, p.Score)
		result.Total += item.Points
	}
	result.Band = bandFor(result.Total, p.Score)
	return result, nil
}

// convertedDays는 관찰된 일수를 창의 길이에 맞춰 환산한다. windowDays × observed ÷ conversationDays를 반올림한 값이다.
//
// 반올림은 0.5에서 올린다. x + 1/2의 내림이므로 (2 × windowDays × observed + conversationDays) ÷ (2 × conversationDays)의 몫과 같다.
// 소수로 계산하면 3.5 같은 값이 표현 오차로 3.4999…가 되어 기계마다 다르게 떨어질 수 있다. 정수로만 구하면 그럴 일이 없다.
//
// 대화한 날은 창의 길이를 넘지 못하므로, 한 번이라도 관찰된 항목의 환산 일수는 1 이상이다.
// 관찰된 항목이 반올림 때문에 0일로 사라지는 일은 없다.
func convertedDays(observed, conversationDays, windowDays int) int {
	// 기록 부족을 먼저 거르기 때문에 여기까지 0이 오지는 않는다. 그래도 0으로 나눠 멈추는 일은 없게 한다.
	if conversationDays <= 0 {
		return 0
	}
	return (2*windowDays*observed + conversationDays) / (2 * conversationDays)
}

// itemPoints는 환산 일수를 항목 점수로 옮긴다. 경계는 "이 일수 이상"이다.
// 기본값에서는 0일이 0점(전혀 없음), 1~6일이 1점(며칠 동안), 7~11일이 2점(일주일 이상), 12~14일이 3점(거의 매일)이다.
func itemPoints(converted int, s params.Score) int {
	switch {
	case converted >= s.ItemScore3MinDays:
		return MaxItemPoints
	case converted >= s.ItemScore2MinDays:
		return 2
	case converted >= s.ItemScore1MinDays:
		return 1
	default:
		return 0
	}
}
