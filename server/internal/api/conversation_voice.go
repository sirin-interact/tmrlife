package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

// 음성 방식의 한도와 조정 값이다. 소리의 꼴(샘플레이트)은 voice 패키지가 정한다.
const (
	// maxAudioFrameBytes는 올라오는 소리 프레임 하나의 최대 크기다. 웹앱은 100ms(3,200바이트)씩 보낸다.
	maxAudioFrameBytes = 32 << 10
	// audioRateBytesPerSecond는 초당 받는 소리의 한도다. 실시간의 두 배면 밀린 소리를 따라잡기에 넉넉하고,
	// 소리를 흘려 넣어 인식 비용을 늘리는 길은 막힌다. 한꺼번에는 1초 분량의 두 배까지 받는다.
	audioRateBytesPerSecond = voice.InputBytesPerSecond * 2
	audioBurstBytes         = audioRateBytesPerSecond
	// playbackChunkBytes는 내려보내는 소리 조각의 크기다. 24kHz s16le로 100ms다.
	playbackChunkBytes = voice.OutputSampleRate * 2 / 10
	// bargeInMinRunes는 끼어든 것으로 보는 중간 결과의 최소 글자 수다. 한 글자짜리 잡음으로 말을 끊지 않는다.
	bargeInMinRunes = 2
	// echoWindow는 AI의 말이 끝난 뒤에도 그 말의 메아리로 볼 시간이다. 스피커 소리가 마이크로 되돌아오는 끝자락이다.
	echoWindow = 1500 * time.Millisecond
	// echoMinRunesAfterPlayback은 재생이 끝난 뒤의 메아리로 보려면 필요한 최소 글자 수다.
	// "네", "응" 같은 짧은 대답이 AI의 말 어딘가에 든 글자라는 이유로 버려지면 안 된다.
	echoMinRunesAfterPlayback = 4
	// voiceSignalBuffer는 인식 사건을 주 고리에 넘기는 통의 크기다.
	voiceSignalBuffer = 32
	// voiceOpenTimeout은 인식 스트림을 여는 데 주는 시간이다.
	voiceOpenTimeout = 10 * time.Second
)

// voiceSignal은 인식 스트림이 낸 사건을 주 고리에 넘기는 꾸러미다. 어느 세션의 것인지 함께 실어서,
// 이미 닫힌 세션의 사건이 뒤늦게 와도 가려낼 수 있다.
type voiceSignal struct {
	session *voiceSession
	event   voice.Event
}

// playback은 지금 읽어 주고 있는 말 하나다.
type playback struct {
	seq    int32
	text   string
	cancel context.CancelFunc
	done   chan struct{}
}

// pendingSpeech는 턴이 도는 동안 들어온 말이다. 턴이 끝나면 한 마디로 합쳐 넣는다.
type pendingSpeech struct {
	texts      []string
	confidence float32
}

// voiceSession은 연결 하나의 음성 방식이다. 인식 스트림 하나와 지금 읽어 주는 말 하나를 들고 있다.
//
// 소리는 어디에도 저장하지 않는다. 올라온 소리는 인식기로 흘러가고, 내려갈 소리는 합성기에서 바로 흘러온다.
type voiceSession struct {
	conn   *conversationConn
	stream voice.RecognitionStream
	// cancel은 사건을 주 고리로 나르는 고루틴을 멈춘다. done은 그 고루틴이 끝나면 닫힌다.
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	closed bool
	// playing은 지금 읽어 주고 있는 말이다. 없으면 nil이다.
	playing *playback
	// lastSpoken은 가장 최근에 읽어 준 말이다. 메아리를 가려내는 데 쓴다. lastSpokenUntil은 그 재생이 끝난 시각이고, 재생 중에는 0이다.
	lastSpoken      string
	lastSpokenUntil time.Time
	// pending은 턴이 도는 동안 들어온 말이다.
	pending pendingSpeech
	// rateTokens와 rateAt은 올라오는 소리의 바이트 한도다.
	rateTokens float64
	rateAt     time.Time
	// audioRejected는 채팅 방식에서 소리가 왔다고 이미 알렸는지다. 프레임마다 알리면 오류가 초당 열 번 나간다.
	audioRejected bool
	// listening은 지금 사용자의 말을 받고 있는지다. 거짓이면 올라온 소리를 버리고 인식 사건도 흘려보낸다.
	listening bool
	// oneShot은 끝점마다 듣기를 스스로 멈추는 방식인지다. 클라이언트가 listen을 한 번이라도 보내면 켜진다.
	// 켜져 있지 않으면 음성 방식인 동안 계속 듣는다.
	oneShot bool
}

