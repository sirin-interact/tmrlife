package api

import (
	"context"
	"log/slog"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
)

type (
	clientInfoKey struct{}
	sessionKey    struct{}
)

// sessionState는 세션 미들웨어가 요청에서 알아낸 것이다.
type sessionState struct {
	// presentedToken은 요청에 딸려 온 쿠키의 값이다. 통하는 세션인지와 상관없이 담는다.
	// 로그인과 가입은 이 세션을 끊고 새 토큰을 내주고, 로그아웃은 이 세션을 끊는다.
	presentedToken string
	// principal은 세션이 통할 때만 있다.
	principal *auth.Principal
}

func withClientInfo(ctx context.Context, info auth.ClientInfo) context.Context {
	return context.WithValue(ctx, clientInfoKey{}, info)
}

// clientInfoFrom은 요청을 보낸 쪽의 주소와 브라우저 정보를 돌려준다. 미들웨어를 거치지 않았으면 빈 값이다.
func clientInfoFrom(ctx context.Context) auth.ClientInfo {
	info, _ := ctx.Value(clientInfoKey{}).(auth.ClientInfo)
	return info
}

func withSession(ctx context.Context, state sessionState) context.Context {
	return context.WithValue(ctx, sessionKey{}, state)
}

func sessionFrom(ctx context.Context) sessionState {
	state, _ := ctx.Value(sessionKey{}).(sessionState)
	return state
}

// PrincipalFrom은 요청을 보낸 사람이 누구인지 돌려준다. 로그인하지 않은 요청이면 false다.
// 핸들러와 그 아래 계층이 "누구의 기록인가"를 알아내는 단 하나의 길이다. 요청 본문이나 경로의 사용자 ID는 믿지 않는다.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	state := sessionFrom(ctx)
	if state.principal == nil {
		return auth.Principal{}, false
	}
	return *state.principal, true
}

// AccessLogAttrs는 접근 로그에 더할 속성이다. 로그인한 요청이면 사용자 ID와 세션 ID를 남긴다.
// 이메일이나 이름은 남기지 않는다.
func AccessLogAttrs(ctx context.Context) []slog.Attr {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	return []slog.Attr{
		slog.String("user_id", principal.User.ID.String()),
		slog.String("session_id", principal.Session.ID.String()),
	}
}
