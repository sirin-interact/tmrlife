package diary_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestDraftFirst(t *testing.T) {
	t.Parallel()

	t.Run("사용자가 한 말만 모델에 보내고, 받은 글을 초안으로 잠가 저장하고, 대화를 닫는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(
			assistant("오늘 하루는 어땠어요?"),
			user("오늘 친구랑 한강 갔다왔어"),
			assistant("제주도 여행은 즐거우셨어요? 승진도 축하드려요."),
			user("치킨 먹고\n자전거도 탔어"),
		)
		f.reply("오늘 친구랑 한강에 갔다 왔다. 치킨을 먹고 자전거도 탔다.")

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Equal(t, diary.ModeFirst, result.Mode)
		assert.Equal(t, 1, result.Conversations)
		assert.Equal(t, 2, result.UserUtterances)
		assert.Equal(t, fakeModel, result.Model)
		assert.Equal(t, 1, result.Rounds)
		assert.Regexp(t, `^[0-9a-f]{12}$`, result.PromptVersion, "어느 지시문으로 만든 초안인지 남긴다")

		req, ok := f.llm.LastRequest()
		require.True(t, ok)
		registry, err := prompts.LoadEmbedded()
		require.NoError(t, err)
		prompt, err := registry.Get(diary.PromptTask)
		require.NoError(t, err)
		assert.Equal(t, diary.PromptTask, req.Task)
		assert.Equal(t, prompt.System, req.System)
		assert.JSONEq(t, string(prompt.Schema), string(req.JSONSchema))
		assert.Equal(t, ai.ThinkingLow, req.Thinking)
		assert.Equal(t, diary.DefaultMaxOutputTokens, req.MaxOutputTokens, "생각 토큰까지 담을 만큼 넉넉해야 한다")

		sent := f.sentText(0)
		assert.Equal(t, "방식: 처음 쓰기\n\n사용자가 한 말:\n1. 오늘 친구랑 한강 갔다왔어\n2. 치킨 먹고 자전거도 탔어", sent)
		for _, leaked := range []string{"제주도", "승진", "어땠어요"} {
			assert.NotContains(t, sent, leaked, "AI가 한 말은 모델에 가지 않는다")
		}

		got := f.mustDiary(c.dayID)
		assert.Equal(t, store.DiaryDraft, got.row.Status)
		assert.Equal(t, "오늘 친구랑 한강에 갔다 왔다. 치킨을 먹고 자전거도 탔다.", got.draft)
		assert.Nil(t, got.row.BodyEnc, "확인한 글은 사용자만 만든다")
		assert.Nil(t, got.row.ConfirmedAt)
		assert.NotContains(t, string(got.row.DraftEnc), "한강", "초안은 평문으로 저장되지 않는다")
		assert.True(t, got.row.UpdatedAt.Equal(f.clock.Now()), "시각은 주입받은 시계에서 온다")
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
	})

	t.Run("기록 날짜로도 부를 수 있고, 기록이 없는 날은 그만둔다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘은 집에서 쉬었어"))
		f.reply("오늘은 집에서 쉬었다.")

		result, err := f.service.DraftByDate(t.Context(), f.userID, f.date)
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Equal(t, "오늘은 집에서 쉬었다.", f.mustDiary(c.dayID).draft)

		result, err = f.service.DraftByDate(t.Context(), f.userID, f.date.AddDays(1))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeGone, result.Outcome)
		assert.Equal(t, 1, f.llm.Calls())
	})

	t.Run("하루에 끝난 대화가 여럿이면 순서대로 모두 담고 대화의 경계를 알린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		morning := f.ended(user("아침에 늦잠 잤어"))
		evening := f.ended(assistant("저녁은 드셨어요?"), user("저녁엔 라면 끓여 먹었어"))
		f.reply("아침에 늦잠을 잤다. 저녁엔 라면을 끓여 먹었다.")

		result, err := f.service.Draft(t.Context(), evening.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, 2, result.Conversations)
		assert.Equal(t,
			"방식: 처음 쓰기\n\n사용자가 한 말:\n1. 아침에 늦잠 잤어\n(시간이 지난 뒤 다시 나눈 대화)\n2. 저녁엔 라면 끓여 먹었어",
			f.sentText(0))
		assert.Equal(t, store.ProcessingDone, f.processingStatus(morning))
		assert.Equal(t, store.ProcessingDone, f.processingStatus(evening))
	})

	t.Run("열려 있는 대화는 담지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		done := f.ended(user("점심에 김밥 먹었어"))
		open := f.open(user("지금은 산책 나왔어"))
		f.reply("점심에 김밥을 먹었다.")

		result, err := f.service.Draft(t.Context(), done.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, 1, result.Conversations)
		assert.NotContains(t, f.sentText(0), "산책", "끝나지 않은 대화의 말은 그 대화가 끝난 뒤에 담는다")
		assert.Equal(t, store.ProcessingNone, f.processingStatus(open))
	})

	t.Run("사용자가 한 말이 없으면 모델을 부르지 않고 일기도 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(assistant("오늘 하루는 어땠어요?"))

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeNothing, result.Outcome)
		assert.Zero(t, f.llm.Calls())
		_, exists := f.diary(c.dayID)
		assert.False(t, exists, "말하지 않은 날에는 일기가 생기지 않는다")
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
	})

	t.Run("모델이 쓸 말이 없다고 답하면 직접 쓸 수 있게 빈 초안을 남긴다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(assistant("오늘 회사에서 힘드셨어요?"), user("응"), assistant("푹 쉬어요."), user("몰라"))
		f.reply("")

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeEmpty, result.Outcome)
		got := f.mustDiary(c.dayID)
		assert.Equal(t, store.DiaryDraft, got.row.Status)
		assert.NotNil(t, got.row.DraftEnc)
		assert.Empty(t, got.draft)
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
	})
}

