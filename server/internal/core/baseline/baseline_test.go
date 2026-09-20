package baseline

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func mustDate(t *testing.T, s string) recorddate.Date {
	t.Helper()
	d, err := recorddate.Parse(s)
	require.NoError(t, err)
	return d
}

// entry는 시험에 쓰는 대화한 날 하나다.
type entry struct {
	// offset은 첫날에서 며칠 뒤인지다. 첫날이 0이고 14일째 날이 13이다.
	offset int
	// observed는 그날 관찰된 항목 수다.
	observed int
}

// build는 entry들을 하루의 목록으로 옮긴다. 관찰된 항목은 앞 항목부터 채운다.
func build(first recorddate.Date, entries []entry) []signal.Day {
	days := make([]signal.Day, 0, len(entries))
	for _, e := range entries {
		day := signal.Day{Date: first.AddDays(e.offset)}
		for i := range e.observed {
			day.Judgements[i] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
		}
		days = append(days, day)
	}
	return days
}

// talkedOn은 관찰된 항목이 없는 대화한 날들을 만든다. 기간만 볼 때 쓴다.
func talkedOn(offsets ...int) []entry {
	entries := make([]entry, 0, len(offsets))
	for _, offset := range offsets {
		entries = append(entries, entry{offset: offset})
	}
	return entries
}

// withBaseline은 기준선의 조정 값만 바꾼 Params를 만든다. 나머지는 기본값이다.
func withBaseline(b params.Baseline) params.Params {
	p := params.Default()
	p.Baseline = b
	return p
}

