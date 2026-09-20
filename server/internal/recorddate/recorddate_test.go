package recorddate

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	valid := []struct {
		name  string
		year  int
		month time.Month
		day   int
		want  string
	}{
		{"평범한 날짜", 2026, time.September, 20, "2026-09-20"},
		{"윤년의 2월 29일", 2028, time.February, 29, "2028-02-29"},
		{"400으로 나뉘는 해의 2월 29일", 2000, time.February, 29, "2000-02-29"},
		{"31일이 있는 달의 31일", 2026, time.December, 31, "2026-12-31"},
		{"30일까지인 달의 30일", 2026, time.April, 30, "2026-04-30"},
		{"지원 범위의 첫날", 1, time.January, 1, "0001-01-01"},
		{"지원 범위의 마지막 날", 9999, time.December, 31, "9999-12-31"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			d, err := New(tt.year, tt.month, tt.day)

			require.NoError(t, err)
			assert.Equal(t, tt.want, d.String())
			assert.Equal(t, tt.year, d.Year())
			assert.Equal(t, tt.month, d.Month())
			assert.Equal(t, tt.day, d.Day())
			assert.False(t, d.IsZero())
		})
	}

	invalid := []struct {
		name  string
		year  int
		month time.Month
		day   int
	}{
		{"평년의 2월 29일", 2026, time.February, 29},
		{"100으로 나뉘는 해의 2월 29일", 2100, time.February, 29},
		{"2월 30일", 2028, time.February, 30},
		{"4월 31일", 2026, time.April, 31},
		{"32일", 2026, time.January, 32},
		{"0일", 2026, time.January, 0},
		{"음수 일", 2026, time.January, -1},
		{"0월", 2026, 0, 1},
		{"13월", 2026, 13, 1},
		{"음수 월", 2026, -1, 1},
		{"0년", 0, time.January, 1},
		{"음수 연도", -1, time.January, 1},
		{"다섯 자리 연도", 10000, time.January, 1},
		{"전부 0", 0, 0, 0},
		{"정수 한계값", math.MaxInt, math.MaxInt, math.MaxInt},
		{"정수 최솟값", math.MinInt, math.MinInt, math.MinInt},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			d, err := New(tt.year, tt.month, tt.day)

			require.ErrorIs(t, err, ErrInvalid)
			assert.True(t, d.IsZero(), "실패하면 빈 값을 돌려준다")
		})
	}

	t.Run("달력 날짜를 든 time.Time은 New(t.Date())로 옮긴다", func(t *testing.T) {
		column := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)

		d, err := New(column.Date())

		require.NoError(t, err)
		assert.Equal(t, "2026-09-20", d.String())
	})
}