func (v *voiceSession) isListening() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.listening
}

// voiceAvailable은 이 서버가 음성 방식을 열 수 있는지다.
func (c *Conversation) voiceAvailable() bool {
	return c.opts.Voice.Available()
}

// currentVoice는 지금의 음성 세션이다. 읽기 고루틴과 주 고리가 함께 보므로 잠금 아래에서 읽는다.
func (c *conversationConn) currentVoice() *voiceSession {
	c.voiceMu.RLock()
	defer c.voiceMu.RUnlock()
	return c.voice
}

func (c *conversationConn) setVoice(v *voiceSession) {
	c.voiceMu.Lock()
	defer c.voiceMu.Unlock()
	c.voice = v
}

// effectiveMode는 지금의 대화 방식이다. 음성 세션이 있으면 음성이다.
func (c *conversationConn) effectiveMode() ConversationMode {
	if c.currentVoice() != nil {
		return ConversationModeVoice
	}
	return ConversationModeChat
}

// openVoice는 인식 스트림을 열고 사건을 나르는 고루틴을 띄운다. 공급자가 없으면 voice.ErrUnavailable이다.
func (c *conversationConn) openVoice(ctx context.Context) (*voiceSession, error) {
	provider := c.channel.opts.Voice
	if !provider.Available() {
		return nil, voice.ErrUnavailable
	}
	openCtx, cancel := context.WithTimeout(ctx, voiceOpenTimeout)
	defer cancel()
	stream, err := provider.Recognizer.Open(openCtx)
	if err != nil {
		c.logger.LogAttrs(ctx, slog.LevelWarn, "recognition stream cannot be opened",
			slog.String("failure", failureName(err)))
		return nil, err
	}

	// ctx는 연결의 컨텍스트다. 사건을 나르는 고루틴은 연결이 끝날 때까지 산다.
	forwardCtx, stop := context.WithCancel(ctx)
	v := &voiceSession{
		conn:       c,
		stream:     stream,
		cancel:     stop,
		done:       make(chan struct{}),
		rateTokens: audioBurstBytes,
		rateAt:     c.channel.opts.Clock.Now(),
		// listen을 보내지 않는 클라이언트는 계속 듣는 방식이다.
		listening: true,
	}
	go func() {
		defer close(v.done)
		for {
			select {
			case <-forwardCtx.Done():
				return
			case event, ok := <-stream.Events():
				if !ok {
					return
				}
				select {
				case c.voiceSignals <- voiceSignal{session: v, event: event}:
				case <-forwardCtx.Done():
					return
				}
			}
		}
	}()
	c.logger.LogAttrs(ctx, slog.LevelInfo, "recognition stream opened")
	return v, nil
}

// close는 재생을 멈추고 인식 스트림을 닫는다. 몇 번을 불러도 된다.
func (v *voiceSession) close() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	v.mu.Unlock()

	v.stopPlayback()
	v.cancel()
	_ = v.stream.Close()
	<-v.done
}

// closeVoice는 지금의 음성 세션을 내리고 채팅으로 돌아간다. 세션이 없으면 아무것도 하지 않는다.
func (c *conversationConn) closeVoice() {
	v := c.currentVoice()
	if v == nil {
		return
	}
	c.setVoice(nil)
	v.close()
}

// allowBytes는 올라온 소리의 양이 한도 안인지 본다. 한도는 바이트 단위의 통으로 센다.
func (v *voiceSession) allowBytes(n int) bool {
	now := v.conn.channel.opts.Clock.Now()
	v.mu.Lock()
	defer v.mu.Unlock()
	if elapsed := now.Sub(v.rateAt).Seconds(); elapsed > 0 {
		v.rateTokens = min(audioBurstBytes, v.rateTokens+elapsed*audioRateBytesPerSecond)
		v.rateAt = now
	}
	if float64(n) > v.rateTokens {
		return false
	}
	v.rateTokens -= float64(n)
	return true
}

