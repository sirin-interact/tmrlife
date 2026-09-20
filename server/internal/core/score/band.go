package score

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// ErrInvalidBand는 정해진 구간이 아닌 값이라는 뜻이다. 메시지에는 입력받은 문자열을 넣지 않는다.
var ErrInvalidBand = errors.New("score: invalid band")

// Band는 추정 점수를 다섯으로 나눈 구간이다.
//
// 여덟 항목 척도에서 널리 쓰는 구간을 그대로 따른다. 구간은 척도를 읽는 방법일 뿐이고,
// 그 점수에서 무엇을 할지는 개입 단계가 따로 정한다.
//
// 빈 값은 "구간 없음"이다. 기록이 부족해 점수를 내지 않았을 때의 값이다.
// 채우지 않은 결과가 조용히 가장 낮은 구간으로 읽히는 일을 막는다.
type Band int

// 숫자가 클수록 높은 구간이다. 크기 비교에 쓸 수 있도록 순서를 바꾸지 않는다.
const (
	// NoBand는 점수를 내지 않아 구간도 없다는 뜻이다.
	NoBand Band = iota
	// Minimal은 기본값에서 0~4다.
	Minimal
	// Mild는 기본값에서 5~9다.
	Mild
	// Moderate는 기본값에서 10~14다.
	Moderate
	// ModeratelySevere는 기본값에서 15~19다.
	ModeratelySevere
	// Severe는 기본값에서 20 이상이다.
	Severe
)

// ParseBand는 식별자를 Band로 읽는다. 대소문자나 앞뒤 공백이 다르면 받지 않는다.
func ParseBand(id string) (Band, error) {
	for b := NoBand; b <= Severe; b++ {
		if b.String() == id {
			return b, nil
		}
	}
	return NoBand, fmt.Errorf("%w: unknown id", ErrInvalidBand)
}

// Valid는 정해진 여섯 값(구간 없음 포함) 가운데 하나인지 알려준다.
func (b Band) Valid() bool {
	return b >= NoBand && b <= Severe
}

// String은 밖으로 내보낼 때 쓰는 식별자다. 정해진 값이 아니면 Band(n) 꼴로 적는다.
func (b Band) String() string {
	switch b {
	case NoBand:
		return "none"
	case Minimal:
		return "minimal"
	case Mild:
		return "mild"
	case Moderate:
		return "moderate"
	case ModeratelySevere:
		return "moderately_severe"
	case Severe:
		return "severe"
	default:
		return "Band(" + strconv.Itoa(int(b)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 정해진 값이 아니면 오류다.
// 구간 없음은 기록 부족일 때의 정상 값이라 "none"으로 적는다.
func (b Band) MarshalText() ([]byte, error) {
	if !b.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidBand)
	}
	return []byte(b.String()), nil
}

// UnmarshalText는 ParseBand와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (b *Band) UnmarshalText(text []byte) error {
	parsed, err := ParseBand(string(text))
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// bandFor는 추정 점수가 드는 구간을 고른다. 경계는 "이 값 이상"이다.
func bandFor(total int, s params.Score) Band {
	switch {
	case total >= s.SevereMin:
		return Severe
	case total >= s.ModeratelySevereMin:
		return ModeratelySevere
	case total >= s.ModerateMin:
		return Moderate
	case total >= s.MildMin:
		return Mild
	default:
		return Minimal
	}
}
