package crisis

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// 기간의 경계는 구현의 상수를 빌리지 않고 시간으로 직접 적는다. 14일은 336시간, 7일은 168시간이다.
const (
	testDay         = 24 * time.Hour
	repeatWindow    = 336 * time.Hour
	sensitiveWindow = 168 * time.Hour
)

// testNow는 시험에서 쓰는 판정 시각이다. 시계를 읽지 않고 고정된 시각을 쓴다.
func testNow() time.Time {
	return time.Date(2026, time.October, 17, 21, 30, 0, 0, time.UTC)
}

// thisConversation은 지금 판정하는 발화가 속한 대화다.
const thisConversation = "conversation-now"

// before는 판정 시각보다 d만큼 앞선 지난 판정을 만든다. d가 음수면 판정 시각보다 뒤의 판정이다.
// 판정마다 다른 대화에서 나온 것으로 둔다. 앞선 시간이 다르면 대화도 다르다. 같은 대화로 묶으려면 during으로 감싼다.
func before(d time.Duration, stage Stage) Event {
	return Event{At: testNow().Add(-d), Stage: stage, ConversationID: "conversation-" + d.String() + "-ago"}
}

// during은 지난 판정을 정해 준 대화의 것으로 바꾼다.
func during(conversation string, e Event) Event {
	e.ConversationID = conversation
	return e
}

// judged는 규칙과 AI 판별이 같은 단계로 본 발화의 입력을 만든다.
func judged(stage Stage) Input {
	return Input{
		Rule:           RuleResult{Stage: stage, Matched: stage >= StageCheck},
		AI:             AIAnswered(stage),
		ConversationID: thisConversation,
		Now:            testNow(),
	}
}

func mustDecide(t *testing.T, in Input) Decision {
	t.Helper()

	got, err := Decide(in, params.Default().Crisis)
	require.NoError(t, err)
	return got
}

func assertAdjustments(t *testing.T, want []Adjustment, got Decision) {
	t.Helper()

	// 바꾼 규칙이 없을 때도 nil이 아니라 빈 슬라이스여야 저장할 때 "값 없음"이 되지 않는다.
	require.NotNil(t, got.Adjustments)
	if len(want) == 0 {
		assert.Empty(t, got.Adjustments)
		return
	}
	assert.Equal(t, want, got.Adjustments)
}

func TestDecide_HigherLayerWins(t *testing.T) {
	tests := []struct {
		name   string
		rule   Stage
		ai     Stage
		want   Stage
		wantBy DetectedBy
	}{
		{"규칙 0, AI 0이면 해당 없음이고 어느 쪽도 잡지 않았다", StageNone, StageNone, StageNone, DetectedByNone},
		{"규칙 0, AI 1이면 확인이고 AI가 잡았다", StageNone, StageCheck, StageCheck, DetectedByAI},
		{"규칙 0, AI 2면 대응이고 AI가 잡았다", StageNone, StageRespond, StageRespond, DetectedByAI},
		{"규칙 0, AI 3이면 긴급이고 AI가 잡았다", StageNone, StageUrgent, StageUrgent, DetectedByAI},

		{"규칙 1, AI 0이면 확인이고 규칙이 잡았다", StageCheck, StageNone, StageCheck, DetectedByRule},
		{"규칙 1, AI 1이면 확인이고 둘 다 잡았다", StageCheck, StageCheck, StageCheck, DetectedByBoth},
		{"규칙 1, AI 2면 높은 쪽인 대응이다", StageCheck, StageRespond, StageRespond, DetectedByBoth},
		{"규칙 1, AI 3이면 높은 쪽인 긴급이다", StageCheck, StageUrgent, StageUrgent, DetectedByBoth},

		{"규칙 2, AI 0이면 대응이고 규칙이 잡았다", StageRespond, StageNone, StageRespond, DetectedByRule},
		{"규칙 2, AI 1이면 높은 쪽인 대응이다", StageRespond, StageCheck, StageRespond, DetectedByBoth},
		{"규칙 2, AI 2면 대응이다", StageRespond, StageRespond, StageRespond, DetectedByBoth},
		{"규칙 2, AI 3이면 높은 쪽인 긴급이다", StageRespond, StageUrgent, StageUrgent, DetectedByBoth},

		{"규칙 3, AI 0이면 긴급이고 규칙이 잡았다", StageUrgent, StageNone, StageUrgent, DetectedByRule},
		{"규칙 3, AI 1이면 높은 쪽인 긴급이다", StageUrgent, StageCheck, StageUrgent, DetectedByBoth},
		{"규칙 3, AI 2면 높은 쪽인 긴급이다", StageUrgent, StageRespond, StageUrgent, DetectedByBoth},
		{"규칙 3, AI 3이면 긴급이다", StageUrgent, StageUrgent, StageUrgent, DetectedByBoth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustDecide(t, Input{
				Rule:           RuleResult{Stage: tt.rule, Matched: tt.rule >= StageCheck},
				AI:             AIAnswered(tt.ai),
				ConversationID: thisConversation,
				Now:            testNow(),
			})

			assert.Equal(t, tt.want, got.Stage)
			assert.Equal(t, tt.wantBy, got.DetectedBy)
			// 높은 쪽을 따르는 것은 합치는 일이지 단계를 바꾼 규칙이 아니다.
			assertAdjustments(t, nil, got)
		})
	}
}

