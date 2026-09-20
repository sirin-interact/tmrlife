package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func insertGateEvent(t *testing.T, st *store.Store, c db.Conversation, u db.Utterance, stage int16, now time.Time) db.GateEvent {
	t.Helper()
	params := db.InsertGateEventParams{
		ID: newID(t), UserID: c.UserID, ConversationID: c.ID, UtteranceID: u.ID,
		RuleStage: &stage, AIStage: &stage, FinalStage: stage,
		DetectedBy: "none", Adjustments: []string{}, Now: now,
	}
	if stage >= 1 {
		params.DetectedBy = "both"
		params.EvidenceEnc = fakeCiphertext
	}
	event, err := st.Queries().InsertGateEvent(t.Context(), params)
	require.NoError(t, err)
	return event
}

func TestInsertGateEvent(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	conversation := openConversation(t, st, mina.ID, recordDate(t, 2026, time.September, 20), baseTime)

	t.Run("AI 판별이 답하지 못한 판정도 그대로 남는다", func(t *testing.T) {
		utterance := appendUtterance(t, st, conversation, store.SpeakerUser, baseTime)
		ruleStage := int16(1)
		latency := int32(2500)
		event, err := st.Queries().InsertGateEvent(t.Context(), db.InsertGateEventParams{
			ID: newID(t), UserID: mina.ID, ConversationID: conversation.ID, UtteranceID: utterance.ID,
			RuleStage: &ruleStage, AIStage: nil, FinalStage: 1, DetectedBy: "rule",
			Adjustments: []string{"ai_failed_floor"}, AIFailed: true, AILatencyMs: &latency,
			EvidenceEnc: fakeCiphertext, Now: baseTime,
		})
		require.NoError(t, err)
		assert.Nil(t, event.AIStage)
		assert.True(t, event.AIFailed)
		assert.Equal(t, []string{"ai_failed_floor"}, event.Adjustments)
		require.NotNil(t, event.AILatencyMs)
		assert.EqualValues(t, 2500, *event.AILatencyMs)
		assertInstant(t, baseTime, event.CreatedAt)
	})

	t.Run("해당 없음도 남기되 근거 발화는 남기지 못한다", func(t *testing.T) {
		utterance := appendUtterance(t, st, conversation, store.SpeakerUser, baseTime)
		zero := int16(0)
		params := db.InsertGateEventParams{
			ID: newID(t), UserID: mina.ID, ConversationID: conversation.ID, UtteranceID: utterance.ID,
			RuleStage: &zero, AIStage: &zero, FinalStage: 0, DetectedBy: "none", Adjustments: []string{},
			EvidenceEnc: fakeCiphertext, Now: baseTime,
		}
		_, err := st.Queries().InsertGateEvent(t.Context(), params)
		requireViolation(t, err, sqlStateCheckViolation, "gate_events_evidence_check")

		params.EvidenceEnc = nil
		event, err := st.Queries().InsertGateEvent(t.Context(), params)
		require.NoError(t, err)
		assert.Nil(t, event.EvidenceEnc)
		assert.Empty(t, event.Adjustments)
	})

	t.Run("같은 발화의 판정은 두 번 남지 않는다", func(t *testing.T) {
		utterance := appendUtterance(t, st, conversation, store.SpeakerUser, baseTime)
		insertGateEvent(t, st, conversation, utterance, 0, baseTime)
		zero := int16(0)
		_, err := st.Queries().InsertGateEvent(t.Context(), db.InsertGateEventParams{
			ID: newID(t), UserID: mina.ID, ConversationID: conversation.ID, UtteranceID: utterance.ID,
			RuleStage: &zero, AIStage: &zero, FinalStage: 0, DetectedBy: "none", Adjustments: []string{}, Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrConflict)
		assert.Equal(t, "gate_events_utterance_id_key", store.ConstraintName(err))
	})
}

func TestGetGateEventByUtterance(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	conversation := openConversation(t, st, mina.ID, recordDate(t, 2026, time.September, 20), baseTime)
	judged := appendUtterance(t, st, conversation, store.SpeakerUser, baseTime)
	event := insertGateEvent(t, st, conversation, judged, 1, baseTime)
	// 발화만 저장되고 판정은 남지 않았다. 처리 도중에 프로세스가 죽은 경우다.
	unjudged := appendUtterance(t, st, conversation, store.SpeakerUser, baseTime)

	got, err := st.Queries().GetGateEventByUtterance(t.Context(), db.GetGateEventByUtteranceParams{UtteranceID: judged.ID, UserID: mina.ID})
	require.NoError(t, err)
	assert.Equal(t, event.ID, got.ID)
	assert.EqualValues(t, 1, got.FinalStage)

	_, err = st.Queries().GetGateEventByUtterance(t.Context(), db.GetGateEventByUtteranceParams{UtteranceID: unjudged.ID, UserID: mina.ID})
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = st.Queries().GetGateEventByUtterance(t.Context(), db.GetGateEventByUtteranceParams{UtteranceID: judged.ID, UserID: joon.ID})
	require.ErrorIs(t, err, store.ErrNotFound, "남의 판정은 없는 것과 같다")
}

func TestListFlaggedGateEventsSince(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	today := recordDate(t, 2026, time.September, 20)
	since := baseTime

	// 지난 대화 하나와 지금 대화 하나에 걸쳐 판정을 남긴다.
	earlier := openConversation(t, st, mina.ID, today.AddDays(-20), since.Add(-20*24*time.Hour))
	tooOld := insertGateEvent(t, st, earlier, appendUtterance(t, st, earlier, store.SpeakerUser, since.Add(-time.Second)), 2, since.Add(-time.Second))
	endConversation(t, st, earlier, since.Add(-time.Second))

	current := openConversation(t, st, mina.ID, today, since)
	onBoundary := insertGateEvent(t, st, current, appendUtterance(t, st, current, store.SpeakerUser, since), 1, since)
	insertGateEvent(t, st, current, appendUtterance(t, st, current, store.SpeakerUser, since.Add(time.Minute)), 0, since.Add(time.Minute))
	later := insertGateEvent(t, st, current, appendUtterance(t, st, current, store.SpeakerUser, since.Add(2*time.Minute)), 2, since.Add(2*time.Minute))

	joonConversation := openConversation(t, st, joon.ID, today, since)
	insertGateEvent(t, st, joonConversation, appendUtterance(t, st, joonConversation, store.SpeakerUser, since), 3, since)

	rows, err := st.Queries().ListFlaggedGateEventsSince(t.Context(), db.ListFlaggedGateEventsSinceParams{UserID: mina.ID, Since: since})
	require.NoError(t, err)
	require.Len(t, rows, 2, "기간 밖의 판정(%s), 해당 없음, 남의 판정은 나오지 않는다", tooOld.ID)

	assert.Equal(t, current.ID, rows[0].ConversationID)
	assert.EqualValues(t, 1, rows[0].FinalStage)
	assertInstant(t, onBoundary.CreatedAt, rows[0].CreatedAt)
	assert.Equal(t, current.ID, rows[1].ConversationID)
	assert.EqualValues(t, 2, rows[1].FinalStage)
	assertInstant(t, later.CreatedAt, rows[1].CreatedAt)

	all, err := st.Queries().ListFlaggedGateEventsSince(t.Context(), db.ListFlaggedGateEventsSinceParams{
		UserID: mina.ID, Since: since.Add(-30 * 24 * time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, earlier.ID, all[0].ConversationID, "쌓임은 대화를 단위로 세므로 어느 대화의 판정인지 함께 나온다")
}

func TestConversationHasCrisisGateEvent(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	conversation := openConversation(t, st, mina.ID, recordDate(t, 2026, time.September, 20), baseTime)

	has := func(userID uuid.UUID) bool {
		crisis, err := st.Queries().ConversationHasCrisisGateEvent(t.Context(), db.ConversationHasCrisisGateEventParams{
			ConversationID: conversation.ID, UserID: userID,
		})
		require.NoError(t, err)
		return crisis
	}

	assert.False(t, has(mina.ID), "판정이 하나도 없는 대화")

	insertGateEvent(t, st, conversation, appendUtterance(t, st, conversation, store.SpeakerUser, baseTime), 0, baseTime)
	insertGateEvent(t, st, conversation, appendUtterance(t, st, conversation, store.SpeakerUser, baseTime), 1, baseTime)
	assert.False(t, has(mina.ID), "확인 단계까지만 간 대화에서는 도움 자원을 고정하지 않는다")

	insertGateEvent(t, st, conversation, appendUtterance(t, st, conversation, store.SpeakerUser, baseTime), 2, baseTime)
	assert.True(t, has(mina.ID))
	assert.False(t, has(joon.ID), "남의 대화에 대해서는 알려주지 않는다")
}
