package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 대화 소켓을 닫을 때 쓰는 코드다. 1000~2999는 표준이 정한 값이라 쓰지 않고, 앱이 쓸 수 있는 4000번대를 쓴다.
// 웹앱은 이 값을 보고 다시 이을지 말지 고른다. 같은 뜻의 코드가 openapi.yaml의 채널 설명에도 적혀 있다.
const (
	// statusTakenOver는 같은 사용자가 다른 연결을 열어 이 연결이 물러난다는 뜻이다. 다시 이으면 또 빼앗는다.
	statusTakenOver websocket.StatusCode = 4001
	// statusGone은 계정이 지워져 더 이어갈 수 없다는 뜻이다.
	statusGone websocket.StatusCode = 4002
)

const (
	// DefaultPingInterval은 연결이 살아 있는지 확인하는 간격이다.
	// 앞단 프록시가 유휴 연결을 들고 있는 시간(보통 60~90초)보다 짧아야 조용한 대화가 중간에 끊기지 않는다.
	DefaultPingInterval = 30 * time.Second
	// DefaultPingTimeout은 ping에 답을 기다리는 시간이다. 넘기면 죽은 연결로 보고 닫는다.
	DefaultPingTimeout = 10 * time.Second

	// DefaultDiaryPollInterval은 대화가 끝난 뒤 일기 초안이 준비됐는지 확인하는 간격이다.
	DefaultDiaryPollInterval = time.Second
	// DefaultDiaryPollTimeout은 초안을 기다리는 시간의 상한이다. 넘기면 알리지 않고 연결을 닫는다.
	// 웹앱은 알림을 받지 못하면 일기를 직접 읽어 보고 없으면 잠시 뒤에 다시 읽는다.
	DefaultDiaryPollTimeout = 30 * time.Second

	// writeTimeout은 메시지 하나를 내보내는 데 주는 시간이다. 받는 쪽이 읽지 않으면 여기서 끝난다.
	writeTimeout = 10 * time.Second
	// closeTimeout은 닫는 인사를 주고받는 데 주는 시간이다.
	closeTimeout = 3 * time.Second
	// shutdownGrace는 서버가 내려갈 때 돌던 턴을 기다리는 시간이다. 넘기면 턴의 컨텍스트를 취소한다.
	shutdownGrace = 5 * time.Second

	// maxSocketsPerUser는 사용자 한 사람이 동시에 열어 둘 수 있는 소켓의 수다.
	// start를 보내지 않은 연결은 사용자의 자리(live)에 앉지 않아서 물려받기로 정리되지 않는다.
	// 그런 연결만 잔뜩 열어 두면 고루틴과 소켓이 한도 없이 쌓인다. 셋이면 죽어 가는 연결과 새 연결이 겹쳐도 넉넉하다.
	maxSocketsPerUser = 3

	// defaultRateMaxKeys는 사용자별 한도가 기억하는 사용자의 최대 수다.
	defaultRateMaxKeys = 50_000
)

// ConversationOptions는 대화 채널을 만드는 데 필요한 것이다.
type ConversationOptions struct {
	// Engine은 대화 한 바퀴를 돌리는 쪽이다.
	Engine *engine.Engine
	// Store는 일기 초안이 준비됐는지 확인하는 데 쓴다.
	Store  *store.Store
	Clock  clock.Clock
	Logger *slog.Logger

	// IdleCheckAfter 동안 말이 없으면 한 번 묻는다. IdleEndAfter를 더 기다려도 답이 없으면 대화를 끝낸다.
	IdleCheckAfter time.Duration
	IdleEndAfter   time.Duration

	// MaxMessageBytes는 클라이언트가 보내는 메시지 하나의 최대 크기다.
	MaxMessageBytes int64
	// MessageRate는 사용자가 모델을 부르는 글을 보낼 수 있는 빈도다. 같은 통을 연결 하나 안에서도 쓴다.
	MessageRate RateLimit
	// MaxKeys는 사용자별 한도가 기억하는 사용자의 최대 수다. 0이면 defaultRateMaxKeys다.
	MaxKeys int

	// PingInterval, PingTimeout, DiaryPollInterval, DiaryPollTimeout이 0이면 위의 기본값을 쓴다.
	PingInterval      time.Duration
	PingTimeout       time.Duration
	DiaryPollInterval time.Duration
	DiaryPollTimeout  time.Duration
}

