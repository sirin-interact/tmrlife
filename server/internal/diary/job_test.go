package diary_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 설정의 기본값과 같다. 처음 한 번에 다시 시도 세 번이다.
const maxAttempts = 4

// attempts는 큐가 작업을 차례로 집어 실행하는 것을 흉내 낸다. 시도 사이의 기다림만 건너뛴다.
type attempts struct {
	t      *testing.T
	worker *diary.Worker
	args   diary.DraftArgs
	number int
}

func newAttempts(t *testing.T, f *fixture, ended db.Conversation) *attempts {
	t.Helper()
	worker, err := diary.NewWorker(f.service, f.logger)
	require.NoError(t, err)
	return &attempts{t: t, worker: worker, args: diary.ArgsFor(ended)}
}

// next는 다음 시도를 돌린다. 오류를 돌려주면 큐는 시도가 남아 있는 한 다시 집는다. nil이면 작업은 끝난 것이다.
func (a *attempts) next() error {
	a.t.Helper()
	a.number++
	require.LessOrEqual(a.t, a.number, maxAttempts, "큐는 정해진 횟수를 넘겨 시도하지 않는다")
	return a.worker.WorkAttempt(a.t.Context(), a.args, a.number, maxAttempts)
}

func providerDown() fake.Step {
	return fake.Fail(ai.NewError(ai.ErrProvider, ai.Detail{Task: diary.PromptTask, Model: fakeModel, Status: 503}))
}

