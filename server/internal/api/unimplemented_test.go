package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 구현이 들어오기 전에도 명세가 거는 문은 닫혀 있어야 한다. 로그인 확인, 다른 출처 막기, 명세 검증은 핸들러보다 앞에 있다.
// 구현이 들어오면 501을 보는 경우만 그 경로의 시험으로 바뀐다.
func TestSpecOnlyRoutes(t *testing.T) {
	srv := newTestServer(t, nil)
	guest := srv.browser(t)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)

	const (
		pathDiaries   = "/api/v1/diaries"
		pathDiary     = "/api/v1/diaries/2026-09-20"
		pathDay       = "/api/v1/days/2026-09-20"
		pathResources = "/api/v1/resources"
	)
	diaryBody := map[string]any{"text": "오늘은 일찍 잤다."}

	t.Run("기록을 읽고 고치는 경로는 로그인해야 한다", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodGet, path: pathDiaries + "?month=2026-09"},
			{method: http.MethodGet, path: pathDiary},
			{method: http.MethodPut, path: pathDiary, body: diaryBody},
			{method: http.MethodDelete, path: pathDay},
		} {
			requireProblem(t, guest.do(r), http.StatusUnauthorized, ProblemCodeUnauthenticated)
		}
	})

	t.Run("도움 자원은 로그인하지 않아도 핸들러까지 간다", func(t *testing.T) {
		requireProblem(t, guest.get(pathResources), http.StatusNotImplemented, ProblemCodeInternalError)
	})

	t.Run("기록을 바꾸는 요청은 다른 출처에서 오면 막힌다", func(t *testing.T) {
		crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
		for _, r := range []request{
			{method: http.MethodPut, path: pathDiary, body: diaryBody, headers: crossSite},
			{method: http.MethodDelete, path: pathDay, headers: crossSite},
		} {
			requireProblem(t, member.do(r), http.StatusForbidden, ProblemCodeCrossOriginRejected)
		}
	})

	t.Run("명세와 맞지 않는 요청은 핸들러에 닿기 전에 걸러진다", func(t *testing.T) {
		tests := []struct {
			name string
			r    request
		}{
			{"날짜가 아닌 경로", request{method: http.MethodGet, path: "/api/v1/diaries/yesterday"}},
			{"없는 날짜", request{method: http.MethodDelete, path: "/api/v1/days/2026-02-30"}},
			{"달의 꼴이 틀린 목록 요청", request{method: http.MethodGet, path: pathDiaries + "?month=2026-13"}},
			{"빈 검색어", request{method: http.MethodGet, path: pathDiaries + "?q="}},
			{"너무 긴 검색어", request{method: http.MethodGet, path: pathDiaries + "?q=" + url.QueryEscape(strings.Repeat("가", 101))}},
			{"글이 없는 일기", request{method: http.MethodPut, path: pathDiary, body: map[string]any{}}},
			{"빈 글", request{method: http.MethodPut, path: pathDiary, body: map[string]any{"text": ""}}},
			{"모르는 필드가 든 일기", request{method: http.MethodPut, path: pathDiary, body: map[string]any{"text": "글", "status": "confirmed"}}},
			{"너무 긴 일기", request{method: http.MethodPut, path: pathDiary, body: map[string]any{"text": strings.Repeat("가", 10001)}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				requireProblem(t, member.do(tt.r), http.StatusBadRequest, ProblemCodeValidationFailed)
			})
		}
	})

	t.Run("명세에 맞는 요청은 핸들러까지 가고, 아직은 501이다", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodGet, path: pathDiaries + "?month=2026-09"},
			{method: http.MethodGet, path: pathDiaries + "?q=" + url.QueryEscape(strings.Repeat("가", 100))},
			{method: http.MethodGet, path: pathDiary},
			{method: http.MethodPut, path: pathDiary, body: map[string]any{"text": strings.Repeat("가", 10000)}},
			{method: http.MethodDelete, path: pathDay},
		} {
			requireProblem(t, member.do(r), http.StatusNotImplemented, ProblemCodeInternalError)
		}
	})
}
