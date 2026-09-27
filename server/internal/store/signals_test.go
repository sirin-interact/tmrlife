package store_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

const testExtractor = "test-extractor-1"

// itemIdentifiers는 여덟 항목의 저장 식별자다. 스키마의 CHECK 제약과 견주는 데 쓴다.
func itemIdentifiers() []string {
	items := signal.AllItems()
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.String())
	}
	return ids
}

// sealFake는 암호화를 흉내 낸다. 이 패키지는 평문을 보지 않으므로 잠근 값이 무엇인지는 상관없고,
// 행 ID가 잠글 때 넘어오는지만 확인한다.
func sealFake(t *testing.T, seen *[]uuid.UUID) store.SealFunc {
	t.Helper()
	return func(rowID uuid.UUID) ([]byte, error) {
		if seen != nil {
			*seen = append(*seen, rowID)
		}
		return append([]byte("evidence:"), rowID[:]...), nil
	}
}

// analysisFor는 여덟 항목을 채운 분석 결과를 만든다. observed에 적은 항목만 관찰됨이고 나머지는 언급 없음이다.
func analysisFor(
	t *testing.T, conversation db.Conversation, evidenceUtterance *uuid.UUID, observed ...signal.Item,
) store.ConversationAnalysis {
	t.Helper()
	in := store.ConversationAnalysis{
		UserID:           conversation.UserID,
		DayID:            conversation.DayID,
		ConversationID:   conversation.ID,
		ExtractorVersion: testExtractor,
		Now:              baseTime,
	}
	for _, item := range observed {
		in.Judgements[item.Index()] = store.SignalJudgement{
			Judgement:           signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct},
			SealEvidence:        sealFake(t, nil),
			EvidenceUtteranceID: evidenceUtterance,
		}
	}
	return in
}

// endedConversation은 끝난 대화 하나와 그 안의 사용자 발화 하나를 만든다.
func endedConversation(t *testing.T, st *store.Store, userID uuid.UUID, d recorddate.Date, now time.Time) (db.Conversation, uuid.UUID) {
	t.Helper()
	conversation := openConversation(t, st, userID, d, now)
	utterance := appendUtterance(t, st, conversation, store.SpeakerUser, now)
	endConversation(t, st, conversation, now.Add(time.Minute))
	return conversation, utterance.ID
}

// pgDate는 기록 날짜를 쿼리에 넘길 값으로 바꾼다.
func pgDate(t *testing.T, d recorddate.Date) pgtype.Date {
	t.Helper()
	value, err := store.PGDate(d)
	require.NoError(t, err)
	return value
}

