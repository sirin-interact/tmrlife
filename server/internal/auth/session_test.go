package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

func TestSessions_IdleExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		after time.Duration
		valid bool
	}{
		{"만든 직후에는 통한다", 0, true},
		{"13일 동안 쓰지 않아도 통한다", 13 * day, true},
		{"쉬는 시간의 한도가 되기 직전까지 통한다", 14*day - time.Microsecond, true},
		{"14일 동안 쓰지 않으면 그 순간부터 통하지 않는다", 14 * day, false},
		{"그 뒤로도 통하지 않는다", 20 * day, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			issued := f.signup(t, "mina@example.com").Session

			f.clock.Advance(tt.after)
			principal, err := f.service.Authenticate(t.Context(), issued.Token.Reveal())
			if !tt.valid {
				require.ErrorIs(t, err, ErrSessionInvalid)
				assert.Equal(t, Principal{}, principal)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, issued.ID, principal.Session.ID)
		})
	}

	t.Run("쓰는 동안에는 쉬는 시간의 한도가 뒤로 밀린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		issued := f.signup(t, "mina@example.com").Session

		// 10일마다 한 번씩 쓴다. 한 번도 14일을 쉬지 않았으므로 20일째에도 통한다.
		for _, elapsed := range []time.Duration{10 * day, 20 * day} {
			f.clock.Set(baseTime.Add(elapsed))
			principal, err := f.service.Authenticate(t.Context(), issued.Token.Reveal())
			require.NoError(t, err, "%s째", elapsed)
			assert.True(t, principal.Renewed)
		}
		// 마지막으로 쓴 20일째에서 14일이 아니라, 로그인한 때에서 30일이 되는 날에 끝난다.
		row := f.sessionRow(t, issued.ID)
		assert.True(t, baseTime.Add(30*day).Equal(row.expiresAt))
	})
}

func TestSessions_AbsoluteExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	issued := f.signup(t, "mina@example.com").Session
	token := issued.Token.Reveal()

	steps := []struct {
		name        string
		elapsed     time.Duration
		valid       bool
		wantExpires time.Duration
	}{
		{"13일째: 통하고, 만료가 27일째로 밀린다", 13 * day, true, 27 * day},
		{"26일째: 통하지만, 만료는 전체 수명인 30일째를 넘지 못한다", 26 * day, true, 30 * day},
		{"30일이 되기 직전: 아직 통한다", 30*day - time.Microsecond, true, 30 * day},
		{"30일째: 계속 써 왔어도 통하지 않는다", 30 * day, false, 0},
		{"31일째: 여전히 통하지 않는다", 31 * day, false, 0},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			f.clock.Set(baseTime.Add(step.elapsed))
			principal, err := f.service.Authenticate(ctx, token)
			if !step.valid {
				require.ErrorIs(t, err, ErrSessionInvalid)
				return
			}
			require.NoError(t, err)
			want := baseTime.Add(step.wantExpires)
			assert.True(t, want.Equal(principal.Session.ExpiresAt), "want %s, got %s", want, principal.Session.ExpiresAt)
			assert.True(t, baseTime.Add(30*day).Equal(principal.Session.AbsoluteExpiresAt))
			assert.True(t, want.Equal(f.sessionRow(t, issued.ID).expiresAt), "DB에 적힌 만료도 같아야 한다")
		})
	}
}

