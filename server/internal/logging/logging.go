// Package logging은 구조화 로그를 설정하고, 사용자의 말이 로그에 섞여 들어가지 않게 막는다.
//
// 규칙은 하나다. 로그에는 식별자와 단계만 남긴다. 발화, 일기, 근거 발화, 기억, 검진 응답은 남기지 않는다.
// 이 패키지는 그 규칙을 두 겹으로 돕는다.
//
//  1. Redacted: 사용자의 말을 들고 다니는 문자열 타입이다. 어떤 길로 출력해도 고정 표시와 길이만 나온다.
//  2. 금지된 키: 로그 속성의 이름이 "utterance", "diary"처럼 내용을 담을 법한 이름이면 값을 가린다.
//     Redacted로 감싸는 것을 잊은 경우를 잡는 안전망이다.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"
)

// redactedKeys는 값에 사용자의 말이나 비밀 값이 들어갈 법한 속성 이름이다.
// 이름이 여기에 걸리면 값의 타입과 상관없이 가린다.
// 식별자(user_id, conversation_id 등)와 단계(stage, step)는 걸리지 않는다.
var redactedKeys = map[string]struct{}{
	// 사용자의 말
	"utterance": {}, "utterances": {}, "transcript": {}, "text": {}, "content": {},
	"message_text": {}, "diary": {}, "entry": {}, "evidence": {}, "quote": {},
	"memory": {}, "memories": {}, "answer": {}, "answers": {}, "response_text": {},
	"prompt": {}, "completion": {}, "body": {}, "request_body": {}, "response_body": {},
	"query": {}, "query_string": {}, "raw_query": {},
	// 개인 정보와 비밀 값
	"email": {}, "password": {}, "token": {}, "session_token": {}, "secret": {},
	"authorization": {}, "cookie": {}, "set_cookie": {}, "api_key": {}, "database_url": {}, "dsn": {},
}

const keyRedactedMarker = "[REDACTED by key]"

// New는 JSON 한 줄짜리 로그를 w에 쓰는 로거를 만든다.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewHandler(w, level))
}

// NewHandler는 금지된 키를 가리는 JSON 핸들러를 만든다.
func NewHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if _, banned := redactedKeys[strings.ToLower(a.Key)]; !banned {
		return a
	}
	// 이 함수가 불릴 때는 Redacted가 이미 표시 문자열로 풀려 있다.
	// 값 전체가 표시 꼴과 정확히 같을 때만 그대로 둔다. 길이 정보가 디버깅에 쓰이기 때문이다.
	if a.Value.Kind() == slog.KindString && redactedMarkerPattern.MatchString(a.Value.String()) {
		return a
	}
	return slog.String(a.Key, keyRedactedMarker)
}

var redactedMarkerPattern = regexp.MustCompile(`^\[REDACTED len=[0-9]+\]$`)

// Redacted는 사용자의 말을 담는 문자열이다.
// 일반 string처럼 쓰되, 출력하면 내용 대신 고정 표시와 글자 수만 나온다.
// 내용이 필요한 자리(암호화, AI 호출, 화면 응답)에서는 string(r)로 명시적으로 바꾼다.
type Redacted string

// marker는 출력에 대신 찍히는 문자열이다. 글자 수는 디버깅에 도움이 되고 내용을 드러내지 않는다.
func (r Redacted) marker() string {
	return fmt.Sprintf("[REDACTED len=%d]", utf8.RuneCountInString(string(r)))
}

// LogValue는 slog 출력에서 내용을 가린다.
func (r Redacted) LogValue() slog.Value {
	return slog.StringValue(r.marker())
}

// Format은 %s, %v, %q, %x, %#v 등 모든 서식 동사를 가로챈다.
// String만 구현하면 %q나 %x로 내용이 새어 나간다.
func (r Redacted) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, r.marker())
}

// String은 fmt를 거치지 않고 직접 부르는 경우를 막는다.
func (r Redacted) String() string {
	return r.marker()
}

// MarshalText는 오류 추적 도구나 JSON 덤프로 나갈 때 내용을 가린다.
// API 응답에 내용을 실어야 할 때는 string으로 바꿔 담는다.
func (r Redacted) MarshalText() ([]byte, error) {
	return []byte(r.marker()), nil
}
