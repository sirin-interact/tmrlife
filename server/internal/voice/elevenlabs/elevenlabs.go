// Package elevenlabs는 ElevenLabs의 HTTP 스트리밍 합성을 voice.Synthesizer로 감싼다.
//
// 답은 한두 문장이라 글을 흘려 넣을 일이 없다. 요청 하나에 글 하나를 보내고, 소리는 응답 본문이 흘러오는 대로 읽힌다.
// 첫 소리까지의 시간은 연결을 새로 맺느냐에 크게 좌우되므로 전송 계층 하나를 함께 쓰고 유휴 연결을 남겨 둔다.
//
// 글과 키는 로그와 오류 어디에도 넣지 않는다. 공급자가 돌려준 실패 문구에는 보낸 글의 일부가 섞여 있을 수 있어서
// 문구는 버리고 상태 코드와 짧은 사유 코드만 남긴다.
package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

const (
	// DefaultLanguageCode는 설정이 언어를 비워 뒀을 때의 언어다.
	DefaultLanguageCode = "ko"

	// 기본 전송 계층의 시간 제한이다. 전체 제한은 두지 않는다. 본문은 흘러오는 동안 읽히므로 그 기한은 부르는 쪽이 ctx로 준다.
	// 첫 소리까지는 보통 0.2초 안쪽이다. 머리말이 이보다 훨씬 늦으면 공급자가 막힌 것이므로 더 기다리지 않는다.
	DefaultDialTimeout           = 5 * time.Second
	DefaultTLSHandshakeTimeout   = 5 * time.Second
	DefaultResponseHeaderTimeout = 10 * time.Second
	// DefaultMaxIdleConnsPerHost는 남겨 두는 유휴 연결의 수다. 고정 문구를 미리 만들 때와 여러 대화가 겹칠 때 함께 쓰인다.
	DefaultMaxIdleConnsPerHost = 4

	// maxErrorBody는 실패 응답에서 사유 코드를 찾으려고 읽는 양의 상한이다.
	maxErrorBody = 8 << 10
	// maxDrain은 실패 응답의 나머지를 버리는 양의 상한이다. 본문을 다 읽어야 연결이 다시 쓰인다.
	maxDrain = 64 << 10
)

// outputFormat은 요청하는 소리의 꼴이다. PCM s16le 모노이고 샘플레이트는 OutputSampleRate다.
var outputFormat = "pcm_" + strconv.Itoa(voice.OutputSampleRate)

var (
	// ErrEmptyText는 빈 글을 받았다는 뜻이다. 요청은 보내지 않는다.
	ErrEmptyText = errors.New("elevenlabs: text is empty")
	// ErrUnauthorized는 키가 틀렸거나 권한이 없다는 뜻이다(401, 403).
	ErrUnauthorized = errors.New("elevenlabs: unauthorized")
	// ErrRateLimited는 요청 한도나 글자 할당량에 걸렸다는 뜻이다(429).
	ErrRateLimited = errors.New("elevenlabs: rate limited")

	errStreamClosed = errors.New("elevenlabs: audio stream is closed")
)

// StatusError는 공급자가 200이 아닌 응답을 돌려줬다는 뜻이다.
// 문구는 담지 않는다. Code는 응답 JSON의 detail.status에서 읽은 짧은 사유 코드이고, 없으면 비어 있다.
type StatusError struct {
	Status int
	Code   string
}

func (e *StatusError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("elevenlabs: unexpected status %d", e.Status)
	}
	return fmt.Sprintf("elevenlabs: unexpected status %d (%s)", e.Status, e.Code)
}

// Is는 상태 코드를 errors.Is로 가릴 수 있게 한다.
func (e *StatusError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
	case ErrRateLimited:
		return e.Status == http.StatusTooManyRequests
	default:
		return false
	}
}

// Config는 합성기 하나를 만드는 데 필요한 값이다.
type Config struct {
	// APIKey는 xi-api-key 헤더로 나간다.
	APIKey config.Secret
	// BaseURL은 공급자의 주소다(예: https://api.elevenlabs.io). 경로는 여기에 붙인다.
	BaseURL string
	// VoiceID는 목소리다. 주소의 일부가 된다.
	VoiceID string
	// Model은 합성 모델이다.
	Model string
	// LanguageCode를 비워 두면 DefaultLanguageCode다.
	LanguageCode string
	// Speed는 말하는 빠르기다. 0이면 voice_settings를 보내지 않고 목소리의 기본값을 따른다.
	Speed float64
	// HTTPClient를 비워 두면 연결을 유지하는 전용 클라이언트를 만든다. 시험에서 가짜 서버의 클라이언트를 끼울 때 쓴다.
	HTTPClient *http.Client
	// Logger는 필수다. 상태, 바이트 수, 걸린 시간만 남긴다.
	Logger *slog.Logger
	// Clock은 걸린 시간을 재는 데 쓴다. 비워 두면 운영체제 시계다.
	Clock clock.Clock
}

