package app

import (
	"context"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/api"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
)

// NewConversationEngine은 대화 엔진을 만든다. 채널(WebSocket)과 음성 파이프라인이 같은 것을 받아 쓴다.
//
// conversation은 대화 모델이다. 늦은 응답에 대비한 예비 모델을 쓰려면 그것을 감싼 LLM(ai.NewHedged)을 넘긴다.
// gateModel은 위기 판별 모델이다. 대화를 만드는 모델과 다른 모델, 다른 지시문, 다른 호출이어야 한다.
// diaries는 대화를 끝낼 때 일기 초안 작업을 넣는 쪽이고, signals는 마음 신호 추출 작업을 넣는 쪽이다.
// 둘은 각각 nil일 수 있고, nil이면 그 작업을 넣지 않는다.
func (d *Deps) NewConversationEngine(
	conversation, gateModel ai.LLM, diaries engine.DiaryEnqueuer, signals engine.AnalysisEnqueuer,
) (*engine.Engine, error) {
	detector, err := d.newGate(gateModel)
	if err != nil {
		return nil, err
	}
	generator, err := d.newReply(conversation)
	if err != nil {
		return nil, err
	}
	catalogue, err := phrases.Load()
	if err != nil {
		return nil, fmt.Errorf("load phrases: %w", err)
	}

	e, err := engine.New(engine.Options{
		Store:    d.Store,
		Sealers:  d.Sealers,
		Gate:     detector,
		Reply:    generator,
		Phrases:  catalogue,
		Diary:    diaries,
		Analysis: signals,
		Clock:    d.Clock,
		Logger:   d.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create conversation engine: %w", err)
	}
	return e, nil
}

// newGate는 위기 관문의 두 겹을 만든다. 판별 모델을 기다리는 시간은 설정에서 온다.
func (d *Deps) newGate(model ai.LLM) (*gate.Detector, error) {
	prompt, err := d.Prompts.Get(classifier.Task)
	if err != nil {
		return nil, fmt.Errorf("load gate prompt: %w", err)
	}
	cls, err := classifier.New(model, prompt, d.Clock, classifier.Config{
		Timeout:  d.Config.LLM.GateTimeout,
		Thinking: string(d.Config.LLM.GateThinking),
	})
	if err != nil {
		return nil, fmt.Errorf("create gate classifier: %w", err)
	}
	lexicon, err := rules.LoadEmbedded()
	if err != nil {
		return nil, fmt.Errorf("load gate lexicon: %w", err)
	}
	detector, err := gate.New(lexicon, cls)
	if err != nil {
		return nil, fmt.Errorf("create gate: %w", err)
	}
	return detector, nil
}

// newReply는 답을 만들어 출력 검사를 거치는 쪽을 만든다.
func (d *Deps) newReply(model ai.LLM) (*reply.Generator, error) {
	catalogue, err := phrases.Load()
	if err != nil {
		return nil, fmt.Errorf("load phrases: %w", err)
	}
	set, err := reply.PromptsFrom(d.Prompts)
	if err != nil {
		return nil, fmt.Errorf("load conversation prompts: %w", err)
	}
	generator, err := reply.New(model, set, catalogue, reply.Options{
		Thinking: string(d.Config.LLM.ConversationThinking),
		// 기다리는 시간의 상한을 여기서 건다. 걸지 않으면 한 턴이 부른 쪽의 컨텍스트만 따라 몇 분씩 이어져서,
		// 사용자는 답도 못 받고 끝내기도 하지 못한 채 앉아 있게 된다. 시간을 다 쓰면 미리 써 둔 말이 나간다.
		Budget: d.Config.LLM.ReplyBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("create reply generator: %w", err)
	}
	return generator, nil
}

// newConversationChannel은 대화 소켓이 쓰는 것을 모두 만든다. 서버만 부른다.
//
// 언어 모델, 엔진, 일기 초안 작업을 넣는 쪽이 여기서 한 번에 만들어진다. 모델 이름이나 지시문이 틀렸으면
// 첫 대화가 아니라 서버가 뜰 때 실패한다. 무응답을 기다리는 시간과 메시지의 한도는 설정에서 온다.
func (d *Deps) newConversationChannel(ctx context.Context) (*api.Conversation, error) {
	models, err := d.NewModels(ctx)
	if err != nil {
		return nil, err
	}
	// 서버는 작업을 넣기만 한다. 대화를 끝내는 트랜잭션 안에서 초안 작업과 신호 추출 작업이 함께 등록된다.
	inserter, err := queue.NewInsertClient(d.Pool, d.Logger)
	if err != nil {
		return nil, err
	}
	diaries, err := d.NewDiaryEnqueuer(inserter)
	if err != nil {
		return nil, err
	}
	signals, err := d.NewAnalysisEnqueuer(inserter)
	if err != nil {
		return nil, err
	}
	conversationEngine, err := d.NewConversationEngine(models.Conversation, models.Gate, diaries, signals)
	if err != nil {
		return nil, err
	}

	cfg := d.Config
	channel, err := api.NewConversation(api.ConversationOptions{
		Engine:          conversationEngine,
		Store:           d.Store,
		Clock:           d.Clock,
		Logger:          d.Logger,
		IdleCheckAfter:  cfg.Conversation.IdleCheckAfter,
		IdleEndAfter:    cfg.Conversation.IdleEndAfter,
		MaxMessageBytes: cfg.WebSocket.MaxMessageBytes,
		MessageRate:     api.RateLimit(cfg.WebSocket.MessageRate),
		MaxKeys:         cfg.RateLimits.MaxKeys,
	})
	if err != nil {
		return nil, fmt.Errorf("create conversation channel: %w", err)
	}
	return channel, nil
}

// NewConversationSweeper는 연결이 끊긴 채 열려 있는 대화를 닫는 쪽을 만든다.
// 기다리는 시간은 설정(DISCONNECT_END_AFTER)에서 온다.
//
// 두 작업을 넣는 쪽을 여기에도 넘긴다. 사용자가 끝내기를 누르지 않고 창을 닫은 대화가 이 길로 닫히는데,
// 그때 작업을 넣지 않으면 그 대화만 일기도 신호도 없이 남는다.
func (d *Deps) NewConversationSweeper(
	diaries engine.DiaryEnqueuer, signals engine.AnalysisEnqueuer,
) (*engine.Sweeper, error) {
	sweeper, err := engine.NewSweeper(engine.SweeperOptions{
		Store:     d.Store,
		Diary:     diaries,
		Analysis:  signals,
		Clock:     d.Clock,
		Logger:    d.Logger,
		IdleAfter: d.Config.Conversation.DisconnectEndAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("create conversation sweeper: %w", err)
	}
	return sweeper, nil
}

// newSweepWorker는 버려진 대화를 닫는 주기 작업을 만든다.
//
// 이 작업은 언어 모델을 부르지 않는다. 대화를 끝내고 일기 초안 작업을 넣는 일뿐이라 언제나 등록한다.
// 등록하지 않으면 프로세스가 죽으면서 남은 열린 대화가 영영 닫히지 않고, 사용자마다 하나뿐인 열린 자리가 막힌다.
// 작업을 넣는 클라이언트를 여기서 따로 만드는 이유는 작업자 클라이언트가 아직 만들어지기 전이기 때문이다.
func (d *Deps) newSweepWorker() (*engine.SweepWorker, error) {
	inserter, err := queue.NewInsertClient(d.Pool, d.Logger)
	if err != nil {
		return nil, err
	}
	diaries, err := d.NewDiaryEnqueuer(inserter)
	if err != nil {
		return nil, err
	}
	signals, err := d.NewAnalysisEnqueuer(inserter)
	if err != nil {
		return nil, err
	}
	sweeper, err := d.NewConversationSweeper(diaries, signals)
	if err != nil {
		return nil, err
	}
	worker, err := engine.NewSweepWorker(sweeper, d.Logger)
	if err != nil {
		return nil, fmt.Errorf("create conversation sweep worker: %w", err)
	}
	return worker, nil
}