// dayObserving은 적어 준 항목이 관찰된 하루를 만든다.
func dayObserving(date recorddate.Date, items ...signal.Item) signal.Day {
	day := signal.Day{Date: date}
	for _, item := range items {
		day.Judgements[item.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
	}
	return day
}

func TestComputePeriod(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	// 빈 날짜를 가리키는 표시다.
	const none = -1

	tests := []struct {
		name            string
		talked          []int
		asOf            int
		wantStart       int
		wantEnd         int
		wantExtended    bool
		wantDays        int
		wantEstablished bool
	}{
		// 첫 14일 안에 대화한 날이 7일 이상인 경우: 기간은 14일째 날(offset 13)에서 끝난다.
		{
			name:   "첫 14일 안에 대화한 날이 7일이면 14일째 날이 지난 뒤에 잡힌다",
			talked: []int{0, 2, 4, 6, 8, 10, 12}, asOf: 14,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "기준일이 14일째 날이면 아직 잡히지 않았다",
			talked: []int{0, 2, 4, 6, 8, 10, 12}, asOf: 13,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: false,
		},
		{
			name:   "7일을 이미 채웠어도 14일째 날 전에는 잡히지 않는다. 마지막 날은 정해져 있다",
			talked: []int{0, 1, 2, 3, 4, 5, 6}, asOf: 7,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: false,
		},
		{
			name:   "잡힌 뒤로는 기준일이 한참 뒤여도 같은 기간이다",
			talked: []int{0, 2, 4, 6, 8, 10, 12}, asOf: 200,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "첫 14일 안에 대화한 날이 7일을 넘으면 그 날들을 모두 쓴다",
			talked: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, asOf: 14,
			wantStart: 0, wantEnd: 13, wantDays: 14, wantEstablished: true,
		},
		{
			name:   "기간이 끝난 뒤의 대화는 기준선에 쓰지 않는다",
			talked: []int{0, 2, 4, 6, 8, 10, 12, 14, 15, 16, 30}, asOf: 40,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "14일째 날의 대화는 첫 14일에 든다",
			talked: []int{0, 1, 2, 3, 4, 5, 13}, asOf: 14,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: true,
		},

		// 첫 14일 안에 대화한 날이 7일이 안 되는 경우: 일곱 번째 대화 날까지 기간을 늘린다.
		{
			name:   "15일째 날의 대화는 첫 14일에 들지 않아서 기간을 늘린다",
			talked: []int{0, 1, 2, 3, 4, 5, 14}, asOf: 15,
			wantStart: 0, wantEnd: 14, wantExtended: true, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "첫 14일에 6일뿐이면 일곱 번째 대화 날이 지난 뒤에 잡힌다",
			talked: []int{0, 1, 2, 3, 4, 5, 20}, asOf: 21,
			wantStart: 0, wantEnd: 20, wantExtended: true, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "기준일이 일곱 번째 대화 날이면 아직 잡히지 않았다",
			talked: []int{0, 1, 2, 3, 4, 5, 20}, asOf: 20,
			wantStart: 0, wantEnd: 20, wantExtended: true, wantDays: 7, wantEstablished: false,
		},
		{
			name:   "일곱 번째 대화 날이 오기 전에는 마지막 날을 정하지 못한다",
			talked: []int{0, 1, 2, 3, 4, 5, 20}, asOf: 19,
			wantStart: 0, wantEnd: none, wantExtended: true, wantDays: 6, wantEstablished: false,
		},
		{
			name:   "늘린 기간은 일곱 번째 대화 날에서 멈춘다. 그 뒤의 대화는 쓰지 않는다",
			talked: []int{0, 1, 2, 3, 4, 5, 20, 21, 22, 23}, asOf: 30,
			wantStart: 0, wantEnd: 20, wantExtended: true, wantDays: 7, wantEstablished: true,
		},
		{
			name:   "드문드문 대화해도 일곱 번째 대화 날까지 늘린다",
			talked: []int{0, 10, 20, 30, 40, 50, 60}, asOf: 61,
			wantStart: 0, wantEnd: 60, wantExtended: true, wantDays: 7, wantEstablished: true,
		},

		// 아직 어느 쪽인지 알 수 없는 동안
		{
			name:   "첫 14일이 끝나기 전이고 대화한 날이 모자라면 마지막 날도, 늘릴지도 아직 모른다",
			talked: []int{0, 1, 2, 3, 4, 5}, asOf: 8,
			wantStart: 0, wantEnd: none, wantDays: 6, wantEstablished: false,
		},
		{
			name:   "기준일이 14일째 날이면 아직 늘린다고 하지 않는다. 그날 대화하면 7일이 찰 수 있다",
			talked: []int{0, 1, 2, 3, 4, 5}, asOf: 13,
			wantStart: 0, wantEnd: none, wantDays: 6, wantEstablished: false,
		},
		{
			name:   "14일째 날이 지났는데 대화한 날이 모자라면 기간을 늘린다",
			talked: []int{0, 1, 2, 3, 4, 5}, asOf: 14,
			wantStart: 0, wantEnd: none, wantExtended: true, wantDays: 6, wantEstablished: false,
		},
		{
			name:   "첫 대화 날 당일에는 그날 하루만 모여 있다",
			talked: []int{0}, asOf: 0,
			wantStart: 0, wantEnd: none, wantDays: 1, wantEstablished: false,
		},
		{
			name:   "하루만 대화하고 오래 쉬면 기준선은 잡히지 않은 채로 남는다",
			talked: []int{0}, asOf: 100,
			wantStart: 0, wantEnd: none, wantExtended: true, wantDays: 1, wantEstablished: false,
		},

		// 기준일보다 뒤의 기록
		{
			name:   "기준일보다 뒤의 대화는 아직 없는 것으로 본다",
			talked: []int{0, 2, 4, 6, 8, 10, 12}, asOf: 5,
			wantStart: 0, wantEnd: none, wantDays: 3, wantEstablished: false,
		},
		{
			name:   "기준일 당일의 대화는 센다",
			talked: []int{0, 2, 4, 6, 8, 10, 12}, asOf: 12,
			wantStart: 0, wantEnd: 13, wantDays: 7, wantEstablished: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := build(first, talkedOn(tt.talked...))
			asOf := first.AddDays(tt.asOf)

			got, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)

			wantDate := func(offset int) recorddate.Date {
				if offset == none {
					return recorddate.Date{}
				}
				return first.AddDays(offset)
			}
			assert.Equal(t, asOf, got.AsOf, "기준일")
			assert.Equal(t, wantDate(tt.wantStart), got.Start, "첫날")
			assert.Equal(t, wantDate(tt.wantEnd), got.End, "마지막 날")
			assert.Equal(t, tt.wantExtended, got.Extended, "기간을 늘렸는지")
			assert.Equal(t, tt.wantDays, got.Days, "기준선에 쓴 대화한 날 수")
			assert.Equal(t, tt.wantEstablished, got.Established, "잡혔는지")
		})
	}
}

