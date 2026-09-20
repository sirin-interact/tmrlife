package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

func TestSignup(t *testing.T) {
	t.Parallel()

	t.Run("사용자, 데이터 키, 설정, 동의, 첫 세션을 함께 만든다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()

		in := signupInput("  Mina.Kim@Example.COM ")
		in.DisplayName = "  미나  "
		result, err := f.service.Signup(ctx, in)
		require.NoError(t, err)

		// 돌려준 사용자
		assert.Equal(t, "mina.kim@example.com", result.User.Email, "다듬은 주소로 저장한다")
		assert.Equal(t, "미나", result.User.DisplayName)
		assert.Equal(t, DefaultTimezone, result.User.Timezone)
		assert.Equal(t, RoleUser, result.User.Role, "가입으로는 일반 사용자만 만들어진다")
		assert.False(t, result.User.IsAdmin())
		assert.False(t, result.User.IsDemo)
		assert.False(t, result.User.EmailVerified)
		assert.True(t, result.User.HasPassword)
		assert.True(t, baseTime.Equal(result.User.CreatedAt))

		// 사용자 행: 비밀번호는 해시로만 있다.
		row, err := f.store.Queries().GetUserByID(ctx, result.User.ID)
		require.NoError(t, err)
		require.NotNil(t, row.PasswordHash)
		assert.True(t, strings.HasPrefix(*row.PasswordHash, "$argon2id$v=19$m=64,t=1,p=1$"))
		assert.NotContains(t, *row.PasswordHash, testPassword)
		ok, err := f.hasher.Verify(ctx, *row.PasswordHash, testPassword)
		require.NoError(t, err)
		assert.True(t, ok)

		// 데이터 키: 저장된 값을 마스터 키로 풀면 글을 잠그고 열 수 있다.
		keyRow, err := f.store.Queries().GetUserKey(ctx, result.User.ID)
		require.NoError(t, err)
		assert.Equal(t, int16(1), keyRow.KEKVersion)
		sealer, err := f.ring.Unwrap(result.User.ID, keyRow.WrappedDEK, int(keyRow.KEKVersion))
		require.NoError(t, err)
		place := crypto.AAD{Table: "diaries", Column: "body_enc", RowID: result.User.ID}
		sealed, err := sealer.SealString("오늘은 조금 걸었다", place)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, place)
		require.NoError(t, err)
		assert.Equal(t, "오늘은 조금 걸었다", opened)

		// 설정: 기본값으로 시작한다.
		settings, err := f.store.Queries().GetUserSettings(ctx, result.User.ID)
		require.NoError(t, err)
		assert.True(t, settings.ReminderEnabled)
		assert.Equal(t, pgtype.Time{Microseconds: 20 * 3600 * 1_000_000, Valid: true}, settings.ReminderTime)
		assert.True(t, settings.AnalysisEnabled)
		assert.True(t, baseTime.Equal(settings.UpdatedAt))

		// 동의: 네 가지가 지금 판으로, 주입받은 시계의 시각으로 남는다.
		consents, err := f.store.Queries().ListActiveConsents(ctx, result.User.ID)
		require.NoError(t, err)
		require.Len(t, consents, 4)
		versions := map[string]string{}
		for _, c := range consents {
			versions[c.Kind] = c.Version
			assert.True(t, baseTime.Equal(c.GrantedAt))
			assert.Nil(t, c.WithdrawnAt)
		}
		assert.Equal(t, map[string]string{
			store.ConsentTerms:            TermsVersion,
			store.ConsentPrivacy:          PrivacyVersion,
			store.ConsentSensitiveData:    SensitiveDataVersion,
			store.ConsentOverseasTransfer: OverseasTransferVersion,
		}, versions)

		// 세션: 토큰은 돌려주기만 하고 DB에는 해시만 있다.
		token := result.Session.Token.Reveal()
		raw, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)
		require.Len(t, raw, 32)
		wantHash := sha256.Sum256(raw)

		var (
			tokenHash []byte
			userAgent *string
			ip        *netip.Addr
			createdAt time.Time
		)
		err = f.pool.QueryRow(ctx, `SELECT token_hash, user_agent, ip, created_at FROM sessions WHERE id = $1`, result.Session.ID).
			Scan(&tokenHash, &userAgent, &ip, &createdAt)
		require.NoError(t, err)
		assert.Equal(t, wantHash[:], tokenHash)
		assert.NotEqual(t, raw, tokenHash, "토큰 원문을 저장하지 않는다")
		require.NotNil(t, userAgent)
		assert.Equal(t, testClient.UserAgent, *userAgent)
		require.NotNil(t, ip)
		assert.Equal(t, testClient.IP, *ip)
		assert.True(t, baseTime.Equal(createdAt))

		assert.Equal(t, result.User.ID, result.Session.UserID)
		assert.True(t, baseTime.Equal(result.Session.CreatedAt))
		assert.True(t, baseTime.Equal(result.Session.LastSeenAt))
		assert.True(t, baseTime.Add(14*day).Equal(result.Session.ExpiresAt), "쓰지 않으면 14일 뒤에 끝난다")
		assert.True(t, baseTime.Add(30*day).Equal(result.Session.AbsoluteExpiresAt), "계속 써도 30일 뒤에 끝난다")

		// 받은 토큰으로 바로 들어올 수 있다.
		principal, err := f.service.Authenticate(ctx, token)
		require.NoError(t, err)
		assert.Equal(t, result.User, principal.User)
		assert.Equal(t, result.Session.ID, principal.Session.ID)

		assert.Equal(t, 1, f.count(t, "users"))
		assert.Equal(t, 1, f.count(t, "user_keys"))
		assert.Equal(t, 1, f.count(t, "user_settings"))
		assert.Equal(t, 4, f.count(t, "consents"))
		assert.Equal(t, 1, f.count(t, "sessions"))

		// 로그에는 식별자만 남는다.
		logs := f.logs.String()
		assert.Contains(t, logs, result.User.ID.String())
		for _, secret := range []string{"mina.kim", "Mina.Kim", "example", testPassword, token, *row.PasswordHash, "미나"} {
			assert.NotContains(t, logs, secret)
		}
	})

	t.Run("시간대를 주면 그 시간대로 가입한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		in := signupInput("mina@example.com")
		in.Timezone = "America/Los_Angeles"
		result, err := f.service.Signup(t.Context(), in)
		require.NoError(t, err)
		assert.Equal(t, "America/Los_Angeles", result.User.Timezone)
		assert.Empty(t, result.User.DisplayName)
	})

	t.Run("자모를 늘어놓은 꼴로 정한 비밀번호로 가입하고 완성형으로 로그인한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		in := signupInput("mina@example.com")
		in.Password = norm.NFD.String("저녁에는 산책을 해요")
		_, err := f.service.Signup(t.Context(), in)
		require.NoError(t, err)

		_, err = f.service.Login(t.Context(), LoginInput{Email: "mina@example.com", Password: "저녁에는 산책을 해요"})
		require.NoError(t, err)
	})
}

