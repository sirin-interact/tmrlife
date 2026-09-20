// Package httpserver는 공통 미들웨어가 붙은 Echo 인스턴스와, 그것을 안전하게 띄우고 내리는 서버를 만든다.
//
// 이 패키지는 경로를 모른다. 상태 확인 두 개만 직접 등록하고, API 경로는 부르는 쪽이 붙인다.
package httpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const (
	// DefaultBodyLimitBytes는 요청 본문의 기본 상한이다. JSON API에는 1 MiB면 넉넉하다.
	DefaultBodyLimitBytes int64 = 1 << 20

	defaultReadyTimeout = 2 * time.Second
)

// Pinger는 준비 상태 확인이 DB에 묻는 단 하나의 질문이다. *pgxpool.Pool이 이 모양을 만족한다.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options는 Echo 인스턴스를 만드는 데 필요한 것들이다.
type Options struct {
	Logger *slog.Logger
	// DB는 /readyz가 확인할 대상이다.
	DB Pinger
	// Production이면 HSTS 헤더를 붙인다. HTTP로 여는 개발 환경에서 붙이면
	// 브라우저가 localhost를 HTTPS로만 열려고 해서 개발 서버에 접속할 수 없게 된다.
	Production bool
	// BodyLimitBytes가 0이면 DefaultBodyLimitBytes를 쓴다.
	BodyLimitBytes int64
	// ReadyTimeout은 /readyz가 DB 응답을 기다리는 시간이다. 0이면 2초다.
	ReadyTimeout time.Duration
	// ErrorHandler는 핸들러와 미들웨어가 돌려준 오류를 응답으로 바꾼다.
	// 오류 응답의 꼴은 API의 약속이라서 경로를 아는 쪽이 정한다. nil이면 상태 코드와 일반 문구만 담은 JSON을 보낸다.
	//
	// 한 오류에 두 번 불린다. 접근 로그가 실제로 나간 상태를 적으려고 먼저 부르고, Echo가 마지막에 한 번 더 부른다.
	// 그래서 이미 응답이 나갔으면 아무것도 하지 않아야 한다.
	ErrorHandler echo.HTTPErrorHandler
	// AccessLogAttrs는 접근 로그 한 줄에 더할 속성을 요청 컨텍스트에서 꺼낸다. 누가 보낸 요청인지(사용자 ID)를 남기는 데 쓴다.
	// 식별자만 돌려줘야 한다. 요청에 담겨 온 글을 돌려주면 그대로 로그에 남는다.
	AccessLogAttrs func(ctx context.Context) []slog.Attr
}

// New는 공통 미들웨어와 상태 확인 경로가 붙은 Echo 인스턴스를 만든다.
func New(opts Options) *echo.Echo {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	bodyLimit := opts.BodyLimitBytes
	if bodyLimit <= 0 {
		bodyLimit = DefaultBodyLimitBytes
	}
	readyTimeout := opts.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = defaultReadyTimeout
	}

	errorHandler := opts.ErrorHandler
	if errorHandler == nil {
		// 내부 오류의 내용은 응답에 싣지 않는다. 클라이언트는 상태 코드와 일반 문구만 받는다.
		errorHandler = echo.DefaultHTTPErrorHandler(false)
	}

	e := echo.NewWithConfig(echo.Config{
		Logger:           logger,
		HTTPErrorHandler: errorHandler,
	})

	// 순서가 뜻을 가진다. 바깥부터:
	//   요청 ID → 접근 로그 → 패닉 복구 → 보안 헤더 → 본문 상한
	// 접근 로그가 패닉 복구보다 바깥에 있어야 패닉으로 끝난 요청도 500으로 기록된다.
	e.Use(
		requestID(),
		accessLog(logger, opts.AccessLogAttrs),
		recoverPanic(logger),
		securityHeaders(opts.Production),
		middleware.BodyLimit(bodyLimit),
	)

	h := &health{db: opts.DB, logger: logger, timeout: readyTimeout}
	e.GET(healthzPath, h.live)
	e.GET(readyzPath, h.ready)

	return e
}
