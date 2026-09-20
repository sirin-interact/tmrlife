package engine_test

import (
	"context"
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

func TestStart(t *testing.T) {
	t.Parallel()

	t.Run("새 대화를 열고 첫 안부를 내보낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		assert.False(t, s.Resumed())
		assert.False(t, s.ResourcesPinned())

		events := f.sink.all()
		require.Len(t, events, 2, "대화를 여는 사건과 첫 안부만 나가야 한다")
		ready, ok := events[0].(engine.Ready)
		require.True(t, ok)
		assert.Equal(t, s.ConversationID(), ready.ConversationID)
		assert.Equal(t, "2026-09-20", ready.RecordDate.String(), "서울의 저녁 9시는 그날의 기록이다")
		assert.False(t, ready.Resumed)
		assert.Empty(t, ready.Utterances)
		assert.False(t, ready.ResourcesPinned)

		opening, ok := events[1].(engine.AIText)
		require.True(t, ok)
		assert.Equal(t, phrases.Opening, opening.Phrase)
		assert.Equal(t, store.OriginFixed, opening.Origin)
		assert.Equal(t, int32(0), opening.Seq)

		stored := f.storedUtterances(s.ConversationID())
		require.Len(t, stored, 1, "첫 안부도 발화로 저장된다")
		assert.Equal(t, store.SpeakerAI, stored[0].Speaker)
		assert.Equal(t, store.OriginFixed, stored[0].Origin)
		assert.Equal(t, opening.Text, stored[0].Text)
		assert.Zero(t, f.talk.Calls(), "첫 안부는 모델을 부르지 않는다")
	})

	t.Run("같은 기록 날짜의 열린 대화를 이어간다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		first := f.start()
		f.turn(first, sayOrdinary, crisis.StageNone, "", replyOrdinary)
		f.sink.reset()

		second := f.start()
		assert.True(t, second.Resumed())
		assert.Equal(t, first.ConversationID(), second.ConversationID())

		events := f.sink.all()
		require.Len(t, events, 1, "이어가는 대화에서는 첫 안부가 다시 나가지 않는다")
		ready, ok := events[0].(engine.Ready)
		require.True(t, ok)
		assert.True(t, ready.Resumed)
		require.Len(t, ready.Utterances, 3, "첫 안부, 사용자의 글, AI의 답이 순번대로 돌아온다")
		assert.Equal(t, sayOrdinary, ready.Utterances[1].Text)
		assert.Equal(t, replyOrdinary, ready.Utterances[2].Text)
		assert.False(t, ready.ResourcesPinned)
	})

	t.Run("기록 날짜가 지난 대화는 먼저 끝내고 새로 연다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		yesterday := f.start()
		f.turn(yesterday, sayOrdinary, crisis.StageNone, "", replyOrdinary)

		// 서울의 새벽 4시를 넘긴다.
		f.clock.Advance(9 * time.Hour)
		f.sink.reset()

		today := f.start()
		assert.NotEqual(t, yesterday.ConversationID(), today.ConversationID())
		assert.False(t, today.Resumed())
		assert.Equal(t, "2026-09-21", today.RecordDate().String())

		closed := f.conversation(yesterday.ConversationID())
		assert.Equal(t, store.ConversationEnded, closed.Status)
		require.NotNil(t, closed.EndReason)
		assert.Equal(t, store.EndReasonIdle, *closed.EndReason)
		assert.Equal(t, store.ProcessingPending, closed.ProcessingStatus)

		calls := f.diary.calls()
		require.Len(t, calls, 1, "지난 날의 대화도 일기 초안 작업을 받는다")
		assert.Equal(t, yesterday.ConversationID(), calls[0].conversationID)
	})

	t.Run("대화 방식이 아니면 시작하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		_, err := f.engine.Start(t.Context(), engine.StartInput{
			User: engine.Participant{ID: f.userID, Timezone: "Asia/Seoul"},
			Mode: "telepathy",
			Sink: f.sink,
		})
		assert.ErrorIs(t, err, engine.ErrUnsupportedMode)
	})

	t.Run("모르는 시간대는 기본 시간대로 본다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s, err := f.engine.Start(t.Context(), engine.StartInput{
			User: engine.Participant{ID: f.userID, Timezone: "Mars/Olympus"},
			Sink: f.sink,
		})
		require.NoError(t, err)
		assert.Equal(t, "2026-09-20", s.RecordDate().String())
	})
}

