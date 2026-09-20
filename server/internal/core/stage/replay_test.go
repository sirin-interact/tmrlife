package stage

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

func TestComputeRisesOnceRecordsSupportIt(t *testing.T) {
	// 세 항목이 날마다 관찰되고 두 항목은 괜찮다고 말한 사람이다. 6월 1일부터 열흘 동안 날마다 대화했다.
	days := diary(t, "2026-06-01", times(10, "OOOxx...")...)

	got, err := Compute(days, mustDate(t, "2026-06-10"), params.Default())
	require.NoError(t, err)

	t.Run("흐름은 첫 대화 날부터 기준일까지 하루에 하나씩이다", func(t *testing.T) {
		assert.Equal(t, mustDate(t, "2026-06-01"), got.From)
		assert.Equal(t, mustDate(t, "2026-06-10"), got.AsOf)
		require.Len(t, got.Series, 10)
		for offset, pt := range got.Series {
			assert.Equal(t, got.From.AddDays(offset), pt.Date)
		}
	})

	t.Run("대화한 날이 7일이 되기 전에는 기록 부족이라 0단계다", func(t *testing.T) {
		for _, pt := range got.Series[:6] {
			assert.True(t, pt.Insufficient, pt.Date.String())
			assert.Equal(t, confidence.Low, pt.Confidence, pt.Date.String())
			assert.Equal(t, Everyday, pt.Stage, pt.Date.String())
			assert.False(t, pt.Held, "점수가 없으니 오르려던 것도 아니다")
			assert.Empty(t, pt.Reasons)
		}
	})

	t.Run("7일째에 점수 9가 나오고 신뢰도가 보통이라 그날 1단계가 된다", func(t *testing.T) {
		// 세 항목이 7일 중 7일 관찰됐다. 환산 14일이라 항목마다 3점, 합이 9다.
		// 신뢰도는 기록 충실도 7/14, 항목 충족도 5/8, 근거 명시성 1 가운데 가장 작은 0.5다.
		want := Point{
			Date:             mustDate(t, "2026-06-07"),
			HasRecord:        true,
			ConversationDays: 7,
			Score:            9,
			Confidence:       confidence.Medium,
			Raw:              Reflection,
			Stage:            Reflection,
			Reasons:          []Reason{ReasonScore},
		}
		assert.Equal(t, want, at(t, got, "2026-06-07"))
	})

	t.Run("기준일의 상태는 흐름의 마지막 날과 같다", func(t *testing.T) {
		assert.Equal(t, got.Series[len(got.Series)-1], got.State)
		assert.Equal(t, Reflection, got.State.Stage)
		assert.Equal(t, 10, got.State.ConversationDays)
	})
}

func TestComputeSustainedStage(t *testing.T) {
	// 네 항목이 날마다 관찰된다. 점수는 12로 머물고 더 오르지 않는다.
	days := diary(t, "2026-06-01", times(25, "OOOO....")...)

	got, err := Compute(days, mustDate(t, "2026-06-25"), params.Default())
	require.NoError(t, err)

	t.Run("7일째에 점수 12가 나오지만 하루에 한 단계라 1단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-06-07")

		assert.Equal(t, 12, pt.Score)
		assert.Equal(t, Suggestion, pt.Raw)
		assert.Equal(t, Reflection, pt.Stage)
		assert.Equal(t, ReasonHeldOneStepPerDay, pt.HeldBy())
		assert.Zero(t, pt.ElevatedDays)
	})

	t.Run("8일째에 2단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-06-08")

		assert.Equal(t, Suggestion, pt.Stage)
		assert.False(t, pt.Held)
		assert.Equal(t, 1, pt.ElevatedDays)
	})

	t.Run("2단계가 13일 이어진 6월 20일까지는 2단계다", func(t *testing.T) {
		pt := at(t, got, "2026-06-20")

		assert.Equal(t, Suggestion, pt.Stage)
		assert.Equal(t, 13, pt.ElevatedDays)
	})

	t.Run("14일째인 6월 21일에 3단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-06-21")

		assert.Equal(t, 12, pt.Score, "점수는 그대로다")
		assert.Equal(t, Recommendation, pt.Stage)
		assert.Equal(t, 14, pt.ElevatedDays)
		assert.Equal(t, []Reason{ReasonScore, ReasonSustained}, pt.Reasons)
	})

	t.Run("처음부터 높았던 사람이라 평소와 다르지 않아 변화 감지는 울리지 않는다", func(t *testing.T) {
		for _, pt := range got.Series {
			assert.False(t, pt.Detected, pt.Date.String())
		}
	})
}