func TestSaveConversationSignals(t *testing.T) {
	t.Parallel()

	t.Run("여덟 항목이 한 번에 들어가고 대화의 분석이 done으로 닫힌다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "eight@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, utterance := endedConversation(t, st, user.ID, today, baseTime)

		in := analysisFor(t, conversation, &utterance, signal.Sleep, signal.Fatigue)
		in.Judgements[signal.Mood.Index()] = store.SignalJudgement{
			Judgement:    signal.Judgement{Status: signal.NotObserved, Explicitness: signal.Indirect},
			SealEvidence: sealFake(t, nil),
		}
		require.NoError(t, st.SaveConversationSignals(t.Context(), in))

		rows, err := st.Queries().ListSignalsByDate(t.Context(), db.ListSignalsByDateParams{
			UserID: user.ID, RecordDate: pgDate(t, today),
		})
		require.NoError(t, err)
		require.Len(t, rows, signal.ItemCount, "분석이 끝난 대화는 언급이 없었던 항목까지 여덟 행을 남긴다")

		byItem := make(map[string]db.ListSignalsByDateRow, len(rows))
		for _, row := range rows {
			byItem[row.Item] = row
		}
		assert.Equal(t, signal.Observed.String(), byItem["sleep"].Status)
		assert.Equal(t, signal.Direct.String(), byItem["sleep"].Explicitness)
		assert.NotEmpty(t, byItem["sleep"].EvidenceEnc)
		require.NotNil(t, byItem["sleep"].UtteranceID)
		assert.Equal(t, utterance, *byItem["sleep"].UtteranceID, "근거가 어느 발화에서 왔는지 함께 온다")
		assert.NotEmpty(t, byItem["sleep"].UtteranceTextEnc)

		assert.Equal(t, signal.NotObserved.String(), byItem["mood"].Status)
		assert.Nil(t, byItem["mood"].UtteranceID, "발화를 가리키지 않는 근거도 받아 준다")

		assert.Equal(t, signal.NotMentioned.String(), byItem["appetite"].Status)
		assert.Equal(t, signal.None.String(), byItem["appetite"].Explicitness)
		assert.Empty(t, byItem["appetite"].EvidenceEnc, "언급 없음인 항목에는 근거가 없다")

		analysed, err := st.Queries().HasAnalysedConversationOnDate(t.Context(), db.HasAnalysedConversationOnDateParams{
			UserID: user.ID, RecordDate: pgDate(t, today),
		})
		require.NoError(t, err)
		assert.True(t, analysed)
	})

	t.Run("근거 암호문은 그 행의 ID에 묶어 잠근다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "aad@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)

		var sealedWith []uuid.UUID
		in := analysisFor(t, conversation, nil)
		in.Judgements[signal.Sleep.Index()] = store.SignalJudgement{
			Judgement:    signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct},
			SealEvidence: sealFake(t, &sealedWith),
		}
		require.NoError(t, st.SaveConversationSignals(t.Context(), in))

		rows, err := st.Queries().ListSignalsByDate(t.Context(), db.ListSignalsByDateParams{
			UserID: user.ID, RecordDate: pgDate(t, today),
		})
		require.NoError(t, err)
		require.Len(t, sealedWith, 1, "근거가 있는 항목만 잠근다")
		for _, row := range rows {
			if row.Item == "sleep" {
				assert.Equal(t, row.ID, sealedWith[0], "잠글 때 넘어온 ID가 저장된 행의 ID다")
			}
		}
	})

	t.Run("같은 대화를 동시에 저장해도 한쪽만 들어가고 여덟 행이 갖춰진다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "race@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)

		const writers = 4
		errs := make([]error, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep))
			}()
		}
		wg.Wait()

		saved := 0
		for _, err := range errs {
			if err == nil {
				saved++
				continue
			}
			require.ErrorIs(t, err, store.ErrSignalsAlreadySaved)
		}
		assert.Equal(t, 1, saved, "한 번만 들어간다")
		assert.Equal(t, signal.ItemCount, count(t, pool, "signals", "conversation_id", conversation.ID),
			"진 쪽의 행은 하나도 남지 않는다")
	})

	t.Run("이미 분석한 대화를 다시 저장하면 먼저 들어간 행이 남는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "again@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)

		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))
		err := st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Appetite))
		require.ErrorIs(t, err, store.ErrSignalsAlreadySaved)
		require.ErrorIs(t, err, store.ErrConflict)

		rows, err := st.Queries().ListSignalsByDate(t.Context(), db.ListSignalsByDateParams{
			UserID: user.ID, RecordDate: pgDate(t, today),
		})
		require.NoError(t, err)
		require.Len(t, rows, signal.ItemCount)
		for _, row := range rows {
			if row.Item == "appetite" {
				assert.Equal(t, signal.NotMentioned.String(), row.Status, "두 번째 결과는 하나도 반영되지 않는다")
			}
		}
	})

	t.Run("열려 있는 대화, 없는 대화, 남의 대화에는 저장하지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		mina := createUser(t, st, "mina-open@example.com")
		joon := createUser(t, st, "joon-open@example.com")
		today := recordDate(t, 2026, time.September, 20)

		open := openConversation(t, st, mina.ID, today, baseTime)
		require.ErrorIs(t, st.SaveConversationSignals(t.Context(), analysisFor(t, open, nil)), store.ErrNotFound,
			"아직 이어질 말이 남은 대화는 분석하지 않는다")

		missing := open
		missing.ID = newID(t)
		require.ErrorIs(t, st.SaveConversationSignals(t.Context(), analysisFor(t, missing, nil)), store.ErrNotFound)

		endConversation(t, st, open, baseTime.Add(time.Minute))
		stolen := open
		stolen.UserID = joon.ID
		require.ErrorIs(t, st.SaveConversationSignals(t.Context(), analysisFor(t, stolen, nil)), store.ErrNotFound)
		assert.Zero(t, count(t, pool, "signals", "conversation_id", open.ID))
	})

	t.Run("판단과 근거가 맞지 않으면 아무것도 저장하지 않는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "mismatch@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, utterance := endedConversation(t, st, user.ID, today, baseTime)

		tests := []struct {
			name      string
			judgement store.SignalJudgement
		}{
			{"근거 없는 관찰됨", store.SignalJudgement{
				Judgement: signal.Judgement{Status: signal.Observed, Explicitness: signal.Direct},
			}},
			{"근거가 딸린 언급 없음", store.SignalJudgement{
				Judgement:    signal.Judgement{Status: signal.NotMentioned, Explicitness: signal.None},
				SealEvidence: sealFake(t, nil),
			}},
			{"명시성 없는 관찰됨", store.SignalJudgement{
				Judgement:    signal.Judgement{Status: signal.Observed, Explicitness: signal.None},
				SealEvidence: sealFake(t, nil),
			}},
			{"근거 없이 발화만 가리키는 항목", store.SignalJudgement{
				Judgement:           signal.Judgement{Status: signal.NotMentioned, Explicitness: signal.None},
				EvidenceUtteranceID: &utterance,
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				in := analysisFor(t, conversation, nil)
				in.Judgements[signal.Sleep.Index()] = tt.judgement
				assert.Error(t, st.SaveConversationSignals(t.Context(), in))
			})
		}
		assert.Zero(t, count(t, pool, "signals", "conversation_id", conversation.ID))

		in := analysisFor(t, conversation, nil)
		in.ExtractorVersion = ""
		assert.Error(t, st.SaveConversationSignals(t.Context(), in), "어떤 지시문으로 뽑았는지 없이 저장하지 않는다")
	})

	t.Run("하루를 지우면 그날의 신호 행이 함께 지워진다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "cascade-signals@example.com")
		today := recordDate(t, 2026, time.September, 20)
		yesterday := recordDate(t, 2026, time.September, 19)

		gone, _ := endedConversation(t, st, user.ID, yesterday, baseTime.Add(-24*time.Hour))
		kept, _ := endedConversation(t, st, user.ID, today, baseTime)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, gone, nil, signal.Sleep)))
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, kept, nil, signal.Mood)))

		_, err := st.Queries().DeleteDayByDate(t.Context(), db.DeleteDayByDateParams{
			UserID: user.ID, RecordDate: pgDate(t, yesterday),
		})
		require.NoError(t, err)

		assert.Zero(t, count(t, pool, "signals", "conversation_id", gone.ID))
		assert.Equal(t, signal.ItemCount, count(t, pool, "signals", "conversation_id", kept.ID), "다른 날은 그대로다")

		analysed, err := st.Queries().HasAnalysedConversationOnDate(t.Context(), db.HasAnalysedConversationOnDateParams{
			UserID: user.ID, RecordDate: pgDate(t, yesterday),
		})
		require.NoError(t, err)
		assert.False(t, analysed, "지운 날은 분석이 끝난 대화가 없는 날로 돌아간다")
	})
}

