// Package fake는 대본대로 알아듣고 무음을 돌려주는 가짜 음성 공급자다.
//
// 시험에서는 소리를 넣는 대신 사건을 직접 넣어 채널의 동작을 본다.
// 브라우저 흐름 테스트(e2e)에서는 가짜 마이크의 소리가 일정량 들어올 때마다 대본의 다음 문장을 알아들은 것으로 친다.
package fake

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

// DefaultScript는 e2e가 기대하는 문장이다. 소리가 ScriptBytes만큼 쌓일 때마다 하나씩 나간다.
var DefaultScript = []string{
	"오늘은 동네 도서관에 다녀왔어",
	"새로 나온 소설을 한 권 빌렸어",
	"저녁에는 오랜만에 친구랑 통화했어",
}

// ScriptBytes는 대본의 문장 하나로 치는 소리의 양이다. 16kHz PCM s16le로 1초다.
const ScriptBytes = voice.InputBytesPerSecond

// RecognizerOptions는 가짜 인식기의 설정이다.
type RecognizerOptions struct {
	// Script는 소리가 쌓일 때마다 차례로 나가는 문장이다. 비우면 소리를 넣어도 아무것도 나가지 않는다(시험은 Emit으로 넣는다).
	Script []string
	// ScriptBytes가 0이면 패키지의 ScriptBytes다.
	ScriptBytes int
	// Confidence는 대본 문장의 확신도다. 0이면 1이다.
	Confidence float32
	// OpenErr가 있으면 Open이 그 오류로 실패한다.
	OpenErr error
	// MaxOpens가 0보다 크면 그만큼 연 뒤의 Open은 실패한다. 인식기가 죽고 다시 열리지 않는 상황을 흉내 낸다.
	MaxOpens int
}

// Recognizer는 voice.Recognizer를 구현한다.
type Recognizer struct {
	opts RecognizerOptions

	mu      sync.Mutex
	streams []*Stream
}

var _ voice.Recognizer = (*Recognizer)(nil)

func NewRecognizer(opts RecognizerOptions) *Recognizer {
	if opts.ScriptBytes <= 0 {
		opts.ScriptBytes = ScriptBytes
	}
	if opts.Confidence <= 0 {
		opts.Confidence = 1
	}
	return &Recognizer{opts: opts}
}

// Open은 스트림을 연다. 시험은 Streams로 열린 스트림을 꺼내 사건을 넣는다.
func (r *Recognizer) Open(_ context.Context) (voice.RecognitionStream, error) {
	if r.opts.OpenErr != nil {
		return nil, r.opts.OpenErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.opts.MaxOpens > 0 && len(r.streams) >= r.opts.MaxOpens {
		return nil, errors.New("fake: recognizer refuses to open again")
	}
	s := &Stream{
		opts:   r.opts,
		events: make(chan voice.Event, 64),
	}
	r.streams = append(r.streams, s)
	return s, nil
}

// Streams는 지금까지 연 스트림을 연 순서대로 돌려준다.
func (r *Recognizer) Streams() []*Stream {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Stream(nil), r.streams...)
}

// Stream은 가짜 인식 스트림이다.
type Stream struct {
	opts   RecognizerOptions
	events chan voice.Event

	mu        sync.Mutex
	closed    bool
	bytes     int
	next      int
	finalized int
	written   int
}

var _ voice.RecognitionStream = (*Stream)(nil)

// Write는 소리의 양만 센다. 대본이 있으면 ScriptBytes마다 다음 문장을 Final로 낸다.
func (s *Stream) Write(_ context.Context, pcm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return voice.ErrStreamClosed
	}
	s.written += len(pcm)
	if len(s.opts.Script) == 0 {
		return nil
	}
	s.bytes += len(pcm)
	for s.bytes >= s.opts.ScriptBytes && s.next < len(s.opts.Script) {
		s.bytes -= s.opts.ScriptBytes
		text := s.opts.Script[s.next]
		s.next++
		s.sendLocked(voice.Event{Kind: voice.EventPartial, Text: text})
		s.sendLocked(voice.Event{Kind: voice.EventFinal, Text: text, MinConfidence: s.opts.Confidence})
	}
	return nil
}

