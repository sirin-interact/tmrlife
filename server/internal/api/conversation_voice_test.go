package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
	voicefake "github.com/sirin-interact/tmrlife/server/internal/voice/fake"
)

// voiceFixture는 가짜 음성 공급자를 붙인 소켓 자리다.
type voiceFixture struct {
	*socketFixture
	recognizer  *voicefake.Recognizer
	synthesizer *voicefake.Synthesizer
}

func newVoiceFixture(
	t *testing.T, recognizer voicefake.RecognizerOptions, synthesizer voicefake.SynthesizerOptions,
	adjust func(*ConversationOptions), adjustEngine func(*engine.Options),
) *voiceFixture {
	t.Helper()
	rec := voicefake.NewRecognizer(recognizer)
	syn := voicefake.NewSynthesizer(synthesizer)
	f := newSocketFixtureWith(t, func(opts *ConversationOptions) {
		opts.Voice = voice.Provider{Recognizer: rec, Synthesizer: syn}
		opts.BargeIn = true
		if adjust != nil {
			adjust(opts)
		}
	}, adjustEngine)
	f.talk.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply("그랬네요. 오늘은 어떤 하루였어요?")
	})
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":0,"evidence":"","reason":"none"}`)
	})
	return &voiceFixture{socketFixture: f, recognizer: rec, synthesizer: syn}
}

func wsStartVoice() string { return `{"type":"start","mode":"voice"}` }

// wsFrame은 받은 프레임 하나다. 글이면 type과 내용이, 소리면 바이트 수가 있다.
type wsFrame struct {
	kind   string
	body   map[string]any
	binary int
}

// wsReader는 소켓에서 오는 프레임을 뒤에서 계속 읽어 모아 둔다.
//
// 소켓 라이브러리는 Read에 준 컨텍스트가 끝나면 연결을 닫아 버린다. 그래서 "잠깐 동안 아무것도 오지 않는다"를
// 짧은 컨텍스트로 확인할 수 없다. 읽기는 한 고루틴이 맡고, 시험은 모아 둔 통에서 기다린다.
type wsReader struct {
	frames chan wsFrame
}

func startReader(ctx context.Context, c *websocket.Conn) *wsReader {
	r := &wsReader{frames: make(chan wsFrame, 4096)}
	go func() {
		defer close(r.frames)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				r.frames <- wsFrame{binary: len(data)}
				continue
			}
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				r.frames <- wsFrame{kind: "?"}
				continue
			}
			kind, _ := m["type"].(string)
			r.frames <- wsFrame{kind: kind, body: m}
		}
	}()
	return r
}

// until은 주어진 종류의 글이 올 때까지 읽는다. 그 사이의 소리 프레임은 세기만 하고, 다른 글은 모아 돌려준다.
func (r *wsReader) until(t *testing.T, kind string) (map[string]any, []wsFrame) {
	t.Helper()
	var skipped []wsFrame
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-r.frames:
			require.True(t, ok, "%s을 기다리는데 연결이 닫혔다", kind)
			if f.kind == kind {
				return f.body, skipped
			}
			require.NotEqual(t, "error", f.kind, "기다리던 %s 대신 오류가 왔다: %v", kind, f.body)
			skipped = append(skipped, f)
		case <-timeout:
			t.Fatalf("%s이 오지 않았다. 그동안 온 것: %v", kind, kinds(skipped))
		}
	}
}

// quiet는 잠깐 동안 아무 글도 오지 않는지 본다. 소리 프레임은 상관하지 않는다.
func (r *wsReader) quiet(t *testing.T, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case f, ok := <-r.frames:
			if !ok {
				return
			}
			require.Empty(t, f.kind, "조용해야 하는데 글이 왔다: %v", f.body)
		case <-deadline:
			return
		}
	}
}

func audioBytes(frames []wsFrame) int {
	n := 0
	for _, f := range frames {
		n += f.binary
	}
	return n
}

func kinds(frames []wsFrame) []string {
	var out []string
	for _, f := range frames {
		if f.kind != "" {
			out = append(out, f.kind)
		}
	}
	return out
}

