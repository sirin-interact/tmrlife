package clock

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err, "시간대 자료(tzdata)가 있어야 한다")
	return loc
}

func utc(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func assertInstant(t *testing.T, want, got time.Time) {
	t.Helper()
	assert.Truef(t, want.Equal(got), "기대한 순간 %s, 받은 순간 %s", want.UTC().Format(time.RFC3339Nano), got.UTC().Format(time.RFC3339Nano))
}

func TestReal_Now(t *testing.T) {
	t.Run("운영체제 시계와 같은 순간을 돌려준다", func(t *testing.T) {
		before := time.Now().Truncate(time.Microsecond)
		got := Real{}.Now()
		after := time.Now()

		assert.False(t, got.Before(before), "읽기 전의 시각보다 앞설 수 없다")
		assert.False(t, got.After(after), "읽은 뒤의 시각보다 늦을 수 없다")
	})

	t.Run("UTC이고 마이크로초 단위다", func(t *testing.T) {
		got := Real{}.Now()

		assert.Equal(t, time.UTC, got.Location())
		assert.Zero(t, got.Nanosecond()%1000, "DB에 넣었다 꺼내도 같은 값이어야 한다")
	})

	t.Run("단조 시계 값이 붙어 있지 않다", func(t *testing.T) {
		got := Real{}.Now()

		// Round(0)은 단조 시계 값만 떼어 낸다. 떼어 낼 것이 없어야 구조체째로 견주어도 같다.
		assert.Equal(t, got.Round(0), got)
	})
}

func TestFake_NowSetAdvance(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	start := utc(2026, time.September, 1, 11, 0, 0)

	t.Run("움직이기 전까지 같은 시각을 돌려준다", func(t *testing.T) {
		fake := NewFake(start)

		assertInstant(t, start, fake.Now())
		assertInstant(t, start, fake.Now())
	})

	t.Run("현지 시각으로 만들어도 Real처럼 UTC, 마이크로초 단위로 돌려준다", func(t *testing.T) {
		local := time.Date(2026, time.September, 1, 20, 0, 0, 123_456_789, seoul)
		fake := NewFake(local)

		got := fake.Now()
		assert.Equal(t, time.UTC, got.Location())
		assert.Equal(t, 11, got.Hour(), "서울 20시는 UTC 11시다")
		assert.Equal(t, 123_456_000, got.Nanosecond())
	})

	t.Run("Set은 앞으로도 뒤로도 옮긴다", func(t *testing.T) {
		fake := NewFake(start)

		later := start.Add(72 * time.Hour)
		fake.Set(later)
		assertInstant(t, later, fake.Now())

		earlier := start.Add(-72 * time.Hour)
		fake.Set(earlier)
		assertInstant(t, earlier, fake.Now())
	})

	t.Run("Set으로 넣은 현지 시각도 UTC로 돌려준다", func(t *testing.T) {
		fake := NewFake(start)
		fake.Set(time.Date(2026, time.September, 2, 1, 30, 0, 0, seoul))

		got := fake.Now()
		assert.Equal(t, time.UTC, got.Location())
		assertInstant(t, utc(2026, time.September, 1, 16, 30, 0), got)
	})

	tests := []struct {
		name string
		d    time.Duration
		want time.Time
	}{
		{"양수만큼 앞으로 간다", 90 * time.Minute, utc(2026, time.September, 1, 12, 30, 0)},
		{"0이면 그대로다", 0, start},
		{"음수면 뒤로 간다", -time.Hour, utc(2026, time.September, 1, 10, 0, 0)},
		{"여섯 주를 한 번에 건너뛴다", 42 * 24 * time.Hour, utc(2026, time.October, 13, 11, 0, 0)},
	}
	for _, tt := range tests {
		t.Run("Advance: "+tt.name, func(t *testing.T) {
			fake := NewFake(start)

			returned := fake.Advance(tt.d)

			assertInstant(t, tt.want, returned)
			assertInstant(t, tt.want, fake.Now())
		})
	}

	t.Run("Advance는 쌓인다", func(t *testing.T) {
		fake := NewFake(start)
		for range 10 {
			fake.Advance(time.Minute)
		}
		assertInstant(t, start.Add(10*time.Minute), fake.Now())
	})
}

func TestFake_AdvanceToNext(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	newYork := mustLoadLocation(t, "America/New_York")
	helsinki := mustLoadLocation(t, "Europe/Helsinki")

	tests := []struct {
		name   string
		start  time.Time
		hour   int
		minute int
		loc    *time.Location
		want   time.Time
	}{
		{
			name:  "서울 아침에서 같은 날 저녁 8시로 간다",
			start: time.Date(2026, time.September, 1, 9, 0, 0, 0, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2026, time.September, 1, 20, 0, 0, 0, seoul),
		},
		{
			name:  "정확히 그 시각이면 다음 날로 간다",
			start: time.Date(2026, time.September, 1, 20, 0, 0, 0, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2026, time.September, 2, 20, 0, 0, 0, seoul),
		},
		{
			name:  "1마이크로초 전이면 같은 날이다",
			start: time.Date(2026, time.September, 1, 19, 59, 59, 999_999_000, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2026, time.September, 1, 20, 0, 0, 0, seoul),
		},
		{
			name:  "1마이크로초 지났으면 다음 날이다",
			start: time.Date(2026, time.September, 1, 20, 0, 0, 1_000, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2026, time.September, 2, 20, 0, 0, 0, seoul),
		},
		{
			name:  "분까지 맞춘다",
			start: time.Date(2026, time.September, 1, 21, 0, 0, 0, seoul),
			hour:  21, minute: 30, loc: seoul,
			want: time.Date(2026, time.September, 1, 21, 30, 0, 0, seoul),
		},
		{
			name:  "시계가 UTC로 들고 있어도 현지 날짜로 따진다",
			start: utc(2026, time.September, 1, 16, 0, 0), // 서울은 이미 9월 2일 01:00이다.
			hour:  20, loc: seoul,
			want: utc(2026, time.September, 2, 11, 0, 0),
		},
		{
			name:  "달과 해를 넘어간다",
			start: time.Date(2026, time.December, 31, 23, 0, 0, 0, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2027, time.January, 1, 20, 0, 0, 0, seoul),
		},
		{
			name:  "윤일을 거쳐 간다",
			start: time.Date(2028, time.February, 28, 21, 0, 0, 0, seoul),
			hour:  20, loc: seoul,
			want: time.Date(2028, time.February, 29, 20, 0, 0, 0, seoul),
		},
		{
			name:  "24시는 다음 날 0시로 올림한다",
			start: time.Date(2026, time.September, 1, 9, 0, 0, 0, seoul),
			hour:  24, loc: seoul,
			want: time.Date(2026, time.September, 2, 0, 0, 0, 0, seoul),
		},
		{
			name:  "시간대가 nil이면 UTC로 본다",
			start: utc(2026, time.September, 1, 9, 0, 0),
			hour:  20, loc: nil,
			want: utc(2026, time.September, 1, 20, 0, 0),
		},
		{
			name:  "뉴욕: 시계를 앞으로 돌려 없는 02:30은 건너뛴 직후(03:00 EDT)로 간다",
			start: utc(2025, time.March, 8, 17, 0, 0),
			hour:  2, minute: 30, loc: newYork,
			want: utc(2025, time.March, 9, 7, 0, 0),
		},
		{
			name:  "뉴욕: 없는 시각 다음 날은 다시 정상으로 02:30이다",
			start: utc(2025, time.March, 9, 7, 0, 0),
			hour:  2, minute: 30, loc: newYork,
			want: utc(2025, time.March, 10, 6, 30, 0),
		},
		{
			name:  "뉴욕: 두 번 오는 01:30은 먼저 오는 쪽(EDT)으로 간다",
			start: utc(2025, time.November, 1, 16, 0, 0),
			hour:  1, minute: 30, loc: newYork,
			want: utc(2025, time.November, 2, 5, 30, 0),
		},
		{
			name:  "뉴욕: 두 번 오는 시각 사이에 있으면 둘째 것에 멈추지 않고 다음 날로 간다",
			start: utc(2025, time.November, 2, 5, 45, 0), // 01:45 EDT. 45분 뒤에 01:30 EST가 다시 온다.
			hour:  1, minute: 30, loc: newYork,
			want: utc(2025, time.November, 3, 6, 30, 0),
		},
		{
			name:  "헬싱키: 두 번 오는 03:30도 먼저 오는 쪽(EEST)으로 간다",
			start: utc(2025, time.October, 25, 12, 0, 0),
			hour:  3, minute: 30, loc: helsinki,
			want: utc(2025, time.October, 26, 0, 30, 0),
		},
		{
			name:  "헬싱키: 없는 03:30은 건너뛴 직후(04:00 EEST)로 간다",
			start: utc(2025, time.March, 29, 12, 0, 0),
			hour:  3, minute: 30, loc: helsinki,
			want: utc(2025, time.March, 30, 1, 0, 0),
		},
		{
			name:  "뉴욕: 시계를 앞으로 돌린 날에는 저녁 8시까지 23시간이다",
			start: utc(2025, time.March, 9, 1, 0, 0), // 3월 8일 20:00 EST
			hour:  20, loc: newYork,
			want: utc(2025, time.March, 10, 0, 0, 0), // 3월 9일 20:00 EDT
		},
		{
			name:  "뉴욕: 시계를 뒤로 돌린 날에는 저녁 8시까지 25시간이다",
			start: utc(2025, time.November, 2, 0, 0, 0), // 11월 1일 20:00 EDT
			hour:  20, loc: newYork,
			want: utc(2025, time.November, 3, 1, 0, 0), // 11월 2일 20:00 EST
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := NewFake(tt.start)

			returned := fake.AdvanceToNext(tt.hour, tt.minute, tt.loc)

			assertInstant(t, tt.want, returned)
			assertInstant(t, tt.want, fake.Now())
			assert.Equal(t, time.UTC, returned.Location())
			assert.True(t, returned.After(tt.start), "언제나 앞으로만 간다")
		})
	}

	t.Run("되풀이해 부르면 여섯 주의 저녁을 하루씩 밟는다", func(t *testing.T) {
		fake := NewFake(time.Date(2026, time.September, 1, 9, 0, 0, 0, seoul))

		for day := range 42 {
			got := fake.AdvanceToNext(20, 0, seoul).In(seoul)

			want := time.Date(2026, time.September, 1+day, 20, 0, 0, 0, seoul)
			assertInstant(t, want, got)
			assert.Equal(t, 20, got.Hour())

			// 그날 대화를 하느라 시간이 조금 흐른다.
			fake.Advance(17 * time.Minute)
		}
		assertInstant(t, time.Date(2026, time.October, 12, 20, 17, 0, 0, seoul), fake.Now())
	})

	t.Run("일광 절약 시간을 지나도 벽시계는 늘 저녁 8시다", func(t *testing.T) {
		fake := NewFake(utc(2025, time.February, 20, 0, 0, 0))

		previous := fake.Now()
		for range 300 {
			got := fake.AdvanceToNext(20, 0, newYork)

			local := got.In(newYork)
			assert.Equal(t, 20, local.Hour())
			assert.Zero(t, local.Minute())
			gap := got.Sub(previous)
			assert.LessOrEqual(t, gap, 25*time.Hour)
			previous = got
		}
		assertInstant(t, time.Date(2025, time.December, 15, 20, 0, 0, 0, newYork), fake.Now())
	})

	t.Run("전환 규칙으로 계산하는 먼 미래의 윤년 연말에서도 멈추지 않는다", func(t *testing.T) {
		for _, year := range []int{2028, 2040, 2096} {
			fake := NewFake(time.Date(year, time.December, 29, 12, 0, 0, 0, newYork))

			for day := 29; day <= 33; day++ {
				got := fake.AdvanceToNext(20, 0, newYork)
				assertInstant(t, time.Date(year, time.December, day, 20, 0, 0, 0, newYork), got)
			}
		}
	})
}

