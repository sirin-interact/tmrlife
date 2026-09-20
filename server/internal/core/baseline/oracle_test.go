package baseline_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func date(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

// diary는 first부터 달력의 하루에 한 줄씩 적은 기록을 하루의 목록으로 옮긴다.
//
// 한 줄은 여덟 글자이고 자리는 흥미, 기분, 수면, 피로, 식욕, 자기 비난, 집중, 움직임 순이다.
// O는 관찰됨(직접 언급), o는 관찰됨(간접 추론), X는 관찰되지 않음(직접), x는 관찰되지 않음(간접), 점은 언급 없음이다.
// 빈 줄은 대화하지 않은 날이라 하루를 만들지 않는다.
func diary(t *testing.T, first string, lines ...string) []signal.Day {
	t.Helper()
	start := date(t, first)
	days := make([]signal.Day, 0, len(lines))
	for offset, line := range lines {
		if line == "" {
			continue
		}
		require.Len(t, line, signal.ItemCount, "하루는 여덟 글자로 적는다")
		day := signal.Day{Date: start.AddDays(offset)}
		for idx := range signal.ItemCount {
			switch line[idx] {
			case 'O':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			case 'o':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
			case 'X':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
			case 'x':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect}
			case '.':
			default:
				require.Failf(t, "모르는 글자", "%q", line[idx])
			}
		}
		days = append(days, day)
	}
	require.NoError(t, signal.ValidateDays(days))
	return days
}

func repeat(n int, line string) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = line
	}
	return lines
}

func join(parts ...[]string) []string {
	var lines []string
	for _, part := range parts {
		lines = append(lines, part...)
	}
	return lines
}

// observing은 앞에서부터 count개 항목이 관찰되고 나머지는 관찰되지 않은 하루다.
func observing(count int) string {
	line := []byte("XXXXXXXX")
	for idx := range count {
		line[idx] = 'O'
	}
	return string(line)
}

// sparse는 length일 가운데 적어 준 자리(첫날이 0)에만 대화가 있는 기록이다.
func sparse(length int, entries map[int]string) []string {
	lines := make([]string, length)
	for offset, line := range entries {
		lines[offset] = line
	}
	return lines
}

func compute(t *testing.T, days []signal.Day, asOf string) baseline.Baseline {
	t.Helper()
	got, err := baseline.Compute(days, date(t, asOf), params.Default())
	require.NoError(t, err)
	return got
}

// 첫 대화 날부터 14일(첫날 포함)이 기준선 기간이고, 14일째 날이 지난 뒤에 잡힌다.
func TestOracleDailyTalker(t *testing.T) {
	// 3월 1일~14일 날마다 대화했다. 그날 관찰된 항목 수는 차례로
	//   2, 0, 1, 3, 0, 0, 1, 2, 0, 1, 0, 2, 1, 1   (합 14)
	// 평소의 하루 평균 = 14 ÷ 14 = 1.0.
	// 항목은 앞에서부터 채우므로 흥미는 수가 1 이상인 날(1, 3, 4, 7, 8, 10, 12, 13, 14일 → 9일),
	// 기분은 2 이상인 날(1, 4, 8, 12일 → 4일), 수면은 3 이상인 날(4일 → 1일)에 관찰됐다.
	counts := []int{2, 0, 1, 3, 0, 0, 1, 2, 0, 1, 0, 2, 1, 1}
	lines := make([]string, 0, len(counts)+6)
	for _, count := range counts {
		lines = append(lines, observing(count))
	}
	// 3월 15일부터는 날마다 여덟 항목이 모두 관찰된다. 기준선에는 들지 않아야 한다.
	lines = append(lines, repeat(6, "OOOOOOOO")...)
	days := diary(t, "2026-03-01", lines...)

	t.Run("14일째 날 당일에는 아직 잡히지 않는다", func(t *testing.T) {
		got := compute(t, days, "2026-03-14")
		assert.False(t, got.Established)
		assert.Equal(t, date(t, "2026-03-01"), got.Start)
	})

	t.Run("13일째 날에도 잡히지 않는다. 대화한 날이 이미 7일을 넘었어도 14일을 다 채운다", func(t *testing.T) {
		got := compute(t, days, "2026-03-13")
		assert.False(t, got.Established)
	})

	t.Run("14일째 날이 지난 3월 15일부터 잡힌다", func(t *testing.T) {
		got := compute(t, days, "2026-03-15")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-01"), got.Start)
		assert.Equal(t, date(t, "2026-03-14"), got.End)
		assert.False(t, got.Extended)
		assert.Equal(t, 14, got.Days)
		assert.Equal(t, 14, got.ObservedTotal)
		assert.InDelta(t, 1.0, got.Mu, 0)

		assert.Equal(t, baseline.Rate{ObservedDays: 9, Days: 14}, got.ItemRate(signal.Interest))
		assert.Equal(t, baseline.Rate{ObservedDays: 4, Days: 14}, got.ItemRate(signal.Mood))
		assert.Equal(t, baseline.Rate{ObservedDays: 1, Days: 14}, got.ItemRate(signal.Sleep))
		assert.Equal(t, baseline.Rate{ObservedDays: 0, Days: 14}, got.ItemRate(signal.Fatigue))
	})

	t.Run("한번 잡힌 기준선은 그 뒤의 나쁜 날들에 움직이지 않는다", func(t *testing.T) {
		// 3월 15일~20일은 하루 여덟 개씩 관찰됐지만 기간 밖이다. 평균은 그대로 1.0이다.
		got := compute(t, days, "2027-01-01")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-14"), got.End)
		assert.Equal(t, 14, got.Days)
		assert.InDelta(t, 1.0, got.Mu, 0)
	})
}

