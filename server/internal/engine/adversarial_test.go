package engine_test

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
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 이 파일은 안전 경로를 일부러 괴롭힌다.
//
// 현장에서 가입한 사람이 무거운 말을 했을 때, 모델이 늦거나 막히거나 이상한 답을 내놓아도,
// 연결이 중간에 끊기거나 다른 기기가 끼어들어도, 나가야 할 말이 나가고 판정이 남아야 한다.
// 여기 있는 시험은 그 약속이 깨지는 길을 찾는다.

// gateFailures는 판별 모델이 답하지 못하는 방식이다. 모두 "답하지 못함"으로 읽혀야 한다.
func gateFailures() []struct {
	name string
	step fake.Step
} {
	return []struct {
		name string
		step fake.Step
	}{
		{"차단", fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{Task: "gate_classifier"}))},
		{"후보 없음", fake.Fail(ai.NewError(ai.ErrNoCandidate, ai.Detail{Task: "gate_classifier"}))},
		{"빈 응답", fake.Reply("")},
		{"잘린 답", fake.Step{Text: `{"stage":`, FinishReason: ai.FinishMaxTokens}},
		{"정상이 아닌 종료", fake.Step{Text: `{"stage":0,"evidence":""}`, FinishReason: ai.FinishSafety}},
		{"JSON이 아님", fake.Reply("단계는 0입니다")},
		{"단계가 범위 밖", fake.Reply(`{"stage":7,"evidence":""}`)},
		{"시간 초과", fake.Step{Latency: 3 * gateTimeout, Text: `{"stage":0,"evidence":""}`}},
	}
}

// 판별 모델이 어떻게 무너지든, 규칙 겹이 잡은 위기 발화에는 고정 문구가 나가야 한다.
// 판별의 실패를 "해당 없음"으로 읽어 평소 대화로 넘기는 길이 없어야 한다.
func TestCrisisAnswerSurvivesEveryGateModelFailure(t *testing.T) {
	t.Parallel()
	for _, tt := range gateFailures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)

			s := f.start()
			f.judgeFails(tt.step)
			// 대화 모델의 대본은 일부러 비워 둔다. 버퍼의 답을 쓰려 들면 ErrNoScript로 드러난다.
			turn := f.say(s, sayCrisis)

			assert.GreaterOrEqual(t, turn.Stage, crisis.StageRespond, "규칙이 잡은 위기 발화가 내려갔다")
			assert.Equal(t, store.OriginFixed, turn.Origin)
			assert.True(t, turn.ResourcesPinned)
			assert.Zero(t, f.talk.Calls(), "위기 발화에는 대화 모델을 부르지 않는다")

			text := f.sink.lastText(t)
			assert.Contains(t, []phrases.ID{phrases.CrisisRespond, phrases.CrisisUrgent}, text.Phrase)

			event := f.lastGateEvent(s.ConversationID())
			assert.True(t, event.AIFailed, "답하지 못한 판별은 실패로 남아야 한다")
			assert.Nil(t, event.AIStage)
			assert.GreaterOrEqual(t, event.FinalStage, int16(crisis.StageRespond))
			assert.Equal(t, 1, f.sink.resources())
		})
	}
}

// 규칙 겹은 놓치고 AI 판별만 잡은 위기다. 미리 만들어 둔 평소의 답이 대화 기록에도 화면에도 남으면 안 된다.
func TestAIOnlyCrisisDiscardsTheBufferedReply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stage  crisis.Stage
		phrase phrases.ID
		first  string
	}{
		{"대응 단계", crisis.StageRespond, phrases.CrisisRespond, "suicide_prevention_109"},
		{"긴급 단계", crisis.StageUrgent, phrases.CrisisUrgent, "suicide_prevention_109"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)

			s := f.start()
			f.replies(fake.Reply(replyOrdinary))
			f.judges(tt.stage, "")
			turn := f.say(s, sayTired)

			assert.Equal(t, tt.stage, turn.Stage)
			assert.Equal(t, store.OriginFixed, turn.Origin)
			assert.Equal(t, tt.phrase, f.sink.lastText(t).Phrase)
			assert.NotContains(t, f.sink.texts(), replyOrdinary, "버린 답이 화면으로 나갔다")

			for _, u := range f.storedUtterances(s.ConversationID()) {
				assert.NotEqual(t, replyOrdinary, u.Text, "버린 답이 대화 기록에 남았다")
			}
			// 자원은 고정 문구보다 먼저 나간다. 발화를 저장하는 쓰기가 실패해도 번호는 화면에 남아야 하기 때문이다.
			var resources engine.Resources
			var sawResources, resourcesFirst bool
			for _, e := range f.sink.all() {
				switch ev := e.(type) {
				case engine.Resources:
					resources, sawResources = ev, true
				case engine.AIText:
					if ev.Phrase == tt.phrase {
						resourcesFirst = sawResources
					}
				}
			}
			require.True(t, sawResources, "자원이 나가야 한다")
			assert.True(t, resourcesFirst, "자원은 고정 문구보다 먼저 나가야 한다")
			require.NotEmpty(t, resources.Items)
			assert.Equal(t, tt.first, resources.Items[0].ID)
		})
	}
}

