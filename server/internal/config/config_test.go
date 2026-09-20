package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKEK는 시험에서만 쓰는 32바이트 키다.
var testKEK = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))

const testDatabaseURL = "postgres://app:s3cr3t-pass@db.internal:5432/app?sslmode=require"

func minimalEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": testDatabaseURL,
		"DATA_KEK_V1":  testKEK,
	}
}

func prodEnv() map[string]string {
	e := minimalEnv()
	e["APP_ENV"] = "prod"
	e["PUBLIC_ORIGIN"] = "https://naeil.example"
	e["GEMINI_API_KEY"] = "gm-prod-key"
	return e
}

func with(base map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func without(base map[string]string, names ...string) map[string]string {
	out := with(base)
	for _, n := range names {
		delete(out, n)
	}
	return out
}

func TestLoadFrom_Defaults(t *testing.T) {
	t.Run("필수 값만 주면 나머지는 기본값으로 채워진다", func(t *testing.T) {
		cfg, err := LoadFrom(minimalEnv())
		require.NoError(t, err)

		assert.Equal(t, EnvDev, cfg.Env)
		assert.Equal(t, ":8080", cfg.HTTPAddr)
		assert.Equal(t, testDatabaseURL, cfg.DatabaseURL.Reveal())
		assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
		assert.Equal(t, "http://localhost:5173", cfg.PublicOrigin)
		assert.Equal(t, "naeil_session", cfg.Session.CookieName)
		assert.False(t, cfg.Session.CookieSecure, "dev에서는 HTTP로도 쿠키가 가야 한다")
		assert.Equal(t, 30*24*time.Hour, cfg.Session.AbsoluteLifetime)
		assert.Equal(t, 14*24*time.Hour, cfg.Session.IdleLifetime)
		assert.Equal(t, 5*time.Minute, cfg.Session.TouchInterval)
		assert.Empty(t, cfg.TrustedProxies, "믿는 프록시가 없으면 전달된 주소 헤더를 보지 않는다")
		assert.Equal(t, RateLimits{
			SignupPerIP:        RateLimit{Burst: 20, Period: time.Hour},
			LoginPerIP:         RateLimit{Burst: 30, Period: 5 * time.Minute},
			LoginPerEmail:      RateLimit{Burst: 10, Period: 10 * time.Minute},
			LoginPerEmailTotal: RateLimit{Burst: 60, Period: time.Hour},
			MaxKeys:            50000,
		}, cfg.RateLimits)
		assert.Equal(t, Password{MemoryKiB: 19456, Time: 2, Parallelism: 1, HashConcurrency: 4}, cfg.Password)
		assert.Equal(t, DataKeyCache{Size: 1024, MaxAge: 10 * time.Minute}, cfg.DataKeyCache)
		assert.Equal(t, 1, cfg.DataKeys.Active)
		assert.Equal(t, []int{1}, cfg.DataKeys.Versions())
		assert.Equal(t, bytes.Repeat([]byte{0x2a}, 32), cfg.DataKeys.Keys[1].Reveal())

		assert.False(t, cfg.Providers.GeminiAPIKey.IsSet())
		assert.False(t, cfg.Providers.SonioxAPIKey.IsSet())
		assert.False(t, cfg.Providers.ElevenLabsAPIKey.IsSet())

		assert.Equal(t, "gemini-3.8-flash", cfg.LLM.ConversationModel)
		assert.Equal(t, "gemini-3.6-flash", cfg.LLM.ConversationFallbackModel)
		assert.Equal(t, "gemini-3.5-flash", cfg.LLM.GateModel)
		assert.Equal(t, "gemini-3.8-flash", cfg.LLM.AnalysisModel)
		assert.Equal(t, ThinkingLow, cfg.LLM.ConversationThinking)
		assert.Equal(t, ThinkingMinimal, cfg.LLM.GateThinking)
		assert.Equal(t, ThinkingLow, cfg.LLM.AnalysisThinking)
		assert.Equal(t, 3*time.Second, cfg.LLM.FallbackAfter)

		assert.Equal(t, AIProviderScripted, cfg.LLM.Provider, "키 없이 띄운 개발 서버도 대화가 되어야 한다")
		assert.Equal(t, 2500*time.Millisecond, cfg.LLM.GateTimeout)
		assert.Equal(t, Conversation{
			IdleCheckAfter:     3 * time.Minute,
			IdleEndAfter:       3 * time.Minute,
			DisconnectEndAfter: 30 * time.Minute,
		}, cfg.Conversation)
		assert.Equal(t, WebSocket{MaxMessageBytes: 16384, MessageRate: RateLimit{Burst: 20, Period: time.Minute}}, cfg.WebSocket)
		assert.Equal(t, DiaryJob{Retries: 3}, cfg.DiaryJob)
		assert.Equal(t, 4, cfg.DiaryJob.MaxAttempts(), "처음 한 번에 다시 시도 세 번을 더한 값이 작업 큐에 간다")
	})

	t.Run("빈 문자열로 넘어온 변수는 없는 것으로 보고 기본값을 쓴다", func(t *testing.T) {
		// Makefile이 .env의 빈 항목을 그대로 내보내는 경우다.
		cfg, err := LoadFrom(with(minimalEnv(),
			"LOG_LEVEL", "",
			"GEMINI_API_KEY", "",
			"SESSION_COOKIE_SECURE", "",
			"LLM_FALLBACK_AFTER", "",
			"AI_PROVIDER", "",
			"GATE_AI_TIMEOUT", "",
			"DIARY_JOB_RETRIES", "",
		))
		require.NoError(t, err)
		assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
		assert.False(t, cfg.Providers.GeminiAPIKey.IsSet())
		assert.Equal(t, 3*time.Second, cfg.LLM.FallbackAfter)
		assert.Equal(t, AIProviderScripted, cfg.LLM.Provider)
		assert.Equal(t, 2500*time.Millisecond, cfg.LLM.GateTimeout)
		assert.Equal(t, 3, cfg.DiaryJob.Retries)
	})

	t.Run("준 값은 기본값을 덮어쓴다", func(t *testing.T) {
		cfg, err := LoadFrom(with(minimalEnv(),
			"APP_ENV", "test",
			"HTTP_ADDR", "127.0.0.1:9090",
			"LOG_LEVEL", "debug",
			"SESSION_COOKIE_NAME", "sid",
			"SESSION_COOKIE_SECURE", "true",
			"GEMINI_API_KEY", "gm-key",
			"LLM_MODEL_GATE", "some-other-model",
			"LLM_THINKING_GATE", "HIGH",
			"LLM_FALLBACK_AFTER", "2500ms",
			"SESSION_ABSOLUTE_LIFETIME", "168h",
			"SESSION_IDLE_LIFETIME", "24h",
			"SESSION_TOUCH_INTERVAL", "90s",
			"PASSWORD_ARGON2_MEMORY_KIB", "65536",
			"PASSWORD_ARGON2_TIME", "3",
			"PASSWORD_ARGON2_PARALLELISM", "2",
			"PASSWORD_HASH_CONCURRENCY", "8",
			"DATA_KEY_CACHE_SIZE", "50",
			"DATA_KEY_CACHE_MAX_AGE", "2m",
			"TRUSTED_PROXIES", " 10.42.0.7/16, 127.0.0.1 ,::1,",
			"RATE_LIMIT_SIGNUP_PER_IP", "3/10m",
			"RATE_LIMIT_LOGIN_PER_IP", " 100 / 1m ",
			"RATE_LIMIT_LOGIN_PER_EMAIL", "5/30s",
			"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL", "50/5m",
			"RATE_LIMIT_MAX_KEYS", "128",
			"GATE_AI_TIMEOUT", "1800ms",
			"IDLE_CHECK_AFTER", "90s",
			"IDLE_END_AFTER", "2m",
			"DISCONNECT_END_AFTER", "10m",
			"WS_MAX_MESSAGE_BYTES", "4096",
			"WS_MESSAGE_RATE_LIMIT", " 5 / 10s ",
			"DIARY_JOB_RETRIES", "0",
		))
		require.NoError(t, err)
		assert.Equal(t, AIProviderGemini, cfg.LLM.Provider, "키가 있으면 적지 않아도 실제 모델을 부른다")
		assert.Equal(t, 1800*time.Millisecond, cfg.LLM.GateTimeout)
		assert.Equal(t, Conversation{
			IdleCheckAfter:     90 * time.Second,
			IdleEndAfter:       2 * time.Minute,
			DisconnectEndAfter: 10 * time.Minute,
		}, cfg.Conversation)
		assert.Equal(t, WebSocket{MaxMessageBytes: 4096, MessageRate: RateLimit{Burst: 5, Period: 10 * time.Second}}, cfg.WebSocket)
		assert.Equal(t, DiaryJob{Retries: 0}, cfg.DiaryJob)
		assert.Equal(t, 1, cfg.DiaryJob.MaxAttempts())
		assert.Equal(t, []netip.Prefix{
			netip.MustParsePrefix("10.42.0.0/16"),
			netip.MustParsePrefix("127.0.0.1/32"),
			netip.MustParsePrefix("::1/128"),
		}, cfg.TrustedProxies, "범위는 네트워크 주소로 맞추고, 주소만 적은 것은 그 주소 하나로 본다")
		assert.Equal(t, RateLimits{
			SignupPerIP:        RateLimit{Burst: 3, Period: 10 * time.Minute},
			LoginPerIP:         RateLimit{Burst: 100, Period: time.Minute},
			LoginPerEmail:      RateLimit{Burst: 5, Period: 30 * time.Second},
			LoginPerEmailTotal: RateLimit{Burst: 50, Period: 5 * time.Minute},
			MaxKeys:            128,
		}, cfg.RateLimits)
		assert.Equal(t, 168*time.Hour, cfg.Session.AbsoluteLifetime)
		assert.Equal(t, 24*time.Hour, cfg.Session.IdleLifetime)
		assert.Equal(t, 90*time.Second, cfg.Session.TouchInterval)
		assert.Equal(t, Password{MemoryKiB: 65536, Time: 3, Parallelism: 2, HashConcurrency: 8}, cfg.Password)
		assert.Equal(t, DataKeyCache{Size: 50, MaxAge: 2 * time.Minute}, cfg.DataKeyCache)
		assert.Equal(t, EnvTest, cfg.Env)
		assert.Equal(t, "127.0.0.1:9090", cfg.HTTPAddr)
		assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
		assert.Equal(t, "sid", cfg.Session.CookieName)
		assert.True(t, cfg.Session.CookieSecure)
		assert.Equal(t, "gm-key", cfg.Providers.GeminiAPIKey.Reveal())
		assert.Equal(t, "some-other-model", cfg.LLM.GateModel)
		assert.Equal(t, ThinkingHigh, cfg.LLM.GateThinking)
		assert.Equal(t, 2500*time.Millisecond, cfg.LLM.FallbackAfter)
	})

	t.Run("HTTPS 전용 쿠키에는 __Host-로 시작하는 이름을 직접 줄 수 있다", func(t *testing.T) {
		cfg, err := LoadFrom(with(minimalEnv(), "SESSION_COOKIE_NAME", "__Host-sid", "SESSION_COOKIE_SECURE", "true"))
		require.NoError(t, err)
		assert.Equal(t, "__Host-sid", cfg.Session.CookieName)
	})
}

func TestLoadFrom_AIProvider(t *testing.T) {
	tests := []struct {
		name     string
		environ  map[string]string
		want     AIProvider
		wantVars []string
	}{
		{"개발에서 적지 않았고 키가 없으면 정해 둔 답을 쓴다", minimalEnv(), AIProviderScripted, nil},
		{"개발에서 적지 않았고 키가 있으면 실제 모델을 부른다", with(minimalEnv(), "GEMINI_API_KEY", "gm-key"), AIProviderGemini, nil},
		{"키가 있어도 정해 둔 답을 쓰겠다고 적으면 그대로 따른다", with(minimalEnv(), "GEMINI_API_KEY", "gm-key", "AI_PROVIDER", "scripted"), AIProviderScripted, nil},
		{"시험 환경도 개발과 같다", with(minimalEnv(), "APP_ENV", "test"), AIProviderScripted, nil},
		{"대소문자와 앞뒤 공백은 가리지 않는다", with(minimalEnv(), "GEMINI_API_KEY", "gm-key", "AI_PROVIDER", " Gemini "), AIProviderGemini, nil},
		{"운영에서 적지 않으면 실제 모델을 부른다", prodEnv(), AIProviderGemini, nil},

		{"실제 모델을 부르겠다고 적고 키를 주지 않으면 개발에서도 뜨지 않는다", with(minimalEnv(), "AI_PROVIDER", "gemini"), "", []string{"GEMINI_API_KEY"}},
		{"키가 공백뿐이면 없는 것이다", with(minimalEnv(), "AI_PROVIDER", "gemini", "GEMINI_API_KEY", "   "), "", []string{"GEMINI_API_KEY"}},
		{"운영에서 키가 빠지면 정해 둔 답으로 넘어가지 않고 뜨지 않는다", without(prodEnv(), "GEMINI_API_KEY"), "", []string{"GEMINI_API_KEY"}},
		{"운영에서는 정해 둔 답을 받지 않는다", with(prodEnv(), "AI_PROVIDER", "scripted"), "", []string{"AI_PROVIDER"}},
		{"운영에서는 키가 없어도 정해 둔 답을 받지 않는다", with(without(prodEnv(), "GEMINI_API_KEY"), "AI_PROVIDER", "scripted"), "", []string{"AI_PROVIDER"}},
		{"모르는 공급자는 거부한다", with(minimalEnv(), "AI_PROVIDER", "openai"), "", []string{"AI_PROVIDER"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFrom(tt.environ)
			if tt.wantVars == nil {
				require.NoError(t, err)
				assert.Equal(t, tt.want, cfg.LLM.Provider)
				return
			}
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.ElementsMatch(t, tt.wantVars, verr.Vars())
		})
	}
}

func TestLoadFrom_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		environ  map[string]string
		wantVars []string
	}{
		{"DATABASE_URL이 없으면 거부한다", without(minimalEnv(), "DATABASE_URL"), []string{"DATABASE_URL"}},
		{"DATABASE_URL이 비어 있으면 거부한다", with(minimalEnv(), "DATABASE_URL", ""), []string{"DATABASE_URL"}},
		{"DATA_KEK_V1이 없으면 거부한다", without(minimalEnv(), "DATA_KEK_V1"), []string{"DATA_KEK_V1"}},
		{"필수 값이 둘 다 없으면 둘 다 알려준다", map[string]string{}, []string{"DATABASE_URL", "DATA_KEK_V1"}},
		{"모르는 실행 환경은 거부한다", with(minimalEnv(), "APP_ENV", "staging"), []string{"APP_ENV"}},
		{"포트가 없는 주소는 거부한다", with(minimalEnv(), "HTTP_ADDR", "localhost"), []string{"HTTP_ADDR"}},
		{"모르는 로그 수준은 거부한다", with(minimalEnv(), "LOG_LEVEL", "verbose"), []string{"LOG_LEVEL"}},
		{"출처에 경로가 붙으면 거부한다", with(minimalEnv(), "PUBLIC_ORIGIN", "http://localhost:5173/"), []string{"PUBLIC_ORIGIN"}},
		{"출처의 스킴이 http(s)가 아니면 거부한다", with(minimalEnv(), "PUBLIC_ORIGIN", "ftp://localhost"), []string{"PUBLIC_ORIGIN"}},
		{"쿠키 이름에 쓸 수 없는 글자가 있으면 거부한다", with(minimalEnv(), "SESSION_COOKIE_NAME", "naeil session;"), []string{"SESSION_COOKIE_NAME"}},
		{"HTTP로도 보내는 쿠키에 __Host- 이름을 주면 거부한다", with(minimalEnv(), "SESSION_COOKIE_NAME", "__Host-sid"), []string{"SESSION_COOKIE_NAME"}},
		{"__Secure- 이름은 거부한다", with(minimalEnv(), "SESSION_COOKIE_NAME", "__Secure-sid", "SESSION_COOKIE_SECURE", "true"), []string{"SESSION_COOKIE_NAME"}},
		{"믿는 프록시가 주소 범위가 아니면 거부한다", with(minimalEnv(), "TRUSTED_PROXIES", "10.42.0.0/16,traefik"), []string{"TRUSTED_PROXIES"}},
		{"시도 한도에 주기가 없으면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_IP", "30"), []string{"RATE_LIMIT_LOGIN_PER_IP"}},
		{"시도 한도의 횟수가 0이면 거부한다", with(minimalEnv(), "RATE_LIMIT_SIGNUP_PER_IP", "0/1h"), []string{"RATE_LIMIT_SIGNUP_PER_IP"}},
		{"시도 한도의 주기가 0이면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL", "10/0s"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL"}},
		{"시도 한도의 주기가 하루를 넘으면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL", "10/48h"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL"}},
		{"계정의 천장이 한 주소의 한도만큼만 받으면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL", "10/10m"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL"}},
		{"계정의 천장이 한 주소의 한도보다 느리게 차면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL", "60/2h"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL"}},
		{"한 주소의 한도만 빠르게 바꿔 천장을 앞지르면 거부한다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL", "5/30s"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL"}},
		{"틀린 한도 값은 크기 비교로 한 번 더 지적하지 않는다", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL", "many"), []string{"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL"}},
		{"한도가 기억하는 키의 수가 0이면 거부한다", with(minimalEnv(), "RATE_LIMIT_MAX_KEYS", "0"), []string{"RATE_LIMIT_MAX_KEYS"}},
		{"쿠키 보안 설정이 참거짓이 아니면 거부한다", with(minimalEnv(), "SESSION_COOKIE_SECURE", "yes please"), []string{"SESSION_COOKIE_SECURE"}},
		{"마스터 키가 base64가 아니면 거부한다", with(minimalEnv(), "DATA_KEK_V1", "REPLACE_ME_WITH_OUTPUT_OF_openssl_rand_base64_32"), []string{"DATA_KEK_V1"}},
		{"마스터 키가 32바이트가 아니면 거부한다", with(minimalEnv(), "DATA_KEK_V1", base64.StdEncoding.EncodeToString([]byte("too-short"))), []string{"DATA_KEK_V1"}},
		{"활성 버전이 숫자가 아니면 거부한다", with(minimalEnv(), "DATA_KEK_ACTIVE", "one"), []string{"DATA_KEK_ACTIVE"}},
		{"활성 버전이 0이면 거부한다", with(minimalEnv(), "DATA_KEK_ACTIVE", "0"), []string{"DATA_KEK_ACTIVE"}},
		{"활성 버전의 키가 없으면 거부한다", with(minimalEnv(), "DATA_KEK_ACTIVE", "2"), []string{"DATA_KEK_ACTIVE"}},
		{"둘째 키가 잘못되면 그 변수 이름으로 알려준다", with(minimalEnv(), "DATA_KEK_V2", "!!!"), []string{"DATA_KEK_V2"}},
		{"DB에 저장할 수 없는 큰 키 버전은 거부한다", with(minimalEnv(), "DATA_KEK_V32768", testKEK), []string{"DATA_KEK_V32768"}},
		{"모르는 생각하기 수준은 거부한다", with(minimalEnv(), "LLM_THINKING_GATE", "none"), []string{"LLM_THINKING_GATE"}},
		{"예비 모델 대기 시간이 시간 꼴이 아니면 거부한다", with(minimalEnv(), "LLM_FALLBACK_AFTER", "3"), []string{"LLM_FALLBACK_AFTER"}},
		{"예비 모델 대기 시간이 0 이하면 거부한다", with(minimalEnv(), "LLM_FALLBACK_AFTER", "0s"), []string{"LLM_FALLBACK_AFTER"}},
		{"모델 이름이 공백뿐이면 거부한다", with(minimalEnv(), "LLM_MODEL_ANALYSIS", "   "), []string{"LLM_MODEL_ANALYSIS"}},
		{"세션 수명이 시간 꼴이 아니면 거부한다", with(minimalEnv(), "SESSION_ABSOLUTE_LIFETIME", "30d"), []string{"SESSION_ABSOLUTE_LIFETIME"}},
		{"세션 수명이 0 이하면 거부한다", with(minimalEnv(), "SESSION_IDLE_LIFETIME", "0s"), []string{"SESSION_IDLE_LIFETIME"}},
		{"세션 수명이 한 해를 넘으면 거부한다", with(minimalEnv(), "SESSION_ABSOLUTE_LIFETIME", "87600h"), []string{"SESSION_ABSOLUTE_LIFETIME"}},
		{"쉬는 시간의 한도가 전체 수명보다 길면 거부한다", with(minimalEnv(), "SESSION_ABSOLUTE_LIFETIME", "24h", "SESSION_IDLE_LIFETIME", "48h"), []string{"SESSION_IDLE_LIFETIME"}},
		{"다시 적는 간격이 쉬는 시간의 한도 이상이면 거부한다", with(minimalEnv(), "SESSION_IDLE_LIFETIME", "10m", "SESSION_TOUCH_INTERVAL", "10m"), []string{"SESSION_TOUCH_INTERVAL"}},
		{"틀린 수명 값은 크기 비교로 한 번 더 지적하지 않는다", with(minimalEnv(), "SESSION_IDLE_LIFETIME", "soon"), []string{"SESSION_IDLE_LIFETIME"}},
		{"해시에 쓰는 메모리가 너무 작으면 거부한다", with(minimalEnv(), "PASSWORD_ARGON2_MEMORY_KIB", "64"), []string{"PASSWORD_ARGON2_MEMORY_KIB"}},
		{"해시에 쓰는 메모리가 1 GiB를 넘으면 거부한다", with(minimalEnv(), "PASSWORD_ARGON2_MEMORY_KIB", "1048577"), []string{"PASSWORD_ARGON2_MEMORY_KIB"}},
		{"해시의 반복 수가 0이면 거부한다", with(minimalEnv(), "PASSWORD_ARGON2_TIME", "0"), []string{"PASSWORD_ARGON2_TIME"}},
		{"해시의 병렬 수가 숫자가 아니면 거부한다", with(minimalEnv(), "PASSWORD_ARGON2_PARALLELISM", "two"), []string{"PASSWORD_ARGON2_PARALLELISM"}},
		{"동시에 계산할 해시의 수가 0이면 거부한다", with(minimalEnv(), "PASSWORD_HASH_CONCURRENCY", "0"), []string{"PASSWORD_HASH_CONCURRENCY"}},
		{"키 캐시의 크기가 0이면 거부한다", with(minimalEnv(), "DATA_KEY_CACHE_SIZE", "0"), []string{"DATA_KEY_CACHE_SIZE"}},
		{"키 캐시의 보관 시간이 0이면 거부한다", with(minimalEnv(), "DATA_KEY_CACHE_MAX_AGE", "0s"), []string{"DATA_KEY_CACHE_MAX_AGE"}},
		{"키 캐시의 보관 시간이 시간 꼴이 아니면 거부한다", with(minimalEnv(), "DATA_KEY_CACHE_MAX_AGE", "10"), []string{"DATA_KEY_CACHE_MAX_AGE"}},
		{"위기 판별을 기다리는 시간이 시간 꼴이 아니면 거부한다", with(minimalEnv(), "GATE_AI_TIMEOUT", "2.5"), []string{"GATE_AI_TIMEOUT"}},
		{"위기 판별을 기다리는 시간이 0 이하면 거부한다", with(minimalEnv(), "GATE_AI_TIMEOUT", "0s"), []string{"GATE_AI_TIMEOUT"}},
		{"위기 판별을 30초 넘게 기다리는 값은 거부한다", with(minimalEnv(), "GATE_AI_TIMEOUT", "5m"), []string{"GATE_AI_TIMEOUT"}},
		{"말이 없을 때 묻기까지의 시간이 0 이하면 거부한다", with(minimalEnv(), "IDLE_CHECK_AFTER", "-3m"), []string{"IDLE_CHECK_AFTER"}},
		{"묻고 나서 기다리는 시간이 시간 꼴이 아니면 거부한다", with(minimalEnv(), "IDLE_END_AFTER", "3"), []string{"IDLE_END_AFTER"}},
		{"끊긴 연결을 하루 넘게 기다리는 값은 거부한다", with(minimalEnv(), "DISCONNECT_END_AFTER", "48h"), []string{"DISCONNECT_END_AFTER"}},
		{"메시지 크기의 한도가 글 하나도 담지 못하면 거부한다", with(minimalEnv(), "WS_MAX_MESSAGE_BYTES", "512"), []string{"WS_MAX_MESSAGE_BYTES"}},
		{"메시지 크기의 한도가 1 MiB를 넘으면 거부한다", with(minimalEnv(), "WS_MAX_MESSAGE_BYTES", "1048577"), []string{"WS_MAX_MESSAGE_BYTES"}},
		{"메시지 빈도의 한도에 주기가 없으면 거부한다", with(minimalEnv(), "WS_MESSAGE_RATE_LIMIT", "20"), []string{"WS_MESSAGE_RATE_LIMIT"}},
		{"일기 초안을 다시 시도하는 횟수가 음수면 거부한다", with(minimalEnv(), "DIARY_JOB_RETRIES", "-1"), []string{"DIARY_JOB_RETRIES"}},
		{"일기 초안을 다시 시도하는 횟수가 너무 크면 거부한다", with(minimalEnv(), "DIARY_JOB_RETRIES", "11"), []string{"DIARY_JOB_RETRIES"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(tt.environ)
			require.Error(t, err)

			var verr *ValidationError
			require.ErrorAs(t, err, &verr, "검증 오류는 ValidationError여야 한다")
			assert.ElementsMatch(t, tt.wantVars, verr.Vars())
			for _, v := range tt.wantVars {
				assert.Contains(t, err.Error(), v, "오류 문구에 변수 이름이 있어야 한다")
			}
		})
	}
}

func TestLoadFrom_DataKeyRotation(t *testing.T) {
	t.Run("둘째 키를 더하고 활성 버전을 올리면 두 키를 모두 들고 있는다", func(t *testing.T) {
		second := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x07}, 32))
		cfg, err := LoadFrom(with(minimalEnv(), "DATA_KEK_V2", second, "DATA_KEK_ACTIVE", "2"))
		require.NoError(t, err)
		assert.Equal(t, 2, cfg.DataKeys.Active)
		assert.Equal(t, []int{1, 2}, cfg.DataKeys.Versions())
		assert.Equal(t, bytes.Repeat([]byte{0x07}, 32), cfg.DataKeys.Keys[2].Reveal())
	})

	t.Run("DB에 저장할 수 있는 가장 큰 버전까지는 받는다", func(t *testing.T) {
		last := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x09}, 32))
		cfg, err := LoadFrom(with(minimalEnv(), "DATA_KEK_V32767", last, "DATA_KEK_ACTIVE", "32767"))
		require.NoError(t, err)
		assert.Equal(t, 32767, cfg.DataKeys.Active)
		assert.Equal(t, []int{1, 32767}, cfg.DataKeys.Versions())
	})

	t.Run("꺼낸 키를 고쳐도 설정 안의 키는 바뀌지 않는다", func(t *testing.T) {
		cfg, err := LoadFrom(minimalEnv())
		require.NoError(t, err)
		got := cfg.DataKeys.Keys[1].Reveal()
		got[0] = 0xff
		assert.Equal(t, byte(0x2a), cfg.DataKeys.Keys[1].Reveal()[0])
	})
}

