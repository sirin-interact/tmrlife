package stage_test

// 개입 단계의 여덟 규칙을 손으로 따라간 값과 견주는 시험이다.
// 기대값은 코드를 돌려서 얻지 않았다. 경우마다 날짜별 셈을 적었으니, 깨지면 셈과 코드 가운데 어느 쪽이 틀렸는지 따져 볼 수 있다.
// 공개된 함수만 부른다.
//
// 손으로 따라간 규칙:
//
//	창은 그날을 포함한 최근 14일, n은 창 안에서 대화한 날 수. n이 7보다 작으면 기록 부족이다.
//	항목마다 환산 일수 = 14 × 관찰된 일수 ÷ n을 반올림(0.5는 올림). 0일 0점, 1~6일 1점, 7~11일 2점, 12~14일 3점.
//	신뢰도 = min(n ÷ 14, 한 번이라도 언급된 항목 수 ÷ 8, 직접 언급인 관찰 ÷ 관찰 전체). 0.4보다 작으면 낮음이다.
//
//	1. 점수 0~4는 0단계, 5~9는 1단계, 10~14는 2단계, 15 이상은 3단계
//	2. 변화 감지 상태면 적어도 1단계
//	3. 2단계 이상이 달력으로 14일째 이어지는 날부터 3단계
//	4. 대화한 날에만 오른다
//	5. 하루에 한 단계만 오른다
//	6. 신뢰도가 낮은 날에는 오르지 않는다. 내려가는 것은 된다
//	7. 기록 부족인 날에는 전날의 단계를 그대로 잇는다. 3번의 일수도 세지 않고 끊지도 않는다
//	8. 창 안에 대화한 날이 없으면 0단계로 돌아가고 3번의 일수도 0이 된다

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

// 첫 대화 날이다. 아래에서 "n일째"는 이날을 1일째로 센 달력 날짜다.
const ov2First = "2026-03-01"

func ov2Nth(t *testing.T, n int) recorddate.Date {
	t.Helper()
	first, err := recorddate.Parse(ov2First)
	require.NoError(t, err)
	return first.AddDays(n - 1)
}

// ov2Diary는 첫 대화 날부터 달력의 하루에 한 줄씩 적은 기록을 하루의 목록으로 옮긴다.
//
// 한 줄은 여덟 글자이고 자리는 항목 순서다.
//
//	O 관찰됨(직접 언급)   o 관찰됨(간접 추론)   x 관찰되지 않음(직접 언급)   . 언급 없음
//
// 빈 줄은 대화하지 않은 날이라 하루를 만들지 않는다.
func ov2Diary(t *testing.T, lines []string) []signal.Day {
	t.Helper()
	days := make([]signal.Day, 0, len(lines))
	for offset, line := range lines {
		if line == "" {
			continue
		}
		require.Len(t, line, signal.ItemCount, "하루는 여덟 글자로 적는다")
		day := signal.Day{Date: ov2Nth(t, offset+1)}
		for idx := range signal.ItemCount {
			switch line[idx] {
			case 'O':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			case 'o':
				day.Judgements[idx] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
			case 'x':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
			case '.':
			default:
				require.FailNow(t, "기록 줄에 쓸 수 없는 글자다")
			}
		}
		days = append(days, day)
	}
	require.NoError(t, signal.ValidateDays(days))
	return days
}

func ov2Rep(n int, line string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = line
	}
	return out
}