func TestSessions_TouchThrottling(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	issued := f.signup(t, "mina@example.com").Session
	token := issued.Token.Reveal()

	steps := []struct {
		name         string
		elapsed      time.Duration
		wantRenewed  bool
		wantLastSeen time.Duration
	}{
		{"1분 뒤의 요청은 DB에 쓰지 않는다", time.Minute, false, 0},
		{"5분이 되기 직전의 요청도 쓰지 않는다", 5*time.Minute - time.Microsecond, false, 0},
		{"5분째의 요청은 마지막으로 쓴 시각을 다시 적는다", 5 * time.Minute, true, 5 * time.Minute},
		{"다시 적은 직후의 요청은 쓰지 않는다", 6 * time.Minute, false, 5 * time.Minute},
		{"거기서 5분이 되기 전까지는 쓰지 않는다", 9 * time.Minute, false, 5 * time.Minute},
		{"거기서 5분이 지나면 다시 적는다", 10 * time.Minute, true, 10 * time.Minute},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			f.clock.Set(baseTime.Add(step.elapsed))
			principal, err := f.service.Authenticate(ctx, token)
			require.NoError(t, err)
			assert.Equal(t, step.wantRenewed, principal.Renewed)

			wantLastSeen := baseTime.Add(step.wantLastSeen)
			row := f.sessionRow(t, issued.ID)
			assert.True(t, wantLastSeen.Equal(row.lastSeenAt), "DB: want %s, got %s", wantLastSeen, row.lastSeenAt)
			assert.True(t, wantLastSeen.Add(14*day).Equal(row.expiresAt), "만료는 마지막으로 적은 시각에서 14일 뒤다")
			assert.True(t, wantLastSeen.Equal(principal.Session.LastSeenAt), "돌려준 값도 DB와 같아야 한다")
			assert.True(t, row.expiresAt.Equal(principal.Session.ExpiresAt))
		})
	}

	t.Run("요청이 몰려도 간격 안에서는 한 번도 쓰지 않는다", func(t *testing.T) {
		f.clock.Set(baseTime.Add(11 * time.Minute))
		before := f.sessionRow(t, issued.ID)
		for range 50 {
			principal, err := f.service.Authenticate(ctx, token)
			require.NoError(t, err)
			assert.False(t, principal.Renewed)
		}
		assert.Equal(t, before, f.sessionRow(t, issued.ID))
	})

	t.Run("시계가 뒤로 가도 마지막으로 쓴 시각을 앞당기지 않는다", func(t *testing.T) {
		f.clock.Set(baseTime.Add(2 * time.Minute))
		before := f.sessionRow(t, issued.ID)
		principal, err := f.service.Authenticate(ctx, token)
		require.NoError(t, err)
		assert.False(t, principal.Renewed)
		assert.Equal(t, before, f.sessionRow(t, issued.ID))
	})
}

func TestSessions_ShorterLifetimesApplyToExistingSessions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	clk := clock.NewFake(baseTime)
	ctx := t.Context()

	// 넉넉한 수명으로 세션을 만든 뒤, 수명을 줄여 서버를 다시 띄웠다.
	generous := newFixtureOn(t, pool, clk, cheapParams, testSessionConfig)
	token := generous.signup(t, "mina@example.com").Session.Token.Reveal()
	strict := newFixtureOn(t, pool, clk, cheapParams, SessionConfig{
		AbsoluteLifetime: 7 * day, IdleLifetime: day, TouchInterval: 5 * time.Minute,
	})

	t.Run("저장된 만료가 남아 있어도 새 쉬는 시간의 한도를 따른다", func(t *testing.T) {
		clk.Set(baseTime.Add(2 * day))
		_, err := generous.service.Authenticate(ctx, token)
		require.NoError(t, err, "옛 설정이라면 아직 통했을 세션이다")

		// 위의 확인이 마지막으로 쓴 시각을 2일째로 옮겼다. 거기서 하루를 더 쉰다.
		clk.Set(baseTime.Add(3 * day))
		_, err = strict.service.Authenticate(ctx, token)
		require.ErrorIs(t, err, ErrSessionInvalid)
	})

	t.Run("새 전체 수명도 따른다", func(t *testing.T) {
		second := generous.login(t, "mina@example.com").Session
		created := clk.Now()
		for elapsed := 12 * time.Hour; elapsed < 7*day; elapsed += 12 * time.Hour {
			clk.Set(created.Add(elapsed))
			_, err := strict.service.Authenticate(ctx, second.Token.Reveal())
			require.NoError(t, err)
		}
		clk.Set(created.Add(7 * day))
		_, err := strict.service.Authenticate(ctx, second.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid)
	})
}

