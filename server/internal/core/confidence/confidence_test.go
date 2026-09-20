package confidence

import (
	"encoding/json"
	"math"
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

func observedDirect() signal.Judgement {
	return signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct}
}

func observedIndirect() signal.Judgement {
	return signal.Judgement{Status: signal.Observed, Explicitness: signal.Indirect}
}

func notObservedDirect() signal.Judgement {
	return signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Direct}
}

func notObservedIndirect() signal.Judgement {
	return signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect}
}

// dayAgo는 기준일에서 ago일 전의 하루를 만든다. 적어 준 항목만 채우고 나머지는 언급 없음이다.
// ago가 음수면 기준일 뒤의 날이다.
func dayAgo(asOf recorddate.Date, ago int, judgements map[signal.Item]signal.Judgement) signal.Day {
	day := signal.Day{Date: asOf.AddDays(-ago)}
	for item, j := range judgements {
		day.Judgements[item.Index()] = j
	}
	return day
}

// everyItem은 여덟 항목 모두에 같은 판단을 적는다.
func everyItem(j signal.Judgement) map[signal.Item]signal.Judgement {
	judgements := make(map[signal.Item]signal.Judgement, signal.ItemCount)
	for _, item := range signal.AllItems() {
		judgements[item] = j
	}
	return judgements
}

// sorted는 하루들을 날짜순으로 늘어놓는다. 시험에서는 읽기 좋은 순서로 적고 입력은 규칙대로 넘긴다.
func sorted(t *testing.T, days ...signal.Day) []signal.Day {
	t.Helper()
	result, err := signal.SortDays(days)
	require.NoError(t, err)
	return result
}

// scenario는 세 요소의 값을 하나씩 정해서 기록을 만든다.
type scenario struct {
	// days는 기준일에서 거슬러 끊김 없이 이어지는 대화한 일수다.
	days int
	// items는 이야기가 나오는 항목 수다. 정해진 순서의 앞에서부터 고르고, 고른 항목은 날마다 나온다.
	items int
	// direct와 indirect는 관찰됨 판단 가운데 직접 언급과 간접 추론의 수다.
	// 남는 자리는 "관찰되지 않음, 직접 언급"으로 채운다.
	direct   int
	indirect int
}

func (sc scenario) build(t *testing.T, asOf recorddate.Date) []signal.Day {
	t.Helper()
	require.LessOrEqual(t, sc.direct+sc.indirect, sc.days*sc.items, "관찰됨 판단을 넣을 자리가 모자란다")

	items := signal.AllItems()
	slot := 0
	days := make([]signal.Day, 0, sc.days)
	for ago := sc.days - 1; ago >= 0; ago-- {
		day := signal.Day{Date: asOf.AddDays(-ago)}
		for _, item := range items[:sc.items] {
			switch {
			case slot < sc.direct:
				day.Judgements[item.Index()] = observedDirect()
			case slot < sc.direct+sc.indirect:
				day.Judgements[item.Index()] = observedIndirect()
			default:
				day.Judgements[item.Index()] = notObservedDirect()
			}
			slot++
		}
		days = append(days, day)
	}
	return days
}

func TestCompute_RecordCoverage(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")
	allFine := everyItem(notObservedDirect())

	tests := []struct {
		name string
		// agos는 기준일에서 며칠 전에 대화했는지다. 음수는 기준일 뒤다.
		agos     []int
		wantDays int
	}{
		{"대화한 날이 없으면 14분의 0이다", nil, 0},
		{"기준일 하루만 대화했으면 14분의 1이다", []int{0}, 1},
		{"기준일에서 13일 전은 창의 첫날이라 센다", []int{13}, 1},
		{"기준일에서 14일 전은 창 밖이라 세지 않는다", []int{14}, 0},
		{"기준일 뒤의 하루는 세지 않는다", []int{-1}, 0},
		{"띄엄띄엄 대화해도 대화한 일수만 센다", []int{12, 10, 8, 6, 4, 2, 0}, 7},
		{"창 안팎에 걸친 기록은 창 안의 날만 센다", []int{40, 20, 14, 13, 7, 0, -1, -3}, 3},
		{"14일 내내 대화했으면 14분의 14다", []int{13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var days []signal.Day
			for _, ago := range tt.agos {
				days = append(days, dayAgo(asOf, ago, allFine))
			}

			got, err := Compute(sorted(t, days...), asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, asOf, got.AsOf)
			assert.Equal(t, tt.wantDays, got.ConversationDays)
			assert.Equal(t, Ratio{Num: tt.wantDays, Den: 14}, got.RecordCoverage)
		})
	}

	t.Run("창의 첫날이 지원하는 날짜 범위 앞으로 나가도 창 안의 날만 센다", func(t *testing.T) {
		earliest := mustDate(t, "0001-01-01")
		edge := earliest.AddDays(4)
		days := scenario{days: 5, items: 8}.build(t, edge)

		got, err := Compute(days, edge, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 5, Den: 14}, got.RecordCoverage)

		// 같은 기록을 한참 뒤에서 보면 창 밖이다. 빈 날짜 때문에 창이 과거로 열려 있지 않다.
		got, err = Compute(days, edge.AddDays(14), params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 0, Den: 14}, got.RecordCoverage)
	})
}

