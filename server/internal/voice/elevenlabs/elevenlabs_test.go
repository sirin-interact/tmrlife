package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

// 아래 글과 키는 오류 문구와 로그 어디에도 나오면 안 된다.
const (
	testAPIKey  = "test-key-0123456789-do-not-leak"
	testText    = "그런 날 있죠. 이유 없이 가라앉는 날엔 쉬는 게 제일이에요."
	testVoiceID = "EXAVITQu4vr4xnSDxMaL"
	testModel   = "eleven_flash_v2_5"
	// serverMessage는 공급자의 실패 문구다. 보낸 글의 일부가 섞여 돌아올 수 있다.
	serverMessage = "the text could not be processed: 그런 날 있죠"
)

var sensitiveTexts = []string{testAPIKey, testText, serverMessage}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func assertNoSensitiveText(t *testing.T, got string) {
	t.Helper()
	for _, s := range sensitiveTexts {
		assert.NotContains(t, got, s)
	}
}

// pcmSample은 내용을 알아볼 수 있는 가짜 소리다. 조각이 뒤섞이면 비교에서 드러난다.
func pcmSample(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i * 7)
	}
	return out
}

// lockedBuffer는 로그를 모은다. 핸들러와 시험이 다른 고루틴에서 닿아도 안전하다.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// capturedRequest는 가짜 서버가 받은 요청 하나다.
type capturedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   map[string]any
}

// recorder는 받은 요청을 기록하고 정해진 응답을 돌려주는 가짜 서버다.
type recorder struct {
	status int
	body   []byte

	mu       sync.Mutex
	requests []capturedRequest
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	r.mu.Lock()
	r.requests = append(r.requests, capturedRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Query:  req.URL.Query(),
		Header: req.Header.Clone(),
		Body:   body,
	})
	r.mu.Unlock()
	w.WriteHeader(r.status)
	_, _ = w.Write(r.body)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recorder) last() capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[len(r.requests)-1]
}

type fixture struct {
	logs  *lockedBuffer
	synth *Synthesizer
}

// newFixture는 가짜 서버를 띄우고 그 서버를 보는 합성기를 만든다. 서버는 시험이 끝나면 닫힌다.
func newFixture(t *testing.T, handler http.Handler, modify func(*Config)) *fixture {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	logs := &lockedBuffer{}
	cfg := Config{
		APIKey:     config.NewSecret(testAPIKey),
		BaseURL:    srv.URL,
		VoiceID:    testVoiceID,
		Model:      testModel,
		HTTPClient: srv.Client(),
		Logger:     logging.New(logs, slog.LevelDebug),
		Clock:      clock.Real{},
	}
	if modify != nil {
		modify(&cfg)
	}
	synth, err := New(cfg)
	require.NoError(t, err)
	return &fixture{logs: logs, synth: synth}
}

