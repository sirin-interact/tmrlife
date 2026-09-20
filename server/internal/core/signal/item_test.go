package signal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllItems(t *testing.T) {
	t.Run("여덟 항목이 정해진 순서로 나온다", func(t *testing.T) {
		want := [ItemCount]Item{
			Interest, Mood, Sleep, Fatigue, Appetite, SelfBlame, Concentration, Psychomotor,
		}

		assert.Equal(t, want, AllItems())
		assert.Len(t, AllItems(), 8)
	})

	t.Run("식별자는 저장하는 값과 글자까지 같다", func(t *testing.T) {
		// 저장소의 item 제약에 적힌 순서와 글자 그대로다.
		want := []string{
			"interest", "mood", "sleep", "fatigue", "appetite", "self_blame", "concentration", "psychomotor",
		}

		got := make([]string, 0, ItemCount)
		for _, item := range AllItems() {
			got = append(got, item.String())
		}

		assert.Equal(t, want, got)
	})

	t.Run("Index는 AllItems에서의 자리와 같다", func(t *testing.T) {
		for pos, item := range AllItems() {
			assert.Equal(t, pos, item.Index(), item.String())
			assert.True(t, item.Valid(), item.String())
		}
	})

	t.Run("받은 배열을 고쳐도 다음 호출에 영향이 없다", func(t *testing.T) {
		items := AllItems()
		items[0] = Psychomotor

		assert.Equal(t, Psychomotor, items[0])
		assert.Equal(t, Interest, AllItems()[0])
	})
}

func TestParseItem(t *testing.T) {
	t.Run("모든 항목이 식별자로 되돌아온다", func(t *testing.T) {
		for _, item := range AllItems() {
			got, err := ParseItem(item.String())

			require.NoError(t, err, item.String())
			assert.Equal(t, item, got)
		}
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Sleep"},
		{"앞에 공백", " sleep"},
		{"뒤에 공백", "sleep "},
		{"하이픈으로 적은 식별자", "self-blame"},
		{"자해와 자살 사고는 항목이 아니다", "suicidal_ideation"},
		{"추세 화면의 줄 이름은 항목이 아니다", "energy"},
		{"항목이 아닌 값을 찍은 글", "Item(0)"},
		{"숫자", "3"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := ParseItem(tt.id)

			require.ErrorIs(t, err, ErrInvalidItem)
			assert.False(t, got.Valid(), "실패하면 항목이 아닌 값을 돌려준다")
		})
	}

	t.Run("오류 메시지에 입력받은 글을 담지 않는다", func(t *testing.T) {
		_, err := ParseItem("어제는 한숨도 못 잤어")

		require.Error(t, err)
		assert.NotContains(t, err.Error(), "한숨")
	})
}

func TestItemInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		item Item
		text string
	}{
		{"빈 값은 항목이 아니다", 0, "Item(0)"},
		{"마지막 항목 다음 값", Psychomotor + 1, "Item(9)"},
		{"음수", -1, "Item(-1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.item.Valid())
			assert.Equal(t, -1, tt.item.Index())
			assert.Equal(t, tt.text, tt.item.String(), "식별자처럼 보이지 않게 적는다")

			_, err := tt.item.MarshalText()
			require.ErrorIs(t, err, ErrInvalidItem)
		})
	}
}

func TestItemJSON(t *testing.T) {
	t.Run("값과 객체의 키가 식별자로 적힌다", func(t *testing.T) {
		encoded, err := json.Marshal(map[Item]Item{SelfBlame: Sleep})

		require.NoError(t, err)
		assert.JSONEq(t, `{"self_blame":"sleep"}`, string(encoded))
	})

	t.Run("식별자를 다시 읽는다", func(t *testing.T) {
		var decoded map[Item]Item

		require.NoError(t, json.Unmarshal([]byte(`{"concentration":"psychomotor"}`), &decoded))
		assert.Equal(t, map[Item]Item{Concentration: Psychomotor}, decoded)
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		item := Fatigue

		err := item.UnmarshalText([]byte("tired"))

		require.ErrorIs(t, err, ErrInvalidItem)
		assert.Equal(t, Fatigue, item)
	})

	t.Run("채우지 않은 항목은 밖으로 나가지 못한다", func(t *testing.T) {
		_, err := json.Marshal(struct{ Item Item }{})

		require.Error(t, err)
	})
}
