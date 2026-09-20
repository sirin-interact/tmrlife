package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/labstack/echo/v5"
	echomiddleware "github.com/oapi-codegen/echo-v5-middleware"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
)

// noStore는 /api의 모든 응답이 브라우저나 중간 프록시에 남지 않게 한다. 응답에 개인의 기록이 담기기 때문이다.
// 바깥의 서버 설정에 같은 기본값이 있어도 여기서 한 번 더 건다. API의 약속이 바깥 설정에 기대지 않게 하려는 것이다.
func noStore() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
			return next(c)
		}
	}
}

// crossOriginProtection은 다른 출처의 페이지가 사용자의 브라우저를 시켜 보낸 상태 변경 요청을 거부한다.
//
// 사용자가 로그인한 채로 다른 사이트를 열면, 그 사이트는 이 서버로 요청을 보내게 할 수 있고 브라우저는 쿠키를 알아서 싣는다.
// 흔한 방어는 폼마다 토큰을 심는 것이지만, 여기서는 토큰 없이 세 겹으로 막는다.
//
//  1. 세션 쿠키가 SameSite=Lax다. 다른 사이트가 보내는 POST, PUT, PATCH, DELETE에는 브라우저가 쿠키를 싣지 않는다.
//     다만 "사이트"는 출처보다 넓다. 같은 도메인의 다른 하위 도메인은 같은 사이트라서 쿠키가 실린다.
//  2. 그 틈은 브라우저가 붙이는 Sec-Fetch-Site 헤더로 막는다. 이 헤더는 스크립트가 고칠 수 없고,
//     same-origin(같은 출처)이나 none(주소창에 직접 입력) 말고는 모두 거부한다. 표준 라이브러리의 CrossOriginProtection이 이 일을 한다.
//  3. 본문은 application/json만 받는다(requireJSONBody). HTML 폼으로는 이 타입을 보낼 수 없고, 스크립트로 보내려면
//     브라우저가 먼저 허락을 구하는 요청(preflight)을 보내는데 이 서버는 어느 출처에도 허락하지 않는다.
//
// 토큰이 막아 주는 것은 이 셋으로 모두 막힌다. 토큰은 화면마다 받아 와서 실어 보내야 하고, 빠뜨리면 멀쩡한 요청이 실패한다.
//
// Sec-Fetch-Site를 보내지 않는 옛 브라우저(2023년 이전)에서는 Origin 헤더를 요청의 Host와 견준다.
// 다르면 거부하되, 웹앱의 출처(PUBLIC_ORIGIN)는 믿는다. 앞단 프록시가 Host를 바꿔 전달하면
// 같은 출처의 요청인데도 둘이 어긋나기 때문이다(개발 서버가 /api를 8080으로 넘길 때가 그렇다).
// 두 헤더가 모두 없으면 브라우저가 아닌 클라이언트(curl, 서버 간 호출)로 보고 통과시킨다.
// 그런 클라이언트에는 몰래 실리는 쿠키가 없어서 이 공격이 성립하지 않는다. Origin조차 보내지 않는 아주 옛 브라우저도
// 이 경우에 들지만, 그때는 1번의 SameSite가 막는다.
//
// GET, HEAD, OPTIONS는 늘 통과한다. 그래서 이 메서드의 핸들러는 상태를 바꾸면 안 된다.
//
// WebSocket 연결을 여는 요청만은 GET이어도 상태를 바꾸는 요청과 같은 검사를 받는다. 열린 뒤에는 상태를 바꾸는 말이 오가고,
// 같은 사이트의 다른 하위 도메인이 열어도 브라우저는 세션 쿠키를 싣는다. 소켓 라이브러리도 출처를 보지만 그 검사는 옵션 하나로 꺼진다.
// 개발 환경을 맞추느라 꺼 둔 옵션이 운영까지 가더라도 여기서 막히게 한다.
func crossOriginProtection(publicOrigin string) (echo.MiddlewareFunc, error) {
	protection := http.NewCrossOriginProtection()
	if err := protection.AddTrustedOrigin(publicOrigin); err != nil {
		// 표준 라이브러리의 오류에는 받은 값이 그대로 들어 있다. 설정 값이라 새어 나가도 되지만 버릇을 들이지 않는다.
		return nil, errors.New("api: public origin is not a valid origin")
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if isWebSocketUpgrade(req) {
				// 검사는 메서드, 헤더, Host만 읽는다. 메서드만 바꾼 사본을 보여준다.
				probe := *req
				probe.Method = http.MethodPost
				req = &probe
			}
			if err := protection.Check(req); err != nil {
				return errCrossOrigin
			}
			return next(c)
		}
	}, nil
}

