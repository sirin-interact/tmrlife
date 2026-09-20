package store_test

import (
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

func openConversation(t *testing.T, st *store.Store, userID uuid.UUID, d recorddate.Date, now time.Time) db.Conversation {
	t.Helper()
	conversation, err := st.OpenConversation(t.Context(), store.NewConversation{
		ID: newID(t), NewDayID: newID(t), UserID: userID, RecordDate: d, StartedMode: store.ModeChat, Now: now,
	})
	require.NoError(t, err)
	return conversation
}

func endConversation(t *testing.T, st *store.Store, c db.Conversation, now time.Time) db.Conversation {
	t.Helper()
	ended, err := st.Queries().EndConversation(t.Context(), db.EndConversationParams{
		ID: c.ID, UserID: c.UserID, EndReason: store.EndReasonUser, ProcessingStatus: store.ProcessingPending, Now: now,
	})
	require.NoError(t, err)
	return ended
}

func appendUtterance(t *testing.T, st *store.Store, c db.Conversation, speaker string, now time.Time) db.Utterance {
	t.Helper()
	origin := store.OriginUser
	if speaker == store.SpeakerAI {
		origin = store.OriginModel
	}
	appended, err := st.AppendUtterance(t.Context(), store.NewUtterance{
		ID: newID(t), ConversationID: c.ID, UserID: c.UserID,
		Speaker: speaker, Modality: store.ModeChat, Origin: origin, TextEnc: fakeCiphertext, Now: now,
	})
	require.NoError(t, err)
	require.False(t, appended.Duplicate)
	return appended.Utterance
}

func TestOpenConversation(t *testing.T) {
	t.Parallel()
	today := recordDate(t, 2026, time.September, 20)

	t.Run("하루를 만들고 그 위에 열린 대화를 만든다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")

		conversation := openConversation(t, st, mina.ID, today, baseTime)
		assert.Equal(t, store.ConversationActive, conversation.Status)
		assert.Equal(t, store.ModeChat, conversation.StartedMode)
		assert.Equal(t, store.CheckStateNone, conversation.CheckState)
		assert.Equal(t, store.ProcessingNone, conversation.ProcessingStatus)
		assert.Nil(t, conversation.EndedAt)
		assert.Nil(t, conversation.EndReason)
		assertInstant(t, baseTime, conversation.StartedAt)
		assert.Equal(t, 1, count(t, pool, "days", "user_id", mina.ID))

		got, err := st.Queries().GetActiveConversation(t.Context(), mina.ID)
		require.NoError(t, err)
		assert.Equal(t, conversation.ID, got.Conversation.ID)
		gotDate, err := store.RecordDate(got.RecordDate)
		require.NoError(t, err)
		assert.Equal(t, today, gotDate, "기록 날짜가 지난 대화인지 가리려면 대화와 함께 날짜가 나와야 한다")
	})

	t.Run("열린 대화가 있으면 새로 열지 못한다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		first := openConversation(t, st, mina.ID, today, baseTime)

		_, err := st.OpenConversation(t.Context(), store.NewConversation{
			ID: newID(t), NewDayID: newID(t), UserID: mina.ID,
			RecordDate: today.AddDays(1), StartedMode: store.ModeChat, Now: baseTime.Add(24 * time.Hour),
		})
		require.ErrorIs(t, err, store.ErrActiveConversationExists)
		require.ErrorIs(t, err, store.ErrConflict)

		assert.Equal(t, 1, count(t, pool, "conversations", "user_id", mina.ID))
		assert.Equal(t, 1, count(t, pool, "days", "user_id", mina.ID), "실패한 시도가 만든 하루는 함께 되돌려진다")
		got, err := st.Queries().GetActiveConversation(t.Context(), mina.ID)
		require.NoError(t, err)
		assert.Equal(t, first.ID, got.Conversation.ID)
	})

	t.Run("끝낸 뒤에는 같은 날에 다시 열 수 있고 같은 하루에 매달린다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		first := openConversation(t, st, mina.ID, today, baseTime)
		endConversation(t, st, first, baseTime.Add(10*time.Minute))

		second := openConversation(t, st, mina.ID, today, baseTime.Add(time.Hour))
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, first.DayID, second.DayID)
		assert.Equal(t, 1, count(t, pool, "days", "user_id", mina.ID))
	})

	t.Run("다른 사용자의 열린 대화는 서로 막지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		openConversation(t, st, mina.ID, today, baseTime)
		openConversation(t, st, joon.ID, today, baseTime)
	})

	t.Run("동시에 열어도 하나만 열린다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")

		const attempts = 8
		errs := make([]error, attempts)
		ids := make([][2]uuid.UUID, attempts)
		for i := range ids {
			ids[i] = [2]uuid.UUID{newID(t), newID(t)}
		}
		var wg sync.WaitGroup
		for i := range attempts {
			wg.Go(func() {
				_, errs[i] = st.OpenConversation(t.Context(), store.NewConversation{
					ID: ids[i][0], NewDayID: ids[i][1], UserID: mina.ID,
					RecordDate: today, StartedMode: store.ModeChat, Now: baseTime,
				})
			})
		}
		wg.Wait()

		opened := 0
		for _, err := range errs {
			if err == nil {
				opened++
				continue
			}
			require.ErrorIs(t, err, store.ErrActiveConversationExists)
		}
		assert.Equal(t, 1, opened)
		assert.Equal(t, 1, count(t, pool, "conversations", "user_id", mina.ID))
		assert.Equal(t, 1, count(t, pool, "days", "user_id", mina.ID))
	})

	t.Run("열린 대화가 없으면 찾지 못함이다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		_, err := st.Queries().GetActiveConversation(t.Context(), mina.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("빈 기록 날짜로는 열지 못한다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		_, err := st.OpenConversation(t.Context(), store.NewConversation{
			ID: newID(t), NewDayID: newID(t), UserID: mina.ID, StartedMode: store.ModeChat, Now: baseTime,
		})
		require.ErrorIs(t, err, recorddate.ErrInvalid)
		assert.Zero(t, count(t, pool, "days", "user_id", mina.ID))
	})
}

