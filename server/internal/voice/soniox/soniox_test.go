package soniox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
)

const (
	testKey = "test-key-must-never-appear"
	wait    = 5 * time.Second
)

// frame은 가짜 서버가 설정 뒤에 받은 프레임 하나다.
type frame struct {
	typ  websocket.MessageType
	data []byte
}

// fakeConn은 가짜 서버가 받은 연결 하나다. 시험은 frames로 받은 것을 보고 send로 응답을 넣는다.
type fakeConn struct {
	conn   *websocket.Conn
	config map[string]any
	frames chan frame
	// gone은 연결이 끝나면 닫힌다.
	gone chan struct{}
}

func (c *fakeConn) send(t *testing.T, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	require.NoError(t, c.conn.Write(ctx, websocket.MessageText, []byte(msg)))
}

func (c *fakeConn) nextFrame(t *testing.T) frame {
	t.Helper()
	// 연결이 끝났어도 먼저 받아 둔 프레임이 있으면 그것부터 돌려준다.
	select {
	case fr := <-c.frames:
		return fr
	default:
	}
	select {
	case fr := <-c.frames:
		return fr
	case <-c.gone:
		t.Fatal("프레임을 받기 전에 연결이 끝났다")
	case <-time.After(wait):
		t.Fatal("프레임을 기다리다 시간이 지났다")
	}
	return frame{}
}

// nextTextFrame은 글(JSON) 프레임이 올 때까지 소리 프레임을 건너뛴다.
func (c *fakeConn) nextTextFrame(t *testing.T) frame {
	t.Helper()
	for {
		fr := c.nextFrame(t)
		if fr.typ == websocket.MessageText {
			return fr
		}
	}
}

func (c *fakeConn) waitGone(t *testing.T) {
	t.Helper()
	select {
	case <-c.gone:
	case <-time.After(wait):
		t.Fatal("연결이 끝나기를 기다리다 시간이 지났다")
	}
}

// fakeServer는 Soniox를 흉내 내는 WebSocket 서버다. 연결마다 첫 메시지(설정)를 읽어 두고 그 뒤의 프레임을 모은다.
type fakeServer struct {
	srv    *httptest.Server
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	conns  chan *fakeConn
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{conns: make(chan *fakeConn, 4)}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() {
		f.cancel()
		f.srv.Close()
		f.wg.Wait()
	})
	return f
}

func (f *fakeServer) url() string {
	return "ws://" + strings.TrimPrefix(f.srv.URL, "http://")
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	f.wg.Add(1)
	defer f.wg.Done()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(readLimit)

	fc := &fakeConn{conn: conn, frames: make(chan frame, 256), gone: make(chan struct{})}
	defer close(fc.gone)

	typ, data, err := conn.Read(f.ctx)
	if err != nil || typ != websocket.MessageText {
		return
	}
	if err := json.Unmarshal(data, &fc.config); err != nil {
		return
	}
	f.conns <- fc

	for {
		typ, data, err := conn.Read(f.ctx)
		if err != nil {
			return
		}
		fc.frames <- frame{typ: typ, data: data}
	}
}

func (f *fakeServer) accepted(t *testing.T) *fakeConn {
	t.Helper()
	select {
	case fc := <-f.conns:
		return fc
	case <-time.After(wait):
		t.Fatal("서버가 설정을 받지 못했다")
	}
	return nil
}

// verifyNoLeaks는 시험이 끝난 뒤(서버와 스트림을 정리한 뒤) 고루틴이 남지 않았는지 본다. 시험의 맨 앞에서 부른다.
func verifyNoLeaks(t *testing.T) {
	t.Helper()
	opt := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, opt) })
}

func testConfig(f *fakeServer) Config {
	return Config{
		APIKey:           config.NewSecret(testKey),
		URL:              f.url(),
		Model:            "stt-rt-v5",
		LanguageHints:    []string{"ko"},
		MaxEndpointDelay: 3 * time.Second,
		DialTimeout:      wait,
	}
}

func newTestRecognizer(t *testing.T, cfg Config) *Recognizer {
	t.Helper()
	r, err := New(cfg)
	require.NoError(t, err)
	return r
}

