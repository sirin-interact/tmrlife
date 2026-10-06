// Package soniox는 Soniox 실시간 음성 인식을 voice.Recognizer로 감싼다.
//
// 연결마다 WebSocket 하나를 열어 소리를 흘려보내고, 공급자가 돌려주는 토큰을 모아 끝점까지의 한 마디로 낸다.
// 공급자마다 다른 것(설정 메시지의 꼴, 토큰의 모양, 끝점 표시, 제어 메시지, 오류의 모양)은 이 패키지 안에서 끝난다.
//
// 소리와 글은 로그와 오류에 남기지 않는다. 바이트 수, 토큰 수, 오류 코드만 남긴다.
// 공급자의 오류 문구(error_message)는 읽지도 않는다. 무엇이 섞여 올지 알 수 없기 때문이다.
package soniox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

const (
	// DefaultDialTimeout은 설정이 정하지 않았을 때 연결을 맺는 데 주는 시간이다.
	DefaultDialTimeout = 10 * time.Second
	// DefaultKeepaliveInterval은 소리가 이만큼 오지 않으면 연결을 지키는 메시지를 보내는 간격이다.
	// 공급자는 소리도 keepalive도 없이 20초가 지나면 연결을 끊는다. 그 절반으로 둔다.
	DefaultKeepaliveInterval = 10 * time.Second

	// 공급자가 받는 끝점 대기의 범위다. 밖의 값은 공급자가 거절한다.
	MinEndpointDelay = 500 * time.Millisecond
	MaxEndpointDelay = 3 * time.Second

	// audioFormat은 voice.InputSampleRate의 PCM s16le 모노를 공급자에 알리는 이름이다.
	audioFormat = "pcm_s16le"
)

var (
	// 모델 이름과 언어 힌트는 로그와 오류에 그대로 들어간다. 글자를 제한해 뜻밖의 것이 섞이지 않게 한다.
	modelNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	languageHintPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

// Config는 인식기 하나를 만드는 데 필요한 값이다.
type Config struct {
	// APIKey는 공급자의 키다. 설정 메시지에만 실려 나가고 로그와 오류에는 나오지 않는다.
	APIKey config.Secret
	// URL은 실시간 인식의 WebSocket 주소다(ws 또는 wss).
	URL string
	// Model은 실시간 인식 모델이다.
	Model string
	// LanguageHints는 공급자에 주는 언어 힌트다. 비워 두면 보내지 않는다.
	LanguageHints []string
	// MaxEndpointDelay는 말이 멈춘 뒤 끝점을 내기까지 공급자가 기다리는 시간의 상한이다.
	// MinEndpointDelay와 MaxEndpointDelay 사이여야 한다.
	MaxEndpointDelay time.Duration
	// Logger를 비워 두면 로그를 남기지 않는다.
	Logger *slog.Logger
	// DialTimeout이 0이면 DefaultDialTimeout이다.
	DialTimeout time.Duration
}

// LogValue는 설정을 로그에 남길 때 키를 뺀 나머지만 보여 준다.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("api_key_set", c.APIKey.IsSet()),
		slog.String("url", c.URL),
		slog.String("model", c.Model),
		slog.Any("language_hints", c.LanguageHints),
		slog.Duration("max_endpoint_delay", c.MaxEndpointDelay),
		slog.Duration("dial_timeout", c.DialTimeout),
	)
}

// Recognizer는 voice.Recognizer를 구현한다. 여러 고루틴에서 함께 써도 된다.
type Recognizer struct {
	cfg    Config
	logger *slog.Logger
	// keepaliveInterval은 소리가 이만큼 오지 않으면 keepalive를 보내는 간격이다. 시험에서 줄인다.
	keepaliveInterval time.Duration
	// streams는 연 스트림의 수다. 로그에서 스트림을 가리는 번호로 쓴다.
	streams atomic.Int64
}

var _ voice.Recognizer = (*Recognizer)(nil)