// 잡히는 날짜를 달력 날짜로 적어 확인한다. 달과 해가 바뀌는 경우도 본다.
func TestComputeEstablishedOnExactDates(t *testing.T) {
	withSeven := []string{
		"2026-09-01", "2026-09-03", "2026-09-05", "2026-09-07", "2026-09-09", "2026-09-11", "2026-09-13",
	}
	withSixThenOne := []string{
		"2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04", "2026-09-05", "2026-09-06", "2026-09-21",
	}
	acrossMonths := []string{
		"2026-09-25", "2026-09-26", "2026-09-27", "2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01",
	}
	acrossLeapDay := []string{
		"2028-02-20", "2028-02-21", "2028-02-22", "2028-02-23", "2028-02-24", "2028-02-25", "2028-02-29",
	}
	acrossYears := []string{
		"2026-12-25", "2026-12-26", "2026-12-27", "2026-12-28", "2026-12-29", "2026-12-30", "2026-12-31",
	}

	tests := []struct {
		name            string
		dates           []string
		asOf            string
		wantEnd         string
		wantEstablished bool
	}{
		{"9월 1일에 시작해 7일을 채웠으면 9월 13일에는 아직이다", withSeven, "2026-09-13", "2026-09-14", false},
		{"기간의 마지막 날인 9월 14일에도 아직이다", withSeven, "2026-09-14", "2026-09-14", false},
		{"9월 15일부터 잡힌 상태다", withSeven, "2026-09-15", "2026-09-14", true},

		{"일곱 번째 대화가 9월 21일이면 9월 20일에는 마지막 날이 없다", withSixThenOne, "2026-09-20", "", false},
		{"일곱 번째 대화 날인 9월 21일에도 아직이다", withSixThenOne, "2026-09-21", "2026-09-21", false},
		{"9월 22일부터 잡힌 상태다", withSixThenOne, "2026-09-22", "2026-09-21", true},

		{"9월 25일에 시작하면 기간은 10월 8일까지다", acrossMonths, "2026-10-08", "2026-10-08", false},
		{"10월 9일부터 잡힌 상태다", acrossMonths, "2026-10-09", "2026-10-08", true},

		{"윤년 2월 20일에 시작하면 기간은 3월 4일까지다", acrossLeapDay, "2028-03-04", "2028-03-04", false},
		{"3월 5일부터 잡힌 상태다", acrossLeapDay, "2028-03-05", "2028-03-04", true},

		{"12월 25일에 시작하면 기간은 이듬해 1월 7일까지다", acrossYears, "2027-01-07", "2027-01-07", false},
		{"1월 8일부터 잡힌 상태다", acrossYears, "2027-01-08", "2027-01-07", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := make([]signal.Day, 0, len(tt.dates))
			for _, s := range tt.dates {
				days = append(days, signal.Day{Date: mustDate(t, s)})
			}

			got, err := Compute(days, mustDate(t, tt.asOf), params.Default())
			require.NoError(t, err)

			wantEnd := recorddate.Date{}
			if tt.wantEnd != "" {
				wantEnd = mustDate(t, tt.wantEnd)
			}
			assert.Equal(t, wantEnd, got.End)
			assert.Equal(t, tt.wantEstablished, got.Established)
		})
	}
}