func openStream(t *testing.T, r *Recognizer, f *fakeServer) (*Stream, *fakeConn) {
	t.Helper()
	rs, err := r.Open(t.Context())
	require.NoError(t, err)
	s, ok := rs.(*Stream)
	require.True(t, ok)
	t.Cleanup(func() { _ = s.Close() })
	return s, f.accepted(t)
}

func nextEvent(t *testing.T, s *Stream) (voice.Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-s.Events():
		return ev, ok
	case <-time.After(wait):
		t.Fatal("사건을 기다리다 시간이 지났다")
	}
	return voice.Event{}, false
}

func requireClosed(t *testing.T, s *Stream) {
	t.Helper()
	ev, ok := nextEvent(t, s)
	require.False(t, ok, "채널이 닫혀야 하는데 사건이 왔다: %+v", ev)
}

// tokensJSON은 토큰 응답 하나를 JSON으로 만든다.
func tokensJSON(t *testing.T, tokens ...token) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"tokens": tokens})
	require.NoError(t, err)
	return string(data)
}

func TestNew(t *testing.T) {
	t.Parallel()

	base := Config{
		APIKey:           config.NewSecret(testKey),
		URL:              "wss://stt-rt.soniox.com/transcribe-websocket",
		Model:            "stt-rt-v5",
		LanguageHints:    []string{"ko", "en-US"},
		MaxEndpointDelay: 3 * time.Second,
	}

	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{"올바른 설정은 통과한다", func(*Config) {}, ""},
		{"키가 없으면 실패한다", func(c *Config) { c.APIKey = config.Secret{} }, "api key"},
		{"주소가 비어 있으면 실패한다", func(c *Config) { c.URL = "" }, "ws or wss"},
		{"주소가 ws나 wss가 아니면 실패한다", func(c *Config) { c.URL = "https://stt-rt.soniox.com/x" }, "ws or wss"},
		{"모델 이름이 비어 있으면 실패한다", func(c *Config) { c.Model = "" }, "model name"},
		{"모델 이름에 이상한 글자가 있으면 실패한다", func(c *Config) { c.Model = "stt rt/v5" }, "model name"},
		{"언어 힌트가 이상하면 실패한다", func(c *Config) { c.LanguageHints = []string{"ko", "한국어"} }, "language hint"},
		{"끝점 대기가 너무 짧으면 실패한다", func(c *Config) { c.MaxEndpointDelay = 100 * time.Millisecond }, "max endpoint delay"},
		{"끝점 대기가 너무 길면 실패한다", func(c *Config) { c.MaxEndpointDelay = 10 * time.Second }, "max endpoint delay"},
		{"연결 기한이 음수면 실패한다", func(c *Config) { c.DialTimeout = -time.Second }, "dial timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mutate(&cfg)
			r, err := New(cfg)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, DefaultDialTimeout, r.cfg.DialTimeout)
				assert.Equal(t, DefaultKeepaliveInterval, r.keepaliveInterval)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.NotContains(t, err.Error(), testKey)
		})
	}

	t.Run("설정의 로그 값에는 키가 없다", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Info("config", slog.Any("soniox", base))
		assert.NotContains(t, buf.String(), testKey)
		assert.Contains(t, buf.String(), `"api_key_set":true`)
		assert.Contains(t, buf.String(), `"model":"stt-rt-v5"`)
	})
}

