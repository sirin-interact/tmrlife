package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

const (
	saltLength = 16
	keyLength  = 32

	phcAlgorithm = "argon2id"
)

// 저장된 해시에서 읽은 값에 거는 한도다. 해시 문자열은 DB에서 오고, 확인할 때는 거기 적힌 값대로 계산한다.
// 깨졌거나 터무니없는 값(수 TiB의 메모리, 끝나지 않는 반복)이 적힌 해시를 계산하지 않으려는 것이다.
//
// 이 한도가 파드의 메모리까지 지켜 주지는 않는다. 한도는 고정된 값이고 파드에 허락된 메모리는 배포마다 다르다.
// 메모리 한도가 1 GiB보다 작은 파드에서는 한도 안의 값으로도 로그인 한 번에 파드가 죽을 수 있다.
// 그렇게 하려면 password_hash를 고쳐 쓸 수 있어야 하는데, 그럴 수 있는 쪽은 해시를 제 것으로 바꿔 그 계정으로 로그인할 수도 있다.
// 한도를 설정의 메모리 값에 맞춰 좁히지 않는 까닭도 있다. 운영자가 메모리 값을 낮추면 그 전에 만들어진 멀쩡한 해시가
// 한도 밖으로 밀려나고, 그 사용자는 비밀번호를 되찾을 길이 없는 동안 로그인하지 못한다.
const (
	maxArgon2MemoryKiB = 1 << 20 // 1 GiB
	maxArgon2Time      = 64
	// 계산이 최소로 필요로 하는 메모리는 병렬 수의 8배다. 그보다 작게 적으면 계산하는 쪽이 조용히 올려 쓴다.
	// 그러면 저장된 문자열에 적힌 값과 실제로 쓴 값이 달라지므로 처음부터 받지 않는다.
	minArgon2MemoryPerLane = 8
	minSaltLength          = 8
	maxSaltLength          = 64
	minKeyLength           = 16
	maxKeyLength           = 128
)

// Argon2Params는 argon2id의 비용을 정하는 값이다.
type Argon2Params struct {
	// MemoryKiB는 해시 하나를 계산하는 데 쓰는 메모리다.
	MemoryKiB uint32
	// Time은 그 메모리를 훑는 횟수다.
	Time uint32
	// Parallelism은 계산을 나눠 맡는 갈래의 수다.
	Parallelism uint8
}

// DefaultArgon2Params는 설정을 주지 않았을 때의 값이다(메모리 19 MiB, 2회, 한 갈래).
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{MemoryKiB: 19456, Time: 2, Parallelism: 1}
}

func (p Argon2Params) validate() error {
	switch {
	case p.Parallelism < 1:
		return errors.New("auth: argon2 parallelism must be at least 1")
	case p.Time < 1 || p.Time > maxArgon2Time:
		return fmt.Errorf("auth: argon2 time must be between 1 and %d", maxArgon2Time)
	case p.MemoryKiB < minArgon2MemoryPerLane*uint32(p.Parallelism):
		return fmt.Errorf("auth: argon2 memory must be at least %d KiB per lane", minArgon2MemoryPerLane)
	case p.MemoryKiB > maxArgon2MemoryKiB:
		return fmt.Errorf("auth: argon2 memory must not exceed %d KiB", maxArgon2MemoryKiB)
	}
	return nil
}

// deriveFunc는 argon2.IDKey의 모양이다. 동시에 도는 수의 한도를 시험할 때 바꿔 끼운다.
type deriveFunc func(password, salt []byte, time, memoryKiB uint32, threads uint8, keyLen uint32) []byte

// Hasher는 비밀번호를 해시하고 확인한다. 여러 고루틴이 함께 써도 된다.
type Hasher struct {
	params Argon2Params
	// slots는 동시에 계산할 수 있는 자리다. 해시 하나가 MemoryKiB만큼 메모리를 쓰므로,
	// 자리가 없으면 로그인이 몰렸을 때 메모리 사용량이 요청 수만큼 불어나 파드가 죽는다.
	slots  chan struct{}
	random io.Reader
	derive deriveFunc
	// dummy는 없는 계정으로 로그인하려 할 때 대신 계산하는 해시다.
	dummy string
	// computed는 실제로 해시를 계산한 횟수다.
	computed atomic.Uint64
}

