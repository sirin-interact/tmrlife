package score

import (
	"fmt"
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

// daysBefore는 기준일에서 거슬러 센 자리마다 하루를 만든다. 0이 기준일이고 13이 기본 창의 첫날이다. 음수는 기준일 뒤다.
// 여덟 항목이 모두 언급 없음인 하루이고, 자리를 어떤 순서로 적든 날짜순으로 돌려준다.
func daysBefore(t *testing.T, asOf recorddate.Date, offsets ...int) []signal.Day {
	t.Helper()
	days := make([]signal.Day, 0, len(offsets))
	for _, offset := range offsets {
		days = append(days, signal.Day{Date: asOf.AddDays(-offset)})
	}
	sorted, err := signal.SortDays(days)
	require.NoError(t, err)
	return sorted
}

// lastDays는 기준일에서 끝나는 연속한 n일이다.
func lastDays(t *testing.T, asOf recorddate.Date, n int) []signal.Day {
	t.Helper()
	offsets := make([]int, n)
	for i := range offsets {
		offsets[i] = i
	}
	return daysBefore(t, asOf, offsets...)
}

// observe는 앞에서부터 count일에 그 항목을 관찰됨으로 적는다.
func observe(days []signal.Day, item signal.Item, count int) {
	for i := range count {
		days[i].Judgements[item.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
	}
}

// 환산 일수와 항목 점수의 표를 한 줄씩 옮겨 적는다.
func TestItemPoints(t *testing.T) {
	tests := []struct {
		name      string
		converted int
		want      int
	}{
		{"환산 0일은 0점이다(전혀 없음)", 0, 0},
		{"환산 1일은 1점이다(며칠 동안의 첫 값)", 1, 1},
		{"환산 2일은 1점이다", 2, 1},
		{"환산 3일은 1점이다", 3, 1},
		{"환산 4일은 1점이다", 4, 1},
		{"환산 5일은 1점이다", 5, 1},
		{"환산 6일은 1점이다(며칠 동안의 끝 값)", 6, 1},
		{"환산 7일은 2점이다(일주일 이상의 첫 값)", 7, 2},
		{"환산 8일은 2점이다", 8, 2},
		{"환산 9일은 2점이다", 9, 2},
		{"환산 10일은 2점이다", 10, 2},
		{"환산 11일은 2점이다(일주일 이상의 끝 값)", 11, 2},
		{"환산 12일은 3점이다(거의 매일의 첫 값)", 12, 3},
		{"환산 13일은 3점이다", 13, 3},
		{"환산 14일은 3점이다(거의 매일의 끝 값)", 14, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, itemPoints(tt.converted, params.Default().Score))
		})
	}

	t.Run("경계는 조정 값에서 온다", func(t *testing.T) {
		custom := params.Score{ItemScore1MinDays: 2, ItemScore2MinDays: 5, ItemScore3MinDays: 9}
		want := map[int]int{0: 0, 1: 0, 2: 1, 4: 1, 5: 2, 8: 2, 9: 3, 14: 3}
		for converted, points := range want {
			assert.Equal(t, points, itemPoints(converted, custom), "환산 %d일", converted)
		}
	})
}

// 추정 점수와 구간의 표를 한 줄씩 옮겨 적는다.
func TestBandFor(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  Band
	}{
		{"0점은 최소다", 0, Minimal},
		{"4점은 최소다", 4, Minimal},
		{"5점은 가벼움이다", 5, Mild},
		{"9점은 가벼움이다", 9, Mild},
		{"10점은 중간이다", 10, Moderate},
		{"14점은 중간이다", 14, Moderate},
		{"15점은 다소 심함이다", 15, ModeratelySevere},
		{"19점은 다소 심함이다", 19, ModeratelySevere},
		{"20점은 심함이다", 20, Severe},
		{"24점은 심함이다", 24, Severe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bandFor(tt.total, params.Default().Score))
		})
	}

	t.Run("0점부터 24점까지 빠진 점수 없이 다섯 구간 가운데 하나에 든다", func(t *testing.T) {
		want := []Band{
			Minimal, Minimal, Minimal, Minimal, Minimal,
			Mild, Mild, Mild, Mild, Mild,
			Moderate, Moderate, Moderate, Moderate, Moderate,
			ModeratelySevere, ModeratelySevere, ModeratelySevere, ModeratelySevere, ModeratelySevere,
			Severe, Severe, Severe, Severe, Severe,
		}
		require.Len(t, want, MaxTotal+1)
		for total, band := range want {
			assert.Equal(t, band, bandFor(total, params.Default().Score), "%d점", total)
		}
	})

	t.Run("경계는 조정 값에서 온다", func(t *testing.T) {
		custom := params.Score{MildMin: 3, ModerateMin: 6, ModeratelySevereMin: 9, SevereMin: 12}
		want := map[int]Band{2: Minimal, 3: Mild, 5: Mild, 6: Moderate, 9: ModeratelySevere, 11: ModeratelySevere, 12: Severe}
		for total, band := range want {
			assert.Equal(t, band, bandFor(total, custom), "%d점", total)
		}
	})
}

