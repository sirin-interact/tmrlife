package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestCreateUser(t *testing.T) {
	t.Parallel()

	t.Run("사용자, 데이터 키, 설정, 동의를 함께 만든다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()

		hash := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"
		name := "하늘"
		id := newID(t)
		user, err := st.CreateUser(ctx, store.NewUser{
			ID:           id,
			Email:        "sky@example.com",
			PasswordHash: &hash,
			DisplayName:  &name,
			Timezone:     "Asia/Seoul",
			WrappedDEK:   fakeCiphertext,
			KEKVersion:   2,
			Consents: []store.NewConsent{
				{ID: newID(t), Kind: store.ConsentTerms, Version: "2026-09"},
				{ID: newID(t), Kind: store.ConsentPrivacy, Version: "2026-09"},
				{ID: newID(t), Kind: store.ConsentSensitiveData, Version: "2026-09"},
				{ID: newID(t), Kind: store.ConsentOverseasTransfer, Version: "2026-09"},
			},
			Now: baseTime,
		})
		require.NoError(t, err)

		assert.Equal(t, id, user.ID)
		assert.Equal(t, "sky@example.com", user.Email)
		assert.Equal(t, &hash, user.PasswordHash)
		assert.Equal(t, &name, user.DisplayName)
		assert.Equal(t, "Asia/Seoul", user.Timezone)
		assert.Equal(t, "user", user.Role, "가입으로는 일반 사용자만 만들어진다")
		assert.False(t, user.IsDemo)
		assert.Nil(t, user.EmailVerifiedAt)
		assertInstant(t, baseTime, user.CreatedAt)
		assertInstant(t, baseTime, user.UpdatedAt)

		key, err := st.Queries().GetUserKey(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, fakeCiphertext, key.WrappedDEK)
		assert.Equal(t, int16(2), key.KEKVersion)
		assertInstant(t, baseTime, key.CreatedAt)

		settings, err := st.Queries().GetUserSettings(ctx, id)
		require.NoError(t, err)
		assert.True(t, settings.ReminderEnabled)
		assert.Equal(t, clock(20, 0), settings.ReminderTime)
		assert.Equal(t, "voice", settings.DefaultMode)
		assert.True(t, settings.AnalysisEnabled)
		assert.True(t, settings.MemoryEnabled)
		assert.False(t, settings.MoodPickEnabled)
		assertInstant(t, baseTime, settings.UpdatedAt)

		consents, err := st.Queries().ListActiveConsents(ctx, id)
		require.NoError(t, err)
		require.Len(t, consents, 4)
		for _, c := range consents {
			assertInstant(t, baseTime, c.GrantedAt)
			assert.Nil(t, c.WithdrawnAt)
		}
	})

	t.Run("비밀번호 없는 계정을 만들 수 있다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)

		user := createUser(t, st, "social@example.com")
		assert.Nil(t, user.PasswordHash)
		assert.Nil(t, user.DisplayName)
	})

	t.Run("도중에 실패하면 아무것도 남지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()

		id := newID(t)
		_, err := st.CreateUser(ctx, store.NewUser{
			ID:         id,
			Email:      "half@example.com",
			Timezone:   "Asia/Seoul",
			WrappedDEK: fakeCiphertext,
			KEKVersion: 1,
			Consents:   []store.NewConsent{{ID: newID(t), Kind: "marketing", Version: "2026-09"}},
			Now:        baseTime,
		})
		requireViolation(t, err, sqlStateCheckViolation, "consents_kind_check")

		_, err = st.Queries().GetUserByID(ctx, id)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, count(t, pool, "user_keys", "user_id", id))
		assert.Zero(t, count(t, pool, "user_settings", "user_id", id))
	})

	t.Run("시간대를 비워 둘 수 없다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)

		_, err := st.CreateUser(t.Context(), store.NewUser{
			ID: newID(t), Email: "tz@example.com", WrappedDEK: fakeCiphertext, KEKVersion: 1, Now: baseTime,
		})
		requireViolation(t, err, sqlStateCheckViolation, "users_timezone_check")
	})
}

func TestUserEmail(t *testing.T) {
	t.Parallel()

	t.Run("대소문자만 다른 이메일로는 다시 가입할 수 없다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()

		first := createUser(t, st, "Mina@Example.com")

		secondID := newID(t)
		_, err := st.CreateUser(ctx, store.NewUser{
			ID: secondID, Email: "mina@example.COM", Timezone: "Asia/Seoul",
			WrappedDEK: fakeCiphertext, KEKVersion: 1, Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrEmailTaken)
		require.ErrorIs(t, err, store.ErrConflict)
		assert.NotContains(t, err.Error(), "example", "오류 문구에 이메일이 들어가면 로그에 그대로 남는다")

		assert.Zero(t, count(t, pool, "user_keys", "user_id", secondID))
		assert.Equal(t, 1, count(t, pool, "user_keys", "user_id", first.ID))
	})

	t.Run("대소문자를 가리지 않고 찾되, 적어 준 그대로 돌려준다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)

		created := createUser(t, st, "Mina@Example.com")

		found, err := st.Queries().GetUserByEmail(t.Context(), "MINA@example.com")
		require.NoError(t, err)
		assert.Equal(t, created.ID, found.ID)
		assert.Equal(t, "Mina@Example.com", found.Email)
	})
}

func TestUpdateUserPasswordHash(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	ctx := t.Context()

	oldHash := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$b2xk"
	newHash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$bmV3"
	user, err := st.CreateUser(ctx, store.NewUser{
		ID: newID(t), Email: "mina@example.com", PasswordHash: &oldHash, Timezone: "Asia/Seoul",
		WrappedDEK: fakeCiphertext, KEKVersion: 1, Now: baseTime,
	})
	require.NoError(t, err)

	t.Run("읽었던 해시가 그대로이면 새 해시로 바꾼다", func(t *testing.T) {
		later := baseTime.Add(time.Hour)
		updated, err := st.Queries().UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
			ID: user.ID, OldHash: oldHash, NewHash: newHash, Now: later,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), updated)

		found, err := st.Queries().GetUserByID(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, &newHash, found.PasswordHash)
		assertInstant(t, later, found.UpdatedAt)
	})

	t.Run("그 사이에 해시가 바뀌었으면 덮어쓰지 않는다", func(t *testing.T) {
		stale := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$c3RhbGU"
		updated, err := st.Queries().UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
			ID: user.ID, OldHash: oldHash, NewHash: stale, Now: baseTime.Add(2 * time.Hour),
		})
		require.NoError(t, err)
		assert.Zero(t, updated)

		found, err := st.Queries().GetUserByID(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, &newHash, found.PasswordHash)
	})
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	ctx := t.Context()

	t.Run("없는 사용자를 찾으면 ErrNotFound다", func(t *testing.T) {
		_, err := st.Queries().GetUserByID(ctx, newID(t))
		require.ErrorIs(t, err, store.ErrNotFound)
		require.ErrorIs(t, err, pgx.ErrNoRows, "드라이버의 오류도 함께 감싸 둔다")

		_, err = st.Queries().GetUserByEmail(ctx, "nobody@example.com")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("없는 사용자를 지우면 ErrNotFound다", func(t *testing.T) {
		_, err := st.Queries().DeleteUser(ctx, newID(t))
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}
