package soniox

import (
	"strings"
	"unicode"

	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

// token은 공급자가 보내는 토큰 하나다. 글에는 앞 공백이 붙어 올 수 있어 그대로 이어 붙인다.
type token struct {
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence"`
	IsFinal    bool    `json:"is_final"`
}

// segment는 끝점 사이의 한 마디다.
//
// 확정 토큰은 한 번만 오고 바뀌지 않으므로 쌓는다. 아직 바뀔 수 있는 토큰은 응답마다 전부 다시 오므로 통째로 바꾼다.
// 지금 말하고 있는 글은 "쌓인 확정 토큰 + 마지막 응답의 미확정 토큰"이다.
type segment struct {
	final   []token
	partial []token
	// lastText는 마지막으로 낸 Partial의 글이다. 같은 글을 되풀이해 내지 않는다.
	lastText string
}

// consume은 응답 하나의 토큰을 받아 낼 사건을 돌려준다.
func (g *segment) consume(tokens []token) []voice.Event {
	var events []voice.Event
	g.partial = g.partial[:0]
	for _, t := range tokens {
		switch {
		case t.Text == endToken || t.Text == finToken:
			// 표시 토큰은 글이 아니다. 확정된 표시만 마디의 끝으로 친다.
			if !t.IsFinal {
				continue
			}
			if ev, ok := g.finish(); ok {
				events = append(events, ev)
			}
		case t.IsFinal:
			g.final = append(g.final, t)
		default:
			g.partial = append(g.partial, t)
		}
	}
	if text := g.text(); text != "" && text != g.lastText {
		g.lastText = text
		events = append(events, voice.Event{Kind: voice.EventPartial, Text: text})
	}
	return events
}

// finish는 쌓인 확정 토큰으로 Final을 만들고 마디를 비운다. 글이 비어 있으면 사건 없이 비우기만 한다.
func (g *segment) finish() (voice.Event, bool) {
	text := joinText(g.final)
	minConf := minConfidence(g.final)
	g.final = g.final[:0]
	g.partial = g.partial[:0]
	g.lastText = ""
	if text == "" {
		return voice.Event{}, false
	}
	return voice.Event{Kind: voice.EventFinal, Text: text, MinConfidence: minConf}, true
}

func (g *segment) text() string {
	var b strings.Builder
	for _, t := range g.final {
		b.WriteString(t.Text)
	}
	for _, t := range g.partial {
		b.WriteString(t.Text)
	}
	return strings.TrimSpace(b.String())
}

func joinText(tokens []token) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteString(t.Text)
	}
	return strings.TrimSpace(b.String())
}

// minConfidence는 낱말 토큰 가운데 가장 낮은 확신도다. 낱말 토큰이 없으면 1이다.
// 쉼표 같은 부호 토큰은 확신도가 낮게 오는 일이 잦고, 그것으로 말을 잘못 들었다고 볼 수는 없다.
func minConfidence(tokens []token) float32 {
	var minConf float32 = 1
	for _, t := range tokens {
		if isWordToken(t.Text) && t.Confidence < minConf {
			minConf = t.Confidence
		}
	}
	return minConf
}

// isWordToken은 한글, 숫자, 로마자가 하나라도 든 토큰인지다.
func isWordToken(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Latin, r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