func TestConvertedDays(t *testing.T) {
	const windowDays = 14

	// 14 × o ÷ n이 정확히 절반에 걸리는 경우다. 0.5는 올린다.
	halves := []struct {
		name             string
		observed         int
		conversationDays int
		want             int
	}{
		{"대화 8일 중 2일 관찰은 3.5일이고 4일로 올린다", 2, 8, 4},
		{"대화 8일 중 6일 관찰은 10.5일이고 11일로 올린다", 6, 8, 11},
		{"대화 12일 중 3일 관찰은 3.5일이고 4일로 올린다", 3, 12, 4},
		{"대화 12일 중 9일 관찰은 10.5일이고 11일로 올린다", 9, 12, 11},
	}
	for _, tt := range halves {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, convertedDays(tt.observed, tt.conversationDays, windowDays))
		})
	}

	t.Run("대화 7일부터 14일 사이에서 정확히 절반에 걸리는 경우는 위의 넷뿐이다", func(t *testing.T) {
		type pair struct{ conversationDays, observed int }
		var found []pair
		for n := 7; n <= windowDays; n++ {
			for o := 0; o <= n; o++ {
				// 14 × o ÷ n의 소수 부분이 정확히 1/2이면 28 × o ÷ n은 홀수인 정수다.
				if (28*o)%n == 0 && (28*o/n)%2 == 1 {
					found = append(found, pair{n, o})
				}
			}
		}
		assert.Equal(t, []pair{{8, 2}, {8, 6}, {12, 3}, {12, 9}}, found)
	})

	// 절반 가까이에서 어느 쪽으로 떨어지는지 본다. 점수 경계(7일, 12일) 바로 옆의 값을 골랐다.
	nearHalves := []struct {
		name             string
		observed         int
		conversationDays int
		want             int
	}{
		{"대화 11일 중 5일 관찰은 6.36일이고 6일로 내린다(1점에 머문다)", 5, 11, 6},
		{"대화 13일 중 6일 관찰은 6.46일이고 6일로 내린다(1점에 머문다)", 6, 13, 6},
		{"대화 13일 중 7일 관찰은 7.54일이고 8일로 올린다", 7, 13, 8},
		{"대화 9일 중 4일 관찰은 6.22일이고 6일로 내린다", 4, 9, 6},
		{"대화 10일 중 4일 관찰은 5.6일이고 6일로 올린다", 4, 10, 6},
		{"대화 11일 중 9일 관찰은 11.45일이고 11일로 내린다(2점에 머문다)", 9, 11, 11},
		{"대화 9일 중 8일 관찰은 12.44일이고 12일로 내린다", 8, 9, 12},
		{"대화 13일 중 11일 관찰은 11.85일이고 12일로 올린다(3점이 된다)", 11, 13, 12},
		{"대화 10일 중 9일 관찰은 12.6일이고 13일로 올린다", 9, 10, 13},
	}
	for _, tt := range nearHalves {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, convertedDays(tt.observed, tt.conversationDays, windowDays))
		})
	}

	t.Run("대화한 날이 0이어도 멈추지 않고 0을 돌려준다", func(t *testing.T) {
		assert.Equal(t, 0, convertedDays(0, 0, windowDays))
		assert.Equal(t, 0, convertedDays(3, 0, windowDays))
	})

	t.Run("창의 길이가 달라도 같은 식으로 환산한다", func(t *testing.T) {
		// 10 × 1 ÷ 4 = 2.5 → 3, 10 × 3 ÷ 4 = 7.5 → 8, 7 × 2 ÷ 4 = 3.5 → 4
		assert.Equal(t, 3, convertedDays(1, 4, 10))
		assert.Equal(t, 8, convertedDays(3, 4, 10))
		assert.Equal(t, 4, convertedDays(2, 4, 7))
	})
}

