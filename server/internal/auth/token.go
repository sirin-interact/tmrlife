package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
)

// tokenLength는 세션 토큰의 바이트 수다. 256비트라 짐작으로 맞힐 수 없다.
const tokenLength = 32

const redactedMarker = "[REDACTED]"

// 쿠키에 적는 꼴이다. 채움 글자가 없는 base64url이라 쿠키 값에 그대로 쓸 수 있다.
// Strict를 걸어 두어야 글자 하나에 한 가지 토큰만 대응한다. 걸지 않으면 끝 글자만 다른 여러 문자열이 같은 토큰으로 읽힌다.
var tokenEncoding = base64.RawURLEncoding.Strict()

// SessionToken은 쿠키에 담아 보낼 세션 토큰이다. 세션을 만든 순간에만 손에 있고, 서버는 그 해시만 저장한다.
// fmt, slog, JSON 어느 길로 나가도 고정 문자열만 보인다. 쿠키에 적을 때만 Reveal로 꺼낸다.
type SessionToken struct {
	value string
}

// Reveal은 쿠키에 적을 값을 돌려준다.
func (t SessionToken) Reveal() string {
	return t.value
}

func (t SessionToken) IsZero() bool {
	return t.value == ""
}

// Format은 %s, %v, %q, %x, %#v 등 모든 서식 동사를 가로챈다.
func (t SessionToken) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, redactedMarker)
}

func (t SessionToken) String() string {
	return redactedMarker
}

func (t SessionToken) LogValue() slog.Value {
	return slog.StringValue(redactedMarker)
}

func (t SessionToken) MarshalText() ([]byte, error) {
	return []byte(redactedMarker), nil
}

// newToken은 토큰을 새로 뽑아, 쿠키에 적을 값과 DB에 저장할 해시를 함께 돌려준다.
func newToken(random io.Reader) (SessionToken, []byte, error) {
	raw := make([]byte, tokenLength)
	if _, err := io.ReadFull(random, raw); err != nil {
		return SessionToken{}, nil, fmt.Errorf("auth: read random session token: %w", err)
	}
	sum := sha256.Sum256(raw)
	token := SessionToken{value: tokenEncoding.EncodeToString(raw)}
	clear(raw)
	return token, sum[:], nil
}

// hashToken은 쿠키로 받은 값을 DB에서 찾을 해시로 바꾼다. 토큰의 꼴이 아니면 false다.
//
// 저장하는 것이 해시라서, 받은 값을 DB의 값과 직접 견주는 일이 없다.
// 견주는 데 걸린 시간으로 저장된 값을 한 글자씩 알아내는 공격이 성립하지 않는다.
func hashToken(presented string) ([]byte, bool) {
	// 꼴이 다른 값은 풀어 보지도 않는다. 아주 긴 쿠키를 보내 일을 시키는 것을 막는다.
	if len(presented) != tokenEncoding.EncodedLen(tokenLength) {
		return nil, false
	}
	raw, err := tokenEncoding.DecodeString(presented)
	if err != nil || len(raw) != tokenLength {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	clear(raw)
	return sum[:], true
}