// 판별이 늦고 대화 모델도 늦는 최악의 순간이다. 고정 문구는 둘 중 어느 것도 기다리지 않아야 한다.
//
// 판별의 시간 제한은 넘겨야 규칙의 판정이 쓰인다. 대화 모델은 그보다 훨씬 늦게 두고,
// 턴이 대화 모델의 시간을 기다리지 않고 끝나는 것을 본다.
func TestSlowGateAndSlowModelDoNotDelayTheFixedPhrase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.judgeFails(fake.Step{Latency: 3 * gateTimeout, Text: `{"stage":0,"evidence":""}`})
	f.replies(fake.Step{Text: replyOrdinary, Latency: slowModel})

	done := make(chan result, 1)
	messageID := newID(t)
	f.remember(sayCrisis)
	go func() {
		turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: messageID, Text: sayCrisis})
		done <- result{turn: turn, err: err}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		assert.GreaterOrEqual(t, got.turn.Stage, crisis.StageRespond)
		assert.Equal(t, store.OriginFixed, got.turn.Origin)
	case <-time.After(slowModel / 2):
		require.Fail(t, "고정 문구가 늦은 모델을 기다렸다")
	}
	assert.Contains(t,
		[]phrases.ID{phrases.CrisisRespond, phrases.CrisisUrgent},
		f.sink.lastText(t).Phrase)
}

// 확인 상태는 대화 행에 있다. 연결이 끊겼다 이어져도 되묻기를 되풀이하지 않고 다음 걸음으로 간다.
func TestTwoStepCheckResumesAcrossConnections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// 첫 연결: 되묻는다.
	first := f.start()
	f.turn(first, sayVague, crisis.StageCheck, sayVague, replyMirror)
	require.Equal(t, store.CheckStateReflected, f.conversation(first.ConversationID()).CheckState)

	// 연결이 끊기고 같은 대화를 이어받는다.
	second := f.start()
	require.True(t, second.Resumed())
	require.Equal(t, first.ConversationID(), second.ConversationID())

	turn := f.turn(second, sayVague2, crisis.StageCheck, sayVague2, "")
	assert.Equal(t, crisis.StageCheck, turn.Stage)
	assert.Equal(t, phrases.DirectAsk, f.sink.lastText(t).Phrase, "이어붙은 연결에서 되묻기를 되풀이했다")
	assert.Equal(t, store.CheckStateAsked, f.conversation(second.ConversationID()).CheckState)

	// 또 한 번 끊겼다 이어진다. 직접 물은 뒤의 확인 단계는 대응 단계로 올라간다.
	third := f.start()
	last := f.turn(third, sayVague, crisis.StageCheck, sayVague, "")
	assert.Equal(t, crisis.StageRespond, last.Stage)
	assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)

	assert.Equal(t, 1, f.sink.count(func(e engine.Event) bool {
		text, ok := e.(engine.AIText)
		return ok && text.Phrase == phrases.DirectAsk
	}), "직접 묻기는 연결이 바뀌어도 한 대화에서 한 번뿐이다")
}

// 위기 응답이 나간 대화를 다른 연결로 이어받으면, 자원 고정도 위기 상황용 지시문도 그대로 이어져야 한다.
func TestCrisisFollowUpSurvivesAReconnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")

	f.sink.reset()
	resumed := f.start()
	require.True(t, resumed.ResourcesPinned())
	assert.Equal(t, 1, f.sink.resources(), "이어붙은 화면에도 자원이 다시 고정돼야 한다")

	f.turn(resumed, sayTired, crisis.StageNone, "", replyListen)
	request, ok := f.talk.LastRequest()
	require.True(t, ok)
	assert.Contains(t, request.Task, "crisis", "이어받은 연결이 평소의 지시문으로 돌아갔다")
}

