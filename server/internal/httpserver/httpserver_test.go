package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePinger struct {
	err   error
	block bool
}

func (p fakePinger) Ping(ctx context.Context) error {
	if p.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.err
}

// syncBuffer는 서버 고루틴과 시험 고루틴이 함께 쓰는 로그 버퍼다.
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

func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &m))
		out = append(out, m)
	}
	return out
}

func (b *syncBuffer) find(t *testing.T, msg string) map[string]any {
	t.Helper()
	for _, line := range b.lines(t) {
		if line["msg"] == msg {
			return line
		}
	}
	return nil
}

func newTestEcho(t *testing.T, opts Options) (*echo.Echo, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	opts.Logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if opts.DB == nil {
		opts.DB = fakePinger{}
	}
	return New(opts), logs
}

func do(e *echo.Echo, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestHealthEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		db         Pinger
		path       string
		wantStatus int
		wantBody   string
	}{
		{"healthz는 DB와 상관없이 200이다", fakePinger{err: errors.New("db down")}, "/healthz", http.StatusOK, `{"status":"ok"}`},
		{"readyz는 DB가 답하면 200이다", fakePinger{}, "/readyz", http.StatusOK, `{"status":"ready"}`},
		{"readyz는 DB가 답하지 않으면 503이다", fakePinger{err: errors.New("db down")}, "/readyz", http.StatusServiceUnavailable, `{"status":"unavailable"}`},
		{"readyz는 DB가 늦으면 기다리지 않고 503이다", fakePinger{block: true}, "/readyz", http.StatusServiceUnavailable, `{"status":"unavailable"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEcho(t, Options{DB: tt.db, ReadyTimeout: 50 * time.Millisecond})
			rec := do(e, httptest.NewRequest(http.MethodGet, tt.path, nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
			assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
		})
	}

	t.Run("DB 없이 만들면 readyz는 503이다", func(t *testing.T) {
		e := New(Options{})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("readyz 실패는 경고로 남고 상태 확인은 접근 로그에 남지 않는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{DB: fakePinger{err: errors.New("db down")}})
		do(e, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		do(e, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		failed := logs.find(t, "readiness check failed")
		require.NotNil(t, failed)
		assert.Equal(t, "WARN", failed["level"])
		assert.Equal(t, "database", failed["dependency"])
		assert.Nil(t, logs.find(t, "request"), "상태 확인은 몇 초마다 오므로 접근 로그에서 뺀다")
	})
}

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name       string
		production bool
		wantHSTS   string
	}{
		{"개발에서는 HSTS를 붙이지 않는다", false, ""},
		{"운영에서는 HSTS를 붙인다", true, "max-age=31536000; includeSubDomains"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEcho(t, Options{Production: tt.production})
			h := do(e, httptest.NewRequest(http.MethodGet, "/healthz", nil)).Header()

			assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
			assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
			assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
			assert.Equal(t, "same-origin", h.Get("Cross-Origin-Resource-Policy"))
			assert.Equal(t, "no-store", h.Get("Cache-Control"))
			assert.Contains(t, h.Get("Content-Security-Policy"), "default-src 'none'")
			assert.Contains(t, h.Get("Content-Security-Policy"), "frame-ancestors 'none'")
			assert.Equal(t, tt.wantHSTS, h.Get("Strict-Transport-Security"))
		})
	}

	t.Run("없는 경로의 오류 응답에도 보안 헤더가 붙는다", func(t *testing.T) {
		e, _ := newTestEcho(t, Options{})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/nope", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})

	t.Run("핸들러는 캐시 기본값을 덮어쓸 수 있다", func(t *testing.T) {
		e, _ := newTestEcho(t, Options{})
		e.GET("/static-ish", func(c *echo.Context) error {
			c.Response().Header().Set("Cache-Control", "public, max-age=60")
			return c.NoContent(http.StatusNoContent)
		})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/static-ish", nil))
		assert.Equal(t, "public, max-age=60", rec.Header().Get("Cache-Control"))
	})
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		inbound  string
		wantKept bool
	}{
		{"보낸 ID가 없으면 새로 만든다", "", false},
		{"안전한 꼴의 ID는 그대로 쓴다", "edge-7f3a9c21.b4", true},
		{"너무 짧은 ID는 버리고 새로 만든다", "abc", false},
		{"너무 긴 ID는 버리고 새로 만든다", strings.Repeat("a", 65), false},
		{"공백이나 글이 섞인 ID는 버리고 새로 만든다", "오늘 너무 힘들었어요", false},
		{"따옴표와 중괄호가 섞인 ID는 버리고 새로 만든다", `x","level":"ERROR","a":"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, logs := newTestEcho(t, Options{})
			var seenInHandler string
			e.GET("/whoami", func(c *echo.Context) error {
				seenInHandler = RequestID(c.Request().Context())
				return c.NoContent(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
			if tt.inbound != "" {
				req.Header.Set("X-Request-Id", tt.inbound)
			}
			rec := do(e, req)

			got := rec.Header().Get("X-Request-Id")
			require.NotEmpty(t, got)
			if tt.wantKept {
				assert.Equal(t, tt.inbound, got)
			} else {
				assert.NotEqual(t, tt.inbound, got)
				assert.Regexp(t, `^[0-9a-f-]{36}$`, got)
			}
			assert.Equal(t, got, seenInHandler, "핸들러가 컨텍스트에서 같은 ID를 봐야 한다")

			line := logs.find(t, "request")
			require.NotNil(t, line)
			assert.Equal(t, got, line["request_id"])
			if !tt.wantKept && tt.inbound != "" {
				assert.NotContains(t, logs.String(), "힘들었어요")
			}
		})
	}

	t.Run("요청 밖의 컨텍스트에서는 빈 문자열이다", func(t *testing.T) {
		assert.Empty(t, RequestID(t.Context()))
	})
}

func TestAccessLog(t *testing.T) {
	t.Run("경로 틀과 상태만 남기고 실제 경로, 쿼리, 본문, 헤더는 남기지 않는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		e.POST("/diaries/:id", func(c *echo.Context) error {
			_, _ = io.Copy(io.Discard, c.Request().Body)
			return c.JSON(http.StatusCreated, map[string]string{"ok": "yes"})
		})

		req := httptest.NewRequest(http.MethodPost, "/diaries/secret-path-value?q=secret-query-value",
			strings.NewReader(`{"text":"secret-body-value"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "naeil_session=secret-cookie-value")
		req.Header.Set("Authorization", "Bearer secret-auth-value")
		rec := do(e, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.Equal(t, "INFO", line["level"])
		assert.Equal(t, "POST", line["method"])
		assert.Equal(t, "/diaries/:id", line["route"])
		assert.EqualValues(t, http.StatusCreated, line["status"])
		assert.Contains(t, line, "latency_ms")
		assert.NotEmpty(t, line["request_id"])

		for _, secret := range []string{"secret-path-value", "secret-query-value", "secret-body-value", "secret-cookie-value", "secret-auth-value"} {
			assert.NotContains(t, logs.String(), secret)
		}
	})

	t.Run("없는 경로는 실제 경로 대신 고정 표시로 남는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/typed/by/someone?x=1", nil))
		require.Equal(t, http.StatusNotFound, rec.Code)

		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.Equal(t, "(unmatched)", line["route"])
		assert.EqualValues(t, http.StatusNotFound, line["status"])
		assert.NotContains(t, logs.String(), "typed/by/someone")
	})

	t.Run("4xx 오류의 문구는 남기지 않는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		e.GET("/bad", func(*echo.Context) error {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid value: whatever-the-user-typed")
		})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/bad", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)

		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.Equal(t, "INFO", line["level"])
		assert.NotContains(t, line, "error")
		assert.NotContains(t, logs.String(), "whatever-the-user-typed")
	})

	t.Run("5xx는 ERROR로 남기고 원인을 함께 적되 응답에는 싣지 않는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		e.GET("/boom", func(*echo.Context) error {
			return errors.New("load diary: connection refused")
		})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/boom", nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "connection refused")

		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.Equal(t, "ERROR", line["level"])
		assert.EqualValues(t, http.StatusInternalServerError, line["status"])
		assert.Equal(t, "load diary: connection refused", line["error"])
	})

	t.Run("안쪽 미들웨어가 컨텍스트에 담은 식별자를 접근 로그에 더한다", func(t *testing.T) {
		type userKey struct{}
		e, logs := newTestEcho(t, Options{
			AccessLogAttrs: func(ctx context.Context) []slog.Attr {
				id, ok := ctx.Value(userKey{}).(string)
				if !ok {
					return nil
				}
				return []slog.Attr{slog.String("user_id", id)}
			},
		})
		// 인증 미들웨어가 하는 일을 흉내 낸다. 접근 로그보다 안쪽에서 요청 컨텍스트를 바꾼다.
		identify := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				req := c.Request()
				c.SetRequest(req.WithContext(context.WithValue(req.Context(), userKey{}, "user-42")))
				return next(c)
			}
		}
		e.GET("/private", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }, identify)
		e.GET("/public", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

		require.Equal(t, http.StatusNoContent, do(e, httptest.NewRequest(http.MethodGet, "/private", nil)).Code)
		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.Equal(t, "user-42", line["user_id"])

		e2, logs2 := newTestEcho(t, Options{AccessLogAttrs: func(context.Context) []slog.Attr { return nil }})
		e2.GET("/public", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
		require.Equal(t, http.StatusNoContent, do(e2, httptest.NewRequest(http.MethodGet, "/public", nil)).Code)
		assert.NotContains(t, logs2.find(t, "request"), "user_id")
	})
}

func TestErrorHandlerOption(t *testing.T) {
	t.Run("오류를 응답으로 바꾸는 일을 부르는 쪽이 정할 수 있고, 접근 로그는 실제로 나간 상태를 남긴다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{
			ErrorHandler: func(c *echo.Context, _ error) {
				// 접근 로그가 상태를 확정하려고 한 번, Echo가 마지막에 한 번 부른다. 이미 나간 응답에는 다시 쓰지 않는다.
				if resp, err := echo.UnwrapResponse(c.Response()); err == nil && resp.Committed {
					return
				}
				_ = c.JSON(http.StatusTeapot, map[string]string{"code": "custom"})
			},
		})
		e.GET("/boom", func(*echo.Context) error { return errors.New("whatever") })

		rec := do(e, httptest.NewRequest(http.MethodGet, "/boom", nil))
		assert.Equal(t, http.StatusTeapot, rec.Code)
		assert.JSONEq(t, `{"code":"custom"}`, rec.Body.String())

		line := logs.find(t, "request")
		require.NotNil(t, line)
		assert.EqualValues(t, http.StatusTeapot, line["status"])
	})
}

func TestRecoverPanic(t *testing.T) {
	t.Run("런타임 패닉은 500이 되고 스택과 문구가 남는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		e.POST("/panic", func(c *echo.Context) error {
			// 정적 검사가 미리 알아채지 못하게 실행 중에 정해지는 값으로 범위를 넘긴다.
			items := []int{1, 2, 3}
			return c.JSON(http.StatusOK, items[len(c.Path())])
		})

		req := httptest.NewRequest(http.MethodPost, "/panic", strings.NewReader(`{"text":"secret-body-value"}`))
		rec := do(e, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.JSONEq(t, `{"message":"Internal Server Error"}`, rec.Body.String())

		line := logs.find(t, "panic recovered")
		require.NotNil(t, line)
		assert.Equal(t, "ERROR", line["level"])
		assert.Equal(t, "/panic", line["route"])
		assert.Contains(t, line["panic"], "index out of range")
		assert.Contains(t, line["stack"], "httpserver_test.go", "스택에 패닉이 난 자리가 있어야 한다")
		assert.NotEmpty(t, line["request_id"])
		assert.NotContains(t, logs.String(), "secret-body-value")

		access := logs.find(t, "request")
		require.NotNil(t, access, "패닉으로 끝난 요청도 접근 로그에 남아야 한다")
		assert.EqualValues(t, http.StatusInternalServerError, access["status"])
		assert.Equal(t, "code=500, message=Internal Server Error, err=panic recovered", access["error"])
	})

	t.Run("임의의 값으로 난 패닉은 타입만 남기고 값은 남기지 않는다", func(t *testing.T) {
		e, logs := newTestEcho(t, Options{})
		e.GET("/panic", func(*echo.Context) error {
			// 복구 동작을 시험하려고 일부러 낸다.
			panic("whatever-the-user-typed")
		})
		rec := do(e, httptest.NewRequest(http.MethodGet, "/panic", nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		line := logs.find(t, "panic recovered")
		require.NotNil(t, line)
		assert.Equal(t, "string", line["panic_type"])
		assert.NotContains(t, line, "panic")
		assert.NotContains(t, rec.Body.String(), "whatever-the-user-typed")
		// 스택에는 값이 아니라 주소만 찍힌다.
		assert.NotContains(t, logs.String(), "whatever-the-user-typed")
	})

	t.Run("패닉 뒤에도 서버는 다음 요청을 받는다", func(t *testing.T) {
		e, _ := newTestEcho(t, Options{})
		e.GET("/panic", func(c *echo.Context) error {
			// 쿼리가 없으니 0으로 나누게 된다.
			return c.JSON(http.StatusOK, 1/len(c.QueryParams()))
		})
		assert.Equal(t, http.StatusInternalServerError, do(e, httptest.NewRequest(http.MethodGet, "/panic", nil)).Code)
		assert.Equal(t, http.StatusOK, do(e, httptest.NewRequest(http.MethodGet, "/healthz", nil)).Code)
	})
}

func TestBodyLimit(t *testing.T) {
	echoBody := func(c *echo.Context) error {
		b, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]int{"bytes": len(b)})
	}

	tests := []struct {
		name       string
		limit      int64
		size       int
		chunked    bool
		wantStatus int
	}{
		{"기본 상한(1 MiB)까지는 받는다", 0, 1 << 20, false, http.StatusOK},
		{"기본 상한을 1바이트 넘으면 413이다", 0, 1<<20 + 1, false, http.StatusRequestEntityTooLarge},
		{"길이를 알리지 않고 보내도 상한을 넘으면 413이다", 0, 1<<20 + 1, true, http.StatusRequestEntityTooLarge},
		{"상한을 따로 주면 그 값을 쓴다", 16, 17, false, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEcho(t, Options{BodyLimitBytes: tt.limit})
			e.POST("/upload", echoBody)

			var body io.Reader = bytes.NewReader(bytes.Repeat([]byte("a"), tt.size))
			if tt.chunked {
				// io.Reader로만 넘기면 httptest가 길이를 알 수 없어 ContentLength가 -1이 된다.
				body = io.MultiReader(body)
			}
			rec := do(e, httptest.NewRequest(http.MethodPost, "/upload", body))
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}