func TestDraftAppend(t *testing.T) {
	t.Parallel()

	t.Run("사용자가 고쳐서 확인한 글은 그대로 두고 새 대화의 단락만 뒤에 붙인다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오늘 팀장한테 깨졌어"))
		f.reply("오늘 팀장한테 깨졌다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)

		// 사용자가 초안을 제 말로 고쳐 확인한다. 끝의 공백과 줄바꿈까지 사용자가 쓴 것이다.
		edited := "팀장한테 한 소리 들었다.  \n그래도 퇴근길엔 좀 풀렸다. \n"
		confirmed := f.confirm(first.dayID, edited)

		second := f.ended(assistant("야근하셨어요?"), user("밤에 맥주 한 캔 마셨어"))
		f.reply("밤에는 맥주를 한 캔 마셨다.")

		result, err := f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Equal(t, diary.ModeAppend, result.Mode)
		assert.Equal(t, 1, result.Conversations, "이미 담긴 대화는 다시 재료가 되지 않는다")

		sent := f.sentText(1)
		assert.Equal(t, "방식: 이어 쓰기\n\n사용자가 한 말:\n1. 밤에 맥주 한 캔 마셨어", sent)
		assert.NotContains(t, sent, "팀장", "앞선 대화도, 사용자가 손으로 쓴 일기도 모델에 가지 않는다")
		assert.NotContains(t, sent, "퇴근길")

		got := f.mustDiary(second.dayID)
		assert.Equal(t, store.DiaryDraft, got.row.Status, "붙인 글을 사용자가 다시 확인한다")
		assert.Equal(t, edited+"\n밤에는 맥주를 한 캔 마셨다.", got.draft, "앞의 글은 한 글자도 바뀌지 않고, 단락 사이는 빈 줄 하나다")
		assert.Equal(t, edited, got.body)
		assert.Equal(t, confirmed.BodyEnc, got.row.BodyEnc, "확인한 글의 암호문은 건드리지 않는다")
		require.NotNil(t, got.row.ConfirmedAt)
		assert.True(t, confirmed.ConfirmedAt.Equal(*got.row.ConfirmedAt))
	})

	t.Run("확인하지 않은 초안이 있으면 그 초안 뒤에 붙인다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("아침에 비가 왔어"))
		f.reply("아침에 비가 왔다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)

		second := f.ended(user("저녁엔 갰어"))
		f.reply("저녁엔 날이 갰다.")
		result, err := f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, diary.ModeAppend, result.Mode)
		got := f.mustDiary(second.dayID)
		assert.Equal(t, "아침에 비가 왔다.\n\n저녁엔 날이 갰다.", got.draft)
		assert.Nil(t, got.row.BodyEnc)
	})

	t.Run("확인한 글 뒤에 붙인 초안을 확인하기 전에 또 대화하면 그 초안 뒤에 붙인다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("출근길에 지하철이 멈췄어"))
		f.reply("출근길에 지하철이 멈췄다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)
		f.confirm(first.dayID, "지하철이 멈춰서 지각했다.")

		second := f.ended(user("점심은 걸렀어"))
		f.reply("점심은 걸렀다.")
		_, err = f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)

		third := f.ended(user("저녁은 든든하게 먹었어"))
		f.reply("저녁은 든든하게 먹었다.")
		_, err = f.service.Draft(t.Context(), third.target(f.userID))
		require.NoError(t, err)

		got := f.mustDiary(third.dayID)
		assert.Equal(t, "지하철이 멈춰서 지각했다.\n\n점심은 걸렀다.\n\n저녁은 든든하게 먹었다.", got.draft)
		assert.Equal(t, "지하철이 멈춰서 지각했다.", got.body)
	})

	t.Run("보탤 말이 없으면 확인한 일기를 건드리지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오늘 도서관 갔어"))
		f.reply("오늘 도서관에 갔다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)
		confirmed := f.confirm(first.dayID, "도서관에서 하루를 보냈다.")

		second := f.ended(assistant("잘 자요."), user("응"))
		f.reply("")
		result, err := f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, diary.OutcomeNothing, result.Outcome)
		got := f.mustDiary(second.dayID)
		assert.Equal(t, store.DiaryConfirmed, got.row.Status, "까닭 없이 확인 필요로 되돌리지 않는다")
		assert.True(t, confirmed.UpdatedAt.Equal(got.row.UpdatedAt))
		assert.Equal(t, store.ProcessingDone, f.processingStatus(second))
	})

	t.Run("모델을 부르는 동안 사용자가 일기를 고치면 고친 글 뒤에 붙이고, 모델은 다시 부르지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오전에 회의가 길었어"))
		f.reply("오전에 회의가 길었다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)

		second := f.ended(user("오후엔 일찍 퇴근했어"))
		f.remember("오후엔 일찍 퇴근했다.")
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step {
			// 모델이 답을 만드는 사이에 사용자가 첫 초안을 고쳐 확인한다.
			f.confirm(first.dayID, "회의가 끝도 없이 길었다.")
			return fake.Reply(entryJSON(t, "오후엔 일찍 퇴근했다."))
		})

		result, err := f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, 2, result.Rounds)
		assert.Equal(t, 2, f.llm.Calls(), "첫 대화에 한 번, 둘째 대화에 한 번뿐이다")
		got := f.mustDiary(second.dayID)
		assert.Equal(t, "회의가 끝도 없이 길었다.\n\n오후엔 일찍 퇴근했다.", got.draft, "고치기 전의 글에 붙인 초안이 올라가면 고친 것이 사라진다")
		assert.Equal(t, "회의가 끝도 없이 길었다.", got.body)
	})
}

