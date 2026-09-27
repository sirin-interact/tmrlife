package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const (
	pathTrend          = "/api/v1/trend"
	pathInternalReview = "/api/v1/internal/review"
)

func daySignalsPath(date string) string { return "/api/v1/days/" + date + "/signals" }
func cancelPath(id uuid.UUID) string    { return "/api/v1/signals/" + id.String() + "/cancel" }
func uncancelPath(id uuid.UUID) string  { return "/api/v1/signals/" + id.String() + "/uncancel" }

// seedJudgement는 시험이 심는 항목 하나의 판단이다. evidence는 언급된 항목에만 있다.
type seedJudgement struct {
	status       signal.Status
	explicitness signal.Explicitness
	evidence     string
}

func seedObserved(evidence string) seedJudgement {
	return seedJudgement{status: signal.Observed, explicitness: signal.Direct, evidence: evidence}
}

func seedNotObserved(evidence string) seedJudgement {
	return seedJudgement{status: signal.NotObserved, explicitness: signal.Indirect, evidence: evidence}
}

// seedAnalysis는 그날의 대화 하나를 끝내고 여덟 항목의 신호 행을 심는다. 작업자가 한 일을 흉내 내는 것이다.
// 넘기지 않은 항목은 언급 없음이 된다.
func seedAnalysis(
	t *testing.T, srv *testServer, userID uuid.UUID, date string, judgements map[signal.Item]seedJudgement,
) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	dayID := seedDay(t, srv, userID, date)
	sealer, err := srv.sealers.For(ctx, userID)
	require.NoError(t, err)

	conversationID, err := store.NewID()
	require.NoError(t, err)
	_, err = srv.store.Queries().CreateConversation(ctx, db.CreateConversationParams{
		ID: conversationID, UserID: userID, DayID: dayID, StartedMode: store.ModeChat, Now: srv.clock.Now(),
	})
	require.NoError(t, err)
	_, err = srv.store.Queries().EndConversation(ctx, db.EndConversationParams{
		Now: srv.clock.Now(), EndReason: store.EndReasonUser, ProcessingStatus: store.ProcessingNone,
		ID: conversationID, UserID: userID,
	})
	require.NoError(t, err)

	var analysis store.ConversationAnalysis
	analysis.UserID = userID
	analysis.DayID = dayID
	analysis.ConversationID = conversationID
	analysis.ExtractorVersion = "test/scripted"
	analysis.Now = srv.clock.Now()
	for _, item := range signal.AllItems() {
		seeded, ok := judgements[item]
		if !ok {
			continue
		}
		text := seeded.evidence
		analysis.Judgements[item.Index()] = store.SignalJudgement{
			Judgement: signal.Judgement{Status: seeded.status, Explicitness: seeded.explicitness},
			SealEvidence: func(rowID uuid.UUID) ([]byte, error) {
				return sealer.SealString(text, sealing.SignalEvidence(rowID))
			},
		}
	}
	require.NoError(t, srv.store.SaveConversationSignals(ctx, analysis))
	return conversationID
}

// seedQuietDays는 그날 대화했지만 어느 항목도 나오지 않은 날을 만든다. 대화한 일수를 채우는 데 쓴다.
func seedQuietDays(t *testing.T, srv *testServer, userID uuid.UUID, dates ...string) {
	t.Helper()
	for _, date := range dates {
		seedAnalysis(t, srv, userID, date, nil)
	}
}

func makeDemo(t *testing.T, srv *testServer, userID uuid.UUID) {
	t.Helper()
	_, err := srv.pool.Exec(t.Context(), `UPDATE users SET is_demo = true WHERE id = $1`, userID)
	require.NoError(t, err)
}

