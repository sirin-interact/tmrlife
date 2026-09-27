package analysis_test

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
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 설정의 기본값과 같다. 처음 한 번에 다시 시도 세 번이다.
const maxAttempts = 4

// attempts는 큐가 작업을 차례로 집어 실행하는 것을 흉내 낸다. 시도 사이의 기다림만 건너뛴다.
type attempts struct {
	t      *testing.T
	worker *analysis.Worker
	args   analysis.ExtractArgs
	number int
}

func newAttempts(t *testing.T, f *fixture, ended db.Conversation) *attempts {
	t.Helper()
	worker, err := analysis.NewWorker(f.service, f.logger)
	require.NoError(t, err)
	return &attempts{t: t, worker: worker, args: analysis.ArgsFor(ended)}
}

// next는 다음 시도를 돌린다. 오류를 돌려주면 큐는 시도가 남아 있는 한 다시 집는다. nil이면 작업은 끝난 것이다.
func (a *attempts) next() error {
	a.t.Helper()
	a.number++
	require.LessOrEqual(a.t, a.number, maxAttempts, "큐는 정해진 횟수를 넘겨 시도하지 않는다")
	return a.worker.WorkAttempt(a.t.Context(), a.args, a.number, maxAttempts)
}

func providerDown() fake.Step {
	return fake.Fail(ai.NewError(ai.ErrProvider, ai.Detail{Task: analysis.PromptTask, Model: fakeModel, Status: 503}))
}

