package store_test

import (
	"crypto/sha256"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const sessionTTL = 30 * 24 * time.Hour

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func createSession(t *testing.T, st *store.Store, userID uuid.UUID, token string, now time.Time) db.Session {
	t.Helper()
	session, err := st.Queries().CreateSession(t.Context(), db.CreateSessionParams{
		ID:        newID(t),
		UserID:    userID,
		TokenHash: tokenHash(token),
		Now:       now,
		ExpiresAt: now.Add(sessionTTL),
	})
	require.NoError(t, err)
	return session
}

// 세션 조회는 로그인한 요청마다 돈다. 그 결과에 비밀번호 해시가 실려 있으면 쓰이지도 않는 값이 요청마다 DB에서 건너오고,
// 결과를 로그나 응답으로 내보내는 코드가 더해지는 날 그대로 새어 나간다. 쿼리가 사용자 행을 통째로 읽는 꼴로 돌아가면 여기서 걸린다.
func TestSessionLookupDoesNotCarryThePasswordHash(t *testing.T) {
	t.Parallel()

	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for field := range typ.Fields() {
			at := path + "." + field.Name
			assert.NotEqual(t, "PasswordHash", field.Name, "%s: 세션 조회가 비밀번호 해시를 읽는다", at)
			assert.NotEqual(t, reflect.TypeFor[db.User](), field.Type, "%s: 세션 조회가 사용자 행을 통째로 읽는다", at)
			// 시각 같은 표준 라이브러리의 타입 안까지 들어가지 않는다.
			if field.Type.PkgPath() == reflect.TypeFor[db.Session]().PkgPath() {
				walk(field.Type, at)
			}
		}
	}
	walk(reflect.TypeFor[db.GetSessionWithUserByTokenHashRow](), "GetSessionWithUserByTokenHashRow")

	t.Run("검사가 실제로 걸러낸다", func(t *testing.T) {
		// 사용자 행에는 해시가 있다. 걸러낼 것이 없는 타입만 봐서 늘 통과하는 검사가 아니라는 것을 보인다.
		_, hasHash := reflect.TypeFor[db.User]().FieldByName("PasswordHash")
		assert.True(t, hasHash)
	})
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("만들고, 토큰 해시로 찾고, 쓰는 동안 만료를 밀고, 지운다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")

		agent := "Mozilla/5.0"
		ip := netip.MustParseAddr("203.0.113.7")
		created, err := st.Queries().CreateSession(ctx, db.CreateSessionParams{
			ID:        newID(t),
			UserID:    user.ID,
			TokenHash: tokenHash("token-a"),
			Now:       baseTime,
			ExpiresAt: baseTime.Add(sessionTTL),
			UserAgent: &agent,
			IP:        &ip,
		})
		require.NoError(t, err)
		assertInstant(t, baseTime, created.CreatedAt)
		assertInstant(t, baseTime, created.LastSeenAt)

		found, err := st.Queries().GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{
			TokenHash: tokenHash("token-a"), Now: baseTime.Add(time.Hour),
		})
		require.NoError(t, err)
		assert.Equal(t, created.ID, found.Session.ID)
		assert.Equal(t, &agent, found.Session.UserAgent)
		assert.Equal(t, &ip, found.Session.IP)
		assert.Equal(t, user.ID, found.Session.UserID)
		assert.Equal(t, user.Email, found.Email)
		assert.False(t, found.HasPassword, "이 사용자는 비밀번호 없이 만들었다")

		later := baseTime.Add(24 * time.Hour)
		touched, err := st.Queries().TouchSession(ctx, db.TouchSessionParams{
			ID: created.ID, Now: later, ExpiresAt: later.Add(sessionTTL),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), touched)

		found, err = st.Queries().GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{
			TokenHash: tokenHash("token-a"), Now: later,
		})
		require.NoError(t, err)
		assertInstant(t, later, found.Session.LastSeenAt)
		assertInstant(t, later.Add(sessionTTL), found.Session.ExpiresAt)
		assertInstant(t, baseTime, found.Session.CreatedAt)

		require.NoError(t, st.Queries().DeleteSession(ctx, created.ID))
		_, err = st.Queries().GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{
			TokenHash: tokenHash("token-a"), Now: later,
		})
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("다른 토큰으로는 찾지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")
		createSession(t, st, user.ID, "token-a", baseTime)

		_, err := st.Queries().GetSessionWithUserByTokenHash(t.Context(), db.GetSessionWithUserByTokenHashParams{
			TokenHash: tokenHash("token-b"), Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("같은 토큰 해시를 두 번 쓸 수 없다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")
		createSession(t, st, user.ID, "token-a", baseTime)

		_, err := st.Queries().CreateSession(t.Context(), db.CreateSessionParams{
			ID: newID(t), UserID: user.ID, TokenHash: tokenHash("token-a"), Now: baseTime, ExpiresAt: baseTime.Add(sessionTTL),
		})
		require.ErrorIs(t, err, store.ErrConflict)
		assert.Equal(t, "sessions_token_hash_key", store.ConstraintName(err))
	})
}

