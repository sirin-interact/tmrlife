// Package crisis는 위기 관문의 마지막 판정을 맡는다.
//
// 사용자의 모든 발화는 AI가 답하기 전에 관문을 거친다. 관문은 두 겹이다.
// 하나는 정해진 표현을 찾는 규칙이고, 다른 하나는 대화를 만드는 AI와 분리된 별도의 AI 판별이다.
// 표현을 찾는 일과 AI를 부르는 일은 이 패키지 밖에서 한다. 여기서는 두 판정과 그 사람의 최근 상태,
// 지난 판정들을 받아 최종 단계를 정하고, 어떤 규칙이 단계를 바꿨는지를 함께 돌려준다.
//
// # 규칙과 순서
//
//  1. 두 겹 가운데 높은 쪽을 따른다. 어느 한쪽이라도 무겁게 봤으면 무겁게 다룬다.
//  2. AI 판별이 실패했거나 제때 오지 않았고 규칙에 걸린 발화면 적어도 확인 단계다. 조용히 통과시키지 않는다.
//  3. 추정 점수가 기준 이상이거나 변화 감지 상태면 한 단계 올린다. 해당 없음은 올리지 않고, 긴급이 천장이다.
//     2번만으로 확인 단계가 된 발화도 올리지 않는다.
//  4. 아직 확인 단계이고 이 대화에서 이미 직접 물었으면 대응 단계다.
//  5. 아직 확인 단계이고, 최근 기간 안에 관문에 걸린(최종 확인 단계 이상) 대화가 이번 대화를 포함해 정해진 수에 닿았으면 대응 단계다.
//  6. 아직 확인 단계이고 최근에 대응 단계 이상이 있었으면 대응 단계다.
//
// 3번에서 해당 없음을 올리지 않는 이유: 올리면 상태가 나쁜 동안에는 "배고파 죽겠다"를 포함한 모든 말이
// 확인 대상이 된다. 그런 반응을 겪은 사람은 말을 고르기 시작하고, 정작 필요한 순간에 말하지 않게 된다.
//
// 3번에서 2번만으로 확인 단계가 된 발화를 올리지 않는 이유: 그 말은 규칙이 관용 표현으로 보고 해당 없음을 준 말이다.
// AI 판별이 멈춘 동안 다시 봐줄 눈이 없어서 되묻는 데까지만 올린 것인데, 거기에 한 단계를 더하면
// "배고파 죽겠다"에 상담 전화 안내가 나간다. 되묻는 것까지는 괜찮고, 그 이상은 지나치다.
// 4~6번은 그런 말에도 그대로 적용한다. 그 규칙들이 보는 것은 상태가 아니라 되풀이이기 때문이다.
//
// 3번을 4~6번보다 먼저 적용하는 이유: 4~6번은 "아직 확인 단계"일 때만 움직이므로,
// 여러 뜻으로 읽히는 표현은 어떤 조건이 겹쳐도 대응 단계까지만 오른다.
// 긴급 단계는 규칙이나 AI 판별이 대응 단계 이상으로 본 말이 3번으로 올라갈 때만 나온다.
// 애매한 말 한마디에 긴급 연결을 맨 앞에 내미는 일이 없게 하려는 것이다.
//
// "애매하면 한 단계 위로"와 "앞뒤 대화를 함께 본다"는 AI 판별의 지시문이 맡는다.
// 여기서는 두 겹 가운데 높은 쪽을 따르는 것으로 반영된다. 이 패키지의 규칙은 단계를 올리기만 하고 내리지 않는다.
//
// # 5번이 세는 것
//
//   - 발화가 아니라 대화를 센다. 확인 단계의 대응은 되묻고, 그래도 애매하면 직접 묻는 두 걸음이라
//     한 대화 안에서 애매한 말이 두 번 나오는 것이 정상이다. 발화를 세면 한 번의 확인이 두 번으로 세어져,
//     다음 대화의 첫 애매한 표현이 되묻는 걸음 없이 곧바로 대응 단계가 된다.
//     같은 대화에서 나온 판정은 몇 개든 하나로 세고, 지금 판정하는 대화의 앞선 판정은 "이번 대화" 하나에 이미 들어 있다.
//   - 최종 단계가 확인 이상이었던 대화를 모두 센다. 애매한 표현이 상태가 나빠서 대응 단계로 올라갔더라도
//     "애매한 표현이 나온 일"이라는 점은 같다. 최종 단계가 확인인 것만 세면 상태가 나쁜 사람일수록 이 규칙에 덜 걸린다.
//
// # 입력이 틀렸을 때
//
// Decide는 오류와 함께 돌려주는 판정도 채워서 준다. 관문은 모든 발화가 거치는 자리라서,
// 지난 판정 한 줄이 틀렸다고 그 사람의 발화가 모두 "판정 없음"이 되면 관문이 꺼진 것과 같다.
// 읽을 수 있는 것만으로 끝까지 판정하고, 읽을 수 없는 것은 단계를 낮추는 쪽으로 쓰지 않는다. 자세한 것은 Decide에 있다.
//
// # 기간을 재는 법
//
// 5번과 6번의 기간은 달력 날짜가 아니라 판정 시각에서 거슬러 흐른 시간으로 잰다.
// 14일은 336시간, 7일은 168시간이다. 판정은 발화마다 시각과 함께 남고,
// "얼마 전에 있었나"를 묻는 규칙이라 하루의 경계나 시간대에 따라 결과가 달라지면 안 되기 때문이다.
package crisis

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// RuleResult는 규칙(표현 사전)의 판정이다.
type RuleResult struct {
	// Stage는 규칙이 본 단계다.
	Stage Stage

	// Matched는 사전의 표현이 하나라도 걸렸는지다.
	//
	// 걸렸지만 관용 표현으로 보고 해당 없음을 준 경우에도 true여야 한다. "규칙에 걸린 발화"는 규칙이 무겁게 본 발화가 아니라
	// 사전에 닿은 발화 전부다. 그런 말은 평소에는 AI 판별이 한 번 더 보는데,
	// AI 판별이 실패하면 다시 봐줄 눈이 없으므로 확인 단계로 올린다. 관용 표현이라고 false로 넘기면 그 바닥이 사라진다.
	// Stage가 확인 이상이면 이 값과 상관없이 걸린 것으로 본다.
	Matched bool
}