// Finalize는 부른 횟수만 센다.
func (s *Stream) Finalize(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return voice.ErrStreamClosed
	}
	s.finalized++
	return nil
}

func (s *Stream) Events() <-chan voice.Event { return s.events }

// Close는 사건 채널을 닫는다. 몇 번을 불러도 된다.
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	return nil
}

// Emit은 시험이 사건을 직접 넣는 길이다. 닫힌 스트림에는 넣지 않는다.
func (s *Stream) Emit(e voice.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.sendLocked(e)
}

// Fail은 실패 사건을 넣고 스트림을 닫는다. 공급자와의 연결이 죽은 것을 흉내 낸다.
func (s *Stream) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.sendLocked(voice.Event{Kind: voice.EventFailed, Err: err})
	s.closed = true
	close(s.events)
}

func (s *Stream) sendLocked(e voice.Event) {
	select {
	case s.events <- e:
	default:
		// 받는 쪽이 멈춰 있으면 버린다. 시험에서 받는 쪽은 언제나 읽고 있다.
	}
}

// Written은 지금까지 받은 소리의 바이트 수다.
func (s *Stream) Written() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// Finalized는 Finalize가 불린 횟수다.
func (s *Stream) Finalized() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalized
}

// Closed는 닫혔는지다.
func (s *Stream) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// SynthesizerOptions는 가짜 합성기의 설정이다.
type SynthesizerOptions struct {
	// MillisPerRune은 글자 하나당 무음의 길이다. 0이면 40ms다.
	MillisPerRune int
	// ChunkBytes는 한 번에 읽히는 조각의 크기다. 0이면 4800(100ms)이다.
	ChunkBytes int
	// ChunkDelay는 조각 사이의 지연이다. 재생이 길게 이어지는 상황을 흉내 낼 때 쓴다.
	ChunkDelay time.Duration
	// Err가 있으면 Synthesize가 그 오류로 실패한다.
	Err error
}

// Synthesizer는 voice.Synthesizer를 구현한다. 글의 길이에 비례하는 무음을 돌려준다.
type Synthesizer struct {
	opts SynthesizerOptions

	mu    sync.Mutex
	texts []string
}

var _ voice.Synthesizer = (*Synthesizer)(nil)

func NewSynthesizer(opts SynthesizerOptions) *Synthesizer {
	if opts.MillisPerRune <= 0 {
		opts.MillisPerRune = 40
	}
	if opts.ChunkBytes <= 0 {
		opts.ChunkBytes = voice.OutputSampleRate * 2 / 10
	}
	return &Synthesizer{opts: opts}
}

func (s *Synthesizer) SampleRate() int { return voice.OutputSampleRate }

// Synthesize는 글자 수에 비례하는 무음을 조각내어 돌려준다. ctx가 끝나면 읽기가 그 오류로 끝난다.
func (s *Synthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if s.opts.Err != nil {
		return nil, s.opts.Err
	}
	s.mu.Lock()
	s.texts = append(s.texts, text)
	s.mu.Unlock()

	runes := 0
	for range text {
		runes++
	}
	total := runes * s.opts.MillisPerRune * voice.OutputSampleRate * 2 / 1000
	// 짝수 바이트(샘플 경계)로 맞춘다.
	total -= total % 2
	if total < s.opts.ChunkBytes {
		total = s.opts.ChunkBytes
	}
	return &silence{ctx: ctx, remaining: total, chunk: s.opts.ChunkBytes, delay: s.opts.ChunkDelay}, nil
}

// Texts는 합성을 요청받은 글을 순서대로 돌려준다.
func (s *Synthesizer) Texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

type silence struct {
	ctx       context.Context
	remaining int
	chunk     int
	delay     time.Duration
	closed    bool
}

func (r *silence) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("fake: audio stream is closed")
	}
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if r.delay > 0 {
		timer := time.NewTimer(r.delay)
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			timer.Stop()
			return 0, r.ctx.Err()
		}
	} else if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n := min(len(p), r.chunk, r.remaining)
	// 짝수 바이트로만 내준다. 샘플 하나가 두 조각에 걸치면 받는 쪽이 어긋난다.
	n -= n % 2
	clear(p[:n])
	r.remaining -= n
	return n, nil
}

func (r *silence) Close() error {
	r.closed = true
	return nil
}
