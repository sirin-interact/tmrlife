package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

// SessionCleanupInterval은 끝난 세션을 지우는 간격이다.
// 끝난 세션은 지우기 전에도 통하지 않으므로 서두를 이유가 없다. 테이블이 끝없이 자라지 않을 만큼만 돌면 된다.
const SessionCleanupInterval = time.Hour

// SessionCleanupArgs는 끝난 세션을 지우는 작업의 인자다. 담을 것이 없다.
type SessionCleanupArgs struct{}

// Kind는 작업 종류의 이름이다. DB에 저장되므로 한번 정하면 바꾸지 않는다.
func (SessionCleanupArgs) Kind() string { return "auth_session_cleanup" }

// SessionCleanupWorker는 끝난 세션의 행을 지운다.
type SessionCleanupWorker struct {
	river.WorkerDefaults[SessionCleanupArgs]
	sessions *Sessions
	logger   *slog.Logger
}

func NewSessionCleanupWorker(sessions *Sessions, logger *slog.Logger) (*SessionCleanupWorker, error) {
	if sessions == nil {
		return nil, errors.New("auth: session cleanup needs a session manager")
	}
	if logger == nil {
		return nil, errors.New("auth: session cleanup needs a logger")
	}
	return &SessionCleanupWorker{sessions: sessions, logger: logger}, nil
}

func (w *SessionCleanupWorker) Work(ctx context.Context, job *river.Job[SessionCleanupArgs]) error {
	deleted, err := w.sessions.DeleteExpired(ctx)
	if err != nil {
		return err
	}
	// 작업자가 뜰 때마다 이 줄이 한 번 찍힌다. 큐에 넣고, 꺼내고, 실행하고, 끝내는 길이 끝까지 통한다는 표시이기도 하다.
	w.logger.LogAttrs(ctx, slog.LevelInfo, "expired sessions deleted",
		slog.Int64("job_id", job.ID),
		slog.Int64("sessions", deleted),
	)
	return nil
}

// SessionCleanupPeriodicJob은 작업자가 뜰 때 한 번, 그 뒤로 SessionCleanupInterval마다 지우는 작업을 넣는다.
// 작업자가 여럿이어도 주기 작업은 그중 대표 하나만 넣는다.
func SessionCleanupPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(SessionCleanupInterval),
		func() (river.JobArgs, *river.InsertOpts) {
			// 실패해도 다시 시도하지 않는다. 한 시간 뒤의 다음 작업이 같은 일을 한다.
			return SessionCleanupArgs{}, &river.InsertOpts{MaxAttempts: 1}
		},
		&river.PeriodicJobOpts{ID: "auth_session_cleanup", RunOnStart: true},
	)
}