func TestSessionLookupTellsWhetherTheAccountHasAPassword(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	ctx := t.Context()

	// 조회는 해시를 읽지 않고 있는지 없는지만 돌려준다. 그 참거짓이 실제 값을 따라가는지 양쪽 다 본다.
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
	withPassword, err := st.CreateUser(ctx, store.NewUser{
		ID: newID(t), Email: "with-password@example.com", PasswordHash: &hash,
		Timezone: "Asia/Seoul", WrappedDEK: fakeCiphertext, KEKVersion: 1, Now: baseTime,
	})
	require.NoError(t, err)
	withoutPassword := createUser(t, st, "without-password@example.com")

	tests := []struct {
		name  string
		user  db.User
		token string
		want  bool
	}{
		{"비밀번호가 있는 계정", withPassword, "token-with", true},
		{"소셜 로그인으로만 들어오는 계정", withoutPassword, "token-without", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createSession(t, st, tt.user.ID, tt.token, baseTime)
			found, err := st.Queries().GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{
				TokenHash: tokenHash(tt.token), Now: baseTime,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, found.HasPassword)
			assert.Equal(t, tt.user.ID, found.Session.UserID)
			assertInstant(t, tt.user.CreatedAt, found.UserCreatedAt)
		})
	}
}

func TestDeleteSessionByTokenHash(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	ctx := t.Context()
	user := createUser(t, st, "mina@example.com")
	phone := createSession(t, st, user.ID, "token-phone", baseTime)
	tablet := createSession(t, st, user.ID, "token-tablet", baseTime)

	t.Run("그 토큰의 세션만 지운다", func(t *testing.T) {
		deleted, err := st.Queries().DeleteSessionByTokenHash(ctx, tokenHash("token-phone"))
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)
		assert.Zero(t, count(t, pool, "sessions", "id", phone.ID))
		assert.Equal(t, 1, count(t, pool, "sessions", "id", tablet.ID))
	})

	t.Run("없는 토큰이면 아무것도 지우지 않고 오류도 아니다", func(t *testing.T) {
		deleted, err := st.Queries().DeleteSessionByTokenHash(ctx, tokenHash("token-nobody"))
		require.NoError(t, err)
		assert.Zero(t, deleted)
		assert.Equal(t, 1, count(t, pool, "sessions", "id", tablet.ID))
	})
}

func TestSessionExpiry(t *testing.T) {
	t.Parallel()

	t.Run("만료된 순간부터 없는 세션으로 친다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")
		session := createSession(t, st, user.ID, "token-a", baseTime)

		tests := []struct {
			name  string
			now   time.Time
			found bool
		}{
			{"만료 직전이면 찾는다", session.ExpiresAt.Add(-time.Microsecond), true},
			{"만료 시각이 되면 찾지 못한다", session.ExpiresAt, false},
			{"만료 뒤에는 찾지 못한다", session.ExpiresAt.Add(time.Hour), false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := st.Queries().GetSessionWithUserByTokenHash(t.Context(), db.GetSessionWithUserByTokenHashParams{
					TokenHash: tokenHash("token-a"), Now: tt.now,
				})
				if tt.found {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, store.ErrNotFound)
				}
			})
		}
	})

	t.Run("만료된 세션은 되살리지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")
		session := createSession(t, st, user.ID, "token-a", baseTime)

		after := session.ExpiresAt.Add(time.Minute)
		touched, err := st.Queries().TouchSession(t.Context(), db.TouchSessionParams{
			ID: session.ID, Now: after, ExpiresAt: after.Add(sessionTTL),
		})
		require.NoError(t, err)
		assert.Zero(t, touched)
	})

	t.Run("만료는 늘어나기만 하고 마지막으로 본 시각은 뒤로 가지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")
		session := createSession(t, st, user.ID, "token-a", baseTime)

		later := baseTime.Add(2 * time.Hour)
		_, err := st.Queries().TouchSession(ctx, db.TouchSessionParams{
			ID: session.ID, Now: later, ExpiresAt: later.Add(sessionTTL),
		})
		require.NoError(t, err)

		// 늦게 도착한 앞선 요청이다. 더 이른 시각과 더 짧은 만료를 들고 온다.
		earlier := baseTime.Add(time.Hour)
		_, err = st.Queries().TouchSession(ctx, db.TouchSessionParams{
			ID: session.ID, Now: earlier, ExpiresAt: earlier.Add(time.Hour),
		})
		require.NoError(t, err)

		found, err := st.Queries().GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{
			TokenHash: tokenHash("token-a"), Now: later,
		})
		require.NoError(t, err)
		assertInstant(t, later, found.Session.LastSeenAt)
		assertInstant(t, later.Add(sessionTTL), found.Session.ExpiresAt)
	})

	t.Run("만료된 세션만 골라 지운다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")
		old := createSession(t, st, user.ID, "token-old", baseTime.Add(-sessionTTL))
		fresh := createSession(t, st, user.ID, "token-fresh", baseTime)

		deleted, err := st.Queries().DeleteExpiredSessions(ctx, baseTime)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)
		assert.Zero(t, count(t, pool, "sessions", "id", old.ID))
		assert.Equal(t, 1, count(t, pool, "sessions", "id", fresh.ID))
	})
}

func TestDeleteSessionsByUser(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	ctx := t.Context()

	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	createSession(t, st, mina.ID, "mina-phone", baseTime)
	createSession(t, st, mina.ID, "mina-tablet", baseTime)
	createSession(t, st, joon.ID, "joon-phone", baseTime)

	t.Run("한 사용자의 세션을 모두 끊고 다른 사용자의 세션은 남긴다", func(t *testing.T) {
		deleted, err := st.Queries().DeleteSessionsByUser(ctx, mina.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)
		assert.Zero(t, count(t, pool, "sessions", "user_id", mina.ID))
		assert.Equal(t, 1, count(t, pool, "sessions", "user_id", joon.ID))
	})
}
