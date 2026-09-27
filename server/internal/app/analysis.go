package app

import (
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
)

// NewAnalysisService는 끝난 대화에서 마음 신호를 뽑는 서비스를 만든다. analysis는 대화 뒤 분석에 쓰는 모델이다.
// 작업자와, 큐를 거치지 않고 뽑아 보는 실행이 같은 길로 만들어 쓴다.
//
// 출력 한도와 기다리는 시간은 설정에서 온다. 일기 초안보다 넉넉한 까닭은 여덟 항목의 JSON을 한 번에 받기 때문이다.
func (d *Deps) NewAnalysisService(model ai.LLM) (*analysis.Service, error) {
	service, err := analysis.NewService(analysis.Options{
		Store:           d.Store,
		Sealers:         d.Sealers,
		LLM:             model,
		Prompts:         d.Prompts,
		Clock:           d.Clock,
		Logger:          d.Logger,
		Thinking:        string(d.Config.LLM.AnalysisThinking),
		MaxOutputTokens: d.Config.LLM.AnalysisMaxOutputTokens,
		CallTimeout:     d.Config.LLM.AnalysisBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("create signal analysis service: %w", err)
	}
	return service, nil
}

// NewAnalysisEnqueuer는 대화를 끝내는 쪽이 신호 추출 작업을 등록할 때 쓰는 것을 만든다. 서버가 부른다.
// 몇 번까지 시도할지는 작업을 넣을 때 정해지므로 설정을 여기서 넘긴다.
func (d *Deps) NewAnalysisEnqueuer(inserter *queue.Client) (*analysis.Enqueuer, error) {
	enqueuer, err := analysis.NewEnqueuer(inserter, d.Config.AnalysisJob.MaxAttempts())
	if err != nil {
		return nil, fmt.Errorf("create signal analysis enqueuer: %w", err)
	}
	return enqueuer, nil
}

// newAnalysisWorker는 분석 모델을 받았을 때만 신호 추출 작업자를 만든다. 받지 않았으면 registered가 false다.
func (d *Deps) newAnalysisWorker(settings workerSettings) (worker *analysis.Worker, registered bool, err error) {
	if settings.analysis == nil {
		return nil, false, nil
	}
	service, err := d.NewAnalysisService(settings.analysis)
	if err != nil {
		return nil, false, err
	}
	worker, err = analysis.NewWorker(service, d.Logger)
	if err != nil {
		return nil, false, fmt.Errorf("create signal analysis worker: %w", err)
	}
	return worker, true, nil
}
