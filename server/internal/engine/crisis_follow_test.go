package engine_test

import (
	"context"
	"errors"
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
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// replyFollow는 위기 뒤 대화의 답이다. 그 방식의 출력 검사는 질문을 받지 않으므로 묻지 않는 말이어야 한다.
const replyFollow = "지금 여기 같이 있을게요. 천천히 이야기해도 괜찮아요."

// assertNoHelpNumbers는 나간 말에 번호가 들어 있지 않은지 본다. 고정 문구가 다시 나갔는지를 가리는 가장 단단한 표시다.
func assertNoHelpNumbers(t *testing.T, text string) {
	t.Helper()
	assert.NotContains(t, text, "109")
	assert.NotContains(t, text, "1577")
	assert.NotContains(t, text, "119")
}

// 위기 고정 문구는 한 대화에서 그 단계의 첫 응답에만 쓴다.
//
// 같은 대화에서 최종 단계가 다시 대응 단계 이상이 되는 일은 흔하다. 그때마다 같은 글을 글자 그대로 다시 읽어 주면
// "전화는 하기 싫어"라고 답한 사람이 같은 번호를 다시 듣게 된다. 듣는 쪽에 머물겠다는 앱이
// 같은 안내만 되풀이하는 모양이다.
func TestCrisisPhraseIsNotRepeatedInOneConversation(t *testing.T) {
	t.Parallel()

	t.Run("대응 단계가 잇따르면 둘째 턴은 이어가는 대화다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		first := f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
		require.Equal(t, store.OriginFixed, first.Origin)
		require.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)

		second := f.turn(s, "전화는 하기 싫어", crisis.StageRespond, "전화는 하기 싫어", replyFollow)
		assert.Equal(t, crisis.StageRespond, second.Stage, "단계는 그대로 대응 단계다")
		assert.Equal(t, store.OriginModel, second.Origin, "둘째 턴의 말은 모델이 만든다")

		text := f.sink.lastText(t)
		assert.NotEqual(t, phrases.CrisisRespond, text.Phrase, "같은 고정 문구가 다시 나갔다")
		assertNoHelpNumbers(t, text.Text)

		request, ok := f.talk.LastRequest()
		require.True(t, ok)
		assert.Contains(t, request.Task, "crisis", "위기 뒤 대화 지시문을 쓴다")
	})

	t.Run("판별이 확인 단계로 봐도 코어가 대응 단계로 올리면 마찬가지다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")

		// 앞선 대응 판정이 있으면 뒤따르는 확인 단계의 표현도 대응 단계로 올라간다. 실제 대화에서 되풀이가 터지는 길이 이쪽이다.
		second := f.turn(s, sayVague, crisis.StageCheck, sayVague, replyFollow)
		require.Equal(t, crisis.StageRespond, second.Stage, "코어가 대응 단계로 올린다")
		assert.Equal(t, store.OriginModel, second.Origin)
		assertNoHelpNumbers(t, f.sink.lastText(t).Text)
	})

	t.Run("더 높은 단계는 그 단계의 고정 문구가 한 번 나간다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
		require.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)

		f.turn(s, sayUrgent, crisis.StageUrgent, sayUrgent, "")
		assert.Equal(t, phrases.CrisisUrgent, f.sink.lastText(t).Phrase, "긴급 단계는 그 단계의 첫 응답이다")

		// 긴급 단계 뒤에 다시 대응 단계가 와도 아래 단계의 고정 문구로 돌아가지 않는다.
		third := f.turn(s, sayTired, crisis.StageRespond, sayTired, replyFollow)
		assert.Equal(t, store.OriginModel, third.Origin)
		assertNoHelpNumbers(t, f.sink.lastText(t).Text)

		row := f.conversation(s.ConversationID())
		assert.Equal(t, int16(crisis.StageUrgent), row.CrisisSpokenStage, "말한 단계는 뒤로 돌아가지 않는다")
	})

	t.Run("답이 나가기 전에 끊긴 턴을 다시 보내면 고정 문구가 그때 처음 나간다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		// 판정만 남기고 답을 내보내기 전에 끊긴 턴이다. 말한 단계는 아직 0이어야 한다.
		id := newID(t)
		stored := f.appendUser(s, id, sayCrisis)
		f.insertGateEvent(s, stored.ID, crisis.StageRespond)
		require.Equal(t, int16(0), f.conversation(s.ConversationID()).CrisisSpokenStage)

		turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayCrisis})
		require.NoError(t, err)
		assert.Equal(t, store.OriginFixed, turn.Origin)
		assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase, "관문 기록만으로 말한 것으로 치면 안 된다")
		assert.Equal(t, int16(crisis.StageRespond), f.conversation(s.ConversationID()).CrisisSpokenStage)
	})

	t.Run("이어가는 말도 미리 만들어 둔다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
		before := f.talk.Calls()

		// 규칙 겹이 대응 단계로 보는 말이다. 그 단계의 고정 문구는 이미 나갔으므로 이어가는 말을 미리 만들어 둔다.
		f.judges(crisis.StageRespond, sayCrisis)
		f.replies(fake.Reply(replyFollow))
		f.say(s, sayCrisis)

		assert.Equal(t, before+1, f.talk.Calls())
		assert.Zero(t, f.talk.Interrupted(), "미리 만든 답을 버리지 않는다")
		assertNoHelpNumbers(t, f.sink.lastText(t).Text)
	})
}

