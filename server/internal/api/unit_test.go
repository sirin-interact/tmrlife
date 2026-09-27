package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestTokenBuckets(t *testing.T) {
	newBuckets := func(t *testing.T, limit RateLimit, maxKeys int) (*tokenBuckets, *clock.Fake) {
		t.Helper()
		clk := clock.NewFake(baseTime)
		b, err := newTokenBuckets(clk, limit, maxKeys)
		require.NoError(t, err)
		return b, clk
	}

	t.Run("한꺼번에 Burst번까지 되고, 그다음은 다음 토큰이 찰 때까지 남은 시간과 함께 거부된다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 3, Period: 30 * time.Second}, 10)
		for range 3 {
			ok, _ := b.take("k")
			require.True(t, ok)
		}
		ok, wait := b.take("k")
		assert.False(t, ok)
		assert.Equal(t, 10*time.Second, wait)

		clk.Advance(4 * time.Second)
		ok, wait = b.take("k")
		assert.False(t, ok)
		assert.Equal(t, 6*time.Second, wait, "거부된 시도는 기다릴 시간을 늘리지 않는다")
	})

	t.Run("알려준 시간만큼 기다리면 정확히 한 번 더 된다", func(t *testing.T) {
		// 나누어떨어지지 않는 값으로 본다. 소수로 계산하면 이런 값에서 반올림 오차가 난다.
		for _, limit := range []RateLimit{
			{Burst: 2, Period: 10 * time.Minute},
			{Burst: 3, Period: time.Minute},
			{Burst: 7, Period: time.Hour},
			{Burst: 30, Period: 5 * time.Minute},
		} {
			b, clk := newBuckets(t, limit, 10)
			for range limit.Burst {
				ok, _ := b.take("k")
				require.True(t, ok)
			}
			for range 50 {
				ok, wait := b.take("k")
				require.False(t, ok)
				require.Positive(t, wait)

				clk.Advance(wait - time.Microsecond)
				ok, _ = b.take("k")
				require.False(t, ok, "%v: 알려준 시간보다 일찍 오면 아직 안 된다", limit)

				clk.Advance(time.Microsecond)
				ok, _ = b.take("k")
				require.True(t, ok, "%v: 알려준 시간에 오면 돼야 한다", limit)
			}
		}
	})

	t.Run("오래 쉬어도 Burst보다 많이 쌓이지 않는다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 2, Period: time.Minute}, 10)
		b.take("k")
		clk.Advance(24 * time.Hour)
		for range 2 {
			ok, _ := b.take("k")
			require.True(t, ok)
		}
		ok, _ := b.take("k")
		assert.False(t, ok)
	})

	t.Run("키마다 따로 센다", func(t *testing.T) {
		b, _ := newBuckets(t, RateLimit{Burst: 1, Period: time.Minute}, 10)
		ok, _ := b.take("a")
		require.True(t, ok)
		ok, _ = b.take("a")
		require.False(t, ok)
		ok, _ = b.take("b")
		assert.True(t, ok)
	})

	t.Run("시계가 뒤로 밀려도 토큰이 거저 생기지 않는다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 1, Period: time.Minute}, 10)
		ok, _ := b.take("k")
		require.True(t, ok)
		clk.Advance(-time.Hour)
		ok, wait := b.take("k")
		assert.False(t, ok)
		assert.Greater(t, wait, time.Hour-time.Second)
	})

	t.Run("기억하는 키는 MaxKeys를 넘지 않고, 가장 오래 쉰 키부터 버린다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 1, Period: time.Hour}, 3)
		for _, key := range []string{"a", "b", "c"} {
			b.take(key)
			clk.Advance(time.Second)
		}
		// a를 다시 써서 가장 오래 쉰 키를 b로 만든다.
		ok, _ := b.take("a")
		require.False(t, ok)

		for i := range 100 {
			b.take("flood-" + strconv.Itoa(i))
			require.LessOrEqual(t, b.size(), 3)
		}
		assert.Equal(t, 3, b.size())
	})

	t.Run("버려지는 것은 가장 오래 쉰 키다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 1, Period: time.Hour}, 2)
		b.take("old")
		clk.Advance(time.Second)
		b.take("recent")
		clk.Advance(time.Second)
		b.take("new")

		ok, _ := b.take("recent")
		assert.False(t, ok, "최근에 쓴 키의 한도는 남아 있어야 한다")
		ok, _ = b.take("old")
		assert.True(t, ok, "버려진 키는 처음 보는 키와 같다")
	})

	t.Run("통이 다시 가득 찬 키는 지나가는 길에 지운다", func(t *testing.T) {
		b, clk := newBuckets(t, RateLimit{Burst: 2, Period: time.Minute}, 1000)
		for i := range 100 {
			b.take("visitor-" + strconv.Itoa(i))
		}
		require.Equal(t, 100, b.size())

		clk.Advance(time.Minute)
		for i := range 60 {
			b.take("later-" + strconv.Itoa(i))
		}
		assert.Less(t, b.size(), 100, "새 키 하나에 옛 키 둘씩 치우므로 줄어들어야 한다")
		for i := range 60 {
			b.take("later-" + strconv.Itoa(i))
		}
		assert.Equal(t, 60, b.size(), "가득 찬 옛 키는 모두 사라지고 방금 쓴 키만 남는다")
	})

	t.Run("여러 고루틴이 함께 써도 한도만큼만 된다", func(t *testing.T) {
		b, _ := newBuckets(t, RateLimit{Burst: 50, Period: time.Hour}, 10)
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			allowed int
		)
		for range 20 {
			wg.Go(func() {
				for range 10 {
					if ok, _ := b.take("k"); ok {
						mu.Lock()
						allowed++
						mu.Unlock()
					}
				}
			})
		}
		wg.Wait()
		assert.Equal(t, 50, allowed)
	})

	t.Run("쓸 수 없는 한도는 만들 때 거부한다", func(t *testing.T) {
		clk := clock.NewFake(baseTime)
		for name, tt := range map[string]struct {
			clk     clock.Clock
			limit   RateLimit
			maxKeys int
		}{
			"시계가 없다":        {nil, RateLimit{Burst: 1, Period: time.Second}, 1},
			"횟수가 0이다":       {clk, RateLimit{Burst: 0, Period: time.Second}, 1},
			"주기가 0이다":       {clk, RateLimit{Burst: 1, Period: 0}, 1},
			"키를 기억할 자리가 없다": {clk, RateLimit{Burst: 1, Period: time.Second}, 0},
			"주기보다 횟수가 많다":   {clk, RateLimit{Burst: 10, Period: 5 * time.Nanosecond}, 1},
		} {
			_, err := newTokenBuckets(tt.clk, tt.limit, tt.maxKeys)
			assert.Error(t, err, name)
		}
	})
}

