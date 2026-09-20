// Package recorddate는 대화와 일기가 속하는 "기록 날짜"를 다룬다.
//
// 기록 날짜는 달력 날짜와 다르다. 하루가 자정이 아니라 새벽에 시작한다(DayStartHour).
// 어떤 순간이 어느 날의 기록인지 정하는 규칙을 이 패키지 한 곳에만 둬서,
// 저장, 집계, 화면이 모두 같은 날짜를 말하게 한다.
//
// 표준 라이브러리만 쓴다. 계산 코어가 이 패키지를 가져다 쓰기 때문이다.
package recorddate

import (
	"errors"
	"fmt"
	"time"
)

// DayStartHour는 기록 날짜가 바뀌는 현지 시각(시)이다.
//
// 하루는 자정이 아니라 새벽 4시에 시작한다. 잠들기 전 늦은 밤에 나눈 대화는
// 자정을 넘겼더라도 그 사람에게는 아직 "오늘" 있었던 일이기 때문이다.
// 그래서 01:00의 대화는 전날의 기록이고, 04:00:00부터 새 날의 기록이다.
const DayStartHour = 4

const (
	// 연도를 네 자리로 묶어 둔다. 그래야 String이 언제나 YYYY-MM-DD 열 글자이고 Parse로 되돌릴 수 있다.
	minYear = 1
	maxYear = 9999

	// 0001-01-01부터 9999-12-31까지의 날 수다. 이보다 크게 옮기면 어디서 출발해도 범위 밖이다.
	maxDaySpan = 3652058

	secondsPerDay = 24 * 60 * 60
)

// ErrInvalid는 달력에 없거나 지원 범위를 벗어난 날짜, 또는 형식이 틀린 문자열을 뜻한다.
// 오류 메시지에는 입력받은 문자열을 넣지 않는다. 날짜 자리에 다른 글이 잘못 들어와도 로그로 새지 않는다.
var ErrInvalid = errors.New("invalid record date")

// Date는 기록 날짜 하루다. ==로 비교할 수 있고 맵의 키로 쓸 수 있다.
//
// 빈 값(Date{})은 "날짜 없음"이다. 빈 값이 아닌 Date는 언제나 달력에 실제로 있는 날짜다.
// 필드를 밖에서 채울 수 없게 막아 둔 것도 이 약속을 지키기 위해서다.
type Date struct {
	year  int
	month time.Month
	day   int
}

// New는 연, 월, 일로 Date를 만든다. 0001-01-01부터 9999-12-31까지, 달력에 있는 날짜만 받는다.
//
// DB의 date 컬럼처럼 이미 달력 날짜인 값을 옮길 때는 New(t.Date())로 쓴다.
// 그런 값에 Of를 쓰면 새벽 경계 규칙이 한 번 더 적용되어 하루가 밀린다.
func New(year int, month time.Month, day int) (Date, error) {
	if year < minYear || year > maxYear {
		return Date{}, fmt.Errorf("%w: year %d is out of range", ErrInvalid, year)
	}
	if month < time.January || month > time.December {
		return Date{}, fmt.Errorf("%w: month %d is out of range", ErrInvalid, int(month))
	}
	if day < 1 || day > daysIn(year, month) {
		return Date{}, fmt.Errorf("%w: %04d-%02d has no day %d", ErrInvalid, year, int(month), day)
	}
	return Date{year: year, month: month, day: day}, nil
}