// 음성으로 시작하면 첫 안부가 글과 소리로 나가고, 알아들은 말로 턴이 돌아 답도 소리로 나간다.
func TestConversationSocketVoiceTurn(t *testing.T) {
	f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{}, nil, nil)
	cookie := f.login(t, "voice-turn@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	rd := startReader(ctx, c)

	wsSend(ctx, t, c, wsStartVoice())
	ready, _ := rd.until(t, "ready")
	assert.Equal(t, "voice", ready["mode"])
	assert.Equal(t, true, ready["voice_available"])
	streams := f.recognizer.Streams()
	require.Len(t, streams, 1, "start에서 인식 스트림이 열린다")
	stream := streams[0]

	opening, _ := rd.until(t, "ai_text")
	start, _ := rd.until(t, "audio_start")
	assert.Equal(t, opening["seq"], start["seq"])
	assert.EqualValues(t, voice.OutputSampleRate, start["sample_rate"])
	end, frames := rd.until(t, "audio_end")
	assert.Equal(t, "done", end["reason"])
	assert.Equal(t, opening["seq"], end["seq"])
	assert.Positive(t, audioBytes(frames), "첫 안부의 소리가 조각으로 내려온다")
	assert.Equal(t, []string{"오늘 하루는 어땠어요?"}, f.synthesizer.Texts(), "음성용 글을 합성한다")

	t.Run("올라온 소리는 인식기로 간다", func(t *testing.T) {
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		require.Eventually(t, func() bool { return stream.Written() == 3200 }, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("중간 결과는 자막으로, 끝점은 글로 턴을 돌린다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventPartial, Text: "오늘은"})
		partial, _ := rd.until(t, "transcript")
		assert.Equal(t, "오늘은", partial["text"])
		assert.Equal(t, false, partial["final"])
		_, hasID := partial["client_message_id"]
		assert.False(t, hasID)

		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "오늘은 좀 피곤했어", MinConfidence: 0.95})
		final, _ := rd.until(t, "transcript")
		assert.Equal(t, "오늘은 좀 피곤했어", final["text"])
		assert.Equal(t, true, final["final"])
		id, _ := final["client_message_id"].(string)
		require.NotEmpty(t, id)

		thinking, _ := rd.until(t, "thinking")
		assert.Equal(t, id, thinking["client_message_id"], "화면은 이 식별자로 짝을 맞춘다")
		answer, _ := rd.until(t, "ai_text")
		assert.Equal(t, "그랬네요. 오늘은 어떤 하루였어요?", answer["text"])
		start, _ := rd.until(t, "audio_start")
		assert.Equal(t, answer["seq"], start["seq"])
		end, frames := rd.until(t, "audio_end")
		assert.Equal(t, "done", end["reason"])
		assert.Positive(t, audioBytes(frames))
	})

	t.Run("빈 끝점은 아무것도 하지 않는다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "   ", MinConfidence: 1})
		rd.quiet(t, 200*time.Millisecond)
	})

	t.Run("글로 쓴 말도 음성 방식에서 그대로 받고 답을 읽어 준다", func(t *testing.T) {
		wsSend(ctx, t, c, wsUserText(uuid.New(), "글로도 써 볼게"))
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
		_, _ = rd.until(t, "audio_start")
		end, _ := rd.until(t, "audio_end")
		assert.Equal(t, "done", end["reason"])
	})

	t.Run("다 말했다고 하면 인식기에 확정을 요청한다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"finalize"}`)
		require.Eventually(t, func() bool { return stream.Finalized() == 1 }, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("끝내면 인식 스트림이 닫힌다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"end"}`)
		ended, _ := rd.until(t, "ended")
		assert.Equal(t, "user", ended["reason"])
		require.Eventually(t, stream.Closed, 2*time.Second, 10*time.Millisecond)
	})
}

