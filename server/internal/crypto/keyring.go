package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// wrapPurpose는 감싼 데이터 키의 AAD 앞에 붙는다.
const wrapPurpose = "naeil:user-dek"

// maxKEKVersion은 버전을 저장하는 DB의 smallint 컬럼에 들어가는 가장 큰 값이다.
// 저장할 수 없는 버전은 첫 가입자의 INSERT가 아니라 프로세스가 뜰 때 걸러져야 한다.
const maxKEKVersion = math.MaxInt16

const wrappedKeySize = headerSize + keySize + tagSize

// KeyRing은 버전별 마스터 키 묶음이다. 만든 뒤에는 바뀌지 않으므로 여러 고루틴이 함께 써도 된다.
//
// 마스터 키를 바꾸는 순서는 이렇다. 새 버전을 더해 띄우고, 활성 버전을 새 버전으로 올리고,
// NeedsRewrap이 참인 키 행을 Rewrap으로 모두 옮긴 뒤에야 옛 버전을 설정에서 뺀다.
// 옮기기 전에 빼면 그 사용자들의 글은 ErrKeyVersion으로 열리지 않는다.
type KeyRing struct {
	active int
	keks   map[int]cipher.AEAD
	random io.Reader
}

// UserKey는 새로 만든 데이터 키다. Wrapped와 KEKVersion을 저장하고, Sealer는 바로 쓸 수 있다.
type UserKey struct {
	Sealer     *Sealer
	Wrapped    []byte
	KEKVersion int
}

// NewKeyRing은 버전별 32바이트 마스터 키로 묶음을 만든다.
// keys의 바이트는 들고 있지 않고 지우지도 않는다. 넘긴 쪽이 알아서 지운다.
func NewKeyRing(active int, keys map[int][]byte) (*KeyRing, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no master keys", ErrInvalidKey)
	}

	// 문제가 여럿일 때 어느 것이 보고될지 실행마다 달라지지 않게 버전 순서로 본다.
	versions := slices.Sorted(maps.Keys(keys))
	keks := make(map[int]cipher.AEAD, len(keys))
	for i, version := range versions {
		if _, ok := kekVersionNumber(version); !ok {
			return nil, fmt.Errorf("%w: master key version %d is out of range 1..%d", ErrKeyVersion, version, maxKEKVersion)
		}
		key := keys[version]
		if len(key) != keySize {
			return nil, fmt.Errorf("%w: master key version %d must be %d bytes, got %d", ErrInvalidKey, version, keySize, len(key))
		}
		if isAllZero(key) {
			return nil, fmt.Errorf("%w: master key version %d is all zero", ErrInvalidKey, version)
		}
		// 옛 키를 새 버전 자리에 붙여 넣고 활성 버전만 올리면, 키를 바꿨다고 믿지만 실제로는 그대로다.
		for _, earlier := range versions[:i] {
			if subtle.ConstantTimeCompare(key, keys[earlier]) == 1 {
				return nil, fmt.Errorf("%w: master key versions %d and %d hold the same key", ErrInvalidKey, earlier, version)
			}
		}
		aead, err := newAEAD(key)
		if err != nil {
			return nil, err
		}
		keks[version] = aead
	}

	if _, ok := keks[active]; !ok {
		return nil, fmt.Errorf("%w: active master key version %d is not loaded", ErrKeyVersion, active)
	}
	return &KeyRing{active: active, keks: keks, random: rand.Reader}, nil
}

// NewKeyRingFromBase64는 표준 base64로 적힌 마스터 키로 묶음을 만든다.
func NewKeyRingFromBase64(active int, encoded map[int]string) (*KeyRing, error) {
	keys := make(map[int][]byte, len(encoded))
	defer func() {
		for _, key := range keys {
			zero(key)
		}
	}()
	for _, version := range slices.Sorted(maps.Keys(encoded)) {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded[version]))
		if err != nil {
			// 틀린 자리 앞까지는 이미 풀려서 돌아온다.
			zero(key)
			// base64 오류는 붙이지 않는다. 키 문자열의 어느 자리가 틀렸는지까지 알릴 이유가 없다.
			return nil, fmt.Errorf("%w: master key version %d is not standard base64", ErrInvalidKey, version)
		}
		keys[version] = key
	}
	return NewKeyRing(active, keys)
}

// ActiveVersion은 새 데이터 키를 감쌀 때 쓰는 버전이다.
func (r *KeyRing) ActiveVersion() int {
	return r.active
}

// Versions는 들고 있는 버전을 오름차순으로 돌려준다.
func (r *KeyRing) Versions() []int {
	return slices.Sorted(maps.Keys(r.keks))
}

