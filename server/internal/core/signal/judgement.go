package signal

import (
	"fmt"
	"strconv"
)

// Status는 한 항목에 대한 판단이다.
//
// 숫자가 클수록 하루로 합칠 때 앞선다: 관찰됨 > 관찰되지 않음 > 언급 없음.
// 빈 값이 언급 없음이라서, 채우지 않은 판단은 "아무 말도 없었다"로 읽힌다.
type Status int

// 순서가 곧 합칠 때의 우선순위다. 바꾸면 MergeDay의 결과가 달라진다.
const (
	// NotMentioned는 그 항목에 대한 이야기가 없었다는 뜻이다. "없었다"가 아니라 "모른다"에 가깝다.
	NotMentioned Status = iota
	// NotObserved는 이야기가 나왔고 괜찮았다는 뜻이다("어젯밤엔 푹 잤어").
	NotObserved
	// Observed는 그 항목의 신호가 있었다는 뜻이다.
	Observed
)

// ParseStatus는 저장된 식별자(observed, not_observed, not_mentioned)를 읽는다.
func ParseStatus(id string) (Status, error) {
	for _, s := range [...]Status{NotMentioned, NotObserved, Observed} {
		if s.String() == id {
			return s, nil
		}
	}
	return NotMentioned, fmt.Errorf("%w: unknown id", ErrInvalidStatus)
}

// Valid는 세 판단 가운데 하나인지 알려준다.
func (s Status) Valid() bool {
	return s >= NotMentioned && s <= Observed
}

// Mentioned는 그 항목이 대화에 나왔는지(관찰됨이든 관찰되지 않음이든) 알려준다.
// 기록이 여덟 항목을 얼마나 덮고 있는지 셀 때 쓴다.
func (s Status) Mentioned() bool {
	return s == Observed || s == NotObserved
}

// String은 저장에 쓰는 식별자다. 판단이 아닌 값은 Status(n) 꼴로 적는다.
func (s Status) String() string {
	switch s {
	case Observed:
		return "observed"
	case NotObserved:
		return "not_observed"
	case NotMentioned:
		return "not_mentioned"
	default:
		return "Status(" + strconv.Itoa(int(s)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 판단이 아닌 값은 오류다.
func (s Status) MarshalText() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidStatus)
	}
	return []byte(s.String()), nil
}

// UnmarshalText는 ParseStatus와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (s *Status) UnmarshalText(text []byte) error {
	parsed, err := ParseStatus(string(text))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// Explicitness는 판단의 근거가 얼마나 분명했는지다.
//
// 숫자가 클수록 분명하다: 직접 언급 > 간접 추론 > 없음. 빈 값은 없음이다.
type Explicitness int

// 순서가 곧 합칠 때의 우선순위다. 바꾸면 MergeDay의 결과가 달라진다.
const (
	// None은 근거가 없다는 뜻이다. 언급 없음인 판단에만 붙는다.
	None Explicitness = iota
	// Indirect는 사용자의 말에서 미루어 짐작한 판단이다.
	Indirect
	// Direct는 사용자가 직접 말한 것에 근거한 판단이다("새벽 4시까지 뒤척였어").
	Direct
)

// ParseExplicitness는 저장된 식별자(direct, indirect, none)를 읽는다.
func ParseExplicitness(id string) (Explicitness, error) {
	for _, e := range [...]Explicitness{None, Indirect, Direct} {
		if e.String() == id {
			return e, nil
		}
	}
	return None, fmt.Errorf("%w: unknown id", ErrInvalidExplicitness)
}

// Valid는 세 값 가운데 하나인지 알려준다.
func (e Explicitness) Valid() bool {
	return e >= None && e <= Direct
}

// String은 저장에 쓰는 식별자다. 정해진 값이 아니면 Explicitness(n) 꼴로 적는다.
func (e Explicitness) String() string {
	switch e {
	case Direct:
		return "direct"
	case Indirect:
		return "indirect"
	case None:
		return "none"
	default:
		return "Explicitness(" + strconv.Itoa(int(e)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
func (e Explicitness) MarshalText() ([]byte, error) {
	if !e.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidExplicitness)
	}
	return []byte(e.String()), nil
}

// UnmarshalText는 ParseExplicitness와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (e *Explicitness) UnmarshalText(text []byte) error {
	parsed, err := ParseExplicitness(string(text))
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

// Judgement는 한 항목에 대한 판단과 그 근거의 분명함이다.
// 빈 값은 "언급 없음, 근거 없음"이다.
type Judgement struct {
	Status       Status
	Explicitness Explicitness
}

// Validate는 판단과 명시성이 서로 맞는지 본다.
//
// 근거 없는 판단은 기록하지 않는다. 그래서 관찰됨과 관찰되지 않음은 직접 언급이나 간접 추론이어야 하고,
// 언급 없음에는 근거가 있을 수 없다. 저장할 때 거는 제약과 같은 규칙이다.
// 여기서 한 번 더 보는 이유: 근거 없는 관찰됨이 섞이면 "직접 언급의 비율"이 조용히 틀어진다.
func (j Judgement) Validate() error {
	if !j.Status.Valid() {
		return ErrInvalidStatus
	}
	if !j.Explicitness.Valid() {
		return ErrInvalidExplicitness
	}
	if j.Status.Mentioned() != (j.Explicitness != None) {
		return fmt.Errorf("%w: %s with %s", ErrInconsistentJudgement, j.Status, j.Explicitness)
	}
	return nil
}