func TestParse(t *testing.T) {
	valid := []struct {
		name string
		in   string
		want Date
	}{
		{"평범한 날짜", "2026-09-20", mustDate(t, 2026, time.September, 20)},
		{"윤일", "2028-02-29", mustDate(t, 2028, time.February, 29)},
		{"앞자리가 0인 연도", "0001-01-01", mustDate(t, 1, time.January, 1)},
		{"마지막 날짜", "9999-12-31", mustDate(t, 9999, time.December, 31)},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.in, got.String(), "다시 적으면 같은 문자열이다")
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"빈 문자열", ""},
		{"공백", " "},
		{"앞에 공백", " 2026-09-20"},
		{"뒤에 공백", "2026-09-20 "},
		{"뒤에 줄바꿈", "2026-09-20\n"},
		{"한 자리 월", "2026-9-20"},
		{"한 자리 일", "2026-09-2"},
		{"한 자리 월을 공백으로 채움", "2026- 9-20"},
		{"두 자리 연도", "26-09-20"},
		{"다섯 자리 연도", "12026-09-20"},
		{"빗금 구분", "2026/09/20"},
		{"점 구분", "2026.09.20"},
		{"첫 구분자만 다름", "2026/09-20"},
		{"둘째 구분자만 다름", "2026-09/20"},
		{"둘째 구분자 자리에 숫자", "2026-09020"},
		{"구분자 없음", "20260920"},
		{"구분자 자리가 다름", "202-609-20"},
		{"시각이 붙음", "2026-09-20T04:00:00Z"},
		{"시각이 공백 뒤에 붙음", "2026-09-20 04:00"},
		{"일-월-연 순서", "20-09-2026"},
		{"부호가 붙은 연도", "+026-09-20"},
		{"음수 연도", "-026-09-20"},
		{"부호가 붙은 월", "2026-+9-20"},
		{"전각 숫자", "２０２６-09-20"},
		{"열 바이트를 채운 아랍 숫자", "2026-09-\u0662"},
		{"영문자", "abcd-ef-gh"},
		{"16진수", "0x26-09-20"},
		{"지수 표기", "2e03-09-20"},
		{"날짜가 아닌 글", "오늘은 아무것도 하기 싫었어요"},
		{"열 바이트짜리 글", "가나다a"},
		{"NUL 바이트", "2026-09-2\x00"},
		{"0월", "2026-00-20"},
		{"13월", "2026-13-01"},
		{"0일", "2026-09-00"},
		{"9월 31일", "2026-09-31"},
		{"평년의 2월 29일", "2026-02-29"},
		{"100으로 나뉘는 해의 2월 29일", "2100-02-29"},
		{"0년", "0000-01-01"},
		{"빈 값을 적은 문자열", "0000-00-00"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)

			require.ErrorIs(t, err, ErrInvalid)
			assert.True(t, got.IsZero(), "실패하면 빈 값을 돌려준다")
			if len(tt.in) > 1 {
				assert.NotContains(t, err.Error(), tt.in, "입력받은 글을 오류 메시지에 담지 않는다")
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		name string
		date Date
		want string
	}{
		{"월과 일을 두 자리로 채운다", mustDate(t, 2026, time.January, 5), "2026-01-05"},
		{"연도를 네 자리로 채운다", mustDate(t, 33, time.April, 3), "0033-04-03"},
		{"빈 값은 눈에 띄게 찍힌다", Date{}, "0000-00-00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.date.String())
			assert.Len(t, tt.date.String(), 10)
		})
	}
}