func TestLoadFrom_Prod(t *testing.T) {
	t.Run("운영에서는 쿠키 보안 설정을 주지 않아도 켜진다", func(t *testing.T) {
		cfg, err := LoadFrom(prodEnv())
		require.NoError(t, err)
		assert.True(t, cfg.Env.IsProd())
		assert.True(t, cfg.Session.CookieSecure)
	})

	tests := []struct {
		name     string
		environ  map[string]string
		wantVars []string
	}{
		{"운영에서 쿠키 보안을 끄면 거부한다", with(prodEnv(), "SESSION_COOKIE_SECURE", "false"), []string{"SESSION_COOKIE_SECURE"}},
		{"운영에서 출처가 https가 아니면 거부한다", with(prodEnv(), "PUBLIC_ORIGIN", "http://naeil.example"), []string{"PUBLIC_ORIGIN"}},
		{"운영에서 출처를 주지 않으면 개발용 기본값이 걸러진다", without(prodEnv(), "PUBLIC_ORIGIN"), []string{"PUBLIC_ORIGIN"}},
		{"운영에서 공개된 개발용 키는 거부한다", with(prodEnv(), "DATA_KEK_V1", devOnlyDataKEK), []string{"DATA_KEK_V1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(tt.environ)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.ElementsMatch(t, tt.wantVars, verr.Vars())
		})
	}

	t.Run("개발에서는 공개된 개발용 키를 받아준다", func(t *testing.T) {
		_, err := LoadFrom(with(minimalEnv(), "DATA_KEK_V1", devOnlyDataKEK))
		require.NoError(t, err)
	})
}

