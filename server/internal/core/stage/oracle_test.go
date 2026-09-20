package stage_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 위에 셈을 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 사람이 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.
//
// 규칙의 순서:
//  1. 점수로 본 단계: 0~4는 0단계, 5~9는 1단계, 10~14는 2단계, 15 이상은 3단계
//  2. 변화 감지 상태면 최소 1단계
//  3. 2단계 이상이 14일째 이어지는 날부터 3단계
//  4. 단계는 대화한 날에만 오른다
//  5. 하루에 한 단계만 오른다
//  6. 신뢰도가 낮은 날에는 전날보다 오르지 않는다. 내려가는 것은 허용한다
//  7. 기록 부족인 날(창 안의 대화가 1~6일)에는 전날의 단계를 그대로 이어 간다. 3번의 일수도 세지 않고 끊지 않는다
//  8. 창 안에 대화한 날이 하나도 없으면 0단계로 돌아가고 이어진 일수도 0이 된다

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
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

// observing은 앞에서부터 count개 항목이 관찰되고 나머지는 관찰되지 않은 하루다. 여덟 항목이 모두 나온 날이다.
func observing(count int) string {
	line := []byte("XXXXXXXX")
	for idx := range count {
		line[idx] = 'O'
	}
	return string(line)
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

func compute(t *testing.T, days []signal.Day, asOf string, p params.Params) stage.Result {
	t.Helper()
	got, err := stage.Compute(days, date(t, asOf), p)
	require.NoError(t, err)
	return got
}

func at(t *testing.T, r stage.Result, day string) stage.Point {
	t.Helper()
	pt, ok := r.At(date(t, day))
	require.True(t, ok, "%s은 흐름 안에 있어야 한다", day)
	require.Equal(t, date(t, day), pt.Date)
	return pt
}

// 점수로 본 단계: 0~4는 0단계, 5~9는 1단계, 10~14는 2단계, 15 이상은 3단계.
func TestOracleStageFromScore(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  stage.Stage
	}{
		{"0점은 0단계", 0, stage.Everyday},
		{"4점은 0단계의 끝", 4, stage.Everyday},
		{"5점은 1단계의 시작", 5, stage.Reflection},
		{"9점은 1단계의 끝", 9, stage.Reflection},
		{"10점은 2단계의 시작", 10, stage.Suggestion},
		{"14점은 2단계의 끝", 14, stage.Suggestion},
		{"15점은 3단계의 시작", 15, stage.Recommendation},
		{"24점은 3단계", 24, stage.Recommendation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stage.FromScore(tt.total, params.Default().Stage))
		})
	}
}

// 기록에서 출발해 같은 경계를 다시 본다. 14일 내내 여덟 항목이 모두 나오고 모두 직접 언급이라 신뢰도는 높음이다.
func TestOracleStageFromRecords(t *testing.T) {
	// 3월 1일~14일 날마다 대화, 기준일 3월 14일. n = 14이면 환산 일수가 관찰된 일수 그대로다(1~6일 1점, 7~11일 2점).
	// 점수는 3월 7일부터 나오고 그 뒤 여드레가 모두 대화한 날이라, 하루에 한 단계씩 올라도 기준일까지는 점수의 단계에 닿는다.
	// 관찰된 날이 앞쪽에 몰려 있어 점수는 3월 7일에 가장 높고 그 뒤로 내려온다. 내려가는 것은 그날 바로 따른다.
	// 2단계는 아무리 일러도 3월 8일에 시작하므로 기준일까지 14일째가 될 수는 없다.
	tests := []struct {
		name   string
		counts [signal.ItemCount]int
		score  int
		want   stage.Stage
	}{
		{"1점짜리 넷, 4점 → 0단계", [signal.ItemCount]int{1, 1, 1, 1, 0, 0, 0, 0}, 4, stage.Everyday},
		{"1점짜리 다섯, 5점 → 1단계", [signal.ItemCount]int{1, 1, 1, 1, 1, 0, 0, 0}, 5, stage.Reflection},
		{"2점 하나와 1점 일곱, 9점 → 1단계", [signal.ItemCount]int{7, 1, 1, 1, 1, 1, 1, 1}, 9, stage.Reflection},
		{"2점 둘과 1점 여섯, 10점 → 2단계", [signal.ItemCount]int{7, 7, 1, 1, 1, 1, 1, 1}, 10, stage.Suggestion},
		{"2점 여섯과 1점 둘, 14점 → 2단계", [signal.ItemCount]int{7, 7, 7, 7, 7, 7, 1, 1}, 14, stage.Suggestion},
		{"2점 일곱과 1점 하나, 15점 → 3단계", [signal.ItemCount]int{7, 7, 7, 7, 7, 7, 7, 1}, 15, stage.Recommendation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compute(t, diary(t, "2026-03-01", countsDiary(14, tt.counts)...), "2026-03-14", params.Default())

			assert.Equal(t, date(t, "2026-03-01"), got.From)
			require.Len(t, got.Series, 14)
			assert.Equal(t, 14, got.State.ConversationDays)
			assert.False(t, got.State.Insufficient)
			assert.Equal(t, tt.score, got.State.Score)
			assert.Equal(t, confidence.High, got.State.Confidence)
			assert.Equal(t, tt.want, got.State.Stage)
			assert.False(t, got.State.Held)
		})
	}
}