// 끼어들기: 사용자가 누르거나 말을 시작하면 읽어 주던 말이 멈춘다. 되돌아온 메아리는 말로 치지 않는다.
func TestConversationSocketVoiceInterrupt(t *testing.T) {
	// 조각마다 지연을 두어 읽어 주는 동안 끼어들 틈을 만든다. 첫 안부(11글자)는 440ms, 조각은 100ms씩이다.
	f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{ChunkDelay: 80 * time.Millisecond}, nil, nil)
	cookie := f.login(t, "voice-interrupt@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	rd := startReader(ctx, c)

	wsSend(ctx, t, c, wsStartVoice())
	_, _ = rd.until(t, "ready")
	stream := f.recognizer.Streams()[0]
	_, _ = rd.until(t, "ai_text")
	_, _ = rd.until(t, "audio_start")

	t.Run("누르면 소리가 멈추고 멈췄다고 알린다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"interrupt"}`)
		end, _ := rd.until(t, "audio_end")
		assert.Equal(t, "interrupted", end["reason"])
	})

	t.Run("읽어 주는 동안 말을 시작하면 멈춘다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "오늘 좀 힘들었어", MinConfidence: 0.9})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
		_, _ = rd.until(t, "audio_start")

		stream.Emit(voice.Event{Kind: voice.EventPartial, Text: "그런데"})
		end, skipped := rd.until(t, "audio_end")
		assert.Equal(t, "interrupted", end["reason"])
		assert.Contains(t, kinds(skipped), "transcript", "끼어든 말도 자막으로 보인다")
	})

	t.Run("읽어 주는 동안 되돌아온 AI의 말은 버린다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "다시 말해 볼게", MinConfidence: 0.9})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		answer, _ := rd.until(t, "ai_text")
		_, _ = rd.until(t, "audio_start")

		spoken, _ := answer["text"].(string)
		stream.Emit(voice.Event{Kind: voice.EventPartial, Text: spoken[:len("그랬네요")]})
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: spoken, MinConfidence: 0.9})
		end, skipped := rd.until(t, "audio_end")
		assert.Equal(t, "done", end["reason"], "메아리로는 말을 끊지 않는다")
		assert.NotContains(t, kinds(skipped), "transcript")
		assert.NotContains(t, kinds(skipped), "thinking", "메아리로 턴이 돌지 않는다")
		rd.quiet(t, 200*time.Millisecond)
	})

	t.Run("답하는 동안 들어온 말은 모아 두었다가 턴이 끝난 뒤 한 마디로 넣는다", func(t *testing.T) {
		// 대화 모델이 답하는 동안 두 번 끝점이 온다.
		release := make(chan struct{})
		f.talk.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return fake.Reply("천천히 말해도 괜찮아요.")
		})
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "첫 마디", MinConfidence: 0.9})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "둘째 마디", MinConfidence: 0.7})
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "셋째 마디", MinConfidence: 0.8})
		close(release)

		_, _ = rd.until(t, "ai_text")
		merged, _ := rd.until(t, "transcript")
		assert.Equal(t, "둘째 마디 셋째 마디", merged["text"])
		assert.Equal(t, true, merged["final"])
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
	})
}

