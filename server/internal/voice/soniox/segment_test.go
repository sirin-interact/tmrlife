package soniox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

func tk(text string, confidence float32, final bool) token {
	return token{Text: text, Confidence: confidence, IsFinal: final}
}

func partial(text string) voice.Event {
	return voice.Event{Kind: voice.EventPartial, Text: text}
}

func final(text string, minConfidence float32) voice.Event {
	return voice.Event{Kind: voice.EventFinal, Text: text, MinConfidence: minConfidence}
}

func TestSegment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		responses [][]token
		want      []voice.Event
	}{
		{
			name:      "미확정 토큰은 이어 붙인 글로 Partial이 된다",
			responses: [][]token{{tk("오늘은", 0.9, false), tk(" 동네", 0.8, false)}},
			want:      []voice.Event{partial("오늘은 동네")},
		},
		{
			name: "미확정 토큰은 응답마다 통째로 바뀐다",
			responses: [][]token{
				{tk("오늘", 0.9, false)},
				{tk("오늘은", 0.9, false), tk(" 동네", 0.8, false)},
			},
			want: []voice.Event{partial("오늘"), partial("오늘은 동네")},
		},
		{
			name: "확정 토큰은 쌓이고 미확정 토큰이 그 뒤에 붙는다",
			responses: [][]token{
				{tk("오늘은", 0.9, true), tk(" 도서", 0.5, false)},
				{tk(" 도서관에", 0.7, false)},
			},
			want: []voice.Event{partial("오늘은 도서"), partial("오늘은 도서관에")},
		},
		{
			name: "끝점이 오면 확정된 글이 Final이 되고 부호 토큰은 확신도에 들지 않는다",
			responses: [][]token{
				{tk("오늘은", 0.9, false)},
				{tk("오늘은", 0.95, true), tk(" 도서관에", 0.8, true), tk(",", 0.2, true), tk(" 다녀왔어", 0.7, true), tk(endToken, 0.1, true)},
			},
			want: []voice.Event{partial("오늘은"), final("오늘은 도서관에, 다녀왔어", 0.7)},
		},
		{
			name: "끝점 뒤에는 새 마디가 시작된다",
			responses: [][]token{
				{tk("응", 0.9, true), tk(endToken, 1, true)},
				{tk(" 그리고", 0.6, false)},
				{tk(" 그리고", 0.9, true), tk(" 또", 0.8, true), tk(endToken, 1, true)},
			},
			want: []voice.Event{final("응", 0.9), partial("그리고"), final("그리고 또", 0.8)},
		},
		{
			name: "끝점과 다음 마디의 미확정 토큰이 한 응답에 오면 둘 다 낸다",
			responses: [][]token{
				{tk("응", 0.9, true), tk(endToken, 1, true), tk(" 그리고", 0.6, false)},
			},
			want: []voice.Event{final("응", 0.9), partial("그리고")},
		},
		{
			name:      "끝점만 오고 글이 없으면 Final을 내지 않는다",
			responses: [][]token{{tk(endToken, 1, true)}},
			want:      nil,
		},
		{
			name:      "공백뿐인 글은 비어 있는 것으로 친다",
			responses: [][]token{{tk(" ", 0.3, true), tk(endToken, 1, true)}},
			want:      nil,
		},
		{
			name:      "finalize의 <fin> 표시도 마디의 끝이다",
			responses: [][]token{{tk("다 말했어", 0.85, true), tk(finToken, 1, true)}},
			want:      []voice.Event{final("다 말했어", 0.85)},
		},
		{
			name:      "확정되지 않은 표시 토큰은 글에 넣지 않고 마디도 끝내지 않는다",
			responses: [][]token{{tk("오늘은", 0.9, true), tk(endToken, 1, false)}},
			want:      []voice.Event{partial("오늘은")},
		},
		{
			name: "글이 바뀌지 않으면 Partial을 되풀이하지 않는다",
			responses: [][]token{
				{tk("오늘", 0.9, false)},
				{tk("오늘", 0.95, false)},
				{tk("오늘", 0.95, true)},
			},
			want: []voice.Event{partial("오늘")},
		},
		{
			name:      "낱말 토큰이 없으면 확신도는 1이다",
			responses: [][]token{{tk("...", 0.1, true), tk(endToken, 1, true)}},
			want:      []voice.Event{final("...", 1)},
		},
		{
			name:      "숫자와 로마자도 낱말 토큰이다",
			responses: [][]token{{tk("109", 0.4, true), tk(" AI", 0.3, true), tk("!", 0.1, true), tk(endToken, 1, true)}},
			want:      []voice.Event{final("109 AI!", 0.3)},
		},
		{
			name:      "자모만 있는 토큰도 낱말 토큰이다",
			responses: [][]token{{tk("ㅋㅋ", 0.5, true), tk(endToken, 1, true)}},
			want:      []voice.Event{final("ㅋㅋ", 0.5)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var g segment
			var got []voice.Event
			for _, tokens := range tc.responses {
				got = append(got, g.consume(tokens)...)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSafeErrorType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"정해진 이름은 그대로 쓴다", "service_unavailable", "service_unavailable"},
		{"비어 있으면 unknown이다", "", "unknown"},
		{"이름이 아닌 글이 오면 unknown이다", "key is bad: 사용자 말", "unknown"},
		{"너무 길면 unknown이다", string(make([]byte, 65)), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, safeErrorType(tc.in))
		})
	}
}
