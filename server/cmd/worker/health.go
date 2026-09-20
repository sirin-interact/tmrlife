package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// healthAddrEnv는 상태 확인용 주소를 정하는 환경 변수다.
	healthAddrEnv = "WORKER_HEALTH_ADDR"
	// API 서버(8080)와 다른 포트라서 한 컴퓨터에 둘을 함께 띄워도 부딪히지 않는다.
	// 배포 구성의 probe가 이 포트를 본다. 바꾸면 그쪽도 함께 바꾼다.
	defaultHealthAddr = ":8081"

	healthReadHeaderTimeout = 5 * time.Second
	healthIdleTimeout       = 30 * time.Second
	healthCloseTimeout      = 2 * time.Second
	healthMaxHeaderBytes    = 8 << 10
)

// healthAddr는 환경 변수의 값을 상태 확인용 주소로 바꾼다. 비어 있으면 기본값을 쓴다.
// 오류에는 변수 이름과 이유만 담는다. 다른 설정 오류와 같은 규칙이다.
func healthAddr(value string) (string, error) {
	addr := strings.TrimSpace(value)
	if addr == "" {
		return defaultHealthAddr, nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", errors.New("invalid configuration: " + healthAddrEnv + ": must look like host:port or :port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", errors.New("invalid configuration: " + healthAddrEnv + ": port must be a number from 0 to 65535")
	}
	return addr, nil
}

// healthListener는 작업자가 큐를 돌리고 있는지 물어볼 수 있는 작은 HTTP 서버다.
//
// 작업자는 밖에서 오는 요청을 받지 않는다. 그래서 검사가 없으면 쿠버네티스는 프로세스가 떠 있다는 것밖에 모르고,
// 새 작업자가 큐에 붙기도 전에 옛 작업자를 내려 버린다. 이 포트는 그 확인에만 쓰고 Service로 열지 않는다.
//
// DB는 보지 않는다. DB가 내려갔다고 작업자를 다시 띄워 봐야 나아지지 않고, 큐 클라이언트가 스스로 다시 붙는다.
type healthListener struct {
	listener net.Listener
	server   *http.Server
	served   chan struct{}
	running  atomic.Bool
}

// listenHealth는 주소를 열고 요청을 받기 시작한다. 처음에는 "돌고 있지 않다"(503)로 답한다.
// 포트를 열지 못하면 큐를 시작하기 전에 알 수 있도록 여기서 바로 오류를 돌려준다.
func listenHealth(ctx context.Context, addr string, logger *slog.Logger) (*healthListener, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	h := &healthListener{listener: ln, served: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	h.server = &http.Server{
		Handler: mux,
		// 헤더를 천천히 보내며 연결을 붙잡는 일을 막는다. 본문은 읽지 않는다.
		ReadHeaderTimeout: healthReadHeaderTimeout,
		ReadTimeout:       healthReadHeaderTimeout,
		WriteTimeout:      healthReadHeaderTimeout,
		IdleTimeout:       healthIdleTimeout,
		MaxHeaderBytes:    healthMaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	go func() {
		defer close(h.served)
		if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 여기서 프로세스를 끝내지 않는다. 답이 없으면 쿠버네티스의 생존 확인이 실패해 다시 띄운다.
			logger.LogAttrs(ctx, slog.LevelError, "health listener failed", slog.String("error", err.Error()))
		}
	}()

	logger.LogAttrs(ctx, slog.LevelInfo, "health listener listening", slog.String("addr", ln.Addr().String()))
	return h, nil
}

// Addr는 실제로 열린 주소다. 포트를 0으로 주면 운영체제가 고른 포트가 들어 있다.
func (h *healthListener) Addr() string {
	return h.listener.Addr().String()
}

// SetRunning은 큐 클라이언트가 돌고 있는지를 적어 둔다.
// 종료 신호를 받고 돌던 작업을 마무리하는 동안도 "돌고 있다"이다. 큐가 완전히 멈춘 뒤에 false로 바꾼다.
func (h *healthListener) SetRunning(running bool) {
	h.running.Store(running)
}

// Close는 새 요청을 받지 않고, 답하던 요청을 잠깐 기다린 뒤 닫는다.
func (h *healthListener) Close(ctx context.Context) {
	// 종료 신호로 이미 끝난 ctx를 그대로 쓰면 기다릴 틈 없이 끊긴다. 취소만 떼어 낸다.
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), healthCloseTimeout)
	defer cancel()
	if err := h.server.Shutdown(closeCtx); err != nil {
		_ = h.server.Close()
	}
	<-h.served
}

func (h *healthListener) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !h.running.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "queue client is not running\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}