// Of는 순간 t가 loc 시간대에서 어느 기록 날짜에 속하는지 돌려준다.
// t에 붙어 있는 Location은 보지 않는다. 같은 순간이면 UTC로 적혀 있든 현지 시각으로 적혀 있든 결과가 같다.
//
// 기록 날짜 d는 loc의 벽시계가 처음으로 d일 04:00 이상을 가리키는 순간에 시작해서
// 다음 날짜가 시작하는 순간에 끝난다. 일광 절약 시간이 없는 시간대(Asia/Seoul)에서는
// "현지 시각이 04:00 전이면 전날"과 같은 말이다.
//
// 일광 절약 시간이 있는 시간대에서는 다음과 같다(America/New_York 기준).
//   - 시계를 앞으로 돌리는 날(02:00 → 03:00): 01:30도 03:30도 전날이고 04:00부터 새 날이다.
//     끝나 가던 전날의 기록 날짜는 23시간이 된다.
//   - 시계를 뒤로 돌리는 날(02:00 → 01:00): 두 번 오는 01:30은 둘 다 전날이다.
//     전날의 기록 날짜는 25시간이 된다.
//   - 건너뛰는 구간이 04:00을 덮는 시간대에서는(03:30 → 04:30) 건너뛴 직후의 순간에 새 날이 시작한다.
//   - 04:00을 넘긴 뒤에 시계를 04:00 앞으로 되돌리는 시간대에서도 이미 시작한 날짜는 되돌아가지 않는다.
//     그래서 한 기록 날짜는 언제나 끊기지 않은 구간 하나다.
//
// 이 정의에서는 Of(t, loc) == d 인 것과 t가 d.Bounds(loc)의 [start, end)에 드는 것이 같은 말이다.
//
// loc가 nil이거나 결과가 지원 범위를 벗어나면 빈 값을 돌려준다.
// nil을 UTC로 봐주지 않는 이유: 시간대를 빠뜨린 실수가 조용히 아홉 시간 어긋난 날짜로 저장되는 것을 막는다.
func Of(t time.Time, loc *time.Location) Date {
	if loc == nil {
		return Date{}
	}

	local := t.In(loc)
	year, month, day := local.Date()
	// 범위를 한참 벗어난 시각으로 아래 반복문을 돌리지 않는다.
	if year < minYear-1 || year > maxYear+1 {
		return Date{}
	}
	if local.Hour() < DayStartHour {
		day--
	}
	// 벽시계가 04:00을 넘겼다가 그 앞으로 되돌아간 구간에서는 벽시계만 보면 전날로 읽힌다.
	// 다음 날짜가 이미 시작했는지를 순간끼리 견주어 바로잡는다. 그런 구간이 아니면 한 번 확인하고 끝난다.
	for !t.Before(dayStart(year, month, day+1, loc)) {
		day++
	}
	return normalized(year, month, day)
}

// Parse는 YYYY-MM-DD 문자열을 읽는다. 앞뒤 공백, 한 자리 월, 시각이 붙은 값은 받지 않는다.
//
// time.Parse를 쓰지 않는 이유: 실패하면 입력받은 문자열을 오류 메시지에 그대로 담는다.
func Parse(s string) (Date, error) {
	const layout = "YYYY-MM-DD"
	if len(s) != len(layout) || s[4] != '-' || s[7] != '-' {
		return Date{}, fmt.Errorf("%w: want %s", ErrInvalid, layout)
	}
	year, okYear := parseDigits(s[0:4])
	month, okMonth := parseDigits(s[5:7])
	day, okDay := parseDigits(s[8:10])
	if !okYear || !okMonth || !okDay {
		return Date{}, fmt.Errorf("%w: want %s", ErrInvalid, layout)
	}
	return New(year, time.Month(month), day)
}

func (d Date) Year() int         { return d.year }
func (d Date) Month() time.Month { return d.month }
func (d Date) Day() int          { return d.day }

// IsZero는 빈 값("날짜 없음")인지 알려준다. JSON 태그 omitzero도 이 메서드를 본다.
func (d Date) IsZero() bool {
	return d == Date{}
}

// String은 YYYY-MM-DD로 적는다. 빈 값은 0000-00-00으로 찍혀 눈에 띈다.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.year, int(d.month), d.day)
}

