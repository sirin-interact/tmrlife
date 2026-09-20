package api

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
)

// RateLimit은 "Period 동안 Burst번"이라는 한도다.
// 한꺼번에 Burst번까지 쓸 수 있고, 쓴 만큼은 Period에 걸쳐 고르게 다시 찬다.
type RateLimit struct {
	Burst  int
	Period time.Duration
}

// RateLimits는 가입과 로그인을 얼마나 자주 시도할 수 있는지다. 값은 설정에서 받는다.
type RateLimits struct {
	SignupPerIP RateLimit
	LoginPerIP  RateLimit
	// LoginPerEmail은 한 주소에서 한 계정에 대한 시도를 센다.
	LoginPerEmail RateLimit
	// LoginPerEmailTotal은 한 계정에 대한 시도를 주소를 가리지 않고 모두 센다. LoginPerEmail보다 느슨해야 한다.
	LoginPerEmailTotal RateLimit
	// MaxKeys는 한도 하나가 기억하는 키의 최대 수다.
	MaxKeys int
}

// clockResolution은 주입받는 시계가 돌려주는 시각의 눈금이다.
const clockResolution = time.Microsecond

// sweepPerTake는 take를 한 번 부를 때마다 살펴보는 오래된 키의 수다.
// 새 키는 한 번에 하나씩만 생기므로, 둘씩 치우면 치우는 쪽이 늘 앞선다.
const sweepPerTake = 2

// tokenBuckets는 키마다 토큰 통을 하나씩 두는 시도 한도다.
//
// 통에는 토큰이 Burst개까지 담기고, 시도 한 번에 하나를 쓴다. 비면 거부하고, 다음 토큰이 찰 때까지 남은 시간을 알려준다.
// 고정된 시간 창으로 세는 방식과 달리 창의 경계에서 두 배가 몰리는 일이 없고, 키마다 시각 하나만 기억하면 된다.
//
// 토큰의 수를 소수로 들고 있지 않고 "통이 다시 가득 차는 시각" 하나로 나타낸다. 토큰 하나가 차는 데 걸리는 시간이 interval이면,
// 가득 차는 시각이 지금보다 n x interval만큼 뒤라는 것은 토큰 n개를 쓴 상태라는 뜻이다.
// 시각과 시간의 정수 계산만으로 끝나므로, 알려준 시간만큼 기다린 요청이 반올림 오차로 또 거부되는 일이 없다.
//
// # 여러 파드
//
// 통은 이 프로세스의 메모리에만 있다. 파드가 여럿이면 파드마다 따로 세므로 실제 한도는 설정값에 파드 수를 곱한 만큼까지 늘어나고,
// 프로세스가 다시 뜨면 처음부터 다시 센다. 파드 하나로 도는 동안은 이것으로 충분하다. 파드를 늘릴 때는 다음 가운데 하나를 고른다.
//
//  1. 설정의 한도를 파드 수로 나눠 적는다. 가장 간단하지만 요청이 파드에 고르게 나뉜다는 가정에 기댄다.
//  2. 주소별 한도를 앞단 프록시(Traefik의 rateLimit 미들웨어)로 옮긴다. 프록시는 모든 요청을 보므로 정확하다.
//     계정별 한도는 본문을 읽어야 해서 프록시로 옮길 수 없다.
//  3. take를 공유 저장소로 옮긴다. PostgreSQL이라면 (키, 가득 차는 시각) 행을 upsert 한 문장으로 고치면 된다.
//     부르는 쪽은 take(key)만 알기 때문에 이 타입만 바꿔 끼우면 된다.
//
// # 메모리
//
// 키는 요청을 보내는 쪽이 얼마든지 만들어 낼 수 있다(주소를 바꿔 가며, 이메일을 바꿔 가며). 그래서 두 가지로 묶어 둔다.
// 오래 쉬어서 통이 다시 가득 찬 키는 기억할 것이 없으므로 지나가는 길에 지운다. 그래도 MaxKeys에 닿으면
// 가장 오래 쉰 키부터 버린다. 버려진 키는 한도를 새로 받는 셈이지만, 그만큼 많은 주소를 가진 공격자는 어차피 주소별 한도로 막을 수 없다.
type tokenBuckets struct {
	clock clock.Clock
	// interval은 토큰 하나가 다시 차는 데 걸리는 시간이다.
	interval time.Duration
	// capacity는 빈 통이 가득 차는 데 걸리는 시간이다(interval x Burst).
	capacity time.Duration
	maxKeys  int

	mu      sync.Mutex
	entries map[string]*list.Element
	// recency의 앞은 방금 쓴 키, 뒤는 가장 오래 쉰 키다.
	recency *list.List
}

