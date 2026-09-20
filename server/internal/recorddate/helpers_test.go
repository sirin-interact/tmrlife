package recorddate

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err, "시간대 자료(tzdata)가 있어야 한다")
	return loc
}

func mustDate(t *testing.T, year int, month time.Month, day int) Date {
	t.Helper()
	d, err := New(year, month, day)
	require.NoError(t, err)
	return d
}

func mustParse(t *testing.T, s string) Date {
	t.Helper()
	d, err := Parse(s)
	require.NoError(t, err)
	return d
}

func mustInstant(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, rfc3339)
	require.NoError(t, err)
	return at
}

func assertInstant(t *testing.T, want, got time.Time, msgAndArgs ...any) {
	t.Helper()
	if !want.Equal(got) {
		assert.Failf(t, "순간이 다르다", "기대 %s, 실제 %s %v",
			want.UTC().Format(time.RFC3339Nano), got.UTC().Format(time.RFC3339Nano), msgAndArgs)
	}
}

type zoneChange struct {
	at            time.Time
	offsetSeconds int
}

// syntheticLocation은 시험에 필요한 전환만 담은 시간대를 만든다.
// 04:00을 넘겼다가 되돌아가는 전환은 실제 시간대 자료에 없어서 직접 만들어야 한다.
// 시간대 파일 형식(TZif 1판)을 손으로 적는다: 머리말, 전환 시각, 전환별 구간 번호, 구간 정보, 약어.
func syntheticLocation(t *testing.T, name string, initialOffsetSeconds int, changes ...zoneChange) *time.Location {
	t.Helper()

	const abbreviations = "TST\x00"
	data := []byte("TZif")
	data = append(data, make([]byte, 16)...) // 판 번호(0은 1판)와 예약 영역
	for _, count := range []int{0, 0, 0, len(changes), len(changes) + 1, len(abbreviations)} {
		data = binary.BigEndian.AppendUint32(data, uint32(count))
	}
	for _, change := range changes {
		data = binary.BigEndian.AppendUint32(data, uint32(int32(change.at.Unix())))
	}
	for i := range changes {
		// 0번 구간은 첫 전환 이전에 쓰이도록 어떤 전환도 가리키지 않게 둔다.
		data = append(data, byte(i+1))
	}
	appendZone := func(offsetSeconds int) {
		data = binary.BigEndian.AppendUint32(data, uint32(int32(offsetSeconds)))
		data = append(data, 0, 0) // 일광 절약 시간 아님, 약어는 0번
	}
	appendZone(initialOffsetSeconds)
	for _, change := range changes {
		appendZone(change.offsetSeconds)
	}
	data = append(data, abbreviations...)

	loc, err := time.LoadLocationFromTZData(name, data)
	require.NoError(t, err)
	return loc
}

const secondsPerHour = 60 * 60

// gapOverBoundary는 2026-05-10 03:30에 시계를 04:30으로 돌린다. 04:00이라는 시각이 그날 없다.
func gapOverBoundary(t *testing.T) *time.Location {
	t.Helper()
	return syntheticLocation(t, "Test/GapOverBoundary", 9*secondsPerHour,
		zoneChange{at: mustInstant(t, "2026-05-09T18:30:00Z"), offsetSeconds: 10 * secondsPerHour})
}

// backAcrossBoundary는 2026-05-10 04:30에 시계를 03:30으로 되돌린다. 04:00을 넘겼다가 그 앞으로 돌아간다.
func backAcrossBoundary(t *testing.T) *time.Location {
	t.Helper()
	return syntheticLocation(t, "Test/BackAcrossBoundary", 10*secondsPerHour,
		zoneChange{at: mustInstant(t, "2026-05-09T18:30:00Z"), offsetSeconds: 9 * secondsPerHour})
}
