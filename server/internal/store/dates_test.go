package store_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func recordDate(t *testing.T, year int, month time.Month, day int) recorddate.Date {
	t.Helper()
	d, err := recorddate.New(year, month, day)
	require.NoError(t, err)
	return d
}

func TestPGDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		year  int
		month time.Month
		day   int
	}{
		{"평범한 날짜는 같은 연월일의 UTC 자정이 된다", 2026, time.September, 20},
		{"윤일도 그대로 옮긴다", 2028, time.February, 29},
		{"해의 마지막 날이 다음 해로 넘어가지 않는다", 2026, time.December, 31},
		{"지원 범위의 첫날을 담는다", 1, time.January, 1},
		{"지원 범위의 마지막 날을 담는다", 9999, time.December, 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := recordDate(t, tt.year, tt.month, tt.day)
			want := pgtype.Date{Time: time.Date(tt.year, tt.month, tt.day, 0, 0, 0, 0, time.UTC), Valid: true}

			got, err := store.PGDate(d)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, want, store.NullablePGDate(d))
		})
	}

	t.Run("빈 값은 NOT NULL 컬럼에 넣을 수 없다", func(t *testing.T) {
		t.Parallel()
		got, err := store.PGDate(recorddate.Date{})
		require.ErrorIs(t, err, recorddate.ErrInvalid)
		// 오류를 무시하고 써도 0001-01-01이 아니라 NULL로 나가서 NOT NULL 제약에 걸린다.
		assert.False(t, got.Valid)
	})

	t.Run("빈 값은 NULL일 수 있는 컬럼에서 NULL이 된다", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, pgtype.Date{}, store.NullablePGDate(recorddate.Date{}))
	})
}

func TestRecordDate(t *testing.T) {
	t.Parallel()

	seoul, err := time.LoadLocation("Asia/Seoul")
	require.NoError(t, err)

	t.Run("DB가 돌려주는 UTC 자정을 같은 연월일로 읽는다", func(t *testing.T) {
		t.Parallel()
		for _, want := range []recorddate.Date{
			recordDate(t, 2026, time.September, 20),
			recordDate(t, 2028, time.February, 29),
			recordDate(t, 1, time.January, 1),
			recordDate(t, 9999, time.December, 31),
		} {
			v := date(want.Year(), want.Month(), want.Day())

			got, err := store.RecordDate(v)
			require.NoError(t, err)
			assert.Equal(t, want, got)

			got, err = store.NullableRecordDate(v)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})

	t.Run("새벽 경계 규칙을 다시 적용하지 않는다", func(t *testing.T) {
		t.Parallel()
		// UTC 자정은 어느 시간대에서 봐도 새벽 4시 전일 수 있다. 시각으로 보고 기록 날짜를 계산하면 하루가 밀린다.
		v := date(2026, time.September, 20)
		require.Equal(t, recordDate(t, 2026, time.September, 19), recorddate.Of(v.Time, time.UTC))

		got, err := store.RecordDate(v)
		require.NoError(t, err)
		assert.Equal(t, recordDate(t, 2026, time.September, 20), got)
	})

	t.Run("기록 날짜를 계산한 결과가 그대로 오간다", func(t *testing.T) {
		t.Parallel()
		// 서울의 9월 21일 01:30은 아직 9월 20일의 기록이다. UTC로는 9월 20일 16:30이다.
		lateNight := time.Date(2026, time.September, 21, 1, 30, 0, 0, seoul)
		d := recorddate.Of(lateNight, seoul)
		require.Equal(t, recordDate(t, 2026, time.September, 20), d)

		v, err := store.PGDate(d)
		require.NoError(t, err)
		back, err := store.RecordDate(v)
		require.NoError(t, err)
		assert.Equal(t, d, back)
	})

	t.Run("여러 해에 걸쳐 하루씩 옮겨도 갔다가 돌아온 값이 같다", func(t *testing.T) {
		t.Parallel()
		d := recordDate(t, 2023, time.December, 25)
		for range 1500 {
			v, err := store.PGDate(d)
			require.NoError(t, err)
			back, err := store.RecordDate(v)
			require.NoError(t, err)
			require.Equal(t, d, back)
			d = d.AddDays(1)
		}
	})

	invalid := []struct {
		name string
		v    pgtype.Date
	}{
		{"infinity는 기록 날짜가 될 수 없다", pgtype.Date{InfinityModifier: pgtype.Infinity, Valid: true}},
		{"-infinity는 기록 날짜가 될 수 없다", pgtype.Date{InfinityModifier: pgtype.NegativeInfinity, Valid: true}},
		{"기원전 날짜는 담지 못한다", pgtype.Date{Time: time.Date(-43, time.March, 15, 0, 0, 0, 0, time.UTC), Valid: true}},
		{"0년은 담지 못한다", pgtype.Date{Time: time.Date(0, time.December, 31, 0, 0, 0, 0, time.UTC), Valid: true}},
		{"10000년은 담지 못한다", pgtype.Date{Time: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), Valid: true}},
		{"시각이 붙은 값은 받지 않는다", pgtype.Date{Time: time.Date(2026, time.September, 20, 16, 30, 0, 0, time.UTC), Valid: true}},
		{"나노초만 붙은 값도 받지 않는다", pgtype.Date{Time: time.Date(2026, time.September, 20, 0, 0, 0, 1, time.UTC), Valid: true}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := store.RecordDate(tt.v)
			require.ErrorIs(t, err, recorddate.ErrInvalid)
			assert.True(t, got.IsZero())

			got, err = store.NullableRecordDate(tt.v)
			require.ErrorIs(t, err, recorddate.ErrInvalid)
			assert.True(t, got.IsZero())
		})
	}

	t.Run("NULL은 NOT NULL 컬럼에서 오류이고, NULL일 수 있는 컬럼에서 빈 값이다", func(t *testing.T) {
		t.Parallel()
		got, err := store.RecordDate(pgtype.Date{})
		require.ErrorIs(t, err, recorddate.ErrInvalid)
		assert.True(t, got.IsZero())

		got, err = store.NullableRecordDate(pgtype.Date{})
		require.NoError(t, err)
		assert.True(t, got.IsZero())
	})

	t.Run("자정이면 값에 붙은 시간대의 연월일을 읽는다", func(t *testing.T) {
		t.Parallel()
		// 드라이버가 DB로 보낼 때와 같은 기준이다. 서울 자정은 UTC로는 전날 15:00이지만 9월 20일로 저장된다.
		v := pgtype.Date{Time: time.Date(2026, time.September, 20, 0, 0, 0, 0, seoul), Valid: true}
		got, err := store.RecordDate(v)
		require.NoError(t, err)
		assert.Equal(t, recordDate(t, 2026, time.September, 20), got)
	})
}