func ov2Cat(parts ...[]string) []string {
	var out []string
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// ov2Compute는 기록의 마지막 줄의 날짜를 기준일로 삼아 단계를 구한다.
func ov2Compute(t *testing.T, lines []string) stage.Result {
	t.Helper()
	result, err := stage.Compute(ov2Diary(t, lines), ov2Nth(t, len(lines)), params.Default())
	require.NoError(t, err)
	require.Len(t, result.Series, len(lines), "첫 대화 날부터 기준일까지 달력의 하루마다 한 줄이어야 한다")
	return result
}

func ov2At(t *testing.T, r stage.Result, nth int) stage.Point {
	t.Helper()
	pt, ok := r.At(ov2Nth(t, nth))
	require.True(t, ok, "%d일째는 흐름 안의 날짜여야 한다", nth)
	return pt
}

// ov2Span은 "from일째부터 to일째까지 이 단계"라는 기대값이다.
type ov2Span struct {
	from, to int
	stage    stage.Stage
}

func ov2AssertStages(t *testing.T, r stage.Result, spans []ov2Span) {
	t.Helper()
	next := 1
	for _, span := range spans {
		require.Equal(t, next, span.from, "기대값이 달력을 빈틈없이 덮어야 한다")
		for nth := span.from; nth <= span.to; nth++ {
			assert.Equal(t, span.stage, ov2At(t, r, nth).Stage, "%d일째의 단계", nth)
		}
		next = span.to + 1
	}
	require.Equal(t, len(r.Series)+1, next, "기대값이 기준일까지 덮어야 한다")
}

const (
	ov2AllObserved  = "OOOOOOOO" // 여덟 항목이 모두 관찰된 날
	ov2FourObserved = "OOOOxxxx" // 네 항목이 관찰되고 나머지는 괜찮다고 말한 날
	ov2Calm         = "xxxxxxxx" // 여덟 항목을 모두 이야기했고 아무것도 관찰되지 않은 날
	ov2NoTalk       = ""
)

// 처음부터 점수가 높은 사람도 0단계에서 3단계까지 가는 데 대화한 날이 사흘 필요하다.
//
// 날마다 여덟 항목이 모두 관찰된다.
//
//	1~6일째: n = 1~6 → 기록 부족 → 전날(0단계)을 잇는다
//	7일째: n = 7, 항목마다 7일 관찰 → 14 × 7 ÷ 7 = 14일 → 3점 × 8 = 24점 → 점수로는 3단계
//	       신뢰도 = min(7/14, 8/8, 56/56) = 0.5 → 보통. 대화한 날이므로 오르되 한 단계만 → 1단계
//	8일째: 24점, 3단계를 향해 한 단계 → 2단계
//	9일째: → 3단계
func TestOracleV2JumpFromZeroTakesThreeRecordDays(t *testing.T) {
	t.Run("날마다 대화하면 7, 8, 9일째에 한 단계씩 오른다", func(t *testing.T) {
		r := ov2Compute(t, ov2Rep(9, ov2AllObserved))

		ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 8, stage.Suggestion}, {9, 9, stage.Recommendation}})

		day7 := ov2At(t, r, 7)
		assert.False(t, day7.Insufficient)
		assert.Equal(t, 24, day7.Score)
		assert.Equal(t, stage.Recommendation, day7.Raw, "그날의 값만으로는 3단계다")
		assert.True(t, day7.Held)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, day7.HeldBy())

		day8 := ov2At(t, r, 8)
		assert.True(t, day8.Held)
		assert.Equal(t, stage.ReasonHeldOneStepPerDay, day8.HeldBy())

		day9 := ov2At(t, r, 9)
		assert.False(t, day9.Held, "오르려던 만큼 다 올랐다")
		assert.Zero(t, day9.HeldBy())

		previous, changed := r.Changed()
		assert.True(t, changed)
		assert.Equal(t, stage.Suggestion, previous)
	})

	// 7일째까지 날마다 대화하고, 8일째 쉼, 9일째 대화, 10일째 쉼, 11일째 대화.
	//
	//	8일째(쉼):  창 안의 n = 7 그대로 → 24점, 3단계를 향하지만 대화하지 않은 날이라 1단계에 묶인다
	//	9일째:      n = 8 → 24점 → 2단계
	//	10일째(쉼): n = 8 → 2단계에 묶인다
	//	11일째:     n = 9 → 3단계
	//
	// 달력으로는 닷새가 걸렸지만 오른 날은 대화한 사흘(7, 9, 11일째)뿐이다.
	t.Run("사이에 쉰 날이 끼면 쉰 날에는 오르지 않는다", func(t *testing.T) {
		r := ov2Compute(t, ov2Cat(
			ov2Rep(7, ov2AllObserved),
			[]string{ov2NoTalk, ov2AllObserved, ov2NoTalk, ov2AllObserved},
		))

		ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 8, stage.Reflection}, {9, 10, stage.Suggestion}, {11, 11, stage.Recommendation}})

		for _, nth := range []int{8, 10} {
			pt := ov2At(t, r, nth)
			assert.False(t, pt.HasRecord, "%d일째는 대화하지 않은 날이다", nth)
			assert.Equal(t, 24, pt.Score, "%d일째의 점수", nth)
			assert.Equal(t, stage.Recommendation, pt.Raw, "%d일째", nth)
			assert.True(t, pt.Held, "%d일째", nth)
			assert.Equal(t, stage.ReasonHeldNoRecordToday, pt.HeldBy(), "%d일째", nth)
		}
	})
}

// 대화하지 않은 날에도 창은 움직인다. 관찰되지 않은 날이 창에서 빠지면 n이 줄어 점수가 오른다.
// 그렇게 쉰 날에 오르려던 단계는 다음에 대화한 날에 오른다.
//
// 1, 2, 4, 6, 8일째는 아무것도 관찰되지 않은 대화, 3, 5, 7, 9일째는 다섯 항목이 관찰된 대화, 10~15일째는 쉼, 16일째는 다섯 항목이 관찰된 대화.
// 아래에서 o는 그 다섯 항목 각각의 관찰된 일수다.
//
//	7일째:     n = 7, o = 3 → 14×3÷7 = 6일 → 1점 × 5 = 5점 → 1단계(신뢰도 7/14 = 0.5 보통, 대화한 날)
//	8일째:     n = 8, o = 3 → 5.25 → 5일 → 5점 → 1단계
//	9일째:     n = 9, o = 4 → 6.22 → 6일 → 5점 → 1단계
//	10~14일째: 창이 아직 1일째를 포함한다. n = 9, o = 4 → 5점 → 1단계
//	15일째(쉼): 창은 2~15일째. 1일째가 빠져 n = 8, o = 4 → 14×4÷8 = 7일 → 2점 × 5 = 10점 → 2단계를 향한다.
//	            대화하지 않은 날이라 1단계에 묶인다(신뢰도는 8/14 = 0.57 보통이라 까닭은 신뢰도가 아니다)
//	16일째:    창은 3~16일째. 2일째가 빠지고 16일째가 들어와 n = 8, o = 5 → 8.75 → 9일 → 2점 × 5 = 10점 → 2단계
func TestOracleV2RaiseAttemptedOnSilentDayLandsOnNextRecordDay(t *testing.T) {
	const five = "OOOOOxxx"
	r := ov2Compute(t, ov2Cat(
		[]string{ov2Calm, ov2Calm, five, ov2Calm, five, ov2Calm, five, ov2Calm, five},
		ov2Rep(6, ov2NoTalk),
		[]string{five},
	))

	ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 15, stage.Reflection}, {16, 16, stage.Suggestion}})

	day14 := ov2At(t, r, 14)
	assert.Equal(t, 9, day14.ConversationDays)
	assert.Equal(t, 5, day14.Score)
	assert.False(t, day14.Held)

	day15 := ov2At(t, r, 15)
	assert.False(t, day15.HasRecord)
	assert.Equal(t, 8, day15.ConversationDays)
	assert.Equal(t, 10, day15.Score)
	assert.Equal(t, confidence.Medium, day15.Confidence)
	assert.Equal(t, stage.Suggestion, day15.Raw)
	assert.Equal(t, stage.Reflection, day15.Stage)
	assert.True(t, day15.Held)
	assert.Equal(t, stage.ReasonHeldNoRecordToday, day15.HeldBy())

	day16 := ov2At(t, r, 16)
	assert.True(t, day16.HasRecord)
	assert.Equal(t, 8, day16.ConversationDays)
	assert.Equal(t, 10, day16.Score)
	assert.Equal(t, stage.Suggestion, day16.Stage)
	assert.False(t, day16.Held)
	assert.Equal(t, 1, day16.ElevatedDays, "2단계 이상의 첫날이다")
}

