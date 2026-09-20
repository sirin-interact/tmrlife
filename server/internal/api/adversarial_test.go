package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 이 파일은 대화 소켓의 안전 경로를 실제 연결 위에서 괴롭힌다.
//
// 엔진 시험은 엔진 안에서 말이 어떻게 정해지는지를 본다. 여기서는 그 말이 정말 선 위로 나가는지,
// 판정 단계가 화면 쪽으로 새지 않는지, 연결이 끊기거나 빼앗기거나 한도에 걸렸을 때
// 무거운 말이 조용히 사라지지 않는지를 본다.

const (
	advCrisisSay   = "죽고 싶다"
	advOrdinarySay = "오늘 친구랑 놀러갔다왔어"
	advReply       = "오 재밌게 놀고 왔네요. 어디 다녀왔어요?"
)

// advChannel은 가짜 모델을 붙인 대화 소켓 하나다. 앞단 미들웨어는 운영과 같은 것이 그대로 돈다.
type advChannel struct {
	srv   *testServer
	http  *httptest.Server
	talk  *fake.LLM
	judge *fake.LLM
	chat  *Conversation
}

func advNewChannel(t *testing.T, adjust func(*ConversationOptions)) *advChannel {
	t.Helper()
	talk := fake.New("fake-conversation")
	judge := fake.New("fake-gate")
	var chat *Conversation

	srv := newTestServer(t, func(opts *Options) {
		registry, err := prompts.LoadEmbedded()
		require.NoError(t, err)
		gatePrompt, err := registry.Get(classifier.Task)
		require.NoError(t, err)
		// 시험에서는 판별을 짧게 기다린다. 실제 설정값보다 짧게 두어 시험이 오래 걸리지 않게 한다.
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

		e, err := engine.New(engine.Options{
			Store: opts.Store, Sealers: opts.Sealers, Gate: detector, Reply: generator,
			Phrases: catalogue, Clock: opts.Clock, Logger: opts.Logger,
		})
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
	return &advChannel{srv: srv, http: ts, talk: talk, judge: judge, chat: chat}
}

// advLogin은 가입한 뒤 세션 쿠키 한 줄을 돌려준다.
func (c *advChannel) advLogin(t *testing.T, email string) string {
	t.Helper()
	rec := c.srv.browser(t).signup(email)
	cookie := c.srv.setCookie(rec)
	require.NotNil(t, cookie)
	return cookie.Name + "=" + cookie.Value
}

// advDial은 대화 소켓을 연다. 브라우저가 붙이는 헤더를 흉내 낸다.
func (c *advChannel) advDial(t *testing.T, cookie string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", cookie)
	header.Set("Origin", c.http.URL)
	header.Set("Sec-Fetch-Site", "same-origin")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx,
		"ws"+strings.TrimPrefix(c.http.URL, "http")+ConversationPath,
		&websocket.DialOptions{HTTPHeader: header})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// advFrame은 서버가 보낸 메시지 하나다. raw를 함께 들고 있어서 명세에 없는 값이 섞였는지도 볼 수 있다.
type advFrame struct {
	kind   string
	fields map[string]any
	raw    string
}

func advSend(t *testing.T, conn *websocket.Conn, raw string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(raw)))
}

func advRead(t *testing.T, conn *websocket.Conn) advFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageText, typ)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	kind, _ := fields["type"].(string)
	return advFrame{kind: kind, fields: fields, raw: string(data)}
}

// advReadUntil은 그 종류의 메시지가 올 때까지 읽는다. 지나친 메시지도 모두 돌려준다.
func (c *advChannel) advReadUntil(t *testing.T, conn *websocket.Conn, kind string) ([]advFrame, advFrame) {
	t.Helper()
	var seen []advFrame
	for range 20 {
		frame := advRead(t, conn)
		seen = append(seen, frame)
		if frame.kind == kind {
			return seen, frame
		}
	}
	require.FailNow(t, "기다리던 메시지가 오지 않았다: "+kind)
	return seen, advFrame{}
}

func advStart() string { return `{"type":"start","mode":"chat"}` }

func advUserText(id uuid.UUID, text string) string {
	raw, err := json.Marshal(map[string]any{
		"type": "user_text", "client_message_id": id.String(), "text": text,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// advJudges는 판별 모델의 대본을 넣는다.
func (c *advChannel) advJudges(t *testing.T, stage int) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"stage": stage, "evidence": ""})
	require.NoError(t, err)
	c.judge.Enqueue(fake.Reply(string(raw)))
}