func TestFake_ConcurrentUse(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	start := utc(2026, time.September, 1, 11, 0, 0)

	t.Run("동시에 Advance해도 하나도 잃지 않는다", func(t *testing.T) {
		const workers, steps = 16, 500
		fake := NewFake(start)

		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for range steps {
					fake.Advance(time.Second)
				}
			})
		}
		wg.Wait()

		assertInstant(t, start.Add(workers*steps*time.Second), fake.Now())
	})

	t.Run("앞으로만 옮기는 동안 읽는 쪽은 시간이 거꾸로 가는 것을 보지 않는다", func(t *testing.T) {
		const readers, steps = 8, 2000
		fake := NewFake(start)

		var writers, observers sync.WaitGroup
		done := make(chan struct{})
		wentBackwards := make([]bool, readers)
		for i := range readers {
			observers.Go(func() {
				last := fake.Now()
				for {
					select {
					case <-done:
						return
					default:
					}
					now := fake.Now()
					if now.Before(last) {
						wentBackwards[i] = true
					}
					last = now
				}
			})
		}
		writers.Go(func() {
			for range steps {
				fake.Advance(time.Millisecond)
			}
		})
		writers.Go(func() {
			for range 50 {
				fake.AdvanceToNext(20, 0, seoul)
			}
		})
		writers.Wait()
		close(done)
		observers.Wait()

		for i, backwards := range wentBackwards {
			assert.Falsef(t, backwards, "읽는 고루틴 %d", i)
		}
	})

	t.Run("읽기와 모든 쓰기를 섞어도 경합이 없다", func(t *testing.T) {
		const workers, steps = 8, 300
		fake := NewFake(start)

		var wg sync.WaitGroup
		for worker := range workers {
			wg.Go(func() {
				for step := range steps {
					switch (worker + step) % 4 {
					case 0:
						fake.Set(start.Add(time.Duration(step) * time.Hour))
					case 1:
						fake.Advance(time.Minute)
					case 2:
						fake.AdvanceToNext(20, 0, seoul)
					default:
						_ = fake.Now()
					}
				}
			})
		}
		wg.Wait()

		got := fake.Now()
		assert.Equal(t, time.UTC, got.Location())
		assert.False(t, got.Before(start), "어느 쓰기도 시작 시각보다 앞으로 옮기지 않는다")
	})
}