// 2단계 이상이 13일째인 날은 아직 2단계이고, 14일째인 날부터 3단계다.
//
// 날마다 네 항목이 관찰된다. 점수는 3점 × 4 = 12점이라 점수만으로는 언제까지나 2단계다.
//
//	7일째: 12점 → 2단계를 향하지만 하루에 한 단계 → 1단계
//	8일째: 2단계. 이어진 일수 1
//	8+k일째: 이어진 일수 1+k → 20일째가 13, 21일째가 14
func TestOracleV2SustainedCounterDay13AndDay14(t *testing.T) {
	t.Run("날마다 대화하면 20일째는 2단계이고 21일째에 3단계가 된다", func(t *testing.T) {
		r := ov2Compute(t, ov2Rep(21, ov2FourObserved))

		ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 20, stage.Suggestion}, {21, 21, stage.Recommendation}})

		day20 := ov2At(t, r, 20)
		assert.Equal(t, 12, day20.Score)
		assert.Equal(t, 13, day20.ElevatedDays)
		assert.Equal(t, stage.Suggestion, day20.Raw)
		assert.NotContains(t, day20.Reasons, stage.ReasonSustained)

		day21 := ov2At(t, r, 21)
		assert.Equal(t, 12, day21.Score, "점수는 그대로다. 오른 까닭은 이어진 일수뿐이다")
		assert.Equal(t, 14, day21.ElevatedDays)
		assert.Contains(t, day21.Reasons, stage.ReasonSustained)
		assert.False(t, day21.Held)
	})

	// 14일째가 되는 날(21일째)에 대화하지 않으면 그날은 오르지 못하고, 다음에 대화한 날(22일째)에 오른다.
	//
	//	21일째(쉼): 창 8~21일째, n = 13, 네 항목이 13일 관찰 → 14일 → 12점. 이어진 일수로는 3단계를 향하지만 묶여서 2단계.
	//	            그날도 2단계 이상이므로 이어진 일수는 14가 된다
	//	22일째:     창 9~22일째, n = 13 → 12점. 이어진 일수 15 → 3단계
	t.Run("14일째인 날에 쉬면 다음에 대화한 날에 3단계가 된다", func(t *testing.T) {
		r := ov2Compute(t, ov2Cat(ov2Rep(20, ov2FourObserved), []string{ov2NoTalk, ov2FourObserved}))

		ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 21, stage.Suggestion}, {22, 22, stage.Recommendation}})

		day21 := ov2At(t, r, 21)
		assert.Equal(t, stage.Recommendation, day21.Raw)
		assert.True(t, day21.Held)
		assert.Equal(t, stage.ReasonHeldNoRecordToday, day21.HeldBy())
		assert.Equal(t, 14, day21.ElevatedDays)

		day22 := ov2At(t, r, 22)
		assert.Equal(t, 15, day22.ElevatedDays)
		assert.Contains(t, day22.Reasons, stage.ReasonSustained)
	})

	// 기록 부족이어서 단계를 이어 간 날은 세지 않고 끊지도 않는다.
	//
	// 1~12일째 날마다 대화(네 항목 관찰), 13~22일째 쉼, 23~30일째 날마다 대화(네 항목 관찰).
	//
	//	8~12일째:  2단계, 이어진 일수 1~5
	//	13~19일째: 쉬었지만 창 안의 n이 12, 12, 11, 10, 9, 8, 7이라 점수가 난다.
	//	           네 항목은 대화한 날마다 관찰됐으므로 환산 일수 14 → 12점 → 2단계. 이어진 일수 6~12
	//	20일째:    창 7~20일째, n = 6 → 기록 부족. 2단계와 이어진 일수 12를 그대로 잇는다
	//	21, 22일째: n = 5, 4 → 그대로
	//	23~28일째: 다시 대화하지만 창 안의 n은 4, 4, 4, 4, 5, 6 → 기록 부족 → 그대로. 이어진 일수는 여전히 12
	//	29일째:    창 16~29일째, n = 7(23~29일째) → 12점 → 2단계. 이어진 일수 13 → 아직 2단계
	//	30일째:    창 17~30일째, n = 8 → 12점. 이어진 일수 14 → 3단계(신뢰도 8/14 = 0.57 보통, 대화한 날)
	//
	// 기록 부족인 날에 0단계로 떨어뜨렸다면 이어진 일수가 20일째에 끊겨, 30일째에는 1단계에서 막 2단계가 된 참이었을 것이다.
	t.Run("기록 부족으로 이어 간 아흐레를 사이에 두고 13일째와 14일째가 갈린다", func(t *testing.T) {
		r := ov2Compute(t, ov2Cat(
			ov2Rep(12, ov2FourObserved),
			ov2Rep(10, ov2NoTalk),
			ov2Rep(8, ov2FourObserved),
		))

		ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 29, stage.Suggestion}, {30, 30, stage.Recommendation}})

		for nth := 8; nth <= 19; nth++ {
			pt := ov2At(t, r, nth)
			assert.False(t, pt.Insufficient, "%d일째에는 점수가 난다", nth)
			assert.Equal(t, 12, pt.Score, "%d일째의 점수", nth)
			assert.Equal(t, nth-7, pt.ElevatedDays, "%d일째의 이어진 일수", nth)
		}

		wantN := map[int]int{20: 6, 21: 5, 22: 4, 23: 4, 24: 4, 25: 4, 26: 4, 27: 5, 28: 6}
		for nth := 20; nth <= 28; nth++ {
			pt := ov2At(t, r, nth)
			assert.Equal(t, wantN[nth], pt.ConversationDays, "%d일째 창 안의 대화한 날 수", nth)
			assert.True(t, pt.Insufficient, "%d일째는 기록 부족이다", nth)
			assert.Equal(t, 12, pt.ElevatedDays, "%d일째에는 세지도 끊지도 않는다", nth)
			assert.Equal(t, stage.Everyday, pt.Raw, "%d일째는 점수가 없어 그날의 값만으로는 0단계다", nth)
			assert.False(t, pt.Held, "%d일째는 오르려던 날이 아니다", nth)
			assert.Contains(t, pt.Reasons, stage.ReasonCarriedInsufficientRecords, "%d일째", nth)
		}

		day29 := ov2At(t, r, 29)
		assert.Equal(t, 7, day29.ConversationDays)
		assert.Equal(t, 12, day29.Score)
		assert.Equal(t, 13, day29.ElevatedDays)
		assert.Equal(t, stage.Suggestion, day29.Raw)

		day30 := ov2At(t, r, 30)
		assert.Equal(t, 8, day30.ConversationDays)
		assert.Equal(t, 12, day30.Score)
		assert.Equal(t, 14, day30.ElevatedDays)
		assert.Equal(t, stage.Recommendation, day30.Raw)
		assert.Contains(t, day30.Reasons, stage.ReasonSustained)
		assert.Equal(t, confidence.Medium, day30.Confidence)
	})
}

