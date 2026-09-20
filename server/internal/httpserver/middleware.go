package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime"
	"runtime/debug"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type requestIDKey struct{}

// RequestID는 요청 컨텍스트에 담긴 요청 ID를 꺼낸다. 없으면 빈 문자열이다.
// 핸들러 아래 계층이 로그를 남길 때 같은 ID로 묶을 수 있게 한다.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// 밖에서 온 요청 ID는 이 꼴일 때만 받아준다.
// 헤더 값이 그대로 로그에 찍히므로, 아무 글이나 로그에 밀어 넣을 수 있는 길을 막는다.
var inboundRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func requestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()

			id := req.Header.Get(echo.HeaderXRequestID)
			if !inboundRequestIDPattern.MatchString(id) {
				id = newRequestID()
			}

			// 뒤따르는 미들웨어가 헤더에서 읽어도 검증된 값을 보게 한다.
			req.Header.Set(echo.HeaderXRequestID, id)
			c.Response().Header().Set(echo.HeaderXRequestID, id)
			c.SetRequest(req.WithContext(context.WithValue(req.Context(), requestIDKey{}, id)))

			return next(c)
		}
	}
}

func newRequestID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// 난수원이 고장 난 드문 경우다. 요청 자체는 막지 않고, 로그에서 구분만 되게 한다.
		return "unavailable"
	}
	return id.String()
}

// accessLog는 요청마다 한 줄을 남긴다.
// 남기는 것은 메서드, 경로 틀, 상태, 걸린 시간, 요청 ID뿐이다.
// 실제 경로와 쿼리 문자열, 본문, 헤더는 남기지 않는다. 경로 변수나 검색어에 사용자의 글이 들어올 수 있기 때문이다.
//
// extra는 누가 보낸 요청인지처럼 이 패키지가 모르는 식별자를 더한다. 안쪽 미들웨어가 요청 컨텍스트에 담아 둔 값을
// 응답이 나간 뒤에 읽으므로, 인증이 끝난 뒤의 값을 본다.
func accessLog(logger *slog.Logger, extra func(ctx context.Context) []slog.Attr) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		// 상태 확인은 몇 초마다 들어온다. 실패는 핸들러가 따로 남긴다.
		Skipper: func(c *echo.Context) bool {
			p := c.Path()
			return p == healthzPath || p == readyzPath
		},
		// 오류를 상태 코드로 바꾸는 일을 여기서 끝내야 실제로 나간 상태가 기록된다.
		HandleError:  true,
		LogLatency:   true,
		LogMethod:    true,
		LogRoutePath: true,
		LogStatus:    true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			route := v.RoutePath
			if route == "" {
				route = "(unmatched)"
			}

			level := slog.LevelInfo
			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("route", route),
				slog.Int("status", v.Status),
				slog.Float64("latency_ms", float64(v.Latency.Microseconds())/1000),
				slog.String("request_id", RequestID(c.Request().Context())),
			}
			if v.Status >= http.StatusInternalServerError {
				level = slog.LevelError
				// 4xx의 오류 문구에는 검증에 걸린 입력값이 섞여 있을 수 있어 남기지 않는다.
				// 5xx는 서버 쪽 원인이라 남긴다. 서버 오류에 사용자의 글을 넣지 않는 것은 오류를 만드는 쪽의 책임이다.
				if v.Error != nil {
					attrs = append(attrs, slog.String("error", v.Error.Error()))
				}
			}
			if extra != nil {
				attrs = append(attrs, extra(c.Request().Context())...)
			}

			logger.LogAttrs(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}

// errPanicRecovered는 패닉 값을 대신해 위로 올라가는 오류다.
// 패닉 값에는 무엇이든 들어 있을 수 있어서 접근 로그나 응답으로 흘러가지 않게 한다.
var errPanicRecovered = errors.New("panic recovered")

// recoverPanic은 핸들러의 패닉을 500으로 바꾸고 스택을 남긴다.
// 스택에는 함수 이름과 줄 번호, 인자의 주소만 있고 요청 본문은 없다.
func recoverPanic(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				// net/http가 응답을 중단시키려고 쓰는 신호다. 삼키면 연결이 끊기지 않는다.
				if abortErr, ok := r.(error); ok && errors.Is(abortErr, http.ErrAbortHandler) {
					panic(r)
				}

				attrs := []slog.Attr{
					slog.String("request_id", RequestID(c.Request().Context())),
					slog.String("method", c.Request().Method),
					slog.String("route", c.Path()),
					slog.String("panic_type", fmt.Sprintf("%T", r)),
				}
				// 런타임 오류(nil 참조, 범위 초과 등)의 문구에는 코드 위치 정보만 있다.
				// 그 밖의 패닉 값은 무엇이 들어 있을지 몰라 타입만 남긴다.
				var runtimeErr runtime.Error
				if asErr, ok := r.(error); ok && errors.As(asErr, &runtimeErr) {
					attrs = append(attrs, slog.String("panic", runtimeErr.Error()))
				}
				attrs = append(attrs, slog.String("stack", string(debug.Stack())))

				logger.LogAttrs(c.Request().Context(), slog.LevelError, "panic recovered", attrs...)
				err = echo.ErrInternalServerError.Wrap(errPanicRecovered)
			}()
			return next(c)
		}
	}
}

const (
	// 이 서버는 JSON만 내보낸다. 응답이 문서로 해석되더라도 아무것도 불러오거나 실행하지 못하게 한다.
	contentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	// 1년. 하위 도메인까지 HTTPS로 고정한다.
	strictTransportSecurity = "max-age=31536000; includeSubDomains"
)

func securityHeaders(production bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set(echo.HeaderXContentTypeOptions, "nosniff")
			h.Set(echo.HeaderReferrerPolicy, "no-referrer")
			h.Set(echo.HeaderContentSecurityPolicy, contentSecurityPolicy)
			h.Set(echo.HeaderXFrameOptions, "DENY")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			// 응답에는 일기와 마음 기록이 담긴다. 브라우저나 중간 프록시에 남지 않게 기본값을 no-store로 둔다.
			// 캐시해도 되는 응답은 핸들러가 덮어쓴다.
			h.Set(echo.HeaderCacheControl, "no-store")
			if production {
				h.Set(echo.HeaderStrictTransportSecurity, strictTransportSecurity)
			}
			return next(c)
		}
	}
}
