package engine_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// slowModel은 답이 아주 늦는 대화 모델이다. 실제 모델의 꼬리 지연을 과장한 것이다.
const slowModel = 10 * time.Second

// result는 다른 고루틴에서 돌린 턴의 결과다. 단언은 시험 고루틴에서만 한다.
type result struct {
	turn engine.Turn
	err  error
}

func TestGateDecisionIsPersistedBeforeAnyText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	// 사건이 나가는 바로 그 순간의 DB를 들여다본다. 판정이 아직 없는데 말이 나가면 여기서 걸린다.
	var checked int
	f.sink.hook = func(e engine.Event) error {
		text, ok := e.(engine.AIText)
		if !ok || text.Seq == 0 {
			// 첫 안부는 사용자의 발화에 대한 답이 아니라 판정을 낄 자리가 없다.
			return nil
		}
		checked++
		_, found := f.gateEventForSeq(s.ConversationID(), text.Seq-1)
		assert.True(t, found, "관문의 판정이 저장되기 전에 말이 나갔다")
		return nil
	}

	f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)
	f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)
	f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
	assert.Equal(t, 3, checked, "세 턴의 말을 모두 확인해야 한다")
}

func TestBufferedReplyNeverLeavesAtStageRespond(t *testing.T) {
	t.Parallel()

	t.Run("버퍼의 답이 먼저 다 만들어져 있어도 나가지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		// 규칙 겹은 놓치고 AI 판별만 잡는 말이다. 그래서 대화 모델이 실제로 돌고, 먼저 끝난다.
		f.replies(fake.Reply(replyOrdinary))
		f.judge.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
			// 대화 모델이 답을 다 내놓은 뒤에 판별이 끝나게 한다.
			time.Sleep(20 * time.Millisecond)
			return fake.Reply(`{"stage":2,"evidence":""}`)
		})

		turn := f.say(s, sayTired)
		assert.Equal(t, crisis.StageRespond, turn.Stage)
		assert.Equal(t, 1, f.talk.Calls(), "대화 모델은 불렸다")
		assert.NotContains(t, f.sink.texts(), replyOrdinary, "버퍼의 답이 나갔다")

		text := f.sink.lastText(t)
		assert.Equal(t, phrases.CrisisRespond, text.Phrase)
		stored := f.storedUtterances(s.ConversationID())
		last := stored[len(stored)-1]
		assert.Equal(t, store.OriginFixed, last.Origin)
		assert.NotEqual(t, replyOrdinary, last.Text, "버려진 답이 대화 기록에 남았다")
	})

	t.Run("느린 대화 모델이 고정 문구를 늦추지 못한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.replies(fake.Step{Text: replyOrdinary, Latency: slowModel})
		f.judges(crisis.StageRespond, "")

		// 시계를 읽지 않고 잰다. 모델이 답할 시간이 되기 한참 전에 턴이 끝나야 한다.
		messageID := newID(t)
		done := make(chan result, 1)
		go func() {
			turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: messageID, Text: sayTired})
			done <- result{turn: turn, err: err}
		}()
		select {
		case got := <-done:
			require.NoError(t, got.err)
			assert.Equal(t, crisis.StageRespond, got.turn.Stage)
		case <-time.After(slowModel / 2):
			require.Fail(t, "미리 써 둔 말이 대화 모델을 기다렸다")
		}
		assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)
		assert.Equal(t, 1, f.talk.Interrupted(), "버린 호출은 취소되어야 한다")
	})

	t.Run("실패한 대화 모델이 고정 문구를 막지 못한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.replies(fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: "conversation"})))
		f.judges(crisis.StageUrgent, "")

		turn := f.say(s, sayTired)
		assert.Equal(t, crisis.StageUrgent, turn.Stage)
		assert.Equal(t, phrases.CrisisUrgent, f.sink.lastText(t).Phrase)
	})
}

func TestResourcesAreEmittedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
	assert.Equal(t, 1, f.sink.resources())

	f.turn(s, sayUrgent, crisis.StageUrgent, sayUrgent, "")
	assert.Equal(t, 1, f.sink.resources(), "같은 연결에서 자원을 두 번 고정하지 않는다")

	// 연결이 끊겼다 이어져도 자원 고정은 남는다.
	f.sink.reset()
	resumed := f.start()
	assert.True(t, resumed.ResourcesPinned())

	events := f.sink.all()
	require.Len(t, events, 2, "이어가는 대화는 여는 사건과 자원만 내보낸다")
	ready, ok := events[0].(engine.Ready)
	require.True(t, ok)
	assert.True(t, ready.ResourcesPinned)
	_, ok = events[1].(engine.Resources)
	assert.True(t, ok, "이어붙은 연결에서는 자원을 한 번 다시 내보낸다")
	assert.Equal(t, 1, f.sink.resources())
}