// 2단계 이상이 14일째 이어지는 날부터 3단계다. 13일째는 아직 2단계다.
func TestOracleFourteenthDay(t *testing.T) {
	// 3월 1일부터 날마다 네 항목(흥미, 기분, 수면, 피로)이 관찰되고 나머지 넷은 관찰되지 않음으로 나온다.
	//   3월 1일~6일  n = 1~6, 기록 부족 → 점수 없음, 0단계
	//   3월 7일      n = 7, 네 항목 모두 o = 7 → 14일 → 3점씩 12점 → 점수로는 2단계.
	//                신뢰도는 기록 7/14 = 0.5가 가장 작아 보통 → 올려도 된다. 하루에 한 단계라 1단계.
	//   3월 8일      2단계. 2단계 이상 1일째.
	//   3월 9일~     o = n이라 점수는 계속 12점. 평소의 하루 평균이 4라서 변화 탐지는 쌓이지 않는다(4 − 4 − 0.5 < 0).
	//   3월 20일     2단계 13일째 → 아직 2단계
	//   3월 21일     14일째 → 3단계
	days := diary(t, "2026-03-01", repeat(25, observing(4))...)
	got := compute(t, days, "2026-03-25", params.Default())
	require.Len(t, got.Series, 25)

	t.Run("첫 엿새는 기록 부족이고 0단계다", func(t *testing.T) {
		for _, day := range []string{"2026-03-01", "2026-03-06"} {
			pt := at(t, got, day)
			assert.True(t, pt.Insufficient, day)
			assert.Equal(t, stage.Everyday, pt.Stage, day)
			assert.False(t, pt.Held, day)
		}
	})

	t.Run("3월 7일에 12점이 나오지만 하루에 한 단계라 1단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-03-07")
		assert.Equal(t, 7, pt.ConversationDays)
		assert.Equal(t, 12, pt.Score)
		assert.Equal(t, confidence.Medium, pt.Confidence)
		assert.Equal(t, stage.Suggestion, pt.Raw)
		assert.Equal(t, stage.Reflection, pt.Stage)
		assert.True(t, pt.Held)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, pt.HeldBy())
		assert.Equal(t, 0, pt.ElevatedDays)
	})

	t.Run("3월 8일에 2단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-03-08")
		assert.Equal(t, stage.Suggestion, pt.Stage)
		assert.False(t, pt.Held)
		assert.Equal(t, 1, pt.ElevatedDays)
	})

	t.Run("3월 20일은 13일째라 아직 2단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-20")
		assert.Equal(t, 12, pt.Score)
		assert.Equal(t, stage.Suggestion, pt.Stage)
		assert.Equal(t, 13, pt.ElevatedDays)
		assert.False(t, pt.Detected)
	})

	t.Run("3월 21일은 14일째라 3단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-21")
		assert.Equal(t, 12, pt.Score, "점수는 그대로 12점이다")
		assert.Equal(t, stage.Recommendation, pt.Stage)
		assert.Equal(t, 14, pt.ElevatedDays)
		assert.False(t, pt.Held)
		assert.Contains(t, pt.Reasons, stage.ReasonSustained)
	})

	t.Run("그 뒤로도 3단계가 이어진다", func(t *testing.T) {
		assert.Equal(t, stage.Recommendation, got.State.Stage)
		assert.Equal(t, 18, got.State.ElevatedDays)
	})

	t.Run("지난 날짜를 기준일로 넣으면 그날의 단계가 나온다", func(t *testing.T) {
		earlier := compute(t, days, "2026-03-20", params.Default())
		require.Len(t, earlier.Series, 20)
		assert.Equal(t, stage.Suggestion, earlier.State.Stage)
		assert.Equal(t, got.Series[:20], earlier.Series)
	})

	t.Run("하루를 지우면 그 뒤의 단계가 모두 다시 정해진다", func(t *testing.T) {
		// 3월 3일을 지우면 3월 7일에는 n = 6이라 점수가 없고, 3월 8일(n = 7)에 1단계, 3월 9일에 2단계가 된다.
		// 13일째는 3월 21일, 14일째는 3월 22일이다. 3단계가 되는 날이 하루 밀린다.
		lines := repeat(25, observing(4))
		lines[2] = ""
		after := compute(t, diary(t, "2026-03-01", lines...), "2026-03-25", params.Default())

		assert.True(t, at(t, after, "2026-03-07").Insufficient)
		assert.Equal(t, stage.Everyday, at(t, after, "2026-03-07").Stage)
		assert.Equal(t, stage.Reflection, at(t, after, "2026-03-08").Stage)
		assert.Equal(t, stage.Suggestion, at(t, after, "2026-03-09").Stage)
		assert.Equal(t, stage.Suggestion, at(t, after, "2026-03-21").Stage)
		assert.Equal(t, stage.Recommendation, at(t, after, "2026-03-22").Stage)
	})
}

// 점수가 단계를 올리려 해도 신뢰도가 낮으면 전날의 단계에 묶이고, 묶였다는 표시가 남는다.
func TestOracleHeldByLowConfidence(t *testing.T) {
	// 말수 적은 사용자. 3월 1일~10일은 흥미, 기분, 수면만 나오고(모두 관찰됨) 나머지 다섯 항목은 언급이 없다.
	//   3월 7일  n = 7, 세 항목이 3점씩 9점 → 점수로는 1단계.
	//            신뢰도: 기록 7/14 = 0.5, 항목 3/8 = 0.375, 명시성 1 → 0.375 < 0.4 낮음 → 0단계에 묶인다.
	//   3월 8일~10일도 항목 3/8이라 낮음 → 계속 묶인다.
	// 3월 11일에 피로가 "관찰되지 않음"으로 처음 나온다.
	//   3월 11일 항목 4/8 = 0.5, 기록 11/14 = 0.786 → 0.5 보통 → 1단계로 오른다. 점수는 그대로 9점.
	lines := join(repeat(10, "OOO....."), repeat(4, "OOOX...."))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-14", params.Default())

	for _, day := range []string{"2026-03-07", "2026-03-08", "2026-03-09", "2026-03-10"} {
		t.Run(day+"에는 1단계로 오르려다 0단계에 묶인다", func(t *testing.T) {
			pt := at(t, got, day)
			assert.False(t, pt.Insufficient)
			assert.Equal(t, 9, pt.Score)
			assert.Equal(t, confidence.Low, pt.Confidence)
			assert.Equal(t, stage.Reflection, pt.Raw)
			assert.Equal(t, stage.Everyday, pt.Stage)
			assert.True(t, pt.Held)
			assert.Contains(t, pt.Reasons, stage.ReasonHeldLowConfidence)
		})
	}

	t.Run("빠진 항목 하나가 채워진 3월 11일에 1단계로 오른다", func(t *testing.T) {
		pt := at(t, got, "2026-03-11")
		assert.Equal(t, 9, pt.Score)
		assert.Equal(t, confidence.Medium, pt.Confidence)
		assert.Equal(t, stage.Reflection, pt.Stage)
		assert.False(t, pt.Held)
	})

	t.Run("기준일 3월 14일에도 1단계다", func(t *testing.T) {
		assert.Equal(t, stage.Reflection, got.State.Stage)
		assert.False(t, got.State.Held)
	})
}