func TestRateLimitKeys(t *testing.T) {
	t.Run("IPv6는 앞의 64비트로 묶고, IPv4는 주소 하나하나로 센다", func(t *testing.T) {
		assert.Equal(t, ipKey(netip.MustParseAddr("2001:db8:1:2::1")), ipKey(netip.MustParseAddr("2001:db8:1:2:ffff:ffff:ffff:ffff")))
		assert.NotEqual(t, ipKey(netip.MustParseAddr("2001:db8:1:2::1")), ipKey(netip.MustParseAddr("2001:db8:1:3::1")))
		assert.NotEqual(t, ipKey(netip.MustParseAddr("203.0.113.1")), ipKey(netip.MustParseAddr("203.0.113.2")))
	})

	t.Run("주소를 알 수 없는 요청은 한 통을 나눠 쓴다", func(t *testing.T) {
		assert.Equal(t, "unknown", ipKey(netip.Addr{}))
	})

	t.Run("대소문자와 공백만 다른 이메일은 같은 계정으로 센다", func(t *testing.T) {
		a, ok := emailKey("haneul@example.com")
		require.True(t, ok)
		b, ok := emailKey("  HANEUL@Example.com ")
		require.True(t, ok)
		assert.Equal(t, a, b)
		assert.NotContains(t, a, "haneul", "키에 이메일을 그대로 두지 않는다")
	})

	t.Run("꼴이 틀린 이메일에는 지킬 계정이 없다", func(t *testing.T) {
		_, ok := emailKey("not-an-email")
		assert.False(t, ok)
	})
}

func TestClientIPResolver(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("fd00::/8")}

	tests := []struct {
		name         string
		trusted      []netip.Prefix
		remoteAddr   string
		forwardedFor []string
		want         string
	}{
		{"믿는 프록시가 없으면 연결의 상대 주소다", nil, "198.51.100.1:5000", []string{"1.1.1.1"}, "198.51.100.1"},
		{"프록시가 아닌 곳에서 온 연결의 헤더는 보지 않는다", trusted, "198.51.100.1:5000", []string{"1.1.1.1"}, "198.51.100.1"},
		{"프록시가 적어 준 주소를 쓴다", trusted, "10.42.0.8:41000", []string{"203.0.113.5"}, "203.0.113.5"},
		{"헤더가 없으면 프록시의 주소다", trusted, "10.42.0.8:41000", nil, "10.42.0.8"},
		{"클라이언트가 지어낸 앞쪽 값은 보지 않는다", trusted, "10.42.0.8:41000", []string{"8.8.8.8, 203.0.113.5"}, "203.0.113.5"},
		{"프록시가 여러 겹이면 믿는 주소를 건너뛴다", trusted, "10.42.0.8:41000", []string{"8.8.8.8, 203.0.113.5, 10.42.1.9"}, "203.0.113.5"},
		{"헤더가 여러 줄이어도 차례대로 이어서 읽는다", trusted, "10.42.0.8:41000", []string{"8.8.8.8", "203.0.113.5", "10.42.1.9"}, "203.0.113.5"},
		{"모두 믿는 주소면 가장 멀리서 온 주소다", trusted, "10.42.0.8:41000", []string{"10.42.3.3, 10.42.1.9"}, "10.42.3.3"},
		{"읽을 수 없는 값을 만나면 거기서 멈춘다", trusted, "10.42.0.8:41000", []string{"203.0.113.5, <script>, 10.42.1.9"}, "10.42.1.9"},
		{"맨 뒤가 읽을 수 없는 값이면 프록시의 주소다", trusted, "10.42.0.8:41000", []string{"203.0.113.5, garbage"}, "10.42.0.8"},
		{"빈 칸은 건너뛴다", trusted, "10.42.0.8:41000", []string{"203.0.113.5, , "}, "203.0.113.5"},
		{"포트가 붙은 값도 읽는다", trusted, "10.42.0.8:41000", []string{"203.0.113.5:443"}, "203.0.113.5"},
		{"IPv6 클라이언트", trusted, "[fd00::8]:41000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"IPv6에 담긴 IPv4는 IPv4로 푼다", trusted, "[::ffff:10.42.0.8]:41000", []string{"::ffff:203.0.113.5"}, "203.0.113.5"},
		{"구역 이름은 뗀다", trusted, "10.42.0.8:41000", []string{"fe80::1%eth0"}, "fe80::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.forwardedFor {
				req.Header.Add("X-Forwarded-For", v)
			}
			got := newClientIPResolver(tt.trusted).resolve(req)
			assert.Equal(t, tt.want, got.String())
		})
	}

	t.Run("연결의 상대 주소를 읽을 수 없으면 빈 값이다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "@"
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		assert.False(t, newClientIPResolver(trusted).resolve(req).IsValid())
	})
}