func TestDecide_AIFailure(t *testing.T) {
	tests := []struct {
		name    string
		rule    RuleResult
		ai      AIResult
		want    Stage
		wantBy  DetectedBy
		wantAdj []Adjustment
	}{
		{
			name: "AI 판별이 실패했고, 규칙에 걸렸지만 규칙은 해당 없음으로 본 말은 확인으로 올린다",
			rule: RuleResult{Stage: StageNone, Matched: true}, ai: AIFailed(),
			want: StageCheck, wantBy: DetectedByRule, wantAdj: []Adjustment{AdjustAIFailedFloor},
		},
		{
			name: "AI 판별이 실패했고 규칙에도 걸리지 않은 말은 해당 없음이다",
			rule: RuleResult{Stage: StageNone, Matched: false}, ai: AIFailed(),
			want: StageNone, wantBy: DetectedByNone,
		},
		{
			name: "AI 판별이 실패했고 규칙이 확인으로 봤으면 그대로 확인이다. 바닥이 바꾼 것은 없다",
			rule: RuleResult{Stage: StageCheck, Matched: true}, ai: AIFailed(),
			want: StageCheck, wantBy: DetectedByRule,
		},
		{
			name: "AI 판별이 실패했고 규칙이 대응으로 봤으면 그대로 대응이다",
			rule: RuleResult{Stage: StageRespond, Matched: true}, ai: AIFailed(),
			want: StageRespond, wantBy: DetectedByRule,
		},
		{
			name: "AI 판별이 실패했고 규칙이 긴급으로 봤으면 그대로 긴급이다",
			rule: RuleResult{Stage: StageUrgent, Matched: true}, ai: AIFailed(),
			want: StageUrgent, wantBy: DetectedByRule,
		},
		{
			name: "AI 판별이 해당 없음이라고 답했으면 규칙에 걸린 말이어도 올리지 않는다",
			rule: RuleResult{Stage: StageNone, Matched: true}, ai: AIAnswered(StageNone),
			want: StageNone, wantBy: DetectedByNone,
		},
		{
			name: "규칙에 걸린 말을 AI 판별만 무겁게 봤으면 AI가 잡은 것이다",
			rule: RuleResult{Stage: StageNone, Matched: true}, ai: AIAnswered(StageRespond),
			want: StageRespond, wantBy: DetectedByAI,
		},
		{
			name: "AI 판별의 빈 값은 실패로 읽힌다",
			rule: RuleResult{Stage: StageNone, Matched: true}, ai: AIResult{},
			want: StageCheck, wantBy: DetectedByRule, wantAdj: []Adjustment{AdjustAIFailedFloor},
		},
		{
			name: "규칙이 확인 이상으로 봤으면 걸림 표시가 빠져 있어도 규칙이 잡은 것이다",
			rule: RuleResult{Stage: StageCheck, Matched: false}, ai: AIFailed(),
			want: StageCheck, wantBy: DetectedByRule,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustDecide(t, Input{Rule: tt.rule, AI: tt.ai, ConversationID: thisConversation, Now: testNow()})

			assert.Equal(t, tt.want, got.Stage)
			assert.Equal(t, tt.wantBy, got.DetectedBy)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}

	t.Run("AIFailed는 빈 값과 같고 AIAnswered는 답한 것으로 표시한다", func(t *testing.T) {
		assert.Equal(t, AIResult{}, AIFailed())
		assert.Equal(t, AIResult{Answered: true, Stage: StageRespond}, AIAnswered(StageRespond))
	})
}

func TestDecide_BadStatePlusOne(t *testing.T) {
	score := State{ScoreElevated: true}
	change := State{ChangeDetected: true}
	both := State{ScoreElevated: true, ChangeDetected: true}
	plusOne := []Adjustment{AdjustBadStatePlusOne}

	tests := []struct {
		name    string
		stage   Stage
		state   State
		want    Stage
		wantAdj []Adjustment
	}{
		{"해당 없음은 추정 점수가 기준 이상이어도 올리지 않는다", StageNone, score, StageNone, nil},
		{"해당 없음은 변화 감지 상태여도 올리지 않는다", StageNone, change, StageNone, nil},
		{"해당 없음은 두 조건이 다 맞아도 올리지 않는다", StageNone, both, StageNone, nil},

		{"확인은 추정 점수가 기준 이상이면 대응이 된다", StageCheck, score, StageRespond, plusOne},
		{"확인은 변화 감지 상태면 대응이 된다", StageCheck, change, StageRespond, plusOne},
		{"두 조건이 다 맞아도 한 단계만 오른다", StageCheck, both, StageRespond, plusOne},

		{"대응은 추정 점수가 기준 이상이면 긴급이 된다", StageRespond, score, StageUrgent, plusOne},
		{"대응은 변화 감지 상태면 긴급이 된다", StageRespond, change, StageUrgent, plusOne},
		{"대응도 두 조건이 다 맞을 때 한 단계만 오른다", StageRespond, both, StageUrgent, plusOne},

		{"긴급은 더 오를 곳이 없고, 바꾼 것이 없으니 규칙으로 적지 않는다", StageUrgent, both, StageUrgent, nil},

		{"상태가 나쁘지 않으면 확인은 확인이다", StageCheck, State{}, StageCheck, nil},
		{"상태가 나쁘지 않으면 대응은 대응이다", StageRespond, State{}, StageRespond, nil},
		{"상태가 나쁘지 않으면 긴급은 긴급이다", StageUrgent, State{}, StageUrgent, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := judged(tt.stage)
			in.State = tt.state

			got := mustDecide(t, in)

			assert.Equal(t, tt.want, got.Stage)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}

	for name, state := range map[string]State{"추정 점수가 기준 이상": score, "변화 감지 상태": change, "둘 다": both} {
		t.Run("AI 판별 실패로만 확인이 된 말은 상태가 나빠도 확인에 머문다: "+name, func(t *testing.T) {
			// 규칙이 관용 표현으로 본 말이다. 다시 봐줄 눈이 없어서 되묻는 데까지만 올린 것이라 거기서 멈춘다.
			got := mustDecide(t, Input{
				Rule:           RuleResult{Stage: StageNone, Matched: true},
				AI:             AIFailed(),
				State:          state,
				ConversationID: thisConversation,
				Now:            testNow(),
			})

			assert.Equal(t, StageCheck, got.Stage)
			assert.Equal(t, DetectedByRule, got.DetectedBy)
			assertAdjustments(t, []Adjustment{AdjustAIFailedFloor}, got)
		})
	}

	t.Run("AI 판별이 실패했어도 규칙이 확인으로 본 말은 상태가 나쁘면 대응이 된다", func(t *testing.T) {
		// 바닥이 올린 말이 아니라 규칙이 스스로 확인으로 본 말이다.
		got := mustDecide(t, Input{
			Rule:           RuleResult{Stage: StageCheck, Matched: true},
			AI:             AIFailed(),
			State:          both,
			ConversationID: thisConversation,
			Now:            testNow(),
		})

		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustBadStatePlusOne}, got)
	})

	t.Run("AI 판별 실패로만 확인이 된 말도 되풀이를 보는 규칙은 그대로 받는다", func(t *testing.T) {
		got := mustDecide(t, Input{
			Rule:           RuleResult{Stage: StageNone, Matched: true},
			AI:             AIFailed(),
			State:          both,
			ConversationID: thisConversation,
			History:        []Event{before(2*testDay, StageRespond)},
			Now:            testNow(),
		})

		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustAIFailedFloor, AdjustSensitiveWindow}, got)
	})

	t.Run("AI 판별이 실패했어도 규칙에 걸리지 않은 말은 상태가 나빠도 해당 없음이다", func(t *testing.T) {
		got := mustDecide(t, Input{AI: AIFailed(), State: both, ConversationID: thisConversation, Now: testNow()})

		assert.Equal(t, StageNone, got.Stage)
		assert.Equal(t, DetectedByNone, got.DetectedBy)
		assertAdjustments(t, nil, got)
	})

	t.Run("단계를 올려도 어느 쪽이 잡았는지는 달라지지 않는다", func(t *testing.T) {
		got := mustDecide(t, Input{
			Rule:           RuleResult{Stage: StageNone},
			AI:             AIAnswered(StageRespond),
			State:          score,
			ConversationID: thisConversation,
			Now:            testNow(),
		})

		assert.Equal(t, StageUrgent, got.Stage)
		assert.Equal(t, DetectedByAI, got.DetectedBy)
	})
}

