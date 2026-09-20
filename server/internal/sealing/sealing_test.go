package sealing_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// 시험은 시계를 읽지 않는다.
var baseTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func newRing(t *testing.T, active int, versions ...int) *crypto.KeyRing {
	t.Helper()
	keys := make(map[int][]byte, len(versions))
	for _, v := range versions {
		key := make([]byte, 32)
		for i := range key {
			key[i] = byte(v*31 + i + 1)
		}
		keys[v] = key
	}
	ring, err := crypto.NewKeyRing(active, keys)
	require.NoError(t, err)
	return ring
}

func newCache(t *testing.T) *crypto.KeyCache {
	t.Helper()
	cache, err := crypto.NewKeyCache(crypto.KeyCacheOptions{MaxEntries: 16})
	require.NoError(t, err)
	return cache
}

// countingReader는 키 행을 몇 번 읽었는지 센다.
type countingReader struct {
	inner sealing.KeyReader
	reads atomic.Int64
}

func (r *countingReader) GetUserKey(ctx context.Context, userID uuid.UUID) (db.UserKey, error) {
	r.reads.Add(1)
	return r.inner.GetUserKey(ctx, userID)
}

// signUp은 가입이 하는 대로 데이터 키를 새로 만들어 감싼 꼴로 저장하고, 가입 때 받은 Sealer를 돌려준다.
func signUp(t *testing.T, st *store.Store, ring *crypto.KeyRing, email string) (uuid.UUID, *crypto.Sealer) {
	t.Helper()
	id, err := store.NewID()
	require.NoError(t, err)
	key, err := ring.NewUserKey(id)
	require.NoError(t, err)
	_, err = st.CreateUser(t.Context(), store.NewUser{
		ID: id, Email: email, Timezone: "Asia/Seoul",
		WrappedDEK: key.Wrapped, KEKVersion: int16(key.KEKVersion), Now: baseTime,
	})
	require.NoError(t, err)
	return id, key.Sealer
}

func TestNew(t *testing.T) {
	t.Parallel()
	ring, cache := newRing(t, 1, 1), newCache(t)
	reader := &countingReader{}

	tests := []struct {
		name  string
		keys  sealing.KeyReader
		ring  *crypto.KeyRing
		cache *crypto.KeyCache
	}{
		{"키 행을 읽을 곳이 없다", nil, ring, cache},
		{"마스터 키 묶음이 없다", reader, nil, cache},
		{"캐시가 없다", reader, ring, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sealing.New(tt.keys, tt.ring, tt.cache)
			require.Error(t, err)
		})
	}
}