type bucket struct {
	key string
	// fullAt은 더 쓰지 않으면 통이 다시 가득 차는 시각이다. 지금보다 앞이면 이미 가득 차 있다.
	fullAt time.Time
}

func newTokenBuckets(clk clock.Clock, limit RateLimit, maxKeys int) (*tokenBuckets, error) {
	switch {
	case clk == nil:
		return nil, errors.New("api: rate limit needs a clock")
	case limit.Burst < 1 || limit.Period <= 0:
		return nil, errors.New("api: rate limit needs a positive burst and period")
	case maxKeys < 1:
		return nil, errors.New("api: rate limit needs room for at least one key")
	}
	interval := limit.Period / time.Duration(limit.Burst)
	if interval < clockResolution {
		return nil, errors.New("api: rate limit allows more than one attempt per microsecond")
	}
	// 시계의 눈금에 맞춰 올림한다. 눈금보다 잘게 계산하면, 알려준 시간만큼 기다린 요청이 눈금 아래의 차이로 또 거부된다.
	// 올림이라서 한도는 설정보다 아주 조금 엄격해질 뿐 느슨해지지는 않는다.
	if rem := interval % clockResolution; rem != 0 {
		interval += clockResolution - rem
	}
	return &tokenBuckets{
		clock:    clk,
		interval: interval,
		capacity: interval * time.Duration(limit.Burst),
		maxKeys:  maxKeys,
		entries:  make(map[string]*list.Element),
		recency:  list.New(),
	}, nil
}

// take는 key의 통에서 토큰 하나를 꺼낸다. 비어 있으면 false와 함께 다음 토큰이 찰 때까지 남은 시간을 돌려준다.
// 거부된 시도는 토큰을 쓰지 않는다. 한도에 걸린 뒤에 계속 두드린다고 기다릴 시간이 늘어나지는 않는다.
func (b *tokenBuckets) take(key string) (allowed bool, retryAfter time.Duration) {
	now := b.clock.Now()

	b.mu.Lock()
	defer b.mu.Unlock()

	b.sweep(now)

	var entry *bucket
	if el, ok := b.entries[key]; ok {
		entry, _ = el.Value.(*bucket)
		b.recency.MoveToFront(el)
	} else {
		if len(b.entries) >= b.maxKeys {
			b.remove(b.recency.Back())
		}
		entry = &bucket{key: key, fullAt: now}
		b.entries[key] = b.recency.PushFront(entry)
	}

	// 가득 찬 뒤로 흐른 시간은 쌓이지 않는다. 시계가 뒤로 밀렸을 때도 이 줄 덕분에 토큰이 거저 생기지 않는다.
	fullAt := entry.fullAt
	if fullAt.Before(now) {
		fullAt = now
	}
	// 토큰 하나를 쓰면 가득 차는 시각이 interval만큼 뒤로 밀린다. 그 시각이 지금에서 capacity보다 멀면 통에 토큰이 없다는 뜻이다.
	next := fullAt.Add(b.interval)
	if wait := next.Sub(now) - b.capacity; wait > 0 {
		return false, wait
	}
	entry.fullAt = next
	return true, 0
}

// size는 기억하고 있는 키의 수다.
func (b *tokenBuckets) size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// sweep은 가장 오래 쉰 키 몇 개를 보고, 통이 다시 가득 찬 것을 지운다. 가득 찬 통은 처음 보는 키와 다를 것이 없다.
// 따로 도는 청소 고루틴이 없으므로 시계를 주입받은 시험에서도 똑같이 움직인다.
func (b *tokenBuckets) sweep(now time.Time) {
	for range sweepPerTake {
		el := b.recency.Back()
		if el == nil {
			return
		}
		if entry, _ := el.Value.(*bucket); entry == nil || entry.fullAt.After(now) {
			return
		}
		b.remove(el)
	}
}

func (b *tokenBuckets) remove(el *list.Element) {
	if el == nil {
		return
	}
	if entry, ok := el.Value.(*bucket); ok {
		delete(b.entries, entry.key)
	}
	b.recency.Remove(el)
}

// 만들어진 코드가 작업마다 미들웨어에 넘겨주는 이름이다. 명세의 operationId를 Go 이름으로 바꾼 값이다.
const (
	operationSignup = "Signup"
	operationLogin  = "Login"
)