func TestSignup_DuplicateEmail(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := f.signup(t, "mina@example.com")

	tests := []struct {
		name  string
		email string
	}{
		{"같은 주소", "mina@example.com"},
		{"대소문자만 다른 주소", "MINA@Example.Com"},
		{"앞뒤에 공백이 붙은 주소", "  mina@example.com  "},
	}
	for _, tt := range tests {
		t.Run(tt.name+"로는 다시 가입할 수 없다", func(t *testing.T) {
			result, err := f.service.Signup(t.Context(), signupInput(tt.email))
			require.ErrorIs(t, err, ErrEmailTaken)
			assert.NotContains(t, err.Error(), "example", "오류 문구에 주소가 들어가면 로그에 그대로 남는다")
			assert.True(t, result.Session.Token.IsZero())

			// 되돌려져서 아무것도 남지 않는다.
			assert.Equal(t, 1, f.count(t, "users"))
			assert.Equal(t, 1, f.count(t, "user_keys"))
			assert.Equal(t, 4, f.count(t, "consents"))
			assert.Equal(t, 1, f.count(t, "sessions"))
		})
	}

	t.Run("먼저 가입한 사람의 세션과 비밀번호는 그대로다", func(t *testing.T) {
		_, err := f.service.Authenticate(t.Context(), first.Session.Token.Reveal())
		require.NoError(t, err)
		f.login(t, "mina@example.com")
	})
}

