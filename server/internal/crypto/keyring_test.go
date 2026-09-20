package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKeyRing_Validation(t *testing.T) {
	const beyondSmallint = math.MaxInt16 + 1

	tests := []struct {
		name    string
		active  int
		keys    map[int][]byte
		wantErr error
	}{
		{"키가 하나도 없으면 거부한다", 1, map[int][]byte{}, ErrInvalidKey},
		{"키 묶음이 nil이면 거부한다", 1, nil, ErrInvalidKey},
		{"활성 버전의 키가 없으면 거부한다", 2, map[int][]byte{1: filledKey(0x11)}, ErrKeyVersion},
		{"활성 버전이 0이면 거부한다", 0, map[int][]byte{1: filledKey(0x11)}, ErrKeyVersion},
		{"버전 0인 키는 거부한다", 1, map[int][]byte{0: filledKey(0x22), 1: filledKey(0x11)}, ErrKeyVersion},
		{"버전이 음수인 키는 거부한다", 1, map[int][]byte{-1: filledKey(0x22), 1: filledKey(0x11)}, ErrKeyVersion},
		{"버전이 DB 컬럼에 들어가지 않으면 거부한다", 1, map[int][]byte{1: filledKey(0x11), beyondSmallint: filledKey(0x22)}, ErrKeyVersion},
		{"31바이트 키는 거부한다", 1, map[int][]byte{1: bytes.Repeat([]byte{0x11}, 31)}, ErrInvalidKey},
		{"33바이트 키는 거부한다", 1, map[int][]byte{1: bytes.Repeat([]byte{0x11}, 33)}, ErrInvalidKey},
		{"AES-128 길이의 키는 거부한다", 1, map[int][]byte{1: bytes.Repeat([]byte{0x11}, 16)}, ErrInvalidKey},
		{"빈 키는 거부한다", 1, map[int][]byte{1: {}}, ErrInvalidKey},
		{"활성이 아닌 키의 길이가 틀려도 거부한다", 2, map[int][]byte{1: {0x01}, 2: filledKey(0x22)}, ErrInvalidKey},
		{"0으로만 찬 키는 거부한다", 1, map[int][]byte{1: make([]byte, keySize)}, ErrInvalidKey},
		{"두 버전이 같은 키면 거부한다", 2, map[int][]byte{1: filledKey(0x11), 2: filledKey(0x11)}, ErrInvalidKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ring, err := NewKeyRing(tt.active, tt.keys)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, ring)
			for _, key := range tt.keys {
				if len(key) < 8 {
					continue
				}
				assert.NotContains(t, err.Error(), hex.EncodeToString(key[:8]))
				assert.NotContains(t, err.Error(), base64.StdEncoding.EncodeToString(key)[:8])
			}
		})
	}

	t.Run("키 하나로 만들 수 있다", func(t *testing.T) {
		ring, err := NewKeyRing(1, map[int][]byte{1: filledKey(0x11)})
		require.NoError(t, err)
		assert.Equal(t, 1, ring.ActiveVersion())
		assert.Equal(t, []int{1}, ring.Versions())
	})

	t.Run("버전이 이어지지 않아도 되고 활성 버전이 가장 크지 않아도 된다", func(t *testing.T) {
		ring, err := NewKeyRing(3, map[int][]byte{1: filledKey(0x11), 3: filledKey(0x33), 7: filledKey(0x77)})
		require.NoError(t, err)
		assert.Equal(t, 3, ring.ActiveVersion())
		assert.Equal(t, []int{1, 3, 7}, ring.Versions())
	})

	t.Run("DB 컬럼에 들어가는 가장 큰 버전까지는 받는다", func(t *testing.T) {
		ring, err := NewKeyRing(math.MaxInt16, map[int][]byte{math.MaxInt16: filledKey(0x11)})
		require.NoError(t, err)
		userKey, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)
		assert.Equal(t, math.MaxInt16, userKey.KEKVersion)
		_, err = ring.Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
	})

	t.Run("만든 뒤에 넘긴 쪽이 키를 지워도 묶음은 그대로 돈다", func(t *testing.T) {
		key := filledKey(0x11)
		ring, err := NewKeyRing(1, map[int][]byte{1: key})
		require.NoError(t, err)
		userKey, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)

		clear(key)

		_, err = ring.Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
		assert.Equal(t, make([]byte, keySize), key, "묶음이 넘겨받은 바이트를 도로 채워 넣지도 않는다")
	})
}