func TestComputeHeldByLowConfidence(t *testing.T) {
	// 잠과 기운 이야기만 하는 사람이다. 두 항목이 날마다 관찰되지만 나머지 여섯 항목은 한 번도 이야기가 나오지 않았다.
	quiet := times(20, "..OO....")
	// 21일째부터는 다른 항목 이야기도 나온다.
	open := times(3, "xxOOxx..")
	days := diary(t, "2026-06-01", join(quiet, open)...)

	got, err := Compute(days, mustDate(t, "2026-06-23"), params.Default())
	require.NoError(t, err)

	t.Run("점수는 6으로 1단계에 해당하지만 항목 충족도가 8분의 2라 0단계에 묶인다", func(t *testing.T) {
		for _, pt := range got.Series[6:20] {
			assert.Equal(t, 6, pt.Score, pt.Date.String())
			assert.Equal(t, confidence.Low, pt.Confidence, pt.Date.String())
			assert.Equal(t, Reflection, pt.Raw, pt.Date.String())
			assert.Equal(t, Everyday, pt.Stage, pt.Date.String())
			assert.True(t, pt.Held, pt.Date.String())
			assert.Equal(t, []Reason{ReasonScore, ReasonHeldLowConfidence}, pt.Reasons, pt.Date.String())
		}
	})

	t.Run("빠진 항목 이야기가 나온 날 신뢰도가 올라 1단계가 된다", func(t *testing.T) {
		pt := at(t, got, "2026-06-21")

		assert.Equal(t, confidence.High, pt.Confidence, "항목 충족도가 8분의 6이 되었다")
		assert.Equal(t, Reflection, pt.Stage)
		assert.False(t, pt.Held)
		assert.Equal(t, []Reason{ReasonScore}, pt.Reasons)
	})
}

func TestComputeChangeDetectedWithLowScore(t *testing.T) {
	// 두 주 동안 네 항목 모두 괜찮다고 말해 평소의 하루 평균이 0으로 잡힌다. 기준선 기간은 6월 14일까지다.
	usual := times(14, "xxxx....")
	// 6월 15일부터 피로가 날마다 관찰된다. 누적값은 하루에 0.5씩 는다.
	tired := times(9, "xxxO....")
	days := diary(t, "2026-06-01", join(usual, tired)...)

	got, err := Compute(days, mustDate(t, "2026-06-23"), params.Default())
	require.NoError(t, err)

	t.Run("누적값이 한계값과 같은 여드레째에는 아직 0단계다", func(t *testing.T) {
		pt := at(t, got, "2026-06-22")

		assert.False(t, pt.Detected)
		assert.Equal(t, Everyday, pt.Stage)
	})

	t.Run("한계값을 넘은 아흐레째에 점수 2로도 1단계가 된다", func(t *testing.T) {
		want := Point{
			Date:             mustDate(t, "2026-06-23"),
			HasRecord:        true,
			ConversationDays: 14,
			// 피로가 14일 중 9일 관찰됐다. 환산 9일이라 2점이고 나머지 항목은 0점이다.
			Score:      2,
			Confidence: confidence.Medium,
			Detected:   true,
			Raw:        Reflection,
			Stage:      Reflection,
			Reasons:    []Reason{ReasonChangeDetected},
		}
		assert.Equal(t, want, got.State)
	})
}

