package stage

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalidReason은 정해진 조건이 아닌 값이라는 뜻이다. 메시지에는 입력받은 글자를 담지 않는다.
var ErrInvalidReason = errors.New("stage: invalid reason")

// Reason은 그날의 단계를 그 값으로 만든 조건 하나다.
//
// 단계를 실제로 움직인 조건만 적는다. 변화 감지 상태였어도 점수만으로 이미 1단계 이상이면
// 변화 감지는 단계를 바꾸지 않았으므로 적지 않는다. 그날의 변화 감지 여부는 Point.Detected에 따로 있다.
// 식별자에는 점수의 경계나 일수 같은 조정 값을 넣지 않는다. 값을 바꿔도 이름이 거짓이 되지 않는다.
// 식별자는 밖에 그대로 적히므로 한번 정하면 바꾸지 않는다. 빈 값(0)은 어떤 조건도 아니다.
type Reason int

// 순서가 곧 규칙의 순서다. Point.Reasons도 이 순서를 따른다.
const (
	// ReasonScore는 추정 점수가 단계를 1단계 이상으로 올렸다는 뜻이다.
	ReasonScore Reason = iota + 1
	// ReasonChangeDetected는 점수로는 0단계인데 변화 감지 상태여서 1단계가 되었다는 뜻이다.
	ReasonChangeDetected
	// ReasonSustained는 2단계 이상이 정해진 일수째 이어져 3단계가 되었다는 뜻이다.
	ReasonSustained
	// ReasonHeldNoRecordToday는 단계가 오르려 했으나 그날 대화한 기록이 없어서 전날의 단계에 묶였다는 뜻이다.
	// 다음에 대화한 날에 그날의 기록까지 넣어 다시 본다.
	ReasonHeldNoRecordToday
	// ReasonHeldOneStepPerDay는 단계가 두 단계 이상 오르려 했으나 하루에 한 단계만 올랐다는 뜻이다.
	// 이 조건이 적힌 날의 단계는 전날보다 한 단계 높다.
	ReasonHeldOneStepPerDay
	// ReasonHeldLowConfidence는 단계가 오르려 했으나 신뢰도가 낮아서 전날의 단계에 묶였다는 뜻이다.
	// 빠진 항목을 자연스럽게 묻거나 자가 점검을 제안할 때다.
	ReasonHeldLowConfidence
	// ReasonHeldInsufficientRecords는 단계가 오르려 했으나 기록 부족이어서 전날의 단계에 묶였다는 뜻이다.
	// 기록 부족인 날에는 점수가 없으므로, 오르려던 까닭은 변화 감지뿐이다.
	ReasonHeldInsufficientRecords
	// ReasonCarriedInsufficientRecords는 기록 부족이어서 전날의 단계를 그대로 이어 갔다는 뜻이다.
	// 그날의 값만으로 본 단계(Point.Raw)가 전날의 단계보다 낮을 때 적는다. 이어 가지 않았다면 내려갔을 날이다.
	ReasonCarriedInsufficientRecords
	// ReasonNoRecentRecords는 창 안에 대화한 날이 하나도 없어서 0단계로 돌아갔다는 뜻이다.
	// 이 조건이 적힌 날에는 다른 조건을 따지지 않는다.
	ReasonNoRecentRecords
)

// ReasonCount는 조건의 수다.
const ReasonCount = 9

// AllReasons는 조건을 규칙의 순서대로 돌려준다.
func AllReasons() [ReasonCount]Reason {
	return [ReasonCount]Reason{
		ReasonScore,
		ReasonChangeDetected,
		ReasonSustained,
		ReasonHeldNoRecordToday,
		ReasonHeldOneStepPerDay,
		ReasonHeldLowConfidence,
		ReasonHeldInsufficientRecords,
		ReasonCarriedInsufficientRecords,
		ReasonNoRecentRecords,
	}
}

// held는 단계가 오르려던 만큼 오르지 못한 까닭을 적는 조건인지 알려준다.
func (r Reason) held() bool {
	return r >= ReasonHeldNoRecordToday && r <= ReasonHeldInsufficientRecords
}

// ParseReason은 식별자를 읽는다.
func ParseReason(id string) (Reason, error) {
	for _, r := range AllReasons() {
		if r.String() == id {
			return r, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown id", ErrInvalidReason)
}

// Valid는 정해진 조건 가운데 하나인지 알려준다.
func (r Reason) Valid() bool {
	return r >= ReasonScore && r <= ReasonNoRecentRecords
}

// String은 밖으로 내보낼 때 쓰는 식별자다. 조건이 아닌 값은 Reason(n) 꼴로 적는다.
func (r Reason) String() string {
	switch r {
	case ReasonScore:
		return "score"
	case ReasonChangeDetected:
		return "change_detected"
	case ReasonSustained:
		return "sustained"
	case ReasonHeldNoRecordToday:
		return "held_no_record_today"
	case ReasonHeldOneStepPerDay:
		return "held_one_step_per_day"
	case ReasonHeldLowConfidence:
		return "held_low_confidence"
	case ReasonHeldInsufficientRecords:
		return "held_insufficient_records"
	case ReasonCarriedInsufficientRecords:
		return "carried_insufficient_records"
	case ReasonNoRecentRecords:
		return "no_recent_records"
	default:
		return "Reason(" + strconv.Itoa(int(r)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 조건이 아닌 값은 오류다.
func (r Reason) MarshalText() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidReason)
	}
	return []byte(r.String()), nil
}

// UnmarshalText는 ParseReason과 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (r *Reason) UnmarshalText(text []byte) error {
	parsed, err := ParseReason(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