func TestSignup_Rejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	tests := []struct {
		name   string
		mutate func(in *SignupInput)
		want   error
	}{
		{"이메일의 꼴이 틀리면", func(in *SignupInput) { in.Email = "Mina <mina@example.com>" }, ErrInvalidEmail},
		{"비밀번호가 짧으면", func(in *SignupInput) { in.Password = "짧은비밀번호" }, ErrWeakPassword},
		{"비밀번호가 흔하면", func(in *SignupInput) { in.Password = "qwertyuiop123" }, ErrWeakPassword},
		{"비밀번호가 이메일과 같으면", func(in *SignupInput) { in.Password = in.Email }, ErrWeakPassword},
		{"동의가 하나도 없으면", func(in *SignupInput) { in.Consents = nil }, ErrConsentRequired},
		{"마음 기록에 대한 동의가 빠지면", func(in *SignupInput) { in.Consents = allCurrentExcept(store.ConsentSensitiveData) }, ErrConsentRequired},
		{"해외 전송에 대한 동의가 빠지면", func(in *SignupInput) { in.Consents = allCurrentExcept(store.ConsentOverseasTransfer) }, ErrConsentRequired},
		{"옛 판의 약관에 동의했으면", func(in *SignupInput) {
			in.Consents = append(allCurrentExcept(store.ConsentTerms), ConsentGrant{Kind: store.ConsentTerms, Version: "2025-01-01"})
		}, ErrConsentRequired},
		{"부를 이름이 너무 길면", func(in *SignupInput) { in.DisplayName = strings.Repeat("가", MaxDisplayNameLength+1) }, ErrInvalidDisplayName},
		{"부를 이름에 줄바꿈이 있으면", func(in *SignupInput) { in.DisplayName = "미나\n관리자" }, ErrInvalidDisplayName},
		{"부를 이름에 글자 방향을 뒤집는 글자가 있으면", func(in *SignupInput) { in.DisplayName = "미나\u202e" }, ErrInvalidDisplayName},
		{"부를 이름에 줄 구분자(U+2028)가 있으면", func(in *SignupInput) { in.DisplayName = "미나\u2028관리자" }, ErrInvalidDisplayName},
		{"부를 이름에 문단 구분자(U+2029)가 있으면", func(in *SignupInput) { in.DisplayName = "미나\u2029관리자" }, ErrInvalidDisplayName},
		{"모르는 시간대면", func(in *SignupInput) { in.Timezone = "Mars/Olympus_Mons" }, ErrInvalidTimezone},
		{"시간대가 서버의 시간대를 가리키면", func(in *SignupInput) { in.Timezone = "Local" }, ErrInvalidTimezone},
		{"시간대 자리에 경로를 넣으면", func(in *SignupInput) { in.Timezone = "../../etc/passwd" }, ErrInvalidTimezone},
	}
	for _, tt := range tests {
		t.Run(tt.name+" 가입할 수 없다", func(t *testing.T) {
			in := signupInput("mina@example.com")
			tt.mutate(&in)
			before := f.hasher.computed.Load()

			// 시도 한도는 이 답을 보고 셀지 말지를 정한다. 가입과 답이 어긋나면 세지 않은 요청이 해시까지 간다.
			require.ErrorIs(t, f.service.CheckSignup(in), tt.want, "미리 보는 검사도 같은 답을 내야 한다")

			result, err := f.service.Signup(t.Context(), in)
			require.ErrorIs(t, err, tt.want)
			assert.True(t, result.Session.Token.IsZero())
			assert.Equal(t, before, f.hasher.computed.Load(), "받지 않을 요청에 해시를 계산하지 않는다")

			for _, table := range []string{"users", "user_keys", "user_settings", "consents", "sessions"} {
				assert.Zero(t, f.count(t, table), table)
			}
		})
	}

	t.Run("빠진 동의가 무엇인지 알려준다", func(t *testing.T) {
		in := signupInput("mina@example.com")
		in.Consents = allCurrentExcept(store.ConsentOverseasTransfer)
		_, err := f.service.Signup(t.Context(), in)
		var consentErr *ConsentError
		require.ErrorAs(t, err, &consentErr)
		assert.Equal(t, []string{store.ConsentOverseasTransfer}, consentErr.Missing)
	})

	t.Run("비밀번호가 어느 규칙을 어겼는지 알려주되 비밀번호는 담지 않는다", func(t *testing.T) {
		in := signupInput("mina@example.com")
		in.Password = "password123"
		_, err := f.service.Signup(t.Context(), in)
		var policyErr *PasswordPolicyError
		require.ErrorAs(t, err, &policyErr)
		assert.Equal(t, []PasswordReason{PasswordTooCommon}, policyErr.Reasons)
		assert.NotContains(t, err.Error(), "password123")
	})
}