// 방식 전환과 음성을 쓸 수 없는 경우.
func TestConversationSocketVoiceModes(t *testing.T) {
	t.Run("채팅으로 열고 음성으로 바꾸고 다시 채팅으로 돌아온다", func(t *testing.T) {
		f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{}, nil, nil)
		cookie := f.login(t, "voice-modes@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		rd := startReader(ctx, c)

		wsSend(ctx, t, c, wsStartMessage())
		ready, _ := rd.until(t, "ready")
		assert.Equal(t, "chat", ready["mode"])
		assert.Equal(t, true, ready["voice_available"])
		_, skipped := rd.until(t, "ai_text")
		assert.Empty(t, skipped)
		rd.quiet(t, 150*time.Millisecond)
		assert.Empty(t, f.synthesizer.Texts(), "채팅 방식에서는 읽어 주지 않는다")

		// 채팅 방식에서 온 소리는 한 번만 알리고 버린다.
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		problem := <-rd.frames
		require.Equal(t, "error", problem.kind, problem.body)
		assert.Equal(t, "invalid_message", problem.body["code"])
		rd.quiet(t, 150*time.Millisecond)

		wsSend(ctx, t, c, `{"type":"set_mode","mode":"voice"}`)
		mode, _ := rd.until(t, "mode")
		assert.Equal(t, "voice", mode["mode"])
		require.Len(t, f.recognizer.Streams(), 1)
		stream := f.recognizer.Streams()[0]

		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "음성으로 바꿨어", MinConfidence: 0.9})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
		_, _ = rd.until(t, "audio_end")

		wsSend(ctx, t, c, `{"type":"set_mode","mode":"chat"}`)
		mode, _ = rd.until(t, "mode")
		assert.Equal(t, "chat", mode["mode"])
		require.Eventually(t, stream.Closed, 2*time.Second, 10*time.Millisecond)

		// 다시 음성으로 가면 새 스트림이 열린다.
		wsSend(ctx, t, c, `{"type":"set_mode","mode":"voice"}`)
		mode, _ = rd.until(t, "mode")
		assert.Equal(t, "voice", mode["mode"])
		assert.Len(t, f.recognizer.Streams(), 2)
	})

	t.Run("음성이 없는 서버는 음성 요청에 채팅으로 연다", func(t *testing.T) {
		f := newSocketFixture(t, nil)
		cookie := f.login(t, "voice-none@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		rd := startReader(ctx, c)

		wsSend(ctx, t, c, wsStartVoice())
		first := <-rd.frames
		require.Equal(t, "error", first.kind, first.body)
		assert.Equal(t, "voice_unavailable", first.body["code"])
		ready, _ := rd.until(t, "ready")
		assert.Equal(t, "chat", ready["mode"])
		assert.Equal(t, false, ready["voice_available"])
		_, _ = rd.until(t, "ai_text")

		wsSend(ctx, t, c, `{"type":"set_mode","mode":"voice"}`)
		problem := <-rd.frames
		require.Equal(t, "error", problem.kind, problem.body)
		assert.Equal(t, "voice_unavailable", problem.body["code"])
		mode, _ := rd.until(t, "mode")
		assert.Equal(t, "chat", mode["mode"])
	})

	t.Run("인식이 죽으면 한 번 다시 열고, 또 죽으면 채팅으로 내려간다", func(t *testing.T) {
		f := newVoiceFixture(t, voicefake.RecognizerOptions{MaxOpens: 2}, voicefake.SynthesizerOptions{}, nil, nil)
		cookie := f.login(t, "voice-fail@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		rd := startReader(ctx, c)

		wsSend(ctx, t, c, wsStartVoice())
		_, _ = rd.until(t, "ready")
		_, _ = rd.until(t, "audio_end")
		first := f.recognizer.Streams()[0]

		first.Fail(errors.New("connection reset"))
		require.Eventually(t, func() bool { return len(f.recognizer.Streams()) == 2 }, 2*time.Second, 10*time.Millisecond)
		rd.quiet(t, 150*time.Millisecond)
		second := f.recognizer.Streams()[1]
		second.Emit(voice.Event{Kind: voice.EventFinal, Text: "아직 들려?", MinConfidence: 0.9})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "audio_end")

		second.Fail(errors.New("connection reset"))
		problem := <-rd.frames
		require.Equal(t, "error", problem.kind, problem.body)
		assert.Equal(t, "voice_unavailable", problem.body["code"])
		mode, _ := rd.until(t, "mode")
		assert.Equal(t, "chat", mode["mode"])

		// 채팅으로는 그대로 이어진다.
		wsSend(ctx, t, c, wsUserText(uuid.New(), "그럼 글로 할게"))
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
		rd.quiet(t, 150*time.Millisecond)
	})

	t.Run("소리가 너무 빨리 오면 한 번 알리고 버린다", func(t *testing.T) {
		f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{}, nil, nil)
		cookie := f.login(t, "voice-flood@example.com")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		c, _, err := f.dial(t, cookie, nil)
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		rd := startReader(ctx, c)

		wsSend(ctx, t, c, wsStartVoice())
		_, _ = rd.until(t, "ready")
		_, _ = rd.until(t, "audio_end")
		stream := f.recognizer.Streams()[0]

		// 한꺼번에 받는 양은 2초 분량(64,000바이트)이다. 그보다 많이 보낸다.
		for range 30 {
			require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		}
		problem := <-rd.frames
		require.Equal(t, "error", problem.kind, problem.body)
		assert.Equal(t, "rate_limited", problem.body["code"])
		assert.LessOrEqual(t, stream.Written(), audioBurstBytes)
	})
}