func TestNewKeyRingFromBase64(t *testing.T) {
	encode := base64.StdEncoding.EncodeToString
	const leak = "this-is-not-base64-but-looks-like-a-pasted-secret!!"

	t.Run("base64로 적힌 키를 읽는다", func(t *testing.T) {
		ring, err := NewKeyRingFromBase64(2, map[int]string{1: encode(filledKey(0x11)), 2: encode(filledKey(0x22))})
		require.NoError(t, err)
		assert.Equal(t, 2, ring.ActiveVersion())
		assert.Equal(t, []int{1, 2}, ring.Versions())
	})

	t.Run("같은 키를 바이트로 넘긴 묶음과 서로 풀 수 있다", func(t *testing.T) {
		fromText, err := NewKeyRingFromBase64(1, map[int]string{1: encode(filledKey(0x11))})
		require.NoError(t, err)
		userKey, err := fromText.NewUserKey(testUserID)
		require.NoError(t, err)

		_, err = newTestRing(t).Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
	})

	t.Run("앞뒤 공백과 줄바꿈은 무시한다", func(t *testing.T) {
		_, err := NewKeyRingFromBase64(1, map[int]string{1: "  " + encode(filledKey(0x11)) + "\n"})
		require.NoError(t, err)
	})

	failures := []struct {
		name    string
		active  int
		encoded map[int]string
		wantErr error
	}{
		{"base64가 아니면 거부한다", 1, map[int]string{1: leak}, ErrInvalidKey},
		{"URL용 base64는 거부한다", 1, map[int]string{1: base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, keySize))}, ErrInvalidKey},
		{"풀었을 때 32바이트가 아니면 거부한다", 1, map[int]string{1: encode([]byte("too-short"))}, ErrInvalidKey},
		{"빈 문자열은 거부한다", 1, map[int]string{1: ""}, ErrInvalidKey},
		{"활성 버전의 키가 없으면 거부한다", 2, map[int]string{1: encode(filledKey(0x11))}, ErrKeyVersion},
		{"활성이 아닌 키가 틀려도 거부한다", 1, map[int]string{1: encode(filledKey(0x11)), 2: leak}, ErrInvalidKey},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			ring, err := NewKeyRingFromBase64(tt.active, tt.encoded)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, ring)
			for _, value := range tt.encoded {
				if len(value) >= 8 {
					assert.NotContains(t, err.Error(), value[:8], "넘겨받은 키 문자열을 오류에 옮겨 적으면 안 된다")
				}
			}
		})
	}
}

