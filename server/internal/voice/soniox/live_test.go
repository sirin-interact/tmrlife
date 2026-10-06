package soniox_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
	"github.com/sirin-interact/tmrlife/server/internal/voice/soniox"
)

// 실제 Soniox에 붙어 설정 메시지와 응답의 모양이 문서와 같은지 본다. 가짜 서버로는 확인할 수 없는 것이다.
// VOICE_LIVE_TESTS=1, SONIOX_API_KEY, 그리고 16kHz 모노 PCM WAV의 경로(SONIOX_LIVE_WAV)가 있을 때만 돈다.
func TestLiveRecognize(t *testing.T) {
	if os.Getenv("VOICE_LIVE_TESTS") != "1" {
		t.Skip("VOICE_LIVE_TESTS=1일 때만 실제 공급자에 붙는다")
	}
	key := strings.TrimSpace(os.Getenv("SONIOX_API_KEY"))
	wavPath := strings.TrimSpace(os.Getenv("SONIOX_LIVE_WAV"))
	if key == "" || wavPath == "" {
		t.Skip("SONIOX_API_KEY와 SONIOX_LIVE_WAV가 있어야 한다")
	}
	wav, err := os.ReadFile(wavPath)
	require.NoError(t, err)
	require.Greater(t, len(wav), 44, "WAV 머리말 뒤에 소리가 있어야 한다")
	pcm := wav[44:]

	rec, err := soniox.New(soniox.Config{
		APIKey:           config.NewSecret(key),
		URL:              envOr("SONIOX_URL", "wss://stt-rt.soniox.com/transcribe-websocket"),
		Model:            envOr("SONIOX_MODEL", "stt-rt-v5"),
		LanguageHints:    []string{"ko"},
		MaxEndpointDelay: 3 * time.Second,
		Logger:           slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	clk := clock.Real{}
	opened := clk.Now()
	stream, err := rec.Open(ctx)
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	t.Logf("연결 %dms", clk.Now().Sub(opened).Milliseconds())

	// 실제 마이크처럼 100ms 분량을 100ms마다 보낸다. 다 보내면 확정을 요청한다.
	const chunk = voice.InputBytesPerSecond / 10
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < len(pcm); i += chunk {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
			end := min(i+chunk, len(pcm))
			if err := stream.Write(ctx, pcm[i:end]); err != nil {
				return
			}
		}
		_ = stream.Finalize(ctx)
	}()

	var finals []voice.Event
	partials := 0
	deadline := time.After(time.Duration(len(pcm)/voice.InputBytesPerSecond+15) * time.Second)
loop:
	for {
		select {
		case e, ok := <-stream.Events():
			if !ok {
				break loop
			}
			switch e.Kind {
			case voice.EventPartial:
				partials++
			case voice.EventFinal:
				finals = append(finals, e)
				t.Logf("끝점: %q (최저 확신도 %.2f)", e.Text, e.MinConfidence)
			case voice.EventFailed:
				require.NoError(t, e.Err)
			}
			if len(finals) >= 3 {
				break loop
			}
		case <-deadline:
			break loop
		}
	}

	require.NotEmpty(t, finals, "끝점까지의 말이 하나는 와야 한다")
	assert.Positive(t, partials, "중간 결과가 먼저 온다")
	for _, f := range finals {
		assert.True(t, strings.ContainsFunc(f.Text, func(r rune) bool { return unicode.Is(unicode.Hangul, r) }),
			"한국어 글이어야 한다: %q", f.Text)
		assert.Greater(t, f.MinConfidence, float32(0))
		assert.LessOrEqual(t, f.MinConfidence, float32(1))
	}
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