// AIResult는 AI 판별의 판정이다.
//
// 빈 값은 "답을 받지 못함"이다. 오류를 처리하는 자리에서 빈 값을 넘기면 그대로 실패로 읽힌다.
// 빈 값이 "해당 없음이라고 답함"이었다면, 실패 표시를 잊은 코드가 걸러야 할 말을 조용히 통과시킨다.
type AIResult struct {
	// Answered는 AI 판별이 제때 답했는지다. 실패, 시간 초과, 읽을 수 없는 답은 모두 false다.
	Answered bool
	// Stage는 AI 판별이 본 단계다. Answered가 false면 비워 둔다.
	Stage Stage
}

// AIAnswered는 AI 판별이 제때 답한 경우의 값을 만든다.
func AIAnswered(stage Stage) AIResult {
	return AIResult{Answered: true, Stage: stage}
}

// AIFailed는 AI 판별이 실패했거나 제때 오지 않은 경우의 값을 만든다. 빈 값과 같다.
func AIFailed() AIResult {
	return AIResult{}
}

// State는 발화를 둘러싼 그 사람의 최근 상태다. 상태가 나쁜 동안에는 같은 말도 더 무겁게 듣는다.
//
// 기록이 모자라 점수를 계산하지 않은 동안에는 ScoreElevated를 false로 두고 변화 감지만 채운다.
// 기록이 모자라도 관문은 평소대로 돌아야 한다.
type State struct {
	// ScoreElevated는 추정 점수가 기준(기본 10) 이상인지다. ScoreElevated 함수로 구한다.
	ScoreElevated bool
	// ChangeDetected는 평소에서 벗어난 상태가 이어지고 있는지(변화 감지)다.
	ChangeDetected bool
}

// ScoreElevated는 추정 점수가 관문의 판정을 한 단계 올릴 기준 이상인지 알려준다.
// 기준을 관문 밖에서 따로 비교하지 않도록 여기에 둔다. 점수가 없을 때는 부르지 않고 false로 둔다.
func ScoreElevated(score int, p params.Crisis) bool {
	return score >= p.EscalationMinScore
}