// isWebSocketUpgrade는 WebSocket 연결을 여는 요청인지 본다. 브라우저는 이 헤더를 스크립트가 고치지 못하게 막는다.
func isWebSocketUpgrade(req *http.Request) bool {
	return req.Method == http.MethodGet && strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket")
}

// clientInfo는 요청을 보낸 쪽의 주소와 브라우저 정보를 컨텍스트에 담는다. 시도 한도와 세션 기록이 쓴다.
//
// 믿는 프록시를 설정해 두었는데 가려낸 주소가 그 프록시의 범위 안이면, 프록시가 클라이언트의 주소를 전해 주지 않고 있다는 뜻이다
// (K3s의 기본 설정이 그렇다). 그러면 모든 사용자가 한 주소로 보여서 주소별 한도를 다 같이 나눠 쓰게 되는데,
// 한도에 걸린 로그에는 주소를 남기지 않으므로 몰려든 시도와 구분할 길이 없다. 그래서 처음 알아챘을 때 한 번 경고한다.
// 거꾸로 주소가 제대로 전해진 첫 요청에도 한 번 알린다. 배포한 뒤에 로그 한 줄로 어느 쪽인지 확인할 수 있다.
// 두 로그 모두 주소는 담지 않고, 프로세스가 도는 동안 한 번씩만 남는다. 프록시 설정을 고친 뒤에 다시 보려면 서버를 다시 띄운다.
func clientInfo(resolver clientIPResolver, logger *slog.Logger) echo.MiddlewareFunc {
	var collapsedLogged, forwardedLogged atomic.Bool
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			ip := resolver.resolve(req)

			if len(resolver.trusted) > 0 && ip.IsValid() {
				switch {
				case resolver.isTrusted(ip):
					if collapsedLogged.CompareAndSwap(false, true) {
						logger.LogAttrs(req.Context(), slog.LevelWarn,
							"client address is not forwarded by the proxy; per-address limits are shared by all users",
							slog.String("request_id", httpserver.RequestID(req.Context())),
						)
					}
				case !forwardedLogged.Load() && resolver.isTrusted(parseHostAddr(req.RemoteAddr)):
					if forwardedLogged.CompareAndSwap(false, true) {
						logger.LogAttrs(req.Context(), slog.LevelInfo, "client address forwarding works",
							slog.String("request_id", httpserver.RequestID(req.Context())),
						)
					}
				}
			}

			info := auth.ClientInfo{UserAgent: req.UserAgent(), IP: ip}
			c.SetRequest(req.WithContext(withClientInfo(req.Context(), info)))
			return next(c)
		}
	}
}

// requestDeadline은 요청에 끝나야 할 시각을 건다.
//
// 서버의 쓰기 제한 시간은 오래 열려 있는 연결(WebSocket) 때문에 꺼 두었다. 그래서 짧게 끝나야 하는 요청은 여기서 기한을 받는다.
// 기한이 없으면 DB가 멎었을 때 세션을 읽는 쿼리와 연결을 기다리는 줄이 클라이언트가 포기할 때까지 서 있고,
// 사용자는 "잠시 뒤에 다시 시도하라"는 답 대신 끝나지 않는 대기 화면을 본다. 기한을 넘긴 요청은 503과 Retry-After를 받는다.
//
// 세션 읽기보다 앞에 둔다. 가입과 로그인이 따로 받는 더 짧은 기한은 핸들러 안에서 시작하므로, 쿠키가 딸려 온 요청은
// 세션을 읽다가 멎으면 그 기한에 닿지도 못한다.
//
// applies가 false인 요청(명세에 적을 수 없는, 오래 열려 있는 경로)에는 걸지 않는다.
func requestDeadline(timeout time.Duration, applies func(c *echo.Context) bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !applies(c) {
				return next(c)
			}
			req := c.Request()
			ctx, cancel := context.WithTimeout(req.Context(), timeout)
			defer cancel()
			c.SetRequest(req.WithContext(ctx))
			return next(c)
		}
	}
}