// 명세가 거는 문은 핸들러보다 앞에 있다. 로그인 확인, 다른 출처 막기, 명세 검증이 구현과 상관없이 먼저 거른다.
func TestSignalRouteGuards(t *testing.T) {
	srv := newTestServer(t, nil)
	guest := srv.browser(t)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	someID := uuid.Must(uuid.NewV7())

	t.Run("마음 신호 경로는 모두 로그인해야 한다", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodGet, path: pathTrend},
			{method: http.MethodGet, path: daySignalsPath("2026-09-20")},
			{method: http.MethodGet, path: pathInternalReview},
			{method: http.MethodPost, path: cancelPath(someID)},
			{method: http.MethodPost, path: uncancelPath(someID)},
		} {
			requireProblem(t, guest.do(r), http.StatusUnauthorized, ProblemCodeUnauthenticated)
		}
	})

	t.Run("신호를 빼고 되돌리는 요청은 다른 출처에서 오면 막힌다", func(t *testing.T) {
		crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
		for _, r := range []request{
			{method: http.MethodPost, path: cancelPath(someID), headers: crossSite},
			{method: http.MethodPost, path: uncancelPath(someID), headers: crossSite},
		} {
			requireProblem(t, member.do(r), http.StatusForbidden, ProblemCodeCrossOriginRejected)
		}
	})

	t.Run("명세와 맞지 않는 요청은 핸들러에 닿기 전에 걸러진다", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodGet, path: daySignalsPath("yesterday")},
			{method: http.MethodGet, path: daySignalsPath("2026-02-30")},
			{method: http.MethodPost, path: "/api/v1/signals/42/cancel"},
			{method: http.MethodPost, path: "/api/v1/signals/not-a-uuid/uncancel"},
		} {
			requireProblem(t, member.do(r), http.StatusBadRequest, ProblemCodeValidationFailed)
		}
	})
}

// 기록이 하나도 없는 계정에도 빈 달력이 온다. 처음 들어온 사람의 화면이 오류로 보이지 않아야 한다.
func TestTrendWithoutRecords(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)

	rec := member.get(pathTrend)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var trend Trend
	decode(t, rec, &trend)
	p := params.Default()

	assert.Equal(t, "2026-09-19", trend.AsOf.String(), "오늘의 분석이 없으면 어제가 기준일이다")
	assert.Equal(t, trend.AsOf, trend.To)
	assert.Equal(t, "2026-09-06", trend.From.String())
	assert.Zero(t, trend.ConversationDays)
	assert.True(t, trend.InsufficientRecords)
	assert.True(t, trend.BaselinePending)

	require.Len(t, trend.Rows, 3)
	assert.Equal(t, []TrendRowKey{TrendRowKeyMood, TrendRowKeySleep, TrendRowKeyEnergy},
		[]TrendRowKey{trend.Rows[0].Row, trend.Rows[1].Row, trend.Rows[2].Row}, "기분, 수면, 에너지 순")
	for _, row := range trend.Rows {
		require.Len(t, row.Cells, p.Window.Days)
		assert.Equal(t, trend.From, row.Cells[0].Date)
		assert.Equal(t, trend.To, row.Cells[len(row.Cells)-1].Date)
		for _, cell := range row.Cells {
			assert.Equal(t, TrendMarkNoConversation, cell.Mark)
		}
		assert.Equal(t, SignalRate{ObservedDays: 0, Days: 0}, row.Window)
		assert.Nil(t, row.Usual, "평소가 잡히기 전에는 내보내지 않는다")
		assert.Equal(t, TrendComparisonNone, row.Comparison)
	}
}