// Bad는 판정을 한 단계 올릴 상태인지 알려준다. 두 조건 가운데 하나만 맞아도 되고, 둘 다 맞아도 한 단계만 올린다.
func (s State) Bad() bool {
	return s.ScoreElevated || s.ChangeDetected
}

// Event는 지난 관문 판정 하나다.
type Event struct {
	// At은 그 판정이 내려진 시각이다.
	At time.Time
	// Stage는 그때의 최종 단계다. 규칙이나 AI가 따로 본 단계가 아니라, 모든 규칙을 거친 뒤의 단계다.
	Stage Stage
	// ConversationID는 그 판정이 나온 대화를 가리키는 식별자다. 같은 대화인지 견주는 데만 쓰고 뜻은 읽지 않는다.
	// 비워 두면 어느 대화인지 알 수 없는 판정이라 오류로 알리고, 따로 떨어진 대화 하나로 센다.
	ConversationID string
}

// Input은 발화 하나를 판정하는 데 필요한 전부다.
type Input struct {
	Rule  RuleResult
	AI    AIResult
	State State

	// DirectAskDone은 이 대화에서 이미 한 번 직접 물었는지다.
	// 확인 단계에서는 먼저 되물어 듣고, 그래도 뜻이 풀리지 않을 때만 돌려 말하지 않고 묻는다.
	// 그 질문은 한 대화에서 한 번뿐이다. 묻고 난 뒤에 확인 단계의 표현이 다시 나오면 대응 단계로 본다.
	DirectAskDone bool

	// ConversationID는 지금 판정하는 발화가 속한 대화의 식별자다. Event.ConversationID와 같은 값으로 견준다.
	// 비워 두면 오류로 알린다. 그때는 지난 판정 가운데 어느 것이 이 대화의 것인지 가릴 수 없으므로
	// 지난 대화를 모두 다른 대화로 센다.
	ConversationID string

	// History는 이 사용자의 지난 판정들이다. 같은 대화의 앞선 발화도 든다. 순서는 상관없다.
	//
	// 판정 시각에서 적어도 HistoryHorizon만큼 거슬러 올라간 판정을 모두 넘긴다.
	// 그보다 짧게 읽어 오면 쌓임을 보는 규칙이 오류 없이 조용히 꺼진다.
	// 해당 없음인 판정이 섞여 있어도 된다(세지 않는다). 지금 판정하는 발화 자신은 넣지 않는다.
	// 사용자가 하루의 기록을 지우면 그날의 판정도 함께 지워지므로 여기에 들어오지 않는다.
	History []Event

	// Now는 이 발화를 판정하는 시각이다. 시계를 읽지 않으므로 반드시 채워서 넘긴다.
	Now time.Time
}

// Decision은 관문의 최종 판정이다.
type Decision struct {
	// Stage는 최종 단계다.
	Stage Stage
	// DetectedBy는 규칙과 AI 판별 가운데 어느 쪽이 잡았는지다. Stage가 해당 없음일 때만 none이다.
	DetectedBy DetectedBy
	// Adjustments는 두 판정을 합친 뒤에 단계를 실제로 바꾼 규칙들이다. 적용 순서대로이고, 없으면 빈 슬라이스다.
	// 조건은 맞았지만 단계를 바꾸지 못한 규칙(이미 긴급이어서 더 올릴 수 없는 경우 등)은 담지 않는다.
	Adjustments []Adjustment
}

// AdjustmentIDs는 단계를 바꾼 규칙의 식별자를 적용 순서대로 돌려준다. 판정 기록의 문자열 배열에 그대로 넣는다.
// 바꾼 규칙이 없어도 nil이 아니라 빈 슬라이스다. nil은 저장할 때 "값 없음"으로 옮겨질 수 있다.
func (d Decision) AdjustmentIDs() []string {
	ids := make([]string, 0, len(d.Adjustments))
	for _, a := range d.Adjustments {
		ids = append(ids, a.String())
	}
	return ids
}