func TestComputeSilence(t *testing.T) {
	t.Run("점수로 받치던 2단계는 기록 부족인 동안 그대로 이어지다가, 창이 비는 날 0단계로 돌아간다", func(t *testing.T) {
		// 6월 1일부터 10일까지 날마다 대화하고 그 뒤로 말이 없다.
		days := diary(t, "2026-06-01", times(10, "OOOO....")...)

		got, err := Compute(days, mustDate(t, "2026-06-30"), params.Default())
		require.NoError(t, err)

		// 6월 7일에 1단계, 6월 8일에 2단계가 되었다. 6월 17일의 창은 6월 4일부터라 대화한 날이 7일 남아 있다.
		// 대화하지 않은 날에도 점수는 나오므로 2단계가 이어지고 이어진 일수도 센다. 6월 8일부터 열흘째다.
		last := at(t, got, "2026-06-17")
		assert.Equal(t, 7, last.ConversationDays)
		assert.False(t, last.HasRecord)
		assert.Equal(t, Suggestion, last.Stage)
		assert.Equal(t, 10, last.ElevatedDays)

		// 6월 18일의 창에는 6일뿐이다. 점수가 없는 날은 나아졌다는 근거도 없으므로 전날의 2단계를 이어 간다.
		carried := at(t, got, "2026-06-18")
		assert.Equal(t, 6, carried.ConversationDays)
		assert.True(t, carried.Insufficient)
		assert.Equal(t, Everyday, carried.Raw)
		assert.Equal(t, Suggestion, carried.Stage)
		assert.False(t, carried.Held)
		assert.Equal(t, []Reason{ReasonCarriedInsufficientRecords}, carried.Reasons)
		assert.Equal(t, 10, carried.ElevatedDays, "기록 부족인 날은 세지 않고 끊지도 않는다")

		// 6월 23일의 창에는 6월 10일 하루가 남아 있고, 6월 24일의 창은 비어 있다.
		edge := at(t, got, "2026-06-23")
		assert.Equal(t, 1, edge.ConversationDays)
		assert.Equal(t, Suggestion, edge.Stage)
		assert.Equal(t, 10, edge.ElevatedDays)
		for _, pt := range got.Series[23:] {
			assert.Zero(t, pt.ConversationDays, pt.Date.String())
			assert.Equal(t, Everyday, pt.Stage, pt.Date.String())
			assert.Zero(t, pt.ElevatedDays, pt.Date.String())
			assert.Equal(t, []Reason{ReasonNoRecentRecords}, pt.Reasons, pt.Date.String())
		}
	})

	t.Run("변화 감지로 받치던 1단계는 창이 빌 때까지 이어지고, 돌아온 날에는 기록 부족에 묶인다", func(t *testing.T) {
		usual := times(14, "xxxx....")
		// 6월 15일부터 25일까지 열하루 동안 피로가 관찰된다. 누적값은 5.5까지 오른다.
		tired := times(11, "xxxO....")
		// 6월 26일부터 7월 31일까지 말이 없다가 8월 1일에 돌아와 괜찮다고 말한다.
		away := silence(36)
		back := []string{"xxxx...."}
		days := diary(t, "2026-06-01", join(usual, tired, away, back)...)

		got, err := Compute(days, mustDate(t, "2026-08-01"), params.Default())
		require.NoError(t, err)

		assert.Equal(t, Reflection, at(t, got, "2026-06-25").Stage)

		// 7월 3일부터 기록 부족이다. 올리는 것이 아니라 머무는 것이므로 1단계가 이어진다.
		held := at(t, got, "2026-07-03")
		assert.Equal(t, 6, held.ConversationDays)
		assert.True(t, held.Insufficient)
		assert.True(t, held.Detected, "대화하지 않는 동안 누적값은 그대로 남는다")
		assert.Equal(t, Reflection, held.Stage)
		assert.False(t, held.Held)

		// 7월 8일의 창에는 6월 25일 하루가 남아 있다. 7월 9일부터 창이 빈다.
		assert.Equal(t, Reflection, at(t, got, "2026-07-08").Stage)
		empty := at(t, got, "2026-07-09")
		assert.True(t, empty.Detected)
		assert.Equal(t, Everyday, empty.Stage)
		assert.Equal(t, []Reason{ReasonNoRecentRecords}, empty.Reasons)

		// 돌아온 날: 누적값은 5.5에서 0.5 줄어 5.0이라 아직 변화 감지 상태다. 하루치 기록으로는 올리지 않는다.
		want := Point{
			Date:             mustDate(t, "2026-08-01"),
			HasRecord:        true,
			ConversationDays: 1,
			Insufficient:     true,
			Confidence:       confidence.Low,
			Detected:         true,
			Raw:              Reflection,
			Stage:            Everyday,
			Held:             true,
			Reasons:          []Reason{ReasonChangeDetected, ReasonHeldInsufficientRecords},
		}
		assert.Equal(t, want, got.State)
	})
}