func TestSessions_Rotation(t *testing.T) {
	t.Parallel()

	t.Run("로그인하면서 들고 온 세션은 끊기고 토큰이 바뀐다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()
		old := f.signup(t, "mina@example.com")

		result, err := f.service.Login(ctx, LoginInput{
			Email: "mina@example.com", Password: testPassword, PresentedToken: old.Session.Token.Reveal(),
		})
		require.NoError(t, err)
		assert.NotEqual(t, old.Session.Token.Reveal(), result.Session.Token.Reveal())

		_, err = f.service.Authenticate(ctx, old.Session.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid, "들고 온 토큰은 더는 통하지 않는다")
		_, err = f.service.Authenticate(ctx, result.Session.Token.Reveal())
		require.NoError(t, err)
		assert.Equal(t, 1, f.countFor(t, "sessions", old.User.ID))
	})

	t.Run("다른 사람의 세션을 들고 와서 로그인해도 그 세션은 끊긴다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()
		// 같은 브라우저를 미나가 쓰다가 준이 로그인한다. 쿠키는 준의 토큰으로 덮인다.
		mina := f.signup(t, "mina@example.com")
		f.signup(t, "joon@example.com")

		result, err := f.service.Login(ctx, LoginInput{
			Email: "joon@example.com", Password: testPassword, PresentedToken: mina.Session.Token.Reveal(),
		})
		require.NoError(t, err)
		assert.Equal(t, "joon@example.com", result.User.Email)

		_, err = f.service.Authenticate(ctx, mina.Session.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid)
	})

	t.Run("가입하면서 들고 온 세션도 끊긴다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()
		mina := f.signup(t, "mina@example.com")

		in := signupInput("joon@example.com")
		in.PresentedToken = mina.Session.Token.Reveal()
		_, err := f.service.Signup(ctx, in)
		require.NoError(t, err)

		_, err = f.service.Authenticate(ctx, mina.Session.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid)
	})

	t.Run("로그인에 실패하면 들고 온 세션은 그대로다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()
		old := f.signup(t, "mina@example.com")

		_, err := f.service.Login(ctx, LoginInput{
			Email: "mina@example.com", Password: "틀린 비밀번호입니다 정말로", PresentedToken: old.Session.Token.Reveal(),
		})
		require.ErrorIs(t, err, ErrInvalidCredentials)
		_, err = f.service.Authenticate(ctx, old.Session.Token.Reveal())
		require.NoError(t, err)
	})

	t.Run("토큰의 꼴이 아닌 쿠키가 딸려 와도 로그인은 된다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.signup(t, "mina@example.com")

		for _, garbage := range []string{"garbage", strings.Repeat("A", 43) + "=", strings.Repeat("x", 100_000)} {
			_, err := f.service.Login(t.Context(), LoginInput{
				Email: "mina@example.com", Password: testPassword, PresentedToken: garbage,
			})
			require.NoError(t, err)
		}
	})
}

func TestSessions_Logout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	phone := f.signup(t, "mina@example.com")
	tablet := f.login(t, "mina@example.com")

	t.Run("로그아웃한 세션은 통하지 않고 다른 기기의 세션은 그대로다", func(t *testing.T) {
		require.NoError(t, f.service.Logout(ctx, phone.Session.Token.Reveal()))

		_, err := f.service.Authenticate(ctx, phone.Session.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid)
		_, err = f.service.Authenticate(ctx, tablet.Session.Token.Reveal())
		require.NoError(t, err)
		assert.Equal(t, 1, f.countFor(t, "sessions", phone.User.ID), "행까지 지운다")
	})

	t.Run("이미 로그아웃한 세션으로 다시 로그아웃해도 오류가 아니다", func(t *testing.T) {
		require.NoError(t, f.service.Logout(ctx, phone.Session.Token.Reveal()))
	})

	t.Run("토큰의 꼴이 아닌 값으로 로그아웃해도 오류가 아니다", func(t *testing.T) {
		for _, garbage := range []string{"", "garbage", strings.Repeat("x", 100_000)} {
			require.NoError(t, f.service.Logout(ctx, garbage))
		}
		_, err := f.service.Authenticate(ctx, tablet.Session.Token.Reveal())
		require.NoError(t, err)
	})
}

func TestSessions_LogoutAll(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	mina := f.signup(t, "mina@example.com")
	minaTokens := []string{
		mina.Session.Token.Reveal(),
		f.login(t, "mina@example.com").Session.Token.Reveal(),
		f.login(t, "mina@example.com").Session.Token.Reveal(),
	}
	joon := f.signup(t, "joon@example.com")

	t.Run("그 사용자의 세션을 모두 끊고 몇 개였는지 돌려준다", func(t *testing.T) {
		closed, err := f.service.LogoutAll(ctx, mina.User.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(3), closed)

		for _, token := range minaTokens {
			_, err := f.service.Authenticate(ctx, token)
			require.ErrorIs(t, err, ErrSessionInvalid)
		}
		assert.Zero(t, f.countFor(t, "sessions", mina.User.ID))
	})

	t.Run("다른 사용자의 세션은 그대로다", func(t *testing.T) {
		_, err := f.service.Authenticate(ctx, joon.Session.Token.Reveal())
		require.NoError(t, err)
	})

	t.Run("끊을 세션이 없어도 오류가 아니다", func(t *testing.T) {
		closed, err := f.service.LogoutAll(ctx, mina.User.ID)
		require.NoError(t, err)
		assert.Zero(t, closed)
	})

	t.Run("다시 로그인하면 새 세션으로 들어온다", func(t *testing.T) {
		again := f.login(t, "mina@example.com")
		_, err := f.service.Authenticate(ctx, again.Session.Token.Reveal())
		require.NoError(t, err)
	})
}