func TestKeyRing_NewUserKey(t *testing.T) {
	ring := newTestRing(t)

	t.Run("감싼 키와 버전과 바로 쓸 수 있는 Sealer를 돌려준다", func(t *testing.T) {
		userKey, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)
		assert.Len(t, userKey.Wrapped, wrappedKeySize)
		assert.Equal(t, formatV1, userKey.Wrapped[0])
		assert.Equal(t, 1, userKey.KEKVersion)

		sealed, err := userKey.Sealer.SealString(written, diaryAAD())
		require.NoError(t, err)

		unwrapped, err := ring.Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
		opened, err := unwrapped.OpenString(sealed, diaryAAD())
		require.NoError(t, err, "저장해 둔 키를 풀면 같은 키가 나와야 한다")
		assert.Equal(t, written, opened)
	})

	t.Run("부를 때마다 다른 키가 나온다", func(t *testing.T) {
		first, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)
		second, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)
		assert.NotEqual(t, first.Wrapped, second.Wrapped)

		sealed, err := first.Sealer.SealString(written, diaryAAD())
		require.NoError(t, err)
		_, err = second.Sealer.Open(sealed, diaryAAD())
		require.ErrorIs(t, err, ErrDecrypt)
	})

	t.Run("사용자 ID가 비어 있으면 만들지 않는다", func(t *testing.T) {
		userKey, err := ring.NewUserKey(uuid.Nil)
		require.ErrorIs(t, err, ErrInvalidAAD)
		assert.Nil(t, userKey)
	})

	t.Run("난수원이 고장 나면 만들지 않는다", func(t *testing.T) {
		broken := newTestRing(t)
		broken.random = brokenReader{}
		userKey, err := broken.NewUserKey(testUserID)
		require.ErrorIs(t, err, errBrokenRandom)
		assert.Nil(t, userKey)
	})

	t.Run("난수원이 0만 내놓으면 그 키를 쓰지 않는다", func(t *testing.T) {
		stuck := newTestRing(t)
		stuck.random = bytes.NewReader(make([]byte, 4*keySize))
		userKey, err := stuck.NewUserKey(testUserID)
		require.ErrorIs(t, err, ErrInvalidKey)
		assert.Nil(t, userKey)
	})
}

func TestKeyRing_Unwrap_Failures(t *testing.T) {
	ring, err := NewKeyRing(2, map[int][]byte{1: filledKey(0x11), 2: filledKey(0x22)})
	require.NoError(t, err)
	userKey, err := ring.NewUserKey(testUserID)
	require.NoError(t, err)
	require.Equal(t, 2, userKey.KEKVersion)

	unknownFormat := bytes.Clone(userKey.Wrapped)
	unknownFormat[0] = 2

	tests := []struct {
		name       string
		userID     uuid.UUID
		wrapped    []byte
		kekVersion int
		wantErr    error
	}{
		{"다른 사용자의 키 행은 풀리지 않는다", otherUserID, userKey.Wrapped, 2, ErrDecrypt},
		{"들고 있지만 감쌀 때와 다른 버전이면 풀리지 않는다", testUserID, userKey.Wrapped, 1, ErrDecrypt},
		{"들고 있지 않은 버전이면 버전 오류다", testUserID, userKey.Wrapped, 3, ErrKeyVersion},
		{"버전 0은 버전 오류다", testUserID, userKey.Wrapped, 0, ErrKeyVersion},
		{"음수 버전은 버전 오류다", testUserID, userKey.Wrapped, -2, ErrKeyVersion},
		{"사용자 ID가 비어 있으면 거부한다", uuid.Nil, userKey.Wrapped, 2, ErrInvalidAAD},
		{"빈 값은 꼴이 틀린 것이다", testUserID, []byte{}, 2, ErrFormat},
		{"nil은 꼴이 틀린 것이다", testUserID, nil, 2, ErrFormat},
		{"한 바이트 모자라면 꼴이 틀린 것이다", testUserID, userKey.Wrapped[:wrappedKeySize-1], 2, ErrFormat},
		{"한 바이트 남으면 꼴이 틀린 것이다", testUserID, append(bytes.Clone(userKey.Wrapped), 0x00), 2, ErrFormat},
		{"모르는 판 번호는 꼴이 틀린 것이다", testUserID, unknownFormat, 2, ErrFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealer, err := ring.Unwrap(tt.userID, tt.wrapped, tt.kekVersion)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, sealer)
		})
	}

	t.Run("어느 바이트를 바꿔도 풀리지 않는다", func(t *testing.T) {
		for index := range userKey.Wrapped {
			sealer, err := ring.Unwrap(testUserID, flipped(userKey.Wrapped, index, 0x01), 2)
			require.Nil(t, sealer, "index=%d", index)
			if index == 0 {
				require.ErrorIs(t, err, ErrFormat)
			} else {
				require.ErrorIs(t, err, ErrDecrypt, "index=%d", index)
			}
		}
	})

	t.Run("같은 마스터 키라도 버전 번호가 다르면 풀리지 않는다", func(t *testing.T) {
		renumbered, err := NewKeyRing(5, map[int][]byte{5: filledKey(0x22)})
		require.NoError(t, err)
		_, err = renumbered.Unwrap(testUserID, userKey.Wrapped, 5)
		require.ErrorIs(t, err, ErrDecrypt)
	})

	t.Run("다른 마스터 키로는 풀리지 않는다", func(t *testing.T) {
		stranger, err := NewKeyRing(2, map[int][]byte{2: filledKey(0x99)})
		require.NoError(t, err)
		_, err = stranger.Unwrap(testUserID, userKey.Wrapped, 2)
		require.ErrorIs(t, err, ErrDecrypt)
	})
}

