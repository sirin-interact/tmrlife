package crisis

import (
	"fmt"
	"strconv"
)

// Adjustment는 두 판정을 합친 뒤에 단계를 바꾼 규칙 하나다.
//
// 식별자는 판정 기록에 그대로 저장되므로 한번 정하면 바꾸지 않는다.
// 횟수나 기간 같은 조정 값을 식별자에 넣지 않은 것도 그래서다. 값을 바꿔도 지난 기록의 이름이 거짓이 되지 않는다.
// 빈 값(0)은 어떤 규칙도 아니다.
type Adjustment int

// 순서가 곧 적용 순서다. Decide가 돌려주는 목록도 이 순서를 따른다.
const (
	// AdjustAIFailedFloor는 AI 판별이 실패했거나 제때 오지 않았는데 규칙에는 걸린 발화를 확인 단계로 올린 것이다.
	// 한쪽 눈을 감은 채로 본 말을 "아무것도 아님"으로 넘기지 않는다.
	AdjustAIFailedFloor Adjustment = iota + 1
	// AdjustBadStatePlusOne은 추정 점수가 기준 이상이거나 변화 감지 상태여서 한 단계 올린 것이다.
	// AdjustAIFailedFloor만으로 확인 단계가 된 발화에는 적용하지 않으므로, 그 둘이 함께 적히는 일은
	// 규칙의 단계를 읽지 못한 입력에서만 있다.
	AdjustBadStatePlusOne
	// AdjustRepeatAfterDirectAsk는 같은 대화에서 이미 직접 물었는데 확인 단계의 표현이 다시 나와 대응 단계로 올린 것이다.
	AdjustRepeatAfterDirectAsk
	// AdjustRepeatedInWindow는 최근 기간 안에 관문에 걸린 대화가 이번 대화를 포함해 정해진 수만큼 쌓여 대응 단계로 올린 것이다.
	AdjustRepeatedInWindow
	// AdjustSensitiveWindow는 대응 단계 이상이 있은 지 얼마 되지 않아 확인 단계의 표현도 대응 단계로 올린 것이다.
	AdjustSensitiveWindow
)

// AdjustmentCount는 규칙의 수다.
const AdjustmentCount = 5

// AllAdjustments는 규칙을 적용 순서대로 돌려준다.
func AllAdjustments() [AdjustmentCount]Adjustment {
	return [AdjustmentCount]Adjustment{
		AdjustAIFailedFloor,
		AdjustBadStatePlusOne,
		AdjustRepeatAfterDirectAsk,
		AdjustRepeatedInWindow,
		AdjustSensitiveWindow,
	}
}

// ParseAdjustment는 저장된 식별자를 읽는다.
func ParseAdjustment(id string) (Adjustment, error) {
	for _, a := range AllAdjustments() {
		if a.String() == id {
			return a, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown id", ErrInvalidAdjustment)
}

// Valid는 다섯 규칙 가운데 하나인지 알려준다.
func (a Adjustment) Valid() bool {
	return a >= AdjustAIFailedFloor && a <= AdjustSensitiveWindow
}

// String은 저장에 쓰는 식별자다. 규칙이 아닌 값은 Adjustment(n) 꼴로 적는다.
func (a Adjustment) String() string {
	switch a {
	case AdjustAIFailedFloor:
		return "ai_failed_floor"
	case AdjustBadStatePlusOne:
		return "bad_state_plus_one"
	case AdjustRepeatAfterDirectAsk:
		return "repeat_after_direct_ask"
	case AdjustRepeatedInWindow:
		return "repeated_in_window"
	case AdjustSensitiveWindow:
		return "sensitive_window"
	default:
		return "Adjustment(" + strconv.Itoa(int(a)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 규칙이 아닌 값은 오류다.
func (a Adjustment) MarshalText() ([]byte, error) {
	if !a.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidAdjustment)
	}
	return []byte(a.String()), nil
}

// UnmarshalText는 ParseAdjustment와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (a *Adjustment) UnmarshalText(text []byte) error {
	parsed, err := ParseAdjustment(string(text))
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}