// 사흘 대화하고 사흘 쉬기를 되풀이하는 사람이다. 대화한 날마다 여섯 항목이 관찰된다.
// 창 안의 대화한 일수가 7, 8, 8, 7, 6, 6일로 돌아서, 엿새마다 이틀씩 기록 부족이 된다.
// 기록 부족인 날에는 전날의 단계를 그대로 이어 가므로 단계가 오르내리지 않는다.
func TestComputeDoesNotFlapAroundTheRecordThreshold(t *testing.T) {
	var lines []string
	for range 10 {
		lines = append(lines, "OOOOOO..", "OOOOOO..", "OOOOOO..", "", "", "")
	}
	days := diary(t, "2026-06-01", lines...)

	got, err := Compute(days, mustDate(t, "2026-07-30"), params.Default())
	require.NoError(t, err)

	t.Run("점수가 처음 나오는 6월 13일부터 하루에 한 단계씩 올라 6월 15일에 3단계가 된다", func(t *testing.T) {
		// 여섯 항목이 모두 3점이라 18점이다. 6월 13일, 14일, 15일은 모두 대화한 날이다.
		before := at(t, got, "2026-06-12")
		assert.True(t, before.Insufficient)
		assert.Equal(t, Everyday, before.Stage)

		first := at(t, got, "2026-06-13")
		assert.Equal(t, 7, first.ConversationDays)
		assert.Equal(t, 18, first.Score)
		assert.Equal(t, Recommendation, first.Raw)
		assert.Equal(t, Reflection, first.Stage)
		assert.Equal(t, []Reason{ReasonScore, ReasonHeldOneStepPerDay}, first.Reasons)

		assert.Equal(t, Suggestion, at(t, got, "2026-06-14").Stage)
		third := at(t, got, "2026-06-15")
		assert.Equal(t, Recommendation, third.Stage)
		assert.Equal(t, []Reason{ReasonScore}, third.Reasons)
	})

	t.Run("그 뒤로는 엿새마다 이틀씩 기록 부족이 끼어도 3단계에서 움직이지 않는다", func(t *testing.T) {
		from, ok := got.At(mustDate(t, "2026-06-13"))
		require.True(t, ok)
		offset := from.Date.DaysSince(got.From)

		// 6월 13일부터 엿새 주기로 대화한 일수가 7, 8, 8, 7, 6, 6이다.
		insufficient := []bool{false, false, false, false, true, true}
		for i, pt := range got.Series[offset:] {
			assert.Equal(t, insufficient[i%len(insufficient)], pt.Insufficient, pt.Date.String())
			if i >= 2 {
				assert.Equal(t, Recommendation, pt.Stage, pt.Date.String())
				assert.False(t, pt.Held, pt.Date.String())
			}
			if pt.Insufficient {
				assert.Equal(t, []Reason{ReasonCarriedInsufficientRecords}, pt.Reasons, pt.Date.String())
			}
		}
	})

	t.Run("2단계 이상이 이어진 일수는 기록 부족인 날만 빼고 끊기지 않고 늘어난다", func(t *testing.T) {
		// 2단계가 된 6월 14일부터 7월 30일까지 47일이고, 그 가운데 기록 부족인 날이 16일이다.
		assert.Equal(t, 31, got.State.ElevatedDays)

		previous := 0
		for _, pt := range got.Series {
			if pt.Stage >= Suggestion {
				assert.GreaterOrEqual(t, pt.ElevatedDays, previous, pt.Date.String())
				assert.LessOrEqual(t, pt.ElevatedDays, previous+1, pt.Date.String())
			}
			previous = pt.ElevatedDays
		}
	})
}