func TestLoadFrom_ErrorsNeverContainValues(t *testing.T) {
	// 틀린 값 자체가 비밀일 수 있다. 예를 들어 키를 다른 변수 자리에 잘못 붙여 넣은 경우다.
	const leak = "SUPER-SECRET-VALUE-9f3a"
	tests := []struct {
		name    string
		environ map[string]string
	}{
		{"마스터 키 자리의 잘못된 값", with(minimalEnv(), "DATA_KEK_V1", leak)},
		{"둘째 마스터 키 자리의 잘못된 값", with(minimalEnv(), "DATA_KEK_V2", leak)},
		{"숫자 자리의 잘못된 값", with(minimalEnv(), "DATA_KEK_ACTIVE", leak)},
		{"시간 자리의 잘못된 값", with(minimalEnv(), "LLM_FALLBACK_AFTER", leak)},
		{"참거짓 자리의 잘못된 값", with(minimalEnv(), "SESSION_COOKIE_SECURE", leak)},
		{"출처 자리의 잘못된 값", with(minimalEnv(), "PUBLIC_ORIGIN", leak)},
		{"주소 자리의 잘못된 값", with(minimalEnv(), "HTTP_ADDR", leak)},
		{"로그 수준 자리의 잘못된 값", with(minimalEnv(), "LOG_LEVEL", leak)},
		{"실행 환경 자리의 잘못된 값", with(minimalEnv(), "APP_ENV", leak)},
		{"생각하기 수준 자리의 잘못된 값", with(minimalEnv(), "LLM_THINKING_ANALYSIS", leak)},
		{"쿠키 이름 자리의 잘못된 값", with(minimalEnv(), "SESSION_COOKIE_NAME", leak+" ;")},
		{"세션 수명 자리의 잘못된 값", with(minimalEnv(), "SESSION_ABSOLUTE_LIFETIME", leak)},
		{"세션을 다시 적는 간격 자리의 잘못된 값", with(minimalEnv(), "SESSION_TOUCH_INTERVAL", leak)},
		{"해시 메모리 자리의 잘못된 값", with(minimalEnv(), "PASSWORD_ARGON2_MEMORY_KIB", leak)},
		{"해시 동시 계산 수 자리의 잘못된 값", with(minimalEnv(), "PASSWORD_HASH_CONCURRENCY", leak)},
		{"키 캐시 크기 자리의 잘못된 값", with(minimalEnv(), "DATA_KEY_CACHE_SIZE", leak)},
		{"키 캐시 보관 시간 자리의 잘못된 값", with(minimalEnv(), "DATA_KEY_CACHE_MAX_AGE", leak)},
		{"믿는 프록시 자리의 잘못된 값", with(minimalEnv(), "TRUSTED_PROXIES", leak)},
		{"시도 한도 자리의 잘못된 값", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_IP", leak)},
		{"시도 한도의 주기 자리의 잘못된 값", with(minimalEnv(), "RATE_LIMIT_LOGIN_PER_EMAIL", "10/"+leak)},
		{"한도가 기억하는 키의 수 자리의 잘못된 값", with(minimalEnv(), "RATE_LIMIT_MAX_KEYS", leak)},
		// 키를 공급자 이름 자리에 잘못 붙여 넣기 쉽다. 바로 옆 변수다.
		{"AI 공급자 자리의 잘못된 값", with(minimalEnv(), "AI_PROVIDER", leak)},
		{"위기 판별 대기 시간 자리의 잘못된 값", with(minimalEnv(), "GATE_AI_TIMEOUT", leak)},
		{"끊긴 연결을 기다리는 시간 자리의 잘못된 값", with(minimalEnv(), "DISCONNECT_END_AFTER", leak)},
		{"메시지 크기 자리의 잘못된 값", with(minimalEnv(), "WS_MAX_MESSAGE_BYTES", leak)},
		{"메시지 빈도 자리의 잘못된 값", with(minimalEnv(), "WS_MESSAGE_RATE_LIMIT", leak+"/1m")},
		{"일기 초안 재시도 횟수 자리의 잘못된 값", with(minimalEnv(), "DIARY_JOB_RETRIES", leak)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(tt.environ)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), leak)
			assert.NotContains(t, fmt.Sprintf("%+v", err), leak)
		})
	}
}

