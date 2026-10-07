// Package config는 환경 변수에서 설정을 읽고 검증한다.
//
// 검증 오류는 어느 변수가 왜 틀렸는지만 말하고 값은 절대 담지 않는다.
// 접속 주소나 키가 틀린 채로 서버를 띄우면 그 오류가 로그에 남기 때문이다.
package config

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Env string

const (
	EnvDev  Env = "dev"
	EnvTest Env = "test"
	EnvProd Env = "prod"
)

func (e Env) IsProd() bool { return e == EnvProd }

// ThinkingLevel은 모델이 답하기 전에 생각에 쓰는 정도다.
type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
)

// AIProvider는 언어 모델의 답을 어디서 받을지다.
type AIProvider string

const (
	// AIProviderGemini는 실제 Gemini API를 부른다. GEMINI_API_KEY가 있어야 한다.
	AIProviderGemini AIProvider = "gemini"
	// AIProviderScripted는 외부로 나가지 않고 미리 정해 둔 답을 돌려준다. 키가 필요 없다.
	// 브라우저 흐름 테스트와 키 없이 돌려 보는 로컬 시연에 쓴다. 운영에서는 받지 않는다.
	AIProviderScripted AIProvider = "scripted"
)

// dataKEKLength는 마스터 키의 바이트 길이다. AES-256에 맞춘다.
const dataKEKLength = 32

// maxDataKEKVersion은 마스터 키 버전의 상한이다.
// 버전은 사용자마다의 키와 함께 DB의 smallint 컬럼에 저장되고, 암호화 계층도 같은 상한으로 키를 거부한다.
// 여기서 먼저 걸러야 어느 변수가 틀렸는지 이름으로 알려줄 수 있다.
const maxDataKEKVersion = math.MaxInt16

// devOnlyDataKEK는 Makefile에 적힌 개발 전용 마스터 키다.
// 공개된 값이라 운영에서 이 키로 뜨면 암호화가 아무 의미가 없다. 그래서 prod에서는 거부한다.
const devOnlyDataKEK = "bmFlaWwtZGV2LW9ubHkta2VrLWRvLW5vdC11c2UhISE=" // #nosec G101 -- 공개된 개발 전용 값이고, 운영에서 걸러내려고 둔다.

// Config는 서버와 작업자가 쓰는 설정 전체다.
type Config struct {
	Env          Env
	HTTPAddr     string
	DatabaseURL  Secret
	LogLevel     slog.Level
	PublicOrigin string
	// TrustedProxies는 X-Forwarded-For를 믿어도 되는 앞단 프록시의 주소 범위다.
	// 비어 있으면 헤더를 보지 않고 연결의 상대 주소를 클라이언트 주소로 쓴다.
	TrustedProxies []netip.Prefix
	Session        Session
	RateLimits     RateLimits
	Password       Password
	DataKeys       DataKeys
	DataKeyCache   DataKeyCache
	Providers      Providers
	LLM            LLM
	Conversation   Conversation
	WebSocket      WebSocket
	Voice          Voice
	DiaryJob       DiaryJob
	AnalysisJob    AnalysisJob
}

type Session struct {
	CookieName   string
	CookieSecure bool
	// AbsoluteLifetime은 로그인한 때부터 잰다. 계속 쓰고 있어도 이 시간이 지나면 다시 로그인해야 한다.
	AbsoluteLifetime time.Duration
	// IdleLifetime은 마지막으로 쓴 때부터 잰다. 이 시간 동안 쓰지 않으면 세션이 끝난다.
	IdleLifetime time.Duration
	// TouchInterval은 마지막으로 쓴 시각을 DB에 다시 적는 최소 간격이다.
	// 요청마다 적으면 읽기만 하는 요청도 모두 쓰기가 된다.
	TouchInterval time.Duration
}

// RateLimit은 "Period 동안 Burst번"이라는 한도다.
// 한꺼번에 Burst번까지 쓸 수 있고, 쓴 만큼은 Period에 걸쳐 고르게 다시 찬다.
type RateLimit struct {
	Burst  int
	Period time.Duration
}

// refillInterval은 시도 한 번의 몫이 다시 차는 데 걸리는 시간이다. 두 한도의 빠르기를 견줄 때 쓴다.
// 곱해서 견주면 가장 큰 주기와 횟수에서 정수 범위를 넘으므로 나눈 값을 소수로 견준다.
func (r RateLimit) refillInterval() float64 {
	return float64(r.Period) / float64(r.Burst)
}

// String은 설정에 적는 꼴(10/1m)로 돌려준다.
func (r RateLimit) String() string {
	return strconv.Itoa(r.Burst) + "/" + r.Period.String()
}

// RateLimits는 가입과 로그인을 얼마나 자주 시도할 수 있는지다.
type RateLimits struct {
	SignupPerIP RateLimit
	LoginPerIP  RateLimit
	// LoginPerEmail은 한 주소에서 한 계정에 대한 시도를 센다. 한 계정의 비밀번호를 대입해 보는 것을 늦춘다.
	LoginPerEmail RateLimit
	// LoginPerEmailTotal은 한 계정에 대한 시도를 주소를 가리지 않고 모두 센다. 주소를 바꿔 가며 대입해 보는 것을 늦춘다.
	// LoginPerEmail보다 한꺼번에 더 많이 받고 그보다 느리지 않게 다시 차야 한다. 그렇지 않으면 이메일을 아는 사람이
	// 한 주소에서 틀린 비밀번호를 꾸준히 넣는 것만으로 이 통을 비워, 계정의 주인을 끝없이 로그인하지 못하게 할 수 있다.
	LoginPerEmailTotal RateLimit
	// MaxKeys는 한도 하나가 기억하는 키(주소나 계정)의 최대 수다. 메모리가 끝없이 늘지 않게 한다.
	MaxKeys int
}

// Password는 비밀번호 해시(argon2id)를 만드는 값이다.
// 값을 올리면 그 뒤로 로그인하는 사용자의 해시가 새 값으로 다시 만들어진다.
type Password struct {
	MemoryKiB   uint32
	Time        uint32
	Parallelism uint8
	// HashConcurrency는 동시에 계산할 수 있는 해시의 수다.
	// 해시 하나가 MemoryKiB만큼 메모리를 쓰므로, 로그인이 몰려도 MemoryKiB x HashConcurrency를 넘겨 쓰지 않는다.
	HashConcurrency int
}

// DataKeyCache는 풀어 둔 사용자별 데이터 키를 프로세스가 들고 있는 한도다.
type DataKeyCache struct {
	Size int
	// MaxAge가 지나면 키 행을 다시 읽는다. 다른 프로세스에서 지운 계정의 키를 이 시간 넘게 들고 있지 않는다.
	MaxAge time.Duration
}

// DataKeys는 사용자별 데이터 키를 감싸는 마스터 키 묶음이다.
// 키를 바꿔도 옛 키로 감싼 데이터 키를 풀 수 있어야 하므로 버전별로 모두 들고 있는다.
type DataKeys struct {
	// Active는 새로 감쌀 때 쓰는 버전이다.
	Active int
	Keys   map[int]SecretBytes
}