// acceptAudio는 읽기 고루틴이 받은 소리 프레임 하나를 인식기로 넘긴다.
// 채팅 방식에서 온 소리는 한 번만 알리고 버린다. 한도를 넘긴 소리도 버린다. 어느 쪽도 연결을 끊지 않는다.
func (c *conversationConn) acceptAudio(ctx context.Context, data []byte) {
	v := c.currentVoice()
	if v == nil {
		c.voiceMu.Lock()
		warned := c.audioRejectedWarned
		c.audioRejectedWarned = true
		c.voiceMu.Unlock()
		if !warned {
			c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
		}
		return
	}
	if !v.allowBytes(len(data)) {
		v.mu.Lock()
		warned := v.audioRejected
		v.audioRejected = true
		v.mu.Unlock()
		if !warned {
			c.sendError(ctx, WsErrorCodeRateLimited, nil)
		}
		return
	}
	if !v.isListening() {
		// 한 마디가 끝나 듣기를 멈춘 동안 올라온 소리다. 혼잣말과 주변 말이 턴이 되지 않게 조용히 버린다.
		return
	}
	// 쓰기가 실패했으면 스트림이 죽은 것이다. 사건 고리가 실패를 전해 오고 거기서 다시 연다. 여기서는 조용히 버린다.
	_ = v.stream.Write(ctx, data)
}

// handleVoiceSignal은 인식 사건 하나를 다룬다. 연결을 이어가면 true다.
func (c *conversationConn) handleVoiceSignal(ctx context.Context, sig voiceSignal, idle *idleTimers) bool {
	v := c.currentVoice()
	if v == nil || sig.session != v {
		// 이미 내린 세션의 사건이다.
		return true
	}
	now := c.channel.opts.Clock.Now()

	switch sig.event.Kind {
	case voice.EventPartial:
		text := strings.TrimSpace(sig.event.Text)
		if text == "" || !v.isListening() || v.isEcho(text, now) {
			return true
		}
		c.sendTranscript(ctx, text, nil)
		// 말하고 있는 중이다. AI가 말하고 있었다면 멈추고 듣는다.
		if c.channel.opts.BargeIn && utf8.RuneCountInString(text) >= bargeInMinRunes {
			v.stopPlayback()
		}
		idle.restart(c.session != nil && !c.ended && !c.turnRunning())
		return true

	case voice.EventFinal:
		text := strings.TrimSpace(sig.event.Text)
		if text == "" || !v.isListening() {
			return true
		}
		if v.isEcho(text, now) {
			c.logger.LogAttrs(ctx, slog.LevelDebug, "recognized speech dropped as playback echo")
			return true
		}
		if c.channel.opts.BargeIn {
			v.stopPlayback()
		}
		// 한 마디씩 듣는 방식이면 여기서 듣기를 멈춘다. 멈췄다는 알림은 그 말의 자막 뒤에 보낸다.
		paused := v.oneShotPauses()
		keep := true
		if c.turnRunning() {
			// 앞의 말에 답하는 중이다. 이 말은 모아 두었다가 턴이 끝나면 한 마디로 넣는다.
			v.queue(text, sig.event.MinConfidence)
		} else {
			keep = c.submitSpeech(ctx, text, sig.event.MinConfidence, idle)
		}
		if paused {
			c.sendListening(ctx, false)
		}
		return keep

	case voice.EventFailed:
		c.logger.LogAttrs(ctx, slog.LevelWarn, "recognition stream failed",
			slog.String("failure", failureName(sig.event.Err)))
		return c.recoverVoice(ctx)
	}
	return true
}

// oneShotPauses는 한 마디씩 듣는 방식이면 듣기를 멈추고 참을 돌려준다.
func (v *voiceSession) oneShotPauses() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.oneShot || !v.listening {
		return false
	}
	v.listening = false
	return true
}

// handleListen은 한 마디 듣기를 켜거나 끈다. 켜면 다음 끝점까지 듣고 스스로 멈춘다.
// 사용자가 말하려고 누른 것이므로, 읽어 주던 말이 있으면 멈춘다.
func (c *conversationConn) handleListen(ctx context.Context, m WsListen) bool {
	v := c.currentVoice()
	if v == nil {
		c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
		return true
	}
	v.mu.Lock()
	v.oneShot = true
	v.listening = m.Active
	v.mu.Unlock()
	if m.Active {
		v.stopPlayback()
	}
	c.sendListening(ctx, m.Active)
	return true
}