func TestWorker(t *testing.T) {
	t.Parallel()

	t.Run("여덟 항목을 저장하면 작업이 끝난다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(assistant("어젯밤엔 잘 잤어요?"), user("새벽 네 시까지 뒤척였어"))
		ended := f.end(c)
		f.reply(map[signal.Item]judged{signal.Sleep: observed(1, "새벽 네 시까지 뒤척였어")})

		require.NoError(t, newAttempts(t, f, ended).next())

		rows := f.assertEightRows(c)
		assert.Equal(t, "새벽 네 시까지 뒤척였어", rows[signal.Sleep.String()].evidence)
		assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))
		assert.Contains(t, f.logs.String(), `"msg":"signal extraction job finished"`)
		assert.Contains(t, f.logs.String(), c.id.String(), "어느 대화의 작업인지는 식별자로 남긴다")
		assert.Contains(t, f.logs.String(), `"observed":1`, "몇 항목이 관찰됐는지는 숫자로 남긴다")
		f.assertLogsClean()
	})

	t.Run("실패하면 오류를 돌려줘 큐가 다시 시도하게 하고, 시도를 다 쓰면 행 없이 failed로 닫는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘은 하루 종일 누워 있었어"))
		ended := f.end(c)
		f.llm.Enqueue(providerDown(), providerDown(), providerDown(), providerDown())
		job := newAttempts(t, f, ended)

		for attempt := 1; attempt < maxAttempts; attempt++ {
			require.ErrorIs(t, job.next(), ai.ErrProvider, "%d번째 시도의 실패는 큐에 알린다", attempt)
			assert.Empty(t, f.signals(c), "시도가 남아 있는 동안에도 행을 반쯤 남기지 않는다")
			assert.Equal(t, store.AnalysisRunning, f.analysisStatus(c))
		}
		require.NoError(t, job.next(), "닫기까지 마친 작업은 할 일을 다 한 것이다")

		assert.Equal(t, maxAttempts, f.llm.Calls())
		assert.Empty(t, f.signals(c), "절반만 남은 행은 모든 숫자를 망긴다. 하나도 남기지 않는다")
		assert.Equal(t, store.AnalysisFailed, f.analysisStatus(c))
		assert.Contains(t, f.logs.String(), `"failure":"ai_provider"`)
		assert.Contains(t, f.logs.String(), `"give_up_outcome":"gave_up"`)
		f.assertLogsClean()
	})

	t.Run("다시 시도해서 되면 그 판단을 저장한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("입맛이 없어서 저녁을 건너뛰었어"))
		ended := f.end(c)
		f.llm.Enqueue(providerDown(), fake.Step{Text: `{"interest":`, FinishReason: ai.FinishMaxTokens})
		f.reply(map[signal.Item]judged{signal.Appetite: observed(1, "입맛이 없어서 저녁을 건너뛰었어")})
		job := newAttempts(t, f, ended)

		require.ErrorIs(t, job.next(), ai.ErrProvider)
		require.ErrorIs(t, job.next(), ai.ErrTruncated)
		require.NoError(t, job.next())

		rows := f.assertEightRows(c)
		assert.Equal(t, "observed", rows[signal.Appetite.String()].status)
		assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))
	})

	t.Run("정해진 꼴을 따르지 않은 답은 버리고 다시 시도한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("하루 종일 멍하니 있었어"))
		ended := f.end(c)
		f.llm.Enqueue(fake.Reply(`{"interest":{"status":"observed","explicitness":"direct","line":1,"evidence":"하루 종일 멍하니 있었어"}}`))
		f.reply(map[signal.Item]judged{signal.Concentration: observed(1, "하루 종일 멍하니 있었어")})
		job := newAttempts(t, f, ended)

		require.ErrorIs(t, job.next(), analysis.ErrRejected)
		assert.Empty(t, f.signals(c))
		require.NoError(t, job.next())

		rows := f.assertEightRows(c)
		assert.Equal(t, "observed", rows[signal.Concentration.String()].status)
		assert.Contains(t, f.logs.String(), `"failure":"rejected_missing_item"`)
		f.assertLogsClean()
	})

	t.Run("다시 해도 같은 결과가 나올 실패는 기다리지 않고 바로 닫는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘 있었던 일을 말했어"))
		ended := f.end(c)
		f.llm.Enqueue(fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: analysis.PromptTask, Reason: "PROHIBITED_CONTENT"})))

		require.NoError(t, newAttempts(t, f, ended).next())

		assert.Equal(t, 1, f.llm.Calls())
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisFailed, f.analysisStatus(c))
	})

	t.Run("발화가 열리지 않으면 다시 시도하지 않고 닫는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("어젯밤에 한숨도 못 잤어"))
		ended := f.end(c)
		f.corrupt(c.utteranceIDs[0])

		require.NoError(t, newAttempts(t, f, ended).next())

		assert.Zero(t, f.llm.Calls(), "열리지 않는 글로는 모델을 부르지 않는다")
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisFailed, f.analysisStatus(c))
		assert.Contains(t, f.logs.String(), `"failure":"unreadable"`)
		f.assertLogsClean()
	})

	t.Run("작업자가 내려가는 중에 끊긴 작업은 시도 횟수를 쓰지 않고 미룬다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘은 늦게까지 일했어"))
		ended := f.end(c)
		worker, err := analysis.NewWorker(f.service, f.logger)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step {
			cancel()
			return fake.Step{Text: answer(t, nil), Latency: 5 * time.Second}
		})

		err = worker.WorkAttempt(ctx, analysis.ArgsFor(ended), maxAttempts, maxAttempts)

		var snooze *river.JobSnoozeError
		require.ErrorAs(t, err, &snooze, "마지막 시도가 끊겼다고 분석을 실패로 닫지 않는다")
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisRunning, f.analysisStatus(c))
	})

	t.Run("작업의 제한 시간은 모델을 기다리는 시간보다 길다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		worker, err := analysis.NewWorker(f.service, f.logger)
		require.NoError(t, err)
		assert.Greater(t, worker.Timeout(nil), analysis.DefaultCallTimeout)
	})

	t.Run("서비스나 로거 없이는 만들 수 없다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		_, err := analysis.NewWorker(nil, f.logger)
		require.Error(t, err)
		_, err = analysis.NewWorker(f.service, nil)
		require.Error(t, err)
	})
}

