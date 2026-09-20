package store_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestUpsertDay(t *testing.T) {
	t.Parallel()

	t.Run("같은 기록 날짜에는 언제나 같은 하루를 돌려준다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")

		firstID := newID(t)
		got, err := st.Queries().UpsertDay(ctx, db.UpsertDayParams{
			ID: firstID, UserID: user.ID, RecordDate: date(2026, time.September, 20), Now: baseTime,
		})
		require.NoError(t, err)
		assert.Equal(t, firstID, got)

		// 그날의 두 번째 대화다. 새 ID를 들고 오지만 이미 있는 하루를 받는다.
		got, err = st.Queries().UpsertDay(ctx, db.UpsertDayParams{
			ID: newID(t), UserID: user.ID, RecordDate: date(2026, time.September, 20), Now: baseTime.Add(3 * time.Hour),
		})
		require.NoError(t, err)
		assert.Equal(t, firstID, got)
		assert.Equal(t, 1, count(t, pool, "days", "user_id", user.ID))

		var createdAt time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT created_at FROM days WHERE id = $1`, firstID).Scan(&createdAt))
		assertInstant(t, baseTime, createdAt)
	})

	t.Run("날짜가 다르거나 사용자가 다르면 다른 하루다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")

		upsert := func(userID uuid.UUID, day int) uuid.UUID {
			id, err := st.Queries().UpsertDay(ctx, db.UpsertDayParams{
				ID: newID(t), UserID: userID, RecordDate: date(2026, time.September, day), Now: baseTime,
			})
			require.NoError(t, err)
			return id
		}
		minaToday := upsert(mina.ID, 20)
		assert.NotEqual(t, minaToday, upsert(mina.ID, 21))
		assert.NotEqual(t, minaToday, upsert(joon.ID, 20))
	})

	t.Run("동시에 들어와도 한 행으로 모인다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "mina@example.com")

		const callers = 8
		proposed := make([]uuid.UUID, callers)
		for i := range proposed {
			proposed[i] = newID(t)
		}
		got := make([]uuid.UUID, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				got[i], errs[i] = st.Queries().UpsertDay(t.Context(), db.UpsertDayParams{
					ID: proposed[i], UserID: user.ID, RecordDate: date(2026, time.September, 20), Now: baseTime,
				})
			})
		}
		wg.Wait()

		for i := range callers {
			require.NoError(t, errs[i])
			assert.Equal(t, got[0], got[i])
		}
		assert.Equal(t, 1, count(t, pool, "days", "user_id", user.ID))
	})
}

func TestDeleteDay(t *testing.T) {
	t.Parallel()

	t.Run("남의 하루는 ID를 알아도 지우지 못하고, 없는 것과 같은 오류가 난다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		minaDay := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))

		_, err := st.Queries().DeleteDay(ctx, db.DeleteDayParams{ID: minaDay.dayID, UserID: joon.ID})
		require.ErrorIs(t, err, store.ErrNotFound)

		_, err = st.Queries().DeleteDay(ctx, db.DeleteDayParams{ID: newID(t), UserID: joon.ID})
		require.ErrorIs(t, err, store.ErrNotFound)

		for _, scoped := range dayScopedTables {
			assert.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(minaDay)), scoped.table)
		}
	})
}
