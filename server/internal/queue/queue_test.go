package queue_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type tickArgs struct{}

func (tickArgs) Kind() string { return "test_tick" }

type tickWorker struct {
	river.WorkerDefaults[tickArgs]
	logger *slog.Logger
}

func (w *tickWorker) Work(ctx context.Context, job *river.Job[tickArgs]) error {
	w.logger.LogAttrs(ctx, slog.LevelInfo, "tick", slog.Int64("job_id", job.ID))
	return nil
}

func TestWorkerClient(t *testing.T) {
	t.Run("주기 작업이 뜨자마자 한 번 끝까지 돌고, 작업자는 깨끗하게 내려간다", func(t *testing.T) {
		pool := testdb.New(t)
		logs := &syncBuffer{}
		logger := slog.New(slog.NewJSONHandler(logs, nil))

		client, err := queue.NewWorkerClient(pool, logger, queue.WorkerOptions{
			SoftStopTimeout: 5 * time.Second,
			Register: func(workers *river.Workers) error {
				return river.AddWorkerSafely(workers, &tickWorker{logger: logger})
			},
			PeriodicJobs: []*river.PeriodicJob{
				river.NewPeriodicJob(
					river.PeriodicInterval(time.Hour),
					func() (river.JobArgs, *river.InsertOpts) { return tickArgs{}, nil },
					&river.PeriodicJobOpts{RunOnStart: true},
				),
			},
		})
		require.NoError(t, err)

		completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
		defer cancelSub()

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		require.NoError(t, client.Start(ctx))

		select {
		case ev := <-completed:
			assert.Equal(t, tickArgs{}.Kind(), ev.Job.Kind)
		case <-time.After(30 * time.Second):
			t.Fatal("주기 작업이 완료되지 않았다")
		}
		assert.Contains(t, logs.String(), `"msg":"tick"`)

		stop()
		select {
		case <-client.Stopped():
		case <-time.After(15 * time.Second):
			t.Fatal("작업자가 내려가지 않았다")
		}
	})

	t.Run("작업 종류를 하나도 등록하지 않은 작업자는 뜨지 않는다", func(t *testing.T) {
		pool := testdb.New(t)
		client, err := queue.NewWorkerClient(pool, slog.New(slog.DiscardHandler), queue.WorkerOptions{})
		require.NoError(t, err)

		err = client.Start(t.Context())
		require.Error(t, err, "할 일을 모르는 작업자가 조용히 떠 있으면 안 된다")
	})

	t.Run("작업 종류를 등록하다 실패하면 만들지 않는다", func(t *testing.T) {
		pool := testdb.New(t)
		logger := slog.New(slog.DiscardHandler)
		_, err := queue.NewWorkerClient(pool, logger, queue.WorkerOptions{
			Register: func(workers *river.Workers) error {
				if err := river.AddWorkerSafely(workers, &tickWorker{logger: logger}); err != nil {
					return err
				}
				// 같은 종류를 두 번 등록한다.
				return river.AddWorkerSafely(workers, &tickWorker{logger: logger})
			},
		})
		require.Error(t, err)
	})

	t.Run("로거 없이는 만들 수 없다", func(t *testing.T) {
		_, err := queue.NewWorkerClient(nil, nil, queue.WorkerOptions{})
		require.Error(t, err)
	})
}

type noteArgs struct {
	ConversationID string `json:"conversation_id"`
}

func (noteArgs) Kind() string { return "test_note" }

type noteWorker struct {
	river.WorkerDefaults[noteArgs]
	got chan string
}

func (w *noteWorker) Work(_ context.Context, job *river.Job[noteArgs]) error {
	w.got <- job.Args.ConversationID
	return nil
}

func TestInsertClient(t *testing.T) {
	t.Run("서버가 트랜잭션 안에서 넣은 작업을 작업자가 받아 실행한다", func(t *testing.T) {
		pool := testdb.New(t)
		logger := slog.New(slog.DiscardHandler)

		got := make(chan string, 1)
		worker, err := queue.NewWorkerClient(pool, logger, queue.WorkerOptions{
			SoftStopTimeout: 5 * time.Second,
			Register: func(workers *river.Workers) error {
				return river.AddWorkerSafely(workers, &noteWorker{got: got})
			},
		})
		require.NoError(t, err)

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		require.NoError(t, worker.Start(ctx))

		inserter, err := queue.NewInsertClient(pool, logger)
		require.NoError(t, err)

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		_, err = inserter.InsertTx(ctx, tx, noteArgs{ConversationID: "c-1"}, nil)
		require.NoError(t, err)

		select {
		case <-got:
			t.Fatal("커밋하기 전에는 작업이 보이면 안 된다")
		case <-time.After(300 * time.Millisecond):
		}
		require.NoError(t, tx.Commit(ctx))

		select {
		case id := <-got:
			assert.Equal(t, "c-1", id)
		case <-time.After(30 * time.Second):
			t.Fatal("넣은 작업이 실행되지 않았다")
		}

		stop()
		<-worker.Stopped()
	})
}