// authRateLimiter는 가입과 로그인의 시도 한도다.
//
// 가입과 로그인은 비밀번호 해시를 한 번씩 계산한다. 해시는 일부러 비싸게 만든 계산이라서, 한도가 없으면
// 요청을 쏟아붓는 것만으로 서버의 CPU와 메모리를 다 쓰게 할 수 있다. 로그인은 비밀번호를 대입해 보는 통로이기도 하다.
//
//   - 주소별 한도: 한 곳에서 쏟아지는 시도를 막는다. 가입은 이미 가입된 이메일인지 알려주므로, 이메일 목록을 확인해 보는 일도 늦춘다.
//   - 계정별 한도(로그인): 한 계정의 비밀번호를 대입해 보는 것을 늦춘다. 두 겹이다.
//     안쪽은 (계정, 주소)마다 따로 세는 빡빡한 한도이고, 바깥쪽은 계정 하나에 대한 시도를 주소를 가리지 않고 모두 세는 느슨한 천장이다.
//
// 계정 하나를 주소와 상관없이 빡빡하게만 세면, 이메일을 아는 사람 누구나 틀린 비밀번호를 1분에 한 번씩 넣는 것만으로
// 그 계정의 주인을 끝없이 로그인하지 못하게 할 수 있다. 다른 길(비밀번호 재설정)이 없는 동안에는 주인이 기록에 닿을 방법이 없어진다.
// 그래서 빡빡한 한도는 주소마다 따로 둔다. 한 주소가 아무리 두드려도 자기 통만 비우고, 천장은 그 주소가 쓰는 속도만큼 다시 차므로
// 다른 주소에서 오는 주인은 로그인할 수 있다. 설정은 천장이 안쪽 한도보다 빨리 차도록 강제한다.
// 천장이 있어서, 주소를 바꿔 가며 대입해도 계정 하나에 하루 동안 해 볼 수 있는 횟수는 주소의 수와 상관없이 묶인다.
//
// 그래도 막지 못하는 것이 있다. 주소를 여럿 가진 사람(와이파이와 이동통신, IPv6 대역을 여러 개 받는 회선)은 천장을 비워
// 주인을 한동안 막을 수 있다. 주인과 같은 주소(같은 공유기)를 쓰는 사람은 안쪽 한도를 함께 쓴다.
// 통은 시간이 지나면 다시 차지만, 공격이 이어지는 동안에는 차지 않는다. 이 틈은 아는 기기를 따로 세는 방식이 생겨야 닫힌다.
// 이미 로그인한 세션은 어느 경우에도 영향을 받지 않는다.
//
// 가입은 해시까지 가는 시도만 센다. 흔한 비밀번호나 빠진 동의로 거부되는 요청은 해시를 계산하지 않으므로 세지 않는다.
// 세면, 한 공유기 뒤에 모인 사람들의 오타가 서로의 가입을 막는다. 이미 가입된 이메일은 해시를 계산한 뒤에 알려주므로 센다.
// 해시까지 간 시도는 성공했어도 센다. 실패만 세려면 결과를 본 뒤에 되돌려야 하는데, 그러면 해시를 계산하기 전에 거부할 수 없다.
//
// 주소별 한도는 같은 공인 주소를 쓰는 사람들이 나눠 쓴다(행사장의 와이파이, 통신사의 주소 변환).
// 한곳에 모인 사람들이 한꺼번에 가입하는 날에는 그 수에 맞춰 설정의 값을 올린다.
type authRateLimiter struct {
	logger             *slog.Logger
	signupPerIP        *tokenBuckets
	loginPerIP         *tokenBuckets
	loginPerEmailIP    *tokenBuckets
	loginPerEmailTotal *tokenBuckets
	// checkSignup은 해시를 계산하기 전에 거부될 가입 요청을 가려낸다. 검사 자체는 인증 서비스의 것이다.
	checkSignup func(auth.SignupInput) error
}

func newAuthRateLimiter(clk clock.Clock, logger *slog.Logger, limits RateLimits, checkSignup func(auth.SignupInput) error) (*authRateLimiter, error) {
	if checkSignup == nil {
		return nil, errors.New("signup check is required")
	}
	signupPerIP, err := newTokenBuckets(clk, limits.SignupPerIP, limits.MaxKeys)
	if err != nil {
		return nil, fmt.Errorf("signup per address: %w", err)
	}
	loginPerIP, err := newTokenBuckets(clk, limits.LoginPerIP, limits.MaxKeys)
	if err != nil {
		return nil, fmt.Errorf("login per address: %w", err)
	}
	loginPerEmailIP, err := newTokenBuckets(clk, limits.LoginPerEmail, limits.MaxKeys)
	if err != nil {
		return nil, fmt.Errorf("login per account and address: %w", err)
	}
	loginPerEmailTotal, err := newTokenBuckets(clk, limits.LoginPerEmailTotal, limits.MaxKeys)
	if err != nil {
		return nil, fmt.Errorf("login per account: %w", err)
	}
	if loginPerEmailTotal.interval > loginPerEmailIP.interval || limits.LoginPerEmailTotal.Burst <= limits.LoginPerEmail.Burst {
		// 천장이 안쪽 한도보다 느리게 차거나 한꺼번에 받는 수가 같으면, 한 주소가 꾸준히 두드리는 것만으로 천장을 비워 주인을 막을 수 있다.
		return nil, errors.New("login per account: the total limit must allow more attempts than the per-address limit and refill at least as fast")
	}
	return &authRateLimiter{
		logger:             logger,
		signupPerIP:        signupPerIP,
		loginPerIP:         loginPerIP,
		loginPerEmailIP:    loginPerEmailIP,
		loginPerEmailTotal: loginPerEmailTotal,
		checkSignup:        checkSignup,
	}, nil
}

