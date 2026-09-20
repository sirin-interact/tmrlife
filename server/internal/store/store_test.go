package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func userParams(id uuid.UUID, email string) db.CreateUserParams {
	return db.CreateUserParams{ID: id, Email: email, Timezone: "Asia/Seoul", Now: baseTime}
}

func TestInTx(t *testing.T) {
	t.Parallel()

	t.Run("fn이 nil을 돌려주면 커밋한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		id := newID(t)

		err := st.InTx(ctx, func(q *db.Queries) error {
			_, err := q.CreateUser(ctx, userParams(id, "mina@example.com"))
			return err
		})
		require.NoError(t, err)

		_, err = st.Queries().GetUserByID(ctx, id)
		require.NoError(t, err)
	})

	t.Run("fn이 오류를 돌려주면 되돌리고 그 오류를 그대로 돌려준다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		id := newID(t)
		errStop := errors.New("stop")

		err := st.InTx(ctx, func(q *db.Queries) error {
			if _, err := q.CreateUser(ctx, userParams(id, "mina@example.com")); err != nil {
				return err
			}
			return errStop
		})
		assert.Equal(t, errStop, err, "감싸지 않아야 부르는 쪽이 자기 오류 값을 그대로 비교한다")

		_, err = st.Queries().GetUserByID(ctx, id)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, pool.Stat().AcquiredConns(), "연결이 풀에 돌아와야 한다")
	})

	t.Run("fn이 패닉해도 되돌리고 연결을 풀에 돌려준다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		id := newID(t)

		assert.PanicsWithValue(t, "boom", func() {
			_ = st.InTx(ctx, func(q *db.Queries) error {
				if _, err := q.CreateUser(ctx, userParams(id, "mina@example.com")); err != nil {
					return err
				}
				panic("boom")
			})
		})

		_, err := st.Queries().GetUserByID(ctx, id)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, pool.Stat().AcquiredConns())
	})

	t.Run("도중에 요청이 취소돼도 되돌리고 풀은 계속 쓸 수 있다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		id := newID(t)

		ctx, cancel := context.WithCancel(t.Context())
		err := st.InTx(ctx, func(q *db.Queries) error {
			if _, err := q.CreateUser(ctx, userParams(id, "mina@example.com")); err != nil {
				return err
			}
			cancel()
			_, err := q.GetUserByID(ctx, id)
			return err
		})
		require.ErrorIs(t, err, context.Canceled)

		_, err = st.Queries().GetUserByID(t.Context(), id)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, pool.Stat().AcquiredConns())
	})

	t.Run("쿼리 도중에 시간이 다 되면 끊긴 연결을 되돌리려다 난 오류를 덧붙이지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		id := newID(t)

		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		err := st.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
			if _, err := q.CreateUser(ctx, userParams(id, "mina@example.com")); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `SELECT pg_sleep(30)`)
			return err
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotContains(t, err.Error(), "roll back")

		_, err = st.Queries().GetUserByID(t.Context(), id)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, pool.Stat().AcquiredConns())
	})

	t.Run("fn이 실패한 문장의 오류를 삼켜도 커밋된 것처럼 넘어가지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		id := newID(t)

		err := st.InTx(ctx, func(q *db.Queries) error {
			if _, err := q.CreateUser(ctx, userParams(id, "mina@example.com")); err != nil {
				return err
			}
			// 없는 사용자의 키를 넣으려다 실패한다. 이 시점에 트랜잭션은 이미 깨져 있다.
			_ = q.CreateUserKey(ctx, db.CreateUserKeyParams{UserID: newID(t), WrappedDEK: fakeCiphertext, KEKVersion: 1, Now: baseTime})
			return nil
		})
		require.ErrorIs(t, err, pgx.ErrTxCommitRollback)

		_, err = st.Queries().GetUserByID(ctx, id)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("트랜잭션 안의 쿼리도 같은 오류 값으로 바뀐다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()

		err := st.InTx(ctx, func(q *db.Queries) error {
			_, err := q.GetUserByID(ctx, newID(t))
			return err
		})
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestInTxRaw(t *testing.T) {
	t.Parallel()

	t.Run("넘겨받은 트랜잭션으로 한 쓰기는 쿼리와 함께 커밋되고 함께 되돌려진다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		errStop := errors.New("stop")

		write := func(userID, consentID uuid.UUID, result error) error {
			return st.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
				if _, err := q.CreateUser(ctx, userParams(userID, userID.String()+"@example.com")); err != nil {
					return err
				}
				// 다른 라이브러리가 같은 트랜잭션에 쓰는 자리를 흉내 낸다.
				_, err := tx.Exec(ctx,
					`INSERT INTO consents (id, user_id, kind, version, granted_at) VALUES ($1, $2, 'terms', '2026-09', $3)`,
					consentID, userID, baseTime)
				if err != nil {
					return err
				}
				return result
			})
		}

		committed := newID(t)
		require.NoError(t, write(committed, newID(t), nil))
		consents, err := st.Queries().ListActiveConsents(ctx, committed)
		require.NoError(t, err)
		assert.Len(t, consents, 1)

		rolledBack := newID(t)
		require.ErrorIs(t, write(rolledBack, newID(t), errStop), errStop)
		_, err = st.Queries().GetUserByID(ctx, rolledBack)
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestNewID(t *testing.T) {
	t.Parallel()

	t.Run("UUIDv7을 만들고, 나중에 만든 ID가 정렬에서 뒤에 온다", func(t *testing.T) {
		t.Parallel()
		previous, err := store.NewID()
		require.NoError(t, err)
		assert.Equal(t, uuid.Version(7), previous.Version())

		for range 100 {
			next, err := store.NewID()
			require.NoError(t, err)
			assert.Less(t, previous.String(), next.String())
			previous = next
		}
	})
}
