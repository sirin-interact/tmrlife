package scripted

import (
	"strings"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// 대화의 답은 서버의 출력 검사를 그대로 통과해야 한다. 두 문장 이하이고, 물음표는 하나 이하이며,
// 전화번호, 기관 이름, 허락을 구하는 말, 평가하는 말, 마침표와 물음표 말고 다른 기호가 없다.
var (
	questionReplies = []string{
		"그런 일이 있었네요. 그때 기분은 어땠어요?",
		"이야기해 줘서 고마워요. 조금 더 들려줄래요?",
		"듣고 있어요. 그 뒤에는 어떻게 됐어요?",
		"그랬어요. 오늘 하루 중에 제일 기억에 남는 건 뭐예요?",
	}
	statementReplies = []string{
		"그런 날도 있죠. 오늘 하루도 애썼어요.",
		"잘 들었어요. 천천히 이야기해도 괜찮아요.",
		"말해 줘서 고마워요. 오늘은 푹 쉬었으면 좋겠어요.",
	}

	// 위기 응답이 나간 뒤의 대화에서는 대화를 닫는 말("푹 쉬어요")을 하지 않는다. 자리를 지키는 말만 한다.
	crisisQuestionReplies = []string{
		"말해 줘서 고마워요. 지금 곁에 누가 있어요?",
		"그만큼 힘들었네요. 언제부터 그랬어요?",
	}
	crisisStatementReplies = []string{
		"그만큼 힘들었네요. 여기서 계속 듣고 있어요.",
		"말이 잘 안 나올 수 있어요. 말하지 않아도 여기 있을게요.",
		"말해 줘서 고마워요. 천천히 이야기해도 괜찮아요.",
	}
)

// 되묻는 답은 사용자의 표현을 그대로 옮긴 문장과 묻는 문장 하나로 이루어진다.
// "하고 말했죠"는 앞말의 받침과 상관없이 붙는다. 조사를 고를 일이 없다.
const (
	reflectPrefix   = "방금 "
	reflectSuffix   = " 하고 말했죠. "
	reflectQuestion = "오늘 무슨 일 있었어요?"
)

// maxMirroredRunes는 되묻는 답에 옮겨 넣는 사용자 표현의 길이 한도다. 답 전체가 짧아야 한다.
const maxMirroredRunes = 24

// conversationReply는 지금까지 AI가 말한 횟수로 답을 고른다.
// 대화가 이어지면 답이 차례로 바뀌고, 같은 요청에는 늘 같은 답이 나온다.
func (l *LLM) conversationReply(req ai.Request, questions, statements []string) string {
	turn := 0
	for _, m := range req.Messages {
		if m.Role == ai.RoleModel {
			turn++
		}
	}
	if l.noQuestion(req) {
		return statements[turn%len(statements)]
	}
	return questions[turn%len(questions)]
}

// reflectReply는 여러 뜻으로 읽히는 말을 들었을 때의 되묻기다. 사용자의 표현을 그대로 받고 무슨 일이 있었는지 하나만 묻는다.
func reflectReply(req ai.Request) string {
	mirrored := mirror(lastUserText(req))
	if mirrored == "" {
		return reflectQuestion
	}
	return reflectPrefix + mirrored + reflectSuffix + reflectQuestion
}

// mirror는 사용자의 마지막 말에서 첫 토막을 한글과 빈칸만 남겨 돌려준다.
// 문장 부호나 기호를 옮기면 답이 세 문장이 되거나 출력 검사의 기호 검사에 걸린다.
func mirror(utterance string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(utterance) {
		switch {
		case r >= 0xAC00 && r <= 0xD7A3:
			b.WriteRune(r)
		case r == ' ' && b.Len() > 0:
			b.WriteRune(r)
		case b.Len() > 0:
			// 첫 토막이 끝났다.
			return clip(b.String())
		}
	}
	return clip(b.String())
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxMirroredRunes {
		return s
	}
	// 낱말 중간에서 끊지 않는다.
	runes := []rune(s)[:maxMirroredRunes]
	if i := strings.LastIndexByte(string(runes), ' '); i > 0 {
		return strings.TrimSpace(string(runes)[:i])
	}
	return string(runes)
}

func lastUserText(req ai.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == ai.RoleUser {
			return req.Messages[i].Text
		}
	}
	return ""
}

// noQuestion은 이번 답이 질문으로 끝나면 안 되는지 본다.
func (l *LLM) noQuestion(req ai.Request) bool {
	last := req.Messages[len(req.Messages)-1].Text
	for _, hint := range l.noQuestionHints {
		if strings.Contains(req.System, hint) || strings.Contains(last, hint) {
			return true
		}
	}

	// 질문이 세 번 이어지면 대화가 아니라 면담이 된다. 서버의 출력 검사도 같은 기준으로 본다.
	// 서버는 연달아 나간 AI의 말을 줄바꿈으로 이어 메시지 하나로 보낸다. 그래서 줄마다 말 하나로 센다.
	asked := 0
	for i := len(req.Messages) - 1; i >= 0 && asked < 2; i-- {
		m := req.Messages[i]
		if m.Role != ai.RoleModel {
			continue
		}
		lines := strings.Split(strings.TrimSpace(m.Text), "\n")
		for j := len(lines) - 1; j >= 0 && asked < 2; j-- {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			if !endsWithQuestion(lines[j]) {
				return false
			}
			asked++
		}
	}
	return asked == 2
}

func endsWithQuestion(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasSuffix(text, "?") || strings.HasSuffix(text, "？")
}