// queue는 턴이 도는 동안 들어온 말을 모아 둔다.
func (v *voiceSession) queue(text string, confidence float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.pending.texts) == 0 || confidence < v.pending.confidence {
		v.pending.confidence = confidence
	}
	v.pending.texts = append(v.pending.texts, text)
}

// takePending은 모아 둔 말을 꺼내고 비운다. 없으면 두 번째 값이 거짓이다.
func (v *voiceSession) takePending() (string, float32, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.pending.texts) == 0 {
		return "", 0, false
	}
	text := strings.Join(v.pending.texts, " ")
	confidence := v.pending.confidence
	v.pending = pendingSpeech{}
	return text, confidence, true
}

// submitPendingSpeech는 턴이 끝난 뒤 모아 둔 말이 있으면 그 말로 다음 턴을 돌린다. 연결을 이어가면 true다.
func (c *conversationConn) submitPendingSpeech(ctx context.Context, idle *idleTimers) bool {
	v := c.currentVoice()
	if v == nil {
		return true
	}
	text, confidence, ok := v.takePending()
	if !ok {
		return true
	}
	return c.submitSpeech(ctx, text, confidence, idle)
}

// submitSpeech는 알아들은 한 마디로 턴을 돌린다. 글로 쓴 말과 같은 길이고, 같은 한도를 쓴다.
func (c *conversationConn) submitSpeech(ctx context.Context, text string, confidence float32, idle *idleTimers) bool {
	if c.session == nil || c.ended {
		return true
	}
	if allowed, _ := c.channel.userLimiter.take(c.user.ID.String()); !allowed {
		c.sendError(ctx, WsErrorCodeRateLimited, nil)
		return true
	}
	id, err := uuid.NewRandom()
	if err != nil {
		c.sendError(ctx, WsErrorCodeInternalError, nil)
		return true
	}
	// 화면은 이 식별자로 뒤따르는 thinking과 짝을 맞춘다.
	c.sendTranscript(ctx, text, &id)
	c.beginTurn(ctx, turnInput{
		clientMessageID: id, text: text, modality: store.ModeVoice, sttMinConfidence: &confidence,
	})
	idle.restart(false)
	return true
}

// recoverVoice는 죽은 인식 스트림을 한 번 다시 연다. 그래도 열리지 않으면 채팅으로 내려가고 그 사실을 알린다.
func (c *conversationConn) recoverVoice(ctx context.Context) bool {
	old := c.currentVoice()
	c.setVoice(nil)
	if old != nil {
		old.close()
	}
	v, err := c.openVoice(ctx)
	if err == nil {
		c.setVoice(v)
		c.logger.LogAttrs(ctx, slog.LevelInfo, "recognition stream reopened")
		return true
	}
	c.sendError(ctx, WsErrorCodeVoiceUnavailable, nil)
	c.fallBackToChat(ctx)
	return true
}

// fallBackToChat은 음성 세션 없이 채팅으로 이어간다는 것을 대화와 화면에 알린다.
func (c *conversationConn) fallBackToChat(ctx context.Context) {
	if c.session != nil {
		c.session.SetMode(store.ModeChat)
	}
	c.sendMode(ctx, ConversationModeChat)
}

// handleSetMode는 대화 방식을 바꾼다. 어느 쪽이든 같은 대화가 이어진다.
func (c *conversationConn) handleSetMode(ctx context.Context, m WsSetMode) bool {
	if c.session == nil {
		c.sendError(ctx, WsErrorCodeNotStarted, nil)
		return true
	}
	if c.ended {
		c.sendError(ctx, WsErrorCodeConversationEnded, nil)
		return true
	}
	switch m.Mode {
	case ConversationModeVoice:
		if c.currentVoice() != nil {
			c.sendMode(ctx, ConversationModeVoice)
			return true
		}
		v, err := c.openVoice(ctx)
		if err != nil {
			c.sendError(ctx, WsErrorCodeVoiceUnavailable, nil)
			c.fallBackToChat(ctx)
			return true
		}
		c.setVoice(v)
		c.session.SetMode(store.ModeVoice)
		c.sendMode(ctx, ConversationModeVoice)
	case ConversationModeChat:
		c.closeVoice()
		c.fallBackToChat(ctx)
	default:
		c.sendError(ctx, WsErrorCodeInvalidMessage, nil)
	}
	return true
}