func TestGetConversation(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	today := recordDate(t, 2026, time.September, 20)
	conversation := openConversation(t, st, mina.ID, today, baseTime)

	t.Run("자기 대화는 기록 날짜와 함께 나온다", func(t *testing.T) {
		got, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: mina.ID})
		require.NoError(t, err)
		assert.Equal(t, conversation.ID, got.Conversation.ID)
		gotDate, err := store.RecordDate(got.RecordDate)
		require.NoError(t, err)
		assert.Equal(t, today, gotDate)
	})

	t.Run("남의 대화는 없는 것과 같다", func(t *testing.T) {
		_, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: joon.ID})
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestEndConversation(t *testing.T) {
	t.Parallel()
	today := recordDate(t, 2026, time.September, 20)

	t.Run("열린 대화를 끝내고, 두 번째로 끝내려는 쪽은 찾지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		endedAt := baseTime.Add(10 * time.Minute)

		ended := endConversation(t, st, conversation, endedAt)
		assert.Equal(t, store.ConversationEnded, ended.Status)
		require.NotNil(t, ended.EndReason)
		assert.Equal(t, store.EndReasonUser, *ended.EndReason)
		require.NotNil(t, ended.EndedAt)
		assertInstant(t, endedAt, *ended.EndedAt)
		assert.Equal(t, store.ProcessingPending, ended.ProcessingStatus)

		// 끝내기 버튼과 무응답 타이머가 겹친 경우다. 일기 초안 작업은 행을 돌려받은 쪽만 등록한다.
		_, err := st.Queries().EndConversation(t.Context(), db.EndConversationParams{
			ID: conversation.ID, UserID: mina.ID, EndReason: store.EndReasonIdle,
			ProcessingStatus: store.ProcessingPending, Now: endedAt.Add(time.Second),
		})
		require.ErrorIs(t, err, store.ErrNotFound)

		got, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: mina.ID})
		require.NoError(t, err)
		require.NotNil(t, got.Conversation.EndReason)
		assert.Equal(t, store.EndReasonUser, *got.Conversation.EndReason, "먼저 끝낸 쪽의 이유가 남는다")
	})

	t.Run("남의 대화는 끝내지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)

		_, err := st.Queries().EndConversation(t.Context(), db.EndConversationParams{
			ID: conversation.ID, UserID: joon.ID, EndReason: store.EndReasonUser,
			ProcessingStatus: store.ProcessingNone, Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = st.Queries().GetActiveConversation(t.Context(), mina.ID)
		require.NoError(t, err)
	})

	t.Run("조용했던 때에만 끝내라고 하면 그 뒤에 발화가 있는 대화는 끝내지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		disconnectedAt := baseTime.Add(5 * time.Minute)
		appendUtterance(t, st, conversation, store.SpeakerUser, disconnectedAt.Add(-time.Minute))

		end := func(now time.Time) error {
			_, err := st.Queries().EndConversation(t.Context(), db.EndConversationParams{
				ID: conversation.ID, UserID: mina.ID, EndReason: store.EndReasonIdle,
				ProcessingStatus: store.ProcessingPending, Now: now, QuietSince: &disconnectedAt,
			})
			return err
		}

		// 연결이 끊긴 뒤에 다른 연결로 돌아와 이야기를 이어갔다.
		appendUtterance(t, st, conversation, store.SpeakerUser, disconnectedAt.Add(10*time.Minute))
		require.ErrorIs(t, end(disconnectedAt.Add(30*time.Minute)), store.ErrNotFound)
		_, err := st.Queries().GetActiveConversation(t.Context(), mina.ID)
		require.NoError(t, err, "이어진 대화는 열린 채로 남는다")
	})

	t.Run("조용했던 때에만 끝내라고 했고 그 뒤로 발화가 없으면 끝낸다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		disconnectedAt := baseTime.Add(5 * time.Minute)
		// 경계의 발화는 끊기기 전의 것이다.
		appendUtterance(t, st, conversation, store.SpeakerUser, disconnectedAt)

		ended, err := st.Queries().EndConversation(t.Context(), db.EndConversationParams{
			ID: conversation.ID, UserID: mina.ID, EndReason: store.EndReasonIdle,
			ProcessingStatus: store.ProcessingPending, Now: disconnectedAt.Add(30 * time.Minute), QuietSince: &disconnectedAt,
		})
		require.NoError(t, err)
		assert.Equal(t, store.ConversationEnded, ended.Status)
	})
}