// 기록 부족인 동안에는 단계를 이어 가다가, 창 안에 대화한 날이 하나도 없게 되는 날 0단계로 돌아간다.
//
// 1~10일째 날마다 대화(네 항목 관찰 → 12점), 11~29일째 쉼, 30일째 대화.
//
//	8~10일째:  2단계, 이어진 일수 1~3
//	11~17일째: 창 안의 n = 10, 10, 10, 10, 9, 8, 7 → 12점 → 2단계. 이어진 일수 4~10
//	18~23일째: n = 6, 5, 4, 3, 2, 1 → 기록 부족 → 2단계와 이어진 일수 10을 잇는다
//	24일째:    창 11~24일째에 대화한 날이 없다 → 0단계, 이어진 일수 0
//	25~29일째: 그대로 0단계
//	30일째:    돌아와 대화했지만 n = 1 → 기록 부족 → 전날(0단계)을 잇는다. 오르려던 것이 없으므로 묶인 날도 아니다
//	           (평소 4에 그날 4개: 4 − 4 − 0.5 < 0이라 변화 감지도 아니다)
func TestOracleV2CarryAcrossInsufficientDaysThenEmptyWindow(t *testing.T) {
	r := ov2Compute(t, ov2Cat(
		ov2Rep(10, ov2FourObserved),
		ov2Rep(19, ov2NoTalk),
		[]string{ov2FourObserved},
	))

	ov2AssertStages(t, r, []ov2Span{{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 23, stage.Suggestion}, {24, 30, stage.Everyday}})

	assert.Equal(t, 10, ov2At(t, r, 17).ElevatedDays)
	assert.False(t, ov2At(t, r, 17).Insufficient)

	wantN := map[int]int{18: 6, 19: 5, 20: 4, 21: 3, 22: 2, 23: 1}
	for nth := 18; nth <= 23; nth++ {
		pt := ov2At(t, r, nth)
		assert.Equal(t, wantN[nth], pt.ConversationDays, "%d일째 창 안의 대화한 날 수", nth)
		assert.True(t, pt.Insufficient, "%d일째", nth)
		assert.Equal(t, 10, pt.ElevatedDays, "%d일째의 이어진 일수", nth)
	}

	for nth := 24; nth <= 29; nth++ {
		pt := ov2At(t, r, nth)
		assert.Zero(t, pt.ConversationDays, "%d일째", nth)
		assert.Zero(t, pt.ElevatedDays, "%d일째의 이어진 일수", nth)
		assert.Equal(t, []stage.Reason{stage.ReasonNoRecentRecords}, pt.Reasons, "%d일째", nth)
		assert.False(t, pt.Held, "%d일째", nth)
	}

	day30 := ov2At(t, r, 30)
	assert.True(t, day30.HasRecord)
	assert.Equal(t, 1, day30.ConversationDays)
	assert.True(t, day30.Insufficient)
	assert.False(t, day30.Detected)
	assert.False(t, day30.Held)
	assert.Zero(t, day30.ElevatedDays)
}

