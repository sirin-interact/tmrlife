package scripted

import (
	"strings"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

type gateRule struct {
	stage    int
	keywords []string
}

// gateRules는 위에서부터 보고 처음 걸린 줄의 단계를 따른다.
//
// 브라우저 흐름 테스트가 네 단계를 모두 밟을 수 있을 만큼만 담은 표다. 판별기가 아니다.
// 문맥을 보지 않으므로 창피한 일을 말하다 나온 과장도 낱말이 같으면 같은 단계로 답한다.
// 지나간 일을 말하는 줄이 지금의 생각을 말하는 줄보다 위에 있는 것은 "예전에 죽고 싶다는 생각 한 적 있어"가 아래 줄에 먼저 걸리지 않게 하려는 것이다.
var gateRules = []gateRule{
	{3, []string{"약을 모아", "오늘 밤에 끝내", "그동안 고마웠어", "마지막 일기", "유서", "번개탄"}},
	{1, []string{"생각 한 적", "생각한 적"}},
	{2, []string{"죽고 싶", "죽고싶", "자해", "살 이유를 모르겠", "손목을 그었", "손목 그었"}},
	{1, []string{"사라졌으면", "사라지고 싶", "그만하고 싶", "일어나지 않았으면", "없어도 아무도 모를"}},
}

// directAskCue는 직전의 AI 말이 "죽고 싶다는 생각도 드는지"를 직접 물은 것인지 알아보는 글귀다.
// 직접 물은 뒤의 짧은 답("응", "아니")은 낱말만으로는 뜻을 알 수 없어서 이 문맥 하나만은 본다.
const directAskCue = "죽고 싶다는 생각"

var (
	// directAskDenials와 directAskFrequency는 발화 어디에 있어도 본다. directAskAffirms는 첫 낱말일 때만 본다.
	// "어"나 "네"는 다른 낱말 속에도 흔히 들어 있어서다.
	directAskDenials   = []string{"아니", "그 정도는", "그런 건", "그런건"}
	directAskAffirms   = []string{"응", "어", "네", "예", "맞아", "그래", "들어"}
	directAskFrequency = []string{"가끔", "자주", "매일"}
)

type gateVerdict struct {
	stage    int
	evidence string
}

func (l *LLM) gateReply(req ai.Request) string {
	utterance, before := l.gateInput(req)
	verdict := classify(utterance, before)
	return fillSchema(req.JSONSchema, fillValues{
		ints:    map[string]int{"stage": verdict.stage},
		strings: map[string]string{"evidence": verdict.evidence, "reason": "scripted"},
	})
}

// gateInput은 판정할 발화와, 그 바로 앞의 AI 말(before)을 가른다.
func (l *LLM) gateInput(req ai.Request) (utterance, before string) {
	lastUser := -1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == ai.RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return req.Messages[len(req.Messages)-1].Text, ""
	}

	text := req.Messages[lastUser].Text
	for _, marker := range l.utteranceMarkers {
		if i := strings.LastIndex(text, marker); i >= 0 {
			return strings.TrimSpace(text[i+len(marker):]), lastLine(text[:i])
		}
	}
	for i := lastUser - 1; i >= 0; i-- {
		if req.Messages[i].Role == ai.RoleModel {
			return text, req.Messages[i].Text
		}
	}
	return text, ""
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}

// classify는 낱말 표를 먼저 본다. 표에 걸리지 않은 짧은 답만 직전의 질문에 비추어 읽는다.
func classify(utterance, before string) gateVerdict {
	for _, rule := range gateRules {
		for _, keyword := range rule.keywords {
			if strings.Contains(utterance, keyword) {
				// 근거는 발화에 글자 그대로 있는 조각이어야 한다. 서버가 그것을 대조한다.
				return gateVerdict{stage: rule.stage, evidence: keyword}
			}
		}
	}

	if strings.Contains(before, directAskCue) {
		for _, denial := range directAskDenials {
			if strings.Contains(utterance, denial) {
				return gateVerdict{}
			}
		}
		first := firstWord(utterance)
		for _, affirm := range directAskAffirms {
			if first == affirm {
				return gateVerdict{stage: 2, evidence: first}
			}
		}
		for _, word := range directAskFrequency {
			if strings.Contains(utterance, word) {
				return gateVerdict{stage: 2, evidence: word}
			}
		}
	}
	return gateVerdict{}
}

func firstWord(text string) string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '!' || r == '?' || r == '\n' || r == '\t'
	})
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
