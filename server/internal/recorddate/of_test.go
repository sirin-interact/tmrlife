package recorddate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDayStartHour(t *testing.T) {
	t.Run("하루는 새벽 4시에 시작한다", func(t *testing.T) {
		assert.Equal(t, 4, DayStartHour)
	})
}

func TestOf_SeoulBoundary(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	at := func(year int, month time.Month, day, hour, minute, second, nanosecond int) time.Time {
		return time.Date(year, month, day, hour, minute, second, nanosecond, seoul)
	}

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"03:59:59는 전날이다", at(2026, time.September, 20, 3, 59, 59, 0), "2026-09-19"},
		{"04:00이 되기 1나노초 전도 전날이다", at(2026, time.September, 20, 3, 59, 59, 999_999_999), "2026-09-19"},
		{"04:00:00 정각부터 새 날이다", at(2026, time.September, 20, 4, 0, 0, 0), "2026-09-20"},
		{"04:00:00에서 1나노초 지나도 새 날이다", at(2026, time.September, 20, 4, 0, 0, 1), "2026-09-20"},
		{"자정은 전날이다", at(2026, time.September, 20, 0, 0, 0, 0), "2026-09-19"},
		{"새벽 1시의 대화는 전날의 기록이다", at(2026, time.September, 20, 1, 0, 0, 0), "2026-09-19"},
		{"자정 1초 전은 그날이다", at(2026, time.September, 20, 23, 59, 59, 0), "2026-09-20"},
		{"한낮은 그날이다", at(2026, time.September, 20, 12, 0, 0, 0), "2026-09-20"},
		{"저녁 8시는 그날이다", at(2026, time.September, 20, 20, 0, 0, 0), "2026-09-20"},
		{"달이 바뀐 새벽은 지난달 마지막 날이다", at(2026, time.October, 1, 2, 0, 0, 0), "2026-09-30"},
		{"달이 바뀐 날 04:00부터는 새 달이다", at(2026, time.October, 1, 4, 0, 0, 0), "2026-10-01"},
		{"31일이 없는 달로 넘어간 새벽은 30일이다", at(2026, time.May, 1, 3, 0, 0, 0), "2026-04-30"},
		{"평년 3월 1일 새벽은 2월 28일이다", at(2026, time.March, 1, 1, 0, 0, 0), "2026-02-28"},
		{"윤년 3월 1일 새벽은 2월 29일이다", at(2028, time.March, 1, 1, 0, 0, 0), "2028-02-29"},
		{"윤일 새벽은 2월 28일이다", at(2028, time.February, 29, 3, 59, 59, 0), "2028-02-28"},
		{"윤일 04:00부터는 윤일이다", at(2028, time.February, 29, 4, 0, 0, 0), "2028-02-29"},
		{"100으로 나뉘는 해는 윤년이 아니다", at(2100, time.March, 1, 3, 0, 0, 0), "2100-02-28"},
		{"400으로 나뉘는 해는 윤년이다", at(2000, time.March, 1, 3, 0, 0, 0), "2000-02-29"},
		{"새해 첫 새벽은 지난해 마지막 날이다", at(2027, time.January, 1, 0, 30, 0, 0), "2026-12-31"},
		{"새해 첫날 03:59:59도 지난해다", at(2027, time.January, 1, 3, 59, 59, 0), "2026-12-31"},
		{"새해 첫날 04:00부터 새해다", at(2027, time.January, 1, 4, 0, 0, 0), "2027-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, mustParse(t, tt.want), Of(tt.at, seoul))
		})
	}
}