func TestComputeMu(t *testing.T) {
	first := mustDate(t, "2026-09-01")

	tests := []struct {
		name      string
		entries   []entry
		wantDays  int
		wantTotal int
		wantMu    float64
	}{
		{
			name: "대화한 날들의 그날 관찰된 항목 수를 평균한다",
			entries: []entry{
				{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 1}, {5, 0}, {6, 0},
			},
			wantDays: 7, wantTotal: 7, wantMu: 1.0,
		},
		{
			name: "대화하지 않은 날은 분모에 들지 않는다. 14일 중 7일만 대화했어도 7로 나눈다",
			entries: []entry{
				{0, 2}, {2, 2}, {4, 2}, {6, 2}, {8, 2}, {10, 2}, {12, 2},
			},
			wantDays: 7, wantTotal: 14, wantMu: 2.0,
		},
		{
			name: "나누어떨어지지 않는 평균",
			entries: []entry{
				{0, 3}, {1, 1}, {2, 0}, {3, 0}, {4, 2}, {5, 1}, {6, 2},
			},
			wantDays: 7, wantTotal: 9, wantMu: 9.0 / 7.0,
		},
		{
			name: "아무것도 관찰되지 않은 사람의 평소는 0이다",
			entries: []entry{
				{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}, {5, 0}, {6, 0},
			},
			wantDays: 7, wantTotal: 0, wantMu: 0,
		},
		{
			name: "처음부터 여덟 항목이 날마다 관찰된 사람의 평소는 8이다",
			entries: []entry{
				{0, 8}, {1, 8}, {2, 8}, {3, 8}, {4, 8}, {5, 8}, {6, 8},
			},
			wantDays: 7, wantTotal: 56, wantMu: 8.0,
		},
		{
			name: "기간이 끝난 뒤에 나빠진 날은 평소에 섞이지 않는다",
			entries: []entry{
				{0, 1}, {2, 1}, {4, 1}, {6, 1}, {8, 1}, {10, 1}, {12, 1},
				{14, 8}, {15, 8}, {16, 8},
			},
			wantDays: 7, wantTotal: 7, wantMu: 1.0,
		},
		{
			name: "기간을 늘렸으면 일곱 번째 대화 날까지만 평균에 든다",
			entries: []entry{
				{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}, {5, 1},
				{20, 8}, {21, 8},
			},
			wantDays: 7, wantTotal: 14, wantMu: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(build(first, tt.entries), first.AddDays(60), params.Default())
			require.NoError(t, err)

			require.True(t, got.Established)
			assert.Equal(t, tt.wantDays, got.Days)
			assert.Equal(t, tt.wantTotal, got.ObservedTotal)
			assert.InDelta(t, tt.wantMu, got.Mu, 1e-12)
		})
	}

	t.Run("잡히기 전의 평균은 지금까지 모인 값이다", func(t *testing.T) {
		got, err := Compute(build(first, []entry{{0, 3}, {1, 1}}), first.AddDays(1), params.Default())
		require.NoError(t, err)

		assert.False(t, got.Established)
		assert.Equal(t, 2, got.Days)
		assert.Equal(t, 4, got.ObservedTotal)
		assert.InDelta(t, 2.0, got.Mu, 0)
	})
}

