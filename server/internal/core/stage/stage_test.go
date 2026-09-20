package stage

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

func TestStage(t *testing.T) {
	tests := []struct {
		name   string
		stage  Stage
		number int
		id     string
	}{
		{"0단계는 일상이다", Everyday, 0, "everyday"},
		{"1단계는 회고다", Reflection, 1, "reflection"},
		{"2단계는 제안이다", Suggestion, 2, "suggestion"},
		{"3단계는 권유다", Recommendation, 3, "recommendation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.number, int(tt.stage))
			assert.Equal(t, tt.id, tt.stage.String())
			assert.True(t, tt.stage.Valid())

			encoded, err := json.Marshal(tt.stage)
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(tt.number), string(encoded), "JSON에는 숫자로 적힌다")
		})
	}

	t.Run("빈 값은 일상이다", func(t *testing.T) {
		var s Stage

		assert.Equal(t, Everyday, s)
	})

	t.Run("숫자가 클수록 더 다가가는 단계다", func(t *testing.T) {
		assert.Less(t, Everyday, Reflection)
		assert.Less(t, Reflection, Suggestion)
		assert.Less(t, Suggestion, Recommendation)
	})

	t.Run("네 단계 밖의 값은 단계가 아니다", func(t *testing.T) {
		for _, s := range []Stage{-1, 4} {
			assert.False(t, s.Valid())
		}
		assert.Equal(t, "Stage(4)", Stage(4).String())
		assert.Equal(t, "Stage(-1)", Stage(-1).String())
	})
}

func TestFromScore(t *testing.T) {
	t.Run("기본값에서 추정 점수 0부터 24까지", func(t *testing.T) {
		// 0~4는 0단계, 5~9는 1단계, 10~14는 2단계, 15 이상은 3단계다.
		want := map[int]Stage{
			0: Everyday, 1: Everyday, 2: Everyday, 3: Everyday, 4: Everyday,
			5: Reflection, 6: Reflection, 7: Reflection, 8: Reflection, 9: Reflection,
			10: Suggestion, 11: Suggestion, 12: Suggestion, 13: Suggestion, 14: Suggestion,
			15: Recommendation, 16: Recommendation, 17: Recommendation, 18: Recommendation, 19: Recommendation,
			20: Recommendation, 21: Recommendation, 22: Recommendation, 23: Recommendation, 24: Recommendation,
		}
		require.Len(t, want, 25)

		for total, stage := range want {
			assert.Equal(t, stage, FromScore(total, params.Default().Stage), "추정 점수 %d", total)
		}
	})

	t.Run("경계는 조정 값에서 받는다", func(t *testing.T) {
		custom := params.Stage{Stage1MinScore: 3, Stage2MinScore: 8, Stage3MinScore: 20, SustainedStage2Days: 14}

		tests := []struct {
			total int
			want  Stage
		}{
			{2, Everyday},
			{3, Reflection},
			{7, Reflection},
			{8, Suggestion},
			{19, Suggestion},
			{20, Recommendation},
		}
		for _, tt := range tests {
			assert.Equal(t, tt.want, FromScore(tt.total, custom), "추정 점수 %d", tt.total)
		}
	})
}
