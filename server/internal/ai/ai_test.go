package ai

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validRequest() Request {
	return Request{
		Task:     "conversation",
		System:   "짧게 답한다.",
		Messages: []Message{{Role: RoleUser, Text: userText}},
	}
}

func TestValidTaskID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"소문자와 숫자", "gate2", true},
		{"밑줄과 붙임표", "signal_extract-v2", true},
		{"빈 이름", "", false},
		{"대문자", "Gate", false},
		{"붙임표로 시작", "-gate", false},
		{"경로 구분자", "a/b", false},
		{"점", "..", false},
		{"공백", "my task", false},
		{"한글", "대화", false},
		{"65자", strings.Repeat("a", 65), false},
		{"64자", strings.Repeat("a", 64), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidTaskID(tt.id))
		})
	}
}

func TestRequest_Validate(t *testing.T) {
	t.Run("필요한 것만 채운 요청은 통과한다", func(t *testing.T) {
		require.NoError(t, validRequest().Validate())
	})

	t.Run("모든 항목을 채운 요청은 통과한다", func(t *testing.T) {
		r := validRequest()
		r.Messages = append(r.Messages, Message{Role: RoleModel, Text: "그랬군요."}, Message{Role: RoleUser, Text: "응"})
		r.MaxOutputTokens = 2048
		r.Thinking = ThinkingLow
		r.JSONSchema = json.RawMessage(`{"type":"object"}`)
		require.NoError(t, r.Validate())
	})

	tests := []struct {
		name   string
		mutate func(*Request)
		reason string
	}{
		{"지시문 ID가 없다", func(r *Request) { r.Task = "" }, "bad_task_id"},
		{"지시문 ID 자리에 문장이 들어왔다", func(r *Request) { r.Task = userText }, "bad_task_id"},
		{"보낼 말이 없다", func(r *Request) { r.Messages = nil }, "no_messages"},
		{"모르는 역할이다", func(r *Request) { r.Messages[0].Role = "system" }, "bad_role"},
		{"공백뿐인 말이다", func(r *Request) { r.Messages[0].Text = " \n" }, "blank_message"},
		{"출력 한도가 음수다", func(r *Request) { r.MaxOutputTokens = -1 }, "negative_max_output_tokens"},
		{"모르는 생각하기 수준이다", func(r *Request) { r.Thinking = "extreme" }, "bad_thinking_level"},
		{"스키마가 JSON이 아니다", func(r *Request) { r.JSONSchema = json.RawMessage(`{"type":`) }, "bad_json_schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRequest()
			tt.mutate(&r)

			err := r.Validate()

			require.ErrorIs(t, err, ErrInvalidRequest)
			var detailed *Error
			require.ErrorAs(t, err, &detailed)
			assert.Equal(t, tt.reason, detailed.Detail.Reason)
			assert.NotContains(t, err.Error(), userText)
			assert.False(t, Classify(err).Retryable)
		})
	}
}