func TestResultChanged(t *testing.T) {
	days := diary(t, "2026-06-01", times(10, "OOOO....")...)

	tests := []struct {
		name         string
		asOf         string
		wantPrevious Stage
		wantChanged  bool
	}{
		{"기록 부족인 동안은 0단계 그대로다", "2026-06-06", Everyday, false},
		{"점수가 처음 나온 날 1단계가 되었다", "2026-06-07", Everyday, true},
		{"그다음 날 한 단계 더 올라 2단계가 되었다", "2026-06-08", Reflection, true},
		{"그 뒤로는 2단계 그대로다", "2026-06-09", Suggestion, false},
		{"기록 부족이 되는 날에도 2단계를 이어 간다", "2026-06-18", Suggestion, false},
		{"창이 비는 날 0단계로 돌아갔다", "2026-06-24", Suggestion, true},
		{"첫 대화 날의 전날은 0단계다", "2026-06-01", Everyday, false},
		{"기록이 시작되기 전에도 0단계 그대로다", "2026-05-20", Everyday, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(days, mustDate(t, tt.asOf), params.Default())
			require.NoError(t, err)

			previous, changed := got.Changed()
			assert.Equal(t, tt.wantPrevious, previous)
			assert.Equal(t, tt.wantChanged, changed)
		})
	}

	t.Run("빈 결과에서도 0단계 그대로다", func(t *testing.T) {
		previous, changed := Result{}.Changed()
		assert.Equal(t, Everyday, previous)
		assert.False(t, changed)
	})
}

func TestComputeWithoutRecords(t *testing.T) {
	asOf := mustDate(t, "2026-09-20")
	want := Result{
		AsOf:   asOf,
		Series: []Point{},
		State: Point{
			Date:         asOf,
			Insufficient: true,
			Confidence:   confidence.Low,
			Reasons:      []Reason{ReasonNoRecentRecords},
		},
	}

	tests := []struct {
		name string
		days []signal.Day
	}{
		{"기록이 하나도 없다(nil)", nil},
		{"기록이 하나도 없다(빈 목록)", []signal.Day{}},
		{"첫 대화 날이 기준일보다 뒤다", diary(t, "2026-09-21", "OOOO....")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compute(tt.days, asOf, params.Default())

			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	t.Run("흐름에 없는 날짜를 물으면 없다고 답한다", func(t *testing.T) {
		got, err := Compute(nil, asOf, params.Default())
		require.NoError(t, err)

		_, ok := got.At(asOf)
		assert.False(t, ok)
	})
}

func TestResultAt(t *testing.T) {
	days := diary(t, "2026-06-01", times(3, "OOOO....")...)
	got, err := Compute(days, mustDate(t, "2026-06-05"), params.Default())
	require.NoError(t, err)

	tests := []struct {
		name   string
		date   recorddate.Date
		wantOK bool
	}{
		{"첫 대화 날", mustDate(t, "2026-06-01"), true},
		{"대화하지 않은 날도 흐름에 있다", mustDate(t, "2026-06-04"), true},
		{"기준일", mustDate(t, "2026-06-05"), true},
		{"첫 대화 날의 전날", mustDate(t, "2026-05-31"), false},
		{"기준일의 다음 날", mustDate(t, "2026-06-06"), false},
		{"빈 날짜", recorddate.Date{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, ok := got.At(tt.date)

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.date, pt.Date)
			}
		})
	}
}

func TestComputeErrors(t *testing.T) {
	asOf := mustDate(t, "2026-06-30")
	days := diary(t, "2026-06-01", times(10, "OOOO....")...)

	t.Run("기준일이 비었다", func(t *testing.T) {
		got, err := Compute(days, recorddate.Date{}, params.Default())

		require.ErrorIs(t, err, ErrNoAsOf)
		assert.Equal(t, Result{}, got)
	})

	t.Run("조정 값이 틀렸다", func(t *testing.T) {
		p := params.Default()
		p.Stage.SustainedStage2Days = 0

		got, err := Compute(days, asOf, p)

		var fieldErr *params.FieldError
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Stage.SustainedStage2Days", fieldErr.Field)
		assert.Equal(t, Result{}, got)
	})

	t.Run("날짜순이 아니다", func(t *testing.T) {
		unsorted := slices.Clone(days)
		slices.Reverse(unsorted)

		_, err := Compute(unsorted, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrUnsortedDays)
	})

	t.Run("같은 날짜가 둘이다", func(t *testing.T) {
		duplicated := append(slices.Clone(days), days[len(days)-1])

		_, err := Compute(duplicated, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrDuplicateDate)
	})

	t.Run("기준일 뒤의 하루가 틀려도 오류다", func(t *testing.T) {
		// 어느 기준일로 돌리든 같은 기록은 똑같이 받거나 똑같이 거절한다.
		broken := append(slices.Clone(days), signal.Day{
			Date:       mustDate(t, "2026-07-15"),
			Judgements: [signal.ItemCount]signal.Judgement{{Status: signal.Observed, Explicitness: signal.None}},
		})

		_, err := Compute(broken, asOf, params.Default())

		require.ErrorIs(t, err, signal.ErrInconsistentJudgement)
	})

	t.Run("창의 첫날이 다룰 수 있는 날짜 범위를 벗어난다", func(t *testing.T) {
		ancient := diary(t, "0001-01-03", "OOOO....")

		_, err := Compute(ancient, mustDate(t, "0001-01-05"), params.Default())

		require.ErrorIs(t, err, score.ErrInvalidWindow)
	})
}