// 환산 일수가 지켜야 하는 성질을 가능한 모든 입력에서 확인한다.
func TestConvertedDaysProperties(t *testing.T) {
	for windowDays := 1; windowDays <= 30; windowDays++ {
		for n := 1; n <= windowDays; n++ {
			previous := 0
			for o := 0; o <= n; o++ {
				got := convertedDays(o, n, windowDays)
				where := fmt.Sprintf("창 %d일, 대화 %d일, 관찰 %d일", windowDays, n, o)

				if o == 0 {
					assert.Equal(t, 0, got, "관찰된 적 없는 항목은 0일이다: %s", where)
				} else {
					assert.GreaterOrEqual(t, got, 1, "한 번이라도 관찰된 항목은 0일이 되지 않는다: %s", where)
				}
				if o == n {
					assert.Equal(t, windowDays, got, "대화한 날마다 관찰됐으면 창 전체다: %s", where)
				}
				assert.LessOrEqual(t, got, windowDays, "창의 길이를 넘지 않는다: %s", where)
				assert.GreaterOrEqual(t, got, previous, "관찰된 날이 늘면 환산 일수가 줄지 않는다: %s", where)
				// 반올림한 값이므로 참값과 0.5 넘게 벌어지지 않는다: |got − windowDays×o÷n| ≤ 1/2
				diff := 2*got*n - 2*windowDays*o
				assert.LessOrEqual(t, diff, n, "참값보다 0.5 넘게 크지 않다: %s", where)
				assert.Greater(t, diff, -n, "참값보다 0.5 이상 작지 않다: %s", where)
				previous = got
			}
		}
	}
}

// 대화한 일수(7~14)와 관찰된 일수의 모든 조합을 손으로 구한 값과 맞춰 본다.
func TestComputeConversionTable(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	// 자리는 관찰된 일수 o다. 값은 14 × o ÷ n을 0.5에서 올려 반올림한 것이다.
	wantConverted := map[int][]int{
		7:  {0, 2, 4, 6, 8, 10, 12, 14},
		8:  {0, 2, 4, 5, 7, 9, 11, 12, 14},
		9:  {0, 2, 3, 5, 6, 8, 9, 11, 12, 14},
		10: {0, 1, 3, 4, 6, 7, 8, 10, 11, 13, 14},
		11: {0, 1, 3, 4, 5, 6, 8, 9, 10, 11, 13, 14},
		12: {0, 1, 2, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14},
		13: {0, 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 12, 13, 14},
		14: {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14},
	}
	// 자리는 환산 일수다.
	wantPoints := [15]int{0, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3, 3}

	for n := 7; n <= 14; n++ {
		require.Len(t, wantConverted[n], n+1)
		for o := 0; o <= n; o++ {
			converted := wantConverted[n][o]
			points := wantPoints[converted]
			name := fmt.Sprintf("대화 %d일 중 %d일 관찰이면 환산 %d일, %d점이다", n, o, converted, points)
			t.Run(name, func(t *testing.T) {
				days := lastDays(t, asOf, n)
				observe(days, signal.Sleep, o)

				got, err := Compute(days, asOf, params.Default())
				require.NoError(t, err)

				assert.False(t, got.Insufficient)
				assert.Equal(t, n, got.ConversationDays)
				assert.Equal(t, ItemResult{
					Item:          signal.Sleep,
					ObservedDays:  o,
					ConvertedDays: converted,
					Points:        points,
				}, got.Item(signal.Sleep))
				assert.Equal(t, points, got.Total)

				for _, item := range signal.AllItems() {
					if item == signal.Sleep {
						continue
					}
					assert.Equal(t, ItemResult{Item: item}, got.Item(item))
				}
			})
		}
	}
}

func TestComputeInsufficient(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	t.Run("대화한 날이 6일이면 기록 부족이고 점수를 내지 않는다", func(t *testing.T) {
		days := lastDays(t, asOf, 6)
		for _, item := range signal.AllItems() {
			observe(days, item, 6)
		}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.True(t, got.Insufficient)
		assert.Equal(t, 6, got.ConversationDays)
		assert.Equal(t, 0, got.Total)
		assert.Equal(t, NoBand, got.Band)
		for _, item := range signal.AllItems() {
			// 관찰된 일수는 센 그대로 남기고, 환산 일수와 항목 점수는 구하지 않는다.
			assert.Equal(t, ItemResult{Item: item, ObservedDays: 6}, got.Item(item))
		}

		total, ok := got.Score()
		assert.False(t, ok)
		assert.Equal(t, 0, total)
	})

	t.Run("대화한 날이 7일이면 점수를 낸다", func(t *testing.T) {
		days := lastDays(t, asOf, 7)
		for _, item := range signal.AllItems() {
			observe(days, item, 7)
		}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.False(t, got.Insufficient)
		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, 24, got.Total)
		assert.Equal(t, Severe, got.Band)

		total, ok := got.Score()
		assert.True(t, ok)
		assert.Equal(t, 24, total)
	})

	t.Run("기록 부족은 0점과 다른 상태다", func(t *testing.T) {
		insufficient, err := Compute(lastDays(t, asOf, 6), asOf, params.Default())
		require.NoError(t, err)
		zero, err := Compute(lastDays(t, asOf, 7), asOf, params.Default())
		require.NoError(t, err)

		assert.True(t, insufficient.Insufficient)
		assert.Equal(t, NoBand, insufficient.Band)

		assert.False(t, zero.Insufficient)
		assert.Equal(t, 0, zero.Total)
		assert.Equal(t, Minimal, zero.Band)
	})

	for n := 0; n <= 6; n++ {
		t.Run(fmt.Sprintf("대화한 날이 %d일이면 기록 부족이다", n), func(t *testing.T) {
			got, err := Compute(lastDays(t, asOf, n), asOf, params.Default())
			require.NoError(t, err)
			assert.True(t, got.Insufficient)
			assert.Equal(t, n, got.ConversationDays)
		})
	}

	t.Run("기록이 하나도 없어도 오류가 아니라 기록 부족이다", func(t *testing.T) {
		got, err := Compute(nil, asOf, params.Default())
		require.NoError(t, err)
		assert.True(t, got.Insufficient)
		assert.Equal(t, 0, got.ConversationDays)
		assert.Equal(t, Window{From: mustDate(t, "2026-10-07"), To: asOf}, got.Window)
	})

	t.Run("필요한 대화 일수는 조정 값에서 온다", func(t *testing.T) {
		p := params.Default()
		p.Window.MinConversationDays = 3

		two, err := Compute(lastDays(t, asOf, 2), asOf, p)
		require.NoError(t, err)
		assert.True(t, two.Insufficient)

		three, err := Compute(lastDays(t, asOf, 3), asOf, p)
		require.NoError(t, err)
		assert.False(t, three.Insufficient)
	})
}

