package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
)

// socketFixture는 운영과 같은 길(New)로 만든 서버에 대화 소켓을 붙이고, 진짜 TCP로 연결해 보는 자리다.
type socketFixture struct {
	srv   *testServer
	http  *httptest.Server
	talk  *fake.LLM
	judge *fake.LLM
	chat  *Conversation
}

func newSocketFixture(t *testing.T, adjust func(*ConversationOptions)) *socketFixture {
	t.Helper()
	return newSocketFixtureWith(t, adjust, nil)
}

// newSocketFixtureWith는 엔진의 조정 값(되묻기 기준 등)까지 바꿀 수 있는 자리다.
func newSocketFixtureWith(
	t *testing.T, adjust func(*ConversationOptions), adjustEngine func(*engine.Options),
) *socketFixture {
	t.Helper()
	talk := fake.New("fake-conversation")
	judge := fake.New("fake-gate")
	var chat *Conversation

	srv := newTestServer(t, func(opts *Options) {
		registry, err := prompts.LoadEmbedded()
		require.NoError(t, err)
		gatePrompt, err := registry.Get(classifier.Task)
		require.NoError(t, err)
		cls, err := classifier.New(judge, gatePrompt, opts.Clock, classifier.Config{
			Timeout: 300 * time.Millisecond, MaxAttempts: 1,
		})
		require.NoError(t, err)
		lexicon, err := rules.LoadEmbedded()
		require.NoError(t, err)
		detector, err := gate.New(lexicon, cls)
		require.NoError(t, err)

		replyPrompts, err := reply.PromptsFrom(registry)
		require.NoError(t, err)
		catalogue, err := phrases.Load()
		require.NoError(t, err)
		generator, err := reply.New(talk, replyPrompts, catalogue, reply.Options{Thinking: ai.ThinkingLow})
		require.NoError(t, err)

		eopts := engine.Options{
			Store: opts.Store, Sealers: opts.Sealers, Gate: detector, Reply: generator,
			Phrases: catalogue, Clock: opts.Clock, Logger: opts.Logger,
		}
		if adjustEngine != nil {
			adjustEngine(&eopts)
		}
		e, err := engine.New(eopts)
		require.NoError(t, err)

		copts := ConversationOptions{
			Engine: e, Store: opts.Store, Clock: opts.Clock, Logger: opts.Logger,
			IdleCheckAfter: time.Hour, IdleEndAfter: time.Hour,
			MaxMessageBytes: 1024,
			MessageRate:     RateLimit{Burst: 50, Period: time.Minute},
			PingInterval:    time.Hour,
		}
		if adjust != nil {
			adjust(&copts)
		}
		chat, err = NewConversation(copts)
		require.NoError(t, err)
		opts.Conversation = chat
	})

	ts := httptest.NewServer(srv.echo)
	t.Cleanup(ts.Close)
	return &socketFixture{srv: srv, http: ts, talk: talk, judge: judge, chat: chat}
}

// login은 가입한 뒤 세션 쿠키 한 줄을 돌려준다.
func (f *socketFixture) login(t *testing.T, email string) string {
	t.Helper()
	b := f.srv.browser(t)
	rec := b.signup(email)
	cookie := f.srv.setCookie(rec)
	require.NotNil(t, cookie)
	return cookie.Name + "=" + cookie.Value
}

func (f *socketFixture) wsURL() string {
	return "ws" + strings.TrimPrefix(f.http.URL, "http") + ConversationPath
}

// dial은 대화 소켓을 연다. 헤더는 브라우저가 붙이는 것을 흉내 낸다.
func (f *socketFixture) dial(t *testing.T, cookie string, extra map[string]string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	if cookie != "" {
		header.Set("Cookie", cookie)
	}
	header.Set("Origin", f.http.URL)
	header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range extra {
		header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return websocket.Dial(ctx, f.wsURL(), &websocket.DialOptions{HTTPHeader: header})
}

// send는 클라이언트 메시지 하나를 보낸다.
func wsSend(ctx context.Context, t *testing.T, c *websocket.Conn, raw string) {
	t.Helper()
	require.NoError(t, c.Write(ctx, websocket.MessageText, []byte(raw)))
}

// recv는 서버 메시지 하나를 읽어 type과 원본을 돌려준다.
func wsRecv(ctx context.Context, t *testing.T, c *websocket.Conn) (string, map[string]any) {
	t.Helper()
	typ, data, err := c.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageText, typ)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	kind, _ := m["type"].(string)
	return kind, m
}

func wsStartMessage() string { return `{"type":"start","mode":"chat"}` }