func TestOf_InstantGivenInAnotherLocation(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	newYork := mustLoadLocation(t, "America/New_York")

	tests := []struct {
		name string
		utc  string
		want string
	}{
		{"UTC 18:59:59는 서울 03:59:59라서 전날이다", "2026-09-19T18:59:59Z", "2026-09-19"},
		{"UTC 19:00:00은 서울 04:00:00이라서 새 날이다", "2026-09-19T19:00:00Z", "2026-09-20"},
		{"UTC 15:00:00은 서울 자정이라서 아직 전날이다", "2026-09-19T15:00:00Z", "2026-09-19"},
		{"UTC 14:59:59는 서울 23:59:59라서 그날이다", "2026-09-19T14:59:59Z", "2026-09-19"},
		{"UTC로는 전날 밤이어도 서울 아침이면 서울 날짜다", "2026-09-19T23:30:00Z", "2026-09-20"},
		{"서울 달력으로 20일 새벽 2시여도 기록 날짜는 19일이다", "2026-09-19T17:00:00Z", "2026-09-19"},
		{"UTC로 해가 바뀌기 전이어도 서울 04:00을 넘겼으면 새해다", "2026-12-31T19:00:00Z", "2027-01-01"},
		{"UTC로 12월 31일 오후여도 서울 새벽이면 12월 31일이다", "2026-12-31T18:59:59Z", "2026-12-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := mustInstant(t, tt.utc)
			require.Equal(t, time.UTC, at.Location())
			want := mustParse(t, tt.want)

			assert.Equal(t, want, Of(at, seoul), "UTC로 적힌 시각")
			assert.Equal(t, want, Of(at.In(seoul), seoul), "서울 시각으로 적힌 같은 순간")
			assert.Equal(t, want, Of(at.In(newYork), seoul), "뉴욕 시각으로 적힌 같은 순간")
			assert.Equal(t, want, Of(at.In(time.FixedZone("", -11*secondsPerHour)), seoul), "고정 시간대로 적힌 같은 순간")
		})
	}

	t.Run("같은 순간도 보는 시간대에 따라 기록 날짜가 다르다", func(t *testing.T) {
		at := mustInstant(t, "2026-09-19T19:30:00Z")

		assert.Equal(t, mustParse(t, "2026-09-20"), Of(at, seoul), "서울은 04:30")
		assert.Equal(t, mustParse(t, "2026-09-19"), Of(at, newYork), "뉴욕은 15:30")
		assert.Equal(t, mustParse(t, "2026-09-19"), Of(at, time.UTC), "UTC는 19:30")
	})

	t.Run("UTC 새벽은 UTC 기준으로도 전날이다", func(t *testing.T) {
		at := mustInstant(t, "2026-09-20T02:00:00Z")

		assert.Equal(t, mustParse(t, "2026-09-20"), Of(at, seoul), "서울은 11:00")
		assert.Equal(t, mustParse(t, "2026-09-19"), Of(at, newYork), "뉴욕은 전날 22:00")
		assert.Equal(t, mustParse(t, "2026-09-19"), Of(at, time.UTC), "UTC는 02:00")
	})
}