// Versions는 들고 있는 키 버전을 오름차순으로 돌려준다.
func (d DataKeys) Versions() []int {
	versions := make([]int, 0, len(d.Keys))
	for v := range d.Keys {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions
}

// Providers는 외부 AI 서비스의 키다. 아직 붙이지 않은 서비스는 비어 있을 수 있다.
type Providers struct {
	GeminiAPIKey     Secret
	SonioxAPIKey     Secret
	ElevenLabsAPIKey Secret
}

// LLM은 일마다 어떤 모델을 어떻게 부를지 정한다.
// 모델은 몇 달마다 새로 나오므로 코드가 아니라 여기서 바꾼다.
type LLM struct {
	// Provider를 설정에 적지 않았을 때: 운영에서는 gemini다. 그 밖에서는 GEMINI_API_KEY가 있으면 gemini, 없으면 scripted다.
	// 키 없이 받아서 바로 띄운 개발 서버도 대화가 되게 하려는 것이다. 어느 쪽으로 떴는지는 시작할 때의 설정 로그에 남는다.
	Provider                  AIProvider
	ConversationModel         string
	ConversationFallbackModel string
	GateModel                 string
	AnalysisModel             string
	ConversationThinking      ThinkingLevel
	GateThinking              ThinkingLevel
	AnalysisThinking          ThinkingLevel
	// FallbackAfter는 대화 모델의 첫 글자를 기다리는 시간이다. 넘기면 예비 모델을 함께 부른다.
	FallbackAfter time.Duration
	// ReplyBudget은 대화 모델의 답을 기다리는 시간의 상한이다. 두 번의 시도를 합친 시간이다.
	// 넘기면 미리 써 둔 말로 바꾼다. 늦게 오는 말보다 제때 오는 안전한 말이 낫다.
	//
	// 이 값이 없으면 한 턴이 부른 쪽의 컨텍스트만 따라 몇 분씩 이어질 수 있고, 그동안 사용자는 답도 못 받고
	// 끝내기도 하지 못한다. FallbackAfter보다 길어야 한다. 그보다 짧으면 예비 모델을 부를 틈이 없어
	// 이 값이 예비 모델을 조용히 꺼 버린다.
	ReplyBudget time.Duration
	// GateTimeout은 위기 판별 모델의 답을 기다리는 시간이다. 넘기면 판별이 실패한 것으로 보고 규칙의 판정만으로 대응한다.
	// 사용자는 이 시간이 지나야 답을 받으므로 길게 잡지 않는다.
	GateTimeout time.Duration

	// AnalysisMaxOutputTokens는 신호 추출 모델의 출력 한도다.
	//
	// 한 번의 답에 여덟 항목의 판단과 항목마다의 근거 발화가 모두 담기고, 겉으로 보이지 않는 생각 토큰도
	// 이 한도에서 빠진다. 빠듯하면 답이 항목 중간에서 잘리고, 잘린 답은 여덟 항목을 채우지 못해 버려진다.
	// 대화의 답과 달리 사람이 기다리는 값이 아니므로 넉넉하게 잡는다.
	AnalysisMaxOutputTokens int
	// AnalysisBudget은 신호 추출 모델의 답을 기다리는 시간이다. 대화가 끝난 뒤에 도는 일이라 느린 답도 기다릴 수 있지만,
	// 아주 느린 응답 하나에 작업자 한 자리가 오래 묶이지 않게 끝을 둔다.
	AnalysisBudget time.Duration
}

// Conversation은 말이 없는 대화와 끊긴 연결을 얼마나 기다릴지다.
type Conversation struct {
	// IdleCheckAfter 동안 말이 없으면 한 번 묻는다.
	IdleCheckAfter time.Duration
	// IdleEndAfter는 묻고 난 뒤에 답을 기다리는 시간이다. 그래도 말이 없으면 대화를 끝낸다.
	IdleEndAfter time.Duration
	// DisconnectEndAfter는 연결이 끊긴 대화를 열어 둔 채 기다리는 시간이다. 그 안에 돌아오면 같은 대화를 이어간다.
	DisconnectEndAfter time.Duration
}

// WebSocket은 대화 채널이 클라이언트에서 받는 메시지의 한도다.
type WebSocket struct {
	// MaxMessageBytes는 메시지 하나의 최대 크기다.
	MaxMessageBytes int64
	// MessageRate는 연결 하나가 보낼 수 있는 메시지의 빈도다. 글 하나마다 모델을 두세 번 부르므로 비용의 상한이기도 하다.
	MessageRate RateLimit
}

// DiaryJob은 일기 초안을 만드는 작업의 설정이다.
type DiaryJob struct {
	// Retries는 초안을 만들지 못했을 때 다시 시도하는 횟수다. 처음 한 번은 여기에 들지 않는다.
	// 다 쓰고도 만들지 못하면 빈 초안을 남겨 사용자가 직접 쓸 수 있게 한다.
	Retries int
}

// MaxAttempts는 처음 시도까지 더한 전체 시도 횟수다. 작업 큐에는 이 값을 넘긴다.
func (d DiaryJob) MaxAttempts() int {
	return d.Retries + 1
}

// AnalysisJob은 대화에서 마음 신호를 뽑는 작업의 설정이다.
type AnalysisJob struct {
	// Retries는 뽑지 못했을 때 다시 시도하는 횟수다. 처음 한 번은 여기에 들지 않는다.
	//
	// 다 쓰고도 뽑지 못하면 그 대화의 분석을 failed로 닫고 신호 행을 남기지 않는다.
	// 일부 항목만 남기는 길은 없다. 그날은 분석이 끝날 때까지 대화하지 않은 날과 똑같이 다뤄진다.
	// 초안과 달리 사용자가 화면에서 기다리는 값이 아니므로 초안보다 넉넉하게 잡아도 된다.
	Retries int
}

// MaxAttempts는 처음 시도까지 더한 전체 시도 횟수다. 작업 큐에는 이 값을 넘긴다.
func (a AnalysisJob) MaxAttempts() int {
	return a.Retries + 1
}

// LogValue는 설정을 통째로 로그에 넘겨도 비밀 값이 나가지 않게 한다.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", string(c.Env)),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("public_origin", c.PublicOrigin),
		slog.Any("trusted_proxies", prefixStrings(c.TrustedProxies)),
		slog.String("rate_limit_signup_per_ip", c.RateLimits.SignupPerIP.String()),
		slog.String("rate_limit_login_per_ip", c.RateLimits.LoginPerIP.String()),
		slog.String("rate_limit_login_per_email", c.RateLimits.LoginPerEmail.String()),
		slog.String("rate_limit_login_per_email_total", c.RateLimits.LoginPerEmailTotal.String()),
		slog.Int("rate_limit_max_keys", c.RateLimits.MaxKeys),
		slog.String("session_cookie_name", c.Session.CookieName),
		slog.Bool("session_cookie_secure", c.Session.CookieSecure),
		slog.Duration("session_absolute_lifetime", c.Session.AbsoluteLifetime),
		slog.Duration("session_idle_lifetime", c.Session.IdleLifetime),
		slog.Duration("session_touch_interval", c.Session.TouchInterval),
		slog.Uint64("password_argon2_memory_kib", uint64(c.Password.MemoryKiB)),
		slog.Uint64("password_argon2_time", uint64(c.Password.Time)),
		slog.Uint64("password_argon2_parallelism", uint64(c.Password.Parallelism)),
		slog.Int("password_hash_concurrency", c.Password.HashConcurrency),
		slog.Int("data_key_cache_size", c.DataKeyCache.Size),
		slog.Duration("data_key_cache_max_age", c.DataKeyCache.MaxAge),
		slog.Int("data_kek_active", c.DataKeys.Active),
		slog.Any("data_kek_versions", c.DataKeys.Versions()),
		slog.String("ai_provider", string(c.LLM.Provider)),
		slog.Bool("gemini_key_set", c.Providers.GeminiAPIKey.IsSet()),
		slog.Bool("soniox_key_set", c.Providers.SonioxAPIKey.IsSet()),
		slog.Bool("elevenlabs_key_set", c.Providers.ElevenLabsAPIKey.IsSet()),
		slog.String("llm_model_conversation", c.LLM.ConversationModel),
		slog.String("llm_model_conversation_fallback", c.LLM.ConversationFallbackModel),
		slog.String("llm_model_gate", c.LLM.GateModel),
		slog.String("llm_model_analysis", c.LLM.AnalysisModel),
		slog.String("llm_thinking_conversation", string(c.LLM.ConversationThinking)),
		slog.String("llm_thinking_gate", string(c.LLM.GateThinking)),
		slog.String("llm_thinking_analysis", string(c.LLM.AnalysisThinking)),
		slog.Duration("llm_fallback_after", c.LLM.FallbackAfter),
		slog.Duration("llm_reply_budget", c.LLM.ReplyBudget),
		slog.Duration("gate_ai_timeout", c.LLM.GateTimeout),
		slog.Int("llm_max_output_analysis", c.LLM.AnalysisMaxOutputTokens),
		slog.Duration("llm_analysis_budget", c.LLM.AnalysisBudget),
		slog.Duration("idle_check_after", c.Conversation.IdleCheckAfter),
		slog.Duration("idle_end_after", c.Conversation.IdleEndAfter),
		slog.Duration("disconnect_end_after", c.Conversation.DisconnectEndAfter),
		slog.Int64("ws_max_message_bytes", c.WebSocket.MaxMessageBytes),
		slog.String("ws_message_rate_limit", c.WebSocket.MessageRate.String()),
		slog.String("voice_provider", string(c.Voice.Provider)),
		slog.String("soniox_model", c.Voice.SonioxModel),
		slog.Any("stt_language_hints", c.Voice.LanguageHints),
		slog.Duration("stt_max_endpoint_delay", c.Voice.MaxEndpointDelay),
		slog.Float64("stt_mishear_below", float64(c.Voice.MishearBelow)),
		slog.String("elevenlabs_model", c.Voice.TTSModel),
		slog.String("elevenlabs_voice_id", c.Voice.VoiceID),
		slog.Float64("elevenlabs_speed", c.Voice.Speed),
		slog.Bool("voice_barge_in", c.Voice.BargeIn),
		slog.Int("diary_job_retries", c.DiaryJob.Retries),
		slog.Int("analysis_job_retries", c.AnalysisJob.Retries),
	)
}