func TestConversationAnalysisStatus(t *testing.T) {
	t.Parallel()

	t.Run("끝난 대화의 분석은 한 작업자만 맡는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "claim@example.com")
		today := recordDate(t, 2026, time.September, 20)

		open := openConversation(t, st, user.ID, today, baseTime)
		_, err := st.Queries().ClaimConversationAnalysis(t.Context(), db.ClaimConversationAnalysisParams{
			ID: open.ID, UserID: user.ID,
		})
		require.ErrorIs(t, err, store.ErrNotFound, "열려 있는 대화는 맡지 않는다")

		endConversation(t, st, open, baseTime.Add(time.Minute))
		claimed, err := st.Queries().ClaimConversationAnalysis(t.Context(), db.ClaimConversationAnalysisParams{
			ID: open.ID, UserID: user.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisRunning, claimed.AnalysisStatus)

		_, err = st.Queries().ClaimConversationAnalysis(t.Context(), db.ClaimConversationAnalysisParams{
			ID: open.ID, UserID: user.ID,
		})
		require.ErrorIs(t, err, store.ErrNotFound, "맡고 있는 분석을 다른 쪽이 다시 맡지 않는다")

		_, err = st.Queries().SetConversationAnalysisStatus(t.Context(), db.SetConversationAnalysisStatusParams{
			AnalysisStatus: store.AnalysisFailed, ID: open.ID, UserID: user.ID,
		})
		require.NoError(t, err)
		retried, err := st.Queries().ClaimConversationAnalysis(t.Context(), db.ClaimConversationAnalysisParams{
			ID: open.ID, UserID: user.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisRunning, retried.AnalysisStatus, "실패한 분석은 다시 맡는다")
	})

	t.Run("분석이 끝난 대화의 상태는 덮이지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "done@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))

		for _, status := range []string{store.AnalysisRunning, store.AnalysisFailed, store.AnalysisPending, store.AnalysisNone} {
			_, err := st.Queries().SetConversationAnalysisStatus(t.Context(), db.SetConversationAnalysisStatusParams{
				AnalysisStatus: status, ID: conversation.ID, UserID: user.ID,
			})
			require.ErrorIs(t, err, store.ErrNotFound, status)
		}

		again, err := st.Queries().SetConversationAnalysisStatus(t.Context(), db.SetConversationAnalysisStatusParams{
			AnalysisStatus: store.AnalysisDone, ID: conversation.ID, UserID: user.ID,
		})
		require.NoError(t, err, "같은 값을 다시 적는 것은 받아 준다")
		assert.Equal(t, store.AnalysisDone, again.AnalysisStatus)

		_, err = st.Queries().ClaimConversationAnalysis(t.Context(), db.ClaimConversationAnalysisParams{
			ID: conversation.ID, UserID: user.ID,
		})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("일기 초안의 진행 상태와 서로를 덮지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "separate@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)

		_, err := st.Queries().SetConversationProcessingStatus(t.Context(), db.SetConversationProcessingStatusParams{
			ProcessingStatus: store.ProcessingDone, ID: conversation.ID, UserID: user.ID,
		})
		require.NoError(t, err)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))

		row, err := st.Queries().GetConversation(t.Context(), db.GetConversationParams{ID: conversation.ID, UserID: user.ID})
		require.NoError(t, err)
		assert.Equal(t, store.ProcessingDone, row.Conversation.ProcessingStatus, "초안 작업이 적은 값은 그대로다")
		assert.Equal(t, store.AnalysisDone, row.Conversation.AnalysisStatus)
	})
}

