package crisis_test

// 이 파일의 기대값은 코드를 돌려서 얻은 것이 아니라 규칙을 손으로 따라가며 구한 것이다.
// 경우마다 어느 규칙이 어떤 순서로 단계를 바꾸는지 적어 두었으니, 시험이 깨지면 코드와 셈 가운데 어느 쪽이 틀렸는지 확인할 수 있다.
// 패키지 밖에서 공개된 함수만 부른다.
//
// 규칙의 순서:
//  1. 규칙과 AI 판별 가운데 높은 쪽
//  2. AI 판별이 답하지 못했고 규칙에 걸린 말이면 최소 1단계
//  3. 추정 점수가 10 이상이거나 변화 감지 상태면 한 단계 위로(0단계는 그대로, 3단계가 천장, 2번만으로 1단계가 된 말도 그대로)
//  4. 아직 1단계이고 이 대화에서 이미 직접 물었으면 2단계
//  5. 아직 1단계이고 최근 14일 안에 관문에 걸린(최종 1단계 이상) 대화가 이번 대화를 포함해 세 개째면 2단계
//  6. 아직 1단계이고 최근 7일 안에 2단계 이상이 있었으면 2단계

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// 판정 시각이다. 시계를 읽지 않고 고정한 값을 쓴다.
func now() time.Time {
	return time.Date(2026, time.March, 20, 12, 30, 0, 0, time.UTC)
}

// thisTalk은 지금 판정하는 발화가 속한 대화다.
const thisTalk = "talk-now"

// ago는 d만큼 전에 있었던 지난 판정이다. 따로 적지 않으면 판정마다 다른 대화에서 나온 것으로 둔다.
func ago(d time.Duration, stage crisis.Stage) crisis.Event {
	return crisis.Event{At: now().Add(-d), Stage: stage, ConversationID: "talk-" + d.String() + "-ago"}
}

// talk은 지난 판정을 정해 준 대화의 것으로 바꾼다.
func talk(id string, e crisis.Event) crisis.Event {
	e.ConversationID = id
	return e
}

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

// 발화를 둘러싼 최근 상태 네 가지다.
func calm() crisis.State        { return crisis.State{} }
func highScore() crisis.State   { return crisis.State{ScoreElevated: true} }
func changed() crisis.State     { return crisis.State{ChangeDetected: true} }
func highAndBoth() crisis.State { return crisis.State{ScoreElevated: true, ChangeDetected: true} }

type gateCase struct {
	name string

	rule      crisis.RuleResult
	ai        crisis.AIResult
	state     crisis.State
	directAsk bool
	history   []crisis.Event

	stage       crisis.Stage
	detectedBy  crisis.DetectedBy
	adjustments []crisis.Adjustment
}

func runGateCases(t *testing.T, p params.Crisis, tests []gateCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := crisis.Decide(crisis.Input{
				Rule:           tt.rule,
				AI:             tt.ai,
				State:          tt.state,
				DirectAskDone:  tt.directAsk,
				ConversationID: thisTalk,
				History:        tt.history,
				Now:            now(),
			}, p)
			require.NoError(t, err)

			assert.Equal(t, tt.stage, got.Stage, "최종 단계")
			assert.Equal(t, tt.detectedBy, got.DetectedBy, "어느 쪽이 잡았는지")
			if len(tt.adjustments) == 0 {
				assert.Empty(t, got.Adjustments, "단계를 바꾼 규칙")
			} else {
				assert.Equal(t, tt.adjustments, got.Adjustments, "단계를 바꾼 규칙")
			}
		})
	}
}

func matched(stage crisis.Stage) crisis.RuleResult {
	return crisis.RuleResult{Stage: stage, Matched: true}
}

func unmatched() crisis.RuleResult {
	return crisis.RuleResult{}
}

