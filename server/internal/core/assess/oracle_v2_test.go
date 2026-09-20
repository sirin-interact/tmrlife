package assess_test

// 위기 관문에 전하는 최근 상태(추정 점수가 10 이상인지, 변화 감지 상태인지)를 손으로 따라간 값과 견주는 시험이다.
// 기대값은 코드를 돌려서 얻지 않았다. 경우마다 날짜별 셈을 적었으니, 깨지면 셈과 코드 가운데 어느 쪽이 틀렸는지 따져 볼 수 있다.
// 공개된 함수만 부른다.
//
// 손으로 따라간 규칙:
//
//	창은 기준일을 포함한 최근 14일, n은 창 안에서 대화한 날 수. n이 7보다 작으면 기록 부족이라 점수가 없다.
//	항목마다 환산 일수 = 14 × 관찰된 일수 ÷ n을 반올림. 0일 0점, 1~6일 1점, 7~11일 2점, 12~14일 3점.
//	평소는 첫 14일의 하루 평균 관찰 수. 15일째부터 대화한 날마다
//	누적값 = min(8, max(0, 누적값 + min(2, 그날 관찰된 수 − 평소 − 0.5))), 4를 넘으면(같으면 아니다) 변화 감지.
//
//	점수 쪽: 점수가 있고 10 이상일 때만 참. 기록 부족이면 거짓.
//	변화 감지 쪽: 변화 감지 상태이고 창 안에 대화한 날이 하나라도 있을 때만 참.
//	              누적값은 쉬는 동안 줄지 않으므로, 창이 비면 오래된 흔적으로 보고 꺼진 것으로 전한다.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
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
// 한 줄은 여덟 글자이고 자리는 항목 순서다. O는 관찰됨(직접 언급), x는 관찰되지 않음(직접 언급)이다.
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
			case 'x':
				day.Judgements[idx] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
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

const (
	ov2Calm  = "xxxxxxxx" // 여덟 항목을 모두 이야기했고 아무것도 관찰되지 않은 날
	ov2Two   = "OOxxxxxx"
	ov2Three = "OOOxxxxx"
	ov2Four  = "OOOOxxxx"
	ov2Five  = "OOOOOxxx"
	ov2All   = "OOOOOOOO"
	ov2Quiet = "" // 대화하지 않은 날
)

func ov2Evaluate(t *testing.T, lines []string, asOfNth int, p params.Params) assess.Evaluation {
	t.Helper()
	e, err := assess.Evaluate(ov2Diary(t, lines), ov2Nth(t, asOfNth), p)
	require.NoError(t, err)
	return e
}

// 점수 쪽의 경계는 "10 이상"이고, 그 기준은 평가에 쓴 조정 값에서 온다.
//
// 기준일은 14일째, 날마다 대화했으므로 n = 14이고 환산 일수는 관찰된 일수 그대로다.
// 기준선은 14일째가 지나야 잡히므로 변화 감지는 아직 돌지 않는다.
//
//	세 항목이 14일 관찰:                     3점 × 3 = 9점
//	거기에 넷째 항목이 하루(5일째) 관찰:       환산 1일 → 1점. 9 + 1 = 10점
//	네 항목이 14일 관찰:                     3점 × 4 = 12점
func TestOracleV2GateStateScoreBoundary(t *testing.T) {
	nineLines := ov2Rep(14, ov2Three)
	tenLines := ov2Rep(14, ov2Three)
	tenLines[4] = ov2Four
	twelveLines := ov2Rep(14, ov2Four)

	tests := []struct {
		name      string
		lines     []string
		minScore  int
		wantScore int
		want      bool
	}{
		{name: "9점은 기준 아래다", lines: nineLines, minScore: 10, wantScore: 9, want: false},
		{name: "10점부터 기준 이상이다", lines: tenLines, minScore: 10, wantScore: 10, want: true},
		{name: "12점은 기준 이상이다", lines: twelveLines, minScore: 10, wantScore: 12, want: true},
		{name: "기준을 13점으로 올리면 12점은 기준 아래다", lines: twelveLines, minScore: 13, wantScore: 12, want: false},
		{name: "기준을 9점으로 내리면 9점도 기준 이상이다", lines: nineLines, minScore: 9, wantScore: 9, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := params.Default()
			p.Crisis.EscalationMinScore = tt.minScore
			e := ov2Evaluate(t, tt.lines, 14, p)

			total, ok := e.Score.Score()
			require.True(t, ok, "n = 14라 점수가 난다")
			require.Equal(t, tt.wantScore, total)

			assert.Equal(t, crisis.State{ScoreElevated: tt.want}, e.GateState())
		})
	}
}

