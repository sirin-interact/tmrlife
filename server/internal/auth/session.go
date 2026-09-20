package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// maxUserAgentLength는 저장하는 User-Agent의 최대 글자 수다.
// 어느 기기의 세션인지 알아볼 만큼이면 되고, 헤더는 보내는 쪽이 얼마든지 길게 만들 수 있다.
const maxUserAgentLength = 255

// 역할 이름이다. users.role의 CHECK 제약과 같은 값이어야 한다.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// SessionConfig는 세션이 언제 끝나는지를 정한다. 값은 설정에서 받는다.
type SessionConfig struct {
	// AbsoluteLifetime은 로그인한 때부터 잰다. 계속 쓰고 있어도 이 시간이 지나면 다시 로그인해야 한다.
	// 훔친 세션을 끝없이 이어 쓸 수 없게 하는 한도다.
	AbsoluteLifetime time.Duration
	// IdleLifetime은 마지막으로 쓴 때부터 잰다.
	IdleLifetime time.Duration
	// TouchInterval은 마지막으로 쓴 시각을 DB에 다시 적는 최소 간격이다.
	TouchInterval time.Duration
}

func (c SessionConfig) validate() error {
	switch {
	case c.AbsoluteLifetime <= 0 || c.IdleLifetime <= 0 || c.TouchInterval <= 0:
		return errors.New("auth: session lifetimes and touch interval must be greater than zero")
	case c.IdleLifetime > c.AbsoluteLifetime:
		return errors.New("auth: session idle lifetime must not exceed the absolute lifetime")
	case c.TouchInterval >= c.IdleLifetime:
		// 다시 적는 간격이 더 길면, 계속 쓰고 있는 세션이 쓰는 도중에 끝난다.
		return errors.New("auth: session touch interval must be shorter than the idle lifetime")
	}
	return nil
}

// ClientInfo는 세션을 만든 요청에 관한 것이다. 사용자가 자기 세션 목록에서 기기를 알아보는 데 쓴다.
type ClientInfo struct {
	// UserAgent는 길면 잘라서 저장한다.
	UserAgent string
	// IP는 부르는 쪽이 준 그대로 저장한다. 프록시 뒤에서 진짜 주소를 가려내는 일은 부르는 쪽이 한다.
	// 모르면 빈 값으로 둔다.
	IP netip.Addr
}

// User는 인증된 사용자다. 비밀번호 해시처럼 밖으로 나가면 안 되는 값은 담지 않는다.
type User struct {
	ID            uuid.UUID
	Email         string
	EmailVerified bool
	// DisplayName은 정하지 않았으면 빈 문자열이다.
	DisplayName string
	// Timezone은 IANA 시간대 이름이다.
	Timezone string
	Role     string
	IsDemo   bool
	// HasPassword가 거짓이면 소셜 로그인으로만 들어올 수 있는 계정이다.
	HasPassword bool
	CreatedAt   time.Time
}

func (u User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

// LogValue는 사용자를 통째로 로그에 넘겨도 ID만 남게 한다.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", u.ID.String()))
}

func userFromRow(row db.User) User {
	u := User{
		ID:            row.ID,
		Email:         row.Email,
		EmailVerified: row.EmailVerifiedAt != nil,
		Timezone:      row.Timezone,
		Role:          row.Role,
		IsDemo:        row.IsDemo,
		HasPassword:   row.PasswordHash != nil,
		CreatedAt:     row.CreatedAt,
	}
	if row.DisplayName != nil {
		u.DisplayName = *row.DisplayName
	}
	return u
}

// userFromSessionRow는 세션을 찾으면서 함께 읽은 사용자를 옮긴다.
// 그 쿼리는 비밀번호 해시를 읽지 않는다. 요청마다 도는 길에 해시를 싣지 않으려는 것이라서, 있는지 없는지만 받는다.
func userFromSessionRow(row db.GetSessionWithUserByTokenHashRow) User {
	u := User{
		ID:            row.Session.UserID,
		Email:         row.Email,
		EmailVerified: row.EmailVerifiedAt != nil,
		Timezone:      row.Timezone,
		Role:          row.Role,
		IsDemo:        row.IsDemo,
		HasPassword:   row.HasPassword,
		CreatedAt:     row.UserCreatedAt,
	}
	if row.DisplayName != nil {
		u.DisplayName = *row.DisplayName
	}
	return u
}