// Problem은 설정 하나가 왜 틀렸는지다. 값은 담지 않는다.
type Problem struct {
	Var    string
	Reason string
}

// ValidationError는 틀린 설정을 한 번에 모아 알려준다.
// 하나 고치고 다시 띄우기를 되풀이하지 않게 하려는 것이다.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		parts = append(parts, p.Var+": "+p.Reason)
	}
	return "invalid configuration: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Vars() []string {
	vars := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		vars = append(vars, p.Var)
	}
	return vars
}

// raw는 환경 변수를 글자 그대로 받는 틀이다.
// 숫자나 시간을 라이브러리가 바로 변환하게 두면 변환 오류 메시지에 값이 섞여 나온다.
// 그래서 전부 문자열로 받고 변환과 오류 문구는 이 패키지가 맡는다.
type raw struct {
	AppEnv              string `env:"APP_ENV" envDefault:"dev"`
	HTTPAddr            string `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL         string `env:"DATABASE_URL,required,notEmpty"`
	LogLevel            string `env:"LOG_LEVEL" envDefault:"info"`
	PublicOrigin        string `env:"PUBLIC_ORIGIN" envDefault:"http://localhost:5173"`
	TrustedProxies      string `env:"TRUSTED_PROXIES"`
	SessionCookieName   string `env:"SESSION_COOKIE_NAME" envDefault:"naeil_session"`
	SessionCookieSecure string `env:"SESSION_COOKIE_SECURE"`

	SessionAbsoluteLifetime string `env:"SESSION_ABSOLUTE_LIFETIME" envDefault:"720h"`
	SessionIdleLifetime     string `env:"SESSION_IDLE_LIFETIME" envDefault:"336h"`
	SessionTouchInterval    string `env:"SESSION_TOUCH_INTERVAL" envDefault:"5m"`

	RateLimitSignupPerIP        string `env:"RATE_LIMIT_SIGNUP_PER_IP" envDefault:"20/1h"`
	RateLimitLoginPerIP         string `env:"RATE_LIMIT_LOGIN_PER_IP" envDefault:"30/5m"`
	RateLimitLoginPerEmail      string `env:"RATE_LIMIT_LOGIN_PER_EMAIL" envDefault:"10/10m"`
	RateLimitLoginPerEmailTotal string `env:"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL" envDefault:"60/1h"`
	RateLimitMaxKeys            string `env:"RATE_LIMIT_MAX_KEYS" envDefault:"50000"`

	PasswordArgon2MemoryKiB   string `env:"PASSWORD_ARGON2_MEMORY_KIB" envDefault:"19456"`
	PasswordArgon2Time        string `env:"PASSWORD_ARGON2_TIME" envDefault:"2"`
	PasswordArgon2Parallelism string `env:"PASSWORD_ARGON2_PARALLELISM" envDefault:"1"`
	PasswordHashConcurrency   string `env:"PASSWORD_HASH_CONCURRENCY" envDefault:"4"`

	DataKeyCacheSize   string `env:"DATA_KEY_CACHE_SIZE" envDefault:"1024"`
	DataKeyCacheMaxAge string `env:"DATA_KEY_CACHE_MAX_AGE" envDefault:"10m"`

	DataKEKActive    string `env:"DATA_KEK_ACTIVE" envDefault:"1"`
	DataKEKV1        string `env:"DATA_KEK_V1,required,notEmpty"`
	GeminiAPIKey     string `env:"GEMINI_API_KEY"`
	SonioxAPIKey     string `env:"SONIOX_API_KEY"`
	ElevenLabsAPIKey string `env:"ELEVENLABS_API_KEY"`

	LLMModelConversation         string `env:"LLM_MODEL_CONVERSATION" envDefault:"gemini-3.8-flash"`
	LLMModelConversationFallback string `env:"LLM_MODEL_CONVERSATION_FALLBACK" envDefault:"gemini-3.6-flash"`
	LLMModelGate                 string `env:"LLM_MODEL_GATE" envDefault:"gemini-3.5-flash"`
	LLMModelAnalysis             string `env:"LLM_MODEL_ANALYSIS" envDefault:"gemini-3.8-flash"`
	LLMThinkingConversation      string `env:"LLM_THINKING_CONVERSATION" envDefault:"low"`
	LLMThinkingGate              string `env:"LLM_THINKING_GATE" envDefault:"minimal"`
	LLMThinkingAnalysis          string `env:"LLM_THINKING_ANALYSIS" envDefault:"low"`
	LLMFallbackAfter             string `env:"LLM_FALLBACK_AFTER" envDefault:"3s"`
	LLMReplyBudget               string `env:"LLM_REPLY_BUDGET" envDefault:"12s"`
	LLMMaxOutputAnalysis         string `env:"LLM_MAX_OUTPUT_ANALYSIS" envDefault:"8192"`
	LLMAnalysisBudget            string `env:"LLM_ANALYSIS_BUDGET" envDefault:"60s"`

	// 기본값을 여기에 적지 않는다. 적지 않았다는 사실이 있어야 환경과 키를 보고 고를 수 있다.
	AIProvider    string `env:"AI_PROVIDER"`
	GateAITimeout string `env:"GATE_AI_TIMEOUT" envDefault:"2500ms"`

	IdleCheckAfter     string `env:"IDLE_CHECK_AFTER" envDefault:"3m"`
	IdleEndAfter       string `env:"IDLE_END_AFTER" envDefault:"3m"`
	DisconnectEndAfter string `env:"DISCONNECT_END_AFTER" envDefault:"30m"`

	WSMaxMessageBytes  string `env:"WS_MAX_MESSAGE_BYTES" envDefault:"16384"`
	WSMessageRateLimit string `env:"WS_MESSAGE_RATE_LIMIT" envDefault:"20/1m"`

	// 기본값을 여기에 적지 않는다. 적지 않았다는 사실이 있어야 키를 보고 고를 수 있다.
	VoiceProvider       string `env:"VOICE_PROVIDER"`
	SonioxURL           string `env:"SONIOX_URL" envDefault:"wss://stt-rt.soniox.com/transcribe-websocket"`
	SonioxModel         string `env:"SONIOX_MODEL" envDefault:"stt-rt-v5"`
	STTLanguageHints    string `env:"STT_LANGUAGE_HINTS" envDefault:"ko"`
	STTMaxEndpointDelay string `env:"STT_MAX_ENDPOINT_DELAY" envDefault:"3s"`
	STTMishearBelow     string `env:"STT_MISHEAR_BELOW" envDefault:"0.6"`
	ElevenLabsURL       string `env:"ELEVENLABS_URL" envDefault:"https://api.elevenlabs.io"`
	ElevenLabsVoiceID   string `env:"ELEVENLABS_VOICE_ID" envDefault:"hWXqitL3DEOLD49pgNWR"`
	ElevenLabsModel     string `env:"ELEVENLABS_MODEL" envDefault:"eleven_flash_v2_5"`
	ElevenLabsSpeed     string `env:"ELEVENLABS_SPEED" envDefault:"1.1"`
	VoiceBargeIn        string `env:"VOICE_BARGE_IN" envDefault:"true"`

	DiaryJobRetries    string `env:"DIARY_JOB_RETRIES" envDefault:"3"`
	AnalysisJobRetries string `env:"ANALYSIS_JOB_RETRIES" envDefault:"3"`
}

// Load는 프로세스의 환경 변수에서 설정을 읽는다.
func Load() (Config, error) {
	return LoadFrom(environMap(os.Environ()))
}

// LoadFrom은 주어진 환경에서 설정을 읽는다. 테스트가 프로세스 환경을 건드리지 않게 한다.
func LoadFrom(environ map[string]string) (Config, error) {
	var r raw
	problems := parseRaw(&r, environ)

	cfg := Config{
		HTTPAddr:     r.HTTPAddr,
		DatabaseURL:  NewSecret(r.DatabaseURL),
		PublicOrigin: r.PublicOrigin,
		Session:      Session{CookieName: r.SessionCookieName},
		Providers: Providers{
			GeminiAPIKey:     NewSecret(strings.TrimSpace(r.GeminiAPIKey)),
			SonioxAPIKey:     NewSecret(strings.TrimSpace(r.SonioxAPIKey)),
			ElevenLabsAPIKey: NewSecret(strings.TrimSpace(r.ElevenLabsAPIKey)),
		},
		LLM: LLM{
			ConversationModel:         strings.TrimSpace(r.LLMModelConversation),
			ConversationFallbackModel: strings.TrimSpace(r.LLMModelConversationFallback),
			GateModel:                 strings.TrimSpace(r.LLMModelGate),
			AnalysisModel:             strings.TrimSpace(r.LLMModelAnalysis),
		},
	}

	add := func(name, reason string) {
		problems = append(problems, Problem{Var: name, Reason: reason})
	}

	switch e := Env(r.AppEnv); e {
	case EnvDev, EnvTest, EnvProd:
		cfg.Env = e
	default:
		add("APP_ENV", "must be one of dev, test, prod")
	}

	if !validListenAddr(r.HTTPAddr) {
		add("HTTP_ADDR", "must look like host:port or :port")
	}

	level, ok := parseLogLevel(r.LogLevel)
	if !ok {
		add("LOG_LEVEL", "must be one of debug, info, warn, error")
	}
	cfg.LogLevel = level

	if reason := checkOrigin(r.PublicOrigin, cfg.Env.IsProd()); reason != "" {
		add("PUBLIC_ORIGIN", reason)
	}

	proxies, ok := parseTrustedProxies(r.TrustedProxies)
	if !ok {
		add("TRUSTED_PROXIES", "must be a comma-separated list of IP ranges such as 10.42.0.0/16")
	}
	cfg.TrustedProxies = proxies

	cfg.Session.CookieSecure = cfg.Env.IsProd()
	if r.SessionCookieSecure != "" {
		secure, err := strconv.ParseBool(r.SessionCookieSecure)
		switch {
		case err != nil:
			add("SESSION_COOKIE_SECURE", "must be true or false")
		case cfg.Env.IsProd() && !secure:
			add("SESSION_COOKIE_SECURE", "must be true when APP_ENV=prod")
		default:
			cfg.Session.CookieSecure = secure
		}
	}

	if reason := checkCookieName(r.SessionCookieName, cfg.Session.CookieSecure); reason != "" {
		add("SESSION_COOKIE_NAME", reason)
	}

	limits, limitProblems := loadRateLimits(r)
	cfg.RateLimits = limits
	problems = append(problems, limitProblems...)

	session, sessionProblems := loadSessionLifetimes(r)
	cfg.Session.AbsoluteLifetime = session.AbsoluteLifetime
	cfg.Session.IdleLifetime = session.IdleLifetime
	cfg.Session.TouchInterval = session.TouchInterval
	problems = append(problems, sessionProblems...)

	password, passwordProblems := loadPassword(r)
	cfg.Password = password
	problems = append(problems, passwordProblems...)

	cache, cacheProblems := loadDataKeyCache(r)
	cfg.DataKeyCache = cache
	problems = append(problems, cacheProblems...)

	keys, keyProblems := loadDataKeys(r, environ, cfg.Env.IsProd())
	cfg.DataKeys = keys
	problems = append(problems, keyProblems...)

	for _, m := range []struct{ name, value string }{
		{"LLM_MODEL_CONVERSATION", cfg.LLM.ConversationModel},
		{"LLM_MODEL_CONVERSATION_FALLBACK", cfg.LLM.ConversationFallbackModel},
		{"LLM_MODEL_GATE", cfg.LLM.GateModel},
		{"LLM_MODEL_ANALYSIS", cfg.LLM.AnalysisModel},
	} {
		if m.value == "" {
			add(m.name, "must not be empty")
		}
	}

	for _, t := range []struct {
		name  string
		value string
		dst   *ThinkingLevel
	}{
		{"LLM_THINKING_CONVERSATION", r.LLMThinkingConversation, &cfg.LLM.ConversationThinking},
		{"LLM_THINKING_GATE", r.LLMThinkingGate, &cfg.LLM.GateThinking},
		{"LLM_THINKING_ANALYSIS", r.LLMThinkingAnalysis, &cfg.LLM.AnalysisThinking},
	} {
		switch lv := ThinkingLevel(strings.ToLower(strings.TrimSpace(t.value))); lv {
		case ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh:
			*t.dst = lv
		default:
			add(t.name, "must be one of minimal, low, medium, high")
		}
	}

	fallbackAfter, err := time.ParseDuration(strings.TrimSpace(r.LLMFallbackAfter))
	switch {
	case err != nil:
		add("LLM_FALLBACK_AFTER", "must be a duration such as 3s or 2500ms")
	case fallbackAfter <= 0:
		add("LLM_FALLBACK_AFTER", "must be greater than zero")
	default:
		cfg.LLM.FallbackAfter = fallbackAfter
	}

	replyBudget, err := time.ParseDuration(strings.TrimSpace(r.LLMReplyBudget))
	switch {
	case err != nil:
		add("LLM_REPLY_BUDGET", "must be a duration such as 12s")
	case replyBudget <= 0:
		add("LLM_REPLY_BUDGET", "must be greater than zero")
	case replyBudget > maxReplyBudget:
		add("LLM_REPLY_BUDGET", "must not be longer than "+maxReplyBudget.String())
	case cfg.LLM.FallbackAfter > 0 && replyBudget <= cfg.LLM.FallbackAfter:
		// 여기서 막지 않으면 예비 모델을 부르기도 전에 시간이 끝난다. 설정 하나가 다른 설정을 조용히 끄는 꼴이다.
		add("LLM_REPLY_BUDGET", "must be longer than LLM_FALLBACK_AFTER")
	default:
		cfg.LLM.ReplyBudget = replyBudget
	}

	provider, providerProblems := loadAIProvider(r, cfg.Env.IsProd(), cfg.Providers.GeminiAPIKey.IsSet())
	cfg.LLM.Provider = provider
	problems = append(problems, providerProblems...)

	voice, voiceProblems := loadVoice(r, cfg.Env.IsProd(),
		cfg.Providers.SonioxAPIKey.IsSet(), cfg.Providers.ElevenLabsAPIKey.IsSet())
	cfg.Voice = voice
	problems = append(problems, voiceProblems...)

	flow, flowProblems := loadConversationFlow(r)
	cfg.LLM.GateTimeout = flow.gateTimeout
	cfg.Conversation = flow.conversation
	cfg.WebSocket = flow.webSocket
	cfg.DiaryJob = flow.diaryJob
	cfg.AnalysisJob = flow.analysisJob
	cfg.LLM.AnalysisBudget = flow.analysisBudget
	cfg.LLM.AnalysisMaxOutputTokens = flow.analysisMaxOutputTokens
	problems = append(problems, flowProblems...)

	if len(problems) > 0 {
		return Config{}, &ValidationError{Problems: problems}
	}
	return cfg, nil
}

// Migration은 마이그레이션 명령에 필요한 최소 설정이다.
// 스키마만 고치는 작업에 마스터 키까지 넘겨줄 이유가 없어 따로 둔다.
type Migration struct {
	Env         Env
	DatabaseURL Secret
	LogLevel    slog.Level
	// AllowDown은 스키마를 내리는 명령을 허락한다. 내리는 구문은 테이블을 통째로 지운다.
	//
	// 기본은 막혀 있다. 마이그레이션은 DB 주소만 받아서 도는 일이 많아(Job, docker run) APP_ENV가 비어 있기 쉽고,
	// 비어 있으면 dev로 읽힌다. 그래서 "운영이 아니면 허락"이 아니라 "허락한다고 적었을 때만 허락"으로 둔다.
	// APP_ENV=prod에서는 이 값이 켜져 있어도 내리지 않는다.
	AllowDown bool
}

type rawMigration struct {
	AppEnv      string `env:"APP_ENV" envDefault:"dev"`
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`
	AllowDown   string `env:"MIGRATE_ALLOW_DOWN"`
}

