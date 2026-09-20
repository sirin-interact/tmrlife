package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// AuthService는 핸들러와 세션 미들웨어가 인증 서비스에서 쓰는 부분이다. *auth.Service가 이 모양을 만족한다.
type AuthService interface {
	// CheckSignup은 해시를 계산하기 전에 거부될 가입 요청을 가려낸다. 시도 한도가 셀지 말지를 정하는 데 쓴다.
	CheckSignup(in auth.SignupInput) error
	Signup(ctx context.Context, in auth.SignupInput) (auth.Result, error)
	Login(ctx context.Context, in auth.LoginInput) (auth.Result, error)
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
	Logout(ctx context.Context, token string) error
}

// SettingsReader는 사용자의 설정을 읽는다. *db.Queries가 이 모양을 만족한다.
type SettingsReader interface {
	GetUserSettings(ctx context.Context, userID uuid.UUID) (db.UserSetting, error)
}

// handlers는 명세에서 만들어진 StrictServerInterface의 구현이다.
//
// 오류는 바꾸지 않고 그대로 돌려준다. 어떤 상태와 code로 나갈지는 problemFor 한 곳에서 정한다.
type handlers struct {
	auth     AuthService
	settings SettingsReader
	// diaries는 일기장 경로가 기록을 읽고 쓰는 자리다.
	diaries *diaryService
	// phrases는 도움 자원 목록이 오는 곳이다. 대화 채널이 보내는 목록과 같은 자료다.
	phrases *phrases.Catalogue
	cookies sessionCookies
	// authTimeout은 가입과 로그인 한 번에 주는 시간이다.
	authTimeout time.Duration
}

var _ StrictServerInterface = (*handlers)(nil)

func (h *handlers) GetAuthRequirements(context.Context, GetAuthRequirementsRequestObject) (GetAuthRequirementsResponseObject, error) {
	current := auth.CurrentConsents()
	consents := make([]ConsentGrant, 0, len(current))
	for _, c := range current {
		kind := ConsentKind(c.Kind)
		if !kind.Valid() {
			// 동의의 종류를 더하면서 명세를 빠뜨린 경우다. 화면이 모르는 값을 받느니 여기서 드러낸다.
			return nil, fmt.Errorf("api: consent kind %q is missing from the API spec", c.Kind)
		}
		consents = append(consents, ConsentGrant{Kind: kind, Version: c.Version})
	}
	return GetAuthRequirements200JSONResponse{
		Password: PasswordRequirements{
			MinLength: auth.MinPasswordLength,
			MaxBytes:  auth.MaxPasswordBytes,
		},
		Consents:             consents,
		DisplayNameMaxLength: auth.MaxDisplayNameLength,
	}, nil
}

func (h *handlers) Signup(ctx context.Context, request SignupRequestObject) (SignupResponseObject, error) {
	if request.Body == nil {
		return nil, errUnreadableBody
	}

	// 비밀번호 해시는 동시에 몇 개만 계산한다. 몰리면 차례를 기다리게 되는데, 끝없이 기다리게 두지 않는다.
	ctx, cancel := context.WithTimeout(ctx, h.authTimeout)
	defer cancel()

	in := signupInputFrom(request.Body)
	in.Client = clientInfoFrom(ctx)
	in.PresentedToken = sessionFrom(ctx).presentedToken
	result, err := h.auth.Signup(ctx, in)
	if err != nil {
		return nil, err
	}
	return Signup201JSONResponse{
		Body:    AuthResponse{User: userResponse(result.User)},
		Headers: Signup201ResponseHeaders{SetCookie: h.cookies.issue(result.Session.Token)},
	}, nil
}

// signupInputFrom은 요청 본문에서 온 값만 옮긴다. 시도 한도의 사전 검사와 핸들러가 같은 값을 보게 하려고 한 곳에 둔다.
func signupInputFrom(body *SignupJSONRequestBody) auth.SignupInput {
	consents := make([]auth.ConsentGrant, 0, len(body.Consents))
	for _, c := range body.Consents {
		consents = append(consents, auth.ConsentGrant{Kind: string(c.Kind), Version: c.Version})
	}
	return auth.SignupInput{
		Email:       body.Email,
		Password:    body.Password,
		DisplayName: deref(body.DisplayName),
		Timezone:    deref(body.Timezone),
		Consents:    consents,
	}
}