func TestAddDays(t *testing.T) {
	tests := []struct {
		name string
		from string
		n    int
		want string
	}{
		{"0일은 그대로다", "2026-09-20", 0, "2026-09-20"},
		{"다음 날", "2026-09-20", 1, "2026-09-21"},
		{"전날", "2026-09-20", -1, "2026-09-19"},
		{"한 주 뒤", "2026-09-20", 7, "2026-09-27"},
		{"두 주 전", "2026-09-20", -14, "2026-09-06"},
		{"달을 넘어간다", "2026-09-30", 1, "2026-10-01"},
		{"달을 거슬러 간다", "2026-10-01", -1, "2026-09-30"},
		{"여러 달을 거슬러 간다", "2026-09-20", -90, "2026-06-22"},
		{"해를 넘어간다", "2026-12-31", 1, "2027-01-01"},
		{"해를 거슬러 간다", "2027-01-01", -1, "2026-12-31"},
		{"평년 2월 28일 다음은 3월 1일이다", "2026-02-28", 1, "2026-03-01"},
		{"윤년 2월 28일 다음은 2월 29일이다", "2028-02-28", 1, "2028-02-29"},
		{"윤일 다음은 3월 1일이다", "2028-02-29", 1, "2028-03-01"},
		{"3월 1일에서 거슬러 가면 윤일이다", "2028-03-01", -1, "2028-02-29"},
		{"평년 3월 1일에서 거슬러 가면 2월 28일이다", "2026-03-01", -1, "2026-02-28"},
		{"윤일에서 한 해 뒤는 2월 28일이다", "2028-02-29", 365, "2029-02-28"},
		{"윤일에서 한 해 전은 3월 1일이다", "2028-02-29", -365, "2027-03-01"},
		{"윤년을 끼면 366일이 한 해다", "2028-01-01", 366, "2029-01-01"},
		{"1900년은 윤년이 아니다", "1900-02-28", 1, "1900-03-01"},
		{"여섯 주 뒤", "2026-09-01", 42, "2026-10-13"},
		{"만 일 뒤", "2026-09-20", 10000, "2054-02-05"},
		{"범위의 첫날까지 간다", "0001-01-02", -1, "0001-01-01"},
		{"범위의 마지막 날까지 간다", "9999-12-30", 1, "9999-12-31"},
		{"범위의 끝에서 끝까지 간다", "0001-01-01", maxDaySpan, "9999-12-31"},
		{"범위의 끝에서 처음까지 간다", "9999-12-31", -maxDaySpan, "0001-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, mustParse(t, tt.want), mustParse(t, tt.from).AddDays(tt.n))
		})
	}

	zero := []struct {
		name string
		from Date
		n    int
	}{
		{"첫날보다 앞으로 가면 빈 값이다", mustDate(t, 1, time.January, 1), -1},
		{"마지막 날보다 뒤로 가면 빈 값이다", mustDate(t, 9999, time.December, 31), 1},
		{"범위보다 크게 옮기면 빈 값이다", mustDate(t, 1, time.January, 1), maxDaySpan + 1},
		{"범위보다 크게 거슬러 가면 빈 값이다", mustDate(t, 9999, time.December, 31), -maxDaySpan - 1},
		{"정수 최댓값을 넘겨도 넘치지 않고 빈 값이다", mustDate(t, 2026, time.September, 20), math.MaxInt},
		{"정수 최솟값을 넘겨도 넘치지 않고 빈 값이다", mustDate(t, 2026, time.September, 20), math.MinInt},
		{"빈 값에 더해도 빈 값이다", Date{}, 1},
		{"빈 값에서 빼도 빈 값이다", Date{}, -1},
		{"빈 값에 0을 더해도 빈 값이다", Date{}, 0},
		{"빈 값에 크게 더해 지원 범위 안에 들어와도 빈 값이다", Date{}, 1000},
		{"빈 값에 범위 전체를 더해도 빈 값이다", Date{}, maxDaySpan},
	}
	for _, tt := range zero {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.from.AddDays(tt.n).IsZero())
		})
	}

	t.Run("받은 값을 바꾸지 않는다", func(t *testing.T) {
		d := mustDate(t, 2026, time.September, 20)

		_ = d.AddDays(5)

		assert.Equal(t, "2026-09-20", d.String())
	})
}