func TestSignalCancellation(t *testing.T) {
	t.Parallel()

	t.Run("취소하면 계산에서 빠지고 행은 남는다", func(t *testing.T) {
		t.Parallel()
		st, pool := newStore(t)
		user := createUser(t, st, "cancel@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))

		sleep := signalRowFor(t, st, user.ID, today, "sleep")
		cancelledAt := baseTime.Add(time.Hour)
		on, err := st.Queries().CancelSignal(t.Context(), db.CancelSignalParams{
			ID: sleep.ID, UserID: user.ID, Now: cancelledAt,
		})
		require.NoError(t, err)
		got, err := store.RecordDate(on)
		require.NoError(t, err)
		assert.Equal(t, today, got, "그 행이 매달린 기록 날짜를 돌려준다")

		assert.True(t, signalRowFor(t, st, user.ID, today, "sleep").Cancelled)

		byDate, err := st.Queries().ListSignalRowsByDateRange(t.Context(), db.ListSignalRowsByDateRangeParams{
			UserID: user.ID, FromDate: pgDate(t, today), ToDate: pgDate(t, today),
		})
		require.NoError(t, err)
		require.Len(t, byDate, signal.ItemCount, "취소한 행도 목록에 그대로 온다")

		days, err := store.SignalDaysInRange(byDate)
		require.NoError(t, err)
		merged, err := signal.MergeDay(today, days[today])
		require.NoError(t, err)
		assert.Equal(t, signal.NotMentioned, merged.Judgement(signal.Sleep).Status, "계산 코어가 취소한 행을 뺀다")
		assert.Equal(t, 0, merged.ObservedCount())

		// 두 번 눌러도 처음 취소한 시각이 그대로다.
		_, err = st.Queries().CancelSignal(t.Context(), db.CancelSignalParams{
			ID: sleep.ID, UserID: user.ID, Now: cancelledAt.Add(time.Hour),
		})
		require.NoError(t, err)
		assertInstant(t, cancelledAt, cancelledAtOf(t, pool, sleep.ID))
	})

	t.Run("취소를 되돌리면 다시 계산에 든다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "uncancel@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, user.ID, today, baseTime)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))

		sleep := signalRowFor(t, st, user.ID, today, "sleep")
		_, err := st.Queries().CancelSignal(t.Context(), db.CancelSignalParams{ID: sleep.ID, UserID: user.ID, Now: baseTime})
		require.NoError(t, err)

		on, err := st.Queries().UncancelSignal(t.Context(), db.UncancelSignalParams{ID: sleep.ID, UserID: user.ID})
		require.NoError(t, err)
		got, err := store.RecordDate(on)
		require.NoError(t, err)
		assert.Equal(t, today, got)
		assert.False(t, signalRowFor(t, st, user.ID, today, "sleep").Cancelled)

		// 취소한 적이 없는 행에 불러도 성공한다.
		mood := signalRowFor(t, st, user.ID, today, "mood")
		_, err = st.Queries().UncancelSignal(t.Context(), db.UncancelSignalParams{ID: mood.ID, UserID: user.ID})
		assert.NoError(t, err)
	})

	t.Run("남의 신호는 ID를 알아도 취소하지 못한다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		mina := createUser(t, st, "mina-cancel@example.com")
		joon := createUser(t, st, "joon-cancel@example.com")
		today := recordDate(t, 2026, time.September, 20)
		conversation, _ := endedConversation(t, st, mina.ID, today, baseTime)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))

		sleep := signalRowFor(t, st, mina.ID, today, "sleep")
		_, err := st.Queries().CancelSignal(t.Context(), db.CancelSignalParams{ID: sleep.ID, UserID: joon.ID, Now: baseTime})
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = st.Queries().UncancelSignal(t.Context(), db.UncancelSignalParams{ID: sleep.ID, UserID: joon.ID})
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.False(t, signalRowFor(t, st, mina.ID, today, "sleep").Cancelled)
	})
}