// Conversation은 대화 채널이다. 연결 하나가 대화 하나를 맡는다.
//
// 채널은 관문도 답 만들기도 모른다. 받은 메시지를 엔진에 넘기고, 엔진이 낸 사건을 명세의 메시지로 바꿔 내보낸다.
// 무응답 타이머와 한도, 연결을 물려받는 일은 여기서 한다. 엔진은 시간을 재지 않는다.
type Conversation struct {
	opts ConversationOptions

	// userLimiter는 사용자마다 모델을 부르는 글의 빈도를 잰다.
	//
	// 연결마다 새 통을 주면 다시 잇는 것만으로 한도가 처음부터 다시 차서, 설정한 값이 비용의 상한이 되지 못한다.
	// 프레임을 풀어 보기 전에 막는 연결 단위의 통은 그대로 두고, 돈이 드는 글만 여기서 한 번 더 센다.
	// 다시 잇느라 오간 start는 이 통을 쓰지 않는다. 신호가 나쁜 곳에 있는 사람이 정작 말을 걸 때 막히면 안 된다.
	userLimiter *tokenBuckets

	// mu는 아래의 목록과 닫힘 표시를 지킨다.
	mu sync.Mutex
	// live는 사용자마다 지금 열려 있는 연결이다. 열린 대화가 사용자마다 하나라서 키도 사용자다.
	live map[uuid.UUID]*conversationConn
	// sockets는 사용자마다 지금 열려 있는 소켓의 수다. start를 보내지 않은 연결도 여기서 센다.
	sockets map[uuid.UUID]int
	// draining이면 서버가 내려가는 중이라 새 연결을 받지 않는다.
	draining bool
}

// NewConversation은 대화 채널을 만든다.
func NewConversation(opts ConversationOptions) (*Conversation, error) {
	switch {
	case opts.Engine == nil:
		return nil, errors.New("api: conversation needs an engine")
	case opts.Store == nil:
		return nil, errors.New("api: conversation needs a store")
	case opts.Clock == nil:
		return nil, errors.New("api: conversation needs a clock")
	case opts.Logger == nil:
		return nil, errors.New("api: conversation needs a logger")
	case opts.IdleCheckAfter <= 0 || opts.IdleEndAfter <= 0:
		return nil, errors.New("api: conversation idle waits must be greater than zero")
	case opts.MaxMessageBytes <= 0:
		return nil, errors.New("api: conversation needs a message size limit")
	}
	if opts.MaxKeys <= 0 {
		opts.MaxKeys = defaultRateMaxKeys
	}
	userLimiter, err := newTokenBuckets(opts.Clock, opts.MessageRate, opts.MaxKeys)
	if err != nil {
		return nil, fmt.Errorf("api: conversation message rate: %w", err)
	}

	if opts.PingInterval <= 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.PingTimeout <= 0 {
		opts.PingTimeout = DefaultPingTimeout
	}
	if opts.DiaryPollInterval <= 0 {
		opts.DiaryPollInterval = DefaultDiaryPollInterval
	}
	if opts.DiaryPollTimeout <= 0 {
		opts.DiaryPollTimeout = DefaultDiaryPollTimeout
	}
	return &Conversation{
		opts:        opts,
		userLimiter: userLimiter,
		live:        make(map[uuid.UUID]*conversationConn),
		sockets:     make(map[uuid.UUID]int),
	}, nil
}

// Shutdown은 열려 있는 연결을 모두 닫는다. 서버가 내려가기 시작할 때 부른다.
//
// net/http의 Shutdown은 넘겨받은 연결(WebSocket)을 닫아 주지 않는다. 여기서 닫지 않으면
// 대화를 열어 둔 사용자가 있는 동안에는 프로세스가 기다림의 끝까지 내려가지 못한다.
// 돌고 있던 턴에는 잠깐의 말미를 주고, 그래도 끝나지 않으면 컨텍스트를 취소한다. 대화는 열린 채로 남아
// 다시 연결하면 이어지고, 돌아오지 않으면 주기 작업이 닫는다.
func (c *Conversation) Shutdown(ctx context.Context) {
	c.mu.Lock()
	c.draining = true
	conns := make([]*conversationConn, 0, len(c.live))
	for _, conn := range c.live {
		conns = append(conns, conn)
	}
	c.mu.Unlock()

	// 하나씩 차례로 닫지 않는다. 연결마다 돌던 턴을 기다리는 유예와 닫는 인사가 붙어서, 열 사람이 이야기하던 중이면
	// 내려가는 데 주어진 시간을 통째로 넘긴다. 연결끼리 함께 쓰는 것이 없으므로 나란히 닫는다.
	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func(conn *conversationConn) {
			defer wg.Done()
			conn.close(ctx, websocket.StatusGoingAway, "server shutting down")
		}(conn)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	c.opts.Logger.LogAttrs(ctx, slog.LevelInfo, "conversation sockets drained",
		slog.Int("connections", len(conns)))
}

// reserveSocket은 사용자의 소켓 하나를 셈에 넣는다. 한도를 넘겼으면 거짓이다.
func (c *Conversation) reserveSocket(userID uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining || c.sockets[userID] >= maxSocketsPerUser {
		return false
	}
	c.sockets[userID]++
	return true
}

func (c *Conversation) releaseSocket(userID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := c.sockets[userID]; n <= 1 {
		delete(c.sockets, userID)
	} else {
		c.sockets[userID] = n - 1
	}
}

