package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v5"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
)

const (
	// pathPrefix 아래의 모든 경로가 이 패키지의 미들웨어를 거친다.
	pathPrefix = "/api"

	// DefaultBodyLimitBytes는 JSON 본문의 기본 상한이다. 지금 받는 본문은 모두 1 KiB 안팎이다.
	DefaultBodyLimitBytes int64 = 64 << 10

	// DefaultAuthTimeout은 가입과 로그인 한 번에 주는 시간이다. 해시 계산은 수십 밀리초면 끝나므로,
	// 이 시간을 넘겼다면 계산의 차례를 기다리는 요청이 그만큼 밀려 있다는 뜻이다.
	DefaultAuthTimeout = 10 * time.Second

	// DefaultRequestTimeout은 명세의 경로로 온 요청 하나에 주는 시간이다. 세션을 읽는 데 쓴 시간까지 포함한다.
	// 가입과 로그인의 기한보다 길게 잡아서, 해시의 차례를 기다리다 끝난 요청이 제 이유(몰려 있다)로 끝나게 한다.
	DefaultRequestTimeout = 15 * time.Second
)

// Options는 API가 기대는 것들이다.
type Options struct {
	Logger *slog.Logger
	Clock  clock.Clock
	// DB는 /readyz가 확인할 대상이다.
	DB httpserver.Pinger
	// Production이면 HSTS 헤더를 붙인다.
	Production bool

	Auth     AuthService
	Settings SettingsReader

	Cookie CookieConfig
	// PublicOrigin은 웹앱이 열리는 출처다. 다른 출처에서 온 상태 변경 요청을 막는 데 쓴다.
	PublicOrigin string
	// TrustedProxies는 X-Forwarded-For를 믿어도 되는 앞단 프록시의 주소 범위다. 비어 있으면 헤더를 보지 않는다.
	TrustedProxies []netip.Prefix
	RateLimits     RateLimits

	// BodyLimitBytes가 0이면 DefaultBodyLimitBytes를 쓴다.
	BodyLimitBytes int64
	// AuthTimeout이 0이면 DefaultAuthTimeout을 쓴다.
	AuthTimeout time.Duration
	// RequestTimeout이 0이면 DefaultRequestTimeout을 쓴다.
	RequestTimeout time.Duration
}

// New는 공통 미들웨어, 상태 확인, /api 경로가 모두 붙은 Echo 인스턴스를 만든다.
// 서버와 시험이 같은 길로 만들어야 시험이 본 것이 운영에서 도는 것과 같다.
func New(opts Options) (*echo.Echo, error) {
	if opts.Logger == nil {
		return nil, errors.New("api: logger is required")
	}
	e := httpserver.New(httpserver.Options{
		Logger:         opts.Logger,
		DB:             opts.DB,
		Production:     opts.Production,
		ErrorHandler:   NewErrorHandler(opts.Logger),
		AccessLogAttrs: AccessLogAttrs,
	})
	if _, err := Register(e, opts); err != nil {
		return nil, err
	}
	return e, nil
}

