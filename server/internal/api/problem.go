package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
)

const (
	problemContentType = "application/problem+json"
	// 오류 종류를 가리키는 URI 참조의 앞머리다. 뒤에 code가 붙는다.
	// 실제로 열리는 주소일 필요는 없다. 도메인이 바뀌어도 값이 그대로이도록 호스트 없이 경로만 쓴다.
	problemTypePrefix = "/problems/"

	// statusClientClosedRequest는 응답을 만들기 전에 클라이언트가 연결을 끊었다는 뜻이다.
	// 표준에 없는 값이지만 아무도 받지 않는 응답이라 접근 로그에만 남는다.
	// 5xx로 적으면 서버가 멀쩡한데도 오류로 집계된다.
	statusClientClosedRequest = 499
)

// 개발자가 읽는 짧은 영어 문구다. 사용자에게 보여줄 문구는 화면이 code를 보고 고른다.
var problemTitles = map[ProblemCode]string{
	ProblemCodeValidationFailed:     "Request validation failed",
	ProblemCodeInvalidCredentials:   "Invalid email or password",
	ProblemCodeUnauthenticated:      "Authentication required",
	ProblemCodeForbidden:            "Forbidden",
	ProblemCodeCrossOriginRejected:  "Cross-origin request rejected",
	ProblemCodeNotFound:             "Not found",
	ProblemCodeMethodNotAllowed:     "Method not allowed",
	ProblemCodeEmailTaken:           "Email is already registered",
	ProblemCodePayloadTooLarge:      "Request body is too large",
	ProblemCodeUnsupportedMediaType: "Content-Type must be application/json",
	ProblemCodeWeakPassword:         "Password does not meet the policy",
	ProblemCodeConsentRequired:      "Required consent is missing or outdated",
	ProblemCodeRateLimited:          "Too many attempts",
	ProblemCodeInternalError:        "Internal server error",
	ProblemCodeServiceUnavailable:   "Service temporarily unavailable",
}

// problemError는 application/problem+json 응답으로 나갈 오류다.
// 요청에 담겨 온 값은 어느 필드에도 넣지 않는다. 담는 것은 이 코드에 적힌 상수뿐이다.
type problemError struct {
	status   int
	code     ProblemCode
	reasons  []PasswordReason
	consents *ConsentProblem
	fields   []ProblemField
	// retryAfter가 0보다 크면 Retry-After 헤더로 나간다.
	retryAfter time.Duration
	// cause는 서버 쪽 원인이다. 응답에는 싣지 않고, 5xx일 때 접근 로그에 남는다.
	cause error
}

func newProblem(status int, code ProblemCode) *problemError {
	return &problemError{status: status, code: code}
}

func (e *problemError) Error() string {
	if e.cause != nil {
		return string(e.code) + ": " + e.cause.Error()
	}
	return string(e.code)
}

func (e *problemError) Unwrap() error {
	return e.cause
}

// StatusCode는 Echo의 미들웨어가 오류에서 상태 코드를 읽을 수 있게 한다.
func (e *problemError) StatusCode() int {
	return e.status
}

func (e *problemError) body(requestID string) Problem {
	p := Problem{
		Type:      problemTypePrefix + string(e.code),
		Title:     problemTitles[e.code],
		Status:    e.status,
		Code:      e.code,
		RequestID: requestID,
		Consents:  e.consents,
	}
	if len(e.reasons) > 0 {
		p.Reasons = &e.reasons
	}
	if len(e.fields) > 0 {
		p.Fields = &e.fields
	}
	return p
}

// 미들웨어가 직접 돌려주는 오류다.
var (
	errUnauthenticated = newProblem(http.StatusUnauthorized, ProblemCodeUnauthenticated)
	errCrossOrigin     = newProblem(http.StatusForbidden, ProblemCodeCrossOriginRejected)
	errBodyTooLarge    = newProblem(http.StatusRequestEntityTooLarge, ProblemCodePayloadTooLarge)
	errNotJSON         = newProblem(http.StatusUnsupportedMediaType, ProblemCodeUnsupportedMediaType)
	errUnreadableBody  = newProblem(http.StatusBadRequest, ProblemCodeValidationFailed)
)