// serve는 연결 하나를 받아 끝날 때까지 맡는다. 로그인 확인은 앞의 미들웨어가 이미 했다.
func (c *Conversation) serve(ec *echo.Context) error {
	req := ec.Request()
	principal, ok := PrincipalFrom(req.Context())
	if !ok {
		return errUnauthenticated
	}

	c.mu.Lock()
	draining := c.draining
	c.mu.Unlock()
	if draining {
		// 내려가는 중에 올라온 연결이다. 열어 봐야 곧 닫힌다.
		return newProblem(http.StatusServiceUnavailable, ProblemCodeServiceUnavailable)
	}
	// 자리를 먼저 잡는다. 소켓을 올린 뒤에 세면 한도를 넘긴 연결도 고루틴과 파일 기술자를 이미 차지한 뒤다.
	if !c.reserveSocket(principal.User.ID) {
		return newProblem(http.StatusTooManyRequests, ProblemCodeRateLimited)
	}
	defer c.releaseSocket(principal.User.ID)

	// 출처는 앞의 미들웨어가 이미 보았다(crossOriginProtection이 연결을 여는 GET을 상태 변경 요청과 같게 다룬다).
	// 소켓 라이브러리의 검사는 꺼 둔다. 두 곳에서 각자 판단하면 한쪽을 고칠 때 다른 쪽이 조용히 남는다.
	socket, err := websocket.Accept(ec.Response(), req, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		// 연결을 올리지 못했다. 응답은 이미 나갔으므로 여기서 더 할 것이 없다.
		c.opts.Logger.LogAttrs(req.Context(), slog.LevelDebug, "conversation socket was not accepted",
			slog.String("request_id", httpserver.RequestID(req.Context())),
		)
		// 소켓 라이브러리가 이미 오류 응답을 썼다. 오류를 올리면 두 번 쓰게 된다.
		return nil
	}
	// 라이브러리가 먼저 연결을 끊어 버리면 한도를 넘겼다는 메시지를 보낼 수 없다. 크기는 직접 잰다.
	socket.SetReadLimit(-1)

	limiter, err := newTokenBuckets(c.opts.Clock, c.opts.MessageRate, 1)
	if err != nil {
		// 손잡기가 끝나 응답을 가로챘다. 오류를 올리면 닫힌 연결에 쓰려 한다.
		c.opts.Logger.LogAttrs(req.Context(), slog.LevelDebug, "conversation rate limiter was not built",
			slog.String("request_id", httpserver.RequestID(req.Context())),
		)
		_ = socket.Close(websocket.StatusInternalError, "")
		return nil
	}
	conn := &conversationConn{
		channel: c,
		socket:  socket,
		user:    engine.Participant{ID: principal.User.ID, Timezone: principal.User.Timezone},
		limiter: limiter,
		logger: c.opts.Logger.With(
			slog.String("user_id", principal.User.ID.String()),
			slog.String("request_id", httpserver.RequestID(req.Context())),
		),
	}
	// 연결이 살아 있는 동안은 요청의 컨텍스트를 그대로 쓴다. 클라이언트가 끊으면 이 컨텍스트가 끝나고
	// 돌고 있던 모델 호출이 모두 멈춘다.
	conn.run(req.Context())
	return nil
}

// register는 이 연결을 사용자의 자리에 앉힌다. 앞서 앉아 있던 연결은 물러난다.
//
// 열린 대화는 사용자마다 하나다. 두 연결이 한 대화를 함께 다루면 같은 말에 답이 둘 나가고,
// 무응답 타이머도 둘이 따로 돌아 한쪽이 대화를 닫아 버린다. 새 연결이 이기는 쪽으로 정한다.
// 사용자가 보고 있는 것은 방금 연 화면이기 때문이다.
func (c *Conversation) register(conn *conversationConn) (*conversationConn, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining {
		return nil, false
	}
	previous := c.live[conn.user.ID]
	c.live[conn.user.ID] = conn
	return previous, true
}

// unregister는 이 연결이 아직 사용자의 자리에 앉아 있을 때만 비운다.
// 이미 다른 연결이 앉았으면 그대로 둔다. 늦게 끝난 옛 연결이 새 연결을 지우지 않게 한다.
func (c *Conversation) unregister(conn *conversationConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live[conn.user.ID] == conn {
		delete(c.live, conn.user.ID)
	}
}

// conversationConn은 연결 하나다.
type conversationConn struct {
	channel *Conversation
	socket  *websocket.Conn
	user    engine.Participant
	limiter *tokenBuckets
	logger  *slog.Logger

	// session은 start를 받은 뒤에 채워진다.
	session *engine.Session
	// ended는 ended를 이미 내보냈는지다. 그 뒤로는 일기 소식만 기다린다.
	ended bool

	// writeMu는 나가는 메시지를 한 줄로 세운다. 소켓은 한 번에 하나만 쓸 수 있다.
	writeMu sync.Mutex

	// closeOnce는 닫는 일이 한 번만 일어나게 한다.
	closeOnce sync.Once

	// turnMu는 아래 두 값을 지킨다.
	turnMu sync.Mutex
	// turnCancel이 있으면 지금 턴이 돌고 있다는 뜻이다. 연결을 빼앗기거나 서버가 내려갈 때 이것으로 멈춘다.
	turnCancel context.CancelFunc
	// turnDone은 돌던 턴이 끝나면 닫힌다.
	turnDone chan struct{}
	// turnResult는 돌고 있는 턴의 결과를 받는다. 도는 턴이 없으면 nil이라 고르지 않는다.
	turnResult chan turnOutcome
}

