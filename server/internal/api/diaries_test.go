package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const (
	pathDiaries   = "/api/v1/diaries"
	pathResources = "/api/v1/resources"
)

func diaryPath(date string) string { return "/api/v1/diaries/" + date }
func dayPath(date string) string   { return "/api/v1/days/" + date }

// 명세가 거는 문은 핸들러보다 앞에 있다. 로그인 확인, 다른 출처 막기, 명세 검증이 구현과 상관없이 먼저 거른다.
func TestDiaryRouteGuards(t *testing.T) {
	srv := newTestServer(t, nil)
	guest := srv.browser(t)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)

	diaryBody := map[string]any{"text": "오늘은 일찍 잤다."}

	t.Run("기록을 읽고 고치는 경로는 로그인해야 한다", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodGet, path: pathDiaries + "?month=2026-09"},
			{method: http.MethodGet, path: diaryPath("2026-09-20")},
			{method: http.MethodPut, path: diaryPath("2026-09-20"), body: diaryBody},
			{method: http.MethodDelete, path: dayPath("2026-09-20")},
		} {
			requireProblem(t, guest.do(r), http.StatusUnauthorized, ProblemCodeUnauthenticated)
		}
	})

	t.Run("도움 자원은 로그인하지 않아도 열린다", func(t *testing.T) {
		rec := guest.get(pathResources)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var list ResourceList
		decode(t, rec, &list)
		require.NotEmpty(t, list.Items)
		assert.Equal(t, "109", list.Items[0].Phone, "급한 곳이 앞에 온다")
		for _, item := range list.Items {
			assert.NotEmpty(t, item.ID)
			assert.NotEmpty(t, item.Name)
			assert.NotEmpty(t, item.Description)
		}
	})

	t.Run("기록을 바꾸는 요청은 다른 출처에서 오면 막힌다", func(t *testing.T) {
		crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
		for _, r := range []request{
			{method: http.MethodPut, path: diaryPath("2026-09-20"), body: diaryBody, headers: crossSite},
			{method: http.MethodDelete, path: dayPath("2026-09-20"), headers: crossSite},
		} {
			requireProblem(t, member.do(r), http.StatusForbidden, ProblemCodeCrossOriginRejected)
		}
	})

	t.Run("명세와 맞지 않는 요청은 핸들러에 닿기 전에 걸러진다", func(t *testing.T) {
		tests := []struct {
			name string
			r    request
		}{
			{"날짜가 아닌 경로", request{method: http.MethodGet, path: diaryPath("yesterday")}},
			{"없는 날짜", request{method: http.MethodDelete, path: dayPath("2026-02-30")}},
			{"달의 꼴이 틀린 목록 요청", request{method: http.MethodGet, path: pathDiaries + "?month=2026-13"}},
			{"빈 검색어", request{method: http.MethodGet, path: pathDiaries + "?q="}},
			{"너무 긴 검색어", request{method: http.MethodGet, path: pathDiaries + "?q=" + url.QueryEscape(strings.Repeat("가", 101))}},
			{"글이 없는 일기", request{method: http.MethodPut, path: diaryPath("2026-09-20"), body: map[string]any{}}},
			{"빈 글", request{method: http.MethodPut, path: diaryPath("2026-09-20"), body: map[string]any{"text": ""}}},
			{"모르는 필드가 든 일기", request{method: http.MethodPut, path: diaryPath("2026-09-20"), body: map[string]any{"text": "글", "status": "confirmed"}}},
			{"너무 긴 일기", request{method: http.MethodPut, path: diaryPath("2026-09-20"), body: map[string]any{"text": strings.Repeat("가", 10001)}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				requireProblem(t, member.do(tt.r), http.StatusBadRequest, ProblemCodeValidationFailed)
			})
		}
	})

	t.Run("달과 검색어는 둘 중 하나만 받는다", func(t *testing.T) {
		requireProblem(t, member.get(pathDiaries), http.StatusBadRequest, ProblemCodeValidationFailed)
		requireProblem(t, member.get(pathDiaries+"?month=2026-09&q=%EB%B0%9C%ED%91%9C"),
			http.StatusBadRequest, ProblemCodeValidationFailed)
	})

	t.Run("공백만 있는 검색어와 글은 어느 필드가 문제인지 알려준다", func(t *testing.T) {
		p := requireProblem(t, member.get(pathDiaries+"?q="+url.QueryEscape("   ")),
			http.StatusUnprocessableEntity, ProblemCodeValidationFailed)
		require.NotNil(t, p.Fields)
		assert.Equal(t, []ProblemField{ProblemFieldQ}, *p.Fields)

		seedDay(t, srv, memberID(t, srv, testEmail), "2026-09-20")
		p = requireProblem(t, member.do(request{
			method: http.MethodPut, path: diaryPath("2026-09-20"), body: map[string]any{"text": "   \n  "},
		}), http.StatusUnprocessableEntity, ProblemCodeValidationFailed)
		require.NotNil(t, p.Fields)
		assert.Equal(t, []ProblemField{ProblemFieldText}, *p.Fields)
	})
}