// 두 조건이 따로 켜지고 따로 꺼지는 흐름을 기준일을 옮겨 가며 본다.
//
// 1~14일째는 아무것도 관찰되지 않은 대화(평소 0), 15~21일째는 여덟 항목이 모두 관찰된 대화, 그 뒤로는 쉰다.
//
//	누적값: 8 − 0 − 0.5 = 7.5 → 2로 묶임. 15일째 2, 16일째 4(같으므로 아직 아니다), 17일째 6(감지), 18일째 8, 그 뒤 8에서 멈춘다.
//	        쉬는 동안에는 8과 감지가 그대로 남는다.
//	점수(15~21일째는 n = 14, 항목마다 관찰된 일수 = 기준일 − 14):
//	  16일째 2일 → 1점 × 8 = 8점    17일째 3일 → 8점    20일째 6일 → 8점    21일째 7일 → 2점 × 8 = 16점
//	  28일째: 창 15~28, n = 7(15~21일째), 7일 관찰 → 14일 → 3점 × 8 = 24점
//	  29일째: 창 16~29, n = 6 → 기록 부족, 점수 없음
//	  34일째: 창 21~34, n = 1 → 기록 부족
//	  35일째: 창 22~35, n = 0 → 창 안에 대화한 날이 없다
func TestOracleV2GateStateOverTime(t *testing.T) {
	lines := ov2Cat(ov2Rep(14, ov2Calm), ov2Rep(7, ov2All), ov2Rep(19, ov2Quiet))

	tests := []struct {
		name         string
		asOf         int
		wantN        int
		wantScore    int
		hasScore     bool
		wantDetected bool
		want         crisis.State
	}{
		{name: "16일째: 8점이고 누적값은 한계값과 같을 뿐이라 둘 다 거짓", asOf: 16, wantN: 14, wantScore: 8, hasScore: true},
		{
			name: "17일째: 8점이지만 누적값 6으로 변화 감지만 참", asOf: 17, wantN: 14, wantScore: 8, hasScore: true,
			wantDetected: true, want: crisis.State{ChangeDetected: true},
		},
		{
			name: "20일째: 아직 8점이라 변화 감지만 참", asOf: 20, wantN: 14, wantScore: 8, hasScore: true,
			wantDetected: true, want: crisis.State{ChangeDetected: true},
		},
		{
			name: "21일째: 16점이 되어 둘 다 참", asOf: 21, wantN: 14, wantScore: 16, hasScore: true,
			wantDetected: true, want: crisis.State{ScoreElevated: true, ChangeDetected: true},
		},
		{
			name: "28일째: 이레를 쉬었어도 n = 7이라 24점, 둘 다 참", asOf: 28, wantN: 7, wantScore: 24, hasScore: true,
			wantDetected: true, want: crisis.State{ScoreElevated: true, ChangeDetected: true},
		},
		{
			name: "29일째: 기록 부족이라 점수 쪽은 거짓, 변화 감지는 그대로 참", asOf: 29, wantN: 6,
			wantDetected: true, want: crisis.State{ChangeDetected: true},
		},
		{
			name: "34일째: 창 안에 대화한 날이 하루 남아 있어 변화 감지는 아직 참", asOf: 34, wantN: 1,
			wantDetected: true, want: crisis.State{ChangeDetected: true},
		},
		{
			name: "35일째: 창이 비면 누적값이 8로 남아 있어도 둘 다 거짓", asOf: 35, wantN: 0,
			wantDetected: true, want: crisis.State{},
		},
		{
			name: "40일째: 그 뒤로도 둘 다 거짓", asOf: 40, wantN: 0,
			wantDetected: true, want: crisis.State{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ov2Evaluate(t, lines[:tt.asOf], tt.asOf, params.Default())

			require.Equal(t, tt.wantN, e.Score.ConversationDays, "창 안의 대화한 날 수")
			total, ok := e.Score.Score()
			require.Equal(t, tt.hasScore, ok, "점수가 나는 날인지")
			if ok {
				require.Equal(t, tt.wantScore, total)
			}
			require.Equal(t, tt.wantDetected, e.Change.State.Detected, "누적값으로 본 변화 감지")

			assert.Equal(t, tt.want, e.GateState())
		})
	}

	t.Run("기준일 뒤의 기록을 함께 넘겨도 같은 값이다", func(t *testing.T) {
		e := ov2Evaluate(t, lines, 20, params.Default())
		assert.Equal(t, crisis.State{ChangeDetected: true}, e.GateState())
	})
}