// 14일 안에 대화한 날이 7일 이상이면 기간을 늘리지 않는다. 대화하지 않은 날은 평균의 분모에 들지 않는다.
func TestOracleSparseButEnough(t *testing.T) {
	// 3월 1, 3, 5, 7, 9, 11, 13일에 대화했고(7일) 날마다 두 항목이 관찰됐다. 평균 = 14 ÷ 7 = 2.0.
	// 3월 14일에는 대화가 없지만 기간의 마지막 날은 달력으로 14일째인 3월 14일이다.
	// 3월 15일의 대화(여덟 항목 관찰)는 기간 밖이다.
	days := diary(t, "2026-03-01", sparse(15, map[int]string{
		0: observing(2), 2: observing(2), 4: observing(2), 6: observing(2),
		8: observing(2), 10: observing(2), 12: observing(2),
		14: "OOOOOOOO",
	})...)

	t.Run("일곱 번째 대화를 한 3월 13일에는 아직 잡히지 않는다", func(t *testing.T) {
		assert.False(t, compute(t, days, "2026-03-13").Established)
	})

	t.Run("3월 14일에도 아직이다", func(t *testing.T) {
		assert.False(t, compute(t, days, "2026-03-14").Established)
	})

	t.Run("3월 15일에 잡히고 그날의 대화는 평균에 들지 않는다", func(t *testing.T) {
		got := compute(t, days, "2026-03-15")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-14"), got.End)
		assert.False(t, got.Extended)
		assert.Equal(t, 7, got.Days)
		assert.Equal(t, 14, got.ObservedTotal)
		assert.InDelta(t, 2.0, got.Mu, 0)
	})
}

// 14일 안에 대화한 날이 7일이 안 되면 일곱 번째 대화 날까지 기간을 늘리고, 그날이 지난 뒤에 잡힌다.
func TestOracleExtendedForSparseTalker(t *testing.T) {
	// 사흘에 한 번 대화한다: 3월 1, 4, 7, 10, 13, 16, 19, 22일.
	// 3월 1일~14일 안에는 다섯 번뿐이라 일곱 번째 대화 날인 3월 19일까지 늘린다.
	// 그날 관찰된 항목 수: 1, 2, 0, 3, 1, 2, 5 (합 14) → 평균 = 14 ÷ 7 = 2.0. 여덟 번째(3월 22일, 8개)는 기간 밖이다.
	days := diary(t, "2026-03-01", sparse(22, map[int]string{
		0: observing(1), 3: observing(2), 6: observing(0), 9: observing(3), 12: observing(1),
		15: observing(2), 18: observing(5),
		21: observing(8),
	})...)

	t.Run("여섯 번째 대화까지만 있던 3월 18일에는 기간의 끝을 아직 모른다", func(t *testing.T) {
		got := compute(t, days, "2026-03-18")
		assert.False(t, got.Established)
		assert.True(t, got.End.IsZero())
	})

	t.Run("일곱 번째 대화 날인 3월 19일 당일에는 아직 잡히지 않는다", func(t *testing.T) {
		assert.False(t, compute(t, days, "2026-03-19").Established)
	})

	t.Run("3월 20일부터 잡힌다", func(t *testing.T) {
		got := compute(t, days, "2026-03-20")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-01"), got.Start)
		assert.Equal(t, date(t, "2026-03-19"), got.End)
		assert.True(t, got.Extended)
		assert.Equal(t, 7, got.Days)
		assert.Equal(t, 14, got.ObservedTotal)
		assert.InDelta(t, 2.0, got.Mu, 0)
	})

	t.Run("여덟 번째 대화는 평균을 바꾸지 못한다", func(t *testing.T) {
		got := compute(t, days, "2026-03-25")
		assert.Equal(t, date(t, "2026-03-19"), got.End)
		assert.Equal(t, 7, got.Days)
		assert.InDelta(t, 2.0, got.Mu, 0)
	})
}