func TestDraftSkipsCrisisConversations(t *testing.T) {
	t.Parallel()

	t.Run("대응 단계 이상의 판정이 있었던 대화는 초안을 만들지 않고 진행 상태만 닫는다", func(t *testing.T) {
		t.Parallel()
		for _, stage := range []int16{2, 3} {
			f := newFixture(t)
			c := f.open(user("요즘 너무 힘들어"), assistant("무슨 일 있었어요?"), user("그냥 죽고 싶어"))
			f.gate(c, 0, 0)
			f.gate(c, 2, stage)
			f.end(c)

			result, err := f.service.Draft(t.Context(), c.target(f.userID))
			require.NoError(t, err)
			assert.Equal(t, diary.OutcomeNothing, result.Outcome)
			assert.Equal(t, 1, result.CrisisSkipped)
			assert.Zero(t, f.llm.Calls(), "그 대화의 말은 모델에 가지 않는다")
			_, exists := f.diary(c.dayID)
			assert.False(t, exists)
			assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
		}
	})

	t.Run("같은 날의 다른 대화는 평소대로 담고, 위기 대응이 있었던 대화의 말은 섞지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		crisis := f.open(user("다 끝내고 싶어"))
		f.gate(crisis, 0, 2)
		f.end(crisis)
		_, err := f.service.Draft(t.Context(), crisis.target(f.userID))
		require.NoError(t, err)

		later := f.ended(user("언니랑 통화하고 좀 나아졌어"))
		f.reply("언니랑 통화하고 좀 나아졌다.")
		result, err := f.service.Draft(t.Context(), later.target(f.userID))
		require.NoError(t, err)

		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Equal(t, diary.ModeFirst, result.Mode)
		assert.NotContains(t, f.sentText(0), "끝내고")
		assert.Equal(t, "언니랑 통화하고 좀 나아졌다.", f.mustDiary(later.dayID).draft)
	})

	t.Run("확인 단계에 머문 대화는 평소대로 초안을 만든다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("다 내려놓고 쉬고 싶어"))
		f.gate(c, 0, 1)
		f.end(c)
		f.reply("다 내려놓고 쉬고 싶었다.")

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Zero(t, result.CrisisSkipped)
	})
}

