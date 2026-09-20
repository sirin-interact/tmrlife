package gemini_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/gemini"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
)

// 아래 글들은 오류 문구와 로그 어디에도 나오면 안 된다.
const (
	testAPIKey  = "test-key-0123456789-do-not-leak"
	systemText  = "친구처럼 편한 존댓말로 한두 문장만 말한다."
	userText    = "그냥 다 사라졌으면 좋겠어"
	earlierText = "오늘 회사에서 크게 혼났어"
	modelText   = "무슨 일 있었어요?"
	replyText   = "사라지고 싶을 만큼요? 오늘 무슨 일 있었어요?"
)

var sensitiveTexts = []string{testAPIKey, systemText, userText, earlierText, modelText, replyText}

func assertNoSensitiveText(t *testing.T, got string) {
	t.Helper()
	for _, s := range sensitiveTexts {
		assert.NotContains(t, got, s)
	}
}

// sentRequest는 SDK가 실제로 내보낸 HTTP 요청이다.
type sentRequest struct {
	URL    string
	Header http.Header
	Body   map[string]any
}

// transport는 가짜 전송 계층이다. 네트워크로 나가지 않고, 받은 요청을 기록한 뒤 respond가 정한 응답을 돌려준다.
type transport struct {
	mu      sync.Mutex
	sent    []sentRequest
	respond func(*http.Request) (*http.Response, error)
}

func (tr *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body map[string]any
	if r.Body != nil {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				return nil, err
			}
		}
	}
	tr.mu.Lock()
	tr.sent = append(tr.sent, sentRequest{URL: r.URL.String(), Header: r.Header.Clone(), Body: body})
	tr.mu.Unlock()
	return tr.respond(r)
}

func (tr *transport) calls() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return len(tr.sent)
}

func (tr *transport) only(t *testing.T) sentRequest {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	require.Len(t, tr.sent, 1)
	return tr.sent[0]
}

func httpResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func jsonResponse(status int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return httpResponse(status, "application/json", body), nil
	}
}

// sseResponse는 흘려 보내는 응답이다. 조각 하나가 "data: {...}" 한 줄이다.
func sseResponse(chunks ...string) func(*http.Request) (*http.Response, error) {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\n\n")
	}
	body := b.String()
	return func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusOK, "text/event-stream", body), nil
	}
}

// blockUntilDone은 컨텍스트가 끝날 때까지 답하지 않는 공급자다.
func blockUntilDone(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

type harness struct {
	transport *transport
	clock     *clock.Fake
	logs      *bytes.Buffer
	client    *gemini.Client
}

func newHarness(t *testing.T, respond func(*http.Request) (*http.Response, error), mutate ...func(*gemini.Config)) *harness {
	t.Helper()
	h := &harness{
		transport: &transport{respond: respond},
		clock:     clock.NewFake(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)),
		logs:      &bytes.Buffer{},
	}
	cfg := gemini.Config{
		APIKey:     config.NewSecret(testAPIKey),
		Clock:      h.clock,
		Logger:     logging.New(h.logs, slog.LevelDebug),
		HTTPClient: &http.Client{Transport: h.transport},
	}
	for _, m := range mutate {
		m(&cfg)
	}
	client, err := gemini.New(context.Background(), cfg)
	require.NoError(t, err)
	h.client = client
	return h
}

func (h *harness) llm(t *testing.T, m gemini.Model) *gemini.LLM {
	t.Helper()
	llm, err := h.client.LLM(m)
	require.NoError(t, err)
	return llm
}

func conversationRequest() ai.Request {
	return ai.Request{
		Task:   "conversation",
		System: systemText,
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: earlierText},
			{Role: ai.RoleModel, Text: modelText},
			{Role: ai.RoleUser, Text: userText},
		},
	}
}

// okBody는 정상 응답이다. text를 JSON 문자열로 옮겨 넣는다.
func okBody(t *testing.T, text string) string {
	t.Helper()
	quoted, err := json.Marshal(text)
	require.NoError(t, err)
	return `{"candidates":[{"content":{"role":"model","parts":[{"text":` + string(quoted) + `}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":812,"candidatesTokenCount":21,"thoughtsTokenCount":388,"totalTokenCount":1221},
		"modelVersion":"gemini-3.8-flash-001"}`
}

func dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		require.Truef(t, ok, "%q 앞까지가 객체가 아니다", key)
		cur, ok = obj[key]
		require.Truef(t, ok, "%q가 요청 본문에 없다", key)
	}
	return cur
}

func TestNew(t *testing.T) {
	base := func() gemini.Config {
		return gemini.Config{APIKey: config.NewSecret(testAPIKey), Clock: clock.Real{}}
	}
	tests := []struct {
		name   string
		mutate func(*gemini.Config)
		ok     bool
	}{
		{"키와 시계만 있으면 만들어진다", func(*gemini.Config) {}, true},
		{"창구를 Developer API로 적어도 같다", func(c *gemini.Config) { c.Backend = gemini.BackendDeveloperAPI }, true},
		{"키가 없으면 만들지 않는다", func(c *gemini.Config) { c.APIKey = config.Secret{} }, false},
		{"시계가 없으면 만들지 않는다", func(c *gemini.Config) { c.Clock = nil }, false},
		{"아직 없는 창구는 받지 않는다", func(c *gemini.Config) { c.Backend = "vertex_ai" }, false},
		{"음수인 시간 제한은 받지 않는다", func(c *gemini.Config) { c.RequestTimeout = -time.Second }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			client, err := gemini.New(context.Background(), cfg)
			if tt.ok {
				require.NoError(t, err)
				require.NotNil(t, client)
				return
			}
			require.Error(t, err)
			assert.Nil(t, client)
			assert.NotContains(t, err.Error(), testAPIKey)
		})
	}
}

