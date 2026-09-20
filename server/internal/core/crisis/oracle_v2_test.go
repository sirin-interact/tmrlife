package crisis_test

// 위기 관문의 판정 규칙 가운데 두 가지를 손으로 따라간 값과 견주는 시험이다.
//
//   - 상태가 나쁠 때의 한 단계 올림은 해당 없음(0단계)인 말과, AI 판별이 답하지 못해서 1단계가 되었을 뿐인 말에는 적용하지 않는다.
//   - "최근 14일 안에 세 번"은 발화가 아니라 대화를 센다. 최종 단계가 1단계 이상이었던 대화를 모두 세고, 이번 대화도 하나로 센다.
//
// 기대값은 코드를 돌려서 얻지 않았다. 경우마다 어느 규칙이 어떤 순서로 단계를 바꾸는지 적었으니,
// 깨지면 셈과 코드 가운데 어느 쪽이 틀렸는지 따져 볼 수 있다. 공개된 함수만 부른다.
//
// 손으로 따라간 순서:
//
//	1. 규칙과 AI 판별 가운데 높은 쪽
//	2. AI 판별이 답하지 못했고 사전에 걸린 말이면 적어도 1단계
//	3. 추정 점수가 10 이상이거나 변화 감지 상태면 한 단계 위로. 0단계는 그대로, 3단계가 천장, 2번만으로 1단계가 된 말도 그대로
//	4. 아직 1단계이고 이 대화에서 이미 직접 물었으면 2단계
//	5. 아직 1단계이고 최근 14일 안에 관문에 걸린(최종 1단계 이상) 대화가 이번 대화를 포함해 세 개째면 2단계
//	6. 아직 1단계이고 최근 7일 안에 2단계 이상이 있었으면 2단계

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// 판정 시각이다. 시계를 읽지 않고 고정한 값을 쓴다.
func ov2Now() time.Time {
	return time.Date(2026, time.May, 20, 21, 0, 0, 0, time.UTC)
}

// ov2Current는 지금 판정하는 발화가 속한 대화다.
const ov2Current = "conv-current"

const ov2Day = 24 * time.Hour

// ov2Talk은 before만큼 전에 시작한 대화 하나에서 나온 판정들이다. 판정은 1분 간격으로 이어진다.
// 적은 단계는 그때의 최종 단계다.
func ov2Talk(id string, before time.Duration, stages ...crisis.Stage) []crisis.Event {
	events := make([]crisis.Event, 0, len(stages))
	for i, stage := range stages {
		events = append(events, crisis.Event{
			At:             ov2Now().Add(-before).Add(time.Duration(i) * time.Minute),
			Stage:          stage,
			ConversationID: id,
		})
	}
	return events
}

func ov2History(talks ...[]crisis.Event) []crisis.Event {
	var out []crisis.Event
	for _, talk := range talks {
		out = append(out, talk...)
	}
	return out
}

// 규칙(표현 사전)의 판정 세 가지다.
func ov2RuleSaw(stage crisis.Stage) crisis.RuleResult {
	return crisis.RuleResult{Stage: stage, Matched: true}
}

// 사전에는 걸렸지만 규칙이 관용 표현으로 보고 0단계를 준 말이다("배고파 죽겠다").
func ov2Idiom() crisis.RuleResult {
	return crisis.RuleResult{Stage: crisis.StageNone, Matched: true}
}

func ov2NoMatch() crisis.RuleResult {
	return crisis.RuleResult{}
}

type ov2Gate struct {
	name    string
	rule    crisis.RuleResult
	ai      crisis.AIResult
	state   crisis.State
	asked   bool
	history []crisis.Event

	want     crisis.Stage
	adjusted []crisis.Adjustment
}