// 변환 함수의 약속을 실제 DB와 드라이버로 확인한다. DB가 글자로 적어 준 날짜와 견주므로 Go 쪽 변환끼리만 맞는 경우를 걸러낸다.
func TestRecordDateRoundTripThroughDatabase(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	ctx := t.Context()
	user := createUser(t, st, "mina@example.com")

	t.Run("하루의 기록 날짜가 DB를 거쳐 그대로 돌아온다", func(t *testing.T) {
		for _, d := range []recorddate.Date{
			recordDate(t, 2026, time.September, 20),
			recordDate(t, 2028, time.February, 29),
			recordDate(t, 1, time.January, 1),
			recordDate(t, 9999, time.December, 31),
		} {
			param, err := store.PGDate(d)
			require.NoError(t, err)
			dayID, err := st.Queries().UpsertDay(ctx, db.UpsertDayParams{
				ID: newID(t), UserID: user.ID, RecordDate: param, Now: baseTime,
			})
			require.NoError(t, err)

			var stored pgtype.Date
			var storedText string
			require.NoError(t, pool.QueryRow(ctx,
				`SELECT record_date, to_char(record_date, 'YYYY-MM-DD') FROM days WHERE id = $1`, dayID,
			).Scan(&stored, &storedText))

			assert.Equal(t, d.String(), storedText)
			got, err := store.RecordDate(stored)
			require.NoError(t, err)
			assert.Equal(t, d, got)
		}
	})

	t.Run("NULL일 수 있는 날짜는 NULL과 빈 값이 서로 오간다", func(t *testing.T) {
		roundTrip := func(d recorddate.Date) (recorddate.Date, bool) {
			var stored pgtype.Date
			var isNull bool
			require.NoError(t, pool.QueryRow(ctx,
				`SELECT v, v IS NULL FROM (SELECT $1::date AS v) AS s`, store.NullablePGDate(d),
			).Scan(&stored, &isNull))
			got, err := store.NullableRecordDate(stored)
			require.NoError(t, err)
			return got, isNull
		}

		got, isNull := roundTrip(recorddate.Date{})
		assert.True(t, isNull)
		assert.True(t, got.IsZero())

		due := recordDate(t, 2026, time.October, 3)
		got, isNull = roundTrip(due)
		assert.False(t, isNull)
		assert.Equal(t, due, got)
	})

	t.Run("DB에만 있을 수 있는 날짜는 오류로 읽힌다", func(t *testing.T) {
		for _, literal := range []string{"infinity", "-infinity", "0044-03-15 BC", "10000-01-01"} {
			var stored pgtype.Date
			require.NoError(t, pool.QueryRow(ctx, `SELECT $1::text::date`, literal).Scan(&stored), literal)
			require.True(t, stored.Valid, literal)

			_, err := store.RecordDate(stored)
			require.ErrorIs(t, err, recorddate.ErrInvalid, literal)
		}
	})
}