// LoadMigration은 프로세스의 환경 변수에서 마이그레이션 설정을 읽는다.
func LoadMigration() (Migration, error) {
	return LoadMigrationFrom(environMap(os.Environ()))
}

// LoadMigrationFrom은 주어진 환경에서 마이그레이션 설정을 읽는다.
func LoadMigrationFrom(environ map[string]string) (Migration, error) {
	var r rawMigration
	problems := parseRaw(&r, environ)

	appEnv := Env(r.AppEnv)
	switch appEnv {
	case EnvDev, EnvTest, EnvProd:
	default:
		problems = append(problems, Problem{Var: "APP_ENV", Reason: "must be one of dev, test, prod"})
	}

	level, ok := parseLogLevel(r.LogLevel)
	if !ok {
		problems = append(problems, Problem{Var: "LOG_LEVEL", Reason: "must be one of debug, info, warn, error"})
	}

	allowDown := false
	if value := strings.TrimSpace(r.AllowDown); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			problems = append(problems, Problem{Var: "MIGRATE_ALLOW_DOWN", Reason: "must be true or false"})
		}
		allowDown = parsed
	}

	if len(problems) > 0 {
		return Migration{}, &ValidationError{Problems: problems}
	}
	return Migration{Env: appEnv, DatabaseURL: NewSecret(r.DatabaseURL), LogLevel: level, AllowDown: allowDown}, nil
}