func (h *handlers) Login(ctx context.Context, request LoginRequestObject) (LoginResponseObject, error) {
	if request.Body == nil {
		return nil, errUnreadableBody
	}

	ctx, cancel := context.WithTimeout(ctx, h.authTimeout)
	defer cancel()

	result, err := h.auth.Login(ctx, auth.LoginInput{
		Email:          request.Body.Email,
		Password:       request.Body.Password,
		Client:         clientInfoFrom(ctx),
		PresentedToken: sessionFrom(ctx).presentedToken,
	})
	if err != nil {
		return nil, err
	}
	return Login200JSONResponse{
		Body:    AuthResponse{User: userResponse(result.User)},
		Headers: Login200ResponseHeaders{SetCookie: h.cookies.issue(result.Session.Token)},
	}, nil
}

// Logout은 세션이 없거나 이미 끝났어도 204다. 로그아웃은 몇 번을 눌러도 같은 결과여야 하고,
// 브라우저에 남은 쿠키는 어느 경우에든 지워야 한다.
func (h *handlers) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if token := sessionFrom(ctx).presentedToken; token != "" {
		if err := h.auth.Logout(ctx, token); err != nil {
			return nil, err
		}
	}
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: h.cookies.clear()}}, nil
}

func (h *handlers) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	// 로그인은 앞의 미들웨어가 이미 확인했다. 그래도 여기서 한 번 더 본다.
	// 누구의 설정을 읽을지가 이 값에서 나오므로, 미들웨어가 빠진 채로 등록되더라도 남의 것을 읽는 쪽으로 틀리지 않는다.
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	settings, err := h.settings.GetUserSettings(ctx, principal.User.ID)
	if err != nil {
		return nil, fmt.Errorf("load user settings: %w", err)
	}
	summary, err := settingsResponse(settings)
	if err != nil {
		return nil, err
	}
	return GetMe200JSONResponse{User: userResponse(principal.User), Settings: summary}, nil
}

// userResponse는 응답에 실을 사용자다. 실을 필드를 하나하나 옮겨 적는다.
// 도메인의 값을 통째로 직렬화하면, 나중에 도메인에 더한 필드가 아무도 모르게 응답으로 나간다.
func userResponse(u auth.User) User {
	out := User{
		ID:        u.ID,
		Email:     u.Email,
		Timezone:  u.Timezone,
		IsDemo:    u.IsDemo,
		CreatedAt: NewUTCTime(u.CreatedAt),
	}
	if u.DisplayName != "" {
		name := u.DisplayName
		out.DisplayName = &name
	}
	return out
}

func settingsResponse(s db.UserSetting) (SettingsSummary, error) {
	mode := ConversationMode(s.DefaultMode)
	if !mode.Valid() {
		return SettingsSummary{}, fmt.Errorf("api: conversation mode %q is missing from the API spec", s.DefaultMode)
	}
	reminderTime, err := formatWallClock(s.ReminderTime)
	if err != nil {
		return SettingsSummary{}, err
	}
	return SettingsSummary{
		ReminderEnabled: s.ReminderEnabled,
		ReminderTime:    reminderTime,
		DefaultMode:     mode,
		AnalysisEnabled: s.AnalysisEnabled,
		MemoryEnabled:   s.MemoryEnabled,
		MoodPickEnabled: s.MoodPickEnabled,
	}, nil
}

// formatWallClock은 DB의 time 값을 HH:MM으로 적는다. 날짜도 시간대도 없는 벽시계 시각이다.
func formatWallClock(t pgtype.Time) (string, error) {
	const (
		microsPerMinute = int64(time.Minute / time.Microsecond)
		minutesPerDay   = 24 * 60
	)
	if !t.Valid {
		return "", errors.New("api: reminder time is null")
	}
	minutes := t.Microseconds / microsPerMinute
	if t.Microseconds < 0 || minutes >= minutesPerDay {
		return "", errors.New("api: reminder time is outside of a day")
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