func ov2RunGates(t *testing.T, p params.Crisis, tests []ov2Gate) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := crisis.Decide(crisis.Input{
				Rule:           tt.rule,
				AI:             tt.ai,
				State:          tt.state,
				DirectAskDone:  tt.asked,
				ConversationID: ov2Current,
				History:        tt.history,
				Now:            ov2Now(),
			}, p)
			require.NoError(t, err)

			assert.Equal(t, tt.want, got.Stage, "최종 단계")
			if len(tt.adjusted) == 0 {
				assert.Empty(t, got.Adjustments, "단계를 바꾼 규칙이 없어야 한다")
			} else {
				assert.Equal(t, tt.adjusted, got.Adjustments, "단계를 바꾼 규칙과 그 순서")
			}
		})
	}
}

// 발화를 둘러싼 상태 네 가지다.
func ov2HighScore() crisis.State { return crisis.State{ScoreElevated: true} }
func ov2Changed() crisis.State   { return crisis.State{ChangeDetected: true} }
func ov2BothBad() crisis.State   { return crisis.State{ScoreElevated: true, ChangeDetected: true} }
func ov2Calm() crisis.State      { return crisis.State{} }

// 상태가 나쁠 때의 한 단계 올림이 어떤 1단계에는 붙고 어떤 1단계에는 붙지 않는지를 가른다.
// 같은 "AI 판별이 멈춘 1단계"라도, 규칙이 스스로 1단계로 본 말은 오르고 바닥 규칙만으로 1단계가 된 말은 오르지 않는다.
func TestOracleV2BadStateSkipsFloorOnlyCheck(t *testing.T) {
	ov2RunGates(t, params.Default().Crisis, []ov2Gate{
		{
			// 1번: 규칙 0, AI는 답이 없어 견줄 것이 없다 → 0. 2번: 사전에 걸렸으므로 1. 3번: 2번만으로 1단계가 된 말이라 올리지 않는다. 4~6번: 맞는 것이 없다 → 1
			name: "AI 판별이 멈춘 동안 관용 표현이 사전에 걸렸고 점수가 10 이상이면 1단계에서 멈춘다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2HighScore(),
			want: crisis.StageCheck, adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			// 위와 같고 나쁜 상태의 까닭만 변화 감지로 바뀌었다 → 1
			name: "같은 말에 변화 감지 상태여도 1단계에서 멈춘다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2Changed(),
			want: crisis.StageCheck, adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			// 두 조건이 다 맞아도 올림은 한 번뿐인데, 그 한 번도 이 말에는 붙지 않는다 → 1
			name: "같은 말에 점수와 변화 감지가 모두 나빠도 1단계에서 멈춘다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2BothBad(),
			want: crisis.StageCheck, adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			// 1번: 규칙 1 → 1. 2번: 이미 1단계라 바닥이 바꾸는 것이 없다. 3번: 규칙이 스스로 본 1단계이므로 한 단계 위 → 2
			name: "AI 판별이 멈췄어도 규칙이 1단계로 본 말은 점수가 10 이상이면 2단계가 된다",
			rule: ov2RuleSaw(crisis.StageCheck), ai: crisis.AIFailed(), state: ov2HighScore(),
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			name: "AI 판별이 멈췄어도 규칙이 1단계로 본 말은 변화 감지 상태면 2단계가 된다",
			rule: ov2RuleSaw(crisis.StageCheck), ai: crisis.AIFailed(), state: ov2Changed(),
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 1번: max(규칙 0, AI 1) = 1. 2번: AI가 답했으므로 해당 없음. 3번: AI가 본 1단계라 올린다 → 2
			name: "규칙은 관용 표현으로 봤지만 AI 판별이 1단계로 답한 말은 상태가 나쁘면 2단계가 된다",
			rule: ov2Idiom(), ai: crisis.AIAnswered(crisis.StageCheck), state: ov2BothBad(),
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 1번: max(0, 0) = 0. 2번: AI가 답했다. 3번: 0단계는 올리지 않는다 → 0
			name: "AI 판별이 0단계로 답한 관용 표현은 상태가 나빠도 0단계다",
			rule: ov2Idiom(), ai: crisis.AIAnswered(crisis.StageNone), state: ov2BothBad(),
			want: crisis.StageNone,
		},
		{
			// 1번: 0. 2번: 사전에 걸리지 않았으므로 바닥이 없다. 3번: 0단계는 올리지 않는다 → 0
			name: "AI 판별이 멈췄어도 사전에 걸리지 않은 말은 상태가 나빠도 0단계다",
			rule: ov2NoMatch(), ai: crisis.AIFailed(), state: ov2BothBad(),
			want: crisis.StageNone,
		},
		{
			// 1번: 0. 2번: 1. 3번: 상태가 나쁘지 않다 → 1
			name: "상태가 평온할 때 AI 판별이 멈추고 사전에 걸린 말은 1단계다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2Calm(),
			want: crisis.StageCheck, adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			// 1번: 2. 2번: 바꾸는 것이 없다. 3번: 2 → 3
			name: "규칙이 2단계로 본 말은 AI 판별이 멈췄어도 상태가 나쁘면 3단계가 된다",
			rule: ov2RuleSaw(crisis.StageRespond), ai: crisis.AIFailed(), state: ov2HighScore(),
			want: crisis.StageUrgent, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 1번: 3. 3번: 3단계가 천장이라 바뀌는 것이 없다. 바뀐 것이 없으므로 적을 규칙도 없다
			name: "규칙이 3단계로 본 말은 상태가 나빠도 3단계 그대로다",
			rule: ov2RuleSaw(crisis.StageUrgent), ai: crisis.AIFailed(), state: ov2BothBad(),
			want: crisis.StageUrgent,
		},
		{
			// 1번: max(1, 2) = 2. 3번: 2 → 3
			name: "규칙 1단계와 AI 2단계 가운데 높은 쪽인 2단계가 상태 때문에 3단계가 된다",
			rule: ov2RuleSaw(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageRespond), state: ov2Changed(),
			want: crisis.StageUrgent, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 1번: 0. 2번: 1. 3번: 바닥만으로 된 1단계라 건너뛴다. 4번: 아직 1단계이고 이미 직접 물었다 → 2.
			// 3번만 건너뛰는 것이다. 되풀이를 보는 4~6번은 이 말에도 그대로 적용된다
			name: "바닥만으로 1단계가 된 말도 이 대화에서 이미 직접 물었으면 2단계가 된다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2BothBad(), asked: true,
			want:     crisis.StageRespond,
			adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatAfterDirectAsk},
		},
		{
			// 1번: 0. 2번: 1. 3번: 건너뛴다. 4번: 묻지 않았다. 5번: 다른 대화 하나 + 이번 대화 = 둘. 6번: 사흘 전에 2단계가 있었다 → 2
			name: "바닥만으로 1단계가 된 말도 사흘 전에 2단계가 있었으면 2단계가 된다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2BothBad(),
			history:  ov2Talk("conv-a", 3*ov2Day, crisis.StageRespond),
			want:     crisis.StageRespond,
			adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustSensitiveWindow},
		},
		{
			// 1번: 0. 2번: 1. 3번: 건너뛴다. 5번: 열흘 전 대화, 나흘 전 대화, 이번 대화 = 세 개째 → 2
			name: "바닥만으로 1단계가 된 말도 관문에 걸린 세 번째 대화면 2단계가 된다",
			rule: ov2Idiom(), ai: crisis.AIFailed(), state: ov2BothBad(),
			history: ov2History(
				ov2Talk("conv-a", 10*ov2Day, crisis.StageCheck),
				ov2Talk("conv-b", 4*ov2Day, crisis.StageCheck),
			),
			want:     crisis.StageRespond,
			adjusted: []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatedInWindow},
		},
		{
			// 1번: 1. 3번: 1 → 2. 4~6번은 "아직 1단계"일 때만 보므로, 세 조건이 모두 맞아도 더 오르지 않는다 → 2.
			// 애매한 표현 하나가 여러 규칙을 거쳐 3단계가 되는 일은 없다
			name: "1단계인 말은 상태가 나쁘고 되풀이 조건이 모두 겹쳐도 2단계까지만 오른다",
			rule: ov2RuleSaw(crisis.StageCheck), ai: crisis.AIFailed(), state: ov2BothBad(), asked: true,
			history: ov2History(
				ov2Talk("conv-a", 10*ov2Day, crisis.StageCheck, crisis.StageCheck),
				ov2Talk("conv-b", 2*ov2Day, crisis.StageCheck, crisis.StageRespond),
			),
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
	})
}