func TestDaysSince(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		other string
		want  int
	}{
		{"같은 날은 0이다", "2026-09-20", "2026-09-20", 0},
		{"어제로부터 1일", "2026-09-20", "2026-09-19", 1},
		{"내일로부터 -1일", "2026-09-20", "2026-09-21", -1},
		{"한 주", "2026-09-20", "2026-09-13", 7},
		{"달을 건너서", "2026-10-01", "2026-09-30", 1},
		{"해를 건너서", "2027-01-01", "2026-12-31", 1},
		{"평년 2월을 건너서", "2026-03-01", "2026-02-28", 1},
		{"윤년 2월을 건너서", "2028-03-01", "2028-02-28", 2},
		{"평년 한 해", "2027-01-01", "2026-01-01", 365},
		{"윤년 한 해", "2029-01-01", "2028-01-01", 366},
		{"여섯 주", "2026-10-13", "2026-09-01", 42},
		{"1970년 이전으로 걸쳐도 센다", "1970-01-02", "1969-12-31", 2},
		{"지원 범위 전체", "9999-12-31", "0001-01-01", maxDaySpan},
		{"지원 범위 전체를 거꾸로", "0001-01-01", "9999-12-31", -maxDaySpan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			date, other := mustParse(t, tt.date), mustParse(t, tt.other)

			assert.Equal(t, tt.want, date.DaysSince(other))
			assert.Equal(t, -tt.want, other.DaysSince(date), "방향을 바꾸면 부호만 바뀐다")
			assert.Equal(t, date, other.AddDays(tt.want), "그만큼 더하면 같은 날짜가 된다")
		})
	}

	t.Run("AddDays와 서로 되돌린다", func(t *testing.T) {
		base := mustDate(t, 2026, time.September, 20)
		for _, n := range []int{-730000, -36525, -1461, -366, -365, -60, -31, -1, 0, 1, 28, 29, 59, 60, 365, 366, 1461, 36524, 146097, 2900000} {
			moved := base.AddDays(n)

			require.False(t, moved.IsZero(), "n=%d", n)
			assert.Equal(t, n, moved.DaysSince(base), "n=%d", n)
			assert.Equal(t, -n, base.DaysSince(moved), "n=%d", n)
			assert.Equal(t, base, moved.AddDays(-n), "n=%d", n)
		}
	})

	t.Run("일광 절약 시간으로 하루 길이가 달라져도 달력으로 센다", func(t *testing.T) {
		newYork := mustLoadLocation(t, "America/New_York")
		before := Of(mustInstant(t, "2025-03-08T17:00:00Z"), newYork)
		after := Of(mustInstant(t, "2025-03-10T16:00:00Z"), newYork) // 47시간 뒤

		assert.Equal(t, 2, after.DaysSince(before))
	})

	t.Run("빈 값은 아주 먼 과거로 계산된다", func(t *testing.T) {
		today := mustDate(t, 2026, time.September, 20)
		earliest := mustDate(t, 1, time.January, 1)

		assert.Greater(t, today.DaysSince(Date{}), 700000)
		assert.Less(t, Date{}.DaysSince(today), -700000)
		assert.Positive(t, earliest.DaysSince(Date{}), "가장 이른 날짜보다도 앞선다")
		assert.Zero(t, Date{}.DaysSince(Date{}))
	})
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a    Date
		b    Date
		want int
	}{
		{"같은 날짜", mustDate(t, 2026, time.September, 20), mustDate(t, 2026, time.September, 20), 0},
		{"일이 앞선다", mustDate(t, 2026, time.September, 19), mustDate(t, 2026, time.September, 20), -1},
		{"월이 앞서면 일이 커도 앞선다", mustDate(t, 2026, time.August, 31), mustDate(t, 2026, time.September, 1), -1},
		{"연이 앞서면 월과 일이 커도 앞선다", mustDate(t, 2025, time.December, 31), mustDate(t, 2026, time.January, 1), -1},
		{"빈 값은 가장 이른 날짜보다 앞선다", Date{}, mustDate(t, 1, time.January, 1), -1},
		{"빈 값끼리는 같다", Date{}, Date{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.a.Compare(tt.b))
			assert.Equal(t, -tt.want, tt.b.Compare(tt.a), "뒤집으면 부호가 바뀐다")

			assert.Equal(t, tt.want < 0, tt.a.Before(tt.b))
			assert.Equal(t, tt.want > 0, tt.a.After(tt.b))
			assert.Equal(t, tt.want > 0, tt.b.Before(tt.a))
			assert.Equal(t, tt.want < 0, tt.b.After(tt.a))
			assert.Equal(t, tt.want == 0, tt.a == tt.b, "Compare가 0인 것과 ==는 같은 말이다")
		})
	}

	t.Run("DaysSince의 부호와 맞는다", func(t *testing.T) {
		base := mustDate(t, 2026, time.September, 20)
		for _, n := range []int{-400, -31, -1, 0, 1, 31, 400} {
			other := base.AddDays(n)
			assert.Equal(t, compareInt(n, 0), other.Compare(base), "n=%d", n)
		}
	})

	t.Run("정렬에 쓸 수 있다", func(t *testing.T) {
		dates := []Date{
			mustParse(t, "2026-09-20"),
			mustParse(t, "2025-12-31"),
			{},
			mustParse(t, "2026-01-01"),
			mustParse(t, "2026-09-03"),
		}

		slices.SortFunc(dates, Date.Compare)

		var got []string
		for _, d := range dates {
			got = append(got, d.String())
		}
		assert.Equal(t, []string{"0000-00-00", "2025-12-31", "2026-01-01", "2026-09-03", "2026-09-20"}, got)
	})
}