func TestOpen(t *testing.T) {
	t.Run("첫 메시지는 설정 JSON이다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		_, fc := openStream(t, r, f)

		assert.Equal(t, testKey, fc.config["api_key"])
		assert.Equal(t, "stt-rt-v5", fc.config["model"])
		assert.Equal(t, "pcm_s16le", fc.config["audio_format"])
		assert.EqualValues(t, voice.InputSampleRate, fc.config["sample_rate"])
		assert.EqualValues(t, 1, fc.config["num_channels"])
		assert.Equal(t, []any{"ko"}, fc.config["language_hints"])
		endpointDetection, _ := fc.config["enable_endpoint_detection"].(bool)
		assert.True(t, endpointDetection)
		assert.EqualValues(t, 3000, fc.config["max_endpoint_delay_ms"])
	})

	t.Run("언어 힌트가 없으면 설정에 넣지 않는다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		cfg := testConfig(f)
		cfg.LanguageHints = nil
		r := newTestRecognizer(t, cfg)
		_, fc := openStream(t, r, f)

		_, present := fc.config["language_hints"]
		assert.False(t, present)
	})

	t.Run("연결하지 못하면 오류이고 오류에 키가 없다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		cfg := testConfig(f)
		// 아무도 듣지 않는 포트다.
		cfg.URL = "ws://127.0.0.1:1"
		cfg.DialTimeout = 2 * time.Second
		r := newTestRecognizer(t, cfg)

		s, err := r.Open(t.Context())
		require.Error(t, err)
		assert.Nil(t, s)
		assert.NotContains(t, err.Error(), testKey)
	})

	t.Run("서버가 연결 요청을 거절하면 오류다", func(t *testing.T) {
		verifyNoLeaks(t)
		rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		t.Cleanup(rejecting.Close)
		cfg := Config{
			APIKey:           config.NewSecret(testKey),
			URL:              "ws://" + strings.TrimPrefix(rejecting.URL, "http://"),
			Model:            "stt-rt-v5",
			MaxEndpointDelay: time.Second,
			DialTimeout:      wait,
		}
		r := newTestRecognizer(t, cfg)

		s, err := r.Open(t.Context())
		require.Error(t, err)
		assert.Nil(t, s)
		assert.NotContains(t, err.Error(), testKey)
	})
}

func TestWrite(t *testing.T) {
	t.Run("소리 조각은 순서대로 바이너리 프레임으로 간다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, fc := openStream(t, r, f)

		chunks := [][]byte{{1, 2, 3, 4}, {5, 6}, bytes.Repeat([]byte{7}, 3200)}
		for _, chunk := range chunks {
			require.NoError(t, s.Write(t.Context(), chunk))
		}
		for _, chunk := range chunks {
			fr := fc.nextFrame(t)
			assert.Equal(t, websocket.MessageBinary, fr.typ)
			assert.Equal(t, chunk, fr.data)
		}
		assert.EqualValues(t, 3206, s.bytesWritten.Load())
	})

	t.Run("빈 조각은 보내지 않는다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, fc := openStream(t, r, f)

		require.NoError(t, s.Write(t.Context(), nil))
		require.NoError(t, s.Write(t.Context(), []byte{9, 9}))
		fr := fc.nextFrame(t)
		assert.Equal(t, websocket.MessageBinary, fr.typ)
		assert.Equal(t, []byte{9, 9}, fr.data)
	})
}

func TestEvents(t *testing.T) {
	cases := []struct {
		name      string
		responses []string
		want      []voice.Event
	}{
		{
			name:      "미확정 토큰은 이어 붙인 글로 Partial이 된다",
			responses: []string{tokensJSON(t, tk("오늘은", 0.9, false), tk(" 동네", 0.8, false))},
			want:      []voice.Event{partial("오늘은 동네")},
		},
		{
			name: "확정 토큰과 끝점이 오면 Final이 되고, 그 뒤에는 새 마디가 시작된다",
			responses: []string{
				tokensJSON(t, tk("오늘은", 0.9, false)),
				tokensJSON(t, tk("오늘은", 0.95, true), tk(" 도서관에", 0.8, true), tk(",", 0.2, true), tk(" 다녀왔어", 0.7, true), tk(endToken, 0.1, true)),
				tokensJSON(t, tk(" 새로", 0.6, false)),
			},
			want: []voice.Event{partial("오늘은"), final("오늘은 도서관에, 다녀왔어", 0.7), partial("새로")},
		},
		{
			name:      "finalize의 <fin> 표시도 마디의 끝이다",
			responses: []string{tokensJSON(t, tk("다 말했어", 0.85, true), tk(finToken, 1, true))},
			want:      []voice.Event{final("다 말했어", 0.85)},
		},
		{
			name:      "글이 없는 토큰 응답은 사건을 내지 않는다",
			responses: []string{`{"tokens":[],"final_audio_proc_ms":0,"total_audio_proc_ms":100}`, tokensJSON(t, tk("응", 0.9, false))},
			want:      []voice.Event{partial("응")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifyNoLeaks(t)
			f := newFakeServer(t)
			r := newTestRecognizer(t, testConfig(f))
			s, fc := openStream(t, r, f)

			for _, msg := range tc.responses {
				fc.send(t, msg)
			}
			var got []voice.Event
			for range tc.want {
				ev, ok := nextEvent(t, s)
				require.True(t, ok, "채널이 먼저 닫혔다")
				got = append(got, ev)
			}
			assert.Equal(t, tc.want, got)

			require.NoError(t, s.Close())
			requireClosed(t, s)
		})
	}

	t.Run("finished가 오면 Failed 없이 채널이 닫힌다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, fc := openStream(t, r, f)

		fc.send(t, tokensJSON(t, tk("끝", 0.9, true), tk(endToken, 1, true)))
		fc.send(t, `{"tokens":[],"finished":true}`)

		ev, ok := nextEvent(t, s)
		require.True(t, ok)
		assert.Equal(t, final("끝", 0.9), ev)
		requireClosed(t, s)
		fc.waitGone(t)
	})
}

