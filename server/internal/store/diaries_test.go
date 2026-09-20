package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// sealFor는 어느 행 ID에 묶어 잠갔는지가 암호문에 드러나는 가짜 잠금이다.
// 진짜 암호문은 다른 행 ID로는 열리지 않는다. 그 성질을 흉내 내어, 엉뚱한 ID로 잠근 글이 저장되면 시험에서 보이게 한다.
func sealFor(text string) store.SealFunc {
	return func(rowID uuid.UUID) ([]byte, error) {
		return []byte(rowID.String() + ":" + text), nil
	}
}

func sealedWith(rowID uuid.UUID, text string) []byte {
	return []byte(rowID.String() + ":" + text)
}

func saveDraft(t *testing.T, st *store.Store, in store.DiaryDraftWrite) (db.Diary, error) {
	t.Helper()
	var diary db.Diary
	err := st.InTx(t.Context(), func(q *db.Queries) error {
		var err error
		diary, err = store.SaveDiaryDraft(t.Context(), q, in)
		return err
	})
	return diary, err
}

func saveBody(t *testing.T, st *store.Store, in store.DiaryBodyWrite) (db.Diary, error) {
	t.Helper()
	var diary db.Diary
	err := st.InTx(t.Context(), func(q *db.Queries) error {
		var err error
		diary, err = store.SaveDiaryBody(t.Context(), q, in)
		return err
	})
	return diary, err
}

// talkedDay는 대화를 한 번 하고 끝낸 하루를 만든다.
func talkedDay(t *testing.T, st *store.Store, userID uuid.UUID, d recorddate.Date) uuid.UUID {
	t.Helper()
	conversation := openConversation(t, st, userID, d, baseTime)
	endConversation(t, st, conversation, baseTime.Add(time.Minute))
	return conversation.DayID
}