// advGateEvents는 그 사용자의 관문 기록을 최종 단계만 뽑아 돌려준다.
func (c *advChannel) advGateEvents(t *testing.T, userID uuid.UUID, conversationID uuid.UUID) []int16 {
	t.Helper()
	rows, err := c.srv.store.Queries().ListUtterancesByConversation(t.Context(),
		db.ListUtterancesByConversationParams{ConversationID: conversationID, UserID: userID})
	require.NoError(t, err)
	var out []int16
	for _, row := range rows {
		if row.Speaker != store.SpeakerUser {
			continue
		}
		event, err := c.srv.store.Queries().GetGateEventByUtterance(t.Context(),
			db.GetGateEventByUtteranceParams{UtteranceID: row.ID, UserID: userID})
		if err != nil {
			continue
		}
		out = append(out, event.FinalStage)
	}
	return out
}

func advUserID(t *testing.T, c *advChannel, email string) uuid.UUID {
	t.Helper()
	user, err := c.srv.store.Queries().GetUserByEmail(t.Context(), email)
	require.NoError(t, err)
	return user.ID
}

// 위기 발화 하나가 선 위에서 어떻게 다뤄지는지를 처음부터 끝까지 본다.
// 고정 문구와 자원이 나가야 하고, 판정은 저장돼 있어야 하고, 판정 단계는 어느 메시지에도 실리면 안 된다.
func TestSocketCrisisTurnDeliversTheFixedPhraseAndResources(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, nil)
	const email = "crisis-wire@example.com"
	cookie := c.advLogin(t, email)
	conn := c.advDial(t, cookie)

	advSend(t, conn, advStart())
	_, ready := c.advReadUntil(t, conn, "ready")
	conversationID := uuid.MustParse(ready.fields["conversation_id"].(string))
	require.False(t, ready.fields["resources_pinned"].(bool))
	_, opening := c.advReadUntil(t, conn, "ai_text")
	require.Equal(t, "fixed", opening.fields["origin"])

	c.advJudges(t, 2)
	advSend(t, conn, advUserText(uuid.New(), advCrisisSay))

	// 도움 자원이 고정 문구보다 먼저 나간다. 나가는 말을 저장하는 쓰기가 실패해도 번호는 화면에 남아야 하기 때문이다.
	before, resources := c.advReadUntil(t, conn, "resources")
	require.Equal(t, "thinking", before[0].kind, "받았다는 표시가 먼저 온다")
	items, ok := resources.fields["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 3, "도움 자원이 화면에 고정돼야 한다")

	_, answer := c.advReadUntil(t, conn, "ai_text")
	assert.Equal(t, "fixed", answer.fields["origin"], "위기 응답은 모델의 말이 아니어야 한다")
	assert.Contains(t, answer.fields["text"], "109", "화면에 고정할 번호가 말 안에 있어야 한다")
	assert.Zero(t, c.talk.Calls(), "위기 발화에는 대화 모델을 부르지 않는다")

	userID := advUserID(t, c, email)
	assert.Equal(t, []int16{2}, c.advGateEvents(t, userID, conversationID), "판정이 저장돼 있어야 한다")

	// 판정 단계는 화면 쪽으로 나가지 않는다.
	for _, frame := range append(before, resources) {
		assert.NotContains(t, frame.raw, `"stage"`, "관문의 단계가 클라이언트로 나갔다")
		assert.NotContains(t, frame.raw, `"final_stage"`)
		assert.NotContains(t, frame.raw, `"evidence"`)
	}
}

// 오류 메시지는 사용자가 쓴 글을 되돌려 보내지 않는다. 로그에도 남지 않는다.
func TestSocketErrorsAndLogsCarryNoUserText(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, nil)
	cookie := c.advLogin(t, "quiet-wire@example.com")
	conn := c.advDial(t, cookie)

	// 아직 시작하지 않았는데 글을 보냈다.
	secret := "아무한테도 말 못 했는데 요즘 정말 힘들어"
	id := uuid.New()
	advSend(t, conn, advUserText(id, secret))
	_, notStarted := c.advReadUntil(t, conn, "error")
	assert.Equal(t, "not_started", notStarted.fields["code"])
	assert.NotContains(t, notStarted.raw, secret, "오류가 사용자의 글을 되돌려 보냈다")
	assert.Equal(t, id.String(), notStarted.fields["client_message_id"], "어느 글인지는 식별자로 알린다")

	// 모르는 type이다. 풀어낸 오류 문구에는 보낸 값이 그대로 들어 있다.
	advSend(t, conn, `{"type":"`+secret+`"}`)
	_, invalid := c.advReadUntil(t, conn, "error")
	assert.Equal(t, "invalid_message", invalid.fields["code"])
	assert.NotContains(t, invalid.raw, secret)

	logs := c.srv.logs.String()
	require.NotEmpty(t, logs)
	for _, word := range strings.Fields(secret) {
		assert.NotContains(t, logs, word, "사용자의 글이 로그에 남았다")
	}
	assert.NotContains(t, c.srv.bodies.String(), secret)
}

