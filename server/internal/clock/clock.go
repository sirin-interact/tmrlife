// Package clock은 서버가 현재 시각을 읽는 단 하나의 통로다.
//
// 현재 시각을 곳곳에서 직접 읽으면 새벽 경계로 나뉘는 기록 날짜나 몇 주에 걸친 변화 추세를
// 시험에서 재현할 수 없다. 그래서 나머지 코드는 Clock을 주입받고, 시험과 모의 실행은
// Fake로 시간을 원하는 만큼 옮긴다.
package clock

import "time"

// Clock은 현재 시각을 알려준다.
type Clock interface {
	Now() time.Time
}

// Real은 운영체제 시계를 읽는다.
type Real struct{}

var _ Clock = Real{}

// Now가 돌려주는 값은 Fake와 마찬가지로 normalize를 거친다.
func (Real) Now() time.Time {
	return normalize(time.Now())
}

// normalize는 시각을 UTC, 마이크로초 단위로 맞춘다.
//
// UTC로 맞추는 이유: 개발 장비와 배포 환경의 시간대가 달라도 같은 값이 나온다.
// 사용자의 시간대가 필요한 계산은 그 시간대를 명시해서 바꿔 써야 하고,
// 그렇게 하지 않은 코드는 시험에서 바로 드러난다.
//
// 마이크로초로 자르는 이유: PostgreSQL의 timestamptz가 마이크로초까지만 담는다.
// 자르지 않으면 메모리에 든 값과 DB에 넣었다 꺼낸 값이 같은 순간인데도 서로 다르다.
//
// 이 과정에서 단조 시계 값이 떨어져 나가므로 두 시각의 차는 벽시계 기준이다.
func normalize(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}
