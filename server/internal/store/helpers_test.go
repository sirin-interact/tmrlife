package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
)

// 시험은 시계를 읽지 않는다. 모든 시각은 이 값에서 더하고 빼서 만든다.
var baseTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// 암호화는 이 패키지의 일이 아니다. _enc 컬럼에는 아무 바이트나 넣는다.
var fakeCiphertext = []byte{0x01, 0x02, 0x03, 0x04}

func newStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return store.New(pool), pool
}

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := store.NewID()
	require.NoError(t, err)
	return id
}

func date(year int, month time.Month, day int) pgtype.Date {
	return pgtype.Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC), Valid: true}
}

func clock(hour, minute int) pgtype.Time {
	return pgtype.Time{Microseconds: (int64(hour)*60 + int64(minute)) * 60 * 1_000_000, Valid: true}
}

func createUser(t *testing.T, st *store.Store, email string) db.User {
	t.Helper()
	user, err := st.CreateUser(t.Context(), store.NewUser{
		ID:         newID(t),
		Email:      email,
		Timezone:   "Asia/Seoul",
		WrappedDEK: fakeCiphertext,
		KEKVersion: 1,
		Now:        baseTime,
	})
	require.NoError(t, err)
	return user
}

// DB는 시각을 마이크로초까지만 담고, 드라이버는 읽은 시각에 다른 시간대를 붙여 돌려준다. 같은 순간인지만 본다.
func assertInstant(t *testing.T, want, got time.Time) {
	t.Helper()
	assert.True(t, want.Equal(got), "want %s, got %s", want.UTC(), got.UTC())
}

func count(t *testing.T, pool *pgxpool.Pool, table, column string, key uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, table, column), key).Scan(&n)
	require.NoError(t, err)
	return n
}

func requireViolation(t *testing.T, err error, sqlState, constraint string) {
	t.Helper()
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, sqlState, pgErr.Code)
	assert.Equal(t, constraint, pgErr.ConstraintName)
}

// dayFixture는 하루에 매달릴 수 있는 모든 종류의 행을 하나 이상씩 가진 하루다.
type dayFixture struct {
	userID         uuid.UUID
	dayID          uuid.UUID
	conversationID uuid.UUID
	// 사용자 발화, AI 발화, 사용자 발화 순이다.
	utteranceIDs [3]uuid.UUID
}

// 하루 하나에 들어가는 행의 수다. 지워졌는지, 남았는지를 이 수로 확인한다.
var dayScopedTables = []struct {
	table  string
	column string
	key    func(f dayFixture) uuid.UUID
	rows   int
}{
	{"days", "id", func(f dayFixture) uuid.UUID { return f.dayID }, 1},
	{"conversations", "day_id", func(f dayFixture) uuid.UUID { return f.dayID }, 1},
	{"utterances", "conversation_id", func(f dayFixture) uuid.UUID { return f.conversationID }, 3},
	{"gate_events", "conversation_id", func(f dayFixture) uuid.UUID { return f.conversationID }, 2},
	{"diaries", "day_id", func(f dayFixture) uuid.UUID { return f.dayID }, 1},
	{"signals", "day_id", func(f dayFixture) uuid.UUID { return f.dayID }, 3},
	{"memories", "source_day_id", func(f dayFixture) uuid.UUID { return f.dayID }, 1},
	{"mood_picks", "day_id", func(f dayFixture) uuid.UUID { return f.dayID }, 1},
}

func seedDay(t *testing.T, st *store.Store, pool *pgxpool.Pool, userID uuid.UUID, recordDate pgtype.Date) dayFixture {
	t.Helper()
	ctx := t.Context()

	dayID, err := st.Queries().UpsertDay(ctx, db.UpsertDayParams{
		ID: newID(t), UserID: userID, RecordDate: recordDate, Now: baseTime,
	})
	require.NoError(t, err)

	f := dayFixture{
		userID:         userID,
		dayID:          dayID,
		conversationID: newID(t),
		utteranceIDs:   [3]uuid.UUID{newID(t), newID(t), newID(t)},
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}

	exec(`INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, ended_at, end_reason, processing_status, created_at)
	      VALUES ($1, $2, $3, 'ended', 'voice', $4, $5, 'user', 'done', $4)`,
		f.conversationID, userID, dayID, baseTime, baseTime.Add(10*time.Minute))

	utterances := []struct {
		speaker, origin string
	}{{"user", "user"}, {"ai", "model"}, {"user", "user"}}
	for i, u := range utterances {
		exec(`INSERT INTO utterances (id, conversation_id, user_id, seq, speaker, modality, origin, text_enc, created_at)
		      VALUES ($1, $2, $3, $4, $5, 'voice', $6, $7, $8)`,
			f.utteranceIDs[i], f.conversationID, userID, i, u.speaker, u.origin, fakeCiphertext, baseTime)
	}

	exec(`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, rule_stage, ai_stage, final_stage, detected_by, created_at)
	      VALUES ($1, $2, $3, $4, 0, 0, 0, 'none', $5)`,
		newID(t), userID, f.conversationID, f.utteranceIDs[0], baseTime)
	exec(`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, rule_stage, ai_stage, final_stage, detected_by, adjustments, ai_latency_ms, evidence_enc, created_at)
	      VALUES ($1, $2, $3, $4, 1, 0, 1, 'rule', '{higher_of_two}', 1650, $5, $6)`,
		newID(t), userID, f.conversationID, f.utteranceIDs[2], fakeCiphertext, baseTime)

	exec(`INSERT INTO diaries (id, day_id, user_id, status, draft_enc, created_at, updated_at)
	      VALUES ($1, $2, $3, 'draft', $4, $5, $5)`,
		newID(t), dayID, userID, fakeCiphertext, baseTime)

	exec(`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, evidence_enc, evidence_utterance_id, extractor_version, created_at)
	      VALUES ($1, $2, $3, $4, 'sleep', 'observed', 'direct', $5, $6, 'test-1', $7)`,
		newID(t), userID, dayID, f.conversationID, fakeCiphertext, f.utteranceIDs[0], baseTime)
	exec(`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, evidence_enc, evidence_utterance_id, extractor_version, created_at)
	      VALUES ($1, $2, $3, $4, 'mood', 'not_observed', 'indirect', $5, $6, 'test-1', $7)`,
		newID(t), userID, dayID, f.conversationID, fakeCiphertext, f.utteranceIDs[2], baseTime)
	exec(`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, extractor_version, created_at)
	      VALUES ($1, $2, $3, $4, 'concentration', 'not_mentioned', 'none', 'test-1', $5)`,
		newID(t), userID, dayID, f.conversationID, baseTime)

	exec(`INSERT INTO memories (id, user_id, source_day_id, kind, content_enc, created_at)
	      VALUES ($1, $2, $3, 'event', $4, $5)`,
		newID(t), userID, dayID, fakeCiphertext, baseTime)

	exec(`INSERT INTO mood_picks (day_id, user_id, value_enc, created_at) VALUES ($1, $2, $3, $4)`,
		dayID, userID, fakeCiphertext, baseTime)

	return f
}