// Decide는 발화 하나의 최종 단계를 정한다. 규칙과 순서는 패키지 설명에 있다.
//
// 같은 입력에서는 언제나 같은 결과가 나온다. in.History를 고치지 않는다.
//
// 오류는 입력이나 조정 값이 틀렸다는 뜻이다. 여러 가지가 틀렸으면 errors.Join으로 묶어 돌려준다.
//   - 단계가 0부터 3이 아님: ErrInvalidStage (지난 판정이면 *EventError로 자리를 알려준다)
//   - AI 판별이 답하지 못했다면서 단계가 채워져 있음: ErrInconsistentAI
//   - 판정 시각이 비어 있음: ErrMissingNow, 지난 판정의 시각이 비어 있음: ErrMissingEventTime
//   - 이번 대화나 지난 판정의 대화 식별자가 비어 있음: ErrMissingConversationID (지난 판정이면 *EventError로 자리를 알려준다)
//   - 조정 값이 범위를 벗어남: ErrInvalidParams (안에 *params.FieldError)
//
// 오류와 함께 돌려주는 Decision도 빈 값이 아니다. 읽을 수 있는 입력만으로 끝까지 판정한 값이고,
// 부르는 쪽은 오류를 알리되 이 판정대로 대응한다. 오류가 난 발화를 해당 없음으로 넘기면 안 된다.
// 읽을 수 없는 입력은 이렇게 다룬다. 단계를 낮추는 쪽으로는 쓰지 않는다.
//   - 규칙의 단계를 읽을 수 없으면 규칙이 무엇을 봤는지 모르는 것이다. 걸린 말로 보고 적어도 확인 단계로 둔다.
//     3번이 "2번만으로 확인 단계가 된 말"을 올리지 않는 것은 규칙이 해당 없음으로 본 것을 알 때뿐이다.
//     단계를 읽지 못한 말은 규칙이 확인 단계로 봤을 수도 있고, 그랬다면 상태가 나쁠 때 대응 단계가 되었을 말이다.
//     그래서 AI 판별까지 답하지 못했어도 상태가 나쁘면 한 단계 올린다.
//     AdjustAIFailedFloor와 AdjustBadStatePlusOne이 함께 적히는 것은 이 경우뿐이다.
//   - AI 판별의 단계를 읽을 수 없으면 답하지 못한 것으로 본다. 규칙에 걸린 말이면 2번이 그대로 적용된다.
//   - AI 판별이 답하지 못했다면서 단계가 채워져 있으면 그 단계를 버리지 않고 답한 것으로 본다.
//   - 단계나 시각을 읽을 수 없는 지난 판정은 그것만 빼고 센다. 오류에는 가장 앞선 자리의 것 하나를 담는다.
//   - 대화 식별자만 빠진 지난 판정은 빼지 않는다. 언제 어떤 단계였는지는 알고 있으므로 6번에는 그대로 쓰고,
//     5번에서는 다른 어느 대화와도 겹치지 않는 대화 하나로 센다. 이번 대화의 식별자가 빠졌을 때도 지난 대화를 덜 세지 않는다.
//   - 판정 시각이 없거나 조정 값이 틀렸으면 지난 판정을 보는 5번과 6번은 적용하지 못한다. 1번부터 4번까지만 적용한다.
func Decide(in Input, p params.Crisis) (Decision, error) {
	view, err := read(in, p)

	adjustments := make([]Adjustment, 0, 2)

	// 1. 두 겹 가운데 높은 쪽을 따른다. 답하지 못한 AI 판별은 비교에 넣지 않는다.
	stage := view.rule
	if view.aiAnswered && view.ai > stage {
		stage = view.ai
	}

	// 2. AI 판별이 답하지 못했고 규칙에 걸린 발화면 적어도 확인 단계다.
	// 규칙이 이미 확인 이상으로 봤다면 1번에서 그 단계가 되었으므로, 여기서 바뀌는 것은
	// "걸렸지만 규칙은 해당 없음으로 본 말"뿐이다.
	floored := false
	// onlyByFloor는 규칙이 해당 없음으로 본 것을 알고 있고, 이 바닥만으로 확인 단계가 된 말이라는 뜻이다.
	// 규칙의 단계를 읽지 못한 말은 규칙이 무엇을 봤는지 모르므로 여기에 넣지 않는다. 그런 말은 3번에서 봐주지 않는다.
	onlyByFloor := false
	if !view.aiAnswered && view.ruleMatched && stage < StageCheck {
		stage = StageCheck
		floored = true
		onlyByFloor = !view.ruleUnreadable
		adjustments = append(adjustments, AdjustAIFailedFloor)
	}
	// 규칙의 단계를 읽지 못한 발화는 AI 판별이 가볍게 봤어도 확인 단계 아래로 두지 않는다.
	// 정해진 규칙이 아니라 틀린 입력 때문에 생긴 바닥이라 Adjustments에는 적지 않는다. 까닭은 함께 돌려주는 오류에 있다.
	if view.ruleUnreadable && stage < StageCheck {
		stage = StageCheck
		floored = true
	}

	// 어느 쪽이 잡았는지는 두 겹의 판정만으로 정한다. 뒤의 규칙들은 이미 잡힌 말의 단계를 올릴 뿐이다.
	// 2번으로 올라간 말은 규칙에 걸렸기 때문에 올라간 것이므로 규칙이 잡은 것으로 적는다.
	detected := detectedBy(
		view.rule >= StageCheck || floored,
		view.aiAnswered && view.ai >= StageCheck,
	)

	// 3. 상태가 나쁘면 한 단계 올린다. 해당 없음은 올리지 않고, 긴급보다 위는 없다.
	// 2번만으로 확인 단계가 된 말은 규칙이 관용 표현으로 본 말이라 되묻는 데서 멈춘다.
	if in.State.Bad() && stage >= StageCheck && stage < StageUrgent && !onlyByFloor {
		stage++
		adjustments = append(adjustments, AdjustBadStatePlusOne)
	}

	// 4~6. 아직 확인 단계일 때만 본다. 하나가 대응 단계로 올리면 나머지는 볼 것이 없으므로 먼저 맞은 규칙만 남는다.
	if stage == StageCheck {
		if adj, ok := raiseCheck(in.DirectAskDone, in.ConversationID, view, p); ok {
			stage = StageRespond
			adjustments = append(adjustments, adj)
		}
	}

	return Decision{Stage: stage, DetectedBy: detected, Adjustments: adjustments}, err
}