// New는 설정을 검사하고 인식기를 만든다. 네트워크에는 나가지 않는다. 키가 틀렸는지는 첫 스트림에서 드러난다.
func New(cfg Config) (*Recognizer, error) {
	if !cfg.APIKey.IsSet() {
		return nil, errors.New("soniox: api key is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, errors.New("soniox: url must be a ws or wss address")
	}
	if !modelNamePattern.MatchString(cfg.Model) {
		return nil, errors.New("soniox: model name is empty or has unexpected characters")
	}
	for _, hint := range cfg.LanguageHints {
		if !languageHintPattern.MatchString(hint) {
			return nil, errors.New("soniox: language hint is empty or has unexpected characters")
		}
	}
	if cfg.MaxEndpointDelay < MinEndpointDelay || cfg.MaxEndpointDelay > MaxEndpointDelay {
		return nil, fmt.Errorf("soniox: max endpoint delay must be between %s and %s", MinEndpointDelay, MaxEndpointDelay)
	}
	if cfg.DialTimeout < 0 {
		return nil, errors.New("soniox: dial timeout must not be negative")
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Recognizer{cfg: cfg, logger: logger, keepaliveInterval: DefaultKeepaliveInterval}, nil
}

// configMessage는 연결을 열고 처음 보내는 메시지다. 공급자는 이 메시지로 소리의 꼴과 인식 방식을 안다.
type configMessage struct {
	APIKey                  string   `json:"api_key"`
	Model                   string   `json:"model"`
	AudioFormat             string   `json:"audio_format"`
	SampleRate              int      `json:"sample_rate"`
	NumChannels             int      `json:"num_channels"`
	LanguageHints           []string `json:"language_hints,omitempty"`
	EnableEndpointDetection bool     `json:"enable_endpoint_detection"`
	MaxEndpointDelayMs      int64    `json:"max_endpoint_delay_ms"`
}

// Open은 연결을 맺고 설정을 보낸 뒤 돌아온다.
//
// 공급자는 설정을 받았다는 답을 따로 보내지 않는다. 첫 메시지는 소리를 보낸 뒤의 토큰이거나, 설정이 틀렸을 때의 오류다.
// 그래서 틀린 키처럼 설정 자체의 문제는 여기서가 아니라 스트림의 첫 사건(EventFailed)으로 드러난다.
func (r *Recognizer) Open(ctx context.Context) (voice.RecognitionStream, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.DialTimeout)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, r.cfg.URL, &websocket.DialOptions{
		// 소리는 압축이 되지 않고, 압축을 끄면 빈 프레임이 프레임 하나로 나간다.
		CompressionMode: websocket.CompressionDisabled,
	})
	if resp != nil && resp.Body != nil {
		// 연결이 맺어지면 본문은 비어 있고(nil), 거절당했을 때만 짧은 본문이 남는다. 읽지 않고 닫는다.
		_ = resp.Body.Close()
	}
	if err != nil {
		// 연결 오류에는 주소와 HTTP 상태만 들어간다. 키는 아직 보내기 전이다.
		return nil, fmt.Errorf("soniox: dial: %w", err)
	}
	conn.SetReadLimit(readLimit)

	msg, err := json.Marshal(configMessage{
		APIKey:                  r.cfg.APIKey.Reveal(),
		Model:                   r.cfg.Model,
		AudioFormat:             audioFormat,
		SampleRate:              voice.InputSampleRate,
		NumChannels:             1,
		LanguageHints:           r.cfg.LanguageHints,
		EnableEndpointDetection: true,
		MaxEndpointDelayMs:      int64(r.cfg.MaxEndpointDelay / time.Millisecond),
	})
	if err != nil {
		_ = conn.CloseNow()
		return nil, errors.New("soniox: config message could not be encoded")
	}
	if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
		_ = conn.CloseNow()
		return nil, fmt.Errorf("soniox: send config: %w", err)
	}

	id := r.streams.Add(1)
	logger := r.logger.With(slog.Int64("stream", id), slog.String("model", r.cfg.Model))
	s := newStream(ctx, conn, logger, r.keepaliveInterval)
	s.start()
	logger.LogAttrs(ctx, slog.LevelDebug, "recognition stream opened")
	return s, nil
}