// requireJSONBody는 본문이 있으면 JSON인지, 한도 안인지 보고, 본문을 끝까지 읽어 메모리에 올려 둔다.
//
// 미리 읽어 두는 까닭: 뒤따르는 검증기와 핸들러가 본문을 한 번씩 읽는다. 읽는 도중에 한도를 넘거나 연결이 끊기면
// 그 오류가 검증기 안에서 "본문이 명세와 다르다"는 400으로 뭉개진다. 여기서 먼저 읽으면 413과 400을 정확히 가를 수 있고,
// 느리게 보내는 본문을 기다리는 일도 이 한 곳에서 끝난다. 한도가 작아서 메모리에 올려도 부담이 없다.
func requireJSONBody(limit int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if req.Body == nil || req.Body == http.NoBody || req.ContentLength == 0 {
				return next(c)
			}

			mediaType, _, err := mime.ParseMediaType(req.Header.Get(echo.HeaderContentType))
			if err != nil || mediaType != echo.MIMEApplicationJSON {
				return errNotJSON
			}
			if req.ContentLength > limit {
				return errBodyTooLarge
			}

			body, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
			if err != nil {
				if echo.StatusCode(err) == http.StatusRequestEntityTooLarge {
					return errBodyTooLarge
				}
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					return errBodyTooLarge
				}
				// 보내다가 연결이 끊겼다.
				return errUnreadableBody
			}
			if int64(len(body)) > limit {
				return errBodyTooLarge
			}

			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			return next(c)
		}
	}
}

// sessionAuthenticator는 세션 미들웨어가 인증 서비스에서 쓰는 부분이다.
type sessionAuthenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
}

// loadSession은 쿠키의 토큰으로 세션과 사용자를 찾아 컨텍스트에 담는다. 로그인을 요구하지는 않는다.
// 로그인이 필요한 경로에서 막는 일은 RequireAuth가 한다.
//
// 쿠키는 있는데 세션이 통하지 않으면(끝났거나 끊겼으면) 응답에 쿠키를 지우는 헤더를 싣는다.
// 지우지 않으면 브라우저가 죽은 토큰을 만료될 때까지 요청마다 실어 보내고, 그때마다 DB를 뒤지게 된다.
// 뒤따르는 핸들러가 새 쿠키를 심으면(로그인) 그 값이 이 헤더를 덮어쓴다.
//
// DB 오류처럼 세션이 통하는지 알 수 없는 경우에는 요청을 실패시킨다. 로그인하지 않은 것으로 치고 넘어가면
// 멀쩡히 로그인한 사용자가 401을 받고 로그인 화면으로 쫓겨난다.
func loadSession(authenticator sessionAuthenticator, cookies sessionCookies) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			state := sessionState{presentedToken: cookies.read(req)}

			if state.presentedToken != "" {
				principal, err := authenticator.Authenticate(req.Context(), state.presentedToken)
				switch {
				case err == nil:
					state.principal = &principal
				case errors.Is(err, auth.ErrSessionInvalid):
					c.Response().Header().Add(echo.HeaderSetCookie, cookies.clear())
				default:
					return fmt.Errorf("load session: %w", err)
				}
			}

			c.SetRequest(req.WithContext(withSession(req.Context(), state)))
			return next(c)
		}
	}
}

