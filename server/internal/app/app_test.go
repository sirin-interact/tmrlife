package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

var (
	firstKEK  = bytes.Repeat([]byte{0x2a}, 32)
	secondKEK = bytes.Repeat([]byte{0x07}, 32)
)

// testConfig는 환경 변수에서 읽는 것과 같은 길로 설정을 만든다. 시험용 DB를 가리킨다.
func testConfig(t *testing.T, overrides map[string]string) config.Config {
	t.Helper()
	pool := testdb.New(t)
	environ := map[string]string{
		"DATABASE_URL": pool.Config().ConnString(),
		"DATA_KEK_V1":  base64.StdEncoding.EncodeToString(firstKEK),
		// 설정이 받아 주는 가장 싼 값이다. 시험을 빨리 돌리려는 것이다.
		"PASSWORD_ARGON2_MEMORY_KIB": "7168",
		"PASSWORD_ARGON2_TIME":       "1",
	}
	for name, value := range overrides {
		environ[name] = value
	}
	cfg, err := config.LoadFrom(environ)
	require.NoError(t, err)
	return cfg
}

func newDeps(t *testing.T, cfg config.Config, name string) *Deps {
	t.Helper()
	deps, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), name)
	require.NoError(t, err)
	t.Cleanup(deps.Close)
	return deps
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("설정에서 프로세스가 기대는 것을 모두 만든다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, nil), NameServer)

		assert.IsType(t, clock.Real{}, deps.Clock, "시계를 주지 않으면 실제 시계를 쓴다")
		require.NotNil(t, deps.Pool)
		require.NotNil(t, deps.Store)
		require.NotNil(t, deps.KeyRing)
		require.NotNil(t, deps.KeyCache)
		require.NotNil(t, deps.Sealers)
		require.NotNil(t, deps.Prompts)
		require.NotNil(t, deps.Sessions)
		assert.NotEmpty(t, deps.Prompts.Tasks(), "지시문은 뜰 때 모두 읽어 둔다")
		assert.Equal(t, 1, deps.KeyRing.ActiveVersion())
	})

	t.Run("넘겨받은 시계가 시각을 읽는 모든 것에 닿는다", func(t *testing.T) {
		t.Parallel()
		start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
		fake := clock.NewFake(start)
		deps, err := New(t.Context(), testConfig(t, nil), slog.New(slog.DiscardHandler), NameServer, WithClock(fake))
		require.NoError(t, err)
		t.Cleanup(deps.Close)
		assert.Same(t, fake, deps.Clock)

		service, err := deps.NewAuthService(t.Context())
		require.NoError(t, err)
		result, err := service.Signup(t.Context(), auth.SignupInput{
			Email:    "mina@example.com",
			Password: "오늘도 수고했어요, 내일 봐요",
			Consents: auth.CurrentConsents(),
		})
		require.NoError(t, err)
		assert.Equal(t, start, result.User.CreatedAt, "인증 서비스가 넘겨받은 시계를 쓴다")

		token := result.Session.Token.Reveal()
		_, err = deps.Sessions.Authenticate(t.Context(), token)
		require.NoError(t, err)

		// 세션 관리자는 만들어질 때 받은 시계를 계속 쓴다. 실제 시계를 쥐고 있었다면 가짜 시계를 옮겨도 세션이 끝나지 않는다.
		fake.Advance(deps.Config.Session.IdleLifetime + time.Second)
		_, err = deps.Sessions.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, auth.ErrSessionInvalid, "세션 관리자가 넘겨받은 시계를 쓴다")
	})

	t.Run("빈 시계는 받지 않는다", func(t *testing.T) {
		t.Parallel()
		_, err := New(t.Context(), testConfig(t, nil), slog.New(slog.DiscardHandler), NameServer, WithClock(nil))
		require.Error(t, err)
	})

	t.Run("DB 연결에 실행 파일의 이름을 남기고 연결 수에 한도를 건다", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{NameServer, NameWorker} {
			deps := newDeps(t, testConfig(t, nil), name)

			var app string
			require.NoError(t, deps.Pool.QueryRow(t.Context(), "SHOW application_name").Scan(&app))
			assert.Equal(t, name, app)
			assert.Equal(t, int32(10), deps.Pool.Config().MaxConns)
		}
	})

	t.Run("마스터 키가 여럿이면 모두 들고 있고, 활성 버전으로 새 키를 감싼다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, map[string]string{
			"DATA_KEK_V2":     base64.StdEncoding.EncodeToString(secondKEK),
			"DATA_KEK_ACTIVE": "2",
		}), NameServer)

		assert.Equal(t, []int{1, 2}, deps.KeyRing.Versions())
		userID := uuid.Must(uuid.NewV7())
		key, err := deps.KeyRing.NewUserKey(userID)
		require.NoError(t, err)
		assert.Equal(t, 2, key.KEKVersion)
	})

	t.Run("데이터 키 캐시는 설정한 수까지만 들고 있는다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, map[string]string{"DATA_KEY_CACHE_SIZE": "3"}), NameServer)

		for range 5 {
			userID := uuid.Must(uuid.NewV7())
			_, err := deps.KeyCache.Get(t.Context(), userID, func(context.Context) (*crypto.Sealer, error) {
				key, err := deps.KeyRing.NewUserKey(userID)
				if err != nil {
					return nil, err
				}
				return key.Sealer, nil
			})
			require.NoError(t, err)
		}
		assert.Equal(t, 3, deps.KeyCache.Len())
	})

	t.Run("DB가 내려가 있어도 만드는 데서는 실패하지 않는다", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t, map[string]string{
			"DATABASE_URL": "postgres://naeil:naeil@127.0.0.1:1/naeil?sslmode=disable",
		})
		deps := newDeps(t, cfg, NameServer)
		assert.Error(t, deps.Pool.Ping(t.Context()), "DB에 닿는지는 띄우는 쪽이 확인한다")
	})
}

