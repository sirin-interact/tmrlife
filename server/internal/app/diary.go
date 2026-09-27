package app

import (
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
)

// WorkerOption은 WorkerOptions가 모으는 작업을 바꾼다.
type WorkerOption func(*workerSettings)

type workerSettings struct {
	analysis ai.LLM
}

// WithAnalysisModel은 대화가 끝난 뒤의 일(일기 초안, 마음 신호 추출)에 쓸 언어 모델을 준다.
//
// 주지 않으면 그 두 작업은 등록되지 않는다. 그 상태로 작업자를 띄우면 서버가 넣은 작업은 "모르는 종류"로 실패하다 버려진다.
// 언어 모델을 아직 붙이지 않은 실행을 위해 남겨 둔 길이고, 그렇게 떴다는 것은 경고 로그로 남는다.
func WithAnalysisModel(llm ai.LLM) WorkerOption {
	return func(s *workerSettings) { s.analysis = llm }
}

// NewDiaryService는 일기 초안을 만드는 서비스를 만든다. analysis는 대화 뒤 분석에 쓰는 모델이다.
// 작업자와, 큐를 거치지 않고 초안을 만들어 보는 실행이 같은 길로 만들어 쓴다.
func (d *Deps) NewDiaryService(analysis ai.LLM) (*diary.Service, error) {
	service, err := diary.NewService(diary.Options{
		Store:    d.Store,
		Sealers:  d.Sealers,
		LLM:      analysis,
		Prompts:  d.Prompts,
		Clock:    d.Clock,
		Logger:   d.Logger,
		Thinking: string(d.Config.LLM.AnalysisThinking),
	})
	if err != nil {
		return nil, fmt.Errorf("create diary service: %w", err)
	}
	return service, nil
}

// NewDiaryEnqueuer는 대화를 끝내는 쪽이 초안 작업을 등록할 때 쓰는 것을 만든다. 서버가 부른다.
// 몇 번까지 시도할지는 작업을 넣을 때 정해지므로 설정을 여기서 넘긴다.
func (d *Deps) NewDiaryEnqueuer(inserter *queue.Client) (*diary.Enqueuer, error) {
	enqueuer, err := diary.NewEnqueuer(inserter, d.Config.DiaryJob.MaxAttempts())
	if err != nil {
		return nil, fmt.Errorf("create diary enqueuer: %w", err)
	}
	return enqueuer, nil
}

// newDiaryWorker는 분석 모델을 받았을 때만 일기 초안 작업자를 만든다. 받지 않았으면 registered가 false다.
func (d *Deps) newDiaryWorker(settings workerSettings) (worker *diary.Worker, registered bool, err error) {
	if settings.analysis == nil {
		return nil, false, nil
	}
	service, err := d.NewDiaryService(settings.analysis)
	if err != nil {
		return nil, false, err
	}
	worker, err = diary.NewWorker(service, d.Logger)
	if err != nil {
		return nil, false, fmt.Errorf("create diary worker: %w", err)
	}
	return worker, true, nil
}