// "최근 14일 안에 세 번"이 무엇을 세는지를 가른다.
// 이 시험의 발화는 모두 규칙과 AI 판별이 1단계로 본 말이고, 따로 적지 않으면 상태는 평온하며 아직 직접 묻지 않았다.
func TestOracleV2RepeatCountsConversations(t *testing.T) {
	check := crisis.StageCheck
	respond := crisis.StageRespond
	none := crisis.StageNone

	// 열흘 전 대화에서 판정 셋, 나흘 전 대화에서 판정 둘. 모두 1단계였다.
	twoTalks := ov2History(
		ov2Talk("conv-a", 10*ov2Day, check, check, check),
		ov2Talk("conv-b", 4*ov2Day, check, check),
	)

	ov2RunGates(t, params.Default().Crisis, []ov2Gate{
		{
			// 대화 a, 대화 b, 이번 대화 = 세 개째 → 2. 판정의 수(다섯)는 상관없다
			name: "판정이 여럿씩 남은 두 대화 뒤의 세 번째 대화면 2단계가 된다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check), history: twoTalks,
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 이틀 전 대화 하나에 판정 다섯 + 이번 대화 = 두 개 → 1. 발화를 셌다면 여섯 번째라 2단계였을 것이다
			name: "한 대화 안에서 판정이 다섯 번 나왔어도 대화로는 하나라 1단계 그대로다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2Talk("conv-a", 2*ov2Day, check, check, check, check, check),
			want:    check,
		},
		{
			// 14일하고 한 시간 전의 대화는 기간 밖이다. 나흘 전 대화 + 이번 대화 = 두 개 → 1
			name: "두 대화 가운데 하나가 14일을 한 시간 넘겼으면 1단계 그대로다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 14*ov2Day+time.Hour, check, check, check),
				ov2Talk("conv-b", 4*ov2Day, check, check),
			),
			want: check,
		},
		{
			// 14일에서 한 시간 모자란 대화는 기간 안이다. 세 개째 → 2
			name: "그 대화가 14일에서 한 시간 모자라면 세 번째 대화라 2단계가 된다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 14*ov2Day-time.Hour, check, check, check),
				ov2Talk("conv-b", 4*ov2Day, check, check),
			),
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 이번 대화의 앞선 판정 둘은 "이번 대화" 하나에 이미 들어 있다. 엿새 전 대화 + 이번 대화 = 두 개 → 1.
			// 되묻고 난 뒤의 두 번째 애매한 말이 곧바로 2단계가 되지 않는다
			name: "이번 대화에 앞선 판정이 둘 있고 다른 대화가 하나뿐이면 1단계 그대로다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk(ov2Current, 10*time.Minute, check, check),
				ov2Talk("conv-a", 6*ov2Day, check, check),
			),
			want: check,
		},
		{
			// 이번 대화(앞선 판정 둘) + 엿새 전 대화 + 열이틀 전 대화 = 세 개 → 2
			name: "이번 대화에 앞선 판정이 있어도 다른 대화가 둘이면 세 번째 대화라 2단계가 된다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk(ov2Current, 10*time.Minute, check, check),
				ov2Talk("conv-a", 6*ov2Day, check),
				ov2Talk("conv-b", 12*ov2Day, check, check),
			),
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 이번 대화의 앞선 판정 셋뿐이다. 대화는 하나 → 1
			name: "이번 대화의 앞선 판정만 셋이면 대화는 하나라 1단계 그대로다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2Talk(ov2Current, 20*time.Minute, check, check, check),
			want:    check,
		},
		{
			// 열이틀 전 대화는 3단계, 아흐레 전 대화는 2단계로 끝났다. 둘 다 "관문에 걸린 대화"다 → 세 개째 → 2.
			// 둘 다 7일이 넘었으므로 6번은 맞지 않는다. 최종 단계가 1인 대화만 셌다면 1단계로 남았을 것이다
			name: "지난 대화의 최종 단계가 2단계나 3단계였어도 걸린 대화로 센다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 12*ov2Day, crisis.StageUrgent),
				ov2Talk("conv-b", 9*ov2Day, respond, respond),
			),
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 사흘 전 대화의 판정은 모두 0단계라 걸린 대화가 아니다. 어제 대화 + 이번 대화 = 두 개 → 1
			name: "판정이 모두 0단계였던 대화는 세지 않는다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 3*ov2Day, none, none, none),
				ov2Talk("conv-b", 1*ov2Day, check),
			),
			want: check,
		},
		{
			// 0단계 판정이 섞여 있어도 1단계 판정이 하나라도 있으면 걸린 대화다. 두 대화 + 이번 대화 = 세 개 → 2
			name: "0단계 판정 사이에 1단계 판정이 하나라도 있는 대화는 센다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 3*ov2Day, none, none, check),
				ov2Talk("conv-b", 2*ov2Day, none, check, check),
			),
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 5번: 대화는 이번 대화 하나뿐이다. 6번: 5분 전, 이 대화에서 2단계가 있었다 → 2
			name: "이번 대화에서 조금 전에 2단계가 있었으면 세 번 규칙이 아니라 민감 기간으로 2단계가 된다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2Talk(ov2Current, 5*time.Minute, respond),
			want:    respond, adjusted: []crisis.Adjustment{crisis.AdjustSensitiveWindow},
		},
		{
			// 3번이 먼저 1 → 2로 올린다. 5번은 "아직 1단계"일 때만 보므로 적용되지 않는다 → 2. 3단계가 되지 않는다
			name: "상태가 나쁘면 세 번째 대화여도 상태 규칙으로만 2단계가 된다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check), state: ov2HighScore(), history: twoTalks,
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 1번: max(1, 2) = 2. 이미 2단계라 5번이 볼 것이 없다 → 2, 바꾼 규칙 없음
			name: "AI 판별이 2단계로 본 말은 세 번째 대화여도 2단계 그대로다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(respond), history: twoTalks,
			want: respond,
		},
		{
			// 4번과 5번이 모두 맞는다. 앞선 4번이 2단계로 올리고 나면 5번은 볼 것이 없다
			name: "이미 직접 물었고 세 번째 대화이기도 하면 직접 묻기 규칙이 적힌다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check), asked: true, history: twoTalks,
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatAfterDirectAsk},
		},
		{
			// 5번과 6번이 모두 맞는다(이틀 전 대화가 2단계로 끝났다). 앞선 5번이 적힌다
			name: "세 번째 대화이고 이틀 전에 2단계도 있었으면 세 번 규칙이 적힌다",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
			history: ov2History(
				ov2Talk("conv-a", 10*ov2Day, check, check),
				ov2Talk("conv-b", 2*ov2Day, check, respond),
			),
			want: respond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
		{
			// 이번 발화가 0단계면 되풀이 규칙은 볼 것이 없다 → 0
			name: "0단계인 말은 걸린 대화가 둘 있었어도 0단계다",
			rule: ov2NoMatch(), ai: crisis.AIAnswered(none), history: twoTalks,
			want: none,
		},
	})
}