func TestSaveDiaryDraft(t *testing.T) {
	t.Parallel()
	today := recordDate(t, 2026, time.September, 20)

	t.Run("그날의 첫 초안은 새 행의 ID에 묶어 잠근다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)
		id := newID(t)

		diary, err := saveDraft(t, st, store.DiaryDraftWrite{
			NewID: id, DayID: dayID, UserID: mina.ID, Seal: sealFor("초안"), Now: baseTime,
		})
		require.NoError(t, err)
		assert.Equal(t, id, diary.ID)
		assert.Equal(t, store.DiaryDraft, diary.Status)
		assert.Equal(t, sealedWith(id, "초안"), diary.DraftEnc)
		assert.Nil(t, diary.BodyEnc)
		assert.Nil(t, diary.ConfirmedAt)
		assertInstant(t, baseTime, diary.CreatedAt)
		assertInstant(t, baseTime, diary.UpdatedAt)
	})

	t.Run("그날 다시 대화하면 이미 있는 행의 ID에 묶어 잠그고, 확인한 글은 그대로 둔다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)

		confirmed, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("고친 글"), Now: baseTime,
		})
		require.NoError(t, err)

		later := baseTime.Add(8 * time.Hour)
		diary, err := saveDraft(t, st, store.DiaryDraftWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID,
			SeenUpdatedAt: &confirmed.UpdatedAt, Seal: sealFor("이어 붙인 초안"), Now: later,
		})
		require.NoError(t, err)
		assert.Equal(t, confirmed.ID, diary.ID)
		assert.Equal(t, store.DiaryDraft, diary.Status, "사용자가 다시 확인해야 한다")
		assert.Equal(t, sealedWith(confirmed.ID, "이어 붙인 초안"), diary.DraftEnc, "새 ID로 잠근 글은 이 행에서 열리지 않는다")
		assert.Equal(t, sealedWith(confirmed.ID, "고친 글"), diary.BodyEnc)
		require.NotNil(t, diary.ConfirmedAt)
		assertInstant(t, baseTime, *diary.ConfirmedAt)
		assertInstant(t, baseTime, diary.CreatedAt)
		assertInstant(t, later, diary.UpdatedAt)
		assert.Equal(t, 1, count(t, pool, "diaries", "day_id", dayID))
	})

	t.Run("초안을 만드는 동안 일기가 바뀌었으면 저장하지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)

		seen, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("처음 글"), Now: baseTime,
		})
		require.NoError(t, err)
		// 초안 작업이 일기를 읽고 AI를 부르는 사이에 사용자가 글을 고쳤다.
		edited, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("고친 글"), Now: baseTime.Add(time.Minute),
		})
		require.NoError(t, err)

		tests := []struct {
			name string
			seen *time.Time
		}{
			{"고치기 전의 일기를 보고 만든 초안", &seen.UpdatedAt},
			{"일기가 없다고 보고 만든 초안", nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := saveDraft(t, st, store.DiaryDraftWrite{
					NewID: newID(t), DayID: dayID, UserID: mina.ID,
					SeenUpdatedAt: tt.seen, Seal: sealFor("낡은 초안"), Now: baseTime.Add(2 * time.Minute),
				})
				require.ErrorIs(t, err, store.ErrDiaryChanged)
			})
		}

		got, err := st.Queries().GetDiaryByDayID(t.Context(), db.GetDiaryByDayIDParams{DayID: dayID, UserID: mina.ID})
		require.NoError(t, err)
		assert.Equal(t, store.DiaryConfirmed, got.Status)
		assert.Nil(t, got.DraftEnc)
		assert.Equal(t, edited.BodyEnc, got.BodyEnc)
	})

	t.Run("있다고 보고 만든 초안인데 일기가 사라졌으면 저장하지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)
		seenAt := baseTime

		_, err := saveDraft(t, st, store.DiaryDraftWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, SeenUpdatedAt: &seenAt, Seal: sealFor("초안"), Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrDiaryChanged)
		assert.Zero(t, count(t, pool, "diaries", "day_id", dayID))
	})

	t.Run("같은 날의 초안 둘이 동시에 와도 일기는 하나이고 늦은 쪽이 다시 만든다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)

		const jobs = 6
		ids := make([]uuid.UUID, jobs)
		for i := range ids {
			ids[i] = newID(t)
		}
		errs := make([]error, jobs)
		var wg sync.WaitGroup
		for i := range jobs {
			wg.Go(func() {
				errs[i] = st.InTx(t.Context(), func(q *db.Queries) error {
					_, err := store.SaveDiaryDraft(t.Context(), q, store.DiaryDraftWrite{
						NewID: ids[i], DayID: dayID, UserID: mina.ID, Seal: sealFor("초안"), Now: baseTime,
					})
					return err
				})
			})
		}
		wg.Wait()

		saved := 0
		for _, err := range errs {
			if err == nil {
				saved++
				continue
			}
			require.ErrorIs(t, err, store.ErrDiaryChanged)
		}
		assert.Equal(t, 1, saved)
		assert.Equal(t, 1, count(t, pool, "diaries", "day_id", dayID))
	})

	t.Run("지워진 하루와 남의 하루에는 초안을 남기지 못한다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		dayID := talkedDay(t, st, mina.ID, today)

		_, err := saveDraft(t, st, store.DiaryDraftWrite{
			NewID: newID(t), DayID: dayID, UserID: joon.ID, Seal: sealFor("초안"), Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrNotFound)

		_, err = st.Queries().DeleteDay(t.Context(), db.DeleteDayParams{ID: dayID, UserID: mina.ID})
		require.NoError(t, err)
		_, err = saveDraft(t, st, store.DiaryDraftWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("초안"), Now: baseTime,
		})
		require.ErrorIs(t, err, store.ErrNotFound, "초안 작업이 도는 사이에 사용자가 하루를 지웠다")
		assert.Zero(t, count(t, pool, "diaries", "user_id", mina.ID))
	})

	t.Run("잠그지 못한 글은 저장하지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)
		errSeal := errors.New("no key")

		tests := []struct {
			name string
			seal store.SealFunc
			is   error
		}{
			{"잠그는 함수가 없다", nil, nil},
			{"잠그다 실패했다", func(uuid.UUID) ([]byte, error) { return nil, errSeal }, errSeal},
			{"빈 암호문이 돌아왔다", func(uuid.UUID) ([]byte, error) { return []byte{}, nil }, nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := saveDraft(t, st, store.DiaryDraftWrite{
					NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: tt.seal, Now: baseTime,
				})
				require.Error(t, err)
				if tt.is != nil {
					require.ErrorIs(t, err, tt.is)
				}
				_, err = saveBody(t, st, store.DiaryBodyWrite{
					NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: tt.seal, Now: baseTime,
				})
				require.Error(t, err)
			})
		}
		assert.Zero(t, count(t, pool, "diaries", "day_id", dayID))
	})
}