func TestEnqueuer(t *testing.T) {
	t.Parallel()

	// endAndEnqueue는 서버가 하는 대로 대화를 끝내는 트랜잭션 안에서 작업을 등록한다.
	endAndEnqueue := func(t *testing.T, f *fixture, enqueuer *analysis.Enqueuer, c conversation, fail error) error {
		t.Helper()
		return f.store.InTxRaw(t.Context(), func(tx pgx.Tx, q *db.Queries) error {
			ended, err := q.EndConversation(t.Context(), db.EndConversationParams{
				Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
				ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
			})
			if err != nil {
				return err
			}
			if err := enqueuer.EnqueueTx(t.Context(), tx, q, analysis.ArgsFor(ended)); err != nil {
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

	newEnqueuer := func(t *testing.T, f *fixture) *analysis.Enqueuer {
		t.Helper()
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := analysis.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)
		return enqueuer
	}

	t.Run("대화를 끝내는 트랜잭션과 함께 등록되고, 인자에는 식별자만 남는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘도 수고했어"))

		require.NoError(t, endAndEnqueue(t, f, newEnqueuer(t, f), c, nil))

		got := jobs(t, f)
		require.Len(t, got, 1)
		assert.Equal(t, "signal_extract", got[0].kind)
		assert.Equal(t, maxAttempts, got[0].maxAttempts, "시도 횟수는 넣을 때 정해진다")
		assert.Equal(t, map[string]any{
			"user_id":         f.userID.String(),
			"conversation_id": c.id.String(),
		}, got[0].args, "작업 큐의 테이블에는 평문으로 남는다. 글도 날짜도 넣지 않는다")
		assert.Equal(t, store.AnalysisPending, f.analysisStatus(c), "작업이 있는 대화는 pending에서 시작한다")
	})

	t.Run("트랜잭션을 되돌리면 상태도 그대로이고 작업도 없다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("오늘도 수고했어"))

		require.ErrorIs(t, endAndEnqueue(t, f, newEnqueuer(t, f), c, assert.AnError), assert.AnError)

		assert.Empty(t, jobs(t, f))
		assert.Equal(t, store.AnalysisNone, f.analysisStatus(c))
		row, err := f.store.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: c.id, UserID: f.userID})
		require.NoError(t, err)
		assert.Equal(t, store.ConversationActive, row.Conversation.Status)
	})

	t.Run("빠진 값이 있으면 등록하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)

		_, err = analysis.NewEnqueuer(nil, maxAttempts)
		require.Error(t, err)
		_, err = analysis.NewEnqueuer(inserter, 0)
		require.Error(t, err)

		enqueuer := newEnqueuer(t, f)
		require.Error(t, enqueuer.EnqueueTx(t.Context(), nil, f.store.Queries(),
			analysis.ExtractArgs{UserID: f.userID, ConversationID: newID(t)}))

		require.NoError(t, f.store.InTxRaw(t.Context(), func(tx pgx.Tx, q *db.Queries) error {
			require.Error(t, enqueuer.EnqueueTx(t.Context(), tx, nil, analysis.ExtractArgs{UserID: f.userID, ConversationID: newID(t)}))
			for _, args := range []analysis.ExtractArgs{
				{ConversationID: newID(t)},
				{UserID: f.userID},
			} {
				require.Error(t, enqueuer.EnqueueTx(t.Context(), tx, q, args))
			}
			return nil
		}))
	})

	t.Run("끝나지 않은 대화에는 등록하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.open(user("아직 이야기 중이야"))
		enqueuer := newEnqueuer(t, f)

		err := f.store.InTxRaw(t.Context(), func(tx pgx.Tx, q *db.Queries) error {
			return enqueuer.EnqueueTx(t.Context(), tx, q, analysis.ExtractArgs{UserID: f.userID, ConversationID: c.id})
		})

		require.ErrorIs(t, err, store.ErrNotFound, "열린 대화에는 분석 상태를 적을 곳이 없다")
		assert.Empty(t, jobs(t, f))
	})
}