func TestNew(t *testing.T) {
	valid := Config{
		APIKey:  config.NewSecret(testAPIKey),
		BaseURL: "https://api.elevenlabs.io",
		VoiceID: testVoiceID,
		Model:   testModel,
		Logger:  slog.New(slog.DiscardHandler),
	}

	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{"키가 없으면 거절한다", func(c *Config) { c.APIKey = config.Secret{} }, "api key"},
		{"주소가 비어 있으면 거절한다", func(c *Config) { c.BaseURL = "" }, "base url"},
		{"주소가 http(s)가 아니면 거절한다", func(c *Config) { c.BaseURL = "wss://api.elevenlabs.io" }, "base url"},
		{"호스트가 없는 주소는 거절한다", func(c *Config) { c.BaseURL = "https://" }, "base url"},
		{"목소리가 비어 있으면 거절한다", func(c *Config) { c.VoiceID = "  " }, "voice id"},
		{"목소리에 주소에 쓸 수 없는 글자가 있으면 거절한다", func(c *Config) { c.VoiceID = "../other" }, "voice id"},
		{"모델이 비어 있으면 거절한다", func(c *Config) { c.Model = " " }, "model"},
		{"빠르기가 음수면 거절한다", func(c *Config) { c.Speed = -1 }, "speed"},
		{"로거가 없으면 거절한다", func(c *Config) { c.Logger = nil }, "logger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.modify(&cfg)
			s, err := New(cfg)
			require.Error(t, err)
			assert.Nil(t, s)
			assert.Contains(t, err.Error(), tt.wantErr)
			assertNoSensitiveText(t, err.Error())
		})
	}

	t.Run("기본값: 언어는 ko, 전송 계층은 연결을 남겨 두고 전체 시간 제한이 없다", func(t *testing.T) {
		s, err := New(valid)
		require.NoError(t, err)
		assert.Equal(t, DefaultLanguageCode, s.language)
		assert.NotNil(t, s.clock)
		assert.Equal(t, "https://api.elevenlabs.io/v1/text-to-speech/"+testVoiceID+"/stream?output_format=pcm_24000", s.endpoint)

		assert.Zero(t, s.client.Timeout, "본문이 흘러오는 동안의 기한은 ctx가 준다")
		transport, ok := s.client.Transport.(*http.Transport)
		require.True(t, ok)
		assert.GreaterOrEqual(t, transport.MaxIdleConnsPerHost, 4)
		assert.Equal(t, DefaultResponseHeaderTimeout, transport.ResponseHeaderTimeout)
		assert.Equal(t, DefaultTLSHandshakeTimeout, transport.TLSHandshakeTimeout)
	})

	t.Run("주소 끝의 빗금과 앞뒤 공백은 상관없다", func(t *testing.T) {
		cfg := valid
		cfg.BaseURL = " https://api.elevenlabs.io/ "
		s, err := New(cfg)
		require.NoError(t, err)
		assert.Equal(t, "https://api.elevenlabs.io/v1/text-to-speech/"+testVoiceID+"/stream?output_format=pcm_24000", s.endpoint)
	})
}

func TestSampleRate(t *testing.T) {
	s, err := New(Config{
		APIKey:  config.NewSecret(testAPIKey),
		BaseURL: "https://api.elevenlabs.io",
		VoiceID: testVoiceID,
		Model:   testModel,
		Logger:  slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	assert.Equal(t, voice.OutputSampleRate, s.SampleRate())
}

func TestSynthesize_요청의_꼴(t *testing.T) {
	tests := []struct {
		name         string
		modify       func(*Config)
		text         string
		wantText     string
		wantLanguage string
		// wantSpeed가 0이면 voice_settings를 보내지 않아야 한다.
		wantSpeed float64
	}{
		{"기본 설정은 언어 ko에 voice_settings가 없다", nil, testText, testText, "ko", 0},
		{"앞뒤 공백은 떼고 보낸다", nil, "  \n" + testText + "\t ", testText, "ko", 0},
		{"언어를 바꾸면 그대로 보낸다", func(c *Config) { c.LanguageCode = "en" }, testText, testText, "en", 0},
		{"빠르기를 정하면 voice_settings.speed로 보낸다", func(c *Config) { c.Speed = 1.1 }, testText, testText, "ko", 1.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{status: http.StatusOK, body: pcmSample(480)}
			f := newFixture(t, rec, tt.modify)

			stream, err := f.synth.Synthesize(t.Context(), tt.text)
			require.NoError(t, err)
			got, err := io.ReadAll(stream)
			require.NoError(t, err)
			require.NoError(t, stream.Close())
			assert.Equal(t, rec.body, got)

			require.Equal(t, 1, rec.count())
			req := rec.last()
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "/v1/text-to-speech/"+testVoiceID+"/stream", req.Path)
			assert.Equal(t, "pcm_24000", req.Query.Get("output_format"))
			assert.Equal(t, testAPIKey, req.Header.Get("xi-api-key"))
			assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
			assert.Equal(t, tt.wantText, req.Body["text"])
			assert.Equal(t, testModel, req.Body["model_id"])
			assert.Equal(t, tt.wantLanguage, req.Body["language_code"])

			settings, ok := req.Body["voice_settings"]
			if tt.wantSpeed == 0 {
				assert.False(t, ok, "voice_settings가 없어야 한다")
			} else {
				require.True(t, ok, "voice_settings가 있어야 한다")
				fields, ok := settings.(map[string]any)
				require.True(t, ok)
				assert.InDelta(t, tt.wantSpeed, fields["speed"], 1e-9)
				assert.Len(t, fields, 1, "정하지 않은 설정은 보내지 않는다")
			}
			assertNoSensitiveText(t, f.logs.String())
		})
	}
}