// Session은 세션 하나의 상태다. 토큰은 담지 않는다.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	// ExpiresAt은 지금부터 쓰지 않으면 세션이 끝나는 시각이다. 쓰는 동안 뒤로 밀리되 AbsoluteExpiresAt을 넘지 않는다.
	ExpiresAt time.Time
	// AbsoluteExpiresAt은 계속 써도 세션이 끝나는 시각이다. 쿠키의 만료로 쓰기에 알맞다.
	AbsoluteExpiresAt time.Time
}

// IssuedSession은 방금 만든 세션과 그 토큰이다. 토큰은 이때 한 번만 손에 있다.
type IssuedSession struct {
	Session
	Token SessionToken
}

// Principal은 요청을 보낸 사람이 누구인지다.
type Principal struct {
	User    User
	Session Session
	// Renewed는 이번 확인에서 세션의 만료가 뒤로 밀렸다는 뜻이다.
	Renewed bool
}

// LogValue는 식별자만 남긴다.
func (p Principal) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user_id", p.User.ID.String()),
		slog.String("session_id", p.Session.ID.String()),
	)
}

// Sessions는 세션을 만들고, 확인하고, 끊는다. 여러 고루틴이 함께 써도 된다.
type Sessions struct {
	store  *store.Store
	clock  clock.Clock
	cfg    SessionConfig
	random io.Reader
}

func NewSessions(st *store.Store, clk clock.Clock, cfg SessionConfig) (*Sessions, error) {
	if st == nil {
		return nil, errors.New("auth: sessions need a store")
	}
	if clk == nil {
		return nil, errors.New("auth: sessions need a clock")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Sessions{store: st, clock: clk, cfg: cfg, random: rand.Reader}, nil
}

// Authenticate는 쿠키로 받은 토큰이 가리키는 세션과 사용자를 돌려준다.
// 세션이 없거나, 끝났거나, 끊겼으면 ErrSessionInvalid다.
//
// 마지막으로 쓴 시각은 TouchInterval이 지났을 때만 다시 적는다. 그 사이의 요청은 DB에 아무것도 쓰지 않는다.
func (s *Sessions) Authenticate(ctx context.Context, token string) (Principal, error) {
	hash, ok := hashToken(token)
	if !ok {
		return Principal{}, ErrSessionInvalid
	}
	now := s.clock.Now()

	q := s.store.Queries()
	row, err := q.GetSessionWithUserByTokenHash(ctx, db.GetSessionWithUserByTokenHashParams{TokenHash: hash, Now: now})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, ErrSessionInvalid
		}
		return Principal{}, fmt.Errorf("auth: look up session: %w", err)
	}

	session := s.sessionFromRow(row.Session)
	// 저장된 만료 시각은 세션을 만들 때의 설정으로 계산한 값이다. 설정에서 수명을 줄였다면
	// 이미 만들어진 세션도 새 한도를 따라야 하므로, 지금 설정으로 한 번 더 본다.
	if !now.Before(session.AbsoluteExpiresAt) || !now.Before(session.LastSeenAt.Add(s.cfg.IdleLifetime)) {
		return Principal{}, ErrSessionInvalid
	}

	principal := Principal{User: userFromSessionRow(row), Session: session}
	if now.Sub(session.LastSeenAt) < s.cfg.TouchInterval {
		return principal, nil
	}

	expiresAt := s.expiresAt(session.CreatedAt, now)
	touched, err := q.TouchSession(ctx, db.TouchSessionParams{ID: session.ID, Now: now, ExpiresAt: expiresAt})
	if err != nil {
		return Principal{}, fmt.Errorf("auth: renew session: %w", err)
	}
	if touched == 0 {
		// 찾은 뒤에 다른 요청이 로그아웃했다.
		return Principal{}, ErrSessionInvalid
	}
	principal.Session.LastSeenAt = now
	if expiresAt.After(principal.Session.ExpiresAt) {
		principal.Session.ExpiresAt = expiresAt
	}
	principal.Renewed = true
	return principal, nil
}

// Logout은 그 토큰의 세션을 끊는다. 이미 없는 세션이어도 오류가 아니다. 로그아웃은 몇 번을 눌러도 같은 결과여야 한다.
func (s *Sessions) Logout(ctx context.Context, token string) error {
	return s.revoke(ctx, s.store.Queries(), token)
}