// 신뢰도가 낮은 날에도 내려가는 것은 막지 않는다.
//
// 1~14일째는 "OOOOxxxx", 15~28일째는 "o......."(첫 항목만 간접 추론으로 관찰, 나머지는 언급 없음). 날마다 대화하므로 15일째부터 n = 14.
// j일째의 창에는 앞의 기록이 a = 28 − j일, 뒤의 기록이 14 − a일 들어 있다. n = 14라 환산 일수는 관찰된 일수 그대로다.
//
//	첫 항목: 14일 모두 관찰 → 3점.   둘째~넷째 항목: a일 관찰.   나머지: 0점
//	15, 16일째: a = 13, 12 → 3점씩 → 3 + 9 = 12점 → 2단계
//	17~21일째:  a = 11~7   → 2점씩 → 3 + 6 = 9점  → 1단계(17일째의 신뢰도는 직접 44 ÷ 관찰 47 = 0.94로 높다)
//	22~27일째:  a = 6~1    → 1점씩 → 3 + 3 = 6점  → 1단계
//	  27일째의 신뢰도: 직접 4 ÷ 관찰 (4 + 13) = 0.24 → 낮음. 점수로 본 단계가 전날과 같아 그대로 1단계
//	28일째:     a = 0 → 3점 → 0단계를 향한다. 언급된 항목 1 ÷ 8 = 0.125, 직접 0 ÷ 14 = 0 → 낮음.
//	            낮은 신뢰도는 오르는 것만 막으므로 0단계로 내려간다
func TestOracleV2FallOnLowConfidenceDay(t *testing.T) {
	r := ov2Compute(t, ov2Cat(ov2Rep(14, ov2FourObserved), ov2Rep(14, "o.......")))

	ov2AssertStages(t, r, []ov2Span{
		{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 16, stage.Suggestion},
		{17, 27, stage.Reflection}, {28, 28, stage.Everyday},
	})

	day16, day17 := ov2At(t, r, 16), ov2At(t, r, 17)
	assert.Equal(t, 12, day16.Score)
	assert.Equal(t, 9, day17.Score)
	assert.Equal(t, confidence.High, day17.Confidence)
	assert.Zero(t, day17.ElevatedDays, "2단계 아래로 내려가면 이어진 일수는 0이다")

	assert.Equal(t, 9, ov2At(t, r, 21).Score)
	assert.Equal(t, 6, ov2At(t, r, 22).Score)

	day27 := ov2At(t, r, 27)
	assert.Equal(t, 6, day27.Score)
	assert.Equal(t, confidence.Low, day27.Confidence)
	assert.Equal(t, stage.Reflection, day27.Stage)

	day28 := ov2At(t, r, 28)
	assert.False(t, day28.Insufficient)
	assert.Equal(t, 3, day28.Score)
	assert.Equal(t, confidence.Low, day28.Confidence)
	assert.Equal(t, stage.Everyday, day28.Raw)
	assert.Equal(t, stage.Everyday, day28.Stage)
	assert.False(t, day28.Held)
}

// 신뢰도가 낮은 동안에는 점수가 아무리 높아도 오르지 않고, 낮음에서 벗어난 날부터 하루에 한 단계씩 오른다.
//
// 1~7일째는 여덟 항목이 모두 간접 추론으로 관찰, 8~14일째는 모두 직접 언급으로 관찰. 점수는 7일째부터 줄곧 24점이다.
// j일째의 직접 언급 비율 = 8(j − 7) ÷ 8j = (j − 7) ÷ j. 나머지 두 요소(n ÷ 14는 7일째에 0.5, 항목은 8/8)는 0.4 이상이다.
//
//	7일째 0/7 = 0, 8일째 1/8 = 0.125, 9일째 0.22, 10일째 0.3, 11일째 4/11 = 0.36 → 낮음 → 0단계에 묶인다
//	12일째 5/12 = 0.417 → 보통 → 1단계
//	13일째 6/13 = 0.46 → 2단계
//	14일째 7/14 = 0.5  → 3단계
func TestOracleV2LowConfidenceBlocksRiseUntilItLifts(t *testing.T) {
	r := ov2Compute(t, ov2Cat(ov2Rep(7, "oooooooo"), ov2Rep(7, ov2AllObserved)))

	ov2AssertStages(t, r, []ov2Span{
		{1, 11, stage.Everyday}, {12, 12, stage.Reflection}, {13, 13, stage.Suggestion}, {14, 14, stage.Recommendation},
	})

	for nth := 7; nth <= 11; nth++ {
		pt := ov2At(t, r, nth)
		assert.Equal(t, 24, pt.Score, "%d일째의 점수", nth)
		assert.Equal(t, confidence.Low, pt.Confidence, "%d일째의 신뢰도", nth)
		assert.Equal(t, stage.Recommendation, pt.Raw, "%d일째", nth)
		assert.True(t, pt.Held, "%d일째", nth)
		assert.Equal(t, stage.ReasonHeldLowConfidence, pt.HeldBy(), "%d일째", nth)
	}

	day12 := ov2At(t, r, 12)
	assert.Equal(t, confidence.Medium, day12.Confidence)
	assert.Equal(t, stage.ReasonHeldOneStepPerDay, day12.HeldBy(), "이제 묶는 것은 하루에 한 단계뿐이다")
}

