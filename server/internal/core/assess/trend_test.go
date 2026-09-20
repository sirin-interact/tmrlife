package assess

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func TestTrendGrid(t *testing.T) {
	// 6월 14일까지 두 주의 기록이 있고, 기준일은 6월 20일이다. 창은 6월 7일부터 20일까지다.
	records := join(
		times(6, "xxxx...."),
		[]string{
			"O.x.....", // 6월 7일: 흥미 저하가 관찰됐다
			"xO.x....", // 6월 8일: 흥미는 괜찮았지만 우울감이 관찰됐다
			"x..O....", // 6월 9일: 기분은 괜찮았고 피로가 관찰됐다
			"x.O.....", // 6월 10일: 잠을 설쳤다
			"....O...", // 6월 11일: 식욕 이야기만 했다. 세 줄 어디에도 들지 않는다
			"",         // 6월 12일: 대화하지 않았다
			".xox....", // 6월 13일: 우울감은 괜찮았고, 잠은 미루어 짐작한 관찰이다
			"oo......", // 6월 14일: 두 항목 모두 관찰됐다
		},
		silence(5),
		[]string{"..x....."}, // 6월 20일
		[]string{"OOOO...."}, // 6월 21일: 기준일 뒤라 보지 않는다
	)
	got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-20").Trend

	t.Run("칸은 기준일을 포함한 최근 14일이고 하루에 한 칸이다", func(t *testing.T) {
		assert.Equal(t, mustDate(t, "2026-06-07"), got.From)
		assert.Equal(t, mustDate(t, "2026-06-20"), got.To)
		require.Len(t, got.Dates, 14)
		for offset, date := range got.Dates {
			assert.Equal(t, got.From.AddDays(offset), date)
		}
		assert.Equal(t, 8, got.ConversationDays)
	})

	t.Run("줄은 기분, 수면, 에너지의 순서다", func(t *testing.T) {
		assert.Equal(t, signal.TrendMood, got.Rows[0].Row)
		assert.Equal(t, signal.TrendSleep, got.Rows[1].Row)
		assert.Equal(t, signal.TrendEnergy, got.Rows[2].Row)
		for _, row := range got.Rows {
			assert.Len(t, row.Marks, 14, row.Row.String())
			assert.Equal(t, 8, row.ConversationDays, row.Row.String())
			assert.Equal(t, row, got.Row(row.Row))
		}
		assert.Equal(t, RowTrend{}, got.Row(signal.TrendRow(0)))
	})

	const (
		blank  = MarkNoConversation
		small  = MarkNotMentioned
		hollow = MarkNotObserved
		filled = MarkObserved
	)

	t.Run("기분 줄은 흥미 저하와 우울감 가운데 하나라도 관찰되면 찬 점이다", func(t *testing.T) {
		want := []Mark{filled, filled, hollow, hollow, small, blank, hollow, filled, blank, blank, blank, blank, blank, small}

		assert.Equal(t, want, got.Row(signal.TrendMood).Marks)
		assert.Equal(t, 3, got.Row(signal.TrendMood).ObservedDays)
	})

	t.Run("수면 줄", func(t *testing.T) {
		want := []Mark{hollow, small, small, filled, small, blank, filled, small, blank, blank, blank, blank, blank, hollow}

		assert.Equal(t, want, got.Row(signal.TrendSleep).Marks)
		assert.Equal(t, 2, got.Row(signal.TrendSleep).ObservedDays, "미루어 짐작한 관찰도 관찰됨이다")
	})

	t.Run("에너지 줄은 피로 항목을 본다", func(t *testing.T) {
		want := []Mark{small, hollow, filled, small, small, blank, hollow, small, blank, blank, blank, blank, blank, small}

		assert.Equal(t, want, got.Row(signal.TrendEnergy).Marks)
		assert.Equal(t, 1, got.Row(signal.TrendEnergy).ObservedDays)
	})

	t.Run("평소의 빈도는 기준선 기간의 일수로 적는다", func(t *testing.T) {
		// 기준선 기간은 6월 1일부터 14일까지이고 그 안에서 13일을 대화했다.
		assert.Equal(t, baseline.Rate{ObservedDays: 3, Days: 13}, got.Row(signal.TrendMood).Usual)
		assert.Equal(t, baseline.Rate{ObservedDays: 2, Days: 13}, got.Row(signal.TrendSleep).Usual)
		assert.Equal(t, baseline.Rate{ObservedDays: 1, Days: 13}, got.Row(signal.TrendEnergy).Usual)
	})
}

func TestTrendFollowsTheWindowSetting(t *testing.T) {
	p := params.Default()
	p.Window.Days = 7
	p.Window.MinConversationDays = 4
	p.Score.ItemScore2MinDays = 4
	p.Score.ItemScore3MinDays = 6

	got := evaluateWith(t, diary(t, "2026-06-01", times(10, "..O.....")...), "2026-06-10", p).Trend

	assert.Equal(t, mustDate(t, "2026-06-04"), got.From)
	assert.Len(t, got.Dates, 7)
	assert.Equal(t, 7, got.Row(signal.TrendSleep).ObservedDays)
}