func wsUserText(id uuid.UUID, text string) string {
	raw, _ := json.Marshal(map[string]any{"type": "user_text", "client_message_id": id.String(), "text": text})
	return string(raw)
}

// 소켓이 실제로 붙어 있는 경로에서 앞단 미들웨어가 그대로 도는지 본다.
// 묶음이 그룹에 걸린다는 것은 다른 시험이 보지만, 대화 소켓은 AddRoute로 따로 붙으므로 그 경로에서 직접 확인한다.
func TestConversationSocketGuards(t *testing.T) {
	f := newSocketFixture(t, nil)
	cookie := f.login(t, "socket-guard@example.com")

	t.Run("로그인하지 않으면 열리지 않는다", func(t *testing.T) {
		c, resp, err := f.dial(t, "", nil)
		require.Error(t, err)
		require.Nil(t, c)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("다른 출처에서 온 연결은 소켓이 열리기 전에 막힌다", func(t *testing.T) {
		c, resp, err := f.dial(t, cookie, map[string]string{
			"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site",
		})
		require.Error(t, err)
		require.Nil(t, c)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("같은 사이트의 다른 하위 도메인도 막힌다", func(t *testing.T) {
		c, resp, err := f.dial(t, cookie, map[string]string{
			"Origin": "http://blog.localhost:5173", "Sec-Fetch-Site": "same-site",
		})
		require.Error(t, err)
		require.Nil(t, c)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("응답에 no-store가 붙는다", func(t *testing.T) {
		c, resp, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	})
}

// 한 바퀴를 돌려 본다. 그리고 다시 보낸 글이 한 번만 저장되는지 본다.
func TestConversationSocketTurn(t *testing.T) {
	f := newSocketFixture(t, nil)
	cookie := f.login(t, "socket-turn@example.com")
	f.talk.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply("그랬네요. 오늘은 어떤 하루였어요?")
	})
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()

	wsSend(ctx, t, c, wsStartMessage())
	kind, ready := wsRecv(ctx, t, c)
	require.Equal(t, "ready", kind, ready)
	kind, _ = wsRecv(ctx, t, c)
	require.Equal(t, "ai_text", kind, "새 대화에는 첫 안부가 나간다")

	id := uuid.New()
	wsSend(ctx, t, c, wsUserText(id, "오늘 친구랑 놀러갔다왔어"))
	kind, thinking := wsRecv(ctx, t, c)
	require.Equal(t, "thinking", kind, thinking)
	kind, answer := wsRecv(ctx, t, c)
	require.Equal(t, "ai_text", kind, answer)

	t.Run("같은 식별자로 다시 보내면 그때 나간 말이 다시 나온다", func(t *testing.T) {
		wsSend(ctx, t, c, wsUserText(id, "오늘 친구랑 놀러갔다왔어"))
		kind, _ := wsRecv(ctx, t, c)
		require.Equal(t, "thinking", kind)
		kind, again := wsRecv(ctx, t, c)
		require.Equal(t, "ai_text", kind)
		assert.Equal(t, answer["text"], again["text"])
		assert.Equal(t, answer["seq"], again["seq"], "새 발화가 생기지 않는다")
	})

	// 식별자를 다시 쓴 클라이언트가 보낸 다른 글도 관문을 그대로 거친다.
	// 글까지 견주지 않으면 사용자가 방금 한 말이 옛 답으로 갈음되어 사라진다.
	t.Run("같은 식별자에 다른 글을 실어도 그 글이 관문을 거친다", func(t *testing.T) {
		f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
			return fake.Reply(`{"stage":2,"evidence":"죽고 싶다","reason":"direct"}`)
		})
		wsSend(ctx, t, c, wsUserText(id, "죽고 싶다"))
		kind, _ := wsRecv(ctx, t, c)
		require.Equal(t, "thinking", kind)

		// 대응 단계에서는 도움 자원이 고정 문구보다 먼저 나간다.
		kind, resources := wsRecv(ctx, t, c)
		require.Equal(t, "resources", kind, resources)
		kind, crisis := wsRecv(ctx, t, c)
		require.Equal(t, "ai_text", kind)
		assert.NotEqual(t, answer["text"], crisis["text"], "먼저 저장된 글의 답으로 갈음하지 않는다")
		assert.Equal(t, "fixed", crisis["origin"])
		assert.Contains(t, crisis["text"], "109")
	})
}

// 크기 한도와 빈도 한도가 어떻게 끝나는지 본다.
func TestConversationSocketLimits(t *testing.T) {
	t.Run("한도를 넘긴 글은 알리고 닫는다", func(t *testing.T) {
		f := newSocketFixture(t, nil)
		cookie := f.login(t, "socket-big@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()

		wsSend(ctx, t, c, wsUserText(uuid.New(), strings.Repeat("가", 2000)))
		kind, msg := wsRecv(ctx, t, c)
		require.Equal(t, "error", kind, msg)
		assert.Equal(t, string(WsErrorCodeMessageTooLarge), msg["code"])

		_, _, readErr := c.Read(ctx)
		assert.Equal(t, websocket.StatusMessageTooBig, websocket.CloseStatus(readErr))
	})

	t.Run("빈도 한도를 넘기면 거절만 하고 연결은 남는다", func(t *testing.T) {
		f := newSocketFixture(t, func(o *ConversationOptions) {
			o.MessageRate = RateLimit{Burst: 2, Period: time.Hour}
		})
		cookie := f.login(t, "socket-rate@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()

		// 첫 두 개는 통을 비우고, 셋째부터 거절된다. start를 보내지 않았으므로 답은 not_started다.
		for range 2 {
			wsSend(ctx, t, c, wsUserText(uuid.New(), "안녕"))
			kind, msg := wsRecv(ctx, t, c)
			require.Equal(t, "error", kind)
			require.Equal(t, string(WsErrorCodeNotStarted), msg["code"])
		}
		wsSend(ctx, t, c, wsUserText(uuid.New(), "안녕"))
		kind, msg := wsRecv(ctx, t, c)
		require.Equal(t, "error", kind)
		assert.Equal(t, string(WsErrorCodeRateLimited), msg["code"])
	})

	// 한도가 연결마다 새로 차면 다시 잇는 것만으로 지날 수 있다. 글 하나마다 모델을 두세 번 부르므로
	// 그 한도는 비용의 상한이기도 하다. 다시 잇느라 오간 start는 이 통을 쓰지 않아야 한다.
	t.Run("모델을 부르는 글의 한도는 다시 이어도 그대로다", func(t *testing.T) {
		f := newSocketFixture(t, func(o *ConversationOptions) {
			o.MessageRate = RateLimit{Burst: 2, Period: time.Hour}
		})
		cookie := f.login(t, "socket-rate-user@example.com")
		f.talk.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("네, 듣고 있어요.") })
		f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
			return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
		})
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()

		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		wsSend(ctx, t, c, wsStartMessage())
		kind, _ := wsRecv(ctx, t, c)
		require.Equal(t, "ready", kind)
		kind, _ = wsRecv(ctx, t, c)
		require.Equal(t, "ai_text", kind)

		// 글 하나로 사용자 통의 절반을 쓴다. 남은 한 칸은 다시 이은 연결이 받는다.
		wsSend(ctx, t, c, wsUserText(uuid.New(), "오늘은 좀 피곤했어"))
		kind, _ = wsRecv(ctx, t, c)
		require.Equal(t, "thinking", kind)
		kind, _ = wsRecv(ctx, t, c)
		require.Equal(t, "ai_text", kind)
		require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))

		again, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = again.CloseNow() }()
		wsSend(ctx, t, again, wsStartMessage())
		kind, _ = wsRecv(ctx, t, again)
		require.Equal(t, "ready", kind, "다시 잇는 일은 사용자 통을 쓰지 않는다")

		wsSend(ctx, t, again, wsUserText(uuid.New(), "그냥 그랬어"))
		kind, _ = wsRecv(ctx, t, again)
		require.Equal(t, "thinking", kind)
		kind, _ = wsRecv(ctx, t, again)
		require.Equal(t, "ai_text", kind)

		wsSend(ctx, t, again, wsUserText(uuid.New(), "하나 더"))
		kind, limited := wsRecv(ctx, t, again)
		require.Equal(t, "error", kind, limited)
		assert.Equal(t, string(WsErrorCodeRateLimited), limited["code"],
			"다시 이은 연결이 빈 통을 새로 받으면 한도가 비용의 상한이 되지 못한다")
	})

	// start를 보내지 않은 연결은 사용자의 자리에 앉지 않아서 물려받기로 정리되지 않는다. 그 수는 따로 막는다.
	t.Run("한 사람이 열어 둘 수 있는 소켓의 수에 상한이 있다", func(t *testing.T) {
		f := newSocketFixture(t, nil)
		cookie := f.login(t, "socket-count@example.com")

		open := make([]*websocket.Conn, 0, maxSocketsPerUser)
		for range maxSocketsPerUser {
			c, _, err := f.dial(t, cookie, nil)
			require.NoError(t, err)
			open = append(open, c)
		}
		defer func() {
			for _, c := range open {
				_ = c.CloseNow()
			}
		}()

		c, resp, err := f.dial(t, cookie, nil)
		require.Error(t, err)
		require.Nil(t, c)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	})

	// 글이 아닌 프레임을 받으면 알리고 연결을 이어간다.
	// 그 프레임의 남은 바이트를 버리지 않으면 다음 프레임의 머리를 그 바이트에서 읽어 규약 위반으로 끊긴다.
	t.Run("글이 아닌 프레임은 알린 뒤에도 연결이 이어진다", func(t *testing.T) {
		f := newSocketFixture(t, nil)
		cookie := f.login(t, "socket-binary@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()

		require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte("바이너리")))
		kind, msg := wsRecv(ctx, t, c)
		require.Equal(t, "error", kind, msg)
		assert.Equal(t, string(WsErrorCodeInvalidMessage), msg["code"])

		wsSend(ctx, t, c, wsStartMessage())
		kind, ready := wsRecv(ctx, t, c)
		assert.Equal(t, "ready", kind, ready)
	})

	t.Run("한도를 넘긴 소리 프레임은 글일 때와 똑같이 끝난다", func(t *testing.T) {
		f := newSocketFixture(t, nil)
		cookie := f.login(t, "socket-binary-big@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()

		// 소리 프레임의 한도는 글의 한도와 따로 있다. 웹앱이 보내는 100ms 조각의 열 배가 넘는 프레임은 고장 난 클라이언트다.
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, maxAudioFrameBytes+1)))
		kind, msg := wsRecv(ctx, t, c)
		require.Equal(t, "error", kind, msg)
		assert.Equal(t, string(WsErrorCodeMessageTooLarge), msg["code"])

		_, _, readErr := c.Read(ctx)
		assert.Equal(t, websocket.StatusMessageTooBig, websocket.CloseStatus(readErr))
	})
}

