package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	// DefaultDrainTimeout은 종료 신호를 받은 뒤 처리 중인 요청을 기다리는 시간이다.
	// 쿠버네티스가 파드를 강제로 죽이기까지의 기본 유예(30초)보다 짧아야 스스로 정리하고 끝낼 수 있다.
	DefaultDrainTimeout = 20 * time.Second

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// 앞단 프록시가 유휴 연결을 들고 있는 시간(보통 90초)보다 길어야 한다.
	// 서버가 먼저 닫으면 프록시가 닫힌 연결에 요청을 실어 보내 502가 난다.
	idleTimeout    = 120 * time.Second
	maxHeaderBytes = 64 << 10
)

// Server는 핸들러를 띄우고, 종료 신호가 오면 처리 중인 요청을 마친 뒤 내린다.
type Server struct {
	httpServer   *http.Server
	logger       *slog.Logger
	drainTimeout time.Duration
}

// ServerOptions는 서버를 띄우는 데 필요한 것들이다.
type ServerOptions struct {
	Addr    string
	Handler http.Handler
	Logger  *slog.Logger
	// DrainTimeout이 0이면 DefaultDrainTimeout을 쓴다.
	DrainTimeout time.Duration
}

// NewServer는 시간 제한이 걸린 서버를 만든다.
func NewServer(opts ServerOptions) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	drain := opts.DrainTimeout
	if drain <= 0 {
		drain = DefaultDrainTimeout
	}

	return &Server{
		logger:       logger,
		drainTimeout: drain,
		httpServer: &http.Server{
			Addr:    opts.Addr,
			Handler: opts.Handler,

			// 헤더를 천천히 보내며 연결을 붙잡는 공격을 막는다.
			ReadHeaderTimeout: readHeaderTimeout,
			// 본문 상한이 작아서 정상 요청은 이 시간 안에 다 읽힌다.
			// WebSocket은 연결을 넘겨받는 순간 net/http가 이 기한을 지우므로 영향받지 않는다.
			ReadTimeout: readTimeout,
			// WriteTimeout은 일부러 두지 않는다.
			// 이 기한은 요청 헤더를 읽은 때부터 재고 응답을 쓰는 동안 늘어나지 않는다.
			// 그래서 전역으로 걸면 몇 분씩 이어지는 대화 연결과 흘려보내는 응답이 중간에 끊긴다.
			// 짧게 끝나야 하는 REST 경로의 상한은 경로별 컨텍스트 기한으로 건다.
			WriteTimeout:   0,
			IdleTimeout:    idleTimeout,
			MaxHeaderBytes: maxHeaderBytes,

			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		},
	}
}

// RegisterOnShutdown은 종료가 시작될 때 부를 함수를 등록한다.
// Shutdown은 넘겨받은 연결(WebSocket)을 닫아주지 않으므로, 그런 연결을 가진 쪽이 여기서 직접 닫아야 한다.
func (s *Server) RegisterOnShutdown(f func()) {
	s.httpServer.RegisterOnShutdown(f)
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.httpServer.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve는 ctx가 끝날 때까지 요청을 받는다.
// ctx가 끝나면 새 연결을 받지 않고, 처리 중인 요청이 끝나기를 DrainTimeout만큼 기다린다.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.logger.LogAttrs(ctx, slog.LevelInfo, "http server listening", slog.String("addr", ln.Addr().String()))

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.httpServer.Serve(ln) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.logger.LogAttrs(ctx, slog.LevelInfo, "http server draining", slog.Duration("timeout", s.drainTimeout))

	// 종료 신호로 이미 끝난 ctx를 물려받으면 기다릴 틈 없이 바로 끊긴다. 취소만 떼어 낸다.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.drainTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(drainCtx); err != nil {
		// 기한 안에 끝나지 않은 요청이 있다. 남은 연결을 끊고 내려간다.
		_ = s.httpServer.Close()
		<-serveErr
		return fmt.Errorf("drain in-flight requests: %w", err)
	}
	<-serveErr

	s.logger.LogAttrs(ctx, slog.LevelInfo, "http server stopped")
	return nil
}
