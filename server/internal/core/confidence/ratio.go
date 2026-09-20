package confidence

import (
	"cmp"
	"strconv"
)

// Ratio는 센 값 그대로의 분수다. 약분하지 않는다.
//
// 소수로 바꿔 들고 다니지 않는 이유는 두 가지다.
// 내부 확인 화면에서 "14일 중 7일", "여덟 항목 중 3개"처럼 센 값을 그대로 보여줄 수 있고,
// 요소끼리 크기를 견줄 때 정수 곱셈만으로 정확히 비교할 수 있다.
//
// 빈 값(0/0)은 "계산하지 않았다"는 뜻이다. 기록 부족일 때의 최종 값이 그렇다.
type Ratio struct {
	Num int
	Den int
}

// IsZero는 계산하지 않은 빈 값인지 알려준다.
func (r Ratio) IsZero() bool {
	return r == Ratio{}
}

// Float64는 화면에 보여줄 소수 값이다. 분모가 0이면 0을 돌려준다.
//
// 정수를 정수로 한 번만 나눈다. 나눗셈 한 번은 참값에 가장 가까운 소수로 맞춰지기 때문에,
// 2/5는 0.4라고 적은 소수와, 7/10은 0.7이라고 적은 소수와 정확히 같은 값이 된다.
// 1/14를 먼저 구해 곱하거나 여러 값을 더해서 만들면 이 성질이 깨진다.
func (r Ratio) Float64() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

// Compare는 r이 other보다 작으면 -1, 같으면 0, 크면 +1을 돌려준다.
//
// 분모를 서로 곱해 정수끼리 견준다. 7/14와 4/8처럼 값이 같은 분수가 소수 오차 때문에 다르게 나오는 일이 없다.
// 분모가 0인 빈 값과의 비교는 뜻이 없다(언제나 0이 나온다).
// 분자와 분모는 창의 길이, 항목 수, 창 안의 판단 수를 넘지 않는 작은 수라서 곱이 넘치지 않는다.
func (r Ratio) Compare(other Ratio) int {
	return cmp.Compare(r.Num*other.Den, other.Num*r.Den)
}

// String은 "7/14"처럼 센 값 그대로 적는다.
func (r Ratio) String() string {
	return strconv.Itoa(r.Num) + "/" + strconv.Itoa(r.Den)
}