// 달력으로 14일째 날의 대화는 기간 안이고 15일째 날의 대화는 기간 밖이다.
func TestOracleFourteenthCalendarDay(t *testing.T) {
	t.Run("일곱 번째 대화가 14일째 날이면 늘리지 않는다", func(t *testing.T) {
		// 3월 1일~6일 엿새와 3월 14일. 14일 안에 7일이므로 기간은 3월 14일까지다.
		days := diary(t, "2026-03-01", join(repeat(6, observing(1)), repeat(7, ""), []string{observing(1)})...)

		got := compute(t, days, "2026-03-15")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-14"), got.End)
		assert.False(t, got.Extended)
		assert.Equal(t, 7, got.Days)
	})

	t.Run("일곱 번째 대화가 15일째 날이면 그날까지 늘린다", func(t *testing.T) {
		// 3월 1일~6일 엿새와 3월 15일. 14일 안에는 6일뿐이라 3월 15일까지 늘리고, 3월 16일에 잡힌다.
		days := diary(t, "2026-03-01", join(repeat(6, observing(1)), repeat(8, ""), []string{observing(1)})...)

		onTheDay := compute(t, days, "2026-03-15")
		assert.False(t, onTheDay.Established)

		got := compute(t, days, "2026-03-16")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-03-15"), got.End)
		assert.True(t, got.Extended)
		assert.Equal(t, 7, got.Days)
	})

	t.Run("일곱 번째 대화가 한 달 뒤여도 그날까지 늘린다", func(t *testing.T) {
		// 3월 1일~6일 엿새와 4월 10일. 4월 10일까지가 기간이고 4월 11일에 잡힌다.
		days := diary(t, "2026-03-01", join(repeat(6, observing(2)), repeat(34, ""), []string{observing(2)})...)

		assert.False(t, compute(t, days, "2026-04-09").Established)
		assert.False(t, compute(t, days, "2026-04-10").Established)

		got := compute(t, days, "2026-04-11")
		assert.True(t, got.Established)
		assert.Equal(t, date(t, "2026-04-10"), got.End)
		assert.InDelta(t, 2.0, got.Mu, 0)
	})
}

// 기간의 날을 지우면 남은 기록으로 처음부터 다시 정해진다.
func TestOracleRecalculatedAfterDeletion(t *testing.T) {
	t.Run("첫날을 지우면 시작이 다음 대화 날로 옮겨 가고 기간도 하루 밀린다", func(t *testing.T) {
		// 3월 1일~20일 날마다 대화. 3월 1일은 여덟 항목, 나머지 날은 한 항목이 관찰됐다.
		// 지우기 전: 3월 1일~14일, 합 8 + 13 = 21 → 평균 1.5.
		// 3월 1일을 지운 뒤: 3월 2일~15일, 합 14 → 평균 1.0.
		lines := join([]string{observing(8)}, repeat(19, observing(1)))
		before := compute(t, diary(t, "2026-03-01", lines...), "2026-03-20")
		assert.Equal(t, date(t, "2026-03-14"), before.End)
		assert.Equal(t, 21, before.ObservedTotal)
		assert.InDelta(t, 1.5, before.Mu, 0)

		lines[0] = ""
		after := compute(t, diary(t, "2026-03-01", lines...), "2026-03-20")
		assert.Equal(t, date(t, "2026-03-02"), after.Start)
		assert.Equal(t, date(t, "2026-03-15"), after.End)
		assert.Equal(t, 14, after.Days)
		assert.Equal(t, 14, after.ObservedTotal)
		assert.InDelta(t, 1.0, after.Mu, 0)
	})

	t.Run("지워서 대화한 날이 모자라게 되면 기간이 늘어난다", func(t *testing.T) {
		// 3월 1, 3, 5, 7, 9, 11, 13일과 3월 20일에 대화했다. 지우기 전에는 14일 안에 7일이라 3월 14일까지다.
		// 3월 5일을 지우면 14일 안에 6일뿐이다. 일곱 번째 대화 날은 3월 20일이 되고, 3월 21일에야 잡힌다.
		entries := map[int]string{
			0: observing(1), 2: observing(1), 4: observing(1), 6: observing(1),
			8: observing(1), 10: observing(1), 12: observing(1), 19: observing(1),
		}
		before := compute(t, diary(t, "2026-03-01", sparse(20, entries)...), "2026-03-20")
		assert.True(t, before.Established)
		assert.Equal(t, date(t, "2026-03-14"), before.End)

		delete(entries, 4)
		days := diary(t, "2026-03-01", sparse(20, entries)...)
		onTheDay := compute(t, days, "2026-03-20")
		assert.False(t, onTheDay.Established, "어제까지 잡혀 있던 기준선이 지우고 나면 아직 잡히지 않은 상태로 돌아간다")

		after := compute(t, days, "2026-03-21")
		assert.True(t, after.Established)
		assert.Equal(t, date(t, "2026-03-20"), after.End)
		assert.True(t, after.Extended)
		assert.Equal(t, 7, after.Days)
	})
}