func TestDecide_RepeatAfterDirectAsk(t *testing.T) {
	tests := []struct {
		name    string
		stage   Stage
		asked   bool
		want    Stage
		wantAdj []Adjustment
	}{
		{"이 대화에서 아직 직접 묻지 않았으면 확인은 확인이다", StageCheck, false, StageCheck, nil},
		{"이미 직접 물었는데 확인 단계의 표현이 다시 나오면 대응이다", StageCheck, true, StageRespond, []Adjustment{AdjustRepeatAfterDirectAsk}},
		{"직접 물은 뒤의 답이 풀려서 해당 없음이면 그대로 평소 대화로 돌아간다", StageNone, true, StageNone, nil},
		{"이미 대응인 말은 이 규칙으로 더 오르지 않는다", StageRespond, true, StageRespond, nil},
		{"이미 긴급인 말은 그대로 긴급이다", StageUrgent, true, StageUrgent, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := judged(tt.stage)
			in.DirectAskDone = tt.asked

			got := mustDecide(t, in)

			assert.Equal(t, tt.want, got.Stage)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}
}

func TestDecide_RepeatedInWindow(t *testing.T) {
	repeated := []Adjustment{AdjustRepeatedInWindow}

	tests := []struct {
		name    string
		stage   Stage
		history []Event
		want    Stage
		wantAdj []Adjustment
	}{
		{
			name:  "지난 판정이 없으면 이번 대화가 첫 번째라 확인이다",
			stage: StageCheck, history: nil,
			want: StageCheck,
		},
		{
			name:  "지난 판정이 빈 목록이어도 같다",
			stage: StageCheck, history: []Event{},
			want: StageCheck,
		},
		{
			name:  "2주 안에 확인이 나온 대화가 하나 있었으면 이번이 두 번째라 확인이다",
			stage: StageCheck, history: []Event{before(3*testDay, StageCheck)},
			want: StageCheck,
		},
		{
			name:  "2주 안에 확인이 나온 대화가 둘 있었으면 이번이 세 번째라 대응이다",
			stage: StageCheck, history: []Event{before(10*testDay, StageCheck), before(3*testDay, StageCheck)},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "세 번을 채운 뒤의 네 번째도 대응이다",
			stage: StageCheck,
			history: []Event{
				before(12*testDay, StageCheck), before(9*testDay, StageCheck), before(8*testDay, StageCheck),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "다섯 번째도 대응이다",
			stage: StageCheck,
			history: []Event{
				before(12*testDay, StageCheck), before(9*testDay, StageCheck),
				before(8*testDay, StageCheck), before(time.Hour, StageCheck),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "두 번 가운데 하나가 2주보다 오래됐으면 세지 않는다",
			stage: StageCheck, history: []Event{before(15*testDay, StageCheck), before(3*testDay, StageCheck)},
			want: StageCheck,
		},
		{
			name:  "해당 없음이었던 판정은 아무리 많아도 세지 않는다",
			stage: StageCheck,
			history: []Event{
				before(time.Hour, StageNone), before(2*time.Hour, StageNone),
				before(3*time.Hour, StageNone), before(3*testDay, StageCheck),
			},
			want: StageCheck,
		},
		{
			name:  "대응이나 긴급으로 끝난 대화도 관문에 걸린 대화로 센다",
			stage: StageCheck,
			history: []Event{
				before(10*testDay, StageRespond), before(9*testDay, StageUrgent),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "같은 대화에서 나온 확인은 몇 번이든 대화 하나로 센다",
			stage: StageCheck,
			history: []Event{
				during("a", before(3*testDay, StageCheck)), during("a", before(3*testDay-time.Minute, StageCheck)),
				during("a", before(3*testDay-2*time.Minute, StageCheck)),
			},
			want: StageCheck,
		},
		{
			name:  "이번 대화의 앞선 확인은 이번 대화에 이미 들어 있어 따로 세지 않는다",
			stage: StageCheck,
			history: []Event{
				during(thisConversation, before(2*time.Minute, StageCheck)), during(thisConversation, before(time.Minute, StageCheck)),
				during("a", before(3*testDay, StageCheck)),
			},
			want: StageCheck,
		},
		{
			name:  "이번 대화의 앞선 확인이 있어도 다른 두 대화가 있으면 세 번째다",
			stage: StageCheck,
			history: []Event{
				during(thisConversation, before(time.Minute, StageCheck)),
				during("a", before(10*testDay, StageCheck)), during("b", before(3*testDay, StageCheck)),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "한 대화의 판정 가운데 확인이 하나라도 있으면 그 대화를 센다",
			stage: StageCheck,
			history: []Event{
				during("a", before(10*testDay, StageNone)), during("a", before(10*testDay-time.Minute, StageCheck)),
				during("b", before(3*testDay, StageCheck)), during("b", before(3*testDay-time.Minute, StageNone)),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "한 대화의 판정 가운데 기간 안의 것이 하나라도 있으면 그 대화를 센다",
			stage: StageCheck,
			history: []Event{
				during("a", before(repeatWindow+time.Minute, StageCheck)), during("a", before(repeatWindow-time.Minute, StageCheck)),
				during("b", before(3*testDay, StageCheck)),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "대화가 둘뿐이면 판정이 아무리 많아도 세 번째가 아니다",
			stage: StageCheck,
			history: []Event{
				during("a", before(8*testDay, StageCheck)), during("a", before(8*testDay-time.Minute, StageCheck)),
				during("a", before(8*testDay-2*time.Minute, StageCheck)), during("a", before(8*testDay-3*time.Minute, StageCheck)),
				during(thisConversation, before(3*time.Minute, StageCheck)), during(thisConversation, before(2*time.Minute, StageCheck)),
			},
			want: StageCheck,
		},
		{
			name:  "이번 말이 해당 없음이면 확인이 쌓여 있어도 올리지 않는다",
			stage: StageNone, history: []Event{before(10*testDay, StageCheck), before(3*testDay, StageCheck)},
			want: StageNone,
		},
		{
			name:  "이번 말이 이미 대응이면 이 규칙은 바꾸는 것이 없다",
			stage: StageRespond, history: []Event{before(10*testDay, StageCheck), before(3*testDay, StageCheck)},
			want: StageRespond,
		},

		{
			name:  "경계: 정확히 336시간 전의 확인은 창 안으로 센다",
			stage: StageCheck, history: []Event{before(repeatWindow, StageCheck), before(3*testDay, StageCheck)},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "경계: 336시간에서 1나노초만 더 지났어도 세지 않는다",
			stage: StageCheck, history: []Event{before(repeatWindow+time.Nanosecond, StageCheck), before(3*testDay, StageCheck)},
			want: StageCheck,
		},
		{
			name:  "경계: 336시간에서 1나노초 모자라면 센다",
			stage: StageCheck, history: []Event{before(repeatWindow-time.Nanosecond, StageCheck), before(3*testDay, StageCheck)},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "판정 시각과 같은 시각의 확인은 이미 일어난 일이라 센다",
			stage: StageCheck, history: []Event{during("a", before(0, StageCheck)), during("b", before(0, StageCheck))},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "판정 시각보다 뒤의 확인은 아직 일어나지 않은 일이라 세지 않는다",
			stage: StageCheck,
			history: []Event{
				before(-time.Nanosecond, StageCheck), before(-time.Hour, StageCheck), before(-3*testDay, StageCheck),
			},
			want: StageCheck,
		},
		{
			name:  "뒤의 판정이 섞여 있어도 앞의 판정만으로 센다",
			stage: StageCheck,
			history: []Event{
				before(-time.Hour, StageCheck), before(10*testDay, StageCheck), before(3*testDay, StageCheck),
			},
			want: StageRespond, wantAdj: repeated,
		},
		{
			name:  "시각 차이를 표현할 수 없을 만큼 오래된 판정도 넘치지 않고 창 밖으로 읽힌다",
			stage: StageCheck,
			history: []Event{
				{At: time.Date(1700, time.January, 1, 0, 0, 0, 0, time.UTC), Stage: StageCheck, ConversationID: "a"},
				{At: time.Date(1, time.January, 2, 0, 0, 0, 0, time.UTC), Stage: StageCheck, ConversationID: "b"},
			},
			want: StageCheck,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := judged(tt.stage)
			in.History = tt.history

			got := mustDecide(t, in)

			assert.Equal(t, tt.want, got.Stage)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}
}

func TestDecide_SensitiveWindow(t *testing.T) {
	sensitive := []Adjustment{AdjustSensitiveWindow}

	tests := []struct {
		name    string
		stage   Stage
		history []Event
		want    Stage
		wantAdj []Adjustment
	}{
		{
			name:  "사흘 전에 대응이 있었으면 확인 단계의 표현도 대응이다",
			stage: StageCheck, history: []Event{before(3*testDay, StageRespond)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "사흘 전에 긴급이 있었어도 같다",
			stage: StageCheck, history: []Event{before(3*testDay, StageUrgent)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "사흘 전에 있었던 것이 확인이면 민감한 기간이 아니다",
			stage: StageCheck, history: []Event{before(3*testDay, StageCheck)},
			want: StageCheck,
		},
		{
			name:  "여드레 전의 대응은 기간이 지났다",
			stage: StageCheck, history: []Event{before(8*testDay, StageRespond)},
			want: StageCheck,
		},
		{
			name:  "오래된 대응과 최근의 대응이 함께 있으면 최근 것 하나로 충분하다",
			stage: StageCheck, history: []Event{before(30*testDay, StageUrgent), before(time.Minute, StageRespond)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "민감한 기간이어도 해당 없음인 말은 올리지 않는다",
			stage: StageNone, history: []Event{before(time.Hour, StageUrgent)},
			want: StageNone,
		},
		{
			name:  "민감한 기간은 확인만 올린다. 대응인 말을 긴급으로 올리지 않는다",
			stage: StageRespond, history: []Event{before(time.Hour, StageUrgent)},
			want: StageRespond,
		},

		{
			name:  "경계: 정확히 168시간 전의 대응은 기간 안으로 본다",
			stage: StageCheck, history: []Event{before(sensitiveWindow, StageRespond)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "경계: 168시간에서 1나노초만 더 지났어도 기간 밖이다",
			stage: StageCheck, history: []Event{before(sensitiveWindow+time.Nanosecond, StageRespond)},
			want: StageCheck,
		},
		{
			name:  "경계: 168시간에서 1나노초 모자라면 기간 안이다",
			stage: StageCheck, history: []Event{before(sensitiveWindow-time.Nanosecond, StageRespond)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "판정 시각과 같은 시각의 대응은 이미 일어난 일이라 본다",
			stage: StageCheck, history: []Event{before(0, StageRespond)},
			want: StageRespond, wantAdj: sensitive,
		},
		{
			name:  "판정 시각보다 뒤의 대응은 아직 일어나지 않은 일이라 보지 않는다",
			stage: StageCheck,
			history: []Event{
				before(-time.Nanosecond, StageRespond), before(-time.Hour, StageUrgent),
			},
			want: StageCheck,
		},
		{
			name:  "7일과 14일 사이의 대응 하나는 민감한 기간도 지났고 세 번째 대화도 아니다",
			stage: StageCheck, history: []Event{before(10*testDay, StageRespond)},
			want: StageCheck,
		},
		{
			name:  "7일과 14일 사이에 대응으로 끝난 대화가 둘이면 민감한 기간은 지났어도 세 번째 대화라 대응이다",
			stage: StageCheck, history: []Event{before(10*testDay, StageRespond), before(9*testDay, StageRespond)},
			want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := judged(tt.stage)
			in.History = tt.history

			got := mustDecide(t, in)

			assert.Equal(t, tt.want, got.Stage)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}
}

func TestDecide_TunedParams(t *testing.T) {
	t.Run("횟수를 2로 줄이면 두 번째 확인이 대응이다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatCount = 2
		in := judged(StageCheck)
		in.History = []Event{before(3*testDay, StageCheck)}

		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustRepeatedInWindow}, got)
	})

	t.Run("쌓임을 보는 기간을 3일로 줄이면 나흘 전의 확인은 세지 않는다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatWindowDays = 3
		in := judged(StageCheck)
		in.History = []Event{before(4*testDay, StageCheck), before(2*testDay, StageCheck)}

		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageCheck, got.Stage)
	})

	t.Run("쌓임을 보는 기간을 3일로 줄여도 정확히 72시간 전의 확인은 센다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatWindowDays = 3
		in := judged(StageCheck)
		in.History = []Event{before(72*time.Hour, StageCheck), before(2*testDay, StageCheck)}

		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageRespond, got.Stage)
	})

	t.Run("민감한 기간을 하루로 줄이면 이틀 전의 대응은 보지 않는다", func(t *testing.T) {
		p := params.Default().Crisis
		p.SensitiveWindowDays = 1
		in := judged(StageCheck)
		in.History = []Event{before(2*testDay, StageRespond)}

		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageCheck, got.Stage)
	})

	t.Run("민감한 기간을 30일로 늘리면 3주 전의 대응도 본다", func(t *testing.T) {
		p := params.Default().Crisis
		p.SensitiveWindowDays = 30
		in := judged(StageCheck)
		in.History = []Event{before(21*testDay, StageRespond)}

		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustSensitiveWindow}, got)
	})

	t.Run("기간이 시간으로 옮길 수 없을 만큼 길어도 창이 사라지지 않는다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatWindowDays = math.MaxInt
		p.SensitiveWindowDays = math.MaxInt
		centuryAgo := time.Date(1926, time.October, 17, 21, 30, 0, 0, time.UTC)

		in := judged(StageCheck)
		in.History = []Event{{At: centuryAgo, Stage: StageRespond, ConversationID: "a"}}
		got, err := Decide(in, p)

		require.NoError(t, err)
		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustSensitiveWindow}, got)
	})
}

// 규칙의 순서가 만드는 성질을 본다.
// 여러 뜻으로 읽히는 표현은 어떤 조건이 겹쳐도 대응까지만 오르고,
// 직접적인 표현은 상태가 나쁠 때에만 긴급이 된다.
func TestDecide_Ordering(t *testing.T) {
	bad := State{ScoreElevated: true, ChangeDetected: true}
	piledUp := []Event{
		before(10*testDay, StageCheck), before(5*testDay, StageCheck), before(4*testDay, StageCheck),
		before(2*testDay, StageRespond), before(time.Hour, StageUrgent),
	}

	tests := []struct {
		name    string
		in      Input
		want    Stage
		wantAdj []Adjustment
	}{
		{
			name: "애매한 표현은 모든 조건이 겹쳐도 대응까지다. 상태 규칙이 먼저 올려서 뒤의 규칙은 볼 것이 없다",
			in: Input{
				Rule: RuleResult{Stage: StageCheck, Matched: true}, AI: AIAnswered(StageCheck),
				State: bad, DirectAskDone: true, History: piledUp,
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustBadStatePlusOne},
		},
		{
			name: "상태가 나쁘지 않으면 직접 물은 뒤라는 것이 먼저 맞아 그 규칙만 남는다",
			in: Input{
				Rule: RuleResult{Stage: StageCheck, Matched: true}, AI: AIAnswered(StageCheck),
				DirectAskDone: true, History: piledUp,
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustRepeatAfterDirectAsk},
		},
		{
			name: "직접 묻지 않았으면 쌓임이 민감한 기간보다 먼저 맞는다",
			in: Input{
				Rule: RuleResult{Stage: StageCheck, Matched: true}, AI: AIAnswered(StageCheck),
				History: piledUp,
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
		},
		{
			name: "쌓인 것이 없으면 민감한 기간이 올린다",
			in: Input{
				Rule: RuleResult{Stage: StageCheck, Matched: true}, AI: AIAnswered(StageCheck),
				History: []Event{before(2*testDay, StageRespond)},
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustSensitiveWindow},
		},
		{
			name: "AI 판별 실패로만 확인이 된 말은 상태 규칙이 건너뛰므로, 모든 조건이 겹치면 직접 물은 뒤라는 것이 올린다",
			in: Input{
				Rule: RuleResult{Stage: StageNone, Matched: true}, AI: AIFailed(),
				State: bad, DirectAskDone: true, History: piledUp,
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustAIFailedFloor, AdjustRepeatAfterDirectAsk},
		},
		{
			name: "AI 판별 실패로만 확인이 된 말은 상태가 나빠도 되풀이가 없으면 확인이다",
			in: Input{
				Rule: RuleResult{Stage: StageNone, Matched: true}, AI: AIFailed(),
				State: bad,
			},
			want: StageCheck, wantAdj: []Adjustment{AdjustAIFailedFloor},
		},
		{
			name: "AI 판별 실패로 확인이 된 말은 상태가 괜찮으면 뒤의 규칙으로 대응이 된다",
			in: Input{
				Rule: RuleResult{Stage: StageNone, Matched: true}, AI: AIFailed(),
				DirectAskDone: true, History: piledUp,
			},
			want: StageRespond, wantAdj: []Adjustment{AdjustAIFailedFloor, AdjustRepeatAfterDirectAsk},
		},
		{
			name: "직접적인 표현은 상태가 괜찮으면 직접 물은 뒤든, 쌓였든, 민감한 기간이든 대응에 머문다",
			in: Input{
				Rule: RuleResult{Stage: StageRespond, Matched: true}, AI: AIAnswered(StageRespond),
				DirectAskDone: true, History: piledUp,
			},
			want: StageRespond,
		},
		{
			name: "직접적인 표현이 긴급이 되는 길은 상태가 나쁠 때의 한 단계뿐이다",
			in: Input{
				Rule: RuleResult{Stage: StageRespond, Matched: true}, AI: AIAnswered(StageRespond),
				State: bad, DirectAskDone: true, History: piledUp,
			},
			want: StageUrgent, wantAdj: []Adjustment{AdjustBadStatePlusOne},
		},
		{
			name: "관용 표현은 모든 조건이 겹쳐도 해당 없음이다",
			in: Input{
				Rule: RuleResult{Stage: StageNone, Matched: true}, AI: AIAnswered(StageNone),
				State: bad, DirectAskDone: true, History: piledUp,
			},
			want: StageNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Now = testNow()
			tt.in.ConversationID = thisConversation

			got := mustDecide(t, tt.in)

			assert.Equal(t, tt.want, got.Stage)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}
}

// 입력의 모든 조합에서 지켜져야 하는 성질이다. 표에 적지 못한 조합에서 순서가 어긋나지 않는지 본다.
func TestDecide_Invariants(t *testing.T) {
	histories := []struct {
		name   string
		events []Event
	}{
		{"지난 판정 없음", nil},
		{"확인이 나온 대화 하나", []Event{before(3*testDay, StageCheck)}},
		{"확인이 나온 대화 둘", []Event{before(10*testDay, StageCheck), before(3*testDay, StageCheck)}},
		{"한 대화에서 확인 두 번과 이번 대화의 앞선 확인", []Event{
			during("a", before(3*testDay, StageCheck)), during("a", before(3*testDay-time.Minute, StageCheck)),
			during(thisConversation, before(time.Minute, StageCheck)),
		}},
		{"최근의 대응", []Event{before(2*testDay, StageRespond)}},
		{"확인 두 번과 최근의 긴급", []Event{before(10*testDay, StageCheck), before(3*testDay, StageCheck), before(time.Hour, StageUrgent)}},
		{"모두 기간 밖이거나 뒤", []Event{before(20*testDay, StageCheck), before(15*testDay, StageUrgent), before(-time.Hour, StageUrgent)}},
	}
	ais := []AIResult{AIFailed(), AIAnswered(StageNone), AIAnswered(StageCheck), AIAnswered(StageRespond), AIAnswered(StageUrgent)}
	stages := []Stage{StageNone, StageCheck, StageRespond, StageUrgent}
	flags := []bool{false, true}

	count := 0
	for _, ruleStage := range stages {
		for _, matched := range flags {
			for _, ai := range ais {
				for _, scoreElevated := range flags {
					for _, changeDetected := range flags {
						for _, asked := range flags {
							for _, h := range histories {
								in := Input{
									Rule:           RuleResult{Stage: ruleStage, Matched: matched},
									AI:             ai,
									State:          State{ScoreElevated: scoreElevated, ChangeDetected: changeDetected},
									DirectAskDone:  asked,
									ConversationID: thisConversation,
									History:        h.events,
									Now:            testNow(),
								}
								checkInvariants(t, in, h.name)
								count++
							}
						}
					}
				}
			}
		}
	}
	assert.Equal(t, 4*2*5*2*2*2*7, count)
}

func checkInvariants(t *testing.T, in Input, historyName string) {
	t.Helper()

	got, err := Decide(in, params.Default().Crisis)
	require.NoError(t, err)

	label := []any{"규칙 %v(걸림 %v), AI %+v, 상태 %+v, 직접 물음 %v, %s", in.Rule.Stage, in.Rule.Matched, in.AI, in.State, in.DirectAskDone, historyName}

	// 두 겹 가운데 높은 쪽
	higher := in.Rule.Stage
	if in.AI.Answered && in.AI.Stage > higher {
		higher = in.AI.Stage
	}
	// 단계를 올리는 규칙들이 보기 시작하는 단계 (AI 판별 실패 때의 바닥까지 거친 뒤)
	start := higher
	if !in.AI.Answered && in.Rule.Matched && start == StageNone {
		start = StageCheck
	}

	assert.True(t, got.Stage.Valid(), label...)
	assert.GreaterOrEqual(t, got.Stage, higher, append([]any{"단계를 내리지 않는다: "}, label...)...)

	// 규칙 하나는 정확히 한 단계를 올린다. 그래서 오른 만큼이 바꾼 규칙의 수다.
	assert.Len(t, got.Adjustments, int(got.Stage-higher), label...)
	assert.LessOrEqual(t, len(got.Adjustments), 2, label...)
	assert.True(t, slices.IsSorted(got.Adjustments), label...)
	assert.Len(t, slices.Compact(slices.Clone(got.Adjustments)), len(got.Adjustments), label...)

	// 해당 없음일 때만 "어느 쪽도 잡지 않음"이다.
	assert.Equal(t, got.Stage == StageNone, got.DetectedBy == DetectedByNone, label...)

	if start == StageNone {
		assert.Equal(t, StageNone, got.Stage, label...)
	}
	if start <= StageCheck {
		assert.LessOrEqual(t, got.Stage, StageRespond, label...)
	}
	if got.Stage == StageUrgent {
		viaBadState := start == StageRespond && in.State.Bad()
		assert.True(t, start == StageUrgent || viaBadState, label...)
	}
	if !in.State.Bad() {
		assert.NotContains(t, got.Adjustments, AdjustBadStatePlusOne, label...)
	}
	// AI 판별 실패 때의 바닥만으로 확인이 된 말은 상태가 나빠도 그 규칙으로는 오르지 않는다.
	if higher == StageNone && start == StageCheck {
		assert.NotContains(t, got.Adjustments, AdjustBadStatePlusOne, label...)
	}
	if in.AI.Answered {
		assert.NotContains(t, got.Adjustments, AdjustAIFailedFloor, label...)
	}
}

// 규칙에 적힌 예시를 대화의 흐름대로 따라간다. 각 판정은 다음 판정의 지난 기록이 된다.
func TestDecide_Flows(t *testing.T) {
	type step struct {
		name    string
		conv    string        // 그 발화가 속한 대화
		after   time.Duration // 첫 발화로부터 흐른 시간
		stage   Stage         // 두 겹이 본 단계
		asked   bool
		bad     bool // 그때 추정 점수가 기준 이상이었는지
		want    Stage
		wantAdj []Adjustment
	}

	run := func(t *testing.T, steps []step) []Event {
		t.Helper()

		start := testNow()
		var history []Event
		for _, s := range steps {
			in := judged(s.stage)
			in.ConversationID = s.conv
			in.Now = start.Add(s.after)
			in.DirectAskDone = s.asked
			in.State = State{ScoreElevated: s.bad}
			in.History = slices.Clone(history)

			got := mustDecide(t, in)

			assert.Equal(t, s.want, got.Stage, s.name)
			assertAdjustments(t, s.wantAdj, got)
			history = append(history, Event{At: in.Now, Stage: got.Stage, ConversationID: s.conv})
		}
		return history
	}

	t.Run("확인은 두 걸음이다: 되묻고, 직접 묻고, 그 뒤에도 애매하면 대응이다", func(t *testing.T) {
		run(t, []step{
			{name: "막연한 표현이 처음 나오면 확인이다. 먼저 되묻는다", conv: "a", after: 0, stage: StageCheck, want: StageCheck},
			{name: "되물은 뒤에도 애매하면 아직 확인이다. 이때 직접 묻는다", conv: "a", after: time.Minute, stage: StageCheck, want: StageCheck},
			{
				name: "직접 물은 뒤에도 확인 단계의 표현이 나오면 대응이다", conv: "a", after: 2 * time.Minute, stage: StageCheck, asked: true,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatAfterDirectAsk},
			},
		})
	})

	t.Run("직접 물은 뒤의 답으로 뜻이 풀리면 평소 대화로 돌아간다", func(t *testing.T) {
		run(t, []step{
			{name: "막연한 표현", conv: "a", after: 0, stage: StageCheck, want: StageCheck},
			{name: "되물은 뒤에도 애매함", conv: "a", after: time.Minute, stage: StageCheck, want: StageCheck},
			{name: "아니라고 답하면 그대로 받아들인다", conv: "a", after: 2 * time.Minute, stage: StageNone, asked: true, want: StageNone},
		})
	})

	t.Run("되물은 답으로 뜻이 풀리면 거기서 끝난다", func(t *testing.T) {
		run(t, []step{
			{name: "막연한 표현", conv: "a", after: 0, stage: StageCheck, want: StageCheck},
			{name: "일이 많아 쉬고 싶다는 뜻이었으면 해당 없음이다", conv: "a", after: time.Minute, stage: StageNone, want: StageNone},
		})
	})

	// 쌓임을 보는 규칙은 발화가 아니라 대화를 센다. 한 대화에서 되묻는 동안 확인이 두 번 남아도 그것은 대화 하나다.
	// 발화를 셌다면 아흐레 뒤의 첫 애매한 표현이 곧 세 번째가 되어, 되묻는 걸음 없이 바로 대응으로 갔을 것이다.
	t.Run("한 대화에서 확인이 두 번 남았어도 다음 대화의 첫 애매한 표현은 되묻는 데서 시작한다", func(t *testing.T) {
		run(t, []step{
			{name: "첫 대화: 막연한 표현", conv: "a", after: 0, stage: StageCheck, want: StageCheck},
			{name: "첫 대화: 되물은 뒤에도 애매함", conv: "a", after: time.Minute, stage: StageCheck, want: StageCheck},
			{name: "첫 대화: 직접 물은 답으로 뜻이 풀림", conv: "a", after: 2 * time.Minute, stage: StageNone, asked: true, want: StageNone},
			{name: "아흐레 뒤의 새 대화: 관문에 걸린 두 번째 대화라 확인이다", conv: "b", after: 9 * testDay, stage: StageCheck, want: StageCheck},
			{name: "같은 대화에서 되물은 뒤에도 애매함: 여전히 두 번째 대화다", conv: "b", after: 9*testDay + time.Minute, stage: StageCheck, want: StageCheck},
			{
				name: "열하루 뒤의 또 다른 대화: 2주 안의 세 번째 대화라 첫 표현부터 대응이다", conv: "c", after: 11 * testDay, stage: StageCheck,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
			},
		})
	})

	// 쌓임을 보는 규칙은 최종 단계가 확인 이상이었던 대화를 모두 센다. 상태가 나빠서 대응으로 끝난 애매한 표현도
	// "애매한 표현이 나온 대화"다. 확인으로 끝난 것만 세면 상태가 나쁜 사람일수록 이 규칙에 덜 걸린다.
	t.Run("상태가 나빠 대응으로 끝난 애매한 표현도 쌓임에 든다", func(t *testing.T) {
		run(t, []step{
			{
				name: "첫날: 애매한 표현인데 점수가 기준 이상이라 대응이다", conv: "a", after: 0, stage: StageCheck, bad: true,
				want: StageRespond, wantAdj: []Adjustment{AdjustBadStatePlusOne},
			},
			{name: "여드레 뒤: 민감한 기간이 지났고 관문에 걸린 대화로는 두 번째다", conv: "b", after: 8 * testDay, stage: StageCheck, want: StageCheck},
			{
				name: "열흘 뒤: 첫날의 대화까지 세어 세 번째다", conv: "c", after: 10 * testDay, stage: StageCheck,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
			},
		})
	})

	t.Run("몇 주에 걸쳐 쌓이고 풀리는 흐름", func(t *testing.T) {
		steps := []step{
			{name: "첫날의 애매한 표현은 확인이다", conv: "c1", after: 0, stage: StageCheck, want: StageCheck},
			{name: "5일 뒤 두 번째 대화도 확인이다", conv: "c2", after: 5 * testDay, stage: StageCheck, want: StageCheck},
			{
				name: "9일 뒤 세 번째 대화는 대응이다", conv: "c3", after: 9 * testDay, stage: StageCheck,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
			},
			{
				name: "12일 뒤에도 앞의 세 대화가 아직 2주 안이라 네 번째다", conv: "c4", after: 12 * testDay, stage: StageCheck,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
			},
			{
				name: "17일 뒤에는 첫날이 2주를 벗어났지만 그 뒤의 세 대화가 남아 있어 여전히 대응이다", conv: "c5", after: 17 * testDay, stage: StageCheck,
				want: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
			},
			{name: "그 사이의 관용 표현은 해당 없음이다", conv: "c6", after: 18 * testDay, stage: StageNone, want: StageNone},
			{
				name: "28일 뒤에는 2주 안의 대화가 17일째의 것 하나뿐이고 민감한 기간도 지나 다시 확인이다", conv: "c7", after: 28 * testDay, stage: StageCheck,
				want: StageCheck,
			},
		}
		history := run(t, steps)

		// 전체 기록을 넘기고 지난 시각을 기준으로 다시 돌려도 그때와 같은 판정이 나온다.
		// 기준 시각보다 뒤의 판정을 세지 않기 때문이다. 다시 판정하는 발화 자신만 뺀다.
		for i, s := range steps {
			others := slices.Delete(slices.Clone(history), i, i+1)
			in := judged(s.stage)
			in.ConversationID = s.conv
			in.Now = history[i].At
			in.History = others

			got := mustDecide(t, in)

			assert.Equal(t, s.want, got.Stage, "다시 돌림: "+s.name)
			assertAdjustments(t, s.wantAdj, got)
		}
	})
}

func TestDecide_InvalidInput(t *testing.T) {
	valid := func() Input {
		in := judged(StageCheck)
		in.History = []Event{before(20*testDay, StageCheck), before(2*testDay, StageRespond), before(3*testDay, StageNone)}
		return in
	}

	t.Run("기준이 되는 입력은 오류가 없다", func(t *testing.T) {
		_, err := Decide(valid(), params.Default().Crisis)

		require.NoError(t, err)
	})

	// 기준이 되는 입력은 두 겹이 모두 확인으로 본 말이다. 2주 안에 관문에 걸린 대화는 이틀 전의 것과 이번뿐이라 쌓임에는 닿지 않고,
	// 이틀 전에 대응이 있어서 민감한 기간의 규칙으로 대응이 된다.
	// 입력이 틀려도 판정은 빈 값이 아니다. 읽을 수 있는 것만으로 끝까지 판정한 값이 오류와 함께 나온다.
	sensitive := []Adjustment{AdjustSensitiveWindow}

	tests := []struct {
		name      string
		mutate    func(in *Input)
		wantErr   error
		wantStage Stage
		wantBy    DetectedBy
		wantAdj   []Adjustment
	}{
		{
			name:   "판정 시각이 비어 있다. 지난 판정을 보는 규칙은 적용하지 못하고 두 겹의 판정은 남는다",
			mutate: func(in *Input) { in.Now = time.Time{} }, wantErr: ErrMissingNow,
			wantStage: StageCheck, wantBy: DetectedByBoth,
		},
		{
			name:   "규칙의 단계가 음수다. AI 판별의 단계와 나머지 규칙으로 판정한다",
			mutate: func(in *Input) { in.Rule.Stage = -1 }, wantErr: ErrInvalidStage,
			wantStage: StageRespond, wantBy: DetectedByAI, wantAdj: sensitive,
		},
		{
			name:   "규칙의 단계가 3보다 크다",
			mutate: func(in *Input) { in.Rule.Stage = StageUrgent + 1 }, wantErr: ErrInvalidStage,
			wantStage: StageRespond, wantBy: DetectedByAI, wantAdj: sensitive,
		},
		{
			name:   "AI 판별의 단계가 음수다. 답하지 못한 것으로 보고 규칙의 단계로 판정한다",
			mutate: func(in *Input) { in.AI = AIAnswered(-1) }, wantErr: ErrInvalidStage,
			wantStage: StageRespond, wantBy: DetectedByRule, wantAdj: sensitive,
		},
		{
			name:   "AI 판별의 단계가 3보다 크다",
			mutate: func(in *Input) { in.AI = AIAnswered(7) }, wantErr: ErrInvalidStage,
			wantStage: StageRespond, wantBy: DetectedByRule, wantAdj: sensitive,
		},
		{
			name:    "AI 판별이 답하지 못했다면서 단계가 채워져 있다. 그 단계를 버리지 않는다",
			mutate:  func(in *Input) { in.AI = AIResult{Answered: false, Stage: StageRespond} },
			wantErr: ErrInconsistentAI, wantStage: StageRespond, wantBy: DetectedByBoth,
		},
		{
			name:    "이번 대화의 식별자가 비어 있다. 나머지 규칙은 그대로 적용한다",
			mutate:  func(in *Input) { in.ConversationID = "" },
			wantErr: ErrMissingConversationID, wantStage: StageRespond, wantBy: DetectedByBoth, wantAdj: sensitive,
		},
		{
			name:    "AI 판별이 답하지 못했다면서 읽을 수 없는 단계가 채워져 있다. 쓸 수 없으니 답하지 못한 것으로 둔다",
			mutate:  func(in *Input) { in.AI = AIResult{Answered: false, Stage: 9} },
			wantErr: ErrInconsistentAI, wantStage: StageRespond, wantBy: DetectedByRule, wantAdj: sensitive,
		},
	}
	for _, tt := range tests {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			in := valid()
			tt.mutate(&in)

			got, err := Decide(in, params.Default().Crisis)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantStage, got.Stage)
			assert.Equal(t, tt.wantBy, got.DetectedBy)
			assertAdjustments(t, tt.wantAdj, got)
		})
	}

	t.Run("규칙의 단계를 읽을 수 없는 말은 AI 판별이 해당 없음으로 봤어도 확인 아래로 두지 않는다", func(t *testing.T) {
		in := judged(StageNone)
		in.Rule.Stage = 5

		got, err := Decide(in, params.Default().Crisis)

		require.ErrorIs(t, err, ErrInvalidStage)
		assert.Equal(t, StageCheck, got.Stage)
		assert.Equal(t, DetectedByRule, got.DetectedBy)
		// 정해진 규칙이 올린 것이 아니라서 바꾼 규칙에는 적히지 않는다. 까닭은 오류에 있다.
		assertAdjustments(t, nil, got)
	})

	// AI 판별 실패 때의 바닥만으로 확인이 된 말은 상태가 나빠도 올리지 않는다. 그것은 규칙이 해당 없음으로 본 것을 알 때의 이야기다.
	// 규칙의 단계를 읽지 못한 말은 규칙이 확인으로 봤을 수도 있고, 그랬다면 상태가 나쁠 때 대응이 되었을 말이다.
	// 모르는 것을 가볍게 다루는 쪽으로 쓰지 않으므로 그런 말은 봐주지 않고 올린다. 바닥과 올림이 함께 적히는 것은 이 경우뿐이다.
	// 맞는 입력은 이 길에 오지 않는다. 규칙의 단계를 만드는 코드가 틀렸을 때만 지나간다.
	unreadableTests := []struct {
		name        string
		rule        RuleResult
		ai          AIResult
		wantErr     error
		wantBy      DetectedBy
		wantCalm    Stage
		wantCalmAdj []Adjustment
		wantBad     Stage
		wantBadAdj  []Adjustment
	}{
		{
			name: "규칙의 단계를 읽을 수 없고 AI 판별도 답하지 못했으면 확인이고, 상태가 나쁘면 대응이다",
			rule: RuleResult{Stage: -3}, ai: AIFailed(), wantErr: ErrInvalidStage, wantBy: DetectedByRule,
			wantCalm: StageCheck, wantCalmAdj: []Adjustment{AdjustAIFailedFloor},
			wantBad: StageRespond, wantBadAdj: []Adjustment{AdjustAIFailedFloor, AdjustBadStatePlusOne},
		},
		{
			name: "읽을 수 없는 단계에 사전에 걸렸다는 표시까지 있어도 같다",
			rule: RuleResult{Stage: 7, Matched: true}, ai: AIFailed(), wantErr: ErrInvalidStage, wantBy: DetectedByRule,
			wantCalm: StageCheck, wantCalmAdj: []Adjustment{AdjustAIFailedFloor},
			wantBad: StageRespond, wantBadAdj: []Adjustment{AdjustAIFailedFloor, AdjustBadStatePlusOne},
		},
		{
			// 틀린 입력 때문에 생긴 바닥은 바꾼 규칙에 적히지 않는다. 상태가 나쁠 때의 올림만 적힌다.
			name: "규칙의 단계를 읽을 수 없고 AI 판별이 해당 없음으로 답했어도 확인이고, 상태가 나쁘면 대응이다",
			rule: RuleResult{Stage: -3}, ai: AIAnswered(StageNone), wantErr: ErrInvalidStage, wantBy: DetectedByRule,
			wantCalm: StageCheck,
			wantBad:  StageRespond, wantBadAdj: []Adjustment{AdjustBadStatePlusOne},
		},
		{
			name: "규칙의 단계를 읽을 수 없고 AI 판별이 확인으로 답했으면 AI 판별이 잡은 말이고, 상태가 나쁘면 대응이다",
			rule: RuleResult{Stage: -3}, ai: AIAnswered(StageCheck), wantErr: ErrInvalidStage, wantBy: DetectedByAI,
			wantCalm: StageCheck,
			wantBad:  StageRespond, wantBadAdj: []Adjustment{AdjustBadStatePlusOne},
		},
		{
			// 견줄 자리다. 규칙이 해당 없음으로 본 것을 알고 있으면 같은 상태에서도 되묻는 데서 멈춘다.
			name: "규칙이 해당 없음으로 본 것을 읽을 수 있으면 AI 판별이 답하지 못했어도 상태와 상관없이 확인이다",
			rule: RuleResult{Stage: StageNone, Matched: true}, ai: AIFailed(), wantBy: DetectedByRule,
			wantCalm: StageCheck, wantCalmAdj: []Adjustment{AdjustAIFailedFloor},
			wantBad: StageCheck, wantBadAdj: []Adjustment{AdjustAIFailedFloor},
		},
	}
	for _, tt := range unreadableTests {
		t.Run(tt.name, func(t *testing.T) {
			states := []struct {
				state   State
				want    Stage
				wantAdj []Adjustment
			}{
				{State{}, tt.wantCalm, tt.wantCalmAdj},
				{State{ScoreElevated: true}, tt.wantBad, tt.wantBadAdj},
				{State{ChangeDetected: true}, tt.wantBad, tt.wantBadAdj},
				{State{ScoreElevated: true, ChangeDetected: true}, tt.wantBad, tt.wantBadAdj},
			}
			for _, s := range states {
				in := Input{Rule: tt.rule, AI: tt.ai, State: s.state, ConversationID: thisConversation, Now: testNow()}

				got, err := Decide(in, params.Default().Crisis)

				if tt.wantErr == nil {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, tt.wantErr)
				}
				assert.Equal(t, s.want, got.Stage, "상태 %+v", s.state)
				assert.Equal(t, tt.wantBy, got.DetectedBy, "상태 %+v", s.state)
				assertAdjustments(t, s.wantAdj, got)
			}
		})
	}

	t.Run("틀린 입력이 해당 없음인 말을 무겁게 만들지는 않는다", func(t *testing.T) {
		// 지난 판정 한 줄이 틀렸다고 평범한 말이 모두 확인 대상이 되면, 그 사람의 대화 전체가 확인 질문으로 덮인다.
		in := judged(StageNone)
		in.History = []Event{{Stage: StageRespond}}
		in.Now = time.Time{}

		got, err := Decide(in, params.Crisis{})

		require.ErrorIs(t, err, ErrInvalidParams)
		require.ErrorIs(t, err, ErrMissingNow)
		require.ErrorIs(t, err, ErrMissingEventTime)
		assert.Equal(t, StageNone, got.Stage)
		assert.Equal(t, DetectedByNone, got.DetectedBy)
		assertAdjustments(t, nil, got)
	})

	t.Run("직접 물은 뒤라는 것은 지난 판정을 읽지 못해도 적용된다", func(t *testing.T) {
		in := valid()
		in.Now = time.Time{}
		in.DirectAskDone = true

		got, err := Decide(in, params.Default().Crisis)

		require.ErrorIs(t, err, ErrMissingNow)
		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustRepeatAfterDirectAsk}, got)
	})

	eventTests := []struct {
		name      string
		history   []Event
		wantIndex int
		wantErr   error
		wantStage Stage
		wantAdj   []Adjustment
	}{
		{
			name:      "지난 판정의 단계가 범위를 벗어났다. 그 판정만 빼고 센다",
			history:   []Event{before(testDay, StageCheck), before(2*testDay, 4)},
			wantIndex: 1, wantErr: ErrInvalidStage,
			wantStage: StageCheck,
		},
		{
			name:      "지난 판정의 시각이 비어 있다. 나머지 둘과 이번으로 세 번째다",
			history:   []Event{before(testDay, StageCheck), before(2*testDay, StageCheck), {Stage: StageCheck}},
			wantIndex: 2, wantErr: ErrMissingEventTime,
			wantStage: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
		},
		{
			name:      "틀린 판정 뒤에 있는 맞는 판정도 빠뜨리지 않는다",
			history:   []Event{{Stage: StageCheck}, before(3*testDay, StageUrgent)},
			wantIndex: 0, wantErr: ErrMissingEventTime,
			wantStage: StageRespond, wantAdj: []Adjustment{AdjustSensitiveWindow},
		},
		{
			name:      "기간 밖이라 세지 않을 판정이어도 틀린 값이면 알린다",
			history:   []Event{before(400*testDay, -2)},
			wantIndex: 0, wantErr: ErrInvalidStage,
			wantStage: StageCheck,
		},
		{
			name:      "해당 없음이라 세지 않을 판정이어도 시각이 비어 있으면 알린다",
			history:   []Event{{Stage: StageNone}},
			wantIndex: 0, wantErr: ErrMissingEventTime,
			wantStage: StageCheck,
		},
		{
			name:      "여러 개가 틀렸으면 앞선 자리의 것을 알리고, 틀린 것은 모두 뺀다",
			history:   []Event{before(testDay, StageCheck), {Stage: StageCheck}, before(testDay, 9)},
			wantIndex: 1, wantErr: ErrMissingEventTime,
			wantStage: StageCheck,
		},
		{
			name:      "지난 판정의 대화 식별자가 비어 있다. 빼지 않고 따로 떨어진 대화 하나로 센다",
			history:   []Event{before(testDay, StageCheck), {At: testNow().Add(-2 * testDay), Stage: StageCheck}},
			wantIndex: 1, wantErr: ErrMissingConversationID,
			wantStage: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
		},
		{
			name: "대화 식별자가 빠진 판정 둘은 같은 대화였을 수도 있지만 덜 세지 않는다",
			history: []Event{
				{At: testNow().Add(-2 * testDay), Stage: StageCheck},
				{At: testNow().Add(-2*testDay + time.Minute), Stage: StageCheck},
			},
			wantIndex: 0, wantErr: ErrMissingConversationID,
			wantStage: StageRespond, wantAdj: []Adjustment{AdjustRepeatedInWindow},
		},
		{
			name:      "대화 식별자가 빠진 대응도 민감한 기간에는 그대로 쓴다",
			history:   []Event{{At: testNow().Add(-2 * testDay), Stage: StageRespond}},
			wantIndex: 0, wantErr: ErrMissingConversationID,
			wantStage: StageRespond, wantAdj: []Adjustment{AdjustSensitiveWindow},
		},
		{
			name:      "해당 없음이라 세지 않을 판정이어도 대화 식별자가 비어 있으면 알린다",
			history:   []Event{{At: testNow().Add(-testDay), Stage: StageNone}},
			wantIndex: 0, wantErr: ErrMissingConversationID,
			wantStage: StageCheck,
		},
		{
			name:      "단계를 읽을 수 없는 판정은 대화 식별자가 있어도 뺀다",
			history:   []Event{before(testDay, StageCheck), before(2*testDay, 7)},
			wantIndex: 1, wantErr: ErrInvalidStage,
			wantStage: StageCheck,
		},
	}
	for _, tt := range eventTests {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			in := valid()
			in.History = tt.history
			original := slices.Clone(tt.history)

			got, err := Decide(in, params.Default().Crisis)

			require.ErrorIs(t, err, tt.wantErr)
			var eventErr *EventError
			require.ErrorAs(t, err, &eventErr)
			assert.Equal(t, tt.wantIndex, eventErr.Index)
			assert.Equal(t, tt.wantStage, got.Stage)
			assert.Equal(t, DetectedByBoth, got.DetectedBy)
			assertAdjustments(t, tt.wantAdj, got)
			assert.Equal(t, original, in.History, "받은 목록은 고치지 않는다")
		})
	}

	t.Run("이번 대화의 식별자가 비어 있으면 지난 대화를 모두 다른 대화로 센다", func(t *testing.T) {
		// 어느 판정이 이 대화의 것인지 가릴 수 없다. 덜 세는 쪽으로 읽지 않는다.
		in := judged(StageCheck)
		in.ConversationID = ""
		in.History = []Event{before(3*testDay, StageCheck), before(time.Minute, StageCheck)}

		got, err := Decide(in, params.Default().Crisis)

		require.ErrorIs(t, err, ErrMissingConversationID)
		var eventErr *EventError
		assert.NotErrorAs(t, err, &eventErr, "지난 판정이 아니라 입력의 식별자가 빠진 것이다")
		assert.Equal(t, StageRespond, got.Stage)
		assertAdjustments(t, []Adjustment{AdjustRepeatedInWindow}, got)
	})

	t.Run("이번 말이 해당 없음이어서 지난 판정을 볼 일이 없어도 틀린 입력은 알린다", func(t *testing.T) {
		in := judged(StageNone)
		in.History = []Event{{Stage: StageCheck}}

		_, err := Decide(in, params.Default().Crisis)

		require.ErrorIs(t, err, ErrMissingEventTime)
	})

	paramTests := []struct {
		name      string
		mutate    func(p *params.Crisis)
		wantField string
	}{
		{"쌓임을 보는 기간이 0일이다", func(p *params.Crisis) { p.RepeatWindowDays = 0 }, "Crisis.RepeatWindowDays"},
		{"쌓임을 보는 기간이 음수다", func(p *params.Crisis) { p.RepeatWindowDays = -14 }, "Crisis.RepeatWindowDays"},
		{"횟수가 1이면 모든 확인이 곧바로 대응이 된다", func(p *params.Crisis) { p.RepeatCount = 1 }, "Crisis.RepeatCount"},
		{"횟수가 0이다", func(p *params.Crisis) { p.RepeatCount = 0 }, "Crisis.RepeatCount"},
		{"민감한 기간이 0일이다", func(p *params.Crisis) { p.SensitiveWindowDays = 0 }, "Crisis.SensitiveWindowDays"},
		{"조정 값을 채우지 않았다", func(p *params.Crisis) { *p = params.Crisis{} }, "Crisis.RepeatWindowDays"},
	}
	for _, tt := range paramTests {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			p := params.Default().Crisis
			tt.mutate(&p)

			got, err := Decide(valid(), p)

			require.ErrorIs(t, err, ErrInvalidParams)
			var fieldErr *params.FieldError
			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, tt.wantField, fieldErr.Field)
			// 기간과 횟수를 믿을 수 없으니 지난 판정을 보는 규칙은 적용하지 못한다. 두 겹의 판정은 남는다.
			assert.Equal(t, StageCheck, got.Stage)
			assert.Equal(t, DetectedByBoth, got.DetectedBy)
			assertAdjustments(t, nil, got)
		})
	}

	t.Run("코어의 조정 값 검사를 통과한 기본값은 여기서도 통과한다", func(t *testing.T) {
		p := params.Default()
		require.NoError(t, p.Validate())

		_, err := Decide(valid(), p.Crisis)

		require.NoError(t, err)
	})

	t.Run("오류 글에는 자리와 고정된 설명만 담긴다", func(t *testing.T) {
		in := valid()
		in.History = []Event{before(testDay, StageCheck), {Stage: StageCheck}}

		_, err := Decide(in, params.Default().Crisis)

		require.EqualError(t, err, "crisis event 1: crisis: event instant is missing")
	})
}

