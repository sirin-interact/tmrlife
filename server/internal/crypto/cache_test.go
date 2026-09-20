package crypto

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock은 시험이 직접 돌리는 시계다.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 3, 1, 21, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// keyStore는 키 행이 담긴 테이블을 흉내 낸다. 사용자마다 몇 번 읽혔는지 센다.
type keyStore struct {
	mu      sync.Mutex
	sealers map[uuid.UUID]*Sealer
	reads   map[uuid.UUID]int
}

func newKeyStore(t *testing.T, users ...uuid.UUID) *keyStore {
	t.Helper()
	ring := newTestRing(t)
	store := &keyStore{sealers: map[uuid.UUID]*Sealer{}, reads: map[uuid.UUID]int{}}
	for _, userID := range users {
		store.sealers[userID] = newTestSealer(t, ring, userID)
	}
	return store
}

func (s *keyStore) loader(userID uuid.UUID) LoadFunc {
	return func(context.Context) (*Sealer, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reads[userID]++
		sealer, ok := s.sealers[userID]
		if !ok {
			return nil, errKeyRowMissing
		}
		return sealer, nil
	}
}

func (s *keyStore) readsOf(userID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[userID]
}

var errKeyRowMissing = errors.New("key row missing")

func newTestCache(t *testing.T, opts KeyCacheOptions) *KeyCache {
	t.Helper()
	cache, err := NewKeyCache(opts)
	require.NoError(t, err)
	return cache
}

func TestNewKeyCache_Validation(t *testing.T) {
	clock := newFakeClock()
	tests := []struct {
		name    string
		opts    KeyCacheOptions
		wantErr bool
	}{
		{"자리가 0이면 거부한다", KeyCacheOptions{MaxEntries: 0}, true},
		{"자리가 음수면 거부한다", KeyCacheOptions{MaxEntries: -1}, true},
		{"보관 시간이 음수면 거부한다", KeyCacheOptions{MaxEntries: 1, MaxAge: -time.Second, Now: clock.Now}, true},
		{"보관 시간을 주고 시계를 안 주면 거부한다", KeyCacheOptions{MaxEntries: 1, MaxAge: time.Minute}, true},
		{"보관 시간이 없으면 시계도 필요 없다", KeyCacheOptions{MaxEntries: 1}, false},
		{"보관 시간과 시계를 함께 주면 된다", KeyCacheOptions{MaxEntries: 8, MaxAge: time.Minute, Now: clock.Now}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache, err := NewKeyCache(tt.opts)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, cache)
				return
			}
			require.NoError(t, err)
			assert.Zero(t, cache.Len())
		})
	}
}

func TestKeyCache_Get(t *testing.T) {
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	t.Run("처음에만 불러오고 다음부터는 들고 있던 것을 준다", func(t *testing.T) {
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		first, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		second, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)

		assert.Same(t, first, second)
		assert.Equal(t, 1, store.readsOf(userA))
		assert.Equal(t, 1, cache.Len())
	})

	t.Run("사용자마다 자기 키를 받는다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		forA, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		forB, err := cache.Get(ctx, userB, store.loader(userB))
		require.NoError(t, err)

		assert.Same(t, store.sealers[userA], forA)
		assert.Same(t, store.sealers[userB], forB)
		assert.NotSame(t, forA, forB)
	})

	t.Run("불러오기에 실패하면 넣어 두지 않고 원래 오류를 알 수 있게 돌려준다", func(t *testing.T) {
		store := newKeyStore(t)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		sealer, err := cache.Get(ctx, userA, store.loader(userA))
		require.ErrorIs(t, err, errKeyRowMissing)
		assert.Nil(t, sealer)
		assert.Zero(t, cache.Len())

		_, err = cache.Get(ctx, userA, store.loader(userA))
		require.ErrorIs(t, err, errKeyRowMissing)
		assert.Equal(t, 2, store.readsOf(userA), "실패를 기억해 두면 가입 직후의 사용자가 막힌다")
	})

	t.Run("불러오는 함수가 아무것도 돌려주지 않으면 오류다", func(t *testing.T) {
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})
		sealer, err := cache.Get(ctx, userA, func(context.Context) (*Sealer, error) { return nil, nil })
		require.Error(t, err)
		assert.Nil(t, sealer)
		assert.Zero(t, cache.Len())
	})

	t.Run("불러오는 함수가 없으면 오류다", func(t *testing.T) {
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})
		_, err := cache.Get(ctx, userA, nil)
		require.Error(t, err)
	})

	t.Run("불러오는 함수는 호출한 쪽의 컨텍스트를 받는다", func(t *testing.T) {
		type ctxKey struct{}
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		var seen any
		_, err := cache.Get(context.WithValue(ctx, ctxKey{}, "turn-1"), userA, func(ctx context.Context) (*Sealer, error) {
			seen = ctx.Value(ctxKey{})
			return store.loader(userA)(ctx)
		})
		require.NoError(t, err)
		assert.Equal(t, "turn-1", seen)
	})

	t.Run("같은 사용자를 겹쳐 불러오면 먼저 들어간 것을 함께 쓴다", func(t *testing.T) {
		ring := newTestRing(t)
		early, late := newTestSealer(t, ring, userA), newTestSealer(t, ring, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		got, err := cache.Get(ctx, userA, func(ctx context.Context) (*Sealer, error) {
			// 바깥쪽이 불러오는 동안 다른 요청이 먼저 끝난 상황이다.
			_, err := cache.Get(ctx, userA, func(context.Context) (*Sealer, error) { return early, nil })
			return late, err
		})
		require.NoError(t, err)
		assert.Same(t, early, got)
		assert.Equal(t, 1, cache.Len())
	})
}