func TestFinalize(t *testing.T) {
	verifyNoLeaks(t)
	f := newFakeServer(t)
	r := newTestRecognizer(t, testConfig(f))
	s, fc := openStream(t, r, f)

	t.Run("finalize 제어 메시지를 글 프레임으로 보낸다", func(t *testing.T) {
		require.NoError(t, s.Finalize(t.Context()))
		fr := fc.nextFrame(t)
		assert.Equal(t, websocket.MessageText, fr.typ)
		assert.JSONEq(t, `{"type":"finalize"}`, string(fr.data))
	})
}

func TestKeepalive(t *testing.T) {
	t.Run("소리가 오지 않으면 keepalive를 거듭 보낸다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		r.keepaliveInterval = 50 * time.Millisecond
		_, fc := openStream(t, r, f)

		for range 2 {
			fr := fc.nextFrame(t)
			assert.Equal(t, websocket.MessageText, fr.typ)
			assert.JSONEq(t, `{"type":"keepalive"}`, string(fr.data))
		}
	})

	t.Run("소리가 오는 동안에는 보내지 않고 소리가 멈추면 다시 보낸다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		r.keepaliveInterval = 200 * time.Millisecond
		s, fc := openStream(t, r, f)

		const chunks = 25
		for range chunks {
			require.NoError(t, s.Write(t.Context(), []byte{0, 0}))
			time.Sleep(20 * time.Millisecond)
		}
		for range chunks {
			fr := fc.nextFrame(t)
			assert.Equal(t, websocket.MessageBinary, fr.typ, "소리가 이어지는 동안 keepalive가 끼어들었다")
		}

		fr := fc.nextFrame(t)
		assert.Equal(t, websocket.MessageText, fr.typ)
		assert.JSONEq(t, `{"type":"keepalive"}`, string(fr.data))
	})
}

func TestFailure(t *testing.T) {
	const secretReason = "private reason with user words"

	cases := []struct {
		name  string
		act   func(t *testing.T, fc *fakeConn)
		check func(t *testing.T, err error)
	}{
		{
			name: "오류 응답이 오면 코드와 종류만 담은 Failed가 온다",
			act: func(t *testing.T, fc *fakeConn) {
				fc.send(t, fmt.Sprintf(`{"tokens":[],"error_code":401,"error_type":"unauthenticated","error_message":%q}`, secretReason))
			},
			check: func(t *testing.T, err error) {
				var serverErr *Error
				require.ErrorAs(t, err, &serverErr)
				assert.Equal(t, 401, serverErr.Code)
				assert.Equal(t, "unauthenticated", serverErr.Type)
			},
		},
		{
			name: "서버가 연결을 끊으면 상태 코드만 담은 Failed가 온다",
			act: func(t *testing.T, fc *fakeConn) {
				require.NoError(t, fc.conn.Close(websocket.StatusInternalError, secretReason))
			},
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrConnectionLost)
				assert.Contains(t, err.Error(), "1011")
			},
		},
		{
			name: "서버가 인사 없이 사라져도 Failed가 온다",
			act: func(t *testing.T, fc *fakeConn) {
				require.NoError(t, fc.conn.CloseNow())
			},
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrConnectionLost)
			},
		},
		{
			name: "읽을 수 없는 응답이 오면 Failed가 온다",
			act: func(t *testing.T, fc *fakeConn) {
				fc.send(t, "not json "+secretReason)
			},
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrMalformedResponse)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifyNoLeaks(t)
			f := newFakeServer(t)
			r := newTestRecognizer(t, testConfig(f))
			s, fc := openStream(t, r, f)

			tc.act(t, fc)

			ev, ok := nextEvent(t, s)
			require.True(t, ok, "Failed 없이 채널이 닫혔다")
			require.Equal(t, voice.EventFailed, ev.Kind)
			require.Error(t, ev.Err)
			tc.check(t, ev.Err)
			assert.NotContains(t, ev.Err.Error(), secretReason)
			assert.NotContains(t, ev.Err.Error(), testKey)
			requireClosed(t, s)

			require.Error(t, s.Write(t.Context(), []byte{1}))
			require.NoError(t, s.Close())
		})
	}
}