// 연결을 물려받는 길과, 끝난 뒤에 남는 고루틴을 본다.
func TestConversationSocketTakeoverAndLeak(t *testing.T) {
	f := newSocketFixture(t, nil)
	cookie := f.login(t, "socket-takeover@example.com")
	f.talk.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("네, 듣고 있어요.") })
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	first, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	wsSend(ctx, t, first, wsStartMessage())
	kind, _ := wsRecv(ctx, t, first)
	require.Equal(t, "ready", kind)
	kind, _ = wsRecv(ctx, t, first)
	require.Equal(t, "ai_text", kind)

	second, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	wsSend(ctx, t, second, wsStartMessage())
	kind, resumed := wsRecv(ctx, t, second)
	require.Equal(t, "ready", kind)
	assert.Equal(t, true, resumed["resumed"], "두 번째 연결은 열린 대화를 이어받는다")

	t.Run("앞선 연결은 4001로 물러난다", func(t *testing.T) {
		_, _, readErr := first.Read(ctx)
		assert.Equal(t, statusTakenOver, websocket.CloseStatus(readErr))
	})

	_ = first.CloseNow()
	require.NoError(t, second.Close(websocket.StatusNormalClosure, ""))
}

// 연결이 끝나면 고루틴과 소켓이 바로 풀리는지 본다.
//
// run의 defer가 쌓인 순서가 뒤집혀 있으면 기다림이 취소보다 먼저 돈다. ping을 보내는 고루틴은 그 컨텍스트가
// 끝나기를 기다리고 있으므로, 돌아가려던 run이 거기서 멈춘다. 넘겨받은 연결은 요청 컨텍스트도 핸들러가 돌아가야
// 끝나므로 풀어 줄 다른 길이 없고, 다음 ping 차례가 와서 닫힌 소켓에 쓰기를 시도하고 실패해야 비로소 풀린다.
// 그 사이에는 고루틴과 소켓이 그대로 남는다(운영 기본값으로 30초).
func TestConversationSocketCleanupDoesNotWaitForPing(t *testing.T) {
	// ping 차례를 아주 길게 둔다. 차례를 기다려야 풀리는 구조라면 여기서 시간이 다 간다.
	const pingInterval = 30 * time.Second
	f := newSocketFixture(t, func(o *ConversationOptions) { o.PingInterval = pingInterval })
	cookie := f.login(t, "socket-cleanup@example.com")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	settle := func() int {
		var n int
		for range 20 {
			time.Sleep(50 * time.Millisecond)
			n = runtime.NumGoroutine()
		}
		return n
	}
	base := settle()

	const conns = 5
	for range conns {
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		wsSend(ctx, t, c, wsStartMessage())
		kind, _ := wsRecv(ctx, t, c)
		require.Equal(t, "ready", kind)
		require.NoError(t, c.Close(websocket.StatusNormalClosure, ""))
	}

	// ping 차례를 한참 남겨 두고도 풀려야 한다. 시각을 읽지 않고 잠깐씩 쉬며 센다.
	const step = 50 * time.Millisecond
	after := runtime.NumGoroutine()
	for range 40 {
		if after <= base+2 {
			break
		}
		time.Sleep(step)
		after = runtime.NumGoroutine()
	}
	assert.LessOrEqual(t, after, base+2,
		"닫은 연결이 ping 차례까지 고루틴과 소켓을 붙들고 있다 (base=%d)", base)
}