// 변화 감지 상태면 점수가 낮아도 1단계다. 기록 부족인 날에는 그 1단계도 새로 오르지 못하고,
// 창이 비면 변화 감지가 켜진 채여도 0단계로 돌아간다.
//
// 1~14일째는 아무것도 관찰되지 않은 대화(평소 0), 15~17일째는 두 항목이 관찰된 대화, 18~39일째 쉼, 40일째 돌아와 대화.
//
//	누적값: 더할 값은 2 − 0 − 0.5 = 1.5 → 15일째 1.5, 16일째 3.0, 17일째 4.5 > 4 → 감지. 쉬는 동안 4.5가 그대로 남는다
//	점수:   15~17일째는 n = 14, 두 항목이 1~3일 관찰 → 1점씩 → 2점 → 점수로는 0단계
//	15, 16일째: 0단계
//	17일째:     감지 → 1단계(신뢰도는 14/14, 8/8, 직접뿐 → 높음. 대화한 날)
//	18~24일째:  쉼. n = 13~7, 두 항목이 3일 관찰 → 환산 3~6일 → 2점 → 점수로는 0단계, 감지 때문에 1단계 그대로
//	25~30일째:  n = 6~1 → 기록 부족 → 1단계를 잇는다
//	31일째:     창 18~31일째에 대화한 날이 없다 → 감지가 켜진 채여도 0단계
//	32~39일째:  0단계
//	40일째:     n = 1 → 기록 부족 → 전날(0단계)을 잇는다
//	  두 항목이 관찰된 날로 돌아오면: 누적값 6.0, 감지 → 1단계를 향하지만 기록 부족이라 0단계에 묶인다
//	  아무것도 관찰되지 않은 날로 돌아오면: 4.5 − 0.5 = 4.0, 한계값과 같으므로 감지가 풀린다 → 오르려던 것이 없다
func TestOracleV2ChangeDetectedFloorAndLongAbsence(t *testing.T) {
	const two = "OOxxxxxx"
	absence := ov2Cat(ov2Rep(14, ov2Calm), ov2Rep(3, two), ov2Rep(22, ov2NoTalk))

	t.Run("두 항목이 관찰된 날로 돌아오면 기록 부족이라 0단계에 묶인다", func(t *testing.T) {
		r := ov2Compute(t, ov2Cat(absence, []string{two}))

		ov2AssertStages(t, r, []ov2Span{{1, 16, stage.Everyday}, {17, 30, stage.Reflection}, {31, 40, stage.Everyday}})

		day16 := ov2At(t, r, 16)
		assert.False(t, day16.Detected, "3.0은 한계값을 넘지 않는다")
		assert.Equal(t, 2, day16.Score)

		day17 := ov2At(t, r, 17)
		assert.True(t, day17.Detected)
		assert.Equal(t, 2, day17.Score)
		assert.Equal(t, []stage.Reason{stage.ReasonChangeDetected}, day17.Reasons)
		assert.False(t, day17.Held)

		day24 := ov2At(t, r, 24)
		assert.Equal(t, 7, day24.ConversationDays)
		assert.Equal(t, 2, day24.Score)

		day30 := ov2At(t, r, 30)
		assert.Equal(t, 1, day30.ConversationDays)
		assert.True(t, day30.Insufficient)
		assert.True(t, day30.Detected)

		day31 := ov2At(t, r, 31)
		assert.Zero(t, day31.ConversationDays)
		assert.True(t, day31.Detected, "누적값은 쉬는 동안 줄지 않으므로 감지 자체는 켜진 채다")
		assert.Equal(t, []stage.Reason{stage.ReasonNoRecentRecords}, day31.Reasons)

		day40 := ov2At(t, r, 40)
		assert.True(t, day40.HasRecord)
		assert.True(t, day40.Insufficient)
		assert.True(t, day40.Detected)
		assert.Equal(t, stage.Reflection, day40.Raw)
		assert.Equal(t, stage.Everyday, day40.Stage)
		assert.True(t, day40.Held)
		assert.Equal(t, stage.ReasonHeldInsufficientRecords, day40.HeldBy())
	})

	t.Run("아무것도 관찰되지 않은 날로 돌아오면 감지가 풀려 오르려던 것도 없다", func(t *testing.T) {
		r := ov2Compute(t, ov2Cat(absence, []string{ov2Calm}))

		day40 := ov2At(t, r, 40)
		assert.False(t, day40.Detected, "4.0은 한계값과 같을 뿐 넘지 않는다")
		assert.Equal(t, stage.Everyday, day40.Raw)
		assert.Equal(t, stage.Everyday, day40.Stage)
		assert.False(t, day40.Held)
	})
}

// 하루에 한 단계는 오르는 쪽에만 걸린다. 내려갈 때는 그날의 값까지 한 번에 내려간다.
//
// 1~21일째는 네 항목이 관찰된 대화, 22~24일째는 아무것도 관찰되지 않은 대화. n = 14.
//
//	21일째: 이어진 일수 14 → 3단계
//	22일째: 네 항목이 13일 관찰 → 3점씩 12점 → 점수로 2단계, 이어진 일수 15 → 3단계
//	23일째: 12일 관찰 → 3점씩 12점 → 3단계
//	24일째: 11일 관찰 → 2점씩 8점 → 점수로 1단계. 2단계가 아니므로 이어진 일수로 올릴 것도 없다 → 3단계에서 1단계로
func TestOracleV2FallIsNotLimitedToOneStep(t *testing.T) {
	r := ov2Compute(t, ov2Cat(ov2Rep(21, ov2FourObserved), ov2Rep(3, ov2Calm)))

	ov2AssertStages(t, r, []ov2Span{
		{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 20, stage.Suggestion},
		{21, 23, stage.Recommendation}, {24, 24, stage.Reflection},
	})

	assert.Equal(t, 16, ov2At(t, r, 23).ElevatedDays)

	day24 := ov2At(t, r, 24)
	assert.Equal(t, 8, day24.Score)
	assert.Zero(t, day24.ElevatedDays)
	assert.False(t, day24.Held)

	previous, changed := r.Changed()
	assert.True(t, changed)
	assert.Equal(t, stage.Recommendation, previous)
}