func TestComputeRates(t *testing.T) {
	first := mustDate(t, "2026-09-01")

	notObserved := signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
	sleptWell := signal.Day{Date: first.AddDays(3)}
	sleptWell.Judgements[signal.Sleep.Index()] = notObserved
	sleptWell.Judgements[signal.Mood.Index()] = notObserved

	days := []signal.Day{
		dayObserving(first, signal.Interest, signal.Sleep),
		dayObserving(first.AddDays(1), signal.Mood),
		dayObserving(first.AddDays(2), signal.Interest, signal.Mood, signal.Fatigue),
		sleptWell,
		dayObserving(first.AddDays(4), signal.Sleep),
		dayObserving(first.AddDays(5)),
		dayObserving(first.AddDays(6), signal.Fatigue, signal.Appetite),
		dayObserving(first.AddDays(7), signal.Concentration),
		// 기간이 끝난 뒤의 날이다. 비율에 들지 않아야 한다.
		dayObserving(first.AddDays(14), signal.Psychomotor, signal.SelfBlame, signal.Sleep),
	}

	got, err := Compute(days, first.AddDays(20), params.Default())
	require.NoError(t, err)
	require.True(t, got.Established)
	require.Equal(t, 8, got.Days)

	assert.Equal(t, 10, got.ObservedTotal)
	assert.InDelta(t, 1.25, got.Mu, 0)

	itemTests := []struct {
		name string
		item signal.Item
		want int
	}{
		{"흥미 저하는 8일 중 2일", signal.Interest, 2},
		{"우울감은 8일 중 2일. 관찰되지 않음은 세지 않는다", signal.Mood, 2},
		{"수면은 8일 중 2일. 잘 잤다고 한 날과 기간 뒤의 날은 세지 않는다", signal.Sleep, 2},
		{"피로는 8일 중 2일", signal.Fatigue, 2},
		{"식욕은 8일 중 1일", signal.Appetite, 1},
		{"자기 비난은 8일 중 0일", signal.SelfBlame, 0},
		{"집중 곤란은 8일 중 1일", signal.Concentration, 1},
		{"느려짐 또는 초조는 8일 중 0일", signal.Psychomotor, 0},
	}
	for _, tt := range itemTests {
		t.Run(tt.name, func(t *testing.T) {
			rate := got.ItemRate(tt.item)
			assert.Equal(t, Rate{ObservedDays: tt.want, Days: 8}, rate)
			assert.InDelta(t, float64(tt.want)/8, rate.Value(), 1e-12)
		})
	}

	rowTests := []struct {
		name string
		row  signal.TrendRow
		want int
	}{
		// 흥미 저하 2일과 우울감 2일 가운데 하루는 같은 날이라 4일이 아니라 3일이다.
		{"기분 줄은 두 항목 가운데 하나라도 관찰된 날을 센다", signal.TrendMood, 3},
		{"수면 줄", signal.TrendSleep, 2},
		{"에너지 줄은 피로 항목을 본다", signal.TrendEnergy, 2},
	}
	for _, tt := range rowTests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, Rate{ObservedDays: tt.want, Days: 8}, got.TrendRate(tt.row))
		})
	}

	t.Run("항목이나 줄이 아닌 값을 물으면 빈 값이다", func(t *testing.T) {
		assert.Equal(t, Rate{}, got.ItemRate(signal.Item(0)))
		assert.Equal(t, Rate{}, got.ItemRate(signal.Item(99)))
		assert.Equal(t, Rate{}, got.TrendRate(signal.TrendRow(0)))
	})

	t.Run("대화한 날이 없는 비율의 값은 0이다", func(t *testing.T) {
		assert.InDelta(t, 0, Rate{}.Value(), 0)
	})
}

// 기준선은 저장해 두지 않고 남은 기록으로 다시 구한다. 하루를 지우면 그날은 목록에서 빠져서 들어온다.
func TestComputeAfterDeletingDays(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	all := []entry{
		{0, 2}, {1, 0}, {2, 4}, {4, 0}, {6, 1}, {8, 0}, {10, 2}, {12, 0},
		// 기간이 끝난 뒤의 날들
		{16, 2}, {18, 3},
	}
	asOf := first.AddDays(25)

	without := func(offsets ...int) []entry {
		return slices.DeleteFunc(slices.Clone(all), func(e entry) bool {
			return slices.Contains(offsets, e.offset)
		})
	}

	tests := []struct {
		name            string
		entries         []entry
		wantStart       int
		wantEnd         int
		wantExtended    bool
		wantDays        int
		wantTotal       int
		wantMu          float64
		wantEstablished bool
	}{
		{
			name:      "지우기 전",
			entries:   all,
			wantStart: 0, wantEnd: 13, wantDays: 8, wantTotal: 9, wantMu: 9.0 / 8.0, wantEstablished: true,
		},
		{
			name:      "기간 안의 날을 지우면 평균이 달라진다",
			entries:   without(2),
			wantStart: 0, wantEnd: 13, wantDays: 7, wantTotal: 5, wantMu: 5.0 / 7.0, wantEstablished: true,
		},
		{
			name:      "지워서 첫 14일의 대화가 6일이 되면 일곱 번째 대화 날까지 기간이 늘어난다",
			entries:   without(2, 4),
			wantStart: 0, wantEnd: 16, wantExtended: true, wantDays: 7, wantTotal: 7, wantMu: 1.0, wantEstablished: true,
		},
		{
			name:      "첫날을 지우면 시작이 다음 대화 날로 옮겨 가고 기간도 하루 밀린다",
			entries:   without(0),
			wantStart: 1, wantEnd: 14, wantDays: 7, wantTotal: 7, wantMu: 1.0, wantEstablished: true,
		},
		{
			name:      "기간이 끝난 뒤의 날을 지워도 기준선은 그대로다",
			entries:   without(16, 18),
			wantStart: 0, wantEnd: 13, wantDays: 8, wantTotal: 9, wantMu: 9.0 / 8.0, wantEstablished: true,
		},
		{
			name:      "지워서 일곱 번째 대화 날이 없어지면 잡혔던 기준선이 풀린다",
			entries:   without(2, 4, 16, 18),
			wantStart: 0, wantEnd: -1, wantExtended: true, wantDays: 6, wantTotal: 5, wantMu: 5.0 / 6.0, wantEstablished: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(build(first, tt.entries), asOf, params.Default())
			require.NoError(t, err)

			wantEnd := recorddate.Date{}
			if tt.wantEnd >= 0 {
				wantEnd = first.AddDays(tt.wantEnd)
			}
			assert.Equal(t, first.AddDays(tt.wantStart), got.Start)
			assert.Equal(t, wantEnd, got.End)
			assert.Equal(t, tt.wantExtended, got.Extended)
			assert.Equal(t, tt.wantDays, got.Days)
			assert.Equal(t, tt.wantTotal, got.ObservedTotal)
			assert.InDelta(t, tt.wantMu, got.Mu, 1e-12)
			assert.Equal(t, tt.wantEstablished, got.Established)
		})
	}

	t.Run("지워서 기간이 늘어나면 잡히는 날짜도 뒤로 밀린다", func(t *testing.T) {
		// 지우기 전에는 9월 15일(offset 14)부터 잡혀 있었다.
		// 두 날을 지우면 일곱 번째 대화 날이 offset 16이 되어, 그날에는 아직 잡히지 않은 것이 된다.
		before, err := Compute(build(first, all), first.AddDays(16), params.Default())
		require.NoError(t, err)
		after, err := Compute(build(first, without(2, 4)), first.AddDays(16), params.Default())
		require.NoError(t, err)

		assert.True(t, before.Established)
		assert.False(t, after.Established)
		assert.Equal(t, first.AddDays(16), after.End)
	})
}