// 계산은 기록 날짜 패키지에 있다. AdvanceToNext가 기대는 성질(없는 시각은 건너뛴 직후, 두 번 오는 시각은 먼저 오는 쪽)을
// 쓰는 쪽에서도 못 박아 둔다. 그쪽 계산이 바뀌어 가짜 시계가 다른 순간에 서게 되면 여기서 드러난다.
func TestFirstInstantAtOrAfter(t *testing.T) {
	newYork := mustLoadLocation(t, "America/New_York")
	helsinki := mustLoadLocation(t, "Europe/Helsinki")
	seoul := mustLoadLocation(t, "Asia/Seoul")

	tests := []struct {
		name string
		wall time.Time
		loc  *time.Location
		want time.Time
	}{
		{"일광 절약 시간이 없으면 UTC 차이만 뺀다", utc(2026, time.September, 1, 20, 0, 0), seoul, utc(2026, time.September, 1, 11, 0, 0)},
		{"UTC에서는 그대로다", utc(2026, time.September, 1, 20, 0, 0), time.UTC, utc(2026, time.September, 1, 20, 0, 0)},
		{"고정 시간대에서도 된다", utc(2026, time.September, 1, 20, 0, 0), time.FixedZone("test", -3*60*60), utc(2026, time.September, 1, 23, 0, 0)},
		{"뉴욕: 없는 시각의 시작점(02:00)은 건너뛴 직후다", utc(2025, time.March, 9, 2, 0, 0), newYork, utc(2025, time.March, 9, 7, 0, 0)},
		{"뉴욕: 없는 시각 바로 앞(01:59:59)은 그대로 있다", utc(2025, time.March, 9, 1, 59, 59), newYork, utc(2025, time.March, 9, 6, 59, 59)},
		{"뉴욕: 건너뛴 직후(03:00)는 전환 순간이다", utc(2025, time.March, 9, 3, 0, 0), newYork, utc(2025, time.March, 9, 7, 0, 0)},
		{"뉴욕: 두 번 오는 01:00은 먼저 오는 쪽이다", utc(2025, time.November, 2, 1, 0, 0), newYork, utc(2025, time.November, 2, 5, 0, 0)},
		{"뉴욕: 두 번 오는 01:59:59도 먼저 오는 쪽이다", utc(2025, time.November, 2, 1, 59, 59), newYork, utc(2025, time.November, 2, 5, 59, 59)},
		{"뉴욕: 되돌린 뒤 한 번만 오는 02:00은 EST다", utc(2025, time.November, 2, 2, 0, 0), newYork, utc(2025, time.November, 2, 7, 0, 0)},
		{"헬싱키: 되돌리는 순간의 04:00은 EET로 한 번만 온다", utc(2025, time.October, 26, 4, 0, 0), helsinki, utc(2025, time.October, 26, 2, 0, 0)},
		{"헬싱키: 건너뛰어 도착한 04:00은 전환 순간이다", utc(2025, time.March, 30, 4, 0, 0), helsinki, utc(2025, time.March, 30, 1, 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertInstant(t, tt.want, recorddate.FirstInstantAtOrAfter(tt.wall, tt.loc))
		})
	}
}