func TestHistoryHorizon(t *testing.T) {
	tests := []struct {
		name string
		p    params.Crisis
		want time.Duration
	}{
		{"기본값에서는 쌓임을 보는 14일이 더 길다", params.Default().Crisis, repeatWindow},
		{
			"민감한 기간을 30일로 늘리면 그쪽을 따른다",
			params.Crisis{EscalationMinScore: 10, RepeatWindowDays: 14, RepeatCount: 3, SensitiveWindowDays: 30},
			30 * testDay,
		},
		{
			"시간으로 옮길 수 없을 만큼 긴 기간은 가장 긴 시간으로 둔다",
			params.Crisis{EscalationMinScore: 10, RepeatWindowDays: math.MaxInt, RepeatCount: 3, SensitiveWindowDays: 7},
			time.Duration(math.MaxInt64),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HistoryHorizon(tt.p))
		})
	}

	t.Run("이만큼 거슬러 읽어 온 기록이면 전부를 넘긴 것과 같은 판정이 나온다", func(t *testing.T) {
		p := params.Default().Crisis
		full := []Event{
			before(repeatWindow, StageCheck), before(3*testDay, StageCheck),
			before(repeatWindow+time.Nanosecond, StageCheck), before(60*testDay, StageUrgent),
		}
		var loaded []Event
		for _, e := range full {
			if testNow().Sub(e.At) <= HistoryHorizon(p) {
				loaded = append(loaded, e)
			}
		}
		require.Len(t, loaded, 2)

		in := judged(StageCheck)
		in.History = full
		want := mustDecide(t, in)
		in.History = loaded
		got := mustDecide(t, in)

		assert.Equal(t, want, got)
		assert.Equal(t, StageRespond, got.Stage)
	})
}