// 세는 개수와 기간은 조정 값을 따른다.
func TestOracleV2RepeatFollowsParams(t *testing.T) {
	check := crisis.StageCheck
	twoTalks := ov2History(
		ov2Talk("conv-a", 10*ov2Day, check, check, check),
		ov2Talk("conv-b", 4*ov2Day, check, check),
	)

	t.Run("네 개째로 바꾸면 세 번째 대화는 1단계 그대로이고 네 번째 대화가 2단계다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatCount = 4
		ov2RunGates(t, p, []ov2Gate{
			{
				// 대화 a, b, 이번 대화 = 세 개 < 네 개 → 1
				name: "세 번째 대화",
				rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check), history: twoTalks,
				want: check,
			},
			{
				// 어제 대화가 하나 더 있다 → 네 개째 → 2
				name: "네 번째 대화",
				rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check),
				history: ov2History(twoTalks, ov2Talk("conv-c", 1*ov2Day, check)),
				want:    crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
			},
		})
	})

	t.Run("기간을 7일로 줄이면 열흘 전 대화가 빠져 1단계 그대로다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatWindowDays = 7
		ov2RunGates(t, p, []ov2Gate{{
			// 나흘 전 대화 + 이번 대화 = 두 개 → 1
			name: "세 번째 대화",
			rule: ov2RuleSaw(check), ai: crisis.AIAnswered(check), history: twoTalks,
			want: check,
		}})
	})
}