func TestSaveDiaryBody(t *testing.T) {
	t.Parallel()
	today := recordDate(t, 2026, time.September, 20)

	t.Run("초안을 고쳐서 확인하면 글이 남고 초안은 비워진다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)
		draft, err := saveDraft(t, st, store.DiaryDraftWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("초안"), Now: baseTime,
		})
		require.NoError(t, err)

		confirmedAt := baseTime.Add(5 * time.Minute)
		diary, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: newID(t), DayID: dayID, UserID: mina.ID, Seal: sealFor("고친 글"), Now: confirmedAt,
		})
		require.NoError(t, err)
		assert.Equal(t, draft.ID, diary.ID)
		assert.Equal(t, store.DiaryConfirmed, diary.Status)
		assert.Equal(t, sealedWith(draft.ID, "고친 글"), diary.BodyEnc)
		assert.Nil(t, diary.DraftEnc, "확인한 글이 초안을 대신한다")
		require.NotNil(t, diary.ConfirmedAt)
		assertInstant(t, confirmedAt, *diary.ConfirmedAt)
		assertInstant(t, baseTime, diary.CreatedAt)
		assertInstant(t, confirmedAt, diary.UpdatedAt)
		assert.Equal(t, 1, count(t, pool, "diaries", "day_id", dayID))
	})

	t.Run("초안이 없는 날에도 대화한 날이면 직접 쓸 수 있다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		dayID := talkedDay(t, st, mina.ID, today)
		id := newID(t)

		diary, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: id, DayID: dayID, UserID: mina.ID, Seal: sealFor("직접 쓴 글"), Now: baseTime,
		})
		require.NoError(t, err)
		assert.Equal(t, id, diary.ID)
		assert.Equal(t, store.DiaryConfirmed, diary.Status)
		assert.Equal(t, sealedWith(id, "직접 쓴 글"), diary.BodyEnc)
		assert.Nil(t, diary.DraftEnc)
	})

	t.Run("대화하지 않은 날과 남의 하루에는 쓰지 못한다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina@example.com")
		joon := createUser(t, st, "joon@example.com")
		dayID := talkedDay(t, st, mina.ID, today)

		for name, in := range map[string]store.DiaryBodyWrite{
			"없는 하루": {NewID: newID(t), DayID: newID(t), UserID: mina.ID, Seal: sealFor("글"), Now: baseTime},
			"남의 하루": {NewID: newID(t), DayID: dayID, UserID: joon.ID, Seal: sealFor("글"), Now: baseTime},
		} {
			_, err := saveBody(t, st, in)
			require.ErrorIs(t, err, store.ErrNotFound, name)
		}
		assert.Zero(t, count(t, pool, "diaries", "day_id", dayID))
	})
}

func TestReadDiaries(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")

	write := func(userID uuid.UUID, d recorddate.Date) db.Diary {
		diary, err := saveBody(t, st, store.DiaryBodyWrite{
			NewID: newID(t), DayID: talkedDay(t, st, userID, d), UserID: userID, Seal: sealFor(d.String()), Now: baseTime,
		})
		require.NoError(t, err)
		return diary
	}
	augustEnd := write(mina.ID, recordDate(t, 2026, time.August, 31))
	septemberStart := write(mina.ID, recordDate(t, 2026, time.September, 1))
	septemberEnd := write(mina.ID, recordDate(t, 2026, time.September, 30))
	octoberStart := write(mina.ID, recordDate(t, 2026, time.October, 1))
	write(joon.ID, recordDate(t, 2026, time.September, 15))
	// 대화만 하고 일기는 없는 날이다.
	talkedDay(t, st, mina.ID, recordDate(t, 2026, time.September, 10))

	byDate := func(d recorddate.Date) db.GetDiaryByDateParams {
		value, err := store.PGDate(d)
		require.NoError(t, err)
		return db.GetDiaryByDateParams{UserID: mina.ID, RecordDate: value}
	}

	t.Run("날짜로 자기 일기를 찾는다", func(t *testing.T) {
		got, err := st.Queries().GetDiaryByDate(t.Context(), byDate(recordDate(t, 2026, time.September, 1)))
		require.NoError(t, err)
		assert.Equal(t, septemberStart.ID, got.ID)
	})

	t.Run("일기가 없는 날과 남의 일기는 찾지 못한다", func(t *testing.T) {
		for _, d := range []recorddate.Date{
			recordDate(t, 2026, time.September, 10),
			recordDate(t, 2026, time.September, 15),
			recordDate(t, 2026, time.September, 2),
		} {
			_, err := st.Queries().GetDiaryByDate(t.Context(), byDate(d))
			require.ErrorIs(t, err, store.ErrNotFound, d.String())
		}
	})

	t.Run("한 달의 일기는 그 달 1일부터 다음 달 1일 앞까지다", func(t *testing.T) {
		from, err := store.PGDate(recordDate(t, 2026, time.September, 1))
		require.NoError(t, err)
		to, err := store.PGDate(recordDate(t, 2026, time.October, 1))
		require.NoError(t, err)

		rows, err := st.Queries().ListDiariesByDateRange(t.Context(), db.ListDiariesByDateRangeParams{
			UserID: mina.ID, FromDate: from, ToDate: to,
		})
		require.NoError(t, err)
		require.Len(t, rows, 2, "8월 31일(%s)과 10월 1일(%s), 남의 일기는 들지 않는다", augustEnd.ID, octoberStart.ID)
		assert.Equal(t, septemberStart.ID, rows[0].Diary.ID)
		assert.Equal(t, septemberEnd.ID, rows[1].Diary.ID)
		gotDate, err := store.RecordDate(rows[1].RecordDate)
		require.NoError(t, err)
		assert.Equal(t, recordDate(t, 2026, time.September, 30), gotDate)
	})

	t.Run("전체 목록은 최근 날짜부터 자기 일기만 나온다", func(t *testing.T) {
		rows, err := st.Queries().ListDiariesByUser(t.Context(), db.ListDiariesByUserParams{
			UserID: mina.ID, MaxRows: 100,
		})
		require.NoError(t, err)
		got := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			got = append(got, row.Diary.ID)
		}
		assert.Equal(t, []uuid.UUID{octoberStart.ID, septemberEnd.ID, septemberStart.ID, augustEnd.ID}, got)
	})

	t.Run("읽어 오는 개수는 상한에서 잘린다", func(t *testing.T) {
		rows, err := st.Queries().ListDiariesByUser(t.Context(), db.ListDiariesByUserParams{
			UserID: mina.ID, MaxRows: 2,
		})
		require.NoError(t, err)
		require.Len(t, rows, 2, "상한을 넘는 암호문은 아예 읽어 오지 않는다")
		assert.Equal(t, octoberStart.ID, rows[0].Diary.ID)
		assert.Equal(t, septemberEnd.ID, rows[1].Diary.ID)
	})
}