func TestQueueRunsExtractJob(t *testing.T) {
	t.Parallel()

	// startWorker는 실제 큐를 띄운다. 큐는 다시 시도할 시각을 제 시계(실제 시각)로 보므로 이 시험만 실제 시계로 돌린다.
	startWorker := func(t *testing.T, f *fixture) (*river.Client[pgx.Tx], <-chan *river.Event) {
		t.Helper()
		service := f.newService(func(o *analysis.Options) {
			o.Clock = clock.Real{}
			o.RetryDelays = []time.Duration{50 * time.Millisecond}
		})
		worker, err := analysis.NewWorker(service, f.logger)
		require.NoError(t, err)
		client, err := queue.NewWorkerClient(f.pool, f.logger, queue.WorkerOptions{
			SoftStopTimeout: 5 * time.Second,
			Register: func(workers *river.Workers) error {
				return river.AddWorkerSafely(workers, worker)
			},
		})
		require.NoError(t, err)
		events, cancelSub := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
		t.Cleanup(cancelSub)
		require.NoError(t, client.Start(t.Context()))
		return client, events
	}

	endAndEnqueue := func(t *testing.T, f *fixture, c conversation) {
		t.Helper()
		inserter, err := queue.NewInsertClient(f.pool, f.logger)
		require.NoError(t, err)
		enqueuer, err := analysis.NewEnqueuer(inserter, maxAttempts)
		require.NoError(t, err)
		require.NoError(t, f.store.InTxRaw(t.Context(), func(tx pgx.Tx, q *db.Queries) error {
			ended, err := q.EndConversation(t.Context(), db.EndConversationParams{
				Now: f.clock.Advance(time.Minute), EndReason: store.EndReasonUser,
				ProcessingStatus: store.ProcessingPending, ID: c.id, UserID: f.userID,
			})
			if err != nil {
				return err
			}
			return enqueuer.EnqueueTx(t.Context(), tx, q, analysis.ArgsFor(ended))
		}))
	}

	stop := func(t *testing.T, client *river.Client[pgx.Tx]) {
		t.Helper()
		if err := client.Stop(t.Context()); err != nil {
			t.Logf("작업자를 내리는 중 오류: %v", err)
		}
	}

	t.Run("서버가 대화를 끝내며 넣은 작업을 작업자가 집어 신호를 저장한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		client, events := startWorker(t, f)

		c := f.open(
			assistant("오늘 하루는 어땠어요?"),
			user("퇴근하고 바로 누웠어. 기운이 하나도 없더라"),
			assistant("많이 지치셨네요."),
			user("밥은 챙겨 먹었어"),
		)
		f.reply(map[signal.Item]judged{
			signal.Fatigue:  observed(1, "기운이 하나도 없더라"),
			signal.Appetite: notObserved(2, "밥은 챙겨 먹었어"),
		})
		endAndEnqueue(t, f, c)

		select {
		case ev := <-events:
			require.Equal(t, river.EventKindJobCompleted, ev.Kind)
			assert.Equal(t, "signal_extract", ev.Job.Kind)
		case <-time.After(30 * time.Second):
			t.Fatal("신호 추출 작업이 끝나지 않았다")
		}

		rows := f.assertEightRows(c)
		assert.Equal(t, "observed", rows[signal.Fatigue.String()].status)
		assert.Equal(t, "not_observed", rows[signal.Appetite.String()].status)
		assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))

		stop(t, client)
		// 큐가 제 로그에 남기는 것까지 포함해서 본다.
		f.assertLogsClean()
	})

	t.Run("모델이 계속 실패하면 큐가 정해진 횟수만큼 다시 시도한 뒤 행 없이 끝낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		client, events := startWorker(t, f)

		c := f.open(user("오늘은 아무것도 하기 싫었어"))
		f.llm.Enqueue(providerDown(), providerDown(), providerDown(), providerDown())
		endAndEnqueue(t, f, c)

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
				t.Fatal("신호 추출 작업이 끝나지 않았다")
			}
		}

		assert.Equal(t, maxAttempts-1, failed, "마지막 시도는 실패로 남지 않고 닫는 절차로 끝난다")
		assert.Equal(t, maxAttempts, f.llm.Calls())
		assert.Empty(t, f.signals(c))
		assert.Equal(t, store.AnalysisFailed, f.analysisStatus(c))

		stop(t, client)
		// 큐는 실패한 시도의 오류 문구를 제 로그와 작업 행에 남긴다. 그 행은 발화가 암호문으로 누워 있는
		// 바로 그 데이터베이스에 평문으로 남으므로, 정해 둔 이름만 적혀야 한다.
		f.assertLogsClean()
		var recorded string
		require.NoError(t, f.pool.QueryRow(t.Context(),
			`SELECT coalesce(array_to_string(errors, ' '), '') FROM river_job WHERE kind = 'signal_extract'`).Scan(&recorded))
		assert.Contains(t, recorded, "ai_provider", "어떤 실패였는지는 남아야 한다")
		assert.NotContains(t, recorded, "하기 싫었어")
	})
}
