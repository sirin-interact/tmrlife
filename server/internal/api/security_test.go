package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossOriginProtection(t *testing.T) {
	srv := newTestServer(t, nil)

	// 요청마다 다른 이메일로 가입을 시도한다. 통과했다면 201, 막혔다면 403이다.
	n := 0
	attempt := func(t *testing.T, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		n++
		b := srv.browser(t)
		b.bare = true
		return b.do(request{
			method:  http.MethodPost,
			path:    pathSignup,
			body:    signupBody("user"+strconv.Itoa(n)+"@example.com", testPassword),
			headers: headers,
		})
	}

	allowed := []struct {
		name    string
		headers map[string]string
	}{
		{"같은 출처의 브라우저 요청", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": testOrigin}},
		{"주소창에서 직접 보낸 요청", map[string]string{"Sec-Fetch-Site": "none"}},
		{"브라우저가 아닌 클라이언트(두 헤더가 모두 없다)", nil},
		{"옛 브라우저가 같은 호스트에서 보낸 요청", map[string]string{"Origin": "http://example.com"}},
		{"옛 브라우저가 웹앱의 출처에서 보낸 요청(프록시가 Host를 바꿨다)", map[string]string{"Origin": testOrigin}},
		{"같은 사이트의 다른 출처지만 웹앱의 출처로 등록된 곳", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": testOrigin}},
	}
	for _, tt := range allowed {
		t.Run(tt.name+"은 통과한다", func(t *testing.T) {
			rec := attempt(t, tt.headers)
			assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		})
	}

	rejected := []struct {
		name    string
		headers map[string]string
	}{
		{"다른 사이트의 페이지가 보낸 요청", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}},
		{"같은 사이트의 다른 하위 도메인이 보낸 요청", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://blog.localhost:5173"}},
		{"출처를 숨긴 다른 사이트의 요청", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "null"}},
		{"옛 브라우저가 다른 출처에서 보낸 요청", map[string]string{"Origin": "https://evil.example"}},
	}
	for _, tt := range rejected {
		t.Run(tt.name+"은 403 cross_origin_rejected다", func(t *testing.T) {
			rec := attempt(t, tt.headers)
			requireProblem(t, rec, http.StatusForbidden, ProblemCodeCrossOriginRejected)
			assert.Nil(t, srv.setCookie(rec))
			assert.NotContains(t, rec.Body.String(), "evil.example", "보낸 값을 되돌려 싣지 않는다")
		})
	}

	t.Run("막힌 요청은 계정을 만들지 않는다", func(t *testing.T) {
		var users int
		require.NoError(t, srv.pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&users))
		assert.Equal(t, len(allowed), users)
	})

	t.Run("다른 출처에서 온 로그아웃도 막는다", func(t *testing.T) {
		victim := srv.browser(t)
		victim.signup("victim@example.com")

		rec := victim.do(request{method: http.MethodPost, path: pathLogout, headers: map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example",
		}})
		requireProblem(t, rec, http.StatusForbidden, ProblemCodeCrossOriginRejected)
		assert.Equal(t, http.StatusOK, victim.get(pathMe).Code, "세션이 그대로여야 한다")
	})

	t.Run("읽기만 하는 요청은 어느 출처에서 와도 통과한다", func(t *testing.T) {
		b := srv.browser(t)
		rec := b.do(request{method: http.MethodGet, path: pathRequirements, headers: map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example",
		}})
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "다른 출처의 스크립트가 응답을 읽도록 허락하지는 않는다")
	})

	t.Run("상태 확인은 /api 밖에 있어서 이 검사를 거치지 않는다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "403이 아니라 라우터의 405여야 한다")
	})
}

