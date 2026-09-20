// Package app은 설정에서 프로세스가 기대는 것들을 만들어 한데 묶는다.
//
// 서버와 작업자는 같은 것들에 기댄다. 시계, 접속 풀, 마스터 키 묶음, 데이터 키 캐시, 사용자별 Sealer, 지시문, 저장소다.
// 만드는 자리가 실행 파일마다 따로 있으면 한쪽에만 빠진 것이 생기고, 그 차이는 운영에서야 드러난다.
// 그래서 만드는 일은 여기 한 곳에서 하고, main은 받아서 띄우기만 한다.
//
// 여기서 만드는 것은 프로세스에 하나씩만 있어야 하는 것들이다. 특히 데이터 키 캐시가 둘이면
// 계정을 지울 때 한쪽에서만 키를 잊게 된다.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/riverqueue/river"

	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/api"
	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/pgdb"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 실행 파일의 이름이다. 로그의 app 속성과 DB 연결의 application_name에 똑같이 쓴다.
const (
	NameServer  = "naeil-server"
	NameWorker  = "naeil-worker"
	NameMigrate = "naeil-migrate"
)

// Deps는 프로세스 하나가 기대는 것들이다. 필드는 모두 채워져 있고, 만든 뒤에는 바꾸지 않는다.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
	// Clock은 New에서만 고른다(WithClock). 데이터 키 캐시와 세션 관리자는 만들어질 때 받은 시계를 계속 쓰므로,
	// 만든 뒤에 이 필드만 바꾸면 인증 서비스와 시도 한도는 새 시계를, 세션과 캐시는 옛 시계를 보는 어긋난 상태가 된다.
	Clock clock.Clock
	// Pool은 Close가 닫는다.
	Pool  *pgxpool.Pool
	Store *store.Store
	// KeyRing은 사용자별 데이터 키를 감싸고 푸는 마스터 키 묶음이다.
	KeyRing *crypto.KeyRing
	// KeyCache는 풀어 둔 데이터 키를 들고 있는다. 프로세스에 하나뿐이어야 한다.
	KeyCache *crypto.KeyCache
	// Sealers는 사용자의 글을 잠그고 여는 쪽(대화, 일기 초안 작업, 일기 API)에 그 사용자의 Sealer를 내준다.
	// 위의 묶음과 캐시를 그대로 쓴다. 글을 다루는 쪽은 묶음과 캐시를 직접 만지지 않고 이것만 받는다.
	Sealers  *sealing.Sealers
	Prompts  *prompts.Registry
	Sessions *auth.Sessions
}

// Option은 New가 만드는 것을 바꾼다.
type Option func(*options)

type options struct {
	clock clock.Clock
}

// WithClock은 실제 시계 대신 쓸 시계를 준다.
//
// 가짜 시계로 몇 주를 몇 분에 돌려 보는 실행이 서버와 같은 길로 만들어진 것들 위에서 돌게 하려는 것이다.
// 그런 실행이 제 손으로 하나하나 만들어 쓰면, 서버에는 있는데 거기에는 빠진 것이 생기고 돌려 본 결과가 운영을 말해 주지 못한다.
// 서버와 작업자는 이 옵션을 주지 않는다.
func WithClock(c clock.Clock) Option {
	return func(o *options) { o.clock = c }
}

