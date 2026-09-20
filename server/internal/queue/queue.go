// Package queue는 PostgreSQL 기반 작업 큐(River)의 클라이언트를 만든다.
//
// 서버는 작업을 넣기만 하고(NewInsertClient), 작업자는 꺼내서 실행한다(NewWorkerClient).
// 둘이 같은 DB를 쓰므로 서버는 대화 저장과 후속 작업 등록을 한 트랜잭션에 묶을 수 있다.
//
// 작업 인자에는 식별자만 담는다. 인자는 river_job 테이블에 평문 JSON으로 남고,
// 실패한 작업의 오류 문구도 같은 테이블에 쌓인다. 발화나 일기를 인자나 오류에 넣으면
// 암호화하지 않은 사용자의 글이 DB와 로그에 남게 된다. 작업은 ID를 받아 필요한 내용을 직접 읽는다.
package queue

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	// DefaultMaxWorkers는 기본 큐에서 동시에 도는 작업 수다.
	DefaultMaxWorkers = 10

	// DefaultSoftStopTimeout은 종료 신호 뒤에 돌던 작업을 기다려 주는 시간이다.
	// 이 시간이 지나면 작업의 컨텍스트가 취소되고, 끝내지 못한 작업은 큐가 다시 시도한다.
	// 쿠버네티스의 기본 종료 유예(30초) 안에 정리까지 마치도록 그보다 짧게 잡는다.
	DefaultSoftStopTimeout = 20 * time.Second
)

// Client는 pgx 트랜잭션에 묶을 수 있는 River 클라이언트다.
type Client = river.Client[pgx.Tx]

// WorkerOptions는 작업자 클라이언트의 설정이다.
type WorkerOptions struct {
	// Register는 작업 종류를 등록하는 자리다. 새 작업 패키지는 여기서 river.AddWorkerSafely를 부른다.
	// 하나도 등록하지 않으면 Start가 실패한다. 할 일을 모르는 작업자를 띄우는 것은 설정이 빠진 것이다.
	Register func(workers *river.Workers) error
	// PeriodicJobs는 정해진 간격으로 넣을 작업이다.
	PeriodicJobs []*river.PeriodicJob
	// MaxWorkers가 0이면 DefaultMaxWorkers를 쓴다.
	MaxWorkers int
	// SoftStopTimeout이 0이면 DefaultSoftStopTimeout을 쓴다.
	SoftStopTimeout time.Duration
}

// NewInsertClient는 작업을 넣기만 하는 클라이언트를 만든다. Start를 부르지 않는다.
func NewInsertClient(pool *pgxpool.Pool, logger *slog.Logger) (*Client, error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("create queue insert client: %w", err)
	}
	return client, nil
}

// NewWorkerClient는 작업을 꺼내 실행하는 클라이언트를 만든다.
// Start에 넘긴 컨텍스트가 취소되면 새 작업을 받지 않고, 돌던 작업을 SoftStopTimeout만큼 기다린 뒤 내려간다.
func NewWorkerClient(pool *pgxpool.Pool, logger *slog.Logger, opts WorkerOptions) (*Client, error) {
	if logger == nil {
		return nil, errors.New("create queue worker client: logger is required")
	}

	workers := river.NewWorkers()
	if opts.Register != nil {
		if err := opts.Register(workers); err != nil {
			return nil, fmt.Errorf("register workers: %w", err)
		}
	}

	maxWorkers := opts.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = DefaultMaxWorkers
	}
	softStop := opts.SoftStopTimeout
	if softStop <= 0 {
		softStop = DefaultSoftStopTimeout
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:          logger,
		Workers:         workers,
		PeriodicJobs:    opts.PeriodicJobs,
		SoftStopTimeout: softStop,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create queue worker client: %w", err)
	}
	return client, nil
}