// reading은 입력에서 판정에 쓸 수 있는 것만 골라낸 값이다. 틀린 값은 여기까지 오지 못한다.
type reading struct {
	rule Stage
	// ruleMatched는 사전에 걸린 말인지다. 규칙의 단계를 읽지 못했을 때도 참이다.
	ruleMatched bool
	// ruleUnreadable은 규칙의 단계가 정해진 값이 아니어서 읽지 못했다는 뜻이다.
	ruleUnreadable bool

	aiAnswered bool
	ai         Stage

	// history는 단계와 시각을 모두 읽을 수 있는 지난 판정이다. 대화 식별자만 빠진 판정은 남아 있다.
	history []Event
	now     time.Time
	// historyUsable은 지난 판정을 보는 규칙을 적용할 수 있는지다. 판정 시각이 없거나 조정 값이 틀렸으면 거짓이다.
	historyUsable bool
}

// read는 입력을 검사하면서 쓸 수 있는 값만 골라낸다. 오류가 있어도 멈추지 않는다.
// 틀린 곳이 하나면 그 오류를 그대로, 여럿이면 errors.Join으로 묶어 돌려준다.
func read(in Input, p params.Crisis) (reading, error) {
	var errs []error
	view := reading{now: in.Now, historyUsable: true}

	if err := checkParams(p); err != nil {
		errs = append(errs, err)
		view.historyUsable = false
	}

	if in.Rule.Stage.Valid() {
		view.rule, view.ruleMatched = in.Rule.Stage, in.Rule.Matched
	} else {
		errs = append(errs, fmt.Errorf("rule result: %w", ErrInvalidStage))
		view.ruleMatched, view.ruleUnreadable = true, true
	}

	switch {
	case in.AI.Answered && in.AI.Stage.Valid():
		view.aiAnswered, view.ai = true, in.AI.Stage
	case in.AI.Answered:
		// 읽을 수 없는 단계는 쓰지 못한다. 답하지 못한 것으로 둔다.
		errs = append(errs, fmt.Errorf("ai result: %w", ErrInvalidStage))
	case in.AI.Stage != StageNone:
		// Answered를 빠뜨린 것일 수 있다. 읽을 수 있는 단계라면 버리지 않고 답한 것으로 본다.
		errs = append(errs, ErrInconsistentAI)
		if in.AI.Stage.Valid() {
			view.aiAnswered, view.ai = true, in.AI.Stage
		}
	}

	if in.Now.IsZero() {
		errs = append(errs, ErrMissingNow)
		view.historyUsable = false
	}
	if in.ConversationID == "" {
		errs = append(errs, fmt.Errorf("input: %w", ErrMissingConversationID))
	}

	// 기간 밖이라 세지 않을 판정이라도 틀린 값이면 알린다. 입력을 만든 쪽의 잘못을 일찍 드러내기 위해서다.
	// 틀린 판정이 없으면 받은 목록을 그대로 쓴다. 있으면 맞는 것만 새 목록에 옮긴다. 받은 목록은 고치지 않는다.
	view.history = in.History
	for i, e := range in.History {
		cause := eventProblem(e)
		if cause == nil {
			continue
		}
		// 틀린 것이 여럿이어도 가장 앞선 자리의 것 하나만 알린다. 빼는 것은 단계나 시각을 읽을 수 없는 것 전부다.
		errs = append(errs, &EventError{Index: i, Err: cause})
		view.history = placeableEvents(in.History)
		break
	}

	switch len(errs) {
	case 0:
		return view, nil
	case 1:
		return view, errs[0]
	default:
		return view, errors.Join(errs...)
	}
}

