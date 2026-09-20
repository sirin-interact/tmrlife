package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func grantConsent(t *testing.T, st *store.Store, userID uuid.UUID, kind, version string, now time.Time) db.Consent {
	t.Helper()
	consent, err := st.Queries().GrantConsent(t.Context(), db.GrantConsentParams{
		ID: newID(t), UserID: userID, Kind: kind, Version: version, Now: now,
	})
	require.NoError(t, err)
	return consent
}

func TestConsents(t *testing.T) {
	t.Parallel()

	t.Run("같은 판에 다시 동의해도 처음 동의한 기록이 그대로 남는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "mina@example.com")

		first := grantConsent(t, st, user.ID, store.ConsentTerms, "2026-09", baseTime)
		again := grantConsent(t, st, user.ID, store.ConsentTerms, "2026-09", baseTime.Add(time.Hour))

		assert.Equal(t, first.ID, again.ID)
		assertInstant(t, baseTime, again.GrantedAt)
		assert.Equal(t, 1, count(t, pool, "consents", "user_id", user.ID))
	})

	t.Run("철회한 동의는 유효한 목록에서 빠지고, 다시 동의하면 새 행으로 돌아온다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")
		grantConsent(t, st, user.ID, store.ConsentTerms, "2026-09", baseTime)
		first := grantConsent(t, st, user.ID, store.ConsentOverseasTransfer, "2026-09", baseTime)

		withdrawn, err := st.Queries().WithdrawConsent(ctx, db.WithdrawConsentParams{
			UserID: user.ID, Kind: store.ConsentOverseasTransfer, Now: baseTime.Add(time.Hour),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), withdrawn)

		active, err := st.Queries().ListActiveConsents(ctx, user.ID)
		require.NoError(t, err)
		require.Len(t, active, 1)
		assert.Equal(t, store.ConsentTerms, active[0].Kind)

		regrantedAt := baseTime.Add(2 * time.Hour)
		regranted := grantConsent(t, st, user.ID, store.ConsentOverseasTransfer, "2026-09", regrantedAt)
		assert.NotEqual(t, first.ID, regranted.ID, "철회한 행을 되살리지 않고 새 행을 만든다")
		assertInstant(t, regrantedAt, regranted.GrantedAt)
		assert.Nil(t, regranted.WithdrawnAt)

		active, err = st.Queries().ListActiveConsents(ctx, user.ID)
		require.NoError(t, err)
		assert.Len(t, active, 2)
	})

	t.Run("동의하고, 철회하고, 다시 동의한 이력이 모두 남는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")

		grantedAt := baseTime
		withdrawnAt := baseTime.Add(24 * time.Hour)
		regrantedAt := baseTime.Add(72 * time.Hour)

		first := grantConsent(t, st, user.ID, store.ConsentSensitiveData, "2026-09", grantedAt)
		_, err := st.Queries().WithdrawConsent(ctx, db.WithdrawConsentParams{
			UserID: user.ID, Kind: store.ConsentSensitiveData, Now: withdrawnAt,
		})
		require.NoError(t, err)
		second := grantConsent(t, st, user.ID, store.ConsentSensitiveData, "2026-09", regrantedAt)

		history, err := st.Queries().ListConsentHistory(ctx, user.ID)
		require.NoError(t, err)
		require.Len(t, history, 2, "다시 동의해도 앞선 철회 기록을 덮어쓰지 않는다")

		assert.Equal(t, first.ID, history[0].ID)
		assertInstant(t, grantedAt, history[0].GrantedAt)
		require.NotNil(t, history[0].WithdrawnAt, "첫 동의의 철회 시각이 그대로 남아 있어야 한다")
		assertInstant(t, withdrawnAt, *history[0].WithdrawnAt)

		assert.Equal(t, second.ID, history[1].ID)
		assertInstant(t, regrantedAt, history[1].GrantedAt)
		assert.Nil(t, history[1].WithdrawnAt)

		active, err := st.Queries().ListActiveConsents(ctx, user.ID)
		require.NoError(t, err)
		require.Len(t, active, 1)
		assert.Equal(t, second.ID, active[0].ID)

		// 한 번 더 철회해도 첫 행의 철회 시각은 바뀌지 않는다.
		again := baseTime.Add(96 * time.Hour)
		withdrawn, err := st.Queries().WithdrawConsent(ctx, db.WithdrawConsentParams{
			UserID: user.ID, Kind: store.ConsentSensitiveData, Now: again,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), withdrawn, "유효한 행만 철회한다")

		history, err = st.Queries().ListConsentHistory(ctx, user.ID)
		require.NoError(t, err)
		require.Len(t, history, 2)
		require.NotNil(t, history[0].WithdrawnAt)
		assertInstant(t, withdrawnAt, *history[0].WithdrawnAt)
		require.NotNil(t, history[1].WithdrawnAt)
		assertInstant(t, again, *history[1].WithdrawnAt)
	})

	t.Run("유효한 동의는 사용자, 종류, 판마다 하나만 있을 수 있다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")
		grantConsent(t, st, user.ID, store.ConsentTerms, "2026-09", baseTime)

		// 쿼리를 거치지 않고 직접 넣어도 DB가 막는다.
		_, err := pool.Exec(ctx,
			`INSERT INTO consents (id, user_id, kind, version, granted_at) VALUES ($1, $2, $3, $4, $5)`,
			newID(t), user.ID, store.ConsentTerms, "2026-09", baseTime.Add(time.Hour))
		requireViolation(t, err, sqlStateUniqueViolation, "consents_active_key")

		// 철회한 행은 몇 개든 쌓일 수 있다.
		for i := range 2 {
			at := baseTime.Add(time.Duration(i+1) * time.Hour)
			_, err = pool.Exec(ctx,
				`INSERT INTO consents (id, user_id, kind, version, granted_at, withdrawn_at) VALUES ($1, $2, $3, $4, $5, $6)`,
				newID(t), user.ID, store.ConsentTerms, "2026-09", at, at.Add(time.Minute))
			require.NoError(t, err)
		}
		assert.Equal(t, 3, count(t, pool, "consents", "user_id", user.ID))
	})

	t.Run("동의 문서의 판이 바뀌면 새 행으로 쌓인다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")
		grantConsent(t, st, user.ID, store.ConsentPrivacy, "2026-09", baseTime)
		grantConsent(t, st, user.ID, store.ConsentPrivacy, "2027-01", baseTime.Add(time.Hour))

		active, err := st.Queries().ListActiveConsents(t.Context(), user.ID)
		require.NoError(t, err)
		require.Len(t, active, 2)
		assert.Equal(t, "2026-09", active[0].Version)
		assert.Equal(t, "2027-01", active[1].Version)
	})

	t.Run("동의가 하나도 없으면 빈 목록이다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")

		active, err := st.Queries().ListActiveConsents(t.Context(), user.ID)
		require.NoError(t, err)
		assert.NotNil(t, active)
		assert.Empty(t, active)
	})

	t.Run("정해지지 않은 종류의 동의는 받지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")

		_, err := st.Queries().GrantConsent(t.Context(), db.GrantConsentParams{
			ID: newID(t), UserID: user.ID, Kind: "marketing", Version: "2026-09", Now: baseTime,
		})
		requireViolation(t, err, sqlStateCheckViolation, "consents_kind_check")
	})

	t.Run("코드의 동의 종류는 모두 DB가 받아들인다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")

		kinds := []string{
			store.ConsentTerms, store.ConsentPrivacy, store.ConsentSensitiveData, store.ConsentOverseasTransfer,
		}
		for _, kind := range kinds {
			grantConsent(t, st, user.ID, kind, "2026-09", baseTime)
		}
	})
}
