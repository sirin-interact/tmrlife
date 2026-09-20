package confidence_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
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

// 세 요소를 따로 구하고 가장 작은 값을 최종 값으로 삼는다. 0.4 미만 낮음, 0.4 이상 0.7 미만 보통, 0.7 이상 높음.
// 아래의 기록은 모두 3월 1일에 시작하고 기준일은 3월 14일이다(창은 3월 1일~14일).
func TestOracleThreeComponents(t *testing.T) {
	tests := []struct {
		name  string
		lines []string

		record       confidence.Ratio
		items        confidence.Ratio
		explicitness confidence.Ratio
		value        float64
		level        confidence.Level
	}{
		{
			// 기록 14/14, 항목 8/8, 관찰됨 112개가 모두 직접 언급 → 1. 가장 작은 값 1 → 높음.
			name:   "14일 내내 여덟 항목을 직접 말했으면 1이고 높음이다",
			lines:  repeat(14, "OOOOOOOO"),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 112, Den: 112},
			value:        1, level: confidence.High,
		},
		{
			// 3월 8일~14일 이레만 대화. 기록 7/14 = 0.5, 항목 8/8, 관찰됨이 없어 명시성은 1. 가장 작은 값 0.5 → 보통.
			name:   "이레만 대화했으면 기록 충실도 0.5가 값을 정하고 보통이다",
			lines:  join(repeat(7, ""), repeat(7, "XXXXXXXX")),
			record: confidence.Ratio{Num: 7, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 1},
			value:        0.5, level: confidence.Medium,
		},
		{
			// 말수 적은 사용자: 날마다 대화하지만 세 항목만 나온다. 기록 14/14, 항목 3/8 = 0.375, 명시성 42/42.
			// 가장 작은 값 0.375 < 0.4 → 낮음.
			name:   "날마다 대화해도 세 항목만 나오면 항목 충족도 0.375로 낮음이다",
			lines:  repeat(14, "OOO....."),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 3, Den: 8},
			explicitness: confidence.Ratio{Num: 42, Den: 42},
			value:        0.375, level: confidence.Low,
		},
		{
			// 관찰됨 10개: 첫날 직접 3개, 둘째 날부터 이레 동안 간접 1개씩 7개. 직접 3/10 = 0.3 → 낮음.
			name:   "관찰됨 열 개 가운데 직접 언급이 셋이면 명시성 0.3으로 낮음이다",
			lines:  join([]string{"OOOXXXXX"}, repeat(7, "oXXXXXXX"), repeat(6, "XXXXXXXX")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 3, Den: 10},
			value:        0.3, level: confidence.Low,
		},
		{
			// 관찰됨 5개 가운데 직접 2개 → 2/5 = 0.4. 0.4 "이상"은 보통이다.
			name:   "명시성이 딱 0.4이면 보통이다",
			lines:  join([]string{"OOXXXXXX", "oooXXXXX"}, repeat(12, "XXXXXXXX")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 2, Den: 5},
			value:        0.4, level: confidence.Medium,
		},
		{
			// 관찰됨 3개 가운데 직접 1개 → 1/3 = 0.333 < 0.4 → 낮음.
			name:   "명시성이 3분의 1이면 낮음이다",
			lines:  join([]string{"OooXXXXX"}, repeat(13, "XXXXXXXX")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 3},
			value:        1.0 / 3.0, level: confidence.Low,
		},
		{
			// 관찰됨 10개 가운데 직접 7개 → 7/10 = 0.7. 0.7 "이상"은 높음이다.
			name:   "명시성이 딱 0.7이면 높음이다",
			lines:  join([]string{"OOOOOOOX", "oooXXXXX"}, repeat(12, "XXXXXXXX")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 7, Den: 10},
			value:        0.7, level: confidence.High,
		},
		{
			// 관찰됨 13개: 직접 8+1 = 9개, 간접 4개 → 9/13 = 0.692 < 0.7 → 보통.
			name:   "명시성이 13분의 9이면 보통이다",
			lines:  join([]string{"OOOOOOOO", "OXXXXXXX", "ooooXXXX"}, repeat(11, "XXXXXXXX")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 9, Den: 13},
			value:        9.0 / 13.0, level: confidence.Medium,
		},
		{
			// 3월 5일~14일 열흘 대화. 기록 10/14 = 0.714 ≥ 0.7 → 높음.
			name:   "열흘 대화했으면 기록 충실도 14분의 10으로 높음이다",
			lines:  join(repeat(4, ""), repeat(10, "XXXXXXXX")),
			record: confidence.Ratio{Num: 10, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 1},
			value:        10.0 / 14.0, level: confidence.High,
		},
		{
			// 3월 6일~14일 아흐레 대화. 기록 9/14 = 0.643 → 보통.
			name:   "아흐레 대화했으면 기록 충실도 14분의 9로 보통이다",
			lines:  join(repeat(5, ""), repeat(9, "XXXXXXXX")),
			record: confidence.Ratio{Num: 9, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 1},
			value:        9.0 / 14.0, level: confidence.Medium,
		},
		{
			// 항목은 창 안에서 한 번이라도 나오면 센다. 14일 가운데 하루만 네 항목이 "관찰되지 않음"으로 나왔다.
			// 기록 14/14, 항목 4/8 = 0.5, 관찰됨 없음 → 1. 가장 작은 값 0.5 → 보통.
			name:   "항목은 창 안에서 한 번만 나와도 충족된 것으로 센다",
			lines:  join(repeat(9, "........"), []string{"XXXX...."}, repeat(4, "........")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 4, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 1},
			value:        0.5, level: confidence.Medium,
		},
		{
			// 명시성은 관찰됨 판단만 본다. 관찰되지 않음이 모두 간접 추론이어도 분모에 들지 않는다.
			// 관찰됨은 첫날의 흥미 하나이고 직접 언급이다 → 1/1. 기록 14/14, 항목 8/8 → 1, 높음.
			name:   "관찰되지 않음의 간접 추론은 명시성을 깎지 않는다",
			lines:  join([]string{"Oxxxxxxx"}, repeat(13, "xxxxxxxx")),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 1, Den: 1},
			value:        1, level: confidence.High,
		},
		{
			// 관찰됨 112개가 모두 간접 추론 → 0/112 = 0 → 낮음.
			name:   "관찰됨이 모두 간접 추론이면 0이고 낮음이다",
			lines:  repeat(14, "oooooooo"),
			record: confidence.Ratio{Num: 14, Den: 14}, items: confidence.Ratio{Num: 8, Den: 8},
			explicitness: confidence.Ratio{Num: 0, Den: 112},
			value:        0, level: confidence.Low,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := confidence.Compute(diary(t, "2026-03-01", tt.lines...), date(t, "2026-03-14"), params.Default())
			require.NoError(t, err)

			require.False(t, got.Insufficient)
			assert.Equal(t, tt.record, got.RecordCoverage, "기록 충실도")
			assert.Equal(t, tt.items, got.ItemCoverage, "항목 충족도")
			assert.Equal(t, tt.explicitness, got.Explicitness, "근거 명시성")
			assert.InDelta(t, tt.value, got.Value.Float64(), 1e-12, "최종 값")
			assert.Equal(t, tt.level, got.Level)
		})
	}
}