func TestClient_LLM(t *testing.T) {
	h := newHarness(t, jsonResponse(http.StatusOK, "{}"))
	tests := []struct {
		name  string
		model gemini.Model
		ok    bool
	}{
		{"모델 이름만 있어도 된다", gemini.Model{Name: "gemini-3.8-flash"}, true},
		{"생각하기 수준과 한도를 기본값으로 둘 수 있다", gemini.Model{Name: "gemini-3.5-flash", Thinking: ai.ThinkingMinimal, MaxOutputTokens: 3000}, true},
		{"모델 이름이 비면 안 된다", gemini.Model{}, false},
		{"모델 이름에 주소를 바꿀 수 있는 글자가 있으면 안 된다", gemini.Model{Name: "gemini?key=1"}, false},
		{"모델 이름 자리에 문장이 오면 안 된다", gemini.Model{Name: "오늘은 아무것도 하기 싫었어"}, false},
		{"모르는 생각하기 수준은 안 된다", gemini.Model{Name: "gemini-3.8-flash", Thinking: "extreme"}, false},
		{"수준을 못 박을 수 있다", gemini.Model{Name: "gemini-3.6-flash", Thinking: ai.ThinkingMinimal, PinThinking: true}, true},
		{"못 박을 수준 없이 못 박을 수는 없다", gemini.Model{Name: "gemini-3.6-flash", PinThinking: true}, false},
		{"음수인 한도는 안 된다", gemini.Model{Name: "gemini-3.8-flash", MaxOutputTokens: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm, err := h.client.LLM(tt.model)
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.model.Name, llm.Model())
				return
			}
			require.Error(t, err)
			assert.Nil(t, llm)
		})
	}
}

func TestGenerate_RequestShape(t *testing.T) {
	t.Run("지시문, 여러 턴의 대화, 키가 제자리에 실린다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		llm := h.llm(t, gemini.Model{Name: "gemini-3.8-flash", Thinking: ai.ThinkingLow})

		req := conversationRequest()
		before := conversationRequest()
		_, err := llm.Generate(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, before, req, "요청을 고치면 안 된다. 같은 요청이 두 모델에 동시에 갈 수 있다")

		sent := h.transport.only(t)
		assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent", sent.URL)
		assert.Equal(t, testAPIKey, sent.Header.Get("x-goog-api-key"))
		assert.NotContains(t, sent.URL, testAPIKey, "키는 주소에 실리면 안 된다. 주소는 프록시와 로그에 남는다")

		assert.Equal(t, systemText, dig(t, sent.Body, "systemInstruction", "parts").([]any)[0].(map[string]any)["text"])

		contents := dig(t, sent.Body, "contents").([]any)
		require.Len(t, contents, 3)
		wantRoles := []string{"user", "model", "user"}
		wantTexts := []string{earlierText, modelText, userText}
		for i, c := range contents {
			content := c.(map[string]any)
			assert.Equal(t, wantRoles[i], content["role"])
			assert.Equal(t, wantTexts[i], content["parts"].([]any)[0].(map[string]any)["text"])
		}

		gen := dig(t, sent.Body, "generationConfig").(map[string]any)
		assert.NotContains(t, gen, "temperature")
		assert.NotContains(t, gen, "responseMimeType")
		assert.NotContains(t, gen, "responseJsonSchema")
		assert.NotContains(t, gen, "responseSchema")
	})

	t.Run("부르는 쪽이 정할 수 있는 안전 범주 넷을 모두 끈다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.NoError(t, err)

		settings := dig(t, h.transport.only(t).Body, "safetySettings").([]any)
		got := map[string]string{}
		for _, s := range settings {
			setting := s.(map[string]any)
			got[setting["category"].(string)] = setting["threshold"].(string)
		}
		assert.Equal(t, map[string]string{
			"HARM_CATEGORY_HARASSMENT":        "OFF",
			"HARM_CATEGORY_HATE_SPEECH":       "OFF",
			"HARM_CATEGORY_SEXUALLY_EXPLICIT": "OFF",
			"HARM_CATEGORY_DANGEROUS_CONTENT": "OFF",
		}, got)
		assert.Len(t, settings, 4)
	})

	t.Run("지시문이 비어 있으면 빈 지시문을 보내지 않는다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		req := conversationRequest()
		req.System = "  \n"
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), req)
		require.NoError(t, err)
		assert.NotContains(t, h.transport.only(t).Body, "systemInstruction")
	})

	t.Run("스키마를 주면 JSON으로 답하라고 청하고 스키마를 그대로 보낸다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, `{"stage":1,"evidence":"사라졌으면"}`)))
		req := conversationRequest()
		req.Task = "gate"
		req.JSONSchema = json.RawMessage(`{"type":"object","properties":{"stage":{"type":"integer","minimum":0,"maximum":3},"evidence":{"type":"string"}},"required":["stage","evidence"]}`)

		resp, err := h.llm(t, gemini.Model{Name: "gemini-3.5-flash"}).Generate(context.Background(), req)
		require.NoError(t, err)
		assert.JSONEq(t, `{"stage":1,"evidence":"사라졌으면"}`, resp.Text)

		gen := dig(t, h.transport.only(t).Body, "generationConfig").(map[string]any)
		assert.Equal(t, "application/json", gen["responseMimeType"])
		var want any
		require.NoError(t, json.Unmarshal(req.JSONSchema, &want))
		assert.Equal(t, want, gen["responseJsonSchema"])
	})

	t.Run("스키마가 객체가 아니면 보내지 않는다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, "{}")))
		req := conversationRequest()
		req.JSONSchema = json.RawMessage(`["stage"]`)
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.5-flash"}).Generate(context.Background(), req)
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.Zero(t, h.transport.calls())
	})

	t.Run("주소는 설정으로만 바꿀 수 있다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)), func(c *gemini.Config) {
			c.BaseURL = "https://llm-proxy.internal.example/"
		})
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.NoError(t, err)
		assert.Equal(t, "https://llm-proxy.internal.example/v1beta/models/gemini-3.8-flash:generateContent", h.transport.only(t).URL)
	})

	t.Run("환경 변수로 창구나 주소를 바꿀 수 없다", func(t *testing.T) {
		t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
		t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://elsewhere.invalid/")
		t.Setenv("GOOGLE_API_KEY", "another-key-from-env")

		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.NoError(t, err)

		sent := h.transport.only(t)
		assert.True(t, strings.HasPrefix(sent.URL, "https://generativelanguage.googleapis.com/"), sent.URL)
		assert.Equal(t, testAPIKey, sent.Header.Get("x-goog-api-key"))
	})
}