// NeedsRewrap은 그 버전으로 감싼 데이터 키를 활성 버전으로 옮겨야 하는지 알려준다.
func (r *KeyRing) NeedsRewrap(kekVersion int) bool {
	return kekVersion != r.active
}

// NewUserKey는 사용자의 데이터 키를 새로 뽑아 활성 마스터 키로 감싼다.
func (r *KeyRing) NewUserKey(userID uuid.UUID) (*UserKey, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: user id is empty", ErrInvalidAAD)
	}
	dek := make([]byte, keySize)
	defer zero(dek)
	if _, err := io.ReadFull(r.random, dek); err != nil {
		return nil, fmt.Errorf("crypto: read random data key: %w", err)
	}
	sealer, err := newSealer(dek)
	if err != nil {
		return nil, err
	}
	wrapped, err := r.wrap(userID, dek)
	if err != nil {
		return nil, err
	}
	return &UserKey{Sealer: sealer, Wrapped: wrapped, KEKVersion: r.active}, nil
}

// Unwrap은 저장해 둔 데이터 키를 풀어 Sealer로 돌려준다.
// 다른 사용자의 키 행이거나 kekVersion이 감쌀 때의 버전과 다르면 ErrDecrypt가 난다.
func (r *KeyRing) Unwrap(userID uuid.UUID, wrapped []byte, kekVersion int) (*Sealer, error) {
	dek, err := r.unwrap(userID, wrapped, kekVersion)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return newSealer(dek)
}

// Rewrap은 옛 마스터 키로 감싼 데이터 키를 활성 마스터 키로 다시 감싼다.
// 데이터 키 자체는 그대로라서 이미 잠근 글은 손대지 않아도 된다.
func (r *KeyRing) Rewrap(userID uuid.UUID, wrapped []byte, kekVersion int) (rewrapped []byte, activeVersion int, err error) {
	dek, err := r.unwrap(userID, wrapped, kekVersion)
	if err != nil {
		return nil, 0, err
	}
	defer zero(dek)
	rewrapped, err = r.wrap(userID, dek)
	if err != nil {
		return nil, 0, err
	}
	return rewrapped, r.active, nil
}

func (r *KeyRing) wrap(userID uuid.UUID, dek []byte) ([]byte, error) {
	version, ok := kekVersionNumber(r.active)
	if !ok {
		return nil, fmt.Errorf("%w: active master key version %d is out of range", ErrKeyVersion, r.active)
	}
	return sealEnvelope(r.keks[r.active], r.random, dek, wrapAAD(formatV1, version, userID))
}

func (r *KeyRing) unwrap(userID uuid.UUID, wrapped []byte, kekVersion int) ([]byte, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: user id is empty", ErrInvalidAAD)
	}
	version, ok := kekVersionNumber(kekVersion)
	kek, loaded := r.keks[kekVersion]
	if !ok || !loaded {
		return nil, fmt.Errorf("%w: master key version %d is not loaded (user %s)", ErrKeyVersion, kekVersion, userID)
	}
	nonce, body, err := splitEnvelope(wrapped)
	if err != nil {
		return nil, fmt.Errorf("%w (wrapped data key of user %s)", err, userID)
	}
	if len(wrapped) != wrappedKeySize {
		return nil, fmt.Errorf("%w: wrapped data key must be %d bytes, got %d (user %s)", ErrFormat, wrappedKeySize, len(wrapped), userID)
	}
	dek, err := kek.Open(nil, nonce, body, wrapAAD(wrapped[0], version, userID))
	if err != nil {
		return nil, fmt.Errorf("%w (wrapped data key of user %s, master key version %d)", ErrDecrypt, userID, kekVersion)
	}
	return dek, nil
}

func kekVersionNumber(version int) (uint32, bool) {
	if version < 1 || version > maxKEKVersion {
		return 0, false
	}
	return uint32(version), true
}

// wrapAAD의 조각은 모두 길이가 고정이라 길이를 따로 붙이지 않아도 서로 겹치지 않는다.
func wrapAAD(format byte, kekVersion uint32, userID uuid.UUID) []byte {
	buf := make([]byte, 0, len(wrapPurpose)+1+4+len(userID))
	buf = append(buf, wrapPurpose...)
	buf = append(buf, format)
	buf = binary.BigEndian.AppendUint32(buf, kekVersion)
	return append(buf, userID[:]...)
}

// String은 묶음이 실수로 출력될 때 안쪽 구조가 드러나지 않게 한다.
func (r *KeyRing) String() string {
	return redactedMarker
}

// LogValue는 버전 번호만 남긴다. 어떤 키로 떠 있는지는 운영에 필요한 정보다.
func (r *KeyRing) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("active", r.active),
		slog.Any("versions", r.Versions()),
	)
}