// parseRaw는 라이브러리의 오류를 값이 없는 Problem으로 바꾼다.
func parseRaw(dst any, environ map[string]string) []Problem {
	err := env.ParseWithOptions(dst, env.Options{Environment: environ})
	if err == nil {
		return nil
	}

	var agg env.AggregateError
	if !errors.As(err, &agg) {
		// 틀 자체가 잘못된 경우다. 값과 무관한 오류라 종류만 알린다.
		return []Problem{{Var: "(config)", Reason: "could not be parsed"}}
	}

	problems := make([]Problem, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		var notSet env.VarIsNotSetError
		var empty env.EmptyVarError
		switch {
		case errors.As(e, &notSet):
			problems = append(problems, Problem{Var: notSet.Key, Reason: "is required but not set"})
		case errors.As(e, &empty):
			problems = append(problems, Problem{Var: empty.Key, Reason: "is required but empty"})
		default:
			problems = append(problems, Problem{Var: "(config)", Reason: "could not be parsed"})
		}
	}
	return problems
}

// 세션 수명의 상한이다. 자릿수를 잘못 적어 사실상 끝나지 않는 세션이 만들어지는 일을 막는다.
const maxSessionLifetime = 366 * 24 * time.Hour

func loadSessionLifetimes(r raw) (Session, []Problem) {
	var s Session
	var problems []Problem

	parse := func(name, value string, dst *time.Duration) bool {
		d, err := time.ParseDuration(strings.TrimSpace(value))
		switch {
		case err != nil:
			problems = append(problems, Problem{Var: name, Reason: "must be a duration such as 720h or 30m"})
		case d <= 0:
			problems = append(problems, Problem{Var: name, Reason: "must be greater than zero"})
		case d > maxSessionLifetime:
			problems = append(problems, Problem{Var: name, Reason: "must not be longer than 8784h (366 days)"})
		default:
			*dst = d
			return true
		}
		return false
	}

	absoluteOK := parse("SESSION_ABSOLUTE_LIFETIME", r.SessionAbsoluteLifetime, &s.AbsoluteLifetime)
	idleOK := parse("SESSION_IDLE_LIFETIME", r.SessionIdleLifetime, &s.IdleLifetime)
	touchOK := parse("SESSION_TOUCH_INTERVAL", r.SessionTouchInterval, &s.TouchInterval)

	// 서로의 크기 관계는 값 하나하나가 멀쩡할 때만 본다. 이미 틀린 값을 두고 한 번 더 지적하지 않는다.
	if absoluteOK && idleOK && s.IdleLifetime > s.AbsoluteLifetime {
		problems = append(problems, Problem{Var: "SESSION_IDLE_LIFETIME", Reason: "must not be longer than SESSION_ABSOLUTE_LIFETIME"})
	}
	// 다시 적는 간격이 쉬는 시간의 한도보다 길면, 계속 쓰고 있는 세션이 쓰는 도중에 끝난다.
	if idleOK && touchOK && s.TouchInterval >= s.IdleLifetime {
		problems = append(problems, Problem{Var: "SESSION_TOUCH_INTERVAL", Reason: "must be shorter than SESSION_IDLE_LIFETIME"})
	}
	return s, problems
}

