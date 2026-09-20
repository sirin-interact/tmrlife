package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const (
	// DefaultTimezone은 가입할 때 시간대를 주지 않은 사용자의 시간대다.
	DefaultTimezone = "Asia/Seoul"

	// MaxDisplayNameLength는 부를 이름의 최대 글자 수다.
	MaxDisplayNameLength = 40

	// 가장 긴 시간대 이름도 이보다 한참 짧다. 시간대 파일을 찾아보기 전에 터무니없는 입력을 걸러낸다.
	maxTimezoneLength = 64
)

// 로그인에 실패한 까닭이다. 로그에만 남기고 응답에는 싣지 않는다.
const (
	failureUnknownEmail  = "unknown_email"
	failureNoPassword    = "no_password"
	failureWrongPassword = "wrong_password"
)

// ServiceOptions는 Service가 기대는 것들이다. 모두 채워야 한다.
type ServiceOptions struct {
	Store    *store.Store
	Clock    clock.Clock
	Logger   *slog.Logger
	Hasher   *Hasher
	Sessions *Sessions
	// KeyRing은 새 사용자의 데이터 키를 만들어 마스터 키로 감싼다.
	KeyRing *crypto.KeyRing
}

// Service는 가입, 로그인, 세션 확인, 로그아웃을 한다. 여러 고루틴이 함께 써도 된다.
type Service struct {
	store    *store.Store
	clock    clock.Clock
	logger   *slog.Logger
	hasher   *Hasher
	sessions *Sessions
	keys     *crypto.KeyRing
	policy   *PasswordPolicy
}

func NewService(opts ServiceOptions) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("auth: service needs a store")
	case opts.Clock == nil:
		return nil, errors.New("auth: service needs a clock")
	case opts.Logger == nil:
		return nil, errors.New("auth: service needs a logger")
	case opts.Hasher == nil:
		return nil, errors.New("auth: service needs a password hasher")
	case opts.Sessions == nil:
		return nil, errors.New("auth: service needs a session manager")
	case opts.KeyRing == nil:
		return nil, errors.New("auth: service needs a key ring")
	}
	policy, err := NewPasswordPolicy()
	if err != nil {
		return nil, err
	}
	return &Service{
		store:    opts.Store,
		clock:    opts.Clock,
		logger:   opts.Logger,
		hasher:   opts.Hasher,
		sessions: opts.Sessions,
		keys:     opts.KeyRing,
		policy:   policy,
	}, nil
}

// SignupInput은 가입 요청이다.
type SignupInput struct {
	Email    string
	Password string
	// DisplayName은 비워 둘 수 있다.
	DisplayName string
	// Timezone은 IANA 시간대 이름이다. 비우면 DefaultTimezone을 쓴다.
	Timezone string
	// Consents에는 CurrentConsents의 넷이 모두 지금 판으로 들어 있어야 한다.
	Consents []ConsentGrant
	Client   ClientInfo
	// PresentedToken은 요청에 딸려 온 세션 쿠키의 값이다. 있으면 그 세션을 끊는다.
	PresentedToken string
}

// LoginInput은 로그인 요청이다.
type LoginInput struct {
	Email    string
	Password string
	Client   ClientInfo
	// PresentedToken은 요청에 딸려 온 세션 쿠키의 값이다. 있으면 그 세션을 끊는다.
	PresentedToken string
}

// Result는 가입이나 로그인에 성공한 결과다. Session.Token을 쿠키에 적는다.
type Result struct {
	User    User
	Session IssuedSession
}

// checkedSignup은 가입 요청에서 다듬고 확인한 값이다.
type checkedSignup struct {
	email       string
	displayName string
	timezone    string
	consents    []ConsentGrant
}

// checkSignup은 DB도 해시도 없이 가려낼 수 있는 것을 모두 본다. 입력만으로 답이 정해지고 아무것도 바꾸지 않는다.
func (s *Service) checkSignup(in SignupInput) (checkedSignup, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return checkedSignup{}, err
	}
	if err := s.policy.Check(in.Password, email); err != nil {
		return checkedSignup{}, err
	}
	consents, err := checkConsents(in.Consents)
	if err != nil {
		return checkedSignup{}, err
	}
	displayName, err := normalizeDisplayName(in.DisplayName)
	if err != nil {
		return checkedSignup{}, err
	}
	timezone, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return checkedSignup{}, err
	}
	return checkedSignup{email: email, displayName: displayName, timezone: timezone, consents: consents}, nil
}

// CheckSignup은 Signup이 해시를 계산하기 전에 거부할 요청인지 미리 알려준다. Signup도 같은 검사로 시작한다.
//
// 시도 한도가 쓴다. 흔한 비밀번호나 빠진 동의로 거부될 요청은 해시를 계산하지 않으므로 한도에서 세지 않는다.
// 세면, 같은 공유기 뒤에 있는 사람들의 오타가 서로의 가입을 막는다.
// 이미 가입된 이메일인지는 여기서 보지 않는다. 그것은 해시를 계산한 뒤에야 알려주고, 그래서 한도에서 센다.
//
// 오류: ErrInvalidEmail, ErrWeakPassword(*PasswordPolicyError), ErrConsentRequired(*ConsentError),
// ErrInvalidDisplayName, ErrInvalidTimezone.
func (s *Service) CheckSignup(in SignupInput) error {
	_, err := s.checkSignup(in)
	return err
}