func TestProblemFor(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ProblemCode
	}{
		{"이미 가입된 이메일", auth.ErrEmailTaken, http.StatusConflict, ProblemCodeEmailTaken},
		{"감싼 오류도 알아본다", fmt.Errorf("sign up: %w", auth.ErrEmailTaken), http.StatusConflict, ProblemCodeEmailTaken},
		{"로그인 실패", auth.ErrInvalidCredentials, http.StatusUnauthorized, ProblemCodeInvalidCredentials},
		{"통하지 않는 세션", auth.ErrSessionInvalid, http.StatusUnauthorized, ProblemCodeUnauthenticated},
		{"규칙에 맞지 않는 비밀번호", auth.ErrWeakPassword, http.StatusUnprocessableEntity, ProblemCodeWeakPassword},
		{"빠진 동의", auth.ErrConsentRequired, http.StatusUnprocessableEntity, ProblemCodeConsentRequired},
		{"꼴이 틀린 이메일", auth.ErrInvalidEmail, http.StatusUnprocessableEntity, ProblemCodeValidationFailed},
		{"쓸 수 없는 이름", auth.ErrInvalidDisplayName, http.StatusUnprocessableEntity, ProblemCodeValidationFailed},
		{"모르는 시간대", auth.ErrInvalidTimezone, http.StatusUnprocessableEntity, ProblemCodeValidationFailed},
		{"깨진 해시는 서버의 문제다", auth.ErrMalformedHash, http.StatusInternalServerError, ProblemCodeInternalError},
		{"기한을 넘긴 요청", fmt.Errorf("hash: %w", context.DeadlineExceeded), http.StatusServiceUnavailable, ProblemCodeServiceUnavailable},
		{"클라이언트가 끊은 요청", fmt.Errorf("query: %w", context.Canceled), statusClientClosedRequest, ProblemCodeServiceUnavailable},
		{"너무 큰 본문", &http.MaxBytesError{Limit: 1}, http.StatusRequestEntityTooLarge, ProblemCodePayloadTooLarge},
		{"Echo의 400", echo.NewHTTPError(http.StatusBadRequest, "value \"typed-by-user\" is wrong"), http.StatusBadRequest, ProblemCodeValidationFailed},
		{"Echo의 401", echo.ErrUnauthorized, http.StatusUnauthorized, ProblemCodeUnauthenticated},
		{"Echo의 403", echo.ErrForbidden, http.StatusForbidden, ProblemCodeForbidden},
		{"Echo의 404", echo.ErrNotFound, http.StatusNotFound, ProblemCodeNotFound},
		{"Echo의 405", echo.ErrMethodNotAllowed, http.StatusMethodNotAllowed, ProblemCodeMethodNotAllowed},
		{"Echo의 413", echo.ErrStatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge, ProblemCodePayloadTooLarge},
		{"Echo의 415", echo.ErrUnsupportedMediaType, http.StatusUnsupportedMediaType, ProblemCodeUnsupportedMediaType},
		{"Echo의 429", echo.ErrTooManyRequests, http.StatusTooManyRequests, ProblemCodeRateLimited},
		{"Echo의 503", echo.ErrServiceUnavailable, http.StatusServiceUnavailable, ProblemCodeServiceUnavailable},
		{"그 밖의 4xx는 상태를 그대로 둔다", echo.NewHTTPError(http.StatusGone, ""), http.StatusGone, ProblemCodeValidationFailed},
		{"그 밖의 5xx는 500으로 모은다", echo.NewHTTPError(http.StatusBadGateway, ""), http.StatusInternalServerError, ProblemCodeInternalError},
		{"모르는 오류는 500이다", errors.New("connection refused"), http.StatusInternalServerError, ProblemCodeInternalError},
		{"검증 미들웨어가 감싼 오류", echo.ErrUnauthorized.Wrap(errUnauthenticated), http.StatusUnauthorized, ProblemCodeUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := problemFor(tt.err)
			assert.Equal(t, tt.wantStatus, p.status)
			assert.Equal(t, tt.wantCode, p.code)

			body, err := json.Marshal(p.body("req-1"))
			require.NoError(t, err)
			assert.NotContains(t, string(body), "typed-by-user", "오류의 문구는 응답에 싣지 않는다")
			assert.NotContains(t, string(body), "connection refused")
		})
	}

	t.Run("서비스가 알려주는 비밀번호 거부 이유는 모두 명세에 있다", func(t *testing.T) {
		reasons := []auth.PasswordReason{
			auth.PasswordTooShort, auth.PasswordTooLong, auth.PasswordTooCommon,
			auth.PasswordMatchesEmail, auth.PasswordInvalidEncoding,
		}
		p := problemFor(&auth.PasswordPolicyError{Reasons: reasons})
		require.Len(t, p.reasons, len(reasons), "명세에 없는 이유는 응답에서 빠진다. 이유를 더했으면 명세에도 더한다")
		for i, r := range reasons {
			assert.Equal(t, string(r), string(p.reasons[i]))
		}
	})

	t.Run("동의 오류는 서비스가 아는 종류만 싣고, 요청에 담겨 온 모르는 종류는 싣지 않는다", func(t *testing.T) {
		p := problemFor(&auth.ConsentError{Missing: []string{"terms"}, Outdated: []string{"privacy", "typed-by-user"}, Unknown: 2})
		require.NotNil(t, p.consents)
		assert.Equal(t, []ConsentKind{ConsentKindTerms}, p.consents.Missing)
		assert.Equal(t, []ConsentKind{ConsentKindPrivacy}, p.consents.Outdated)

		body, err := json.Marshal(p.body("req-1"))
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"type": "/problems/consent_required", "title": "Required consent is missing or outdated",
			"status": 422, "code": "consent_required", "request_id": "req-1",
			"consents": {"missing": ["terms"], "outdated": ["privacy"]}
		}`, string(body))
	})

	t.Run("명세의 모든 code에 문구가 있다", func(t *testing.T) {
		spec, err := GetSpec()
		require.NoError(t, err)
		codes := spec.Components.Schemas["ProblemCode"].Value.Enum
		require.NotEmpty(t, codes)
		for _, c := range codes {
			code, ok := c.(string)
			require.True(t, ok)
			assert.NotEmpty(t, problemTitles[ProblemCode(code)], code)
		}
		assert.Len(t, problemTitles, len(codes), "명세에 없는 code에 문구가 있다")
	})
}

func TestUTCTime(t *testing.T) {
	seoul := time.FixedZone("KST", 9*60*60)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"UTC의 정각", time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), `"2026-09-20T12:00:00.000Z"`},
		{"다른 시간대의 값도 UTC로 나간다", time.Date(2026, 9, 21, 3, 30, 0, 0, seoul), `"2026-09-20T18:30:00.000Z"`},
		{"밀리초 아래는 버린다", time.Date(2026, 9, 20, 12, 0, 0, 123_456_789, time.UTC), `"2026-09-20T12:00:00.123Z"`},
		{"0으로 끝나는 밀리초도 세 자리를 채운다", time.Date(2026, 9, 20, 12, 0, 0, 100_000_000, time.UTC), `"2026-09-20T12:00:00.100Z"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(NewUTCTime(tt.in))
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))

			var back UTCTime
			require.NoError(t, json.Unmarshal(got, &back))
			assert.True(t, back.Time().Equal(tt.in.Truncate(time.Millisecond)))
		})
	}

	t.Run("네 자리를 넘는 연도는 적을 수 없다", func(t *testing.T) {
		_, err := json.Marshal(NewUTCTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)))
		assert.Error(t, err)
	})

	t.Run("읽을 수 없는 값의 오류에는 받은 글자가 들어 있지 않다", func(t *testing.T) {
		var u UTCTime
		for _, in := range []string{`"typed-by-user"`, `12345`, `null`} {
			err := json.Unmarshal([]byte(in), &u)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "typed-by-user")
		}
	})
}

