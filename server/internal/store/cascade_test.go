package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 사용자에 매달리는 모든 테이블이다. 새 테이블을 더하면 여기에도 더해, 전체 삭제가 그 테이블까지 닿는지 확인받는다.
var userScopedTables = []struct {
	table  string
	column string
}{
	{"users", "id"},
	{"user_identities", "user_id"},
	{"user_keys", "user_id"},
	{"sessions", "user_id"},
	{"consents", "user_id"},
	{"user_settings", "user_id"},
	{"days", "user_id"},
	{"conversations", "user_id"},
	{"utterances", "user_id"},
	{"gate_events", "user_id"},
	{"diaries", "user_id"},
	{"signals", "user_id"},
	{"memories", "user_id"},
	{"mood_picks", "user_id"},
	{"self_checks", "user_id"},
}

func rowsByUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(userScopedTables))
	for _, scoped := range userScopedTables {
		counts[scoped.table] = count(t, pool, scoped.table, scoped.column, userID)
	}
	return counts
}

// seedAccount는 사용자에 매달릴 수 있는 모든 종류의 행을 하나 이상씩 만든다. 하루는 이틀 치다.
func seedAccount(t *testing.T, st *store.Store, pool *pgxpool.Pool, email string) (db.User, [2]dayFixture) {
	t.Helper()
	ctx := t.Context()
	user := createUser(t, st, email)

	createSession(t, st, user.ID, "token-"+email, baseTime)
	grantConsent(t, st, user.ID, store.ConsentTerms, "2026-09", baseTime)

	_, err := pool.Exec(ctx,
		`INSERT INTO user_identities (id, user_id, provider, subject, email, created_at) VALUES ($1, $2, 'google', $3, $4, $5)`,
		newID(t), user.ID, "subject-"+email, email, baseTime)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO self_checks (id, user_id, record_date, result_enc, created_at) VALUES ($1, $2, $3, $4, $5)`,
		newID(t), user.ID, date(2026, time.September, 19), fakeCiphertext, baseTime)
	require.NoError(t, err)

	days := [2]dayFixture{
		seedDay(t, st, pool, user.ID, date(2026, time.September, 19)),
		seedDay(t, st, pool, user.ID, date(2026, time.September, 20)),
	}
	return user, days
}

func TestCascadeContract(t *testing.T) {
	t.Parallel()

	t.Run("하루를 지우면 그날의 모든 것이 지워지고 다른 날과 다른 사용자는 그대로다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()

		mina, minaDays := seedAccount(t, st, pool, "mina@example.com")
		joon, joonDays := seedAccount(t, st, pool, "joon@example.com")
		deleted, kept := minaDays[0], minaDays[1]
		joonBefore := rowsByUser(t, pool, joon.ID)

		for _, scoped := range dayScopedTables {
			require.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(deleted)),
				"%s: 지우기 전에 행이 있어야 지워졌다는 확인에 뜻이 있다", scoped.table)
		}

		id, err := st.Queries().DeleteDay(ctx, db.DeleteDayParams{ID: deleted.dayID, UserID: mina.ID})
		require.NoError(t, err)
		assert.Equal(t, deleted.dayID, id)

		for _, scoped := range dayScopedTables {
			assert.Zero(t, count(t, pool, scoped.table, scoped.column, scoped.key(deleted)), "%s: 지운 날", scoped.table)
			assert.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(kept)), "%s: 남긴 날", scoped.table)
			for _, day := range joonDays {
				assert.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(day)), "%s: 다른 사용자", scoped.table)
			}
		}
		assert.Equal(t, joonBefore, rowsByUser(t, pool, joon.ID))

		// 하루에 매달리지 않는 것들은 남는다. 자가 검진은 같은 기록 날짜에 했더라도 지워지지 않는다.
		after := rowsByUser(t, pool, mina.ID)
		for _, table := range []string{"users", "user_identities", "user_keys", "sessions", "consents", "user_settings", "self_checks"} {
			assert.Equal(t, 1, after[table], table)
		}
	})

	t.Run("지운 날짜에 다시 대화하면 빈 하루에서 새로 시작한다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		mina, days := seedAccount(t, st, pool, "mina@example.com")

		_, err := st.Queries().DeleteDay(ctx, db.DeleteDayParams{ID: days[1].dayID, UserID: mina.ID})
		require.NoError(t, err)

		again, err := st.Queries().UpsertDay(ctx, db.UpsertDayParams{
			ID: newID(t), UserID: mina.ID, RecordDate: date(2026, time.September, 20), Now: baseTime.Add(time.Hour),
		})
		require.NoError(t, err)
		assert.NotEqual(t, days[1].dayID, again)
		assert.Zero(t, count(t, pool, "conversations", "day_id", again))
		assert.Zero(t, count(t, pool, "diaries", "day_id", again))
	})

	t.Run("사용자를 지우면 그 사용자의 모든 것이 지워지고 다른 사용자는 그대로다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()

		mina, _ := seedAccount(t, st, pool, "mina@example.com")
		joon, _ := seedAccount(t, st, pool, "joon@example.com")
		joonBefore := rowsByUser(t, pool, joon.ID)

		for table, rows := range rowsByUser(t, pool, mina.ID) {
			require.Positive(t, rows, "%s: 지우기 전에 행이 있어야 지워졌다는 확인에 뜻이 있다", table)
		}

		id, err := st.Queries().DeleteUser(ctx, mina.ID)
		require.NoError(t, err)
		assert.Equal(t, mina.ID, id)

		for table, rows := range rowsByUser(t, pool, mina.ID) {
			assert.Zero(t, rows, table)
		}
		assert.Equal(t, joonBefore, rowsByUser(t, pool, joon.ID))

		_, err = st.Queries().GetUserByEmail(ctx, "mina@example.com")
		require.ErrorIs(t, err, store.ErrNotFound)

		// 같은 이메일로 다시 가입할 수 있다. 지난 기록은 하나도 따라오지 않는다.
		rejoined := createUser(t, st, "mina@example.com")
		assert.NotEqual(t, mina.ID, rejoined.ID)
		assert.Zero(t, count(t, pool, "days", "user_id", rejoined.ID))
	})

	t.Run("사용자에 매달리는 테이블이 빠짐없이 시험 목록에 있다", func(t *testing.T) {
		t.Parallel()
		_, pool := newStore(t)

		listed := make([]string, 0, len(userScopedTables))
		for _, scoped := range userScopedTables {
			listed = append(listed, scoped.table)
		}
		assert.ElementsMatch(t, existingTables(t, pool), listed)
	})
}
