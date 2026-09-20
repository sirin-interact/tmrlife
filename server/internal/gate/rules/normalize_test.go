package rules

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"띄어쓰기를 없앤다", "죽고 싶다", "죽고싶다"},
		{"글자마다 띄어 써도 같다", "죽 고 싶 다", "죽고싶다"},
		{"줄바꿈과 탭도 공백이다", "죽고\n싶\t다", "죽고싶다"},
		{"문장 부호를 없앤다", "죽.고,싶-다!!?", "죽고싶다"},
		{"말줄임표와 따옴표를 없앤다", "“죽고… 싶다”", "죽고싶다"},
		{"웃음과 울음의 낱자를 없앤다", "죽고 싶다ㅋㅋㅋ ㅠㅠ", "죽고싶다"},
		{"낱자가 표현 사이에 끼어도 없앤다", "죽고ㅋㅋ싶다", "죽고싶다"},
		{"이모지를 없앤다", "죽고 싶다 😢🙏", "죽고싶다"},
		{"보이지 않는 글자를 없앤다", "죽\u200b고\u2060싶\ufeff다", "죽고싶다"},
		{"한글 채움 문자를 없앤다", "죽고\u3164싶다", "죽고싶다"},
		{"세 번 이상 이어진 글자는 두 번으로 줄인다", "죽고 싶다아아아아", "죽고싶다아아"},
		{"두 번 이어진 글자는 그대로 둔다", "스스로 상상했었었어", "스스로상상했었었어"},
		{"사이에 부호가 끼어도 줄인다", "아.아.아.아", "아아"},
		{"숫자는 줄이지 않는다", "1000번", "1000번"},
		{"전각 글자를 보통 글자로 바꾼다", "ＳＵＩＣＩＤＥ　１０９", "suicide109"},
		{"로마자는 소문자로 바꾼다", "Kill Myself", "killmyself"},
		{"조합형으로 들어온 한글을 완성형으로 모은다", norm.NFD.String("죽고 싶다"), "죽고싶다"},
		{"빈 글", "", ""},
		{"남는 글자가 없는 글", "ㅋㅋㅋ ... !!! 😂", ""},
		{"깨진 바이트는 버린다", "죽고\xff\xfe 싶다", "죽고싶다"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize(tt.in)
			assert.Equal(t, tt.want, got.text)
			assert.Len(t, got.start, len(got.text))
			assert.Len(t, got.end, len(got.text))
		})
	}
}

func TestNormalizeSpan(t *testing.T) {
	t.Run("다듬은 글의 구간을 원문의 구간으로 옮긴다", func(t *testing.T) {
		original := "오늘은… 그냥 죽 고  싶 다, 진짜로"
		n := normalize(original)
		at := strings.Index(n.text, "죽고싶다")
		require.GreaterOrEqual(t, at, 0)

		start, end := n.span(at, at+len("죽고싶다"))
		assert.Equal(t, "죽 고  싶 다", original[start:end])
	})

	t.Run("줄여 없앤 글자도 원문의 구간에 들어간다", func(t *testing.T) {
		original := "죽고 싶다아아아아 정말"
		n := normalize(original)
		at := strings.Index(n.text, "싶다아아")
		require.GreaterOrEqual(t, at, 0)

		start, end := n.span(at, at+len("싶다아아"))
		assert.Equal(t, "싶다아아아아", original[start:end])
	})

	t.Run("정규화로 글자 수가 달라져도 원문의 자리를 가리킨다", func(t *testing.T) {
		original := "㈜ 회사에서 " + norm.NFD.String("죽고 싶다") + " 했어"
		n := normalize(original)
		at := strings.Index(n.text, "죽고싶다")
		require.GreaterOrEqual(t, at, 0)

		start, end := n.span(at, at+len("죽고싶다"))
		assert.Equal(t, norm.NFD.String("죽고 싶다"), original[start:end])
	})

	t.Run("범위를 벗어난 구간에는 빈 구간을 돌려준다", func(t *testing.T) {
		n := normalize("죽고 싶다")
		start, end := n.span(3, 3)
		assert.Equal(t, 0, end-start)
		start, end = n.span(0, len(n.text)+1)
		assert.Equal(t, 0, end-start)
	})
}