func TestWorker(t *testing.T) {
	t.Parallel()

	t.Run("초안을 만들면 작업이 끝난다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘 엄마랑 시장 다녀왔어"))
		ended := f.end(c)
		f.reply("오늘 엄마랑 시장에 다녀왔다.")

		require.NoError(t, newAttempts(t, f, ended).next())

		assert.Equal(t, "오늘 엄마랑 시장에 다녀왔다.", f.mustDiary(c.dayID).draft)
		assert.Contains(t, f.logs.String(), `"msg":"diary draft job finished"`)
		assert.Contains(t, f.logs.String(), c.id.String(), "어느 대화의 작업인지는 식별자로 남긴다")
		f.assertLogsClean()
	})

	t.Run("실패하면 오류를 돌려줘 큐가 다시 시도하게 하고, 시도를 다 쓰면 빈 초안을 남기고 끝난다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘은 하루 종일 누워 있었어"))
		ended := f.end(c)
		f.llm.Enqueue(providerDown(), providerDown(), providerDown(), providerDown())
		job := newAttempts(t, f, ended)

		for attempt := 1; attempt < maxAttempts; attempt++ {
			require.ErrorIs(t, job.next(), ai.ErrProvider, "%d번째 시도의 실패는 큐에 알린다", attempt)
			_, exists := f.diary(c.dayID)
			assert.False(t, exists, "시도가 남아 있는 동안에는 빈 초안을 만들지 않는다")
			assert.Equal(t, store.ProcessingPending, f.processingStatus(c))
		}
		require.NoError(t, job.next(), "포기까지 마친 작업은 할 일을 다 한 것이다")

		assert.Equal(t, maxAttempts, f.llm.Calls())
		got := f.mustDiary(c.dayID)
		assert.Equal(t, store.DiaryDraft, got.row.Status)
		assert.NotNil(t, got.row.DraftEnc, "빈 글도 잠가서 저장한다")
		assert.Empty(t, got.draft, "사용자가 직접 쓸 수 있는 빈 초안이다")
		assert.Equal(t, store.ProcessingFailed, f.processingStatus(c))
		assert.Contains(t, f.logs.String(), `"failure":"ai_provider"`)
		f.assertLogsClean()
	})

	t.Run("다시 시도해서 되면 그 초안을 저장한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("퇴근하고 수영 갔어"))
		ended := f.end(c)
		f.llm.Enqueue(providerDown(), fake.Step{Text: `{"entry": "퇴근하고`, FinishReason: ai.FinishMaxTokens})
		f.reply("퇴근하고 수영을 갔다.")
		job := newAttempts(t, f, ended)

		require.ErrorIs(t, job.next(), ai.ErrProvider)
		require.ErrorIs(t, job.next(), ai.ErrTruncated)
		require.NoError(t, job.next())

		assert.Equal(t, "퇴근하고 수영을 갔다.", f.mustDiary(c.dayID).draft)
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
	})

	t.Run("출력 검사에 걸린 답은 버리고 다시 시도한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("요즘 잠을 통 못 자"))
		ended := f.end(c)
		f.reply("요즘 불면증 때문에 잠을 통 못 잔다.")
		f.reply("요즘 잠을 통 못 잔다.")
		job := newAttempts(t, f, ended)

		require.ErrorIs(t, job.next(), diary.ErrRejected)
		_, exists := f.diary(c.dayID)
		assert.False(t, exists)
		require.NoError(t, job.next())

		assert.Equal(t, "요즘 잠을 통 못 잔다.", f.mustDiary(c.dayID).draft)
		assert.Contains(t, f.logs.String(), `"failure":"rejected_labeling_term"`)
		f.assertLogsClean()
	})

	t.Run("다시 해도 같은 결과가 나올 실패는 기다리지 않고 바로 빈 초안을 남긴다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘 있었던 일을 말했어"))
		ended := f.end(c)
		f.llm.Enqueue(fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: diary.PromptTask, Reason: "PROHIBITED_CONTENT"})))

		require.NoError(t, newAttempts(t, f, ended).next())

		assert.Equal(t, 1, f.llm.Calls())
		assert.Empty(t, f.mustDiary(c.dayID).draft)
		assert.Equal(t, store.ProcessingFailed, f.processingStatus(c))
	})

	t.Run("이미 글이 있는 날에는 포기해도 그 글을 빈 초안으로 덮지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		first := f.ended(user("오전에는 빨래를 했어"))
		f.reply("오전에는 빨래를 했다.")
		_, err := f.service.Draft(t.Context(), first.target(f.userID))
		require.NoError(t, err)
		confirmed := f.confirm(first.dayID, "빨래를 하고 나니 개운했다.")

		second := f.open(user("오후에는 낮잠을 잤어"))
		ended := f.end(second)
		f.llm.Enqueue(fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: diary.PromptTask})))

		require.NoError(t, newAttempts(t, f, ended).next())

		got := f.mustDiary(first.dayID)
		assert.Equal(t, store.DiaryConfirmed, got.row.Status)
		assert.Equal(t, "빨래를 하고 나니 개운했다.", got.body)
		assert.Equal(t, confirmed.BodyEnc, got.row.BodyEnc)
		assert.True(t, confirmed.UpdatedAt.Equal(got.row.UpdatedAt))
		assert.Equal(t, store.ProcessingFailed, f.processingStatus(second))

		// 포기한 대화는 다음 대화의 초안에 뒤늦게 끼어들지 않는다. 그사이 사용자가 직접 썼을 수 있다.
		third := f.ended(user("저녁에는 산책했어"))
		f.reply("저녁에는 산책을 했다.")
		_, err = f.service.Draft(t.Context(), third.target(f.userID))
		require.NoError(t, err)
		assert.NotContains(t, f.sentText(f.llm.Calls()-1), "낮잠")
	})

	t.Run("위기 대응이 있었던 대화의 작업은 모델을 부르지 않고 끝난다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("약을 모아뒀어"))
		f.gate(c, 0, 3)
		ended := f.end(c)

		require.NoError(t, newAttempts(t, f, ended).next())

		assert.Zero(t, f.llm.Calls())
		_, exists := f.diary(c.dayID)
		assert.False(t, exists)
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))
		f.assertLogsClean()
	})

	t.Run("작업자가 내려가는 중에 끊긴 작업은 시도 횟수를 쓰지 않고 미룬다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘은 늦게까지 일했어"))
		ended := f.end(c)
		worker, err := diary.NewWorker(f.service, f.logger)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step {
			cancel()
			return fake.Step{Text: entryJSON(t, "오늘은 늦게까지 일했다."), Latency: 5 * time.Second}
		})

		err = worker.WorkAttempt(ctx, diary.ArgsFor(ended), maxAttempts, maxAttempts)

		var snooze *river.JobSnoozeError
		require.ErrorAs(t, err, &snooze, "마지막 시도가 끊겼다고 빈 초안으로 넘어가지 않는다")
		_, exists := f.diary(c.dayID)
		assert.False(t, exists)
		assert.Equal(t, store.ProcessingPending, f.processingStatus(c))
	})

	t.Run("작업의 제한 시간은 모델을 기다리는 시간보다 길다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		worker, err := diary.NewWorker(f.service, f.logger)
		require.NoError(t, err)
		assert.Greater(t, worker.Timeout(nil), diary.DefaultCallTimeout)
	})

	t.Run("서비스나 로거 없이는 만들 수 없다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		_, err := diary.NewWorker(nil, f.logger)
		require.Error(t, err)
		_, err = diary.NewWorker(f.service, nil)
		require.Error(t, err)
	})
}

