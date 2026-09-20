package store_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func seqs(t *testing.T, pool *pgxpool.Pool, conversationID uuid.UUID) []int32 {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT seq FROM utterances WHERE conversation_id = $1 ORDER BY seq`, conversationID)
	require.NoError(t, err)
	values, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	require.NoError(t, err)
	return values
}

func TestAppendUtterance(t *testing.T) {
	t.Parallel()
	today := recordDate(t, 2026, time.September, 20)

	t.Run("순번은 0에서 시작해 하나씩 늘고, 넘긴 값이 그대로 저장된다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)

		opening, err := st.AppendUtterance(t.Context(), store.NewUtterance{
			ID: newID(t), ConversationID: conversation.ID, UserID: mina.ID,
			Speaker: store.SpeakerAI, Modality: store.ModeChat, Origin: store.OriginFixed,
			TextEnc: fakeCiphertext, Now: baseTime,
		})
		require.NoError(t, err)
		assert.EqualValues(t, 0, opening.Utterance.Seq)
		assert.Equal(t, store.OriginFixed, opening.Utterance.Origin)
		assert.Nil(t, opening.Utterance.ClientMessageID)

		id, clientID := newID(t), newID(t)
		confidence := float32(0.42)
		said, err := st.AppendUtterance(t.Context(), store.NewUtterance{
			ID: id, ConversationID: conversation.ID, UserID: mina.ID,
			Speaker: store.SpeakerUser, Modality: store.ModeVoice, Origin: store.OriginUser,
			TextEnc: []byte{9, 8, 7}, STTMinConfidence: &confidence, ClientMessageID: &clientID, Now: baseTime.Add(time.Second),
		})
		require.NoError(t, err)
		assert.False(t, said.Duplicate)
		assert.Equal(t, id, said.Utterance.ID)
		assert.EqualValues(t, 1, said.Utterance.Seq)
		assert.Equal(t, store.ModeVoice, said.Utterance.Modality)
		assert.Equal(t, []byte{9, 8, 7}, said.Utterance.TextEnc)
		require.NotNil(t, said.Utterance.STTMinConfidence)
		assert.InDelta(t, 0.42, *said.Utterance.STTMinConfidence, 1e-6)
		require.NotNil(t, said.Utterance.ClientMessageID)
		assert.Equal(t, clientID, *said.Utterance.ClientMessageID)
		assertInstant(t, baseTime.Add(time.Second), said.Utterance.CreatedAt)
	})

	t.Run("순번은 대화마다 따로 센다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		minaConversation := openConversation(t, st, mina.ID, today, baseTime)
		joonConversation := openConversation(t, st, joon.ID, today, baseTime)

		appendUtterance(t, st, minaConversation, store.SpeakerAI, baseTime)
		appendUtterance(t, st, minaConversation, store.SpeakerUser, baseTime)
		assert.EqualValues(t, 0, appendUtterance(t, st, joonConversation, store.SpeakerAI, baseTime).Seq)
		assert.EqualValues(t, 2, appendUtterance(t, st, minaConversation, store.SpeakerAI, baseTime).Seq)
	})

	t.Run("동시에 더해도 순번이 빈 데 없이, 겹치지 않고 이어진다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)

		// 접속 풀의 한도보다 많은 고루틴이 같은 대화에 몰린다. 잠금을 기다리는 쪽과 연결을 기다리는 쪽이 함께 생긴다.
		const writers, perWriter = 16, 25
		ids := make([][]uuid.UUID, writers)
		for w := range ids {
			ids[w] = make([]uuid.UUID, perWriter)
			for i := range ids[w] {
				ids[w][i] = newID(t)
			}
		}
		errs := make([]error, writers)
		got := make([][]int32, writers)

		var wg sync.WaitGroup
		for w := range writers {
			wg.Go(func() {
				speaker, origin := store.SpeakerUser, store.OriginUser
				if w%2 == 1 {
					speaker, origin = store.SpeakerAI, store.OriginModel
				}
				for i := range perWriter {
					appended, err := st.AppendUtterance(t.Context(), store.NewUtterance{
						ID: ids[w][i], ConversationID: conversation.ID, UserID: mina.ID,
						Speaker: speaker, Modality: store.ModeChat, Origin: origin, TextEnc: fakeCiphertext, Now: baseTime,
					})
					if err != nil {
						errs[w] = err
						return
					}
					got[w] = append(got[w], appended.Utterance.Seq)
				}
			})
		}
		wg.Wait()

		for w, err := range errs {
			require.NoError(t, err, "고루틴 %d: 순번이 겹치면 유일 제약에 걸려 여기서 드러난다", w)
		}

		want := make([]int32, writers*perWriter)
		for i := range want {
			want[i] = int32(i)
		}
		assert.Equal(t, want, seqs(t, pool, conversation.ID), "DB에 남은 순번")

		// 돌려받은 순번도 저장된 것과 같아야 한다. 한 고루틴 안에서는 뒤에 더한 발화의 순번이 더 크다.
		returned := make(map[int32]struct{}, len(want))
		for w := range got {
			for i, seq := range got[w] {
				returned[seq] = struct{}{}
				if i > 0 {
					assert.Greater(t, seq, got[w][i-1])
				}
			}
		}
		assert.Len(t, returned, len(want))
	})

	t.Run("같은 식별자로 다시 보낸 글은 한 번만 저장되고 먼저 저장된 발화가 나온다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		clientID := newID(t)

		send := func(now time.Time) store.AppendedUtterance {
			appended, err := st.AppendUtterance(t.Context(), store.NewUtterance{
				ID: newID(t), ConversationID: conversation.ID, UserID: mina.ID,
				Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
				TextEnc: fakeCiphertext, ClientMessageID: &clientID, Now: now,
			})
			require.NoError(t, err)
			return appended
		}

		first := send(baseTime)
		assert.False(t, first.Duplicate)
		appendUtterance(t, st, conversation, store.SpeakerAI, baseTime.Add(time.Second))

		again := send(baseTime.Add(2 * time.Second))
		assert.True(t, again.Duplicate)
		assert.Equal(t, first.Utterance.ID, again.Utterance.ID, "암호문은 먼저 저장된 행의 ID에 묶여 있으므로 그 행이 나와야 한다")
		assert.EqualValues(t, 0, again.Utterance.Seq)
		assertInstant(t, baseTime, again.Utterance.CreatedAt)
		assert.Equal(t, []int32{0, 1}, seqs(t, pool, conversation.ID), "다시 보낸 글이 순번을 쓰지 않는다")
	})

	t.Run("같은 글이 동시에 여러 번 와도 한 번만 저장된다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		clientID := newID(t)

		const senders = 12
		ids := make([]uuid.UUID, senders)
		for i := range ids {
			ids[i] = newID(t)
		}
		results := make([]store.AppendedUtterance, senders)
		errs := make([]error, senders)
		var wg sync.WaitGroup
		for i := range senders {
			wg.Go(func() {
				results[i], errs[i] = st.AppendUtterance(t.Context(), store.NewUtterance{
					ID: ids[i], ConversationID: conversation.ID, UserID: mina.ID,
					Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
					TextEnc: fakeCiphertext, ClientMessageID: &clientID, Now: baseTime,
				})
			})
		}
		wg.Wait()

		stored := 0
		for i, err := range errs {
			require.NoError(t, err, "보낸 쪽 %d", i)
			if !results[i].Duplicate {
				stored++
			}
			assert.Equal(t, results[0].Utterance.ID, results[i].Utterance.ID)
		}
		assert.Equal(t, 1, stored, "새로 저장했다고 들은 쪽은 하나뿐이어야 위기 관문이 한 번만 돈다")
		assert.Equal(t, []int32{0}, seqs(t, pool, conversation.ID))
	})

	t.Run("식별자는 대화 안에서만 견준다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		clientID := newID(t)

		for _, conversation := range []db.Conversation{
			openConversation(t, st, mina.ID, today, baseTime),
			openConversation(t, st, joon.ID, today, baseTime),
		} {
			appended, err := st.AppendUtterance(t.Context(), store.NewUtterance{
				ID: newID(t), ConversationID: conversation.ID, UserID: conversation.UserID,
				Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
				TextEnc: fakeCiphertext, ClientMessageID: &clientID, Now: baseTime,
			})
			require.NoError(t, err)
			assert.False(t, appended.Duplicate)
		}
	})

	t.Run("끝난 대화에는 더하지 못하지만 끝나기 전에 받은 글을 다시 보낸 것은 알아본다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		clientID := newID(t)
		sent := store.NewUtterance{
			ID: newID(t), ConversationID: conversation.ID, UserID: mina.ID,
			Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
			TextEnc: fakeCiphertext, ClientMessageID: &clientID, Now: baseTime,
		}
		first, err := st.AppendUtterance(t.Context(), sent)
		require.NoError(t, err)
		endConversation(t, st, conversation, baseTime.Add(time.Minute))

		late := sent
		late.ID, late.ClientMessageID = newID(t), nil
		_, err = st.AppendUtterance(t.Context(), late)
		require.ErrorIs(t, err, store.ErrConversationNotActive)

		otherID := newID(t)
		late.ClientMessageID = &otherID
		_, err = st.AppendUtterance(t.Context(), late)
		require.ErrorIs(t, err, store.ErrConversationNotActive)

		resent := sent
		resent.ID = newID(t)
		again, err := st.AppendUtterance(t.Context(), resent)
		require.NoError(t, err)
		assert.True(t, again.Duplicate)
		assert.Equal(t, first.Utterance.ID, again.Utterance.ID)
		assert.Equal(t, []int32{0}, seqs(t, pool, conversation.ID))
	})

	t.Run("없는 대화와 남의 대화는 구분 없이 찾지 못함이다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)

		for name, conversationID := range map[string]uuid.UUID{"남의 대화": conversation.ID, "없는 대화": newID(t)} {
			_, err := st.AppendUtterance(t.Context(), store.NewUtterance{
				ID: newID(t), ConversationID: conversationID, UserID: joon.ID,
				Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
				TextEnc: fakeCiphertext, Now: baseTime,
			})
			require.ErrorIs(t, err, store.ErrNotFound, name)
		}
		assert.Empty(t, seqs(t, pool, conversation.ID))
	})

	t.Run("AI의 말에는 클라이언트의 식별자를 붙이지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		clientID := newID(t)

		_, err := st.AppendUtterance(t.Context(), store.NewUtterance{
			ID: newID(t), ConversationID: conversation.ID, UserID: mina.ID,
			Speaker: store.SpeakerAI, Modality: store.ModeChat, Origin: store.OriginModel,
			TextEnc: fakeCiphertext, ClientMessageID: &clientID, Now: baseTime,
		})
		requireViolation(t, err, sqlStateCheckViolation, "utterances_client_message_id_check")
	})

	t.Run("부르는 쪽의 트랜잭션이 되돌려지면 발화도 순번도 남지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		conversation := openConversation(t, st, mina.ID, today, baseTime)
		appendUtterance(t, st, conversation, store.SpeakerAI, baseTime)

		err := st.InTx(t.Context(), func(q *db.Queries) error {
			appended, err := store.AppendUtterance(t.Context(), q, store.NewUtterance{
				ID: newID(t), ConversationID: conversation.ID, UserID: mina.ID,
				Speaker: store.SpeakerUser, Modality: store.ModeChat, Origin: store.OriginUser,
				TextEnc: fakeCiphertext, Now: baseTime,
			})
			require.NoError(t, err)
			assert.EqualValues(t, 1, appended.Utterance.Seq)
			return assert.AnError
		})
		require.ErrorIs(t, err, assert.AnError)

		assert.EqualValues(t, 1, appendUtterance(t, st, conversation, store.SpeakerUser, baseTime).Seq, "되돌려진 순번을 다음 발화가 받는다")
		assert.Equal(t, []int32{0, 1}, seqs(t, pool, conversation.ID))
	})
}

func TestListUtterances(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	today := recordDate(t, 2026, time.September, 20)

	morning := openConversation(t, st, mina.ID, today, baseTime)
	morningIDs := []uuid.UUID{
		appendUtterance(t, st, morning, store.SpeakerAI, baseTime).ID,
		appendUtterance(t, st, morning, store.SpeakerUser, baseTime.Add(time.Second)).ID,
		appendUtterance(t, st, morning, store.SpeakerAI, baseTime.Add(2*time.Second)).ID,
	}
	endConversation(t, st, morning, baseTime.Add(time.Minute))

	evening := openConversation(t, st, mina.ID, today, baseTime.Add(8*time.Hour))
	eveningIDs := []uuid.UUID{
		appendUtterance(t, st, evening, store.SpeakerAI, baseTime.Add(8*time.Hour)).ID,
		appendUtterance(t, st, evening, store.SpeakerUser, baseTime.Add(8*time.Hour+time.Second)).ID,
	}
	endConversation(t, st, evening, baseTime.Add(9*time.Hour))

	tomorrow := openConversation(t, st, mina.ID, today.AddDays(1), baseTime.Add(24*time.Hour))
	appendUtterance(t, st, tomorrow, store.SpeakerAI, baseTime.Add(24*time.Hour))

	ids := func(utterances []db.Utterance) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(utterances))
		for _, u := range utterances {
			out = append(out, u.ID)
		}
		return out
	}

	t.Run("대화의 발화는 순번대로 나온다", func(t *testing.T) {
		got, err := st.Queries().ListUtterancesByConversation(t.Context(), db.ListUtterancesByConversationParams{
			ConversationID: morning.ID, UserID: mina.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, morningIDs, ids(got))
	})

	t.Run("최근 발화는 가장 나중 것부터 정해진 수만큼 나온다", func(t *testing.T) {
		got, err := st.Queries().ListLastUtterances(t.Context(), db.ListLastUtterancesParams{
			ConversationID: morning.ID, UserID: mina.ID, MaxRows: 2,
		})
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{morningIDs[2], morningIDs[1]}, ids(got))
	})

	t.Run("하루의 발화는 대화를 시작한 순서, 그 안에서는 순번대로 나온다", func(t *testing.T) {
		got, err := st.Queries().ListUtterancesByDay(t.Context(), db.ListUtterancesByDayParams{DayID: morning.DayID, UserID: mina.ID})
		require.NoError(t, err)
		assert.Equal(t, append(append([]uuid.UUID{}, morningIDs...), eveningIDs...), ids(got), "다른 날의 발화는 섞이지 않는다")
	})

	t.Run("남의 발화는 나오지 않는다", func(t *testing.T) {
		byConversation, err := st.Queries().ListUtterancesByConversation(t.Context(), db.ListUtterancesByConversationParams{
			ConversationID: morning.ID, UserID: joon.ID,
		})
		require.NoError(t, err)
		assert.Empty(t, byConversation)

		last, err := st.Queries().ListLastUtterances(t.Context(), db.ListLastUtterancesParams{
			ConversationID: morning.ID, UserID: joon.ID, MaxRows: 10,
		})
		require.NoError(t, err)
		assert.Empty(t, last)

		byDay, err := st.Queries().ListUtterancesByDay(t.Context(), db.ListUtterancesByDayParams{DayID: morning.DayID, UserID: joon.ID})
		require.NoError(t, err)
		assert.Empty(t, byDay)
	})
}