// handleInterrupt는 사용자가 끼어들었다는 뜻이다. 읽어 주던 말을 멈춘다. 멈출 것이 없으면 아무 일도 없다.
func (c *conversationConn) handleInterrupt() {
	if v := c.currentVoice(); v != nil {
		v.stopPlayback()
	}
}

// handleFinalize는 지금까지 들은 말을 끝점을 기다리지 않고 확정하게 한다.
func (c *conversationConn) handleFinalize(ctx context.Context) {
	v := c.currentVoice()
	if v == nil {
		return
	}
	// 실패했으면 스트림이 죽은 것이다. 사건 고리가 알려 온다.
	_ = v.stream.Finalize(ctx)
}

// speak는 나간 말을 읽어 준다. 음성 방식이 아니면 아무것도 하지 않는다.
// 앞의 말이 아직 읽히고 있으면 그 말을 멈추고 이 말을 읽는다. 말은 나간 순서대로 들려야 한다.
func (c *conversationConn) speak(seq int32, text string) {
	v := c.currentVoice()
	if v == nil || strings.TrimSpace(text) == "" {
		return
	}
	v.speak(c.connCtx, seq, text)
}

func (v *voiceSession) speak(ctx context.Context, seq int32, text string) {
	v.stopPlayback()

	playCtx, cancel := context.WithCancel(ctx)
	pb := &playback{seq: seq, text: text, cancel: cancel, done: make(chan struct{})}
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		cancel()
		close(pb.done)
		return
	}
	v.playing = pb
	v.lastSpoken = text
	v.lastSpokenUntil = time.Time{}
	v.mu.Unlock()

	go v.play(playCtx, pb)
}