// LogoutAll은 그 사용자의 모든 세션을 끊고 끊은 수를 돌려준다.
// 비밀번호를 바꿨을 때, 기기를 잃어버렸을 때, 계정을 지울 때 쓴다.
func (s *Sessions) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	deleted, err := s.store.Queries().DeleteSessionsByUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: delete sessions of user: %w", err)
	}
	return deleted, nil
}

// DeleteExpired는 끝난 세션의 행을 지우고 지운 수를 돌려준다.
// 끝난 세션은 지우지 않아도 통하지 않는다. 지우는 것은 테이블이 끝없이 자라지 않게 하려는 것이다.
func (s *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	deleted, err := s.store.Queries().DeleteExpiredSessions(ctx, s.clock.Now())
	if err != nil {
		return 0, fmt.Errorf("auth: delete expired sessions: %w", err)
	}
	return deleted, nil
}

// issue는 q가 묶인 트랜잭션 안에서 세션을 만든다. 계정을 만드는 일이나 해시를 바꾸는 일과 함께 성공하거나 함께 실패한다.
func (s *Sessions) issue(ctx context.Context, q *db.Queries, userID uuid.UUID, client ClientInfo, now time.Time) (IssuedSession, error) {
	id, err := store.NewID()
	if err != nil {
		return IssuedSession{}, err
	}
	token, hash, err := newToken(s.random)
	if err != nil {
		return IssuedSession{}, err
	}

	params := db.CreateSessionParams{
		ID:        id,
		UserID:    userID,
		TokenHash: hash,
		Now:       now,
		ExpiresAt: s.expiresAt(now, now),
	}
	if agent := cleanUserAgent(client.UserAgent); agent != "" {
		params.UserAgent = &agent
	}
	if client.IP.IsValid() {
		ip := client.IP
		params.IP = &ip
	}

	row, err := q.CreateSession(ctx, params)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("auth: insert session: %w", err)
	}
	return IssuedSession{Session: s.sessionFromRow(row), Token: token}, nil
}

// revoke는 그 토큰의 세션을 지운다. 토큰의 꼴이 아니면 지울 것이 없으므로 아무 일도 하지 않는다.
func (s *Sessions) revoke(ctx context.Context, q *db.Queries, token string) error {
	hash, ok := hashToken(token)
	if !ok {
		return nil
	}
	if _, err := q.DeleteSessionByTokenHash(ctx, hash); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// expiresAt은 now에 세션을 썼을 때의 만료 시각이다. 쉬는 시간의 한도만큼 뒤로 밀되 전체 수명을 넘기지 않는다.
func (s *Sessions) expiresAt(createdAt, now time.Time) time.Time {
	idleEnd := now.Add(s.cfg.IdleLifetime)
	absoluteEnd := createdAt.Add(s.cfg.AbsoluteLifetime)
	if idleEnd.After(absoluteEnd) {
		return absoluteEnd
	}
	return idleEnd
}

func (s *Sessions) sessionFromRow(row db.Session) Session {
	return Session{
		ID:                row.ID,
		UserID:            row.UserID,
		CreatedAt:         row.CreatedAt,
		LastSeenAt:        row.LastSeenAt,
		ExpiresAt:         row.ExpiresAt,
		AbsoluteExpiresAt: row.CreatedAt.Add(s.cfg.AbsoluteLifetime),
	}
}

// cleanUserAgent는 헤더 값을 DB의 text 컬럼에 넣을 수 있는 꼴로 다듬고 길이를 자른다.
// 헤더는 보내는 쪽이 마음대로 만든다. 글자로 읽을 수 없는 바이트나 NUL이 섞여 있으면 DB가 행을 거부하고,
// 그러면 헤더 하나로 로그인을 실패시킬 수 있게 된다.
func cleanUserAgent(raw string) string {
	agent := strings.ToValidUTF8(raw, "")
	agent = strings.TrimSpace(strings.ReplaceAll(agent, "\x00", ""))
	// 바이트가 아니라 글자 수로 자른다. 바이트로 자르면 여러 바이트짜리 글자의 가운데가 잘릴 수 있다.
	seen := 0
	for i := range agent {
		if seen == maxUserAgentLength {
			return agent[:i]
		}
		seen++
	}
	return agent
}