// Register는 /api 아래의 미들웨어와 경로를 e에 붙인다. /healthz와 /readyz는 /api 밖에 있어서 이 미들웨어를 거치지 않는다.
//
// 미들웨어의 순서가 뜻을 가진다. 바깥부터:
//
//	캐시 금지 → 다른 출처 막기 → 클라이언트 주소 → 본문 한도 → 요청 기한 → 세션 읽기 → 명세 검증(로그인 확인 포함) → 로그인 확인 → 시도 한도 → 핸들러
//
// 싸게 거부할 수 있는 것을 앞에 둔다. 다른 출처의 요청과 너무 큰 본문은 DB를 보기 전에 끝난다.
// 요청 기한은 DB를 처음 보는 세션 읽기 바로 앞에서 시작한다. 본문을 느리게 보내는 요청은 서버의 읽기 제한 시간이 이미 막는다.
// 시도 한도는 본문의 이메일을 봐야 해서 검증 뒤에 온다.
//
// 돌려주는 그룹에는 명세에 적을 수 없는 경로(WebSocket)를 붙인다. 그런 경로도 세션 읽기까지의 미들웨어를 똑같이 거친다.
// 명세 검증과 그에 딸린 로그인 확인, 요청 기한은 거치지 않으므로, 로그인이 필요하면 RequireAuth를 직접 붙인다.
//
// 그런 경로를 e에 바로 붙이지 않는다. 이 그룹은 /api 아래에만 걸려 있어서, 바깥에 붙인 경로(예: /ws)는
// 다른 출처 막기, 클라이언트 주소, 세션 읽기를 하나도 거치지 않는다. 연결을 여는 요청은 GET이라서 눈에 띄는 오류 없이 열리고,
// 같은 사이트의 다른 하위 도메인이 사용자의 세션으로 대화 소켓을 열 수 있게 된다. 소켓도 /api 아래(이 그룹)에 둔다.
func Register(e *echo.Echo, opts Options) (*echo.Group, error) {
	switch {
	case opts.Logger == nil:
		return nil, errors.New("api: logger is required")
	case opts.Clock == nil:
		return nil, errors.New("api: clock is required")
	case opts.Auth == nil:
		return nil, errors.New("api: auth service is required")
	case opts.Settings == nil:
		return nil, errors.New("api: settings reader is required")
	}
	bodyLimit := opts.BodyLimitBytes
	if bodyLimit <= 0 {
		bodyLimit = DefaultBodyLimitBytes
	}
	authTimeout := opts.AuthTimeout
	if authTimeout <= 0 {
		authTimeout = DefaultAuthTimeout
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}

	cookies, err := newSessionCookies(opts.Cookie)
	if err != nil {
		return nil, err
	}
	crossOrigin, err := crossOriginProtection(opts.PublicOrigin)
	if err != nil {
		return nil, err
	}
	limiter, err := newAuthRateLimiter(opts.Clock, opts.Logger, opts.RateLimits, opts.Auth.CheckSignup)
	if err != nil {
		return nil, fmt.Errorf("api: rate limits: %w", err)
	}

	spec, err := GetSpec()
	if err != nil {
		return nil, fmt.Errorf("api: load the embedded spec: %w", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("api: the embedded spec is invalid: %w", err)
	}
	// 명세에서 온 경로의 목록이다. 아래에서 경로를 등록하면서 채운다. 요청을 받기 시작한 뒤로는 읽기만 한다.
	specRoutes := make(map[string]struct{})
	// 명세 검증과 요청 기한은 명세의 경로에만 건다. 명세에 적을 수 없는 경로(WebSocket)는 검증기가 404로 막아 버리고,
	// 오래 열려 있어야 해서 기한도 받으면 안 된다.
	inSpec := func(c *echo.Context) bool {
		if _, ok := specRoutes[routeKey(c.Request().Method, c.Path())]; ok {
			return true
		}
		// 라우터가 어느 경로에도 맞추지 못한 요청도 같이 다룬다. 그룹에 미들웨어가 있으면 라우터는 메서드만 틀린 요청에도
		// 404를 내는데, 검증기는 명세를 보고 404(없는 경로)와 405(받지 않는 메서드)를 가려 준다.
		// 그런 요청도 쿠키가 있으면 세션을 읽으므로 기한이 필요하다.
		return c.RouteInfo().Method == echo.RouteNotFound
	}
	validator, err := validateRequests(spec, inSpec)
	if err != nil {
		return nil, err
	}

	group := e.Group(pathPrefix,
		noStore(),
		crossOrigin,
		clientInfo(newClientIPResolver(opts.TrustedProxies), opts.Logger),
		requireJSONBody(bodyLimit),
		requestDeadline(requestTimeout, inSpec),
		loadSession(opts.Auth, cookies),
		validator,
	)

	handler := NewStrictHandler(
		&handlers{auth: opts.Auth, settings: opts.Settings, cookies: cookies, authTimeout: authTimeout},
		[]StrictMiddlewareFunc{limiter.middleware},
	)
	if err := registerOperations(group, handler, protectedOperations(spec), specRoutes); err != nil {
		return nil, err
	}
	if operations := countOperations(spec); len(specRoutes) != operations {
		// 검증을 건너뛰는 경로가 생긴다는 뜻이다. 조용히 넘어가지 않는다.
		return nil, fmt.Errorf("api: the spec has %d operations but %d routes were registered", operations, len(specRoutes))
	}
	return group, nil
}

func routeKey(method, path string) string {
	return method + " " + path
}

func countOperations(spec *openapi3.T) int {
	n := 0
	for _, item := range spec.Paths.Map() {
		n += len(item.Operations())
	}
	return n
}

// registerOperations는 명세의 작업을 그룹에 등록하고, protected에 든 작업에는 RequireAuth를 붙인다.
//
// 만들어진 코드는 작업의 이름으로 미들웨어를 찾는데, 이름이 어긋나면 아무 말 없이 미들웨어 없이 등록한다.
// 그러면 로그인 확인이 빠진 채로 경로가 열린다. 그래서 붙인 수를 세어, 붙어야 할 수와 다르면 서버를 띄우지 않는다.
//
// 등록한 경로는 registered에 적는다. 명세 검증은 여기에 적힌 경로에만 건다.
func registerOperations(group *echo.Group, handler ServerInterface, protected []string, registered map[string]struct{}) error {
	operationMiddlewares := make(map[string][]echo.MiddlewareFunc, len(protected))
	for _, id := range protected {
		operationMiddlewares[id] = []echo.MiddlewareFunc{RequireAuth()}
	}

	router := &groupRouter{group: group, prefix: pathPrefix, registered: registered}
	RegisterHandlersWithOptions(router, handler, RegisterHandlersOptions{OperationMiddlewares: operationMiddlewares})
	if router.err != nil {
		return router.err
	}
	if router.guarded != len(operationMiddlewares) {
		return fmt.Errorf("api: %d operations require a login but %d routes were registered with the check",
			len(operationMiddlewares), router.guarded)
	}
	return nil
}

// groupRouter는 만들어진 코드가 경로를 등록할 때 쓰는 라우터다.
//
// 만들어진 코드는 명세에 적힌 전체 경로(/api/v1/...)로 등록한다. 그런데 미들웨어를 묶어 둔 그룹은 자기 앞머리(/api)를
// 경로 앞에 붙이므로, 그대로 넘기면 /api/api/v1/...이 된다. 그래서 앞머리를 떼고 그룹에 넘긴다.
// 명세에 전체 경로를 적는 쪽을 고른 것은 문서, 웹의 타입, 검증기가 모두 같은 경로를 보게 하려는 것이다.
type groupRouter struct {
	group  *echo.Group
	prefix string
	// err는 앞머리 밖의 경로가 명세에 있었다는 뜻이다. 그런 경로는 미들웨어 없이 열리게 되므로 등록하지 않는다.
	err error
	// guarded는 작업별 미들웨어(로그인 확인)가 붙은 채로 등록된 경로의 수다.
	guarded int
	// registered에는 등록한 경로를 적는다. nil이면 적지 않는다.
	registered map[string]struct{}
}

var _ EchoRouter = (*groupRouter)(nil)

func (r *groupRouter) add(method, path string, h echo.HandlerFunc, m []echo.MiddlewareFunc) echo.RouteInfo {
	rest, ok := strings.CutPrefix(path, r.prefix+"/")
	if !ok {
		r.err = errors.Join(r.err, fmt.Errorf("api: path %s %s in the spec is outside of %s", method, path, r.prefix))
		return echo.RouteInfo{}
	}
	// 라우터가 경로를 받아 주지 않으면 Add는 패닉한다. 오류로 받아서 서버가 뜰 때 알려준다.
	info, err := r.group.AddRoute(echo.Route{Method: method, Path: "/" + rest, Handler: h, Middlewares: m})
	if err != nil {
		r.err = errors.Join(r.err, fmt.Errorf("api: register %s %s: %w", method, path, err))
		return info
	}
	if len(m) > 0 {
		r.guarded++
	}
	if r.registered != nil {
		r.registered[routeKey(method, path)] = struct{}{}
	}
	return info
}

func (r *groupRouter) CONNECT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("CONNECT", path, h, m)
}

func (r *groupRouter) DELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("DELETE", path, h, m)
}

func (r *groupRouter) GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("GET", path, h, m)
}

func (r *groupRouter) HEAD(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("HEAD", path, h, m)
}

func (r *groupRouter) OPTIONS(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("OPTIONS", path, h, m)
}

func (r *groupRouter) PATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("PATCH", path, h, m)
}

func (r *groupRouter) POST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("POST", path, h, m)
}

func (r *groupRouter) PUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("PUT", path, h, m)
}

func (r *groupRouter) TRACE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo {
	return r.add("TRACE", path, h, m)
}
