package crypto

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 아래 값은 이 패키지가 아니라 다른 언어의 AES-256-GCM 구현으로 만들었다.
// 저장 꼴이나 AAD를 만드는 방식이 조금이라도 바뀌면 이미 저장된 글이 모두 열리지 않게 되는데,
// 잠그고 바로 여는 시험은 양쪽이 함께 바뀌므로 그것을 잡지 못한다. 이 시험이 잡는다.
// 이 시험이 깨졌다면 값을 새로 뽑지 말고 코드를 되돌린다. 꼴을 바꿔야 한다면 새 판 번호를 더한다.
const (
	goldenKEKHex      = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	goldenKEKVersion  = 3
	goldenWrappedHex  = "01000102030405060708090a0bd191a3346c78c7aca78218eac835592299260f26ca2acccb75f1c706e0688c840ff65b1aab9e35b44c6072bee270c747"
	goldenSealedHex   = "01f0f1f2f3f4f5f6f7f8f9fafb0036965e61fc1ebdcc16b2e554c96a43386850db9d6a7e624650ba292543f6b41df696d01126d61c9b7d"
	goldenEmptyHex    = "01f0f1f2f3f4f5f6f7f8f9fafb7faad16d6b3c5f7c6d978619a2aac70a"
	goldenPlaintext   = "오늘은 조금 걸었다"
	goldenTable       = "diary_entries"
	goldenColumn      = "body_enc"
	goldenDataKeyFill = 0xa0
)

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(s)
	require.NoError(t, err)
	return decoded
}

func hexReader(t testing.TB, s string) io.Reader {
	t.Helper()
	return bytes.NewReader(mustHex(t, s))
}

func goldenSealer(t testing.TB) *Sealer {
	t.Helper()
	ring, err := NewKeyRing(goldenKEKVersion, map[int][]byte{goldenKEKVersion: mustHex(t, goldenKEKHex)})
	require.NoError(t, err)
	sealer, err := ring.Unwrap(testUserID, mustHex(t, goldenWrappedHex), goldenKEKVersion)
	require.NoError(t, err, "감싼 데이터 키의 꼴이나 AAD가 바뀌었다")
	return sealer
}

func TestGolden_StoredFormatIsStable(t *testing.T) {
	aad := AAD{Table: goldenTable, Column: goldenColumn, RowID: testRowID}

	t.Run("다른 구현이 감싼 데이터 키를 풀고 그 키로 잠근 글을 연다", func(t *testing.T) {
		opened, err := goldenSealer(t).OpenString(mustHex(t, goldenSealedHex), aad)
		require.NoError(t, err, "컬럼 암호문의 꼴이나 AAD가 바뀌었다")
		assert.Equal(t, goldenPlaintext, opened)
	})

	t.Run("다른 구현이 잠근 빈 글을 연다", func(t *testing.T) {
		opened, err := goldenSealer(t).Open(mustHex(t, goldenEmptyHex), aad)
		require.NoError(t, err)
		assert.Empty(t, opened)
		assert.NotNil(t, opened)
	})

	t.Run("풀린 데이터 키가 기대한 그 키다", func(t *testing.T) {
		// 데이터 키는 0xa0부터 1씩 오르는 32바이트다. 같은 키로 직접 만든 Sealer와 서로 열려야 한다.
		dek := make([]byte, keySize)
		for i := range dek {
			dek[i] = goldenDataKeyFill + byte(i)
		}
		direct, err := newSealer(dek)
		require.NoError(t, err)

		sealed, err := direct.SealString(goldenPlaintext, aad)
		require.NoError(t, err)
		opened, err := goldenSealer(t).OpenString(sealed, aad)
		require.NoError(t, err)
		assert.Equal(t, goldenPlaintext, opened)
	})

	t.Run("같은 nonce를 주면 다른 구현과 바이트까지 같은 암호문이 나온다", func(t *testing.T) {
		sealer := goldenSealer(t)
		sealer.random = hexReader(t, "f0f1f2f3f4f5f6f7f8f9fafb")
		sealed, err := sealer.SealString(goldenPlaintext, aad)
		require.NoError(t, err)
		assert.Equal(t, goldenSealedHex, hex.EncodeToString(sealed))
	})

	t.Run("같은 nonce를 주면 다른 구현과 바이트까지 같게 감싼다", func(t *testing.T) {
		ring, err := NewKeyRing(goldenKEKVersion, map[int][]byte{goldenKEKVersion: mustHex(t, goldenKEKHex)})
		require.NoError(t, err)
		dek := make([]byte, keySize)
		for i := range dek {
			dek[i] = goldenDataKeyFill + byte(i)
		}
		// 난수원은 데이터 키 32바이트를 먼저, 이어서 nonce 12바이트를 내준다.
		ring.random = hexReader(t, hex.EncodeToString(dek)+"000102030405060708090a0b")
		userKey, err := ring.NewUserKey(testUserID)
		require.NoError(t, err)
		assert.Equal(t, goldenWrappedHex, hex.EncodeToString(userKey.Wrapped))
		assert.Equal(t, goldenKEKVersion, userKey.KEKVersion)
	})
}