func TestSealersFor(t *testing.T) {
	t.Parallel()

	t.Run("가입 때 잠근 글을 나중에 받은 Sealer로 연다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		ring := newRing(t, 1, 1)
		mina, signupSealer := signUp(t, st, ring, "mina@example.com")
		sealers, err := sealing.New(st.Queries(), ring, newCache(t))
		require.NoError(t, err)

		rowID := uuid.Must(uuid.NewV7())
		sealed, err := signupSealer.SealString("오늘은 좀 피곤했어", sealing.UtteranceText(rowID))
		require.NoError(t, err)

		sealer, err := sealers.For(t.Context(), mina)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, sealing.UtteranceText(rowID))
		require.NoError(t, err)
		assert.Equal(t, "오늘은 좀 피곤했어", opened)
	})

	t.Run("다른 사용자의 Sealer로는 열리지 않는다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		ring := newRing(t, 1, 1)
		mina, _ := signUp(t, st, ring, "mina@example.com")
		joon, _ := signUp(t, st, ring, "joon@example.com")
		sealers, err := sealing.New(st.Queries(), ring, newCache(t))
		require.NoError(t, err)

		minaSealer, err := sealers.For(t.Context(), mina)
		require.NoError(t, err)
		joonSealer, err := sealers.For(t.Context(), joon)
		require.NoError(t, err)

		rowID := uuid.Must(uuid.NewV7())
		sealed, err := minaSealer.SealString("일기", sealing.DiaryBody(rowID))
		require.NoError(t, err)
		_, err = joonSealer.OpenString(sealed, sealing.DiaryBody(rowID))
		require.ErrorIs(t, err, crypto.ErrDecrypt)
	})

	t.Run("두 번째부터는 키 행을 읽지 않고, 잊은 뒤에는 다시 읽는다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		ring := newRing(t, 1, 1)
		mina, _ := signUp(t, st, ring, "mina@example.com")
		reader := &countingReader{inner: st.Queries()}
		sealers, err := sealing.New(reader, ring, newCache(t))
		require.NoError(t, err)

		first, err := sealers.For(t.Context(), mina)
		require.NoError(t, err)
		second, err := sealers.For(t.Context(), mina)
		require.NoError(t, err)
		assert.Same(t, first, second)
		assert.EqualValues(t, 1, reader.reads.Load())

		sealers.Forget(mina)
		_, err = sealers.For(t.Context(), mina)
		require.NoError(t, err)
		assert.EqualValues(t, 2, reader.reads.Load())
	})

	t.Run("계정을 지우고 잊으면 더는 Sealer를 받지 못한다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		ring := newRing(t, 1, 1)
		mina, _ := signUp(t, st, ring, "mina@example.com")
		cache := newCache(t)
		sealers, err := sealing.New(st.Queries(), ring, cache)
		require.NoError(t, err)
		_, err = sealers.For(t.Context(), mina)
		require.NoError(t, err)

		_, err = st.Queries().DeleteUser(t.Context(), mina)
		require.NoError(t, err)
		sealers.Forget(mina)

		_, err = sealers.For(t.Context(), mina)
		require.ErrorIs(t, err, sealing.ErrNoKey)
		require.ErrorIs(t, err, store.ErrNotFound)
		assert.Zero(t, cache.Len(), "실패한 조회는 캐시에 아무것도 남기지 않는다")
	})

	t.Run("없는 사용자와 빈 ID는 Sealer를 받지 못한다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		sealers, err := sealing.New(st.Queries(), newRing(t, 1, 1), newCache(t))
		require.NoError(t, err)

		_, err = sealers.For(t.Context(), uuid.Must(uuid.NewV7()))
		require.ErrorIs(t, err, sealing.ErrNoKey)
		_, err = sealers.For(t.Context(), uuid.Nil)
		require.Error(t, err)
		assert.NotErrorIs(t, err, sealing.ErrNoKey)
	})

	t.Run("옛 마스터 키로 감싼 키도 그 버전을 들고 있으면 풀린다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		mina, signupSealer := signUp(t, st, newRing(t, 1, 1), "mina@example.com")
		rowID := uuid.Must(uuid.NewV7())
		sealed, err := signupSealer.SealString("지난달의 일기", sealing.DiaryBody(rowID))
		require.NoError(t, err)

		// 마스터 키를 2번으로 바꾼 뒤다. 1번은 아직 설정에 남아 있다.
		rotated, err := sealing.New(st.Queries(), newRing(t, 2, 1, 2), newCache(t))
		require.NoError(t, err)
		sealer, err := rotated.For(t.Context(), mina)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, sealing.DiaryBody(rowID))
		require.NoError(t, err)
		assert.Equal(t, "지난달의 일기", opened)

		// 1번을 너무 일찍 뺀 경우다. 조용히 새 키를 만들지 않고 실패한다.
		dropped, err := sealing.New(st.Queries(), newRing(t, 2, 2), newCache(t))
		require.NoError(t, err)
		_, err = dropped.For(t.Context(), mina)
		require.ErrorIs(t, err, crypto.ErrKeyVersion)
		assert.NotErrorIs(t, err, sealing.ErrNoKey)
	})

	t.Run("키 행을 읽다 난 오류는 키 없음으로 바뀌지 않고 그대로 나온다", func(t *testing.T) {
		t.Parallel()
		errDown := errors.New("database is down")
		sealers, err := sealing.New(failingReader{err: errDown}, newRing(t, 1, 1), newCache(t))
		require.NoError(t, err)

		_, err = sealers.For(t.Context(), uuid.Must(uuid.NewV7()))
		require.ErrorIs(t, err, errDown)
		assert.NotErrorIs(t, err, sealing.ErrNoKey, "잠깐의 장애를 계정이 지워진 것으로 읽으면 하던 작업을 버리게 된다")
	})

	t.Run("여러 고루틴이 한꺼번에 받아도 모두 같은 키다", func(t *testing.T) {
		t.Parallel()
		st := store.New(testdb.New(t))
		ring := newRing(t, 1, 1)
		mina, signupSealer := signUp(t, st, ring, "mina@example.com")
		sealers, err := sealing.New(st.Queries(), ring, newCache(t))
		require.NoError(t, err)

		rowID := uuid.Must(uuid.NewV7())
		sealed, err := signupSealer.SealString("같은 글", sealing.UtteranceText(rowID))
		require.NoError(t, err)

		const readers = 16
		opened := make([]string, readers)
		errs := make([]error, readers)
		var wg sync.WaitGroup
		for i := range readers {
			wg.Go(func() {
				sealer, err := sealers.For(t.Context(), mina)
				if err != nil {
					errs[i] = err
					return
				}
				opened[i], errs[i] = sealer.OpenString(sealed, sealing.UtteranceText(rowID))
			})
		}
		wg.Wait()
		for i := range readers {
			require.NoError(t, errs[i])
			assert.Equal(t, "같은 글", opened[i])
		}
	})
}

