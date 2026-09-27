package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// 시험은 실제 시계를 읽지 않는다. 가짜 시계는 이 시각에서 출발한다(서울의 저녁 9시).
var startTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

const (
	conversationModel = "fake-conversation"
	gateModel         = "fake-gate"
	// gateTimeout은 시험에서 AI 판별을 기다리는 시간이다. 실제 설정값(2.5초)보다 짧게 두어 시험이 오래 걸리지 않게 한다.
	gateTimeout = 300 * time.Millisecond
)

// 시험에 쓰는 말이다. 규칙 겹이 어떻게 보는지는 관문 사전이 정한다.
const (
	sayOrdinary = "오늘 친구랑 놀러갔다왔어"
	sayTired    = "오늘은 하루종일 우울해서 집에만 있었어"
	sayVague    = "그냥 다 사라졌으면 좋겠어"
	sayVague2   = "몰라 그냥 내가 없어도 아무도 모를 것 같아"
	sayCrisis   = "죽고 싶다"
	sayUrgent   = "약을 모아뒀어"
	sayIdiom    = "배고파 죽겠다"

	replyOrdinary = "오 재밌게 놀고 왔네요. 어디 다녀왔어요?"
	replyShort    = "그런 날 있죠. 오늘은 푹 쉬어요."
	replyMirror   = "사라졌으면 좋겠다는 말이 마음에 남아요. 오늘 무슨 일 있었어요?"
	replyListen   = "그랬네요. 지금 여기 같이 있을게요."
)

// syncBuffer는 여러 고루틴이 함께 쓰는 로그를 모은다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recorder는 엔진이 낸 사건을 받아 두는 Sink다.
type recorder struct {
	mu     sync.Mutex
	events []engine.Event
	// hook은 사건마다 불린다. 사건이 나간 그 순간의 DB를 들여다볼 때 쓴다.
	hook func(e engine.Event) error
}

func (r *recorder) Emit(_ context.Context, e engine.Event) error {
	r.mu.Lock()
	hook := r.hook
	r.events = append(r.events, e)
	r.mu.Unlock()
	if hook != nil {
		return hook(e)
	}
	return nil
}

func (r *recorder) all() []engine.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Event(nil), r.events...)
}

// texts는 지금까지 나간 AI의 말을 순서대로 돌려준다.
func (r *recorder) texts() []string {
	var out []string
	for _, e := range r.all() {
		if t, ok := e.(engine.AIText); ok {
			out = append(out, t.Text)
		}
	}
	return out
}

// lastText는 마지막으로 나간 AI의 말이다.
func (r *recorder) lastText(t *testing.T) engine.AIText {
	t.Helper()
	events := r.all()
	for i := len(events) - 1; i >= 0; i-- {
		if text, ok := events[i].(engine.AIText); ok {
			return text
		}
	}
	require.Fail(t, "나간 말이 하나도 없다")
	return engine.AIText{}
}

func (r *recorder) count(match func(e engine.Event) bool) int {
	n := 0
	for _, e := range r.all() {
		if match(e) {
			n++
		}
	}
	return n
}

// lastResources는 마지막으로 나간 도움 자원이다. 자원은 고정 문구보다 먼저 나간다.
func (r *recorder) lastResources(t *testing.T) engine.Resources {
	t.Helper()
	events := r.all()
	for i := len(events) - 1; i >= 0; i-- {
		if items, ok := events[i].(engine.Resources); ok {
			return items
		}
	}
	require.Fail(t, "자원이 한 번도 나가지 않았다")
	return engine.Resources{}
}

func (r *recorder) resources() int {
	return r.count(func(e engine.Event) bool { _, ok := e.(engine.Resources); return ok })
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

type fixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	store    *store.Store
	sealers  *sealing.Sealers
	clock    *clock.Fake
	talk     *fake.LLM
	judge    *fake.LLM
	logs     *syncBuffer
	logger   *slog.Logger
	engine   *engine.Engine
	sink     *recorder
	diary    *fakeEnqueuer
	analysis *fakeAnalysisEnqueuer

	userID uuid.UUID
	sealer *crypto.Sealer

	// plaintexts는 시험이 다룬 모든 글이다. 로그에 하나도 나오면 안 된다.
	mu         sync.Mutex
	plaintexts []string
}