// Signup은 계정을 만들고 첫 세션까지 연다.
//
// 사용자, 데이터 키, 설정, 동의, 세션을 한 트랜잭션으로 만든다. 키가 없는 사용자는 아무것도 기록할 수 없고,
// 동의가 빠진 사용자는 있어서는 안 되며, 가입은 됐는데 로그인은 안 된 상태도 쓸모가 없다.
//
// 오류: CheckSignup의 오류와 ErrEmailTaken.
func (s *Service) Signup(ctx context.Context, in SignupInput) (Result, error) {
	checked, err := s.checkSignup(in)
	if err != nil {
		return Result{}, err
	}

	// 해시는 트랜잭션을 열기 전에 계산한다. 오래 걸리는 계산을 하는 동안 DB 연결을 쥐고 있지 않으려는 것이다.
	// 이미 가입된 이메일이어도 여기까지는 똑같이 온다. 가입된 주소인지에 따라 걸리는 시간이 달라지지 않는다.
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return Result{}, err
	}

	userID, err := store.NewID()
	if err != nil {
		return Result{}, err
	}
	userKey, err := s.keys.NewUserKey(userID)
	if err != nil {
		return Result{}, fmt.Errorf("auth: create user data key: %w", err)
	}
	if userKey.KEKVersion < 1 || userKey.KEKVersion > math.MaxInt16 {
		return Result{}, fmt.Errorf("auth: master key version %d cannot be stored", userKey.KEKVersion)
	}

	newUser := store.NewUser{
		ID:           userID,
		Email:        checked.email,
		PasswordHash: &hash,
		Timezone:     checked.timezone,
		WrappedDEK:   userKey.Wrapped,
		KEKVersion:   int16(userKey.KEKVersion),
		Now:          s.clock.Now(),
	}
	if checked.displayName != "" {
		newUser.DisplayName = &checked.displayName
	}
	for _, c := range checked.consents {
		id, err := store.NewID()
		if err != nil {
			return Result{}, err
		}
		newUser.Consents = append(newUser.Consents, store.NewConsent{ID: id, Kind: c.Kind, Version: c.Version})
	}

	var result Result
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		row, err := store.InsertUser(ctx, q, newUser)
		if err != nil {
			return err
		}
		session, err := s.openSession(ctx, q, userID, in.Client, in.PresentedToken, newUser.Now)
		if err != nil {
			return err
		}
		result = Result{User: userFromRow(row), Session: session}
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			return Result{}, ErrEmailTaken
		}
		return Result{}, fmt.Errorf("auth: sign up: %w", err)
	}

	s.logger.LogAttrs(ctx, slog.LevelInfo, "user signed up",
		slog.String("user_id", userID.String()),
		slog.String("session_id", result.Session.ID.String()),
	)
	return result, nil
}

// Login은 이메일과 비밀번호를 확인하고 새 세션을 연다.
//
// 없는 이메일이든, 비밀번호가 틀렸든, 비밀번호가 없는 계정이든 똑같이 ErrInvalidCredentials다.
// 어느 경우에도 해시를 한 번 계산하므로 걸리는 시간도 같다.
func (s *Service) Login(ctx context.Context, in LoginInput) (Result, error) {
	var (
		user  db.User
		found bool
	)
	// 꼴이 틀린 주소는 가입되어 있을 수 없다. 찾아보지 않을 뿐, 뒤의 계산은 똑같이 한다.
	if email, err := NormalizeEmail(in.Email); err == nil {
		user, err = s.store.Queries().GetUserByEmail(ctx, email)
		switch {
		case err == nil:
			found = true
		case !errors.Is(err, store.ErrNotFound):
			return Result{}, fmt.Errorf("auth: look up user: %w", err)
		}
	}

	if !found || user.PasswordHash == nil {
		if err := s.hasher.VerifyDummy(ctx, in.Password); err != nil {
			return Result{}, err
		}
		if found {
			s.logLoginFailure(ctx, failureNoPassword, user.ID)
		} else {
			s.logLoginFailure(ctx, failureUnknownEmail, uuid.Nil)
		}
		return Result{}, ErrInvalidCredentials
	}

	storedHash := *user.PasswordHash
	match, err := s.hasher.Verify(ctx, storedHash, in.Password)
	if err != nil {
		if errors.Is(err, ErrMalformedHash) {
			s.logger.LogAttrs(ctx, slog.LevelError, "stored password hash cannot be read",
				slog.String("user_id", user.ID.String()),
			)
		}
		return Result{}, err
	}
	if !match {
		s.logLoginFailure(ctx, failureWrongPassword, user.ID)
		return Result{}, ErrInvalidCredentials
	}

	// 설정이 바뀐 뒤 처음 로그인하는 사용자의 해시를 지금 설정으로 다시 만든다. 비밀번호를 아는 순간은 지금뿐이다.
	// 다시 만들지 못해도 로그인은 그대로 된다. 다음 로그인에서 다시 해 보면 된다.
	var newHash string
	if s.hasher.NeedsRehash(storedHash) {
		newHash, err = s.hasher.Hash(ctx, in.Password)
		if err != nil {
			newHash = ""
			s.logger.LogAttrs(ctx, slog.LevelWarn, "password rehash skipped",
				slog.String("user_id", user.ID.String()),
				slog.String("error", err.Error()),
			)
		}
	}

	now := s.clock.Now()
	var (
		session  IssuedSession
		rehashed bool
	)
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		if newHash != "" {
			updated, err := q.UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{
				ID: user.ID, OldHash: storedHash, NewHash: newHash, Now: now,
			})
			if err != nil {
				return fmt.Errorf("update password hash: %w", err)
			}
			// 0이면 그 사이에 비밀번호가 바뀐 것이다. 새 비밀번호의 해시를 건드리지 않는다.
			rehashed = updated > 0
		}
		var err error
		session, err = s.openSession(ctx, q, user.ID, in.Client, in.PresentedToken, now)
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("auth: log in: %w", err)
	}

	s.logger.LogAttrs(ctx, slog.LevelInfo, "user logged in",
		slog.String("user_id", user.ID.String()),
		slog.String("session_id", session.ID.String()),
		slog.Bool("password_rehashed", rehashed),
	)
	return Result{User: userFromRow(user), Session: session}, nil
}