// 첫날에는 전날이 없다. 전날의 단계는 0단계로 본다.
func TestOracleHoldRuleOnFirstDate(t *testing.T) {
	firstDay := diary(t, "2026-03-01", "OOOOOOOO")

	t.Run("기본값에서는 첫날이 아무리 나빠도 기록 부족이라 0단계다", func(t *testing.T) {
		// n = 1 < 7 → 점수 없음. 기준선도 없어 변화 탐지도 돌지 않는다. 오르려는 단계가 없으므로 묶임 표시도 없다.
		got := compute(t, firstDay, "2026-03-01", params.Default())
		require.Len(t, got.Series, 1)
		assert.True(t, got.State.Insufficient)
		assert.Equal(t, 1, got.State.ConversationDays)
		assert.Equal(t, stage.Everyday, got.State.Stage)
		assert.False(t, got.State.Held)
		assert.False(t, got.State.Detected)
	})

	t.Run("하루만으로 점수를 내게 해도 신뢰도가 낮아 0단계에 묶인다", func(t *testing.T) {
		// 기록 부족의 기준을 1일로 낮춘다. n = 1, 여덟 항목 모두 o = 1 → 14일 → 3점씩 24점 → 점수로는 3단계.
		// 신뢰도: 기록 1/14 = 0.071 → 낮음. 전날이 없으니 0단계보다 오를 수 없다.
		p := params.Default()
		p.Window.MinConversationDays = 1
		got := compute(t, firstDay, "2026-03-01", p)

		assert.False(t, got.State.Insufficient)
		assert.Equal(t, 24, got.State.Score)
		assert.Equal(t, confidence.Low, got.State.Confidence)
		assert.Equal(t, stage.Recommendation, got.State.Raw)
		assert.Equal(t, stage.Everyday, got.State.Stage)
		assert.True(t, got.State.Held)
	})

	t.Run("신뢰도가 받쳐 줘도 첫날에는 한 단계만 오르고, 사흘에 걸쳐 3단계가 된다", func(t *testing.T) {
		// 위와 같되 신뢰도의 경계를 0.05와 0.06으로 낮춘다. 1/14 = 0.071 ≥ 0.06 → 높음 → 신뢰도로는 묶이지 않는다.
		// 전날이 없는 첫날의 전날은 0단계이므로 첫날은 1단계까지다. 이틀째(n = 2, 여전히 24점) 2단계, 사흘째 3단계.
		p := params.Default()
		p.Window.MinConversationDays = 1
		p.Confidence = params.Confidence{MediumMin: 0.05, HighMin: 0.06}
		got := compute(t, diary(t, "2026-03-01", repeat(3, "OOOOOOOO")...), "2026-03-03", p)

		first := at(t, got, "2026-03-01")
		assert.Equal(t, confidence.High, first.Confidence)
		assert.Equal(t, stage.Recommendation, first.Raw)
		assert.Equal(t, stage.Reflection, first.Stage)
		assert.True(t, first.Held)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, first.HeldBy())
		assert.Equal(t, 0, first.ElevatedDays)

		second := at(t, got, "2026-03-02")
		assert.Equal(t, 24, second.Score)
		assert.Equal(t, stage.Suggestion, second.Stage)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, second.HeldBy())
		assert.Equal(t, 1, second.ElevatedDays)

		assert.Equal(t, stage.Recommendation, got.State.Stage)
		assert.False(t, got.State.Held)
		assert.Equal(t, 2, got.State.ElevatedDays)
	})
}

// 변화 감지 상태면 점수가 낮아도 최소 1단계다.
func TestOracleChangeDetectionFloor(t *testing.T) {
	// 3월 1일~14일: 날마다 여덟 항목이 나오지만 관찰된 것은 없다 → 평소 0. 기준선은 3월 15일부터 잡힌다.
	// 3월 15일부터 날마다 흥미와 기분이 관찰된다(x = 2) → 하루 +1.5(2 − 0 − 0.5).
	//   3월 15일 S = 1.5, 16일 S = 3.0, 17일 S = 4.5 > 4 → 감지.
	// 점수(n = 14, 환산 일수 = 관찰된 일수): 두 항목이 1~6일이면 1점씩 2점 → 점수로는 0단계.
	//   3월 16일 점수 2, 감지 아님 → 0단계
	//   3월 17일 점수 2, 감지 → 1단계. 신뢰도는 14/14, 8/8, 모두 직접 → 높음.
	//   3월 21일 o = 7 → 2점씩 4점 → 점수로는 여전히 0단계, 감지로 1단계
	//   3월 26일 o = 12 → 3점씩 6점 → 점수만으로도 1단계
	lines := join(repeat(14, observing(0)), repeat(12, observing(2)))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-26", params.Default())

	t.Run("3월 16일은 누적값이 3.0이라 아직 0단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-16")
		assert.Equal(t, 2, pt.Score)
		assert.False(t, pt.Detected)
		assert.Equal(t, stage.Everyday, pt.Stage)
	})

	t.Run("3월 17일에 감지되어 점수 2점인 채로 1단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-03-17")
		assert.Equal(t, 2, pt.Score)
		assert.True(t, pt.Detected)
		assert.Equal(t, confidence.High, pt.Confidence)
		assert.Equal(t, stage.Reflection, pt.Stage)
		assert.False(t, pt.Held)
		assert.Contains(t, pt.Reasons, stage.ReasonChangeDetected)
	})

	t.Run("3월 21일은 4점이지만 감지 상태라 1단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-21")
		assert.Equal(t, 4, pt.Score)
		assert.Equal(t, stage.Reflection, pt.Stage)
	})

	t.Run("3월 26일은 6점이라 점수만으로도 1단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-26")
		assert.Equal(t, 6, pt.Score)
		assert.Equal(t, stage.Reflection, pt.Stage)
	})
}

