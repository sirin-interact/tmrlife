package config

import (
	"fmt"
	"log/slog"
)

// redactedMarker는 비밀 값이 출력될 자리에 대신 찍히는 고정 문자열이다.
// 길이조차 드러내지 않으려고 값과 무관한 상수로 둔다.
const redactedMarker = "[REDACTED]"

// Secret은 API 키나 DB 접속 주소처럼 로그에 찍히면 안 되는 문자열이다.
// fmt, slog, JSON 어느 길로 나가도 고정 문자열만 보이고, 실제 값은 Reveal로만 꺼낸다.
// 꺼내는 자리가 코드에 드러나 있어야 검토할 때 찾을 수 있다.
type Secret struct {
	value string
}

func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal은 실제 값을 돌려준다. 외부 서비스에 넘기는 자리에서만 부른다.
func (s Secret) Reveal() string {
	return s.value
}

func (s Secret) IsSet() bool {
	return s.value != ""
}

// Format은 %s, %v, %+v, %#v, %q, %x 등 모든 서식 동사를 가로챈다.
// String만 구현하면 %x나 %#v로 값이 새어 나간다.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, redactedMarker)
}

// String은 fmt를 거치지 않고 직접 부르는 경우를 막는다.
func (s Secret) String() string {
	return redactedMarker
}

// LogValue는 slog가 구조체 안쪽을 들여다보지 못하게 한다.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(redactedMarker)
}

// MarshalText는 JSON, YAML 등으로 직렬화될 때 값을 가린다.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redactedMarker), nil
}

// SecretBytes는 마스터 키처럼 로그에 찍히면 안 되는 바이트열이다.
type SecretBytes struct {
	value []byte
}

// NewSecretBytes는 바이트열을 복사해 감싼다. 호출한 쪽이 원본을 지워도 영향받지 않는다.
func NewSecretBytes(value []byte) SecretBytes {
	copied := make([]byte, len(value))
	copy(copied, value)
	return SecretBytes{value: copied}
}

// Reveal은 실제 바이트열의 복사본을 돌려준다. 받은 쪽이 고쳐도 설정은 바뀌지 않는다.
func (s SecretBytes) Reveal() []byte {
	copied := make([]byte, len(s.value))
	copy(copied, s.value)
	return copied
}

// Len은 키 길이를 돌려준다. 길이 검증에 쓴다.
func (s SecretBytes) Len() int {
	return len(s.value)
}

// Format은 모든 서식 동사를 가로챈다.
func (s SecretBytes) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, redactedMarker)
}

// String은 fmt를 거치지 않고 직접 부르는 경우를 막는다.
func (s SecretBytes) String() string {
	return redactedMarker
}

// LogValue는 slog 출력에서 값을 가린다.
func (s SecretBytes) LogValue() slog.Value {
	return slog.StringValue(redactedMarker)
}

// MarshalText는 직렬화될 때 값을 가린다.
func (s SecretBytes) MarshalText() ([]byte, error) {
	return []byte(redactedMarker), nil
}