// 어느 요소가 값을 정했는지와 빠진 항목은 다음 대화에서 무엇을 물을지의 근거가 된다.
func TestOracleWeakestLinkAndMissingItems(t *testing.T) {
	// 말수 적은 사용자: 14일 내내 흥미, 기분, 수면만 나온다. 가장 약한 요소는 항목 충족도(3/8)다.
	// 나오지 않은 항목은 피로, 식욕, 자기 비난, 집중, 움직임이다.
	got, err := confidence.Compute(diary(t, "2026-03-01", repeat(14, "OOO.....")...), date(t, "2026-03-14"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, confidence.ComponentItemCoverage, got.Limiting)
	assert.Equal(t, confidence.Ratio{Num: 3, Den: 8}, got.Value)
	assert.Equal(t, 3, got.MentionedItems)
	assert.Equal(t,
		[]signal.Item{signal.Fatigue, signal.Appetite, signal.SelfBlame, signal.Concentration, signal.Psychomotor},
		got.MissingItems())
}

// 기록 부족이면 신뢰도도 구하지 않고 낮음으로 다룬다. 6일과 7일이 갈린다.
func TestOracleInsufficientRecords(t *testing.T) {
	t.Run("엿새 치 기록은 아무리 고르게 쌓였어도 낮음이다", func(t *testing.T) {
		// 3월 9일~14일, n = 6. 세 요소만 보면 6/14 = 0.43으로 보통이지만 기록 부족이라 값을 내지 않는다.
		got, err := confidence.Compute(diary(t, "2026-03-09", repeat(6, "OOOOOOOO")...), date(t, "2026-03-14"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 6, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, confidence.Low, got.Level)
		assert.True(t, got.Value.IsZero(), "최종 값은 구하지 않는다")
		assert.InDelta(t, 0, got.Value.Float64(), 0)
	})

	t.Run("이레면 값을 낸다", func(t *testing.T) {
		// 3월 8일~14일, n = 7. 7/14 = 0.5 → 보통.
		got, err := confidence.Compute(diary(t, "2026-03-08", repeat(7, "OOOOOOOO")...), date(t, "2026-03-14"), params.Default())
		require.NoError(t, err)

		assert.False(t, got.Insufficient)
		assert.InDelta(t, 0.5, got.Value.Float64(), 0)
		assert.Equal(t, confidence.Medium, got.Level)
	})

	t.Run("기록이 하나도 없으면 기록 부족이고 낮음이다", func(t *testing.T) {
		got, err := confidence.Compute(nil, date(t, "2026-03-14"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 0, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, confidence.Low, got.Level)
		assert.Equal(t, confidence.Ratio{Num: 0, Den: 14}, got.RecordCoverage)
	})
}

// 창은 점수와 같다. 기준일에서 13일 전은 창 안이고 14일 전은 창 밖이다.
func TestOracleWindowEdge(t *testing.T) {
	// 2월 28일에는 여덟 항목이 모두 나왔고, 3월 8일~14일 이레 동안은 흥미만 나왔다.
	days := diary(t, "2026-02-28", join([]string{"XXXXXXXX"}, repeat(7, ""), repeat(7, "X......."))...)

	t.Run("기준일 3월 13일: 13일 전인 2월 28일이 창에 든다", func(t *testing.T) {
		// 창 2월 28일~3월 13일. n = 1 + 6 = 7, 항목 8/8, 관찰됨 없음.
		// 가장 작은 값은 기록 7/14 = 0.5 → 보통.
		got, err := confidence.Compute(days, date(t, "2026-03-13"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, confidence.Ratio{Num: 8, Den: 8}, got.ItemCoverage)
		assert.InDelta(t, 0.5, got.Value.Float64(), 0)
		assert.Equal(t, confidence.Medium, got.Level)
	})

	t.Run("기준일 3월 14일: 14일 전인 2월 28일은 창 밖이다", func(t *testing.T) {
		// 창 3월 1일~14일. n = 7, 나온 항목은 흥미 하나 → 1/8 = 0.125 → 낮음.
		got, err := confidence.Compute(days, date(t, "2026-03-14"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, confidence.Ratio{Num: 1, Den: 8}, got.ItemCoverage)
		assert.InDelta(t, 0.125, got.Value.Float64(), 0)
		assert.Equal(t, confidence.Low, got.Level)
	})
}

// 지난 날짜를 기준일로 넣으면 그날까지의 기록만 본다.
func TestOracleIgnoresDaysAfterAsOf(t *testing.T) {
	// 3월 1일~20일 내내 대화했다. 기준일 3월 7일에는 n = 7 → 0.5, 보통이다.
	got, err := confidence.Compute(diary(t, "2026-03-01", repeat(20, "OOOOOOOO")...), date(t, "2026-03-07"), params.Default())
	require.NoError(t, err)

	assert.Equal(t, 7, got.ConversationDays)
	assert.Equal(t, 56, got.ObservedJudgements)
	assert.Equal(t, confidence.Medium, got.Level)
}

// 대화하지 않는 날에도 창은 하루씩 움직인다.
func TestOracleSeriesThroughSilence(t *testing.T) {
	// 3월 1일~10일 열흘 대화하고 그 뒤로는 대화하지 않았다. 날마다 여덟 항목이 모두 나오므로 값을 정하는 것은 기록 충실도다.
	//   3월 1일~6일   n = 1~6           기록 부족 → 낮음
	//   3월 7일~9일   n = 7, 8, 9       0.5, 0.571, 0.643 → 보통
	//   3월 10일~14일 n = 10            0.714 → 높음
	//   3월 15일~17일 n = 9, 8, 7       창의 첫날이 3월 2일, 3일, 4일로 밀린다 → 보통
	//   3월 18일~23일 n = 6, 5, …, 1    기록 부족 → 낮음
	//   3월 24일      n = 0             창이 3월 11일~24일이라 아무것도 없다
	days := diary(t, "2026-03-01", repeat(10, "XXXXXXXX")...)
	series, err := confidence.Series(days, date(t, "2026-03-01"), date(t, "2026-03-24"), params.Default())
	require.NoError(t, err)
	require.Len(t, series, 24)

	wantDays := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}
	wantLevels := []confidence.Level{
		confidence.Low, confidence.Low, confidence.Low, confidence.Low, confidence.Low, confidence.Low,
		confidence.Medium, confidence.Medium, confidence.Medium,
		confidence.High, confidence.High, confidence.High, confidence.High, confidence.High,
		confidence.Medium, confidence.Medium, confidence.Medium,
		confidence.Low, confidence.Low, confidence.Low, confidence.Low, confidence.Low, confidence.Low, confidence.Low,
	}
	for i, got := range series {
		assert.Equal(t, date(t, "2026-03-01").AddDays(i), got.AsOf)
		assert.Equal(t, wantDays[i], got.ConversationDays, got.AsOf.String())
		assert.Equal(t, wantLevels[i], got.Level, got.AsOf.String())
		assert.Equal(t, wantDays[i] < 7, got.Insufficient, got.AsOf.String())
	}
}
