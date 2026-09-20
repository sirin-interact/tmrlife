package diary_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// 시험은 실제 시계를 읽지 않는다. 가짜 시계는 이 시각에서 출발한다(서울의 저녁 9시).
var startTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

const fakeModel = "fake-analysis"

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

// turn은 대화의 말 하나다.
type turn struct {
	speaker string
	text    string
}

func user(text string) turn { return turn{speaker: store.SpeakerUser, text: text} }

// assistant의 말은 모델이 만든 말로 저장한다.
func assistant(text string) turn { return turn{speaker: store.SpeakerAI, text: text} }

type fixture struct {
	t       *testing.T
	pool    *pgxpool.Pool
	store   *store.Store
	sealers *sealing.Sealers
	clock   *clock.Fake
	llm     *fake.LLM
	logs    *syncBuffer
	logger  *slog.Logger
	service *diary.Service

	userID uuid.UUID
	sealer *crypto.Sealer
	date   recorddate.Date

	// plaintexts는 시험이 DB에 넣었거나 모델이 돌려준 모든 글이다. 로그에 하나도 나오면 안 된다.
	mu         sync.Mutex
	plaintexts []string
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

	date, err := recorddate.New(2026, time.September, 20)
	require.NoError(t, err)

	f := &fixture{
		t:       t,
		pool:    pool,
		store:   st,
		sealers: sealers,
		clock:   clock.NewFake(startTime),
		llm:     fake.New(fakeModel),
		logs:    &syncBuffer{},
		userID:  userID,
		sealer:  key.Sealer,
		date:    date,
	}
	// 운영과 같은 핸들러에, 가장 낮은 수준까지 모두 남긴다.
	f.logger = logging.New(f.logs, slog.LevelDebug)
	f.service = f.newService(nil)
	return f
}

// newService는 시험용 서비스를 만든다. adjust로 기본 설정을 바꾼다.
func (f *fixture) newService(adjust func(*diary.Options)) *diary.Service {
	f.t.Helper()
	registry, err := prompts.LoadEmbedded()
	require.NoError(f.t, err)
	opts := diary.Options{
		Store:    f.store,
		Sealers:  f.sealers,
		LLM:      f.llm,
		Prompts:  registry,
		Clock:    f.clock,
		Logger:   f.logger,
		Thinking: ai.ThinkingLow,
	}
	if adjust != nil {
		adjust(&opts)
	}
	service, err := diary.NewService(opts)
	require.NoError(f.t, err)
	return service
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

// conversation은 시험이 만든 대화다.
type conversation struct {
	id    uuid.UUID
	dayID uuid.UUID
	// utteranceIDs는 turns와 같은 순서다.
	utteranceIDs []uuid.UUID
}

func (c conversation) target(userID uuid.UUID) diary.Target {
	return diary.Target{UserID: userID, DayID: c.dayID}
}

// open은 서버가 하는 길 그대로 대화를 열고 말을 잠가 저장한다. 말 사이에 시계가 조금씩 간다.
func (f *fixture) open(turns ...turn) conversation {
	f.t.Helper()
	ctx := f.t.Context()

	row, err := f.store.OpenConversation(ctx, store.NewConversation{
		ID: newID(f.t), NewDayID: newID(f.t), UserID: f.userID,
		RecordDate: f.date, StartedMode: store.ModeChat, Now: f.clock.Now(),
	})
	require.NoError(f.t, err)

	c := conversation{id: row.ID, dayID: row.DayID}
	for _, tn := range turns {
		f.remember(tn.text)
		id := newID(f.t)
		sealed, err := f.sealer.SealString(tn.text, sealing.UtteranceText(id))
		require.NoError(f.t, err)
		origin := store.OriginUser
		if tn.speaker == store.SpeakerAI {
			origin = store.OriginModel
		}
		_, err = f.store.AppendUtterance(ctx, store.NewUtterance{
			ID: id, ConversationID: row.ID, UserID: f.userID,
			Speaker: tn.speaker, Modality: store.ModeChat, Origin: origin,
			TextEnc: sealed, Now: f.clock.Advance(20 * time.Second),
		})
		require.NoError(f.t, err)
		c.utteranceIDs = append(c.utteranceIDs, id)
	}
	return c
}

// gate는 발화 하나에 위기 관문의 판정을 남긴다.
func (f *fixture) gate(c conversation, utteranceIndex int, stage int16) {
	f.t.Helper()
	detectedBy := "none"
	if stage > 0 {
		detectedBy = "rule"
	}
	_, err := f.store.Queries().InsertGateEvent(f.t.Context(), db.InsertGateEventParams{
		ID: newID(f.t), UserID: f.userID, ConversationID: c.id, UtteranceID: c.utteranceIDs[utteranceIndex],
		RuleStage: &stage, FinalStage: stage, DetectedBy: detectedBy, Adjustments: []string{}, Now: f.clock.Now(),
	})
	require.NoError(f.t, err)
}

// end는 끝내기 버튼을 누른 것처럼 대화를 끝낸다. 작업 등록은 하지 않는다.
func (f *fixture) end(c conversation) db.Conversation {
	f.t.Helper()
	row, err := f.store.Queries().EndConversation(f.t.Context(), db.EndConversationParams{
		Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
		ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
	})
	require.NoError(f.t, err)
	return row
}

// ended는 대화를 열고, 말을 남기고, 끝낸다.
func (f *fixture) ended(turns ...turn) conversation {
	f.t.Helper()
	c := f.open(turns...)
	f.end(c)
	return c
}

// reply는 모델이 entry로 돌려줄 답을 대본에 넣는다.
func (f *fixture) reply(entry string) {
	f.t.Helper()
	f.remember(entry)
	f.llm.Enqueue(fake.Reply(entryJSON(f.t, entry)))
}

func entryJSON(t *testing.T, entry string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"entry": entry})
	require.NoError(t, err)
	return string(raw)
}