// turnOutcome은 따로 돌린 턴 하나가 끝난 결과다.
type turnOutcome struct {
	clientMessageID uuid.UUID
	turn            engine.Turn
	err             error
}

// run은 연결이 끝날 때까지 메시지를 받아 처리한다.
//
// 턴은 따로 돌린다. 턴이 도는 동안에도 이 고리는 프레임을 계속 읽는다. 그러지 않으면 끝내기 단추가
// 모델이 답할 때까지 먹히지 않는다. 끝내기 버튼은 언제나 있어야 하고, 대화 모델은 몇십 초씩 걸릴 수 있다.
// 턴이 도는 동안 들어온 사용자의 글은 하나만 받아 두었다가 턴이 끝난 뒤에 처리해 순서를 지킨다.
func (c *conversationConn) run(ctx context.Context) {
	defer c.channel.unregister(c)
	defer func() { _ = c.socket.CloseNow() }()

	// 연결이 끊기거나 닫히면 이 컨텍스트가 끝난다. 돌던 모델 호출, ping, 무응답 타이머가 함께 물러난다.
	// 취소를 기다림보다 먼저 등록한다. defer는 쌓인 반대 순서로 도므로, 이 순서라야 취소가 먼저 돌고 그다음에 기다린다.
	// 반대로 두면 ping이 제 차례를 기다리는 동안 고루틴과 소켓이 그대로 남는다(운영 기본값으로 30초).
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.keepAlive(ctx)
	}()

	idle := newIdleTimers(c.channel.opts.IdleCheckAfter, c.channel.opts.IdleEndAfter)
	defer idle.stop()

	incoming := make(chan []byte)
	readErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 읽기가 끝났다는 것은 상대가 사라졌다는 뜻이다. 돌고 있던 모델 호출을 여기서 멈춘다.
		// 멈추지 않으면 탭을 닫고 간 사람을 위해 돈이 드는 호출이 끝까지 돌고, 다시 열었을 때 그만큼 기다리게 된다.
		defer cancel()
		defer close(incoming)
		readErr <- c.readLoop(ctx, incoming)
	}()

	// deferred는 턴이 도는 동안 받아 둔 메시지다. 하나를 들고 있는 동안에는 소켓에서 더 읽지 않는다.
	var deferred any
	for {
		frames := incoming
		if deferred != nil {
			frames = nil
		}
		select {
		case <-ctx.Done():
			return

		case outcome := <-c.turnResult:
			if !c.finishTurn(ctx, outcome) {
				return
			}
			if deferred != nil {
				message := deferred
				deferred = nil
				if !c.dispatch(ctx, message) {
					return
				}
			}
			idle.restart(c.session != nil && !c.ended)

		case data, ok := <-frames:
			if !ok {
				c.reportRead(ctx, readErr)
				return
			}
			message, ok := c.decodeFrame(ctx, data)
			if !ok {
				continue
			}
			// 턴이 도는 동안에도 끝내기는 바로 받는다. 나머지는 차례를 지키려고 뒤로 미룬다.
			if _, isEnd := message.(WsEnd); c.turnRunning() && !isEnd {
				deferred = message
				continue
			}
			if !c.dispatch(ctx, message) {
				return
			}
			idle.restart(c.session != nil && !c.ended && !c.turnRunning())

		case <-idle.check():
			// 3분쯤 말이 없다. 한 번 묻고 다시 기다린다.
			if err := c.channel.opts.Engine.Nudge(ctx, c.session); err != nil {
				c.finishAfterEngineError(ctx, err, nil)
				return
			}
			idle.armEnd()

		case <-idle.end():
			// 물어도 답이 없다. 사유는 무응답이다. 일기 초안 작업은 그대로 돈다.
			if !c.endConversation(ctx, store.EndReasonIdle) {
				return
			}
			idle.stop()
		}
	}
}

// reportRead는 읽기가 끝난 까닭을 남긴다. 주고받은 글은 담지 않는다.
func (c *conversationConn) reportRead(ctx context.Context, readErr <-chan error) {
	select {
	case err := <-readErr:
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		if status := websocket.CloseStatus(err); status != -1 {
			c.logger.LogAttrs(ctx, slog.LevelDebug, "conversation socket closed",
				slog.Int("status", int(status)))
			return
		}
		c.logger.LogAttrs(ctx, slog.LevelDebug, "conversation socket read stopped")
	default:
	}
}