func TestDraftIsIdempotent(t *testing.T) {
	t.Parallel()

	t.Run("같은 작업이 두 번 돌아도 글이 두 번 붙지 않고 모델도 다시 부르지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오늘 이사했어"))
		f.reply("오늘 이사했다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)
		f.confirm(first.dayID, "드디어 이사했다.")

		second := f.ended(user("짐 정리는 반도 못 했어"))
		f.reply("짐 정리는 반도 못 했다.")
		_, err = f.service.Draft(t.Context(), second.target(f.userID))
		require.NoError(t, err)
		before := f.mustDiary(second.dayID)

		f.clock.Advance(time.Hour)
		for range 2 {
			result, err := f.service.Draft(t.Context(), second.target(f.userID))
			require.NoError(t, err)
			assert.Equal(t, diary.OutcomeNothing, result.Outcome)
		}

		after := f.mustDiary(second.dayID)
		assert.Equal(t, 2, f.llm.Calls())
		assert.Equal(t, "드디어 이사했다.\n\n짐 정리는 반도 못 했다.", after.draft)
		assert.Equal(t, before.row.DraftEnc, after.row.DraftEnc, "다시 돈 작업은 일기를 건드리지 않는다")
		assert.True(t, before.row.UpdatedAt.Equal(after.row.UpdatedAt))
	})

	t.Run("같은 날의 작업 둘이 동시에 돌아도 한쪽만 붙인다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("아침 운동 다녀왔어"))
		f.reply("아침 운동을 다녀왔다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)

		second := f.ended(user("낮잠을 길게 잤어"))
		f.remember("낮잠을 길게 잤다.")
		// 둘 다 같은 것을 읽고 모델을 부른 뒤에야 저장하러 간다.
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step {
			return fake.Step{Text: entryJSON(t, "낮잠을 길게 잤다."), Latency: 300 * time.Millisecond}
		})

		var wg sync.WaitGroup
		results := make([]diary.Result, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = f.service.Draft(t.Context(), second.target(f.userID))
			}()
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		outcomes := []diary.Outcome{results[0].Outcome, results[1].Outcome}
		assert.ElementsMatch(t, []diary.Outcome{diary.OutcomeDrafted, diary.OutcomeNothing}, outcomes)
		got := f.mustDiary(second.dayID)
		assert.Equal(t, "아침 운동을 다녀왔다.\n\n낮잠을 길게 잤다.", got.draft)
		assert.Equal(t, 1, strings.Count(got.draft, "낮잠"))
	})
}

