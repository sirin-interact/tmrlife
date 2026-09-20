package crisis

import (
	"fmt"
	"strconv"
)

// Stage는 위기 관문이 발화 하나에 매기는 단계다.
//
// 숫자가 클수록 무겁다. 두 판정 가운데 높은 쪽을 따를 때 이 순서를 그대로 쓴다.
// 빈 값은 해당 없음이다. 저장할 때도 JSON에서도 숫자(0부터 3) 그대로 적는다.
type Stage int

// 순서가 곧 무게다. 바꾸면 Decide의 결과가 달라진다.
const (
	// StageNone은 해당 없음이다. 죽음에 관한 낱말이 상태를 강조하는 관용 표현으로 쓰였거나
	// 다른 사람, 작품, 뉴스 이야기인 경우다. 평소 대화를 이어간다.
	StageNone Stage = iota
	// StageCheck는 확인이다. 막연히 사라지고 싶다는 말처럼 여러 뜻으로 읽히는 표현이다.
	// 먼저 되물어 더 듣고, 그래도 애매하면 한 번 직접 묻는다.
	StageCheck
	// StageRespond는 대응이다. 지금의 생각이나 행동을 직접 말했고 방법이나 계획은 없는 경우다.
	// 하던 대화를 멈추고 즉시 대응으로 넘어간다.
	StageRespond
	// StageUrgent는 긴급이다. 방법, 계획, 시점, 준비, 작별 인사 가운데 하나라도 있는 경우다.
	StageUrgent
)

// StageFromInt는 저장된 숫자나 AI 판별이 돌려준 숫자를 단계로 읽는다.
//
// 0부터 3이 아니면 오류다. AI 판별의 답이 여기서 걸리면 그 답은 버리고 AIFailed로 넘겨야 한다.
// 읽지 못한 답을 "해당 없음"으로 읽으면 걸러야 할 말을 조용히 통과시키게 된다.
func StageFromInt(n int) (Stage, error) {
	stage := Stage(n)
	if !stage.Valid() {
		return StageNone, fmt.Errorf("%w: out of range", ErrInvalidStage)
	}
	return stage, nil
}

// Valid는 네 단계 가운데 하나인지 알려준다.
func (s Stage) Valid() bool {
	return s >= StageNone && s <= StageUrgent
}

// String은 사람이 읽는 이름이다. 저장에는 숫자를 쓰므로 이 이름은 시험 출력과 디버깅에만 나온다.
func (s Stage) String() string {
	switch s {
	case StageNone:
		return "none"
	case StageCheck:
		return "check"
	case StageRespond:
		return "respond"
	case StageUrgent:
		return "urgent"
	default:
		return "Stage(" + strconv.Itoa(int(s)) + ")"
	}
}

// DetectedBy는 규칙과 AI 판별 가운데 어느 쪽이 그 발화를 잡았는지다.
//
// 나중에 "규칙은 놓치고 AI만 잡은 말", "AI는 넘기고 규칙만 잡은 말"을 가려 보려고 남긴다.
// 빈 값은 어느 쪽도 잡지 않았다는 뜻이다.
type DetectedBy int

const (
	// DetectedByNone은 어느 쪽도 잡지 않았다는 뜻이다. 최종 단계가 해당 없음일 때만 나온다.
	DetectedByNone DetectedBy = iota
	// DetectedByRule은 규칙만 잡았다는 뜻이다.
	DetectedByRule
	// DetectedByAI는 AI 판별만 잡았다는 뜻이다.
	DetectedByAI
	// DetectedByBoth는 둘 다 잡았다는 뜻이다.
	DetectedByBoth
)

// ParseDetectedBy는 저장된 식별자(none, rule, ai, both)를 읽는다.
func ParseDetectedBy(id string) (DetectedBy, error) {
	for _, d := range [...]DetectedBy{DetectedByNone, DetectedByRule, DetectedByAI, DetectedByBoth} {
		if d.String() == id {
			return d, nil
		}
	}
	return DetectedByNone, fmt.Errorf("%w: unknown id", ErrInvalidDetectedBy)
}

// Valid는 네 값 가운데 하나인지 알려준다.
func (d DetectedBy) Valid() bool {
	return d >= DetectedByNone && d <= DetectedByBoth
}

// String은 저장에 쓰는 식별자다. 정해진 값이 아니면 DetectedBy(n) 꼴로 적는다.
func (d DetectedBy) String() string {
	switch d {
	case DetectedByNone:
		return "none"
	case DetectedByRule:
		return "rule"
	case DetectedByAI:
		return "ai"
	case DetectedByBoth:
		return "both"
	default:
		return "DetectedBy(" + strconv.Itoa(int(d)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
func (d DetectedBy) MarshalText() ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidDetectedBy)
	}
	return []byte(d.String()), nil
}

// UnmarshalText는 ParseDetectedBy와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (d *DetectedBy) UnmarshalText(text []byte) error {
	parsed, err := ParseDetectedBy(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