func TestOf_DaylightSaving(t *testing.T) {
	newYork := mustLoadLocation(t, "America/New_York")
	helsinki := mustLoadLocation(t, "Europe/Helsinki")
	hongKong := mustLoadLocation(t, "Asia/Hong_Kong")
	apia := mustLoadLocation(t, "Pacific/Apia")

	tests := []struct {
		name string
		loc  *time.Location
		utc  string
		want string
	}{
		// 뉴욕 2025-03-09: 02:00 EST에 시계를 03:00 EDT로 돌린다(07:00Z).
		{"뉴욕 봄: 돌리기 전 01:30 EST는 전날이다", newYork, "2025-03-09T06:30:00Z", "2025-03-08"},
		{"뉴욕 봄: 돌리기 직전 01:59:59 EST는 전날이다", newYork, "2025-03-09T06:59:59Z", "2025-03-08"},
		{"뉴욕 봄: 돌린 직후 03:00 EDT도 전날이다", newYork, "2025-03-09T07:00:00Z", "2025-03-08"},
		{"뉴욕 봄: 03:59:59 EDT는 전날이다", newYork, "2025-03-09T07:59:59Z", "2025-03-08"},
		{"뉴욕 봄: 04:00 EDT부터 새 날이다", newYork, "2025-03-09T08:00:00Z", "2025-03-09"},
		{"뉴욕 봄: EST였다면 04:00일 순간에는 이미 새 날이다", newYork, "2025-03-09T09:00:00Z", "2025-03-09"},

		// 뉴욕 2025-11-02: 02:00 EDT에 시계를 01:00 EST로 되돌린다(06:00Z).
		{"뉴욕 가을: 처음 오는 01:30 EDT는 전날이다", newYork, "2025-11-02T05:30:00Z", "2025-11-01"},
		{"뉴욕 가을: 다시 오는 01:30 EST도 전날이다", newYork, "2025-11-02T06:30:00Z", "2025-11-01"},
		{"뉴욕 가을: EDT였다면 04:00일 순간은 03:00 EST라서 아직 전날이다", newYork, "2025-11-02T08:00:00Z", "2025-11-01"},
		{"뉴욕 가을: 03:59:59 EST는 전날이다", newYork, "2025-11-02T08:59:59Z", "2025-11-01"},
		{"뉴욕 가을: 04:00 EST부터 새 날이다", newYork, "2025-11-02T09:00:00Z", "2025-11-02"},

		// 헬싱키는 전환이 04:00에 닿는다. 봄에는 03:00 EET → 04:00 EEST, 가을에는 04:00 EEST → 03:00 EET (둘 다 01:00Z).
		{"헬싱키 봄: 돌리기 직전 02:59:59 EET는 전날이다", helsinki, "2025-03-30T00:59:59Z", "2025-03-29"},
		{"헬싱키 봄: 돌리자마자 04:00 EEST라서 새 날이다", helsinki, "2025-03-30T01:00:00Z", "2025-03-30"},
		{"헬싱키 가을: 되돌리기 직전 03:59:59 EEST는 전날이다", helsinki, "2025-10-26T00:59:59Z", "2025-10-25"},
		{"헬싱키 가을: 04:00 EEST는 오지 않고 03:00 EET가 되므로 전날이다", helsinki, "2025-10-26T01:00:00Z", "2025-10-25"},
		{"헬싱키 가을: 다시 오는 03:59:59 EET도 전날이다", helsinki, "2025-10-26T01:59:59Z", "2025-10-25"},
		{"헬싱키 가을: 04:00 EET부터 새 날이다", helsinki, "2025-10-26T02:00:00Z", "2025-10-26"},

		// 홍콩 1976-04-18: 03:30 HKT에 시계를 04:30 HKST로 돌렸다(전날 19:30Z). 04:00이 없는 날이다.
		{"홍콩 1976: 건너뛰기 직전 03:29:59는 전날이다", hongKong, "1976-04-17T19:29:59Z", "1976-04-17"},
		{"홍콩 1976: 건너뛴 직후 04:30에 새 날이 시작한다", hongKong, "1976-04-17T19:30:00Z", "1976-04-18"},

		// 사모아 2011: 12월 29일 다음 날이 12월 31일이었다(12-30T10:00Z에 -10에서 +14로).
		{"사모아 2011: 건너뛰기 직전은 12월 29일이다", apia, "2011-12-30T09:59:59Z", "2011-12-29"},
		{"사모아 2011: 12월 31일 자정부터 04:00 전까지는 달력에 없던 12월 30일의 기록이다", apia, "2011-12-30T10:00:00Z", "2011-12-30"},
		{"사모아 2011: 12월 31일 03:59:59도 12월 30일의 기록이다", apia, "2011-12-30T13:59:59Z", "2011-12-30"},
		{"사모아 2011: 12월 31일 04:00부터 12월 31일이다", apia, "2011-12-30T14:00:00Z", "2011-12-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, mustParse(t, tt.want), Of(mustInstant(t, tt.utc), tt.loc))
		})
	}
}

func TestOf_SyntheticTransitionsAtBoundary(t *testing.T) {
	gap := gapOverBoundary(t)
	back := backAcrossBoundary(t)

	t.Run("시험용 시간대가 뜻한 대로 움직인다", func(t *testing.T) {
		at := mustInstant(t, "2026-05-09T18:30:00Z")

		assert.Equal(t, "2026-05-10 03:29:59", at.Add(-time.Second).In(gap).Format(time.DateTime))
		assert.Equal(t, "2026-05-10 04:30:00", at.In(gap).Format(time.DateTime))
		assert.Equal(t, "2026-05-10 04:29:59", at.Add(-time.Second).In(back).Format(time.DateTime))
		assert.Equal(t, "2026-05-10 03:30:00", at.In(back).Format(time.DateTime))
	})

	tests := []struct {
		name string
		loc  *time.Location
		utc  string
		want string
	}{
		{"04:00을 건너뛰는 날: 건너뛰기 1나노초 전은 전날이다", gap, "2026-05-09T18:29:59.999999999Z", "2026-05-09"},
		{"04:00을 건너뛰는 날: 건너뛴 순간(04:30)부터 새 날이다", gap, "2026-05-09T18:30:00Z", "2026-05-10"},
		{"04:00을 건너뛴 다음 날은 평소처럼 04:00에 바뀐다", gap, "2026-05-10T17:59:59Z", "2026-05-10"},
		{"04:00을 건너뛴 다음 날 04:00", gap, "2026-05-10T18:00:00Z", "2026-05-11"},

		{"되돌리는 날: 처음 04:00이 되기 직전은 전날이다", back, "2026-05-09T17:59:59Z", "2026-05-09"},
		{"되돌리는 날: 처음 04:00에 새 날이 시작한다", back, "2026-05-09T18:00:00Z", "2026-05-10"},
		{"되돌리는 날: 되돌리기 직전 04:29:59는 새 날이다", back, "2026-05-09T18:29:59Z", "2026-05-10"},
		{"되돌리는 날: 03:30으로 돌아가도 이미 시작한 날짜에 머문다", back, "2026-05-09T18:30:00Z", "2026-05-10"},
		{"되돌리는 날: 다시 오는 03:59:59도 새 날이다", back, "2026-05-09T18:59:59Z", "2026-05-10"},
		{"되돌리는 날: 다시 오는 04:00에 날짜가 또 바뀌지 않는다", back, "2026-05-09T19:00:00Z", "2026-05-10"},
		{"되돌린 다음 날은 평소처럼 04:00에 바뀐다", back, "2026-05-10T18:59:59Z", "2026-05-10"},
		{"되돌린 다음 날 04:00", back, "2026-05-10T19:00:00Z", "2026-05-11"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, mustParse(t, tt.want), Of(mustInstant(t, tt.utc), tt.loc))
		})
	}
}