func TestKeyCache_Eviction(t *testing.T) {
	ctx := context.Background()
	userA, userB, userC := uuid.New(), uuid.New(), uuid.New()

	t.Run("자리가 차면 가장 먼저 불러온 것이 나간다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB, userC)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 2})

		for _, userID := range []uuid.UUID{userA, userB, userC} {
			_, err := cache.Get(ctx, userID, store.loader(userID))
			require.NoError(t, err)
		}
		assert.Equal(t, 2, cache.Len())

		_, err := cache.Get(ctx, userB, store.loader(userB))
		require.NoError(t, err)
		_, err = cache.Get(ctx, userC, store.loader(userC))
		require.NoError(t, err)
		assert.Equal(t, 1, store.readsOf(userB), "B는 남아 있어야 한다")
		assert.Equal(t, 1, store.readsOf(userC), "C는 남아 있어야 한다")

		_, err = cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 2, store.readsOf(userA), "A는 나갔으므로 다시 불러와야 한다")
		assert.Equal(t, 2, cache.Len())
	})

	t.Run("방금 썼더라도 먼저 불러온 것이 먼저 나간다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB, userC)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 2})

		for _, userID := range []uuid.UUID{userA, userB, userA, userC} {
			_, err := cache.Get(ctx, userID, store.loader(userID))
			require.NoError(t, err)
		}
		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 2, store.readsOf(userA))
	})

	t.Run("밀려난 Sealer를 들고 있던 요청은 끝까지 쓸 수 있다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 1})

		held, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		_, err = cache.Get(ctx, userB, store.loader(userB))
		require.NoError(t, err)
		require.Equal(t, 1, cache.Len())

		sealed, err := held.SealString(written, diaryAAD())
		require.NoError(t, err)
		opened, err := store.sealers[userA].OpenString(sealed, diaryAAD())
		require.NoError(t, err, "밀려났다고 키가 0으로 바뀌면 누구나 아는 키로 글을 잠그게 된다")
		assert.Equal(t, written, opened)
	})
}

func TestKeyCache_Forget(t *testing.T) {
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	t.Run("잊은 사용자는 다시 불러와야 한다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})
		for _, userID := range []uuid.UUID{userA, userB} {
			_, err := cache.Get(ctx, userID, store.loader(userID))
			require.NoError(t, err)
		}

		cache.Forget(userA)
		assert.Equal(t, 1, cache.Len())

		delete(store.sealers, userA)
		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.ErrorIs(t, err, errKeyRowMissing, "키 행이 지워진 뒤에는 캐시가 대신 답해 주면 안 된다")

		_, err = cache.Get(ctx, userB, store.loader(userB))
		require.NoError(t, err)
		assert.Equal(t, 1, store.readsOf(userB), "다른 사용자의 키는 그대로다")
	})

	t.Run("들고 있지 않은 사용자를 잊어도 아무 일 없다", func(t *testing.T) {
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})
		cache.Forget(userA)
		assert.Zero(t, cache.Len())
	})

	t.Run("불러오는 사이에 잊었으면 넣어 두지 않는다", func(t *testing.T) {
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})

		sealer, err := cache.Get(ctx, userA, func(ctx context.Context) (*Sealer, error) {
			loaded, err := store.loader(userA)(ctx)
			// 키 행을 읽은 직후에 계정 삭제가 끝난 상황이다.
			cache.Forget(userA)
			return loaded, err
		})
		require.NoError(t, err)
		assert.NotNil(t, sealer, "이미 시작한 요청은 마저 돌 수 있다")
		assert.Zero(t, cache.Len(), "지워진 계정의 키가 캐시에 남으면 안 된다")
	})

	t.Run("Purge는 모두 비운다", func(t *testing.T) {
		store := newKeyStore(t, userA, userB)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4})
		for _, userID := range []uuid.UUID{userA, userB} {
			_, err := cache.Get(ctx, userID, store.loader(userID))
			require.NoError(t, err)
		}

		cache.Purge()
		assert.Zero(t, cache.Len())

		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 2, store.readsOf(userA))
		assert.Equal(t, 1, cache.Len(), "비운 뒤에도 계속 쓸 수 있다")
	})
}

