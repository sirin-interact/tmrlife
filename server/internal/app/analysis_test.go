package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 신호 추출 작업이 실제로 등록되어 있는지 본다.
// 등록되지 않으면 서버가 넣은 작업은 "모르는 종류"로 실패하다 버려지고, 화면은 영원히 비어 있다.
// 그 어긋남은 서버도 작업자도 오류를 내지 않아서 눈으로는 잡히지 않는다.
func TestWorkerOptionsRegistersTheSignalExtractWorker(t *testing.T) {
	t.Parallel()
	deps := newDeps(t, testConfig(t, map[string]string{"ANALYSIS_JOB_RETRIES": "2"}), NameWorker)
	model := fake.New("fake-analysis")

	options, err := deps.WorkerOptions(t.Context(), WithAnalysisModel(model))
	require.NoError(t, err)
	options.SoftStopTimeout = 5 * time.Second
	client, err := queue.NewWorkerClient(deps.Pool, slog.New(slog.DiscardHandler), options)
	require.NoError(t, err)
	completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
	defer cancelSub()

	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	require.NoError(t, client.Start(ctx))

	inserter, err := queue.NewInsertClient(deps.Pool, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	enqueuer, err := deps.NewAnalysisEnqueuer(inserter)
	require.NoError(t, err)

	// 그사이 지워진 대화의 작업이다. 작업자는 모델을 부르지 않고 그만둔다. 등록과 배선만 보려는 것이다.
	args := analysis.ExtractArgs{
		UserID:         uuid.Must(uuid.NewV7()),
		ConversationID: uuid.Must(uuid.NewV7()),
	}
	require.NoError(t, deps.Store.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		err := enqueuer.EnqueueTx(ctx, tx, q, args)
		// 대화가 없으므로 상태를 적을 곳이 없다. 작업만 넣어 등록을 확인한다.
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = inserter.InsertTx(ctx, tx, args, &river.InsertOpts{MaxAttempts: 3})
		return err
	}))

	deadline := time.After(30 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-completed:
			if ev.Job.Kind == args.Kind() {
				done = true
			}
		case <-deadline:
			t.Fatal("신호 추출 작업이 끝나지 않았다")
		}
	}
	assert.Zero(t, model.Calls(), "지워진 대화에는 모델을 부르지 않는다")

	stop()
	select {
	case <-client.Stopped():
	case <-time.After(15 * time.Second):
		t.Fatal("작업자가 내려가지 않았다")
	}
}

func TestNewAnalysisServiceNeedsAModel(t *testing.T) {
	t.Parallel()
	deps := newDeps(t, testConfig(t, nil), NameWorker)
	_, err := deps.NewAnalysisService(nil)
	require.Error(t, err)
	_, err = deps.NewAnalysisEnqueuer(nil)
	require.Error(t, err)
}