func TestCheckSignup(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	t.Run("받을 수 있는 요청은 통과시키고 아무것도 만들지 않는다", func(t *testing.T) {
		before := f.hasher.computed.Load()
		require.NoError(t, f.service.CheckSignup(signupInput("mina@example.com")))
		assert.Equal(t, before, f.hasher.computed.Load(), "해시를 계산하지 않는다")
		assert.Zero(t, f.count(t, "users"))
	})

	t.Run("이미 가입된 이메일인지는 알려주지 않는다", func(t *testing.T) {
		f.signup(t, "taken@example.com")
		assert.NoError(t, f.service.CheckSignup(signupInput("taken@example.com")), "가입된 주소인지는 해시를 계산한 뒤에만 알려준다")
	})
}

func TestSignup_AllOrNothing(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	f := newFixtureOn(t, pool, clock.NewFake(baseTime), cheapParams, testSessionConfig)

	// 세션을 만드는 마지막 단계에서 실패하게 한다. 그 앞에 만든 사용자, 키, 설정, 동의가 함께 되돌려져야 한다.
	f.sessions.random = brokenReader{}
	_, err := f.service.Signup(t.Context(), signupInput("mina@example.com"))
	require.ErrorIs(t, err, errBrokenRandom)

	for _, table := range []string{"users", "user_keys", "user_settings", "consents", "sessions"} {
		assert.Zero(t, f.count(t, table), "%s: 가입은 됐는데 로그인은 안 된 상태를 남기지 않는다", table)
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()

	t.Run("맞는 이메일과 비밀번호로 새 세션을 연다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()
		signedUp := f.signup(t, "mina@example.com")
		f.clock.Advance(time.Hour)

		before := f.hasher.computed.Load()
		result, err := f.service.Login(ctx, LoginInput{
			Email: " MINA@example.com ", Password: testPassword, Client: testClient,
		})
		require.NoError(t, err)
		assert.Equal(t, before+1, f.hasher.computed.Load(), "해시를 한 번만 계산한다")

		assert.Equal(t, signedUp.User, result.User)
		assert.NotEqual(t, signedUp.Session.ID, result.Session.ID)
		assert.NotEqual(t, signedUp.Session.Token.Reveal(), result.Session.Token.Reveal())
		assert.True(t, baseTime.Add(time.Hour).Equal(result.Session.CreatedAt), "세션의 시각은 주입받은 시계에서 온다")

		principal, err := f.service.Authenticate(ctx, result.Session.Token.Reveal())
		require.NoError(t, err)
		assert.Equal(t, signedUp.User.ID, principal.User.ID)

		// 다른 기기의 세션은 그대로 둔다. 요청에 딸려 온 세션만 끊는다.
		_, err = f.service.Authenticate(ctx, signedUp.Session.Token.Reveal())
		require.NoError(t, err)
		assert.Equal(t, 2, f.countFor(t, "sessions", signedUp.User.ID))

		logs := f.logs.String()
		assert.Contains(t, logs, `"msg":"user logged in"`)
		for _, secret := range []string{"mina@", "MINA@", testPassword, result.Session.Token.Reveal()} {
			assert.NotContains(t, logs, secret)
		}
	})
}