func TestDecide_Deterministic(t *testing.T) {
	history := []Event{
		before(13*testDay, StageCheck),
		before(6*testDay, StageRespond),
		before(40*testDay, StageUrgent),
		before(2*time.Hour, StageCheck),
	}

	t.Run("같은 입력이면 몇 번을 불러도 같은 결과다", func(t *testing.T) {
		in := judged(StageCheck)
		in.History = history

		first := mustDecide(t, in)
		for range 10 {
			assert.Equal(t, first, mustDecide(t, in))
		}
	})

	t.Run("지난 판정의 순서를 어떻게 바꿔도 결과가 같다", func(t *testing.T) {
		// 민감한 기간보다 쌓임이 먼저 맞는 입력이다. 순서에 따라 남는 규칙이 달라지면 안 된다.
		in := judged(StageCheck)
		in.History = history
		want := mustDecide(t, in)
		require.Equal(t, []Adjustment{AdjustRepeatedInWindow}, want.Adjustments)

		perms := permutations(history)
		require.Len(t, perms, 24)
		for _, perm := range perms {
			in.History = perm

			assert.Equal(t, want, mustDecide(t, in))
		}
	})

	t.Run("넘겨받은 지난 판정을 고치지 않는다", func(t *testing.T) {
		in := judged(StageCheck)
		in.History = slices.Clone(history)

		mustDecide(t, in)

		assert.Equal(t, history, in.History)
	})

	t.Run("같은 순간이면 시간대가 달라도 결과가 같다", func(t *testing.T) {
		seoul := time.FixedZone("KST", 9*60*60)
		newYork := time.FixedZone("EDT", -4*60*60)

		// 경계에 정확히 걸린 판정으로 본다. 시간대를 옮기다 어긋나면 여기서 드러난다.
		utc := judged(StageCheck)
		utc.History = []Event{before(sensitiveWindow, StageRespond)}
		want := mustDecide(t, utc)
		require.Equal(t, StageRespond, want.Stage)

		moved := judged(StageCheck)
		moved.Now = utc.Now.In(seoul)
		moved.History = []Event{{At: utc.History[0].At.In(newYork), Stage: StageRespond, ConversationID: utc.History[0].ConversationID}}

		assert.Equal(t, want, mustDecide(t, moved))
	})

	t.Run("달력 날짜가 아니라 흐른 시간으로 잰다", func(t *testing.T) {
		// 판정 시각은 10월 17일 21시 30분이다. 10월 10일 21시 29분의 대응은 달력으로는 "7일 전"이지만
		// 168시간에서 1분이 더 지났으므로 민감한 기간 밖이다.
		in := judged(StageCheck)
		in.History = []Event{{At: time.Date(2026, time.October, 10, 21, 29, 0, 0, time.UTC), Stage: StageRespond, ConversationID: "a"}}
		assert.Equal(t, StageCheck, mustDecide(t, in).Stage)

		// 같은 날 21시 30분이면 정확히 168시간 전이라 기간 안이다.
		in.History = []Event{{At: time.Date(2026, time.October, 10, 21, 30, 0, 0, time.UTC), Stage: StageRespond, ConversationID: "a"}}
		assert.Equal(t, StageRespond, mustDecide(t, in).Stage)

		// 10월 3일 21시 30분은 정확히 336시간 전이다. 그 시각에 서로 다른 두 대화에서 확인이 있었다.
		in.History = []Event{
			{At: time.Date(2026, time.October, 3, 21, 30, 0, 0, time.UTC), Stage: StageCheck, ConversationID: "a"},
			{At: time.Date(2026, time.October, 3, 21, 30, 0, 0, time.UTC), Stage: StageCheck, ConversationID: "b"},
		}
		assert.Equal(t, StageRespond, mustDecide(t, in).Stage)

		in.History = []Event{
			{At: time.Date(2026, time.October, 3, 21, 29, 59, 0, time.UTC), Stage: StageCheck, ConversationID: "a"},
			{At: time.Date(2026, time.October, 3, 21, 30, 0, 0, time.UTC), Stage: StageCheck, ConversationID: "b"},
		}
		assert.Equal(t, StageCheck, mustDecide(t, in).Stage)
	})
}

