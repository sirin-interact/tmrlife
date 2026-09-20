package httpserver

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 대화 연결은 WebSocket으로 몇 분씩 열려 있다. 공통 미들웨어와 서버의 시간 제한이
// 그 연결을 막거나 끊지 않는다는 것을 실제 연결로 확인한다.
func TestWebSocketThroughMiddleware(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	e := New(Options{Logger: logger, DB: fakePinger{}})
	e.GET("/ws", func(c *echo.Context) error {
		conn, err := websocket.Accept(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			typ, msg, err := conn.Read(c.Request().Context())
			if err != nil {
				return nil
			}
			if err := conn.Write(c.Request().Context(), typ, msg); err != nil {
				return nil
			}
		}
	})

	srv := NewServer(ServerOptions{Handler: e, Logger: logger, DrainTimeout: time.Second})
	// 읽기 기한을 아주 짧게 줄여, 기한이 지나도 넘겨받은 연결이 살아 있는지 본다.
	const shortReadTimeout = 150 * time.Millisecond
	srv.httpServer.ReadTimeout = shortReadTimeout
	srv.httpServer.ReadHeaderTimeout = shortReadTimeout

	ln := listenLocal(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, ln) }()

	dialCtx, dialCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer dialCancel()
	conn, resp, err := websocket.Dial(dialCtx, "ws://"+ln.Addr().String()+"/ws", nil)
	require.NoError(t, err, "미들웨어를 거쳐도 연결이 열려야 한다")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer func() { _ = conn.CloseNow() }()

	t.Run("연결을 여는 응답에도 요청 ID가 붙는다", func(t *testing.T) {
		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"))
	})

	t.Run("서버의 읽기 기한이 지나도 연결이 끊기지 않는다", func(t *testing.T) {
		<-time.After(3 * shortReadTimeout)

		require.NoError(t, conn.Write(dialCtx, websocket.MessageText, []byte("ping")))
		typ, msg, err := conn.Read(dialCtx)
		require.NoError(t, err)
		assert.Equal(t, websocket.MessageText, typ)
		assert.Equal(t, "ping", string(msg))
	})

	t.Run("주고받은 내용은 로그에 남지 않는다", func(t *testing.T) {
		require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
		cancel()
		select {
		case <-serveDone:
		case <-time.After(5 * time.Second):
			t.Fatal("서버가 내려가지 않았다")
		}

		// 넘겨받은 연결은 net/http가 활성 연결로 세지 않는다. 그래서 Shutdown은 이 연결의 핸들러를 기다리지 않고,
		// 접근 로그는 서버가 내려간 뒤에 찍히기도 한다. 한 번만 읽으면 기계가 바쁜 날에만 실패하는 시험이 된다.
		var line map[string]any
		for range 500 {
			if line = logs.find(t, "request"); line != nil {
				break
			}
			<-time.After(10 * time.Millisecond)
		}
		require.NotNil(t, line, "연결이 끝나면 접근 로그 한 줄이 남는다")
		assert.Equal(t, "/ws", line["route"])
		assert.NotContains(t, logs.String(), "ping")
	})
}
