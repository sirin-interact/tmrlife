package auth

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
)

func TestSessions_DeleteExpired(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	// 첫날에 가입하고 그 뒤로 쓰지 않은 세션과, 10일 뒤에 로그인한 세션이다.
	f.signup(t, "mina@example.com")
	f.clock.Set(baseTime.Add(10 * day))
	fresh := f.login(t, "mina@example.com")

	t.Run("아직 끝난 세션이 없으면 아무것도 지우지 않는다", func(t *testing.T) {
		deleted, err := f.sessions.DeleteExpired(ctx)
		require.NoError(t, err)
		assert.Zero(t, deleted)
		assert.Equal(t, 2, f.count(t, "sessions"))
	})

	t.Run("주입받은 시계로 보아 끝난 세션만 지운다", func(t *testing.T) {
		f.clock.Set(baseTime.Add(14 * day))
		deleted, err := f.sessions.DeleteExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)

		_, err = f.service.Authenticate(ctx, fresh.Session.Token.Reveal())
		require.NoError(t, err, "아직 통하는 세션은 남아 있어야 한다")
		assert.Equal(t, 1, f.count(t, "sessions"))
	})
}

func TestSessionCleanupJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// 끝난 세션 둘과 아직 통하는 세션 하나를 만든다.
	f.signup(t, "mina@example.com")
	f.login(t, "mina@example.com")
	f.clock.Set(baseTime.Add(20 * day))
	fresh := f.login(t, "mina@example.com")
	require.Equal(t, 3, f.count(t, "sessions"))

	logger := logging.New(f.logs, slog.LevelInfo)
	worker, err := NewSessionCleanupWorker(f.sessions, logger)
	require.NoError(t, err)

	// 작업자가 실제로 뜨는 것과 같은 길로 돌린다. 주기 작업이 뜨자마자 한 번 들어가고, 큐가 꺼내 실행한다.
	client, err := queue.NewWorkerClient(f.pool, logger, queue.WorkerOptions{
		SoftStopTimeout: 5 * time.Second,
		Register: func(workers *river.Workers) error {
			return river.AddWorkerSafely(workers, worker)
		},
		PeriodicJobs: []*river.PeriodicJob{SessionCleanupPeriodicJob()},
	})
	require.NoError(t, err)

	completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
	defer cancelSub()

	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	require.NoError(t, client.Start(ctx))

	select {
	case ev := <-completed:
		assert.Equal(t, SessionCleanupArgs{}.Kind(), ev.Job.Kind)
		assert.Equal(t, 1, ev.Job.MaxAttempts, "실패해도 다시 시도하지 않는다. 다음 주기의 작업이 같은 일을 한다")
	case <-time.After(30 * time.Second):
		t.Fatal("끝난 세션을 지우는 작업이 돌지 않았다")
	}

	assert.Equal(t, 1, f.count(t, "sessions"), "끝난 세션 둘만 지워져야 한다")
	_, err = f.service.Authenticate(t.Context(), fresh.Session.Token.Reveal())
	require.NoError(t, err)
	assert.Contains(t, f.logs.String(), `"msg":"expired sessions deleted"`)
	assert.Contains(t, f.logs.String(), `"sessions":2`)

	stop()
	select {
	case <-client.Stopped():
	case <-time.After(15 * time.Second):
		t.Fatal("작업자가 내려가지 않았다")
	}
}

func TestSessionCleanup_Settings(t *testing.T) {
	t.Parallel()

	t.Run("한 시간마다 돈다", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, time.Hour, SessionCleanupInterval)
	})

	t.Run("작업 종류의 이름은 DB에 남으므로 바꾸지 않는다", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "auth_session_cleanup", SessionCleanupArgs{}.Kind())
	})

	t.Run("세션 관리자와 로거 없이는 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		_, err := NewSessionCleanupWorker(nil, slog.New(slog.DiscardHandler))
		require.Error(t, err)
		_, err = NewSessionCleanupWorker(f.sessions, nil)
		require.Error(t, err)
	})
}