// 한 마디씩 듣기: 누르면 다음 끝점까지만 듣고 스스로 멈춘다. 멈춘 동안의 소리와 말은 버린다.
func TestConversationSocketVoiceListenOnce(t *testing.T) {
	f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{ChunkDelay: 80 * time.Millisecond}, nil, nil)
	cookie := f.login(t, "voice-listen@example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	rd := startReader(ctx, c)

	wsSend(ctx, t, c, wsStartVoice())
	_, _ = rd.until(t, "ready")
	stream := f.recognizer.Streams()[0]
	_, _ = rd.until(t, "audio_start")

	t.Run("누르면 읽어 주던 말을 멈추고 듣기 시작한다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"listen","active":true}`)
		end, _ := rd.until(t, "audio_end")
		assert.Equal(t, "interrupted", end["reason"])
		listening, _ := rd.until(t, "listening")
		assert.Equal(t, true, listening["active"])
	})

	t.Run("끝점이 오면 그 말로 턴을 돌리고 스스로 듣기를 멈춘다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "오늘은 좀 피곤했어", MinConfidence: 0.9})
		final, _ := rd.until(t, "transcript")
		assert.Equal(t, true, final["final"])
		listening, _ := rd.until(t, "listening")
		assert.Equal(t, false, listening["active"])
		_, _ = rd.until(t, "thinking")
		_, _ = rd.until(t, "ai_text")
		_, _ = rd.until(t, "audio_end")
	})

	t.Run("멈춘 동안의 소리는 인식기로 가지 않고, 말은 턴이 되지 않는다", func(t *testing.T) {
		written := stream.Written()
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		stream.Emit(voice.Event{Kind: voice.EventPartial, Text: "혼잣말"})
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "아무 말이나 중얼중얼", MinConfidence: 0.9})
		rd.quiet(t, 300*time.Millisecond)
		assert.Equal(t, written, stream.Written())
	})

	t.Run("다시 누르면 다음 한 마디를 듣는다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"listen","active":true}`)
		listening, _ := rd.until(t, "listening")
		assert.Equal(t, true, listening["active"])
		require.NoError(t, c.Write(ctx, websocket.MessageBinary, make([]byte, 3200)))
		require.Eventually(t, func() bool { return stream.Written() == 3200 }, 2*time.Second, 10*time.Millisecond)

		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "두 번째 말", MinConfidence: 0.9})
		final, _ := rd.until(t, "transcript")
		assert.Equal(t, "두 번째 말", final["text"])
		listening, _ = rd.until(t, "listening")
		assert.Equal(t, false, listening["active"])
		_, _ = rd.until(t, "audio_end")
	})

	t.Run("듣는 중에 끄면 바로 멈춘다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"listen","active":true}`)
		_, _ = rd.until(t, "listening")
		wsSend(ctx, t, c, `{"type":"listen","active":false}`)
		listening, _ := rd.until(t, "listening")
		assert.Equal(t, false, listening["active"])
	})

	t.Run("채팅 방식에서는 받지 않는다", func(t *testing.T) {
		wsSend(ctx, t, c, `{"type":"set_mode","mode":"chat"}`)
		_, _ = rd.until(t, "mode")
		wsSend(ctx, t, c, `{"type":"listen","active":true}`)
		problem := <-rd.frames
		require.Equal(t, "error", problem.kind, problem.body)
		assert.Equal(t, "invalid_message", problem.body["code"])
	})
}

// 흐릿하게 들은 무거운 말에는 되묻고, 바로 앞에서 되물었으면 또 묻지 않는다.
func TestConversationSocketVoiceMishear(t *testing.T) {
	f := newVoiceFixture(t, voicefake.RecognizerOptions{}, voicefake.SynthesizerOptions{}, nil,
		func(opts *engine.Options) { opts.MishearBelow = 0.6 })
	cookie := f.login(t, "voice-mishear@example.com")
	f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
		return fake.Reply(`{"stage":1,"evidence":"사라졌으면","reason":"vague"}`)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	c, _, err := f.dial(t, cookie, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	rd := startReader(ctx, c)

	wsSend(ctx, t, c, wsStartVoice())
	_, _ = rd.until(t, "ready")
	_, _ = rd.until(t, "audio_end")
	stream := f.recognizer.Streams()[0]

	stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "그냥 다 사라졌으면 좋겠어", MinConfidence: 0.4})
	_, _ = rd.until(t, "transcript")
	_, _ = rd.until(t, "thinking")
	check, _ := rd.until(t, "ai_text")
	assert.Equal(t, "fixed", check["origin"])
	assert.Contains(t, check["text"], "제대로 들었는지")
	_, _ = rd.until(t, "audio_end")

	t.Run("되물은 뒤에 또 흐릿하면 들은 대로 되묻기 걸음으로 간다", func(t *testing.T) {
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "그냥 다 사라졌으면 좋겠다고", MinConfidence: 0.4})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		reflect, _ := rd.until(t, "ai_text")
		assert.NotContains(t, reflect["text"], "제대로 들었는지")
		_, _ = rd.until(t, "audio_end")
	})

	t.Run("또렷하게 들은 무거운 말에는 되묻지 않는다", func(t *testing.T) {
		f.judge.SetHandler(func(context.Context, ai.Request) fake.Step {
			return fake.Reply(`{"stage":2,"evidence":"죽고 싶다","reason":"direct"}`)
		})
		stream.Emit(voice.Event{Kind: voice.EventFinal, Text: "죽고 싶다", MinConfidence: 0.3})
		_, _ = rd.until(t, "transcript")
		_, _ = rd.until(t, "thinking")
		answer, skipped := rd.until(t, "ai_text")
		assert.Contains(t, kinds(skipped), "resources")
		assert.Contains(t, answer["text"], "109", "대응 단계는 확신도와 상관없이 미리 써 둔 말이다")
		assert.Equal(t, "fixed", answer["origin"])
	})
}