func TestComputeLeavesInputUntouched(t *testing.T) {
	days := diary(t, "2026-06-01", join(times(10, "OOOO...."), silence(3), times(4, "xxOO...."))...)
	before := slices.Clone(days)

	_, err := Compute(days, mustDate(t, "2026-06-20"), params.Default())

	require.NoError(t, err)
	assert.Equal(t, before, days)
}

// 지난 날짜를 기준일로 다시 돌리면 그날까지의 흐름이 똑같이 나와야 한다.
// 기준일 뒤의 기록이 지난 날의 단계를 바꾸면 "그날 알 수 있었던 기록만으로 구한 값"이 아니게 된다.
func TestComputeReplaysThePastFaithfully(t *testing.T) {
	days := mixedRecords(t)
	last := days[len(days)-1].Date

	full, err := Compute(days, last, params.Default())
	require.NoError(t, err)

	for _, offset := range []int{0, 6, 13, 14, 15, 30, 59, 60, 61, 100} {
		asOf := full.From.AddDays(offset)
		t.Run("기준일 "+asOf.String(), func(t *testing.T) {
			got, err := Compute(days, asOf, params.Default())
			require.NoError(t, err)

			assert.Equal(t, full.Series[:offset+1], got.Series)
			assert.Equal(t, full.Series[offset], got.State)
		})
	}
}

// 하루를 돌릴 때 창 안의 날만 잘라 넘기는 것은 빠르게 하려는 것일 뿐이다.
// 날짜마다 기록 전체를 넘겨 따로 계산한 값과 같아야 한다.
func TestComputeMatchesIndependentCalculations(t *testing.T) {
	shortWindow := params.Default()
	shortWindow.Window.Days = 10
	shortWindow.Window.MinConversationDays = 5
	shortWindow.Score.ItemScore2MinDays = 5
	shortWindow.Score.ItemScore3MinDays = 9

	settings := []struct {
		name string
		p    params.Params
	}{
		{"기본값", params.Default()},
		{"창을 열흘로 줄인 값", shortWindow},
	}
	for _, tt := range settings {
		t.Run(tt.name, func(t *testing.T) {
			days := mixedRecords(t)
			p := tt.p

			got, err := Compute(days, days[len(days)-1].Date, p)
			require.NoError(t, err)

			for _, pt := range got.Series {
				estimated, err := score.Compute(days, pt.Date, p)
				require.NoError(t, err)
				reliability, err := confidence.Compute(days, pt.Date, p)
				require.NoError(t, err)
				base, err := baseline.Compute(days, pt.Date, p)
				require.NoError(t, err)
				change, err := cusum.Run(days, base, p)
				require.NoError(t, err)

				total, hasScore := estimated.Score()
				assert.Equal(t, estimated.ConversationDays, pt.ConversationDays, pt.Date.String())
				assert.Equal(t, reliability.ConversationDays, pt.ConversationDays, pt.Date.String())
				assert.Equal(t, !hasScore, pt.Insufficient, pt.Date.String())
				assert.Equal(t, total, pt.Score, pt.Date.String())
				assert.Equal(t, reliability.Level, pt.Confidence, pt.Date.String())
				assert.Equal(t, change.State.Detected, pt.Detected, pt.Date.String())
			}
		})
	}
}

