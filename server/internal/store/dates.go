package store

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// 기록 날짜(recorddate.Date)와 date 컬럼의 값(pgtype.Date)을 서로 바꾸는 자리다.
//
// 두 값은 모두 시각이 아니라 달력 날짜다. 그래서 연, 월, 일을 그대로 옮기기만 한다.
// 어느 순간이 어느 기록 날짜인지는 recorddate.Of가 이미 정했다. 여기서 새벽 경계 규칙을 다시 적용하면 하루가 밀린다.
//
// 값이 없을 수 있는 컬럼과 없으면 안 되는 컬럼의 함수를 나눠 두었다.
// 채우지 않은 날짜가 NOT NULL 컬럼에 0001-01-01로 조용히 저장되거나,
// 읽어 온 NULL이 "날짜 없음"으로 조용히 바뀌는 일을 쓰는 쪽이 고른 함수의 이름에서 막는다.

// PGDate는 기록 날짜를 NOT NULL인 date 컬럼에 넣을 값으로 바꾼다. 빈 값이면 오류다.
func PGDate(d recorddate.Date) (pgtype.Date, error) {
	if d.IsZero() {
		return pgtype.Date{}, fmt.Errorf("store: %w: zero value cannot be stored", recorddate.ErrInvalid)
	}
	return pgtype.Date{Time: d.UTCMidnight(), Valid: true}, nil
}

// NullablePGDate는 기록 날짜를 NULL일 수 있는 date 컬럼에 넣을 값으로 바꾼다. 빈 값은 NULL이 된다.
func NullablePGDate(d recorddate.Date) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.UTCMidnight(), Valid: true}
}

// RecordDate는 NOT NULL인 date 컬럼에서 읽은 값을 기록 날짜로 바꾼다.
// NULL, infinity, 기록 날짜가 담지 못하는 연도(기원전, 9999년 이후)는 오류다.
func RecordDate(v pgtype.Date) (recorddate.Date, error) {
	if !v.Valid {
		return recorddate.Date{}, fmt.Errorf("store: %w: unexpected null", recorddate.ErrInvalid)
	}
	return fromValidPGDate(v)
}

// NullableRecordDate는 NULL일 수 있는 date 컬럼에서 읽은 값을 기록 날짜로 바꾼다. NULL은 빈 값이 된다.
func NullableRecordDate(v pgtype.Date) (recorddate.Date, error) {
	if !v.Valid {
		return recorddate.Date{}, nil
	}
	return fromValidPGDate(v)
}

func fromValidPGDate(v pgtype.Date) (recorddate.Date, error) {
	if v.InfinityModifier != pgtype.Finite {
		return recorddate.Date{}, fmt.Errorf("store: %w: infinite date", recorddate.ErrInvalid)
	}
	// 드라이버는 date를 언제나 자정으로 돌려준다. 자정이 아니면 DB에서 온 값이 아니라 어떤 순간을 잘라 만든 값이다.
	// 그런 값을 받아 주면 새벽 경계 규칙을 건너뛴 날짜가 기록 날짜 행세를 한다.
	hour, minute, second := v.Time.Clock()
	if hour != 0 || minute != 0 || second != 0 || v.Time.Nanosecond() != 0 {
		return recorddate.Date{}, fmt.Errorf("store: %w: date value carries a time of day", recorddate.ErrInvalid)
	}
	// 드라이버가 date를 DB로 보낼 때도 값에 붙은 시간대의 연, 월, 일을 그대로 쓴다. 읽을 때도 같은 기준으로 맞춘다.
	d, err := recorddate.New(v.Time.Date())
	if err != nil {
		return recorddate.Date{}, fmt.Errorf("store: %w", err)
	}
	return d, nil
}