// readLoop는 메시지를 읽어 넘긴다. 크기 한도는 여기서 잰다.
func (c *conversationConn) readLoop(ctx context.Context, out chan<- []byte) error {
	limit := c.channel.opts.MaxMessageBytes
	for {
		typ, reader, err := c.socket.Reader(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			// 남은 바이트를 버리지 않으면 다음 프레임의 머리를 이 바이트에서 읽는다. 그러면 규약 위반으로 연결이 끊긴다.
			n, err := io.Copy(io.Discard, io.LimitReader(reader, limit+1))
			if err != nil {
				return err
			}
			if n > limit {
				// 한도를 넘겼다. 남은 바이트는 읽지 않는다. 글일 때와 똑같이 알리고 닫는다.
				c.sendError(ctx, WsErrorCodeMessageTooLarge, nil)
				c.close(ctx, websocket.StatusMessageTooBig, "message too large")
				return nil
			}
			c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
			continue
		}
		// 한도보다 한 바이트를 더 읽어 본다. 더 있으면 한도를 넘긴 것이다.
		data, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > limit {
			// 남은 바이트는 읽지 않는다. 알리고 바로 닫는다.
			c.sendError(ctx, WsErrorCodeMessageTooLarge, nil)
			c.close(ctx, websocket.StatusMessageTooBig, "message too large")
			return nil
		}
		select {
		case out <- data:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// keepAlive는 조용한 연결이 앞단에서 끊기지 않게 ping을 보낸다. 답이 없으면 연결을 닫는다.
func (c *conversationConn) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(c.channel.opts.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, c.channel.opts.PingTimeout)
			err := c.socket.Ping(pingCtx)
			cancel()
			if err != nil {
				c.close(ctx, websocket.StatusPolicyViolation, "ping timed out")
				return
			}
		}
	}
}

// decodeFrame은 프레임 하나를 풀어 본다. 두 번째 값이 거짓이면 이미 알렸고 더 볼 것이 없다.
func (c *conversationConn) decodeFrame(ctx context.Context, data []byte) (any, bool) {
	if allowed, _ := c.limiter.take("connection"); !allowed {
		// 어느 글인지는 모른 채로 거절한다. 풀어 보기 전에 막는 것이 한도의 뜻이다.
		c.sendError(ctx, WsErrorCodeRateLimited, nil)
		return nil, false
	}

	var envelope WsClientMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
		return nil, false
	}
	// 풀어낸 오류의 문구에는 클라이언트가 보낸 type이 그대로 들어 있다. 로그에도 응답에도 옮기지 않는다.
	message, err := envelope.ValueByDiscriminator()
	if err != nil {
		c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
		return nil, false
	}
	return message, true
}

// dispatch는 풀어낸 메시지 하나를 다룬다. 연결을 이어가면 true다.
func (c *conversationConn) dispatch(ctx context.Context, message any) bool {
	switch m := message.(type) {
	case WsStart:
		return c.handleStart(ctx, m)
	case WsUserText:
		return c.handleUserText(ctx, m)
	case WsEnd:
		if c.session == nil {
			c.sendError(ctx, WsErrorCodeNotStarted, nil)
			return true
		}
		// 끝내기는 돌고 있는 턴을 먼저 멈춘다. 엔진의 End는 그 턴이 쥔 잠금을 기다리기 때문에,
		// 멈추지 않고 부르면 모델이 답할 때까지 끝내기가 먹히지 않는다.
		c.stopTurn(ctx)
		return c.endConversation(ctx, store.EndReasonUser)
	default:
		c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
		return true
	}
}

func (c *conversationConn) handleStart(ctx context.Context, m WsStart) bool {
	if c.session != nil {
		c.sendError(ctx, WsErrorCodeAlreadyStarted, nil)
		return true
	}
	if m.Mode != ConversationModeChat {
		// 음성은 아직 이 채널로 받지 않는다.
		c.sendError(ctx, WsErrorCodeUnsupportedMode, nil)
		return true
	}

	previous, ok := c.channel.register(c)
	if !ok {
		c.close(ctx, websocket.StatusGoingAway, "server shutting down")
		return false
	}
	if previous != nil {
		// 앞선 연결이 돌리던 턴은 바로 멈추고, 그 턴이 끝난 것을 확인한 뒤에 대화를 연다.
		// 방금 화면을 연 사람은 옛 화면의 답을 기다리지 않는다. 다만 순서는 지켜야 한다.
		// 옛 턴이 남기는 위기 판정이 여기서 읽는 것보다 늦게 쓰이면, 새 화면에 도움 자원이 고정되지 않는다.
		previous.waitForTurn(ctx, 0)
		// 닫는 인사는 이미 사라진 화면과 주고받는 것이라 기다릴 까닭이 없다.
		go previous.shutSocket(statusTakenOver, "another connection took over")
	}

	session, err := c.channel.opts.Engine.Start(ctx, engine.StartInput{
		User: c.user,
		Mode: store.ModeChat,
		Sink: engine.SinkFunc(c.emit),
	})
	if err != nil {
		c.finishAfterEngineError(ctx, err, nil)
		return false
	}
	c.session = session
	return true
}

func (c *conversationConn) handleUserText(ctx context.Context, m WsUserText) bool {
	if c.session == nil {
		c.sendError(ctx, WsErrorCodeNotStarted, &m.ClientMessageID)
		return true
	}
	if c.ended {
		c.sendError(ctx, WsErrorCodeConversationEnded, &m.ClientMessageID)
		return true
	}
	// 돈이 드는 것은 글 하나마다 모델을 두세 번 부르는 이 길이다. 사용자별 한도는 여기서만 센다.
	if allowed, _ := c.channel.userLimiter.take(c.user.ID.String()); !allowed {
		c.sendError(ctx, WsErrorCodeRateLimited, &m.ClientMessageID)
		return true
	}

	c.beginTurn(ctx, m)
	return true
}