func TestConfig_NeverPrintsSecrets(t *testing.T) {
	cfg, err := LoadFrom(with(minimalEnv(),
		"GEMINI_API_KEY", "gm-secret-key",
		"SONIOX_API_KEY", "sx-secret-key",
		"ELEVENLABS_API_KEY", "el-secret-key",
	))
	require.NoError(t, err)

	secrets := []string{
		"s3cr3t-pass", testDatabaseURL, testKEK,
		"gm-secret-key", "sx-secret-key", "el-secret-key",
		// 키 바이트가 숫자 배열이나 16진수로 찍히는 경우
		"42 42 42", "2a2a2a",
	}
	assertClean := func(t *testing.T, out string) {
		t.Helper()
		for _, s := range secrets {
			assert.NotContains(t, out, s)
		}
	}

	t.Run("fmt의 어떤 서식으로 찍어도 비밀 값이 나오지 않는다", func(t *testing.T) {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			assertClean(t, fmt.Sprintf(verb, cfg))
		}
	})

	t.Run("slog로 통째로 넘겨도 비밀 값이 나오지 않는다", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Info("config loaded", "config", cfg, "db", cfg.DatabaseURL, "kek", cfg.DataKeys.Keys[1])
		assertClean(t, buf.String())
		assert.Contains(t, buf.String(), `"gemini_key_set":true`)
		assert.Contains(t, buf.String(), `"ai_provider":"gemini"`, "어느 쪽으로 떴는지는 시작할 때의 로그에서 보여야 한다")
		assert.Contains(t, buf.String(), `"env":"dev"`)
	})

	t.Run("JSON으로 직렬화해도 비밀 값이 나오지 않는다", func(t *testing.T) {
		out, err := json.Marshal(cfg)
		require.NoError(t, err)
		assertClean(t, string(out))
	})
}