func TestOf_ZeroResults(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")

	tests := []struct {
		name string
		at   time.Time
		loc  *time.Location
		want Date
	}{
		{"시간대가 nil이면 빈 값이다", mustInstant(t, "2026-09-20T12:00:00Z"), nil, Date{}},
		{"time.Time의 빈 값은 UTC에서 0년 12월 31일이라 범위 밖이다", time.Time{}, time.UTC, Date{}},
		{"0001-01-01 04:00 UTC는 첫 날짜다", time.Date(1, time.January, 1, 4, 0, 0, 0, time.UTC), time.UTC, mustDate(t, 1, time.January, 1)},
		{"9999-12-31 한낮은 마지막 날짜다", time.Date(9999, time.December, 31, 12, 0, 0, 0, seoul), seoul, mustDate(t, 9999, time.December, 31)},
		{"10000-01-01 새벽은 아직 마지막 날짜다", time.Date(10000, time.January, 1, 3, 59, 59, 0, seoul), seoul, mustDate(t, 9999, time.December, 31)},
		{"10000-01-01 04:00부터는 범위 밖이다", time.Date(10000, time.January, 1, 4, 0, 0, 0, seoul), seoul, Date{}},
		{"아주 먼 미래는 범위 밖이다", time.Date(250000, time.June, 1, 12, 0, 0, 0, time.UTC), seoul, Date{}},
		{"기원전은 범위 밖이다", time.Date(-500, time.June, 1, 12, 0, 0, 0, time.UTC), seoul, Date{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Of(tt.at, tt.loc))
		})
	}
}