func TestComputeIsFixedOnceEstablished(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	entries := []entry{
		{0, 1}, {2, 3}, {4, 0}, {6, 2}, {8, 1}, {10, 0}, {12, 2},
		{14, 6}, {15, 7}, {20, 8}, {33, 5},
	}
	days := build(first, entries)
	p := params.Default()

	reference, err := Compute(days, first.AddDays(14), p)
	require.NoError(t, err)
	require.True(t, reference.Established)

	t.Run("기준일을 뒤로 옮겨도 기준일 말고는 달라지지 않는다", func(t *testing.T) {
		for offset := 14; offset <= 60; offset++ {
			got, err := Compute(days, first.AddDays(offset), p)
			require.NoError(t, err)

			want := reference
			want.AsOf = first.AddDays(offset)
			assert.Equal(t, want, got, "기준일 offset %d", offset)
		}
	})

	t.Run("기준일보다 뒤의 기록은 넘겨도 넘기지 않아도 결과가 같다", func(t *testing.T) {
		for offset := 0; offset <= 40; offset++ {
			asOf := first.AddDays(offset)
			known := slices.DeleteFunc(slices.Clone(days), func(d signal.Day) bool { return d.Date.After(asOf) })

			withFuture, err := Compute(days, asOf, p)
			require.NoError(t, err)
			withoutFuture, err := Compute(known, asOf, p)
			require.NoError(t, err)

			assert.Equal(t, withoutFuture, withFuture, "기준일 offset %d", offset)
		}
	})
}

func TestComputeWithoutConversation(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	tests := []struct {
		name string
		days []signal.Day
	}{
		{"기록이 nil이다", nil},
		{"기록이 비어 있다", []signal.Day{}},
		{"대화한 날이 모두 기준일보다 뒤다", []signal.Day{{Date: mustDate(t, "2026-09-21")}, {Date: mustDate(t, "2026-09-25")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())
			require.NoError(t, err)
			assert.Equal(t, Baseline{AsOf: asOf}, got)
		})
	}
}