func TestNew_FailsFast(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name   string
		mutate func(cfg *config.Config)
		want   string
	}{
		{"마스터 키가 하나도 없으면", func(cfg *config.Config) { cfg.DataKeys.Keys = nil }, "load master keys"},
		{"활성 버전의 마스터 키가 없으면", func(cfg *config.Config) { cfg.DataKeys.Active = 9 }, "load master keys"},
		{"데이터 키 캐시의 크기가 0이면", func(cfg *config.Config) { cfg.DataKeyCache.Size = 0 }, "create data key cache"},
		{"DB 주소를 읽을 수 없으면", func(cfg *config.Config) {
			cfg.DatabaseURL = config.NewSecret("postgres://app:s3cr3t-pass@db.internal:notaport/app")
		}, "open database pool"},
		{"세션 수명이 틀렸으면", func(cfg *config.Config) { cfg.Session.IdleLifetime = 0 }, "create session manager"},
	}
	for _, tt := range tests {
		t.Run(tt.name+" 뜨지 않는다", func(t *testing.T) {
			t.Parallel()
			cfg := testConfig(t, nil)
			tt.mutate(&cfg)

			deps, err := New(t.Context(), cfg, logger, NameServer)
			require.Error(t, err)
			assert.Nil(t, deps)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "s3cr3t-pass")
		})
	}

	t.Run("로거 없이는 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		_, err := New(t.Context(), testConfig(t, nil), nil, NameServer)
		require.Error(t, err)
	})
}

