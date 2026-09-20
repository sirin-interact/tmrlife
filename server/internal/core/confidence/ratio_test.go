package confidence

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRatio(t *testing.T) {
	t.Run("소수 값은 분자를 분모로 나눈 값이다", func(t *testing.T) {
		tests := []struct {
			name  string
			ratio Ratio
			want  float64
		}{
			{"14일 중 7일", Ratio{Num: 7, Den: 14}, 0.5},
			{"여덟 항목 중 3개", Ratio{Num: 3, Den: 8}, 0.375},
			{"여덟 항목 중 6개", Ratio{Num: 6, Den: 8}, 0.75},
			{"14일 중 10일", Ratio{Num: 10, Den: 14}, 0.7142857142857143},
			{"하나도 없음", Ratio{Num: 0, Den: 14}, 0},
			{"전부", Ratio{Num: 1, Den: 1}, 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.InDelta(t, tt.want, tt.ratio.Float64(), 1e-15)
			})
		}
	})

	t.Run("빈 값은 계산하지 않았다는 뜻이고 소수 값은 0이다", func(t *testing.T) {
		var ratio Ratio

		assert.True(t, ratio.IsZero())
		assert.Zero(t, ratio.Float64())
		assert.False(t, Ratio{Num: 0, Den: 14}.IsZero(), "0/14는 센 결과가 0인 것이지 빈 값이 아니다")
	})

	t.Run("센 값 그대로 적고 약분하지 않는다", func(t *testing.T) {
		assert.Equal(t, "7/14", Ratio{Num: 7, Den: 14}.String())
		assert.Equal(t, "1/1", Ratio{Num: 1, Den: 1}.String())
		assert.Equal(t, "0/0", Ratio{}.String())
	})

	t.Run("크기는 분모를 서로 곱해 정수로 견준다", func(t *testing.T) {
		tests := []struct {
			name string
			a, b Ratio
			want int
		}{
			{"14분의 7과 8분의 4는 같다", Ratio{Num: 7, Den: 14}, Ratio{Num: 4, Den: 8}, 0},
			{"5분의 2와 10분의 4는 같다", Ratio{Num: 2, Den: 5}, Ratio{Num: 4, Den: 10}, 0},
			{"14분의 0과 8분의 0은 같다", Ratio{Num: 0, Den: 14}, Ratio{Num: 0, Den: 8}, 0},
			{"14분의 14와 1분의 1은 같다", Ratio{Num: 14, Den: 14}, Ratio{Num: 1, Den: 1}, 0},
			// 아래 셋은 1/분모를 먼저 구해 곱한 소수로 견주면 서로 다르다고 나오는 짝이다.
			{"98분의 49와 14분의 7은 같다", Ratio{Num: 49, Den: 98}, Ratio{Num: 7, Den: 14}, 0},
			{"35분의 25와 14분의 10은 같다", Ratio{Num: 25, Den: 35}, Ratio{Num: 10, Den: 14}, 0},
			{"49분의 49와 14분의 14는 같다", Ratio{Num: 49, Den: 49}, Ratio{Num: 14, Den: 14}, 0},
			{"8분의 3은 5분의 2보다 작다", Ratio{Num: 3, Den: 8}, Ratio{Num: 2, Den: 5}, -1},
			{"14분의 10은 10분의 7보다 크다", Ratio{Num: 10, Den: 14}, Ratio{Num: 7, Den: 10}, 1},
			{"14분의 9는 8분의 5보다 크다", Ratio{Num: 9, Den: 14}, Ratio{Num: 5, Den: 8}, 1},
			{"14분의 13은 1분의 1보다 작다", Ratio{Num: 13, Den: 14}, Ratio{Num: 1, Den: 1}, -1},
			{"112분의 111은 1분의 1보다 작다", Ratio{Num: 111, Den: 112}, Ratio{Num: 1, Den: 1}, -1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, tt.a.Compare(tt.b))
				assert.Equal(t, -tt.want, tt.b.Compare(tt.a), "순서를 바꾸면 부호만 바뀐다")
			})
		}
	})
}
