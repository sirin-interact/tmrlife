package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"
)

func newTestPolicy(t *testing.T) *PasswordPolicy {
	t.Helper()
	policy, err := NewPasswordPolicy()
	require.NoError(t, err)
	return policy
}

func TestPasswordPolicy_Check(t *testing.T) {
	t.Parallel()
	policy := newTestPolicy(t)
	const email = "mina.kim2026@example.com"

	accepted := []struct {
		name     string
		password string
	}{
		{"열 글자면 받는다", "gT7#kq2Lw9"},
		{"한글 열 글자는 서른 바이트지만 글자 수로 세어 받는다", "오늘도수고했어요내일"},
		{"띄어쓰기가 든 문장을 받는다", "저녁에는 산책을 해요"},
		{"128바이트까지 받는다", strings.Repeat("kR8", 42) + "zQ"},
		{"이메일의 일부만 들어 있는 것은 괜찮다", "mina.kim2026-그리고더긴말"},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, policy.Check(tt.password, email))
		})
	}

	rejected := []struct {
		name     string
		password string
		want     []PasswordReason
	}{
		{"아홉 글자는 짧다", "gT7#kq2Lw", []PasswordReason{PasswordTooShort}},
		{"빈 비밀번호는 짧다", "", []PasswordReason{PasswordTooShort}},
		{"한글 아홉 글자는 27바이트여도 짧다", "오늘도수고했어요내", []PasswordReason{PasswordTooShort}},
		{"129바이트는 길다", strings.Repeat("kR8", 43), []PasswordReason{PasswordTooLong}},
		{"한글 마흔세 글자는 129바이트라 길다", strings.Repeat("가", 43), []PasswordReason{PasswordTooLong}},
		{"다듬기 전의 한도를 넘는 입력은 다듬어 보지도 않는다", strings.Repeat("a", 1025), []PasswordReason{PasswordTooLong}},
		{"흔한 비밀번호는 길이가 맞아도 받지 않는다", "password123", []PasswordReason{PasswordTooCommon}},
		{"흔한 비밀번호는 대소문자를 바꿔도 받지 않는다", "PassWord123", []PasswordReason{PasswordTooCommon}},
		{"전각 글자로 적은 흔한 비밀번호도 받지 않는다", "ｐａｓｓｗｏｒｄ１２３", []PasswordReason{PasswordTooCommon}},
		{"한글 낱말을 영문 자판으로 친 것도 받지 않는다", "qlalfqjsgh", []PasswordReason{PasswordTooCommon}},
		{"이메일을 그대로 쓴 것은 받지 않는다", "mina.kim2026@example.com", []PasswordReason{PasswordMatchesEmail}},
		{"이메일을 대문자로 쓴 것도 받지 않는다", "MINA.KIM2026@EXAMPLE.COM", []PasswordReason{PasswordMatchesEmail}},
		{"이메일의 앞부분을 그대로 쓴 것은 받지 않는다", "mina.kim2026", []PasswordReason{PasswordMatchesEmail}},
		{"글자로 읽을 수 없는 바이트가 든 것은 받지 않는다", "valid-prefix-\xff\xfe", []PasswordReason{PasswordInvalidEncoding}},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := policy.Check(tt.password, email)
			require.ErrorIs(t, err, ErrWeakPassword)

			var policyErr *PasswordPolicyError
			require.ErrorAs(t, err, &policyErr)
			assert.Equal(t, tt.want, policyErr.Reasons)
			if len(tt.password) >= 8 {
				assert.NotContains(t, err.Error(), tt.password, "오류 문구에 비밀번호가 들어가면 로그에 그대로 남는다")
			}
		})
	}

	t.Run("어긴 규칙이 여럿이면 모두 알려준다", func(t *testing.T) {
		t.Parallel()
		// 앞부분이 곧 흔한 비밀번호인 주소다.
		err := policy.Check("password123", "password123@example.com")
		var policyErr *PasswordPolicyError
		require.ErrorAs(t, err, &policyErr)
		assert.Equal(t, []PasswordReason{PasswordTooCommon, PasswordMatchesEmail}, policyErr.Reasons)
	})

	t.Run("이메일을 모르면 이메일과 견주는 규칙을 건너뛴다", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, policy.Check("mina.kim2026", ""))
	})
}

