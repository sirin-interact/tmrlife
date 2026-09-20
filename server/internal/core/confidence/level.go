package confidence

import (
	"errors"
	"fmt"
	"strconv"
)

// 식별자를 읽지 못했을 때의 오류다. 메시지에는 입력받은 글자를 담지 않는다.
var (
	ErrInvalidLevel     = errors.New("confidence: invalid level")
	ErrInvalidComponent = errors.New("confidence: invalid component")
)

// Level은 신뢰도의 구간이다.
//
// 숫자가 클수록 믿을 만하다: 낮음 < 보통 < 높음. `level >= Medium`처럼 견줄 수 있다.
// 빈 값이 낮음이다. 채우지 않은 신뢰도는 "믿고 움직일 근거가 없다"로 읽혀서,
// 계산을 빠뜨린 실수가 개입 단계를 올리는 쪽으로 새지 않는다.
type Level int

// 순서가 곧 크기다. 바꾸면 `level >= Medium` 같은 비교의 뜻이 달라진다.
const (
	// Low는 기록이 점수를 받치지 못한다는 뜻이다. 개입 단계를 올리지 않고,
	// 빠진 항목을 자연스럽게 묻거나 자가 점검을 제안하는 쪽으로 간다. 기록 부족도 낮음으로 다룬다.
	Low Level = iota
	// Medium은 점수에 기대어 움직여도 되는 가장 낮은 구간이다.
	Medium
	// High는 기록이 고르게 쌓여 있다는 뜻이다.
	High
)

// ParseLevel은 식별자(low, medium, high)를 읽는다.
func ParseLevel(id string) (Level, error) {
	for _, l := range [...]Level{Low, Medium, High} {
		if l.String() == id {
			return l, nil
		}
	}
	return Low, fmt.Errorf("%w: unknown id", ErrInvalidLevel)
}

// Valid는 세 구간 가운데 하나인지 알려준다.
func (l Level) Valid() bool {
	return l >= Low && l <= High
}

// String은 구간의 식별자다. 구간이 아닌 값은 Level(n) 꼴로 적는다.
func (l Level) String() string {
	switch l {
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	default:
		return "Level(" + strconv.Itoa(int(l)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 구간이 아닌 값은 오류다.
func (l Level) MarshalText() ([]byte, error) {
	if !l.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidLevel)
	}
	return []byte(l.String()), nil
}

// UnmarshalText는 ParseLevel과 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (l *Level) UnmarshalText(text []byte) error {
	parsed, err := ParseLevel(string(text))
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

// Component는 신뢰도를 이루는 세 요소 가운데 하나를 가리킨다.
// 최종 값을 정한 가장 약한 요소가 무엇인지 적는 데 쓴다.
//
// 빈 값은 ComponentNone이다. 기록 부족으로 최종 값을 계산하지 않았을 때 그렇다.
type Component int

const (
	// ComponentNone은 최종 값을 계산하지 않아 가리킬 요소가 없다는 뜻이다.
	ComponentNone Component = iota
	// ComponentRecordCoverage는 기록 충실도(창 안에서 대화한 일수의 비율)다.
	ComponentRecordCoverage
	// ComponentItemCoverage는 항목 충족도(한 번이라도 이야기가 나온 항목의 비율)다.
	ComponentItemCoverage
	// ComponentExplicitness는 근거 명시성(관찰됨 판단 가운데 직접 언급의 비율)이다.
	ComponentExplicitness
)

// ParseComponent는 식별자(none, record_coverage, item_coverage, explicitness)를 읽는다.
func ParseComponent(id string) (Component, error) {
	for _, c := range [...]Component{
		ComponentNone, ComponentRecordCoverage, ComponentItemCoverage, ComponentExplicitness,
	} {
		if c.String() == id {
			return c, nil
		}
	}
	return ComponentNone, fmt.Errorf("%w: unknown id", ErrInvalidComponent)
}

// Valid는 정해진 값 가운데 하나인지 알려준다. ComponentNone도 정해진 값이다.
func (c Component) Valid() bool {
	return c >= ComponentNone && c <= ComponentExplicitness
}

// String은 요소의 식별자다. 정해진 값이 아니면 Component(n) 꼴로 적는다.
func (c Component) String() string {
	switch c {
	case ComponentNone:
		return "none"
	case ComponentRecordCoverage:
		return "record_coverage"
	case ComponentItemCoverage:
		return "item_coverage"
	case ComponentExplicitness:
		return "explicitness"
	default:
		return "Component(" + strconv.Itoa(int(c)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
func (c Component) MarshalText() ([]byte, error) {
	if !c.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidComponent)
	}
	return []byte(c.String()), nil
}

// UnmarshalText는 ParseComponent와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (c *Component) UnmarshalText(text []byte) error {
	parsed, err := ParseComponent(string(text))
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}
