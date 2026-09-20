package signal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    Status
		id        string
		mentioned bool
	}{
		{"관찰됨", Observed, "observed", true},
		{"관찰되지 않음", NotObserved, "not_observed", true},
		{"언급 없음", NotMentioned, "not_mentioned", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.status.Valid())
			assert.Equal(t, tt.id, tt.status.String(), "저장하는 값과 같아야 한다")
			assert.Equal(t, tt.mentioned, tt.status.Mentioned())

			parsed, err := ParseStatus(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.status, parsed)

			encoded, err := json.Marshal(tt.status)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Status
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.status, decoded)
		})
	}

	t.Run("우선순위는 관찰됨, 관찰되지 않음, 언급 없음 순이다", func(t *testing.T) {
		assert.Greater(t, Observed, NotObserved)
		assert.Greater(t, NotObserved, NotMentioned)
	})

	t.Run("빈 값은 언급 없음이다", func(t *testing.T) {
		var status Status

		assert.Equal(t, NotMentioned, status)
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Observed"},
		{"띄어 쓴 식별자", "not observed"},
		{"명시성의 식별자", "direct"},
		{"뒤에 공백", "observed "},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := ParseStatus(tt.id)

			require.ErrorIs(t, err, ErrInvalidStatus)
			assert.Equal(t, NotMentioned, got, "실패하면 가장 약한 판단을 돌려준다")
		})
	}

	t.Run("정해진 값이 아니면 식별자로 적지 않는다", func(t *testing.T) {
		for _, status := range []Status{-1, Observed + 1} {
			assert.False(t, status.Valid())
			assert.False(t, status.Mentioned())
			assert.Contains(t, status.String(), "Status(")

			_, err := status.MarshalText()
			require.ErrorIs(t, err, ErrInvalidStatus)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		status := Observed

		err := status.UnmarshalText([]byte("seen"))

		require.ErrorIs(t, err, ErrInvalidStatus)
		assert.Equal(t, Observed, status)
	})
}

func TestExplicitness(t *testing.T) {
	tests := []struct {
		name         string
		explicitness Explicitness
		id           string
	}{
		{"직접 언급", Direct, "direct"},
		{"간접 추론", Indirect, "indirect"},
		{"없음", None, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.explicitness.Valid())
			assert.Equal(t, tt.id, tt.explicitness.String(), "저장하는 값과 같아야 한다")

			parsed, err := ParseExplicitness(tt.id)
			require.NoError(t, err)
			assert.Equal(t, tt.explicitness, parsed)

			encoded, err := json.Marshal(tt.explicitness)
			require.NoError(t, err)
			assert.JSONEq(t, `"`+tt.id+`"`, string(encoded))

			var decoded Explicitness
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.explicitness, decoded)
		})
	}

	t.Run("직접 언급이 간접 추론보다, 간접 추론이 없음보다 앞선다", func(t *testing.T) {
		assert.Greater(t, Direct, Indirect)
		assert.Greater(t, Indirect, None)
	})

	t.Run("빈 값은 없음이다", func(t *testing.T) {
		var explicitness Explicitness

		assert.Equal(t, None, explicitness)
	})

	invalid := []struct {
		name string
		id   string
	}{
		{"빈 문자열", ""},
		{"대문자", "Direct"},
		{"판단의 식별자", "observed"},
		{"해당 없음을 다르게 적은 값", "n/a"},
	}
	for _, tt := range invalid {
		t.Run("거부: "+tt.name, func(t *testing.T) {
			got, err := ParseExplicitness(tt.id)

			require.ErrorIs(t, err, ErrInvalidExplicitness)
			assert.Equal(t, None, got)
		})
	}

	t.Run("정해진 값이 아니면 식별자로 적지 않는다", func(t *testing.T) {
		for _, explicitness := range []Explicitness{-1, Direct + 1} {
			assert.False(t, explicitness.Valid())
			assert.Contains(t, explicitness.String(), "Explicitness(")

			_, err := explicitness.MarshalText()
			require.ErrorIs(t, err, ErrInvalidExplicitness)
		}
	})

	t.Run("모르는 식별자를 읽으면 실패하고 값은 그대로다", func(t *testing.T) {
		explicitness := Direct

		err := explicitness.UnmarshalText([]byte("explicit"))

		require.ErrorIs(t, err, ErrInvalidExplicitness)
		assert.Equal(t, Direct, explicitness)
	})
}

// 판단과 명시성의 아홉 가지 조합을 모두 적는다. 저장할 때 거는 제약과 같은 표다.
func TestJudgementValidate(t *testing.T) {
	tests := []struct {
		name         string
		status       Status
		explicitness Explicitness
		wantErr      error
	}{
		{"관찰됨, 직접 언급", Observed, Direct, nil},
		{"관찰됨, 간접 추론", Observed, Indirect, nil},
		{"관찰됨인데 근거가 없다", Observed, None, ErrInconsistentJudgement},
		{"관찰되지 않음, 직접 언급", NotObserved, Direct, nil},
		{"관찰되지 않음, 간접 추론", NotObserved, Indirect, nil},
		{"관찰되지 않음인데 근거가 없다", NotObserved, None, ErrInconsistentJudgement},
		{"언급 없음인데 직접 언급이다", NotMentioned, Direct, ErrInconsistentJudgement},
		{"언급 없음인데 간접 추론이다", NotMentioned, Indirect, ErrInconsistentJudgement},
		{"언급 없음, 근거 없음", NotMentioned, None, nil},
		{"판단이 정해진 값이 아니다", Observed + 1, Direct, ErrInvalidStatus},
		{"명시성이 정해진 값이 아니다", Observed, Direct + 1, ErrInvalidExplicitness},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Judgement{Status: tt.status, Explicitness: tt.explicitness}.Validate()

			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}

	t.Run("빈 값은 언급 없음이고 유효하다", func(t *testing.T) {
		require.NoError(t, Judgement{}.Validate())
		assert.Equal(t, Judgement{Status: NotMentioned, Explicitness: None}, Judgement{})
	})
}