// fakeEnqueuer는 일기 초안 작업이 등록되었는지만 센다. 큐를 띄우지 않고 트랜잭션에 묶이는 것만 본다.
type fakeEnqueuer struct {
	mu   sync.Mutex
	args []diaryArgs
	err  error
}

type diaryArgs struct {
	userID         uuid.UUID
	dayID          uuid.UUID
	conversationID uuid.UUID
}

func (f *fakeEnqueuer) EnqueueTx(_ context.Context, tx pgx.Tx, args diary.DraftArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if tx == nil {
		panic("일기 작업은 대화를 끝내는 트랜잭션 안에서 등록돼야 한다")
	}
	f.args = append(f.args, diaryArgs{userID: args.UserID, dayID: args.DayID, conversationID: args.ConversationID})
	return nil
}

func (f *fakeEnqueuer) calls() []diaryArgs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]diaryArgs(nil), f.args...)
}

// fakeAnalysisEnqueuer는 신호 추출 작업이 등록되었는지만 센다.
// 트랜잭션의 쿼리까지 받는지도 함께 본다. 추출 쪽은 그 쿼리로 대화의 분석 상태를 적기 때문에,
// 쿼리 없이 불리면 "pending인데 작업이 없는" 대화가 생긴다.
type fakeAnalysisEnqueuer struct {
	mu   sync.Mutex
	args []analysisArgs
	err  error
}

type analysisArgs struct {
	userID         uuid.UUID
	conversationID uuid.UUID
}

func (f *fakeAnalysisEnqueuer) EnqueueTx(
	_ context.Context, tx pgx.Tx, q *db.Queries, args analysis.ExtractArgs,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if tx == nil || q == nil {
		panic("신호 추출 작업은 대화를 끝내는 트랜잭션 안에서 등록돼야 한다")
	}
	f.args = append(f.args, analysisArgs{userID: args.UserID, conversationID: args.ConversationID})
	return nil
}

func (f *fakeAnalysisEnqueuer) calls() []analysisArgs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]analysisArgs(nil), f.args...)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)

	ring, err := crypto.NewKeyRing(1, map[int][]byte{1: bytes.Repeat([]byte{0x2a}, 32)})
	require.NoError(t, err)
	cache, err := crypto.NewKeyCache(crypto.KeyCacheOptions{MaxEntries: 16})
	require.NoError(t, err)
	sealers, err := sealing.New(st.Queries(), ring, cache)
	require.NoError(t, err)

	userID, err := store.NewID()
	require.NoError(t, err)
	key, err := ring.NewUserKey(userID)
	require.NoError(t, err)
	_, err = st.CreateUser(t.Context(), store.NewUser{
		ID: userID, Email: "mina@example.com", Timezone: "Asia/Seoul",
		WrappedDEK: key.Wrapped, KEKVersion: int16(key.KEKVersion), Now: startTime,
	})
	require.NoError(t, err)

	f := &fixture{
		t:        t,
		pool:     pool,
		store:    st,
		sealers:  sealers,
		clock:    clock.NewFake(startTime),
		talk:     fake.New(conversationModel),
		judge:    fake.New(gateModel),
		logs:     &syncBuffer{},
		sink:     &recorder{},
		diary:    &fakeEnqueuer{},
		analysis: &fakeAnalysisEnqueuer{},
		userID:   userID,
		sealer:   key.Sealer,
	}
	// 운영과 같은 핸들러에, 가장 낮은 수준까지 모두 남긴다.
	f.logger = logging.New(f.logs, slog.LevelDebug)
	f.engine = f.newEngine(nil)
	return f
}

