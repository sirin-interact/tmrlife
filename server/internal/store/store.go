package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const rollbackTimeout = 5 * time.Second

// Store는 접속 풀에 묶인 쿼리와 트랜잭션 도우미를 묶는다.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// New는 이미 열려 있는 풀로 Store를 만든다. 풀을 닫는 일은 풀을 연 쪽이 한다.
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:    pool,
		queries: db.New(mappedDB{inner: pool}),
	}
}

// Queries는 트랜잭션 밖에서 한 문장씩 실행하는 쿼리다.
func (s *Store) Queries() *db.Queries {
	return s.queries
}

// InTx는 fn을 한 트랜잭션 안에서 돌린다. fn이 오류를 돌려주거나 패닉하면 되돌리고, 아니면 커밋한다.
// fn이 돌려준 오류는 감싸지 않고 그대로 돌려준다. 부르는 쪽이 자기 오류 값을 그대로 비교할 수 있어야 한다.
func (s *Store) InTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.InTxRaw(ctx, func(_ pgx.Tx, q *db.Queries) error { return fn(q) })
}

// InTxRaw는 InTx와 같되 트랜잭션 자체도 넘겨준다.
// 작업 큐에 작업을 넣는 일처럼, 다른 라이브러리의 쓰기를 같은 트랜잭션에 묶어
// 함께 성공하거나 함께 실패하게 해야 할 때만 쓴다. fn 안에서 직접 커밋하거나 되돌리지 않는다.
func (s *Store) InTxRaw(ctx context.Context, fn func(tx pgx.Tx, q *db.Queries) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// 커밋을 시도한 뒤에는 성공이든 실패든 트랜잭션이 끝났고 연결은 풀에 돌아가 있다. 그 뒤로는 tx를 건드리지 않는다.
	finished := false
	defer func() {
		if !finished {
			// 요청이 취소되면 드라이버가 연결을 끊는다. 그때는 서버가 알아서 되돌리므로 되돌리기가 실패해도 알릴 것이 없다.
			// 그래도 Rollback은 불러야 한다. 연결을 풀에 돌려주는 일을 Rollback이 하기 때문이다.
			connClosed := tx.Conn().IsClosed()
			// ctx가 이미 취소됐더라도 되돌리기는 끝내야 연결이 깨끗한 상태로 풀에 돌아간다.
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			defer cancel()
			rollbackErr := tx.Rollback(rollbackCtx)
			if rollbackErr != nil && !connClosed && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("roll back transaction: %w", rollbackErr))
			}
		}
	}()

	if err := fn(tx, db.New(mappedDB{inner: tx})); err != nil {
		return err
	}
	finished = true
	// fn이 실패한 문장의 오류를 삼키고 nil을 돌려줬다면 트랜잭션은 이미 깨져 있고, 커밋이 그 사실을 오류로 알려준다.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", mapError(err))
	}
	return nil
}

// NewID는 새 행의 ID(UUIDv7)를 만든다. 시간순으로 늘어나는 값이라 기본 키 인덱스가 끝에서만 자란다.
//
// ID에 든 시각은 실제 시계에서 온다. 고르게 정렬되라고 있는 값이지 기록 시각이 아니므로 읽어 쓰지 않는다.
// 기록 시각은 주입받은 시계에서 온 컬럼 값이다.
func NewID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate id: %w", err)
	}
	return id, nil
}
