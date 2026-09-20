package crisis

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStage(t *testing.T) {
	tests := []struct {
		name   string
		stage  Stage
		number int
		label  string
	}{
		{"0은 해당 없음이다", StageNone, 0, "none"},
		{"1은 확인이다", StageCheck, 1, "check"},
		{"2는 대응이다", StageRespond, 2, "respond"},
		{"3은 긴급이다", StageUrgent, 3, "urgent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.stage.Valid())
			assert.Equal(t, tt.number, int(tt.stage))
			assert.Equal(t, tt.label, tt.stage.String())

			parsed, err := StageFromInt(tt.number)
			require.NoError(t, err)
			assert.Equal(t, tt.stage, parsed)
		})
	}

	t.Run("빈 값은 해당 없음이다", func(t *testing.T) {
		var stage Stage

		assert.Equal(t, StageNone, stage)
	})

	t.Run("숫자가 클수록 무겁다", func(t *testing.T) {
		assert.Less(t, StageNone, StageCheck)
		assert.Less(t, StageCheck, StageRespond)
		assert.Less(t, StageRespond, StageUrgent)
	})

	t.Run("JSON에는 숫자로 적힌다", func(t *testing.T) {
		encoded, err := json.Marshal(struct{ Stage Stage }{StageRespond})
		require.NoError(t, err)
		assert.JSONEq(t, `{"Stage":2}`, string(encoded))

		var decoded struct{ Stage Stage }
		require.NoError(t, json.Unmarshal([]byte(`{"Stage":3}`), &decoded))
		assert.Equal(t, StageUrgent, decoded.Stage)
	})

	for _, n := range []int{-1, 4, 10} {
		t.Run("거부: 0부터 3이 아닌 숫자", func(t *testing.T) {
			got, err := StageFromInt(n)

			require.ErrorIs(t, err, ErrInvalidStage)
			assert.Equal(t, StageNone, got)
			assert.False(t, Stage(n).Valid())
			assert.Contains(t, Stage(n).String(), "Stage(")
		})
	}
}

func TestDetectedBy(t *testing.T) {
	tests := []struct {
		name string
		by   DetectedBy
		id   string
	}{
		{"어느 쪽도 잡지 않음", DetectedByNone, "none"},
		{"규칙만 잡음", DetectedByRule, "rule"},
		{"AI 판별만 잡음", DetectedByAI, "ai"},
		{"둘 다 잡음", DetectedByBoth, "both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.by.Valid())
			assert.Equal(t, tt.id, tt.by.String())

			parsed, err := ParseDetectedBy(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.by, parsed)

			encoded, err := json.Marshal(tt.by)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded DetectedBy
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.by, decoded)
		})
	}

	t.Run("빈 값은 어느 쪽도 잡지 않음이다", func(t *testing.T) {
		var by DetectedBy

		assert.Equal(t, DetectedByNone, by)
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "AI"},
		{"모르는 이름", "rules"},
		{"우리말 이름", "규칙"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			_, err := ParseDetectedBy(tt.id)

			require.ErrorIs(t, err, ErrInvalidDetectedBy)
		})
	}

	t.Run("정해진 값이 아니면 적지 못한다", func(t *testing.T) {
		for _, by := range []DetectedBy{-1, DetectedByBoth + 1} {
			assert.False(t, by.Valid())
			assert.Contains(t, by.String(), "DetectedBy(")

			_, err := by.MarshalText()
			require.ErrorIs(t, err, ErrInvalidDetectedBy)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		by := DetectedByRule

		err := by.UnmarshalText([]byte("nobody"))

		require.ErrorIs(t, err, ErrInvalidDetectedBy)
		assert.Equal(t, DetectedByRule, by)
	})
}

func TestAdjustment(t *testing.T) {
	tests := []struct {
		name string
		adj  Adjustment
		id   string
	}{
		{"AI 판별 실패 때의 바닥", AdjustAIFailedFloor, "ai_failed_floor"},
		{"상태가 나쁠 때 한 단계 위로", AdjustBadStatePlusOne, "bad_state_plus_one"},
		{"직접 물은 뒤에 다시 나옴", AdjustRepeatAfterDirectAsk, "repeat_after_direct_ask"},
		{"최근 기간 안에 쌓임", AdjustRepeatedInWindow, "repeated_in_window"},
		{"대응 단계 뒤의 민감한 기간", AdjustSensitiveWindow, "sensitive_window"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.adj.Valid())
			assert.Equal(t, tt.id, tt.adj.String())

			parsed, err := ParseAdjustment(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.adj, parsed)

			encoded, err := json.Marshal(tt.adj)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Adjustment
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.adj, decoded)
		})
	}

	t.Run("목록은 규칙을 적용하는 순서다", func(t *testing.T) {
		assert.Equal(t, [AdjustmentCount]Adjustment{
			AdjustAIFailedFloor,
			AdjustBadStatePlusOne,
			AdjustRepeatAfterDirectAsk,
			AdjustRepeatedInWindow,
			AdjustSensitiveWindow,
		}, AllAdjustments())
	})

	t.Run("식별자에는 바꿀 수 있는 횟수나 기간이 들어 있지 않다", func(t *testing.T) {
		for _, adj := range AllAdjustments() {
			assert.NotRegexp(t, `[0-9]`, adj.String())
		}
	})

	t.Run("빈 값은 규칙이 아니다", func(t *testing.T) {
		for _, adj := range []Adjustment{0, -1, AdjustSensitiveWindow + 1} {
			assert.False(t, adj.Valid())
			assert.Contains(t, adj.String(), "Adjustment(")

			_, err := adj.MarshalText()
			require.ErrorIs(t, err, ErrInvalidAdjustment)
		}
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "AI_FAILED_FLOOR"},
		{"모르는 이름", "plus_two"},
		{"앞뒤 공백", " sensitive_window"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := ParseAdjustment(tt.id)

			require.ErrorIs(t, err, ErrInvalidAdjustment)
			assert.False(t, got.Valid())
		})
	}

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		adj := AdjustSensitiveWindow

		err := adj.UnmarshalText([]byte("unknown_rule"))

		require.ErrorIs(t, err, ErrInvalidAdjustment)
		assert.Equal(t, AdjustSensitiveWindow, adj)
	})
}