const (
	maxRateLimitBurst = 1_000_000
	// 하루보다 긴 주기는 오타로 본다. 한도는 프로세스의 메모리에만 있어서 다시 뜨면 어차피 처음부터 센다.
	maxRateLimitPeriod  = 24 * time.Hour
	maxRateLimitMaxKeys = 10_000_000
)

func loadRateLimits(r raw) (RateLimits, []Problem) {
	var limits RateLimits
	var problems []Problem

	for _, l := range []struct {
		name  string
		value string
		dst   *RateLimit
	}{
		{"RATE_LIMIT_SIGNUP_PER_IP", r.RateLimitSignupPerIP, &limits.SignupPerIP},
		{"RATE_LIMIT_LOGIN_PER_IP", r.RateLimitLoginPerIP, &limits.LoginPerIP},
		{"RATE_LIMIT_LOGIN_PER_EMAIL", r.RateLimitLoginPerEmail, &limits.LoginPerEmail},
		{"RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL", r.RateLimitLoginPerEmailTotal, &limits.LoginPerEmailTotal},
	} {
		limit, ok := parseRateLimit(l.value)
		if !ok {
			problems = append(problems, Problem{Var: l.name, Reason: "must look like 10/1m (count/period), with a count of at least 1 and a period of at most 24h"})
			continue
		}
		*l.dst = limit
	}

	// 서로의 크기 관계는 두 값이 모두 멀쩡할 때만 본다. 이미 틀린 값을 두고 한 번 더 지적하지 않는다.
	if perAddress, total := limits.LoginPerEmail, limits.LoginPerEmailTotal; perAddress.Burst > 0 && total.Burst > 0 {
		if total.Burst <= perAddress.Burst || total.refillInterval() > perAddress.refillInterval() {
			problems = append(problems, Problem{
				Var:    "RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL",
				Reason: "must allow more attempts than RATE_LIMIT_LOGIN_PER_EMAIL and refill at least as fast, or a single address can keep an account locked out",
			})
		}
	}

	if n, reason := parseIntInRange(r.RateLimitMaxKeys, 1, maxRateLimitMaxKeys); reason != "" {
		problems = append(problems, Problem{Var: "RATE_LIMIT_MAX_KEYS", Reason: reason})
	} else {
		limits.MaxKeys = n
	}
	return limits, problems
}

// parseRateLimit은 "10/1m" 꼴을 읽는다.
func parseRateLimit(value string) (RateLimit, bool) {
	count, period, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found {
		return RateLimit{}, false
	}
	burst, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || burst < 1 || burst > maxRateLimitBurst {
		return RateLimit{}, false
	}
	d, err := time.ParseDuration(strings.TrimSpace(period))
	if err != nil || d <= 0 || d > maxRateLimitPeriod {
		return RateLimit{}, false
	}
	return RateLimit{Burst: burst, Period: d}, true
}

// parseTrustedProxies는 쉼표로 나눈 주소 범위를 읽는다. 범위 없이 주소만 적으면 그 주소 하나로 본다.
func parseTrustedProxies(value string) ([]netip.Prefix, bool) {
	var prefixes []netip.Prefix
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(part); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, false
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, true
}

func prefixStrings(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	return out
}

const (
	// 이보다 작으면 해시를 만드는 값이 너무 싸서 새어 나간 해시를 빠르게 대입해 볼 수 있다.
	minArgon2MemoryKiB = 7168
	// 해시 하나에 1 GiB를 넘게 쓰게 하는 값은 오타로 본다.
	maxArgon2MemoryKiB = 1 << 20
	maxArgon2Time      = 16
	// 해시 함수가 받는 병렬 수의 상한은 255다. 파드에 그만큼의 CPU를 줄 일은 없으므로 더 낮게 잡는다.
	maxArgon2Parallelism = 64
	maxHashConcurrency   = 256
)

