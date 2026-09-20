package scripted

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// 대화 기록을 한 덩어리의 글로 넘겨받을 때 사용자의 말을 가리는 실마리다.
var (
	// 서버의 일기 작업은 사용자의 말에만 번호를 붙여 보낸다("1. 오늘 친구랑 놀러갔다왔어").
	numberedLine = regexp.MustCompile(`^[0-9]{1,3}[.)]\s+(.+)$`)
	// 번호가 없으면 줄 머리의 이름으로 누구의 말인지 가린다.
	userLinePrefixes  = []string{"사용자:", "나:", "user:", "User:", "USER:"}
	otherLinePrefixes = []string{"AI:", "ai:", "상대:", "model:", "Model:", "assistant:"}
)

// appendCue는 오늘 이미 써 둔 일기 뒤에 붙을 단락을 청하는 요청인지 알아보는 글귀다.
const appendCue = "이어 쓰기"

const (
	diaryOpening       = "오늘 나는 이런 이야기를 했다."
	diaryAppendOpening = "그 뒤에 이런 이야기도 했다."
	// diaryEmpty는 사용자의 말을 하나도 찾지 못했을 때의 글이다. 빈 글은 실패로 걸러지므로 비워 두지 않는다.
	diaryEmpty = "오늘은 짧게 이야기를 나눴다."

	// maxDiaryRunes는 글의 길이 한도다. 초안은 짧아야 하고, 서버는 너무 긴 초안을 지시를 어긴 답으로 버린다.
	// 넘치면 뒤의 발화를 싣지 않는다.
	maxDiaryRunes = 600
)

// diaryReply는 사용자의 발화를 순서대로 이어 1인칭 글을 만든다.
// 사용자가 하지 않은 말은 넣지 않는다. AI의 말도 재료로 쓰지 않는다.
func diaryReply(req ai.Request) string {
	utterances, appending := diaryMaterial(req)

	text := diaryEmpty
	if len(utterances) > 0 {
		opening := diaryOpening
		if appending {
			opening = diaryAppendOpening
		}
		parts := []string{opening}
		used := utf8.RuneCountInString(opening)
		for _, u := range utterances {
			s := sentence(u)
			if n := utf8.RuneCountInString(s) + 1; used+n <= maxDiaryRunes {
				parts = append(parts, s)
				used += n
			}
		}
		text = strings.Join(parts, " ")
	}

	if req.JSONSchema == nil {
		return text
	}
	return fillSchema(req.JSONSchema, fillValues{text: text})
}

// diaryMaterial은 요청에서 사용자의 말을 찾고, 이어 쓰는 요청인지 본다.
//
// 대화를 메시지 여러 개로 받았으면 사용자 쪽 메시지가 곧 발화다.
// 메시지 하나에 기록을 통째로 받았으면 번호가 붙은 줄, 없으면 사용자 이름이 붙은 줄, 그것도 없으면 모든 줄을 발화로 본다.
func diaryMaterial(req ai.Request) (utterances []string, appending bool) {
	if len(req.Messages) != 1 {
		for _, m := range req.Messages {
			if m.Role == ai.RoleUser {
				utterances = append(utterances, m.Text)
			}
		}
		return trimAll(utterances), false
	}

	var numbered, labelled, plain []string
	sawLabel := false
	for _, line := range strings.Split(req.Messages[0].Text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := numberedLine.FindStringSubmatch(line); m != nil {
			numbered = append(numbered, m[1])
			continue
		}
		if len(numbered) == 0 && strings.Contains(line, appendCue) {
			// 발화보다 앞에 있는 머리말에서만 본다. 사용자가 한 말 속의 같은 글귀에 걸리지 않게 한다.
			appending = true
		}
		if rest, ok := cutAnyPrefix(line, userLinePrefixes); ok {
			sawLabel = true
			labelled = append(labelled, rest)
			continue
		}
		if _, ok := cutAnyPrefix(line, otherLinePrefixes); ok {
			sawLabel = true
			continue
		}
		plain = append(plain, line)
	}

	switch {
	case len(numbered) > 0:
		return trimAll(numbered), appending
	case sawLabel:
		// 이름이 붙은 기록이라면 이름 없는 줄은 머리말이나 지시다.
		return trimAll(labelled), false
	default:
		return trimAll(plain), false
	}
}

func cutAnyPrefix(line string, prefixes []string) (string, bool) {
	for _, p := range prefixes {
		if rest, ok := strings.CutPrefix(line, p); ok {
			return rest, true
		}
	}
	return "", false
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// sentence는 발화를 글자 그대로 두고, 문장 부호로 끝나지 않으면 마침표만 붙인다.
func sentence(utterance string) string {
	for _, end := range []string{".", "!", "?", "…", "。"} {
		if strings.HasSuffix(utterance, end) {
			return utterance
		}
	}
	return utterance + "."
}
