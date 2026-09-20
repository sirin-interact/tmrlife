package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// mappedDB는 sqlc가 만든 쿼리와 실제 연결 사이에 끼어, 모든 쿼리의 오류를 mapError로 바꾼다.
// 쿼리마다 손으로 감싸는 함수를 두면 새 쿼리를 더할 때 빠뜨리기 쉽다. 한 곳에서 바꾸면 빠뜨릴 수가 없다.
type mappedDB struct {
	inner db.DBTX
}

func (m mappedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := m.inner.Exec(ctx, sql, args...)
	return tag, mapError(err)
}

func (m mappedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := m.inner.Query(ctx, sql, args...)
	if err != nil {
		return rows, mapError(err)
	}
	return mappedRows{Rows: rows}, nil
}

func (m mappedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return mappedRow{inner: m.inner.QueryRow(ctx, sql, args...)}
}

// pgx는 한 행짜리 쿼리의 오류를 Scan에서 돌려준다. 행이 없다는 것도 여기서 드러난다.
type mappedRow struct {
	inner pgx.Row
}

func (r mappedRow) Scan(dest ...any) error {
	return mapError(r.inner.Scan(dest...))
}

// 여러 행을 읽는 도중의 오류는 다 읽은 뒤 Err에서 나온다.
type mappedRows struct {
	pgx.Rows
}

func (r mappedRows) Err() error {
	return mapError(r.Rows.Err())
}