// beginTurn은 턴 하나를 따로 돌린다. 결과는 run의 고리가 turnResult로 받는다.
// 연결을 빼앗기거나 끝내기가 들어오거나 서버가 내려갈 때 이 컨텍스트가 취소된다.
func (c *conversationConn) beginTurn(ctx context.Context, m WsUserText) {
	turnCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	result := make(chan turnOutcome, 1)

	c.turnMu.Lock()
	c.turnCancel, c.turnDone = cancel, done
	c.turnMu.Unlock()
	c.turnResult = result

	go func() {
		defer close(done)
		turn, err := c.channel.opts.Engine.Handle(turnCtx, c.session, engine.Say{
			ClientMessageID: m.ClientMessageID,
			Text:            m.Text,
		})
		result <- turnOutcome{clientMessageID: m.ClientMessageID, turn: turn, err: err}
	}()
}

// finishTurn은 끝난 턴을 마무리한다. 연결을 이어가면 true다.
func (c *conversationConn) finishTurn(ctx context.Context, outcome turnOutcome) bool {
	c.clearTurn()
	if outcome.err != nil {
		c.finishAfterEngineError(ctx, outcome.err, &outcome.clientMessageID)
		return !c.ended && !isFatalEngineError(outcome.err)
	}
	// 턴이 끝났다는 줄은 엔진이 남긴다. 여기서 한 번 더 남기면 같은 줄이 둘이 되어 턴의 수를 두 배로 읽게 된다.
	return true
}

// turnRunning은 지금 턴이 돌고 있는지다.
func (c *conversationConn) turnRunning() bool {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	return c.turnDone != nil
}

// clearTurn은 끝난 턴의 자리를 비운다.
func (c *conversationConn) clearTurn() {
	c.turnMu.Lock()
	cancel := c.turnCancel
	c.turnCancel, c.turnDone = nil, nil
	c.turnMu.Unlock()
	c.turnResult = nil
	if cancel != nil {
		cancel()
	}
}

// stopTurn은 돌고 있는 턴을 바로 멈추고 끝날 때까지 기다린다. 도는 턴이 없으면 그냥 돌아온다.
func (c *conversationConn) stopTurn(ctx context.Context) {
	c.waitForTurn(ctx, 0)
	select {
	case <-c.turnResult:
	default:
	}
	c.clearTurn()
}

// endConversation은 대화를 끝내고, 초안을 기다릴 것이 있으면 기다렸다가 알린다. 연결을 이어가면 true다.
func (c *conversationConn) endConversation(ctx context.Context, reason string) bool {
	if c.ended {
		c.sendError(ctx, WsErrorCodeConversationEnded, nil)
		return true
	}
	// 끝내기는 돌고 있는 턴이 끝난 뒤에 이뤄진다(엔진이 같은 잠금으로 줄을 세운다).
	if err := c.channel.opts.Engine.End(ctx, c.session, reason); err != nil {
		c.finishAfterEngineError(ctx, err, nil)
		return false
	}
	c.ended = true
	// 여기서부터는 사용자의 글을 더 받지 않는다. 일기 소식만 기다린다.
	c.awaitDiary(ctx)
	c.close(ctx, websocket.StatusNormalClosure, "conversation ended")
	return false
}

// awaitDiary는 일기 초안 작업이 끝날 때까지 대화의 진행 상태를 짧게 살핀다.
//
// 작업은 다른 프로세스(작업자)에서 돈다. 끝났다고 그날의 일기가 생긴 것은 아니다(위기 대응이 있었던 대화,
// 사용자가 한 말이 없는 대화). 그래서 일기가 있는지를 한 번 더 확인하고, 있을 때만 알린다.
func (c *conversationConn) awaitDiary(ctx context.Context) {
	opts := c.channel.opts
	deadline, cancel := context.WithTimeout(ctx, opts.DiaryPollTimeout)
	defer cancel()

	ticker := time.NewTicker(opts.DiaryPollInterval)
	defer ticker.Stop()

	for {
		finished, err := c.diaryFinished(deadline)
		switch {
		case err != nil:
			c.logger.LogAttrs(ctx, slog.LevelWarn, "diary readiness cannot be read",
				slog.String("conversation_id", c.session.ConversationID().String()))
			return
		case finished:
			c.emitDiaryReady(ctx)
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.Done():
			// 기다릴 만큼 기다렸다. 웹앱이 일기를 직접 읽어 본다.
			return
		}
	}
}