// newEngine은 시험용 엔진을 만든다. adjust로 기본 설정을 바꾼다.
func (f *fixture) newEngine(adjust func(*engine.Options)) *engine.Engine {
	f.t.Helper()
	registry, err := prompts.LoadEmbedded()
	require.NoError(f.t, err)

	gatePrompt, err := registry.Get(classifier.Task)
	require.NoError(f.t, err)
	cls, err := classifier.New(f.judge, gatePrompt, f.clock, classifier.Config{
		Timeout: gateTimeout, MaxAttempts: 1,
	})
	require.NoError(f.t, err)
	lexicon, err := rules.LoadEmbedded()
	require.NoError(f.t, err)
	detector, err := gate.New(lexicon, cls)
	require.NoError(f.t, err)

	replyPrompts, err := reply.PromptsFrom(registry)
	require.NoError(f.t, err)
	catalogue, err := phrases.Load()
	require.NoError(f.t, err)
	generator, err := reply.New(f.talk, replyPrompts, catalogue, reply.Options{Thinking: ai.ThinkingLow})
	require.NoError(f.t, err)

	opts := engine.Options{
		Store:    f.store,
		Sealers:  f.sealers,
		Gate:     detector,
		Reply:    generator,
		Phrases:  catalogue,
		Diary:    f.diary,
		Analysis: f.analysis,
		Clock:    f.clock,
		Logger:   f.logger,
	}
	if adjust != nil {
		adjust(&opts)
	}
	e, err := engine.New(opts)
	require.NoError(f.t, err)
	return e
}

// start는 대화를 시작하거나 이어간다.
func (f *fixture) start() *engine.Session {
	f.t.Helper()
	s, err := f.engine.Start(f.t.Context(), engine.StartInput{
		User: engine.Participant{ID: f.userID, Timezone: "Asia/Seoul"},
		Mode: store.ModeChat,
		Sink: f.sink,
	})
	require.NoError(f.t, err)
	return s
}

// say는 사용자가 글 하나를 보낸 것과 같다. 판별 모델과 대화 모델의 대본은 부르기 전에 넣어 둔다.
func (f *fixture) say(s *engine.Session, text string) engine.Turn {
	f.t.Helper()
	f.remember(text)
	turn, err := f.engine.Handle(f.t.Context(), s, engine.Say{ClientMessageID: newID(f.t), Text: text})
	require.NoError(f.t, err)
	return turn
}

// judges는 판별 모델이 돌려줄 답을 대본에 넣는다.
func (f *fixture) judges(stage crisis.Stage, evidence string) {
	f.t.Helper()
	raw, err := json.Marshal(map[string]any{"stage": int(stage), "evidence": evidence})
	require.NoError(f.t, err)
	f.judge.Enqueue(fake.Reply(string(raw)))
}

// judgeFails는 판별 모델이 실패하게 한다.
func (f *fixture) judgeFails(step fake.Step) {
	f.t.Helper()
	f.judge.Enqueue(step)
}

// replies는 대화 모델이 돌려줄 답을 대본에 넣는다.
func (f *fixture) replies(steps ...fake.Step) {
	f.t.Helper()
	for _, step := range steps {
		if step.Text != "" {
			f.remember(step.Text)
		}
	}
	f.talk.Enqueue(steps...)
}

// turn은 판별과 답을 한꺼번에 준비하고 한 턴을 돌린다.
func (f *fixture) turn(s *engine.Session, text string, stage crisis.Stage, evidence, answer string) engine.Turn {
	f.t.Helper()
	f.judges(stage, evidence)
	if answer != "" {
		f.replies(fake.Reply(answer))
	}
	return f.say(s, text)
}

func (f *fixture) remember(texts ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plaintexts = append(f.plaintexts, texts...)
}

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := store.NewID()
	require.NoError(t, err)
	return id
}