// 변화 감지로 오르려는 단계도 신뢰도가 낮으면 묶인다.
func TestOracleChangeDetectionHeldByLowConfidence(t *testing.T) {
	// 3월 1일~14일: 대화는 하지만 아무 항목도 나오지 않는다 → 평소 0.
	// 3월 15일부터 흥미와 기분만, 그것도 간접 추론으로 관찰된다.
	//   3월 17일 S = 4.5 → 감지 → 1단계로 오르려 한다.
	//   신뢰도: 기록 14/14, 항목 2/8 = 0.25, 명시성 0/6 = 0 → 0 → 낮음 → 0단계에 묶인다.
	lines := join(repeat(14, "........"), repeat(3, "oo......"))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-17", params.Default())

	assert.True(t, got.State.Detected)
	assert.Equal(t, 2, got.State.Score)
	assert.Equal(t, confidence.Low, got.State.Confidence)
	assert.Equal(t, stage.Reflection, got.State.Raw)
	assert.Equal(t, stage.Everyday, got.State.Stage)
	assert.True(t, got.State.Held)
}

// 14일째에 3단계로 오르려는 것도 신뢰도가 낮으면 묶인다. 이어진 일수는 계속 세고, 신뢰도가 돌아오는 날 오른다.
func TestOracleSustainedRaiseHeldThenReleased(t *testing.T) {
	// 날마다 같은 네 항목이 관찰되고 나머지 넷은 관찰되지 않음이다. 점수는 3월 7일부터 내내 12점, 평소는 4라 변화 탐지는 조용하다.
	// 달라지는 것은 관찰됨의 근거뿐이다: 3월 1일~10일 직접 언급, 11일~21일 간접 추론, 22일~27일 다시 직접 언급.
	// 관찰됨은 하루 네 개씩이므로 명시성 = 창 안의 "직접 언급인 날 수" ÷ 창 안의 대화한 날 수.
	//   3월 14일 10/14 = 0.714 높음      3월 18일 6/14 = 0.429 보통
	//   3월 19일 5/14 = 0.357 낮음       3월 20일 4/14, 3월 21일 3/14 낮음
	//   3월 22일 창 3월 9일~22일: 직접 9, 10, 22일 → 3/14 낮음
	//   3월 23일 창 3월 10일~: 10, 22, 23일 → 3/14     3월 24일 창 3월 11일~: 22, 23, 24일 → 3/14
	//   3월 25일 4/14 = 0.286            3월 26일 5/14 = 0.357 낮음
	//   3월 27일 6/14 = 0.429 → 보통
	// 단계: 3월 7일 1단계(하루에 한 단계), 3월 8일~20일 2단계(13일). 19일과 20일은 신뢰도가 낮지만 오르려는 것이 아니라서 묶임이 아니다.
	//       3월 21일 14일째 → 3단계로 오르려 하나 낮음 → 2단계에 묶임. 26일까지 같다.
	//       3월 27일 보통 → 3단계. 2단계 이상이 이어진 지 20일째다.
	lines := join(repeat(10, "OOOOXXXX"), repeat(11, "ooooXXXX"), repeat(6, "OOOOXXXX"))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-27", params.Default())

	t.Run("신뢰도가 낮아져도 단계가 그대로면 묶임이 아니다", func(t *testing.T) {
		for _, day := range []string{"2026-03-19", "2026-03-20"} {
			pt := at(t, got, day)
			assert.Equal(t, confidence.Low, pt.Confidence, day)
			assert.Equal(t, stage.Suggestion, pt.Stage, day)
			assert.False(t, pt.Held, day)
		}
		assert.Equal(t, confidence.Medium, at(t, got, "2026-03-18").Confidence)
		assert.Equal(t, 13, at(t, got, "2026-03-20").ElevatedDays)
	})

	t.Run("14일째부터 3단계로 오르려다 2단계에 묶인다", func(t *testing.T) {
		for offset, day := range []string{
			"2026-03-21", "2026-03-22", "2026-03-23", "2026-03-24", "2026-03-25", "2026-03-26",
		} {
			pt := at(t, got, day)
			assert.Equal(t, 12, pt.Score, day)
			assert.Equal(t, confidence.Low, pt.Confidence, day)
			assert.Equal(t, stage.Recommendation, pt.Raw, day)
			assert.Equal(t, stage.Suggestion, pt.Stage, day)
			assert.True(t, pt.Held, day)
			assert.Equal(t, stage.ReasonHeldLowConfidence, pt.HeldBy(), day)
			assert.Equal(t, 14+offset, pt.ElevatedDays, day)
		}
	})

	t.Run("신뢰도가 보통으로 돌아온 3월 27일에 3단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-03-27")
		assert.Equal(t, confidence.Medium, pt.Confidence)
		assert.Equal(t, stage.Recommendation, pt.Stage)
		assert.False(t, pt.Held)
		assert.Equal(t, 20, pt.ElevatedDays)
	})
}