// eventProblem은 지난 판정 하나에서 틀린 곳을 알려준다. 없으면 nil이다.
func eventProblem(e Event) error {
	switch {
	case !e.Stage.Valid():
		return ErrInvalidStage
	case e.At.IsZero():
		return ErrMissingEventTime
	case e.ConversationID == "":
		return ErrMissingConversationID
	default:
		return nil
	}
}

// placeable은 지난 판정이 언제 어떤 단계였는지 읽을 수 있는지 알려준다. 대화 식별자는 보지 않는다.
func placeable(e Event) bool {
	return e.Stage.Valid() && !e.At.IsZero()
}

// placeableEvents는 단계와 시각을 모두 읽을 수 있는 지난 판정만 새 목록에 담는다.
func placeableEvents(history []Event) []Event {
	kept := make([]Event, 0, len(history))
	for _, e := range history {
		if placeable(e) {
			kept = append(kept, e)
		}
	}
	return kept
}

// HistoryHorizon은 발화 하나를 판정할 때 지난 판정을 얼마나 거슬러 읽어 와야 하는지다.
// 쌓임을 보는 기간과 민감한 기간 가운데 긴 쪽이다. 판정 시각에서 이만큼 전까지(경계 포함)의 판정을 Input.History에 넘긴다.
//
// 읽어 오는 쪽이 기간을 따로 적어 두면, 조정 값을 늘렸을 때 읽어 오는 범위만 그대로 남아 규칙이 조용히 꺼진다.
func HistoryHorizon(p params.Crisis) time.Duration {
	return windowOf(max(p.RepeatWindowDays, p.SensitiveWindowDays))
}

// raiseCheck는 확인 단계의 발화를 대응 단계로 올릴 규칙이 있는지 순서대로 본다.
func raiseCheck(directAskDone bool, conversationID string, view reading, p params.Crisis) (Adjustment, bool) {
	// 4. 직접 묻고 난 뒤에 다시 나온 확인 단계의 표현은 그때의 답과 상관없이 무겁게 본다.
	if directAskDone {
		return AdjustRepeatAfterDirectAsk, true
	}
	if !view.historyUsable {
		return 0, false
	}

	// 5. 최근 기간 안에 관문에 걸린 대화가 이번 대화를 포함해 정해진 수에 닿았는지 본다.
	if flaggedConversations(conversationID, view, windowOf(p.RepeatWindowDays)) >= p.RepeatCount {
		return AdjustRepeatedInWindow, true
	}

	// 6. 대응 단계 이상이 있은 뒤 얼마 동안은 확인 단계의 표현도 대응 단계로 다룬다.
	sensitiveWindow := windowOf(p.SensitiveWindowDays)
	for _, e := range view.history {
		if e.Stage >= StageRespond && happenedWithin(e.At, view.now, sensitiveWindow) {
			return AdjustSensitiveWindow, true
		}
	}

	return 0, false
}

