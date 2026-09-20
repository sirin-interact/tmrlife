package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
)

const (
	// formatV1은 "판 번호 | nonce | GCM 출력" 꼴이다.
	// 꼴을 바꿀 일이 생기면 새 번호를 더하고, 옛 번호는 읽기 전용으로 남긴다.
	formatV1 byte = 1

	keySize    = 32
	nonceSize  = 12
	tagSize    = 16
	headerSize = 1 + nonceSize

	// minSealedSize는 빈 평문을 잠갔을 때의 길이다. 이보다 짧으면 잘린 것이다.
	minSealedSize = headerSize + tagSize
)

const redactedMarker = "[REDACTED]"

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("%w: key must be %d bytes, got %d", ErrInvalidKey, keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}
	return aead, nil
}

// sealEnvelope는 nonce를 새로 뽑아 저장 꼴로 잠근다.
// nonce를 다 채우지 못하면 아무것도 돌려주지 않는다. 덜 채워진 nonce로 잠그면 같은 nonce가 되풀이될 수 있다.
func sealEnvelope(aead cipher.AEAD, random io.Reader, plaintext, aad []byte) ([]byte, error) {
	out := make([]byte, headerSize, headerSize+len(plaintext)+tagSize)
	out[0] = formatV1
	nonce := out[1:headerSize]
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("crypto: read random nonce: %w", err)
	}
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// splitEnvelope는 판 번호를 확인하고 nonce와 GCM 출력을 갈라 준다.
// 판 번호를 길이보다 먼저 본다. 뒤에 생길 판은 길이 규칙이 다를 수 있다.
func splitEnvelope(sealed []byte) (nonce, body []byte, err error) {
	if len(sealed) == 0 {
		return nil, nil, fmt.Errorf("%w: empty", ErrFormat)
	}
	if sealed[0] != formatV1 {
		return nil, nil, fmt.Errorf("%w: unknown format version %d", ErrFormat, sealed[0])
	}
	if len(sealed) < minSealedSize {
		return nil, nil, fmt.Errorf("%w: %d bytes is shorter than the minimum %d", ErrFormat, len(sealed), minSealedSize)
	}
	return sealed[1:headerSize], sealed[headerSize:], nil
}

func zero(b []byte) {
	clear(b)
}

func isAllZero(b []byte) bool {
	var acc byte
	for _, v := range b {
		acc |= v
	}
	return acc == 0
}
