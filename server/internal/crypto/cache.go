package crypto

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

var errLoaderReturnedNil = errors.New("crypto: key loader returned no sealer and no error")

// LoadFunc는 캐시에 없을 때 불린다. 저장된 키 행을 읽어 KeyRing.Unwrap으로 푼 Sealer를 돌려준다.
type LoadFunc func(ctx context.Context) (*Sealer, error)

// KeyCacheOptions는 캐시의 한도다. 값은 설정에서 받아 넘긴다.
type KeyCacheOptions struct {
	// MaxEntries는 함께 들고 있을 사용자 수의 상한이다. 넘으면 가장 먼저 불러온 것부터 내보낸다.
	MaxEntries int

	// MaxAge는 풀어 둔 키를 키 행과 다시 맞춰 보지 않고 쓰는 최대 시간이다. 0이면 시간으로는 내보내지 않는다.
	// Forget은 이 프로세스에만 닿는다. 서버와 워커처럼 프로세스가 여럿이면
	// 다른 프로세스는 이 시간이 지나 키 행을 다시 읽을 때에야 계정이 지워진 것을 안다.
	// 마지막으로 쓴 때가 아니라 불러온 때부터 잰다. 자주 쓰는 사용자의 키도 같은 주기로 다시 확인된다.
	MaxAge time.Duration

	// Now는 주입받은 시계다. MaxAge를 쓸 때만 필요하다.
	Now func() time.Time
}

// KeyCache는 요청마다 키 행을 읽고 푸는 일을 줄이려고 사용자별 Sealer를 들고 있는다.
// 여러 고루틴이 함께 써도 된다.
//
// 내보내는 순서는 불러온 순서다. 최근에 썼는지는 보지 않는다.
// 놓쳤을 때 드는 값이 키 행 하나를 읽는 정도라 그것으로 충분하고,
// 순서가 하나뿐이어야 MaxAge가 지난 키를 쓰이지 않는 것까지 빠짐없이 내보낼 수 있다.
//
// 내보낼 때 0으로 덮을 바이트는 없다. 원본 키 바이트는 Sealer를 만들 때 이미 지웠고,
// 남은 것은 표준 라이브러리 안의 키 일정뿐이다. 그래서 내보내기는 참조를 끊는 일이고,
// 밀려난 Sealer를 쓰고 있던 요청은 끝까지 정상으로 돈다.
type KeyCache struct {
	maxEntries int
	maxAge     time.Duration
	now        func() time.Time

	mu      sync.Mutex
	loaded  *list.List // 앞이 가장 최근에 불러온 것이다.
	entries map[uuid.UUID]*list.Element
	// dropped는 Forget이나 Purge가 불릴 때마다 오른다.
	// 키를 불러오는 동안 계정이 지워졌는지 알아내는 데 쓴다.
	dropped uint64
}

type cacheEntry struct {
	userID   uuid.UUID
	sealer   *Sealer
	loadedAt time.Time
}

func NewKeyCache(opts KeyCacheOptions) (*KeyCache, error) {
	if opts.MaxEntries < 1 {
		return nil, fmt.Errorf("crypto: key cache needs room for at least one entry, got %d", opts.MaxEntries)
	}
	if opts.MaxAge < 0 {
		return nil, fmt.Errorf("crypto: key cache max age must not be negative, got %s", opts.MaxAge)
	}
	if opts.MaxAge > 0 && opts.Now == nil {
		return nil, errors.New("crypto: key cache with a max age needs a clock")
	}
	return &KeyCache{
		maxEntries: opts.MaxEntries,
		maxAge:     opts.MaxAge,
		now:        opts.Now,
		loaded:     list.New(),
		entries:    make(map[uuid.UUID]*list.Element),
	}, nil
}

// Get은 사용자의 Sealer를 돌려준다. 없으면 load로 불러와 넣어 둔다.
//
// load는 잠금 밖에서 돈다. DB를 읽는 동안 다른 사용자의 요청을 세우지 않으려는 것이다.
// 그래서 같은 사용자의 요청이 겹치면 load가 두 번 돌 수 있다. 결과는 같은 키이므로 해가 없다.
// load가 실패하면 아무것도 넣지 않는다.
func (c *KeyCache) Get(ctx context.Context, userID uuid.UUID, load LoadFunc) (*Sealer, error) {
	if load == nil {
		return nil, errors.New("crypto: key cache needs a loader")
	}

	c.mu.Lock()
	sealer := c.lookupLocked(userID)
	dropped := c.dropped
	c.mu.Unlock()
	if sealer != nil {
		return sealer, nil
	}

	sealer, err := load(ctx)
	if err != nil {
		return nil, fmt.Errorf("crypto: load user key: %w", err)
	}
	if sealer == nil {
		return nil, errLoaderReturnedNil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dropped != dropped {
		// 불러오는 사이에 Forget이 있었다. 방금 읽은 키 행이 그 직후에 지워졌을 수 있으므로
		// 이번 요청에만 쓰고 넣어 두지는 않는다. 넣으면 지워진 계정의 키가 캐시에 남는다.
		return sealer, nil
	}
	if existing := c.lookupLocked(userID); existing != nil {
		return existing, nil
	}
	c.insertLocked(userID, sealer)
	return sealer, nil
}

// Forget은 사용자의 키를 캐시에서 뺀다. 계정을 지울 때 키 행을 지운 다음에 부른다.
func (c *KeyCache) Forget(userID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropped++
	if element, ok := c.entries[userID]; ok {
		c.removeLocked(element)
	}
}

// Purge는 캐시를 통째로 비운다.
func (c *KeyCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropped++
	c.loaded.Init()
	clear(c.entries)
}

func (c *KeyCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireLocked()
	return len(c.entries)
}

func (c *KeyCache) lookupLocked(userID uuid.UUID) *Sealer {
	c.expireLocked()
	element, ok := c.entries[userID]
	if !ok {
		return nil
	}
	entry, ok := element.Value.(*cacheEntry)
	if !ok {
		return nil
	}
	return entry.sealer
}

func (c *KeyCache) insertLocked(userID uuid.UUID, sealer *Sealer) {
	entry := &cacheEntry{userID: userID, sealer: sealer}
	if c.maxAge > 0 {
		entry.loadedAt = c.now()
	}
	c.entries[userID] = c.loaded.PushFront(entry)
	for len(c.entries) > c.maxEntries {
		c.removeLocked(c.loaded.Back())
	}
}

// expireLocked는 조회할 때마다 돈다. 다시 찾지 않는 사용자의 키도 MaxAge를 넘겨 남지 않게 하려는 것이다.
// 목록이 불러온 순서라서 뒤에서부터 보다가 아직 유효한 것을 만나면 멈춘다.
func (c *KeyCache) expireLocked() {
	if c.maxAge <= 0 {
		return
	}
	now := c.now()
	for element := c.loaded.Back(); element != nil; element = c.loaded.Back() {
		entry, ok := element.Value.(*cacheEntry)
		if !ok {
			return
		}
		// 시계가 뒤로 가서 나이가 음수로 나오면 믿을 수 없는 값이므로 내보내는 쪽을 택한다.
		if age := now.Sub(entry.loadedAt); age >= 0 && age < c.maxAge {
			return
		}
		c.removeLocked(element)
	}
}

func (c *KeyCache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	if entry, ok := c.loaded.Remove(element).(*cacheEntry); ok {
		delete(c.entries, entry.userID)
	}
}