func TestCompute_ItemCoverage(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	// quietDays는 아무 항목도 나오지 않은 대화로 14일을 채운다. 기록 부족에 걸리지 않게 하기 위해서다.
	quietDays := func(overrides map[int]map[signal.Item]signal.Judgement) []signal.Day {
		days := make([]signal.Day, 0, 14)
		for ago := 13; ago >= 0; ago-- {
			days = append(days, dayAgo(asOf, ago, overrides[ago]))
		}
		return days
	}

	tests := []struct {
		name      string
		days      []signal.Day
		wantItems []signal.Item
	}{
		{
			name:      "날마다 대화해도 아무 항목도 나오지 않았으면 8분의 0이다",
			days:      quietDays(nil),
			wantItems: nil,
		},
		{
			name: "관찰됨은 이야기가 나온 것이다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				3: {signal.Sleep: observedIndirect()},
			}),
			wantItems: []signal.Item{signal.Sleep},
		},
		{
			name: "관찰되지 않음도 이야기가 나온 것이다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				3: {signal.Appetite: notObservedDirect()},
			}),
			wantItems: []signal.Item{signal.Appetite},
		},
		{
			name: "창 안에서 한 번만 나와도 센다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				13: {signal.Concentration: notObservedIndirect()},
			}),
			wantItems: []signal.Item{signal.Concentration},
		},
		{
			name: "같은 항목이 여러 날 나와도 한 번만 센다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				9: {signal.Sleep: observedDirect()},
				5: {signal.Sleep: notObservedDirect()},
				0: {signal.Sleep: observedDirect()},
			}),
			wantItems: []signal.Item{signal.Sleep},
		},
		{
			name: "서로 다른 날에 나온 항목을 모아서 센다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				10: {signal.Mood: observedDirect()},
				6:  {signal.Fatigue: notObservedDirect(), signal.Sleep: observedIndirect()},
				1:  {signal.SelfBlame: observedIndirect()},
			}),
			wantItems: []signal.Item{signal.Mood, signal.Sleep, signal.Fatigue, signal.SelfBlame},
		},
		{
			name: "창 밖에서만 나온 항목은 세지 않는다",
			days: sorted(t, append(
				quietDays(map[int]map[signal.Item]signal.Judgement{2: {signal.Mood: observedDirect()}}),
				dayAgo(asOf, 14, map[signal.Item]signal.Judgement{signal.Appetite: observedDirect()}),
			)...),
			wantItems: []signal.Item{signal.Mood},
		},
		{
			name: "기준일 뒤에 나온 항목은 세지 않는다",
			days: sorted(t, append(
				quietDays(map[int]map[signal.Item]signal.Judgement{2: {signal.Mood: observedDirect()}}),
				dayAgo(asOf, -1, map[signal.Item]signal.Judgement{signal.Psychomotor: observedDirect()}),
			)...),
			wantItems: []signal.Item{signal.Mood},
		},
		{
			name: "여덟 항목이 모두 나오면 8분의 8이다",
			days: quietDays(map[int]map[signal.Item]signal.Judgement{
				7: everyItem(notObservedDirect()),
			}),
			wantItems: []signal.Item{
				signal.Interest, signal.Mood, signal.Sleep, signal.Fatigue,
				signal.Appetite, signal.SelfBlame, signal.Concentration, signal.Psychomotor,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, len(tt.wantItems), got.MentionedItems)
			assert.Equal(t, Ratio{Num: len(tt.wantItems), Den: 8}, got.ItemCoverage)

			var wantMissing []signal.Item
			for _, item := range signal.AllItems() {
				mentioned := slices.Contains(tt.wantItems, item)
				assert.Equal(t, mentioned, got.Mentioned[item.Index()], item.String())
				if !mentioned {
					wantMissing = append(wantMissing, item)
				}
			}
			assert.Equal(t, wantMissing, got.MissingItems())
		})
	}
}

func TestCompute_Explicitness(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	tests := []struct {
		name         string
		days         []signal.Day
		wantObserved int
		wantDirect   int
		want         Ratio
	}{
		{
			name:         "대화한 날이 없으면 따질 판단이 없으므로 1이다",
			days:         nil,
			wantObserved: 0, wantDirect: 0,
			want: Ratio{Num: 1, Den: 1},
		},
		{
			name:         "관찰됨이 하나도 없으면 1로 둔다",
			days:         scenario{days: 14, items: 8}.build(t, asOf),
			wantObserved: 0, wantDirect: 0,
			want: Ratio{Num: 1, Den: 1},
		},
		{
			name:         "관찰됨이 모두 직접 언급이면 1이다",
			days:         scenario{days: 14, items: 8, direct: 3}.build(t, asOf),
			wantObserved: 3, wantDirect: 3,
			want: Ratio{Num: 3, Den: 3},
		},
		{
			name:         "관찰됨이 모두 간접 추론이면 0이다",
			days:         scenario{days: 14, items: 8, indirect: 3}.build(t, asOf),
			wantObserved: 3, wantDirect: 0,
			want: Ratio{Num: 0, Den: 3},
		},
		{
			name:         "직접 언급 둘, 간접 추론 셋이면 5분의 2다",
			days:         scenario{days: 14, items: 8, direct: 2, indirect: 3}.build(t, asOf),
			wantObserved: 5, wantDirect: 2,
			want: Ratio{Num: 2, Den: 5},
		},
		{
			name: "관찰되지 않음의 명시성은 세지 않는다",
			days: sorted(t,
				dayAgo(asOf, 2, map[signal.Item]signal.Judgement{
					signal.Sleep:    observedDirect(),
					signal.Mood:     notObservedIndirect(),
					signal.Fatigue:  notObservedIndirect(),
					signal.Appetite: notObservedIndirect(),
				}),
				dayAgo(asOf, 1, everyItem(notObservedIndirect())),
			),
			wantObserved: 1, wantDirect: 1,
			want: Ratio{Num: 1, Den: 1},
		},
		{
			name: "하루의 항목 하나가 판단 하나다: 같은 항목이 사흘 관찰되면 셋으로 센다",
			days: sorted(t,
				dayAgo(asOf, 2, map[signal.Item]signal.Judgement{signal.Sleep: observedDirect()}),
				dayAgo(asOf, 1, map[signal.Item]signal.Judgement{signal.Sleep: observedIndirect()}),
				dayAgo(asOf, 0, map[signal.Item]signal.Judgement{signal.Sleep: observedIndirect()}),
			),
			wantObserved: 3, wantDirect: 1,
			want: Ratio{Num: 1, Den: 3},
		},
		{
			name: "창 밖과 기준일 뒤의 관찰됨은 세지 않는다",
			days: sorted(t,
				dayAgo(asOf, 14, map[signal.Item]signal.Judgement{signal.Mood: observedIndirect()}),
				dayAgo(asOf, 13, map[signal.Item]signal.Judgement{signal.Mood: observedDirect()}),
				dayAgo(asOf, -1, map[signal.Item]signal.Judgement{signal.Mood: observedIndirect()}),
			),
			wantObserved: 1, wantDirect: 1,
			want: Ratio{Num: 1, Den: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, tt.wantObserved, got.ObservedJudgements)
			assert.Equal(t, tt.wantDirect, got.DirectJudgements)
			assert.Equal(t, tt.want, got.Explicitness)
		})
	}
}

