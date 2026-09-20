package stage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReason(t *testing.T) {
	tests := []struct {
		name   string
		reason Reason
		id     string
	}{
		{"추정 점수가 단계를 올렸다", ReasonScore, "score"},
		{"변화 감지 상태여서 1단계가 되었다", ReasonChangeDetected, "change_detected"},
		{"2단계 이상이 오래 이어져 3단계가 되었다", ReasonSustained, "sustained"},
		{"그날 대화한 기록이 없어서 오르지 못했다", ReasonHeldNoRecordToday, "held_no_record_today"},
		{"하루에 한 단계만 올랐다", ReasonHeldOneStepPerDay, "held_one_step_per_day"},
		{"신뢰도가 낮아서 오르지 못했다", ReasonHeldLowConfidence, "held_low_confidence"},
		{"기록 부족이어서 오르지 못했다", ReasonHeldInsufficientRecords, "held_insufficient_records"},
		{"기록 부족이어서 전날의 단계를 이어 갔다", ReasonCarriedInsufficientRecords, "carried_insufficient_records"},
		{"창 안에 대화한 날이 없어 0단계로 돌아갔다", ReasonNoRecentRecords, "no_recent_records"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.reason.Valid())
			assert.Equal(t, tt.id, tt.reason.String())

			parsed, err := ParseReason(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.reason, parsed)

			encoded, err := json.Marshal(tt.reason)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Reason
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.reason, decoded)
		})
	}

	t.Run("표에 빠뜨린 조건이 없다", func(t *testing.T) {
		assert.Len(t, tests, ReasonCount)
		assert.Len(t, AllReasons(), ReasonCount)
	})

	t.Run("묶인 까닭을 적는 조건은 넷이다", func(t *testing.T) {
		var held []Reason
		for _, r := range AllReasons() {
			if r.held() {
				held = append(held, r)
			}
		}
		assert.Equal(t, []Reason{
			ReasonHeldNoRecordToday, ReasonHeldOneStepPerDay, ReasonHeldLowConfidence, ReasonHeldInsufficientRecords,
		}, held)
		assert.False(t, Reason(0).held())
	})

	t.Run("AllReasons는 규칙의 순서이고 숫자도 그 순서로 커진다", func(t *testing.T) {
		all := AllReasons()
		for i := 1; i < len(all); i++ {
			assert.Less(t, all[i-1], all[i])
		}
		assert.Equal(t, ReasonScore, all[0])
		assert.Equal(t, ReasonNoRecentRecords, all[len(all)-1])
	})

	t.Run("식별자에 숫자가 없다", func(t *testing.T) {
		// 점수의 경계나 일수를 이름에 넣으면 조정 값을 바꿨을 때 이름이 거짓이 된다.
		for _, r := range AllReasons() {
			assert.False(t, strings.ContainsAny(r.String(), "0123456789"), r.String())
		}
	})

	t.Run("빈 값과 범위 밖의 값은 조건이 아니다", func(t *testing.T) {
		for _, r := range []Reason{0, -1, ReasonCount + 1} {
			assert.False(t, r.Valid())

			_, err := r.MarshalText()
			require.ErrorIs(t, err, ErrInvalidReason)
		}
		assert.Equal(t, "Reason(0)", Reason(0).String())
		assert.Equal(t, "Reason(10)", Reason(10).String())
	})

	t.Run("모르는 식별자는 읽지 않고 입력받은 글자를 오류에 담지 않는다", func(t *testing.T) {
		for _, id := range []string{"", "Score", " score", "score ", "held", "Reason(1)"} {
			_, err := ParseReason(id)

			require.ErrorIs(t, err, ErrInvalidReason)
			if id != "" {
				assert.NotContains(t, err.Error(), id)
			}
		}
	})

	t.Run("읽지 못하면 값을 바꾸지 않는다", func(t *testing.T) {
		r := ReasonSustained

		err := r.UnmarshalText([]byte("unknown"))

		require.ErrorIs(t, err, ErrInvalidReason)
		assert.Equal(t, ReasonSustained, r)
	})
}