func TestDiaries(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)

	t.Run("대화하지 않은 날에는 일기를 읽을 수도 쓸 수도 없다", func(t *testing.T) {
		requireProblem(t, member.get(diaryPath("2026-09-01")), http.StatusNotFound, ProblemCodeNotFound)
		requireProblem(t, member.do(request{
			method: http.MethodPut, path: diaryPath("2026-09-01"), body: map[string]any{"text": "쓴 적 없는 날"},
		}), http.StatusNotFound, ProblemCodeNotFound)
	})

	t.Run("대화한 날에는 초안이 없어도 직접 써서 확인할 수 있다", func(t *testing.T) {
		seedDay(t, srv, userID, "2026-09-02")

		rec := member.do(request{
			method: http.MethodPut, path: diaryPath("2026-09-02"),
			body: map[string]any{"text": "  오늘은 오래 걸었다.\n바람이 좋았다.  "},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var diary Diary
		decode(t, rec, &diary)
		assert.Equal(t, DiaryStatusConfirmed, diary.Status)
		assert.Equal(t, "오늘은 오래 걸었다.\n바람이 좋았다.", diary.Text, "앞뒤 공백은 떼고 저장한다")
		require.NotNil(t, diary.ConfirmedAt)
		assert.Equal(t, "2026-09-02", diary.Date.Format("2006-01-02"))

		// 다시 읽어도 같은 글이다.
		rec = member.get(diaryPath("2026-09-02"))
		require.Equal(t, http.StatusOK, rec.Code)
		var again Diary
		decode(t, rec, &again)
		assert.Equal(t, diary.Text, again.Text)
		assert.Equal(t, diary.UpdatedAt, again.UpdatedAt)
	})

	t.Run("초안은 그대로 읽히고, 확인하면 상태가 바뀐다", func(t *testing.T) {
		seedDiaryDraft(t, srv, userID, "2026-09-03", "오늘은 팀에서 발표를 했다.")

		rec := member.get(diaryPath("2026-09-03"))
		require.Equal(t, http.StatusOK, rec.Code)
		var draft Diary
		decode(t, rec, &draft)
		assert.Equal(t, DiaryStatusDraft, draft.Status)
		assert.Equal(t, "오늘은 팀에서 발표를 했다.", draft.Text)
		assert.Nil(t, draft.ConfirmedAt)

		rec = member.do(request{
			method: http.MethodPut, path: diaryPath("2026-09-03"),
			body: map[string]any{"text": "오늘은 팀에서 발표를 했다. 생각보다 괜찮았다."},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var confirmed Diary
		decode(t, rec, &confirmed)
		assert.Equal(t, DiaryStatusConfirmed, confirmed.Status)
		assert.Equal(t, "오늘은 팀에서 발표를 했다. 생각보다 괜찮았다.", confirmed.Text)
	})

	t.Run("한 달의 목록은 날짜순이고 첫 줄을 담는다", func(t *testing.T) {
		rec := member.get(pathDiaries + "?month=2026-09")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var list DiaryList
		decode(t, rec, &list)
		require.Len(t, list.Items, 2)
		assert.Equal(t, "2026-09-02", list.Items[0].Date.Format("2006-01-02"))
		assert.Equal(t, "오늘은 오래 걸었다.", list.Items[0].FirstLine, "첫 줄만 담는다")
		assert.Equal(t, "2026-09-03", list.Items[1].Date.Format("2006-01-02"))
		assert.Nil(t, list.Items[0].Snippet, "목록에는 앞뒤 글이 없다")

		empty := member.get(pathDiaries + "?month=2026-08")
		require.Equal(t, http.StatusOK, empty.Code)
		var none DiaryList
		decode(t, empty, &none)
		assert.Empty(t, none.Items, "일기가 없는 달은 빈 목록이다")
	})

	t.Run("검색은 최근 날짜부터 돌려주고 맞은 자리를 함께 담는다", func(t *testing.T) {
		rec := member.get(pathDiaries + "?q=" + url.QueryEscape("발표"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var list DiaryList
		decode(t, rec, &list)
		require.Len(t, list.Items, 1)
		assert.Equal(t, "2026-09-03", list.Items[0].Date.Format("2006-01-02"))
		require.NotNil(t, list.Items[0].Snippet)
		assert.Contains(t, *list.Items[0].Snippet, "발표")

		none := member.get(pathDiaries + "?q=" + url.QueryEscape("등산"))
		require.Equal(t, http.StatusOK, none.Code)
		var empty DiaryList
		decode(t, none, &empty)
		assert.Empty(t, empty.Items)
	})

	t.Run("검색은 앞뒤 공백을 떼고 영문은 대소문자를 가리지 않는다", func(t *testing.T) {
		seedDiaryDraft(t, srv, userID, "2026-09-04", "Standup 회의가 길었다.")

		rec := member.get(pathDiaries + "?q=" + url.QueryEscape("  standup  "))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var list DiaryList
		decode(t, rec, &list)
		require.Len(t, list.Items, 1)
		assert.Equal(t, "2026-09-04", list.Items[0].Date.Format("2006-01-02"))
	})

	t.Run("아직 확인하지 않은 빈 초안은 첫 줄이 비어 있다", func(t *testing.T) {
		seedDiaryDraft(t, srv, userID, "2026-09-05", "")
		rec := member.get(diaryPath("2026-09-05"))
		require.Equal(t, http.StatusOK, rec.Code)
		var diary Diary
		decode(t, rec, &diary)
		assert.Empty(t, diary.Text)
		assert.Equal(t, DiaryStatusDraft, diary.Status)
	})

	t.Run("남의 날짜는 없는 것과 똑같다", func(t *testing.T) {
		other := srv.browser(t)
		require.Equal(t, http.StatusCreated, other.signup("other@example.com").Code)

		requireProblem(t, other.get(diaryPath("2026-09-03")), http.StatusNotFound, ProblemCodeNotFound)
		requireProblem(t, other.do(request{
			method: http.MethodPut, path: diaryPath("2026-09-03"), body: map[string]any{"text": "남의 날"},
		}), http.StatusNotFound, ProblemCodeNotFound)
		requireProblem(t, other.do(request{method: http.MethodDelete, path: dayPath("2026-09-03")}),
			http.StatusNotFound, ProblemCodeNotFound)

		list := other.get(pathDiaries + "?month=2026-09")
		require.Equal(t, http.StatusOK, list.Code)
		var items DiaryList
		decode(t, list, &items)
		assert.Empty(t, items.Items, "남의 일기는 목록에도 없다")

		// 주인의 일기는 그대로다.
		require.Equal(t, http.StatusOK, member.get(diaryPath("2026-09-03")).Code)
	})

	t.Run("하루를 지우면 그날의 대화와 발화까지 함께 사라진다", func(t *testing.T) {
		ctx := t.Context()
		dayID := seedDay(t, srv, userID, "2026-09-06")
		conversationID := seedConversation(t, srv, userID, dayID)
		seedDiaryDraft(t, srv, userID, "2026-09-06", "지울 날의 일기")

		rec := member.do(request{method: http.MethodDelete, path: dayPath("2026-09-06")})
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		requireProblem(t, member.get(diaryPath("2026-09-06")), http.StatusNotFound, ProblemCodeNotFound)
		_, err := srv.store.Queries().GetConversation(ctx, db.GetConversationParams{ID: conversationID, UserID: userID})
		require.ErrorIs(t, err, store.ErrNotFound, "그날의 대화도 함께 지워진다")
		utterances, err := srv.store.Queries().ListUtterancesByConversation(ctx, db.ListUtterancesByConversationParams{
			ConversationID: conversationID, UserID: userID,
		})
		require.NoError(t, err)
		assert.Empty(t, utterances, "발화도 함께 지워진다")

		// 이미 지운 날을 다시 지우려 해도 찾지 못함이다.
		requireProblem(t, member.do(request{method: http.MethodDelete, path: dayPath("2026-09-06")}),
			http.StatusNotFound, ProblemCodeNotFound)
	})

	t.Run("주고받은 글은 로그에 남지 않는다", func(t *testing.T) {
		logs := srv.logs.String()
		for _, secret := range []string{"오늘은 오래 걸었다", "발표를 했다", "Standup 회의", "지울 날의 일기"} {
			assert.NotContains(t, logs, secret)
		}
	})
}

// memberID는 그 이메일로 가입한 사용자의 식별자를 돌려준다.
func memberID(t *testing.T, srv *testServer, email string) uuid.UUID {
	t.Helper()
	user, err := srv.store.Queries().GetUserByEmail(t.Context(), email)
	require.NoError(t, err)
	return user.ID
}

// seedDay는 그날의 하루 행을 만든다. 대화한 날을 흉내 내는 것이다.
func seedDay(t *testing.T, srv *testServer, userID uuid.UUID, date string) uuid.UUID {
	t.Helper()
	recordDate, err := recorddate.Parse(date)
	require.NoError(t, err)
	on, err := store.PGDate(recordDate)
	require.NoError(t, err)
	id, err := store.NewID()
	require.NoError(t, err)

	dayID, err := srv.store.Queries().UpsertDay(t.Context(), db.UpsertDayParams{
		ID: id, UserID: userID, RecordDate: on, Now: srv.clock.Now(),
	})
	require.NoError(t, err)
	return dayID
}

// seedConversation은 그 하루에 끝난 대화 하나와 발화 하나를 만든다.
func seedConversation(t *testing.T, srv *testServer, userID, dayID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	conversationID, err := store.NewID()
	require.NoError(t, err)
	_, err = srv.store.Queries().CreateConversation(ctx, db.CreateConversationParams{
		ID: conversationID, UserID: userID, DayID: dayID, StartedMode: store.ModeChat, Now: srv.clock.Now(),
	})
	require.NoError(t, err)

	sealer, err := srv.sealers.For(ctx, userID)
	require.NoError(t, err)
	utteranceID, err := store.NewID()
	require.NoError(t, err)
	text, err := sealer.SealString("오늘 하루는 어땠어요?", sealing.UtteranceText(utteranceID))
	require.NoError(t, err)
	_, err = srv.store.AppendUtterance(ctx, store.NewUtterance{
		ID: utteranceID, ConversationID: conversationID, UserID: userID,
		Speaker: store.SpeakerAI, Modality: store.ModeChat, Origin: store.OriginFixed,
		TextEnc: text, Now: srv.clock.Now(),
	})
	require.NoError(t, err)
	return conversationID
}

// seedDiaryDraft는 그날의 일기 초안을 만든다. 작업자가 만든 초안을 흉내 내는 것이다.
func seedDiaryDraft(t *testing.T, srv *testServer, userID uuid.UUID, date, text string) {
	t.Helper()
	ctx := t.Context()
	dayID := seedDay(t, srv, userID, date)
	sealer, err := srv.sealers.For(ctx, userID)
	require.NoError(t, err)
	newID, err := store.NewID()
	require.NoError(t, err)

	require.NoError(t, srv.store.InTx(ctx, func(q *db.Queries) error {
		_, err := store.SaveDiaryDraft(ctx, q, store.DiaryDraftWrite{
			NewID: newID, DayID: dayID, UserID: userID,
			Seal: func(rowID uuid.UUID) ([]byte, error) {
				return sealer.SealString(text, sealing.DiaryDraft(rowID))
			},
			Now: srv.clock.Now(),
		})
		return err
	}))
}