// 대화하지 않는 날도 하루로 돌린다. 그런 날에는 단계가 오르지 않고, 기록 부족이면 그대로 이어 가고,
// 창 안에 대화한 날이 하나도 없으면 0단계로 돌아간다.
func TestOracleSilenceAndEmptyWindow(t *testing.T) {
	// 3월 1일~14일: 여덟 항목이 나오지만 관찰된 것은 없다 → 평소 0.
	// 3월 15일~21일: 날마다 네 항목이 관찰된다 → 하루 +3.5가 2로 묶인다. 2, 4(한계값과 같음), 6(감지), 8, 그 뒤로는 천장 8에 머문다.
	// 3월 22일부터 대화가 끊긴다. 누적값은 8에 머물고 감지 상태도 그대로다.
	// 4월 20일에 돌아와 아무것도 관찰되지 않은 하루를 보낸다(−0.5 → 7.5, 여전히 감지).
	//
	// 날짜별 셈(네 항목은 점수가 같다):
	//   3월 15일 n=14 o=1 → 1점씩 4점 → 0단계. S = 2라 감지도 아니다.
	//   3월 16일 o=2 → 4점, S = 4 → 한계값과 같아 아직 → 0단계
	//   3월 17일 o=3 → 4점, S = 6 감지 → 1단계
	//   3월 21일 o=7 → 7일 → 2점씩 8점 → 1단계
	//   3월 26일 창 3월 13일~: n=9,  14×7÷9 = 10.89 → 11일 → 2점씩 8점 → 1단계
	//   3월 27일 창 3월 14일~: n=8,  14×7÷8 = 12.25 → 12일 → 3점씩 12점 → 점수로는 2단계. 신뢰도 8/14 = 0.571 보통.
	//            (관찰되지 않은 날들이 창에서 빠지면서 대화가 없는데도 점수가 오른다. 환산식의 성질이다.)
	//            대화하지 않은 날이라 단계는 오르지 않는다 → 1단계에 묶임.
	//   3월 28일 창 3월 15일~: n=7 → 14일 → 12점 → 같은 까닭으로 1단계에 묶임
	//   3월 29일 n=6 → 기록 부족 → 전날의 1단계를 이어 간다. 4월 3일 n=1(3월 21일만 남는다)까지 같다.
	//   4월 4일  창 3월 22일~4월 4일 → n=0 → 0단계. 감지 상태여도 그렇다.
	//   4월 20일 n=1 → 기록 부족이라 0단계에서 오르지 못한다. 감지 때문에 1단계로 오르려던 것이라 묶임 표시가 남는다.
	lines := join(repeat(14, observing(0)), repeat(7, observing(4)), repeat(29, ""), []string{observing(0)})
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-04-20", params.Default())
	require.Len(t, got.Series, 51, "3월 1일부터 4월 20일까지 달력의 하루하루가 모두 들어 있다")

	t.Run("3월 15일은 4점이고 감지 전이라 0단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-15")
		assert.Equal(t, 4, pt.Score)
		assert.False(t, pt.Detected)
		assert.Equal(t, stage.Everyday, pt.Stage)
	})

	t.Run("3월 16일은 누적값이 한계값과 같아 아직 0단계다", func(t *testing.T) {
		pt := at(t, got, "2026-03-16")
		assert.Equal(t, 4, pt.Score)
		assert.False(t, pt.Detected)
		assert.Equal(t, stage.Everyday, pt.Stage)
	})

	t.Run("3월 17일에 감지되어 1단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-03-17")
		assert.Equal(t, 4, pt.Score)
		assert.True(t, pt.Detected)
		assert.Equal(t, stage.Reflection, pt.Stage)
	})

	t.Run("대화가 끊긴 뒤 3월 26일까지는 8점, 1단계다", func(t *testing.T) {
		for _, day := range []string{"2026-03-21", "2026-03-22", "2026-03-26"} {
			pt := at(t, got, day)
			assert.Equal(t, 8, pt.Score, day)
			assert.Equal(t, stage.Reflection, pt.Stage, day)
			assert.False(t, pt.Held, day)
		}
		assert.Equal(t, 9, at(t, got, "2026-03-26").ConversationDays)
	})

	t.Run("3월 27일에는 창이 밀려 12점이 되지만 대화하지 않은 날이라 1단계에 머문다", func(t *testing.T) {
		pt := at(t, got, "2026-03-27")
		assert.Equal(t, 8, pt.ConversationDays)
		assert.Equal(t, 12, pt.Score)
		assert.Equal(t, confidence.Medium, pt.Confidence)
		assert.Equal(t, stage.Suggestion, pt.Raw)
		assert.Equal(t, stage.Reflection, pt.Stage)
		assert.Equal(t, stage.ReasonHeldNoRecordToday, pt.HeldBy())

		next := at(t, got, "2026-03-28")
		assert.Equal(t, 7, next.ConversationDays)
		assert.Equal(t, 12, next.Score)
		assert.Equal(t, stage.Reflection, next.Stage)
		assert.Equal(t, stage.ReasonHeldNoRecordToday, next.HeldBy())
	})

	t.Run("기록 부족인 날에는 전날의 1단계를 그대로 이어 간다", func(t *testing.T) {
		for offset := 1; offset <= 6; offset++ {
			day := date(t, "2026-03-28").AddDays(offset).String()
			pt := at(t, got, day)
			assert.True(t, pt.Insufficient, day)
			assert.Equal(t, 7-offset, pt.ConversationDays, day)
			assert.Equal(t, confidence.Low, pt.Confidence, day)
			assert.Equal(t, stage.Reflection, pt.Stage, day)
			assert.False(t, pt.Held, day)
		}
	})

	t.Run("창이 빈 4월 4일부터는 감지 상태여도 0단계다", func(t *testing.T) {
		for _, day := range []string{"2026-04-04", "2026-04-10", "2026-04-19"} {
			pt := at(t, got, day)
			assert.Equal(t, 0, pt.ConversationDays, day)
			assert.True(t, pt.Detected, day)
			assert.Equal(t, stage.Everyday, pt.Stage, day)
			assert.False(t, pt.Held, day)
		}
	})

	t.Run("한 달 만에 돌아온 날은 기록 부족이라 0단계에 묶인다", func(t *testing.T) {
		pt := got.State
		assert.Equal(t, date(t, "2026-04-20"), pt.Date)
		assert.Equal(t, 1, pt.ConversationDays)
		assert.True(t, pt.Insufficient)
		assert.True(t, pt.Detected)
		assert.Equal(t, stage.Everyday, pt.Stage)
		assert.True(t, pt.Held)
		assert.Equal(t, stage.ReasonHeldInsufficientRecords, pt.HeldBy())
	})
}

