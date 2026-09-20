package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// 시험은 시계를 읽지 않는다. 모든 시각은 이 값에서 시작해 가짜 시계를 옮겨 만든다.
var baseTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

const (
	day = 24 * time.Hour

	testOrigin   = "http://localhost:5173"
	testEmail    = "haneul@example.com"
	testPassword = "오늘도 수고했어요, 내일 봐요"

	pathRequirements = "/api/v1/auth/requirements"
	pathSignup       = "/api/v1/auth/signup"
	pathLogin        = "/api/v1/auth/login"
	pathLogout       = "/api/v1/auth/logout"
	pathMe           = "/api/v1/me"
)

// 설정의 기본값과 같은 수명이다.
var testSessionConfig = auth.SessionConfig{
	AbsoluteLifetime: 30 * day,
	IdleLifetime:     14 * day,
	TouchInterval:    5 * time.Minute,
}

// 시험에서는 한도에 걸리지 않게 넉넉히 준다. 한도를 보는 시험만 값을 줄인다.
var generousLimits = RateLimits{
	SignupPerIP:        RateLimit{Burst: 1000, Period: time.Minute},
	LoginPerIP:         RateLimit{Burst: 1000, Period: time.Minute},
	LoginPerEmail:      RateLimit{Burst: 1000, Period: time.Minute},
	LoginPerEmailTotal: RateLimit{Burst: 2000, Period: time.Minute},
	MaxKeys:            1000,
}

// syncBuffer는 서버와 시험이 함께 쓰는 로그 버퍼다.
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

// lines는 msg가 같은 로그 줄을 모두 돌려준다.
func (b *syncBuffer) lines(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line))
		if line["msg"] == msg {
			out = append(out, line)
		}
	}
	return out
}

// testServer는 진짜 DB와 가짜 시계 위에, 운영과 같은 길(New)로 만든 서버다.
type testServer struct {
	echo    *echo.Echo
	clock   *clock.Fake
	pool    *pgxpool.Pool
	logs    *syncBuffer
	cookies sessionCookies
	// bodies에는 이 서버가 내보낸 모든 응답 본문이 쌓인다. 새어 나가면 안 되는 값을 찾을 때 쓴다.
	bodies *syncBuffer
}

func newTestServer(t *testing.T, mutate func(*Options)) *testServer {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(baseTime)
	st := store.New(pool)
	logs := &syncBuffer{}
	// 운영과 같은 로거를 쓴다. 금지된 이름의 속성을 가리는 안전망까지 포함해서 본다.
	logger := logging.New(logs, slog.LevelDebug)

	ring, err := crypto.NewKeyRing(1, map[int][]byte{1: bytes.Repeat([]byte{0x11}, 32)})
	require.NoError(t, err)
	sessions, err := auth.NewSessions(st, clk, testSessionConfig)
	require.NoError(t, err)
	// 해시가 받아 주는 가장 싼 값이다. 시험을 빨리 돌리려는 것이다.
	hasher, err := auth.NewHasher(t.Context(), auth.Argon2Params{MemoryKiB: 64, Time: 1, Parallelism: 1}, 4)
	require.NoError(t, err)
	service, err := auth.NewService(auth.ServiceOptions{
		Store: st, Clock: clk, Logger: logger, Hasher: hasher, Sessions: sessions, KeyRing: ring,
	})
	require.NoError(t, err)

	opts := Options{
		Logger:       logger,
		Clock:        clk,
		DB:           pool,
		Auth:         service,
		Settings:     st.Queries(),
		Cookie:       CookieConfig{Name: "naeil_session", Secure: false, MaxAge: testSessionConfig.AbsoluteLifetime},
		PublicOrigin: testOrigin,
		RateLimits:   generousLimits,
	}
	if mutate != nil {
		mutate(&opts)
	}
	e, err := New(opts)
	require.NoError(t, err)
	cookies, err := newSessionCookies(opts.Cookie)
	require.NoError(t, err)

	return &testServer{echo: e, clock: clk, pool: pool, logs: logs, cookies: cookies, bodies: &syncBuffer{}}
}