func TestCompute_ValueIsTheWeakestComponent(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	tests := []struct {
		name         string
		scenario     scenario
		wantRecord   Ratio
		wantItems    Ratio
		wantExplicit Ratio
		wantValue    Ratio
		wantLimiting Component
		wantLevel    Level
	}{
		{
			name:       "기록 충실도가 가장 낮으면 그 값이 신뢰도다",
			scenario:   scenario{days: 7, items: 8, direct: 4},
			wantRecord: Ratio{Num: 7, Den: 14}, wantItems: Ratio{Num: 8, Den: 8}, wantExplicit: Ratio{Num: 4, Den: 4},
			wantValue: Ratio{Num: 7, Den: 14}, wantLimiting: ComponentRecordCoverage, wantLevel: Medium,
		},
		{
			name:       "항목 충족도가 가장 낮으면 그 값이 신뢰도다",
			scenario:   scenario{days: 14, items: 2, direct: 4},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 2, Den: 8}, wantExplicit: Ratio{Num: 4, Den: 4},
			wantValue: Ratio{Num: 2, Den: 8}, wantLimiting: ComponentItemCoverage, wantLevel: Low,
		},
		{
			name:       "근거 명시성이 가장 낮으면 그 값이 신뢰도다",
			scenario:   scenario{days: 14, items: 8, direct: 1, indirect: 9},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 8, Den: 8}, wantExplicit: Ratio{Num: 1, Den: 10},
			wantValue: Ratio{Num: 1, Den: 10}, wantLimiting: ComponentExplicitness, wantLevel: Low,
		},
		{
			name:       "세 요소가 다 다르면 가장 나쁜 쪽이 값을 정한다",
			scenario:   scenario{days: 10, items: 5, direct: 3, indirect: 1},
			wantRecord: Ratio{Num: 10, Den: 14}, wantItems: Ratio{Num: 5, Den: 8}, wantExplicit: Ratio{Num: 3, Den: 4},
			wantValue: Ratio{Num: 5, Den: 8}, wantLimiting: ComponentItemCoverage, wantLevel: Medium,
		},
		{
			// 평균이면 (1 + 1/8 + 1) ÷ 3 ≈ 0.71로 높음이 된다. 한 요소가 아주 나쁜 것이 다른 요소에 묻히면 안 된다.
			name:       "평균을 내면 높음이겠지만 가장 작은 값을 쓰므로 낮음이다",
			scenario:   scenario{days: 14, items: 1, direct: 2},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 1, Den: 8}, wantExplicit: Ratio{Num: 2, Den: 2},
			wantValue: Ratio{Num: 1, Den: 8}, wantLimiting: ComponentItemCoverage, wantLevel: Low,
		},
		{
			name:       "기록 충실도와 항목 충족도가 같으면 기록 충실도를 적는다",
			scenario:   scenario{days: 7, items: 4},
			wantRecord: Ratio{Num: 7, Den: 14}, wantItems: Ratio{Num: 4, Den: 8}, wantExplicit: Ratio{Num: 1, Den: 1},
			wantValue: Ratio{Num: 7, Den: 14}, wantLimiting: ComponentRecordCoverage, wantLevel: Medium,
		},
		{
			name:       "항목 충족도와 근거 명시성이 같으면 항목 충족도를 적는다",
			scenario:   scenario{days: 14, items: 4, direct: 1, indirect: 1},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 4, Den: 8}, wantExplicit: Ratio{Num: 1, Den: 2},
			wantValue: Ratio{Num: 4, Den: 8}, wantLimiting: ComponentItemCoverage, wantLevel: Medium,
		},
		{
			// 소수로 바꿔 견주면 오차 때문에 한쪽이 더 작다고 나올 수 있는 짝이다. 정수로 견주므로 같은 값이다.
			name:       "기록 충실도 14분의 10과 근거 명시성 35분의 25는 같은 값이므로 기록 충실도를 적는다",
			scenario:   scenario{days: 10, items: 8, direct: 25, indirect: 10},
			wantRecord: Ratio{Num: 10, Den: 14}, wantItems: Ratio{Num: 8, Den: 8}, wantExplicit: Ratio{Num: 25, Den: 35},
			wantValue: Ratio{Num: 10, Den: 14}, wantLimiting: ComponentRecordCoverage, wantLevel: High,
		},
		{
			name:       "기록 충실도 14분의 14와 근거 명시성 49분의 49는 같은 값이므로 기록 충실도를 적는다",
			scenario:   scenario{days: 14, items: 8, direct: 49},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 8, Den: 8}, wantExplicit: Ratio{Num: 49, Den: 49},
			wantValue: Ratio{Num: 14, Den: 14}, wantLimiting: ComponentRecordCoverage, wantLevel: High,
		},
		{
			name:       "세 요소가 모두 1이면 신뢰도도 1이다",
			scenario:   scenario{days: 14, items: 8, direct: 20},
			wantRecord: Ratio{Num: 14, Den: 14}, wantItems: Ratio{Num: 8, Den: 8}, wantExplicit: Ratio{Num: 20, Den: 20},
			wantValue: Ratio{Num: 14, Den: 14}, wantLimiting: ComponentRecordCoverage, wantLevel: High,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.scenario.build(t, asOf), asOf, params.Default())

			require.NoError(t, err)
			assert.False(t, got.Insufficient)
			assert.Equal(t, tt.wantRecord, got.RecordCoverage, "기록 충실도")
			assert.Equal(t, tt.wantItems, got.ItemCoverage, "항목 충족도")
			assert.Equal(t, tt.wantExplicit, got.Explicitness, "근거 명시성")
			assert.Equal(t, tt.wantValue, got.Value, "최종 값")
			assert.Equal(t, tt.wantLimiting, got.Limiting, "가장 약한 요소")
			assert.Equal(t, tt.wantLevel, got.Level, "구간")
		})
	}
}