func TestLogin_Failures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	f.signup(t, "mina@example.com")

	// 비밀번호 없이 만들어진 계정이다(소셜 로그인).
	socialID, err := store.NewID()
	require.NoError(t, err)
	_, err = f.store.CreateUser(ctx, store.NewUser{
		ID: socialID, Email: "social@example.com", Timezone: DefaultTimezone,
		WrappedDEK: []byte{1, 2, 3}, KEKVersion: 1, Now: baseTime,
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"없는 이메일", "nobody@example.com", testPassword},
		{"틀린 비밀번호", "mina@example.com", testPassword + "!"},
		{"빈 비밀번호", "mina@example.com", ""},
		{"비밀번호가 없는 계정", "social@example.com", testPassword},
		{"꼴이 틀린 이메일", "Mina <mina@example.com>", testPassword},
		{"빈 이메일", "", testPassword},
		{"한도를 넘는 긴 비밀번호", "mina@example.com", strings.Repeat("a", 5000)},
		{"없는 이메일에 한도를 넘는 긴 비밀번호", "nobody@example.com", strings.Repeat("a", 5000)},
	}

	var first error
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionsBefore := f.count(t, "sessions")
			computedBefore := f.hasher.computed.Load()

			result, err := f.service.Login(ctx, LoginInput{Email: tt.email, Password: tt.password, Client: testClient})

			require.ErrorIs(t, err, ErrInvalidCredentials)
			// 어느 경우든 돌려주는 오류가 글자 하나까지 같아야 한다. 다르면 그 차이로 가입된 주소인지 알 수 있다.
			if first == nil {
				first = err
			}
			assert.Equal(t, first, err)
			assert.Equal(t, first.Error(), err.Error())

			// 어느 경우든 해시를 정확히 한 번 계산한다. 계산을 건너뛰는 경우가 있으면 걸린 시간으로 가려낼 수 있다.
			assert.Equal(t, computedBefore+1, f.hasher.computed.Load())

			assert.Equal(t, User{}, result.User)
			assert.True(t, result.Session.Token.IsZero())
			assert.Equal(t, sessionsBefore, f.count(t, "sessions"), "세션을 만들지 않는다")
		})
	}

	t.Run("로그에는 실패한 까닭과 사용자 ID만 남는다", func(t *testing.T) {
		logs := f.logs.String()
		assert.Contains(t, logs, `"reason":"unknown_email"`)
		assert.Contains(t, logs, `"reason":"wrong_password"`)
		assert.Contains(t, logs, `"reason":"no_password"`)
		assert.Contains(t, logs, socialID.String())
		for _, secret := range []string{"@example.com", testPassword, "nobody", "aaaaaaaa"} {
			assert.NotContains(t, logs, secret)
		}
	})
}

func TestLogin_MalformedStoredHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	signedUp := f.signup(t, "mina@example.com")

	_, err := f.pool.Exec(ctx, `UPDATE users SET password_hash = 'not-a-hash' WHERE id = $1`, signedUp.User.ID)
	require.NoError(t, err)

	_, err = f.service.Login(ctx, LoginInput{Email: "mina@example.com", Password: testPassword})
	require.ErrorIs(t, err, ErrMalformedHash)
	require.NotErrorIs(t, err, ErrInvalidCredentials, "데이터가 깨진 것을 비밀번호가 틀린 것으로 알리지 않는다")
	assert.Contains(t, f.logs.String(), `"msg":"stored password hash cannot be read"`)
	assert.NotContains(t, f.logs.String(), "not-a-hash")
}

