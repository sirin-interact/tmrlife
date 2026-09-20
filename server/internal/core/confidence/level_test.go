package confidence

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevel(t *testing.T) {
	tests := []struct {
		name  string
		level Level
		id    string
	}{
		{"낮음", Low, "low"},
		{"보통", Medium, "medium"},
		{"높음", High, "high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.level.Valid())
			assert.Equal(t, tt.id, tt.level.String())

			parsed, err := ParseLevel(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.level, parsed)

			encoded, err := json.Marshal(tt.level)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Level
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.level, decoded)
		})
	}

	t.Run("낮음, 보통, 높음 순으로 커진다", func(t *testing.T) {
		assert.Less(t, Low, Medium)
		assert.Less(t, Medium, High)
	})

	t.Run("빈 값은 낮음이다", func(t *testing.T) {
		var level Level

		assert.Equal(t, Low, level)
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Low"},
		{"앞뒤 공백", " low"},
		{"우리말 이름", "낮음"},
		{"숫자", "0"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			_, err := ParseLevel(tt.id)

			require.ErrorIs(t, err, ErrInvalidLevel)
			if tt.id != "" {
				assert.NotContains(t, err.Error(), tt.id, "오류 메시지에 입력받은 글자를 담지 않는다")
			}
		})
	}

	t.Run("구간이 아닌 값", func(t *testing.T) {
		for _, level := range []Level{-1, High + 1} {
			assert.False(t, level.Valid())
			assert.Contains(t, level.String(), "Level(")

			_, err := level.MarshalText()
			require.ErrorIs(t, err, ErrInvalidLevel)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		level := High

		err := level.UnmarshalText([]byte("certain"))

		require.ErrorIs(t, err, ErrInvalidLevel)
		assert.Equal(t, High, level)
	})
}

func TestComponent(t *testing.T) {
	tests := []struct {
		name      string
		component Component
		id        string
	}{
		{"계산하지 않음", ComponentNone, "none"},
		{"기록 충실도", ComponentRecordCoverage, "record_coverage"},
		{"항목 충족도", ComponentItemCoverage, "item_coverage"},
		{"근거 명시성", ComponentExplicitness, "explicitness"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.component.Valid())
			assert.Equal(t, tt.id, tt.component.String())

			parsed, err := ParseComponent(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.component, parsed)

			encoded, err := json.Marshal(tt.component)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Component
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.component, decoded)
		})
	}

	t.Run("빈 값은 계산하지 않음이다", func(t *testing.T) {
		var component Component

		assert.Equal(t, ComponentNone, component)
	})

	t.Run("정해진 값이 아닌 경우", func(t *testing.T) {
		for _, component := range []Component{-1, ComponentExplicitness + 1} {
			assert.False(t, component.Valid())
			assert.Contains(t, component.String(), "Component(")

			_, err := component.MarshalText()
			require.ErrorIs(t, err, ErrInvalidComponent)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		component := ComponentItemCoverage

		err := component.UnmarshalText([]byte("Item_Coverage"))

		require.ErrorIs(t, err, ErrInvalidComponent)
		assert.Equal(t, ComponentItemCoverage, component)
	})
}