// 구간: 0.4 미만 낮음, 0.4 이상 0.7 미만 보통, 0.7 이상 높음.
func TestCompute_LevelCutPoints(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	tests := []struct {
		name      string
		scenario  scenario
		wantValue Ratio
		wantLevel Level
	}{
		// 항목 충족도만 움직인다. 0.4는 3/8과 4/8 사이, 0.7은 5/8과 6/8 사이에 있다.
		{"항목 8분의 0 = 0은 낮음", scenario{days: 14, items: 0}, Ratio{Num: 0, Den: 8}, Low},
		{"항목 8분의 3 = 0.375는 낮음", scenario{days: 14, items: 3}, Ratio{Num: 3, Den: 8}, Low},
		{"항목 8분의 4 = 0.5는 보통", scenario{days: 14, items: 4}, Ratio{Num: 4, Den: 8}, Medium},
		{"항목 8분의 5 = 0.625는 보통", scenario{days: 14, items: 5}, Ratio{Num: 5, Den: 8}, Medium},
		{"항목 8분의 6 = 0.75는 높음", scenario{days: 14, items: 6}, Ratio{Num: 6, Den: 8}, High},
		{"항목 8분의 7 = 0.875는 높음", scenario{days: 14, items: 7}, Ratio{Num: 7, Den: 8}, High},

		// 기록 충실도만 움직인다. 기록 부족이 아닌 가장 적은 일수가 7일이고, 0.7은 9/14와 10/14 사이에 있다.
		{"기록 14분의 7 = 0.5는 보통", scenario{days: 7, items: 8}, Ratio{Num: 7, Den: 14}, Medium},
		{"기록 14분의 9 ≈ 0.643은 보통", scenario{days: 9, items: 8}, Ratio{Num: 9, Den: 14}, Medium},
		{"기록 14분의 10 ≈ 0.714는 높음", scenario{days: 10, items: 8}, Ratio{Num: 10, Den: 14}, High},
		{"기록 14분의 14 = 1은 높음", scenario{days: 14, items: 8}, Ratio{Num: 14, Den: 14}, High},

		// 근거 명시성만 움직인다. 세 요소 가운데 경계에 딱 걸릴 수 있는 것은 이 값뿐이다.
		{"명시성 5분의 0 = 0은 낮음", scenario{days: 14, items: 8, indirect: 5}, Ratio{Num: 0, Den: 5}, Low},
		{"명시성 100분의 39 = 0.39는 낮음", scenario{days: 14, items: 8, direct: 39, indirect: 61}, Ratio{Num: 39, Den: 100}, Low},
		{"명시성 5분의 2 = 0.4는 보통", scenario{days: 14, items: 8, direct: 2, indirect: 3}, Ratio{Num: 2, Den: 5}, Medium},
		{"명시성 10분의 4 = 0.4는 보통", scenario{days: 14, items: 8, direct: 4, indirect: 6}, Ratio{Num: 4, Den: 10}, Medium},
		{"명시성 15분의 6 = 0.4는 보통", scenario{days: 14, items: 8, direct: 6, indirect: 9}, Ratio{Num: 6, Den: 15}, Medium},
		{"명시성 110분의 44 = 0.4는 보통", scenario{days: 14, items: 8, direct: 44, indirect: 66}, Ratio{Num: 44, Den: 110}, Medium},
		{"명시성 108분의 43 ≈ 0.398은 낮음", scenario{days: 14, items: 8, direct: 43, indirect: 65}, Ratio{Num: 43, Den: 108}, Low},
		{"명시성 100분의 69 = 0.69는 보통", scenario{days: 14, items: 8, direct: 69, indirect: 31}, Ratio{Num: 69, Den: 100}, Medium},
		{"명시성 10분의 7 = 0.7은 높음", scenario{days: 14, items: 8, direct: 7, indirect: 3}, Ratio{Num: 7, Den: 10}, High},
		{"명시성 20분의 14 = 0.7은 높음", scenario{days: 14, items: 8, direct: 14, indirect: 6}, Ratio{Num: 14, Den: 20}, High},
		{"명시성 30분의 21 = 0.7은 높음", scenario{days: 14, items: 8, direct: 21, indirect: 9}, Ratio{Num: 21, Den: 30}, High},
		{"명시성 110분의 77 = 0.7은 높음", scenario{days: 14, items: 8, direct: 77, indirect: 33}, Ratio{Num: 77, Den: 110}, High},
		{"명시성 109분의 76 ≈ 0.697은 보통", scenario{days: 14, items: 8, direct: 76, indirect: 33}, Ratio{Num: 76, Den: 109}, Medium},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.scenario.build(t, asOf), asOf, params.Default())

			require.NoError(t, err)
			assert.False(t, got.Insufficient)
			assert.Equal(t, tt.wantValue, got.Value)
			assert.Equal(t, tt.wantLevel, got.Level)
		})
	}
}