// 점수로 본 단계의 경계는 "이 점수 이상"이다: 0~4는 0단계, 5~9는 1단계, 10~14는 2단계, 15 이상은 3단계.
func TestOracleV2StageFromScoreBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		score int
		want  stage.Stage
	}{
		{name: "0점은 0단계", score: 0, want: stage.Everyday},
		{name: "4점은 0단계", score: 4, want: stage.Everyday},
		{name: "5점부터 1단계", score: 5, want: stage.Reflection},
		{name: "9점은 1단계", score: 9, want: stage.Reflection},
		{name: "10점부터 2단계", score: 10, want: stage.Suggestion},
		{name: "14점은 2단계", score: 14, want: stage.Suggestion},
		{name: "15점부터 3단계", score: 15, want: stage.Recommendation},
		{name: "가장 높은 24점은 3단계", score: 24, want: stage.Recommendation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stage.FromScore(tt.score, params.Default().Stage))
		})
	}
}

// ov2ThreeOnThreeOff는 사흘 대화하고 사흘 쉬기를 되풀이하는 사람의 기록이다. 1~3일째 대화, 4~6일째 쉼, 7~9일째 대화 …
func ov2ThreeOnThreeOff(total int, line string) []string {
	lines := make([]string, total)
	for i := range lines {
		if i%6 < 3 {
			lines[i] = line
		}
	}
	return lines
}

// 사흘 대화하고 사흘 쉬는 사람은 엿새마다 이틀씩 기록 부족이 된다. 그 이틀 동안 단계는 떨어지지 않고 이어지며,
// 2단계 이상이 이어진 일수는 그 이틀을 세지도 끊지도 않는다.
//
// 대화한 날: 1, 2, 3, 7, 8, 9, 13, 14, 15, 19, 20, 21, 25, 26, 27, 31, 32, 33일째.
// 창(그날을 포함한 최근 14일) 안의 대화한 날 수 n:
//
//	1~12일째:  1, 2, 3, 3, 3, 3, 4, 5, 6, 6, 6, 6 → 모두 기록 부족
//	13일째: 7(1, 2, 3, 7, 8, 9, 13)   14일째: 8   15일째: 8(창 2~15)   16일째: 7(창 3~16)
//	17일째: 6(창 4~17에 7, 8, 9, 13, 14, 15)   18일째: 6
//	그 뒤로 엿새마다 7, 8, 8, 7, 6, 6이 되풀이된다(19~24일째, 25~30일째, 31일째부터)
//
// 대화한 날마다 같은 항목이 관찰되므로, n이 7 이상인 날의 환산 일수는 언제나 14일이고 점수는 관찰된 항목 수 × 3점이다.
// 평소는 대화한 날마다 같은 수라서 더할 값이 언제나 −0.5이고, 변화 감지는 켜지지 않는다.
func TestOracleV2ThreeDaysOnThreeDaysOff(t *testing.T) {
	insufficientDays := map[int]bool{17: true, 18: true, 23: true, 24: true, 29: true, 30: true}

	// 여덟 항목이 모두 관찰되는 사람: 24점, 점수로는 3단계.
	//
	//	13일째: 0단계에서 한 단계 → 1(신뢰도 7/14 = 0.5 보통)   14일째: 2   15일째: 3
	//	16일째(쉼, n = 7): 24점, 3단계 그대로
	//	17, 18일째: 기록 부족 → 3단계를 잇는다. 점수가 없다고 0단계로 떨어지지 않는다
	//	이어진 일수: 14일째 1, 15일째 2, 16일째 3, (17, 18일째 3), 19~22일째 4~7, (23, 24일째 7), 25~28일째 8~11, (29, 30일째 11)
	t.Run("점수가 높은 사람은 15일째에 3단계가 된 뒤 기록 부족인 날에도 3단계에 머문다", func(t *testing.T) {
		r := ov2Compute(t, ov2ThreeOnThreeOff(30, ov2AllObserved))

		ov2AssertStages(t, r, []ov2Span{
			{1, 12, stage.Everyday}, {13, 13, stage.Reflection}, {14, 14, stage.Suggestion}, {15, 30, stage.Recommendation},
		})

		wantN := map[int]int{13: 7, 14: 8, 15: 8, 16: 7, 17: 6, 18: 6, 19: 7, 20: 8, 21: 8, 22: 7, 23: 6, 24: 6, 25: 7, 26: 8, 27: 8, 28: 7, 29: 6, 30: 6}
		wantElevated := map[int]int{14: 1, 15: 2, 16: 3, 17: 3, 18: 3, 19: 4, 20: 5, 21: 6, 22: 7, 23: 7, 24: 7, 25: 8, 26: 9, 27: 10, 28: 11, 29: 11, 30: 11}
		for nth := 13; nth <= 30; nth++ {
			pt := ov2At(t, r, nth)
			assert.Equal(t, wantN[nth], pt.ConversationDays, "%d일째 창 안의 대화한 날 수", nth)
			assert.Equal(t, insufficientDays[nth], pt.Insufficient, "%d일째 기록 부족 여부", nth)
			assert.Equal(t, wantElevated[nth], pt.ElevatedDays, "%d일째의 이어진 일수", nth)
			assert.False(t, pt.Detected, "%d일째", nth)
			if !pt.Insufficient {
				assert.Equal(t, 24, pt.Score, "%d일째의 점수", nth)
			}
		}
		for nth := range insufficientDays {
			pt := ov2At(t, r, nth)
			assert.Equal(t, stage.Everyday, pt.Raw, "%d일째는 점수가 없어 그날의 값만으로는 0단계다", nth)
			assert.Contains(t, pt.Reasons, stage.ReasonCarriedInsufficientRecords, "%d일째", nth)
			assert.False(t, pt.Held, "%d일째", nth)
		}
	})

	// 네 항목이 관찰되는 사람: 12점, 점수로는 2단계. 3단계는 이어진 일수로만 된다.
	//
	//	13일째: 1단계   14일째: 2단계, 이어진 일수 1
	//	엿새마다 나흘씩 센다: 16일째 3, 22일째 7, 28일째 11, (29, 30일째 11), 31일째 12, 32일째 13, 33일째 14
	//	32일째: 13일째라 아직 2단계
	//	33일째: 14일째 → 3단계(대화한 날, 신뢰도 8/14 = 0.57 보통)
	//
	// 기록 부족인 날마다 끊겼다면 이어진 일수는 나흘을 넘지 못해 3단계가 되는 날이 오지 않는다.
	t.Run("점수로는 2단계인 사람은 기록 부족인 엿새를 건너 33일째에 3단계가 된다", func(t *testing.T) {
		r := ov2Compute(t, ov2ThreeOnThreeOff(33, ov2FourObserved))

		ov2AssertStages(t, r, []ov2Span{
			{1, 12, stage.Everyday}, {13, 13, stage.Reflection}, {14, 32, stage.Suggestion}, {33, 33, stage.Recommendation},
		})

		assert.Equal(t, 11, ov2At(t, r, 28).ElevatedDays)
		assert.Equal(t, 11, ov2At(t, r, 30).ElevatedDays)
		assert.True(t, ov2At(t, r, 30).Insufficient)

		day31 := ov2At(t, r, 31)
		assert.Equal(t, 7, day31.ConversationDays)
		assert.Equal(t, 12, day31.Score)
		assert.Equal(t, 12, day31.ElevatedDays)

		day32 := ov2At(t, r, 32)
		assert.Equal(t, 13, day32.ElevatedDays)
		assert.Equal(t, stage.Suggestion, day32.Raw)
		assert.NotContains(t, day32.Reasons, stage.ReasonSustained)

		day33 := ov2At(t, r, 33)
		assert.Equal(t, 8, day33.ConversationDays)
		assert.Equal(t, 12, day33.Score)
		assert.Equal(t, 14, day33.ElevatedDays)
		assert.Equal(t, confidence.Medium, day33.Confidence)
		assert.Contains(t, day33.Reasons, stage.ReasonSustained)
		assert.False(t, day33.Held)
	})
}