// 이레만 대화하고 떠난 사용자. 창이 밀리면서 대화한 일수가 줄어든다.
func TestOracleShortRecordThenSilence(t *testing.T) {
	// 3월 1일~7일 날마다 네 항목이 관찰됐다. 기준일 3월 25일.
	//   3월 7일       n = 7, 12점 → 점수로는 2단계, 하루에 한 단계라 1단계
	//   3월 8일~14일  창에 이레가 모두 들어 있어 여전히 12점이지만 대화하지 않은 날이라 오르지 않는다 → 1단계
	//   3월 15일      창 3월 2일~ → n = 6 기록 부족 → 1단계를 이어 간다
	//   3월 20일      창 3월 7일~ → n = 1 → 1단계를 이어 간다
	//   3월 21일~     창 3월 8일~ → n = 0 → 0단계
	// 대화한 날이 일곱뿐이라 기준선은 3월 15일에 잡히지만, 그 뒤로 대화가 없어 변화 탐지는 쌓이지 않는다.
	got := compute(t, diary(t, "2026-03-01", repeat(7, observing(4))...), "2026-03-25", params.Default())
	require.Len(t, got.Series, 25)

	for _, day := range []string{"2026-03-07", "2026-03-10", "2026-03-14"} {
		pt := at(t, got, day)
		assert.Equal(t, 7, pt.ConversationDays, day)
		assert.Equal(t, 12, pt.Score, day)
		assert.Equal(t, stage.Suggestion, pt.Raw, day)
		assert.Equal(t, stage.Reflection, pt.Stage, day)
		assert.Equal(t, 0, pt.ElevatedDays, day)
	}
	assert.Equal(t, stage.ReasonHeldOneStepPerDay, at(t, got, "2026-03-07").HeldBy())
	assert.Equal(t, stage.ReasonHeldNoRecordToday, at(t, got, "2026-03-10").HeldBy())
	assert.Equal(t, stage.ReasonHeldNoRecordToday, at(t, got, "2026-03-14").HeldBy())

	edge := at(t, got, "2026-03-15")
	assert.Equal(t, 6, edge.ConversationDays)
	assert.True(t, edge.Insufficient)
	assert.Equal(t, stage.Reflection, edge.Stage)
	assert.False(t, edge.Held)

	last := at(t, got, "2026-03-20")
	assert.Equal(t, 1, last.ConversationDays)
	assert.Equal(t, stage.Reflection, last.Stage)

	for _, day := range []string{"2026-03-21", "2026-03-25"} {
		pt := at(t, got, day)
		assert.Equal(t, 0, pt.ConversationDays, day)
		assert.Equal(t, stage.Everyday, pt.Stage, day)
		assert.Equal(t, 0, pt.ElevatedDays, day)
		assert.False(t, pt.Detected, day)
	}
}

// 기록이 없으면 0단계다. 오류는 아니다.
func TestOracleNoRecords(t *testing.T) {
	got := compute(t, nil, "2026-03-25", params.Default())

	assert.Empty(t, got.Series)
	assert.True(t, got.From.IsZero())
	assert.Equal(t, date(t, "2026-03-25"), got.State.Date)
	assert.Equal(t, stage.Everyday, got.State.Stage)
	assert.Equal(t, 0, got.State.ConversationDays)
	assert.False(t, got.State.Held)
}

// 3단계로 보낸 날도 "2단계 이상이 이어진 날"에 든다. 점수가 2단계로 내려와도 이어진 일수는 끊기지 않는다.
func TestOracleSustainedCountsDaysAtStageThree(t *testing.T) {
	// 3월 1일~16일: 다섯 항목이 날마다 관찰된다. 3월 17일부터는 식욕이 빠져 네 항목만 관찰된다.
	//   3월 7일  n = 7, 다섯 항목 3점씩 15점 → 점수로는 3단계(신뢰도 0.5 보통). 하루에 한 단계라 1단계.
	//   3월 8일  15점 → 2단계. 2단계 이상 1일째.
	//   3월 9일  15점 → 3단계 (2일째)
	//   3월 17일 창 3월 4일~: 식욕 o=13 → 3점 → 15점 → 3단계 (10일째)
	//   3월 18일 식욕 o=12 → 3점 → 15점 → 3단계 (11일째)
	//   3월 19일 식욕 o=11 → 2점 → 14점 → 점수로는 2단계. 12일째라 그대로 2단계.
	//   3월 20일 식욕 o=10 → 14점 → 2단계 (13일째)
	//   3월 21일 식욕 o=9  → 14점. 14일째 → 3단계.
	lines := join(repeat(16, "OOOOOXXX"), repeat(5, "OOOOXXXX"))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-21", params.Default())

	wants := []struct {
		day      string
		score    int
		stage    stage.Stage
		elevated int
		heldBy   stage.Reason
	}{
		{"2026-03-07", 15, stage.Reflection, 0, stage.ReasonHeldOneStepPerDay},
		{"2026-03-08", 15, stage.Suggestion, 1, stage.ReasonHeldOneStepPerDay},
		{"2026-03-09", 15, stage.Recommendation, 2, 0},
		{"2026-03-17", 15, stage.Recommendation, 10, 0},
		{"2026-03-18", 15, stage.Recommendation, 11, 0},
		{"2026-03-19", 14, stage.Suggestion, 12, 0},
		{"2026-03-20", 14, stage.Suggestion, 13, 0},
		{"2026-03-21", 14, stage.Recommendation, 14, 0},
	}
	for _, want := range wants {
		t.Run(want.day, func(t *testing.T) {
			pt := at(t, got, want.day)
			assert.Equal(t, want.score, pt.Score)
			assert.Equal(t, want.stage, pt.Stage)
			assert.Equal(t, want.elevated, pt.ElevatedDays)
			assert.Equal(t, want.heldBy, pt.HeldBy())
		})
	}
}