// 위기 응답은 두 번째 쓰기의 성패에 매달리지 않는다.
//
// 판정은 먼저 저장된다. 그 뒤로 나가는 말을 저장하는 쓰기가 실패하면, 고치기 전에는 고정 문구도 도움 자원도
// 화면에 닿지 않았다. 관문 기록에는 대응 단계가 남고 사람이 본 마지막 말은 첫 안부였다.
func TestCrisisReplyReachesTheScreenWhenItCannotBeStored(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	// 자원이 나가는 그 순간에 대화를 끝낸다. 기록 날짜가 넘어가 다른 복제본이 대화를 닫는 경우와 같은 모양이다.
	// 그 뒤의 쓰기는 모두 실패한다.
	var once sync.Once
	f.sink.hook = func(e engine.Event) error {
		if _, ok := e.(engine.Resources); !ok {
			return nil
		}
		once.Do(func() {
			_, err := f.store.Queries().EndConversation(t.Context(), db.EndConversationParams{
				Now: f.clock.Now(), EndReason: store.EndReasonIdle, ProcessingStatus: store.ProcessingNone,
				ID: s.ConversationID(), UserID: f.userID,
			})
			require.NoError(t, err)
		})
		return nil
	}

	f.judges(crisis.StageRespond, sayCrisis)
	f.remember(sayCrisis)
	_, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayCrisis})
	require.Error(t, err, "대화가 끝났다는 사실은 부르는 쪽이 알아야 한다")
	require.ErrorIs(t, err, engine.ErrConversationEnded)

	require.Equal(t, 1, f.sink.resources(), "도움 자원이 화면에 닿아야 한다")
	text := f.sink.lastText(t)
	assert.Equal(t, phrases.CrisisRespond, text.Phrase, "고정 문구가 화면에 닿아야 한다")
	assert.Contains(t, text.Text, "109")
	assert.Positive(t, text.Seq, "순번이 없으면 화면이 첫 안부보다 위에 놓는다")

	// 저장하지 못했다는 사실은 남는다. 글은 남지 않는다.
	assert.Contains(t, f.logs.String(), "crisis reply cannot be stored")
	f.assertLogsClean()
}