// 한도에 걸린 글은 관문을 거치지 않는다. 거치지 않았으면 기록도 남지 않아야 하고,
// 클라이언트는 다시 보내라는 신호를 받아야 한다. 무거운 말이 조용히 사라지는 길이 없어야 한다.
func TestSocketRateLimitedMessageIsNeitherGatedNorStored(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, func(opts *ConversationOptions) {
		// start 하나로 통을 비운다. 그다음 글은 한도에 걸린다.
		opts.MessageRate = RateLimit{Burst: 1, Period: time.Hour}
	})
	const email = "limited-wire@example.com"
	cookie := c.advLogin(t, email)
	conn := c.advDial(t, cookie)

	advSend(t, conn, advStart())
	_, ready := c.advReadUntil(t, conn, "ready")
	conversationID := uuid.MustParse(ready.fields["conversation_id"].(string))
	c.advReadUntil(t, conn, "ai_text")

	id := uuid.New()
	c.advJudges(t, 2)
	advSend(t, conn, advUserText(id, advCrisisSay))
	_, limited := c.advReadUntil(t, conn, "error")
	assert.Equal(t, "rate_limited", limited.fields["code"])

	userID := advUserID(t, c, email)
	assert.Empty(t, c.advGateEvents(t, userID, conversationID), "한도에 걸린 글은 판정을 남기지 않는다")
	rows, err := c.srv.store.Queries().ListUtterancesByConversation(t.Context(),
		db.ListUtterancesByConversationParams{ConversationID: conversationID, UserID: userID})
	require.NoError(t, err)
	require.Len(t, rows, 1, "첫 안부만 남아야 한다")
	assert.Equal(t, store.SpeakerAI, rows[0].Speaker)
	assert.Zero(t, c.judge.Calls(), "한도에 걸린 글에는 판별을 부르지 않는다")
}

// 답이 나가려는 순간에 연결이 사라진 경우다. 대화 기록에는 남지만 화면은 그것을 보지 못했다.
// 다시 이으면 그 말이 지난 말로 돌아오고, 같은 식별자로 다시 보내도 새 답을 지어내지 않고 그때의 말을 다시 낸다.
func TestSocketResendAfterADropDeliversTheCrisisAnswer(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, nil)
	const email = "dropped-wire@example.com"
	cookie := c.advLogin(t, email)
	userID := advUserID(t, c, email)

	first := c.advDial(t, cookie)
	advSend(t, first, advStart())
	_, ready := c.advReadUntil(t, first, "ready")
	conversationID := uuid.MustParse(ready.fields["conversation_id"].(string))
	c.advReadUntil(t, first, "ai_text")

	// 글을 받았다는 표시까지만 보고 연결이 끊긴다. 돌던 모델 호출은 거기서 멈추지만 판정은 남는다.
	// 판정이 남지 않으면 무거운 말이 오간 대화가 자동 일기의 재료로 흘러간다.
	c.advJudges(t, 2)
	id := uuid.New()
	advSend(t, first, advUserText(id, advCrisisSay))
	c.advReadUntil(t, first, "thinking")
	require.NoError(t, first.CloseNow())
	advWaitForGateEvent(t, c, userID, conversationID)

	// 다시 이으면 자원이 다시 고정된다. 끊긴 턴의 답은 다시 보낼 때 나온다.
	second := c.advDial(t, cookie)
	advSend(t, second, advStart())
	_, resumed := c.advReadUntil(t, second, "ready")
	assert.True(t, resumed.fields["resumed"].(bool))
	assert.Equal(t, conversationID.String(), resumed.fields["conversation_id"])
	assert.True(t, resumed.fields["resources_pinned"].(bool))
	c.advReadUntil(t, second, "resources")

	// 웹앱은 답을 받지 못한 글을 같은 식별자로 다시 보낸다. 저장된 판정을 그대로 써서 고정 문구가 나간다.
	advSend(t, second, advUserText(id, advCrisisSay))
	_, answer := c.advReadUntil(t, second, "ai_text")
	assert.Equal(t, "fixed", answer.fields["origin"])
	assert.Contains(t, answer.fields["text"], "109", "끊겼던 위기 발화의 답이 돌아와야 한다")

	assert.Equal(t, []int16{2}, c.advGateEvents(t, userID, conversationID), "판정은 한 번만 남는다")
	assert.Equal(t, 1, c.judge.Calls(), "다시 보낸 글에 판별을 또 부르지 않는다")
	assert.Zero(t, c.talk.Calls(), "위기 발화에는 대화 모델을 부르지 않는다")
}