// 1번: 두 겹 가운데 높은 쪽을 따른다.
func TestOracleHigherOfTwoLayers(t *testing.T) {
	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "둘 다 해당 없음이면 0단계이고 아무도 잡지 않았다",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageNone), state: calm(),
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			// "배고파 죽겠다": 사전의 단어에는 걸렸지만 규칙도 AI도 관용 표현으로 봤다.
			name: "사전에 걸렸어도 규칙과 AI가 모두 0단계로 봤으면 0단계다",
			rule: matched(crisis.StageNone), ai: crisis.AIAnswered(crisis.StageNone), state: calm(),
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			name: "규칙 1, AI 0 → 1단계, 규칙이 잡았다",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageNone), state: calm(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByRule,
		},
		{
			name: "규칙 0, AI 1 → 1단계, AI가 잡았다",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageCheck), state: calm(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByAI,
		},
		{
			name: "규칙 1, AI 2 → 2단계, 둘 다 잡았다",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageRespond), state: calm(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "규칙 3, AI 1 → 3단계",
			rule: matched(crisis.StageUrgent), ai: crisis.AIAnswered(crisis.StageCheck), state: calm(),
			stage: crisis.StageUrgent, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "규칙 0, AI 3 → 3단계, AI가 잡았다",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageUrgent), state: calm(),
			stage: crisis.StageUrgent, detectedBy: crisis.DetectedByAI,
		},
		{
			name: "규칙 2, AI 0 → 2단계. AI가 낮게 봐도 내려가지 않는다",
			rule: matched(crisis.StageRespond), ai: crisis.AIAnswered(crisis.StageNone), state: calm(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule,
		},
	})
}

// 2번: AI 판별이 실패했거나 제때 오지 않았을 때. 조용히 통과시키지 않는다.
func TestOracleAIFailure(t *testing.T) {
	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "AI 실패, 사전에 걸렸지만 규칙은 0단계로 본 말 → 최소 1단계",
			rule: matched(crisis.StageNone), ai: crisis.AIFailed(), state: calm(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			name: "AI 실패, 사전에 걸리지 않은 말 → 0단계 그대로",
			rule: unmatched(), ai: crisis.AIFailed(), state: calm(),
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			name: "AI 실패, 규칙이 1단계로 본 말 → 1단계(이미 1단계라 바꾼 규칙은 없다)",
			rule: matched(crisis.StageCheck), ai: crisis.AIFailed(), state: calm(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByRule,
		},
		{
			name: "AI 실패, 규칙이 2단계로 본 말 → 2단계. 최소 1단계는 하한이지 상한이 아니다",
			rule: matched(crisis.StageRespond), ai: crisis.AIFailed(), state: calm(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule,
		},
		{
			name: "AI 실패, 규칙이 3단계로 본 말 → 3단계",
			rule: matched(crisis.StageUrgent), ai: crisis.AIFailed(), state: calm(),
			stage: crisis.StageUrgent, detectedBy: crisis.DetectedByRule,
		},
		{
			// "배고파 죽겠다"가 AI 판별이 멈춘 동안 나왔다. 2번으로 1단계(되묻기)까지만 가고, 3번은 이런 말을 올리지 않는다.
			name: "AI 실패로만 1단계가 된 말은 점수가 높아도 1단계에 머문다",
			rule: matched(crisis.StageNone), ai: crisis.AIFailed(), state: highScore(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			name: "AI 실패로만 1단계가 된 말은 변화 감지 상태여도, 두 조건이 다 맞아도 1단계에 머문다",
			rule: matched(crisis.StageNone), ai: crisis.AIFailed(), state: highAndBoth(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustAIFailedFloor},
		},
		{
			// 규칙이 스스로 1단계로 본 말은 2번이 올린 것이 아니다. 3번이 그대로 적용된다: 1 → 2.
			name: "AI 실패여도 규칙이 1단계로 본 말은 상태가 나쁘면 2단계다",
			rule: matched(crisis.StageCheck), ai: crisis.AIFailed(), state: highScore(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustBadStatePlusOne},
		},
		{
			// 3번은 건너뛰지만 4번은 상태가 아니라 되풀이를 보는 규칙이라 그대로 적용된다: 0 → 1 → 2.
			name: "AI 실패로만 1단계가 된 말도 직접 물은 뒤라면 2단계다",
			rule: matched(crisis.StageNone), ai: crisis.AIFailed(), state: highScore(), directAsk: true,
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatAfterDirectAsk},
		},
		{
			// 사전에 걸리지 않았으면 2번이 움직이지 않아 0단계이고, 3번은 0단계를 올리지 않는다.
			name: "AI 실패, 사전에 걸리지 않은 말은 상태가 나빠도 0단계다",
			rule: unmatched(), ai: crisis.AIFailed(), state: highAndBoth(),
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			// 2번으로 1단계 → 5번: 관문에 걸린 지난 대화 둘(3일 전, 10일 전)에 이번 대화가 세 번째 → 2단계.
			name: "AI 실패로 1단계가 된 말이 2주 안의 세 번째 대화에서 나왔으면 2단계다",
			rule: matched(crisis.StageNone), ai: crisis.AIFailed(), state: calm(),
			history: []crisis.Event{ago(3*day, crisis.StageCheck), ago(10*day, crisis.StageCheck)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatedInWindow},
		},
	})
}