// 경계가 소수로 주어져도 구간은 분수를 정수로 따진 것과 언제나 같아야 한다.
// 14일 × 여덟 항목에서 나올 수 있는 모든 분수를, 여러 분모로 적은 경계와 견준다.
func TestLevelOf_MatchesIntegerArithmetic(t *testing.T) {
	const maxJudgements = 14 * signal.ItemCount

	for _, cutDen := range []int{3, 7, 8, 10, 14, 20, 100} {
		for cutNum := 1; cutNum < cutDen; cutNum++ {
			threshold := float64(cutNum) / float64(cutDen)
			// 경계 하나만 보려고 다른 쪽 경계는 닿지 않는 곳에 둔다.
			asMedium := params.Confidence{MediumMin: threshold, HighMin: math.Inf(1)}
			asHigh := params.Confidence{MediumMin: threshold, HighMin: threshold}

			for den := 1; den <= maxJudgements; den++ {
				for num := 0; num <= den; num++ {
					reaches := num*cutDen >= cutNum*den

					wantMedium, wantHigh := Low, Low
					if reaches {
						wantMedium, wantHigh = Medium, High
					}
					value := Ratio{Num: num, Den: den}
					if got := levelOf(value, asMedium); got != wantMedium {
						t.Fatalf("%s는 경계 %d/%d에서 %s여야 하는데 %s다", value, cutNum, cutDen, wantMedium, got)
					}
					if got := levelOf(value, asHigh); got != wantHigh {
						t.Fatalf("%s는 경계 %d/%d에서 %s여야 하는데 %s다", value, cutNum, cutDen, wantHigh, got)
					}
				}
			}
		}
	}
}

func TestCompute_InsufficientRecords(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	t.Run("대화한 날이 6일이면 기록 부족이고 낮음으로 다룬다", func(t *testing.T) {
		// 요소만 보면 6/14 ≈ 0.43으로 보통 구간이다. 그래도 낮음이어야 한다.
		days := scenario{days: 6, items: 8, direct: 5}.build(t, asOf)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.True(t, got.Insufficient)
		assert.Equal(t, Low, got.Level)
		assert.True(t, got.Value.IsZero(), "최종 값은 계산하지 않는다")
		assert.Equal(t, ComponentNone, got.Limiting)
	})

	t.Run("기록 부족이어도 세 요소와 센 값은 채워서 돌려준다", func(t *testing.T) {
		days := scenario{days: 6, items: 5, direct: 3, indirect: 1}.build(t, asOf)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.True(t, got.Insufficient)
		assert.Equal(t, 6, got.ConversationDays)
		assert.Equal(t, 5, got.MentionedItems)
		assert.Equal(t, 4, got.ObservedJudgements)
		assert.Equal(t, 3, got.DirectJudgements)
		assert.Equal(t, Ratio{Num: 6, Den: 14}, got.RecordCoverage)
		assert.Equal(t, Ratio{Num: 5, Den: 8}, got.ItemCoverage)
		assert.Equal(t, Ratio{Num: 3, Den: 4}, got.Explicitness)
		assert.Equal(t,
			[]signal.Item{signal.SelfBlame, signal.Concentration, signal.Psychomotor},
			got.MissingItems())
	})

	t.Run("대화한 날이 7일이면 계산한다", func(t *testing.T) {
		days := scenario{days: 7, items: 8, direct: 5}.build(t, asOf)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.False(t, got.Insufficient)
		assert.Equal(t, Ratio{Num: 7, Den: 14}, got.Value)
		assert.Equal(t, Medium, got.Level)
	})

	t.Run("창 밖의 날까지 합쳐 7일이어도 창 안이 6일이면 기록 부족이다", func(t *testing.T) {
		days := sorted(t, append(
			scenario{days: 6, items: 8}.build(t, asOf),
			dayAgo(asOf, 14, everyItem(notObservedDirect())),
		)...)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, 6, got.ConversationDays)
		assert.True(t, got.Insufficient)
		assert.Equal(t, Low, got.Level)
	})

	for _, tt := range []struct {
		name string
		days []signal.Day
	}{
		{"기록이 하나도 없는 경우(nil)", nil},
		{"기록이 하나도 없는 경우(빈 목록)", []signal.Day{}},
		{"오래 쉬어서 창 안에 대화가 없는 경우", scenario{days: 14, items: 8, direct: 9}.build(t, asOf.AddDays(-30))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, Result{
				AsOf:           asOf,
				RecordCoverage: Ratio{Num: 0, Den: 14},
				ItemCoverage:   Ratio{Num: 0, Den: 8},
				Explicitness:   Ratio{Num: 1, Den: 1},
				Insufficient:   true,
				Level:          Low,
			}, got)
			assert.Len(t, got.MissingItems(), signal.ItemCount)
		})
	}
}

