package soniox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

const (
	// readLimit은 공급자가 보내는 메시지 하나의 상한이다. 토큰 응답은 몇 KB지만 긴 마디가 한 번에 확정되면 더 커질 수 있다.
	readLimit = 1 << 20
	// eventBuffer는 받는 쪽이 잠깐 늦어도 읽기가 막히지 않게 하는 여유다. 중간 결과는 초에 몇 번씩 온다.
	eventBuffer = 64
	// closeTimeout은 끝 프레임과 닫는 인사에 주는 시간이다. 그 안에 끝나지 않으면 연결을 그냥 끊는다.
	closeTimeout = 2 * time.Second
	// keepaliveWriteTimeout은 keepalive 하나를 쓰는 데 주는 시간이다.
	keepaliveWriteTimeout = 5 * time.Second

	// 공급자가 토큰의 글로 보내는 표시다. 끝점(<end>)은 말이 끝났다는 뜻이고, <fin>은 finalize 요청을 처리했다는 뜻이다.
	endToken = "<end>"
	finToken = "<fin>"
)

var (
	// 공급자에 보내는 제어 메시지다. 둘 다 글(JSON) 프레임으로 간다.
	keepaliveMessage = []byte(`{"type":"keepalive"}`)
	finalizeMessage  = []byte(`{"type":"finalize"}`)

	// ErrConnectionLost는 공급자와의 연결이 Close 없이 끊겼다는 뜻이다.
	ErrConnectionLost = errors.New("soniox: connection lost")
	// ErrMalformedResponse는 공급자의 메시지를 읽을 수 없었다는 뜻이다. 원문은 오류에 넣지 않는다.
	ErrMalformedResponse = errors.New("soniox: malformed response")
)

// Error는 공급자가 오류 응답으로 알린 실패다. 코드와 종류만 담고 문구(error_message)는 담지 않는다.
type Error struct {
	Code int
	Type string
}

func (e *Error) Error() string {
	return "soniox: server error " + strconv.Itoa(e.Code) + " (" + e.Type + ")"
}

// response는 공급자가 보내는 메시지다. 쓰는 항목만 읽는다. error_message와 request_id는 일부러 읽지 않는다.
type response struct {
	Tokens    []token `json:"tokens"`
	Finished  bool    `json:"finished"`
	ErrorCode int     `json:"error_code"`
	ErrorType string  `json:"error_type"`
}

func (r response) isError() bool { return r.ErrorCode != 0 || r.ErrorType != "" }

// Stream은 voice.RecognitionStream을 구현한다. 연결 하나에 읽기 고루틴과 keepalive 고루틴이 붙는다.
type Stream struct {
	conn   *websocket.Conn
	logger *slog.Logger
	events chan voice.Event
	// ctx는 스트림이 사는 동안 쓰는 컨텍스트다. 스트림은 Open이 돌아온 뒤에도 살므로 Open의 ctx에서 취소는 떼고
	// 값(요청 ID 같은 로그 속성)만 물려받는다.
	ctx context.Context

	keepaliveInterval time.Duration

	// writeMu는 소켓 쓰기를 한 줄로 세운다. 소리, finalize, keepalive, 끝 프레임이 서로 다른 고루틴에서 온다.
	writeMu   sync.Mutex
	closed    atomic.Bool
	closeOnce sync.Once
	// done은 Close가 닫는다. keepalive 고루틴이 멈추고, 받는 쪽이 없어도 사건을 넘기던 읽기 고루틴이 풀린다.
	done chan struct{}
	// readerDone은 읽기 고루틴이 끝나면 닫힌다. 연결이 끝났는데 keepalive만 남아 있지 않게 한다.
	readerDone chan struct{}
	// audioWritten은 소리를 보낼 때마다 알린다. 가득 차 있으면 버린다(한 번만 알려도 타이머는 되돌아간다).
	audioWritten chan struct{}
	wg           sync.WaitGroup

	// seg는 읽기 고루틴만 만진다.
	seg segment

	bytesWritten  atomic.Int64
	framesWritten atomic.Int64
	tokensRead    atomic.Int64
}

