package signal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrendRows(t *testing.T) {
	tests := []struct {
		name  string
		row   TrendRow
		id    string
		index int
		items []Item
	}{
		{"기분 줄은 흥미 저하와 우울감을 본다", TrendMood, "mood", 0, []Item{Interest, Mood}},
		{"수면 줄은 수면을 본다", TrendSleep, "sleep", 1, []Item{Sleep}},
		{"에너지 줄은 피로를 본다", TrendEnergy, "energy", 2, []Item{Fatigue}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.row.Valid())
			assert.Equal(t, tt.id, tt.row.String())
			assert.Equal(t, tt.index, tt.row.Index())
			assert.Equal(t, tt.items, tt.row.Items())

			parsed, err := ParseTrendRow(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.row, parsed)

			encoded, err := json.Marshal(tt.row)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded TrendRow
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.row, decoded)
		})
	}

	t.Run("화면에 그리는 순서는 기분, 수면, 에너지다", func(t *testing.T) {
		assert.Equal(t, [TrendRowCount]TrendRow{TrendMood, TrendSleep, TrendEnergy}, AllTrendRows())
	})

	t.Run("받은 항목 목록을 고쳐도 다음 호출에 영향이 없다", func(t *testing.T) {
		items := TrendMood.Items()
		items[0] = Psychomotor

		assert.Equal(t, []Item{Interest, Mood}, TrendMood.Items())
	})

	t.Run("줄에 들지 않는 항목은 식욕, 자기 비난, 집중 곤란, 느려짐 또는 초조다", func(t *testing.T) {
		inRows := map[Item]bool{}
		for _, row := range AllTrendRows() {
			for _, item := range row.Items() {
				assert.False(t, inRows[item], "한 항목이 두 줄에 들지 않는다")
				inRows[item] = true
			}
		}

		var outside []Item
		for _, item := range AllItems() {
			if !inRows[item] {
				outside = append(outside, item)
			}
		}
		assert.Equal(t, []Item{Appetite, SelfBlame, Concentration, Psychomotor}, outside)
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Mood"},
		{"줄이 아닌 항목의 식별자", "fatigue"},
		{"우리말 이름", "기분"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := ParseTrendRow(tt.id)

			require.ErrorIs(t, err, ErrInvalidTrendRow)
			assert.False(t, got.Valid())
		})
	}

	t.Run("줄이 아닌 값", func(t *testing.T) {
		for _, row := range []TrendRow{0, -1, TrendEnergy + 1} {
			assert.False(t, row.Valid())
			assert.Equal(t, -1, row.Index())
			assert.Nil(t, row.Items())
			assert.Contains(t, row.String(), "TrendRow(")

			_, err := row.MarshalText()
			require.ErrorIs(t, err, ErrInvalidTrendRow)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		row := TrendSleep

		err := row.UnmarshalText([]byte("rest"))

		require.ErrorIs(t, err, ErrInvalidTrendRow)
		assert.Equal(t, TrendSleep, row)
	})
}

func TestDayTrendStatus(t *testing.T) {
	date := mustDate(t, "2026-10-03")
	observed := Judgement{Status: Observed, Explicitness: Direct}
	notObserved := Judgement{Status: NotObserved, Explicitness: Direct}

	tests := []struct {
		name       string
		judgements map[Item]Judgement
		wantMood   Status
		wantSleep  Status
		wantEnergy Status
	}{
		{
			name:       "아무 이야기도 없던 날은 세 줄 모두 언급 없음이다",
			judgements: nil,
			wantMood:   NotMentioned, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "흥미 저하만 관찰돼도 기분 줄은 관찰됨이다",
			judgements: map[Item]Judgement{Interest: observed},
			wantMood:   Observed, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "우울감만 관찰돼도 기분 줄은 관찰됨이다",
			judgements: map[Item]Judgement{Mood: observed},
			wantMood:   Observed, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "둘 다 관찰되어도 기분 줄은 한 번만 찍힌다",
			judgements: map[Item]Judgement{Interest: observed, Mood: observed},
			wantMood:   Observed, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "한쪽은 괜찮았고 다른 쪽은 관찰됐으면 기분 줄은 관찰됨이다",
			judgements: map[Item]Judgement{Interest: notObserved, Mood: observed},
			wantMood:   Observed, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "한쪽만 괜찮았다고 나왔고 다른 쪽은 이야기가 없었으면 관찰되지 않음이다",
			judgements: map[Item]Judgement{Interest: notObserved},
			wantMood:   NotObserved, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
		{
			name:       "수면과 에너지는 각자의 항목을 그대로 따른다",
			judgements: map[Item]Judgement{Sleep: observed, Fatigue: notObserved},
			wantMood:   NotMentioned, wantSleep: Observed, wantEnergy: NotObserved,
		},
		{
			name: "줄에 들지 않는 항목은 어느 줄에도 찍히지 않는다",
			judgements: map[Item]Judgement{
				Appetite: observed, SelfBlame: observed, Concentration: observed, Psychomotor: observed,
			},
			wantMood: NotMentioned, wantSleep: NotMentioned, wantEnergy: NotMentioned,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := dayWith(date, tt.judgements)

			assert.Equal(t, tt.wantMood, day.TrendStatus(TrendMood), "기분")
			assert.Equal(t, tt.wantSleep, day.TrendStatus(TrendSleep), "수면")
			assert.Equal(t, tt.wantEnergy, day.TrendStatus(TrendEnergy), "에너지")
		})
	}

	t.Run("줄이 아닌 값을 넘기면 언급 없음이다", func(t *testing.T) {
		day := dayWith(date, map[Item]Judgement{Interest: observed, Sleep: observed, Fatigue: observed})

		assert.Equal(t, NotMentioned, day.TrendStatus(0))
	})
}