// AddDays는 n일 뒤(음수면 앞)의 날짜를 돌려준다.
// 결과가 지원 범위를 벗어나거나 d가 빈 값이면 빈 값을 돌려준다.
func (d Date) AddDays(n int) Date {
	if d.IsZero() || n > maxDaySpan || n < -maxDaySpan {
		return Date{}
	}
	return normalized(d.year, d.month, d.day+n)
}

// DaysSince는 other에서 d까지 며칠인지 돌려준다. d가 더 앞선 날짜면 음수다.
// 시각이 아니라 달력으로 세기 때문에 일광 절약 시간으로 하루 길이가 달라져도 영향받지 않는다.
//
// 빈 값은 지원 범위보다 앞선 아주 먼 과거로 계산된다.
// 날짜가 빠진 기록이 "최근 며칠" 안에 잘못 끼어드는 일은 없다.
func (d Date) DaysSince(other Date) int {
	return d.dayNumber() - other.dayNumber()
}

func (d Date) Before(other Date) bool { return d.Compare(other) < 0 }
func (d Date) After(other Date) bool  { return d.Compare(other) > 0 }

// Compare는 d가 other보다 앞서면 -1, 같으면 0, 뒤면 +1을 돌려준다. 빈 값은 모든 날짜보다 앞선다.
// slices.SortFunc(dates, recorddate.Date.Compare)처럼 쓸 수 있다.
func (d Date) Compare(other Date) int {
	switch {
	case d.year != other.year:
		return compareInt(d.year, other.year)
	case d.month != other.month:
		return compareInt(int(d.month), int(other.month))
	default:
		return compareInt(d.day, other.day)
	}
}

func (d Date) Weekday() time.Weekday {
	return d.UTCMidnight().Weekday()
}

// UTCMidnight은 같은 연월일의 UTC 자정을 돌려준다. 빈 값이면 time.Time의 빈 값을 돌려준다.
// DB의 date 컬럼에 넣을 값을 만들 때 쓴다.
//
// 이 시각은 달력 날짜를 담는 그릇일 뿐이고 기록 날짜가 시작하는 순간이 아니다.
// 시각 범위로 찾을 때는 Bounds를 쓴다.
func (d Date) UTCMidnight() time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, time.UTC)
}

// Bounds는 loc 시간대에서 기록 날짜 d에 속하는 순간의 범위 [start, end)를 UTC로 돌려준다.
// 현지 04:00부터 다음 날 현지 04:00 직전까지다. end는 다음 날짜의 start와 같아서 날짜끼리 겹치거나 비지 않는다.
// 시각 컬럼을 날짜로 찾을 때 `at >= start AND at < end`로 쓴다.
//
// 일광 절약 시간으로 시계가 바뀌는 날에는 길이가 24시간이 아닐 수 있다. 경계를 정하는 규칙은 Of와 같다.
// d가 빈 값이거나 loc가 nil이면 둘 다 time.Time의 빈 값이다.
func (d Date) Bounds(loc *time.Location) (start, end time.Time) {
	if d.IsZero() || loc == nil {
		return time.Time{}, time.Time{}
	}
	return dayStart(d.year, d.month, d.day, loc), dayStart(d.year, d.month, d.day+1, loc)
}

// MarshalText는 JSON 값과 JSON 객체의 키에 YYYY-MM-DD로 적히게 한다.
// 빈 값은 오류다. 채우지 않은 날짜가 0000-00-00으로 밖에 나가는 것을 막는다.
// 없을 수 있는 날짜는 포인터로 두거나 omitzero 태그를 붙인다.
func (d Date) MarshalText() ([]byte, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("%w: zero value cannot be encoded", ErrInvalid)
	}
	return []byte(d.String()), nil
}

// UnmarshalText는 Parse와 같은 규칙으로 읽는다. 실패하면 d를 바꾸지 않는다.
func (d *Date) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// dayNumber는 1970-01-01부터 센 날 수다. UTC 자정은 언제나 하루의 배수라서 나눗셈이 딱 떨어진다.
func (d Date) dayNumber() int {
	return int(time.Date(d.year, d.month, d.day, 0, 0, 0, 0, time.UTC).Unix() / secondsPerDay)
}