func TestGenerate_ThinkingLevel(t *testing.T) {
	tests := []struct {
		name         string
		modelDefault string
		pinned       bool
		request      string
		want         string // 비어 있으면 thinkingConfig를 보내지 않는다
	}{
		{"둘 다 비어 있으면 모델의 기본 수준에 맡긴다", "", false, "", ""},
		{"요청이 비워 두면 모델에 정해 둔 수준을 쓴다", ai.ThinkingLow, false, "", "LOW"},
		{"요청이 정하면 그 수준이 먼저다", ai.ThinkingLow, false, ai.ThinkingMinimal, "MINIMAL"},
		{"모델에 정해 둔 수준이 없어도 요청의 수준을 쓴다", "", false, ai.ThinkingHigh, "HIGH"},
		{"medium도 그대로 옮긴다", ai.ThinkingMedium, false, "", "MEDIUM"},
		{"수준을 못 박은 모델은 요청이 다른 수준을 정해도 제 수준을 쓴다", ai.ThinkingMinimal, true, ai.ThinkingLow, "MINIMAL"},
		{"수준을 못 박은 모델은 요청이 비워 두어도 제 수준을 쓴다", ai.ThinkingMinimal, true, "", "MINIMAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
			req := conversationRequest()
			req.Thinking = tt.request
			model := gemini.Model{Name: "gemini-3.8-flash", Thinking: tt.modelDefault, PinThinking: tt.pinned}
			_, err := h.llm(t, model).Generate(context.Background(), req)
			require.NoError(t, err)

			gen := dig(t, h.transport.only(t).Body, "generationConfig").(map[string]any)
			if tt.want == "" {
				assert.NotContains(t, gen, "thinkingConfig")
				return
			}
			thinking := gen["thinkingConfig"].(map[string]any)
			assert.Equal(t, tt.want, thinking["thinkingLevel"])
			assert.NotContains(t, thinking, "includeThoughts", "생각을 글로 받지 않는다")
		})
	}
}

func TestGenerate_MaxOutputTokens(t *testing.T) {
	tests := []struct {
		name         string
		modelDefault int
		request      int
		want         int
	}{
		{"아무도 정하지 않으면 넉넉한 기본 한도다", 0, 0, gemini.DefaultMaxOutputTokens},
		{"요청이 비워 두면 모델에 정해 둔 한도를 쓴다", 8000, 0, 8000},
		{"요청이 정하면 그 한도가 먼저다", 8000, 6000, 6000},
		{"생각 토큰이 다 써 버릴 만큼 낮은 한도는 바닥까지 올린다", 0, 400, gemini.OutputTokenFloor},
		{"모델에 정해 둔 한도가 낮아도 바닥까지 올린다", 256, 0, gemini.OutputTokenFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
			req := conversationRequest()
			req.MaxOutputTokens = tt.request
			_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash", MaxOutputTokens: tt.modelDefault}).Generate(context.Background(), req)
			require.NoError(t, err)
			got := dig(t, h.transport.only(t).Body, "generationConfig", "maxOutputTokens")
			assert.InDelta(t, float64(tt.want), got, 0)
		})
	}

	t.Run("기본 한도는 바닥보다 낮지 않다", func(t *testing.T) {
		assert.GreaterOrEqual(t, gemini.DefaultMaxOutputTokens, gemini.OutputTokenFloor)
	})
}