func TestTurnAtStageNone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	turn := f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)

	assert.Equal(t, crisis.StageNone, turn.Stage)
	assert.Equal(t, store.OriginModel, turn.Origin)
	assert.False(t, turn.Duplicate)
	assert.False(t, turn.ResourcesPinned)

	events := f.sink.all()
	require.Len(t, events, 4, "대화 열기, 첫 안부, 받았다는 확인, 답")
	accepted, ok := events[2].(engine.Accepted)
	require.True(t, ok)
	assert.Equal(t, int32(1), accepted.Seq)

	answer, ok := events[3].(engine.AIText)
	require.True(t, ok)
	assert.Equal(t, replyOrdinary, answer.Text)
	assert.Equal(t, replyOrdinary, answer.Speech)
	assert.Equal(t, store.OriginModel, answer.Origin)
	assert.Equal(t, int32(2), answer.Seq)

	stored := f.storedUtterances(s.ConversationID())
	require.Len(t, stored, 3)
	assert.Equal(t, sayOrdinary, stored[1].Text)
	require.NotNil(t, stored[1].ClientMessageID)
	assert.Equal(t, replyOrdinary, stored[2].Text)
	assert.Equal(t, store.OriginModel, stored[2].Origin)

	event := f.lastGateEvent(s.ConversationID())
	assert.Equal(t, int16(0), event.FinalStage, "해당 없음도 관문 기록을 남긴다")
	require.NotNil(t, event.RuleStage)
	assert.Equal(t, int16(0), *event.RuleStage)
	require.NotNil(t, event.AIStage)
	assert.Equal(t, int16(0), *event.AIStage)
	assert.False(t, event.AIFailed)
	assert.Equal(t, "none", event.DetectedBy)
	assert.Empty(t, event.Adjustments)
	assert.Nil(t, event.EvidenceEnc, "해당 없음에는 근거를 남기지 않는다")

	f.assertLogsClean()
}

func TestTurnAtStageRespond(t *testing.T) {
	t.Parallel()

	t.Run("버퍼의 답을 버리고 고정 문구와 자원을 내보낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		turn := f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")

		assert.Equal(t, crisis.StageRespond, turn.Stage)
		assert.Equal(t, store.OriginFixed, turn.Origin)
		assert.True(t, turn.ResourcesPinned)
		assert.Zero(t, f.talk.Calls(), "규칙 겹이 이미 잡은 말에는 대화 모델을 부르지 않는다")

		text := f.sink.lastText(t)
		assert.Equal(t, phrases.CrisisRespond, text.Phrase)
		assert.Equal(t, store.OriginFixed, text.Origin)
		assert.Contains(t, text.Text, "109")
		assert.NotContains(t, text.Speech, "109", "음성으로 나가는 글은 번호를 한글로 푼다")

		events := f.sink.all()
		resources, ok := events[len(events)-1].(engine.Resources)
		require.True(t, ok, "고정 문구 다음에 자원이 나간다")
		require.Len(t, resources.Items, 3)
		assert.Equal(t, "suicide_prevention_109", resources.Items[0].ID)

		event := f.lastGateEvent(s.ConversationID())
		assert.Equal(t, int16(2), event.FinalStage)
		assert.Equal(t, "both", event.DetectedBy)
		assert.Equal(t, sayCrisis, f.openEvidence(event), "대응 단계에는 근거 발화를 남긴다")
	})

	t.Run("긴급 단계는 급한 번호를 앞에 둔 자원을 내보낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		turn := f.turn(s, sayUrgent, crisis.StageUrgent, sayUrgent, "")

		assert.Equal(t, crisis.StageUrgent, turn.Stage)
		text := f.sink.lastText(t)
		assert.Equal(t, phrases.CrisisUrgent, text.Phrase)

		events := f.sink.all()
		resources, ok := events[len(events)-1].(engine.Resources)
		require.True(t, ok)
		require.Len(t, resources.Items, 3)
		assert.Equal(t, "suicide_prevention_109", resources.Items[0].ID)
		assert.Equal(t, "emergency_119", resources.Items[1].ID)
	})

	t.Run("위기 응답 뒤의 평소 대화는 위기 상황용 지시문으로 이어간다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
		f.turn(s, sayTired, crisis.StageNone, "", replyListen)

		request, ok := f.talk.LastRequest()
		require.True(t, ok)
		assert.Contains(t, request.Task, "crisis", "위기 상황용 지시문을 쓴다")
		assert.Equal(t, 1, f.sink.resources(), "자원은 한 연결에서 한 번만 나간다")
	})
}