// permutations는 지난 판정의 모든 순서를 만든다. 넘겨받은 슬라이스는 고치지 않는다.
func permutations(events []Event) [][]Event {
	if len(events) <= 1 {
		return [][]Event{slices.Clone(events)}
	}
	var result [][]Event
	for i := range events {
		rest := slices.Delete(slices.Clone(events), i, i+1)
		for _, perm := range permutations(rest) {
			result = append(result, append([]Event{events[i]}, perm...))
		}
	}
	return result
}

func TestScoreElevated(t *testing.T) {
	p := params.Default().Crisis

	tests := []struct {
		name  string
		score int
		want  bool
	}{
		{"추정 점수 0은 기준 아래다", 0, false},
		{"추정 점수 9는 기준 아래다", 9, false},
		{"추정 점수 10부터 기준 이상이다", 10, true},
		{"추정 점수 11도 기준 이상이다", 11, true},
		{"가장 높은 점수 24도 기준 이상이다", 24, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ScoreElevated(tt.score, p))
		})
	}

	t.Run("기준을 12로 올리면 11은 아래고 12부터 이상이다", func(t *testing.T) {
		tuned := p
		tuned.EscalationMinScore = 12

		assert.False(t, ScoreElevated(11, tuned))
		assert.True(t, ScoreElevated(12, tuned))
	})
}