func TestComputeWindow(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	t.Run("창은 기준일과 그 앞의 13일이다", func(t *testing.T) {
		got, err := Compute(nil, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, mustDate(t, "2026-10-07"), got.Window.From)
		assert.Equal(t, asOf, got.Window.To)
		assert.Equal(t, 14, got.Window.Length())
	})

	t.Run("기준일에서 13일 앞은 창 안이다", func(t *testing.T) {
		// 창 안의 6일에 13일 앞의 하루를 더하면 7일이 되어 점수가 나온다.
		days := daysBefore(t, asOf, 13, 5, 4, 3, 2, 1, 0)
		observe(days, signal.Mood, 1) // 13일 앞의 하루에 적힌다

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.False(t, got.Insufficient)
		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, 1, got.Item(signal.Mood).ObservedDays)
		assert.Equal(t, 2, got.Item(signal.Mood).ConvertedDays)
	})

	t.Run("기준일에서 14일 앞은 창 밖이다", func(t *testing.T) {
		// 같은 기록에서 가장 앞의 하루만 하루 더 앞으로 옮기면 창 안은 6일뿐이라 기록 부족이다.
		days := daysBefore(t, asOf, 14, 5, 4, 3, 2, 1, 0)
		observe(days, signal.Mood, 1) // 14일 앞의 하루에 적힌다

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.True(t, got.Insufficient)
		assert.Equal(t, 6, got.ConversationDays)
		assert.Equal(t, 0, got.Item(signal.Mood).ObservedDays)
	})

	t.Run("기준일 당일의 기록이 들어간다", func(t *testing.T) {
		days := lastDays(t, asOf, 7)
		days[len(days)-1].Judgements[signal.Fatigue.Index()] = signal.Judgement{
			Status: signal.Observed, Explicitness: signal.Direct,
		}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, 1, got.Item(signal.Fatigue).ObservedDays)
	})

	t.Run("기준일 뒤의 기록은 보지 않는다", func(t *testing.T) {
		// 기준일까지 7일, 그 뒤로 3일. 뒤의 3일은 여덟 항목이 모두 관찰됨이다.
		days := daysBefore(t, asOf, 6, 5, 4, 3, 2, 1, 0, -1, -2, -3)
		for i := 7; i < len(days); i++ {
			for _, item := range signal.AllItems() {
				days[i].Judgements[item.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			}
		}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, 0, got.Total)
		for _, item := range signal.AllItems() {
			assert.Equal(t, 0, got.Item(item).ObservedDays)
		}
	})

	t.Run("창보다 앞선 기록은 대화한 일수에도 관찰된 일수에도 들지 않는다", func(t *testing.T) {
		// 창 안에 8일, 창 앞에 5일. 창 앞의 5일에만 수면이 관찰됐다.
		days := daysBefore(t, asOf, 40, 30, 20, 15, 14, 13, 10, 8, 6, 4, 2, 1, 0)
		observe(days, signal.Sleep, 5)

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.Equal(t, 8, got.ConversationDays)
		assert.Equal(t, 0, got.Item(signal.Sleep).ObservedDays)
		assert.Equal(t, 0, got.Total)
	})

	t.Run("지난 날짜를 기준일로 넘기면 그날의 점수가 나온다", func(t *testing.T) {
		// 10월 1일부터 20일까지 매일 대화했고, 앞의 10일에만 수면이 관찰됐다.
		days := lastDays(t, asOf, 20)
		observe(days, signal.Sleep, 10)

		// 10월 10일 기준: 창은 9월 27일~10월 10일, 그 안의 대화는 10일이고 모두 관찰됨이다.
		early, err := Compute(days, mustDate(t, "2026-10-10"), params.Default())
		require.NoError(t, err)
		assert.Equal(t, 10, early.ConversationDays)
		assert.Equal(t, 10, early.Item(signal.Sleep).ObservedDays)
		assert.Equal(t, 14, early.Item(signal.Sleep).ConvertedDays)
		assert.Equal(t, 3, early.Total)

		// 10월 20일 기준: 창은 10월 7일~20일, 14일 가운데 관찰된 날은 7일~10일의 4일이다.
		late, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, 14, late.ConversationDays)
		assert.Equal(t, 4, late.Item(signal.Sleep).ObservedDays)
		assert.Equal(t, 4, late.Item(signal.Sleep).ConvertedDays)
		assert.Equal(t, 1, late.Total)
	})

	t.Run("창 안에서 날이 어디에 놓였는지는 결과를 바꾸지 않는다", func(t *testing.T) {
		packed := lastDays(t, asOf, 8)
		spread := daysBefore(t, asOf, 13, 11, 9, 7, 6, 4, 2, 0)
		observe(packed, signal.Appetite, 3)
		observe(spread, signal.Appetite, 3)

		a, err := Compute(packed, asOf, params.Default())
		require.NoError(t, err)
		b, err := Compute(spread, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, a, b)
	})
}