// 세 대화에 걸친 흐름을 발화 순서대로 다시 돌린다. 앞선 발화의 최종 단계를 그대로 다음 발화의 지난 판정으로 넘긴다.
// 발화는 모두 규칙과 AI 판별이 1단계로 본 애매한 말이고 상태는 평온하다.
//
//	그제 대화 ① 지난 판정 없음 → 1(되묻는다)
//	그제 대화 ② 대화는 이번 하나 → 1(이 말 뒤에 직접 묻는다)
//	어제 대화 ① 그제 대화 + 이번 대화 = 두 개 → 1. 발화를 셌다면 세 번째 발화라 되묻지도 않고 2단계였을 것이다
//	어제 대화 ② 두 개 → 1
//	어제 대화 ③ 이미 직접 물었다 → 4번으로 2
//	오늘 대화 ① 그제, 어제, 이번 = 세 개째 → 5번으로 2(어제의 2단계 때문에 6번도 맞지만 5번이 앞선다)
func TestOracleV2ThreeTalksReplay(t *testing.T) {
	steps := []struct {
		name     string
		conv     string
		at       time.Time
		asked    bool
		want     crisis.Stage
		adjusted []crisis.Adjustment
	}{
		{name: "그제 대화의 첫 애매한 말", conv: "conv-1", at: ov2Now().Add(-2 * ov2Day), want: crisis.StageCheck},
		{name: "그제 대화의 두 번째 애매한 말", conv: "conv-1", at: ov2Now().Add(-2*ov2Day + 5*time.Minute), want: crisis.StageCheck},
		{name: "어제 대화의 첫 애매한 말", conv: "conv-2", at: ov2Now().Add(-1 * ov2Day), want: crisis.StageCheck},
		{name: "어제 대화의 두 번째 애매한 말", conv: "conv-2", at: ov2Now().Add(-1*ov2Day + 5*time.Minute), want: crisis.StageCheck},
		{
			name: "어제 대화에서 직접 묻고 난 뒤의 애매한 말", conv: "conv-2", at: ov2Now().Add(-1*ov2Day + 10*time.Minute), asked: true,
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatAfterDirectAsk},
		},
		{
			name: "오늘 대화의 첫 애매한 말", conv: "conv-3", at: ov2Now(),
			want: crisis.StageRespond, adjusted: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
	}

	var history []crisis.Event
	for _, step := range steps {
		got, err := crisis.Decide(crisis.Input{
			Rule:           ov2RuleSaw(crisis.StageCheck),
			AI:             crisis.AIAnswered(crisis.StageCheck),
			DirectAskDone:  step.asked,
			ConversationID: step.conv,
			History:        history,
			Now:            step.at,
		}, params.Default().Crisis)
		require.NoError(t, err, step.name)

		assert.Equal(t, step.want, got.Stage, step.name)
		if len(step.adjusted) == 0 {
			assert.Empty(t, got.Adjustments, step.name)
		} else {
			assert.Equal(t, step.adjusted, got.Adjustments, step.name)
		}

		history = append(history, crisis.Event{At: step.at, Stage: got.Stage, ConversationID: step.conv})
	}
}

// 점수 쪽의 기준은 "10 이상"이다.
func TestOracleV2ScoreElevatedBoundary(t *testing.T) {
	p := params.Default().Crisis
	assert.False(t, crisis.ScoreElevated(9, p), "9점은 기준 아래다")
	assert.True(t, crisis.ScoreElevated(10, p), "10점부터다")
	assert.True(t, crisis.ScoreElevated(24, p))
}