func TestSynthesize_소리는_만들어지는_대로_읽힌다(t *testing.T) {
	first := pcmSample(4800)
	second := pcmSample(2400)

	release := make(chan struct{})
	var releaseOnce sync.Once
	letSecondGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letSecondGo)
	var secondWritten atomic.Bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "audio/pcm")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(first)
		flusher.Flush()
		// 둘째 조각은 시험이 풀어 줄 때까지 쓰지 않는다. 그 전에 첫 조각이 읽혀야 흘러오는 대로 읽히는 것이다.
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		secondWritten.Store(true)
		_, _ = w.Write(second)
		flusher.Flush()
	})
	f := newFixture(t, handler, nil)

	// 본문 전체를 모아서 돌려주는 구현이면 여기서 영원히 기다린다. 기한을 두어 실패로 드러나게 한다.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := f.synth.Synthesize(ctx, testText)
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	buf := make([]byte, len(first))
	_, err = io.ReadFull(stream, buf)
	require.NoError(t, err)
	assert.Equal(t, first, buf)
	assert.False(t, secondWritten.Load(), "둘째 조각이 쓰이기 전에 첫 조각이 읽혀야 한다")

	letSecondGo()
	rest, err := io.ReadAll(stream)
	require.NoError(t, err)
	assert.Equal(t, second, rest)
	require.NoError(t, stream.Close())
	assertNoSensitiveText(t, f.logs.String())
}

func TestSynthesize_취소하면_읽기가_끝나고_공급자_호출이_멈춘다(t *testing.T) {
	first := pcmSample(4800)
	gone := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(first)
		flusher.Flush()
		// 클라이언트가 떠나면 요청 컨텍스트가 끝난다. 그때까지는 소리를 더 보내지 않고 기다린다.
		select {
		case <-r.Context().Done():
			close(gone)
		case <-time.After(5 * time.Second):
		}
	})
	f := newFixture(t, handler, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := f.synth.Synthesize(ctx, testText)
	require.NoError(t, err)

	buf := make([]byte, len(first))
	_, err = io.ReadFull(stream, buf)
	require.NoError(t, err)
	assert.Equal(t, first, buf)

	cancel()
	_, err = stream.Read(buf)
	require.ErrorIs(t, err, context.Canceled)

	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("서버가 클라이언트가 떠난 것을 알아채지 못했다")
	}

	// Close는 몇 번을 불러도 된다. 닫힌 뒤의 읽기는 오류다.
	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close())
	_, err = stream.Read(buf)
	require.Error(t, err)

	logs := f.logs.String()
	assert.Contains(t, logs, `"complete":false`)
	assert.Contains(t, logs, `"canceled":true`)
	assertNoSensitiveText(t, logs)
}