// 2단계 아래로 하루라도 내려가면 이어진 일수는 처음부터 다시 센다.
func TestOracleStreakRestartsAfterDip(t *testing.T) {
	// 손으로 따라가기 쉽게 기간을 줄인다: 창 3일, 하루만 대화해도 점수를 내고, 항목 점수의 경계는 1일, 2일, 3일,
	// 2단계 이상이 3일째 이어지는 날부터 3단계.
	// 환산 일수 = 3 × o ÷ n. 날마다 대화하면 3월 3일부터 n = 3이라 관찰된 일수가 그대로 점수다.
	//
	// 날마다 네 항목이 관찰되는데 3월 4일 하루만 흥미 하나로 줄었다.
	//   3월 1일  n=1, 네 항목 o=1 → 3일 → 3점씩 12점 → 2단계로 오르려 하나 기록 1/3 = 0.33 낮음 → 0단계에 묶임
	//   3월 2일  n=2, o=2 → 3일 → 12점. 기록 2/3 = 0.67 보통 → 하루에 한 단계라 1단계
	//   3월 3일  n=3, 12점 → 2단계 (1일째)
	//   3월 4일  창 2일~4일: 흥미 3일 3점, 나머지 셋은 2일 2점 → 9점 → 1단계. 이어진 일수 0.
	//   3월 5일  창 3일~5일: 나머지 셋은 3일, 5일 → 2점 → 9점 → 1단계
	//   3월 6일  창 4일~6일: 나머지 셋은 5일, 6일 → 2점 → 9점 → 1단계
	//   3월 7일  창 5일~7일: 모두 3일 → 12점 → 2단계 (다시 1일째)
	//   3월 8일  2일째 → 2단계
	//   3월 9일  3일째 → 3단계
	// 3월 4일에 줄지 않았다면 3월 3, 4, 5일이 1~3일째이고 3월 5일에 3단계가 됐을 것이다.
	p := params.Default()
	p.Window = params.Window{Days: 3, MinConversationDays: 1}
	p.Score.ItemScore1MinDays, p.Score.ItemScore2MinDays, p.Score.ItemScore3MinDays = 1, 2, 3
	p.Stage.SustainedStage2Days = 3
	require.NoError(t, p.Validate())

	t.Run("하루 내려갔다 오면 3월 9일에야 3단계다", func(t *testing.T) {
		lines := join(repeat(3, "OOOOXXXX"), []string{"OXXXXXXX"}, repeat(6, "OOOOXXXX"))
		got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-10", p)

		first := at(t, got, "2026-03-01")
		assert.Equal(t, 12, first.Score)
		assert.Equal(t, stage.Everyday, first.Stage)
		assert.True(t, first.Held)

		wants := []struct {
			day      string
			score    int
			stage    stage.Stage
			elevated int
		}{
			{"2026-03-02", 12, stage.Reflection, 0},
			{"2026-03-03", 12, stage.Suggestion, 1},
			{"2026-03-04", 9, stage.Reflection, 0},
			{"2026-03-05", 9, stage.Reflection, 0},
			{"2026-03-06", 9, stage.Reflection, 0},
			{"2026-03-07", 12, stage.Suggestion, 1},
			{"2026-03-08", 12, stage.Suggestion, 2},
			{"2026-03-09", 12, stage.Recommendation, 3},
			{"2026-03-10", 12, stage.Recommendation, 4},
		}
		for _, want := range wants {
			pt := at(t, got, want.day)
			assert.Equal(t, want.score, pt.Score, want.day)
			assert.Equal(t, want.stage, pt.Stage, want.day)
			assert.Equal(t, want.elevated, pt.ElevatedDays, want.day)
		}
	})

	t.Run("내려간 날이 없으면 3월 5일에 3단계다", func(t *testing.T) {
		got := compute(t, diary(t, "2026-03-01", repeat(10, "OOOOXXXX")...), "2026-03-10", p)

		assert.Equal(t, stage.Suggestion, at(t, got, "2026-03-04").Stage)
		assert.Equal(t, 2, at(t, got, "2026-03-04").ElevatedDays)
		assert.Equal(t, stage.Recommendation, at(t, got, "2026-03-05").Stage)
		assert.Equal(t, 3, at(t, got, "2026-03-05").ElevatedDays)
	})
}

// 신뢰도가 낮아도 내려가는 것은 막지 않는다.
func TestOracleLoweringAllowedUnderLowConfidence(t *testing.T) {
	// 3월 1일~10일: 네 항목이 직접 언급으로 관찰된다. 3월 11일~24일: 피로가 빠지고 세 항목이 간접 추론으로만 관찰된다.
	// 여덟 항목은 날마다 모두 나오고, 평소(52 ÷ 14 ≈ 3.7)보다 많은 날이 없어 변화 탐지는 조용하다.
	//
	// 점수: 세 항목은 날마다 관찰돼 3점씩 9점. 피로는 직접 언급하던 날이 창에서 하루씩 빠진다.
	//   3월 13일 n=13 피로 o=10 → 10.77 → 11일 → 2점 → 11점
	//   3월 18일 n=14 피로 o=6 → 1점 → 10점      3월 23일 피로 o=1 → 1점 → 10점
	//   3월 24일 피로 o=0 → 0점 → 9점 → 점수로는 1단계
	// 명시성 = 직접 4개 × (창 안의 3월 10일까지의 날 수) ÷ 창 안의 관찰됨 전부:
	//   3월 19일 20/47 = 0.43 보통      3월 20일 16/46 = 0.35 낮음      3월 21일 12/45 낮음
	//   3월 22일 8/44, 3월 23일 4/43, 3월 24일 0/42 → 낮음
	// 단계: 3월 7일 1단계(하루에 한 단계), 3월 8일~20일 2단계(13일). 3월 21일~23일은 14일째 이후라 3단계로 오르려 하나 낮음 → 2단계에 묶임.
	//       3월 24일은 점수가 9점이라 1단계로 내려간다. 신뢰도가 낮아도 내려가는 것은 허용한다.
	lines := join(repeat(10, "OOOOXXXX"), repeat(14, "oooXXXXX"))
	got := compute(t, diary(t, "2026-03-01", lines...), "2026-03-24", params.Default())

	assert.Equal(t, 11, at(t, got, "2026-03-13").Score)
	assert.Equal(t, confidence.Medium, at(t, got, "2026-03-19").Confidence)

	thirteenth := at(t, got, "2026-03-20")
	assert.Equal(t, 10, thirteenth.Score)
	assert.Equal(t, confidence.Low, thirteenth.Confidence)
	assert.Equal(t, stage.Suggestion, thirteenth.Stage)
	assert.Equal(t, 13, thirteenth.ElevatedDays)
	assert.False(t, thirteenth.Held)

	for _, day := range []string{"2026-03-21", "2026-03-22", "2026-03-23"} {
		pt := at(t, got, day)
		assert.Equal(t, 10, pt.Score, day)
		assert.Equal(t, confidence.Low, pt.Confidence, day)
		assert.Equal(t, stage.Recommendation, pt.Raw, day)
		assert.Equal(t, stage.Suggestion, pt.Stage, day)
		assert.True(t, pt.Held, day)
	}

	last := got.State
	assert.Equal(t, 9, last.Score)
	assert.Equal(t, confidence.Low, last.Confidence)
	assert.False(t, last.Detected)
	assert.Equal(t, stage.Reflection, last.Stage)
	assert.False(t, last.Held)
	assert.Equal(t, 0, last.ElevatedDays)
}