func TestBounds(t *testing.T) {
	seoul := mustLoadLocation(t, "Asia/Seoul")
	newYork := mustLoadLocation(t, "America/New_York")
	helsinki := mustLoadLocation(t, "Europe/Helsinki")
	hongKong := mustLoadLocation(t, "Asia/Hong_Kong")
	apia := mustLoadLocation(t, "Pacific/Apia")

	tests := []struct {
		name      string
		date      string
		loc       *time.Location
		wantStart string
		wantEnd   string
		wantHours float64
	}{
		{"서울은 전날 19:00Z부터 24시간이다", "2026-09-20", seoul, "2026-09-19T19:00:00Z", "2026-09-20T19:00:00Z", 24},
		{"UTC는 04:00Z부터다", "2026-09-20", time.UTC, "2026-09-20T04:00:00Z", "2026-09-21T04:00:00Z", 24},
		{"고정 시간대도 된다", "2026-09-20", time.FixedZone("", -3*secondsPerHour), "2026-09-20T07:00:00Z", "2026-09-21T07:00:00Z", 24},
		{"달의 마지막 날은 다음 달 1일 04:00에 끝난다", "2026-09-30", seoul, "2026-09-29T19:00:00Z", "2026-09-30T19:00:00Z", 24},
		{"윤일도 하루다", "2028-02-29", seoul, "2028-02-28T19:00:00Z", "2028-02-29T19:00:00Z", 24},
		{"해의 마지막 날은 새해 첫날 04:00에 끝난다", "2026-12-31", seoul, "2026-12-30T19:00:00Z", "2026-12-31T19:00:00Z", 24},
		{"지원 범위의 첫 날짜", "0001-01-01", time.UTC, "0001-01-01T04:00:00Z", "0001-01-02T04:00:00Z", 24},

		{"뉴욕: 시계를 앞으로 돌리는 날로 넘어가는 기록 날짜는 23시간이다", "2025-03-08", newYork, "2025-03-08T09:00:00Z", "2025-03-09T08:00:00Z", 23},
		{"뉴욕: 시계를 앞으로 돌린 날 자체는 24시간이다", "2025-03-09", newYork, "2025-03-09T08:00:00Z", "2025-03-10T08:00:00Z", 24},
		{"뉴욕: 시계를 뒤로 돌리는 날로 넘어가는 기록 날짜는 25시간이다", "2025-11-01", newYork, "2025-11-01T08:00:00Z", "2025-11-02T09:00:00Z", 25},
		{"뉴욕: 시계를 뒤로 돌린 날 자체는 24시간이다", "2025-11-02", newYork, "2025-11-02T09:00:00Z", "2025-11-03T09:00:00Z", 24},

		{"헬싱키 봄: 전환 순간이 곧 04:00이라 거기서 끝난다", "2025-03-29", helsinki, "2025-03-29T02:00:00Z", "2025-03-30T01:00:00Z", 23},
		{"헬싱키 봄: 새 날은 전환 순간에 시작한다", "2025-03-30", helsinki, "2025-03-30T01:00:00Z", "2025-03-31T01:00:00Z", 24},
		{"헬싱키 가을: 04:00 EEST가 아니라 한 시간 뒤의 04:00 EET에 끝난다", "2025-10-25", helsinki, "2025-10-25T01:00:00Z", "2025-10-26T02:00:00Z", 25},
		{"헬싱키 가을: 새 날은 04:00 EET에 시작한다", "2025-10-26", helsinki, "2025-10-26T02:00:00Z", "2025-10-27T02:00:00Z", 24},

		{"홍콩 1976: 04:00을 건너뛰기 전날은 03:30에 끝난다", "1976-04-17", hongKong, "1976-04-16T20:00:00Z", "1976-04-17T19:30:00Z", 23.5},
		{"홍콩 1976: 04:00이 없는 날은 건너뛴 직후(04:30)에 시작한다", "1976-04-18", hongKong, "1976-04-17T19:30:00Z", "1976-04-18T19:00:00Z", 23.5},

		{"사모아 2011: 하루를 건너뛰기 전날은 20시간이다", "2011-12-29", apia, "2011-12-29T14:00:00Z", "2011-12-30T10:00:00Z", 20},
		{"사모아 2011: 달력에 없던 날의 기록 날짜는 4시간이다", "2011-12-30", apia, "2011-12-30T10:00:00Z", "2011-12-30T14:00:00Z", 4},
		{"사모아 2011: 건너뛴 뒤의 첫 날은 24시간이다", "2011-12-31", apia, "2011-12-30T14:00:00Z", "2011-12-31T14:00:00Z", 24},

		{"04:00을 건너뛰기 전날", "2026-05-09", gapOverBoundary(t), "2026-05-08T19:00:00Z", "2026-05-09T18:30:00Z", 23.5},
		{"04:00을 건너뛰는 날", "2026-05-10", gapOverBoundary(t), "2026-05-09T18:30:00Z", "2026-05-10T18:00:00Z", 23.5},
		{"04:00을 넘겼다 되돌아가기 전날", "2026-05-09", backAcrossBoundary(t), "2026-05-08T18:00:00Z", "2026-05-09T18:00:00Z", 24},
		{"04:00을 넘겼다 되돌아가는 날은 처음 04:00에 시작해 한 번만 이어진다", "2026-05-10", backAcrossBoundary(t), "2026-05-09T18:00:00Z", "2026-05-10T19:00:00Z", 25},

		// 전환 규칙으로 계산하는 먼 미래에서 표준 라이브러리가 윤년 연말의 구간 끝을 잘못 어림하는 자리다.
		{"뉴욕: 윤년 12월 30일", "2028-12-30", newYork, "2028-12-30T09:00:00Z", "2028-12-31T09:00:00Z", 24},
		{"뉴욕: 윤년 12월 31일", "2028-12-31", newYork, "2028-12-31T09:00:00Z", "2029-01-01T09:00:00Z", 24},
		{"뉴욕: 윤년 다음 해 1월 1일", "2029-01-01", newYork, "2029-01-01T09:00:00Z", "2029-01-02T09:00:00Z", 24},
		{"뉴욕: 전환 표가 끝난 뒤의 윤년 12월 31일", "2040-12-31", newYork, "2040-12-31T09:00:00Z", "2041-01-01T09:00:00Z", 24},
		{"헬싱키: 전환 표가 끝난 뒤의 윤년 12월 31일", "2040-12-31", helsinki, "2040-12-31T02:00:00Z", "2041-01-01T02:00:00Z", 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := mustParse(t, tt.date).Bounds(tt.loc)

			assertInstant(t, mustInstant(t, tt.wantStart), start, "start")
			assertInstant(t, mustInstant(t, tt.wantEnd), end, "end")
			assert.InDelta(t, tt.wantHours, end.Sub(start).Hours(), 1e-9)
			assert.Equal(t, time.UTC, start.Location(), "DB에 넘길 값이라 UTC로 돌려준다")
			assert.Equal(t, time.UTC, end.Location())
		})
	}

	t.Run("마지막 날짜의 끝은 10000년 1월 1일 04:00이다", func(t *testing.T) {
		start, end := mustDate(t, 9999, time.December, 31).Bounds(time.UTC)

		assertInstant(t, time.Date(9999, time.December, 31, 4, 0, 0, 0, time.UTC), start)
		assertInstant(t, time.Date(10000, time.January, 1, 4, 0, 0, 0, time.UTC), end)
	})

	t.Run("빈 값이면 둘 다 빈 시각이다", func(t *testing.T) {
		start, end := Date{}.Bounds(seoul)

		assert.True(t, start.IsZero())
		assert.True(t, end.IsZero())
	})

	t.Run("시간대가 nil이면 둘 다 빈 시각이다", func(t *testing.T) {
		start, end := mustDate(t, 2026, time.September, 20).Bounds(nil)

		assert.True(t, start.IsZero())
		assert.True(t, end.IsZero())
	})

	t.Run("여러 날을 한 번에 찾을 때는 첫날의 start와 끝날의 end를 쓴다", func(t *testing.T) {
		first := mustDate(t, 2025, time.March, 3)
		last := first.AddDays(13)

		start, _ := first.Bounds(newYork)
		_, end := last.Bounds(newYork)

		assertInstant(t, mustInstant(t, "2025-03-03T09:00:00Z"), start)
		assertInstant(t, mustInstant(t, "2025-03-17T08:00:00Z"), end)
		assert.InDelta(t, 14*24-1, end.Sub(start).Hours(), 1e-9, "그 사이에 시계를 한 시간 앞으로 돌렸다")
	})
}