func TestGenerate_Success(t *testing.T) {
	h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
	resp, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
	require.NoError(t, err)

	assert.Equal(t, replyText, resp.Text)
	assert.Equal(t, ai.FinishStop, resp.FinishReason)
	assert.Equal(t, ai.Usage{InputTokens: 812, OutputTokens: 21, ReasoningTokens: 388}, resp.Usage)
	assert.Equal(t, "gemini-3.8-flash", resp.Model, "공급자가 알려준 판 이름이 아니라 설정에 적은 이름이어야 예비 모델이 답했는지 견줄 수 있다")
}

const usageJSON = `"usageMetadata":{"promptTokenCount":812,"candidatesTokenCount":12,"thoughtsTokenCount":388}`

func TestGenerate_FailureKinds(t *testing.T) {
	quotedReply, err := json.Marshal(replyText)
	require.NoError(t, err)
	quotedUser, err := json.Marshal("echo: " + userText)
	require.NoError(t, err)
	candidate := func(finish string, parts string) string {
		finishField := ""
		if finish != "" {
			finishField = `,"finishReason":"` + finish + `"`
		}
		return `{"candidates":[{"content":{"role":"model","parts":[` + parts + `]}` + finishField + `}],` + usageJSON + `}`
	}
	textPart := `{"text":` + string(quotedReply) + `}`

	tests := []struct {
		name       string
		respond    func(*http.Request) (*http.Response, error)
		json       bool
		want       error
		wantKind   ai.Kind
		retryable  bool
		wantReason string
		wantStatus int
		wantUsage  bool
	}{
		{
			name:    "요청이 막히면 차단이다",
			respond: jsonResponse(http.StatusOK, `{"promptFeedback":{"blockReason":"SAFETY"},`+usageJSON+`}`),
			want:    ai.ErrBlocked, wantKind: ai.KindBlocked, wantReason: "SAFETY", wantUsage: true,
		},
		{
			name:    "금지된 내용이라는 사유로 막혀도 차단이다",
			respond: jsonResponse(http.StatusOK, `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`),
			want:    ai.ErrBlocked, wantKind: ai.KindBlocked, wantReason: "PROHIBITED_CONTENT",
		},
		{
			name:    "차단 사유와 함께 온 후보는 쓰지 않는다",
			respond: jsonResponse(http.StatusOK, `{"promptFeedback":{"blockReason":"OTHER"},"candidates":[{"content":{"parts":[`+textPart+`]},"finishReason":"STOP"}]}`),
			want:    ai.ErrBlocked, wantKind: ai.KindBlocked, wantReason: "OTHER",
		},
		{
			name:    "사유도 후보도 없으면 후보 없음이다",
			respond: jsonResponse(http.StatusOK, `{`+usageJSON+`}`),
			want:    ai.ErrNoCandidate, wantKind: ai.KindNoCandidate, retryable: true, wantUsage: true,
		},
		{
			name:    "본문이 통째로 비어도 후보 없음이다",
			respond: jsonResponse(http.StatusOK, ``),
			want:    ai.ErrNoCandidate, wantKind: ai.KindNoCandidate, retryable: true,
		},
		{
			name:    "안전 필터로 끊긴 답은 비정상 종료다",
			respond: jsonResponse(http.StatusOK, candidate("SAFETY", "")),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "SAFETY", wantUsage: true,
		},
		{
			name:    "안전 필터로 끊기기 전에 나온 글이 있어도 쓰지 않는다",
			respond: jsonResponse(http.StatusOK, candidate("SAFETY", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "SAFETY", wantUsage: true,
		},
		{
			name:    "인용 제한으로 끝난 답은 비정상 종료다",
			respond: jsonResponse(http.StatusOK, candidate("RECITATION", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "RECITATION", wantUsage: true,
		},
		{
			name:    "금지된 내용으로 끝난 답은 비정상 종료다",
			respond: jsonResponse(http.StatusOK, candidate("PROHIBITED_CONTENT", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "PROHIBITED_CONTENT", wantUsage: true,
		},
		{
			name:    "그 밖의 사유로 끝난 답은 비정상 종료다",
			respond: jsonResponse(http.StatusOK, candidate("OTHER", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "OTHER", wantUsage: true,
		},
		{
			name:    "처음 보는 종료 사유는 정상으로 치지 않는다",
			respond: jsonResponse(http.StatusOK, candidate("SOMETHING_NEW", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "SOMETHING_NEW", wantUsage: true,
		},
		{
			name:    "종료 사유가 없는 답은 정상으로 치지 않는다",
			respond: jsonResponse(http.StatusOK, candidate("", textPart)),
			want:    ai.ErrAbnormalFinish, wantKind: ai.KindAbnormalFinish, retryable: true, wantReason: "NO_FINISH_REASON", wantUsage: true,
		},
		{
			name:    "정상 종료인데 글이 없으면 빈 답이다",
			respond: jsonResponse(http.StatusOK, candidate("STOP", "")),
			want:    ai.ErrEmpty, wantKind: ai.KindEmpty, retryable: true, wantReason: "STOP", wantUsage: true,
		},
		{
			name:    "정상 종료인데 공백뿐이어도 빈 답이다",
			respond: jsonResponse(http.StatusOK, candidate("STOP", `{"text":" \n "}`)),
			want:    ai.ErrEmpty, wantKind: ai.KindEmpty, retryable: true, wantReason: "STOP", wantUsage: true,
		},
		{
			name:    "생각을 옮긴 조각만 있으면 빈 답이다",
			respond: jsonResponse(http.StatusOK, candidate("STOP", `{"text":`+string(quotedReply)+`,"thought":true}`)),
			want:    ai.ErrEmpty, wantKind: ai.KindEmpty, retryable: true, wantReason: "STOP", wantUsage: true,
		},
		{
			name:    "후보에 내용이 아예 없어도 빈 답이다",
			respond: jsonResponse(http.StatusOK, `{"candidates":[{"finishReason":"STOP"}]}`),
			want:    ai.ErrEmpty, wantKind: ai.KindEmpty, retryable: true, wantReason: "STOP",
		},
		{
			name:    "출력 한도에 걸려 문장 중간에서 끊기면 잘린 답이다",
			respond: jsonResponse(http.StatusOK, candidate("MAX_TOKENS", textPart)),
			want:    ai.ErrTruncated, wantKind: ai.KindTruncated, retryable: true, wantReason: "MAX_TOKENS", wantUsage: true,
		},
		{
			name:    "생각 토큰이 한도를 다 써서 글이 하나도 없어도 잘린 답이다",
			respond: jsonResponse(http.StatusOK, candidate("MAX_TOKENS", "")),
			want:    ai.ErrTruncated, wantKind: ai.KindTruncated, retryable: true, wantReason: "MAX_TOKENS", wantUsage: true,
		},
		{
			name:    "JSON으로 청했는데 JSON이 아니면 잘못된 JSON이다",
			respond: jsonResponse(http.StatusOK, candidate("STOP", textPart)),
			json:    true,
			want:    ai.ErrInvalidJSON, wantKind: ai.KindInvalidJSON, retryable: true, wantReason: "STOP", wantUsage: true,
		},
		{
			name:    "429는 공급자의 일시적인 실패다",
			respond: jsonResponse(http.StatusTooManyRequests, `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`),
			want:    ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantReason: "RESOURCE_EXHAUSTED", wantStatus: 429,
		},
		{
			name:    "500은 공급자의 일시적인 실패다",
			respond: jsonResponse(http.StatusInternalServerError, `{"error":{"code":500,"message":"internal","status":"INTERNAL"}}`),
			want:    ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantReason: "INTERNAL", wantStatus: 500,
		},
		{
			name:    "503은 공급자의 일시적인 실패다",
			respond: jsonResponse(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`),
			want:    ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantReason: "UNAVAILABLE", wantStatus: 503,
		},
		{
			name: "오류 본문이 JSON이 아니어도 상태 코드로 가린다",
			respond: func(*http.Request) (*http.Response, error) {
				return httpResponse(http.StatusBadGateway, "text/html", "<html>bad gateway</html>"), nil
			},
			want: ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantStatus: 502,
		},
		{
			name:    "504는 시간 초과다",
			respond: jsonResponse(http.StatusGatewayTimeout, `{"error":{"code":504,"message":"deadline","status":"DEADLINE_EXCEEDED"}}`),
			want:    ai.ErrTimeout, wantKind: ai.KindTimeout, retryable: true, wantReason: "DEADLINE_EXCEEDED", wantStatus: 504,
		},
		{
			name: "400은 잘못된 요청이다. 공급자가 오류 문구에 요청을 되풀이해도 옮기지 않는다",
			respond: jsonResponse(http.StatusBadRequest,
				`{"error":{"code":400,"message":`+string(quotedUser)+`,"status":"INVALID_ARGUMENT"}}`),
			want: ai.ErrInvalidRequest, wantKind: ai.KindInvalidRequest, wantReason: "INVALID_ARGUMENT", wantStatus: 400,
		},
		{
			name:    "사유 코드 자리에 문장이 오면 버린다",
			respond: jsonResponse(http.StatusBadRequest, `{"error":{"code":400,"message":"x","status":`+string(quotedUser)+`}}`),
			want:    ai.ErrInvalidRequest, wantKind: ai.KindInvalidRequest, wantStatus: 400,
		},
		{
			name:    "틀린 키(403)는 다시 보내도 같다",
			respond: jsonResponse(http.StatusForbidden, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`),
			want:    ai.ErrInvalidRequest, wantKind: ai.KindInvalidRequest, wantReason: "PERMISSION_DENIED", wantStatus: 403,
		},
		{
			name:    "없는 모델(404)은 다시 보내도 같다",
			respond: jsonResponse(http.StatusNotFound, `{"error":{"code":404,"message":"no such model","status":"NOT_FOUND"}}`),
			want:    ai.ErrInvalidRequest, wantKind: ai.KindInvalidRequest, wantReason: "NOT_FOUND", wantStatus: 404,
		},
		{
			name:    "연결하지 못하면 공급자의 일시적인 실패다",
			respond: func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: connection refused") },
			want:    ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantReason: "transport",
		},
		{
			name:    "전송 계층의 시간 초과는 시간 초과다",
			respond: func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
			want:    ai.ErrTimeout, wantKind: ai.KindTimeout, retryable: true, wantReason: "transport_timeout",
		},
		{
			name:    "200인데 본문이 깨져 있으면 공급자의 일시적인 실패다",
			respond: jsonResponse(http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":`+string(quotedReply)),
			want:    ai.ErrProvider, wantKind: ai.KindProvider, retryable: true, wantReason: "unreadable_response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.respond)
			req := conversationRequest()
			if tt.json {
				req.JSONSchema = json.RawMessage(`{"type":"object"}`)
			}
			resp, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), req)

			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, ai.Response{}, resp, "실패한 호출은 답을 돌려주지 않는다")
			assert.Equal(t, ai.Failure{Kind: tt.wantKind, Retryable: tt.retryable}, ai.Classify(err))

			var aiErr *ai.Error
			require.ErrorAs(t, err, &aiErr)
			assert.Equal(t, "conversation", aiErr.Detail.Task)
			assert.Equal(t, "gemini-3.8-flash", aiErr.Detail.Model)
			assert.Equal(t, tt.wantReason, aiErr.Detail.Reason)
			assert.Equal(t, tt.wantStatus, aiErr.Detail.Status)
			if tt.wantUsage {
				assert.Equal(t, ai.Usage{InputTokens: 812, OutputTokens: 12, ReasoningTokens: 388}, aiErr.Detail.Usage)
			}

			assertNoSensitiveText(t, err.Error())
			assertNoSensitiveText(t, h.logs.String())
			assert.Equal(t, 1, h.transport.calls(), "이 계층은 스스로 다시 부르지 않는다. 다시 부를지는 부르는 쪽이 정한다")
		})
	}
}

// timeoutError는 전송 계층이 내는 시간 초과(연결, TLS 등)를 흉내 낸다.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestGenerate_Context(t *testing.T) {
	t.Run("기한이 지나면 시간 초과이고 기한 오류로도 잡힌다", func(t *testing.T) {
		h := newHarness(t, blockUntilDone)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		_, err := h.llm(t, gemini.Model{Name: "gemini-3.5-flash"}).Generate(ctx, conversationRequest())
		require.ErrorIs(t, err, ai.ErrTimeout)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, ai.Failure{Kind: ai.KindTimeout, Retryable: true}, ai.Classify(err))
		assertNoSensitiveText(t, err.Error())
	})

	t.Run("부른 쪽이 취소하면 바로 돌아오고 실패가 아니라 취소로 남는다", func(t *testing.T) {
		h := newHarness(t, blockUntilDone)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(ctx, conversationRequest())
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ai.ErrTimeout)
		assert.Equal(t, ai.KindCanceled, ai.Classify(err).Kind)
		assert.Contains(t, h.logs.String(), `"level":"INFO"`, "취소는 고칠 것이 없으므로 경고로 남기지 않는다")
	})

	t.Run("이미 끝난 컨텍스트로는 공급자를 부르지 않는다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(ctx, conversationRequest())
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, h.transport.calls())
	})

	t.Run("부른 쪽이 기한을 주지 않아도 끝없이 기다리지 않는다", func(t *testing.T) {
		h := newHarness(t, blockUntilDone, func(c *gemini.Config) { c.RequestTimeout = 30 * time.Millisecond })
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.ErrorIs(t, err, ai.ErrTimeout)
	})

	t.Run("틀린 요청은 보내지 않는다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
		req := conversationRequest()
		req.Messages = nil
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), req)
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.Zero(t, h.transport.calls())
	})
}