func TestDraftStopsWhenRecordsAreGone(t *testing.T) {
	t.Parallel()

	t.Run("하루를 지웠으면 그만둔다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘 일은 지우고 싶어"))
		_, err := f.store.Queries().DeleteDay(t.Context(), db.DeleteDayParams{ID: c.dayID, UserID: f.userID})
		require.NoError(t, err)

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeGone, result.Outcome)
		assert.Zero(t, f.llm.Calls())
	})

	t.Run("모델을 부르는 동안 하루를 지우면 초안을 되살리지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("방금 한 말은 없던 걸로 해줘"))
		f.remember("없던 걸로 하고 싶은 말을 했다.")
		f.llm.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
			// 가짜 모델은 Draft를 부른 고루틴에서 이 함수를 부른다. 여기서 시험을 멈춰도 된다.
			_, err := f.store.Queries().DeleteDay(ctx, db.DeleteDayParams{ID: c.dayID, UserID: f.userID})
			require.NoError(t, err)
			return fake.Reply(entryJSON(t, "없던 걸로 하고 싶은 말을 했다."))
		})

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeGone, result.Outcome)
		var diaries int
		require.NoError(t, f.pool.QueryRow(t.Context(), `SELECT count(*) FROM diaries WHERE user_id = $1`, f.userID).Scan(&diaries))
		assert.Zero(t, diaries)
	})

	t.Run("계정의 데이터 키가 없으면 그만두고 다시 시도하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("계정을 지우기 전에 한 말"))
		_, err := f.pool.Exec(t.Context(), `DELETE FROM user_keys WHERE user_id = $1`, f.userID)
		require.NoError(t, err)

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeGone, result.Outcome)

		result, err = f.service.GiveUp(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeGone, result.Outcome)
	})
}

func TestDraftDecryptFailures(t *testing.T) {
	t.Parallel()

	corrupt := func(t *testing.T, f *fixture, table, column string, id any) {
		t.Helper()
		// 첫 바이트(형식 표시)는 두고 뒤를 뒤집는다. 인증에 실패해 열리지 않는 암호문이 된다.
		_, err := f.pool.Exec(t.Context(),
			`UPDATE `+table+` SET `+column+` = substring(`+column+` from 1 for 13) || '\xdeadbeefdeadbeefdeadbeefdeadbeef'::bytea WHERE id = $1`, id)
		require.NoError(t, err)
	}

	t.Run("발화 하나가 열리지 않으면 그 말만 빼고 쓰고, 어느 발화인지 식별자로만 남긴다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘 면접 봤어"), user("이 말은 열리지 않는다"), user("결과는 다음 주에 나온대"))
		corrupt(t, f, "utterances", "text_enc", c.utteranceIDs[1])
		f.reply("오늘 면접을 봤다. 결과는 다음 주에 나온다고 한다.")

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
		assert.Equal(t, 1, result.UnreadableUtterances)
		assert.Equal(t, 2, result.UserUtterances)
		assert.Equal(t, "방식: 처음 쓰기\n\n사용자가 한 말:\n1. 오늘 면접 봤어\n2. 결과는 다음 주에 나온대", f.sentText(0))
		assert.Contains(t, f.logs.String(), c.utteranceIDs[1].String())
		f.assertLogsClean()
	})

	t.Run("사용자의 말이 하나도 열리지 않으면 할 말이 없던 날로 넘기지 않고 실패한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(assistant("오늘은 어땠어요?"), user("첫 번째 말"), user("두 번째 말"))
		corrupt(t, f, "utterances", "text_enc", c.utteranceIDs[1])
		corrupt(t, f, "utterances", "text_enc", c.utteranceIDs[2])

		_, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.ErrorIs(t, err, diary.ErrUnreadable)
		assert.True(t, diary.Permanent(err), "다시 읽어도 열리지 않는다")
		assert.Zero(t, f.llm.Calls())
		_, exists := f.diary(c.dayID)
		assert.False(t, exists)
		assert.Equal(t, store.ProcessingPending, f.processingStatus(c), "실패한 실행은 아무것도 바꾸지 않는다")
		assert.NotContains(t, err.Error(), "번째")
	})

	t.Run("이미 있는 글이 열리지 않으면 덮어쓰지 않고 실패한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오전엔 병원에 다녀왔어"))
		f.reply("오전엔 병원에 다녀왔다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)
		confirmed := f.confirm(first.dayID, "병원에 다녀온 날.")
		corrupt(t, f, "diaries", "body_enc", confirmed.ID)
		broken, err := f.store.Queries().GetDiaryByDayID(t.Context(), db.GetDiaryByDayIDParams{DayID: first.dayID, UserID: f.userID})
		require.NoError(t, err)

		second := f.ended(user("오후엔 푹 잤어"))
		_, err = f.service.Draft(t.Context(), second.target(f.userID))
		require.ErrorIs(t, err, diary.ErrUnreadable)
		assert.True(t, diary.Permanent(err))
		assert.Equal(t, 1, f.llm.Calls(), "붙일 자리가 없으면 모델을 부르지 않는다")

		after, err := f.store.Queries().GetDiaryByDayID(t.Context(), db.GetDiaryByDayIDParams{DayID: first.dayID, UserID: f.userID})
		require.NoError(t, err)
		assert.Equal(t, broken, after, "열리지 않는 글이라도 사용자의 글이다. 건드리지 않는다")
	})
}

func TestDraftOutputChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		said   string
		entry  string
		reason string
	}{
		{"사용자가 하지 않은 상태의 이름을 붙이면 버린다", "요즘 계속 기운이 없어", "요즘 우울증 증상처럼 계속 기운이 없다.", "labeling_term"},
		{"상담을 권하는 말을 붙이면 버린다", "오늘도 잠을 설쳤어", "오늘도 잠을 설쳤다. 전문가와 이야기해 봐야겠다.", "labeling_term"},
		{"한자가 섞이면 버린다", "오늘 도서관에서 공부했어", "오늘 도서관에서 工夫했다.", "foreign_script"},
		{"가나가 섞이면 버린다", "오늘 도서관에서 공부했어", "오늘 도서관에서 공부했다よ.", "foreign_script"},
		{"그림 문자를 넣으면 버린다", "오늘 기분 좋았어", "오늘 기분이 좋았다 😊", "symbol"},
		{"목록으로 쓰면 버린다", "장 보고 청소했어", "- 장을 봤다\n- 청소를 했다", "list_or_heading"},
		{"제목을 달면 버린다", "장 보고 청소했어", "# 오늘의 일기\n장을 보고 청소했다.", "list_or_heading"},
		{"너무 길면 버린다", "오늘은 별일 없었어", strings.Repeat("오늘은 별일이 없었다. ", 120), "too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			c := f.ended(user(tt.said))
			f.reply(tt.entry)

			_, err := f.service.Draft(t.Context(), c.target(f.userID))
			require.ErrorIs(t, err, diary.ErrRejected)
			var rejected *diary.RejectError
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, tt.reason, rejected.Reason)
			assert.False(t, diary.Permanent(err), "답은 부를 때마다 달라지므로 다시 시도한다")
			_, exists := f.diary(c.dayID)
			assert.False(t, exists, "걸린 글은 저장하지 않는다")
			assert.Equal(t, store.ProcessingPending, f.processingStatus(c))
		})
	}

	t.Run("사용자가 제 입으로 한 말이면 같은 낱말도 일기에 쓸 수 있다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("오늘 병원에서 상담받고 왔어 ㅠㅠ 기분은 좀 나아짐 ^^"))
		f.reply("오늘 병원에서 상담을 받고 왔다. 기분은 좀 나아졌다 ^^")

		result, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, diary.OutcomeDrafted, result.Outcome)
	})

	t.Run("앞뒤 공백과 겹친 빈 줄은 다듬어 저장한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.ended(user("아침엔 맑았고 밤엔 비가 왔어"))
		f.reply("\n 아침엔 맑았다.  \r\n\r\n\r\n\r\n밤엔 비가 왔다.\n\n")

		_, err := f.service.Draft(t.Context(), c.target(f.userID))
		require.NoError(t, err)
		assert.Equal(t, "아침엔 맑았다.\n\n밤엔 비가 왔다.", f.mustDiary(c.dayID).draft)
	})

	failures := []struct {
		name      string
		step      fake.Step
		is        error
		permanent bool
	}{
		{"JSON이 아닌 답", fake.Reply("오늘은 좋은 하루였다."), ai.ErrInvalidJSON, false},
		{"entry가 없는 JSON", fake.Reply(`{"diary": "오늘은 좋은 하루였다."}`), diary.ErrRejected, false},
		{"빈 답", fake.Reply("  "), ai.ErrEmpty, false},
		{"한도에 걸려 잘린 답", fake.Step{Text: `{"entry": "오늘은`, FinishReason: ai.FinishMaxTokens}, ai.ErrTruncated, false},
		{"안전 필터에 끊긴 답", fake.Step{Text: `{"entry": ""}`, FinishReason: ai.FinishSafety}, ai.ErrAbnormalFinish, false},
		{"후보 없음", fake.Fail(ai.NewError(ai.ErrNoCandidate, ai.Detail{Task: diary.PromptTask})), ai.ErrNoCandidate, false},
		{"공급자 오류", fake.Fail(ai.NewError(ai.ErrProvider, ai.Detail{Task: diary.PromptTask, Status: 503})), ai.ErrProvider, false},
		{"거절된 요청", fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: diary.PromptTask, Reason: "SAFETY"})), ai.ErrBlocked, true},
		{"틀린 요청", fake.Fail(ai.NewError(ai.ErrInvalidRequest, ai.Detail{Task: diary.PromptTask, Status: 400})), ai.ErrInvalidRequest, true},
	}
	for _, tt := range failures {
		t.Run("모델의 실패: "+tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			c := f.ended(user("오늘은 좋은 하루였어"))
			f.llm.Enqueue(tt.step)

			_, err := f.service.Draft(t.Context(), c.target(f.userID))
			require.ErrorIs(t, err, tt.is)
			assert.Equal(t, tt.permanent, diary.Permanent(err))
			_, exists := f.diary(c.dayID)
			assert.False(t, exists)
			assert.Equal(t, store.ProcessingPending, f.processingStatus(c))
		})
	}

	t.Run("정해진 시간 안에 답이 없으면 기다리지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		service := f.newService(func(o *diary.Options) { o.CallTimeout = 50 * time.Millisecond })
		c := f.ended(user("오늘은 좋은 하루였어"))
		f.llm.Enqueue(fake.Step{Text: entryJSON(t, "오늘은 좋은 하루였다."), Latency: 5 * time.Second})

		_, err := service.Draft(t.Context(), c.target(f.userID))
		require.ErrorIs(t, err, ai.ErrTimeout)
		assert.False(t, diary.Permanent(err))
		assert.Equal(t, 1, f.llm.Interrupted(), "기다리던 호출은 멈춘다")
	})
}