// 어느 시간대든 Of와 Bounds는 같은 경계를 말해야 한다.
// 날짜로 저장한 기록과 시각 범위로 찾은 기록이 어긋나지 않는다는 뜻이다.
func TestOfAndBoundsAgree(t *testing.T) {
	type span struct {
		zone string
		from string
		days int
	}
	spans := []span{
		{"Asia/Seoul", "2025-01-01", 400},
		{"UTC", "2025-01-01", 400},
		{"America/New_York", "2025-01-01", 400},
		{"America/New_York", "2028-10-01", 200}, // 윤년 연말
		{"America/New_York", "2040-10-01", 200}, // 전환 표가 끝난 뒤의 윤년 연말
		{"Europe/Helsinki", "2025-01-01", 400},
		{"Europe/Helsinki", "2044-10-01", 200},
		{"Europe/London", "2025-01-01", 400},
		{"Australia/Sydney", "2024-01-01", 400},    // 남반구, 윤년
		{"Australia/Lord_Howe", "2025-01-01", 400}, // 30분만 옮긴다
		{"America/Santiago", "2025-01-01", 400},    // 자정에 전환한다
		{"America/St_Johns", "2025-01-01", 400},    // UTC-03:30
		{"Pacific/Chatham", "2025-01-01", 400},     // UTC+12:45
		{"Pacific/Kiritimati", "2025-01-01", 60},   // UTC+14
		{"Etc/GMT+12", "2025-01-01", 60},           // UTC-12
		{"Asia/Kolkata", "2025-01-01", 60},         // UTC+05:30
		{"Africa/Casablanca", "2025-01-01", 400},   // 라마단에 맞춰 옮긴다
		{"Asia/Hong_Kong", "1976-01-01", 400},      // 04:00을 건너뛴 해
		{"Antarctica/Casey", "2019-01-01", 400},    // 세 시간을 건너뛴 해
		{"Pacific/Apia", "2011-06-01", 400},        // 하루를 통째로 건너뛴 해
		{"Asia/Seoul", "1987-01-01", 800},          // 서울에 일광 절약 시간이 있던 두 해
		{"America/Juneau", "1867-09-01", 100},      // 날짜 변경선이 옮겨져 같은 날짜가 두 번 왔다
		{"Asia/Manila", "1844-12-01", 60},          // 날짜 변경선이 옮겨져 하루가 사라졌다
	}

	check := func(t *testing.T, loc *time.Location, from Date, days int) {
		t.Helper()
		for i := range days {
			d := from.AddDays(i)
			start, end := d.Bounds(loc)

			require.Truef(t, start.Before(end), "%s: 기록 날짜는 비어 있지 않다", d)
			require.Equalf(t, d, Of(start, loc), "%s: start는 그날에 속한다", d)
			require.Equalf(t, d.AddDays(-1), Of(start.Add(-time.Nanosecond), loc), "%s: start 직전은 전날이다", d)
			require.Equalf(t, d, Of(end.Add(-time.Nanosecond), loc), "%s: end 직전은 그날이다", d)
			require.Equalf(t, d.AddDays(1), Of(end, loc), "%s: end는 다음 날이다", d)

			nextStart, _ := d.AddDays(1).Bounds(loc)
			require.Truef(t, end.Equal(nextStart), "%s: end는 다음 날의 start와 같다", d)

			// 하루 안의 순간을 37분 간격으로 훑는다. 전환이 있는 날의 안쪽까지 확인한다.
			for at := start; at.Before(end); at = at.Add(37 * time.Minute) {
				require.Equalf(t, d, Of(at, loc), "%s: %s", d, at.Format(time.RFC3339))
			}
		}
	}

	for _, s := range spans {
		t.Run(s.zone+" "+s.from+"부터", func(t *testing.T) {
			check(t, mustLoadLocation(t, s.zone), mustParse(t, s.from), s.days)
		})
	}
	t.Run("04:00을 건너뛰는 시험용 시간대", func(t *testing.T) {
		check(t, gapOverBoundary(t), mustParse(t, "2026-05-01"), 20)
	})
	t.Run("04:00을 넘겼다 되돌아가는 시험용 시간대", func(t *testing.T) {
		check(t, backAcrossBoundary(t), mustParse(t, "2026-05-01"), 20)
	})
	t.Run("고정 시간대", func(t *testing.T) {
		check(t, time.FixedZone("", 9*secondsPerHour), mustParse(t, "2026-09-01"), 60)
	})
}