func TestHealthOutsideAPI(t *testing.T) {
	srv := newTestServer(t, nil)

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path+"는 로그인 없이, 죽은 쿠키가 있어도 200이고 쿠키를 건드리지 않는다", func(t *testing.T) {
			b := srv.browser(t)
			b.cookies[srv.cookies.name] = "not-a-token"
			rec := b.get(path)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Empty(t, rec.Header().Values("Set-Cookie"))
		})
	}

	t.Run("/api 밖의 없는 경로도 같은 꼴의 오류를 받는다", func(t *testing.T) {
		rec := srv.browser(t).get("/nope")
		requireProblem(t, rec, http.StatusNotFound, ProblemCodeNotFound)
	})
}

func TestSessionCookieAttributes(t *testing.T) {
	tests := []struct {
		name       string
		cookie     CookieConfig
		wantName   string
		wantSecure bool
	}{
		{
			name:     "개발 설정에서는 Secure 없이 설정의 이름 그대로 심는다",
			cookie:   CookieConfig{Name: "naeil_session", Secure: false, MaxAge: 30 * day},
			wantName: "naeil_session",
		},
		{
			name:       "운영 설정에서는 Secure를 붙이고 이름 앞에 __Host-를 붙인다",
			cookie:     CookieConfig{Name: "naeil_session", Secure: true, MaxAge: 30 * day},
			wantName:   "__Host-naeil_session",
			wantSecure: true,
		},
		{
			name:       "설정의 이름에 이미 __Host-가 있으면 한 번 더 붙이지 않는다",
			cookie:     CookieConfig{Name: "__Host-sid", Secure: true, MaxAge: 30 * day},
			wantName:   "__Host-sid",
			wantSecure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, func(o *Options) {
				o.Cookie = tt.cookie
				o.Production = tt.cookie.Secure
			})
			b := srv.browser(t)

			check := func(t *testing.T, rec *httptest.ResponseRecorder, wantMaxAge int) {
				t.Helper()
				cookies := rec.Result().Cookies()
				require.Len(t, cookies, 1)
				c := cookies[0]
				assert.Equal(t, tt.wantName, c.Name)
				assert.Equal(t, tt.wantSecure, c.Secure)
				assert.True(t, c.HttpOnly, "스크립트가 읽을 수 없어야 한다")
				assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
				assert.Equal(t, "/", c.Path)
				assert.Empty(t, c.Domain, "Domain을 주면 하위 도메인에도 쿠키가 간다")
				assert.Equal(t, wantMaxAge, c.MaxAge)
			}

			signup := b.signup(testEmail)
			check(t, signup, int((30 * day).Seconds()))
			assert.Len(t, signup.Result().Cookies()[0].Value, 43, "256비트 토큰의 base64url 길이다")

			require.Equal(t, http.StatusOK, b.get(pathMe).Code, "심은 이름의 쿠키로 로그인 상태가 이어져야 한다")

			login := b.post(pathLogin, loginBody(testEmail, testPassword))
			require.Equal(t, http.StatusOK, login.Code)
			check(t, login, int((30 * day).Seconds()))

			logout := b.post(pathLogout, nil)
			require.Equal(t, http.StatusNoContent, logout.Code)
			check(t, logout, -1)

			if tt.cookie.Secure {
				assert.NotEmpty(t, signup.Header().Get("Strict-Transport-Security"))
			}
		})
	}

	t.Run("운영 설정에서는 앞머리가 없는 이름의 쿠키를 세션으로 보지 않는다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) {
			o.Cookie = CookieConfig{Name: "naeil_session", Secure: true, MaxAge: 30 * day}
		})
		b := srv.browser(t)
		b.signup(testEmail)
		token := b.cookies["__Host-naeil_session"]
		require.NotEmpty(t, token)

		// 하위 도메인이나 HTTP 응답이 심을 수 있는 것은 앞머리 없는 이름뿐이다.
		planted := srv.browser(t)
		planted.cookies["naeil_session"] = token
		requireProblem(t, planted.get(pathMe), http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("쓸 수 없는 쿠키 설정은 서버를 만들 때 거부한다", func(t *testing.T) {
		for name, cfg := range map[string]CookieConfig{
			"이름이 없다":               {Name: "", MaxAge: day},
			"수명이 없다":               {Name: "sid"},
			"Secure 없이 __Host- 이름": {Name: "__Host-sid", MaxAge: day},
			"쿠키 이름에 쓸 수 없는 글자가 있다": {Name: "naeil session", MaxAge: day},
		} {
			_, err := newSessionCookies(cfg)
			assert.Error(t, err, name)
		}
	})
}