func TestDayByDate(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	kept := seedDay(t, st, pool, mina.ID, date(2026, time.September, 19))
	gone := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))
	joonDay := seedDay(t, st, pool, joon.ID, date(2026, time.September, 20))

	t.Run("날짜로 자기 하루를 찾는다", func(t *testing.T) {
		got, err := st.Queries().GetDayByDate(t.Context(), db.GetDayByDateParams{UserID: mina.ID, RecordDate: date(2026, time.September, 20)})
		require.NoError(t, err)
		assert.Equal(t, gone.dayID, got.ID)

		_, err = st.Queries().GetDayByDate(t.Context(), db.GetDayByDateParams{UserID: mina.ID, RecordDate: date(2026, time.September, 21)})
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("날짜로 하루를 지우면 그날에 매달린 것만 함께 사라진다", func(t *testing.T) {
		deleted, err := st.Queries().DeleteDayByDate(t.Context(), db.DeleteDayByDateParams{UserID: mina.ID, RecordDate: date(2026, time.September, 20)})
		require.NoError(t, err)
		assert.Equal(t, gone.dayID, deleted)

		for _, scoped := range dayScopedTables {
			assert.Zero(t, count(t, pool, scoped.table, scoped.column, scoped.key(gone)), "%s: 지운 날", scoped.table)
			assert.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(kept)), "%s: 자기의 다른 날", scoped.table)
			assert.Equal(t, scoped.rows, count(t, pool, scoped.table, scoped.column, scoped.key(joonDay)), "%s: 남의 같은 날", scoped.table)
		}

		_, err = st.Queries().DeleteDayByDate(t.Context(), db.DeleteDayByDateParams{UserID: mina.ID, RecordDate: date(2026, time.September, 20)})
		require.ErrorIs(t, err, store.ErrNotFound, "이미 지운 날은 없는 날이다")
	})
}

func TestListSignalRowsByUser(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	day := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))
	seedDay(t, st, pool, joon.ID, date(2026, time.September, 20))

	_, err := pool.Exec(t.Context(),
		`UPDATE signals SET cancelled_at = $1 WHERE conversation_id = $2 AND item = 'sleep'`, baseTime, day.conversationID)
	require.NoError(t, err)

	rows, err := st.Queries().ListSignalRowsByUser(t.Context(), mina.ID)
	require.NoError(t, err)
	require.Len(t, rows, 3, "남의 신호는 나오지 않는다")

	type judgement struct {
		status, explicitness string
		cancelled            bool
	}
	got := make(map[string]judgement, len(rows))
	for _, row := range rows {
		assert.Equal(t, day.conversationID, row.ConversationID)
		d, err := store.RecordDate(row.RecordDate)
		require.NoError(t, err)
		assert.Equal(t, recordDate(t, 2026, time.September, 20), d)
		got[row.Item] = judgement{row.Status, row.Explicitness, row.Cancelled}
	}
	assert.Equal(t, map[string]judgement{
		"sleep":         {"observed", "direct", true},
		"mood":          {"not_observed", "indirect", false},
		"concentration": {"not_mentioned", "none", false},
	}, got, "취소한 신호도 행은 나온다. 계산에서 빼는 일은 계산 쪽이 한다")
}