// New는 설정에서 Deps를 만든다. name은 NameServer나 NameWorker다.
//
// DB에 닿는지는 확인하지 않는다. 풀은 처음 쓸 때 연결을 맺으므로, DB 없이는 뜰 수 없는 프로세스는 직접 확인한다.
// 그 밖의 것은 여기서 모두 확인한다. 마스터 키가 틀렸거나 지시문 파일이 빠졌으면 첫 요청이 아니라 뜰 때 실패한다.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, name string, opts ...Option) (*Deps, error) {
	if logger == nil {
		return nil, errors.New("app: logger is required")
	}
	// 시계는 여기서 한 번 정해서 시각을 읽는 모든 것에 같은 값을 넘긴다.
	o := options{clock: clock.Real{}}
	for _, apply := range opts {
		apply(&o)
	}
	if o.clock == nil {
		return nil, errors.New("app: clock must not be nil")
	}
	clk := o.clock

	ring, err := newKeyRing(cfg.DataKeys)
	if err != nil {
		return nil, fmt.Errorf("load master keys: %w", err)
	}

	cache, err := crypto.NewKeyCache(crypto.KeyCacheOptions{
		MaxEntries: cfg.DataKeyCache.Size,
		MaxAge:     cfg.DataKeyCache.MaxAge,
		Now:        clk.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("create data key cache: %w", err)
	}

	registry, err := prompts.LoadEmbedded()
	if err != nil {
		return nil, fmt.Errorf("load prompts: %w", err)
	}

	pool, err := pgdb.Open(ctx, cfg.DatabaseURL.Reveal(), name)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	st := store.New(pool)

	sealers, err := sealing.New(st.Queries(), ring, cache)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create sealers: %w", err)
	}

	sessions, err := auth.NewSessions(st, clk, auth.SessionConfig{
		AbsoluteLifetime: cfg.Session.AbsoluteLifetime,
		IdleLifetime:     cfg.Session.IdleLifetime,
		TouchInterval:    cfg.Session.TouchInterval,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create session manager: %w", err)
	}

	return &Deps{
		Config:   cfg,
		Logger:   logger,
		Clock:    clk,
		Pool:     pool,
		Store:    st,
		KeyRing:  ring,
		KeyCache: cache,
		Sealers:  sealers,
		Prompts:  registry,
		Sessions: sessions,
	}, nil
}

// Close는 접속 풀을 닫고 풀어 둔 데이터 키를 놓는다. 돌던 쿼리가 끝나기를 기다린다.
func (d *Deps) Close() {
	d.KeyCache.Purge()
	d.Pool.Close()
}