func TestAdvanceConversationCheckState(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	today := recordDate(t, 2026, time.September, 20)
	conversation := openConversation(t, st, mina.ID, today, baseTime)

	setState := func(t *testing.T, state string) {
		t.Helper()
		_, err := pool.Exec(t.Context(), `UPDATE conversations SET check_state = $1 WHERE id = $2`, state, conversation.ID)
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		from, to string
		accepted bool
	}{
		{"처음 애매한 표현이 나오면 되물었다고 적는다", store.CheckStateNone, store.CheckStateReflected, true},
		{"되물은 뒤에 다시 나오면 직접 물었다고 적는다", store.CheckStateReflected, store.CheckStateAsked, true},
		{"같은 값을 다시 적는 것은 받아 준다", store.CheckStateReflected, store.CheckStateReflected, true},
		{"건너뛰어 앞으로 가는 것은 받아 준다", store.CheckStateNone, store.CheckStateAsked, true},
		{"직접 물은 뒤에는 되물음으로 돌아가지 않는다", store.CheckStateAsked, store.CheckStateReflected, false},
		{"직접 물은 뒤에는 처음으로 돌아가지 않는다", store.CheckStateAsked, store.CheckStateNone, false},
		{"되물은 뒤에는 처음으로 돌아가지 않는다", store.CheckStateReflected, store.CheckStateNone, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setState(t, tt.from)
			got, err := st.Queries().AdvanceConversationCheckState(t.Context(), db.AdvanceConversationCheckStateParams{
				ID: conversation.ID, UserID: mina.ID, CheckState: tt.to,
			})
			if tt.accepted {
				require.NoError(t, err)
				assert.Equal(t, tt.to, got.CheckState)
				return
			}
			require.ErrorIs(t, err, store.ErrNotFound)
			current, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: mina.ID})
			require.NoError(t, err)
			assert.Equal(t, tt.from, current.Conversation.CheckState, "한 대화에서 직접 묻기가 두 번 나가지 않게 하는 값이다")
		})
	}

	t.Run("모르는 값은 받지 않는다", func(t *testing.T) {
		setState(t, store.CheckStateNone)
		_, err := st.Queries().AdvanceConversationCheckState(t.Context(), db.AdvanceConversationCheckStateParams{
			ID: conversation.ID, UserID: mina.ID, CheckState: "confirmed",
		})
		require.Error(t, err)
	})

	t.Run("끝난 대화의 상태는 바꾸지 못한다", func(t *testing.T) {
		setState(t, store.CheckStateNone)
		endConversation(t, st, conversation, baseTime.Add(time.Minute))
		_, err := st.Queries().AdvanceConversationCheckState(t.Context(), db.AdvanceConversationCheckStateParams{
			ID: conversation.ID, UserID: mina.ID, CheckState: store.CheckStateReflected,
		})
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestSetConversationProcessingStatus(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	conversation := openConversation(t, st, mina.ID, recordDate(t, 2026, time.September, 20), baseTime)
	endConversation(t, st, conversation, baseTime.Add(time.Minute))

	rows, err := st.Queries().SetConversationProcessingStatus(t.Context(), db.SetConversationProcessingStatusParams{
		ID: conversation.ID, UserID: mina.ID, ProcessingStatus: store.ProcessingDone,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	rows, err = st.Queries().SetConversationProcessingStatus(t.Context(), db.SetConversationProcessingStatusParams{
		ID: conversation.ID, UserID: joon.ID, ProcessingStatus: store.ProcessingFailed,
	})
	require.NoError(t, err)
	assert.Zero(t, rows, "남의 대화는 건드리지 못한다")

	got, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: mina.ID})
	require.NoError(t, err)
	assert.Equal(t, store.ProcessingDone, got.Conversation.ProcessingStatus)
}

func TestListStaleActiveConversations(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	today := recordDate(t, 2026, time.September, 20)
	cutoff := baseTime.Add(time.Hour)

	// 오래전에 시작했고 그 뒤로 말이 없다.
	quiet := openConversation(t, st, createUser(t, st, "quiet@example.com").ID, today, baseTime)
	appendUtterance(t, st, quiet, store.SpeakerUser, baseTime.Add(time.Minute))
	// 오래전에 시작했지만 방금까지 이야기했다.
	talking := openConversation(t, st, createUser(t, st, "talking@example.com").ID, today, baseTime)
	appendUtterance(t, st, talking, store.SpeakerUser, cutoff)
	// 방금 시작했다.
	openConversation(t, st, createUser(t, st, "fresh@example.com").ID, today, cutoff)
	// 오래전에 시작했고 이미 끝났다.
	ended := openConversation(t, st, createUser(t, st, "ended@example.com").ID, today, baseTime)
	endConversation(t, st, ended, baseTime.Add(time.Minute))
	// 발화가 하나도 없이 오래 열려 있다.
	empty := openConversation(t, st, createUser(t, st, "empty@example.com").ID, today, baseTime.Add(time.Second))

	rows, err := st.Queries().ListStaleActiveConversations(t.Context(), db.ListStaleActiveConversationsParams{
		IdleBefore: cutoff, MaxRows: 10,
	})
	require.NoError(t, err)
	got := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.ID)
	}
	assert.Equal(t, []uuid.UUID{quiet.ID, empty.ID}, got, "시작한 순서대로, 조용한 열린 대화만 나온다")
	assert.Equal(t, quiet.UserID, rows[0].UserID)
	assert.Equal(t, quiet.DayID, rows[0].DayID)

	limited, err := st.Queries().ListStaleActiveConversations(t.Context(), db.ListStaleActiveConversationsParams{
		IdleBefore: cutoff, MaxRows: 1,
	})
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestListConversationsByDay(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	today := recordDate(t, 2026, time.September, 20)

	calm := openConversation(t, st, mina.ID, today, baseTime)
	insertGateEvent(t, st, calm, appendUtterance(t, st, calm, store.SpeakerUser, baseTime), 1, baseTime)
	endConversation(t, st, calm, baseTime.Add(time.Minute))

	heavy := openConversation(t, st, mina.ID, today, baseTime.Add(time.Hour))
	insertGateEvent(t, st, heavy, appendUtterance(t, st, heavy, store.SpeakerUser, baseTime.Add(time.Hour)), 2, baseTime.Add(time.Hour))

	rows, err := st.Queries().ListConversationsByDay(t.Context(), db.ListConversationsByDayParams{DayID: calm.DayID, UserID: mina.ID})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, calm.ID, rows[0].Conversation.ID)
	assert.False(t, rows[0].Crisis, "확인 단계만 있었던 대화는 평소처럼 일기 초안을 만든다")
	assert.Equal(t, heavy.ID, rows[1].Conversation.ID)
	assert.True(t, rows[1].Crisis)

	others, err := st.Queries().ListConversationsByDay(t.Context(), db.ListConversationsByDayParams{DayID: calm.DayID, UserID: joon.ID})
	require.NoError(t, err)
	assert.Empty(t, others)
}