// 말수가 적은 사용자: 날마다 대화하지만 꺼내는 이야기가 몇 가지뿐이다.
// 점수는 낮게 나오지만 그 점수를 믿을 수 없다는 것이 신뢰도에 드러나야 한다.
func TestCompute_QuietUser(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	// 14일 내내 대화했고, 잠 이야기만 가끔 하고 기분은 괜찮다고 두 번 말했다.
	var days []signal.Day
	for ago := 13; ago >= 0; ago-- {
		judgements := map[signal.Item]signal.Judgement{}
		if ago%3 == 0 {
			judgements[signal.Sleep] = observedDirect()
		}
		if ago == 8 || ago == 1 {
			judgements[signal.Mood] = notObservedDirect()
		}
		days = append(days, dayAgo(asOf, ago, judgements))
	}

	t.Run("두 항목만 나왔으면 날마다 대화했어도 낮음이다", func(t *testing.T) {
		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.False(t, got.Insufficient, "기록 부족은 아니다")
		assert.Equal(t, Ratio{Num: 14, Den: 14}, got.RecordCoverage)
		assert.Equal(t, Ratio{Num: 5, Den: 5}, got.Explicitness)
		assert.Equal(t, Ratio{Num: 2, Den: 8}, got.ItemCoverage)
		assert.Equal(t, Ratio{Num: 2, Den: 8}, got.Value)
		assert.Equal(t, ComponentItemCoverage, got.Limiting)
		assert.Equal(t, Low, got.Level)
	})

	t.Run("빠진 항목이 무엇인지 알려준다", func(t *testing.T) {
		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, []signal.Item{
			signal.Interest, signal.Fatigue, signal.Appetite,
			signal.SelfBlame, signal.Concentration, signal.Psychomotor,
		}, got.MissingItems())
	})

	t.Run("세 항목이어도 아직 낮음이다", func(t *testing.T) {
		asked := slices.Clone(days)
		asked[13] = dayAgo(asOf, 0, map[signal.Item]signal.Judgement{
			signal.Sleep:   observedDirect(),
			signal.Fatigue: notObservedDirect(),
		})

		got, err := Compute(asked, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 3, Den: 8}, got.Value)
		assert.Equal(t, Low, got.Level)
	})

	t.Run("빠진 항목 둘을 물어 네 항목이 되면 보통으로 올라간다", func(t *testing.T) {
		asked := slices.Clone(days)
		asked[13] = dayAgo(asOf, 0, map[signal.Item]signal.Judgement{
			signal.Sleep:    observedDirect(),
			signal.Fatigue:  notObservedDirect(),
			signal.Appetite: notObservedDirect(),
		})

		got, err := Compute(asked, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 4, Den: 8}, got.Value)
		assert.Equal(t, Medium, got.Level)
	})
}

func TestCompute_UsesParams(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	t.Run("창의 길이를 바꾸면 세는 기간과 기록 충실도의 분모가 함께 바뀐다", func(t *testing.T) {
		p := params.Default()
		p.Window.Days = 10
		p.Window.MinConversationDays = 5
		p.Score.ItemScore3MinDays = 9
		days := scenario{days: 14, items: 8}.build(t, asOf)

		got, err := Compute(days, asOf, p)

		require.NoError(t, err)
		assert.Equal(t, 10, got.ConversationDays)
		assert.Equal(t, Ratio{Num: 10, Den: 10}, got.RecordCoverage)
	})

	t.Run("창의 첫날은 기준일에서 창의 길이보다 하루 적게 거슬러 간 날이다", func(t *testing.T) {
		p := params.Default()
		p.Window.Days = 10
		p.Window.MinConversationDays = 1
		p.Score.ItemScore3MinDays = 9
		days := sorted(t,
			dayAgo(asOf, 10, everyItem(notObservedDirect())),
			dayAgo(asOf, 9, everyItem(notObservedDirect())),
		)

		got, err := Compute(days, asOf, p)

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 1, Den: 10}, got.RecordCoverage)
	})

	t.Run("기록 부족의 기준 일수를 바꾸면 그 일수부터 계산한다", func(t *testing.T) {
		p := params.Default()
		p.Window.MinConversationDays = 3
		days := scenario{days: 3, items: 8}.build(t, asOf)

		got, err := Compute(days, asOf, p)

		require.NoError(t, err)
		assert.False(t, got.Insufficient)
		assert.Equal(t, Ratio{Num: 3, Den: 14}, got.Value)
		assert.Equal(t, Low, got.Level, "3/14 ≈ 0.21은 값으로도 낮음이다")

		got, err = Compute(days[1:], asOf, p)

		require.NoError(t, err)
		assert.True(t, got.Insufficient)
	})

	t.Run("구간의 경계를 바꾸면 같은 값의 구간이 달라진다", func(t *testing.T) {
		p := params.Default()
		p.Confidence.MediumMin = 0.625
		p.Confidence.HighMin = 0.875

		tests := []struct {
			items int
			want  Level
		}{
			{4, Low},
			{5, Medium},
			{6, Medium},
			{7, High},
			{8, High},
		}
		for _, tt := range tests {
			got, err := Compute(scenario{days: 14, items: tt.items}.build(t, asOf), asOf, p)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Level, "항목 %d/8", tt.items)
		}
	})
}

