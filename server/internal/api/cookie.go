package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
)

// cookieHostPrefix가 이름 앞에 붙은 쿠키는 브라우저가 세 조건을 모두 지킬 때만 받아 준다.
// Secure가 있어야 하고, Path가 /여야 하고, Domain이 없어야 한다.
const cookieHostPrefix = "__Host-"

// CookieConfig는 세션 쿠키를 어떻게 심을지다. 값은 설정에서 받는다.
type CookieConfig struct {
	// Name은 설정에 적힌 이름이다. Secure가 켜져 있으면 실제 쿠키 이름에는 __Host-가 붙는다.
	Name string
	// Secure를 켜면 HTTPS로만 보낸다. 운영에서는 늘 켠다.
	Secure bool
	// MaxAge는 브라우저가 쿠키를 들고 있는 시간이다. 세션의 전체 수명과 같게 준다.
	MaxAge time.Duration
}

// sessionCookies는 세션 쿠키를 읽고, 심고, 지운다.
//
// 쿠키의 속성:
//   - HttpOnly: 스크립트가 읽지 못한다. 화면에 스크립트를 심는 공격으로도 토큰을 빼 갈 수 없다.
//   - SameSite=Lax: 다른 사이트의 페이지가 보내는 POST와 fetch에는 실리지 않는다. 링크를 눌러 들어오는 GET에는 실려서,
//     메일이나 알림의 링크로 들어와도 로그인이 유지된다. GET은 상태를 바꾸지 않으므로 그래도 안전하다.
//   - Path=/: 앱 전체에서 하나의 쿠키를 쓴다.
//   - Max-Age: 세션의 전체 수명. 세션은 서버에서 먼저 끝날 수 있고, 그때는 서버가 쿠키를 지운다.
//   - Domain은 주지 않는다. 주지 않으면 쿠키를 심은 호스트에만 돌아오고 하위 도메인에는 가지 않는다.
type sessionCookies struct {
	name   string
	secure bool
	maxAge int
}

// newSessionCookies는 실제로 쓸 쿠키 이름을 정한다.
//
// HTTPS 전용이면 이름 앞에 __Host-를 붙인다. 이 앞머리가 없으면 같은 사이트의 다른 하위 도메인이나
// 암호화되지 않은 HTTP 응답이 같은 이름의 쿠키를 심을 수 있고, 그렇게 심은 토큰으로 피해자를 로그인시키는 공격이 된다.
// 앞머리가 붙은 이름은 브라우저가 그런 쿠키를 받지 않으므로, 이 이름으로 온 쿠키는 이 서버가 HTTPS로 심은 것뿐이다.
// 설정에 이미 앞머리를 붙여 적었으면 한 번 더 붙이지 않는다.
//
// HTTP로 여는 개발 환경에서는 붙이지 않는다. Secure 없이 앞머리만 붙이면 브라우저가 쿠키를 버린다.
func newSessionCookies(cfg CookieConfig) (sessionCookies, error) {
	if cfg.Name == "" {
		return sessionCookies{}, errors.New("api: session cookie name is required")
	}
	if cfg.MaxAge < time.Second {
		return sessionCookies{}, errors.New("api: session cookie max age must be at least one second")
	}

	name := cfg.Name
	hasPrefix := strings.HasPrefix(name, cookieHostPrefix)
	switch {
	case cfg.Secure && !hasPrefix:
		name = cookieHostPrefix + name
	case !cfg.Secure && hasPrefix:
		return sessionCookies{}, errors.New("api: a __Host- session cookie name requires a secure cookie")
	}

	c := sessionCookies{name: name, secure: cfg.Secure, maxAge: int(cfg.MaxAge / time.Second)}
	// 쿠키 이름에 쓸 수 없는 글자가 있으면 표준 라이브러리는 조용히 빈 문자열을 만든다. 뜰 때 알아챈다.
	if c.clear() == "" {
		return sessionCookies{}, errors.New("api: session cookie name contains characters that are not allowed")
	}
	return c, nil
}

// read는 요청에 딸려 온 세션 토큰을 돌려준다. 없으면 빈 문자열이다.
func (c sessionCookies) read(req *http.Request) string {
	cookie, err := req.Cookie(c.name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// issue는 새 세션의 토큰을 심는 Set-Cookie 값을 만든다. 토큰을 꺼내 쓰는 곳은 여기 하나다.
func (c sessionCookies) issue(token auth.SessionToken) string {
	return c.build(token.Reveal(), c.maxAge)
}

// clear는 쿠키를 지우는 Set-Cookie 값을 만든다. 속성이 심을 때와 같아야 브라우저가 같은 쿠키로 보고 지운다.
func (c sessionCookies) clear() string {
	return c.build("", -1)
}

func (c sessionCookies) build(value string, maxAge int) string {
	// Secure는 설정을 따른다. HTTP로 여는 개발 환경에서 켜면 브라우저가 쿠키를 받지 않는다. 운영에서는 설정 검증이 끌 수 없게 막는다.
	return (&http.Cookie{ // #nosec G124 -- Secure는 설정에서 오고 운영에서는 늘 켜진다. HttpOnly와 SameSite는 늘 준다.
		Name:     c.name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
}