// 끼어든 취소다. 판별이 도는 동안 연결이 끊기면 답은 나가지 못하지만 판정은 남는다.
//
// 판정까지 함께 잃으면, 무거운 말을 하고 탭을 닫은 사람의 대화가 위기 기록 없이 자동 일기의 재료로 흘러간다.
// 같은 식별자로 다시 보내면 저장된 판정 그대로 고정 문구가 나간다.
func TestCancelDuringTheGateKeepsTheVerdictAndTheResendRecovers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	release := make(chan struct{})
	f.judge.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
		close(release)
		<-ctx.Done()
		return fake.Fail(ai.ContextError(ctx.Err(), ai.Detail{Task: "gate_classifier"}))
	})

	ctx, cancel := context.WithCancel(t.Context())
	messageID := newID(t)
	f.remember(sayCrisis)
	done := make(chan error, 1)
	go func() {
		_, err := f.engine.Handle(ctx, s, engine.Say{ClientMessageID: messageID, Text: sayCrisis})
		done <- err
	}()

	<-release
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.Fail(t, "끊긴 턴이 돌아오지 않았다")
	}

	stored := f.storedUtterances(s.ConversationID())
	require.Len(t, stored, 2, "첫 안부와 사용자의 글만 남아야 한다")
	assert.Equal(t, store.SpeakerUser, stored[1].Speaker)

	// 끊겼어도 판정은 남는다. 이 기록이 없으면 무거운 말이 오간 대화가 자동 일기의 재료로 흘러간다.
	// 판별 모델은 멈췄으므로 규칙 겹의 판정만으로 내려진 기록이다.
	events := f.gateEvents(s.ConversationID())
	require.Len(t, events, 1, "연결이 끊겼다고 위기 판정까지 잃지 않는다")
	assert.Equal(t, int16(crisis.StageRespond), events[0].FinalStage)
	assert.True(t, events[0].AIFailed, "멈춘 판별은 실패로 남는다")

	// 클라이언트가 같은 식별자로 다시 보낸다. 저장된 판정 그대로 고정 문구가 나가야 한다.
	f.judge.SetHandler(nil)
	judged := f.judge.Calls()
	turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: messageID, Text: sayCrisis})
	require.NoError(t, err)
	assert.True(t, turn.Duplicate)
	assert.Equal(t, crisis.StageRespond, turn.Stage)
	assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)
	assert.Len(t, f.gateEvents(s.ConversationID()), 1, "판정을 두 번 남기지 않는다")
	assert.Equal(t, judged, f.judge.Calls(), "저장된 판정을 쓰므로 판별을 다시 부르지 않는다")
}

// 화면으로 내보내다 실패해도 판정과 나간 말은 남는다. 다시 이으면 그 말과 자원이 화면에 돌아와야 한다.
// 사용자가 탭을 닫은 순간에 위기 응답이 나가는 경우다.
func TestCrisisRecordSurvivesAFailedEmit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.sink.hook = func(e engine.Event) error {
		if text, ok := e.(engine.AIText); ok && text.Phrase == phrases.CrisisRespond {
			return context.Canceled
		}
		return nil
	}
	f.judges(crisis.StageRespond, sayCrisis)
	f.remember(sayCrisis)
	_, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayCrisis})
	require.Error(t, err, "내보내지 못한 턴은 오류로 돌아와야 한다")

	event := f.lastGateEvent(s.ConversationID())
	assert.Equal(t, int16(crisis.StageRespond), event.FinalStage, "판정은 남아야 한다")
	stored := f.storedUtterances(s.ConversationID())
	last := stored[len(stored)-1]
	assert.Equal(t, store.OriginFixed, last.Origin, "나간 말은 대화 기록에 남아야 한다")

	f.sink.hook = nil
	f.sink.reset()
	resumed := f.start()
	assert.True(t, resumed.ResourcesPinned(), "다시 이으면 자원 고정이 돌아와야 한다")
	assert.Equal(t, 1, f.sink.resources())
	ready, ok := f.sink.all()[0].(engine.Ready)
	require.True(t, ok)
	assert.True(t, ready.ResourcesPinned)
	require.NotEmpty(t, ready.Utterances)
	assert.Equal(t, store.OriginFixed, ready.Utterances[len(ready.Utterances)-1].Origin,
		"보지 못한 위기 응답이 지난 말로 돌아와야 한다")
}