// confirm은 사용자가 일기 화면에서 글을 고쳐 저장한 것과 같다.
func (f *fixture) confirm(dayID uuid.UUID, text string) db.Diary {
	f.t.Helper()
	f.remember(text)
	var saved db.Diary
	err := f.store.InTx(f.t.Context(), func(q *db.Queries) error {
		var err error
		saved, err = store.SaveDiaryBody(f.t.Context(), q, store.DiaryBodyWrite{
			NewID: newID(f.t), DayID: dayID, UserID: f.userID,
			Seal: func(rowID uuid.UUID) ([]byte, error) {
				return f.sealer.SealString(text, sealing.DiaryBody(rowID))
			},
			Now: f.clock.Advance(time.Minute),
		})
		return err
	})
	require.NoError(f.t, err)
	return saved
}

// storedDiary는 그날의 일기와 풀어 본 글이다.
type storedDiary struct {
	row   db.Diary
	draft string
	body  string
}

func (f *fixture) diary(dayID uuid.UUID) (storedDiary, bool) {
	f.t.Helper()
	row, err := f.store.Queries().GetDiaryByDayID(f.t.Context(), db.GetDiaryByDayIDParams{DayID: dayID, UserID: f.userID})
	if err != nil {
		require.ErrorIs(f.t, err, store.ErrNotFound)
		return storedDiary{}, false
	}
	out := storedDiary{row: row}
	if row.DraftEnc != nil {
		out.draft, err = f.sealer.OpenString(row.DraftEnc, sealing.DiaryDraft(row.ID))
		require.NoError(f.t, err, "초안은 초안의 자리에 묶여 잠겨 있어야 한다")
	}
	if row.BodyEnc != nil {
		out.body, err = f.sealer.OpenString(row.BodyEnc, sealing.DiaryBody(row.ID))
		require.NoError(f.t, err)
	}
	return out, true
}

func (f *fixture) mustDiary(dayID uuid.UUID) storedDiary {
	f.t.Helper()
	d, ok := f.diary(dayID)
	require.True(f.t, ok, "그날의 일기가 있어야 한다")
	return d
}

func (f *fixture) processingStatus(c conversation) string {
	f.t.Helper()
	row, err := f.store.Queries().GetConversation(f.t.Context(), db.GetConversationParams{ID: c.id, UserID: f.userID})
	require.NoError(f.t, err)
	return row.Conversation.ProcessingStatus
}

// sentText는 n번째 호출에서 모델에 보낸 글(지시문 빼고)이다.
func (f *fixture) sentText(n int) string {
	f.t.Helper()
	requests := f.llm.Requests()
	require.Greater(f.t, len(requests), n)
	require.Len(f.t, requests[n].Messages, 1)
	return requests[n].Messages[0].Text
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
