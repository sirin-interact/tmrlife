package clock

import (
	"sync"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// Fake는 손으로 움직이는 시계다. 시험과 모의 실행에서 쓴다.
// 스스로 흐르지 않으므로 Set, Advance, AdvanceToNext를 부르기 전까지 같은 시각을 돌려준다.
// 여러 고루틴이 동시에 읽고 움직여도 된다.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

var _ Clock = (*Fake)(nil)

// NewFake는 start에 멈춰 있는 시계를 만든다.
// Real과 똑같이 UTC, 마이크로초 단위로 맞춘 값을 돌려주므로,
// 돌려받은 값을 기대값과 견줄 때는 == 대신 time.Time.Equal을 쓴다.
func NewFake(start time.Time) *Fake {
	return &Fake{now: normalize(start)}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set은 시계를 t로 옮긴다. 과거로도 옮길 수 있다.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = normalize(t)
}

// Advance는 시계를 d만큼 옮기고 옮긴 뒤의 시각을 돌려준다.
// 음수도 받는다. 시계가 뒤로 밀리는 상황을 시험할 때 쓴다.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = normalize(f.now.Add(d))
	return f.now
}

// AdvanceToNext는 loc의 벽시계가 다음번에 hour시 minute분 0초를 가리키는 순간으로 시계를 옮기고
// 그 시각을 돌려준다. 지금이 정확히 그 시각이면 다음 날로 간다.
// 그래서 되풀이해 부르면 매일 저녁 8시처럼 하루 한 번씩 차례로 밟아 갈 수 있다.
//
// 일광 절약 시간이 있는 시간대에서는 다음과 같이 움직인다.
//   - 시계를 앞으로 돌려 그 시각이 없는 날에는, 건너뛴 직후의 첫 순간으로 간다.
//   - 시계를 뒤로 돌려 그 시각이 두 번 오는 날에도 하루 한 번, 먼저 오는 쪽에만 멈춘다.
//
// hour와 minute이 범위를 넘으면 time.Date와 같은 방식으로 올림한다(24시는 다음 날 0시).
// loc가 nil이면 UTC로 본다.
func (f *Fake) AdvanceToNext(hour, minute int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	year, month, day := f.now.In(loc).Date()
	// 오늘 몫이 아직 남았으면 오늘, 이미 지났으면 내일이다.
	// 목표 벽시계가 하루씩 늦어지면 찾은 순간도 같거나 늦어지므로 언젠가는 지금보다 뒤가 된다.
	for ; ; day++ {
		wall := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
		// 없는 시각과 두 번 오는 시각을 다루는 계산은 기록 날짜의 경계를 정하는 것과 같은 함수를 쓴다.
		// 둘이 다른 순간을 고르면 가짜 시계로 돌린 하루가 기록 날짜와 어긋난다.
		candidate := recorddate.FirstInstantAtOrAfter(wall, loc)
		if candidate.After(f.now) {
			f.now = normalize(candidate)
			return f.now
		}
	}
}