func TestState_Bad(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{"빈 상태는 나쁜 상태가 아니다", State{}, false},
		{"추정 점수가 기준 이상이면 나쁜 상태다", State{ScoreElevated: true}, true},
		{"변화 감지 상태면 나쁜 상태다. 기록이 모자라 점수가 없을 때도 이쪽은 본다", State{ChangeDetected: true}, true},
		{"둘 다 맞아도 나쁜 상태일 뿐이다", State{ScoreElevated: true, ChangeDetected: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.Bad())
		})
	}
}

func TestDecision_AdjustmentIDs(t *testing.T) {
	t.Run("바꾼 규칙의 식별자를 적용 순서대로 돌려준다", func(t *testing.T) {
		got := mustDecide(t, Input{
			Rule:           RuleResult{Stage: StageNone, Matched: true},
			AI:             AIFailed(),
			DirectAskDone:  true,
			ConversationID: thisConversation,
			Now:            testNow(),
		})

		assert.Equal(t, []string{"ai_failed_floor", "repeat_after_direct_ask"}, got.AdjustmentIDs())
	})

	t.Run("바꾼 규칙이 없으면 nil이 아니라 빈 슬라이스다", func(t *testing.T) {
		got := mustDecide(t, judged(StageRespond))

		ids := got.AdjustmentIDs()

		require.NotNil(t, ids)
		assert.Empty(t, ids)
	})

	t.Run("빈 판정에서도 nil이 아니다", func(t *testing.T) {
		ids := Decision{}.AdjustmentIDs()

		require.NotNil(t, ids)
		assert.Empty(t, ids)
	})
}