func TestEnqueuer(t *testing.T) {
	t.Parallel()

	// endAndEnqueue는 서버가 하는 대로 대화를 끝내는 트랜잭션 안에서 작업을 등록한다.
	endAndEnqueue := func(t *testing.T, f *fixture, enqueuer *diary.Enqueuer, c conversation, fail error) error {
		t.Helper()
		return f.store.InTxRaw(t.Context(), func(tx pgx.Tx, q *db.Queries) error {
			ended, err := q.EndConversation(t.Context(), db.EndConversationParams{
				Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
				ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
			})
			if err != nil {
				return err
			}
			if err := enqueuer.EnqueueTx(t.Context(), tx, diary.ArgsFor(ended)); err != nil {
				return err
			}
			return fail
		})
	}

	type queued struct {
		kind        string
		maxAttempts int
		args        map[string]any
	}
	jobs := func(t *testing.T, f *fixture) []queued {
		t.Helper()
		rows, err := f.pool.Query(t.Context(), `SELECT kind, max_attempts, args FROM river_job ORDER BY id`)
		require.NoError(t, err)
		defer rows.Close()
		var out []queued
		for rows.Next() {
			var (
				q   queued
				raw []byte
			)
			require.NoError(t, rows.Scan(&q.kind, &q.maxAttempts, &raw))
			require.NoError(t, json.Unmarshal(raw, &q.args))
			out = append(out, q)
		}
		require.NoError(t, rows.Err())
		return out
	}

	t.Run("대화를 끝내는 트랜잭션과 함께 등록되고, 인자에는 식별자만 남는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := diary.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)
		c := f.open(user("오늘도 수고했어"))

		require.NoError(t, endAndEnqueue(t, f, enqueuer, c, nil))

		got := jobs(t, f)
		require.Len(t, got, 1)
		assert.Equal(t, "diary_draft", got[0].kind)
		assert.Equal(t, maxAttempts, got[0].maxAttempts, "시도 횟수는 넣을 때 정해진다")
		assert.Equal(t, map[string]any{
			"user_id":         f.userID.String(),
			"day_id":          c.dayID.String(),
			"conversation_id": c.id.String(),
		}, got[0].args, "작업 큐의 테이블에는 평문으로 남는다. 글도 날짜도 넣지 않는다")
	})

	t.Run("트랜잭션을 되돌리면 대화도 끝나지 않고 작업도 없다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := diary.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)
		c := f.open(user("오늘도 수고했어"))

		require.ErrorIs(t, endAndEnqueue(t, f, enqueuer, c, assert.AnError), assert.AnError)

		assert.Empty(t, jobs(t, f))
		row, err := f.store.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: c.id, UserID: f.userID})
		require.NoError(t, err)
		assert.Equal(t, store.ConversationActive, row.Conversation.Status)
	})

	t.Run("빠진 값이 있으면 등록하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)

		_, err = diary.NewEnqueuer(nil, maxAttempts)
		require.Error(t, err)
		_, err = diary.NewEnqueuer(inserter, 0)
		require.Error(t, err)

		enqueuer, err := diary.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)
		require.Error(t, enqueuer.EnqueueTx(t.Context(), nil, diary.DraftArgs{UserID: f.userID, DayID: newID(t), ConversationID: newID(t)}))

		tx, err := f.pool.Begin(t.Context())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
		for _, args := range []diary.DraftArgs{
			{DayID: newID(t), ConversationID: newID(t)},
			{UserID: f.userID, ConversationID: newID(t)},
			{UserID: f.userID, DayID: newID(t)},
		} {
			require.Error(t, enqueuer.EnqueueTx(t.Context(), tx, args))
		}
	})
}