func TestCheckResponse(t *testing.T) {
	jsonRequest := validRequest()
	jsonRequest.JSONSchema = json.RawMessage(`{"type":"object"}`)

	tests := []struct {
		name string
		req  Request
		resp Response
		want error
	}{
		{"정상 종료에 글이 있으면 쓸 수 있다", validRequest(), Response{Text: "그랬군요.", FinishReason: FinishStop}, nil},
		{"정상 종료인데 글이 비었다", validRequest(), Response{Text: "", FinishReason: FinishStop}, ErrEmpty},
		{"정상 종료인데 공백뿐이다", validRequest(), Response{Text: " \n\t", FinishReason: FinishStop}, ErrEmpty},
		{"출력 한도에 걸려 문장 중간에서 끊겼다", validRequest(), Response{Text: "오늘 많이 힘드", FinishReason: FinishMaxTokens}, ErrTruncated},
		{"생각 토큰이 한도를 다 써서 글 없이 끊겼다", validRequest(), Response{FinishReason: FinishMaxTokens, Usage: Usage{ReasoningTokens: 400}}, ErrTruncated},
		{"안전 필터에 걸려 끝났다", validRequest(), Response{Text: "오늘", FinishReason: FinishSafety}, ErrAbnormalFinish},
		{"그 밖의 사유로 끝났다", validRequest(), Response{Text: "오늘", FinishReason: FinishOther}, ErrAbnormalFinish},
		{"종료 사유가 없다", validRequest(), Response{Text: "오늘"}, ErrAbnormalFinish},
		{"JSON을 요구했고 JSON이 왔다", jsonRequest, Response{Text: `{"ok":true}`, FinishReason: FinishStop}, nil},
		{"JSON을 요구했는데 글이 왔다", jsonRequest, Response{Text: "네, 알겠습니다.", FinishReason: FinishStop}, ErrInvalidJSON},
		{"JSON을 요구하지 않았으면 글이어도 된다", validRequest(), Response{Text: "네, 알겠습니다.", FinishReason: FinishStop}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckResponse(tt.req, tt.resp, "")
			if tt.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.want)
			if text := strings.TrimSpace(tt.resp.Text); text != "" {
				assert.NotContains(t, err.Error(), text, "답의 내용이 오류에 들어가면 안 된다")
			}
		})
	}

	t.Run("오류에는 지시문, 모델, 공급자의 사유 코드, 토큰 수가 남는다", func(t *testing.T) {
		resp := Response{
			Text: "오늘 많이 힘드", FinishReason: FinishMaxTokens, Model: "gemini-3.8-flash",
			Usage: Usage{InputTokens: 900, OutputTokens: 12, ReasoningTokens: 388},
		}

		err := CheckResponse(validRequest(), resp, "MAX_TOKENS")

		var detailed *Error
		require.ErrorAs(t, err, &detailed)
		assert.Equal(t, Detail{
			Task: "conversation", Model: "gemini-3.8-flash", Reason: "MAX_TOKENS",
			Usage: Usage{InputTokens: 900, OutputTokens: 12, ReasoningTokens: 388},
		}, detailed.Detail)
		assert.NotContains(t, err.Error(), "힘드")
	})

	t.Run("공급자의 사유 코드가 없으면 종료 사유를 적는다", func(t *testing.T) {
		err := CheckResponse(validRequest(), Response{FinishReason: FinishSafety}, "")

		var detailed *Error
		require.ErrorAs(t, err, &detailed)
		assert.Equal(t, "safety", detailed.Detail.Reason)
	})
}

func TestDecodeJSON(t *testing.T) {
	type verdict struct {
		Level int `json:"level"`
	}

	t.Run("약속한 모양의 답을 푼다", func(t *testing.T) {
		var got verdict
		require.NoError(t, DecodeJSON(validRequest(), Response{Text: `{"level":2}`}, &got))
		assert.Equal(t, 2, got.Level)
	})

	t.Run("풀리지 않는 답은 내용 없이 종류만 알린다", func(t *testing.T) {
		var got verdict
		err := DecodeJSON(validRequest(), Response{Text: `{"level":"` + userText + `"}`, Model: "m"}, &got)

		require.ErrorIs(t, err, ErrInvalidJSON)
		assert.NotContains(t, err.Error(), userText)
		assert.True(t, Classify(err).Retryable)
	})
}

func TestLogValue_HidesText(t *testing.T) {
	const systemText = "두 문장 이하로 답한다."
	const replyText = "무슨 일이 있었어요?"

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	req := Request{
		Task:       "conversation",
		System:     systemText,
		Messages:   []Message{{Role: RoleUser, Text: userText}},
		Thinking:   ThinkingLow,
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	}
	resp := Response{Text: replyText, FinishReason: FinishStop, Model: "gemini-3.8-flash", Usage: Usage{OutputTokens: 9}}

	logger.Info("llm call", slog.Any("request", req), slog.Any("message", req.Messages[0]), slog.Any("response", resp))

	out := buf.String()
	assert.NotContains(t, out, userText)
	assert.NotContains(t, out, systemText)
	assert.NotContains(t, out, replyText)

	var line struct {
		Request struct {
			Task         string `json:"task"`
			MessageCount int    `json:"message_count"`
			JSON         bool   `json:"json"`
		} `json:"request"`
		Message struct {
			Role  string `json:"role"`
			Chars int    `json:"chars"`
		} `json:"message"`
		Response struct {
			Model        string `json:"model"`
			OutputTokens int    `json:"output_tokens"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "conversation", line.Request.Task)
	assert.Equal(t, 1, line.Request.MessageCount)
	assert.True(t, line.Request.JSON)
	assert.Equal(t, "user", line.Message.Role)
	assert.Equal(t, 15, line.Message.Chars)
	assert.Equal(t, "gemini-3.8-flash", line.Response.Model)
	assert.Equal(t, 9, line.Response.OutputTokens)
}