// 대화한 날이 기준을 채우면 기록 부족이 풀리고, 칸마다 그날의 표시가 찍힌다.
func TestTrendWithRecords(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)

	// 오늘까지 이레를 채운다. 마지막 날에만 잠 이야기가 나왔다.
	seedQuietDays(t, srv, userID, "2026-09-14", "2026-09-15", "2026-09-16", "2026-09-17", "2026-09-18", "2026-09-19")
	seedAnalysis(t, srv, userID, "2026-09-20", map[signal.Item]seedJudgement{
		signal.Sleep:    seedObserved("어젯밤에 세 번 깼어"),
		signal.Appetite: seedNotObserved("밥은 잘 먹었어"),
	})

	rec := member.get(pathTrend)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var trend Trend
	decode(t, rec, &trend)
	assert.Equal(t, "2026-09-20", trend.AsOf.String(), "오늘의 분석이 있으면 오늘이 기준일이다")
	assert.Equal(t, 7, trend.ConversationDays)
	assert.False(t, trend.InsufficientRecords)
	assert.True(t, trend.BaselinePending, "평소는 첫 대화 날부터 얼마 뒤에야 잡힌다")

	marks := map[TrendRowKey][]TrendMark{}
	for _, row := range trend.Rows {
		marks[row.Row] = make([]TrendMark, 0, len(row.Cells))
		for _, cell := range row.Cells {
			marks[row.Row] = append(marks[row.Row], cell.Mark)
		}
	}
	// 창은 9월 7일부터다. 9월 14일이 여덟 번째 칸이고 9월 20일이 마지막 칸이다.
	assert.Equal(t, TrendMarkObserved, marks[TrendRowKeySleep][13], "잠 이야기가 나온 날은 찬 점")
	assert.Equal(t, TrendMarkNotMentioned, marks[TrendRowKeySleep][12], "대화만 한 날은 작은 점")
	assert.Equal(t, TrendMarkNoConversation, marks[TrendRowKeySleep][0], "대화하지 않은 날은 빈칸")
	assert.Equal(t, TrendMarkNotMentioned, marks[TrendRowKeyMood][13], "그날 기분 이야기는 없었다")

	sleep := trend.Rows[1]
	require.Equal(t, TrendRowKeySleep, sleep.Row)
	assert.Equal(t, SignalRate{ObservedDays: 1, Days: 7}, sleep.Window)
}