// diaryFinished는 이 대화의 초안 작업이 끝났고 그날에 일기가 남았는지 본다.
func (c *conversationConn) diaryFinished(ctx context.Context) (bool, error) {
	queries := c.channel.opts.Store.Queries()
	row, err := queries.GetConversation(ctx, db.GetConversationParams{
		ID: c.session.ConversationID(), UserID: c.user.ID,
	})
	if err != nil {
		return false, err
	}
	if !diary.Finished(row.Conversation.ProcessingStatus) {
		return false, nil
	}
	if _, err := queries.GetDiaryByDayID(ctx, db.GetDiaryByDayIDParams{
		DayID: c.session.DayID(), UserID: c.user.ID,
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 초안을 만들지 않는 대화였다. 알릴 것이 없다.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *conversationConn) emitDiaryReady(ctx context.Context) {
	var message WsServerMessage
	if err := message.FromWsDiaryReady(WsDiaryReady{RecordDate: apiDate(c.session.RecordDate())}); err != nil {
		return
	}
	c.write(ctx, message)
}

// emit은 엔진이 낸 사건을 명세의 메시지로 바꿔 내보낸다. 사건의 종류와 메시지의 종류가 하나씩 짝을 이룬다.
// 관문의 판정 단계는 어느 메시지에도 실리지 않는다.
func (c *conversationConn) emit(ctx context.Context, event engine.Event) error {
	var message WsServerMessage
	var err error
	switch e := event.(type) {
	case engine.Ready:
		err = message.FromWsReady(WsReady{
			ConversationID:  e.ConversationID,
			RecordDate:      apiDate(e.RecordDate),
			Resumed:         e.Resumed,
			Utterances:      utteranceList(e.Utterances),
			ResourcesPinned: e.ResourcesPinned,
		})
	case engine.Accepted:
		err = message.FromWsThinking(WsThinking{ClientMessageID: e.ClientMessageID, Seq: seqOf(e.Seq)})
	case engine.AIText:
		err = message.FromWsAIText(WsAIText{Seq: seqOf(e.Seq), Text: e.Text, Origin: WsOrigin(e.Origin)})
	case engine.Resources:
		err = message.FromWsResources(WsResources{Items: resourceList(e.Items)})
	case engine.Ended:
		reason := WsEndReason(e.Reason)
		if !reason.Valid() {
			// 화면에 보여줄 수 없는 사유다(오류, 위기). 사용자에게는 서버가 닫았다는 뜻으로 알린다.
			reason = WsEndReasonIdle
		}
		err = message.FromWsEnded(WsEnded{
			Reason:        reason,
			RecordDate:    apiDate(e.RecordDate),
			DiaryExpected: e.DiaryExpected,
		})
	default:
		return fmt.Errorf("api: unknown conversation event %T", event)
	}
	if err != nil {
		return fmt.Errorf("api: build conversation message: %w", err)
	}
	return c.writeErr(ctx, message)
}

// sendError는 오류 메시지 하나를 내보낸다. 사용자가 쓴 글은 담지 않는다.
func (c *conversationConn) sendError(ctx context.Context, code WsErrorCode, clientMessageID *uuid.UUID) {
	var message WsServerMessage
	if err := message.FromWsError(WsError{Code: code, ClientMessageID: clientMessageID}); err != nil {
		return
	}
	c.logger.LogAttrs(ctx, slog.LevelDebug, "conversation error sent", slog.String("code", string(code)))
	c.write(ctx, message)
}

func (c *conversationConn) write(ctx context.Context, message WsServerMessage) {
	_ = c.writeErr(ctx, message)
}

// writeErr은 메시지 하나를 내보낸다. 소켓은 한 번에 하나만 쓸 수 있어서 줄을 세운다.
func (c *conversationConn) writeErr(ctx context.Context, message WsServerMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("api: encode conversation message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// 이미 끝난 컨텍스트(연결이 끊겼다)로도 써 보지 않는다. 다만 쓰는 데 주는 시간은 컨텍스트와 따로 둔다.
	if err := ctx.Err(); err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := c.socket.Write(writeCtx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("api: write conversation message: %w", err)
	}
	return nil
}

// finishAfterEngineError는 엔진이 돌려준 오류를 클라이언트가 알아들을 코드로 바꿔 알린다.
// 오류의 문구는 보내지 않는다. 사용자의 글이 섞여 있을 수 있기 때문이다.
func (c *conversationConn) finishAfterEngineError(ctx context.Context, err error, clientMessageID *uuid.UUID) {
	switch {
	case errors.Is(err, context.Canceled):
		// 사용자가 연결을 끊었거나 이 턴이 밀려났다. 알릴 곳이 없다.
		return

	case errors.Is(err, engine.ErrGone):
		c.logger.LogAttrs(ctx, slog.LevelWarn, "conversation user is gone")
		c.sendError(ctx, WsErrorCodeInternalError, clientMessageID)
		c.close(ctx, statusGone, "account is gone")

	case errors.Is(err, engine.ErrConversationEnded):
		c.ended = true
		c.sendError(ctx, WsErrorCodeConversationEnded, clientMessageID)

	case errors.Is(err, engine.ErrUnsupportedMode):
		c.sendError(ctx, WsErrorCodeUnsupportedMode, clientMessageID)

	case errors.Is(err, engine.ErrEmptyText), errors.Is(err, engine.ErrNoClientMessageID):
		c.sendError(ctx, WsErrorCodeInvalidMessage, clientMessageID)

	default:
		c.logger.LogAttrs(ctx, slog.LevelError, "conversation turn failed",
			slog.String("error", failureName(err)))
		c.sendError(ctx, WsErrorCodeInternalError, clientMessageID)
	}
}

// isFatalEngineError는 그 오류 뒤로 이 연결을 이어갈 수 없는지다.
func isFatalEngineError(err error) bool {
	return errors.Is(err, engine.ErrGone) || errors.Is(err, context.Canceled)
}

// failureName은 오류를 로그에 남길 짧은 이름으로 바꾼다. 오류의 문구는 남기지 않는다.
// 엔진과 그 아래에서 온 오류에는 사용자의 글이나 쿼리 조각이 섞여 있을 수 있다.
func failureName(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	default:
		return "internal"
	}
}

// close는 돌던 턴을 정리하고 연결을 닫는다. 몇 번을 불러도 한 번만 닫힌다.
// 돌고 있던 턴에는 잠깐의 말미를 주고, 그래도 끝나지 않으면 컨텍스트를 취소한다.
func (c *conversationConn) close(ctx context.Context, status websocket.StatusCode, reason string) {
	c.waitForTurn(ctx, shutdownGrace)
	c.shutSocket(status, reason)
}

// shutSocket은 닫는 인사를 주고받고 연결을 끝낸다. 돌던 턴은 보지 않는다.
func (c *conversationConn) shutSocket(status websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		// 닫는 인사를 기다리다 걸리더라도 연결은 끝난다. CloseNow가 뒤에서 마무리한다.
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = c.socket.Close(status, reason)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			_ = c.socket.CloseNow()
		}
	})
}