// NewHasher는 해시를 만드는 값과 동시에 계산할 수 있는 수로 Hasher를 만든다.
// 가짜 해시를 미리 계산해 두므로, 해시 하나를 계산하는 만큼의 시간이 걸린다.
func NewHasher(ctx context.Context, params Argon2Params, maxConcurrent int) (*Hasher, error) {
	if err := params.validate(); err != nil {
		return nil, err
	}
	if maxConcurrent < 1 {
		return nil, errors.New("auth: hasher needs room for at least one concurrent computation")
	}
	h := &Hasher{
		params: params,
		slots:  make(chan struct{}, maxConcurrent),
		random: rand.Reader,
		derive: argon2.IDKey,
	}

	// 가짜 해시는 지금 설정으로 만든다. 새로 가입한 계정의 해시와 계산 비용이 같다.
	// 아무도 모르는 무작위 값의 해시라서 어떤 비밀번호와도 맞지 않는다.
	secret := make([]byte, keyLength)
	if _, err := io.ReadFull(h.random, secret); err != nil {
		return nil, fmt.Errorf("auth: read random bytes for the dummy hash: %w", err)
	}
	dummy, err := h.Hash(ctx, hex.EncodeToString(secret))
	clear(secret)
	if err != nil {
		return nil, fmt.Errorf("auth: compute the dummy hash: %w", err)
	}
	h.dummy = dummy
	return h, nil
}

// Params는 새 해시를 만들 때 쓰는 값이다.
func (h *Hasher) Params() Argon2Params {
	return h.params
}

// Hash는 비밀번호의 해시를 매개변수와 함께 한 문자열로 돌려준다.
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// 규칙에 맞는 비밀번호인지는 보지 않는다. 그 일은 PasswordPolicy가 한다.
// ctx가 끝나면 자리를 기다리는 것을 그만둔다. 이미 시작한 계산은 도중에 멈출 수 없다.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	normalized, reason := normalizePassword(password)
	if reason != "" {
		return "", &PasswordPolicyError{Reasons: []PasswordReason{reason}}
	}
	salt := make([]byte, saltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("auth: read random salt: %w", err)
	}
	key, err := h.compute(ctx, normalized, salt, h.params, keyLength)
	if err != nil {
		return "", err
	}
	return encodeHash(h.params, salt, key), nil
}

// Verify는 비밀번호가 저장된 해시와 맞는지 본다. 해시에 적힌 값대로 계산하므로 설정이 바뀐 뒤에도 옛 해시를 확인할 수 있다.
// 해시를 읽을 수 없으면 ErrMalformedHash다.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (bool, error) {
	stored, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	normalized, reason := normalizePassword(password)
	if reason != "" {
		// 받을 수 없는 입력은 어떤 해시와도 맞지 않는다. 그래도 계산은 똑같이 한다.
		// 건너뛰면 이런 입력만 빨리 거절되어, 걸린 시간으로 입력을 가려낼 수 있게 된다.
		normalized = ""
	}
	keyLen := uint32(len(stored.key)) // #nosec G115 -- decodeHash가 길이의 상한을 확인했다.
	key, err := h.compute(ctx, normalized, stored.salt, stored.params, keyLen)
	if err != nil {
		return false, err
	}
	match := subtle.ConstantTimeCompare(key, stored.key) == 1
	return match && reason == "", nil
}

// VerifyDummy는 없는 계정이나 비밀번호가 없는 계정으로 로그인하려 할 때 부른다.
// 결과는 언제나 불일치다. 진짜 해시를 확인할 때와 같은 계산을 해서, 걸린 시간으로 계정이 있는지 알 수 없게 한다.
//
// 가짜 해시의 비용은 지금 설정을 따른다. 설정을 바꾼 직후에는 아직 옛 설정의 해시를 가진 계정과 걸리는 시간이 다를 수 있다.
// 그 차이는 그 계정이 한 번 로그인해 해시가 다시 만들어지면 없어진다.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