func TestComputeTotals(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	for n := 7; n <= 14; n++ {
		t.Run(fmt.Sprintf("대화 %d일 내내 여덟 항목이 모두 관찰되면 24점이다", n), func(t *testing.T) {
			days := lastDays(t, asOf, n)
			for _, item := range signal.AllItems() {
				observe(days, item, n)
			}

			got, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)

			assert.Equal(t, MaxTotal, got.Total)
			assert.Equal(t, Severe, got.Band)
			for _, item := range signal.AllItems() {
				assert.Equal(t, ItemResult{Item: item, ObservedDays: n, ConvertedDays: 14, Points: 3}, got.Item(item))
			}
		})

		t.Run(fmt.Sprintf("대화 %d일 동안 관찰된 것이 없으면 0점이다", n), func(t *testing.T) {
			got, err := Compute(lastDays(t, asOf, n), asOf, params.Default())
			require.NoError(t, err)

			assert.False(t, got.Insufficient)
			assert.Equal(t, 0, got.Total)
			assert.Equal(t, Minimal, got.Band)
		})
	}

	// 14일 내내 대화한 기록이다. 관찰된 일수가 곧 환산 일수라서 항목 점수를 바로 읽을 수 있다.
	// 자리는 signal.AllItems의 순서다.
	bands := []struct {
		name      string
		observed  [signal.ItemCount]int
		wantTotal int
		wantBand  Band
	}{
		{"네 항목이 하루씩이면 4점, 최소다", [signal.ItemCount]int{1, 1, 1, 1, 0, 0, 0, 0}, 4, Minimal},
		{"다섯 항목이 하루씩이면 5점, 가벼움이다", [signal.ItemCount]int{1, 1, 1, 1, 1, 0, 0, 0}, 5, Mild},
		{"세 항목이 거의 매일이면 9점, 가벼움이다", [signal.ItemCount]int{12, 13, 14, 0, 0, 0, 0, 0}, 9, Mild},
		{"세 항목이 거의 매일이고 한 항목이 며칠이면 10점, 중간이다", [signal.ItemCount]int{12, 13, 14, 6, 0, 0, 0, 0}, 10, Moderate},
		{"네 항목이 거의 매일이고 한 항목이 일주일 이상이면 14점, 중간이다", [signal.ItemCount]int{12, 12, 12, 12, 7, 0, 0, 0}, 14, Moderate},
		{"다섯 항목이 거의 매일이면 15점, 다소 심함이다", [signal.ItemCount]int{14, 14, 14, 14, 14, 0, 0, 0}, 15, ModeratelySevere},
		{"여섯 항목이 거의 매일이고 한 항목이 며칠이면 19점, 다소 심함이다", [signal.ItemCount]int{14, 14, 14, 14, 14, 14, 1, 0}, 19, ModeratelySevere},
		{"여섯 항목이 거의 매일이고 한 항목이 일주일 이상이면 20점, 심함이다", [signal.ItemCount]int{14, 14, 14, 14, 14, 14, 11, 0}, 20, Severe},
		{"항목마다 점수가 다르면 그대로 더한다", [signal.ItemCount]int{0, 1, 6, 7, 11, 12, 14, 0}, 0 + 1 + 1 + 2 + 2 + 3 + 3 + 0, Moderate},
	}
	for _, tt := range bands {
		t.Run(tt.name, func(t *testing.T) {
			days := lastDays(t, asOf, 14)
			for _, item := range signal.AllItems() {
				observe(days, item, tt.observed[item.Index()])
			}

			got, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)

			assert.Equal(t, tt.wantTotal, got.Total)
			assert.Equal(t, tt.wantBand, got.Band)

			sum := 0
			for _, item := range got.Items {
				sum += item.Points
			}
			assert.Equal(t, sum, got.Total, "추정 점수는 항목 점수의 합이다")
		})
	}
}