type failingReader struct{ err error }

func (r failingReader) GetUserKey(context.Context, uuid.UUID) (db.UserKey, error) {
	return db.UserKey{}, r.err
}

func TestSlots(t *testing.T) {
	t.Parallel()

	t.Run("스키마의 암호문 컬럼마다 자리가 하나씩 있다", func(t *testing.T) {
		t.Parallel()
		pool := testdb.New(t)
		rows, err := pool.Query(t.Context(), `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE table_schema = 'public' AND column_name LIKE '%\_enc'
			ORDER BY 1`)
		require.NoError(t, err)
		columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		require.NotEmpty(t, columns)

		// 컬럼을 더하고 자리를 빠뜨리면 쓰는 쪽이 이름을 직접 적게 된다. 자리만 있고 컬럼이 없으면 이름이 틀린 것이다.
		assert.ElementsMatch(t, columns, sealing.Columns())
	})

	t.Run("자리는 행 ID를 그대로 담고, 같은 행의 다른 컬럼과 섞이지 않는다", func(t *testing.T) {
		t.Parallel()
		rowID := uuid.Must(uuid.NewV7())
		slots := map[string]crypto.AAD{
			"utterances.text_enc":      sealing.UtteranceText(rowID),
			"gate_events.evidence_enc": sealing.GateEvidence(rowID),
			"diaries.draft_enc":        sealing.DiaryDraft(rowID),
			"diaries.body_enc":         sealing.DiaryBody(rowID),
			"signals.evidence_enc":     sealing.SignalEvidence(rowID),
			"memories.content_enc":     sealing.MemoryContent(rowID),
			"mood_picks.value_enc":     sealing.MoodPickValue(rowID),
			"self_checks.result_enc":   sealing.SelfCheckResult(rowID),
		}
		names := make([]string, 0, len(slots))
		for name, aad := range slots {
			assert.Equal(t, name, aad.Table+"."+aad.Column)
			assert.Equal(t, rowID, aad.RowID)
			names = append(names, name)
		}
		assert.ElementsMatch(t, sealing.Columns(), names)

		ring := newRing(t, 1, 1)
		key, err := ring.NewUserKey(uuid.Must(uuid.NewV7()))
		require.NoError(t, err)
		draft, err := key.Sealer.SealString("초안", sealing.DiaryDraft(rowID))
		require.NoError(t, err)
		_, err = key.Sealer.OpenString(draft, sealing.DiaryBody(rowID))
		require.ErrorIs(t, err, crypto.ErrDecrypt, "초안의 암호문을 확인한 글의 컬럼에 옮겨 놓아도 열리지 않는다")
	})
}