func TestRateLimiting(t *testing.T) {
	limits := RateLimits{
		SignupPerIP:        RateLimit{Burst: 2, Period: 10 * time.Minute},
		LoginPerIP:         RateLimit{Burst: 5, Period: 5 * time.Minute},
		LoginPerEmail:      RateLimit{Burst: 3, Period: 3 * time.Minute},
		LoginPerEmailTotal: RateLimit{Burst: 6, Period: 6 * time.Minute},
		MaxKeys:            100,
	}
	// 계정별 한도만 볼 때는 주소별 한도가 끼어들지 않게 넉넉히 준다.
	accountLimits := limits
	accountLimits.LoginPerIP = RateLimit{Burst: 1000, Period: time.Minute}

	t.Run("가입은 주소별 한도를 넘으면 429이고, 시간이 지나면 다시 된다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = limits })
		b := srv.browser(t)
		b.bare = true

		for i := range 2 {
			rec := b.post(pathSignup, signupBody("u"+strconv.Itoa(i)+"@example.com", testPassword))
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		}
		rec := b.post(pathSignup, signupBody("u2@example.com", testPassword))
		requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		assert.Equal(t, "300", rec.Header().Get("Retry-After"), "10분에 2번이면 토큰 하나가 차는 데 5분이다")
		assert.Nil(t, srv.setCookie(rec))

		t.Run("다른 주소에서는 된다", func(t *testing.T) {
			rec := b.do(request{method: http.MethodPost, path: pathSignup, remoteAddr: "198.51.100.9:4321",
				body: signupBody("other@example.com", testPassword)})
			assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		})

		t.Run("알려준 시간이 되기 직전에는 아직 안 된다", func(t *testing.T) {
			srv.clock.Advance(5*time.Minute - time.Second)
			rec := b.post(pathSignup, signupBody("u2@example.com", testPassword))
			requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
			assert.Equal(t, "1", rec.Header().Get("Retry-After"))
		})

		t.Run("알려준 시간이 지나면 다시 된다", func(t *testing.T) {
			srv.clock.Advance(time.Second)
			rec := b.post(pathSignup, signupBody("u2@example.com", testPassword))
			assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			requireProblem(t, b.post(pathSignup, signupBody("u3@example.com", testPassword)), http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("한도에 걸린 것은 어느 한도인지만 로그에 남는다", func(t *testing.T) {
			lines := srv.logs.lines(t, "rate limit exceeded")
			require.NotEmpty(t, lines)
			assert.Equal(t, "signup_per_ip", lines[0]["limit"])
			assert.Equal(t, "WARN", lines[0]["level"])
			assert.NotEmpty(t, lines[0]["request_id"])
			assert.NotContains(t, srv.logs.String(), "192.0.2.1", "주소는 남기지 않는다")
			assert.NotContains(t, srv.logs.String(), "example.com")
		})
	})

	t.Run("가입은 해시까지 가는 시도만 센다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = limits })
		b := srv.browser(t)
		b.bare = true

		rejected := []struct {
			name   string
			mutate func(body map[string]any)
			status int
			code   ProblemCode
		}{
			{"흔한 비밀번호", func(body map[string]any) { body["password"] = "1234567890" }, http.StatusUnprocessableEntity, ProblemCodeWeakPassword},
			{"빠진 동의", func(body map[string]any) { body["consents"] = []map[string]string{} }, http.StatusUnprocessableEntity, ProblemCodeConsentRequired},
			{"꼴이 틀린 이메일", func(body map[string]any) { body["email"] = "not-an-email" }, http.StatusUnprocessableEntity, ProblemCodeValidationFailed},
			{"모르는 시간대", func(body map[string]any) { body["timezone"] = "Mars/Olympus_Mons" }, http.StatusUnprocessableEntity, ProblemCodeValidationFailed},
		}
		// 한도(2번)보다 훨씬 많이 거부당한다. 화면이 미리 걸러 주지 못하는 흔한 비밀번호가 실제로 가장 잦다.
		for range 3 {
			for _, tt := range rejected {
				body := signupBody("typo@example.com", testPassword)
				tt.mutate(body)
				requireProblem(t, b.post(pathSignup, body), tt.status, tt.code)
			}
		}
		assert.Empty(t, srv.logs.lines(t, "rate limit exceeded"))

		t.Run("거부된 시도가 아무리 많아도 같은 주소의 다음 가입은 된다", func(t *testing.T) {
			rec := b.post(pathSignup, signupBody("first@example.com", testPassword))
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		})

		t.Run("이미 가입된 이메일은 해시를 계산한 뒤에 알려주므로 센다", func(t *testing.T) {
			requireProblem(t, b.post(pathSignup, signupBody("first@example.com", testPassword)), http.StatusConflict, ProblemCodeEmailTaken)
			// 가입 한 번과 가입된 이메일 한 번으로 한도를 다 썼다. 이메일 목록을 확인해 보는 일은 여전히 한도에 걸린다.
			requireProblem(t, b.post(pathSignup, signupBody("first@example.com", testPassword)), http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("한도에 걸린 뒤에도 받지 않을 요청에는 429가 아니라 무엇이 틀렸는지를 알려준다", func(t *testing.T) {
			body := signupBody("typo@example.com", "1234567890")
			requireProblem(t, b.post(pathSignup, body), http.StatusUnprocessableEntity, ProblemCodeWeakPassword)
		})
	})

	t.Run("한 주소에서 계정 하나를 두드리면 그 주소만 막히고, 주인은 다른 주소에서 로그인할 수 있다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = accountLimits })
		srv.browser(t).signup(testEmail)
		attacker := srv.browser(t)
		attacker.bare = true
		const attackerAddr = "198.51.100.10:5000"

		try := func(b *browser, email, password, remoteAddr string) *httptest.ResponseRecorder {
			return b.do(request{method: http.MethodPost, path: pathLogin, remoteAddr: remoteAddr, body: loginBody(email, password)})
		}

		for i := range 3 {
			rec := try(attacker, testEmail, "대입해 보는 비밀번호 "+strconv.Itoa(i), attackerAddr)
			requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		}
		rec := try(attacker, " HANEUL@example.com ", "대입해 보는 비밀번호 3", attackerAddr)
		requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		assert.Equal(t, "60", rec.Header().Get("Retry-After"))
		lines := srv.logs.lines(t, "rate limit exceeded")
		require.Len(t, lines, 1)
		assert.Equal(t, "login_per_email_ip", lines[0]["limit"])

		t.Run("같은 주소에서는 맞는 비밀번호로도 한동안 로그인할 수 없다", func(t *testing.T) {
			requireProblem(t, try(attacker, testEmail, testPassword, attackerAddr), http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("같은 주소에서 다른 계정은 영향을 받지 않는다", func(t *testing.T) {
			rec := try(attacker, "someone-else@example.com", testPassword, attackerAddr)
			requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		})

		t.Run("토큰이 찰 때마다 두드려서 막아 두려 해도 주인은 로그인한다", func(t *testing.T) {
			// 이메일만 알면 되는 공격이다. 기다리라는 시간에 맞춰 1분에 한 번씩, 한 시간 동안 틀린 비밀번호를 넣는다.
			for range 60 {
				srv.clock.Advance(time.Minute)
				requireProblem(t, try(attacker, testEmail, "대입해 보는 비밀번호", attackerAddr), http.StatusUnauthorized, ProblemCodeInvalidCredentials)
				requireProblem(t, try(attacker, testEmail, "대입해 보는 비밀번호", attackerAddr), http.StatusTooManyRequests, ProblemCodeRateLimited)
			}
			owner := srv.browser(t)
			rec := try(owner, testEmail, testPassword, "203.0.113.50:5000")
			assert.Equal(t, http.StatusOK, rec.Code, "남이 한 주소에서 두드리는 것만으로 주인이 잠기면 안 된다: %s", rec.Body.String())
			assert.Equal(t, http.StatusOK, owner.get(pathMe).Code)
		})
	})

	t.Run("주소를 바꿔 가며 계정 하나를 두드리면 계정의 천장에 걸린다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = accountLimits })
		owner := srv.browser(t)
		owner.signup(testEmail)
		attacker := srv.browser(t)
		attacker.bare = true

		try := func(email, password, remoteAddr string) *httptest.ResponseRecorder {
			return attacker.do(request{method: http.MethodPost, path: pathLogin, remoteAddr: remoteAddr, body: loginBody(email, password)})
		}

		// 한 주소가 자기 한도(3번)를 다 쓰고도 계속 두드린다. 막힌 시도는 천장을 깎지 않아야 한다.
		for i := range 3 {
			requireProblem(t, try(testEmail, "대입해 보는 비밀번호 "+strconv.Itoa(i), "198.51.100.10:5000"), http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		}
		for range 20 {
			requireProblem(t, try(testEmail, "대입해 보는 비밀번호", "198.51.100.10:5000"), http.StatusTooManyRequests, ProblemCodeRateLimited)
		}
		// 천장(6번)에서 남은 것은 3번이다. 막힌 20번이 천장을 깎았다면 아래의 첫 시도부터 429다.
		for i := range 3 {
			rec := try(testEmail, "대입해 보는 비밀번호", "198.51.100."+strconv.Itoa(20+i)+":5000")
			requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		}
		rec := try(testEmail, "대입해 보는 비밀번호", "198.51.100.99:5000")
		requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		assert.Equal(t, "60", rec.Header().Get("Retry-After"))
		lines := srv.logs.lines(t, "rate limit exceeded")
		assert.Equal(t, "login_per_email", lines[len(lines)-1]["limit"])

		t.Run("천장에 걸린 동안에는 새 주소에서 맞는 비밀번호로도 로그인할 수 없다", func(t *testing.T) {
			requireProblem(t, try(testEmail, testPassword, "203.0.113.50:5000"), http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("다른 계정은 영향을 받지 않는다", func(t *testing.T) {
			requireProblem(t, try("someone-else@example.com", testPassword, "203.0.113.50:5000"), http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		})

		t.Run("이미 로그인한 세션은 그대로 쓸 수 있다", func(t *testing.T) {
			assert.Equal(t, http.StatusOK, owner.get(pathMe).Code)
		})

		t.Run("시간이 지나면 다시 로그인할 수 있다", func(t *testing.T) {
			srv.clock.Advance(time.Minute)
			rec := try(testEmail, testPassword, "203.0.113.52:5000")
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	})

	t.Run("로그인은 주소별 한도를 넘으면 계정을 바꿔도 429이고, 막힌 시도는 계정의 한도를 깎지 않는다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = limits })
		srv.browser(t).signup(testEmail)
		attacker := srv.browser(t)
		attacker.bare = true

		for i := range 5 {
			rec := attacker.post(pathLogin, loginBody("target"+strconv.Itoa(i)+"@example.com", testPassword))
			requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		}
		// 주소의 한도가 찬 뒤에 피해자의 계정을 두드린다.
		for range 10 {
			rec := attacker.post(pathLogin, loginBody(testEmail, "대입해 보는 비밀번호"))
			requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		}

		owner := srv.browser(t)
		rec := owner.do(request{method: http.MethodPost, path: pathLogin, remoteAddr: "203.0.113.77:5000", body: loginBody(testEmail, testPassword)})
		assert.Equal(t, http.StatusOK, rec.Code, "막힌 공격자의 시도 때문에 주인이 로그인하지 못하면 안 된다")
	})

	t.Run("한도에 걸린 요청은 해시를 계산하지 않고, 로그아웃과 내 정보에는 한도가 없다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = limits })
		b := srv.browser(t)
		b.signup(testEmail)
		for range 20 {
			require.Equal(t, http.StatusOK, b.get(pathMe).Code)
		}
		for range 20 {
			require.Equal(t, http.StatusOK, b.get(pathRequirements).Code)
		}
	})
}

func TestClientIPBehindProxy(t *testing.T) {
	limits := RateLimits{
		SignupPerIP:   RateLimit{Burst: 1, Period: time.Hour},
		LoginPerIP:    RateLimit{Burst: 1000, Period: time.Minute},
		LoginPerEmail: RateLimit{Burst: 1000, Period: time.Minute},
		MaxKeys:       100,
	}
	limits.LoginPerEmailTotal = RateLimit{Burst: 2000, Period: time.Minute}
	const proxy = "10.42.0.8:41000"

	signupFrom := func(t *testing.T, srv *testServer, n int, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
		t.Helper()
		b := srv.browser(t)
		headers := map[string]string{}
		if forwardedFor != "" {
			headers["X-Forwarded-For"] = forwardedFor
		}
		return b.do(request{method: http.MethodPost, path: pathSignup, remoteAddr: remoteAddr, headers: headers,
			body: signupBody("u"+strconv.Itoa(n)+"@example.com", testPassword)})
	}

	t.Run("믿는 프록시가 없으면 전달된 주소 헤더를 보지 않는다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) { o.RateLimits = limits })
		require.Equal(t, http.StatusCreated, signupFrom(t, srv, 1, "198.51.100.1:5000", "1.1.1.1").Code)
		rec := signupFrom(t, srv, 2, "198.51.100.1:5000", "2.2.2.2")
		requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
	})

	t.Run("믿는 프록시 뒤에서는 프록시가 적어 준 주소로 센다", func(t *testing.T) {
		srv := newTestServer(t, func(o *Options) {
			o.RateLimits = limits
			o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
		})
		require.Equal(t, http.StatusCreated, signupFrom(t, srv, 1, proxy, "203.0.113.5").Code)
		require.Equal(t, http.StatusCreated, signupFrom(t, srv, 2, proxy, "203.0.113.6").Code, "프록시가 같아도 클라이언트가 다르면 따로 센다")

		rec := signupFrom(t, srv, 3, proxy, "203.0.113.5")
		requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)

		t.Run("클라이언트가 지어낸 앞쪽 값으로는 한도를 피하지 못한다", func(t *testing.T) {
			rec := signupFrom(t, srv, 4, proxy, "8.8.8.8, 203.0.113.5")
			requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("프록시를 거치지 않고 온 요청의 헤더는 믿지 않는다", func(t *testing.T) {
			require.Equal(t, http.StatusCreated, signupFrom(t, srv, 5, "198.51.100.1:5000", "203.0.113.200").Code)
			rec := signupFrom(t, srv, 6, "198.51.100.1:5000", "203.0.113.201")
			requireProblem(t, rec, http.StatusTooManyRequests, ProblemCodeRateLimited)
		})

		t.Run("세션에는 프록시가 아니라 클라이언트의 주소가 기록된다", func(t *testing.T) {
			var ips []string
			rows, err := srv.pool.Query(t.Context(), `SELECT host(ip) FROM sessions ORDER BY created_at, id`)
			require.NoError(t, err)
			defer rows.Close()
			for rows.Next() {
				var ip string
				require.NoError(t, rows.Scan(&ip))
				ips = append(ips, ip)
			}
			require.NoError(t, rows.Err())
			assert.Equal(t, []string{"203.0.113.5", "203.0.113.6", "198.51.100.1"}, ips)
		})
	})
}