// middleware는 본문이 검증되고 풀린 뒤, 핸들러 앞에서 돈다. 계정별 한도는 본문의 이메일을 봐야 해서 이 자리여야 한다.
// 명세에 맞지 않는 요청은 여기까지 오지 않는다. 그런 요청은 해시를 계산하지 않고 거부되므로 셀 필요가 없다.
func (l *authRateLimiter) middleware(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
	switch operationID {
	case operationSignup:
		return func(c *echo.Context, request any) (any, error) {
			// 해시까지 가지 못할 요청은 토큰을 쓰지 않고 여기서 같은 오류로 끝난다. 핸들러까지 갔어도 같은 답을 받았을 요청이다.
			if signup, ok := request.(SignupRequestObject); ok && signup.Body != nil {
				if err := l.checkSignup(signupInputFrom(signup.Body)); err != nil {
					return nil, err
				}
			}
			ip := clientInfoFrom(c.Request().Context()).IP
			if err := l.take(c, l.signupPerIP, "signup_per_ip", ipKey(ip)); err != nil {
				return nil, err
			}
			return next(c, request)
		}

	case operationLogin:
		return func(c *echo.Context, request any) (any, error) {
			ip := clientInfoFrom(c.Request().Context()).IP
			// 넓은 것부터 본다: 주소, (계정, 주소), 계정. 앞에서 거부된 시도는 뒤의 통을 건드리지 않는다.
			// 순서가 반대면, 이미 막힌 공격자가 계속 두드리는 것만으로 남의 계정의 통을 비울 수 있다.
			if err := l.take(c, l.loginPerIP, "login_per_ip", ipKey(ip)); err != nil {
				return nil, err
			}
			if login, ok := request.(LoginRequestObject); ok && login.Body != nil {
				if key, ok := emailKey(login.Body.Email); ok {
					if err := l.take(c, l.loginPerEmailIP, "login_per_email_ip", key+"|"+ipKey(ip)); err != nil {
						return nil, err
					}
					if err := l.take(c, l.loginPerEmailTotal, "login_per_email", key); err != nil {
						return nil, err
					}
				}
			}
			return next(c, request)
		}
	}
	return next
}

func (l *authRateLimiter) take(c *echo.Context, buckets *tokenBuckets, name, key string) error {
	allowed, retryAfter := buckets.take(key)
	if allowed {
		return nil
	}
	ctx := c.Request().Context()
	// 어느 주소, 어느 계정인지는 남기지 않는다. 어느 한도에 걸렸는지만 남긴다.
	l.logger.LogAttrs(ctx, slog.LevelWarn, "rate limit exceeded",
		slog.String("limit", name),
		slog.String("request_id", httpserver.RequestID(ctx)),
	)
	p := newProblem(http.StatusTooManyRequests, ProblemCodeRateLimited)
	p.retryAfter = retryAfter
	return p
}

// ipKey는 주소를 한도의 키로 바꾼다.
// IPv6는 앞의 64비트로 묶는다. 가입자 한 명이 /64 대역을 통째로 받는 것이 보통이라서,
// 주소 하나하나로 세면 같은 사람이 주소를 바꿔 가며 한도를 피해 갈 수 있다.
func ipKey(addr netip.Addr) string {
	if !addr.IsValid() {
		// 주소를 알 수 없는 요청은 모두 한 통을 나눠 쓴다. 알 수 없다는 이유로 한도를 비켜 가지 못하게 한다.
		return "unknown"
	}
	if addr.Is6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}

// emailKey는 이메일을 한도의 키로 바꾼다. 가입된 주소와 같은 꼴로 다듬어서, 대소문자나 공백만 바꾼 시도를 같은 계정으로 센다.
// 메모리에 이메일을 그대로 쌓아 두지 않으려고 해시를 키로 쓴다.
// 꼴이 틀린 주소는 가입되어 있을 수 없으므로 지킬 계정이 없다. false를 돌려주고, 주소별 한도만 적용된다.
func emailKey(raw string) (string, bool) {
	email, err := auth.NormalizeEmail(raw)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(email))
	return string(sum[:]), true
}