// 3번: 상태가 나쁘면 한 단계 올린다. 0단계는 올리지 않는다.
func TestOracleBadStateRaisesOneStep(t *testing.T) {
	plusOne := []crisis.Adjustment{crisis.AdjustBadStatePlusOne}
	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "점수가 높을 때의 1단계 → 2단계",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageCheck), state: highScore(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByAI, adjustments: plusOne,
		},
		{
			name: "변화 감지 상태의 1단계 → 2단계",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageNone), state: changed(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule, adjustments: plusOne,
		},
		{
			name: "두 조건이 모두 맞아도 한 단계만 오른다: 1단계 → 2단계",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageCheck), state: highAndBoth(),
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: plusOne,
		},
		{
			name: "상태가 나쁠 때의 2단계 → 3단계",
			rule: matched(crisis.StageRespond), ai: crisis.AIAnswered(crisis.StageRespond), state: changed(),
			stage: crisis.StageUrgent, detectedBy: crisis.DetectedByBoth, adjustments: plusOne,
		},
		{
			name: "3단계는 더 오를 곳이 없다",
			rule: matched(crisis.StageUrgent), ai: crisis.AIAnswered(crisis.StageUrgent), state: highAndBoth(),
			stage: crisis.StageUrgent, detectedBy: crisis.DetectedByBoth,
		},
		{
			// "배고파 죽겠다"는 상태가 나쁜 동안에도 평소 대화다.
			name: "상태가 나빠도 0단계로 본 말은 올리지 않는다",
			rule: matched(crisis.StageNone), ai: crisis.AIAnswered(crisis.StageNone), state: highAndBoth(),
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			// 3번으로 2단계가 되면 "아직 1단계"가 아니므로 4~6번은 움직이지 않는다.
			// 직접 묻기도 했고, 2주 안에 1단계가 다섯 번 있었고, 어제 3단계가 있었어도 2단계에서 멈춘다.
			name: "애매한 표현은 어떤 조건이 겹쳐도 3단계가 되지 않는다",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageCheck), state: highAndBoth(),
			directAsk: true,
			history: []crisis.Event{
				ago(1*day, crisis.StageUrgent),
				ago(2*day, crisis.StageCheck), ago(3*day, crisis.StageCheck), ago(4*day, crisis.StageCheck),
				ago(5*day, crisis.StageCheck), ago(6*day, crisis.StageCheck),
			},
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: plusOne,
		},
	})

	t.Run("점수의 기준은 10점 이상이다", func(t *testing.T) {
		p := params.Default().Crisis
		assert.False(t, crisis.ScoreElevated(9, p))
		assert.True(t, crisis.ScoreElevated(10, p))
		assert.True(t, crisis.ScoreElevated(24, p))
	})

	t.Run("두 조건 가운데 하나만 맞아도 나쁜 상태다", func(t *testing.T) {
		assert.False(t, calm().Bad())
		assert.True(t, highScore().Bad())
		assert.True(t, changed().Bad())
		assert.True(t, highAndBoth().Bad())
	})
}

// 4번: 직접 묻기는 한 대화에서 한 번이다. 묻고 난 뒤에 1단계 표현이 다시 나오면 2단계다.
func TestOracleRepeatAfterDirectAsk(t *testing.T) {
	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "직접 물은 뒤의 1단계 → 2단계",
			rule: matched(crisis.StageCheck), ai: crisis.AIAnswered(crisis.StageCheck), state: calm(), directAsk: true,
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth,
			adjustments: []crisis.Adjustment{crisis.AdjustRepeatAfterDirectAsk},
		},
		{
			name: "직접 물은 뒤의 평소 말은 0단계 그대로다",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageNone), state: calm(), directAsk: true,
			stage: crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			name: "직접 물은 뒤의 2단계는 2단계 그대로다(더 올리는 규칙이 아니다)",
			rule: matched(crisis.StageRespond), ai: crisis.AIAnswered(crisis.StageRespond), state: calm(), directAsk: true,
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth,
		},
	})
}