func TestTrendComparison(t *testing.T) {
	t.Run("기준선이 잡히기 전에는 평소의 빈도를 내보내지 않고 견주지도 않는다", func(t *testing.T) {
		days := diary(t, "2026-06-01", times(14, "..O.....")...)

		// 6월 14일은 기준선 기간의 마지막 날이다. 그날이 지나야 기준선이 잡힌다.
		got := evaluate(t, days, "2026-06-14")

		require.False(t, got.Baseline.Established)
		require.Equal(t, 14, got.Baseline.TrendRate(signal.TrendSleep).ObservedDays, "모으는 중인 값은 있다")
		for _, row := range got.Trend.Rows {
			assert.Equal(t, baseline.Rate{}, row.Usual, row.Row.String())
			assert.Equal(t, ComparisonNone, row.Comparison, row.Row.String())
		}
	})

	t.Run("기준선이 잡힌 다음 날부터 견준다", func(t *testing.T) {
		days := diary(t, "2026-06-01", times(15, "..O.....")...)

		got := evaluate(t, days, "2026-06-15")

		assert.Equal(t, Similar, got.Trend.Row(signal.TrendSleep).Comparison)
	})

	t.Run("창 안에서 대화한 날이 7일이 안 되면 견주지 않는다", func(t *testing.T) {
		// 두 주를 채운 뒤 여드레를 쉬고 엿새를 대화했다. 엿새 모두 잠을 설쳤어도 "평소보다 잦음"이라고 말하지 않는다.
		records := join(times(14, "..x....."), silence(8), times(6, "..O....."))

		got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-28")

		sleep := got.Trend.Row(signal.TrendSleep)
		assert.Equal(t, 6, sleep.ObservedDays)
		assert.Equal(t, 6, sleep.ConversationDays)
		assert.Equal(t, baseline.Rate{ObservedDays: 0, Days: 14}, sleep.Usual, "평소의 빈도는 그대로 보여줄 수 있다")
		assert.Equal(t, ComparisonNone, sleep.Comparison)
	})

	t.Run("하루를 더 대화해 7일이 되면 견준다", func(t *testing.T) {
		records := join(times(14, "..x....."), silence(8), times(7, "..O....."))

		got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-29")

		assert.Equal(t, MoreOften, got.Trend.Row(signal.TrendSleep).Comparison)
	})

	t.Run("나아진 것도 말할 수 있다", func(t *testing.T) {
		records := join(times(14, "..O....."), times(14, "..x....."))

		got := evaluate(t, diary(t, "2026-06-01", records...), "2026-06-28")

		assert.Equal(t, LessOften, got.Trend.Row(signal.TrendSleep).Comparison)
	})
}

func TestCompare(t *testing.T) {
	usual := func(observed, days int) baseline.Rate {
		return baseline.Rate{ObservedDays: observed, Days: days}
	}

	tests := []struct {
		name     string
		observed int
		days     int
		usual    baseline.Rate
		want     Comparison
	}{
		{"11일 중 7일은 평소 9일 중 3일보다 잦다", 7, 11, usual(3, 9), MoreOften},
		{"11일 중 5일은 평소 9일 중 3일과 비슷하다", 5, 11, usual(3, 9), Similar},
		{"11일 중 1일은 평소 9일 중 3일보다 드물다", 1, 11, usual(3, 9), LessOften},

		// 차이가 기준과 딱 같은 경우: 10일 중 2일(20%)이 평소일 때
		{"차이가 딱 20퍼센트포인트면 잦음이다", 4, 10, usual(2, 10), MoreOften},
		{"차이가 10퍼센트포인트면 비슷함이다", 3, 10, usual(2, 10), Similar},
		{"차이가 딱 20퍼센트포인트 아래로 벌어지면 드묾이다", 0, 10, usual(2, 10), LessOften},
		{"차이가 10퍼센트포인트 아래면 비슷함이다", 1, 10, usual(2, 10), Similar},

		// 분모가 서로 다를 때: 14일 중 5일(35.7%)과 7일 중 1일(14.3%)의 차이는 21.4다
		{"분모가 달라도 비율의 차이로 견준다", 5, 14, usual(1, 7), MoreOften},
		{"14일 중 4일과 7일 중 1일의 차이는 14.3이라 비슷함이다", 4, 14, usual(1, 7), Similar},

		{"평소에도 최근에도 한 번도 없었으면 비슷함이다", 0, 14, usual(0, 14), Similar},
		{"평소에도 최근에도 날마다였으면 비슷함이다", 14, 14, usual(14, 14), Similar},
		{"평소에는 없던 것이 날마다 있으면 잦음이다", 14, 14, usual(0, 14), MoreOften},

		{"평소가 아직 없으면 견줄 수 없다", 7, 11, baseline.Rate{}, ComparisonNone},
		{"대화한 날이 6일이면 견줄 수 없다", 6, 6, usual(0, 14), ComparisonNone},
		{"대화한 날이 7일이면 견준다", 7, 7, usual(0, 14), MoreOften},
		{"대화한 날이 없으면 견줄 수 없다", 0, 0, usual(3, 9), ComparisonNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compare(tt.observed, tt.days, tt.usual, params.Default()))
		})
	}

	t.Run("차이의 기준은 조정 값에서 받는다", func(t *testing.T) {
		p := params.Default()
		p.Trend.MinDifferencePercent = 10

		assert.Equal(t, MoreOften, compare(3, 10, usual(2, 10), p))
		assert.Equal(t, LessOften, compare(1, 10, usual(2, 10), p))
		assert.Equal(t, Similar, compare(2, 10, usual(2, 10), p))
	})

	t.Run("견주는 데 필요한 대화 일수도 조정 값에서 받는다", func(t *testing.T) {
		p := params.Default()
		p.Window.MinConversationDays = 3

		assert.Equal(t, MoreOften, compare(3, 3, usual(0, 14), p))
	})
}