func chunk(t *testing.T, text, finish string, extra string) string {
	t.Helper()
	quoted, err := json.Marshal(text)
	require.NoError(t, err)
	finishField := ""
	if finish != "" {
		finishField = `,"finishReason":"` + finish + `"`
	}
	return `{"candidates":[{"content":{"role":"model","parts":[{"text":` + string(quoted) + `}]}` + finishField + `}]` + extra + `}`
}

func TestGenerateStream(t *testing.T) {
	collect := func(into *[]string) ai.DeltaFunc {
		return func(delta string) error {
			*into = append(*into, delta)
			return nil
		}
	}

	t.Run("조각을 순서대로 넘기고, 돌려주는 글은 조각을 이은 것이다", func(t *testing.T) {
		h := newHarness(t, sseResponse(
			chunk(t, "사라지고 싶을 만큼요? ", "", `,"usageMetadata":{"promptTokenCount":812,"candidatesTokenCount":9}`),
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"속으로 하는 생각","thought":true}]}}]}`,
			chunk(t, "오늘 무슨 일 있었어요?", "STOP", `,"usageMetadata":{"promptTokenCount":812,"candidatesTokenCount":21,"thoughtsTokenCount":388}`),
		))
		var deltas []string
		resp, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash", Thinking: ai.ThinkingLow}).
			GenerateStream(context.Background(), conversationRequest(), collect(&deltas))
		require.NoError(t, err)

		assert.Equal(t, []string{"사라지고 싶을 만큼요? ", "오늘 무슨 일 있었어요?"}, deltas, "생각을 옮긴 조각은 넘기지 않는다")
		assert.Equal(t, replyText, resp.Text)
		assert.Equal(t, strings.Join(deltas, ""), resp.Text)
		assert.Equal(t, ai.Usage{InputTokens: 812, OutputTokens: 21, ReasoningTokens: 388}, resp.Usage, "마지막 조각의 누계가 전체다")
		assert.Equal(t, "gemini-3.8-flash", resp.Model)

		sent := h.transport.only(t)
		assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse", sent.URL)
		assert.Equal(t, "LOW", dig(t, sent.Body, "generationConfig", "thinkingConfig", "thinkingLevel"))
		assert.Len(t, dig(t, sent.Body, "safetySettings"), 4)
		assert.Contains(t, h.logs.String(), `"first_delta_ms"`)
		assertNoSensitiveText(t, h.logs.String())
	})

	tests := []struct {
		name       string
		respond    func(*http.Request) (*http.Response, error)
		want       error
		wantReason string
		wantDeltas int
	}{
		{
			name:    "조각을 넘긴 뒤에 한도에 걸리면 잘린 답이다",
			respond: sseResponse(chunk(t, "사라지고 싶을", "", ""), chunk(t, " 만큼", "MAX_TOKENS", "")),
			want:    ai.ErrTruncated, wantReason: "MAX_TOKENS", wantDeltas: 2,
		},
		{
			name:    "조각을 넘긴 뒤에 안전 필터로 끊기면 비정상 종료다",
			respond: sseResponse(chunk(t, "사라지고 싶을", "", ""), `{"candidates":[{"finishReason":"SAFETY"}]}`),
			want:    ai.ErrAbnormalFinish, wantReason: "SAFETY", wantDeltas: 1,
		},
		{
			name:    "첫 조각에 차단 사유가 오면 차단이다",
			respond: sseResponse(`{"promptFeedback":{"blockReason":"SAFETY"}}`),
			want:    ai.ErrBlocked, wantReason: "SAFETY",
		},
		{
			name:    "종료 사유 없이 흐름이 끝나면 정상 종료로 치지 않는다",
			respond: sseResponse(chunk(t, "사라지고 싶을", "", "")),
			want:    ai.ErrAbnormalFinish, wantReason: "NO_FINISH_REASON", wantDeltas: 1,
		},
		{
			name:    "조각이 하나도 없으면 후보 없음이다",
			respond: sseResponse(),
			want:    ai.ErrNoCandidate,
		},
		{
			name:    "정상 종료인데 글이 없으면 빈 답이다",
			respond: sseResponse(`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`),
			want:    ai.ErrEmpty, wantReason: "STOP",
		},
		{
			name:    "흘려 받는 호출도 503은 공급자의 일시적인 실패다",
			respond: jsonResponse(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`),
			want:    ai.ErrProvider, wantReason: "UNAVAILABLE",
		},
		{
			name: "흐름 중간에 온 오류도 상태 코드로 가린다",
			respond: func(*http.Request) (*http.Response, error) {
				body := "data: " + chunk(t, "사라지고 싶을", "", "") + "\n\n" +
					`{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}` + "\n\n"
				return httpResponse(http.StatusOK, "text/event-stream", body), nil
			},
			want: ai.ErrProvider, wantReason: "RESOURCE_EXHAUSTED", wantDeltas: 1,
		},
		{
			name: "흐름 중간에 깨진 조각이 오면 공급자의 일시적인 실패다",
			respond: func(*http.Request) (*http.Response, error) {
				return httpResponse(http.StatusOK, "text/event-stream", "data: {\"candidates\":[{\"content\"\n\n"), nil
			},
			want: ai.ErrProvider, wantReason: "unreadable_response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.respond)
			var deltas []string
			resp, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).
				GenerateStream(context.Background(), conversationRequest(), collect(&deltas))

			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, ai.Response{}, resp)
			assert.Len(t, deltas, tt.wantDeltas)
			var aiErr *ai.Error
			require.ErrorAs(t, err, &aiErr)
			assert.Equal(t, tt.wantReason, aiErr.Detail.Reason)
			assertNoSensitiveText(t, err.Error())
			assertNoSensitiveText(t, h.logs.String())
		})
	}

	t.Run("받는 쪽이 오류를 돌려주면 멈추고 그 오류를 그대로 돌려준다", func(t *testing.T) {
		h := newHarness(t, sseResponse(chunk(t, "하나", "", ""), chunk(t, "둘", "", ""), chunk(t, "셋", "STOP", "")))
		stop := errors.New("listener interrupted: " + userText)
		var deltas []string
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).GenerateStream(context.Background(), conversationRequest(),
			func(delta string) error {
				deltas = append(deltas, delta)
				if len(deltas) == 2 {
					return stop
				}
				return nil
			})
		require.ErrorIs(t, err, stop)
		assert.Equal(t, []string{"하나", "둘"}, deltas)
		assertNoSensitiveText(t, h.logs.String())
	})

	t.Run("받을 함수가 없으면 보내지 않는다", func(t *testing.T) {
		h := newHarness(t, sseResponse(chunk(t, "하나", "STOP", "")))
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).GenerateStream(context.Background(), conversationRequest(), nil)
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.Zero(t, h.transport.calls())
	})

	t.Run("흘려 받는 중에 부른 쪽이 취소하면 취소로 끝난다", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		first := "data: " + chunk(t, "하나", "", "") + "\n\n"
		h := newHarness(t, func(r *http.Request) (*http.Response, error) {
			resp := httpResponse(http.StatusOK, "text/event-stream", "")
			resp.Body = &stallingBody{first: strings.NewReader(first), done: r.Context().Done(), err: r.Context().Err}
			return resp, nil
		})

		var deltas []string
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).GenerateStream(ctx, conversationRequest(),
			func(delta string) error {
				deltas = append(deltas, delta)
				cancel()
				return nil
			})
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, ai.KindCanceled, ai.Classify(err).Kind)
		assert.Equal(t, []string{"하나"}, deltas)
	})
}