// 하루의 근거는 항목마다 사용자가 한 말을 그대로 싣는다.
func TestDaySignals(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)

	const (
		sleepSaid    = "어젯밤에 세 번 깼어"
		appetiteSaid = "밥은 잘 먹었어"
	)
	conversationID := seedAnalysis(t, srv, userID, "2026-09-20", map[signal.Item]seedJudgement{
		signal.Sleep:    seedObserved(sleepSaid),
		signal.Appetite: seedNotObserved(appetiteSaid),
	})

	rec := member.get(daySignalsPath("2026-09-20"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var day DaySignals
	decode(t, rec, &day)
	assert.Equal(t, "2026-09-20", day.Date.String())
	require.True(t, day.Analysed)
	require.Len(t, day.Items, signal.ItemCount, "언급이 없었던 항목도 온다")

	byItem := map[SignalItem]DaySignalItem{}
	order := make([]SignalItem, 0, len(day.Items))
	for _, item := range day.Items {
		byItem[item.Item] = item
		order = append(order, item.Item)
	}
	assert.Equal(t, []SignalItem{
		SignalItemInterest, SignalItemMood, SignalItemSleep, SignalItemFatigue,
		SignalItemAppetite, SignalItemSelfBlame, SignalItemConcentration, SignalItemPsychomotor,
	}, order, "정해진 항목 순서로 온다")

	sleep := byItem[SignalItemSleep]
	assert.Equal(t, SignalStatusObserved, sleep.Status)
	assert.Equal(t, SignalExplicitnessDirect, sleep.Explicitness)
	require.Len(t, sleep.Rows, 1)
	require.NotNil(t, sleep.Rows[0].Evidence)
	assert.Equal(t, sleepSaid, *sleep.Rows[0].Evidence, "근거는 사용자가 한 말 그대로여야 한다")
	assert.Equal(t, conversationID, sleep.Rows[0].ConversationID)
	assert.False(t, sleep.Rows[0].Cancelled)
	assert.NotEqual(t, uuid.Nil, sleep.Rows[0].ID)

	appetite := byItem[SignalItemAppetite]
	assert.Equal(t, SignalStatusNotObserved, appetite.Status)
	assert.Equal(t, SignalExplicitnessIndirect, appetite.Explicitness)
	require.Len(t, appetite.Rows, 1)
	require.NotNil(t, appetite.Rows[0].Evidence)
	assert.Equal(t, appetiteSaid, *appetite.Rows[0].Evidence)

	mood := byItem[SignalItemMood]
	assert.Equal(t, SignalStatusNotMentioned, mood.Status)
	assert.Equal(t, SignalExplicitnessNone, mood.Explicitness)
	require.Len(t, mood.Rows, 1)
	assert.Nil(t, mood.Rows[0].Evidence, "언급 없음인 판단에는 근거가 없다")

	t.Run("대화하지 않은 날은 200이고 항목이 비어 있다", func(t *testing.T) {
		rec := member.get(daySignalsPath("2026-09-19"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var empty DaySignals
		decode(t, rec, &empty)
		assert.False(t, empty.Analysed)
		assert.Empty(t, empty.Items)
	})

	t.Run("남의 날짜는 비어 있는 것과 같다", func(t *testing.T) {
		other := srv.browser(t)
		require.Equal(t, http.StatusCreated, other.signup("other@example.com").Code)
		rec := other.get(daySignalsPath("2026-09-20"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var empty DaySignals
		decode(t, rec, &empty)
		assert.False(t, empty.Analysed)
		assert.Empty(t, empty.Items)
	})
}

// 신호를 빼면 그 항목의 합친 판단이 함께 바뀌고, 추세의 일수도 다음에 읽을 때 줄어든다.
func TestCancelSignal(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)

	seedQuietDays(t, srv, userID, "2026-09-14", "2026-09-15", "2026-09-16", "2026-09-17", "2026-09-18", "2026-09-19")
	seedAnalysis(t, srv, userID, "2026-09-20", map[signal.Item]seedJudgement{
		signal.Sleep: seedObserved("어젯밤에 세 번 깼어"),
	})

	rec := member.get(daySignalsPath("2026-09-20"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var before DaySignals
	decode(t, rec, &before)
	signalID := itemOf(t, before, SignalItemSleep).Rows[0].ID

	trendObserved := func() int {
		t.Helper()
		rec := member.get(pathTrend)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var trend Trend
		decode(t, rec, &trend)
		for _, row := range trend.Rows {
			if row.Row == TrendRowKeySleep {
				return row.Window.ObservedDays
			}
		}
		t.Fatal("수면 줄이 없다")
		return 0
	}
	require.Equal(t, 1, trendObserved())

	t.Run("빼면 그날의 판단이 언급 없음으로 바뀐다", func(t *testing.T) {
		rec := member.post(cancelPath(signalID), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var after DaySignals
		decode(t, rec, &after)
		sleep := itemOf(t, after, SignalItemSleep)
		assert.Equal(t, SignalStatusNotMentioned, sleep.Status, "남은 기록으로 다시 합친 값이다")
		require.Len(t, sleep.Rows, 1)
		assert.True(t, sleep.Rows[0].Cancelled)
		require.NotNil(t, sleep.Rows[0].Evidence, "행은 남으므로 무엇을 뺐는지 볼 수 있다")
	})

	t.Run("추세의 일수가 줄어든다", func(t *testing.T) {
		assert.Zero(t, trendObserved())
	})

	t.Run("두 번 빼도 결과가 같다", func(t *testing.T) {
		rec := member.post(cancelPath(signalID), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var after DaySignals
		decode(t, rec, &after)
		assert.True(t, itemOf(t, after, SignalItemSleep).Rows[0].Cancelled)
	})

	t.Run("되돌리면 다시 계산에 든다", func(t *testing.T) {
		rec := member.post(uncancelPath(signalID), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var after DaySignals
		decode(t, rec, &after)
		sleep := itemOf(t, after, SignalItemSleep)
		assert.Equal(t, SignalStatusObserved, sleep.Status)
		assert.False(t, sleep.Rows[0].Cancelled)
		assert.Equal(t, 1, trendObserved())
	})

	t.Run("뺀 적이 없는 신호를 되돌려도 200이다", func(t *testing.T) {
		rec := member.post(uncancelPath(signalID), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("남의 신호는 없는 것과 같다", func(t *testing.T) {
		other := srv.browser(t)
		require.Equal(t, http.StatusCreated, other.signup("other@example.com").Code)
		requireProblem(t, other.post(cancelPath(signalID), nil), http.StatusNotFound, ProblemCodeNotFound)
		requireProblem(t, other.post(uncancelPath(signalID), nil), http.StatusNotFound, ProblemCodeNotFound)
	})

	t.Run("없는 신호도 찾지 못함이다", func(t *testing.T) {
		requireProblem(t, member.post(cancelPath(uuid.Must(uuid.NewV7())), nil),
			http.StatusNotFound, ProblemCodeNotFound)
	})
}

func itemOf(t *testing.T, day DaySignals, want SignalItem) DaySignalItem {
	t.Helper()
	for _, item := range day.Items {
		if item.Item == want {
			return item
		}
	}
	t.Fatalf("%s 항목이 없다", want)
	return DaySignalItem{}
}

// 추정 점수와 개입 단계는 시연 계정과 관리자만 본다. 사용자에게는 그 숫자를 보여주지 않는다.
func TestInternalReviewIsForbiddenForOrdinaryAccounts(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)

	requireProblem(t, member.get(pathInternalReview), http.StatusForbidden, ProblemCodeForbidden)
	assert.NotContains(t, srv.bodies.String(), "\"total\"", "점수는 응답에 실리지 않는다")
}

func TestInternalReview(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)
	makeDemo(t, srv, userID)

	// 이레 동안 날마다 잠 이야기가 나왔다. 기록 부족이 풀리고 항목 점수가 붙는다.
	for _, date := range []string{
		"2026-09-14", "2026-09-15", "2026-09-16", "2026-09-17", "2026-09-18", "2026-09-19", "2026-09-20",
	} {
		seedAnalysis(t, srv, userID, date, map[signal.Item]seedJudgement{
			signal.Sleep: seedObserved("어젯밤에 세 번 깼어"),
		})
	}

	// 시연 계정은 세션을 다시 받아야 is_demo가 실린다.
	fresh := srv.browser(t)
	require.Equal(t, http.StatusOK, fresh.post(pathLogin, loginBody(testEmail, testPassword)).Code)

	rec := fresh.get(pathInternalReview)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var review InternalReview
	decode(t, rec, &review)
	p := params.Default()

	assert.Equal(t, "2026-09-20", review.AsOf.String())

	// 저장된 판단만 보고는 어떤 모델이 읽은 것인지 가릴 수 없다. 행에 남은 표시를 응답이 함께 싣는다.
	require.Len(t, review.Extractors, 1)
	assert.Equal(t, "test/scripted", review.Extractors[0].Version)
	assert.Equal(t, 7*signal.ItemCount, review.Extractors[0].Rows, "언급 없음인 항목의 행도 그 추출기가 남긴 것이다")
	assert.False(t, review.Extractors[0].LastAt.IsZero())

	assert.Equal(t, p.Window.Days, review.Params.WindowDays)
	assert.Equal(t, []int{p.Score.MildMin, p.Score.ModerateMin, p.Score.ModeratelySevereMin, p.Score.SevereMin},
		review.Params.BandMinScores)

	assert.Equal(t, 7, review.Score.ConversationDays)
	assert.False(t, review.Score.Insufficient)
	require.Len(t, review.Score.Items, signal.ItemCount)
	sleep := scoreItemOf(t, review.Score, SignalItemSleep)
	assert.Equal(t, 7, sleep.ObservedDays)
	assert.Equal(t, 14, sleep.ConvertedDays, "이레 중 이레는 두 주로 환산하면 열나흘이다")
	assert.Equal(t, 3, sleep.Points)
	assert.Equal(t, 3, review.Score.Total, "잠 말고는 이야기가 없었다")
	assert.Equal(t, ScoreBandMinimal, review.Score.Band)

	assert.Equal(t, ReviewRatio{Num: 7, Den: p.Window.Days}, review.Confidence.RecordCoverage)
	assert.Equal(t, ReviewRatio{Num: 1, Den: signal.ItemCount}, review.Confidence.ItemCoverage)
	assert.Equal(t, ReviewRatio{Num: 7, Den: 7}, review.Confidence.Explicitness)
	assert.Equal(t, ConfidenceComponentItemCoverage, review.Confidence.Limiting, "가장 약한 고리가 결과를 정한다")
	assert.Equal(t, ConfidenceLevelLow, review.Confidence.Level)
	assert.Len(t, review.Confidence.MissingItems, signal.ItemCount-1)

	assert.False(t, review.Baseline.Established)
	require.NotNil(t, review.Baseline.Start)
	assert.Equal(t, "2026-09-14", review.Baseline.Start.String())
	assert.Equal(t, 7, review.Baseline.Days)
	assert.InDelta(t, 1, review.Baseline.Mu, 1e-6, "날마다 신호 하나였다")
	require.Len(t, review.Baseline.ItemRates, signal.ItemCount)
	require.Len(t, review.Baseline.TrendRates, 3)

	assert.False(t, review.Change.Running, "평소가 잡히기 전에는 돌지 않는다")
	assert.Empty(t, review.Change.Series)
	assert.Nil(t, review.Change.From)

	require.NotNil(t, review.Stage.From)
	assert.Equal(t, "2026-09-14", review.Stage.From.String())
	require.Len(t, review.Stage.Series, 7, "첫 대화 날부터 기준일까지 하루씩")
	last := review.Stage.Series[len(review.Stage.Series)-1]
	assert.Equal(t, "2026-09-20", last.Date.String())
	assert.True(t, last.HasRecord)
	assert.Equal(t, review.Stage.Stage, last.Stage)
	assert.NotNil(t, last.Reasons)
}

func scoreItemOf(t *testing.T, score ReviewScore, want SignalItem) ReviewScoreItem {
	t.Helper()
	for _, item := range score.Items {
		if item.Item == want {
			return item
		}
	}
	t.Fatalf("%s 항목이 없다", want)
	return ReviewScoreItem{}
}

// 응답의 숫자는 계산 코어가 만든 값 그대로여야 한다. 핸들러가 따로 세거나 나누면 여기서 어긋난다.
func TestTrendMatchesTheCore(t *testing.T) {
	srv := newTestServer(t, nil)
	member := srv.browser(t)
	require.Equal(t, http.StatusCreated, member.signup(testEmail).Code)
	userID := memberID(t, srv, testEmail)

	for _, date := range []string{
		"2026-09-08", "2026-09-10", "2026-09-12", "2026-09-14", "2026-09-16", "2026-09-18", "2026-09-20",
	} {
		seedAnalysis(t, srv, userID, date, map[signal.Item]seedJudgement{
			signal.Mood:  seedObserved("오늘은 계속 가라앉아 있었어"),
			signal.Sleep: seedNotObserved("잠은 잘 잤어"),
		})
	}

	rec := member.get(pathTrend)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var trend Trend
	decode(t, rec, &trend)

	rows, err := srv.store.Queries().ListSignalRowsByUser(t.Context(), userID)
	require.NoError(t, err)
	byDate, err := store.SignalDaysByUser(rows)
	require.NoError(t, err)
	days, err := signal.MergeDays(byDate)
	require.NoError(t, err)
	asOf, err := recorddate.Parse(trend.AsOf.String())
	require.NoError(t, err)
	evaluation, err := assess.Evaluate(days, asOf, params.Default())
	require.NoError(t, err)

	assert.Equal(t, evaluation.Trend.ConversationDays, trend.ConversationDays)
	for i, row := range trend.Rows {
		line := evaluation.Trend.Rows[i]
		assert.Equal(t, line.Row.String(), string(row.Row))
		assert.Equal(t, line.ObservedDays, row.Window.ObservedDays)
		assert.Equal(t, line.ConversationDays, row.Window.Days)
		assert.Equal(t, line.Comparison.String(), string(row.Comparison))
		require.Len(t, row.Cells, len(line.Marks))
		for j, cell := range row.Cells {
			assert.Equal(t, line.Marks[j].String(), string(cell.Mark))
		}
	}
}