// NeedsRehash는 저장된 해시가 지금 설정과 다른 값으로 만들어졌는지 알려준다.
// 참이면 로그인에 성공한 김에 새 해시로 바꿔 넣는다. 비밀번호를 아는 순간은 그때뿐이다.
func (h *Hasher) NeedsRehash(encoded string) bool {
	stored, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return stored.params != h.params || len(stored.salt) != saltLength || len(stored.key) != keyLength
}

func (h *Hasher) compute(ctx context.Context, password string, salt []byte, params Argon2Params, keyLen uint32) ([]byte, error) {
	// 이미 끝난 요청이면 자리가 비어 있어도 계산을 시작하지 않는다. 받을 사람이 없는 계산에 메모리를 쓰지 않는다.
	// select는 둘 다 준비되어 있으면 아무 쪽이나 고르므로 먼저 따로 본다.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("auth: hashing abandoned: %w", err)
	}
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: wait for a free hashing slot: %w", ctx.Err())
	}
	defer func() { <-h.slots }()

	h.computed.Add(1)
	input := []byte(password)
	defer clear(input)
	return h.derive(input, salt, params.Time, params.MemoryKiB, params.Parallelism, keyLen), nil
}

// String은 Hasher가 실수로 출력될 때 가짜 해시가 드러나지 않게 한다.
func (h *Hasher) String() string {
	return "auth.Hasher"
}

// LogValue는 해시를 만드는 값만 남긴다.
func (h *Hasher) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Uint64("memory_kib", uint64(h.params.MemoryKiB)),
		slog.Uint64("time", uint64(h.params.Time)),
		slog.Uint64("parallelism", uint64(h.params.Parallelism)),
		slog.Int("max_concurrent", cap(h.slots)),
	)
}

type storedHash struct {
	params Argon2Params
	salt   []byte
	key    []byte
}

func encodeHash(params Argon2Params, salt, key []byte) string {
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		phcAlgorithm, argon2.Version, params.MemoryKiB, params.Time, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// decodeHash의 오류에는 해시의 어떤 부분도 담지 않는다. 어디가 틀렸는지만 말한다.
func decodeHash(encoded string) (storedHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return storedHash{}, fmt.Errorf("%w: expected five '$'-separated fields", ErrMalformedHash)
	}
	if parts[1] != phcAlgorithm {
		return storedHash{}, fmt.Errorf("%w: unknown algorithm", ErrMalformedHash)
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return storedHash{}, fmt.Errorf("%w: unknown argon2 version", ErrMalformedHash)
	}

	params, err := decodeParams(parts[3])
	if err != nil {
		return storedHash{}, err
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < minSaltLength || len(salt) > maxSaltLength {
		return storedHash{}, fmt.Errorf("%w: bad salt", ErrMalformedHash)
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < minKeyLength || len(key) > maxKeyLength {
		return storedHash{}, fmt.Errorf("%w: bad hash", ErrMalformedHash)
	}
	return storedHash{params: params, salt: salt, key: key}, nil
}

func decodeParams(field string) (Argon2Params, error) {
	fields := strings.Split(field, ",")
	if len(fields) != 3 {
		return Argon2Params{}, fmt.Errorf("%w: expected m, t and p", ErrMalformedHash)
	}
	var values [3]uint64
	for i, name := range [3]string{"m", "t", "p"} {
		value, ok := strings.CutPrefix(fields[i], name+"=")
		if !ok {
			return Argon2Params{}, fmt.Errorf("%w: expected m, t and p in order", ErrMalformedHash)
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return Argon2Params{}, fmt.Errorf("%w: parameter %s is not a number", ErrMalformedHash, name)
		}
		values[i] = n
	}
	if values[2] < 1 || values[2] > 255 {
		return Argon2Params{}, fmt.Errorf("%w: parameter p is out of range", ErrMalformedHash)
	}
	params := Argon2Params{
		MemoryKiB:   uint32(values[0]), // #nosec G115 -- 위에서 32비트로 읽었다.
		Time:        uint32(values[1]), // #nosec G115 -- 위에서 32비트로 읽었다.
		Parallelism: uint8(values[2]),  // #nosec G115 -- 바로 위에서 범위를 확인했다.
	}
	if err := params.validate(); err != nil {
		return Argon2Params{}, fmt.Errorf("%w: parameters are out of range", ErrMalformedHash)
	}
	return params, nil
}
