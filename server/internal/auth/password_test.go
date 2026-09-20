package auth

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"
)

// cheapParams는 시험을 빨리 돌리려고 쓰는 값이다. 운영에서 쓸 값이 아니다.
var cheapParams = Argon2Params{MemoryKiB: 64, Time: 1, Parallelism: 1}

const testPassword = "오늘도 수고했어요, 내일 봐요"

func newTestHasher(t *testing.T, params Argon2Params, maxConcurrent int) *Hasher {
	t.Helper()
	h, err := NewHasher(t.Context(), params, maxConcurrent)
	require.NoError(t, err)
	return h
}

func TestHasher_HashAndVerify(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 2)
	ctx := t.Context()

	encoded, err := h.Hash(ctx, testPassword)
	require.NoError(t, err)

	t.Run("매개변수를 함께 적은 문자열로 저장한다", func(t *testing.T) {
		t.Parallel()
		assert.Regexp(t, `^\$argon2id\$v=19\$m=64,t=1,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, encoded)
		assert.NotContains(t, encoded, testPassword)
	})

	t.Run("맞는 비밀번호는 통과하고 틀린 비밀번호는 통과하지 못한다", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			password string
			want     bool
		}{
			{"같은 비밀번호", testPassword, true},
			{"한 글자 다른 비밀번호", testPassword + "!", false},
			{"대소문자만 다른 것도 다른 비밀번호다", strings.ToUpper("abc") + testPassword, false},
			{"빈 비밀번호", "", false},
			{"다듬기 전의 한도를 넘는 입력", strings.Repeat("a", 2000), false},
			{"글자로 읽을 수 없는 입력", "\xff\xfe\xfd", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := h.Verify(ctx, encoded, tt.password)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("같은 비밀번호여도 해시는 매번 다르다", func(t *testing.T) {
		t.Parallel()
		again, err := h.Hash(ctx, testPassword)
		require.NoError(t, err)
		assert.NotEqual(t, encoded, again, "소금을 매번 새로 뽑는다")
	})

	t.Run("받을 수 없는 입력으로는 해시를 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		_, err := h.Hash(ctx, strings.Repeat("a", 2000))
		require.ErrorIs(t, err, ErrWeakPassword)
		_, err = h.Hash(ctx, "\xff\xfe\xfd")
		require.ErrorIs(t, err, ErrWeakPassword)
	})
}

func TestHasher_UnicodeForms(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 2)
	ctx := t.Context()

	tests := []struct {
		name    string
		stored  string
		entered string
	}{
		{"완성형으로 정한 한글 비밀번호를 자모를 늘어놓은 꼴로 넣는다", "오늘도수고했어요내일", norm.NFD.String("오늘도수고했어요내일")},
		{"자모를 늘어놓은 꼴로 정한 비밀번호를 완성형으로 넣는다", norm.NFD.String("저녁에는 산책을 해요"), "저녁에는 산책을 해요"},
		{"반각으로 정한 비밀번호를 전각으로 넣는다", "Walk2026!!", "Ｗａｌｋ２０２６！！"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NotEqual(t, tt.stored, tt.entered, "두 입력은 바이트로는 달라야 한다")

			encoded, err := h.Hash(ctx, tt.stored)
			require.NoError(t, err)
			ok, err := h.Verify(ctx, encoded, tt.entered)
			require.NoError(t, err)
			assert.True(t, ok, "같은 글자는 어떤 꼴로 들어와도 같은 비밀번호다")
		})
	}
}

func TestHasher_NeedsRehash(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	old := newTestHasher(t, cheapParams, 1)
	encoded, err := old.Hash(ctx, testPassword)
	require.NoError(t, err)

	tests := []struct {
		name   string
		params Argon2Params
		want   bool
	}{
		{"설정이 그대로면 다시 만들지 않는다", cheapParams, false},
		{"메모리를 올리면 다시 만든다", Argon2Params{MemoryKiB: 128, Time: 1, Parallelism: 1}, true},
		{"반복 수를 올리면 다시 만든다", Argon2Params{MemoryKiB: 64, Time: 2, Parallelism: 1}, true},
		{"병렬 수를 바꾸면 다시 만든다", Argon2Params{MemoryKiB: 64, Time: 1, Parallelism: 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			current := newTestHasher(t, tt.params, 1)
			assert.Equal(t, tt.want, current.NeedsRehash(encoded))

			// 설정이 바뀌어도 옛 해시는 거기 적힌 값대로 확인된다.
			ok, err := current.Verify(ctx, encoded, testPassword)
			require.NoError(t, err)
			assert.True(t, ok)
		})
	}

	t.Run("읽을 수 없는 해시는 다시 만들어야 하는 것으로 친다", func(t *testing.T) {
		t.Parallel()
		assert.True(t, old.NeedsRehash("not-a-hash"))
	})
}

func TestHasher_MalformedHash(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 1)

	const salt = "c29tZXNhbHQxMjM0NTY"
	const key = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI"
	tests := []struct {
		name    string
		encoded string
	}{
		{"빈 문자열", ""},
		{"해시가 아닌 글자", "plaintext-password"},
		{"다른 알고리즘", "$argon2i$v=19$m=64,t=1,p=1$" + salt + "$" + key},
		{"bcrypt 해시", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"모르는 판", "$argon2id$v=16$m=64,t=1,p=1$" + salt + "$" + key},
		{"칸이 모자란다", "$argon2id$v=19$m=64,t=1,p=1$" + salt},
		{"칸이 남는다", "$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + key + "$extra"},
		{"매개변수의 순서가 다르다", "$argon2id$v=19$t=1,m=64,p=1$" + salt + "$" + key},
		{"매개변수가 숫자가 아니다", "$argon2id$v=19$m=lots,t=1,p=1$" + salt + "$" + key},
		{"매개변수에 부호가 붙었다", "$argon2id$v=19$m=+64,t=1,p=1$" + salt + "$" + key},
		{"매개변수가 빠졌다", "$argon2id$v=19$m=64,t=1$" + salt + "$" + key},
		{"병렬 수가 0이다", "$argon2id$v=19$m=64,t=1,p=0$" + salt + "$" + key},
		{"병렬 수가 한 바이트를 넘는다", "$argon2id$v=19$m=4096,t=1,p=256$" + salt + "$" + key},
		{"반복 수가 0이다", "$argon2id$v=19$m=64,t=0,p=1$" + salt + "$" + key},
		{"메모리가 계산에 필요한 최소보다 작다", "$argon2id$v=19$m=7,t=1,p=1$" + salt + "$" + key},
		{"메모리를 터무니없이 크게 적었다", "$argon2id$v=19$m=4294967295,t=1,p=1$" + salt + "$" + key},
		{"반복 수를 터무니없이 크게 적었다", "$argon2id$v=19$m=64,t=4294967295,p=1$" + salt + "$" + key},
		{"소금이 base64가 아니다", "$argon2id$v=19$m=64,t=1,p=1$!!!$" + key},
		{"소금에 채움 글자가 붙었다", "$argon2id$v=19$m=64,t=1,p=1$" + salt + "=$" + key},
		{"소금이 너무 짧다", "$argon2id$v=19$m=64,t=1,p=1$YWJj$" + key},
		{"해시가 너무 짧다", "$argon2id$v=19$m=64,t=1,p=1$" + salt + "$YWJj"},
		{"해시가 비었다", "$argon2id$v=19$m=64,t=1,p=1$" + salt + "$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ok, err := h.Verify(t.Context(), tt.encoded, testPassword)
			require.ErrorIs(t, err, ErrMalformedHash)
			assert.False(t, ok)
			if len(tt.encoded) > 8 {
				assert.NotContains(t, err.Error(), tt.encoded, "오류 문구에 해시를 옮겨 적지 않는다")
			}
		})
	}

	// 나란히 도는 위의 시험들은 이 함수가 끝난 뒤에야 시작한다. 그래서 여기서는 계산 횟수가 다른 시험과 섞이지 않는다.
	t.Run("읽을 수 없는 해시로는 계산을 시작하지 않는다", func(t *testing.T) {
		// 메모리를 4 TiB로 적은 해시도 들어 있다. 계산을 시작했다면 여기까지 오지 못한다.
		before := h.computed.Load()
		for _, tt := range tests {
			_, err := h.Verify(t.Context(), tt.encoded, testPassword)
			require.ErrorIs(t, err, ErrMalformedHash, tt.name)
		}
		assert.Equal(t, before, h.computed.Load())
	})
}

func TestNewHasher_RejectsBadSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		params        Argon2Params
		maxConcurrent int
	}{
		{"병렬 수가 0이다", Argon2Params{MemoryKiB: 64, Time: 1, Parallelism: 0}, 1},
		{"반복 수가 0이다", Argon2Params{MemoryKiB: 64, Time: 0, Parallelism: 1}, 1},
		{"메모리가 병렬 수의 8배보다 작다", Argon2Params{MemoryKiB: 15, Time: 1, Parallelism: 2}, 1},
		{"메모리가 1 GiB를 넘는다", Argon2Params{MemoryKiB: 1<<20 + 1, Time: 1, Parallelism: 1}, 1},
		{"동시에 계산할 수 있는 수가 0이다", cheapParams, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, err := NewHasher(t.Context(), tt.params, tt.maxConcurrent)
			require.Error(t, err)
			assert.Nil(t, h)
		})
	}

	t.Run("기본값은 메모리 19 MiB, 2회, 한 갈래다", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, Argon2Params{MemoryKiB: 19456, Time: 2, Parallelism: 1}, DefaultArgon2Params())
	})
}

func TestHasher_Dummy(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 1)
	ctx := t.Context()

	t.Run("가짜 해시는 지금 설정으로 만든 진짜 해시와 같은 꼴이다", func(t *testing.T) {
		t.Parallel()
		assert.False(t, h.NeedsRehash(h.dummy), "계산 비용이 새로 가입한 계정의 해시와 같아야 한다")
	})

	t.Run("가짜 해시를 확인할 때도 해시를 한 번 계산한다", func(t *testing.T) {
		before := h.computed.Load()
		require.NoError(t, h.VerifyDummy(ctx, testPassword))
		assert.Equal(t, before+1, h.computed.Load())
	})

	t.Run("가짜 해시는 어떤 비밀번호와도 맞지 않는다", func(t *testing.T) {
		t.Parallel()
		for _, password := range []string{"", testPassword, "password123"} {
			ok, err := h.Verify(ctx, h.dummy, password)
			require.NoError(t, err)
			assert.False(t, ok)
		}
	})

	t.Run("만들 때마다 다른 가짜 해시가 나온다", func(t *testing.T) {
		t.Parallel()
		other := newTestHasher(t, cheapParams, 1)
		assert.NotEqual(t, h.dummy, other.dummy)
	})

	t.Run("어떻게 출력해도 가짜 해시가 나오지 않는다", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Info("hasher ready", "hasher", h)
		out := buf.String() + fmt.Sprintf("%v %+v %s", h, h, h)
		assert.NotContains(t, out, h.dummy)
		assert.NotContains(t, out, "argon2id")
		assert.Contains(t, buf.String(), `"memory_kib":64`)
	})
}

func TestHasher_BoundsConcurrentComputations(t *testing.T) {
	t.Parallel()
	const limit = 3
	const callers = 12

	h := newTestHasher(t, cheapParams, limit)

	var running, peak atomic.Int64
	entered := make(chan struct{}, callers)
	release := make(chan struct{})
	h.derive = func(_, _ []byte, _, _ uint32, _ uint8, keyLen uint32) []byte {
		now := running.Add(1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		running.Add(-1)
		return make([]byte, keyLen)
	}

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			_, err := h.Hash(context.Background(), testPassword)
			assert.NoError(t, err)
		})
	}

	// 한도만큼은 들어온다.
	for range limit {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("한도만큼의 계산이 시작되지 않았다")
		}
	}
	// 나머지는 자리가 날 때까지 들어오지 못한다.
	select {
	case <-entered:
		t.Fatal("한도를 넘어 계산이 시작됐다")
	case <-time.After(200 * time.Millisecond):
	}
	assert.Equal(t, int64(limit), running.Load())

	close(release)
	wg.Wait()
	assert.Equal(t, int64(limit), peak.Load(), "동시에 돈 계산이 한도를 넘지 않아야 한다")
	assert.Zero(t, running.Load())
}

func TestHasher_DoesNotStartForCancelledRequest(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 4)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// 자리는 넉넉히 비어 있다. 그래도 이미 끝난 요청의 계산은 시작하지 않는다.
	for range 50 {
		before := h.computed.Load()
		_, err := h.Hash(ctx, testPassword)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, h.VerifyDummy(ctx, testPassword), context.Canceled)
		require.Equal(t, before, h.computed.Load())
	}

	t.Run("끝난 컨텍스트로는 Hasher를 만들지도 않는다", func(t *testing.T) {
		t.Parallel()
		_, err := NewHasher(ctx, cheapParams, 1)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestHasher_StopsWaitingWhenRequestIsCancelled(t *testing.T) {
	t.Parallel()
	h := newTestHasher(t, cheapParams, 1)

	entered := make(chan struct{})
	release := make(chan struct{})
	h.derive = func(_, _ []byte, _, _ uint32, _ uint8, keyLen uint32) []byte {
		close(entered)
		<-release
		return make([]byte, keyLen)
	}

	done := make(chan error, 1)
	go func() {
		_, err := h.Hash(context.Background(), testPassword)
		done <- err
	}()
	<-entered

	// 하나뿐인 자리가 차 있다. 부를 때는 살아 있던 요청이 기다리는 도중에 끝나면, 자리를 더 기다리지 않고 물러난다.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := h.Hash(ctx, testPassword)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "wait for a free hashing slot")
	assert.NotContains(t, err.Error(), testPassword)

	ok, err := h.Verify(ctx, h.dummy, testPassword)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ok)

	close(release)
	require.NoError(t, <-done)
}