func TestLogin_Rehash(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	clk := clock.NewFake(baseTime)
	ctx := t.Context()

	// 옛 설정으로 가입한다.
	old := newFixtureOn(t, pool, clk, cheapParams, testSessionConfig)
	signedUp := old.signup(t, "mina@example.com")
	oldRow, err := old.store.Queries().GetUserByID(ctx, signedUp.User.ID)
	require.NoError(t, err)
	require.Contains(t, *oldRow.PasswordHash, "$m=64,t=1,p=1$")

	// 설정을 올려 서버를 다시 띄웠다.
	stronger := Argon2Params{MemoryKiB: 128, Time: 2, Parallelism: 1}
	current := newFixtureOn(t, pool, clk, stronger, testSessionConfig)
	loginAt := clk.Advance(3 * day)

	t.Run("틀린 비밀번호로는 해시를 바꾸지 않는다", func(t *testing.T) {
		_, err := current.service.Login(ctx, LoginInput{Email: "mina@example.com", Password: "틀린 비밀번호입니다 정말로"})
		require.ErrorIs(t, err, ErrInvalidCredentials)

		row, err := current.store.Queries().GetUserByID(ctx, signedUp.User.ID)
		require.NoError(t, err)
		assert.Equal(t, oldRow.PasswordHash, row.PasswordHash)
	})

	t.Run("로그인에 성공하면 해시를 지금 설정으로 다시 만들어 넣는다", func(t *testing.T) {
		before := current.hasher.computed.Load()
		_, err := current.service.Login(ctx, LoginInput{Email: "mina@example.com", Password: testPassword})
		require.NoError(t, err)
		assert.Equal(t, before+2, current.hasher.computed.Load(), "확인에 한 번, 다시 만드는 데 한 번")

		row, err := current.store.Queries().GetUserByID(ctx, signedUp.User.ID)
		require.NoError(t, err)
		require.NotNil(t, row.PasswordHash)
		assert.Contains(t, *row.PasswordHash, "$m=128,t=2,p=1$")
		assert.False(t, current.hasher.NeedsRehash(*row.PasswordHash))
		assert.True(t, loginAt.Equal(row.UpdatedAt))

		ok, err := current.hasher.Verify(ctx, *row.PasswordHash, testPassword)
		require.NoError(t, err)
		assert.True(t, ok, "같은 비밀번호로 계속 로그인할 수 있어야 한다")
		assert.Contains(t, current.logs.String(), `"password_rehashed":true`)
	})

	t.Run("다시 만든 뒤의 로그인은 해시를 건드리지 않는다", func(t *testing.T) {
		row, err := current.store.Queries().GetUserByID(ctx, signedUp.User.ID)
		require.NoError(t, err)

		clk.Advance(day)
		before := current.hasher.computed.Load()
		_, err = current.service.Login(ctx, LoginInput{Email: "mina@example.com", Password: testPassword})
		require.NoError(t, err)
		assert.Equal(t, before+1, current.hasher.computed.Load())

		after, err := current.store.Queries().GetUserByID(ctx, signedUp.User.ID)
		require.NoError(t, err)
		assert.Equal(t, row.PasswordHash, after.PasswordHash)
		assert.True(t, row.UpdatedAt.Equal(after.UpdatedAt))
	})
}

func TestNewService_RequiresEverything(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	full := ServiceOptions{
		Store: f.store, Clock: f.clock, Logger: f.service.logger, Hasher: f.hasher, Sessions: f.sessions, KeyRing: f.ring,
	}

	tests := []struct {
		name   string
		mutate func(o *ServiceOptions)
	}{
		{"저장소", func(o *ServiceOptions) { o.Store = nil }},
		{"시계", func(o *ServiceOptions) { o.Clock = nil }},
		{"로거", func(o *ServiceOptions) { o.Logger = nil }},
		{"해시 계산기", func(o *ServiceOptions) { o.Hasher = nil }},
		{"세션 관리자", func(o *ServiceOptions) { o.Sessions = nil }},
		{"마스터 키 묶음", func(o *ServiceOptions) { o.KeyRing = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name+" 없이는 만들지 않는다", func(t *testing.T) {
			t.Parallel()
			opts := full
			tt.mutate(&opts)
			service, err := NewService(opts)
			require.Error(t, err)
			assert.Nil(t, service)
		})
	}
}