// 직접 묻기는 한 대화에서 한 번이다. 두 연결이 같은 대화를 함께 다뤄도 마찬가지다.
//
// 대화 행의 확인 상태를 읽고 나서 옮기는 것으로는 모자란다. 둘 다 "되물었다"를 읽고 둘 다 직접 묻기로 가면,
// 사람은 "죽고 싶다는 생각을 하고 있나요"를 연달아 두 번 받는다.
func TestDirectAskHappensOnceAcrossTwoConnections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// 첫 걸음까지는 한 연결로 간다. 여기까지 오면 확인 상태는 "되물었다"다.
	first := f.start()
	f.turn(first, sayVague, crisis.StageCheck, sayVague, replyMirror)
	require.Equal(t, store.CheckStateReflected, f.conversation(first.ConversationID()).CheckState)

	// 다른 연결이 같은 대화를 이어받는다. 두 연결이 각자의 사건을 받는다.
	secondSink := &recorder{}
	second, err := f.engine.Start(t.Context(), engine.StartInput{
		User: engine.Participant{ID: f.userID, Timezone: "Asia/Seoul"},
		Mode: store.ModeChat, Sink: secondSink,
	})
	require.NoError(t, err)
	require.Equal(t, first.ConversationID(), second.ConversationID())

	// 두 연결에 애매한 말이 하나씩 들어온다. 판별이 둘 다 확인 단계로 본다.
	// 판별에 걸리는 시간을 겹쳐 둔다. 두 턴이 확인 상태를 같은 값으로 읽는 순간을 만드는 것이 이 시험의 요점이다.
	f.judge.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
		return fake.Step{Text: `{"stage":1,"evidence":""}`, Latency: 150 * time.Millisecond}
	})
	f.talk.SetHandler(func(_ context.Context, _ ai.Request) fake.Step {
		return fake.Reply(replyFollow)
	})
	f.remember(sayVague2, replyFollow)

	// 식별자는 고루틴 밖에서 미리 만든다. 만드는 자리에 단언이 있다.
	says := []engine.Say{
		{ClientMessageID: newID(t), Text: sayVague2},
		{ClientMessageID: newID(t), Text: sayVague2},
	}
	var wg sync.WaitGroup
	for i, s := range []*engine.Session{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 오류는 여기서 단언하지 않는다. 진 쪽이 무엇을 내보냈는지가 이 시험이 보는 것이다.
			_, _ = f.engine.Handle(t.Context(), s, says[i])
		}()
	}
	wg.Wait()

	asked := 0
	for _, r := range []*recorder{f.sink, secondSink} {
		for _, e := range r.all() {
			if text, ok := e.(engine.AIText); ok && text.Phrase == phrases.DirectAsk {
				asked++
			}
		}
	}
	assert.Equal(t, 1, asked, "같은 질문을 연달아 두 번 하지 않는다")
	assert.Equal(t, store.CheckStateAsked, f.conversation(first.ConversationID()).CheckState)
}

// 식별자를 다시 쓴 클라이언트가 보낸 다른 글도 관문을 그대로 거친다.
//
// 다시 보내기를 가리는 일은 식별자만 본다. 글까지 견주지 않으면, 같은 식별자에 실린 새 글은 저장된 옛 글의 답으로
// 갈음되어 관문을 한 번도 거치지 않는다. 웹앱은 글마다 새 식별자를 만들지만 서버가 그것에 기대지 않는다.
func TestReusedClientMessageIDWithDifferentTextStillReachesTheGate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)

	// 같은 식별자에 전혀 다른, 무거운 글을 싣는다.
	reused := f.storedUtterances(s.ConversationID())[1].ClientMessageID
	require.NotNil(t, reused)
	id := *reused

	f.judges(crisis.StageRespond, sayCrisis)
	f.remember(sayCrisis)
	turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayCrisis})
	require.NoError(t, err)

	assert.False(t, turn.Replayed, "먼저 저장된 글의 답으로 갈음하지 않는다")
	assert.Equal(t, crisis.StageRespond, turn.Stage)
	assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)
	require.Len(t, f.gateEvents(s.ConversationID()), 2, "새 글에도 관문 기록이 남는다")

	var found bool
	for _, u := range f.storedUtterances(s.ConversationID()) {
		if u.Speaker == store.SpeakerUser && u.Text == sayCrisis {
			found = true
			assert.Nil(t, u.ClientMessageID, "식별자는 먼저 저장된 글이 들고 있다")
		}
	}
	assert.True(t, found, "보낸 글이 대화 기록에 남아야 한다")
}