// play는 말 하나를 합성해 조각으로 내려보낸다. 끝나면 어떻게 끝났는지 알린다.
func (v *voiceSession) play(ctx context.Context, pb *playback) {
	defer close(pb.done)
	c := v.conn
	reason := WsAudioEndReasonFailed
	defer func() {
		v.mu.Lock()
		if v.playing == pb {
			v.playing = nil
		}
		v.lastSpokenUntil = c.channel.opts.Clock.Now()
		v.mu.Unlock()
		// 끼어들어 멈춘 재생도 끝은 알려야 한다. 취소된 컨텍스트로는 쓰지 못하므로 취소를 뗀 것으로 보낸다.
		// 연결 자체가 끊겼으면 쓰기가 바로 실패하고, 그것으로 그만이다.
		c.sendAudioEnd(context.WithoutCancel(ctx), pb.seq, reason)
	}()

	synth := c.channel.opts.Voice.Synthesizer
	reader, err := synth.Synthesize(ctx, pb.text)
	if err != nil {
		if ctx.Err() != nil {
			reason = WsAudioEndReasonInterrupted
			return
		}
		c.logger.LogAttrs(ctx, slog.LevelWarn, "speech cannot be synthesized",
			slog.Int("seq", int(pb.seq)), slog.String("failure", failureName(err)))
		return
	}
	defer func() { _ = reader.Close() }()

	if err := c.sendAudioStart(ctx, pb.seq, synth.SampleRate()); err != nil {
		if ctx.Err() != nil {
			reason = WsAudioEndReasonInterrupted
		}
		return
	}
	buf := make([]byte, playbackChunkBytes)
	for {
		n, err := io.ReadFull(reader, buf)
		// 샘플 하나는 두 바이트다. 홀수 바이트를 보내면 받는 쪽의 샘플이 어긋난다.
		n -= n % 2
		if n > 0 {
			if werr := c.writeBinary(ctx, buf[:n]); werr != nil {
				if ctx.Err() != nil {
					reason = WsAudioEndReasonInterrupted
				}
				return
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			reason = WsAudioEndReasonDone
			return
		case ctx.Err() != nil:
			reason = WsAudioEndReasonInterrupted
			return
		default:
			c.logger.LogAttrs(ctx, slog.LevelWarn, "speech stream broke",
				slog.Int("seq", int(pb.seq)), slog.String("failure", failureName(err)))
			return
		}
	}
}

// stopPlayback은 읽어 주던 말을 멈추고 끝났다는 알림이 나갈 때까지 기다린다. 읽는 말이 없으면 바로 돌아온다.
func (v *voiceSession) stopPlayback() {
	v.mu.Lock()
	pb := v.playing
	v.mu.Unlock()
	if pb == nil {
		return
	}
	pb.cancel()
	<-pb.done
}

// isEcho는 알아들은 글이 스피커로 나간 AI의 말이 되돌아온 것인지 본다.
//
// 브라우저의 에코 제거가 놓친 소리는 AI의 말을 글자 그대로 되풀이한다. 재생 중에는 AI의 말 어딘가에 든 글이면 메아리로 보고,
// 재생이 끝난 직후에는 짧은 대답("네", "응")을 버리지 않도록 몇 글자 이상일 때만 메아리로 본다.
func (v *voiceSession) isEcho(text string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.lastSpoken == "" {
		return false
	}
	heard := normalizeSpeech(text)
	if heard == "" {
		return true
	}
	switch {
	case v.playing != nil:
	case v.lastSpokenUntil.IsZero() || now.Sub(v.lastSpokenUntil) > echoWindow:
		return false
	case utf8.RuneCountInString(heard) < echoMinRunesAfterPlayback:
		return false
	}
	return strings.Contains(normalizeSpeech(v.lastSpoken), heard)
}

// normalizeSpeech는 글자와 숫자만 남기고 소문자로 만든다. 띄어쓰기와 문장 부호는 인식기마다 다르게 붙는다.
func normalizeSpeech(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// ---- 내보내기 ---------------------------------------------------------------------

func (c *conversationConn) sendTranscript(ctx context.Context, text string, clientMessageID *uuid.UUID) {
	var message WsServerMessage
	if err := message.FromWsTranscript(WsTranscript{
		Text: text, Final: clientMessageID != nil, ClientMessageID: clientMessageID,
	}); err != nil {
		return
	}
	c.write(ctx, message)
}

func (c *conversationConn) sendListening(ctx context.Context, active bool) {
	var message WsServerMessage
	if err := message.FromWsListening(WsListening{Active: active}); err != nil {
		return
	}
	c.write(ctx, message)
}

func (c *conversationConn) sendMode(ctx context.Context, mode ConversationMode) {
	var message WsServerMessage
	if err := message.FromWsMode(WsMode{Mode: mode}); err != nil {
		return
	}
	c.write(ctx, message)
}

func (c *conversationConn) sendAudioStart(ctx context.Context, seq int32, sampleRate int) error {
	var message WsServerMessage
	if err := message.FromWsAudioStart(WsAudioStart{Seq: seqOf(seq), SampleRate: sampleRate}); err != nil {
		return err
	}
	return c.writeErr(ctx, message)
}

func (c *conversationConn) sendAudioEnd(ctx context.Context, seq int32, reason WsAudioEndReason) {
	var message WsServerMessage
	if err := message.FromWsAudioEnd(WsAudioEnd{Seq: seqOf(seq), Reason: reason}); err != nil {
		return
	}
	c.write(ctx, message)
}

// writeBinary는 소리 조각 하나를 내보낸다. 글과 같은 줄에 선다.
func (c *conversationConn) writeBinary(ctx context.Context, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := c.socket.Write(writeCtx, websocketBinary, data); err != nil {
		return errors.New("api: write audio frame")
	}
	return nil
}

// turnInput은 턴 하나를 돌리는 데 필요한 것이다. 글로 쓴 말과 알아들은 말이 같은 길로 간다.
type turnInput struct {
	clientMessageID uuid.UUID
	text            string
	// modality는 store.ModeChat이나 store.ModeVoice다. 비우면 대화를 시작할 때의 방식이다.
	modality string
	// sttMinConfidence는 알아들은 말의 가장 낮은 확신도다. 글로 쓴 말에는 nil이다.
	sttMinConfidence *float32
}

func (in turnInput) say() engine.Say {
	return engine.Say{
		ClientMessageID:  in.clientMessageID,
		Text:             in.text,
		Modality:         in.modality,
		STTMinConfidence: in.sttMinConfidence,
	}
}
