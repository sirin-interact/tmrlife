package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	longLocal := strings.Repeat("a", 64)
	longDomain := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 57) + ".com"

	valid := []struct {
		name string
		in   string
		want string
	}{
		{"평범한 주소는 그대로 받는다", "mina@example.com", "mina@example.com"},
		{"앞뒤 공백을 뗀다", "  mina@example.com\t\n", "mina@example.com"},
		{"대문자를 소문자로 바꾼다", "Mina.Kim@Example.COM", "mina.kim@example.com"},
		{"더하기 기호가 든 앞부분을 받는다", "mina+diary@example.com", "mina+diary@example.com"},
		{"하위 도메인과 하이픈을 받는다", "mina@mail.my-company.co.kr", "mina@mail.my-company.co.kr"},
		{"앞부분은 64바이트까지 받는다", longLocal + "@example.com", longLocal + "@example.com"},
		{"전체는 254바이트까지 받는다", longLocal + "@" + longDomain, longLocal + "@" + longDomain},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeEmail(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	require.Len(t, longLocal+"@"+longDomain, 254, "시험이 한도에 딱 맞는 값을 쓰고 있어야 한다")

	invalid := []struct {
		name string
		in   string
	}{
		{"빈 문자열", ""},
		{"공백뿐", "   "},
		{"골뱅이가 없다", "mina.example.com"},
		{"앞부분이 없다", "@example.com"},
		{"도메인이 없다", "mina@"},
		{"도메인에 점이 없다", "mina@localhost"},
		{"이름을 붙인 꼴", "Mina <mina@example.com>"},
		{"따옴표로 묶은 이름을 붙인 꼴", `"Mina Kim" <mina@example.com>`},
		{"꺾쇠로 감싼 꼴", "<mina@example.com>"},
		{"덧붙인 말이 있는 꼴", "mina@example.com (Mina)"},
		{"따옴표로 묶은 앞부분", `"mina kim"@example.com`},
		{"가운데에 공백", "mina kim@example.com"},
		{"주소가 둘", "mina@example.com, joon@example.com"},
		{"대괄호로 적은 IP 주소", "mina@[127.0.0.1]"},
		{"숫자로만 된 도메인", "mina@127.0.0.1"},
		{"점이 연달아 있는 도메인", "mina@example..com"},
		{"점으로 끝나는 도메인", "mina@example.com."},
		{"하이픈으로 시작하는 마디", "mina@-example.com"},
		{"밑줄이 든 도메인", "mina@exa_mple.com"},
		{"점으로 시작하는 앞부분", ".mina@example.com"},
		{"한글이 든 앞부분", "미나@example.com"},
		{"한글 도메인", "mina@한국.kr"},
		{"모양만 같은 다른 글자가 든 도메인", "mina@ex\u0430mple.com"},
		{"줄바꿈을 끼워 넣은 주소", "mina@example.com\r\nBcc: other@example.com"},
		{"NUL이 든 주소", "mina\x00@example.com"},
		{"앞부분이 65바이트", longLocal + "a@example.com"},
		{"전체가 255바이트", longLocal + "@a" + longDomain},
		{"아주 긴 입력", strings.Repeat("a", 100_000) + "@example.com"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeEmail(tt.in)
			require.ErrorIs(t, err, ErrInvalidEmail)
			assert.Empty(t, got)
			if trimmed := strings.TrimSpace(tt.in); len(trimmed) > 3 {
				assert.NotContains(t, err.Error(), trimmed, "오류 문구에 주소가 들어가면 로그에 그대로 남는다")
			}
		})
	}
}

func TestEmailLocalPart(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "mina.kim", emailLocalPart("mina.kim@example.com"))
	assert.Empty(t, emailLocalPart("no-at-sign"))
}