// RequireAuth는 로그인하지 않은 요청을 401 unauthenticated로 거부한다. loadSession 뒤에 둔다.
//
// 명세에 적힌 경로에는 Register가 명세의 security를 보고 알아서 붙인다. 명세에 적을 수 없는 경로(WebSocket)에는
// 경로를 등록하는 쪽이 직접 붙인다.
func RequireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if _, ok := PrincipalFrom(c.Request().Context()); !ok {
				return errUnauthenticated
			}
			return next(c)
		}
	}
}

// validateRequests는 요청을 실행 파일에 담긴 명세와 견준다. 모르는 필드, 틀린 타입, 길이 초과, 빠진 필수 값을 여기서 거부하므로
// 핸들러는 명세에 맞는 요청만 받는다.
//
// 명세의 security도 여기서 본다. 검증기는 security를 본문보다 먼저 보므로, 로그인하지 않은 요청은
// 본문이 틀렸더라도 400이 아니라 401을 받는다. 무엇이 틀렸는지는 로그인한 사람에게만 알려준다.
//
// 검증기가 돌려주는 오류의 문구에는 검증에 걸린 입력값이 들어 있다. 그 문구는 응답에도 로그에도 옮기지 않고
// 상태 코드만 쓴다(problemFor).
//
// inSpec이 false를 돌려주는 요청은 검증하지 않고 넘긴다. 명세에 적을 수 없는 경로(WebSocket)를 같은 그룹에 붙일 수 있게 하려는 것이다.
// 그런 경로는 검증기가 "명세에 없는 경로"라며 404로 막아 버린다. 넘겨도 열리는 것은 없다. 등록된 경로가 없으면 라우터가 404를 낸다.
func validateRequests(spec *openapi3.T, inSpec func(c *echo.Context) bool) (echo.MiddlewareFunc, error) {
	// 미들웨어는 명세로 라우터를 만들지 못하면 패닉한다. 같은 일을 먼저 해 보고 오류로 돌려준다.
	if _, err := gorillamux.NewRouter(spec); err != nil {
		return nil, fmt.Errorf("api: build router from the embedded spec: %w", err)
	}
	return echomiddleware.OapiRequestValidatorWithOptions(spec, &echomiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authenticateFromContext,
		},
		Skipper: func(c *echo.Context) bool { return !inSpec(c) },
		// 명세에 servers를 적지 않으므로 Host를 견주는 일은 없다. 적게 되더라도 경고를 표준 로거로 찍지 않게 한다.
		SilenceServersWarning: true,
	}), nil
}

// authenticateFromContext는 명세의 security가 걸린 경로에서 검증기가 부른다.
// 검증 미들웨어는 *echo.HTTPError만 그대로 위로 올려 주므로, 그것으로 한 번 감싼다.
//
// 첫 인자는 쓰지 않는다. 검증 미들웨어가 넘겨주는 것은 요청의 컨텍스트가 아니라 새로 만든 빈 컨텍스트라서
// 세션 미들웨어가 담아 둔 값이 없다. 그 값은 요청의 컨텍스트에 있다.
func authenticateFromContext(_ context.Context, input *openapi3filter.AuthenticationInput) error {
	requestCtx := input.RequestValidationInput.Request.Context()
	if _, ok := PrincipalFrom(requestCtx); !ok { //nolint:contextcheck // 위 주석대로 요청의 컨텍스트를 봐야 한다.
		return echo.ErrUnauthorized.Wrap(errUnauthenticated)
	}
	return nil
}

// protectedOperations는 명세에서 로그인이 필요한 작업의 operationId를 모은다.
// 작업에 security가 없으면 명세 전체의 security를 따른다. 그래서 새 경로는 따로 풀어 주지 않는 한 로그인이 필요하다.
func protectedOperations(spec *openapi3.T) []string {
	var ids []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			security := spec.Security
			if op.Security != nil {
				security = *op.Security
			}
			if len(security) > 0 {
				ids = append(ids, op.OperationID)
			}
		}
	}
	return ids
}
