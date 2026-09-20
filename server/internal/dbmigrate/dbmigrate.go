// Package dbmigrate는 DB 스키마를 올리고 내린다.
//
// 스키마는 두 갈래다. 앱의 테이블은 goose가, 작업 큐의 테이블은 River가 관리한다.
// 둘을 한 명령으로 묶어 두어야 "서버는 떴는데 큐 테이블이 없다"는 상태가 생기지 않는다.
package dbmigrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/sirin-interact/tmrlife/server/migrations"
)

// queueLockID는 작업 큐 마이그레이션이 잡는 자문 잠금의 번호다.
// 여러 파드가 동시에 뜨면서 저마다 마이그레이션을 돌려도 한 번에 하나만 들어가게 한다.
// 앱 스키마 쪽은 goose의 세션 잠금이 같은 일을 한다.
const queueLockID int64 = 0x6e61_6569_6c71 // "naeilq"

const (
	unlockTimeout = 5 * time.Second

	lockProbeSeconds  = 1
	lockProbeAttempts = 300
)

// Applied는 이번 실행에서 실제로 적용된 마이그레이션이다.
type Applied struct {
	App   []int64
	Queue []int
}

// Up은 앱 스키마를 끝까지 올린 다음 작업 큐 스키마를 끝까지 올린다.
func Up(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (Applied, error) {
	var applied Applied

	provider, closeDB, err := newProvider(pool, logger)
	if err != nil {
		return applied, err
	}
	defer closeDB()

	results, err := provider.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			applied.App = append(applied.App, r.Source.Version)
		}
	}
	if err != nil {
		return applied, fmt.Errorf("apply app migrations: %w", err)
	}

	queueVersions, err := migrateQueue(ctx, pool, logger)
	applied.Queue = queueVersions
	if err != nil {
		return applied, err
	}
	return applied, nil
}

// Down은 앱 스키마를 한 단계 내린다. 내릴 것이 없으면 0을 돌려준다.
// 작업 큐 스키마는 건드리지 않는다. 앱 마이그레이션 하나를 되돌리려다 쌓여 있던 작업이 함께 사라지면 안 된다.
func Down(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (int64, error) {
	provider, closeDB, err := newProvider(pool, logger)
	if err != nil {
		return 0, err
	}
	defer closeDB()

	result, err := provider.Down(ctx)
	if err != nil {
		if errors.Is(err, goose.ErrNoNextVersion) {
			return 0, nil
		}
		return 0, fmt.Errorf("roll back app migration: %w", err)
	}
	return result.Source.Version, nil
}

// AppStatus는 앱 마이그레이션 하나의 상태다.
type AppStatus struct {
	Version int64
	Name    string
	Applied bool
}

// Status는 두 스키마의 현재 상태다.
type Status struct {
	App []AppStatus
	// QueueApplied와 QueueLatest가 같으면 작업 큐 스키마가 최신이다.
	QueueApplied int
	QueueLatest  int
}

// Pending은 아직 적용되지 않은 마이그레이션이 있는지 알려준다.
func (s Status) Pending() bool {
	for _, a := range s.App {
		if !a.Applied {
			return true
		}
	}
	return s.QueueApplied < s.QueueLatest
}

// GetStatus는 상태를 읽는다. 스키마는 바꾸지 않는다.
// 다만 한 번도 마이그레이션한 적 없는 DB라면 goose가 적용 기록을 담는 테이블을 만든다.
func GetStatus(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (Status, error) {
	var status Status

	provider, closeDB, err := newProvider(pool, logger)
	if err != nil {
		return status, err
	}
	defer closeDB()

	appStatuses, err := provider.Status(ctx)
	if err != nil {
		return status, fmt.Errorf("read app migration status: %w", err)
	}
	for _, s := range appStatuses {
		status.App = append(status.App, AppStatus{
			Version: s.Source.Version,
			Name:    s.Source.Path,
			Applied: s.State == goose.StateApplied,
		})
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: logger})
	if err != nil {
		return status, fmt.Errorf("create queue migrator: %w", err)
	}
	for _, m := range migrator.AllVersions() {
		status.QueueLatest = max(status.QueueLatest, m.Version)
	}
	existing, err := migrator.ExistingVersions(ctx)
	if err != nil {
		return status, fmt.Errorf("read queue migration status: %w", err)
	}
	for _, m := range existing {
		status.QueueApplied = max(status.QueueApplied, m.Version)
	}
	return status, nil
}

func newProvider(pool *pgxpool.Pool, logger *slog.Logger) (*goose.Provider, func(), error) {
	// goose는 database/sql만 받는다. 같은 풀을 빌려 쓰면 접속 설정을 두 번 하지 않아도 된다.
	db := stdlib.OpenDBFromPool(pool)
	closeDB := func() { _ = db.Close() }

	// 잠금을 먼저 잡은 쪽이 끝나기를 1초 간격으로 최대 5분 기다린다.
	// 기본 간격(5초)은 나란히 뜨는 파드의 시작을 그만큼 늦춘다.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(lockProbeSeconds, lockProbeAttempts))
	if err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("create migration locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker),
		goose.WithSlog(logger),
		// 실행 파일에 담긴 SQL만 쓴다. 다른 패키지가 전역으로 등록한 Go 마이그레이션이 끼어들지 못하게 한다.
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, closeDB, nil
}

func migrateQueue(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (versions []int, err error) {
	// 자문 잠금은 세션에 묶인다. 풀에서 연결 하나를 빌려 끝날 때까지 쥐고 있어야 잠금이 유지된다.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection for queue migration lock: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", queueLockID); err != nil {
		return nil, fmt.Errorf("take queue migration lock: %w", err)
	}
	defer func() {
		// ctx가 이미 끝났더라도 잠금은 풀어야 한다. 풀지 못하면 연결을 버려 세션과 함께 잠금이 사라지게 한다.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancel()
		if _, unlockErr := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", queueLockID); unlockErr != nil {
			_ = conn.Conn().Close(unlockCtx)
			err = errors.Join(err, fmt.Errorf("release queue migration lock: %w", unlockErr))
		}
	}()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("create queue migrator: %w", err)
	}
	result, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return nil, fmt.Errorf("apply queue migrations: %w", err)
	}
	for _, v := range result.Versions {
		versions = append(versions, v.Version)
	}
	return versions, nil
}