// Authenticate는 쿠키로 받은 토큰이 가리키는 세션과 사용자를 돌려준다. 통하지 않으면 ErrSessionInvalid다.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	return s.sessions.Authenticate(ctx, token)
}

// Logout은 그 토큰의 세션을 끊는다. 이미 없는 세션이어도 오류가 아니다.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.Logout(ctx, token)
}

// LogoutAll은 그 사용자의 모든 세션을 끊고 끊은 수를 돌려준다.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	deleted, err := s.sessions.LogoutAll(ctx, userID)
	if err != nil {
		return 0, err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "all sessions of user closed",
		slog.String("user_id", userID.String()),
		slog.Int64("sessions", deleted),
	)
	return deleted, nil
}

// openSession은 요청에 딸려 온 옛 세션을 끊고 새 세션을 만든다.
//
// 로그인하면 세션 토큰이 반드시 바뀌어야 한다. 누군가 미리 심어 둔 토큰으로 로그인하게 만든 뒤
// 그 토큰으로 따라 들어오는 공격을 막는다. 브라우저의 쿠키는 새 토큰으로 덮이므로, 옛 세션을 남겨 두면
// 아무도 쓰지 않는데 만료될 때까지 통하는 세션이 된다.
func (s *Service) openSession(ctx context.Context, q *db.Queries, userID uuid.UUID, client ClientInfo, presentedToken string, now time.Time) (IssuedSession, error) {
	if err := s.sessions.revoke(ctx, q, presentedToken); err != nil {
		return IssuedSession{}, err
	}
	return s.sessions.issue(ctx, q, userID, client, now)
}

func (s *Service) logLoginFailure(ctx context.Context, reason string, userID uuid.UUID) {
	attrs := []slog.Attr{slog.String("reason", reason)}
	if userID != uuid.Nil {
		attrs = append(attrs, slog.String("user_id", userID.String()))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "login failed", attrs...)
}

// normalizeDisplayName은 부를 이름을 다듬는다. 정하지 않았으면 빈 문자열을 돌려준다.
func normalizeDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxDisplayNameLength {
		return "", ErrInvalidDisplayName
	}
	for _, r := range name {
		// 줄바꿈이나 방향을 뒤집는 글자가 이름에 들어가면 화면과 알림 문구가 깨진다.
		// 줄 구분자(U+2028)와 문단 구분자(U+2029)는 제어 문자로 분류되지 않지만 줄을 바꾼다.
		// 이름은 알림 문구와 지시문에 끼워 넣으므로, 줄을 바꿀 수 있으면 그 자리에 제 줄을 끼워 넣을 수 있다.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", ErrInvalidDisplayName
		}
	}
	return name, nil
}

// normalizeTimezone은 시간대 이름이 실제로 있는 것인지 본다. 기록 날짜의 경계와 알림 시각이 이 값으로 계산된다.
func normalizeTimezone(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return DefaultTimezone, nil
	}
	if len(name) > maxTimezoneLength {
		return "", ErrInvalidTimezone
	}
	// "Local"은 서버의 시간대를 뜻한다. 사용자의 시간대가 서버가 어디서 도는지에 따라 달라지면 안 된다.
	if name == "Local" {
		return "", ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "", ErrInvalidTimezone
	}
	return name, nil
}