// 5번: 관문에 걸린(최종 1단계 이상) 대화가 2주 안에 이번 대화를 포함해 세 개째면 2단계.
// 발화가 아니라 대화를 센다. 기간은 흐른 시간으로 재고 14일은 336시간이다.
func TestOracleRepeatedWithinTwoWeeks(t *testing.T) {
	check := matched(crisis.StageCheck)
	answered := crisis.AIAnswered(crisis.StageCheck)
	repeated := []crisis.Adjustment{crisis.AdjustRepeatedInWindow}

	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "관문에 걸린 지난 대화가 없으면 이번이 첫 번째 → 1단계",
			rule: check, ai: answered, state: calm(),
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "관문에 걸린 지난 대화가 하나면 이번이 두 번째 → 1단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(2*day, crisis.StageCheck)},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "관문에 걸린 지난 대화가 둘(3일 전, 10일 전)이면 이번이 세 번째 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(3*day, crisis.StageCheck), ago(10*day, crisis.StageCheck)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		},
		{
			// 되묻고 직접 묻는 동안 이 대화에 1단계가 이미 둘 남았다. 모두 "이번 대화" 하나다.
			name: "이번 대화 안에서 몇 분 간격으로 나온 것은 따로 세지 않는다 → 1단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{talk(thisTalk, ago(2*time.Minute, crisis.StageCheck)), talk(thisTalk, ago(9*time.Minute, crisis.StageCheck))},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			// 엿새 전의 대화 하나에 1단계가 셋 남았다. 대화로는 하나라서 이번이 두 번째다.
			name: "지난 대화 하나에 1단계가 세 번 남았어도 대화 하나로 센다 → 1단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				talk("monday", ago(6*day, crisis.StageCheck)), talk("monday", ago(6*day-time.Minute, crisis.StageCheck)),
				talk("monday", ago(6*day-2*time.Minute, crisis.StageCheck)),
			},
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			// 엿새 전의 대화(1단계 둘), 사흘 전의 대화(1단계 하나), 이번 대화(앞서 1단계 하나) → 대화 셋.
			name: "대화가 셋이면 그 안의 발화 수와 상관없이 세 번째 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				talk("monday", ago(6*day, crisis.StageCheck)), talk("monday", ago(6*day-time.Minute, crisis.StageCheck)),
				talk("thursday", ago(3*day, crisis.StageCheck)),
				talk(thisTalk, ago(time.Minute, crisis.StageCheck)),
			},
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		},
		{
			// 딱 336시간 전은 "14일 안"으로 본다. 어느 쪽인지 애매할 때는 낮춰 잡지 않는다.
			name: "하나가 딱 336시간 전이어도 센다 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(336*hour, crisis.StageCheck), ago(1*hour, crisis.StageCheck)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		},
		{
			name: "하나가 336시간에서 1초 더 전이면 세지 않는다 → 1단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(336*hour+time.Second, crisis.StageCheck), ago(1*hour, crisis.StageCheck)},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "한 달 전의 1단계는 몇 번이든 세지 않는다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				ago(30*day, crisis.StageCheck), ago(31*day, crisis.StageCheck), ago(32*day, crisis.StageCheck),
			},
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "0단계였던 지난 판정은 몇 번이든 세지 않는다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				ago(1*day, crisis.StageNone), ago(2*day, crisis.StageNone), ago(3*day, crisis.StageNone),
			},
			stage: crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			// 10일 전의 2단계는 7일이 지나 6번에는 들지 않지만, 관문에 걸린 대화이므로 5번은 센다.
			// 10일 전, 5일 전, 이번 → 세 번째.
			name: "10일 전에 2단계로 끝난 대화와 5일 전의 1단계 → 세 번째 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(10*day, crisis.StageRespond), ago(5*day, crisis.StageCheck)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		},
		{
			// 10일 전의 3단계 하나뿐이면 이번이 두 번째이고, 7일이 지나 6번에도 들지 않는다.
			name: "10일 전에 3단계로 끝난 대화 하나 → 두 번째 → 1단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(10*day, crisis.StageUrgent)},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByBoth,
		},
		{
			name: "네 번째, 다섯 번째도 2단계다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				ago(1*day, crisis.StageCheck), ago(4*day, crisis.StageCheck),
				ago(8*day, crisis.StageCheck), ago(12*day, crisis.StageCheck),
			},
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		},
		{
			name: "이번 말이 0단계면 관문에 걸린 지난 대화가 아무리 많아도 0단계다",
			rule: unmatched(), ai: crisis.AIAnswered(crisis.StageNone), state: calm(),
			history: []crisis.Event{ago(1*day, crisis.StageCheck), ago(2*day, crisis.StageCheck)},
			stage:   crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
	})

	t.Run("횟수를 2로 낮추면 두 번째에 2단계다", func(t *testing.T) {
		p := params.Default().Crisis
		p.RepeatCount = 2
		runGateCases(t, p, []gateCase{{
			name: "관문에 걸린 지난 대화 하나와 이번 대화",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(13*day, crisis.StageCheck)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByBoth, adjustments: repeated,
		}})
	})
}

