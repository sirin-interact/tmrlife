package engine_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 같은 글을 다시 보내는 경우다. 클라이언트는 받았다는 확인을 받지 못하면 같은 식별자로 다시 보낸다.
// 먼저 저장된 발화의 처리가 어디까지 갔는지에 따라 세 갈래다.
func TestResend(t *testing.T) {
	t.Parallel()

	t.Run("끝난 턴이면 그때 나간 말을 다시 내보낸다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		id := newID(t)
		f.judges(crisis.StageNone, "")
		f.replies(fake.Reply(replyOrdinary))
		first, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayOrdinary})
		require.NoError(t, err)
		assert.False(t, first.Duplicate)

		f.sink.reset()
		again, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayOrdinary})
		require.NoError(t, err)
		assert.True(t, again.Duplicate)
		assert.True(t, again.Replayed)
		assert.Equal(t, first.Seq, again.Seq)

		assert.Equal(t, []string{replyOrdinary}, f.sink.texts(), "같은 말이 한 번 더 나간다")
		assert.Len(t, f.storedUtterances(s.ConversationID()), 3, "저장된 말은 늘지 않는다")
		assert.Len(t, f.gateEvents(s.ConversationID()), 1, "관문 기록도 늘지 않는다")
		assert.Equal(t, 1, f.talk.Calls(), "대화 모델을 다시 부르지 않는다")
	})

	t.Run("판정도 답도 없으면 그 발화로 턴을 처음부터 돌린다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		// 발화만 저장하고 판정을 남기기 전에 끊긴 턴을 그대로 만든다.
		id := newID(t)
		stored := f.appendUser(s, id, sayOrdinary)

		f.judges(crisis.StageNone, "")
		f.replies(fake.Reply(replyOrdinary))
		turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayOrdinary})
		require.NoError(t, err)
		assert.True(t, turn.Duplicate)
		assert.False(t, turn.Replayed)
		assert.Equal(t, stored.Seq, turn.Seq)

		assert.Equal(t, []string{replyOrdinary}, f.sink.texts()[1:], "첫 안부 다음에 이 턴의 답이 나간다")
		events := f.gateEvents(s.ConversationID())
		require.Len(t, events, 1, "다시 돈 턴이 판정을 남긴다")
		assert.Equal(t, stored.ID, events[0].UtteranceID)
	})

	t.Run("판정만 있고 답이 없으면 그 판정으로 답만 다시 만든다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		id := newID(t)
		stored := f.appendUser(s, id, sayCrisis)
		f.insertGateEvent(s, stored.ID, crisis.StageRespond)

		turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayCrisis})
		require.NoError(t, err)
		assert.True(t, turn.Duplicate)
		assert.Equal(t, crisis.StageRespond, turn.Stage, "저장된 판정을 그대로 쓴다")
		assert.Equal(t, phrases.CrisisRespond, f.sink.lastText(t).Phrase)
		assert.Equal(t, 1, f.sink.resources())

		assert.Len(t, f.gateEvents(s.ConversationID()), 1, "판정을 두 번 남기지 않는다")
		assert.Zero(t, f.judge.Calls(), "판별을 다시 부르지 않는다")
	})

	t.Run("그 뒤로 대화가 흘러갔으면 다시 답하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		id := newID(t)
		stored := f.appendUser(s, id, sayOrdinary)
		f.insertGateEvent(s, stored.ID, crisis.StageNone)
		// 그 발화 뒤에 사용자의 다음 글이 이미 들어와 있다.
		f.appendUser(s, newID(t), sayTired)

		f.sink.reset()
		turn, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: id, Text: sayOrdinary})
		require.NoError(t, err)
		assert.True(t, turn.Duplicate)
		assert.Empty(t, f.sink.texts(), "지난 자리에 말을 끼워 넣지 않는다")
		assert.Zero(t, f.talk.Calls())
	})
}

// appendUser는 엔진을 거치지 않고 사용자의 발화를 저장한다. 턴이 중간에 끊긴 상태를 만들 때 쓴다.
func (f *fixture) appendUser(s *engine.Session, clientMessageID uuid.UUID, text string) db.Utterance {
	f.t.Helper()
	f.remember(text)
	id := newID(f.t)
	sealed, err := f.sealer.SealString(text, sealing.UtteranceText(id))
	require.NoError(f.t, err)
	appended, err := f.store.AppendUtterance(f.t.Context(), store.NewUtterance{
		ID: id, ConversationID: s.ConversationID(), UserID: f.userID,
		Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
		TextEnc: sealed, ClientMessageID: &clientMessageID, Now: f.clock.Now(),
	})
	require.NoError(f.t, err)
	return appended.Utterance
}

// insertGateEvent는 엔진을 거치지 않고 관문 기록을 남긴다.
func (f *fixture) insertGateEvent(s *engine.Session, utteranceID uuid.UUID, stage crisis.Stage) {
	f.t.Helper()
	ruleStage := int16(stage)
	_, err := f.store.Queries().InsertGateEvent(f.t.Context(), db.InsertGateEventParams{
		ID: newID(f.t), UserID: f.userID, ConversationID: s.ConversationID(), UtteranceID: utteranceID,
		RuleStage: &ruleStage, FinalStage: int16(stage), DetectedBy: "rule",
		Adjustments: []string{}, Now: f.clock.Now(),
	})
	require.NoError(f.t, err)
}
