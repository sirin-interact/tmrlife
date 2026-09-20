package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/riverqueue/river"

	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const (
	// DefaultSweepInterval은 버려진 대화를 찾아 닫는 간격이다.
	// 끊긴 연결을 기다리는 시간보다 촘촘해야 그 시간이 지난 대화가 오래 열려 있지 않다.
	DefaultSweepInterval = 5 * time.Minute
	// DefaultSweepMaxRows는 한 번에 닫는 대화의 수다. 한 번에 다 닫지 못하면 다음 차례가 이어서 닫는다.
	DefaultSweepMaxRows = 200
)

// SweeperOptions는 쓸어 담는 쪽을 만드는 데 필요한 것이다.
type SweeperOptions struct {
	Store *store.Store
	// Diary는 비워 둘 수 있다. 비우면 초안 작업을 넣지 않는다.
	Diary  DiaryEnqueuer
	Clock  clock.Clock
	Logger *slog.Logger
	// IdleAfter는 마지막 발화(없으면 시작한 시각)로부터 이만큼 지난 열린 대화를 닫는다.
	// 설정의 DISCONNECT_END_AFTER를 넘긴다.
	IdleAfter time.Duration
	// MaxRows가 0이면 DefaultSweepMaxRows다.
	MaxRows int
}

// Sweeper는 연결이 끊긴 채 열려 있는 대화를 닫는다.
//
// 끊긴 연결을 기다리는 타이머는 프로세스의 메모리에 있어서 프로세스가 다시 뜨면 사라진다.
// 그렇게 남은 대화를 닫아 주는 쪽이 없으면 사용자마다 하나뿐인 열린 자리가 막혀 다음 대화를 시작할 수 없고,
// 그날의 일기 초안도 만들어지지 않는다.
type Sweeper struct {
	closer

	logger    *slog.Logger
	idleAfter time.Duration
	maxRows   int
}

// NewSweeper는 쓸어 담는 쪽을 만든다.
func NewSweeper(opts SweeperOptions) (*Sweeper, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("engine: store is required")
	case opts.Clock == nil:
		return nil, errors.New("engine: clock is required")
	case opts.Logger == nil:
		return nil, errors.New("engine: logger is required")
	case opts.IdleAfter <= 0:
		return nil, errors.New("engine: idle after must be greater than zero")
	case opts.MaxRows < 0:
		return nil, errors.New("engine: max rows must not be negative")
	}
	s := &Sweeper{
		closer:    closer{store: opts.Store, diary: opts.Diary, clock: opts.Clock},
		logger:    opts.Logger,
		idleAfter: opts.IdleAfter,
		maxRows:   opts.MaxRows,
	}
	if s.maxRows == 0 {
		s.maxRows = DefaultSweepMaxRows
	}
	return s, nil
}

// SweepResult는 한 번 쓸어 담은 결과다.
type SweepResult struct {
	// Scanned는 조건에 걸린 대화의 수다.
	Scanned int
	// Ended는 이번에 닫은 대화의 수다.
	Ended int
	// Resumed는 닫으려다 만 대화의 수다. 그 사이에 사용자가 다시 붙어 말을 이었거나 다른 쪽이 먼저 닫았다.
	Resumed int
	// DiaryJobs는 등록한 일기 초안 작업의 수다.
	DiaryJobs int
}

// LogValue는 결과를 로그 속성 묶음으로 바꾼다.
func (r SweepResult) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("scanned", r.Scanned),
		slog.Int("ended", r.Ended),
		slog.Int("resumed", r.Resumed),
		slog.Int("diary_jobs", r.DiaryJobs),
	)
}

