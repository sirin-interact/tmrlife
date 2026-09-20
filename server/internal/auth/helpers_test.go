package auth

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// 시험은 시계를 읽지 않는다. 모든 시각은 이 값에서 시작해 가짜 시계를 옮겨 만든다.
var baseTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// 설정의 기본값과 같은 수명이다.
var testSessionConfig = SessionConfig{
	AbsoluteLifetime: 30 * day,
	IdleLifetime:     14 * day,
	TouchInterval:    5 * time.Minute,
}

var testClient = ClientInfo{
	UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X)",
	IP:        netip.MustParseAddr("203.0.113.7"),
}

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

// fixture는 진짜 DB와 가짜 시계 위에 올린 인증 서비스다.
type fixture struct {
	service  *Service
	sessions *Sessions
	hasher   *Hasher
	store    *store.Store
	pool     *pgxpool.Pool
	clock    *clock.Fake
	ring     *crypto.KeyRing
	logs     *syncBuffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	return newFixtureOn(t, pool, clock.NewFake(baseTime), cheapParams, testSessionConfig)
}

// newFixtureOn은 같은 DB와 시계 위에 다른 설정의 서비스를 하나 더 올릴 때 쓴다. 설정을 바꿔 다시 띄운 서버를 흉내 낸다.
func newFixtureOn(t *testing.T, pool *pgxpool.Pool, clk *clock.Fake, params Argon2Params, sessionCfg SessionConfig) *fixture {
	t.Helper()
	st := store.New(pool)

	ring, err := crypto.NewKeyRing(1, map[int][]byte{1: bytes.Repeat([]byte{0x11}, 32)})
	require.NoError(t, err)
	sessions, err := NewSessions(st, clk, sessionCfg)
	require.NoError(t, err)
	hasher := newTestHasher(t, params, 4)

	// 운영과 같은 로거를 쓴다. 금지된 이름의 속성을 가리는 안전망까지 포함해서 본다.
	logs := &syncBuffer{}
	service, err := NewService(ServiceOptions{
		Store:    st,
		Clock:    clk,
		Logger:   logging.New(logs, slog.LevelDebug),
		Hasher:   hasher,
		Sessions: sessions,
		KeyRing:  ring,
	})
	require.NoError(t, err)

	return &fixture{
		service: service, sessions: sessions, hasher: hasher,
		store: st, pool: pool, clock: clk, ring: ring, logs: logs,
	}
}

func signupInput(email string) SignupInput {
	return SignupInput{
		Email:    email,
		Password: testPassword,
		Consents: CurrentConsents(),
		Client:   testClient,
	}
}

func (f *fixture) signup(t *testing.T, email string) Result {
	t.Helper()
	result, err := f.service.Signup(t.Context(), signupInput(email))
	require.NoError(t, err)
	return result
}

func (f *fixture) login(t *testing.T, email string) Result {
	t.Helper()
	result, err := f.service.Login(t.Context(), LoginInput{Email: email, Password: testPassword, Client: testClient})
	require.NoError(t, err)
	return result
}

func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&n))
	return n
}

func (f *fixture) countFor(t *testing.T, table string, userID uuid.UUID) int {
	t.Helper()
	var n int
	err := f.pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1`, table), userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// sessionRow는 DB에 저장된 세션의 시각이다. 서비스가 돌려준 값이 아니라 실제로 적힌 값을 볼 때 쓴다.
type sessionRow struct {
	lastSeenAt time.Time
	expiresAt  time.Time
}

func (f *fixture) sessionRow(t *testing.T, id uuid.UUID) sessionRow {
	t.Helper()
	var row sessionRow
	err := f.pool.QueryRow(t.Context(), `SELECT last_seen_at, expires_at FROM sessions WHERE id = $1`, id).
		Scan(&row.lastSeenAt, &row.expiresAt)
	require.NoError(t, err)
	return row
}