// 40일 만에 돌아온 사용자. 돌아오기 전의 흔적으로는 말을 무겁게 듣지 않고, 돌아온 날의 기록이 생기면 그때의 누적값으로 다시 본다.
//
// 1~14일째는 아무것도 관찰되지 않은 대화(평소 0), 15~17일째는 두 항목이 관찰된 대화, 18~39일째는 쉼.
//
//	누적값: 2 − 0 − 0.5 = 1.5씩 → 1.5, 3.0, 4.5(감지). 쉬는 동안 4.5가 남는다.
//	30일째: 창 17~30에 17일째 하루 → 변화 감지 참.   31일째: 창 18~31이 비었다 → 거짓.
//	40일째, 대화 도중(그날의 기록이 아직 없다): 어제(39일째)를 기준일로 본다. 창 26~39가 비었다 → 둘 다 거짓
//	40일째의 기록이 "두 항목 관찰"로 생긴 뒤: 누적값 6.0, 창 27~40에 40일째 하루 → 변화 감지 참. n = 1이라 점수 쪽은 거짓
//	40일째의 기록이 "아무것도 관찰되지 않음"으로 생긴 뒤: 4.5 − 0.5 = 4.0, 한계값과 같으므로 감지가 풀린다 → 둘 다 거짓
func TestOracleV2GateStateAfterLongAbsence(t *testing.T) {
	absence := ov2Cat(ov2Rep(14, ov2Calm), ov2Rep(3, ov2Two), ov2Rep(22, ov2Quiet))
	require.Len(t, absence, 39)

	t.Run("30일째까지는 변화 감지가 전해지고 31일째부터는 꺼진 것으로 전해진다", func(t *testing.T) {
		day30 := ov2Evaluate(t, absence, 30, params.Default())
		require.Equal(t, 1, day30.Score.ConversationDays)
		require.True(t, day30.Change.State.Detected)
		assert.Equal(t, crisis.State{ChangeDetected: true}, day30.GateState())

		day31 := ov2Evaluate(t, absence, 31, params.Default())
		require.Zero(t, day31.Score.ConversationDays)
		require.True(t, day31.Change.State.Detected, "누적값은 쉬는 동안 줄지 않는다")
		assert.Equal(t, crisis.State{}, day31.GateState())
	})

	t.Run("돌아온 날 대화 도중에는 어제를 기준일로 보아 둘 다 거짓이다", func(t *testing.T) {
		live, err := assess.EvaluateLive(ov2Diary(t, absence), ov2Nth(t, 40), params.Default())
		require.NoError(t, err)

		assert.Equal(t, ov2Nth(t, 39), live.AsOf)
		require.True(t, live.Change.State.Detected)
		assert.Equal(t, crisis.State{}, live.GateState())
	})

	t.Run("돌아온 날의 기록에 두 항목이 관찰됐으면 변화 감지가 다시 전해진다", func(t *testing.T) {
		days := ov2Diary(t, ov2Cat(absence, []string{ov2Two}))
		live, err := assess.EvaluateLive(days, ov2Nth(t, 40), params.Default())
		require.NoError(t, err)

		assert.Equal(t, ov2Nth(t, 40), live.AsOf, "오늘의 기록이 있으면 오늘이 기준일이다")
		require.Equal(t, 1, live.Score.ConversationDays)
		_, ok := live.Score.Score()
		require.False(t, ok, "n = 1이라 점수가 없다")
		assert.InDelta(t, 6.0, live.Change.State.S, 1e-9)
		assert.Equal(t, crisis.State{ChangeDetected: true}, live.GateState())
	})

	t.Run("돌아온 날의 기록에 아무것도 관찰되지 않았으면 감지가 풀려 둘 다 거짓이다", func(t *testing.T) {
		e := ov2Evaluate(t, ov2Cat(absence, []string{ov2Calm}), 40, params.Default())

		assert.InDelta(t, 4.0, e.Change.State.S, 1e-9)
		require.False(t, e.Change.State.Detected, "4.0은 한계값과 같을 뿐 넘지 않는다")
		assert.Equal(t, crisis.State{}, e.GateState())
	})
}