func TestNewKeyRing_WipesRevealedKeys(t *testing.T) {
	t.Parallel()

	revealed := map[int][]byte{1: bytes.Clone(firstKEK), 2: bytes.Clone(secondKEK)}
	ring, err := keyRingFrom(2, revealed)
	require.NoError(t, err)

	for version, key := range revealed {
		assert.Equal(t, make([]byte, 32), key, "버전 %d의 복사본이 메모리에 남아 있다", version)
	}

	// 복사본을 지운 뒤에도 묶음은 제 키로 감싸고 푼다.
	userID := uuid.Must(uuid.NewV7())
	key, err := ring.NewUserKey(userID)
	require.NoError(t, err)
	_, err = ring.Unwrap(userID, key.Wrapped, key.KEKVersion)
	require.NoError(t, err)

	t.Run("묶음을 만들지 못했을 때도 지운다", func(t *testing.T) {
		t.Parallel()
		revealed := map[int][]byte{1: bytes.Clone(firstKEK)}
		_, err := keyRingFrom(5, revealed)
		require.Error(t, err)
		assert.Equal(t, make([]byte, 32), revealed[1])
	})

	t.Run("설정 안의 키는 그대로다", func(t *testing.T) {
		t.Parallel()
		keys := config.DataKeys{Active: 1, Keys: map[int]config.SecretBytes{1: config.NewSecretBytes(firstKEK)}}
		_, err := newKeyRing(keys)
		require.NoError(t, err)
		assert.Equal(t, firstKEK, keys.Keys[1].Reveal(), "지우는 것은 꺼낸 복사본이지 설정이 아니다")
	})
}

func TestNewAuthService(t *testing.T) {
	t.Parallel()
	deps := newDeps(t, testConfig(t, nil), NameServer)
	ctx := t.Context()

	service, err := deps.NewAuthService(ctx)
	require.NoError(t, err)

	t.Run("설정한 값으로 해시를 만들고, 가입부터 세션 확인까지 이어진다", func(t *testing.T) {
		result, err := service.Signup(ctx, auth.SignupInput{
			Email:    "mina@example.com",
			Password: "오늘도 수고했어요, 내일 봐요",
			Consents: auth.CurrentConsents(),
		})
		require.NoError(t, err)

		row, err := deps.Store.Queries().GetUserByID(ctx, result.User.ID)
		require.NoError(t, err)
		require.NotNil(t, row.PasswordHash)
		assert.Contains(t, *row.PasswordHash, "$m=7168,t=1,p=1$")

		// 세션은 같은 Deps의 세션 관리자로도 확인된다. 서버와 작업자가 같은 세션을 본다.
		principal, err := deps.Sessions.Authenticate(ctx, result.Session.Token.Reveal())
		require.NoError(t, err)
		assert.Equal(t, result.User.ID, principal.User.ID)

		// 가입하며 저장한 데이터 키는 같은 Deps의 마스터 키로 풀린다.
		keyRow, err := deps.Store.Queries().GetUserKey(ctx, result.User.ID)
		require.NoError(t, err)
		_, err = deps.KeyRing.Unwrap(result.User.ID, keyRow.WrappedDEK, int(keyRow.KEKVersion))
		require.NoError(t, err)

		// 글을 다루는 쪽이 받는 Sealer도 같은 키다. 같은 캐시를 거치므로 계정을 지울 때 한 곳만 잊으면 된다.
		sealer, err := deps.Sealers.For(ctx, result.User.ID)
		require.NoError(t, err)
		rowID := uuid.Must(uuid.NewV7())
		sealed, err := sealer.SealString("오늘의 일기", sealing.DiaryBody(rowID))
		require.NoError(t, err)
		assert.Equal(t, 1, deps.KeyCache.Len())

		deps.Sealers.Forget(result.User.ID)
		assert.Zero(t, deps.KeyCache.Len())
		again, err := deps.Sealers.For(ctx, result.User.ID)
		require.NoError(t, err)
		opened, err := again.OpenString(sealed, sealing.DiaryBody(rowID))
		require.NoError(t, err)
		assert.Equal(t, "오늘의 일기", opened)
	})

	t.Run("해시 설정이 틀렸으면 만들지 않는다", func(t *testing.T) {
		broken := *deps
		broken.Config.Password.HashConcurrency = 0
		_, err := broken.NewAuthService(ctx)
		require.Error(t, err)
	})
}