func TestSynthesize_실패_응답(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		// wantIs가 nil이면 errors.Is 검사를 하지 않는다.
		wantIs   error
		wantCode string
	}{
		{"401은 ErrUnauthorized다", http.StatusUnauthorized,
			`{"detail":{"status":"invalid_api_key","message":"` + serverMessage + `"}}`, ErrUnauthorized, "invalid_api_key"},
		{"403도 ErrUnauthorized다", http.StatusForbidden,
			`{"detail":{"status":"missing_permissions","message":"` + serverMessage + `"}}`, ErrUnauthorized, "missing_permissions"},
		{"429는 ErrRateLimited다", http.StatusTooManyRequests,
			`{"detail":{"status":"quota_exceeded","message":"` + serverMessage + `"}}`, ErrRateLimited, "quota_exceeded"},
		{"422는 사유 코드가 든 StatusError다", http.StatusUnprocessableEntity,
			`{"detail":{"status":"invalid_text","message":"` + serverMessage + `"}}`, nil, "invalid_text"},
		{"검증 실패의 목록 꼴 detail에는 코드가 없다", http.StatusUnprocessableEntity,
			`{"detail":[{"loc":["body","text"],"msg":"` + serverMessage + `","type":"value_error"}]}`, nil, ""},
		{"JSON이 아닌 본문에는 코드가 없다", http.StatusInternalServerError,
			"<html>" + serverMessage + "</html>", nil, ""},
		{"빈 본문에는 코드가 없다", http.StatusBadGateway, "", nil, ""},
		{"사유 코드가 이름의 꼴이 아니면 버린다", http.StatusBadRequest,
			`{"detail":{"status":"bad code: ` + serverMessage + `"}}`, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{status: tt.status, body: []byte(tt.body)}
			f := newFixture(t, rec, nil)

			stream, err := f.synth.Synthesize(t.Context(), testText)
			require.Error(t, err)
			assert.Nil(t, stream)

			var statusErr *StatusError
			require.ErrorAs(t, err, &statusErr)
			assert.Equal(t, tt.status, statusErr.Status)
			assert.Equal(t, tt.wantCode, statusErr.Code)
			if tt.wantIs != nil {
				require.ErrorIs(t, err, tt.wantIs)
			}

			// 공급자의 문구에는 보낸 글이 섞일 수 있다. 오류에도 로그에도 옮기지 않는다.
			assert.NotContains(t, err.Error(), serverMessage)
			assertNoSensitiveText(t, err.Error())
			logs := f.logs.String()
			assertNoSensitiveText(t, logs)
			assert.Contains(t, logs, `"status":`+strconv.Itoa(tt.status))
			if tt.wantCode != "" {
				assert.Contains(t, err.Error(), tt.wantCode)
				assert.Contains(t, logs, `"code":"`+tt.wantCode+`"`)
			}
		})
	}

	t.Run("다른 상태는 ErrUnauthorized도 ErrRateLimited도 아니다", func(t *testing.T) {
		err := error(&StatusError{Status: http.StatusUnprocessableEntity, Code: "invalid_text"})
		require.NotErrorIs(t, err, ErrUnauthorized)
		require.NotErrorIs(t, err, ErrRateLimited)
	})
}

func TestSynthesize_빈_글은_보내지_않는다(t *testing.T) {
	rec := &recorder{status: http.StatusOK, body: pcmSample(480)}
	f := newFixture(t, rec, nil)

	tests := []struct {
		name string
		text string
	}{
		{"빈 문자열", ""},
		{"공백만", "   "},
		{"줄바꿈과 탭만", "\n\t \r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := f.synth.Synthesize(t.Context(), tt.text)
			require.ErrorIs(t, err, ErrEmptyText)
			assert.Nil(t, stream)
		})
	}

	t.Run("이미 끝난 ctx로는 요청을 보내지 않는다", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		stream, err := f.synth.Synthesize(ctx, testText)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, stream)
	})

	assert.Equal(t, 0, rec.count(), "서버에 요청이 가면 안 된다")
	assert.Empty(t, f.logs.String())
}

func TestSynthesize_로그에는_상태와_양과_시간만_남는다(t *testing.T) {
	rec := &recorder{status: http.StatusOK, body: pcmSample(9600)}
	f := newFixture(t, rec, nil)

	stream, err := f.synth.Synthesize(t.Context(), testText)
	require.NoError(t, err)
	got, err := io.ReadAll(stream)
	require.NoError(t, err)
	assert.Len(t, got, 9600)
	require.NoError(t, stream.Close())

	logs := f.logs.String()
	assert.Equal(t, 1, strings.Count(logs, `"msg":"tts stream ended"`), "끝까지 읽은 뒤 Close해도 한 번만 남긴다")
	assert.Contains(t, logs, `"status":200`)
	assert.Contains(t, logs, `"bytes":9600`)
	assert.Contains(t, logs, `"complete":true`)
	assert.Contains(t, logs, `"latency_ms":`)
	assertNoSensitiveText(t, logs)
}
