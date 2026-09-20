package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return ln
}

func TestNewServer_Timeouts(t *testing.T) {
	t.Run("헤더와 유휴 시간은 제한하고 쓰기 시간은 제한하지 않는다", func(t *testing.T) {
		s := NewServer(ServerOptions{Addr: ":0", Handler: http.NotFoundHandler()})

		assert.Positive(t, s.httpServer.ReadHeaderTimeout)
		assert.Positive(t, s.httpServer.ReadTimeout)
		assert.Positive(t, s.httpServer.IdleTimeout)
		assert.Positive(t, s.httpServer.MaxHeaderBytes)
		assert.Zero(t, s.httpServer.WriteTimeout, "전역 쓰기 기한은 오래 열려 있는 대화 연결을 끊는다")
		assert.Equal(t, DefaultDrainTimeout, s.drainTimeout)
	})
}

func TestServer_GracefulShutdown(t *testing.T) {
	t.Run("종료 신호가 와도 처리 중이던 요청은 끝까지 응답한다", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "finished")
		})

		s := NewServer(ServerOptions{Handler: handler, DrainTimeout: 5 * time.Second})
		ln := listenLocal(t)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		serveDone := make(chan error, 1)
		go func() { serveDone <- s.Serve(ctx, ln) }()

		type result struct {
			body string
			err  error
		}
		respDone := make(chan result, 1)
		go func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
			if err != nil {
				respDone <- result{err: err}
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				respDone <- result{err: err}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, err := io.ReadAll(resp.Body)
			respDone <- result{body: string(b), err: err}
		}()

		<-started
		cancel()

		select {
		case err := <-serveDone:
			t.Fatalf("처리 중인 요청이 있는데 서버가 먼저 내려갔다: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		close(release)

		select {
		case r := <-respDone:
			require.NoError(t, r.err)
			assert.Equal(t, "finished", r.body)
		case <-time.After(5 * time.Second):
			t.Fatal("응답이 오지 않았다")
		}
		select {
		case err := <-serveDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("서버가 내려가지 않았다")
		}
	})

	t.Run("기한 안에 끝나지 않는 요청이 있으면 연결을 끊고 오류로 알린다", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(started)
			<-release
		})

		s := NewServer(ServerOptions{Handler: handler, DrainTimeout: 50 * time.Millisecond})
		ln := listenLocal(t)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		serveDone := make(chan error, 1)
		go func() { serveDone <- s.Serve(ctx, ln) }()

		go func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()

		<-started
		cancel()

		select {
		case err := <-serveDone:
			require.ErrorIs(t, err, context.DeadlineExceeded)
		case <-time.After(5 * time.Second):
			t.Fatal("기한이 지났는데 서버가 내려가지 않았다")
		}
	})

	t.Run("요청이 없으면 종료 신호에 바로 내려간다", func(t *testing.T) {
		s := NewServer(ServerOptions{Handler: http.NotFoundHandler()})
		ln := listenLocal(t)

		ctx, cancel := context.WithCancel(t.Context())
		serveDone := make(chan error, 1)
		go func() { serveDone <- s.Serve(ctx, ln) }()
		cancel()

		select {
		case err := <-serveDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("서버가 내려가지 않았다")
		}
	})

	// 넘겨받은 연결(WebSocket)을 닫는 일이 여기에 걸린다. 부르기만 하고 돌아가 버리면
	// 접속 풀이 닫히고 프로세스가 끝날 때까지도 소켓은 열린 채다.
	t.Run("종료가 시작되면 정리 함수를 부르고 끝날 때까지 기다린다", func(t *testing.T) {
		called := make(chan struct{})
		finished := make(chan struct{})
		s := NewServer(ServerOptions{
			Handler: http.NotFoundHandler(),
			OnDrain: func(context.Context) {
				close(called)
				time.Sleep(200 * time.Millisecond)
				close(finished)
			},
		})
		ln := listenLocal(t)

		ctx, cancel := context.WithCancel(t.Context())
		serveDone := make(chan error, 1)
		go func() { serveDone <- s.Serve(ctx, ln) }()
		cancel()

		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatal("정리 함수가 불리지 않았다")
		}
		require.NoError(t, <-serveDone)
		select {
		case <-finished:
		default:
			t.Fatal("정리가 끝나기 전에 Serve가 돌아왔다")
		}
	})

	t.Run("정리가 기한 안에 끝나지 않으면 거기서 그만 기다린다", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		s := NewServer(ServerOptions{
			Handler:      http.NotFoundHandler(),
			DrainTimeout: 100 * time.Millisecond,
			OnDrain:      func(context.Context) { <-release },
		})
		ln := listenLocal(t)

		ctx, cancel := context.WithCancel(t.Context())
		serveDone := make(chan error, 1)
		go func() { serveDone <- s.Serve(ctx, ln) }()
		cancel()

		select {
		case err := <-serveDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("정리를 기다리다 내려가지 못했다")
		}
	})

	t.Run("이미 쓰이는 주소면 Run이 오류를 돌려준다", func(t *testing.T) {
		ln := listenLocal(t)
		defer func() { _ = ln.Close() }()

		s := NewServer(ServerOptions{Addr: ln.Addr().String(), Handler: http.NotFoundHandler()})
		err := s.Run(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listen on")
	})
}