func TestFormatWallClock(t *testing.T) {
	const minute = int64(time.Minute / time.Microsecond)
	tests := []struct {
		name    string
		in      pgtype.Time
		want    string
		wantErr bool
	}{
		{"저녁 8시", pgtype.Time{Microseconds: 20 * 60 * minute, Valid: true}, "20:00", false},
		{"자정", pgtype.Time{Microseconds: 0, Valid: true}, "00:00", false},
		{"하루의 마지막 분", pgtype.Time{Microseconds: 23*60*minute + 59*minute + 59_999_999, Valid: true}, "23:59", false},
		{"한 자리 시각은 0을 채운다", pgtype.Time{Microseconds: 7*60*minute + 5*minute, Valid: true}, "07:05", false},
		{"NULL은 오류다", pgtype.Time{}, "", true},
		{"24:00은 하루 밖이다", pgtype.Time{Microseconds: 24 * 60 * minute, Valid: true}, "", true},
		{"음수는 하루 밖이다", pgtype.Time{Microseconds: -1, Valid: true}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatWallClock(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpecSecurity(t *testing.T) {
	spec, err := GetSpec()
	require.NoError(t, err)

	t.Run("명세는 그 자체로 올바르다", func(t *testing.T) {
		require.NoError(t, spec.Validate(t.Context()))
	})

	t.Run("로그인이 필요한 작업은 명세의 security에서 나온다", func(t *testing.T) {
		// 기록을 읽고 고치는 경로는 모두 로그인이 필요하다. 도움 자원(listResources)만 로그인 없이 열린다.
		assert.ElementsMatch(t,
			[]string{
				"getMe", "listDiaries", "getDiary", "putDiary", "deleteDay",
				"getTrend", "getDaySignals", "cancelSignal", "uncancelSignal", "getInternalReview",
			},
			protectedOperations(spec))
	})

	t.Run("security를 적지 않은 작업은 로그인이 필요한 쪽으로 틀린다", func(t *testing.T) {
		require.NotEmpty(t, spec.Security, "명세 전체의 기본값이 있어야 한다")

		path := spec.Paths.Find(pathRequirements)
		require.NotNil(t, path)
		original := path.Get.Security
		path.Get.Security = nil
		defer func() { path.Get.Security = original }()

		assert.Contains(t, protectedOperations(spec), "getAuthRequirements")
	})

	t.Run("모든 경로는 /api/v1 아래에 있고 요청 본문은 모르는 필드를 받지 않는다", func(t *testing.T) {
		for path, item := range spec.Paths.Map() {
			assert.Regexp(t, `^/api/v1/`, path)
			for method, op := range item.Operations() {
				if op.RequestBody == nil {
					continue
				}
				schema := op.RequestBody.Value.Content["application/json"].Schema.Value
				require.NotNil(t, schema.AdditionalProperties.Has, "%s %s", method, path)
				assert.False(t, *schema.AdditionalProperties.Has, "%s %s", method, path)
			}
		}
	})
}

// stubAuth는 인증 서비스 자리에 끼우는 가짜다. DB 없이 미들웨어만 볼 때 쓴다.
type stubAuth struct {
	AuthService
	principal auth.Principal
	err       error
}

func (s stubAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	return s.principal, s.err
}

type stubSettings struct{}

func (stubSettings) GetUserSettings(context.Context, uuid.UUID) (db.UserSetting, error) {
	return db.UserSetting{}, errors.New("not used")
}

func TestLoadSessionFailure(t *testing.T) {
	logs := &syncBuffer{}
	opts := Options{
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
		Clock:        clock.NewFake(baseTime),
		Auth:         stubAuth{err: errors.New("connection refused")},
		Settings:     stubSettings{},
		Cookie:       CookieConfig{Name: "sid", MaxAge: day},
		PublicOrigin: testOrigin,
		RateLimits:   generousLimits,
	}
	withOfflineRecords(t, &opts)
	e, err := New(opts)
	require.NoError(t, err)

	t.Run("세션이 통하는지 알 수 없으면 401이 아니라 500이고 쿠키를 지우지 않는다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, pathMe, nil)
		req.AddCookie(&http.Cookie{Name: "sid", Value: "whatever"})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		requireProblem(t, rec, http.StatusInternalServerError, ProblemCodeInternalError)
		assert.Empty(t, rec.Header().Values("Set-Cookie"), "멀쩡한 세션일 수 있다")
		assert.NotContains(t, rec.Body.String(), "connection refused", "원인은 응답에 싣지 않는다")
		assert.Contains(t, logs.String(), "connection refused", "원인은 접근 로그에 남는다")
	})

	t.Run("쿠키가 없는 요청은 인증 서비스를 거치지 않는다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pathRequirements, nil))
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

// stalledAuth는 DB가 멎은 인증 서비스다. 컨텍스트가 끝날 때까지 답하지 않는다.
type stalledAuth struct {
	AuthService
}

func (stalledAuth) Authenticate(ctx context.Context, _ string) (auth.Principal, error) {
	<-ctx.Done()
	return auth.Principal{}, fmt.Errorf("auth: look up session: %w", ctx.Err())
}

func (stalledAuth) CheckSignup(auth.SignupInput) error { return nil }

func TestRequestDeadline(t *testing.T) {
	opts := Options{
		Logger:         slog.New(slog.DiscardHandler),
		Clock:          clock.NewFake(baseTime),
		Auth:           stalledAuth{},
		Settings:       stubSettings{},
		Cookie:         CookieConfig{Name: "sid", MaxAge: day},
		PublicOrigin:   testOrigin,
		RateLimits:     generousLimits,
		RequestTimeout: 50 * time.Millisecond,
		// 가입과 로그인이 따로 받는 기한은 세션을 읽은 뒤에야 시작한다. 그 기한에 기대고 있지 않다는 것을 보이려고 길게 준다.
		AuthTimeout: time.Hour,
	}
	withOfflineRecords(t, &opts)
	e, err := New(opts)
	require.NoError(t, err)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"내 정보", http.MethodGet, pathMe, ""},
		{"쿠키가 딸려 온 로그인", http.MethodPost, pathLogin, `{"email":"haneul@example.com","password":"x"}`},
		{"로그아웃", http.MethodPost, pathLogout, ""},
		{"없는 경로", http.MethodGet, "/api/v1/nope", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name+"은 DB가 멎어도 기한 안에 503과 Retry-After로 끝난다", func(t *testing.T) {
			// 기한이 걸려 있지 않으면 이 요청은 끝나지 않는다. 시험이 멈춰 서지 않게 취소로 끊는다.
			// 기한(DeadlineExceeded)이 아니라 취소(Canceled)로 끝난 요청은 503이 아니라서 아래에서 걸린다.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			guard := time.AfterFunc(10*time.Second, cancel)
			defer guard.Stop()

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequestWithContext(ctx, tt.method, tt.path, body)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			req.AddCookie(&http.Cookie{Name: "sid", Value: "valid"})
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			requireProblem(t, rec, http.StatusServiceUnavailable, ProblemCodeServiceUnavailable)
			assert.Equal(t, "5", rec.Header().Get("Retry-After"))
			assert.Empty(t, rec.Header().Values("Set-Cookie"), "멀쩡한 세션일 수 있다")
		})
	}
}

func TestClientAddressForwardingDiagnostics(t *testing.T) {
	const (
		collapsed = "client address is not forwarded by the proxy; per-address limits are shared by all users"
		forwarded = "client address forwarding works"
		proxy     = "10.42.0.8:41000"
	)
	newServer := func(t *testing.T, trusted []netip.Prefix) (*echo.Echo, *syncBuffer) {
		t.Helper()
		logs := &syncBuffer{}
		opts := Options{
			Logger:         slog.New(slog.NewJSONHandler(logs, nil)),
			Clock:          clock.NewFake(baseTime),
			Auth:           stubAuth{},
			Settings:       stubSettings{},
			Cookie:         CookieConfig{Name: "sid", MaxAge: day},
			PublicOrigin:   testOrigin,
			TrustedProxies: trusted,
			RateLimits:     generousLimits,
		}
		withOfflineRecords(t, &opts)
		e, err := New(opts)
		require.NoError(t, err)
		return e, logs
	}
	get := func(e *echo.Echo, remoteAddr, forwardedFor string) {
		req := httptest.NewRequest(http.MethodGet, pathRequirements, nil)
		req.RemoteAddr = remoteAddr
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		e.ServeHTTP(httptest.NewRecorder(), req)
	}
	podRange := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}

	t.Run("프록시가 클라이언트의 주소를 전해 주지 않으면 한 번 경고하고 주소는 남기지 않는다", func(t *testing.T) {
		e, logs := newServer(t, podRange)
		// 프록시가 본 상대도 클러스터 안의 주소인 경우와, 헤더가 아예 없는 경우다.
		get(e, proxy, "10.42.0.1")
		get(e, proxy, "")
		get(e, proxy, "10.42.0.1")

		lines := logs.lines(t, collapsed)
		require.Len(t, lines, 1, "요청마다 남기면 로그가 이 줄로 덮인다")
		assert.Equal(t, "WARN", lines[0]["level"])
		assert.NotEmpty(t, lines[0]["request_id"])
		assert.NotContains(t, logs.String(), "10.42.")
		assert.Empty(t, logs.lines(t, forwarded))
	})

	t.Run("주소가 제대로 전해지면 그 사실을 한 번 알린다", func(t *testing.T) {
		e, logs := newServer(t, podRange)
		get(e, proxy, "203.0.113.5")
		get(e, proxy, "203.0.113.6")

		lines := logs.lines(t, forwarded)
		require.Len(t, lines, 1)
		assert.Equal(t, "INFO", lines[0]["level"])
		assert.NotContains(t, logs.String(), "203.0.113.")
		assert.Empty(t, logs.lines(t, collapsed))
	})

	t.Run("프록시를 거치지 않고 온 요청으로는 어느 쪽도 말하지 않는다", func(t *testing.T) {
		e, logs := newServer(t, podRange)
		get(e, "198.51.100.1:5000", "203.0.113.5")
		assert.Empty(t, logs.lines(t, collapsed))
		assert.Empty(t, logs.lines(t, forwarded))
	})

	t.Run("믿는 프록시를 설정하지 않았으면 아무것도 남기지 않는다", func(t *testing.T) {
		e, logs := newServer(t, nil)
		get(e, "127.0.0.1:5000", "")
		get(e, proxy, "10.42.0.1")
		assert.Empty(t, logs.lines(t, collapsed))
		assert.Empty(t, logs.lines(t, forwarded))
	})
}

func TestRequireAuth(t *testing.T) {
	principal := auth.Principal{User: auth.User{ID: uuid.New()}, Session: auth.Session{ID: uuid.New()}}

	newEcho := func(t *testing.T, authenticator stubAuth) *echo.Echo {
		t.Helper()
		e := echo.NewWithConfig(echo.Config{HTTPErrorHandler: NewErrorHandler(nil)})
		cookies, err := newSessionCookies(CookieConfig{Name: "sid", MaxAge: day})
		require.NoError(t, err)
		// 명세에 없는 경로(WebSocket 같은)에 직접 붙여 쓰는 모습이다.
		e.GET("/api/v1/stream", func(c *echo.Context) error {
			p, ok := PrincipalFrom(c.Request().Context())
			require.True(t, ok)
			return c.String(http.StatusOK, p.User.ID.String())
		}, loadSession(authenticator, cookies), RequireAuth())
		return e
	}

	t.Run("로그인한 요청은 통과하고 핸들러가 누구인지 안다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
		req.AddCookie(&http.Cookie{Name: "sid", Value: "valid"})
		rec := httptest.NewRecorder()
		newEcho(t, stubAuth{principal: principal}).ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, principal.User.ID.String(), rec.Body.String())
	})

	t.Run("쿠키가 없으면 401 unauthenticated다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		newEcho(t, stubAuth{principal: principal}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil))
		requireProblemWithoutRequestID(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("통하지 않는 세션이면 401 unauthenticated다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
		req.AddCookie(&http.Cookie{Name: "sid", Value: "stale"})
		rec := httptest.NewRecorder()
		newEcho(t, stubAuth{err: auth.ErrSessionInvalid}).ServeHTTP(rec, req)
		requireProblemWithoutRequestID(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("미들웨어를 거치지 않은 컨텍스트에는 아무도 없다", func(t *testing.T) {
		_, ok := PrincipalFrom(t.Context())
		assert.False(t, ok)
		assert.Empty(t, AccessLogAttrs(t.Context()))
	})
}

// requireProblemWithoutRequestID는 요청 ID 미들웨어 없이 만든 서버의 오류 응답을 본다.
func requireProblemWithoutRequestID(t *testing.T, rec *httptest.ResponseRecorder, status int, code ProblemCode) {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	var p Problem
	decode(t, rec, &p)
	assert.Equal(t, code, p.Code)
	assert.Equal(t, status, p.Status)
}

func TestErrorHandler(t *testing.T) {
	e := echo.NewWithConfig(echo.Config{HTTPErrorHandler: NewErrorHandler(nil)})
	e.GET("/limited", func(*echo.Context) error {
		p := newProblem(http.StatusTooManyRequests, ProblemCodeRateLimited)
		p.retryAfter = 1500 * time.Millisecond
		return p
	})
	e.GET("/late", func(c *echo.Context) error {
		if err := c.String(http.StatusOK, "already sent"); err != nil {
			return err
		}
		return errors.New("failed after the response was written")
	})

	t.Run("기다릴 시간은 초로 올림해서 알려준다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/limited", nil))
		assert.Equal(t, http.StatusTooManyRequests, rec.Code)
		assert.Equal(t, "2", rec.Header().Get("Retry-After"))
	})

	t.Run("HEAD 요청의 오류에는 본문이 없다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/nope", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Empty(t, rec.Body.String())
	})

	t.Run("이미 나간 응답에는 덧쓰지 않는다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/late", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "already sent", rec.Body.String())
	})
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	complete := func() Options {
		opts := Options{
			Logger:       slog.New(slog.DiscardHandler),
			Clock:        clock.NewFake(baseTime),
			Auth:         stubAuth{},
			Settings:     stubSettings{},
			Cookie:       CookieConfig{Name: "sid", MaxAge: day},
			PublicOrigin: testOrigin,
			RateLimits:   generousLimits,
		}
		withOfflineRecords(t, &opts)
		return opts
	}
	_, err := New(complete())
	require.NoError(t, err)

	for name, mutate := range map[string]func(*Options){
		"로거가 없다":         func(o *Options) { o.Logger = nil },
		"시계가 없다":         func(o *Options) { o.Clock = nil },
		"인증 서비스가 없다":     func(o *Options) { o.Auth = nil },
		"설정을 읽을 곳이 없다":   func(o *Options) { o.Settings = nil },
		"저장소가 없다":        func(o *Options) { o.Store = nil },
		"글을 잠글 곳이 없다":    func(o *Options) { o.Sealers = nil },
		"쿠키 이름이 없다":      func(o *Options) { o.Cookie.Name = "" },
		"출처가 출처의 꼴이 아니다": func(o *Options) { o.PublicOrigin = "localhost:5173/app" },
		"시도 한도가 비어 있다":   func(o *Options) { o.RateLimits = RateLimits{} },
		"계정의 천장이 한 주소의 한도보다 넉넉하지 않다": func(o *Options) {
			o.RateLimits.LoginPerEmailTotal = o.RateLimits.LoginPerEmail
		},
		"계정의 천장이 한 주소의 한도보다 느리게 찬다": func(o *Options) {
			o.RateLimits.LoginPerEmail = RateLimit{Burst: 10, Period: 10 * time.Minute}
			o.RateLimits.LoginPerEmailTotal = RateLimit{Burst: 20, Period: time.Hour}
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts := complete()
			mutate(&opts)
			_, err := New(opts)
			assert.Error(t, err)
		})
	}
}