func TestLoadMigrationFrom(t *testing.T) {
	t.Run("마스터 키 없이 DB 주소만으로 읽힌다", func(t *testing.T) {
		m, err := LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL})
		require.NoError(t, err)
		assert.Equal(t, testDatabaseURL, m.DatabaseURL.Reveal())
		assert.Equal(t, slog.LevelInfo, m.LogLevel)
	})

	t.Run("스키마를 내리는 허락은 적어 줬을 때만 켜지고, 실행 환경이 비어 있으면 dev로 읽는다", func(t *testing.T) {
		m, err := LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL})
		require.NoError(t, err)
		assert.False(t, m.AllowDown, "DB 주소만 받아 도는 곳에서 내리는 명령이 열려 있으면 안 된다")
		assert.Equal(t, EnvDev, m.Env)

		m, err = LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL, "MIGRATE_ALLOW_DOWN": "true", "APP_ENV": "prod"})
		require.NoError(t, err)
		assert.True(t, m.AllowDown)
		assert.Equal(t, EnvProd, m.Env, "운영에서 막는 일은 명령이 한다. 설정은 읽은 대로 전한다")

		m, err = LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL, "MIGRATE_ALLOW_DOWN": "0"})
		require.NoError(t, err)
		assert.False(t, m.AllowDown)
	})

	t.Run("허락이 참거짓이 아니거나 모르는 실행 환경이면 변수 이름을 알려준다", func(t *testing.T) {
		_, err := LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL, "MIGRATE_ALLOW_DOWN": "please", "APP_ENV": "staging"})
		var verr *ValidationError
		require.ErrorAs(t, err, &verr)
		assert.ElementsMatch(t, []string{"MIGRATE_ALLOW_DOWN", "APP_ENV"}, verr.Vars())
		assert.NotContains(t, err.Error(), "please")
		assert.NotContains(t, err.Error(), "staging")
	})

	t.Run("DB 주소가 없으면 변수 이름을 알려준다", func(t *testing.T) {
		_, err := LoadMigrationFrom(map[string]string{})
		var verr *ValidationError
		require.ErrorAs(t, err, &verr)
		assert.Equal(t, []string{"DATABASE_URL"}, verr.Vars())
	})

	t.Run("모르는 로그 수준은 거부한다", func(t *testing.T) {
		_, err := LoadMigrationFrom(map[string]string{"DATABASE_URL": testDatabaseURL, "LOG_LEVEL": "loud"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LOG_LEVEL")
	})
}