func TestComputeCountsOnlyObserved(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	t.Run("관찰되지 않음과 언급 없음은 관찰된 일수에 들지 않지만 대화한 날에는 든다", func(t *testing.T) {
		days := lastDays(t, asOf, 10)
		for i := range days {
			days[i].Judgements[signal.Sleep.Index()] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
			days[i].Judgements[signal.Mood.Index()] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect}
		}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.Equal(t, 10, got.ConversationDays)
		assert.Equal(t, 0, got.Total)
		for _, item := range signal.AllItems() {
			assert.Equal(t, 0, got.Item(item).ObservedDays)
		}
	})

	t.Run("간접 추론으로 관찰된 날도 직접 언급과 똑같이 하루로 센다", func(t *testing.T) {
		days := lastDays(t, asOf, 10)
		for i := range 5 {
			days[i].Judgements[signal.Concentration.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
		}
		days[5].Judgements[signal.Concentration.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		// 14 × 6 ÷ 10 = 8.4 → 8일, 2점
		assert.Equal(t, ItemResult{Item: signal.Concentration, ObservedDays: 6, ConvertedDays: 8, Points: 2}, got.Item(signal.Concentration))
	})

	t.Run("대화하지 않은 날을 신호 없음으로 세지 않는다", func(t *testing.T) {
		// 7일만 대화했고 그 7일 내내 수면이 관찰됐다. 14일로 나누면 7일(2점)이지만, 대화한 날로 나누므로 14일(3점)이다.
		days := daysBefore(t, asOf, 12, 10, 8, 6, 4, 2, 0)
		observe(days, signal.Sleep, 7)

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, ItemResult{Item: signal.Sleep, ObservedDays: 7, ConvertedDays: 14, Points: 3}, got.Item(signal.Sleep))
	})

	t.Run("하루로 합친 기록에서 바로 계산한다", func(t *testing.T) {
		// 하루에 대화를 두 번 했고 한쪽에서만 관찰된 날도 관찰된 하루다. 취소한 신호는 세지 않는다.
		rowsByDate := map[recorddate.Date][]signal.Row{}
		for offset := range 7 {
			rowsByDate[asOf.AddDays(-offset)] = []signal.Row{
				{ConversationID: "morning", Item: signal.Sleep, Status: signal.NotObserved, Explicitness: signal.Direct},
				{ConversationID: "night", Item: signal.Sleep, Status: signal.Observed, Explicitness: signal.Direct},
				{ConversationID: "night", Item: signal.Mood, Status: signal.Observed, Explicitness: signal.Indirect, Cancelled: true},
			}
		}
		days, err := signal.MergeDays(rowsByDate)
		require.NoError(t, err)

		got, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		assert.Equal(t, 7, got.ConversationDays)
		assert.Equal(t, 7, got.Item(signal.Sleep).ObservedDays)
		assert.Equal(t, 0, got.Item(signal.Mood).ObservedDays)
		assert.Equal(t, 3, got.Total)
	})
}

func TestComputeObservedItemNeverScoresZero(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	for n := 7; n <= 14; n++ {
		for o := 1; o <= n; o++ {
			days := lastDays(t, asOf, n)
			observe(days, signal.Psychomotor, o)

			got, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)

			item := got.Item(signal.Psychomotor)
			assert.GreaterOrEqual(t, item.ConvertedDays, 1, "대화 %d일 중 %d일 관찰", n, o)
			assert.GreaterOrEqual(t, item.Points, 1, "대화 %d일 중 %d일 관찰", n, o)
			assert.GreaterOrEqual(t, got.Total, 1, "대화 %d일 중 %d일 관찰", n, o)
		}
	}
}