// Sweep은 한동안 말이 없는 열린 대화를 찾아 닫는다. 사유는 무응답이다.
//
// 닫을 때 그 시각 뒤로 발화가 없는지 다시 확인한다. 목록을 읽고 닫는 사이에 사용자가 돌아와
// 이야기를 이어갔을 수 있기 때문이다. 그런 대화는 건드리지 않고 다음 차례로 넘긴다.
func (s *Sweeper) Sweep(ctx context.Context) (SweepResult, error) {
	idleBefore := s.clock.Now().Add(-s.idleAfter)
	rows, err := s.store.Queries().ListStaleActiveConversations(ctx, db.ListStaleActiveConversationsParams{
		IdleBefore: idleBefore,
		//nolint:gosec // G115: 위에서 int32의 상한으로 자른다.
		MaxRows: int32(min(s.maxRows, math.MaxInt32)),
	})
	if err != nil {
		return SweepResult{}, fmt.Errorf("engine: list stale conversations: %w", err)
	}

	result := SweepResult{Scanned: len(rows)}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("engine: %w", err)
		}
		ended, err := s.end(ctx, endTarget{
			userID: row.UserID, conversationID: row.ID, dayID: row.DayID,
		}, store.EndReasonIdle, &idleBefore)
		switch {
		case errors.Is(err, errNotActive):
			result.Resumed++
			continue
		case err != nil:
			// 하나가 실패했다고 나머지를 버리지 않는다. 다음 차례가 같은 대화를 다시 본다.
			s.logger.LogAttrs(ctx, slog.LevelError, "stale conversation cannot be ended",
				slog.String("user_id", row.UserID.String()),
				slog.String("conversation_id", row.ID.String()),
				slog.String("failure", failureName(err)),
			)
			continue
		}
		result.Ended++
		if ended.DiaryExpected {
			result.DiaryJobs++
		}
	}
	if result.Ended > 0 || result.Resumed > 0 {
		s.logger.LogAttrs(ctx, slog.LevelInfo, "stale conversations swept", slog.Any("result", result))
	}
	return result, nil
}

// SweepArgs는 버려진 대화를 닫는 작업의 인자다. 담을 것이 없다.
type SweepArgs struct{}

// Kind는 작업 종류의 이름이다. DB에 저장되므로 한번 정하면 바꾸지 않는다.
func (SweepArgs) Kind() string { return "conversation_sweep" }

// SweepWorker는 주기 작업으로 Sweep을 돌린다.
type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	sweeper *Sweeper
	logger  *slog.Logger
}

// NewSweepWorker는 작업자를 만든다.
func NewSweepWorker(sweeper *Sweeper, logger *slog.Logger) (*SweepWorker, error) {
	if sweeper == nil {
		return nil, errors.New("engine: sweep worker needs a sweeper")
	}
	if logger == nil {
		return nil, errors.New("engine: sweep worker needs a logger")
	}
	return &SweepWorker{sweeper: sweeper, logger: logger}, nil
}

// Work는 한 번 쓸어 담는다. 실패하면 오류를 돌려주고, 다음 차례가 같은 일을 한다.
func (w *SweepWorker) Work(ctx context.Context, job *river.Job[SweepArgs]) error {
	result, err := w.sweeper.Sweep(ctx)
	if err != nil {
		return err
	}
	w.logger.LogAttrs(ctx, slog.LevelDebug, "conversation sweep finished",
		slog.Int64("job_id", job.ID), slog.Any("result", result))
	return nil
}

// SweepPeriodicJob은 작업자가 뜰 때 한 번, 그 뒤로 interval마다 쓸어 담는 작업을 넣는다.
// interval이 0보다 크지 않으면 DefaultSweepInterval이다.
func SweepPeriodicJob(interval time.Duration) *river.PeriodicJob {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return river.NewPeriodicJob(
		river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) {
			// 실패해도 다시 시도하지 않는다. 다음 차례가 같은 대화를 다시 본다.
			return SweepArgs{}, &river.InsertOpts{MaxAttempts: 1}
		},
		&river.PeriodicJobOpts{ID: "conversation_sweep", RunOnStart: true},
	)
}