var _ voice.RecognitionStream = (*Stream)(nil)

func newStream(ctx context.Context, conn *websocket.Conn, logger *slog.Logger, keepaliveInterval time.Duration) *Stream {
	return &Stream{
		conn:              conn,
		logger:            logger,
		events:            make(chan voice.Event, eventBuffer),
		ctx:               context.WithoutCancel(ctx),
		keepaliveInterval: keepaliveInterval,
		done:              make(chan struct{}),
		readerDone:        make(chan struct{}),
		audioWritten:      make(chan struct{}, 1),
	}
}

func (s *Stream) start() {
	s.wg.Add(2)
	go s.readLoop(s.ctx)
	go s.keepaliveLoop(s.ctx)
}

// Write는 소리 한 조각을 바이너리 프레임으로 보낸다. 빈 조각은 보내지 않는다. 빈 프레임은 스트림의 끝이라는 뜻이기 때문이다.
// ctx가 쓰는 도중에 끝나면 연결이 통째로 닫히므로, 짧지 않은 기한을 준다.
func (s *Stream) Write(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		if s.closed.Load() {
			return voice.ErrStreamClosed
		}
		return nil
	}
	if err := s.send(ctx, websocket.MessageBinary, pcm); err != nil {
		return err
	}
	s.bytesWritten.Add(int64(len(pcm)))
	s.framesWritten.Add(1)
	select {
	case s.audioWritten <- struct{}{}:
	default:
	}
	return nil
}

// Finalize는 지금까지 들은 말을 바로 확정하게 한다. 공급자는 확정 토큰 뒤에 <fin> 표시를 보낸다.
func (s *Stream) Finalize(ctx context.Context) error {
	return s.send(ctx, websocket.MessageText, finalizeMessage)
}

func (s *Stream) Events() <-chan voice.Event { return s.events }

// send는 닫힌 뒤의 쓰기를 막고 소켓 쓰기를 한 줄로 세운다.
func (s *Stream) send(ctx context.Context, typ websocket.MessageType, data []byte) error {
	if s.closed.Load() {
		return voice.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return voice.ErrStreamClosed
	}
	if err := s.conn.Write(ctx, typ, data); err != nil {
		// 소켓 오류에는 보낸 내용이 들어가지 않는다. 연결이 이미 죽었으면 읽기 고루틴이 Failed로 알린다.
		return fmt.Errorf("soniox: write: %w", err)
	}
	return nil
}

// Close는 스트림의 끝을 알리고 연결을 닫는다. 몇 번을 불러도 한 번만 닫힌다. 돌아올 때 고루틴은 모두 끝나 있고 사건 채널은 닫혀 있다.
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.done)

		ctx, cancel := context.WithTimeout(s.ctx, closeTimeout)
		defer cancel()

		// 빈 프레임은 "소리가 끝났다"는 뜻이다. 공급자는 남은 토큰과 finished를 보내고 연결을 닫는다. 받지 못해도 괜찮다.
		s.writeMu.Lock()
		_ = s.conn.Write(ctx, websocket.MessageBinary, nil)
		s.writeMu.Unlock()

		s.shutSocket(ctx)
		s.wg.Wait()

		s.logger.LogAttrs(ctx, slog.LevelDebug, "recognition stream closed",
			slog.Int64("bytes_written", s.bytesWritten.Load()),
			slog.Int64("frames_written", s.framesWritten.Load()),
			slog.Int64("tokens_read", s.tokensRead.Load()))
	})
	return nil
}

// shutSocket은 닫는 인사를 주고받고 연결을 끝낸다. 인사를 기다리다 기한이 지나면 그냥 끊는다.
func (s *Stream) shutSocket(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = s.conn.CloseNow()
		<-done
	}
}