func TestNewHTTPHandler(t *testing.T) {
	t.Parallel()

	t.Run("설정의 값이 경로까지 이어진다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, map[string]string{
			"APP_ENV":                  "prod",
			"PUBLIC_ORIGIN":            "https://naeil.example",
			"SESSION_COOKIE_NAME":      "sid",
			"RATE_LIMIT_SIGNUP_PER_IP": "1/1h",
			"TRUSTED_PROXIES":          "10.42.0.0/16",
			// 운영에서는 실제 모델을 부르는 설정만 받는다. 이 시험은 모델을 부르지 않으므로 키는 자리만 채운다.
			"GEMINI_API_KEY": "gm-test-key",
		}), NameServer)
		handler, err := deps.NewHTTPHandler(t.Context())
		require.NoError(t, err)

		signup := func(email, forwardedFor string) *httptest.ResponseRecorder {
			consents := make([]map[string]string, 0, len(auth.CurrentConsents()))
			for _, c := range auth.CurrentConsents() {
				consents = append(consents, map[string]string{"kind": c.Kind, "version": c.Version})
			}
			body, err := json.Marshal(map[string]any{"email": email, "password": "오늘도 수고했어요, 내일 봐요", "consents": consents})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", forwardedFor)
			req.RemoteAddr = "10.42.0.8:41000"
			rec := httptest.NewRecorder()
			handler.Echo.ServeHTTP(rec, req)
			return rec
		}

		rec := signup("mina@example.com", "203.0.113.5")
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "__Host-sid", cookies[0].Name, "운영에서는 쿠키 이름에 __Host-가 붙는다")
		assert.True(t, cookies[0].Secure)
		assert.Equal(t, int(deps.Config.Session.AbsoluteLifetime.Seconds()), cookies[0].MaxAge, "쿠키의 수명은 세션의 전체 수명과 같다")
		assert.NotEmpty(t, rec.Header().Get("Strict-Transport-Security"))

		assert.Equal(t, http.StatusTooManyRequests, signup("other@example.com", "203.0.113.5").Code, "설정한 시도 한도가 걸려 있다")
		assert.Equal(t, http.StatusCreated, signup("third@example.com", "203.0.113.6").Code, "믿는 프록시가 적어 준 주소로 센다")

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		ready := httptest.NewRecorder()
		handler.Echo.ServeHTTP(ready, req)
		assert.Equal(t, http.StatusOK, ready.Code, "상태 확인은 로그인 없이 열려 있다")
	})

	t.Run("해시 설정이 틀렸으면 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, nil), NameServer)
		broken := *deps
		broken.Config.Password.HashConcurrency = 0
		_, err := broken.NewHTTPHandler(t.Context())
		require.Error(t, err)
	})

	t.Run("경로의 설정이 틀렸으면 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, nil), NameServer)
		broken := *deps
		broken.Config.RateLimits.MaxKeys = 0
		_, err := broken.NewHTTPHandler(t.Context())
		require.Error(t, err)
	})
}

func TestWorkerOptions(t *testing.T) {
	t.Parallel()
	deps := newDeps(t, testConfig(t, nil), NameWorker)

	options, err := deps.WorkerOptions(t.Context())
	require.NoError(t, err)
	options.SoftStopTimeout = 5 * time.Second

	client, err := queue.NewWorkerClient(deps.Pool, slog.New(slog.DiscardHandler), options)
	require.NoError(t, err)

	completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
	defer cancelSub()

	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	require.NoError(t, client.Start(ctx), "등록한 작업 종류가 있어야 작업자가 뜬다")

	t.Run("작업자가 뜨면 주기 작업이 모두 한 번씩 돈다", func(t *testing.T) {
		// 뜰 때 도는 주기 작업이 둘이다. 끝나는 순서는 정해져 있지 않으므로 둘 다 볼 때까지 기다린다.
		waiting := map[string]bool{
			auth.SessionCleanupArgs{}.Kind(): true,
			engine.SweepArgs{}.Kind():        true,
		}
		deadline := time.After(30 * time.Second)
		for len(waiting) > 0 {
			select {
			case ev := <-completed:
				delete(waiting, ev.Job.Kind)
			case <-deadline:
				t.Fatalf("주기 작업이 돌지 않았다: %v", waiting)
			}
		}
	})

	stop()
	select {
	case <-client.Stopped():
	case <-time.After(15 * time.Second):
		t.Fatal("작업자가 내려가지 않았다")
	}
}