func loadPassword(r raw) (Password, []Problem) {
	var p Password
	var problems []Problem

	if n, reason := parseIntInRange(r.PasswordArgon2MemoryKiB, minArgon2MemoryKiB, maxArgon2MemoryKiB); reason != "" {
		problems = append(problems, Problem{Var: "PASSWORD_ARGON2_MEMORY_KIB", Reason: reason})
	} else {
		p.MemoryKiB = uint32(n) // #nosec G115 -- 위에서 범위를 확인했다.
	}
	if n, reason := parseIntInRange(r.PasswordArgon2Time, 1, maxArgon2Time); reason != "" {
		problems = append(problems, Problem{Var: "PASSWORD_ARGON2_TIME", Reason: reason})
	} else {
		p.Time = uint32(n) // #nosec G115 -- 위에서 범위를 확인했다.
	}
	if n, reason := parseIntInRange(r.PasswordArgon2Parallelism, 1, maxArgon2Parallelism); reason != "" {
		problems = append(problems, Problem{Var: "PASSWORD_ARGON2_PARALLELISM", Reason: reason})
	} else {
		p.Parallelism = uint8(n) // #nosec G115 -- 위에서 범위를 확인했다.
	}
	if n, reason := parseIntInRange(r.PasswordHashConcurrency, 1, maxHashConcurrency); reason != "" {
		problems = append(problems, Problem{Var: "PASSWORD_HASH_CONCURRENCY", Reason: reason})
	} else {
		p.HashConcurrency = n
	}
	return p, problems
}

const maxDataKeyCacheSize = 1_000_000

func loadDataKeyCache(r raw) (DataKeyCache, []Problem) {
	var c DataKeyCache
	var problems []Problem

	if n, reason := parseIntInRange(r.DataKeyCacheSize, 1, maxDataKeyCacheSize); reason != "" {
		problems = append(problems, Problem{Var: "DATA_KEY_CACHE_SIZE", Reason: reason})
	} else {
		c.Size = n
	}

	maxAge, err := time.ParseDuration(strings.TrimSpace(r.DataKeyCacheMaxAge))
	switch {
	case err != nil:
		problems = append(problems, Problem{Var: "DATA_KEY_CACHE_MAX_AGE", Reason: "must be a duration such as 10m or 90s"})
	case maxAge <= 0:
		// 0은 "시간으로는 내보내지 않는다"는 뜻이 된다. 그러면 다른 프로세스에서 지운 계정의 키가 끝없이 남는다.
		problems = append(problems, Problem{Var: "DATA_KEY_CACHE_MAX_AGE", Reason: "must be greater than zero"})
	default:
		c.MaxAge = maxAge
	}
	return c, problems
}

// loadAIProvider는 언어 모델의 답을 어디서 받을지 정한다.
//
// 적지 않았을 때의 기본값이 환경에 따라 다르다. 운영에서는 언제나 gemini다. 키가 빠졌다면 조용히 다른 길로 가지 않고 뜨지 않는다.
// 그 밖에서는 키가 있으면 gemini, 없으면 scripted다. 저장소를 받아 키 없이 띄운 서버도 대화가 끝까지 이어져야 하기 때문이다.
// 직접 적은 값은 그대로 따른다. gemini라고 적고 키를 주지 않았으면 개발에서도 뜨지 않는다.
func loadAIProvider(r raw, prod, geminiKeySet bool) (AIProvider, []Problem) {
	provider := AIProvider(strings.ToLower(strings.TrimSpace(r.AIProvider)))
	if provider == "" {
		provider = AIProviderGemini
		if !prod && !geminiKeySet {
			provider = AIProviderScripted
		}
	}

	switch provider {
	case AIProviderGemini:
		if !geminiKeySet {
			return "", []Problem{{Var: "GEMINI_API_KEY", Reason: "is required when AI_PROVIDER=gemini (which is the default when APP_ENV=prod)"}}
		}
	case AIProviderScripted:
		if prod {
			// 정해 둔 답만 하는 서버가 운영에 뜨면 위기 판별도 정해 둔 답이 된다.
			return "", []Problem{{Var: "AI_PROVIDER", Reason: "must be gemini when APP_ENV=prod"}}
		}
	default:
		return "", []Problem{{Var: "AI_PROVIDER", Reason: "must be one of gemini, scripted"}}
	}
	return provider, nil
}

const (
	// 위기 판별을 이보다 오래 기다리면 사용자는 답이 멈춘 것으로 느낀다. 그보다 긴 값은 오타로 본다.
	maxGateAITimeout = 30 * time.Second
	// 대화 모델을 이보다 오래 기다리면 사용자는 답도 못 받고 끝내기도 하지 못한 채 앉아 있게 된다.
	maxReplyBudget = 60 * time.Second
	// 말이 없는 대화와 끊긴 연결을 하루 넘게 열어 두는 값은 오타로 본다. 새벽의 경계를 넘긴 대화는 어차피 이어가지 않는다.
	maxConversationWait = 24 * time.Hour
	// start, user_text, end 가운데 가장 큰 것은 글 하나다. 그 한도보다 작으면 글을 끝까지 받을 수 없다.
	minWSMessageBytes  = 1024
	maxWSMessageBytes  = 1 << 20
	maxDiaryJobRetries = 10
	// 다시 시도할 때마다 모델을 한 번 더 부른다. 이보다 많은 값은 오타로 본다.
	maxAnalysisJobRetries = 10
	// 여덟 항목의 판단과 항목마다의 근거 발화가 담기는 답이다. 이보다 작으면 답이 항목 중간에서 잘린다.
	minAnalysisMaxOutputTokens = 1024
	maxAnalysisMaxOutputTokens = 1 << 16
	// 대화가 끝난 뒤의 일이라 느린 답도 기다리지만, 작업자 한 자리를 몇 분씩 묶어 두는 값은 오타로 본다.
	maxAnalysisBudget = 5 * time.Minute
)

type conversationFlow struct {
	gateTimeout    time.Duration
	analysisBudget time.Duration
	conversation   Conversation
	webSocket      WebSocket
	diaryJob       DiaryJob
	analysisJob    AnalysisJob
	// analysisMaxOutputTokens는 신호 추출 모델의 출력 한도다.
	analysisMaxOutputTokens int
}