func TestFirstInstantAtOrAfter(t *testing.T) {
	newYork := mustLoadLocation(t, "America/New_York")
	helsinki := mustLoadLocation(t, "Europe/Helsinki")

	tests := []struct {
		name string
		wall string
		loc  *time.Location
		want string
	}{
		{"뉴욕: 없는 시각 02:30은 건너뛴 직후다", "2025-03-09T02:30:00Z", newYork, "2025-03-09T07:00:00Z"},
		{"뉴욕: 두 번 오는 01:30은 먼저 오는 쪽이다", "2025-11-02T01:30:00Z", newYork, "2025-11-02T05:30:00Z"},
		{"헬싱키: 두 번 오는 03:30도 먼저 오는 쪽이다", "2025-10-26T03:30:00Z", helsinki, "2025-10-26T00:30:00Z"},
		{"헬싱키: 없는 시각 03:30은 건너뛴 직후다", "2025-03-30T03:30:00Z", helsinki, "2025-03-30T01:00:00Z"},
		{"UTC 차이가 터무니없이 큰 고정 시간대에서도 된다", "2026-09-20T04:00:00Z", time.FixedZone("", 100*secondsPerHour), "2026-09-16T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// wall은 벽시계 값을 UTC인 것처럼 적은 것이다.
			got := FirstInstantAtOrAfter(mustInstant(t, tt.wall), tt.loc)
			assertInstant(t, mustInstant(t, tt.want), got)
		})
	}
}