func TestComputeCustomParams(t *testing.T) {
	first := mustDate(t, "2026-09-01")

	tests := []struct {
		name            string
		p               params.Baseline
		talked          []int
		asOf            int
		wantEnd         int
		wantExtended    bool
		wantDays        int
		wantEstablished bool
	}{
		{
			name:   "기간 7일에 대화 3일: 7일째 날이 지나면 잡힌다",
			p:      params.Baseline{WindowDays: 7, MinConversationDays: 3},
			talked: []int{0, 3, 6, 7}, asOf: 7,
			wantEnd: 6, wantDays: 3, wantEstablished: true,
		},
		{
			name:   "기간 7일에 대화 3일: 모자라면 세 번째 대화 날까지 늘린다",
			p:      params.Baseline{WindowDays: 7, MinConversationDays: 3},
			talked: []int{0, 3, 9, 10}, asOf: 10,
			wantEnd: 9, wantExtended: true, wantDays: 3, wantEstablished: true,
		},
		{
			name:   "기간 1일에 대화 1일: 첫날 하루가 기준선이고 다음 날부터 잡힌다",
			p:      params.Baseline{WindowDays: 1, MinConversationDays: 1},
			talked: []int{0, 1, 2}, asOf: 1,
			wantEnd: 0, wantDays: 1, wantEstablished: true,
		},
		{
			name:   "기간 1일에 대화 1일: 첫날 당일에는 아직이다",
			p:      params.Baseline{WindowDays: 1, MinConversationDays: 1},
			talked: []int{0, 1, 2}, asOf: 0,
			wantEnd: 0, wantDays: 1, wantEstablished: false,
		},
		{
			name:   "기간 28일에 대화 28일: 하루도 빠지지 않아야 늘리지 않는다",
			p:      params.Baseline{WindowDays: 28, MinConversationDays: 28},
			talked: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27},
			asOf:   28, wantEnd: 27, wantDays: 28, wantEstablished: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(build(first, talkedOn(tt.talked...)), first.AddDays(tt.asOf), withBaseline(tt.p))
			require.NoError(t, err)

			assert.Equal(t, first.AddDays(tt.wantEnd), got.End)
			assert.Equal(t, tt.wantExtended, got.Extended)
			assert.Equal(t, tt.wantDays, got.Days)
			assert.Equal(t, tt.wantEstablished, got.Established)
		})
	}
}

func TestComputeNearEndOfSupportedDates(t *testing.T) {
	// 14일째 날이 날짜의 지원 범위(9999-12-31)를 넘어가면 마지막 날을 적을 수 없다.
	// 틀린 날짜를 만들어 내지 않고 잡히지 않은 상태로 둔다.
	first := mustDate(t, "9999-12-25")
	days := build(first, []entry{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}, {5, 1}, {6, 1}})

	got, err := Compute(days, mustDate(t, "9999-12-31"), params.Default())
	require.NoError(t, err)

	assert.False(t, got.Established)
	assert.True(t, got.End.IsZero())
	assert.Equal(t, 7, got.Days)
	assert.InDelta(t, 1.0, got.Mu, 0)
}

func TestContains(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	p := params.Default()

	established, err := Compute(build(first, talkedOn(0, 2, 4, 6, 8, 10, 12, 20)), first.AddDays(30), p)
	require.NoError(t, err)
	require.True(t, established.Established)

	collecting, err := Compute(build(first, talkedOn(0, 2, 4)), first.AddDays(5), p)
	require.NoError(t, err)
	require.True(t, collecting.End.IsZero())

	empty, err := Compute(nil, first, p)
	require.NoError(t, err)

	tests := []struct {
		name   string
		b      Baseline
		offset int
		want   bool
	}{
		{"첫날은 기간에 든다", established, 0, true},
		{"기간 안의 대화하지 않은 날도 기간에 든다", established, 5, true},
		{"마지막 날은 기간에 든다", established, 13, true},
		{"마지막 날의 다음 날은 기간 밖이다", established, 14, false},
		{"첫날의 전날은 기간 밖이다", established, -1, false},
		{"모으는 중에는 기준일까지가 기간이다", collecting, 5, true},
		{"모으는 중에 기준일보다 뒤는 기간 밖이다", collecting, 6, false},
		{"대화한 날이 없으면 어떤 날도 기간에 들지 않는다", empty, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.b.Contains(first.AddDays(tt.offset)))
		})
	}

	t.Run("빈 날짜는 기간에 들지 않는다", func(t *testing.T) {
		assert.False(t, established.Contains(recorddate.Date{}))
	})
}