// 6번: 2단계 이상이 있었던 뒤 7일(168시간) 동안은 1단계 표현도 2단계로 대응한다.
func TestOracleSensitiveWeek(t *testing.T) {
	check := matched(crisis.StageCheck)
	answered := crisis.AIAnswered(crisis.StageNone)
	sensitive := []crisis.Adjustment{crisis.AdjustSensitiveWindow}

	runGateCases(t, params.Default().Crisis, []gateCase{
		{
			name: "이틀 전에 2단계가 있었으면 1단계 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(2*day, crisis.StageRespond)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByRule, adjustments: sensitive,
		},
		{
			name: "이틀 전에 3단계가 있었어도 같다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(2*day, crisis.StageUrgent)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByRule, adjustments: sensitive,
		},
		{
			// 딱 168시간 전은 "7일 안"으로 본다. 어느 쪽인지 애매할 때는 낮춰 잡지 않는다.
			name: "딱 168시간 전의 2단계도 아직 민감한 기간이다 → 2단계",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(168*hour, crisis.StageRespond)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByRule, adjustments: sensitive,
		},
		{
			name: "168시간에서 1초 더 지났으면 1단계다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(168*hour+time.Second, crisis.StageRespond)},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByRule,
		},
		{
			name: "지난 1단계 하나는 민감한 기간을 만들지 않는다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{ago(1*day, crisis.StageCheck)},
			stage:   crisis.StageCheck, detectedBy: crisis.DetectedByRule,
		},
		{
			name: "민감한 기간에도 0단계 말은 0단계다",
			rule: unmatched(), ai: answered, state: calm(),
			history: []crisis.Event{ago(1*day, crisis.StageUrgent)},
			stage:   crisis.StageNone, detectedBy: crisis.DetectedByNone,
		},
		{
			name: "민감한 기간의 2단계는 2단계 그대로다. 3단계로 올리는 규칙이 아니다",
			rule: matched(crisis.StageRespond), ai: answered, state: calm(),
			history: []crisis.Event{ago(1*day, crisis.StageUrgent)},
			stage:   crisis.StageRespond, detectedBy: crisis.DetectedByRule,
		},
		{
			// 5번이 6번보다 먼저다. 둘 다 맞으면 먼저 맞은 5번만 단계를 바꾼 규칙으로 남는다.
			name: "세 번째이면서 민감한 기간이기도 하면 2단계이고 바꾼 규칙은 앞의 것이다",
			rule: check, ai: answered, state: calm(),
			history: []crisis.Event{
				ago(1*day, crisis.StageRespond), ago(2*day, crisis.StageCheck), ago(3*day, crisis.StageCheck),
			},
			stage: crisis.StageRespond, detectedBy: crisis.DetectedByRule,
			adjustments: []crisis.Adjustment{crisis.AdjustRepeatedInWindow},
		},
	})
}

