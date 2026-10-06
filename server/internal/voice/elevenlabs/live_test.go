package elevenlabs_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
	"github.com/sirin-interact/tmrlife/server/internal/voice/elevenlabs"
)

// 실제 ElevenLabs에 붙어 요청의 모양과 돌려주는 소리의 꼴을 본다. VOICE_LIVE_TESTS=1과 키가 있을 때만 돈다.
func TestLiveSynthesize(t *testing.T) {
	if os.Getenv("VOICE_LIVE_TESTS") != "1" {
		t.Skip("VOICE_LIVE_TESTS=1일 때만 실제 공급자에 붙는다")
	}
	key := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	if key == "" {
		t.Skip("ELEVENLABS_API_KEY가 있어야 한다")
	}
	// 연결을 유지하는 클라이언트는 시험이 끝난 뒤에도 유휴 연결의 고루틴을 남긴다. 누수 검사가 그것을 잡지 않게 여기서 닫는다.
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	defer client.CloseIdleConnections()
	synth, err := elevenlabs.New(elevenlabs.Config{
		APIKey:     config.NewSecret(key),
		BaseURL:    envOr("ELEVENLABS_URL", "https://api.elevenlabs.io"),
		VoiceID:    envOr("ELEVENLABS_VOICE_ID", "hWXqitL3DEOLD49pgNWR"),
		Model:      envOr("ELEVENLABS_MODEL", "eleven_flash_v2_5"),
		HTTPClient: client,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	clk := clock.Real{}
	started := clk.Now()
	stream, err := synth.Synthesize(ctx, "오늘 하루는 어땠어요?")
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()

	first := make([]byte, 1024)
	n, err := stream.Read(first)
	require.NoError(t, err)
	t.Logf("첫 소리까지 %dms", clk.Now().Sub(started).Milliseconds())
	rest, err := io.ReadAll(stream)
	require.NoError(t, err)
	total := n + len(rest)
	t.Logf("전체 %d바이트 = %.2f초 (%dms)", total, float64(total)/float64(voice.OutputSampleRate*2), clk.Now().Sub(started).Milliseconds())

	assert.Equal(t, 0, total%2, "s16le 샘플은 두 바이트씩이다")
	// 짧은 한 문장이라도 반 초는 넘는다. 24kHz s16le로 반 초는 24,000바이트다.
	assert.Greater(t, total, voice.OutputSampleRate)
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