func TestPasswordPolicy_SameTextInDifferentUnicodeForms(t *testing.T) {
	t.Parallel()
	policy := newTestPolicy(t)

	// 같은 열 글자를 완성형으로, 그리고 자모를 늘어놓은 꼴로 적은 것이다. 뒤의 것은 90바이트다.
	composed := "오늘도수고했어요내일"
	decomposed := norm.NFD.String(composed)
	require.NotEqual(t, composed, decomposed)
	require.Greater(t, len(decomposed), len(composed))

	t.Run("어느 꼴로 들어와도 길이를 똑같이 센다", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, policy.Check(composed, ""))
		require.NoError(t, policy.Check(decomposed, ""))

		short := "오늘도수고했어요내"
		require.ErrorIs(t, policy.Check(short, ""), ErrWeakPassword)
		require.ErrorIs(t, policy.Check(norm.NFD.String(short), ""), ErrWeakPassword,
			"자모로 세면 스물일곱 글자지만 아홉 글자짜리 비밀번호다")
	})

	t.Run("자모를 늘어놓은 꼴은 바이트가 세 배지만 같은 한도로 받는다", func(t *testing.T) {
		t.Parallel()
		// 완성형으로 126바이트다. 자모로 늘어놓으면 378바이트가 되지만 같은 비밀번호다.
		long := strings.Repeat("가", 42)
		require.NoError(t, policy.Check(long, ""))
		require.NoError(t, policy.Check(norm.NFD.String(long), ""))
	})

	t.Run("흔한 비밀번호 목록의 한글도 어느 꼴로든 걸린다", func(t *testing.T) {
		t.Parallel()
		common := "가나다라마바사아자차"
		for _, form := range []string{common, norm.NFD.String(common)} {
			var policyErr *PasswordPolicyError
			require.ErrorAs(t, policy.Check(form, ""), &policyErr)
			assert.Equal(t, []PasswordReason{PasswordTooCommon}, policyErr.Reasons)
		}
	})
}

func TestCommonPasswordList(t *testing.T) {
	t.Parallel()

	t.Run("실행 파일에 담긴 목록은 수백 개이고 그대로 읽힌다", func(t *testing.T) {
		t.Parallel()
		policy := newTestPolicy(t)
		assert.GreaterOrEqual(t, policy.CommonPasswordCount(), 300)
		assert.Less(t, policy.CommonPasswordCount(), 5000, "작은 목록이어야 한다. 큰 목록은 파일이 아니라 다른 방법으로 다룬다")
	})

	t.Run("목록의 값은 모두 소문자이고 다듬은 꼴이다", func(t *testing.T) {
		t.Parallel()
		// 견줄 때 입력을 소문자로 바꾸므로, 목록에 대문자가 있으면 그 줄은 영영 걸리지 않는다.
		for i, line := range strings.Split(string(commonPasswordsFile), "\n") {
			entry := strings.TrimSpace(line)
			if entry == "" || strings.HasPrefix(entry, "#") {
				continue
			}
			assert.Equal(t, strings.ToLower(norm.NFKC.String(entry)), entry, "%d번째 줄", i+1)
		}
	})

	bad := []struct {
		name string
		list string
	}{
		{"빈 목록", "# 설명만 있다\n\n"},
		{"최소 길이보다 짧아 걸릴 일이 없는 값", "password123\nqwerty\n"},
		{"같은 값이 두 번", "password123\nPassword123\n"},
		{"글자로 읽을 수 없는 값", "password123\n\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8\xf7\xf6\n"},
	}
	for _, tt := range bad {
		t.Run("틀린 목록으로는 만들지 않는다: "+tt.name, func(t *testing.T) {
			t.Parallel()
			policy, err := newPasswordPolicy([]byte(tt.list))
			require.Error(t, err)
			assert.Nil(t, policy)
		})
	}

	t.Run("설명 줄과 빈 줄은 건너뛴다", func(t *testing.T) {
		t.Parallel()
		policy, err := newPasswordPolicy([]byte("# 설명\n\n  password123  \r\n#qwertyuiop\n"))
		require.NoError(t, err)
		assert.Equal(t, 1, policy.CommonPasswordCount())
		require.ErrorIs(t, policy.Check("password123", ""), ErrWeakPassword)
		require.NoError(t, policy.Check("qwertyuiop", ""), "설명으로 막아 둔 줄은 목록에 들지 않는다")
	})
}