func TestWeekday(t *testing.T) {
	tests := []struct {
		name string
		date string
		want time.Weekday
	}{
		{"2026-09-20은 일요일이다", "2026-09-20", time.Sunday},
		{"2026-09-21은 월요일이다", "2026-09-21", time.Monday},
		{"2028년의 윤일은 화요일이다", "2028-02-29", time.Tuesday},
		{"2000-01-01은 토요일이다", "2000-01-01", time.Saturday},
		{"1970-01-01은 목요일이다", "1970-01-01", time.Thursday},
		{"0001-01-01은 월요일이다", "0001-01-01", time.Monday},
		{"9999-12-31은 금요일이다", "9999-12-31", time.Friday},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mustParse(t, tt.date).Weekday())
		})
	}

	t.Run("하루에 하나씩 넘어가고 이레마다 돌아온다", func(t *testing.T) {
		d := mustParse(t, "2027-12-20")
		for range 800 {
			next := d.AddDays(1)
			assert.Equal(t, (d.Weekday()+1)%7, next.Weekday(), "%s", next)
			assert.Equal(t, d.Weekday(), d.AddDays(7).Weekday(), "%s", d)
			d = next
		}
	})

	t.Run("기록 날짜의 요일은 새벽 대화에도 전날의 요일이다", func(t *testing.T) {
		seoul := mustLoadLocation(t, "Asia/Seoul")
		mondayDawn := time.Date(2026, time.September, 21, 1, 0, 0, 0, seoul)

		require.Equal(t, time.Monday, mondayDawn.Weekday())
		assert.Equal(t, time.Sunday, Of(mondayDawn, seoul).Weekday())
	})
}

func TestUTCMidnight(t *testing.T) {
	t.Run("같은 연월일의 UTC 자정이다", func(t *testing.T) {
		got := mustDate(t, 2026, time.September, 20).UTCMidnight()

		assert.Equal(t, time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC), got)
	})

	t.Run("New(t.Date())로 되돌아온다", func(t *testing.T) {
		for _, s := range []string{"0001-01-01", "1969-12-31", "2026-09-20", "2028-02-29", "9999-12-31"} {
			d := mustParse(t, s)

			back, err := New(d.UTCMidnight().Date())

			require.NoError(t, err)
			assert.Equal(t, d, back)
		}
	})

	t.Run("빈 값이면 빈 시각이다", func(t *testing.T) {
		assert.True(t, Date{}.UTCMidnight().IsZero())
	})

	t.Run("이 값에 Of를 쓰면 하루가 밀리므로 New(t.Date())를 써야 한다", func(t *testing.T) {
		d := mustDate(t, 2026, time.September, 20)

		assert.Equal(t, d.AddDays(-1), Of(d.UTCMidnight(), time.UTC))
	})
}

// 표준 라이브러리에 기대지 않고 달력을 직접 넘겨 보며 AddDays, DaysSince, Parse, Weekday를 맞춰 본다.
func TestCalendarWalk(t *testing.T) {
	isLeap := func(year int) bool {
		return year%4 == 0 && (year%100 != 0 || year%400 == 0)
	}
	daysInMonth := func(year, month int) int {
		switch month {
		case 2:
			if isLeap(year) {
				return 29
			}
			return 28
		case 4, 6, 9, 11:
			return 30
		default:
			return 31
		}
	}

	// 1896년부터 2104년까지: 윤년이 아닌 1900년과 2100년, 윤년인 2000년을 지난다.
	year, month, day := 1896, 1, 1
	first := mustDate(t, year, time.Month(month), day)
	d := first
	weekday := time.Wednesday // 1896-01-01
	count := 0
	for year < 2105 {
		require.Equal(t, year, d.Year())
		require.Equal(t, time.Month(month), d.Month())
		require.Equal(t, day, d.Day())
		require.Equal(t, weekday, d.Weekday(), "%s", d)
		require.Equal(t, count, d.DaysSince(first), "%s", d)
		require.Equal(t, d, first.AddDays(count), "%s", d)
		require.Equal(t, d, mustParse(t, d.String()))

		day++
		if day > daysInMonth(year, month) {
			day = 1
			month++
		}
		if month > 12 {
			month = 1
			year++
		}
		weekday = (weekday + 1) % 7
		count++

		next := d.AddDays(1)
		require.True(t, next.After(d))
		require.True(t, d.Before(next))
		require.Equal(t, d, next.AddDays(-1))
		d = next
	}
	assert.Equal(t, 76336, count, "209년 동안의 날 수")
}