func TestCompute_Errors(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")
	valid := scenario{days: 14, items: 8, direct: 3}.build(t, asOf)

	t.Run("기준일이 빠지면 오류다", func(t *testing.T) {
		got, err := Compute(valid, recorddate.Date{}, params.Default())

		require.ErrorIs(t, err, ErrNoAsOf)
		assert.Equal(t, Result{}, got)
	})

	t.Run("조정 값이 틀리면 어느 값인지 알려준다", func(t *testing.T) {
		p := params.Default()
		p.Window.Days = 0

		got, err := Compute(valid, asOf, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Window.Days", fieldErr.Field)
		assert.Equal(t, Result{}, got)
	})

	t.Run("구간의 경계가 숫자가 아니면 오류다", func(t *testing.T) {
		p := params.Default()
		p.Confidence.MediumMin = math.NaN()

		_, err := Compute(valid, asOf, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Confidence.MediumMin", fieldErr.Field)
	})

	t.Run("날짜순이 아니면 오류다", func(t *testing.T) {
		days := slices.Clone(valid)
		days[3], days[4] = days[4], days[3]

		got, err := Compute(days, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrUnsortedDays)
		var dayErr *signal.DayError
		require.ErrorAs(t, err, &dayErr)
		assert.Equal(t, 4, dayErr.Index)
		assert.Equal(t, Result{}, got)
	})

	t.Run("같은 날짜가 두 번 나오면 오류다", func(t *testing.T) {
		days := slices.Clone(valid)
		days[5].Date = days[4].Date

		_, err := Compute(days, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrDuplicateDate)
	})

	t.Run("근거 없는 관찰됨이 섞여 있으면 오류다", func(t *testing.T) {
		days := slices.Clone(valid)
		days[0].Judgements[signal.Sleep.Index()] = signal.Judgement{Status: signal.Observed, Explicitness: signal.None}

		_, err := Compute(days, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})

	t.Run("창 밖의 하루가 틀려도 오류다", func(t *testing.T) {
		broken := dayAgo(asOf, 60, nil)
		broken.Judgements[signal.Mood.Index()] = signal.Judgement{Status: signal.NotMentioned, Explicitness: signal.Direct}
		days := append([]signal.Day{broken}, valid...)

		_, err := Compute(days, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})
}

func TestCompute_PureAndDeterministic(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")
	days := scenario{days: 12, items: 6, direct: 17, indirect: 8}.build(t, asOf)

	t.Run("같은 입력이면 언제나 같은 결과다", func(t *testing.T) {
		first, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		for range 5 {
			again, err := Compute(days, asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, first, again)
		}
	})

	t.Run("받은 목록을 고치지 않는다", func(t *testing.T) {
		before := slices.Clone(days)

		_, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, before, days)
	})

	t.Run("기준일 뒤의 기록은 결과를 바꾸지 않는다", func(t *testing.T) {
		want, err := Compute(days, asOf, params.Default())
		require.NoError(t, err)

		withFuture := append(slices.Clone(days),
			dayAgo(asOf, -1, everyItem(observedIndirect())),
			dayAgo(asOf, -5, everyItem(observedIndirect())),
		)
		got, err := Compute(withFuture, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("하루를 지우면 남은 기록으로 다시 계산된다", func(t *testing.T) {
		sevenDays := scenario{days: 7, items: 8}.build(t, asOf)

		before, err := Compute(sevenDays, asOf, params.Default())
		require.NoError(t, err)
		after, err := Compute(slices.Delete(slices.Clone(sevenDays), 2, 3), asOf, params.Default())
		require.NoError(t, err)

		assert.False(t, before.Insufficient)
		assert.Equal(t, Medium, before.Level)
		assert.True(t, after.Insufficient, "엿새가 남아 기록 부족이 된다")
		assert.Equal(t, Low, after.Level)
	})
}

// 대화별 신호 행에서 하루를 만들어 넣는 실제 흐름을 따라가 본다.
func TestCompute_FromSignalRows(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")
	row := func(item signal.Item, status signal.Status, explicitness signal.Explicitness) signal.Row {
		return signal.Row{ConversationID: "c", Item: item, Status: status, Explicitness: explicitness}
	}

	rowsByDate := map[recorddate.Date][]signal.Row{}
	for ago := 6; ago >= 0; ago-- {
		rowsByDate[asOf.AddDays(-ago)] = []signal.Row{
			row(signal.Sleep, signal.Observed, signal.Direct),
			row(signal.Mood, signal.NotObserved, signal.Direct),
			row(signal.Fatigue, signal.NotObserved, signal.Indirect),
			row(signal.Appetite, signal.NotMentioned, signal.None),
		}
	}
	// 하루에 두 번 대화한 날: 아침에는 간접 추론, 밤에는 직접 언급으로 관찰됐다. 그날의 판단은 직접 언급 하나다.
	rowsByDate[asOf] = append(rowsByDate[asOf], row(signal.Sleep, signal.Observed, signal.Indirect))
	// 분석을 꺼 둔 날은 신호 행이 없다. 대화하지 않은 날과 똑같이 빠진다.
	rowsByDate[asOf.AddDays(-7)] = nil

	t.Run("취소하기 전", func(t *testing.T) {
		days, err := signal.MergeDays(rowsByDate)
		require.NoError(t, err)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 7, Den: 14}, got.RecordCoverage)
		assert.Equal(t, Ratio{Num: 3, Den: 8}, got.ItemCoverage)
		assert.Equal(t, Ratio{Num: 7, Den: 7}, got.Explicitness)
	})

	t.Run("취소한 신호는 언급 없음과 같아서 항목 충족도와 근거 명시성에서 빠진다", func(t *testing.T) {
		cancelled := map[recorddate.Date][]signal.Row{}
		for date, rows := range rowsByDate {
			rows = slices.Clone(rows)
			for i := range rows {
				// 피로는 하루만 남기고 모두 취소하고, 잠은 기준일의 직접 언급만 취소한다.
				if rows[i].Item == signal.Fatigue && date != asOf.AddDays(-3) {
					rows[i].Cancelled = true
				}
				if rows[i].Item == signal.Sleep && date == asOf && rows[i].Explicitness == signal.Direct {
					rows[i].Cancelled = true
				}
			}
			cancelled[date] = rows
		}
		days, err := signal.MergeDays(cancelled)
		require.NoError(t, err)

		got, err := Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 7, Den: 14}, got.RecordCoverage, "취소해도 대화한 날은 그대로다")
		assert.Equal(t, Ratio{Num: 3, Den: 8}, got.ItemCoverage, "피로는 하루 남아 있어 아직 나온 항목이다")
		assert.Equal(t, Ratio{Num: 6, Den: 7}, got.Explicitness, "기준일의 잠은 간접 추론만 남았다")

		for date, rows := range cancelled {
			for i := range rows {
				if rows[i].Item == signal.Fatigue {
					rows[i].Cancelled = true
				}
			}
			cancelled[date] = rows
		}
		days, err = signal.MergeDays(cancelled)
		require.NoError(t, err)

		got, err = Compute(days, asOf, params.Default())

		require.NoError(t, err)
		assert.Equal(t, Ratio{Num: 2, Den: 8}, got.ItemCoverage, "피로를 모두 취소하면 나온 적 없는 항목이 된다")
		assert.Equal(t, Low, got.Level)
	})
}

func TestResult_JSON(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")

	t.Run("계산한 결과", func(t *testing.T) {
		got, err := Compute(scenario{days: 14, items: 5, direct: 3}.build(t, asOf), asOf, params.Default())
		require.NoError(t, err)

		encoded, err := json.Marshal(got)

		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		assert.Equal(t, "2026-09-20", decoded["AsOf"])
		assert.Equal(t, "medium", decoded["Level"])
		assert.Equal(t, "item_coverage", decoded["Limiting"])
		assert.Equal(t, map[string]any{"Num": 5.0, "Den": 8.0}, decoded["Value"])
	})

	t.Run("기록 부족인 결과도 그대로 적힌다", func(t *testing.T) {
		got, err := Compute(nil, asOf, params.Default())
		require.NoError(t, err)

		encoded, err := json.Marshal(got)

		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		assert.Equal(t, true, decoded["Insufficient"])
		assert.Equal(t, "low", decoded["Level"])
		assert.Equal(t, "none", decoded["Limiting"])

		var back Result
		require.NoError(t, json.Unmarshal(encoded, &back))
		assert.Equal(t, got, back)
	})
}

func TestSeries(t *testing.T) {
	first := mustDate(t, "2026-09-01")
	// 첫날부터 이레 동안 날마다 대화하고 그 뒤로는 쉬었다.
	days := scenario{days: 7, items: 8, direct: 6, indirect: 2}.build(t, first.AddDays(6))

	t.Run("날짜마다 Compute를 부른 것과 같다", func(t *testing.T) {
		from, to := first.AddDays(-2), first.AddDays(25)

		got, err := Series(days, from, to, params.Default())

		require.NoError(t, err)
		require.Len(t, got, 28)
		for i, result := range got {
			want, err := Compute(days, from.AddDays(i), params.Default())
			require.NoError(t, err)
			assert.Equal(t, want, result, from.AddDays(i).String())
		}
	})

	t.Run("대화하지 않은 날에도 창이 움직여 대화한 일수가 바뀐다", func(t *testing.T) {
		got, err := Series(days, first.AddDays(-1), first.AddDays(20), params.Default())
		require.NoError(t, err)

		var conversationDays []int
		var insufficient []bool
		for _, result := range got {
			conversationDays = append(conversationDays, result.ConversationDays)
			insufficient = append(insufficient, result.Insufficient)
		}

		assert.Equal(t,
			// 첫 대화 전날, 이레 동안 하루씩 늘고, 창에 다 들어 있는 이레, 하루씩 빠져나가는 이레, 다 빠져나간 날
			[]int{0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0},
			conversationDays)
		for i, n := range conversationDays {
			assert.Equal(t, n < 7, insufficient[i], "%d번째 날", i)
		}
	})

	t.Run("시작과 끝이 같으면 하루다", func(t *testing.T) {
		got, err := Series(days, first, first, params.Default())

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, first, got[0].AsOf)
	})

	t.Run("지원하는 마지막 날짜까지 돌려도 끝난다", func(t *testing.T) {
		last := mustDate(t, "9999-12-31")

		got, err := Series(nil, last.AddDays(-1), last, params.Default())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, last, got[1].AsOf)
	})

	t.Run("끝이 시작보다 앞서면 오류다", func(t *testing.T) {
		got, err := Series(days, first.AddDays(1), first, params.Default())

		require.ErrorIs(t, err, ErrInvalidRange)
		assert.Nil(t, got)
	})

	t.Run("날짜가 빠지면 오류다", func(t *testing.T) {
		_, err := Series(days, recorddate.Date{}, first, params.Default())
		require.ErrorIs(t, err, ErrNoAsOf)

		_, err = Series(days, first, recorddate.Date{}, params.Default())
		require.ErrorIs(t, err, ErrNoAsOf)
	})

	t.Run("입력이 틀리면 Compute와 같은 오류다", func(t *testing.T) {
		p := params.Default()
		p.Confidence.HighMin = 0.2

		_, err := Series(days, first, first.AddDays(3), p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Confidence.HighMin", fieldErr.Field)

		unsorted := slices.Clone(days)
		slices.Reverse(unsorted)

		_, err = Series(unsorted, first, first.AddDays(3), params.Default())

		require.ErrorIs(t, err, signal.ErrUnsortedDays)
	})
}
