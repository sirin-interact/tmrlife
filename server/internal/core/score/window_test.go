package score

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func TestNewWindow(t *testing.T) {
	tests := []struct {
		name     string
		asOf     string
		length   int
		wantFrom string
	}{
		{"14일 창은 기준일과 그 앞의 13일이다", "2026-10-20", 14, "2026-10-07"},
		{"하루짜리 창은 기준일 하루다", "2026-10-20", 1, "2026-10-20"},
		{"달을 넘어간다", "2026-10-05", 14, "2026-09-22"},
		{"해를 넘어간다", "2026-01-05", 14, "2025-12-23"},
		{"윤년의 2월 29일을 하루로 센다", "2028-03-05", 14, "2028-02-21"},
		{"평년에는 2월 29일이 없다", "2026-03-05", 14, "2026-02-20"},
		{"다룰 수 있는 가장 앞의 날짜에서 시작할 수 있다", "0001-01-14", 14, "0001-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asOf := mustDate(t, tt.asOf)

			got, err := NewWindow(asOf, tt.length)
			require.NoError(t, err)

			assert.Equal(t, mustDate(t, tt.wantFrom), got.From)
			assert.Equal(t, asOf, got.To)
			assert.Equal(t, tt.length, got.Length())
		})
	}

	failures := []struct {
		name   string
		asOf   recorddate.Date
		length int
	}{
		{"기준일이 비어 있으면 만들 수 없다", recorddate.Date{}, 14},
		{"길이가 0이면 만들 수 없다", mustDate(t, "2026-10-20"), 0},
		{"길이가 음수면 만들 수 없다", mustDate(t, "2026-10-20"), -3},
		{"첫날이 다룰 수 있는 날짜보다 앞이면 만들 수 없다", mustDate(t, "0001-01-13"), 14},
		{"길이가 터무니없이 길면 만들 수 없다", mustDate(t, "2026-10-20"), 10_000_000},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewWindow(tt.asOf, tt.length)
			require.ErrorIs(t, err, ErrInvalidWindow)
			assert.Equal(t, Window{}, got)
		})
	}
}

func TestWindowContains(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	window, err := NewWindow(asOf, 14)
	require.NoError(t, err)

	tests := []struct {
		name string
		date recorddate.Date
		want bool
	}{
		{"기준일은 창 안이다", asOf, true},
		{"기준일 다음 날은 창 밖이다", asOf.AddDays(1), false},
		{"기준일에서 13일 앞은 창 안이다", asOf.AddDays(-13), true},
		{"기준일에서 14일 앞은 창 밖이다", asOf.AddDays(-14), false},
		{"창 가운데의 날은 창 안이다", asOf.AddDays(-7), true},
		{"빈 날짜는 창 밖이다", recorddate.Date{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, window.Contains(tt.date))
		})
	}

	t.Run("빈 창에는 아무 날짜도 들지 않는다", func(t *testing.T) {
		assert.False(t, Window{}.Contains(asOf))
		assert.False(t, Window{}.Contains(recorddate.Date{}))
		assert.Equal(t, 0, Window{}.Length())
	})
}

func TestWindowDaysIn(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	window, err := NewWindow(asOf, 14)
	require.NoError(t, err)

	t.Run("창 안의 하루만 받은 순서대로 고른다", func(t *testing.T) {
		days := daysBefore(t, asOf, 20, 14, 13, 8, 0, -1, -9)

		got := window.DaysIn(days)

		require.Len(t, got, 3)
		assert.Equal(t, asOf.AddDays(-13), got[0].Date)
		assert.Equal(t, asOf.AddDays(-8), got[1].Date)
		assert.Equal(t, asOf, got[2].Date)
	})

	t.Run("받은 슬라이스를 고치지 않고, 돌려준 슬라이스를 고쳐도 원래 기록은 그대로다", func(t *testing.T) {
		days := daysBefore(t, asOf, 14, 13, 8, 0)
		before := slices.Clone(days)

		got := window.DaysIn(days)
		require.Len(t, got, 3)
		got[0].Judgements[signal.Sleep.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}

		assert.Equal(t, before, days)
	})

	t.Run("창 안에 든 날이 없으면 빈 슬라이스다", func(t *testing.T) {
		assert.Empty(t, window.DaysIn(daysBefore(t, asOf, 30, 14, -1)))
		assert.Empty(t, window.DaysIn(nil))
	})

	t.Run("점수가 센 대화 일수와 같은 날을 고른다", func(t *testing.T) {
		days := sampleDays(t, asOf)
		result, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)
		assert.Len(t, window.DaysIn(days), result.ConversationDays)
	})
}
