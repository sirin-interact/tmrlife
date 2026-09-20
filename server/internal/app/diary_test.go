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
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestWorkerOptionsWithAnalysisModel(t *testing.T) {
	t.Parallel()

	t.Run("분석 모델을 주면 서버가 넣은 일기 초안 작업을 작업자가 집어 끝낸다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, map[string]string{"DIARY_JOB_RETRIES": "2"}), NameWorker)
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
		enqueuer, err := deps.NewDiaryEnqueuer(inserter)
		require.NoError(t, err)

		// 그사이 하루가 지워진 작업이다. 작업자는 모델을 부르지 않고 그만둔다. 등록과 배선만 보려는 것이다.
		args := diary.DraftArgs{
			UserID:         uuid.Must(uuid.NewV7()),
			DayID:          uuid.Must(uuid.NewV7()),
			ConversationID: uuid.Must(uuid.NewV7()),
		}
		require.NoError(t, deps.Store.InTxRaw(ctx, func(tx pgx.Tx, _ *db.Queries) error {
			return enqueuer.EnqueueTx(ctx, tx, args)
		}))

		deadline := time.After(30 * time.Second)
		for done := false; !done; {
			select {
			case ev := <-completed:
				if ev.Job.Kind == args.Kind() {
					assert.Equal(t, 3, ev.Job.MaxAttempts, "다시 시도 횟수에 처음 한 번을 더한 값으로 등록된다")
					done = true
				}
			case <-deadline:
				t.Fatal("일기 초안 작업이 끝나지 않았다")
			}
		}
		assert.Zero(t, model.Calls())

		stop()
		select {
		case <-client.Stopped():
		case <-time.After(15 * time.Second):
			t.Fatal("작업자가 내려가지 않았다")
		}
	})

	t.Run("모델 없이는 일기 서비스를 만들 수 없다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, nil), NameWorker)
		_, err := deps.NewDiaryService(nil)
		require.Error(t, err)
		_, err = deps.NewDiaryEnqueuer(nil)
		require.Error(t, err)
	})
}