// 일주일에 사흘만 대화하는 사용자는 14일 창 안의 대화가 언제나 6일이라 점수가 나오지 않는다.
// 변화가 감지돼도 단계는 오르지 못하고, 오르려다 묶였다는 표시만 남는다.
func TestOracleSparseTalkerIsHeldNotRaised(t *testing.T) {
	// 2026년 3월 2일은 월요일이다. 월, 수, 금에만 대화한다: 3월 2, 4, 6, 9, 11, 13, 16, 18, 20, 23일.
	// 첫 14일(3월 2일~15일) 안에는 6일뿐이라 일곱 번째 대화 날인 3월 16일까지가 기준선 기간이고, 3월 17일에 잡힌다.
	// 그때까지 관찰된 것이 없어 평소는 0이다. 3월 18일부터 대화한 날마다 세 항목 → +2.5가 2로 묶인다:
	// 3월 18일 2, 3월 20일 4(한계값과 같아 아직), 3월 23일 6 → 감지.
	// 3월 23일의 창(3월 10일~23일)에는 11, 13, 16, 18, 20, 23일 → n = 6 → 기록 부족.
	week := []string{"XXXXXXXX", "", "XXXXXXXX", "", "XXXXXXXX", "", ""}
	// 3월 16일(월) 평소대로, 17일(화) 없음, 18일(수) 세 항목, 19일(목) 없음, 20일(금) 세 항목, 주말 없음, 23일(월) 세 항목.
	third := []string{"XXXXXXXX", "", "OOOXXXXX", "", "OOOXXXXX", "", "", "OOOXXXXX"}
	lines := join(week, week, third)
	got := compute(t, diary(t, "2026-03-02", lines...), "2026-03-23", params.Default())
	require.Len(t, got.Series, 22)

	for _, pt := range got.Series {
		assert.LessOrEqual(t, pt.ConversationDays, 6, pt.Date.String())
		assert.True(t, pt.Insufficient, pt.Date.String())
		assert.Equal(t, stage.Everyday, pt.Stage, pt.Date.String())
	}

	before := at(t, got, "2026-03-20")
	assert.False(t, before.Detected, "누적값 4는 한계값과 같아 아직 감지가 아니다")
	assert.False(t, before.Held)

	last := got.State
	assert.Equal(t, 6, last.ConversationDays)
	assert.True(t, last.Detected)
	assert.Equal(t, stage.Reflection, last.Raw)
	assert.Equal(t, stage.Everyday, last.Stage)
	assert.True(t, last.Held)
	assert.Equal(t, stage.ReasonHeldInsufficientRecords, last.HeldBy())
}

// 사흘 대화하고 사흘 쉬기를 되풀이하는 사용자. 창 안의 대화한 일수가 기준을 오르내려도 단계가 오르내리지 않는다.
func TestOracleThreeOnThreeOffDoesNotFlap(t *testing.T) {
	// 3월 1일부터 사흘 대화(다섯 항목 관찰, 나머지는 관찰되지 않음)하고 사흘 쉰다. 대화한 날: 1, 2, 3, 7, 8, 9, 13, 14, 15, 19, 20, 21, 25, 26, 27일.
	// 관찰된 날 수 = 대화한 날 수라서 점수가 나오는 날에는 언제나 다섯 항목이 3점씩 15점 → 점수로는 3단계.
	// 평소는 5라서 변화 탐지는 조용하다(5 − 5 − 0.5 < 0).
	//   3월 13일 창 2월 28일~: 1, 2, 3, 7, 8, 9, 13일 → n = 7 → 15점. 신뢰도 7/14 = 0.5 보통. 하루에 한 단계 → 1단계
	//   3월 14일 n = 8 → 2단계 (1일째)       3월 15일 n = 8 → 3단계 (2일째)
	//   3월 16일 n = 7, 대화하지 않은 날이지만 오르려는 것이 아니다 → 3단계 (3일째)
	//   3월 17일 창 3월 4일~: 7, 8, 9, 13, 14, 15일 → n = 6 기록 부족 → 3단계를 이어 간다. 일수는 3일 그대로.
	//   3월 18일 n = 6 → 같다
	//   3월 19일 n = 7 → 3단계 (4일째)       3월 20일, 21일 n = 8 (5, 6일째)       3월 22일 n = 7 (7일째)
	//   3월 23일, 24일 n = 6 → 이어 간다(7일째 그대로)
	//   3월 25일 (8일째), 26일 (9일째), 27일 (10일째)
	var lines []string
	for range 5 {
		lines = append(lines, "OOOOOXXX", "OOOOOXXX", "OOOOOXXX", "", "", "")
	}
	got := compute(t, diary(t, "2026-03-01", lines[:27]...), "2026-03-27", params.Default())
	require.Len(t, got.Series, 27)

	wants := []struct {
		day          string
		n            int
		insufficient bool
		stage        stage.Stage
		elevated     int
	}{
		{"2026-03-12", 6, true, stage.Everyday, 0},
		{"2026-03-13", 7, false, stage.Reflection, 0},
		{"2026-03-14", 8, false, stage.Suggestion, 1},
		{"2026-03-15", 8, false, stage.Recommendation, 2},
		{"2026-03-16", 7, false, stage.Recommendation, 3},
		{"2026-03-17", 6, true, stage.Recommendation, 3},
		{"2026-03-18", 6, true, stage.Recommendation, 3},
		{"2026-03-19", 7, false, stage.Recommendation, 4},
		{"2026-03-22", 7, false, stage.Recommendation, 7},
		{"2026-03-23", 6, true, stage.Recommendation, 7},
		{"2026-03-24", 6, true, stage.Recommendation, 7},
		{"2026-03-25", 7, false, stage.Recommendation, 8},
		{"2026-03-27", 8, false, stage.Recommendation, 10},
	}
	for _, want := range wants {
		t.Run(want.day, func(t *testing.T) {
			pt := at(t, got, want.day)
			assert.Equal(t, want.n, pt.ConversationDays)
			assert.Equal(t, want.insufficient, pt.Insufficient)
			assert.Equal(t, want.stage, pt.Stage)
			assert.Equal(t, want.elevated, pt.ElevatedDays)
			assert.False(t, pt.Detected)
		})
	}

	t.Run("기록 부족인 날에는 이어 갔다는 것만 적힌다", func(t *testing.T) {
		pt := at(t, got, "2026-03-17")
		assert.Equal(t, stage.Everyday, pt.Raw)
		assert.False(t, pt.Held)
		assert.Equal(t, []stage.Reason{stage.ReasonCarriedInsufficientRecords}, pt.Reasons)
	})
}