// 어떤 기록에서도 지켜져야 하는 약속을 흐름 전체에서 확인한다.
func TestComputeInvariants(t *testing.T) {
	settings := map[string]params.Params{"기본값": params.Default()}
	loose := params.Default()
	loose.CUSUM.MaxStep, loose.CUSUM.MaxS = 0, 0
	loose.Stage.SustainedStage2Days = 5
	settings["변화 탐지의 상한과 천장을 끄고 이어진 일수를 짧게 잡은 값"] = loose

	histories := map[string][]signal.Day{
		"여러 시기가 이어진 기록":    mixedRecords(t),
		"규칙마다 한 번씩 걸리는 기록": everyRuleRecords(t),
	}

	for name, p := range settings {
		t.Run(name, func(t *testing.T) {
			seen := map[Stage]bool{}
			seenReasons := map[Reason]bool{}
			for historyName, days := range histories {
				got, err := Compute(days, days[len(days)-1].Date.AddDays(20), p)
				require.NoError(t, err)
				require.NotEmpty(t, got.Series)

				recorded := map[recorddate.Date]bool{}
				for _, day := range days {
					recorded[day.Date] = true
				}

				var previous Point
				for _, pt := range got.Series {
					name := historyName + " " + pt.Date.String()
					checkPointInvariants(t, name, previous, pt, recorded[pt.Date])
					seen[pt.Stage] = true
					for _, reason := range pt.Reasons {
						seenReasons[reason] = true
					}
					previous = pt
				}
				assert.Equal(t, previous, got.State, historyName)
			}

			// 시험용 기록이 규칙을 고루 건드리는지 확인한다. 그렇지 않으면 위의 확인이 빈말이 된다.
			for _, s := range []Stage{Everyday, Reflection, Suggestion, Recommendation} {
				assert.True(t, seen[s], "%s 단계가 한 번은 나와야 한다", s)
			}
			for _, reason := range AllReasons() {
				assert.True(t, seenReasons[reason], "%s 조건이 한 번은 나와야 한다", reason)
			}
		})
	}
}

// checkPointInvariants는 하루의 값이 전날의 값과 앞뒤가 맞는지 본다.
func checkPointInvariants(t *testing.T, name string, previous, pt Point, recorded bool) {
	t.Helper()

	assert.True(t, pt.Stage.Valid(), name)
	assert.True(t, pt.Raw.Valid(), name)
	assert.NotNil(t, pt.Reasons, name)
	assert.True(t, slices.IsSorted(pt.Reasons), "조건은 규칙의 순서대로다: %s", name)
	assert.Equal(t, recorded, pt.HasRecord, name)

	assert.Equal(t, pt.Raw > pt.Stage, pt.Held, "묶였다는 표시는 오르려던 단계가 더 높을 때만 켜진다: %s", name)
	assert.Equal(t, pt.Held, pt.HeldBy() != 0, "묶인 날에는 까닭이 하나 적힌다: %s", name)
	switch pt.HeldBy() {
	case ReasonHeldOneStepPerDay:
		assert.Equal(t, previous.Stage+1, pt.Stage, "하루에 한 단계는 올랐다: %s", name)
	case ReasonHeldNoRecordToday, ReasonHeldLowConfidence, ReasonHeldInsufficientRecords:
		assert.Equal(t, previous.Stage, pt.Stage, "묶인 날은 전날의 단계다: %s", name)
	}
	assert.Equal(t, pt.HeldBy() == ReasonHeldNoRecordToday, pt.Held && !pt.HasRecord && !pt.Insufficient && pt.Confidence != confidence.Low, name)
	assert.Equal(t, pt.HeldBy() == ReasonHeldLowConfidence, pt.Held && !pt.Insufficient && pt.Confidence == confidence.Low, name)
	assert.Equal(t, pt.HeldBy() == ReasonHeldInsufficientRecords, pt.Held && pt.Insufficient, name)

	if pt.Stage > previous.Stage {
		assert.Equal(t, previous.Stage+1, pt.Stage, "하루에 한 단계만 오른다: %s", name)
		assert.True(t, pt.HasRecord, "대화하지 않은 날에는 오르지 않는다: %s", name)
		assert.False(t, pt.Insufficient, "기록 부족인 날에는 오르지 않는다: %s", name)
		assert.GreaterOrEqual(t, pt.Confidence, confidence.Medium, "신뢰도가 낮은 날에는 오르지 않는다: %s", name)
	}
	if pt.Insufficient {
		assert.Equal(t, confidence.Low, pt.Confidence, name)
		assert.Zero(t, pt.Score, name)
	}
	switch {
	case pt.ConversationDays == 0:
		assert.Equal(t, Everyday, pt.Stage, name)
		assert.Zero(t, pt.ElevatedDays, name)
		assert.Equal(t, []Reason{ReasonNoRecentRecords}, pt.Reasons, name)
	case pt.Insufficient:
		assert.Equal(t, previous.Stage, pt.Stage, "기록 부족인 날에는 오르지도 내려가지도 않는다: %s", name)
		assert.Equal(t, previous.ElevatedDays, pt.ElevatedDays, "기록 부족인 날은 세지 않고 끊지도 않는다: %s", name)
		assert.Equal(t, pt.Raw < pt.Stage, slices.Contains(pt.Reasons, ReasonCarriedInsufficientRecords), name)
	default:
		assert.LessOrEqual(t, pt.Stage, pt.Raw, "그날의 값으로 정한 것보다 높은 단계는 없다: %s", name)
		if pt.Stage >= Suggestion {
			assert.Equal(t, previous.ElevatedDays+1, pt.ElevatedDays, name)
		} else {
			assert.Zero(t, pt.ElevatedDays, name)
		}
	}
	assert.Equal(t, pt.Stage >= Suggestion, pt.ElevatedDays > 0, name)
}