// normalized는 넘치는 일(0일, 32일, 음수)을 달력에 맞게 올림, 내림한다. 범위를 벗어나면 빈 값이다.
func normalized(year int, month time.Month, day int) Date {
	y, m, d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Date()
	if y < minYear || y > maxYear {
		return Date{}
	}
	return Date{year: y, month: m, day: d}
}

// daysIn은 그 달의 날 수다. 다음 달 0일은 이번 달 마지막 날이다.
func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// parseDigits는 ASCII 숫자만 받는다. strconv.Atoi는 부호를 받아 주기 때문에 쓰지 않는다.
func parseDigits(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// dayStart는 기록 날짜가 시작하는 순간을 UTC로 돌려준다. day는 넘쳐도 된다(32일은 다음 달 1일).
func dayStart(year int, month time.Month, day int, loc *time.Location) time.Time {
	wall := time.Date(year, month, day, DayStartHour, 0, 0, 0, time.UTC)
	return FirstInstantAtOrAfter(wall, loc).UTC()
}

// FirstInstantAtOrAfter는 loc의 벽시계가 처음으로 wall 이상을 가리키는 순간을 찾는다.
// wall은 벽시계에 적힌 연월일시분초를 UTC인 것처럼 담은 값이다.
//
// time.Date(..., loc)를 쓰지 않는 이유: 없는 시각이나 두 번 오는 시각을 넘기면
// 어느 쪽 순간을 돌려줄지 정해져 있지 않고, 실제로 시간대마다 다르게 고른다.
// 여기서는 시간대의 구간을 앞에서부터 훑어 항상 가장 이른 순간을 고른다.
//
// 밖에서도 쓰게 열어 둔다. 가짜 시계가 "다음 저녁 8시"로 건너뛸 때 같은 계산이 필요한데, 따로 베껴 두면
// 한쪽만 고쳤을 때 가짜 시계가 멈춰 서는 순간과 기록 날짜의 경계가 시계를 바꾸는 시간대에서 어긋난다.
// 그 어긋남은 몇 주를 돌려 보는 실행에서만 드러난다. 그래서 계산은 표준 라이브러리만 쓰는 이 패키지에 하나만 둔다.
func FirstInstantAtOrAfter(wall time.Time, loc *time.Location) time.Time {
	// 실제 시간대의 UTC 차이는 이 값을 넘지 않는다.
	// wall에서 이만큼 앞선 순간부터 훑으면 그 이전에는 답이 있을 수 없다.
	const maxUTCOffset = 26 * time.Hour

	probe := wall.Add(-maxUTCOffset).In(loc)
	for {
		periodStart, periodEnd := probe.ZoneBounds()
		_, offsetSeconds := probe.Zone()

		// 이 구간의 UTC 차이가 그대로 이어진다고 할 때 벽시계가 wall을 가리키는 순간이다.
		candidate := wall.Add(-time.Duration(offsetSeconds) * time.Second)
		// 구간이 시작할 때 벽시계가 이미 wall을 지나 있으면(건너뛴 경우) 구간의 첫 순간이 답이다.
		if !periodStart.IsZero() && candidate.Before(periodStart) {
			candidate = periodStart
		}
		if periodEnd.IsZero() || candidate.Before(periodEnd) {
			return candidate
		}
		// 표준 라이브러리는 전환 규칙으로 계산하는 먼 미래의 구간 끝을 어림값으로 돌려주는데,
		// 윤년의 12월 31일에는 그 값이 물어본 순간보다 앞선다. 그대로 따라가면 제자리를 맴돈다.
		// 연말에는 시계를 바꾸는 시간대가 없으므로 지금 구간의 UTC 차이로 구한 값이 답이다.
		if !periodEnd.After(probe) {
			return candidate
		}
		probe = periodEnd
	}
}