func TestQueueRunsDraftJob(t *testing.T) {
	t.Parallel()

	t.Run("모델이 계속 실패하면 큐가 정해진 횟수만큼 다시 시도한 뒤 빈 초안을 남기고 작업을 끝낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		// 큐는 다시 시도할 시각을 제 시계(실제 시각)로 본다. 이 시험만 실제 시계로 돌리고 간격을 짧게 잡는다.
		service := f.newService(func(o *diary.Options) {
			o.Clock = clock.Real{}
			o.RetryDelays = []time.Duration{50 * time.Millisecond}
		})
		worker, err := diary.NewWorker(service, f.logger)
		require.NoError(t, err)
		client, err := queue.NewWorkerClient(f.pool, f.logger, queue.WorkerOptions{
			SoftStopTimeout: 5 * time.Second,
			Register: func(workers *river.Workers) error {
				return river.AddWorkerSafely(workers, worker)
			},
		})
		require.NoError(t, err)
		events, cancelSub := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
		defer cancelSub()

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		require.NoError(t, client.Start(ctx))

		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := diary.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)

		c := f.open(user("오늘은 아무것도 하기 싫었어"))
		f.llm.Enqueue(providerDown(), providerDown(), providerDown(), providerDown())
		require.NoError(t, f.store.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
			ended, err := q.EndConversation(ctx, db.EndConversationParams{
				Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
				ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
			})
			if err != nil {
				return err
			}
			return enqueuer.EnqueueTx(ctx, tx, diary.ArgsFor(ended))
		}))

		failed := 0
	wait:
		for {
			select {
			case ev := <-events:
				if ev.Kind == river.EventKindJobFailed {
					failed++
					continue
				}
				assert.Equal(t, maxAttempts, ev.Job.Attempt)
				break wait
			case <-time.After(60 * time.Second):
				t.Fatal("초안 작업이 끝나지 않았다")
			}
		}

		assert.Equal(t, maxAttempts-1, failed, "마지막 시도는 실패로 남지 않고 포기 절차로 끝난다")
		assert.Equal(t, maxAttempts, f.llm.Calls())
		got := f.mustDiary(c.dayID)
		assert.Equal(t, store.DiaryDraft, got.row.Status)
		assert.Empty(t, got.draft)
		assert.Equal(t, store.ProcessingFailed, f.processingStatus(c))

		stop()
		select {
		case <-client.Stopped():
		case <-time.After(15 * time.Second):
			t.Fatal("작업자가 내려가지 않았다")
		}
		// 큐는 실패한 시도의 오류 문구를 제 로그와 작업 행에 남긴다. 그 행은 일기가 암호문으로 누워 있는
		// 바로 그 데이터베이스에 평문으로 남으므로, 정해 둔 이름만 적혀야 한다.
		f.assertLogsClean()
		var recorded string
		require.NoError(t, f.pool.QueryRow(t.Context(), `SELECT coalesce(array_to_string(errors, ' '), '') FROM river_job WHERE kind = 'diary_draft'`).Scan(&recorded))
		assert.Contains(t, recorded, "ai_provider", "어떤 실패였는지는 남아야 한다")
		assert.NotContains(t, recorded, "하기 싫었어")
	})

	t.Run("서버가 대화를 끝내며 넣은 작업을 작업자가 집어 초안을 만든다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		worker, err := diary.NewWorker(f.service, f.logger)
		require.NoError(t, err)
		client, err := queue.NewWorkerClient(f.pool, f.logger, queue.WorkerOptions{
			SoftStopTimeout: 5 * time.Second,
			Register: func(workers *river.Workers) error {
				return river.AddWorkerSafely(workers, worker)
			},
		})
		require.NoError(t, err)
		completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
		defer cancelSub()

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		require.NoError(t, client.Start(ctx))

		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := diary.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)

		c := f.open(assistant("오늘 하루는 어땠어요?"), user("오늘 발표 잘 끝났어"), assistant("고생하셨어요."), user("끝나고 팀원들이랑 밥 먹었어"))
		f.reply("오늘 발표가 잘 끝났다. 끝나고 팀원들이랑 밥을 먹었다.")
		var ended db.Conversation
		require.NoError(t, f.store.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
			var err error
			ended, err = q.EndConversation(ctx, db.EndConversationParams{
				Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
				ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
			})
			if err != nil {
				return err
			}
			return enqueuer.EnqueueTx(ctx, tx, diary.ArgsFor(ended))
		}))

		select {
		case ev := <-completed:
			assert.Equal(t, "diary_draft", ev.Job.Kind)
		case <-time.After(30 * time.Second):
			t.Fatal("초안 작업이 끝나지 않았다")
		}

		assert.Equal(t, "오늘 발표가 잘 끝났다. 끝나고 팀원들이랑 밥을 먹었다.", f.mustDiary(c.dayID).draft)
		assert.Equal(t, store.ProcessingDone, f.processingStatus(c))

		stop()
		select {
		case <-client.Stopped():
		case <-time.After(15 * time.Second):
			t.Fatal("작업자가 내려가지 않았다")
		}
		// 큐가 제 로그에 남기는 것까지 포함해서 본다.
		f.assertLogsClean()
	})
}