// 평균은 대화한 날의 "그날 관찰된 항목 수"를 평균한 것이다. 아무 항목도 나오지 않은 날도 대화한 날이다.
func TestOracleMeanCountsQuietDays(t *testing.T) {
	// 3월 1일~7일 이레 대화했다. 첫날 네 항목이 관찰됐고 나머지 엿새는 아무 항목도 나오지 않았다.
	// 평균 = 4 ÷ 7. 대화하지 않은 3월 8일~14일은 분모에 들지 않는다.
	days := diary(t, "2026-03-01", join([]string{"OOOO...."}, repeat(6, "........"))...)
	got := compute(t, days, "2026-03-15")

	assert.True(t, got.Established)
	assert.Equal(t, 7, got.Days)
	assert.Equal(t, 4, got.ObservedTotal)
	assert.InDelta(t, 4.0/7.0, got.Mu, 1e-12)
}

// 추세 화면의 기분 줄은 흥미와 기분 가운데 하나라도 관찰된 날을 센다.
func TestOracleMoodRowIsUnionPerDay(t *testing.T) {
	// 3월 1일 흥미만, 2일 기분만, 3일 둘 다, 4일 수면과 피로. 나머지 열흘은 아무것도 관찰되지 않았다.
	// 흥미 2일, 기분 2일이지만 기분 줄은 1, 2, 3일 → 3일이다(3일을 두 번 세지 않는다).
	days := diary(t, "2026-03-01", join(
		[]string{"OXXXXXXX", "XOXXXXXX", "OOXXXXXX", "XXOOXXXX"}, repeat(10, "XXXXXXXX"))...)
	got := compute(t, days, "2026-03-15")

	require.True(t, got.Established)
	assert.Equal(t, baseline.Rate{ObservedDays: 2, Days: 14}, got.ItemRate(signal.Interest))
	assert.Equal(t, baseline.Rate{ObservedDays: 2, Days: 14}, got.ItemRate(signal.Mood))
	assert.Equal(t, baseline.Rate{ObservedDays: 3, Days: 14}, got.TrendRate(signal.TrendMood))
	assert.Equal(t, baseline.Rate{ObservedDays: 1, Days: 14}, got.TrendRate(signal.TrendSleep))
	assert.Equal(t, baseline.Rate{ObservedDays: 1, Days: 14}, got.TrendRate(signal.TrendEnergy))
	assert.Equal(t, 6, got.ObservedTotal)
}

// 기록이 없으면 기준선도 없다. 오류는 아니다.
func TestOracleNoRecords(t *testing.T) {
	t.Run("기록이 하나도 없다", func(t *testing.T) {
		got := compute(t, nil, "2026-03-15")
		assert.False(t, got.Established)
		assert.True(t, got.Start.IsZero())
		assert.Equal(t, 0, got.Days)
	})

	t.Run("기록이 모두 기준일 뒤에 있다", func(t *testing.T) {
		got := compute(t, diary(t, "2026-04-01", repeat(20, observing(1))...), "2026-03-15")
		assert.False(t, got.Established)
		assert.True(t, got.Start.IsZero())
	})
}

// 14일은 달력으로 센다. 달과 해가 바뀌어도, 윤년의 2월 29일이 끼어도 같다.
func TestOracleCalendarBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		first string
		end   string
		next  string
	}{
		{"1월 25일에 시작하면 2월 7일까지다", "2026-01-25", "2026-02-07", "2026-02-08"},
		{"12월 25일에 시작하면 이듬해 1월 7일까지다", "2026-12-25", "2027-01-07", "2027-01-08"},
		{"평년 2월 20일에 시작하면 3월 5일까지다", "2027-02-20", "2027-03-05", "2027-03-06"},
		{"윤년 2월 20일에 시작하면 3월 4일까지다", "2028-02-20", "2028-03-04", "2028-03-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := diary(t, tt.first, repeat(20, observing(1))...)

			onTheDay := compute(t, days, tt.end)
			assert.False(t, onTheDay.Established)

			got := compute(t, days, tt.next)
			assert.True(t, got.Established)
			assert.Equal(t, date(t, tt.end), got.End)
			assert.Equal(t, 14, got.Days)
		})
	}
}