func TestNewService(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	registry, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	valid := diary.Options{
		Store: f.store, Sealers: f.sealers, LLM: f.llm, Prompts: registry, Clock: f.clock, Logger: f.logger,
	}

	tests := []struct {
		name   string
		mutate func(*diary.Options)
	}{
		{"저장소가 없다", func(o *diary.Options) { o.Store = nil }},
		{"Sealer를 받을 곳이 없다", func(o *diary.Options) { o.Sealers = nil }},
		{"모델이 없다", func(o *diary.Options) { o.LLM = nil }},
		{"지시문이 없다", func(o *diary.Options) { o.Prompts = nil }},
		{"시계가 없다", func(o *diary.Options) { o.Clock = nil }},
		{"로거가 없다", func(o *diary.Options) { o.Logger = nil }},
		{"한도가 음수다", func(o *diary.Options) { o.MaxEntryRunes = -1 }},
		{"다시 시도까지의 간격이 음수다", func(o *diary.Options) { o.RetryDelays = []time.Duration{time.Second, -time.Second} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := valid
			tt.mutate(&opts)
			_, err := diary.NewService(opts)
			require.Error(t, err)
		})
	}

	t.Run("실행 파일에 담긴 지시문으로 만들어진다", func(t *testing.T) {
		service, err := diary.NewService(valid)
		require.NoError(t, err)
		assert.Greater(t, service.MaxDuration(), diary.DefaultCallTimeout)
	})

	t.Run("다시 시도할 시각은 주입받은 시계에서 셈하고, 정해 둔 간격을 다 쓰면 마지막 간격을 되풀이한다", func(t *testing.T) {
		opts := valid
		opts.RetryDelays = []time.Duration{time.Second, 3 * time.Second}
		service, err := diary.NewService(opts)
		require.NoError(t, err)
		now := f.clock.Now()
		for attempt, want := range map[int]time.Duration{0: time.Second, 1: time.Second, 2: 3 * time.Second, 9: 3 * time.Second} {
			assert.Equal(t, now.Add(want), service.NextRetry(attempt), "%d번째 시도 뒤", attempt)
		}

		service, err = diary.NewService(valid)
		require.NoError(t, err)
		assert.Equal(t, now.Add(diary.DefaultRetryDelays()[0]), service.NextRetry(1))
	})
}