// 2단계 이상이 14일째가 되는 날에 신뢰도가 낮으면 3단계로 오르지 못하고, 낮음에서 벗어난 날에 오른다.
// 묶여 있는 동안에도 2단계이므로 이어진 일수는 계속 센다.
//
// 1~7일째 "OOOOxxxx", 8~21일째 "oooo...."(네 항목이 간접 추론으로 관찰, 나머지는 언급 없음), 22~27일째 다시 "OOOOxxxx".
// 네 항목은 날마다 관찰되므로 7일째부터 점수는 줄곧 12점(점수로는 2단계)이다. 평소 4, 날마다 4개라 변화 감지는 없다.
//
// 직접 언급의 비율 = 창 안의 직접 언급한 날 수 ÷ n(관찰은 날마다 네 개씩이라 날 수로 셈해도 같다):
//
//	14일째 7/14 = 0.5   15일째(창 2~15) 6/14 = 0.43 → 보통
//	16일째 5/14 = 0.36  …  21일째(창 8~21) 0/14 → 낮음. 21일째는 언급된 항목도 4/8이다
//	22일째 1/14  …  26일째(창 13~26) 5/14 = 0.36 → 낮음
//	27일째(창 14~27) 6/14 = 0.43 → 보통. 언급된 항목 8/8, n = 14
//
//	7일째: 1단계   8일째: 2단계, 이어진 일수 1  …  20일째: 13
//	21일째: 14일째라 3단계를 향하지만 신뢰도가 낮아 2단계에 묶인다. 이어진 일수 14
//	22~26일째: 여전히 낮음 → 2단계에 묶인다. 이어진 일수 15~19
//	27일째: 보통 → 3단계. 이어진 일수 20
func TestOracleV2SustainedRiseWaitsForConfidence(t *testing.T) {
	r := ov2Compute(t, ov2Cat(
		ov2Rep(7, ov2FourObserved),
		ov2Rep(14, "oooo...."),
		ov2Rep(6, ov2FourObserved),
	))

	ov2AssertStages(t, r, []ov2Span{
		{1, 6, stage.Everyday}, {7, 7, stage.Reflection}, {8, 26, stage.Suggestion}, {27, 27, stage.Recommendation},
	})

	assert.Equal(t, confidence.Medium, ov2At(t, r, 15).Confidence)
	assert.Equal(t, confidence.Low, ov2At(t, r, 16).Confidence)

	day20 := ov2At(t, r, 20)
	assert.Equal(t, 13, day20.ElevatedDays)
	assert.Equal(t, stage.Suggestion, day20.Raw)
	assert.False(t, day20.Held, "신뢰도가 낮아도 오르려던 날이 아니면 묶인 날이 아니다")

	for nth := 21; nth <= 26; nth++ {
		pt := ov2At(t, r, nth)
		assert.Equal(t, 12, pt.Score, "%d일째의 점수", nth)
		assert.Equal(t, confidence.Low, pt.Confidence, "%d일째의 신뢰도", nth)
		assert.Equal(t, stage.Recommendation, pt.Raw, "%d일째", nth)
		assert.True(t, pt.Held, "%d일째", nth)
		assert.Equal(t, stage.ReasonHeldLowConfidence, pt.HeldBy(), "%d일째", nth)
		assert.Equal(t, nth-7, pt.ElevatedDays, "%d일째의 이어진 일수", nth)
	}

	day27 := ov2At(t, r, 27)
	assert.Equal(t, 12, day27.Score)
	assert.Equal(t, confidence.Medium, day27.Confidence)
	assert.Equal(t, 20, day27.ElevatedDays)
	assert.Contains(t, day27.Reasons, stage.ReasonSustained)
	assert.False(t, day27.Held)
}
