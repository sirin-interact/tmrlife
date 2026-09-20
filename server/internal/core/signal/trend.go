package signal

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalidTrendRow는 추세 화면의 세 줄 가운데 하나가 아닌 값이라는 뜻이다.
var ErrInvalidTrendRow = errors.New("signal: invalid trend row")

// TrendRow는 추세 화면의 점 달력에 그리는 줄 하나다.
//
// 화면에는 여덟 항목을 다 그리지 않는다. 사용자가 자기 말로 떠올리기 쉬운 세 가지
// (기분, 수면, 에너지)만 줄로 보여주고 나머지 항목은 근거 화면에서 본다.
// 어느 항목이 어느 줄에 드는지를 화면과 집계가 따로 정하지 않도록 여기 한 곳에 둔다.
// 빈 값(0)은 어떤 줄도 아니다.
type TrendRow int

const (
	// TrendMood는 기분 줄이다. 흥미 저하와 우울감 가운데 하나라도 해당하면 이 줄에 찍힌다.
	TrendMood TrendRow = iota + 1
	// TrendSleep은 수면 줄이다.
	TrendSleep
	// TrendEnergy는 에너지 줄이다. 피로 항목을 본다.
	TrendEnergy
)

// TrendRowCount는 줄의 수다.
const TrendRowCount = 3

// AllTrendRows는 세 줄을 화면에 그리는 순서(기분, 수면, 에너지)로 돌려준다.
func AllTrendRows() [TrendRowCount]TrendRow {
	return [TrendRowCount]TrendRow{TrendMood, TrendSleep, TrendEnergy}
}

// ParseTrendRow는 식별자(mood, sleep, energy)를 읽는다.
func ParseTrendRow(id string) (TrendRow, error) {
	for _, row := range AllTrendRows() {
		if row.String() == id {
			return row, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown id", ErrInvalidTrendRow)
}

// Valid는 세 줄 가운데 하나인지 알려준다.
func (r TrendRow) Valid() bool {
	return r >= TrendMood && r <= TrendEnergy
}

// Index는 줄별 배열에서의 자리(0부터 2)다. 줄이 아니면 -1이다.
func (r TrendRow) Index() int {
	if !r.Valid() {
		return -1
	}
	return int(r) - 1
}

// Items는 그 줄에 드는 항목이다. 줄이 아니면 nil이다.
// 부를 때마다 새 슬라이스를 돌려주므로 받은 쪽에서 고쳐도 다른 곳에 영향이 없다.
func (r TrendRow) Items() []Item {
	switch r {
	case TrendMood:
		return []Item{Interest, Mood}
	case TrendSleep:
		return []Item{Sleep}
	case TrendEnergy:
		return []Item{Fatigue}
	default:
		return nil
	}
}

// String은 줄의 식별자다. 기분 줄의 식별자(mood)는 우울감 항목의 식별자와 글자가 같지만 타입이 달라 섞이지 않는다.
func (r TrendRow) String() string {
	switch r {
	case TrendMood:
		return "mood"
	case TrendSleep:
		return "sleep"
	case TrendEnergy:
		return "energy"
	default:
		return "TrendRow(" + strconv.Itoa(int(r)) + ")"
	}
}

// MarshalText는 JSON에 식별자로 적히게 한다. 줄이 아닌 값은 오류다.
func (r TrendRow) MarshalText() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidTrendRow)
	}
	return []byte(r.String()), nil
}

// UnmarshalText는 ParseTrendRow와 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (r *TrendRow) UnmarshalText(text []byte) error {
	parsed, err := ParseTrendRow(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// TrendStatus는 그날 그 줄에 찍을 판단이다.
//
// 줄에 드는 항목 가운데 하나라도 관찰됨이면 관찰됨이다. 아니면 하나라도 관찰되지 않음일 때 관찰되지 않음이고,
// 둘 다 이야기가 없었을 때만 언급 없음이다. 하루로 합칠 때와 같은 우선순위다.
// 줄이 아닌 값을 넘기면 언급 없음을 돌려준다.
func (d Day) TrendStatus(row TrendRow) Status {
	status := NotMentioned
	for _, item := range row.Items() {
		if s := d.Judgement(item).Status; s > status {
			status = s
		}
	}
	return status
}
