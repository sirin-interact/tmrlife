package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/gemini"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/config"
)

// Models는 일마다 쓰는 언어 모델이다.
//
// 대화와 위기 판별은 서로 다른 모델, 다른 지시문, 다른 호출이어야 한다. 대화를 만드는 모델 하나에 안전을 맡기지 않으려는 구조라서,
// 같은 값을 두 자리에 넣는 길을 만들지 않는다.
type Models struct {
	// Conversation은 실시간 대화에 쓴다. 늦은 응답에 대비한 예비 모델이 함께 묶여 있을 수 있다.
	Conversation ai.LLM
	// Gate는 위기 판별에 쓴다. 사용자가 답을 받기까지의 시간을 정하므로 가장 빠른 모델을 둔다.
	Gate ai.LLM
	// Analysis는 대화가 끝난 뒤의 일(일기 초안)에 쓴다. 작업자가 쓴다.
	Analysis ai.LLM
	// Provider는 이 모델들이 어디서 왔는지다. 로그에 남긴다.
	Provider config.AIProvider
}

// NewModels는 설정이 고른 공급자로 모델을 만든다.
//
// AI_PROVIDER가 scripted면 밖으로 나가지 않고 정해 둔 답을 돌려주는 모델 하나가 세 자리를 모두 맡는다.
// 그 모델은 지시문 ID를 보고 규칙을 고르므로 자리마다 다른 답을 낸다. 운영에서는 받지 않는다.
func (d *Deps) NewModels(ctx context.Context) (*Models, error) {
	cfg := d.Config
	switch cfg.LLM.Provider {
	case config.AIProviderScripted:
		if cfg.Env.IsProd() {
			// 설정이 이미 막지만, 설정을 거치지 않고 Deps를 만든 길이 생겨도 여기서 막힌다.
			// 정해 둔 답만 하는 서버가 운영에 뜨면 위기 판별도 정해 둔 답이 된다.
			return nil, errors.New("app: the scripted provider must not be used when APP_ENV=prod")
		}
		llm, err := scripted.New(cfg.LLM.Provider, scripted.Options{})
		if err != nil {
			return nil, fmt.Errorf("create scripted models: %w", err)
		}
		d.Logger.LogAttrs(ctx, slog.LevelWarn, "language models answer from a fixed script",
			slog.String("ai_provider", string(cfg.LLM.Provider)))
		return &Models{Conversation: llm, Gate: llm, Analysis: llm, Provider: cfg.LLM.Provider}, nil

	case config.AIProviderGemini:
		return d.newGeminiModels(ctx)

	default:
		return nil, fmt.Errorf("app: unknown ai provider %q", cfg.LLM.Provider)
	}
}

// newGeminiModels는 연결 하나를 만들고 일마다 모델을 꺼낸다.
// 연결을 함께 써야 요청마다 새로 맺는 시간(0.1초 남짓)을 아낀다.
func (d *Deps) newGeminiModels(ctx context.Context) (*Models, error) {
	cfg := d.Config
	client, err := gemini.New(ctx, gemini.Config{
		APIKey: cfg.Providers.GeminiAPIKey,
		Clock:  d.Clock,
		Logger: d.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to the language model provider: %w", err)
	}

	primary, err := client.LLM(gemini.Model{
		Name:     cfg.LLM.ConversationModel,
		Thinking: string(cfg.LLM.ConversationThinking),
	})
	if err != nil {
		return nil, fmt.Errorf("create the conversation model: %w", err)
	}
	// 예비 모델의 생각하기 수준은 고정한다. 늦은 답을 대신하려고 띄우는 모델이 주 모델의 수준을 물려받아
	// 몇 초씩 생각하면 띄우는 뜻이 없다. 설정에 이 값만을 위한 변수를 두지 않고 가장 낮은 수준으로 못 박는다.
	fallback, err := client.LLM(gemini.Model{
		Name:        cfg.LLM.ConversationFallbackModel,
		Thinking:    string(config.ThinkingMinimal),
		PinThinking: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create the fallback conversation model: %w", err)
	}
	conversation, err := ai.NewHedged(primary, fallback, cfg.LLM.FallbackAfter)
	if err != nil {
		return nil, fmt.Errorf("create the hedged conversation model: %w", err)
	}

	gate, err := client.LLM(gemini.Model{
		Name:     cfg.LLM.GateModel,
		Thinking: string(cfg.LLM.GateThinking),
	})
	if err != nil {
		return nil, fmt.Errorf("create the gate model: %w", err)
	}
	analysis, err := client.LLM(gemini.Model{
		Name:     cfg.LLM.AnalysisModel,
		Thinking: string(cfg.LLM.AnalysisThinking),
	})
	if err != nil {
		return nil, fmt.Errorf("create the analysis model: %w", err)
	}

	d.Logger.LogAttrs(ctx, slog.LevelInfo, "language models ready",
		slog.String("ai_provider", string(cfg.LLM.Provider)),
		slog.String("conversation", primary.Model()),
		slog.String("conversation_fallback", fallback.Model()),
		slog.String("gate", gate.Model()),
		slog.String("analysis", analysis.Model()),
	)
	return &Models{Conversation: conversation, Gate: gate, Analysis: analysis, Provider: cfg.LLM.Provider}, nil
}