// stallingBody는 첫 조각을 준 뒤 컨텍스트가 끝날 때까지 다음 조각을 주지 않는 응답 본문이다.
type stallingBody struct {
	first *strings.Reader
	done  <-chan struct{}
	err   func() error
}

func (b *stallingBody) Read(p []byte) (int, error) {
	if b.first.Len() > 0 {
		return b.first.Read(p)
	}
	<-b.done
	return 0, b.err()
}

func (b *stallingBody) Close() error { return nil }

func TestLLM_Probe(t *testing.T) {
	t.Run("받아들여지면 오류가 없다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, "ok")))
		require.NoError(t, h.llm(t, gemini.Model{Name: "gemini-3.5-flash", Thinking: ai.ThinkingMinimal}).Probe(context.Background()))
		assert.Equal(t, "MINIMAL", dig(t, h.transport.only(t).Body, "generationConfig", "thinkingConfig", "thinkingLevel"))
	})

	t.Run("모델이 그 생각하기 수준을 받지 않으면 설정이 틀린 것으로 알려준다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusBadRequest, `{"error":{"code":400,"message":"thinking level is not supported","status":"INVALID_ARGUMENT"}}`))
		err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash", Thinking: ai.ThinkingMinimal}).Probe(context.Background())
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.False(t, ai.Classify(err).Retryable)
	})
}