// flaggedConversations는 기간 안에 관문에 걸린(최종 확인 단계 이상) 대화가 이번 대화를 포함해 몇 개인지 센다.
//
// 같은 대화에서 나온 판정은 몇 개든 하나다. 이번 대화의 앞선 판정은 처음에 센 "이번 대화"에 이미 들어 있다.
// 대화 식별자가 빠진 판정은 어느 대화와도 겹치지 않는 대화 하나로 센다. 모르는 것을 덜 세는 쪽으로 쓰지 않는다.
func flaggedConversations(current string, view reading, window time.Duration) int {
	count := 1 // 이번 대화
	var seen map[string]struct{}
	for _, e := range view.history {
		if e.Stage < StageCheck || !happenedWithin(e.At, view.now, window) {
			continue
		}
		if e.ConversationID == "" {
			count++
			continue
		}
		if e.ConversationID == current {
			continue
		}
		if _, dup := seen[e.ConversationID]; dup {
			continue
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		seen[e.ConversationID] = struct{}{}
		count++
	}
	return count
}

// happenedWithin은 사건이 판정 시각에서 거슬러 window 안에 있었는지 본다.
//
//   - 판정 시각보다 뒤의 사건은 세지 않는다. 그 시각에는 아직 일어나지 않은 일이다.
//     지난 기록 전체를 넘기고 과거의 어느 시각을 기준으로 다시 돌려도 그때와 같은 판정이 나오게 하려는 것이다.
//   - 판정 시각과 같은 시각의 사건은 센다. 흐른 시간이 0일 뿐 이미 일어난 일이다.
//   - 정확히 경계에 걸린 사건(기본값으로는 336시간 전, 168시간 전)은 창 안으로 본다.
//     어느 쪽인지 애매할 때는 낮춰 잡지 않는다.
func happenedWithin(at, now time.Time, window time.Duration) bool {
	if at.After(now) {
		return false
	}
	// 두 시각이 표현할 수 없을 만큼 멀면 Sub는 가장 긴 시간을 돌려준다. 넘쳐서 음수가 되는 일은 없다.
	return now.Sub(at) <= window
}

// 기간의 하루는 달력의 하루가 아니라 24시간이다.
const day = 24 * time.Hour

// windowOf는 일수를 흐른 시간으로 옮긴다.
// 일수가 터무니없이 커서 시간으로 옮길 수 없으면 표현할 수 있는 가장 긴 시간으로 둔다.
// 넘쳐서 음수가 되면 창이 사라져 규칙이 조용히 꺼지기 때문이다.
func windowOf(days int) time.Duration {
	if int64(days) > math.MaxInt64/int64(day) {
		return math.MaxInt64
	}
	return time.Duration(days) * day
}

func detectedBy(rule, ai bool) DetectedBy {
	switch {
	case rule && ai:
		return DetectedByBoth
	case rule:
		return DetectedByRule
	case ai:
		return DetectedByAI
	default:
		return DetectedByNone
	}
}

// checkParams는 이 패키지가 쓰는 조정 값만 다시 본다. 조건은 params.Params.Validate와 같다.
// 기간이 0이거나 횟수가 1이면 오류 없이도 계산은 되지만 규칙이 꺼지거나 모든 말이 곧바로 대응 단계가 된다.
// 안전을 맡은 규칙이 조용히 달라지는 것보다 멈추고 알리는 쪽이 낫다.
func checkParams(p params.Crisis) error {
	checks := []struct {
		field string
		got   int
		floor int
	}{
		{"Crisis.RepeatWindowDays", p.RepeatWindowDays, 1},
		{"Crisis.RepeatCount", p.RepeatCount, 2},
		{"Crisis.SensitiveWindowDays", p.SensitiveWindowDays, 1},
	}
	for _, c := range checks {
		if c.got < c.floor {
			return fmt.Errorf("%w: %w", ErrInvalidParams, &params.FieldError{
				Field:  c.field,
				Reason: fmt.Sprintf("must be at least %d, got %d", c.floor, c.got),
			})
		}
	}
	return nil
}