// voiceIDPattern은 주소에 그대로 넣어도 되는 목소리 ID의 꼴이다.
var voiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Synthesizer는 voice.Synthesizer를 구현한다. 여러 고루틴에서 함께 써도 된다.
type Synthesizer struct {
	client   *http.Client
	logger   *slog.Logger
	clock    clock.Clock
	endpoint string
	apiKey   config.Secret
	model    string
	language string
	speed    float64
}

var _ voice.Synthesizer = (*Synthesizer)(nil)

// New는 합성기를 만든다. 네트워크에는 나가지 않는다. 키나 목소리가 틀렸는지는 첫 호출에서 드러난다.
func New(cfg Config) (*Synthesizer, error) {
	if !cfg.APIKey.IsSet() {
		return nil, errors.New("elevenlabs: api key is required")
	}
	endpoint, err := endpointURL(cfg.BaseURL, strings.TrimSpace(cfg.VoiceID))
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("elevenlabs: model is required")
	}
	if cfg.Speed < 0 {
		return nil, errors.New("elevenlabs: speed must not be negative")
	}
	if cfg.Logger == nil {
		return nil, errors.New("elevenlabs: logger is required")
	}

	s := &Synthesizer{
		client:   cfg.HTTPClient,
		logger:   cfg.Logger,
		clock:    cfg.Clock,
		endpoint: endpoint,
		apiKey:   cfg.APIKey,
		model:    model,
		language: strings.TrimSpace(cfg.LanguageCode),
		speed:    cfg.Speed,
	}
	if s.client == nil {
		s.client = newHTTPClient()
	}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	if s.language == "" {
		s.language = DefaultLanguageCode
	}
	return s, nil
}

// endpointURL은 스트리밍 합성 주소를 만든다. 목소리 ID는 경로에 들어가므로 이름의 꼴일 때만 받는다.
func endpointURL(baseURL, voiceID string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", errors.New("elevenlabs: base url must be an http(s) url")
	}
	if !voiceIDPattern.MatchString(voiceID) {
		return "", errors.New("elevenlabs: voice id is empty or has unexpected characters")
	}
	u := base.JoinPath("v1", "text-to-speech", voiceID, "stream")
	u.RawQuery = url.Values{"output_format": {outputFormat}}.Encode()
	return u.String(), nil
}

// newHTTPClient는 합성기가 혼자 쓰는 클라이언트다. 전체 시간 제한은 두지 않고 단계별 제한만 둔다.
func newHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	transport = transport.Clone()
	transport.DialContext = (&net.Dialer{Timeout: DefaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
	transport.MaxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	return &http.Client{Transport: transport}
}

func (s *Synthesizer) SampleRate() int { return voice.OutputSampleRate }

// Synthesize는 글 하나를 보내고 소리 스트림을 돌려준다. 첫 조각은 공급자가 내보내는 대로 읽힌다.
// ctx가 끝나면 읽기가 ctx의 오류로 끝나고 연결이 닫혀 공급자 호출이 멈춘다.
func (s *Synthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyText
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("elevenlabs: %w", err)
	}
	body, err := s.encodeRequest(text)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		// 주소는 New에서 검사했으므로 여기서 실패하면 프로그램의 잘못이다. 오류에는 주소만 들어 있고 글은 없다.
		return nil, fmt.Errorf("elevenlabs: build request: %w", err)
	}
	req.Header.Set("xi-api-key", s.apiKey.Reveal())
	req.Header.Set("Content-Type", "application/json")

	started := s.clock.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelDebug, "tts request failed",
			slog.Int64("latency_ms", s.since(started)),
			slog.Bool("canceled", ctx.Err() != nil))
		// 전송 오류에는 주소와 연결 상태만 들어 있다. 요청 본문은 섞이지 않는다.
		return nil, fmt.Errorf("elevenlabs: send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		code := readErrorCode(resp.Body)
		_ = resp.Body.Close()
		s.logger.LogAttrs(ctx, slog.LevelDebug, "tts request rejected",
			slog.Int("status", resp.StatusCode),
			slog.String("code", code),
			slog.Int64("latency_ms", s.since(started)))
		return nil, &StatusError{Status: resp.StatusCode, Code: code}
	}
	return newStream(ctx, resp.Body, s, started, resp.StatusCode), nil
}

func (s *Synthesizer) since(started time.Time) int64 {
	return s.clock.Now().Sub(started).Milliseconds()
}

