package dbmigrate_test

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/dbmigrate"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestUp(t *testing.T) {
	t.Run("빈 DB에서 앱 스키마와 작업 큐 스키마를 모두 올린다", func(t *testing.T) {
		pool := testdb.NewEmpty(t)
		ctx := t.Context()

		before, err := dbmigrate.GetStatus(ctx, pool, quiet())
		require.NoError(t, err)
		assert.True(t, before.Pending())
		assert.Zero(t, before.QueueApplied)
		require.NotEmpty(t, before.App)
		assert.Equal(t, int64(1), before.App[0].Version)
		assert.False(t, before.App[0].Applied)

		applied, err := dbmigrate.Up(ctx, pool, quiet())
		require.NoError(t, err)
		assert.Contains(t, applied.App, int64(1))
		assert.NotEmpty(t, applied.Queue)

		after, err := dbmigrate.GetStatus(ctx, pool, quiet())
		require.NoError(t, err)
		assert.False(t, after.Pending())
		assert.Equal(t, after.QueueLatest, after.QueueApplied)

		var hasCitext, hasRiverJob bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')`).Scan(&hasCitext))
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT to_regclass('river_job') IS NOT NULL`).Scan(&hasRiverJob))
		assert.True(t, hasCitext, "citext 확장이 있어야 한다")
		assert.True(t, hasRiverJob, "작업 큐 테이블이 있어야 한다")
	})

	t.Run("다시 돌려도 아무것도 적용하지 않고 성공한다", func(t *testing.T) {
		pool := testdb.NewEmpty(t)
		_, err := dbmigrate.Up(t.Context(), pool, quiet())
		require.NoError(t, err)

		again, err := dbmigrate.Up(t.Context(), pool, quiet())
		require.NoError(t, err)
		assert.Empty(t, again.App)
		assert.Empty(t, again.Queue)
	})

	t.Run("여러 곳에서 동시에 돌려도 모두 성공하고 한 번씩만 적용된다", func(t *testing.T) {
		// 파드 여러 개가 동시에 뜨면서 저마다 마이그레이션을 돌리는 상황이다.
		pool := testdb.NewEmpty(t)

		const runners = 4
		results := make([]dbmigrate.Applied, runners)
		errs := make([]error, runners)
		var wg sync.WaitGroup
		for i := range runners {
			wg.Go(func() {
				results[i], errs[i] = dbmigrate.Up(t.Context(), pool, quiet())
			})
		}
		wg.Wait()

		appliedFirst := 0
		for i := range runners {
			require.NoError(t, errs[i])
			for _, v := range results[i].App {
				if v == 1 {
					appliedFirst++
				}
			}
		}
		assert.Equal(t, 1, appliedFirst, "첫 마이그레이션은 한 곳에서만 적용돼야 한다")

		status, err := dbmigrate.GetStatus(t.Context(), pool, quiet())
		require.NoError(t, err)
		assert.False(t, status.Pending())
	})
}

func TestDown(t *testing.T) {
	t.Run("한 단계 내리면 마지막 앱 마이그레이션만 되돌리고 작업 큐는 그대로 둔다", func(t *testing.T) {
		pool := testdb.New(t)
		ctx := t.Context()

		before, err := dbmigrate.GetStatus(ctx, pool, quiet())
		require.NoError(t, err)
		require.False(t, before.Pending())
		latest := before.App[len(before.App)-1].Version

		rolledBack, err := dbmigrate.Down(ctx, pool, quiet())
		require.NoError(t, err)
		assert.Equal(t, latest, rolledBack)

		after, err := dbmigrate.GetStatus(ctx, pool, quiet())
		require.NoError(t, err)
		assert.False(t, after.App[len(after.App)-1].Applied)
		assert.Equal(t, before.QueueApplied, after.QueueApplied, "작업 큐 스키마는 건드리지 않는다")
	})

	t.Run("끝까지 내렸다가 다시 올릴 수 있다", func(t *testing.T) {
		// Down 구문이 빠졌거나 틀린 마이그레이션을 잡아낸다.
		pool := testdb.New(t)
		ctx := t.Context()

		for {
			v, err := dbmigrate.Down(ctx, pool, quiet())
			require.NoError(t, err)
			if v == 0 {
				break
			}
		}
		_, err := dbmigrate.Up(ctx, pool, quiet())
		require.NoError(t, err)

		status, err := dbmigrate.GetStatus(ctx, pool, quiet())
		require.NoError(t, err)
		assert.False(t, status.Pending())
	})

	t.Run("내릴 것이 없으면 0을 돌려주고 오류를 내지 않는다", func(t *testing.T) {
		pool := testdb.NewEmpty(t)
		v, err := dbmigrate.Down(t.Context(), pool, quiet())
		require.NoError(t, err)
		assert.Zero(t, v)
	})
}