func TestMark(t *testing.T) {
	tests := []struct {
		name string
		mark Mark
		id   string
	}{
		{"대화하지 않은 날은 빈칸이다", MarkNoConversation, "no_conversation"},
		{"언급 없음은 작은 점이다", MarkNotMentioned, "not_mentioned"},
		{"관찰되지 않음은 빈 점이다", MarkNotObserved, "not_observed"},
		{"관찰됨은 찬 점이다", MarkObserved, "observed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.mark.Valid())
			assert.Equal(t, tt.id, tt.mark.String())

			parsed, err := ParseMark(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.mark, parsed)

			encoded, err := json.Marshal(tt.mark)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Mark
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.mark, decoded)
		})
	}

	t.Run("빈 값은 대화하지 않은 날이다", func(t *testing.T) {
		var m Mark

		assert.Equal(t, MarkNoConversation, m)
	})

	t.Run("대화한 날의 표시는 그날의 판단에서 온다", func(t *testing.T) {
		assert.Equal(t, MarkObserved, markOf(signal.Observed))
		assert.Equal(t, MarkNotObserved, markOf(signal.NotObserved))
		assert.Equal(t, MarkNotMentioned, markOf(signal.NotMentioned))
	})

	t.Run("정해진 값이 아니면 적지도 읽지도 않는다", func(t *testing.T) {
		for _, m := range []Mark{-1, 4} {
			assert.False(t, m.Valid())
			_, err := m.MarshalText()
			require.ErrorIs(t, err, ErrInvalidMark)
		}
		assert.Equal(t, "Mark(4)", Mark(4).String())

		m := MarkObserved
		err := m.UnmarshalText([]byte("Observed"))
		require.ErrorIs(t, err, ErrInvalidMark)
		assert.NotContains(t, err.Error(), "Observed")
		assert.Equal(t, MarkObserved, m, "읽지 못하면 값을 바꾸지 않는다")
	})
}

func TestComparison(t *testing.T) {
	tests := []struct {
		name       string
		comparison Comparison
		id         string
	}{
		{"견줄 수 없음", ComparisonNone, "none"},
		{"평소보다 드묾", LessOften, "less_often"},
		{"평소와 비슷함", Similar, "similar"},
		{"평소보다 잦음", MoreOften, "more_often"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.comparison.Valid())
			assert.Equal(t, tt.id, tt.comparison.String())

			parsed, err := ParseComparison(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.comparison, parsed)

			encoded, err := json.Marshal(tt.comparison)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Comparison
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.comparison, decoded)
		})
	}

	t.Run("빈 값은 견줄 수 없음이다", func(t *testing.T) {
		var c Comparison

		assert.Equal(t, ComparisonNone, c)
	})

	t.Run("정해진 값이 아니면 적지도 읽지도 않는다", func(t *testing.T) {
		for _, c := range []Comparison{-1, 4} {
			assert.False(t, c.Valid())
			_, err := c.MarshalText()
			require.ErrorIs(t, err, ErrInvalidComparison)
		}
		assert.Equal(t, "Comparison(4)", Comparison(4).String())

		c := MoreOften
		err := c.UnmarshalText([]byte("more often"))
		require.ErrorIs(t, err, ErrInvalidComparison)
		assert.NotContains(t, err.Error(), "more often")
		assert.Equal(t, MoreOften, c, "읽지 못하면 값을 바꾸지 않는다")
	})
}

func TestTrendDatesAreFresh(t *testing.T) {
	days := diary(t, "2026-06-01", times(10, "..O.....")...)
	first := evaluate(t, days, "2026-06-10")
	first.Trend.Dates[0] = recorddate.Date{}
	first.Trend.Rows[0].Marks[0] = MarkObserved

	second := evaluate(t, days, "2026-06-10")

	assert.Equal(t, mustDate(t, "2026-05-28"), second.Trend.Dates[0], "돌려받은 값을 고쳐도 다음 평가에 영향이 없다")
	assert.Equal(t, MarkNoConversation, second.Trend.Rows[0].Marks[0])
}