// 이틀에 한 번 대화하는 사람. 오늘의 기록이 아직 없는데 오늘을 기준일로 삼으면 그 순간에만 기록 부족이 되어 점수 쪽이 꺼진다.
//
// 1, 3, 5, 7, 9, 11, 13일째에 다섯 항목이 관찰된 대화. 오늘은 15일째다.
//
//	어제(14일째) 기준: 창 1~14, n = 7, 다섯 항목이 7일 관찰 → 14일 → 3점 × 5 = 15점 → 점수 쪽 참
//	오늘(15일째) 기준, 오늘의 기록 없음: 창 2~15, n = 6(3~13일째) → 기록 부족 → 점수 쪽 거짓
//	오늘의 기록이 생긴 뒤: 창 2~15, n = 7(3~15일째) → 15점 → 점수 쪽 참.
//	  평소 5(첫 14일의 7일, 합 35), 15일째 5개: 5 − 5 − 0.5 < 0 → 누적값 0, 변화 감지는 거짓
func TestOracleV2GateStateLiveForEveryOtherDayTalker(t *testing.T) {
	lines := make([]string, 13)
	for i := 0; i < len(lines); i += 2 {
		lines[i] = ov2Five
	}
	days := ov2Diary(t, lines)
	require.Len(t, days, 7)
	today := ov2Nth(t, 15)

	t.Run("대화 도중에는 어제를 기준일로 보아 점수 쪽이 참이다", func(t *testing.T) {
		live, err := assess.EvaluateLive(days, today, params.Default())
		require.NoError(t, err)

		assert.Equal(t, ov2Nth(t, 14), live.AsOf)
		total, ok := live.Score.Score()
		require.True(t, ok)
		require.Equal(t, 15, total)
		assert.Equal(t, crisis.State{ScoreElevated: true}, live.GateState())
	})

	t.Run("기록 없는 오늘을 기준일로 넣으면 기록 부족이라 점수 쪽이 거짓이다", func(t *testing.T) {
		e, err := assess.Evaluate(days, today, params.Default())
		require.NoError(t, err)

		require.Equal(t, 6, e.Score.ConversationDays)
		_, ok := e.Score.Score()
		require.False(t, ok)
		assert.Equal(t, crisis.State{}, e.GateState())
	})

	t.Run("오늘의 기록이 생기면 오늘을 기준일로 보아 점수 쪽이 참이다", func(t *testing.T) {
		withToday := ov2Diary(t, ov2Cat(lines, []string{ov2Quiet, ov2Five}))
		live, err := assess.EvaluateLive(withToday, today, params.Default())
		require.NoError(t, err)

		assert.Equal(t, today, live.AsOf)
		require.Equal(t, 7, live.Score.ConversationDays)
		total, ok := live.Score.Score()
		require.True(t, ok)
		require.Equal(t, 15, total)
		assert.Equal(t, crisis.State{ScoreElevated: true}, live.GateState())
	})
}