func TestKeyCache_MaxAge(t *testing.T) {
	ctx := context.Background()
	const maxAge = 10 * time.Minute
	userA, userB := uuid.New(), uuid.New()

	t.Run("시간이 다 되기 전에는 들고 있던 것을 준다", func(t *testing.T) {
		clock := newFakeClock()
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: maxAge, Now: clock.Now})

		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		clock.Advance(maxAge - time.Nanosecond)
		_, err = cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 1, store.readsOf(userA))
	})

	t.Run("시간이 다 되면 키 행을 다시 읽는다", func(t *testing.T) {
		clock := newFakeClock()
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: maxAge, Now: clock.Now})

		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		clock.Advance(maxAge)
		_, err = cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 2, store.readsOf(userA))
	})

	t.Run("자주 써도 불러온 때부터 잰다", func(t *testing.T) {
		clock := newFakeClock()
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: maxAge, Now: clock.Now})

		for range 4 {
			_, err := cache.Get(ctx, userA, store.loader(userA))
			require.NoError(t, err)
			clock.Advance(maxAge / 3)
		}
		assert.Equal(t, 2, store.readsOf(userA), "쓸 때마다 시간이 늘어나면 지워진 계정의 키가 끝없이 남는다")
	})

	t.Run("다시 찾지 않는 사용자의 키도 시간이 지나면 나간다", func(t *testing.T) {
		clock := newFakeClock()
		store := newKeyStore(t, userA, userB)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: maxAge, Now: clock.Now})

		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		clock.Advance(maxAge / 2)
		_, err = cache.Get(ctx, userB, store.loader(userB))
		require.NoError(t, err)
		require.Equal(t, 2, cache.Len())

		clock.Advance(maxAge / 2)
		assert.Equal(t, 1, cache.Len(), "A만 시간이 다 됐다")
		clock.Advance(maxAge / 2)
		assert.Zero(t, cache.Len())
	})

	t.Run("시계가 뒤로 가면 들고 있던 것을 믿지 않는다", func(t *testing.T) {
		clock := newFakeClock()
		store := newKeyStore(t, userA)
		cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: maxAge, Now: clock.Now})

		_, err := cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		clock.Advance(-time.Hour)
		_, err = cache.Get(ctx, userA, store.loader(userA))
		require.NoError(t, err)
		assert.Equal(t, 2, store.readsOf(userA))
	})
}

func TestKeyCache_ConcurrentUse(t *testing.T) {
	const (
		workers    = 12
		iterations = 400
		userCount  = 16
	)
	ctx := context.Background()
	users := make([]uuid.UUID, userCount)
	for i := range users {
		users[i] = uuid.New()
	}
	store := newKeyStore(t, users...)
	clock := newFakeClock()
	// 자리를 사용자 수보다 훨씬 작게 잡아 밀어내기가 계속 일어나게 한다.
	cache := newTestCache(t, KeyCacheOptions{MaxEntries: 4, MaxAge: time.Minute, Now: clock.Now})

	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 0x5eed))
			for i := range iterations {
				userID := users[random.IntN(userCount)]
				switch random.IntN(20) {
				case 0:
					cache.Forget(userID)
					continue
				case 1:
					clock.Advance(7 * time.Second)
				case 2:
					if cache.Len() > 4 {
						failures <- errors.New("cache grew past its bound")
						return
					}
				case 3:
					if worker == 0 && i%100 == 0 {
						cache.Purge()
					}
				}

				sealer, err := cache.Get(ctx, userID, store.loader(userID))
				if err != nil {
					failures <- fmt.Errorf("get: %w", err)
					return
				}
				aad := AAD{Table: "memories", Column: "body_enc", RowID: uuid.New()}
				sealed, err := sealer.SealString(written, aad)
				if err != nil {
					failures <- fmt.Errorf("seal: %w", err)
					return
				}
				// 캐시가 남의 키를 내주면 그 사용자의 진짜 키로는 열리지 않는다.
				if _, err := store.sealers[userID].OpenString(sealed, aad); err != nil {
					failures <- fmt.Errorf("cache handed out another user's key: %w", err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	assert.LessOrEqual(t, cache.Len(), 4)
}
