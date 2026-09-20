package crypto

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// written은 시험에서 사용자의 글 자리에 넣는 문장이다.
const written = "오늘은 아무것도 하기 싫었어요"

var (
	testUserID  = uuid.MustParse("01927f3e-5b7a-7c11-8d22-0123456789ab")
	otherUserID = uuid.MustParse("01927f3e-5b7a-7c11-8d22-ffffffffffff")
	testRowID   = uuid.MustParse("01927f3e-6c8b-7d33-9e44-ba9876543210")
	otherRowID  = uuid.MustParse("01927f3e-6c8b-7d33-9e44-000000000001")
)

var errBrokenRandom = errors.New("broken random source")

func filledKey(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, keySize)
}

func newTestRing(t *testing.T) *KeyRing {
	t.Helper()
	ring, err := NewKeyRing(1, map[int][]byte{1: filledKey(0x11)})
	require.NoError(t, err)
	return ring
}

func newTestSealer(t *testing.T, ring *KeyRing, userID uuid.UUID) *Sealer {
	t.Helper()
	key, err := ring.NewUserKey(userID)
	require.NoError(t, err)
	return key.Sealer
}

func diaryAAD() AAD {
	return AAD{Table: "diary_entries", Column: "body_enc", RowID: testRowID}
}

// brokenReader는 난수원이 고장 난 상황을 만든다.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) {
	return 0, errBrokenRandom
}

// flipped는 한 바이트의 비트를 뒤집은 복사본을 돌려준다.
func flipped(src []byte, index int, mask byte) []byte {
	out := bytes.Clone(src)
	out[index] ^= mask
	return out
}