func TestComputeErrors(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	asOf := first.AddDays(20)
	valid := build(first, talkedOn(0, 1, 2))

	t.Run("기준일이 비어 있다", func(t *testing.T) {
		_, err := Compute(valid, recorddate.Date{}, params.Default())
		assert.ErrorIs(t, err, ErrZeroAsOf)
	})

	dayTests := []struct {
		name      string
		days      []signal.Day
		wantErr   error
		wantIndex int
	}{
		{
			name:    "날짜순이 아니다",
			days:    build(first, talkedOn(0, 2, 1)),
			wantErr: signal.ErrUnsortedDays, wantIndex: 2,
		},
		{
			name:    "같은 날짜가 두 번 나온다",
			days:    build(first, talkedOn(0, 1, 1)),
			wantErr: signal.ErrDuplicateDate, wantIndex: 2,
		},
		{
			name:    "날짜가 빠진 하루가 있다",
			days:    []signal.Day{{}},
			wantErr: signal.ErrZeroDate, wantIndex: 0,
		},
		{
			name: "판단과 명시성이 맞지 않는 하루가 있다",
			days: []signal.Day{
				{Date: first},
				{Date: first.AddDays(1), Judgements: [signal.ItemCount]signal.Judgement{{Status: signal.Observed}}},
			},
			wantErr: signal.ErrInconsistentJudgement, wantIndex: 1,
		},
		{
			name:    "기준일보다 뒤에 있는 틀린 하루도 그냥 넘기지 않는다",
			days:    build(first, talkedOn(0, 1, 30, 29)),
			wantErr: signal.ErrUnsortedDays, wantIndex: 3,
		},
	}
	for _, tt := range dayTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())
			require.ErrorIs(t, err, tt.wantErr)

			var dayErr *signal.DayError
			require.ErrorAs(t, err, &dayErr)
			assert.Equal(t, tt.wantIndex, dayErr.Index)
			assert.Equal(t, Baseline{}, got)
		})
	}

	paramTests := []struct {
		name      string
		p         params.Baseline
		wantField string
	}{
		{"기간이 0일이다", params.Baseline{WindowDays: 0, MinConversationDays: 7}, "Baseline.WindowDays"},
		{"기간이 음수다", params.Baseline{WindowDays: -14, MinConversationDays: 7}, "Baseline.WindowDays"},
		{"필요한 대화 일수가 0이다", params.Baseline{WindowDays: 14, MinConversationDays: 0}, "Baseline.MinConversationDays"},
		{"필요한 대화 일수가 기간보다 길다", params.Baseline{WindowDays: 14, MinConversationDays: 15}, "Baseline.MinConversationDays"},
		{"빈 조정 값이다", params.Baseline{}, "Baseline.WindowDays"},
	}
	for _, tt := range paramTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compute(valid, asOf, withBaseline(tt.p))

			var fieldErr *params.FieldError
			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, tt.wantField, fieldErr.Field)
		})
	}

	t.Run("다른 계산에 쓰는 조정 값이 틀려도 돌리지 않는다", func(t *testing.T) {
		p := params.Default()
		p.CUSUM.H = 0

		_, err := Compute(valid, asOf, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "CUSUM.H", fieldErr.Field)
	})
}

func TestComputeIsPure(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	days := build(first, []entry{{0, 1}, {2, 3}, {4, 0}, {6, 2}, {8, 1}, {10, 0}, {12, 2}, {14, 6}})
	original := slices.Clone(days)
	asOf := first.AddDays(20)

	a, err := Compute(days, asOf, params.Default())
	require.NoError(t, err)
	b, err := Compute(days, asOf, params.Default())
	require.NoError(t, err)

	t.Run("같은 기록에서는 같은 값이 나온다", func(t *testing.T) {
		assert.Equal(t, a, b)
	})
	t.Run("받은 기록을 고치지 않는다", func(t *testing.T) {
		assert.Equal(t, original, days)
	})
}
