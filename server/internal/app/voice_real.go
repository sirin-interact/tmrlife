package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
	"github.com/sirin-interact/tmrlife/server/internal/voice/elevenlabs"
	"github.com/sirin-interact/tmrlife/server/internal/voice/soniox"
)

// prewarmBudget은 고정 문구의 소리를 미리 만드는 데 주는 시간이다. 서버가 뜨는 것을 막지 않고 뒤에서 돈다.
const prewarmBudget = 2 * time.Minute

// newRealVoiceProvider는 Soniox로 알아듣고 ElevenLabs로 읽는 공급자를 만든다.
//
// 고정 문구의 소리는 뒤에서 미리 만들어 둔다. 위기 응답처럼 가장 무거운 순간의 말이 합성을 기다리지 않게 하려는 것이다.
// 미리 만들지 못한 글은 그때그때 공급자를 부른다.
func (d *Deps) newRealVoiceProvider(ctx context.Context) (voice.Provider, error) {
	cfg := d.Config
	recognizer, err := soniox.New(soniox.Config{
		APIKey:           cfg.Providers.SonioxAPIKey,
		URL:              cfg.Voice.SonioxURL,
		Model:            cfg.Voice.SonioxModel,
		LanguageHints:    cfg.Voice.LanguageHints,
		MaxEndpointDelay: cfg.Voice.MaxEndpointDelay,
		Logger:           d.Logger,
	})
	if err != nil {
		return voice.Provider{}, fmt.Errorf("create the speech recognizer: %w", err)
	}
	synthesizer, err := elevenlabs.New(elevenlabs.Config{
		APIKey:  cfg.Providers.ElevenLabsAPIKey,
		BaseURL: cfg.Voice.ElevenLabsURL,
		VoiceID: cfg.Voice.VoiceID,
		Model:   cfg.Voice.TTSModel,
		Speed:   cfg.Voice.Speed,
		Logger:  d.Logger,
		Clock:   d.Clock,
	})
	if err != nil {
		return voice.Provider{}, fmt.Errorf("create the speech synthesizer: %w", err)
	}
	cached, err := voice.NewCached(synthesizer, d.Logger)
	if err != nil {
		return voice.Provider{}, fmt.Errorf("create the speech cache: %w", err)
	}
	catalogue, err := phrases.Load()
	if err != nil {
		return voice.Provider{}, fmt.Errorf("load phrases: %w", err)
	}

	// 시작할 때 받은 컨텍스트는 뜨는 동안만 산다. 미리 만드는 일은 그보다 오래 걸릴 수 있어 취소를 떼고 시간만 건다.
	prewarmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prewarmBudget)
	go func() {
		defer cancel()
		cached.Prewarm(prewarmCtx, catalogue.SpeechTexts())
	}()

	d.Logger.LogAttrs(ctx, slog.LevelInfo, "voice providers ready",
		slog.String("voice_provider", string(cfg.Voice.Provider)),
		slog.String("soniox_model", cfg.Voice.SonioxModel),
		slog.String("elevenlabs_model", cfg.Voice.TTSModel),
	)
	return voice.Provider{Recognizer: recognizer, Synthesizer: cached}, nil
}
