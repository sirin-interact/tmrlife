package pgdb_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/pgdb"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	t.Run("주소에 한도가 없으면 기본 한도를 걸고, 세션 시간대를 UTC로 고정하고, 프로세스 이름을 남긴다", func(t *testing.T) {
		t.Parallel()
		base := testdb.New(t)

		pool, err := pgdb.Open(t.Context(), base.Config().ConnString(), "naeil-test")
		require.NoError(t, err)
		defer pool.Close()

		cfg := pool.Config()
		assert.Equal(t, int32(10), cfg.MaxConns)
		assert.Equal(t, 30*time.Minute, cfg.MaxConnLifetime)
		assert.Equal(t, 5*time.Minute, cfg.MaxConnLifetimeJitter)
		assert.Equal(t, 10*time.Minute, cfg.MaxConnIdleTime)

		var tz, app string
		require.NoError(t, pool.QueryRow(t.Context(), "SHOW timezone").Scan(&tz))
		require.NoError(t, pool.QueryRow(t.Context(), "SHOW application_name").Scan(&app))
		assert.Equal(t, "UTC", tz)
		assert.Equal(t, "naeil-test", app)
	})

	t.Run("주소에 적힌 한도가 기본값보다 우선한다", func(t *testing.T) {
		t.Parallel()
		base := testdb.New(t)
		url := base.Config().ConnString() +
			"&pool_max_conns=3&pool_max_conn_lifetime=2h&pool_max_conn_lifetime_jitter=1m&pool_max_conn_idle_time=45m"

		pool, err := pgdb.Open(t.Context(), url, "naeil-test")
		require.NoError(t, err)
		defer pool.Close()

		cfg := pool.Config()
		assert.Equal(t, int32(3), cfg.MaxConns)
		assert.Equal(t, 2*time.Hour, cfg.MaxConnLifetime)
		assert.Equal(t, time.Minute, cfg.MaxConnLifetimeJitter)
		assert.Equal(t, 45*time.Minute, cfg.MaxConnIdleTime)
	})

	t.Run("프로세스 이름은 주소에 적힌 것보다 실행 파일이 준 것이 우선한다", func(t *testing.T) {
		t.Parallel()
		base := testdb.New(t)

		pool, err := pgdb.Open(t.Context(), base.Config().ConnString()+"&application_name=from-url", "naeil-worker")
		require.NoError(t, err)
		defer pool.Close()

		var app string
		require.NoError(t, pool.QueryRow(t.Context(), "SHOW application_name").Scan(&app))
		assert.Equal(t, "naeil-worker", app)
	})

	t.Run("프로세스 이름 없이는 열지 않는다", func(t *testing.T) {
		t.Parallel()
		pool, err := pgdb.Open(t.Context(), "postgres://naeil:naeil@127.0.0.1:1/naeil?sslmode=disable", "")
		require.Error(t, err)
		assert.Nil(t, pool)
	})

	t.Run("DB가 내려가 있어도 풀을 만드는 데서는 실패하지 않는다", func(t *testing.T) {
		t.Parallel()
		pool, err := pgdb.Open(t.Context(), "postgres://naeil:naeil@127.0.0.1:1/naeil?sslmode=disable", "naeil-test")
		require.NoError(t, err)
		defer pool.Close()
		assert.Error(t, pool.Ping(t.Context()))
	})

	t.Run("접속 주소가 틀렸을 때 오류에 주소의 어느 조각도 나오지 않는다", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			url  string
			// leaks는 오류에 나오면 안 되는 조각이다. 비밀번호만이 아니라 그 일부, 호스트, 사용자, DB 이름도 본다.
			leaks []string
		}{
			{"포트가 숫자가 아니다", "postgres://app:s3cr3t-pass@db.internal:notaport/app",
				[]string{"s3cr3t-pass", "db.internal", "notaport"}},
			{"모르는 sslmode다", "postgres://app:s3cr3t-pass@db.internal:5432/app?sslmode=bogus",
				[]string{"s3cr3t-pass", "db.internal", "bogus"}},
			{"풀 설정 값이 틀렸다", "postgres://app:s3cr3t-pass@db.internal:5432/app?pool_max_conns=many",
				[]string{"s3cr3t-pass", "db.internal"}},
			// 드라이버는 오류에 주소를 통째로 옮기면서 비밀번호를 가리는데, 아래 두 꼴에서는 가리다 만다.
			// 공백이 든 비밀번호는 뒤쪽 조각이, 따옴표가 든 비밀번호는 따옴표 뒤의 조각이 그대로 남는다.
			{"키=값 꼴에서 비밀번호에 공백이 있다", "host=db.internal user=app password=S3cret TailPW dbname=app",
				[]string{"S3cret", "TailPW", "db.internal"}},
			{"키=값 꼴에서 비밀번호의 따옴표가 어긋났다", "host=db.internal user=app password='S3cr'etTailPW' dbname=app",
				[]string{"S3cr", "etTailPW", "db.internal"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				pool, err := pgdb.Open(t.Context(), tt.url, "naeil-test")
				require.Error(t, err)
				assert.Nil(t, pool)
				for _, leak := range tt.leaks {
					assert.NotContains(t, err.Error(), leak)
				}
				assert.Contains(t, err.Error(), "DATABASE_URL", "무엇을 고쳐야 하는지는 알려줘야 한다")
			})
		}
	})
}

func TestOpen_TimestampsComeBackInUTC(t *testing.T) {
	t.Parallel()
	base := testdb.New(t)

	pool, err := pgdb.Open(t.Context(), base.Config().ConnString(), "naeil-test")
	require.NoError(t, err)
	defer pool.Close()

	// 서울의 저녁 8시다. 같은 순간을 UTC로 적으면 오전 11시다.
	seoul := time.FixedZone("KST", 9*60*60)
	evening := time.Date(2026, 9, 20, 20, 0, 0, 0, seoul)
	wantUTC := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		// 드라이버는 인자가 있으면 이진 형식으로, 없으면 글자 형식으로 값을 주고받는다. 두 길을 모두 본다.
		mode pgx.QueryExecMode
	}{
		{"이진 형식으로 읽을 때", pgx.QueryExecModeCacheStatement},
		{"글자 형식으로 읽을 때", pgx.QueryExecModeSimpleProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got time.Time
			require.NoError(t, pool.QueryRow(t.Context(), "SELECT $1::timestamptz", tt.mode, evening).Scan(&got))
			assert.Equal(t, time.UTC, got.Location(), "읽은 시각에는 UTC가 붙어 있어야 한다")
			assert.True(t, wantUTC.Equal(got), "want %s, got %s", wantUTC, got)
			assert.Equal(t, 11, got.Hour())
		})
	}

	t.Run("배열로 읽은 시각도 UTC다", func(t *testing.T) {
		var got []time.Time
		require.NoError(t, pool.QueryRow(t.Context(), "SELECT ARRAY[$1::timestamptz]", evening).Scan(&got))
		require.Len(t, got, 1)
		assert.Equal(t, time.UTC, got[0].Location())
		assert.True(t, wantUTC.Equal(got[0]))
	})

	t.Run("새로 맺은 연결에도 똑같이 적용된다", func(t *testing.T) {
		// 풀의 연결을 모두 버리면 다음 쿼리는 새 연결에서 돈다.
		pool.Reset()
		var got time.Time
		require.NoError(t, pool.QueryRow(t.Context(), "SELECT $1::timestamptz", evening).Scan(&got))
		assert.Equal(t, time.UTC, got.Location())
	})
}