func TestListSignalRowsByDateRange(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	mina := createUser(t, st, "range-mina@example.com")
	joon := createUser(t, st, "range-joon@example.com")

	dates := []recorddate.Date{
		recordDate(t, 2026, time.September, 18),
		recordDate(t, 2026, time.September, 19),
		recordDate(t, 2026, time.September, 20),
	}
	for i, d := range dates {
		at := baseTime.Add(time.Duration(i-len(dates)) * 24 * time.Hour)
		conversation, _ := endedConversation(t, st, mina.ID, d, at)
		require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, conversation, nil, signal.Sleep)))
	}
	others, _ := endedConversation(t, st, joon.ID, dates[1], baseTime)
	require.NoError(t, st.SaveConversationSignals(t.Context(), analysisFor(t, others, nil, signal.Mood)))

	rows, err := st.Queries().ListSignalRowsByDateRange(t.Context(), db.ListSignalRowsByDateRangeParams{
		UserID: mina.ID, FromDate: pgDate(t, dates[1]), ToDate: pgDate(t, dates[2]),
	})
	require.NoError(t, err)
	assert.Len(t, rows, 2*signal.ItemCount, "기간의 첫날과 마지막 날을 모두 넣는다")

	byDate, err := store.SignalDaysInRange(rows)
	require.NoError(t, err)
	assert.Len(t, byDate, 2)
	assert.NotContains(t, byDate, dates[0], "기간 밖의 날은 오지 않는다")

	days, err := signal.MergeDays(byDate)
	require.NoError(t, err)
	require.Len(t, days, 2)
	assert.Equal(t, dates[1], days[0].Date, "날짜순으로 온다")
	assert.Equal(t, signal.Observed, days[0].Judgement(signal.Sleep).Status)
	assert.Equal(t, signal.NotMentioned, days[0].Judgement(signal.Mood).Status, "남의 기록은 섞이지 않는다")

	all, err := st.Queries().ListSignalRowsByUser(t.Context(), mina.ID)
	require.NoError(t, err)
	fromAll, err := store.SignalDaysByUser(all)
	require.NoError(t, err)
	assert.Len(t, fromAll, len(dates), "기간을 자르지 않는 조회는 첫 대화 날부터 온다")
}