// 서버가 내려갈 때 열린 소켓이 정말로 닫히는지, 그리고 여럿이어도 차례로 기다리지 않는지 본다.
func TestConversationSocketDrainOnShutdown(t *testing.T) {
	f := newSocketFixture(t, nil)
	f.talk.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("네, 듣고 있어요.") })
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const people = 4
	conns := make([]*websocket.Conn, 0, people)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.CloseNow()
		}
	})
	for i := range people {
		cookie := f.login(t, fmt.Sprintf("socket-drain-%d@example.com", i))
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		conns = append(conns, c)
		wsSend(ctx, t, c, wsStartMessage())
		kind, _ := wsRecv(ctx, t, c)
		require.Equal(t, "ready", kind)
		kind, _ = wsRecv(ctx, t, c)
		require.Equal(t, "ai_text", kind)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.chat.Shutdown(ctx)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		require.Fail(t, "내려가는 일이 끝나지 않았다")
	}

	// 돌아왔다는 것은 소켓이 모두 닫힌 뒤라는 뜻이다.
	for _, c := range conns {
		_, _, readErr := c.Read(ctx)
		require.Error(t, readErr)
		assert.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(readErr))
	}
}

// 끝내기 단추는 대화 모델이 답하기를 기다리지 않는다.
//
// 턴을 이 고리 안에서 돌리면 턴이 끝날 때까지 다음 프레임을 읽지 못하고, 엔진의 End도 그 턴이 쥔 잠금을 기다린다.
// 그러면 모델이 늦는 만큼 끝내기가 먹히지 않는다. 끝내기는 언제나 있어야 한다.
func TestConversationSocketEndDuringASlowTurn(t *testing.T) {
	const modelLatency = 10 * time.Second
	f := newSocketFixture(t, nil)
	cookie := f.login(t, "socket-end-slow@example.com")
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
	})
	f.talk.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
		select {
		case <-ctx.Done():
			return fake.Fail(ai.ContextError(ctx.Err(), ai.Detail{Task: "conversation"}))
		case <-time.After(modelLatency):
			return fake.Reply("네, 듣고 있어요.")
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	wsSend(ctx, t, c, wsStartMessage())
	kind, _ := wsRecv(ctx, t, c)
	require.Equal(t, "ready", kind)
	kind, _ = wsRecv(ctx, t, c)
	require.Equal(t, "ai_text", kind)

	wsSend(ctx, t, c, wsUserText(uuid.New(), "오늘은 좀 피곤했어"))
	kind, _ = wsRecv(ctx, t, c)
	require.Equal(t, "thinking", kind)

	// 모델이 아직 답하기 전에 끝내기를 누른다. 시각을 읽지 않고, 읽기에 준 시간으로 잰다.
	// 끝내기가 모델의 답을 기다리면 이 시간 안에 아무것도 오지 않아 읽기가 실패한다.
	wsSend(ctx, t, c, `{"type":"end"}`)
	readCtx, cancelRead := context.WithTimeout(ctx, modelLatency/2)
	defer cancelRead()
	for {
		kind, _ := wsRecv(readCtx, t, c)
		if kind == "ended" {
			return
		}
		require.NotEqual(t, "ai_text", kind, "끝내기보다 모델의 답이 먼저 나갔다")
	}
}

// 세션이 끝난 뒤에 남은 쿠키로는 소켓이 열리지 않는지 본다.
func TestConversationSocketSessionExpiry(t *testing.T) {
	f := newSocketFixture(t, nil)
	cookie := f.login(t, "socket-expiry@example.com")

	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	_ = c.CloseNow()

	// 세션의 전체 수명을 넘긴다.
	f.srv.clock.Advance(testSessionConfig.AbsoluteLifetime + time.Hour)

	c2, resp, err := f.dial(t, cookie, nil)
	require.Error(t, err)
	require.Nil(t, c2)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
