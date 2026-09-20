package auth

import (
	"bufio"
	"bytes"
	_ "embed" // 흔한 비밀번호 목록을 실행 파일에 담는다.
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// MinPasswordLength는 비밀번호의 최소 길이다. 글자 수로 센다.
	// 바이트로 세면 한글 네 글자가 열두 바이트라서, 짧은 한글 비밀번호가 규칙을 통과한다.
	MinPasswordLength = 10

	// MaxPasswordBytes는 비밀번호의 최대 길이다. 바이트로 센다.
	// 해시 함수에 들어가는 입력의 크기를 묶어 두려는 값이라 글자 수가 아니라 바이트가 기준이다.
	MaxPasswordBytes = 128

	// maxRawPasswordBytes는 다듬기 전의 입력에 거는 한도다. 다듬는 일 자체에 드는 비용을 묶어 둔다.
	// 자모를 늘어놓은 한글은 다듬으면 3분의 1로 줄어든다. 다듬은 뒤에 한도 안에 드는 입력이 여기서 잘리지 않게 넉넉히 잡는다.
	maxRawPasswordBytes = 1024
)

// PasswordReason은 비밀번호를 받지 않는 이유다. 화면이 이 값으로 안내 문구를 고른다.
type PasswordReason string

const (
	PasswordTooShort PasswordReason = "too_short"
	PasswordTooLong  PasswordReason = "too_long"
	// PasswordTooCommon은 누구나 먼저 넣어 보는 흔한 비밀번호라는 뜻이다.
	PasswordTooCommon PasswordReason = "too_common"
	// PasswordMatchesEmail은 이메일 주소나 그 앞부분을 그대로 썼다는 뜻이다.
	PasswordMatchesEmail PasswordReason = "matches_email"
	// PasswordInvalidEncoding은 글자로 읽을 수 없는 바이트가 들어 있다는 뜻이다.
	PasswordInvalidEncoding PasswordReason = "invalid_encoding"
)

// PasswordPolicyError는 비밀번호가 어긴 규칙을 모두 담는다. 비밀번호 자체는 담지 않는다.
// errors.Is(err, ErrWeakPassword)가 참이다.
type PasswordPolicyError struct {
	Reasons []PasswordReason
}

func (e *PasswordPolicyError) Error() string {
	reasons := make([]string, 0, len(e.Reasons))
	for _, r := range e.Reasons {
		reasons = append(reasons, string(r))
	}
	return ErrWeakPassword.Error() + ": " + strings.Join(reasons, ", ")
}

func (e *PasswordPolicyError) Is(target error) bool {
	return target == ErrWeakPassword
}

//go:embed common_passwords.txt
var commonPasswordsFile []byte

// PasswordPolicy는 새 비밀번호가 받을 만한지 본다. 만든 뒤에는 바뀌지 않으므로 여러 고루틴이 함께 써도 된다.
type PasswordPolicy struct {
	common map[string]struct{}
}

// NewPasswordPolicy는 실행 파일에 담긴 흔한 비밀번호 목록을 읽어 규칙을 만든다.
func NewPasswordPolicy() (*PasswordPolicy, error) {
	return newPasswordPolicy(commonPasswordsFile)
}

func newPasswordPolicy(list []byte) (*PasswordPolicy, error) {
	common := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(list))
	for line := 1; scanner.Scan(); line++ {
		entry := strings.TrimSpace(scanner.Text())
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		key, ok := comparableForm(entry)
		if !ok {
			return nil, fmt.Errorf("auth: common password list line %d is not valid text", line)
		}
		// 최소 길이보다 짧은 값은 길이 규칙에서 먼저 걸러져 여기까지 오지 않는다.
		// 목록에 있으면 막고 있다는 착각만 준다.
		if utf8.RuneCountInString(key) < MinPasswordLength {
			return nil, fmt.Errorf("auth: common password list line %d is shorter than the minimum length and can never match", line)
		}
		if _, dup := common[key]; dup {
			return nil, fmt.Errorf("auth: common password list line %d is a duplicate", line)
		}
		common[key] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("auth: read common password list: %w", err)
	}
	if len(common) == 0 {
		return nil, errors.New("auth: common password list is empty")
	}
	return &PasswordPolicy{common: common}, nil
}

// CommonPasswordCount는 읽어 들인 흔한 비밀번호의 수다.
func (p *PasswordPolicy) CommonPasswordCount() int {
	return len(p.common)
}

// Check는 새 비밀번호를 본다. 받을 만하면 nil, 아니면 *PasswordPolicyError를 돌려준다.
// email은 NormalizeEmail을 거친 주소다. 비어 있으면 이메일과 견주는 규칙을 건너뛴다.
func (p *PasswordPolicy) Check(password, email string) error {
	normalized, reason := normalizePassword(password)
	if reason != "" {
		return &PasswordPolicyError{Reasons: []PasswordReason{reason}}
	}

	var reasons []PasswordReason
	if utf8.RuneCountInString(normalized) < MinPasswordLength {
		reasons = append(reasons, PasswordTooShort)
	}
	if len(normalized) > MaxPasswordBytes {
		reasons = append(reasons, PasswordTooLong)
	}

	folded := strings.ToLower(normalized)
	if _, common := p.common[folded]; common {
		reasons = append(reasons, PasswordTooCommon)
	}
	if email != "" {
		emailFolded := strings.ToLower(email)
		if folded == emailFolded || folded == emailLocalPart(emailFolded) {
			reasons = append(reasons, PasswordMatchesEmail)
		}
	}

	if len(reasons) > 0 {
		return &PasswordPolicyError{Reasons: reasons}
	}
	return nil
}

// normalizePassword는 비밀번호를 해시할 꼴(NFKC)로 맞춘다. 맞출 수 없으면 그 이유를 돌려준다.
//
// 한글은 같은 글자가 완성형 한 글자로도, 자모 여러 개로도 들어온다. 어느 쪽으로 들어왔든 같은 비밀번호로 쳐야 한다.
// 전각 영문자와 숫자처럼 입력기가 바꿔 넣는 글자도 같은 이유로 반각으로 모은다.
func normalizePassword(password string) (normalized string, reason PasswordReason) {
	if len(password) > maxRawPasswordBytes {
		return "", PasswordTooLong
	}
	if !utf8.ValidString(password) {
		return "", PasswordInvalidEncoding
	}
	return norm.NFKC.String(password), ""
}

// comparableForm은 흔한 비밀번호 목록과 견줄 때 쓰는 꼴이다. 대소문자를 가리지 않는다.
func comparableForm(s string) (string, bool) {
	normalized, reason := normalizePassword(s)
	if reason != "" {
		return "", false
	}
	return strings.ToLower(normalized), true
}
