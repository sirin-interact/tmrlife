// Package signal은 마음 신호의 기본 타입과, 대화별 판단을 하루 단위로 합치는 규칙을 담는다.
//
// 신호는 하루 단위로 센다. 하루에 대화를 여러 번 해도 그날의 판단은 항목마다 하나다.
// 점수, 신뢰도, 기준선, 변화 탐지는 모두 여기서 만든 Day의 목록을 입력으로 받는다.
//
// 문자열 식별자(String, Parse*)는 DB에 저장하는 값과 같다. 한번 정한 뒤에는 바꾸지 않는다.
// 오류 메시지에는 입력받은 문자열을 넣지 않는다. 항목 자리에 다른 글이 잘못 들어와도 로그로 새지 않는다.
package signal

import (
	"fmt"
	"strconv"
)

// Item은 점수에 쓰는 여덟 항목 가운데 하나다.
//
// 빈 값(0)은 어떤 항목도 아니다. 항목을 채우지 않은 행이 조용히 첫 항목으로 세어지는 일을 막는다.
// 자해나 자살에 관한 표현은 항목이 아니다. 점수로 쌓지 않고 위기 관문이 따로 맡는다.
type Item int

// 순서는 AllItems와 Index의 순서이고, 항목별 배열의 자리이기도 하다. 중간에 끼워 넣거나 바꾸지 않는다.
const (
	// Interest는 흥미와 즐거움이 줄어든 것이다.
	Interest Item = iota + 1
	// Mood는 우울하거나 희망이 없다고 느끼는 것이다.
	Mood
	// Sleep은 잠들기 어렵거나 자주 깨거나 너무 많이 자는 것이다.
	Sleep
	// Fatigue는 피곤하고 기운이 없는 것이다.
	Fatigue
	// Appetite는 입맛이 없거나 지나치게 먹는 것이다.
	Appetite
	// SelfBlame은 자신을 탓하거나 쓸모없다고 느끼는 것이다.
	SelfBlame
	// Concentration은 일이나 공부에 집중하기 어려운 것이다.
	Concentration
	// Psychomotor는 말과 움직임이 눈에 띄게 느려지거나, 반대로 안절부절못하는 것이다.
	Psychomotor
)

// ItemCount는 항목의 수다. 항목별 값을 담는 배열의 길이로 쓴다.
const ItemCount = 8

// AllItems는 여덟 항목을 정해진 순서로 돌려준다.
// 맵을 돌면 순서가 실행마다 달라지므로, 항목을 차례로 볼 때는 언제나 이 순서를 쓴다.
// 배열을 값으로 돌려주기 때문에 받은 쪽에서 고쳐도 다른 곳에 영향이 없다.
func AllItems() [ItemCount]Item {
	return [ItemCount]Item{
		Interest, Mood, Sleep, Fatigue, Appetite, SelfBlame, Concentration, Psychomotor,
	}
}

// ParseItem은 저장된 식별자를 Item으로 읽는다. 대소문자나 앞뒤 공백이 다르면 받지 않는다.
func ParseItem(id string) (Item, error) {
	for _, item := range AllItems() {
		if item.String() == id {
			return item, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown id", ErrInvalidItem)
}

// Valid는 여덟 항목 가운데 하나인지 알려준다.
func (i Item) Valid() bool {
	return i >= Interest && i <= Psychomotor
}

// Index는 항목별 배열에서의 자리(0부터 7)다. 항목이 아니면 -1이다.
func (i Item) Index() int {
	if !i.Valid() {
		return -1
	}
	return int(i) - 1
}

// String은 저장에 쓰는 식별자다. 항목이 아닌 값은 식별자처럼 보이지 않게 Item(n) 꼴로 적는다.
func (i Item) String() string {
	switch i {
	case Interest:
		return "interest"
	case Mood:
		return "mood"
	case Sleep:
		return "sleep"
	case Fatigue:
		return "fatigue"
	case Appetite:
		return "appetite"
	case SelfBlame:
		return "self_blame"
	case Concentration:
		return "concentration"
	case Psychomotor:
		return "psychomotor"
	default:
		return "Item(" + strconv.Itoa(int(i)) + ")"
	}
}

// MarshalText는 JSON 값과 JSON 객체의 키에 식별자로 적히게 한다.
// 항목이 아닌 값은 오류다. 채우지 않은 항목이 밖으로 나가는 것을 막는다.
func (i Item) MarshalText() ([]byte, error) {
	if !i.Valid() {
		return nil, fmt.Errorf("%w: cannot be encoded", ErrInvalidItem)
	}
	return []byte(i.String()), nil
}

// UnmarshalText는 ParseItem과 같은 규칙으로 읽는다. 실패하면 값을 바꾸지 않는다.
func (i *Item) UnmarshalText(text []byte) error {
	parsed, err := ParseItem(string(text))
	if err != nil {
		return err
	}
	*i = parsed
	return nil
}
