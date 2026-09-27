package scripted

import (
	"encoding/json"
	"strings"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// 마음 신호 추출의 답에 쓰는 이름이다. 지시문의 스키마(prompts/signal_extract)와 같은 글자여야 한다.
// 판단과 명시성의 식별자는 계산 코어가 저장하는 값과도 같다.
const (
	statusNotMentioned = "not_mentioned"
	statusNotObserved  = "not_observed"
	statusObserved     = "observed"

	explicitnessNone     = "none"
	explicitnessIndirect = "indirect"
	explicitnessDirect   = "direct"
)

// signalItems는 답에 담아야 하는 여덟 항목이다. 정해진 순서로 둔다.
// 하나라도 빠지면 서버는 답 전체를 버리므로(답하지 않은 것과 "언급 없음"은 다르다) 표를 고칠 때 함께 본다.
var signalItems = []string{
	"interest", "mood", "sleep", "fatigue", "appetite", "self_blame", "concentration", "psychomotor",
}

// signalRule은 항목 하나를 어떤 낱말로 가릴지다.
//
// 브라우저 흐름 테스트와 키 없이 돌려 보는 시연이 여덟 항목의 화면을 실제로 채울 수 있을 만큼만 담은 표다.
// 판별기가 아니다. 문맥을 보지 못해서 "잠을 못 잔 줄 알았는데 잘 잤어"는 잘못 걸리고, 남이 한 말을 옮긴 줄도 가리지 못한다.
// 낱말 바로 뒤에 붙은 부정만 본다(negatedKeyword). 화면에 판단과 정반대인 문장이 근거로 걸리는 것을 막으려는 것이다.
// 이 표를 고칠 때는 위기 관문의 낱말 표(gate.go)와 겹치지 않게 둔다. 관문에 걸린 발화는 어느 항목의 근거도 될 수 없어서,
// 겹치는 낱말로 고르면 판단이 조용히 언급 없음으로 되돌려진다.
type signalRule struct {
	// observed와 notObserved는 그 낱말이 든 사용자의 줄을 찾으면 그 판단으로 본다. observed를 먼저 본다.
	observed    []string
	notObserved []string
	// indirect에 든 낱말로 걸린 줄은 "미루어 본" 근거로 답한다. 나머지는 직접 말한 것으로 본다.
	indirect []string
}

var signalRules = map[string]signalRule{
	"interest": {
		observed:    []string{"재미없", "재미가 없", "흥미가 없", "즐겁지 않", "아무것도 하고 싶지 않", "관심이 안"},
		notObserved: []string{"재밌었", "재미있었", "즐거웠", "신났"},
		indirect:    []string{"아무것도 하고 싶지 않"},
	},
	"mood": {
		observed:    []string{"우울", "가라앉", "슬펐", "희망이 없", "속상", "울었"},
		notObserved: []string{"기분이 좋", "괜찮았", "행복", "웃었"},
		indirect:    []string{"속상", "울었"},
	},
	"sleep": {
		observed:    []string{"잠을 못", "잠이 안", "잘 못 자", "못 잤", "깼어", "불면", "새벽까지", "설쳤"},
		notObserved: []string{"잘 잤", "푹 잤"},
	},
	"fatigue": {
		observed:    []string{"피곤", "기운이 없", "지쳤", "힘이 없", "졸렸"},
		notObserved: []string{"기운이 났", "쌩쌩"},
		indirect:    []string{"졸렸"},
	},
	"appetite": {
		observed:    []string{"입맛이 없", "밥을 못", "굶었", "폭식"},
		notObserved: []string{"밥은 잘 먹", "잘 먹었", "맛있게 먹"},
	},
	"self_blame": {
		observed:    []string{"내 탓", "내가 못나", "쓸모없", "자책", "내가 부족"},
		notObserved: []string{"내 잘못은 아니", "잘했다고 생각"},
		indirect:    []string{"내가 부족"},
	},
	"concentration": {
		observed:    []string{"집중이 안", "집중을 못", "딴생각", "멍하"},
		notObserved: []string{"집중이 잘", "집중해서"},
		indirect:    []string{"멍하"},
	},
	"psychomotor": {
		observed:    []string{"안절부절", "가만히 있지", "몸이 무거", "말이 느려"},
		notObserved: []string{"몸이 가볍"},
		indirect:    []string{"몸이 무거"},
	},
}

// signalReply는 번호가 붙은 사용자의 줄에서 낱말을 찾아 여덟 항목의 판단을 JSON으로 답한다.
//
// 근거는 걸린 줄을 글자 그대로 옮긴다. 서버는 근거가 사용자의 어느 한 발화에 그대로 있는지 대조하고
// 없으면 그 항목을 버리므로, 줄을 통째로 옮기는 것이 대조를 지나가는 가장 곧은 길이다.
// 짧게 자른 낱말만 옮기면 화면에 "내가 한 말"로 보일 글이 낱말 하나가 되어 근거 화면이 읽을 것이 없어진다.
func signalReply(req ai.Request) string {
	lines := numberedUserLines(req)

	reply := make(map[string]any, len(signalItems))
	for _, item := range signalItems {
		reply[item] = judgeSignal(signalRules[item], lines)
	}
	encoded, err := json.Marshal(reply)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// signalAnswer는 항목 하나의 답이다. 필드 이름은 지시문의 스키마와 같다.
type signalAnswer struct {
	Status       string `json:"status"`
	Explicitness string `json:"explicitness"`
	Line         int    `json:"line"`
	Evidence     string `json:"evidence"`
}

// numberedLine은 사용자의 말 하나다.
type userLine struct {
	number int
	text   string
}

// numberedUserLines는 요청에서 번호가 붙은 줄만 골라 번호순으로 돌려준다.
// 번호가 없는 줄([상대]가 붙은 AI의 말과 머리말)은 근거가 될 수 없으므로 아예 보지 않는다.
func numberedUserLines(req ai.Request) []userLine {
	var out []userLine
	for _, message := range req.Messages {
		for _, raw := range strings.Split(message.Text, "\n") {
			line := strings.TrimSpace(raw)
			m := numberedLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			number, ok := leadingNumber(line)
			if !ok {
				continue
			}
			out = append(out, userLine{number: number, text: strings.TrimSpace(m[1])})
		}
	}
	return out
}

// leadingNumber는 "12. 오늘은…"의 12를 읽는다.
func leadingNumber(line string) (int, bool) {
	number := 0
	for i, r := range line {
		if r >= '0' && r <= '9' {
			number = number*10 + int(r-'0')
			continue
		}
		return number, i > 0
	}
	return 0, false
}

// judgeSignal은 항목 하나의 판단을 고른다. 걸리는 줄이 없으면 언급 없음이다.
// 관찰됨을 먼저 본다. 아침에 "잘 잤어"라고 했어도 밤에 못 잤다는 말이 나왔으면 그날은 관찰됨이다.
func judgeSignal(rule signalRule, lines []userLine) signalAnswer {
	if line, keyword, ok := findKeyword(rule.observed, lines); ok {
		return signalAnswer{
			Status: statusObserved, Explicitness: explicitnessOf(rule, keyword),
			Line: line.number, Evidence: line.text,
		}
	}
	if line, keyword, ok := findKeyword(rule.notObserved, lines); ok {
		return signalAnswer{
			Status: statusNotObserved, Explicitness: explicitnessOf(rule, keyword),
			Line: line.number, Evidence: line.text,
		}
	}
	// 관찰됨의 낱말이 부정으로 뒤집힌 줄만 남았다. 그 이야기는 나왔고 괜찮았다는 말이다.
	// 언급 없음으로 두면 그 줄이 어디에도 쓰이지 않고, 관찰됨으로 두면 "피로 관찰됨" 옆에 "피곤하지도 않아"가 걸린다.
	if line, ok := findNegated(rule.observed, lines); ok {
		return signalAnswer{
			Status: statusNotObserved, Explicitness: explicitnessDirect,
			Line: line.number, Evidence: line.text,
		}
	}
	return signalAnswer{Status: statusNotMentioned, Explicitness: explicitnessNone}
}

// findKeyword는 줄을 번호순으로 보며 처음 걸린 줄과 걸린 낱말을 돌려준다. 부정으로 뒤집힌 자리는 걸리지 않은 것으로 본다.
func findKeyword(keywords []string, lines []userLine) (userLine, string, bool) {
	for _, line := range lines {
		for _, keyword := range keywords {
			if strings.Contains(line.text, keyword) && !negatedKeyword(line.text, keyword) {
				return line, keyword, true
			}
		}
	}
	return userLine{}, "", false
}

// findNegated는 낱말이 부정으로 뒤집힌 채 걸린 첫 줄을 돌려준다.
func findNegated(keywords []string, lines []userLine) (userLine, bool) {
	for _, line := range lines {
		for _, keyword := range keywords {
			if negatedKeyword(line.text, keyword) {
				return line, true
			}
		}
	}
	return userLine{}, false
}

// negationWindow는 걸린 낱말 뒤에서 부정을 찾는 글자 수다.
//
// 한국어의 부정은 어간 바로 뒤에 붙는다("피곤하지도 않아", "내 탓이 아니야").
// 줄 끝까지 보면 "우울한데 아무것도 하고 싶지 않아"처럼 다른 자리의 부정이 앞의 낱말을 뒤집는다.
// 대신 부정이 멀리 떨어진 자리는 놓친다. 이 표는 판별기가 아니라서, 놓치는 쪽이 뒤집는 쪽보다 낫다.
const negationWindow = 10

// negationMarkers는 부정의 표시다. "아니"는 말버릇으로도 쓰이지만("아니 그게"),
// 그것은 말의 앞머리에 오므로 낱말 뒤 열 글자 안에서는 대개 부정이다.
var negationMarkers = []string{"않", "아니"}

// negatedKeyword는 그 낱말이 든 자리 바로 뒤에 부정이 붙었는지 본다.
// 같은 줄에 그 낱말이 두 번 나오면 처음 자리만 본다. 낱말 표로 가리는 데 그 이상은 필요하지 않다.
func negatedKeyword(text, keyword string) bool {
	i := strings.Index(text, keyword)
	if i < 0 {
		return false
	}
	rest := []rune(text[i+len(keyword):])
	if len(rest) > negationWindow {
		rest = rest[:negationWindow]
	}
	after := string(rest)
	for _, marker := range negationMarkers {
		if strings.Contains(after, marker) {
			return true
		}
	}
	return false
}

func explicitnessOf(rule signalRule, keyword string) string {
	for _, indirect := range rule.indirect {
		if keyword == indirect {
			return explicitnessIndirect
		}
	}
	return explicitnessDirect
}