func TestKeyRing_Rotation(t *testing.T) {
	oldRing, err := NewKeyRing(1, map[int][]byte{1: filledKey(0x11)})
	require.NoError(t, err)
	userKey, err := oldRing.NewUserKey(testUserID)
	require.NoError(t, err)
	sealed, err := userKey.Sealer.SealString(written, diaryAAD())
	require.NoError(t, err)

	bothRing, err := NewKeyRing(2, map[int][]byte{1: filledKey(0x11), 2: filledKey(0x22)})
	require.NoError(t, err)
	newRing, err := NewKeyRing(2, map[int][]byte{2: filledKey(0x22)})
	require.NoError(t, err)

	t.Run("새 키를 더한 묶음은 옛 버전으로 감싼 키도 푼다", func(t *testing.T) {
		assert.True(t, bothRing.NeedsRewrap(userKey.KEKVersion))
		sealer, err := bothRing.Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, diaryAAD())
		require.NoError(t, err)
		assert.Equal(t, written, opened)
	})

	t.Run("다시 감싸면 옛 마스터 키 없이도 예전 글이 열린다", func(t *testing.T) {
		rewrapped, version, err := bothRing.Rewrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.NoError(t, err)
		assert.Equal(t, 2, version)
		assert.False(t, bothRing.NeedsRewrap(version))
		assert.NotEqual(t, userKey.Wrapped, rewrapped)

		sealer, err := newRing.Unwrap(testUserID, rewrapped, version)
		require.NoError(t, err)
		opened, err := sealer.OpenString(sealed, diaryAAD())
		require.NoError(t, err, "데이터 키는 그대로이므로 글을 다시 잠그지 않아도 된다")
		assert.Equal(t, written, opened)
	})

	t.Run("다시 감싸기 전에 옛 마스터 키를 빼면 버전 오류가 난다", func(t *testing.T) {
		_, err := newRing.Unwrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.ErrorIs(t, err, ErrKeyVersion)
		_, _, err = newRing.Rewrap(testUserID, userKey.Wrapped, userKey.KEKVersion)
		require.ErrorIs(t, err, ErrKeyVersion)
	})

	t.Run("남의 키 행은 다시 감싸지지 않는다", func(t *testing.T) {
		rewrapped, version, err := bothRing.Rewrap(otherUserID, userKey.Wrapped, userKey.KEKVersion)
		require.ErrorIs(t, err, ErrDecrypt)
		assert.Nil(t, rewrapped)
		assert.Zero(t, version)
	})
}

func TestKeyRing_Output(t *testing.T) {
	key := filledKey(0x11)
	ring, err := NewKeyRing(1, map[int][]byte{1: key, 4: filledKey(0x44)})
	require.NoError(t, err)

	t.Run("출력하면 고정 문자열만 나온다", func(t *testing.T) {
		assert.Equal(t, "[REDACTED] [REDACTED] [REDACTED]", fmt.Sprintf("%v %+v %s", ring, ring, ring))
	})

	t.Run("로그에는 버전 번호만 남는다", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("key ring ready", "key_ring", ring)
		assert.Contains(t, buf.String(), `"key_ring":{"active":1,"versions":[1,4]}`)
		assert.NotContains(t, buf.String(), base64.StdEncoding.EncodeToString(key)[:8])
		assert.NotContains(t, buf.String(), hex.EncodeToString(key[:4]))
	})
}