func TestClose(t *testing.T) {
	t.Run("빈 프레임을 보내고 연결을 닫으며 채널은 Failed 없이 닫힌다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, fc := openStream(t, r, f)
		require.NoError(t, s.Write(t.Context(), []byte{1, 2}))

		require.NoError(t, s.Close())

		fr := fc.nextFrame(t)
		assert.Equal(t, []byte{1, 2}, fr.data)
		fr = fc.nextFrame(t)
		assert.Equal(t, websocket.MessageBinary, fr.typ)
		assert.Empty(t, fr.data, "스트림의 끝은 빈 프레임이다")
		fc.waitGone(t)
		requireClosed(t, s)
	})

	t.Run("몇 번을 불러도 되고 그 뒤의 Write와 Finalize는 ErrStreamClosed다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, _ := openStream(t, r, f)

		require.NoError(t, s.Close())
		require.NoError(t, s.Close())
		require.ErrorIs(t, s.Write(t.Context(), []byte{1}), voice.ErrStreamClosed)
		require.ErrorIs(t, s.Write(t.Context(), nil), voice.ErrStreamClosed)
		require.ErrorIs(t, s.Finalize(t.Context()), voice.ErrStreamClosed)
	})

	t.Run("받는 쪽이 사건을 읽지 않아도 Close는 돌아온다", func(t *testing.T) {
		verifyNoLeaks(t)
		f := newFakeServer(t)
		r := newTestRecognizer(t, testConfig(f))
		s, fc := openStream(t, r, f)

		// 버퍼보다 많은 사건을 밀어 넣어 읽기 고루틴이 받는 쪽을 기다리게 만든다.
		for i := range eventBuffer + 8 {
			fc.send(t, tokensJSON(t, tk(fmt.Sprintf("말%d", i), 0.9, false)))
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = s.Close()
		}()
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatal("Close가 돌아오지 않는다")
		}
		for {
			if _, ok := nextEvent(t, s); !ok {
				break
			}
		}
	})
}

func TestLogsCarryNoSpeech(t *testing.T) {
	verifyNoLeaks(t)
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewJSONHandler(&lockedWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	f := newFakeServer(t)
	cfg := testConfig(f)
	cfg.Logger = logger
	r := newTestRecognizer(t, cfg)
	r.keepaliveInterval = 50 * time.Millisecond
	s, fc := openStream(t, r, f)

	const speech = "비밀스러운 말"
	require.NoError(t, s.Write(t.Context(), []byte{1, 2, 3}))
	fc.send(t, tokensJSON(t, tk(speech, 0.9, true), tk(endToken, 1, true)))
	ev, ok := nextEvent(t, s)
	require.True(t, ok)
	assert.Equal(t, final(speech, 0.9), ev)
	// keepalive가 적어도 하나 지나가게 한다. 소리 프레임과의 순서는 타이머에 달려 있으므로 보지 않는다.
	assert.JSONEq(t, `{"type":"keepalive"}`, string(fc.nextTextFrame(t).data))
	fc.send(t, `{"tokens":[],"error_code":503,"error_type":"service_unavailable","error_message":"`+speech+`"}`)
	ev, ok = nextEvent(t, s)
	require.True(t, ok)
	require.Equal(t, voice.EventFailed, ev.Kind)
	requireClosed(t, s)
	require.NoError(t, s.Close())

	mu.Lock()
	logs := buf.String()
	mu.Unlock()
	assert.Contains(t, logs, "recognition stream opened")
	assert.Contains(t, logs, "keepalive sent")
	assert.Contains(t, logs, `"error_code":503`)
	assert.Contains(t, logs, "recognition stream closed")
	assert.NotContains(t, logs, testKey)
	assert.NotContains(t, logs, speech)
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
