package diary

import (
	"context"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
)

// 모델에 보내는 글의 뼈대다. 지시문(prompts/diary_draft)이 이 낱말들을 그대로 가리키므로 함께 고친다.
const (
	labelMode        = "방식: "
	labelModeFirst   = "처음 쓰기"
	labelModeAppend  = "이어 쓰기"
	labelLines       = "사용자가 한 말:"
	labelNextSession = "(시간이 지난 뒤 다시 나눈 대화)"
)

// entryReply는 모델이 돌려주는 JSON이다.
// entry는 포인터로 받는다. 빈 글("쓸 말이 없다")과 entry가 아예 없는 엉뚱한 답을 가려야 한다.
type entryReply struct {
	Entry *string `json:"entry"`
}

// compose는 재료를 모델에 보내 일기 글을 받는다. 사용자가 한 말이 없으면 모델을 부르지 않고 빈 글을 돌려준다.
func (s *Service) compose(ctx context.Context, snap *snapshot) (composed, error) {
	if snap.lineCount() == 0 {
		return composed{}, nil
	}

	req := s.prompt.Request([]ai.Message{{Role: ai.RoleUser, Text: buildMessage(snap.mode, snap.lines)}})
	req.MaxOutputTokens = s.maxOutputTokens
	req.Thinking = s.thinking

	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	resp, err := s.llm.Generate(callCtx, req)
	if err != nil {
		return composed{}, err
	}

	var reply entryReply
	if err := ai.DecodeJSON(req, resp, &reply); err != nil {
		return composed{}, err
	}
	if reply.Entry == nil {
		return composed{}, &RejectError{Reason: "missing_entry"}
	}
	entry := tidy(*reply.Entry)
	if err := checkEntry(entry, snap.lines, s.maxEntryRunes); err != nil {
		return composed{}, err
	}
	return composed{
		entry: logging.Redacted(entry),
		runes: utf8.RuneCountInString(entry),
		model: resp.Model,
	}, nil
}

// buildMessage는 사용자가 한 말에 번호를 붙여 늘어놓는다. AI가 한 말은 받지도 않는다.
func buildMessage(mode Mode, conversations [][]logging.Redacted) string {
	var b strings.Builder
	b.WriteString(labelMode)
	if mode == ModeAppend {
		b.WriteString(labelModeAppend)
	} else {
		b.WriteString(labelModeFirst)
	}
	b.WriteString("\n\n")
	b.WriteString(labelLines)

	n := 0
	wroteConversation := false
	for _, lines := range conversations {
		if len(lines) == 0 {
			continue
		}
		if wroteConversation {
			// 하루에 여러 번 대화했으면 그 사이에 시간이 흘렀다. 한 자리에서 이어 한 말처럼 엮이지 않게 경계를 알린다.
			b.WriteString("\n")
			b.WriteString(labelNextSession)
		}
		wroteConversation = true
		for _, line := range lines {
			n++
			b.WriteString("\n")
			b.WriteString(strconv.Itoa(n))
			b.WriteString(". ")
			b.WriteString(string(line))
		}
	}
	return b.String()
}

// tidy는 글의 앞뒤 공백을 떼고, 줄 끝을 맞추고, 빈 줄이 둘 넘게 이어지지 않게 한다.
func tidy(entry string) string {
	entry = strings.ReplaceAll(entry, "\r\n", "\n")
	lines := strings.Split(entry, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// labelingTerms는 상태에 이름을 붙이거나 안내문처럼 들리는 말이다. 일기는 진단도 조언도 하지 않는다.
// 사용자가 제 입으로 한 말이면 일기에 나와도 된다("병원에서 상담받고 왔어"). 그래서 사용자의 말에 없는데 글에만 있을 때 걸린다.
var labelingTerms = []string{
	"우울증", "불면증", "번아웃", "공황", "증상", "진단", "환자", "치료", "정신과", "전문가", "상담",
}

// checkEntry는 모델이 쓴 글을 저장하기 전에 본다. 걸리면 그 답을 버리고 다시 시도한다.
// 글의 내용이 지어낸 것인지는 여기서 가릴 수 없다. 그 일은 재료를 사용자의 말로만 좁힌 것과 지시문이 맡는다.
// 여기서는 기계적으로 가릴 수 있는 것만 본다.
func checkEntry(entry string, conversations [][]logging.Redacted, maxRunes int) error {
	if entry == "" {
		// 쓸 말이 없다는 답이다. 빈 글은 정상이다.
		return nil
	}
	if utf8.RuneCountInString(entry) > maxRunes {
		return &RejectError{Reason: "too_long"}
	}

	var said strings.Builder
	for _, lines := range conversations {
		for _, line := range lines {
			said.WriteString(string(line))
			said.WriteByte('\n')
		}
	}
	userText := said.String()

	for _, term := range labelingTerms {
		if strings.Contains(entry, term) && !strings.Contains(userText, term) {
			return &RejectError{Reason: "labeling_term"}
		}
	}

	for _, line := range strings.Split(entry, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, marker := range []string{"#", "- ", "* ", "• "} {
			if strings.HasPrefix(trimmed, marker) {
				return &RejectError{Reason: "list_or_heading"}
			}
		}
	}

	for _, r := range entry {
		if !foreignToDiary(r) || strings.ContainsRune(userText, r) {
			continue
		}
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) {
			return &RejectError{Reason: "symbol"}
		}
		return &RejectError{Reason: "foreign_script"}
	}
	return nil
}

// foreignToDiary는 한국어 일기에 나올 까닭이 없는 글자인지 본다.
// 모델이 한국어 답에 한자나 가나를 섞어 내는 일이 실제로 있다. 그림 문자와 장식 기호도 사용자가 쓰지 않았으면 넣지 않는다.
func foreignToDiary(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
		return true
	case unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Arabic, r), unicode.Is(unicode.Thai, r):
		return true
	case unicode.Is(unicode.So, r), unicode.Is(unicode.Sk, r):
		return true
	default:
		return false
	}
}

// joinEntry는 이미 있는 글 뒤에 새 단락을 붙인다. 앞의 글은 한 글자도 바꾸지 않는다. 끝의 공백조차 사용자가 쓴 것이다.
func joinEntry(base, entry logging.Redacted) string {
	if strings.TrimSpace(string(base)) == "" {
		return string(entry)
	}
	if entry == "" {
		return string(base)
	}
	separator := "\n\n"
	switch {
	case strings.HasSuffix(string(base), "\n\n"):
		separator = ""
	case strings.HasSuffix(string(base), "\n"):
		separator = "\n"
	}
	return string(base) + separator + string(entry)
}