func loadConversationFlow(r raw) (conversationFlow, []Problem) {
	var flow conversationFlow
	var problems []Problem

	for _, d := range []struct {
		name  string
		value string
		limit time.Duration
		dst   *time.Duration
	}{
		{"GATE_AI_TIMEOUT", r.GateAITimeout, maxGateAITimeout, &flow.gateTimeout},
		{"LLM_ANALYSIS_BUDGET", r.LLMAnalysisBudget, maxAnalysisBudget, &flow.analysisBudget},
		{"IDLE_CHECK_AFTER", r.IdleCheckAfter, maxConversationWait, &flow.conversation.IdleCheckAfter},
		{"IDLE_END_AFTER", r.IdleEndAfter, maxConversationWait, &flow.conversation.IdleEndAfter},
		{"DISCONNECT_END_AFTER", r.DisconnectEndAfter, maxConversationWait, &flow.conversation.DisconnectEndAfter},
	} {
		value, err := time.ParseDuration(strings.TrimSpace(d.value))
		switch {
		case err != nil:
			problems = append(problems, Problem{Var: d.name, Reason: "must be a duration such as 2500ms, 3m or 30m"})
		case value <= 0:
			problems = append(problems, Problem{Var: d.name, Reason: "must be greater than zero"})
		case value > d.limit:
			problems = append(problems, Problem{Var: d.name, Reason: "must not be longer than " + d.limit.String()})
		default:
			*d.dst = value
		}
	}

	if n, reason := parseIntInRange(r.WSMaxMessageBytes, minWSMessageBytes, maxWSMessageBytes); reason != "" {
		problems = append(problems, Problem{Var: "WS_MAX_MESSAGE_BYTES", Reason: reason})
	} else {
		flow.webSocket.MaxMessageBytes = int64(n)
	}

	if limit, ok := parseRateLimit(r.WSMessageRateLimit); ok {
		flow.webSocket.MessageRate = limit
	} else {
		problems = append(problems, Problem{Var: "WS_MESSAGE_RATE_LIMIT", Reason: "must look like 20/1m (count/period), with a count of at least 1 and a period of at most 24h"})
	}

	if n, reason := parseIntInRange(r.DiaryJobRetries, 0, maxDiaryJobRetries); reason != "" {
		problems = append(problems, Problem{Var: "DIARY_JOB_RETRIES", Reason: reason})
	} else {
		flow.diaryJob.Retries = n
	}

	if n, reason := parseIntInRange(r.AnalysisJobRetries, 0, maxAnalysisJobRetries); reason != "" {
		problems = append(problems, Problem{Var: "ANALYSIS_JOB_RETRIES", Reason: reason})
	} else {
		flow.analysisJob.Retries = n
	}

	if n, reason := parseIntInRange(r.LLMMaxOutputAnalysis, minAnalysisMaxOutputTokens, maxAnalysisMaxOutputTokens); reason != "" {
		problems = append(problems, Problem{Var: "LLM_MAX_OUTPUT_ANALYSIS", Reason: reason})
	} else {
		flow.analysisMaxOutputTokens = n
	}
	return flow, problems
}

// parseIntInRange는 틀린 이유를 값 없이 돌려준다. 맞으면 이유는 빈 문자열이다.
func parseIntInRange(value string, lo, hi int) (n int, reason string) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < lo || n > hi {
		return 0, "must be an integer between " + strconv.Itoa(lo) + " and " + strconv.Itoa(hi)
	}
	return n, ""
}

var dataKEKVarPattern = regexp.MustCompile(`^DATA_KEK_V([1-9][0-9]{0,5})$`)

// loadDataKeys는 DATA_KEK_V<n> 꼴의 변수를 모두 모은다.
// 키를 바꿀 때 V2를 더하고 ACTIVE만 올리면 되도록, 버전 목록을 코드에 박아 두지 않는다.
func loadDataKeys(r raw, environ map[string]string, prod bool) (DataKeys, []Problem) {
	var problems []Problem
	keys := DataKeys{Keys: map[int]SecretBytes{}}

	devKey, err := base64.StdEncoding.DecodeString(devOnlyDataKEK)
	if err != nil {
		devKey = nil
	}

	candidates := map[string]string{"DATA_KEK_V1": r.DataKEKV1}
	for name, value := range environ {
		if dataKEKVarPattern.MatchString(name) && value != "" {
			candidates[name] = value
		}
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := strings.TrimSpace(candidates[name])
		if value == "" {
			// V1이 비어 있으면 필수 검사에서 이미 걸렸다. 같은 문제를 두 번 적지 않는다.
			continue
		}
		version, convErr := strconv.Atoi(dataKEKVarPattern.FindStringSubmatch(name)[1])
		if convErr != nil {
			problems = append(problems, Problem{Var: name, Reason: "has an invalid version number"})
			continue
		}
		if version > maxDataKEKVersion {
			problems = append(problems, Problem{Var: name, Reason: "has a version number above " + strconv.Itoa(maxDataKEKVersion)})
			continue
		}
		key, decodeErr := base64.StdEncoding.DecodeString(value)
		switch {
		case decodeErr != nil:
			problems = append(problems, Problem{Var: name, Reason: "must be standard base64 (openssl rand -base64 32)"})
		case len(key) != dataKEKLength:
			problems = append(problems, Problem{Var: name, Reason: "must decode to exactly 32 bytes"})
		case prod && subtle.ConstantTimeCompare(key, devKey) == 1:
			problems = append(problems, Problem{Var: name, Reason: "is the published development key and must not be used when APP_ENV=prod"})
		default:
			keys.Keys[version] = NewSecretBytes(key)
		}
	}

	active, err := strconv.Atoi(strings.TrimSpace(r.DataKEKActive))
	switch {
	case err != nil || active < 1:
		problems = append(problems, Problem{Var: "DATA_KEK_ACTIVE", Reason: "must be a positive integer"})
	default:
		keys.Active = active
		if _, ok := candidates["DATA_KEK_V"+strconv.Itoa(active)]; !ok {
			problems = append(problems, Problem{Var: "DATA_KEK_ACTIVE", Reason: "points to a key version that is not set (DATA_KEK_V" + strconv.Itoa(active) + ")"})
		}
	}

	return keys, problems
}

var cookieNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// 브라우저가 특별하게 다루는 쿠키 이름의 앞머리다.
const (
	cookieHostPrefix   = "__Host-"
	cookieSecurePrefix = "__Secure-"
)

// checkCookieName은 틀린 이유를 값 없이 돌려준다. 맞으면 빈 문자열이다.
func checkCookieName(name string, secure bool) string {
	switch {
	case !cookieNamePattern.MatchString(name):
		return "must contain only letters, digits, '_' and '-'"
	case strings.HasPrefix(name, cookieSecurePrefix):
		// 쿠키가 HTTPS 전용이면 서버가 더 엄격한 __Host-를 알아서 붙인다. 두 앞머리가 겹치면 브라우저가 어느 쪽 규칙도 적용하지 않는다.
		return "must not start with __Secure- (the server adds __Host- by itself when the cookie is secure)"
	case strings.HasPrefix(name, cookieHostPrefix) && !secure:
		// 브라우저는 Secure가 없는 __Host- 쿠키를 받지 않고 버린다. 로그인이 되는 것처럼 보이다가 다음 요청에서 풀린다.
		return "must not start with __Host- unless SESSION_COOKIE_SECURE=true"
	}
	return ""
}

func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

func validListenAddr(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 0 && n <= 65535
}

// checkOrigin은 출처가 브라우저가 보내는 Origin 헤더와 글자 그대로 비교할 수 있는 꼴인지 본다.
// 경로나 끝의 슬래시가 붙어 있으면 비교가 항상 어긋나므로 받아주지 않는다.
func checkOrigin(origin string, prod bool) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "must be an origin such as https://example.com"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "must start with http:// or https://"
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "must not contain a path, query, fragment or credentials (no trailing slash)"
	}
	if prod && u.Scheme != "https" {
		return "must use https when APP_ENV=prod"
	}
	return ""
}

func environMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok {
			m[name] = value
		}
	}
	return m
}