func TestSessions_Authenticate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	issued := f.signup(t, "mina@example.com")

	t.Run("토큰의 꼴이 아닌 값은 DB를 찾아보지도 않고 거절한다", func(t *testing.T) {
		for _, garbage := range []string{"", "garbage", issued.Session.Token.Reveal() + "A", strings.Repeat("x", 100_000)} {
			_, err := f.service.Authenticate(ctx, garbage)
			require.ErrorIs(t, err, ErrSessionInvalid)
		}
	})

	t.Run("꼴은 맞지만 발급한 적 없는 토큰은 통하지 않는다", func(t *testing.T) {
		_, err := f.service.Authenticate(ctx, strings.Repeat("A", 43))
		require.ErrorIs(t, err, ErrSessionInvalid)
	})

	t.Run("사용자를 지우면 세션도 함께 끊긴다", func(t *testing.T) {
		_, err := f.store.Queries().DeleteUser(ctx, issued.User.ID)
		require.NoError(t, err)
		_, err = f.service.Authenticate(ctx, issued.Session.Token.Reveal())
		require.ErrorIs(t, err, ErrSessionInvalid)
	})
}

func TestSessions_UserAgent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	f.signup(t, "mina@example.com")

	tests := []struct {
		name  string
		agent string
		want  *string
	}{
		{"255자까지는 그대로 저장한다", strings.Repeat("a", 255), ptr(strings.Repeat("a", 255))},
		{"255자를 넘으면 잘라서 저장한다", strings.Repeat("a", 300), ptr(strings.Repeat("a", 255))},
		{"바이트가 아니라 글자 수로 자른다", strings.Repeat("가", 300), ptr(strings.Repeat("가", 255))},
		{"글자로 읽을 수 없는 바이트와 NUL은 빼고 저장한다", "Mozilla\xff/5.0\x00 (test)", ptr("Mozilla/5.0 (test)")},
		{"비어 있으면 저장하지 않는다", "   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := f.service.Login(ctx, LoginInput{
				Email: "mina@example.com", Password: testPassword, Client: ClientInfo{UserAgent: tt.agent},
			})
			require.NoError(t, err, "헤더 하나로 로그인을 실패시킬 수 없어야 한다")

			var agent *string
			var hasIP bool
			err = f.pool.QueryRow(ctx, `SELECT user_agent, ip IS NOT NULL FROM sessions WHERE id = $1`, result.Session.ID).
				Scan(&agent, &hasIP)
			require.NoError(t, err)
			assert.Equal(t, tt.want, agent)
			assert.False(t, hasIP, "주소를 모르면 비워 둔다")
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestNewSessions_RejectsBadSettings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	tests := []struct {
		name string
		cfg  SessionConfig
	}{
		{"전체 수명이 0이다", SessionConfig{AbsoluteLifetime: 0, IdleLifetime: day, TouchInterval: time.Minute}},
		{"쉬는 시간의 한도가 0이다", SessionConfig{AbsoluteLifetime: day, IdleLifetime: 0, TouchInterval: time.Minute}},
		{"다시 적는 간격이 0이다", SessionConfig{AbsoluteLifetime: day, IdleLifetime: day, TouchInterval: 0}},
		{"쉬는 시간의 한도가 전체 수명보다 길다", SessionConfig{AbsoluteLifetime: day, IdleLifetime: 2 * day, TouchInterval: time.Minute}},
		{"다시 적는 간격이 쉬는 시간의 한도와 같다", SessionConfig{AbsoluteLifetime: day, IdleLifetime: time.Hour, TouchInterval: time.Hour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sessions, err := NewSessions(f.store, f.clock, tt.cfg)
			require.Error(t, err)
			assert.Nil(t, sessions)
		})
	}

	t.Run("저장소와 시계 없이는 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		_, err := NewSessions(nil, f.clock, testSessionConfig)
		require.Error(t, err)
		_, err = NewSessions(f.store, nil, testSessionConfig)
		require.Error(t, err)
	})
}

func TestPrincipal_LogsIdentifiersOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	issued := f.signup(t, "mina@example.com")
	principal, err := f.service.Authenticate(t.Context(), issued.Session.Token.Reveal())
	require.NoError(t, err)

	f.service.logger.Info("request authenticated", "principal", principal, "user", principal.User)
	logs := f.logs.String()
	assert.Contains(t, logs, principal.User.ID.String())
	assert.Contains(t, logs, principal.Session.ID.String())
	assert.NotContains(t, logs, "mina@example.com")
}