// 이 시험은 나란히 돌리지 않는다. 고루틴을 세는 동안 다른 시험이 제 접속 풀을 띄우면 그것까지 샌 것으로 읽힌다.
func TestCancellationLeavesNoGoroutines(t *testing.T) {
	f := newFixture(t)

	s := f.start()
	// 접속 풀과 시험 기반이 띄운 고루틴은 이 시점에 이미 떠 있다. 여기서부터 늘어난 것만 본다.
	f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)
	defer goleak.VerifyNone(t,
		goleak.IgnoreCurrent(),
		// 접속 풀은 필요할 때 제 고루틴을 띄운다. 엔진이 남긴 고루틴만 보려고 그것들은 뺀다.
		goleak.IgnoreAnyFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
		goleak.IgnoreAnyFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).triggerHealthCheck.func1"),
	)

	f.replies(fake.Step{Text: replyOrdinary, Latency: slowModel})
	f.judge.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
		<-ctx.Done()
		return fake.Fail(ai.ContextError(ctx.Err(), ai.Detail{Task: "gate_classifier"}))
	})

	ctx, cancel := context.WithCancel(t.Context())
	messageID := newID(t)
	done := make(chan error, 1)
	go func() {
		_, err := f.engine.Handle(ctx, s, engine.Say{ClientMessageID: messageID, Text: sayTired})
		done <- err
	}()

	// 발화가 저장되고 모델 호출이 떠 있을 만큼만 기다렸다가 끊는다.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled, "끊긴 까닭이 그대로 올라와야 한다")
	case <-time.After(5 * time.Second):
		require.Fail(t, "끊긴 턴이 돌아오지 않았다")
	}
	assert.Equal(t, 1, f.talk.Interrupted(), "돌던 대화 모델 호출이 멈춰야 한다")

	// 버려진 답도 마찬가지다. 대응 단계에서는 미리 써 둔 말이 먼저 나가고, 돌던 호출은 턴이 끝나기 전에 멈춘다.
	f.replies(fake.Step{Text: replyOrdinary, Latency: slowModel})
	f.judge.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
		return fake.Reply(`{"stage":2,"evidence":""}`)
	})
	turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayTired})
	require.NoError(t, err)
	assert.Equal(t, crisis.StageRespond, turn.Stage)
	assert.Equal(t, 2, f.talk.Interrupted(), "버린 답의 호출도 멈춰야 한다")
}

func TestTurnsOnOneConversationAreSerialised(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.judges(crisis.StageNone, "")
	f.judges(crisis.StageNone, "")
	f.replies(
		fake.Step{Text: replyOrdinary, Latency: 30 * time.Millisecond},
		fake.Step{Text: replyShort, Latency: 30 * time.Millisecond},
	)

	says := []engine.Say{
		{ClientMessageID: newID(t), Text: sayOrdinary},
		{ClientMessageID: newID(t), Text: sayTired},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(says))
	for _, say := range says {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.engine.Handle(t.Context(), s, say)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	stored := f.storedUtterances(s.ConversationID())
	require.Len(t, stored, 5, "첫 안부와 두 턴이 모두 남아야 한다")
	for i, u := range stored[1:] {
		want := store.SpeakerUser
		if i%2 == 1 {
			want = store.SpeakerAI
		}
		assert.Equal(t, want, u.Speaker, "두 글이 겹쳐 돌면 사용자의 말이 잇따라 저장된다")
		assert.Equal(t, int32(i+1), u.Seq)
	}
	assert.Len(t, f.gateEvents(s.ConversationID()), 2)
}

func TestLogsCarryIdentifiersOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)
	f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
	require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))

	logs := f.logs.String()
	assert.Contains(t, logs, s.ConversationID().String(), "어느 대화인지는 남아야 한다")
	assert.Contains(t, logs, "final_stage")
	assert.Contains(t, logs, "latency")
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		assert.NotContains(t, line, "evidence\":\"", "근거 발화가 로그에 남았다")
	}
	f.assertLogsClean()
}