// request는 시험이 보내는 요청 하나다.
type request struct {
	method string
	path   string
	// body는 JSON으로 바꿔 싣는다. rawBody를 주면 그대로 싣는다.
	body    any
	rawBody string
	headers map[string]string
	// remoteAddr는 연결의 상대 주소다. 비우면 httptest의 기본값(192.0.2.1:1234)이다.
	remoteAddr string
	// chunked면 본문의 길이를 알리지 않고 보낸다.
	chunked bool
}

// browser는 쿠키를 기억하는 클라이언트다. 브라우저처럼 같은 출처의 요청임을 알리는 헤더를 붙인다.
type browser struct {
	t       *testing.T
	srv     *testServer
	cookies map[string]string
	// bare면 브라우저가 붙이는 헤더 없이 보낸다(curl 같은 클라이언트).
	bare bool
}

func (s *testServer) browser(t *testing.T) *browser {
	t.Helper()
	return &browser{t: t, srv: s, cookies: map[string]string{}}
}

func (b *browser) do(r request) *httptest.ResponseRecorder {
	b.t.Helper()

	var body io.Reader
	switch {
	case r.rawBody != "":
		body = strings.NewReader(r.rawBody)
	case r.body != nil:
		encoded, err := json.Marshal(r.body)
		require.NoError(b.t, err)
		body = bytes.NewReader(encoded)
	}
	if body != nil && r.chunked {
		// 길이를 알 수 없는 읽개로 감싸면 Content-Length 없이 흘려보내는 요청이 된다.
		body = io.MultiReader(body)
	}
	req := httptest.NewRequest(r.method, r.path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !b.bare {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X)")
	}
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	for name, value := range r.headers {
		req.Header.Set(name, value)
	}
	if r.remoteAddr != "" {
		req.RemoteAddr = r.remoteAddr
	}

	rec := httptest.NewRecorder()
	b.srv.echo.ServeHTTP(rec, req)

	_, _ = b.srv.bodies.Write(rec.Body.Bytes())
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return rec
}

func (b *browser) get(path string) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.do(request{method: http.MethodGet, path: path})
}

func (b *browser) post(path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.do(request{method: http.MethodPost, path: path, body: body})
}

// signupBody는 가입 규칙이 알려준 동의를 그대로 실은 가입 요청이다.
func signupBody(email, password string) map[string]any {
	consents := make([]map[string]string, 0, len(auth.CurrentConsents()))
	for _, c := range auth.CurrentConsents() {
		consents = append(consents, map[string]string{"kind": c.Kind, "version": c.Version})
	}
	return map[string]any{"email": email, "password": password, "consents": consents}
}

func loginBody(email, password string) map[string]string {
	return map[string]string{"email": email, "password": password}
}

func (b *browser) signup(email string) *httptest.ResponseRecorder {
	b.t.Helper()
	rec := b.post(pathSignup, signupBody(email, testPassword))
	require.Equal(b.t, http.StatusCreated, rec.Code, rec.Body.String())
	return rec
}

// decode는 응답 본문을 v로 읽는다. 명세에 없는 필드가 있으면 실패한다.
func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(v), rec.Body.String())
}

// requireProblem은 응답이 application/problem+json이고 상태와 code가 맞는지 본다.
func requireProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code ProblemCode) Problem {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var p Problem
	decode(t, rec, &p)
	require.Equal(t, code, p.Code)
	require.Equal(t, status, p.Status)
	require.Equal(t, "/problems/"+string(code), p.Type)
	require.NotEmpty(t, p.Title)
	require.NotEmpty(t, p.RequestID)
	require.Equal(t, rec.Header().Get("X-Request-Id"), p.RequestID)
	return p
}

// setCookie는 응답이 심은 세션 쿠키를 돌려준다. 없으면 nil이다.
func (s *testServer) setCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == s.cookies.name {
			return c
		}
	}
	return nil
}
