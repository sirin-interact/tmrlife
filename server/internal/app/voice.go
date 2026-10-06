package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/voice"
	"github.com/sirin-interact/tmrlife/server/internal/voice/fake"
)

// NewVoiceProvider는 설정이 고른 공급자로 음성 인식과 합성을 만든다.
//
// off면 빈 Provider를 돌려주고, 채널은 음성 방식을 열지 않는다. fake는 대본대로 알아듣고 무음을 돌려준다(운영에서는 설정이 막는다).
// 실제 공급자는 고정 문구의 소리를 뒤에서 미리 만들어 둔다. 가장 무거운 순간의 말이 합성을 기다리지 않게 하려는 것이다.
func (d *Deps) NewVoiceProvider(ctx context.Context) (voice.Provider, error) {
	cfg := d.Config.Voice
	switch cfg.Provider {
	case config.VoiceProviderOff:
		d.Logger.LogAttrs(ctx, slog.LevelInfo, "voice is off; conversations are chat only")
		return voice.Provider{}, nil

	case config.VoiceProviderFake:
		d.Logger.LogAttrs(ctx, slog.LevelWarn, "voice answers from a fixed script",
			slog.String("voice_provider", string(cfg.Provider)))
		return voice.Provider{
			Recognizer:  fake.NewRecognizer(fake.RecognizerOptions{Script: fake.DefaultScript}),
			Synthesizer: fake.NewSynthesizer(fake.SynthesizerOptions{}),
		}, nil

	case config.VoiceProviderSonioxElevenLabs:
		return d.newRealVoiceProvider(ctx)

	default:
		return voice.Provider{}, fmt.Errorf("app: unknown voice provider %q", cfg.Provider)
	}
}