func TestComparableAndMapKey(t *testing.T) {
	t.Run("따로 만든 같은 날짜는 ==로 같다", func(t *testing.T) {
		seoul := mustLoadLocation(t, "Asia/Seoul")
		a := mustDate(t, 2026, time.September, 20)
		b := mustParse(t, "2026-09-20")
		c := Of(time.Date(2026, time.September, 21, 3, 0, 0, 0, seoul), seoul)
		d := mustDate(t, 2026, time.September, 13).AddDays(7)

		allSame := a == b && b == c && c == d
		assert.True(t, allSame)
		differs := a != a.AddDays(1)
		assert.True(t, differs)
	})

	t.Run("맵의 키로 써서 날짜별로 모은다", func(t *testing.T) {
		seoul := mustLoadLocation(t, "Asia/Seoul")
		turns := []time.Time{
			time.Date(2026, time.September, 20, 21, 0, 0, 0, seoul),
			time.Date(2026, time.September, 20, 23, 50, 0, 0, seoul),
			time.Date(2026, time.September, 21, 0, 40, 0, 0, seoul), // 자정을 넘겼지만 같은 날의 대화다.
			time.Date(2026, time.September, 21, 20, 0, 0, 0, seoul),
		}

		perDay := map[Date]int{}
		for _, at := range turns {
			perDay[Of(at, seoul)]++
		}

		assert.Equal(t, map[Date]int{
			mustDate(t, 2026, time.September, 20): 3,
			mustDate(t, 2026, time.September, 21): 1,
		}, perDay)
	})
}