func TestTwoStepCheck(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()

	// 첫 걸음: 사용자의 표현을 받아 되묻는다.
	first := f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)
	assert.Equal(t, crisis.StageCheck, first.Stage)
	assert.Equal(t, store.CheckStateReflected, f.conversation(s.ConversationID()).CheckState)
	assert.Zero(t, f.sink.resources(), "확인 단계에서는 자원을 고정하지 않는다")

	mirror := f.sink.lastText(t)
	assert.Contains(t, []string{store.OriginModel, store.OriginTemplate}, mirror.Origin)
	assert.NotEqual(t, store.OriginFixed, mirror.Origin, "되묻기는 대화 모델이 만든다")

	// 둘째 걸음: 돌려 말하지 않고 한 번 묻는다.
	second := f.turn(s, sayVague2, crisis.StageCheck, sayVague2, "")
	assert.Equal(t, crisis.StageCheck, second.Stage)
	assert.Equal(t, store.OriginFixed, second.Origin)
	assert.Equal(t, phrases.DirectAsk, f.sink.lastText(t).Phrase)
	assert.Equal(t, store.CheckStateAsked, f.conversation(s.ConversationID()).CheckState)

	// 직접 물은 뒤에 또 확인 단계가 나오면 코어의 규칙이 대응 단계로 올린다.
	third := f.turn(s, sayVague, crisis.StageCheck, sayVague, "")
	assert.Equal(t, crisis.StageRespond, third.Stage)
	assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)

	event := f.lastGateEvent(s.ConversationID())
	assert.Contains(t, event.Adjustments, crisis.AdjustRepeatAfterDirectAsk.String())
	assert.Equal(t, 1, f.sink.count(func(e engine.Event) bool {
		text, ok := e.(engine.AIText)
		return ok && text.Phrase == phrases.DirectAsk
	}), "직접 묻기는 한 대화에서 한 번뿐이다")

	f.assertLogsClean()
}

func TestGateFailures(t *testing.T) {
	t.Parallel()

	t.Run("판별이 시간을 넘겨도 규칙에 걸린 말은 적어도 확인 단계다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.judgeFails(fake.Step{Latency: 2 * gateTimeout, Text: `{"stage":0,"evidence":""}`})
		f.replies(fake.Reply(replyMirror))
		turn := f.say(s, sayVague)

		assert.GreaterOrEqual(t, turn.Stage, crisis.StageCheck)
		event := f.lastGateEvent(s.ConversationID())
		assert.True(t, event.AIFailed)
		assert.Nil(t, event.AIStage)
		require.NotNil(t, event.RuleStage)
		assert.Equal(t, int16(1), *event.RuleStage)
		assert.Equal(t, "rule", event.DetectedBy)
	})

	t.Run("판별이 실패하고 규칙에도 걸리지 않으면 평소 대화로 지나간다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.judgeFails(fake.Reply(""))
		f.replies(fake.Reply(replyOrdinary))
		turn := f.say(s, sayOrdinary)

		assert.Equal(t, crisis.StageNone, turn.Stage)
		assert.Equal(t, store.OriginModel, turn.Origin)

		event := f.lastGateEvent(s.ConversationID())
		assert.Equal(t, int16(0), event.FinalStage)
		assert.True(t, event.AIFailed, "답하지 못한 판별은 실패로 기록한다")
		assert.Nil(t, event.AIStage)
	})

	t.Run("판별에 걸린 시간을 기록한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.judge.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
			f.clock.Advance(120 * time.Millisecond)
			return fake.Reply(`{"stage":0,"evidence":""}`)
		})
		f.replies(fake.Reply(replyOrdinary))
		f.say(s, sayOrdinary)

		event := f.lastGateEvent(s.ConversationID())
		require.NotNil(t, event.AILatencyMs)
		assert.Equal(t, int32(120), *event.AILatencyMs)
	})
}