// request는 공급자에 보내는 본문이다.
type request struct {
	Text          string         `json:"text"`
	ModelID       string         `json:"model_id"`
	LanguageCode  string         `json:"language_code,omitempty"`
	VoiceSettings *voiceSettings `json:"voice_settings,omitempty"`
}

type voiceSettings struct {
	Speed float64 `json:"speed"`
}

func (s *Synthesizer) encodeRequest(text string) ([]byte, error) {
	r := request{Text: text, ModelID: s.model, LanguageCode: s.language}
	if s.speed != 0 {
		r.VoiceSettings = &voiceSettings{Speed: s.speed}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// 글의 <, >, &를 <처럼 바꾸지 않는다. JSON으로는 같지만 공급자가 받는 글을 그대로 두는 편이 안전하다.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		// 인코더의 오류 문구에는 값이 들어갈 수 있으므로 감싸지 않는다.
		return nil, errors.New("elevenlabs: request could not be encoded")
	}
	return buf.Bytes(), nil
}

// errorBody는 공급자가 실패를 알리는 JSON의 꼴이다.
// detail은 보통 {"status": "...", "message": "..."}이지만 요청 검증 실패(422)에서는 목록으로 오기도 한다. 그때는 코드가 없다.
type errorBody struct {
	Detail json.RawMessage `json:"detail"`
}

type errorDetail struct {
	Status string `json:"status"`
}

// codePattern은 오류와 로그에 그대로 넣어도 되는 사유 코드의 꼴이다.
var codePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// readErrorCode는 실패 응답에서 사유 코드만 꺼내고 나머지는 버린다. 문구는 읽지 않은 것처럼 다룬다.
func readErrorCode(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBody))
	// 나머지를 비워야 연결이 다시 쓰인다. 끝없이 긴 본문은 기다리지 않는다.
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var parsed errorBody
	if json.Unmarshal(raw, &parsed) != nil {
		return ""
	}
	var detail errorDetail
	if json.Unmarshal(parsed.Detail, &detail) != nil {
		return ""
	}
	if !codePattern.MatchString(detail.Status) {
		return ""
	}
	return detail.Status
}

// stream은 응답 본문을 감싼다. Close는 몇 번을 불러도 되고 Read와 함께 불러도 안전하다.
// ctx가 끝난 뒤의 읽기는 전송 계층의 오류 대신 ctx의 오류로 끝나서 부르는 쪽이 errors.Is로 가릴 수 있다.
type stream struct {
	ctx     context.Context
	body    io.ReadCloser
	synth   *Synthesizer
	started time.Time
	status  int

	bytes      atomic.Int64
	firstAudio atomic.Int64
	finished   atomic.Bool
	closed     atomic.Bool
	logOnce    sync.Once
}

func newStream(ctx context.Context, body io.ReadCloser, synth *Synthesizer, started time.Time, status int) *stream {
	return &stream{ctx: ctx, body: body, synth: synth, started: started, status: status}
}

func (st *stream) Read(p []byte) (int, error) {
	if st.closed.Load() {
		return 0, errStreamClosed
	}
	n, err := st.body.Read(p)
	if n > 0 && st.bytes.Add(int64(n)) == int64(n) {
		st.firstAudio.Store(st.synth.since(st.started))
	}
	if err == nil {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		st.finished.Store(true)
		st.log(true)
		return n, io.EOF
	}
	if ctxErr := st.ctx.Err(); ctxErr != nil {
		return n, fmt.Errorf("elevenlabs: audio stream aborted: %w", ctxErr)
	}
	if st.closed.Load() {
		return n, errStreamClosed
	}
	return n, fmt.Errorf("elevenlabs: read audio stream: %w", err)
}

func (st *stream) Close() error {
	if !st.closed.CompareAndSwap(false, true) {
		return nil
	}
	err := st.body.Close()
	st.log(st.finished.Load())
	if err != nil {
		return fmt.Errorf("elevenlabs: close audio stream: %w", err)
	}
	return nil
}

// log는 스트림 하나가 어떻게 끝났는지 한 줄로 남긴다. 끝까지 읽혔든 Close로 끊겼든 한 번만 남긴다.
func (st *stream) log(complete bool) {
	st.logOnce.Do(func() {
		st.synth.logger.LogAttrs(st.ctx, slog.LevelDebug, "tts stream ended",
			slog.Int("status", st.status),
			slog.Int64("bytes", st.bytes.Load()),
			slog.Int64("first_audio_ms", st.firstAudio.Load()),
			slog.Int64("latency_ms", st.synth.since(st.started)),
			slog.Bool("complete", complete),
			slog.Bool("canceled", st.ctx.Err() != nil))
	})
}
