package reply

import (
	"strings"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// toMessages는 대화를 모델에 보낼 꼴로 옮긴다.
// 같은 쪽의 말이 연달아 있으면 하나로 잇는다(첫 안부 뒤에 나간 "아직 거기 있어요?"처럼). 번갈아 오지 않는 대화를 받지 않는 모델이 있다.
func toMessages(turns []Turn) []ai.Message {
	out := make([]ai.Message, 0, len(turns))
	for _, tn := range turns {
		role := ai.RoleUser
		if tn.Speaker == SpeakerAI {
			role = ai.RoleModel
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text += "\n" + tn.Text
			continue
		}
		out = append(out, ai.Message{Role: role, Text: tn.Text})
	}
	return out
}

const (
	hintHeading    = "# 이번 답에서 특히 지킬 것"
	noQuestionHint = "- 바로 앞의 두 번을 모두 질문으로 끝냈다. 이번에는 묻지 않는다. 물음표를 쓰지 않고, 궁금하다는 말도 하지 않고, 들은 말에 반응만 한다."
	retryHint      = "- 방금 만든 답은 아래 까닭으로 쓸 수 없었다. 같은 실수 없이 새로 말한다."
)

// correctiveHints는 걸린 검사마다 모델에게 돌려줄 말이다. 버린 답의 글은 돌려주지 않는다. 같은 말에 다시 끌려가기 쉽다.
var correctiveHints = map[Rule]string{
	RuleEmpty:              "아무 말도 하지 않았다. 한두 문장으로 말한다.",
	RuleTooManySentences:   "문장이 셋 이상이었다. 한 문장이나 두 문장으로 줄인다.",
	RuleTooLong:            "너무 길었다. 훨씬 짧게, 말하듯이 한다.",
	RuleTooManyQuestions:   "물음표가 둘 이상이었다. 묻는 것은 하나만 남기고, 되받는 말은 물음표 없이 끝낸다.",
	RuleQuestionNotAllowed: "묻지 않아야 하는데 물었다. 물음표를 쓰지 않고 반응만 한다.",
	RulePhoneNumber:        "전화번호를 말했다. 번호는 말하지 않는다.",
	RuleReferral:           "기관, 전문가, 상담, 병원, 치료 같은 말을 꺼냈다. 그런 안내는 하지 않고 사용자의 말만 받는다.",
	RulePermissionAsking:   "물어봐도 되는지 허락을 구했다. 허락을 구하지 않고 그냥 말한다.",
	RuleSecondPerson:       "사용자를 \"당신\"이라고 불렀다. 부르는 말 없이 말한다.",
	RuleDiagnosis:          "병 이름이나 진단, 증상 같은 말을 썼거나 맞다 아니다를 판정했다. 상태에 이름을 붙이지도 판정하지도 않는다.",
	RuleSymbol:             "이모지, 특수문자, 자모, 줄바꿈 가운데 하나를 썼다. 한글과 쉼표, 마침표, 물음표, 느낌표만 쓴다.",
	RuleForeignScript:      "한글이 아닌 글자가 섞였다. 한국어로만 말한다.",
	RuleMissingQuestion:    "묻는 말이 없었다. 무슨 일이 있었는지 물음표로 하나 묻는다.",
	RuleNotMirrored:        "사용자의 표현을 받지 않았다. 사용자가 쓴 낱말을 그대로 넣어 되묻는다.",
}

// request는 모델에 보낼 요청을 만든다. rejected는 바로 앞의 시도가 걸린 검사다.
//
// 그 턴에만 해당하는 지시는 지시문 끝에 덧붙인다. 사용자의 말 사이에 끼워 넣으면 모델이 그것을 사용자의 말로 읽는다.
func (g *Generator) request(t turn, rejected []Violation) ai.Request {
	req := t.prompt.Request(append([]ai.Message(nil), t.messages...))
	req.MaxOutputTokens = g.opts.MaxOutputTokens
	req.Thinking = g.opts.Thinking

	var hints []string
	if t.noQuestion {
		hints = append(hints, noQuestionHint)
	}
	if len(rejected) > 0 {
		hints = append(hints, retryHint)
		seen := map[Rule]bool{}
		for _, v := range rejected {
			if hint, ok := correctiveHints[v.Rule]; ok && !seen[v.Rule] {
				seen[v.Rule] = true
				hints = append(hints, "  - "+hint)
			}
		}
	}
	if len(hints) > 0 {
		req.System += "\n\n" + hintHeading + "\n" + strings.Join(hints, "\n")
	}
	return req
}