// waitForTurn은 돌고 있는 턴이 끝나기를 grace만큼 기다리고, 그래도 끝나지 않으면 취소하고 기다린다.
// grace가 0이면 바로 취소한다. ctx가 먼저 끝나면 말미도 거기서 끊는다.
func (c *conversationConn) waitForTurn(ctx context.Context, grace time.Duration) {
	c.turnMu.Lock()
	cancel, done := c.turnCancel, c.turnDone
	c.turnMu.Unlock()
	if done == nil {
		return
	}
	if grace > 0 {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	if cancel != nil {
		cancel()
	}
	<-done
}

// idleTimers는 무응답을 재는 두 개의 타이머다. 시각을 읽지 않고 흐른 시간만 잰다.
//
// 처음 IdleCheckAfter 동안 말이 없으면 한 번 묻고, 그 뒤로 IdleEndAfter 동안 답이 없으면 대화를 끝낸다.
// 사용자의 말이 오면 처음부터 다시 잰다.
type idleTimers struct {
	checkAfter time.Duration
	endAfter   time.Duration
	checkTimer *time.Timer
	endTimer   *time.Timer
}

func newIdleTimers(checkAfter, endAfter time.Duration) *idleTimers {
	t := &idleTimers{
		checkAfter: checkAfter,
		endAfter:   endAfter,
		checkTimer: time.NewTimer(checkAfter),
		endTimer:   time.NewTimer(endAfter),
	}
	// 대화가 열리기 전에는 재지 않는다. 처음 말이 오갈 때 restart가 건다.
	stopTimer(t.checkTimer)
	stopTimer(t.endTimer)
	return t
}

func (t *idleTimers) check() <-chan time.Time { return t.checkTimer.C }
func (t *idleTimers) end() <-chan time.Time   { return t.endTimer.C }

// restart는 처음부터 다시 잰다. active가 거짓이면 아예 재지 않는다.
func (t *idleTimers) restart(active bool) {
	stopTimer(t.checkTimer)
	stopTimer(t.endTimer)
	if active {
		t.checkTimer.Reset(t.checkAfter)
	}
}

// armEnd는 한 번 물은 뒤 끝내기까지를 잰다.
func (t *idleTimers) armEnd() {
	stopTimer(t.endTimer)
	t.endTimer.Reset(t.endAfter)
}

func (t *idleTimers) stop() {
	stopTimer(t.checkTimer)
	stopTimer(t.endTimer)
}

// stopTimer는 타이머를 멈추고, 이미 울려서 남아 있는 값을 비운다.
func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func utteranceList(in []engine.Utterance) []WsUtterance {
	out := make([]WsUtterance, 0, len(in))
	for _, u := range in {
		out = append(out, WsUtterance{
			Seq:             seqOf(u.Seq),
			Speaker:         WsSpeaker(u.Speaker),
			Origin:          WsOrigin(u.Origin),
			Text:            u.Text,
			ClientMessageID: u.ClientMessageID,
			CreatedAt:       NewUTCTime(u.CreatedAt),
		})
	}
	return out
}

// seqOf는 순번을 명세의 타입으로 옮긴다. 순번은 발화의 수만큼만 늘어나므로 넘칠 일이 없지만 상한에서 자른다.
func seqOf(seq int32) int {
	if seq < 0 {
		return 0
	}
	return int(min(int64(seq), math.MaxInt32))
}