// everyRuleRecords는 단계의 규칙이 하나씩 걸리도록 짠 기록이다. 시기와 시기 사이에는 창이 빌 만큼 쉰다.
func everyRuleRecords(t *testing.T) []signal.Day {
	t.Helper()
	return diary(t, "2026-03-01", join(
		// 처음부터 여섯 항목이 날마다 관찰된다. 점수가 나오는 날부터 하루에 한 단계씩 오른다.
		// 그 뒤로 쉬는 동안 기록 부족이 되면 단계를 이어 가다가 창이 비면 0단계로 돌아간다.
		times(9, "OOOOOO.."), silence(24),
		// 한 주는 괜찮다고 말하고 다음 한 주는 네 항목이 관찰된 뒤 말이 없다.
		// 괜찮았던 날들이 창에서 빠지면서 대화하지 않은 날에 점수가 오른다. 그날은 단계가 오르지 않는다.
		times(7, "xxxxxxxx"), times(7, "OOOOxxxx"), silence(24),
		// 잠과 기운 이야기만 해서 신뢰도가 낮다.
		times(10, "..OO...."), silence(24),
		// 여덟 항목이 모두 관찰되는 사흘로 변화 감지가 켜지지만 기록 부족이라 오르지 못한다.
		// 이어서 네 항목이 3주 넘게 관찰된다. 점수가 2단계 구간으로 내려온 뒤에도 오래 이어져 3단계가 된다.
		times(3, "OOOOOOOO"), times(25, "OOOOxxxx"),
	)...)
}

// mixedRecords는 여러 시기가 이어진 넉 달 남짓의 기록이다. 규칙을 고루 건드리도록 짰다.
func mixedRecords(t *testing.T) []signal.Day {
	t.Helper()
	return diary(t, "2026-03-01", join(
		// 별일 없는 두 주. 가끔 잠을 설친다.
		[]string{
			"xxxx....", "..O.x...", "xx..x.x.", "", "xxO.....", "....xx..", "xx.x....",
			"xxxx....", "..x.x...", "xx..x.x.", "xxxx....", "", "....xx..", "xxOx....",
		},
		// 조금씩 나빠진다.
		times(4, "xxOO...."), []string{"", "xOOO...."}, times(5, "OOOOx..."), []string{""},
		times(6, "OOOOO.o."), times(16, "OOOOOOo."),
		// 말수가 줄어 잠 이야기만 한다.
		[]string{"", "..O.....", "", "", "..O.....", "", "..O.....", ""},
		times(12, "..OO...."),
		// 한참 쉰다.
		silence(20),
		// 돌아와서 다시 날마다 이야기한다.
		times(9, "OOxOx..."), times(8, "xxxxx.x."),
	)...)
}