func TestRegisterOperations(t *testing.T) {
	cookies, err := newSessionCookies(CookieConfig{Name: "sid", MaxAge: day})
	require.NoError(t, err)
	handler := NewStrictHandler(&handlers{auth: stubAuth{}, settings: stubSettings{}, cookies: cookies, authTimeout: time.Second}, nil)

	t.Run("검증기 없이 로그인 확인만으로도 로그인이 필요한 경로가 막힌다", func(t *testing.T) {
		spec, err := GetSpec()
		require.NoError(t, err)
		e := echo.NewWithConfig(echo.Config{HTTPErrorHandler: NewErrorHandler(nil)})
		require.NoError(t, registerOperations(e.Group(pathPrefix), handler, protectedOperations(spec), nil))

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pathMe, nil))
		requireProblemWithoutRequestID(t, rec, http.StatusUnauthorized, ProblemCodeUnauthenticated)

		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pathRequirements, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "로그인이 필요 없는 경로는 열려 있다")
	})

	t.Run("작업의 이름이 어긋나 로그인 확인이 붙지 않으면 등록을 거부한다", func(t *testing.T) {
		e := echo.New()
		err := registerOperations(e.Group(pathPrefix), handler, []string{"GetMe"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "require a login")
	})

	t.Run("앞머리 밖의 경로는 등록하지 않는다", func(t *testing.T) {
		e := echo.New()
		router := &groupRouter{group: e.Group(pathPrefix), prefix: pathPrefix}
		router.GET("/internal/v1/secret", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
		require.Error(t, router.err)

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/v1/secret", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// /ws 아래의 경로가 /api와 똑같은 미들웨어 묶음을 거치는지 본다.
// 묶음은 Register가 한 번만 만들어 두 앞머리에 건다. 한쪽에만 더하는 실수로 둘이 어긋날 수 없다.
func TestRoutesOutsideTheSpec(t *testing.T) {
	principal := auth.Principal{User: auth.User{ID: uuid.New()}, Session: auth.Session{ID: uuid.New()}}
	logger := slog.New(slog.DiscardHandler)
	e := httpserver.New(httpserver.Options{Logger: logger, ErrorHandler: NewErrorHandler(logger), AccessLogAttrs: AccessLogAttrs})
	opts := Options{
		Logger:       logger,
		Clock:        clock.NewFake(baseTime),
		Auth:         stubAuth{principal: principal},
		Settings:     stubSettings{},
		Cookie:       CookieConfig{Name: "sid", MaxAge: day},
		PublicOrigin: testOrigin,
		RateLimits:   generousLimits,
	}
	withOfflineRecords(t, &opts)
	group, err := Register(e, opts)
	require.NoError(t, err)

	// 대화 소켓이 붙는 자리다. 소켓 대신 무엇이 거쳐 왔는지 알려주는 핸들러를 붙여 본다.
	stream := func(c *echo.Context) error {
		p, ok := PrincipalFrom(c.Request().Context())
		require.True(t, ok)
		return c.String(http.StatusOK, p.User.ID.String())
	}
	group.GET("/v1/stream", stream, RequireAuth())
	group.POST("/v1/stream", stream, RequireAuth())

	do := func(method string, withCookie bool, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/ws/v1/stream", nil)
		if withCookie {
			req.AddCookie(&http.Cookie{Name: "sid", Value: "valid"})
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	t.Run("명세에 없다는 이유로 검증기에 막히지 않고, 세션까지는 똑같이 읽힌다", func(t *testing.T) {
		rec := do(http.MethodGet, true, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, principal.User.ID.String(), rec.Body.String())
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("로그인하지 않았으면 401이다", func(t *testing.T) {
		requireProblem(t, do(http.MethodGet, false, nil), http.StatusUnauthorized, ProblemCodeUnauthenticated)
	})

	t.Run("다른 출처에서 온 상태 변경 요청은 여기서도 막힌다", func(t *testing.T) {
		rec := do(http.MethodPost, true, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
		requireProblem(t, rec, http.StatusForbidden, ProblemCodeCrossOriginRejected)
	})

	t.Run("WebSocket 연결을 여는 요청은 GET이어도 출처를 본다", func(t *testing.T) {
		upgrade := func(extra map[string]string) map[string]string {
			headers := map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13"}
			for k, v := range extra {
				headers[k] = v
			}
			return headers
		}

		rejected := map[string]map[string]string{
			"다른 사이트의 페이지":                    {"Origin": "https://evil.example"},
			"같은 사이트의 다른 하위 도메인(쿠키가 실리는 자리다)": {"Sec-Fetch-Site": "same-site", "Origin": "http://blog.localhost:5173"},
			"헤더의 대소문자를 바꾼 요청":                {"Origin": "https://evil.example", "Upgrade": "WebSocket"},
		}
		for name, headers := range rejected {
			rec := do(http.MethodGet, true, upgrade(headers))
			requireProblem(t, rec, http.StatusForbidden, ProblemCodeCrossOriginRejected)
			assert.NotContains(t, rec.Body.String(), principal.User.ID.String(), name)
		}

		allowed := map[string]map[string]string{
			"웹앱의 출처": {"Origin": testOrigin},
			"브라우저가 같은 출처라고 알려준 요청":      {"Sec-Fetch-Site": "same-origin", "Origin": testOrigin},
			"브라우저가 아닌 클라이언트(출처 헤더가 없다)": nil,
		}
		for name, headers := range allowed {
			assert.Equal(t, http.StatusOK, do(http.MethodGet, true, upgrade(headers)).Code, name)
		}

		requireProblem(t, do(http.MethodGet, false, upgrade(map[string]string{"Origin": testOrigin})),
			http.StatusUnauthorized, ProblemCodeUnauthenticated)

		rec := do(http.MethodGet, true, map[string]string{"Origin": "https://evil.example"})
		assert.Equal(t, http.StatusOK, rec.Code, "연결을 열지 않는 GET은 전과 같이 어느 출처에서 와도 통과한다")
	})

	t.Run("오래 열려 있는 경로에는 요청 기한을 걸지 않고, 명세의 경로에는 건다", func(t *testing.T) {
		var streamHasDeadline bool
		group.GET("/v1/long-lived", func(c *echo.Context) error {
			_, streamHasDeadline = c.Request().Context().Deadline()
			return c.NoContent(http.StatusNoContent)
		})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/v1/long-lived", nil))
		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.False(t, streamHasDeadline, "기한이 걸리면 대화가 도중에 끊긴다")
	})

	t.Run("명세의 경로는 여전히 검증을 거친다", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, pathLogin, strings.NewReader(`{"email":"a@example.com","password":"x","extra":1}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		requireProblem(t, rec, http.StatusBadRequest, ProblemCodeValidationFailed)
	})

	t.Run("어디에도 없는 경로는 404다", func(t *testing.T) {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
		requireProblem(t, rec, http.StatusNotFound, ProblemCodeNotFound)
	})
}