func TestGenerate_Logging(t *testing.T) {
	t.Run("성공한 호출은 길이, 토큰 수, 모델, 걸린 시간만 남긴다", func(t *testing.T) {
		var h *harness
		h = newHarness(t, func(*http.Request) (*http.Response, error) {
			h.clock.Advance(1700 * time.Millisecond)
			return httpResponse(http.StatusOK, "application/json", okBody(t, replyText)), nil
		})
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash", Thinking: ai.ThinkingLow}).Generate(context.Background(), conversationRequest())
		require.NoError(t, err)

		entry := onlyLogEntry(t, h.logs)
		assert.Equal(t, "INFO", entry["level"])
		assert.Equal(t, "llm call succeeded", entry["msg"])
		assert.Equal(t, "gemini-3.8-flash", entry["model"])
		assert.Equal(t, "gemini-3.8-flash-001", entry["model_version"])
		assert.Equal(t, false, entry["stream"])
		assert.InDelta(t, 1700, entry["latency_ms"], 0)
		assert.Equal(t, "LOW", entry["sent_thinking"])
		assert.InDelta(t, gemini.DefaultMaxOutputTokens, entry["sent_max_output_tokens"], 0)
		assert.Equal(t, "STOP", entry["provider_reason"])

		request := entry["request"].(map[string]any)
		assert.Equal(t, "conversation", request["task"])
		assert.InDelta(t, 3, request["message_count"], 0)

		response := entry["response"].(map[string]any)
		assert.InDelta(t, len([]rune(replyText)), response["chars"], 0)
		assert.InDelta(t, 812, response["input_tokens"], 0)
		assert.InDelta(t, 21, response["output_tokens"], 0)
		assert.InDelta(t, 388, response["reasoning_tokens"], 0)

		assertNoSensitiveText(t, h.logs.String())
	})

	t.Run("실패한 호출은 실패 종류와 사유 코드를 경고로 남긴다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"사라지고"}]},"finishReason":"MAX_TOKENS"}],`+usageJSON+`}`))
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.ErrorIs(t, err, ai.ErrTruncated)

		entry := onlyLogEntry(t, h.logs)
		assert.Equal(t, "WARN", entry["level"])
		assert.Equal(t, "llm call failed", entry["msg"])
		assert.Equal(t, "truncated", entry["failure_kind"])
		assert.Equal(t, true, entry["retryable"])
		assert.Equal(t, "MAX_TOKENS", entry["reason"])
		assert.InDelta(t, 388, entry["reasoning_tokens"], 0)
		assert.NotContains(t, entry, "response")
		assertNoSensitiveText(t, h.logs.String())
	})

	t.Run("로거를 주지 않아도 돈다", func(t *testing.T) {
		h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)), func(c *gemini.Config) { c.Logger = nil })
		_, err := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"}).Generate(context.Background(), conversationRequest())
		require.NoError(t, err)
	})
}

func onlyLogEntry(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	require.Len(t, lines, 1)
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	return entry
}

func TestLLM_ConcurrentUse(t *testing.T) {
	h := newHarness(t, jsonResponse(http.StatusOK, okBody(t, replyText)))
	llm := h.llm(t, gemini.Model{Name: "gemini-3.8-flash"})

	// 고루틴 안에서는 시험을 멈출 수 없으므로 결과만 모으고 바깥에서 확인한다.
	const callers = 8
	texts := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			resp, err := llm.Generate(context.Background(), conversationRequest())
			texts[i], errs[i] = resp.Text, err
		})
	}
	wg.Wait()
	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, replyText, texts[i])
	}
	assert.Equal(t, callers, h.transport.calls())
}
