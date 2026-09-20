package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
)

// fieldPurpose는 컬럼 암호문의 AAD 앞에 붙는다.
// 데이터 키를 감쌀 때의 AAD와 같은 바이트열이 나올 수 없게 쓰임새를 갈라 둔다.
const fieldPurpose = "naeil:field"

// maxIdentifierLen은 PostgreSQL 식별자의 최대 길이다.
const maxIdentifierLen = 63

// AAD는 암호문이 놓이는 자리다. 잠글 때와 열 때 같은 값을 줘야 열린다.
//
// Table과 Column은 DB의 실제 이름을 따르되, 한번 쓰기 시작하면 바꾸지 않는 꼬리표로 다룬다.
// 마이그레이션으로 테이블이나 컬럼 이름을 바꾸더라도 여기 넣는 값은 그대로 둬야 한다.
// 함께 바꾸면 그전에 잠근 글이 모두 열리지 않는다.
type AAD struct {
	Table  string
	Column string
	RowID  uuid.UUID
}

// validate는 소문자 snake_case 식별자만 받는다.
// 인자 순서를 헷갈려 사용자의 글이 이 자리에 들어오면 여기서 걸러진다.
// 그래서 통과한 값은 오류 메시지에 넣어도 된다.
func (a AAD) validate() error {
	if !isIdentifier(a.Table) {
		return fmt.Errorf("%w: table must be a lowercase identifier of 1 to %d bytes (got %d bytes)", ErrInvalidAAD, maxIdentifierLen, len(a.Table))
	}
	if !isIdentifier(a.Column) {
		return fmt.Errorf("%w: column must be a lowercase identifier of 1 to %d bytes (got %d bytes)", ErrInvalidAAD, maxIdentifierLen, len(a.Column))
	}
	if a.RowID == uuid.Nil {
		return fmt.Errorf("%w: row id is empty; generate the id before sealing", ErrInvalidAAD)
	}
	return nil
}

func isIdentifier(s string) bool {
	if len(s) == 0 || len(s) > maxIdentifierLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		letter := (c >= 'a' && c <= 'z') || c == '_'
		digit := c >= '0' && c <= '9'
		if !letter && (!digit || i == 0) {
			return false
		}
	}
	return true
}

// encode는 서로 다른 자리가 같은 바이트열이 되지 않게 이름마다 길이를 앞에 붙인다.
// 길이가 없으면 ("ab", "c")와 ("a", "bc")를 구분할 수 없다.
func (a AAD) encode(format byte) []byte {
	buf := make([]byte, 0, len(fieldPurpose)+3+len(a.Table)+len(a.Column)+len(a.RowID))
	buf = append(buf, fieldPurpose...)
	buf = append(buf, format)
	buf = appendIdentifier(buf, a.Table)
	buf = appendIdentifier(buf, a.Column)
	return append(buf, a.RowID[:]...)
}

// appendIdentifier는 validate를 통과한 이름만 받는다. 그래서 길이가 1바이트에 들어간다.
func appendIdentifier(buf []byte, name string) []byte {
	buf = append(buf, byte(len(name)&0xff))
	return append(buf, name...)
}

// Sealer는 사용자 한 명의 데이터 키로 글을 잠그고 연다. 여러 고루틴이 함께 써도 된다.
//
// 원본 키 바이트는 들고 있지 않는다. KeyRing.NewUserKey나 KeyRing.Unwrap으로만 만들 수 있다.
type Sealer struct {
	aead   cipher.AEAD
	random io.Reader
}

func newSealer(dek []byte) (*Sealer, error) {
	if isAllZero(dek) {
		// 지워진 버퍼로 Sealer를 만들면 누구나 아는 키로 글을 잠그게 된다.
		return nil, fmt.Errorf("%w: data key is all zero", ErrInvalidKey)
	}
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead, random: rand.Reader}, nil
}

// Seal은 평문을 잠근다. 빈 평문도 잠글 수 있고, 열면 빈 값이 그대로 나온다.
func (s *Sealer) Seal(plaintext []byte, aad AAD) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, fmt.Errorf("%w: sealer has no key", ErrInvalidKey)
	}
	if err := aad.validate(); err != nil {
		return nil, err
	}
	return sealEnvelope(s.aead, s.random, plaintext, aad.encode(formatV1))
}

// Open은 Seal이 만든 암호문을 연다. 성공하면 nil이 아닌 슬라이스를 돌려준다.
func (s *Sealer) Open(sealed []byte, aad AAD) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, fmt.Errorf("%w: sealer has no key", ErrInvalidKey)
	}
	if err := aad.validate(); err != nil {
		return nil, err
	}
	nonce, body, err := splitEnvelope(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w (%s.%s row %s)", err, aad.Table, aad.Column, aad.RowID)
	}
	plaintext, err := s.aead.Open(nil, nonce, body, aad.encode(sealed[0]))
	if err != nil {
		// 표준 라이브러리의 오류는 붙이지 않는다. 담긴 정보가 없고, 바깥에는 ErrDecrypt 하나만 보이게 한다.
		return nil, fmt.Errorf("%w (%s.%s row %s)", ErrDecrypt, aad.Table, aad.Column, aad.RowID)
	}
	if plaintext == nil {
		plaintext = []byte{}
	}
	return plaintext, nil
}

func (s *Sealer) SealString(plaintext string, aad AAD) ([]byte, error) {
	return s.Seal([]byte(plaintext), aad)
}

func (s *Sealer) OpenString(sealed []byte, aad AAD) (string, error) {
	plaintext, err := s.Open(sealed, aad)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// String은 Sealer가 실수로 출력될 때 안쪽 구조가 드러나지 않게 한다.
func (s *Sealer) String() string {
	return redactedMarker
}

// LogValue는 slog가 구조체 안쪽을 들여다보지 못하게 한다.
func (s *Sealer) LogValue() slog.Value {
	return slog.StringValue(redactedMarker)
}
