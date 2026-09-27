package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

func TestEnd(t *testing.T) {
	t.Parallel()

	t.Run("끝내면 일기 초안 작업이 같은 트랜잭션에서 등록된다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)
		require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))

		events := f.sink.all()
		ended, ok := events[len(events)-1].(engine.Ended)
		require.True(t, ok)
		assert.Equal(t, store.EndReasonUser, ended.Reason)
		assert.Equal(t, s.RecordDate(), ended.RecordDate)
		assert.True(t, ended.DiaryExpected)

		row := f.conversation(s.ConversationID())
		assert.Equal(t, store.ConversationEnded, row.Status)
		assert.Equal(t, store.ProcessingPending, row.ProcessingStatus)
		require.NotNil(t, row.EndedAt)

		calls := f.diary.calls()
		require.Len(t, calls, 1)
		assert.Equal(t, s.ConversationID(), calls[0].conversationID)
		assert.Equal(t, s.DayID(), calls[0].dayID)
		assert.Equal(t, f.userID, calls[0].userID)

		extractions := f.analysis.calls()
		require.Len(t, extractions, 1, "신호 추출 작업도 같은 트랜잭션에서 등록된다")
		assert.Equal(t, s.ConversationID(), extractions[0].conversationID)
		assert.Equal(t, f.userID, extractions[0].userID)
	})

	t.Run("대응 단계가 있었던 대화는 초안을 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")
		require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))

		events := f.sink.all()
		ended, ok := events[len(events)-1].(engine.Ended)
		require.True(t, ok)
		assert.False(t, ended.DiaryExpected)

		row := f.conversation(s.ConversationID())
		assert.Equal(t, store.ConversationEnded, row.Status)
		assert.Equal(t, store.ProcessingNone, row.ProcessingStatus, "초안 작업이 없으면 기다리는 상태로 두지 않는다")
		assert.Empty(t, f.diary.calls())

		// 신호는 그런 대화에서도 뽑는다. 그 하루를 비워 두면 가장 무거운 날이 "대화하지 않은 날"이 되어
		// 점수를 나누는 일수까지 줄어든다. 까닭은 closer.end에 적혀 있다.
		assert.Len(t, f.analysis.calls(), 1, "위기 대응이 있었던 대화에서도 신호는 뽑는다")
	})

	t.Run("두 번 끝내지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))
		require.ErrorIs(t, f.engine.End(t.Context(), s, store.EndReasonUser), engine.ErrConversationEnded)
		assert.Len(t, f.diary.calls(), 1, "작업이 두 번 등록되지 않는다")
		assert.Len(t, f.analysis.calls(), 1, "작업이 두 번 등록되지 않는다")
	})

	t.Run("끝난 대화에는 말을 더할 수 없다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonUser))
		_, err := f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayOrdinary})
		assert.ErrorIs(t, err, engine.ErrConversationEnded)
	})

	t.Run("모르는 사유는 받지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		require.Error(t, f.engine.End(t.Context(), s, "bored"))
		assert.Equal(t, store.ConversationActive, f.conversation(s.ConversationID()).Status)
	})

	t.Run("다른 쪽이 먼저 끝냈으면 끝난 것으로 받는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		sweeper := f.newSweeper(time.Minute)
		f.clock.Advance(2 * time.Minute)
		result, err := sweeper.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, result.Ended)

		require.ErrorIs(t, f.engine.End(t.Context(), s, store.EndReasonUser), engine.ErrConversationEnded)
		assert.Len(t, f.diary.calls(), 1, "작업은 대화를 끝낸 쪽만 등록한다")
		assert.Len(t, f.analysis.calls(), 1, "쓸어 담는 쪽도 신호 추출 작업을 등록한다")

		_, err = f.engine.Handle(t.Context(), s, engine.Say{ClientMessageID: newID(t), Text: sayOrdinary})
		assert.ErrorIs(t, err, engine.ErrConversationEnded, "닫힌 대화에는 말이 더해지지 않는다")
	})
}

func TestNudge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	s := f.start()
	require.NoError(t, f.engine.Nudge(t.Context(), s))

	text := f.sink.lastText(t)
	assert.Equal(t, phrases.IdleCheck, text.Phrase)
	assert.Equal(t, store.OriginFixed, text.Origin)
	assert.Zero(t, f.talk.Calls(), "묻는 말은 모델을 거치지 않는다")

	stored := f.storedUtterances(s.ConversationID())
	require.Len(t, stored, 2, "화면에 보인 말은 대화 기록에도 남는다")
	assert.Equal(t, text.Text, stored[1].Text)

	require.NoError(t, f.engine.End(t.Context(), s, store.EndReasonIdle))
	assert.ErrorIs(t, f.engine.Nudge(t.Context(), s), engine.ErrConversationEnded)
}

func TestSweep(t *testing.T) {
	t.Parallel()

	t.Run("한동안 말이 없는 대화를 닫고 초안 작업을 등록한다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)

		sweeper := f.newSweeper(30 * time.Minute)
		f.clock.Advance(31 * time.Minute)
		result, err := sweeper.Sweep(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, result.Scanned)
		assert.Equal(t, 1, result.Ended)
		assert.Equal(t, 1, result.DiaryJobs)

		row := f.conversation(s.ConversationID())
		assert.Equal(t, store.ConversationEnded, row.Status)
		require.NotNil(t, row.EndReason)
		assert.Equal(t, store.EndReasonIdle, *row.EndReason)
		assert.Len(t, f.diary.calls(), 1)
	})

	t.Run("아직 말이 오가는 대화는 건드리지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		sweeper := f.newSweeper(30 * time.Minute)
		f.clock.Advance(31 * time.Minute)
		f.turn(s, sayOrdinary, crisis.StageNone, "", replyOrdinary)

		result, err := sweeper.Sweep(t.Context())
		require.NoError(t, err)
		assert.Zero(t, result.Scanned)
		assert.Equal(t, store.ConversationActive, f.conversation(s.ConversationID()).Status)
		assert.Empty(t, f.diary.calls())
	})

	t.Run("대응 단계가 있었던 대화는 닫되 초안 작업을 넣지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		s := f.start()
		f.turn(s, sayCrisis, crisis.StageRespond, sayCrisis, "")

		sweeper := f.newSweeper(30 * time.Minute)
		f.clock.Advance(31 * time.Minute)
		result, err := sweeper.Sweep(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, result.Ended)
		assert.Zero(t, result.DiaryJobs)
		assert.Equal(t, store.ProcessingNone, f.conversation(s.ConversationID()).ProcessingStatus)
	})

	t.Run("닫을 대화가 없으면 아무 일도 하지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)

		sweeper := f.newSweeper(30 * time.Minute)
		result, err := sweeper.Sweep(t.Context())
		require.NoError(t, err)
		assert.Equal(t, engine.SweepResult{}, result)
	})
}

// newSweeper는 시험용 쓸어 담기를 만든다.
func (f *fixture) newSweeper(idleAfter time.Duration) *engine.Sweeper {
	f.t.Helper()
	sweeper, err := engine.NewSweeper(engine.SweeperOptions{
		Store:     f.store,
		Diary:     f.diary,
		Analysis:  f.analysis,
		Clock:     f.clock,
		Logger:    f.logger,
		IdleAfter: idleAfter,
	})
	require.NoError(f.t, err)
	return sweeper
}