// 평가에서 읽은 상태를 그대로 관문에 넘겼을 때의 최종 단계까지 따라간다.
func TestOracleV2GateStateFeedsTheGate(t *testing.T) {
	now := ov2Nth(t, 41).UTCMidnight()
	decide := func(t *testing.T, state crisis.State, rule crisis.RuleResult, ai crisis.AIResult) crisis.Decision {
		t.Helper()
		got, err := crisis.Decide(crisis.Input{
			Rule: rule, AI: ai, State: state, ConversationID: "conv-today", Now: now,
		}, params.Default().Crisis)
		require.NoError(t, err)
		return got
	}
	ambiguous := crisis.RuleResult{Stage: crisis.StageCheck, Matched: true}
	idiom := crisis.RuleResult{Stage: crisis.StageNone, Matched: true}

	absence := ov2Cat(ov2Rep(14, ov2Calm), ov2Rep(3, ov2Two), ov2Rep(22, ov2Quiet))
	bad := ov2Cat(ov2Rep(14, ov2Calm), ov2Rep(7, ov2All))

	// 누적값 4.5가 남아 있지만 창 26~39가 비었다 → 상태는 둘 다 거짓 → 1단계인 말은 1단계 그대로
	t.Run("40일 만에 돌아온 사용자의 첫 애매한 말은 1단계 그대로다", func(t *testing.T) {
		live, err := assess.EvaluateLive(ov2Diary(t, absence), ov2Nth(t, 40), params.Default())
		require.NoError(t, err)

		got := decide(t, live.GateState(), ambiguous, crisis.AIAnswered(crisis.StageCheck))
		assert.Equal(t, crisis.StageCheck, got.Stage)
		assert.Empty(t, got.Adjustments)
	})

	// 30일째: 창 17~30에 대화한 날이 하루 있고 누적값 4.5 → 변화 감지 참 → 1단계인 말은 2단계
	t.Run("쉰 지 13일째에는 같은 말이 변화 감지 때문에 2단계가 된다", func(t *testing.T) {
		e := ov2Evaluate(t, absence, 30, params.Default())

		got := decide(t, e.GateState(), ambiguous, crisis.AIAnswered(crisis.StageCheck))
		assert.Equal(t, crisis.StageRespond, got.Stage)
		assert.Equal(t, []crisis.Adjustment{crisis.AdjustBadStatePlusOne}, got.Adjustments)
	})

	// 21일째: 16점, 누적값 8 → 둘 다 참.
	//   AI 판별이 멈춘 동안 사전에 걸린 관용 표현: 바닥으로 1단계, 상태가 나빠도 거기서 멈춘다
	//   규칙이 스스로 1단계로 본 말: 상태 때문에 2단계
	t.Run("둘 다 참인 날에도 바닥만으로 1단계가 된 말은 1단계이고 규칙이 1단계로 본 말은 2단계다", func(t *testing.T) {
		e := ov2Evaluate(t, bad, 21, params.Default())
		require.Equal(t, crisis.State{ScoreElevated: true, ChangeDetected: true}, e.GateState())

		floored := decide(t, e.GateState(), idiom, crisis.AIFailed())
		assert.Equal(t, crisis.StageCheck, floored.Stage)
		assert.Equal(t, []crisis.Adjustment{crisis.AdjustAIFailedFloor}, floored.Adjustments)

		seen := decide(t, e.GateState(), ambiguous, crisis.AIFailed())
		assert.Equal(t, crisis.StageRespond, seen.Stage)
		assert.Equal(t, []crisis.Adjustment{crisis.AdjustBadStatePlusOne}, seen.Adjustments)
	})
}

// 평가가 오류로 끝나 빈 값만 남았어도 관문은 평소대로 돌아야 한다. 빈 평가에서 읽은 상태는 둘 다 거짓이다.
func TestOracleV2GateStateOfEmptyEvaluation(t *testing.T) {
	assert.Equal(t, crisis.State{}, assess.Evaluation{}.GateState())
}
