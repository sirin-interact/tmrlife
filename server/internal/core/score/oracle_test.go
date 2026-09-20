package score_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
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

// countsDiary는 연속한 n일의 기록을 만든다. 항목마다 앞에서부터 counts[항목]일 동안 관찰됨이고 나머지 날은 관찰되지 않음이다.
func countsDiary(n int, counts [signal.ItemCount]int) []string {
	lines := make([]string, n)
	for i := range lines {
		line := make([]byte, signal.ItemCount)
		for idx := range line {
			line[idx] = 'X'
			if i < counts[idx] {
				line[idx] = 'O'
			}
		}
		lines[i] = string(line)
	}
	return lines
}

// 창은 기준일을 포함한 14일이다. 기준일에서 13일 전은 창 안이고 14일 전은 창 밖이다.
func TestOracleWindowEdge(t *testing.T) {
	// 3월 6일부터 12일까지 이레 동안 대화했고, 기분은 첫날(3월 6일)에만 관찰됐다.
	days := diary(t, "2026-03-06", join([]string{".O......"}, repeat(6, "........"))...)

	t.Run("기준일이 3월 19일이면 13일 전인 3월 6일까지 창에 든다", func(t *testing.T) {
		// 창: 3월 6일~19일. n = 7이라 점수를 낸다.
		// 기분 o = 1 → 14×1÷7 = 2일 → 1점. 나머지는 0점. 합 1점, 최소 구간.
		got, err := score.Compute(days, date(t, "2026-03-19"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, date(t, "2026-03-06"), got.Window.From)
		assert.Equal(t, date(t, "2026-03-19"), got.Window.To)
		assert.Equal(t, 7, got.ConversationDays)
		assert.False(t, got.Insufficient)
		assert.Equal(t, 1, got.Item(signal.Mood).ObservedDays)
		assert.Equal(t, 2, got.Item(signal.Mood).ConvertedDays)
		assert.Equal(t, 1, got.Item(signal.Mood).Points)
		assert.Equal(t, 1, got.Total)
		assert.Equal(t, score.Minimal, got.Band)
	})

	t.Run("기준일이 3월 20일이면 14일 전인 3월 6일은 창 밖이다", func(t *testing.T) {
		// 창: 3월 7일~20일. 3월 6일이 빠져 n = 6 → 기록 부족, 점수 없음.
		// 기분이 관찰된 날도 창 밖이라 o = 0이다.
		got, err := score.Compute(days, date(t, "2026-03-20"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, date(t, "2026-03-07"), got.Window.From)
		assert.Equal(t, 6, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, 0, got.Item(signal.Mood).ObservedDays)
		assert.Equal(t, score.NoBand, got.Band)
		_, ok := got.Score()
		assert.False(t, ok, "기록 부족은 0점이 아니라 점수 없음이다")
	})
}

// 대화한 날이 7일 미만이면 점수를 내지 않는다. 6일과 7일이 갈린다.
func TestOracleMinimumConversationDays(t *testing.T) {
	asOf := "2026-04-10"

	t.Run("엿새 내내 여덟 항목이 모두 관찰돼도 점수를 내지 않는다", func(t *testing.T) {
		// 4월 5일~10일, n = 6 < 7.
		got, err := score.Compute(diary(t, "2026-04-05", repeat(6, "OOOOOOOO")...), date(t, asOf), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 6, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, score.NoBand, got.Band)
		assert.Equal(t, 6, got.Item(signal.Sleep).ObservedDays, "관찰된 일수는 기록 부족이어도 센다")
		_, ok := got.Score()
		assert.False(t, ok)
	})

	t.Run("이레 내내 여덟 항목이 모두 관찰되면 24점이다", func(t *testing.T) {
		// 4월 4일~10일, n = 7. 항목마다 o = 7 → 14×7÷7 = 14일 → 3점. 8 × 3 = 24점, 심함.
		got, err := score.Compute(diary(t, "2026-04-04", repeat(7, "OOOOOOOO")...), date(t, asOf), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.ConversationDays)
		assert.False(t, got.Insufficient)
		total, ok := got.Score()
		assert.True(t, ok)
		assert.Equal(t, 24, total)
		assert.Equal(t, score.Severe, got.Band)
	})
}

// 환산 일수는 14 × o ÷ n을 반올림한 값이고 0.5는 올린다.
// n이 7부터 14일 때 딱 0.5로 떨어지는 경우는 넷뿐이다: (n=8, o=2) 3.5, (n=8, o=6) 10.5, (n=12, o=3) 3.5, (n=12, o=9) 10.5.
func TestOracleConvertedDaysTable(t *testing.T) {
	tests := []struct {
		n, o       int
		derivation string
		converted  int
		points     int
	}{
		// n = 7: 14÷7 = 2배
		{7, 1, "14×1÷7=2", 2, 1},
		{7, 3, "14×3÷7=6", 6, 1},
		{7, 4, "14×4÷7=8", 8, 2},
		{7, 5, "14×5÷7=10", 10, 2},
		{7, 6, "14×6÷7=12", 12, 3},
		{7, 7, "14×7÷7=14", 14, 3},
		// n = 8: 1.75배
		{8, 1, "14×1÷8=1.75", 2, 1},
		{8, 2, "14×2÷8=3.5, 절반은 올린다", 4, 1},
		{8, 3, "14×3÷8=5.25", 5, 1},
		{8, 4, "14×4÷8=7", 7, 2},
		{8, 5, "14×5÷8=8.75", 9, 2},
		{8, 6, "14×6÷8=10.5, 절반은 올린다", 11, 2},
		{8, 7, "14×7÷8=12.25", 12, 3},
		{8, 8, "14×8÷8=14", 14, 3},
		// n = 9
		{9, 1, "14×1÷9=1.56", 2, 1},
		{9, 2, "14×2÷9=3.11", 3, 1},
		{9, 4, "14×4÷9=6.22", 6, 1},
		{9, 5, "14×5÷9=7.78", 8, 2},
		{9, 7, "14×7÷9=10.89", 11, 2},
		{9, 8, "14×8÷9=12.44", 12, 3},
		// n = 10: 1.4배
		{10, 1, "14×1÷10=1.4", 1, 1},
		{10, 4, "14×4÷10=5.6", 6, 1},
		{10, 5, "14×5÷10=7", 7, 2},
		{10, 8, "14×8÷10=11.2", 11, 2},
		{10, 9, "14×9÷10=12.6", 13, 3},
		// n = 11
		{11, 1, "14×1÷11=1.27", 1, 1},
		{11, 5, "14×5÷11=6.36", 6, 1},
		{11, 6, "14×6÷11=7.64", 8, 2},
		{11, 9, "14×9÷11=11.45", 11, 2},
		{11, 10, "14×10÷11=12.73", 13, 3},
		// n = 12
		{12, 1, "14×1÷12=1.17", 1, 1},
		{12, 3, "14×3÷12=3.5, 절반은 올린다", 4, 1},
		{12, 5, "14×5÷12=5.83", 6, 1},
		{12, 6, "14×6÷12=7", 7, 2},
		{12, 9, "14×9÷12=10.5, 절반은 올린다", 11, 2},
		{12, 10, "14×10÷12=11.67", 12, 3},
		// n = 13
		{13, 1, "14×1÷13=1.08", 1, 1},
		{13, 6, "14×6÷13=6.46", 6, 1},
		{13, 7, "14×7÷13=7.54", 8, 2},
		{13, 11, "14×11÷13=11.85", 12, 3},
		{13, 12, "14×12÷13=12.92", 13, 3},
		// n = 14: 환산 일수가 관찰된 일수 그대로다
		{14, 0, "관찰된 적 없음", 0, 0},
		{14, 1, "1일", 1, 1},
		{14, 6, "6일", 6, 1},
		{14, 7, "7일", 7, 2},
		{14, 11, "11일", 11, 2},
		{14, 12, "12일", 12, 3},
		{14, 14, "14일", 14, 3},
	}

	asOf := date(t, "2026-05-20")
	for _, tt := range tests {
		name := fmt.Sprintf("대화 %d일 중 %d일 관찰: %s → %d일 %d점", tt.n, tt.o, tt.derivation, tt.converted, tt.points)
		t.Run(name, func(t *testing.T) {
			// 기준일에서 끝나는 연속한 n일. 앞의 o일은 기분이 관찰됐고, 나머지 날은 "관찰되지 않음"이다.
			// 관찰되지 않은 날도 대화한 날이므로 n에는 들고 o에는 들지 않는다.
			first := asOf.AddDays(-(tt.n - 1))
			lines := join(repeat(tt.o, ".O......"), repeat(tt.n-tt.o, ".X......"))
			got, err := score.Compute(diary(t, first.String(), lines...), asOf, params.Default())
			require.NoError(t, err)

			require.False(t, got.Insufficient)
			assert.Equal(t, tt.n, got.ConversationDays)
			mood := got.Item(signal.Mood)
			assert.Equal(t, tt.o, mood.ObservedDays)
			assert.Equal(t, tt.converted, mood.ConvertedDays)
			assert.Equal(t, tt.points, mood.Points)
			assert.Equal(t, tt.points, got.Total, "다른 항목은 관찰된 적이 없어 0점이다")
			assert.Equal(t, score.Minimal, got.Band)
		})
	}
}

// 한 번이라도 관찰된 항목은 n이 얼마든 환산 일수가 1 이상이라 0점으로 사라지지 않는다.
func TestOracleObservedOnceNeverScoresZero(t *testing.T) {
	asOf := date(t, "2026-05-20")
	for n := 7; n <= 14; n++ {
		t.Run(fmt.Sprintf("대화 %d일 중 하루만 관찰돼도 1점이다", n), func(t *testing.T) {
			// 14×1÷n은 n이 14 이하이면 1 이상이다. 가장 작은 경우가 n = 14일 때의 1이다.
			first := asOf.AddDays(-(n - 1))
			lines := join(repeat(1, "..O....."), repeat(n-1, "........"))
			got, err := score.Compute(diary(t, first.String(), lines...), asOf, params.Default())
			require.NoError(t, err)

			assert.GreaterOrEqual(t, got.Item(signal.Sleep).ConvertedDays, 1)
			assert.Equal(t, 1, got.Item(signal.Sleep).Points)
		})
	}
}

// 여덟 항목을 따로 환산해 더한다.
func TestOracleTotalOfEightItems(t *testing.T) {
	// 4월 1일~10일 열흘 내내 대화했다(n = 10, 1.4배).
	//   흥미      o=10 → 14    → 3점
	//   기분      o=9  → 12.6  → 13 → 3점
	//   수면      o=7  → 9.8   → 10 → 2점
	//   피로      o=5  → 7     → 2점
	//   식욕      o=4  → 5.6   → 6  → 1점
	//   자기 비난 o=1  → 1.4   → 1  → 1점
	//   집중      o=0  → 0     → 0점
	//   움직임    o=8  → 11.2  → 11 → 2점
	// 합 3+3+2+2+1+1+0+2 = 14점, 중간 구간(10~14)의 끝이다.
	counts := [signal.ItemCount]int{10, 9, 7, 5, 4, 1, 0, 8}
	got, err := score.Compute(diary(t, "2026-04-01", countsDiary(10, counts)...), date(t, "2026-04-10"), params.Default())
	require.NoError(t, err)

	wantConverted := [signal.ItemCount]int{14, 13, 10, 7, 6, 1, 0, 11}
	wantPoints := [signal.ItemCount]int{3, 3, 2, 2, 1, 1, 0, 2}
	for _, item := range signal.AllItems() {
		result := got.Item(item)
		assert.Equal(t, item, result.Item)
		assert.Equal(t, counts[item.Index()], result.ObservedDays, item.String())
		assert.Equal(t, wantConverted[item.Index()], result.ConvertedDays, item.String())
		assert.Equal(t, wantPoints[item.Index()], result.Points, item.String())
	}
	assert.Equal(t, 14, got.Total)
	assert.Equal(t, score.Moderate, got.Band)
}

// 구간: 0~4 최소, 5~9 가벼움, 10~14 중간, 15~19 다소 심함, 20 이상 심함.
func TestOracleBandBoundaries(t *testing.T) {
	// 14일 내내 대화하면 환산 일수가 관찰된 일수 그대로다: 1~6일 1점, 7~11일 2점, 12~14일 3점.
	tests := []struct {
		name   string
		counts [signal.ItemCount]int
		total  int
		band   score.Band
	}{
		{"0점은 최소다", [signal.ItemCount]int{}, 0, score.Minimal},
		{"1점짜리 넷, 4점은 최소의 끝이다", [signal.ItemCount]int{1, 1, 1, 1, 0, 0, 0, 0}, 4, score.Minimal},
		{"1점짜리 다섯, 5점은 가벼움의 시작이다", [signal.ItemCount]int{1, 1, 1, 1, 1, 0, 0, 0}, 5, score.Mild},
		{"2점 하나와 1점 일곱, 9점은 가벼움의 끝이다", [signal.ItemCount]int{7, 1, 1, 1, 1, 1, 1, 1}, 9, score.Mild},
		{"2점 둘과 1점 여섯, 10점은 중간의 시작이다", [signal.ItemCount]int{7, 7, 1, 1, 1, 1, 1, 1}, 10, score.Moderate},
		{"2점 여섯과 1점 둘, 14점은 중간의 끝이다", [signal.ItemCount]int{7, 7, 7, 7, 7, 7, 1, 1}, 14, score.Moderate},
		{"2점 일곱과 1점 하나, 15점은 다소 심함의 시작이다", [signal.ItemCount]int{7, 7, 7, 7, 7, 7, 7, 1}, 15, score.ModeratelySevere},
		{"3점 셋과 2점 다섯, 19점은 다소 심함의 끝이다", [signal.ItemCount]int{12, 12, 12, 7, 7, 7, 7, 7}, 19, score.ModeratelySevere},
		{"3점 넷과 2점 넷, 20점은 심함의 시작이다", [signal.ItemCount]int{12, 12, 12, 12, 7, 7, 7, 7}, 20, score.Severe},
		{"3점 여덟, 24점이 가장 높다", [signal.ItemCount]int{14, 14, 14, 14, 14, 14, 14, 14}, 24, score.Severe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := score.Compute(diary(t, "2026-06-01", countsDiary(14, tt.counts)...), date(t, "2026-06-14"), params.Default())
			require.NoError(t, err)

			require.False(t, got.Insufficient)
			assert.Equal(t, 14, got.ConversationDays)
			assert.Equal(t, tt.total, got.Total)
			assert.Equal(t, tt.band, got.Band)
		})
	}
}

// 관찰됨은 직접 언급이든 간접 추론이든 하루로 센다. 관찰되지 않음과 언급 없음은 세지 않는다.
func TestOracleOnlyObservedCounts(t *testing.T) {
	// 4월 4일~10일(n = 7)의 기분: 간접 관찰 2일, 직접 관찰 1일, 관찰되지 않음 2일, 언급 없음 2일.
	// o = 3 → 14×3÷7 = 6일 → 1점.
	days := diary(t, "2026-04-04",
		".o......", ".o......", ".O......", ".X......", ".x......", "........", "........")
	got, err := score.Compute(days, date(t, "2026-04-10"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, 7, got.ConversationDays)
	assert.Equal(t, 3, got.Item(signal.Mood).ObservedDays)
	assert.Equal(t, 6, got.Item(signal.Mood).ConvertedDays)
	assert.Equal(t, 1, got.Total)
}

// 대화하지 않은 날은 분모에서 빠진다. 드문드문 대화한 기록도 같은 식으로 환산한다.
func TestOracleSparseDaysInsideWindow(t *testing.T) {
	// 기준일 3월 20일, 창은 3월 7일~20일.
	// 대화한 날: 3월 6일(창 밖), 7, 10, 12, 14, 16, 18, 20일 → 창 안의 n = 7.
	// 피로가 관찰된 날: 3월 6일(창 밖), 7일, 20일 → o = 2 → 14×2÷7 = 4일 → 1점.
	days := diary(t, "2026-03-06",
		"...O....", // 6일, 창 밖
		"...O....", // 7일, 창의 첫날
		"", "",
		"........", // 10일
		"",
		"........", // 12일
		"",
		"........", // 14일
		"",
		"........", // 16일
		"",
		"........", // 18일
		"",
		"...O....", // 20일, 기준일
	)
	got, err := score.Compute(days, date(t, "2026-03-20"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, 7, got.ConversationDays)
	assert.False(t, got.Insufficient)
	assert.Equal(t, 2, got.Item(signal.Fatigue).ObservedDays)
	assert.Equal(t, 4, got.Item(signal.Fatigue).ConvertedDays)
	assert.Equal(t, 1, got.Total)
}

// 창 안에 기록이 하나도 없으면 점수가 없다. 오류는 아니다.
func TestOracleWindowWithoutData(t *testing.T) {
	t.Run("마지막 대화가 한 달도 더 전이면 n은 0이다", func(t *testing.T) {
		// 3월 1일~10일에 대화했고 기준일은 4월 30일이다. 창(4월 17일~30일)에는 아무것도 없다.
		got, err := score.Compute(diary(t, "2026-03-01", repeat(10, "OOOOOOOO")...), date(t, "2026-04-30"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 0, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, score.NoBand, got.Band)
		assert.Equal(t, 0, got.Item(signal.Mood).ObservedDays)
	})

	t.Run("기록이 아예 없어도 같다", func(t *testing.T) {
		got, err := score.Compute(nil, date(t, "2026-04-30"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 0, got.ConversationDays)
		assert.True(t, got.Insufficient)
		_, ok := got.Score()
		assert.False(t, ok)
	})
}

// 지난 날짜를 기준일로 넣으면 그날까지의 기록만으로 구한다.
func TestOracleIgnoresDaysAfterAsOf(t *testing.T) {
	// 3월 1일~20일 내내 기분이 관찰됐다. 기준일을 3월 7일로 두면 n = 7, o = 7 → 14일 → 3점.
	// 3월 8일 뒤의 기록이 섞이면 n이 7을 넘는다.
	got, err := score.Compute(diary(t, "2026-03-01", repeat(20, ".O......")...), date(t, "2026-03-07"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, 7, got.ConversationDays)
	assert.Equal(t, 7, got.Item(signal.Mood).ObservedDays)
	assert.Equal(t, 3, got.Total)
}

// 창의 길이를 바꾸면 환산의 기준 일수도 함께 바뀐다.
func TestOracleCustomWindowLength(t *testing.T) {
	// 창 10일, 기록 부족의 기준 4일, 항목 점수의 경계는 1일, 5일, 9일.
	// 기준일 5월 10일, 창은 5월 1일~10일. 5월 7일~10일 나흘 대화(n = 4)했고 수면은 하루만 관찰됐다.
	// 10×1÷4 = 2.5 → 절반은 올려 3일 → 1점.
	// 흥미는 이틀 관찰됐다: 10×2÷4 = 5일 → 2점. 합 3점.
	p := params.Default()
	p.Window = params.Window{Days: 10, MinConversationDays: 4}
	p.Score.ItemScore1MinDays, p.Score.ItemScore2MinDays, p.Score.ItemScore3MinDays = 1, 5, 9
	require.NoError(t, p.Validate())

	days := diary(t, "2026-05-07", "O.O.....", "O.......", "........", "........")
	got, err := score.Compute(days, date(t, "2026-05-10"), p)
	require.NoError(t, err)

	assert.Equal(t, date(t, "2026-05-01"), got.Window.From)
	assert.Equal(t, 4, got.ConversationDays)
	require.False(t, got.Insufficient)
	assert.Equal(t, 3, got.Item(signal.Sleep).ConvertedDays)
	assert.Equal(t, 1, got.Item(signal.Sleep).Points)
	assert.Equal(t, 5, got.Item(signal.Interest).ConvertedDays)
	assert.Equal(t, 2, got.Item(signal.Interest).Points)
	assert.Equal(t, 3, got.Total)
}