func TestParseSignalJudgement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                       string
		item, status, explicitness string
		wantItem                   signal.Item
		wantStatus                 signal.Status
		wantExplicitness           signal.Explicitness
		wantErr                    bool
		// unknownID는 읽지 못한 식별자가 있는 경우다. 그때의 오류 문구에는 읽은 글자가 들어가면 안 된다.
		// 앞뒤가 맞지 않는 판단의 문구에는 코어가 정한 이름이 들어가는데, 그것은 읽어 온 글자가 아니다.
		unknownID bool
	}{
		{name: "관찰됨", item: "sleep", status: "observed", explicitness: "direct",
			wantItem: signal.Sleep, wantStatus: signal.Observed, wantExplicitness: signal.Direct},
		{name: "언급 없음", item: "self_blame", status: "not_mentioned", explicitness: "none",
			wantItem: signal.SelfBlame, wantStatus: signal.NotMentioned, wantExplicitness: signal.None},
		{name: "모르는 항목", item: "hopeless", status: "observed", explicitness: "direct", wantErr: true, unknownID: true},
		{name: "모르는 판단", item: "sleep", status: "maybe", explicitness: "direct", wantErr: true, unknownID: true},
		{name: "모르는 명시성", item: "sleep", status: "observed", explicitness: "guessed", wantErr: true, unknownID: true},
		{name: "앞뒤가 맞지 않는 판단", item: "sleep", status: "observed", explicitness: "none", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, judgement, err := store.ParseSignalJudgement(tt.item, tt.status, tt.explicitness)
			if tt.wantErr {
				require.Error(t, err)
				if tt.unknownID {
					assert.NotContains(t, err.Error(), tt.item, "오류 문구에 읽은 문자열을 담지 않는다")
					assert.NotContains(t, err.Error(), tt.status)
					assert.NotContains(t, err.Error(), tt.explicitness)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantItem, item)
			assert.Equal(t, tt.wantStatus, judgement.Status)
			assert.Equal(t, tt.wantExplicitness, judgement.Explicitness)
		})
	}
}

// signalRowFor는 그날 그 항목의 신호 행을 찾는다. 하루에 대화가 하나일 때만 쓴다.
func signalRowFor(t *testing.T, st *store.Store, userID uuid.UUID, d recorddate.Date, item string) db.ListSignalsByDateRow {
	t.Helper()
	rows, err := st.Queries().ListSignalsByDate(t.Context(), db.ListSignalsByDateParams{
		UserID: userID, RecordDate: pgDate(t, d),
	})
	require.NoError(t, err)
	for _, row := range rows {
		if row.Item == item {
			return row
		}
	}
	t.Fatalf("%s 항목의 신호 행이 없다", item)
	return db.ListSignalsByDateRow{}
}

// cancelledAtOf는 그 신호 행을 취소한 시각을 읽는다. 쿼리로는 취소 여부만 나오므로 여기서만 컬럼을 직접 본다.
func cancelledAtOf(t *testing.T, pool *pgxpool.Pool, signalID uuid.UUID) time.Time {
	t.Helper()
	var at *time.Time
	err := pool.QueryRow(t.Context(), `SELECT cancelled_at FROM signals WHERE id = $1`, signalID).Scan(&at)
	require.NoError(t, err)
	require.NotNil(t, at)
	return *at
}