// NewAuthService는 가입과 로그인을 맡는 서비스를 만든다. 서버만 부른다.
//
// 비밀번호 해시를 한 번 계산해 두므로 그만큼의 시간과 메모리를 쓴다. 비밀번호를 다루지 않는 작업자가 치를 값이 아니다.
func (d *Deps) NewAuthService(ctx context.Context) (*auth.Service, error) {
	hasher, err := auth.NewHasher(ctx, auth.Argon2Params{
		MemoryKiB:   d.Config.Password.MemoryKiB,
		Time:        d.Config.Password.Time,
		Parallelism: d.Config.Password.Parallelism,
	}, d.Config.Password.HashConcurrency)
	if err != nil {
		return nil, fmt.Errorf("create password hasher: %w", err)
	}
	service, err := auth.NewService(auth.ServiceOptions{
		Store:    d.Store,
		Clock:    d.Clock,
		Logger:   d.Logger,
		Hasher:   hasher,
		Sessions: d.Sessions,
		KeyRing:  d.KeyRing,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth service: %w", err)
	}
	return service, nil
}

// NewHTTPHandler는 서버가 받는 모든 경로가 붙은 핸들러를 만든다. 서버만 부른다.
// 상태 확인(/healthz, /readyz)은 /api 밖에 있고 로그인을 거치지 않는다.
//
// 인증 서비스를 여기서 만들므로 비밀번호 해시 설정이 틀렸으면 첫 로그인이 아니라 뜰 때 실패한다.
func (d *Deps) NewHTTPHandler(ctx context.Context) (*echo.Echo, error) {
	authService, err := d.NewAuthService(ctx)
	if err != nil {
		return nil, err
	}
	cfg := d.Config
	// 미들웨어는 시작할 때의 ctx가 아니라 요청마다의 컨텍스트를 쓴다. 검사기가 그 차이를 알지 못한다.
	handler, err := api.New(api.Options{ //nolint:contextcheck // 요청 컨텍스트를 쓰는 것이 맞다.
		Logger:     d.Logger,
		Clock:      d.Clock,
		DB:         d.Pool,
		Production: cfg.Env.IsProd(),
		Auth:       authService,
		Settings:   d.Store.Queries(),
		Cookie: api.CookieConfig{
			Name:   cfg.Session.CookieName,
			Secure: cfg.Session.CookieSecure,
			// 쿠키가 세션보다 먼저 사라지면 멀쩡한 세션을 두고 다시 로그인해야 하고,
			// 더 오래 남으면 죽은 토큰을 계속 실어 보낸다. 세션의 전체 수명과 같게 맞춘다.
			MaxAge: cfg.Session.AbsoluteLifetime,
		},
		PublicOrigin:   cfg.PublicOrigin,
		TrustedProxies: cfg.TrustedProxies,
		RateLimits: api.RateLimits{
			SignupPerIP:        api.RateLimit(cfg.RateLimits.SignupPerIP),
			LoginPerIP:         api.RateLimit(cfg.RateLimits.LoginPerIP),
			LoginPerEmail:      api.RateLimit(cfg.RateLimits.LoginPerEmail),
			LoginPerEmailTotal: api.RateLimit(cfg.RateLimits.LoginPerEmailTotal),
			MaxKeys:            cfg.RateLimits.MaxKeys,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create http handler: %w", err)
	}
	return handler, nil
}

// WorkerOptions는 작업자가 맡는 작업 종류와 주기 작업을 모은다. 새 작업은 여기에 더한다.
// 언어 모델을 부르는 작업은 그 모델을 받았을 때만 등록된다(WithAnalysisModel).
func (d *Deps) WorkerOptions(ctx context.Context, opts ...WorkerOption) (queue.WorkerOptions, error) {
	var settings workerSettings
	for _, apply := range opts {
		apply(&settings)
	}

	sessionCleanup, err := auth.NewSessionCleanupWorker(d.Sessions, d.Logger)
	if err != nil {
		return queue.WorkerOptions{}, err
	}
	diaryDrafts, draftsDiaries, err := d.newDiaryWorker(settings)
	if err != nil {
		return queue.WorkerOptions{}, err
	}
	if !draftsDiaries {
		d.Logger.LogAttrs(ctx, slog.LevelWarn, "diary draft worker is not registered: no analysis model was given")
	}
	return queue.WorkerOptions{
		Register: func(workers *river.Workers) error {
			if err := river.AddWorkerSafely(workers, sessionCleanup); err != nil {
				return err
			}
			if !draftsDiaries {
				return nil
			}
			return river.AddWorkerSafely(workers, diaryDrafts)
		},
		PeriodicJobs: []*river.PeriodicJob{
			auth.SessionCleanupPeriodicJob(),
		},
	}, nil
}

// newKeyRing은 설정의 마스터 키로 묶음을 만든다.
// 설정에서 꺼낸 키는 복사본이다. 묶음이 키 일정을 만들고 나면 더는 필요 없으므로 바로 지운다.
// 지우지 않으면 같은 키가 메모리 여기저기에 남아, 메모리가 새어 나갔을 때 찾을 자리가 늘어난다.
func newKeyRing(keys config.DataKeys) (*crypto.KeyRing, error) {
	revealed := make(map[int][]byte, len(keys.Keys))
	for version, key := range keys.Keys {
		revealed[version] = key.Reveal()
	}
	return keyRingFrom(keys.Active, revealed)
}

// keyRingFrom은 묶음을 만들고, 성공하든 실패하든 넘겨받은 키 바이트를 0으로 덮는다.
func keyRingFrom(active int, revealed map[int][]byte) (*crypto.KeyRing, error) {
	defer func() {
		for _, key := range revealed {
			clear(key)
		}
	}()
	return crypto.NewKeyRing(active, revealed)
}
