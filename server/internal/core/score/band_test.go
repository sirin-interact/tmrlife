package score

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBandIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		band Band
		id   string
	}{
		{"구간 없음", NoBand, "none"},
		{"최소", Minimal, "minimal"},
		{"가벼움", Mild, "mild"},
		{"중간", Moderate, "moderate"},
		{"다소 심함", ModeratelySevere, "moderately_severe"},
		{"심함", Severe, "severe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.band.Valid())
			assert.Equal(t, tt.id, tt.band.String())

			parsed, err := ParseBand(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.band, parsed)

			text, err := tt.band.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tt.id, string(text))

			var decoded Band
			require.NoError(t, decoded.UnmarshalText(text))
			assert.Equal(t, tt.band, decoded)
		})
	}

	t.Run("빈 값은 구간 없음이다", func(t *testing.T) {
		var b Band
		assert.Equal(t, NoBand, b)
	})

	t.Run("숫자가 클수록 높은 구간이다", func(t *testing.T) {
		assert.Less(t, NoBand, Minimal)
		assert.Less(t, Minimal, Mild)
		assert.Less(t, Mild, Moderate)
		assert.Less(t, Moderate, ModeratelySevere)
		assert.Less(t, ModeratelySevere, Severe)
	})
}

func TestBandInvalid(t *testing.T) {
	t.Run("정해진 값이 아니면 식별자처럼 보이지 않게 적고 밖으로 내보내지 않는다", func(t *testing.T) {
		for _, b := range []Band{-1, 6, 99} {
			assert.False(t, b.Valid())
			_, err := b.MarshalText()
			require.ErrorIs(t, err, ErrInvalidBand)
		}
		assert.Equal(t, "Band(6)", Band(6).String())
		assert.Equal(t, "Band(-1)", Band(-1).String())
	})

	unknown := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Mild"},
		{"앞뒤 공백", " mild "},
		{"모르는 이름", "very_severe"},
		{"정해진 값이 아닐 때의 표기", "Band(6)"},
	}
	for _, tt := range unknown {
		t.Run(tt.name+"은 읽지 않는다", func(t *testing.T) {
			parsed, err := ParseBand(tt.id)
			require.ErrorIs(t, err, ErrInvalidBand)
			assert.Equal(t, NoBand, parsed)
			if tt.id != "" {
				assert.NotContains(t, err.Error(), tt.id, "오류 메시지에 입력받은 글자를 담지 않는다")
			}

			b := Severe
			require.ErrorIs(t, b.UnmarshalText([]byte(tt.id)), ErrInvalidBand)
			assert.Equal(t, Severe, b, "읽지 못하면 값을 바꾸지 않는다")
		})
	}
}

func TestBandJSON(t *testing.T) {
	type payload struct {
		Band Band `json:"band"`
	}

	t.Run("JSON에 식별자로 적힌다", func(t *testing.T) {
		encoded, err := json.Marshal(payload{Band: ModeratelySevere})
		require.NoError(t, err)
		assert.JSONEq(t, `{"band":"moderately_severe"}`, string(encoded))

		var decoded payload
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		assert.Equal(t, ModeratelySevere, decoded.Band)
	})

	t.Run("기록 부족일 때의 구간 없음도 그대로 적힌다", func(t *testing.T) {
		encoded, err := json.Marshal(payload{})
		require.NoError(t, err)
		assert.JSONEq(t, `{"band":"none"}`, string(encoded))
	})
}