// 다시 내보내는 고정 문구는 처음 나갔을 때와 같아야 한다. 음성으로 읽을 글에 번호가 숫자로 남으면 안 된다.
func TestReplayedFixedPhraseKeepsItsSpeech(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	id := newID(t)
	f.judges(crisis.StageRespond, sayCrisis)
	f.remember(sayCrisis)
	_, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayCrisis})
	require.NoError(t, err)
	first := f.sink.lastText(t)
	require.Equal(t, phrases.CrisisRespond, first.Phrase)

	// 같은 식별자로 다시 보낸다. 그때 나간 말이 그대로 다시 나가야 한다.
	f.sink.reset()
	turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayCrisis})
	require.NoError(t, err)
	require.True(t, turn.Replayed)

	again := f.sink.lastText(t)
	assert.Equal(t, first.Text, again.Text)
	assert.Equal(t, first.Phrase, again.Phrase, "다시 나간 말이 어느 문구였는지 알 수 없게 됐다")
	assert.Equal(t, first.Speech, again.Speech)
	assert.NotContains(t, again.Speech, "109", "음성으로 나가는 글은 번호를 한글로 푼다")
}

// 도움 자원은 내보내기가 실패하면 그 연결에 아직 빚으로 남는다.
func TestResourcesStayOwedWhenTheirFirstEmitFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	var failed bool
	f.sink.hook = func(e engine.Event) error {
		if _, ok := e.(engine.Resources); ok && !failed {
			failed = true
			return errors.New("연결이 끊겼다")
		}
		return nil
	}

	f.judges(crisis.StageRespond, sayCrisis)
	f.remember(sayCrisis)
	_, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayCrisis})
	require.Error(t, err, "내보내지 못한 자원은 턴을 세운다")
	require.True(t, failed)

	// 받는 쪽에 닿지 않은 것은 세지 않는다.
	f.sink.reset()
	f.judges(crisis.StageUrgent, sayUrgent)
	f.remember(sayUrgent)
	_, err = f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayUrgent})
	require.NoError(t, err)
	assert.Equal(t, 1, f.sink.resources(), "나가지 못한 자원은 다음 턴에 다시 나간다")
}

// 화면을 새로 고쳐도 가장 급한 판정을 받은 사람의 번호 차례는 그대로다.
func TestUrgentResourceOrderSurvivesAReconnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	f.turn(s, sayUrgent, crisis.StageUrgent, sayUrgent, "")
	require.Equal(t, "emergency_119", f.sink.lastResources(t).Items[1].ID)

	f.sink.reset()
	resumed := f.start()
	require.True(t, resumed.ResourcesPinned())

	items := f.sink.lastResources(t).Items
	require.Len(t, items, 3)
	assert.Equal(t, "suicide_prevention_109", items[0].ID)
	assert.Equal(t, "emergency_119", items[1].ID, "보통의 차례로 돌아가지 않는다")
}

// 사용자가 그날의 기록을 다른 화면에서 통째로 지우면 대화 행도 함께 사라진다.
// 그때 열려 있던 소켓은 "끝난 대화"를 받아야 한다. 일반 오류로 알리면 화면에는 다시 눌러도 되지 않는 단추만 남는다.
func TestDeletingTheDayEndsAnOpenConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	date, err := store.PGDate(s.RecordDate())
	require.NoError(t, err)
	_, err = f.store.Queries().DeleteDayByDate(t.Context(), db.DeleteDayByDateParams{
		UserID: f.userID, RecordDate: date,
	})
	require.NoError(t, err)

	f.judges(crisis.StageNone, "")
	f.remember(sayOrdinary)
	_, err = f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayOrdinary})
	require.Error(t, err)
	require.ErrorIs(t, err, engine.ErrConversationEnded, "없어진 대화는 끝난 대화로 알린다")
	assert.NotContains(t, strings.ToLower(f.logs.String()), "panic")
}