func TestJSON(t *testing.T) {
	type entry struct {
		Date     Date  `json:"date"`
		Optional *Date `json:"optional"`
		Skipped  Date  `json:"skipped,omitzero"`
	}

	t.Run("YYYY-MM-DD 문자열로 적는다", func(t *testing.T) {
		optional := mustDate(t, 2028, time.February, 29)
		raw, err := json.Marshal(entry{Date: mustDate(t, 2026, time.September, 20), Optional: &optional})

		require.NoError(t, err)
		assert.JSONEq(t, `{"date":"2026-09-20","optional":"2028-02-29"}`, string(raw))
	})

	t.Run("없는 날짜는 포인터면 null, omitzero면 빠진다", func(t *testing.T) {
		raw, err := json.Marshal(entry{Date: mustDate(t, 2026, time.September, 20)})

		require.NoError(t, err)
		assert.JSONEq(t, `{"date":"2026-09-20","optional":null}`, string(raw))
	})

	t.Run("빈 값을 그대로 적으려 하면 오류다", func(t *testing.T) {
		_, err := json.Marshal(entry{})

		require.ErrorIs(t, err, ErrInvalid)

		_, err = Date{}.MarshalText()
		require.ErrorIs(t, err, ErrInvalid)
	})

	t.Run("읽어 들인다", func(t *testing.T) {
		var got entry

		err := json.Unmarshal([]byte(`{"date":"2026-09-20","optional":"2028-02-29","skipped":"2026-01-01"}`), &got)

		require.NoError(t, err)
		assert.Equal(t, mustDate(t, 2026, time.September, 20), got.Date)
		require.NotNil(t, got.Optional)
		assert.Equal(t, mustDate(t, 2028, time.February, 29), *got.Optional)
		assert.Equal(t, mustDate(t, 2026, time.January, 1), got.Skipped)
	})

	t.Run("적었다 읽으면 같은 값이다", func(t *testing.T) {
		optional := mustDate(t, 1, time.January, 1)
		want := entry{Date: mustDate(t, 9999, time.December, 31), Optional: &optional, Skipped: mustDate(t, 2026, time.September, 20)}

		raw, err := json.Marshal(want)
		require.NoError(t, err)
		var got entry
		require.NoError(t, json.Unmarshal(raw, &got))

		assert.Equal(t, want, got)
	})

	t.Run("null은 값을 바꾸지 않고 포인터는 nil이 된다", func(t *testing.T) {
		optional := mustDate(t, 2028, time.February, 29)
		got := entry{Date: mustDate(t, 2026, time.September, 20), Optional: &optional}

		err := json.Unmarshal([]byte(`{"date":null,"optional":null}`), &got)

		require.NoError(t, err)
		assert.Equal(t, mustDate(t, 2026, time.September, 20), got.Date)
		assert.Nil(t, got.Optional)
	})

	t.Run("객체의 키로 쓰고 읽는다", func(t *testing.T) {
		want := map[Date]int{
			mustDate(t, 2026, time.September, 19): 2,
			mustDate(t, 2026, time.September, 20): 5,
		}

		raw, err := json.Marshal(want)
		require.NoError(t, err)
		assert.JSONEq(t, `{"2026-09-19":2,"2026-09-20":5}`, string(raw))

		var got map[Date]int
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, want, got)
	})

	invalid := []struct {
		name string
		in   string
	}{
		{"빈 문자열", `{"date":""}`},
		{"달력에 없는 날짜", `{"date":"2026-02-30"}`},
		{"시각이 붙은 값", `{"date":"2026-09-20T00:00:00Z"}`},
		{"빈 값을 적은 문자열", `{"date":"0000-00-00"}`},
		{"숫자", `{"date":20260920}`},
		{"날짜가 아닌 글", `{"date":"오늘은 아무것도 하기 싫었어요"}`},
	}
	for _, tt := range invalid {
		t.Run("읽기 거부: "+tt.name, func(t *testing.T) {
			before := mustDate(t, 2026, time.September, 20)
			got := entry{Date: before}

			err := json.Unmarshal([]byte(tt.in), &got)

			require.Error(t, err)
			assert.Equal(t, before, got.Date, "실패하면 원래 값을 그대로 둔다")
			assert.NotContains(t, err.Error(), "하기 싫었어요", "입력받은 글을 오류 메시지에 담지 않는다")
		})
	}

	t.Run("UnmarshalText는 실패하면 받는 쪽을 바꾸지 않는다", func(t *testing.T) {
		d := mustDate(t, 2026, time.September, 20)

		err := d.UnmarshalText([]byte("2026-13-01"))

		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, "2026-09-20", d.String())
	})
}

func TestErrorsDoNotEchoInput(t *testing.T) {
	t.Run("형식이 맞고 달력에만 없는 날짜도 문자열째로 담지 않는다", func(t *testing.T) {
		_, err := Parse("2026-02-30")

		require.ErrorIs(t, err, ErrInvalid)
		assert.NotContains(t, err.Error(), "2026-02-30")
	})

	t.Run("오류 메시지는 ErrInvalid의 문구로 시작한다", func(t *testing.T) {
		_, parseErr := Parse("nope")
		_, newErr := New(2026, 13, 1)

		assert.True(t, strings.HasPrefix(parseErr.Error(), ErrInvalid.Error()))
		assert.True(t, strings.HasPrefix(newErr.Error(), ErrInvalid.Error()))
	})
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"2026-09-20", "2028-02-29", "2026-02-29", "0001-01-01", "9999-12-31", "0000-00-00",
		"", "2026-9-20", "2026-09-20T00:00:00Z", "+026-09-20", "２０２６-09-20", "오늘은 아무것도",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(s)
		if err != nil {
			if !d.IsZero() {
				t.Fatalf("실패했는데 빈 값이 아니다: %v", d)
			}
			return
		}
		if d.String() != s {
			t.Fatalf("받아들인 문자열과 다시 적은 문자열이 다르다: %q", d.String())
		}
		if _, err := New(d.Year(), d.Month(), d.Day()); err != nil {
			t.Fatalf("Parse가 받아들인 날짜를 New가 거부한다: %v", err)
		}
		if d.AddDays(1).AddDays(-1) != d && d.String() != "9999-12-31" {
			t.Fatalf("하루 갔다 돌아오면 같은 날짜여야 한다: %v", d)
		}
	})
}