// sampleDays는 창 앞, 창 안, 기준일 뒤에 걸친 기록이다. 항목마다 관찰된 날이 다르다.
func sampleDays(t *testing.T, asOf recorddate.Date) []signal.Day {
	t.Helper()
	days := daysBefore(t, asOf, 21, 16, 13, 12, 10, 9, 7, 6, 4, 3, 1, 0, -2)
	for i := range days {
		for _, item := range signal.AllItems() {
			// 날과 항목의 자리로 정한 고정된 무늬다.
			switch (i + 2*item.Index()) % 4 {
			case 0:
				days[i].Judgements[item.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
			case 1:
				days[i].Judgements[item.Index()] = signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect}
			case 2:
				days[i].Judgements[item.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
			}
		}
	}
	return days
}

func TestComputeIsDeterministic(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	days := sampleDays(t, asOf)
	before := slices.Clone(days)

	first, err := Compute(days, asOf, params.Default())
	require.NoError(t, err)
	require.False(t, first.Insufficient)
	require.Positive(t, first.Total)

	t.Run("같은 기록에서는 몇 번을 돌려도 같은 값이다", func(t *testing.T) {
		for range 20 {
			again, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)
			assert.Equal(t, first, again)
		}
	})

	t.Run("받은 기록을 고치지 않는다", func(t *testing.T) {
		assert.Equal(t, before, days)
	})

	t.Run("똑같이 만든 다른 슬라이스에서도 같은 값이다", func(t *testing.T) {
		again, err := Compute(sampleDays(t, asOf), asOf, params.Default())
		require.NoError(t, err)
		assert.Equal(t, first, again)
	})
}

func TestComputeInputOrder(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	days := sampleDays(t, asOf)

	want, err := Compute(days, asOf, params.Default())
	require.NoError(t, err)

	reversed := slices.Clone(days)
	slices.Reverse(reversed)

	rotated := slices.Concat(days[5:], days[:5])

	// 짝수 자리를 먼저, 홀수 자리를 거꾸로 뒤에 붙인다.
	var interleaved []signal.Day
	for i := 0; i < len(days); i += 2 {
		interleaved = append(interleaved, days[i])
	}
	for i := len(days) - 1; i >= 0; i-- {
		if i%2 == 1 {
			interleaved = append(interleaved, days[i])
		}
	}

	shuffles := []struct {
		name string
		days []signal.Day
	}{
		{"거꾸로 받은 기록도 정렬을 거치면 같은 값이다", reversed},
		{"돌려 놓은 기록도 정렬을 거치면 같은 값이다", rotated},
		{"뒤섞인 기록도 정렬을 거치면 같은 값이다", interleaved},
	}
	for _, tt := range shuffles {
		t.Run(tt.name, func(t *testing.T) {
			require.Len(t, tt.days, len(days))

			sorted, err := signal.SortDays(tt.days)
			require.NoError(t, err)

			got, err := Compute(sorted, asOf, params.Default())
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})

		t.Run(tt.name+" (정렬 없이 넘기면 오류다)", func(t *testing.T) {
			_, err := Compute(tt.days, asOf, params.Default())
			require.ErrorIs(t, err, signal.ErrUnsortedDays)
		})
	}
}

func TestComputeCustomParams(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")

	p := params.Default()
	p.Window.Days = 10
	p.Window.MinConversationDays = 4
	p.Score.ItemScore1MinDays = 1
	p.Score.ItemScore2MinDays = 3
	p.Score.ItemScore3MinDays = 8
	p.Score.MildMin = 2
	p.Score.ModerateMin = 3
	p.Score.ModeratelySevereMin = 4
	p.Score.SevereMin = 5
	require.NoError(t, p.Validate())

	t.Run("창의 길이가 환산의 기준 일수다", func(t *testing.T) {
		days := daysBefore(t, asOf, 9, 6, 3, 0)
		observe(days, signal.Interest, 1) // 10 × 1 ÷ 4 = 2.5 → 3일, 경계가 3일이라 2점
		observe(days, signal.Mood, 3)     // 10 × 3 ÷ 4 = 7.5 → 8일, 경계가 8일이라 3점

		got, err := Compute(days, asOf, p)
		require.NoError(t, err)

		assert.Equal(t, Window{From: mustDate(t, "2026-10-11"), To: asOf}, got.Window)
		assert.Equal(t, 10, got.Window.Length())
		assert.Equal(t, 4, got.ConversationDays)
		assert.Equal(t, ItemResult{Item: signal.Interest, ObservedDays: 1, ConvertedDays: 3, Points: 2}, got.Item(signal.Interest))
		assert.Equal(t, ItemResult{Item: signal.Mood, ObservedDays: 3, ConvertedDays: 8, Points: 3}, got.Item(signal.Mood))
		assert.Equal(t, 5, got.Total)
		assert.Equal(t, Severe, got.Band)
	})

	t.Run("창의 길이가 바뀌면 창의 첫날도 바뀐다", func(t *testing.T) {
		// 기본 창이라면 들어갔을 10일 앞의 하루가 빠져서 대화한 날이 3일뿐이다.
		got, err := Compute(daysBefore(t, asOf, 10, 6, 3, 0), asOf, p)
		require.NoError(t, err)
		assert.True(t, got.Insufficient)
		assert.Equal(t, 3, got.ConversationDays)
	})
}