// storedUtterances는 그 대화에 저장된 말을 순번대로, 글을 열어 돌려준다.
func (f *fixture) storedUtterances(conversationID uuid.UUID) []engine.Utterance {
	f.t.Helper()
	rows, err := f.store.Queries().ListUtterancesByConversation(f.t.Context(), db.ListUtterancesByConversationParams{
		ConversationID: conversationID, UserID: f.userID,
	})
	require.NoError(f.t, err)
	out := make([]engine.Utterance, 0, len(rows))
	for _, row := range rows {
		text, err := f.sealer.OpenString(row.TextEnc, sealing.UtteranceText(row.ID))
		require.NoError(f.t, err, "발화는 그 발화의 자리에 묶여 잠겨 있어야 한다")
		out = append(out, engine.Utterance{
			Seq: row.Seq, Speaker: row.Speaker, Origin: row.Origin,
			Text: text, ClientMessageID: row.ClientMessageID, CreatedAt: row.CreatedAt,
		})
	}
	return out
}

// gateEvents는 그 대화의 관문 기록을 저장된 순서대로 돌려준다.
func (f *fixture) gateEvents(conversationID uuid.UUID) []db.GateEvent {
	f.t.Helper()
	rows := f.storedUtterances(conversationID)
	var out []db.GateEvent
	for _, u := range rows {
		if u.Speaker != store.SpeakerUser {
			continue
		}
		event, ok := f.gateEventForSeq(conversationID, u.Seq)
		if ok {
			out = append(out, event)
		}
	}
	return out
}

func (f *fixture) gateEventForSeq(conversationID uuid.UUID, seq int32) (db.GateEvent, bool) {
	f.t.Helper()
	rows, err := f.store.Queries().ListUtterancesByConversation(f.t.Context(), db.ListUtterancesByConversationParams{
		ConversationID: conversationID, UserID: f.userID,
	})
	require.NoError(f.t, err)
	for _, row := range rows {
		if row.Seq != seq {
			continue
		}
		event, err := f.store.Queries().GetGateEventByUtterance(f.t.Context(), db.GetGateEventByUtteranceParams{
			UtteranceID: row.ID, UserID: f.userID,
		})
		if err != nil {
			require.ErrorIs(f.t, err, store.ErrNotFound)
			return db.GateEvent{}, false
		}
		return event, true
	}
	return db.GateEvent{}, false
}

// lastGateEvent는 마지막 사용자 발화의 관문 기록이다.
func (f *fixture) lastGateEvent(conversationID uuid.UUID) db.GateEvent {
	f.t.Helper()
	events := f.gateEvents(conversationID)
	require.NotEmpty(f.t, events, "관문 기록이 하나도 없다")
	return events[len(events)-1]
}

func (f *fixture) conversation(id uuid.UUID) db.Conversation {
	f.t.Helper()
	row, err := f.store.Queries().GetConversation(f.t.Context(), db.GetConversationParams{ID: id, UserID: f.userID})
	require.NoError(f.t, err)
	return row.Conversation
}

// openEvidence는 관문 기록의 근거 발화를 연다. 남아 있지 않으면 빈 글이다.
func (f *fixture) openEvidence(event db.GateEvent) string {
	f.t.Helper()
	if event.EvidenceEnc == nil {
		return ""
	}
	text, err := f.sealer.OpenString(event.EvidenceEnc, sealing.GateEvidence(event.ID))
	require.NoError(f.t, err, "근거는 관문 기록의 자리에 묶여 잠겨 있어야 한다")
	return text
}

// assertLogsClean은 시험이 다룬 어떤 글도 로그에 나오지 않았는지 본다.
// 문장 전체뿐 아니라 낱말 단위로도 본다. 글의 일부만 잘라 남기는 실수도 잡으려는 것이다.
func (f *fixture) assertLogsClean() {
	f.t.Helper()
	logs := f.logs.String()
	require.NotEmpty(f.t, logs, "로그가 하나도 없으면 아무것도 확인하지 못한 것이다")

	f.mu.Lock()
	texts := append([]string(nil), f.plaintexts...)
	f.mu.Unlock()
	require.NotEmpty(f.t, texts)

	for _, text := range texts {
		for _, word := range strings.Fields(text) {
			word = strings.Trim(word, ".,!?\"'")
			if utf8.RuneCountInString(word) < 3 {
				continue
			}
			assert.NotContains(f.t, logs, word, "사용자의 글이나 모델의 답이 로그에 남았다")
		}
	}
}