// advWaitForGateEvent는 그 대화에 관문 기록이 남을 때까지 기다린다.
// 연결이 끊겨도 판정만은 남는 것을 확인하는 자리다.
func advWaitForGateEvent(t *testing.T, c *advChannel, userID, conversationID uuid.UUID) {
	t.Helper()
	// 시각을 읽지 않고 흐른 시간만 센다. 모두 합쳐 15초를 기다린다.
	for range 150 {
		if len(c.advGateEvents(t, userID, conversationID)) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.FailNow(t, "연결이 끊겼다고 위기 판정까지 사라졌다")
}

// 다른 기기가 끼어들어 연결을 빼앗아도, 이미 내려진 위기 판정과 자원 고정은 새 연결에 그대로 이어져야 한다.
func TestSocketTakeoverKeepsTheCrisisState(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, nil)
	const email = "takeover-wire@example.com"
	cookie := c.advLogin(t, email)

	first := c.advDial(t, cookie)
	advSend(t, first, advStart())
	_, ready := c.advReadUntil(t, first, "ready")
	conversationID := uuid.MustParse(ready.fields["conversation_id"].(string))
	c.advReadUntil(t, first, "ai_text")

	c.advJudges(t, 2)
	advSend(t, first, advUserText(uuid.New(), advCrisisSay))
	c.advReadUntil(t, first, "resources")
	c.advReadUntil(t, first, "ai_text")

	// 두 번째 기기가 같은 대화를 연다. 앞선 연결은 물러난다.
	second := c.advDial(t, cookie)
	advSend(t, second, advStart())
	_, resumed := c.advReadUntil(t, second, "ready")
	assert.True(t, resumed.fields["resumed"].(bool))
	assert.True(t, resumed.fields["resources_pinned"].(bool), "빼앗은 연결에도 자원 고정이 이어져야 한다")
	_, resources := c.advReadUntil(t, second, "resources")
	items, ok := resources.fields["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 3)

	// 앞선 연결은 다시 이으라는 뜻의 코드로 닫힌다.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, _, err := first.Read(ctx)
	require.Error(t, err)
	assert.Equal(t, statusTakenOver, websocket.CloseStatus(err))

	// 대화는 열린 채다. 빼앗겼다고 그날의 기록이 닫히지 않는다.
	userID := advUserID(t, c, email)
	row, err := c.srv.store.Queries().GetConversation(t.Context(),
		db.GetConversationParams{ID: conversationID, UserID: userID})
	require.NoError(t, err)
	assert.Equal(t, store.ConversationActive, row.Conversation.Status)
}

// 서버가 내려가는 중에 턴이 돌고 있으면, 연결은 닫히되 대화는 열린 채로 남아 다시 이을 수 있어야 한다.
func TestSocketShutdownMidTurnLeavesTheConversationResumable(t *testing.T) {
	t.Parallel()
	c := advNewChannel(t, nil)
	const email = "drain-wire@example.com"
	cookie := c.advLogin(t, email)
	conn := c.advDial(t, cookie)

	advSend(t, conn, advStart())
	_, ready := c.advReadUntil(t, conn, "ready")
	conversationID := uuid.MustParse(ready.fields["conversation_id"].(string))
	c.advReadUntil(t, conn, "ai_text")

	release := make(chan struct{})
	c.judge.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
		close(release)
		<-ctx.Done()
		return fake.Fail(ai.ContextError(ctx.Err(), ai.Detail{Task: "gate_classifier"}))
	})
	advSend(t, conn, advUserText(uuid.New(), advOrdinarySay))
	<-release

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.chat.Shutdown(t.Context())
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		require.Fail(t, "내려가는 일이 끝나지 않았다")
	}

	userID := advUserID(t, c, email)
	row, err := c.srv.store.Queries().GetConversation(t.Context(),
		db.GetConversationParams{ID: conversationID, UserID: userID})
	require.NoError(t, err)
	assert.Equal(t, store.ConversationActive, row.Conversation.Status, "대화는 열린 채로 남아야 한다")
}