func TestComputeErrors(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	observedWithoutEvidence := signal.Judgement{Status: signal.Observed, Explicitness: signal.None}

	t.Run("기준일이 비어 있으면 오류다", func(t *testing.T) {
		_, err := Compute(lastDays(t, asOf, 7), recorddate.Date{}, params.Default())
		require.ErrorIs(t, err, ErrInvalidWindow)
	})

	t.Run("창의 첫날이 다룰 수 있는 날짜보다 앞이면 오류다", func(t *testing.T) {
		_, err := Compute(nil, mustDate(t, "0001-01-05"), params.Default())
		require.ErrorIs(t, err, ErrInvalidWindow)
	})

	t.Run("다룰 수 있는 가장 앞의 창은 받는다", func(t *testing.T) {
		got, err := Compute(nil, mustDate(t, "0001-01-14"), params.Default())
		require.NoError(t, err)
		assert.Equal(t, mustDate(t, "0001-01-01"), got.Window.From)
	})

	t.Run("조정 값이 틀리면 어느 값인지 알려준다", func(t *testing.T) {
		p := params.Default()
		p.Window.MinConversationDays = 0

		_, err := Compute(lastDays(t, asOf, 7), asOf, p)
		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Window.MinConversationDays", fieldErr.Field)
	})

	t.Run("항목 점수의 경계가 창보다 길면 오류다", func(t *testing.T) {
		p := params.Default()
		p.Window.Days = 10

		_, err := Compute(lastDays(t, asOf, 7), asOf, p)
		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Score.ItemScore3MinDays", fieldErr.Field)
	})

	t.Run("같은 날짜가 둘이면 오류다", func(t *testing.T) {
		days := lastDays(t, asOf, 7)
		days = slices.Insert(days, 3, days[2])

		_, err := Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrDuplicateDate)

		var dayErr *signal.DayError
		require.ErrorAs(t, err, &dayErr)
		assert.Equal(t, 3, dayErr.Index)
	})

	t.Run("날짜가 빠진 하루가 있으면 오류다", func(t *testing.T) {
		days := append([]signal.Day{{}}, lastDays(t, asOf, 7)...)

		_, err := Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrZeroDate)
	})

	t.Run("근거 없는 관찰됨이 있으면 오류다", func(t *testing.T) {
		days := lastDays(t, asOf, 7)
		days[4].Judgements[signal.Sleep.Index()] = observedWithoutEvidence

		_, err := Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})

	t.Run("창 밖의 하루가 틀려도 오류다", func(t *testing.T) {
		// 어느 기준일로 돌리느냐에 따라 같은 기록이 받아들여졌다 거부됐다 하지 않게 한다.
		days := daysBefore(t, asOf, 30, 6, 5, 4, 3, 2, 1, 0, -5)
		days[0].Judgements[signal.Sleep.Index()] = observedWithoutEvidence

		_, err := Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)

		days[0].Judgements[signal.Sleep.Index()] = signal.Judgement{}
		days[len(days)-1].Judgements[signal.Sleep.Index()] = observedWithoutEvidence

		_, err = Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})

	t.Run("오류일 때는 빈 결과를 돌려준다", func(t *testing.T) {
		got, err := Compute(lastDays(t, asOf, 7), recorddate.Date{}, params.Default())
		require.Error(t, err)
		assert.Equal(t, Result{}, got)
	})

	t.Run("어느 계산에서 난 오류인지 메시지 앞에 적는다", func(t *testing.T) {
		days := lastDays(t, asOf, 7)
		slices.Reverse(days)

		_, err := Compute(days, asOf, params.Default())
		require.ErrorIs(t, err, signal.ErrUnsortedDays)
		assert.Contains(t, err.Error(), "score: ")
	})
}

func TestResultItem(t *testing.T) {
	asOf := mustDate(t, "2026-10-20")
	days := lastDays(t, asOf, 7)
	observe(days, signal.SelfBlame, 2)

	got, err := Compute(days, asOf, params.Default())
	require.NoError(t, err)

	t.Run("항목별 결과의 자리는 항목의 순서와 같다", func(t *testing.T) {
		for idx, item := range signal.AllItems() {
			assert.Equal(t, item, got.Items[idx].Item)
			assert.Equal(t, got.Items[idx], got.Item(item))
		}
	})

	t.Run("항목이 아닌 값에는 빈 결과를 돌려준다", func(t *testing.T) {
		assert.Equal(t, ItemResult{}, got.Item(signal.Item(0)))
		assert.Equal(t, ItemResult{}, got.Item(signal.Item(9)))
	})
}
