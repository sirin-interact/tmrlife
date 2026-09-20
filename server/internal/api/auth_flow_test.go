package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
)

func TestAuthFlow(t *testing.T) {
	srv := newTestServer(t, nil)
	b := srv.browser(t)

	t.Run("가입 규칙은 로그인 없이 받을 수 있고 서비스의 값과 같다", func(t *testing.T) {
		rec := b.get(pathRequirements)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var got AuthRequirements
		decode(t, rec, &got)
		assert.Equal(t, auth.MinPasswordLength, got.Password.MinLength)
		assert.Equal(t, auth.MaxPasswordBytes, got.Password.MaxBytes)
		assert.Equal(t, auth.MaxDisplayNameLength, got.DisplayNameMaxLength)

		want := auth.CurrentConsents()
		require.Len(t, got.Consents, len(want))
		for i, c := range want {
			assert.Equal(t, c.Kind, string(got.Consents[i].Kind))
			assert.Equal(t, c.Version, got.Consents[i].Version)
		}
	})

	var userID string
	t.Run("가입하면 201과 사용자, 세션 쿠키를 받는다", func(t *testing.T) {
		body := signupBody("  Haneul@Example.COM ", testPassword)
		body["display_name"] = "  하늘  "
		body["timezone"] = "America/New_York"
		rec := b.post(pathSignup, body)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

		var got AuthResponse
		decode(t, rec, &got)
		userID = got.User.ID.String()
		assert.Equal(t, testEmail, got.User.Email, "이메일은 다듬은 꼴로 돌려준다")
		require.NotNil(t, got.User.DisplayName)
		assert.Equal(t, "하늘", *got.User.DisplayName)
		assert.Equal(t, "America/New_York", got.User.Timezone)
		assert.False(t, got.User.IsDemo)
		assert.True(t, got.User.CreatedAt.Time().Equal(baseTime))

		require.NotNil(t, srv.setCookie(rec), "가입과 함께 로그인된다")
		assert.Len(t, rec.Header().Values("Set-Cookie"), 1)
	})

	t.Run("쿠키가 있으면 나와 내 설정을 받는다", func(t *testing.T) {
		rec := b.get(pathMe)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

		var got Me
		decode(t, rec, &got)
		assert.Equal(t, userID, got.User.ID.String())
		assert.Equal(t, testEmail, got.User.Email)
		assert.Equal(t, SettingsSummary{
			ReminderEnabled: true,
			ReminderTime:    "20:00",
			DefaultMode:     ConversationModeVoice,
			AnalysisEnabled: true,
			MemoryEnabled:   true,
			MoodPickEnabled: false,
		}, got.Settings)
		assert.Contains(t, rec.Body.String(), `"created_at":"2026-09-20T12:00:00.000Z"`, "시각은 UTC, 밀리초 세 자리로 고정된 꼴이다")
		assert.Empty(t, rec.Header().Values("Set-Cookie"), "통하는 세션의 쿠키는 건드리지 않는다")
	})

	t.Run("로그아웃하면 204와 함께 쿠키가 지워지고 세션이 끊긴다", func(t *testing.T) {
		token := b.cookies[srv.cookies.name]
		require.NotEmpty(t, token)

		rec := b.post(pathLogout, nil)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.Empty(t, rec.Body.String())
		cookie := srv.setCookie(rec)
		require.NotNil(t, cookie)
		assert.Negative(t, cookie.MaxAge)
		assert.Empty(t, cookie.Value)
		assert.Empty(t, b.cookies, "브라우저에 쿠키가 남지 않는다")

		// 쿠키만 지운 것이 아니라 서버의 세션도 끊겼는지 본다. 빼돌린 토큰으로 다시 와도 통하지 않아야 한다.
		b.cookies[srv.cookies.name] = token
		requireProblem(t, b.get(pathMe), http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("로그아웃한 뒤에는 나를 볼 수 없다", func(t *testing.T) {
		rec := b.get(pathMe)
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("세션 없이 로그아웃해도 204다", func(t *testing.T) {
		rec := b.post(pathLogout, nil)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.NotNil(t, srv.setCookie(rec), "남아 있을지 모르는 쿠키를 지운다")
	})

	t.Run("틀린 비밀번호와 없는 이메일은 같은 401을 받는다", func(t *testing.T) {
		wrongPassword := b.post(pathLogin, loginBody(testEmail, "틀린 비밀번호입니다 정말로"))
		unknownEmail := b.post(pathLogin, loginBody("nobody@example.com", testPassword))

		p1 := requireProblem(t, wrongPassword, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		p2 := requireProblem(t, unknownEmail, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
		p1.RequestID, p2.RequestID = "", ""
		assert.Equal(t, p1, p2, "어느 쪽이 틀렸는지 응답으로 구분할 수 없어야 한다")
		assert.Nil(t, srv.setCookie(wrongPassword))
	})

	t.Run("로그인하면 200과 사용자, 새 세션 쿠키를 받는다", func(t *testing.T) {
		rec := b.post(pathLogin, loginBody("HANEUL@example.com", testPassword))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var got AuthResponse
		decode(t, rec, &got)
		assert.Equal(t, userID, got.User.ID.String())
		require.NotNil(t, srv.setCookie(rec))
	})

	t.Run("다시 로그인한 뒤에는 나를 볼 수 있다", func(t *testing.T) {
		rec := b.get(pathMe)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got Me
		decode(t, rec, &got)
		assert.Equal(t, userID, got.User.ID.String())
	})

	t.Run("로그인한 채로 다시 로그인하면 옛 세션은 끊기고 토큰이 바뀐다", func(t *testing.T) {
		old := b.cookies[srv.cookies.name]
		rec := b.post(pathLogin, loginBody(testEmail, testPassword))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotEqual(t, old, b.cookies[srv.cookies.name])

		stale := srv.browser(t)
		stale.cookies[srv.cookies.name] = old
		requireProblem(t, stale.get(pathMe), http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("접근 로그에는 누구의 요청인지가 식별자로 남는다", func(t *testing.T) {
		var found bool
		for _, line := range srv.logs.lines(t, "request") {
			if line["route"] == pathMe && line["user_id"] == userID {
				found = true
				assert.NotEmpty(t, line["session_id"])
			}
		}
		assert.True(t, found, "로그인한 요청의 접근 로그에 user_id가 있어야 한다")
	})

	t.Run("응답에는 비밀번호 해시와 세션 토큰이 실리지 않는다", func(t *testing.T) {
		var passwordHash string
		require.NoError(t, srv.pool.QueryRow(t.Context(), `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&passwordHash))
		require.NotEmpty(t, passwordHash)
		token := b.cookies[srv.cookies.name]
		require.NotEmpty(t, token)

		bodies := srv.bodies.String()
		require.Contains(t, bodies, testEmail, "본문을 제대로 모으고 있는지부터 확인한다")
		for name, secret := range map[string]string{
			"비밀번호 해시":     passwordHash,
			"해시의 앞머리":     "$argon2",
			"세션 토큰":       token,
			"비밀번호":        testPassword,
			"해시라는 필드 이름":  "password_hash",
			"토큰이라는 필드 이름": "token",
		} {
			assert.NotContains(t, bodies, secret, name)
		}
	})

	t.Run("로그에는 이메일, 비밀번호, 토큰이 남지 않는다", func(t *testing.T) {
		token := b.cookies[srv.cookies.name]
		raw, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)
		tokenHash := sha256.Sum256(raw)

		logs := srv.logs.String()
		require.Contains(t, logs, `"msg":"user logged in"`, "로그를 제대로 모으고 있는지부터 확인한다")
		for name, secret := range map[string]string{
			"이메일":         testEmail,
			"이메일의 앞부분":    "haneul",
			"대문자로 보낸 이메일": "HANEUL",
			"없는 이메일":      "nobody@example.com",
			"비밀번호":        testPassword,
			"틀린 비밀번호":     "틀린 비밀번호입니다",
			"세션 토큰":       token,
			"토큰의 해시":      hex.EncodeToString(tokenHash[:]),
			"부를 이름":       "하늘",
			"클라이언트 주소":    "192.0.2.1",
		} {
			assert.NotContains(t, logs, secret, name)
		}
		assert.NotContains(t, logs, "Mozilla", "브라우저 정보도 로그에 남기지 않는다")
	})
}

func TestSignupRejections(t *testing.T) {
	srv := newTestServer(t, nil)
	b := srv.browser(t)
	b.signup(testEmail)
	b = srv.browser(t)

	t.Run("이미 가입된 이메일이면 409 email_taken이다", func(t *testing.T) {
		rec := b.post(pathSignup, signupBody("Haneul@example.com", testPassword))
		p := requireProblem(t, rec, http.StatusConflict, ProblemCodeEmailTaken)
		assert.Nil(t, p.Reasons)
		assert.Nil(t, srv.setCookie(rec))
		assert.NotContains(t, rec.Body.String(), "example.com", "보낸 값을 되돌려 싣지 않는다")
	})

	passwordTests := []struct {
		name     string
		email    string
		password string
		want     []PasswordReason
	}{
		{"짧은 비밀번호는 too_short다", "a@example.com", "짧아요", []PasswordReason{PasswordReasonTooShort}},
		{"흔한 비밀번호는 too_common이다", "b@example.com", "1234567890", []PasswordReason{PasswordReasonTooCommon}},
		{"이메일과 같은 비밀번호는 matches_email이다", "someone.long@example.com", "someone.long@example.com", []PasswordReason{PasswordReasonMatchesEmail}},
		{"너무 긴 비밀번호는 too_long이다", "c@example.com", strings.Repeat("가", 50), []PasswordReason{PasswordReasonTooLong}},
	}
	for _, tt := range passwordTests {
		t.Run(tt.name, func(t *testing.T) {
			rec := b.post(pathSignup, signupBody(tt.email, tt.password))
			p := requireProblem(t, rec, http.StatusUnprocessableEntity, ProblemCodeWeakPassword)
			require.NotNil(t, p.Reasons)
			assert.ElementsMatch(t, tt.want, *p.Reasons)
			assert.Nil(t, srv.setCookie(rec))
			assert.NotContains(t, rec.Body.String(), tt.password, "보낸 비밀번호를 되돌려 싣지 않는다")
		})
	}

	t.Run("동의가 빠지면 422 consent_required이고 어느 동의인지 알려준다", func(t *testing.T) {
		body := signupBody("d@example.com", testPassword)
		body["consents"] = []map[string]string{
			{"kind": "terms", "version": auth.TermsVersion},
			{"kind": "privacy", "version": auth.PrivacyVersion},
		}
		p := requireProblem(t, b.post(pathSignup, body), http.StatusUnprocessableEntity, ProblemCodeConsentRequired)
		require.NotNil(t, p.Consents)
		assert.Equal(t, []ConsentKind{ConsentKindOverseasTransfer, ConsentKindSensitiveData}, p.Consents.Missing)
		assert.Empty(t, p.Consents.Outdated)
	})

	t.Run("동의를 하나도 보내지 않아도 422 consent_required다", func(t *testing.T) {
		body := signupBody("d@example.com", testPassword)
		body["consents"] = []map[string]string{}
		p := requireProblem(t, b.post(pathSignup, body), http.StatusUnprocessableEntity, ProblemCodeConsentRequired)
		require.NotNil(t, p.Consents)
		assert.Len(t, p.Consents.Missing, 4)
	})

	t.Run("옛 판의 동의는 422 consent_required이고 outdated로 알려준다", func(t *testing.T) {
		body := signupBody("e@example.com", testPassword)
		consents, ok := body["consents"].([]map[string]string)
		require.True(t, ok)
		for _, c := range consents {
			if c["kind"] == "privacy" {
				c["version"] = "2020-01-01"
			}
		}
		rec := b.post(pathSignup, body)
		p := requireProblem(t, rec, http.StatusUnprocessableEntity, ProblemCodeConsentRequired)
		require.NotNil(t, p.Consents)
		assert.Empty(t, p.Consents.Missing)
		assert.Equal(t, []ConsentKind{ConsentKindPrivacy}, p.Consents.Outdated)
		assert.NotContains(t, rec.Body.String(), "2020-01-01", "보낸 값을 되돌려 싣지 않는다")
	})

	fieldTests := []struct {
		name   string
		mutate func(body map[string]any)
		want   ProblemField
	}{
		{"꼴이 틀린 이메일은 fields에 email이 담긴다", func(b map[string]any) { b["email"] = "not-an-email" }, ProblemFieldEmail},
		{"줄바꿈이 든 이름은 fields에 display_name이 담긴다", func(b map[string]any) { b["display_name"] = "하늘\n관리자" }, ProblemFieldDisplayName},
		{"모르는 시간대는 fields에 timezone이 담긴다", func(b map[string]any) { b["timezone"] = "Mars/Olympus" }, ProblemFieldTimezone},
	}
	for _, tt := range fieldTests {
		t.Run(tt.name, func(t *testing.T) {
			body := signupBody("f@example.com", testPassword)
			tt.mutate(body)
			p := requireProblem(t, b.post(pathSignup, body), http.StatusUnprocessableEntity, ProblemCodeValidationFailed)
			require.NotNil(t, p.Fields)
			assert.Equal(t, []ProblemField{tt.want}, *p.Fields)
		})
	}

	t.Run("거부된 가입은 계정을 남기지 않는다", func(t *testing.T) {
		var n int
		require.NoError(t, srv.pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&n))
		assert.Equal(t, 1, n)
	})
}

func TestRequestValidation(t *testing.T) {
	srv := newTestServer(t, nil)
	b := srv.browser(t)

	tests := []struct {
		name string
		req  request
	}{
		{"모르는 필드가 있는 가입 요청", request{method: http.MethodPost, path: pathSignup, body: func() map[string]any {
			body := signupBody(testEmail, testPassword)
			body["role"] = "admin"
			return body
		}()}},
		{"모르는 필드가 있는 로그인 요청", request{method: http.MethodPost, path: pathLogin, body: map[string]any{
			"email": testEmail, "password": testPassword, "remember_me": true,
		}}},
		{"동의 안에 모르는 필드가 있는 요청", request{method: http.MethodPost, path: pathSignup, body: map[string]any{
			"email": testEmail, "password": testPassword,
			"consents": []map[string]any{{"kind": "terms", "version": "2026-09-20", "granted_at": "2020-01-01"}},
		}}},
		{"모르는 종류의 동의", request{method: http.MethodPost, path: pathSignup, body: map[string]any{
			"email": testEmail, "password": testPassword,
			"consents": []map[string]any{{"kind": "marketing", "version": "2026-09-20"}},
		}}},
		{"필수 값이 빠진 요청", request{method: http.MethodPost, path: pathLogin, body: map[string]any{"email": testEmail}}},
		{"타입이 틀린 요청", request{method: http.MethodPost, path: pathLogin, body: map[string]any{"email": testEmail, "password": 12345678901}}},
		{"너무 긴 값", request{method: http.MethodPost, path: pathLogin, body: map[string]any{"email": strings.Repeat("a", 400), "password": testPassword}}},
		{"깨진 JSON", request{method: http.MethodPost, path: pathLogin, rawBody: `{"email": "secret-value-in-broken-json`}},
		{"본문이 없는 로그인 요청", request{method: http.MethodPost, path: pathLogin}},
		{"JSON 배열", request{method: http.MethodPost, path: pathLogin, rawBody: `["secret-value-in-array"]`}},
	}
	for _, tt := range tests {
		t.Run(tt.name+"은 400 validation_failed다", func(t *testing.T) {
			rec := b.do(tt.req)
			p := requireProblem(t, rec, http.StatusBadRequest, ProblemCodeValidationFailed)
			assert.Nil(t, p.Fields)
			for _, echoed := range []string{"admin", "remember_me", "marketing", "secret-value", testEmail, "12345678901"} {
				assert.NotContains(t, rec.Body.String(), echoed, "보낸 값을 되돌려 싣지 않는다")
			}
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}

	t.Run("거부된 요청은 계정을 만들지 않고, 보낸 값을 로그에 남기지 않는다", func(t *testing.T) {
		var n int
		require.NoError(t, srv.pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&n))
		assert.Zero(t, n)
		for _, secret := range []string{"secret-value", testEmail, testPassword, "remember_me"} {
			assert.NotContains(t, srv.logs.String(), secret)
		}
	})

	t.Run("JSON이 아닌 본문은 415 unsupported_media_type이다", func(t *testing.T) {
		rec := b.do(request{
			method: http.MethodPost, path: pathLogin,
			rawBody: "email=haneul%40example.com&password=x",
			headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		})
		requireProblem(t, rec, http.StatusUnsupportedMediaType, ProblemCodeUnsupportedMediaType)
	})

	t.Run("charset이 붙은 JSON은 받는다", func(t *testing.T) {
		rec := b.do(request{
			method: http.MethodPost, path: pathLogin, body: loginBody(testEmail, testPassword),
			headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		})
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeInvalidCredentials)
	})

	t.Run("너무 큰 본문은 413 payload_too_large다", func(t *testing.T) {
		huge := `{"email":"a@example.com","password":"` + strings.Repeat("x", int(DefaultBodyLimitBytes)) + `"}`
		rec := b.do(request{method: http.MethodPost, path: pathLogin, rawBody: huge})
		requireProblem(t, rec, http.StatusRequestEntityTooLarge, ProblemCodePayloadTooLarge)
	})

	t.Run("길이를 알리지 않고 흘려보내는 큰 본문도 413이다", func(t *testing.T) {
		huge := `{"email":"a@example.com","password":"` + strings.Repeat("x", int(DefaultBodyLimitBytes)) + `"}`
		rec := b.do(request{method: http.MethodPost, path: pathLogin, rawBody: huge, chunked: true})
		requireProblem(t, rec, http.StatusRequestEntityTooLarge, ProblemCodePayloadTooLarge)
	})

	t.Run("없는 경로는 404 not_found다", func(t *testing.T) {
		rec := b.get("/api/v1/nope")
		requireProblem(t, rec, http.StatusNotFound, ProblemCodeNotFound)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("받지 않는 메서드는 405 method_not_allowed다", func(t *testing.T) {
		requireProblem(t, b.get(pathLogin), http.StatusMethodNotAllowed, ProblemCodeMethodNotAllowed)
	})

	t.Run("로그인하지 않았으면 본문이 틀렸는지보다 먼저 401을 받는다", func(t *testing.T) {
		rec := b.do(request{method: http.MethodGet, path: pathMe, rawBody: `{"unexpected":true}`})
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})
}

func TestExpiredSession(t *testing.T) {
	srv := newTestServer(t, nil)
	b := srv.browser(t)
	b.signup(testEmail)
	token := b.cookies[srv.cookies.name]

	t.Run("쓰는 동안에는 세션이 이어진다", func(t *testing.T) {
		srv.clock.Advance(13 * day)
		require.Equal(t, http.StatusOK, b.get(pathMe).Code)
	})

	t.Run("오래 쓰지 않아 끝난 세션은 401과 함께 쿠키가 지워진다", func(t *testing.T) {
		srv.clock.Advance(14*day + time.Second)
		rec := b.get(pathMe)
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)

		cookie := srv.setCookie(rec)
		require.NotNil(t, cookie, "죽은 토큰을 브라우저가 계속 실어 보내지 않게 지운다")
		assert.Negative(t, cookie.MaxAge)
		assert.Empty(t, cookie.Value)
		assert.Equal(t, "/", cookie.Path)
		assert.True(t, cookie.HttpOnly)
		assert.Empty(t, b.cookies)
	})

	t.Run("로그인이 필요 없는 경로에서도 죽은 쿠키는 지워진다", func(t *testing.T) {
		b.cookies[srv.cookies.name] = token
		rec := b.get(pathRequirements)
		require.Equal(t, http.StatusOK, rec.Code)
		cookie := srv.setCookie(rec)
		require.NotNil(t, cookie)
		assert.Negative(t, cookie.MaxAge)
	})

	t.Run("죽은 쿠키를 든 채로 로그인하면 지우는 쿠키가 아니라 새 쿠키 하나만 받는다", func(t *testing.T) {
		b.cookies[srv.cookies.name] = token
		rec := b.post(pathLogin, loginBody(testEmail, testPassword))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Len(t, rec.Header().Values("Set-Cookie"), 1)
		cookie := srv.setCookie(rec)
		require.NotNil(t, cookie)
		assert.Positive(t, cookie.MaxAge)
		assert.NotEqual(t, token, cookie.Value)
	})

	t.Run("토큰의 꼴이 아닌 쿠키도 지워진다", func(t *testing.T) {
		stranger := srv.browser(t)
		stranger.cookies[srv.cookies.name] = "not-a-token"
		rec := stranger.get(pathMe)
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
		require.NotNil(t, srv.setCookie(rec))
	})

	t.Run("쿠키 없이 온 요청에는 쿠키를 건드리지 않는다", func(t *testing.T) {
		rec := srv.browser(t).get(pathMe)
		requireProblem(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
		assert.Empty(t, rec.Header().Values("Set-Cookie"))
	})
}