var quotedLiteral = regexp.MustCompile(`'([a-z_]+)'`)

// checkValues는 CHECK 제약이 받아 주는 열거 값을 돌려준다.
func checkValues(t *testing.T, pool *pgxpool.Pool, constraint string) []string {
	t.Helper()
	var def string
	err := pool.QueryRow(t.Context(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`, constraint).Scan(&def)
	require.NoError(t, err, constraint)
	var values []string
	for _, m := range quotedLiteral.FindAllStringSubmatch(def, -1) {
		values = append(values, m[1])
	}
	return values
}

func TestEnumConstantsMatchTheSchema(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)

	tests := []struct {
		constraint string
		constants  []string
	}{
		{"conversations_status_check", []string{store.ConversationActive, store.ConversationEnded, store.ConversationAbandoned}},
		{"conversations_started_mode_check", []string{store.ModeVoice, store.ModeChat}},
		{"conversations_check_state_check", []string{store.CheckStateNone, store.CheckStateReflected, store.CheckStateAsked}},
		{"conversations_end_reason_check", []string{store.EndReasonUser, store.EndReasonIdle, store.EndReasonCrisis, store.EndReasonError}},
		{"conversations_processing_status_check", []string{
			store.ProcessingNone, store.ProcessingPending, store.ProcessingRunning, store.ProcessingDone, store.ProcessingFailed,
		}},
		{"utterances_speaker_check", []string{store.SpeakerUser, store.SpeakerAI}},
		{"utterances_modality_check", []string{store.ModeVoice, store.ModeChat}},
		{"utterances_origin_check", []string{store.OriginUser, store.OriginModel, store.OriginFixed, store.OriginTemplate}},
		{"diaries_status_check", []string{store.DiaryDraft, store.DiaryConfirmed}},
	}
	for _, tt := range tests {
		t.Run(tt.constraint, func(t *testing.T) {
			assert.ElementsMatch(t, tt.constants, checkValues(t, pool, tt.constraint))
		})
	}
}