// problemFor는 어떤 오류든 응답으로 나갈 꼴로 바꾼다. 도메인의 오류가 어떤 상태와 code가 되는지는 여기서만 정한다.
// 핸들러는 서비스가 돌려준 오류를 그대로 돌려주기만 한다.
//
// 모르는 오류는 500이 된다. 오류의 문구는 어느 경우에도 응답에 싣지 않는다.
// 검증기의 문구에는 검증에 걸린 입력값이, DB 오류의 문구에는 쿼리의 조각이 들어 있을 수 있다.
func problemFor(err error) *problemError {
	var problem *problemError
	if errors.As(err, &problem) {
		return problem
	}

	var (
		policy   *auth.PasswordPolicyError
		consent  *auth.ConsentError
		tooLarge *http.MaxBytesError
	)
	switch {
	case errors.As(err, &policy):
		p := newProblem(http.StatusUnprocessableEntity, ProblemCodeWeakPassword)
		for _, r := range policy.Reasons {
			// 명세에 없는 이유는 싣지 않는다. 이유를 더하면서 명세를 빠뜨린 경우이고, 시험이 잡는다.
			if reason := PasswordReason(r); reason.Valid() {
				p.reasons = append(p.reasons, reason)
			}
		}
		return p
	case errors.Is(err, auth.ErrWeakPassword):
		return newProblem(http.StatusUnprocessableEntity, ProblemCodeWeakPassword)

	case errors.As(err, &consent):
		p := newProblem(http.StatusUnprocessableEntity, ProblemCodeConsentRequired)
		p.consents = &ConsentProblem{
			Missing:  consentKinds(consent.Missing),
			Outdated: consentKinds(consent.Outdated),
		}
		return p
	case errors.Is(err, auth.ErrConsentRequired):
		return newProblem(http.StatusUnprocessableEntity, ProblemCodeConsentRequired)

	case errors.Is(err, auth.ErrInvalidEmail):
		return invalidField(ProblemFieldEmail)
	case errors.Is(err, auth.ErrInvalidDisplayName):
		return invalidField(ProblemFieldDisplayName)
	case errors.Is(err, auth.ErrInvalidTimezone):
		return invalidField(ProblemFieldTimezone)

	case errors.Is(err, auth.ErrEmailTaken):
		return newProblem(http.StatusConflict, ProblemCodeEmailTaken)
	case errors.Is(err, auth.ErrInvalidCredentials):
		return newProblem(http.StatusUnauthorized, ProblemCodeInvalidCredentials)
	case errors.Is(err, auth.ErrSessionInvalid):
		return errUnauthenticated

	case errors.As(err, &tooLarge):
		return errBodyTooLarge

	case errors.Is(err, context.DeadlineExceeded):
		// 해시 계산의 차례를 기다리다, 또는 DB가 멎어서 요청의 기한을 넘긴 경우다.
		// 어느 쪽이든 다시 보내면 될 일이므로 잠시 뒤에 다시 오게 한다.
		p := newProblem(http.StatusServiceUnavailable, ProblemCodeServiceUnavailable)
		p.retryAfter = retryAfterOverload
		p.cause = err
		return p
	case errors.Is(err, context.Canceled):
		p := newProblem(statusClientClosedRequest, ProblemCodeServiceUnavailable)
		p.cause = err
		return p
	}

	return problemForStatus(echo.StatusCode(err), err)
}

// retryAfterOverload는 서버가 몰려서 처리하지 못한 요청에 알려주는 대기 시간이다.
const retryAfterOverload = 5 * time.Second

// problemForStatus는 Echo와 검증 미들웨어가 상태 코드만 담아 돌려준 오류를 바꾼다.
func problemForStatus(status int, cause error) *problemError {
	switch status {
	case http.StatusBadRequest:
		return newProblem(status, ProblemCodeValidationFailed)
	case http.StatusUnauthorized:
		return errUnauthenticated
	case http.StatusForbidden:
		return newProblem(status, ProblemCodeForbidden)
	case http.StatusNotFound:
		return newProblem(status, ProblemCodeNotFound)
	case http.StatusMethodNotAllowed:
		return newProblem(status, ProblemCodeMethodNotAllowed)
	case http.StatusRequestEntityTooLarge:
		return errBodyTooLarge
	case http.StatusUnsupportedMediaType:
		return errNotJSON
	case http.StatusTooManyRequests:
		return newProblem(status, ProblemCodeRateLimited)
	case http.StatusServiceUnavailable:
		p := newProblem(status, ProblemCodeServiceUnavailable)
		p.cause = cause
		return p
	}
	if status >= 400 && status < 500 {
		return newProblem(status, ProblemCodeValidationFailed)
	}
	p := newProblem(http.StatusInternalServerError, ProblemCodeInternalError)
	p.cause = cause
	return p
}

func invalidField(field ProblemField) *problemError {
	p := newProblem(http.StatusUnprocessableEntity, ProblemCodeValidationFailed)
	p.fields = []ProblemField{field}
	return p
}

// consentKinds는 서비스가 알려준 동의의 종류를 응답의 타입으로 옮긴다.
// 서비스는 자기가 아는 종류만 돌려준다. 요청에 담겨 온 모르는 종류의 이름은 서비스가 이미 버렸다.
func consentKinds(kinds []string) []ConsentKind {
	out := make([]ConsentKind, 0, len(kinds))
	for _, k := range kinds {
		if kind := ConsentKind(k); kind.Valid() {
			out = append(out, kind)
		}
	}
	return out
}

// NewErrorHandler는 핸들러와 미들웨어가 돌려준 오류를 application/problem+json 응답으로 바꾸는 Echo의 오류 처리기다.
// 없는 경로(404)와 패닉(500)처럼 /api의 미들웨어를 거치지 않은 오류도 같은 꼴로 나간다.
//
// 오류를 따로 로그에 남기지는 않는다. 5xx의 원인은 접근 로그가 요청 ID와 함께 남긴다.
func NewErrorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return func(c *echo.Context, err error) {
		// 접근 로그가 상태를 적으려고 한 번, Echo가 마지막에 한 번 부른다. 이미 나간 응답에는 다시 쓰지 않는다.
		if resp, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && resp.Committed {
			return
		}
		if writeErr := writeProblem(c, problemFor(err)); writeErr != nil {
			// 클라이언트가 이미 연결을 끊은 경우다. 할 수 있는 일이 없다.
			logger.LogAttrs(c.Request().Context(), slog.LevelDebug, "problem response not written",
				slog.String("request_id", httpserver.RequestID(c.Request().Context())),
				slog.String("error", writeErr.Error()),
			)
		}
	}
}

func writeProblem(c *echo.Context, p *problemError) error {
	req := c.Request()
	header := c.Response().Header()
	if p.retryAfter > 0 {
		header.Set(echo.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds(p.retryAfter)))
	}
	if req.Method == http.MethodHead {
		return c.NoContent(p.status)
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(p.body(httpserver.RequestID(req.Context()))); err != nil {
		return err
	}
	return c.Blob(p.status, problemContentType, buf.Bytes())
}

// retryAfterSeconds는 기다릴 시간을 초로 올림한다. 내림하면 그 시각에 다시 온 요청이 또 거부된다.
func retryAfterSeconds(d time.Duration) int {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