// 지난 판정의 순서는 결과를 바꾸지 못한다.
func TestOracleHistoryOrderDoesNotMatter(t *testing.T) {
	events := []crisis.Event{
		ago(20*day, crisis.StageRespond), ago(3*day, crisis.StageCheck), ago(9*day, crisis.StageNone), ago(10*day, crisis.StageCheck),
	}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {1, 3, 0, 2}, {2, 0, 3, 1}}

	for _, order := range orders {
		history := make([]crisis.Event, 0, len(events))
		for _, idx := range order {
			history = append(history, events[idx])
		}
		got, err := crisis.Decide(crisis.Input{
			Rule:           matched(crisis.StageCheck),
			AI:             crisis.AIAnswered(crisis.StageCheck),
			ConversationID: thisTalk,
			History:        history,
			Now:            now(),
		}, params.Default().Crisis)
		require.NoError(t, err)

		// 관문에 걸린 대화는 3일 전, 10일 전, 이번 → 세 번째 → 2단계. 20일 전의 2단계와 9일 전의 0단계는 어디에도 들지 않는다.
		assert.Equal(t, crisis.StageRespond, got.Stage)
		assert.Equal(t, []crisis.Adjustment{crisis.AdjustRepeatedInWindow}, got.Adjustments)
	}
}

// 판정의 근거는 그대로 저장할 수 있는 꼴로 나온다.
func TestOracleDecisionRecord(t *testing.T) {
	// 사전에 걸렸지만 규칙은 0단계로 본 말, AI 실패 → 2번으로 1단계. 이 대화에서 이미 직접 물었으므로 4번으로 2단계.
	got, err := crisis.Decide(crisis.Input{
		Rule:           matched(crisis.StageNone),
		AI:             crisis.AIFailed(),
		State:          changed(),
		DirectAskDone:  true,
		ConversationID: thisTalk,
		Now:            now(),
	}, params.Default().Crisis)
	require.NoError(t, err)

	assert.Equal(t, crisis.StageRespond, got.Stage)
	assert.Equal(t, "rule", got.DetectedBy.String())
	assert.Equal(t, []string{"ai_failed_floor", "repeat_after_direct_ask"}, got.AdjustmentIDs())
}

// 틀린 입력은 조용히 0단계로 넘기지 않고 오류로 알린다.
func TestOracleRejectsBrokenInput(t *testing.T) {
	t.Run("판정 시각이 비어 있으면 오류다", func(t *testing.T) {
		_, err := crisis.Decide(crisis.Input{Rule: matched(crisis.StageRespond), AI: crisis.AIFailed()}, params.Default().Crisis)
		require.ErrorIs(t, err, crisis.ErrMissingNow)
	})

	t.Run("답하지 못했다면서 단계가 채워진 AI 판정은 오류다", func(t *testing.T) {
		_, err := crisis.Decide(crisis.Input{
			AI:  crisis.AIResult{Answered: false, Stage: crisis.StageRespond},
			Now: now(),
		}, params.Default().Crisis)
		require.ErrorIs(t, err, crisis.ErrInconsistentAI)
	})

	t.Run("대화 식별자가 비어 있으면 오류다. 판정은 그래도 나온다", func(t *testing.T) {
		got, err := crisis.Decide(crisis.Input{
			Rule: matched(crisis.StageRespond), AI: crisis.AIAnswered(crisis.StageRespond), Now: now(),
		}, params.Default().Crisis)
		require.ErrorIs(t, err, crisis.ErrMissingConversationID)
		assert.Equal(t, crisis.StageRespond, got.Stage)

		_, err = crisis.Decide(crisis.Input{
			Rule: matched(crisis.StageCheck), AI: crisis.AIAnswered(crisis.StageCheck), ConversationID: thisTalk, Now: now(),
			History: []crisis.Event{{At: now().Add(-day), Stage: crisis.StageCheck}},
		}, params.Default().Crisis)
		require.ErrorIs(t, err, crisis.ErrMissingConversationID)
		var eventErr *crisis.EventError
		require.ErrorAs(t, err, &eventErr)
		assert.Equal(t, 0, eventErr.Index)
	})

	t.Run("0부터 3이 아닌 단계는 받지 않는다", func(t *testing.T) {
		_, err := crisis.StageFromInt(4)
		require.ErrorIs(t, err, crisis.ErrInvalidStage)
		_, err = crisis.StageFromInt(-1)
		require.ErrorIs(t, err, crisis.ErrInvalidStage)

		_, err = crisis.Decide(crisis.Input{Rule: crisis.RuleResult{Stage: 4}, AI: crisis.AIFailed(), Now: now()}, params.Default().Crisis)
		require.ErrorIs(t, err, crisis.ErrInvalidStage)
	})
}