// 글 둘이 거의 동시에 들어와도 저마다의 판정을 남긴다. 위기 발화가 다른 글의 답에 묻히지 않아야 한다.
func TestRapidMessagesAreEachGated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	// 두 글이 어느 쪽 먼저 돌지 알 수 없다. 판별은 대본이 아니라 받은 글을 보고 답한다.
	// 지난 말에도 그 글이 들어 있으므로 이번에 판별할 마지막 발화만 본다.
	const lastSaid = `"last_user_utterance":"` + sayCrisis + `"`
	f.judge.SetHandler(func(_ context.Context, req ai.Request) fake.Step {
		for _, m := range req.Messages {
			if strings.Contains(m.Text, lastSaid) {
				return fake.Reply(`{"stage":2,"evidence":""}`)
			}
		}
		return fake.Reply(`{"stage":0,"evidence":""}`)
	})
	f.talk.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
		return fake.Step{Text: replyOrdinary, Latency: 30 * time.Millisecond}
	})
	f.remember(replyOrdinary)

	says := []engine.Say{
		{ClientMessageID: newID(t), Text: sayOrdinary},
		{ClientMessageID: newID(t), Text: sayCrisis},
	}
	f.remember(sayOrdinary, sayCrisis)

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

	events := f.gateEvents(s.ConversationID())
	require.Len(t, events, 2, "글마다 판정이 하나씩 남아야 한다")
	assert.Equal(t, 1, f.sink.count(func(e engine.Event) bool {
		text, ok := e.(engine.AIText)
		return ok && text.Phrase == phrases.CrisisRespond
	}), "위기 발화의 고정 문구가 나가야 한다")
	assert.Equal(t, 1, f.sink.resources())
}

// 나가는 말은 모두 그 자리에 맞는 출처를 가진다. 관문을 거치지 않고 나가는 말(첫 안부, 무응답 확인)은
// 사용자의 발화에 대한 답이 아니며, 사용자의 발화에 대한 답은 모두 그 발화의 판정이 남은 뒤에 나간다.
func TestNoAnswerLeavesWithoutItsVerdict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	var answers int
	f.sink.hook = func(e engine.Event) error {
		text, ok := e.(engine.AIText)
		if !ok {
			return nil
		}
		if text.Phrase == phrases.Opening || text.Phrase == phrases.IdleCheck {
			// 사용자의 말에 대한 답이 아니다. 판정을 낄 자리가 없고, 언제나 미리 써 둔 말이다.
			assert.Equal(t, store.OriginFixed, text.Origin)
			return nil
		}
		answers++
		_, found := f.gateEventForSeq(s.ConversationID(), text.Seq-1)
		assert.True(t, found, "관문의 판정이 저장되기 전에 말이 나갔다")
		return nil
	}

	f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)
	require.NoError(t, f.engine.Nudge(t.Context(), s))
	f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)
	f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
	f.turn(s, sayTired, crisis.StageNone, "", replyListen)
	assert.Equal(t, 4, answers, "네 턴의 답을 모두 확인해야 한다")

	// 다시 보낸 글의 답도 마찬가지다. 그때 남은 판정 위에서 나간 말이다.
	f.sink.hook = nil
	f.assertLogsClean()
}

// 관용 표현은 관문을 거치되 위기로 다루지 않는다. 안전 경로가 일상 대화를 삼키면 사람들은 말을 고르기 시작한다.
func TestIdiomDoesNotTriggerTheCrisisPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	turn := f.turn(s, sayIdiom, crisis.StageNone, "", replyShort)

	assert.Equal(t, crisis.StageNone, turn.Stage)
	assert.Equal(t, store.OriginModel, turn.Origin)
	assert.False(t, turn.ResourcesPinned)
	assert.Zero(t, f.sink.resources())

	event := f.lastGateEvent(s.ConversationID())
	assert.Equal(t, int16(0), event.FinalStage, "관용 표현에도 판정은 남는다")
	assert.Nil(t, event.EvidenceEnc)
}

// 끝난 대화에는 어떤 말도 더 나가지 않는다. 다른 연결이나 주기 작업이 먼저 닫은 경우다.
func TestNothingLeavesAfterTheConversationIsClosedElsewhere(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	sweeper := f.newSweeper(time.Minute)
	f.clock.Advance(2 * time.Minute)
	result, err := sweeper.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, result.Ended)

	f.sink.reset()
	f.remember(sayCrisis)
	_, err = f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayCrisis})
	require.ErrorIs(t, err, engine.ErrConversationEnded)
	assert.Empty(t, f.sink.texts(), "닫힌 대화에 말이 나갔다")
	assert.Empty(t, f.gateEvents(s.ConversationID()))
	assert.Zero(t, f.judge.Calls(), "닫힌 대화에는 모델을 부르지 않는다")
}

// 위기 상황에서 오간 말은 로그 어디에도 남지 않는다. 근거 발화도 마찬가지다.
func TestCrisisPathLogsCarryIdentifiersOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)
	f.turn(s, sayUrgent, crisis.StageUrgent, sayUrgent, "")
	f.turn(s, sayTired, crisis.StageNone, "", replyListen)
	require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))

	logs := f.logs.String()
	require.Contains(t, logs, "gate decision recorded")
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		assert.NotContains(t, line, "evidence\":\"", "근거 발화가 로그에 남았다")
	}
	f.assertLogsClean()
}