// readLoop는 공급자의 메시지를 읽어 사건으로 바꾼다. 끝나면 사건 채널을 닫는다. 채널은 여기서만 닫는다.
func (s *Stream) readLoop(ctx context.Context) {
	defer s.wg.Done()
	defer close(s.events)
	defer close(s.readerDone)

	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			if s.closed.Load() {
				return
			}
			s.fail(ctx, connectionError(err))
			return
		}
		if typ != websocket.MessageText {
			// 공급자는 글(JSON)만 보낸다.
			continue
		}

		var resp response
		if err := json.Unmarshal(data, &resp); err != nil {
			s.fail(ctx, ErrMalformedResponse)
			return
		}
		if resp.isError() {
			s.fail(ctx, &Error{Code: resp.ErrorCode, Type: safeErrorType(resp.ErrorType)})
			return
		}

		s.tokensRead.Add(int64(len(resp.Tokens)))
		for _, ev := range s.seg.consume(resp.Tokens) {
			if !s.emit(ev) {
				return
			}
		}
		if resp.Finished {
			s.logger.LogAttrs(ctx, slog.LevelDebug, "recognition stream finished by provider")
			s.disconnect()
			return
		}
	}
}

// fail은 실패를 알리고 연결을 끊는다. 뒤이어 readLoop가 채널을 닫는다.
func (s *Stream) fail(ctx context.Context, err error) {
	var serverErr *Error
	if errors.As(err, &serverErr) {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "recognition stream failed",
			slog.Int("error_code", serverErr.Code), slog.String("error_type", serverErr.Type))
	} else {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "recognition stream failed", slog.String("reason", err.Error()))
	}
	s.emit(voice.Event{Kind: voice.EventFailed, Err: err})
	s.disconnect()
}

// disconnect는 공급자 쪽에서 끝난 연결을 우리 쪽에서도 바로 놓는다. Close가 뒤에 와도 된다.
func (s *Stream) disconnect() {
	if s.closed.Load() {
		return
	}
	_ = s.conn.CloseNow()
}

// emit은 사건을 넘긴다. 받는 쪽이 늦으면 기다리되, 닫히는 중이면 버리고 false를 돌려준다.
func (s *Stream) emit(ev voice.Event) bool {
	if s.closed.Load() {
		return false
	}
	select {
	case s.events <- ev:
		return true
	case <-s.done:
		return false
	}
}

// keepaliveLoop는 소리가 keepaliveInterval 동안 오지 않을 때마다 keepalive를 보낸다.
// 사용자가 말을 멈추고 AI의 답을 듣는 동안에도 소리는 계속 올라오지만, 브라우저가 멈추거나 잠깐 끊긴 사이에 연결을 잃지 않게 한다.
func (s *Stream) keepaliveLoop(ctx context.Context) {
	defer s.wg.Done()

	timer := time.NewTimer(s.keepaliveInterval)
	defer timer.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.readerDone:
			return
		case <-s.audioWritten:
			timer.Reset(s.keepaliveInterval)
		case <-timer.C:
			if err := s.sendKeepalive(ctx); err != nil {
				// 닫혔거나 연결이 죽은 것이다. 죽은 연결은 읽기 고루틴이 알린다.
				return
			}
			timer.Reset(s.keepaliveInterval)
		}
	}
}

func (s *Stream) sendKeepalive(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, keepaliveWriteTimeout)
	defer cancel()
	if err := s.send(ctx, websocket.MessageText, keepaliveMessage); err != nil {
		return err
	}
	s.logger.LogAttrs(ctx, slog.LevelDebug, "keepalive sent")
	return nil
}

// connectionError는 소켓 읽기 오류를 밖에 내도 되는 오류로 바꾼다.
// 닫는 사유 문구는 공급자가 쓴 것이라 옮기지 않고 상태 코드만 남긴다. 그 밖의 오류(EOF, 네트워크)는 내용이 없으므로 감싼다.
func connectionError(err error) error {
	if status := websocket.CloseStatus(err); status != -1 {
		return fmt.Errorf("%w: close status %d", ErrConnectionLost, status)
	}
	return fmt.Errorf("%w: %w", ErrConnectionLost, err)
}

// safeErrorType은 오류 종류가 정해진 이름(unauthenticated, service_unavailable 같은)일 때만 그대로 쓴다.
func safeErrorType(t string) string {
	if len(t) == 0 || len(t) > 64 {
		return "unknown"
	}
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return "unknown"
		}
	}
	return t
}
