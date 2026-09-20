package auth

import "errors"

// 아래 오류는 errors.Is로 확인한다. 어느 것도 이메일, 비밀번호, 토큰을 문구에 담지 않는다.
var (
	// ErrInvalidEmail은 이메일 주소의 꼴이 틀렸다는 뜻이다.
	ErrInvalidEmail = errors.New("auth: invalid email address")

	// ErrWeakPassword는 비밀번호가 규칙에 맞지 않는다는 뜻이다.
	// 어느 규칙인지는 errors.As로 *PasswordPolicyError를 꺼내 Reasons에서 본다.
	ErrWeakPassword = errors.New("auth: password does not meet the policy")

	// ErrConsentRequired는 가입에 필요한 동의가 빠졌거나 지금 판이 아니라는 뜻이다.
	// 어느 동의인지는 errors.As로 *ConsentError를 꺼내서 본다.
	ErrConsentRequired = errors.New("auth: required consent is missing or outdated")

	// ErrInvalidDisplayName은 부를 이름이 너무 길거나 쓸 수 없는 글자가 들어 있다는 뜻이다.
	ErrInvalidDisplayName = errors.New("auth: invalid display name")

	// ErrInvalidTimezone은 모르는 시간대 이름이라는 뜻이다.
	ErrInvalidTimezone = errors.New("auth: unknown time zone")

	// ErrEmailTaken은 이미 가입된 이메일이라는 뜻이다.
	ErrEmailTaken = errors.New("auth: email is already registered")

	// ErrInvalidCredentials는 로그인에 실패했다는 뜻이다.
	// 없는 이메일인지, 비밀번호가 틀렸는지, 비밀번호가 없는 계정인지 구분하지 않는다.
	ErrInvalidCredentials = errors.New("auth: invalid email or password")

	// ErrSessionInvalid는 세션이 없거나, 끝났거나, 끊겼다는 뜻이다. 셋을 구분하지 않는다.
	ErrSessionInvalid = errors.New("auth: session is missing, expired or revoked")

	// ErrMalformedHash는 저장된 비밀번호 해시를 읽을 수 없다는 뜻이다. 사용자의 잘못이 아니라 데이터가 깨진 것이다.
	ErrMalformedHash = errors.New("auth: stored password hash is malformed")
)
