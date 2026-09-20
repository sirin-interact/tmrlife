package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 동의의 종류다. consents.kind의 CHECK 제약과 같은 값이어야 한다.
const (
	ConsentTerms   = "terms"
	ConsentPrivacy = "privacy"
	// ConsentSensitiveData는 마음 건강에 관한 기록을 다루는 데 대한 동의다.
	ConsentSensitiveData = "sensitive_data"
	// ConsentOverseasTransfer는 음성과 대화 내용이 외부 AI 서비스로 전송되어 해외에서 처리될 수 있다는 데 대한 동의다.
	ConsentOverseasTransfer = "overseas_transfer"
)

// NewUser는 계정 하나를 만드는 데 필요한 값이다.
type NewUser struct {
	ID    uuid.UUID
	Email string
	// PasswordHash가 nil이면 비밀번호 없는 계정이다(소셜 로그인).
	PasswordHash *string
	DisplayName  *string
	// Timezone은 IANA 시간대 이름이다. 비워 둘 수 없다.
	Timezone string
	IsDemo   bool
	// WrappedDEK는 이 사용자의 데이터 키를 마스터 키로 감싼 값이고, KEKVersion은 감싼 마스터 키의 번호다.
	WrappedDEK []byte
	KEKVersion int16
	Consents   []NewConsent
	Now        time.Time
}

// NewConsent는 가입과 함께 기록할 동의 하나다.
type NewConsent struct {
	ID      uuid.UUID
	Kind    string
	Version string
}

// CreateUser는 사용자, 데이터 키, 설정, 동의를 한 트랜잭션으로 만든다.
// 키가 없는 사용자는 아무것도 기록할 수 없고 동의가 빠진 사용자는 있어서는 안 되므로, 일부만 만들어진 상태를 남기지 않는다.
// 이미 가입된 이메일이면 ErrEmailTaken을 돌려준다.
func (s *Store) CreateUser(ctx context.Context, in NewUser) (db.User, error) {
	var user db.User
	err := s.InTx(ctx, func(q *db.Queries) error {
		var err error
		user, err = InsertUser(ctx, q, in)
		return err
	})
	if err != nil {
		return db.User{}, err
	}
	return user, nil
}

// InsertUser는 CreateUser와 같은 행들을, 부르는 쪽이 이미 연 트랜잭션 안에서 만든다.
// 가입처럼 계정과 첫 세션이 함께 생기거나 함께 없어야 할 때 쓴다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 트랜잭션 밖의 쿼리를 넘기면 도중에 실패했을 때 앞서 만든 행이 남는다.
// 오류가 나면 트랜잭션은 이미 깨져 있으므로, 부르는 쪽은 그 오류를 그대로 돌려줘 되돌리게 한다.
func InsertUser(ctx context.Context, q *db.Queries, in NewUser) (db.User, error) {
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		ID:           in.ID,
		Email:        in.Email,
		PasswordHash: in.PasswordHash,
		DisplayName:  in.DisplayName,
		Timezone:     in.Timezone,
		IsDemo:       in.IsDemo,
		Now:          in.Now,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) && ConstraintName(err) == constraintUsersEmail {
			return db.User{}, ErrEmailTaken
		}
		return db.User{}, fmt.Errorf("insert user: %w", err)
	}
	if err := q.CreateUserKey(ctx, db.CreateUserKeyParams{
		UserID:     in.ID,
		WrappedDEK: in.WrappedDEK,
		KEKVersion: in.KEKVersion,
		Now:        in.Now,
	}); err != nil {
		return db.User{}, fmt.Errorf("insert user key: %w", err)
	}
	if _, err := q.CreateUserSettings(ctx, db.CreateUserSettingsParams{UserID: in.ID, Now: in.Now}); err != nil {
		return db.User{}, fmt.Errorf("insert user settings: %w", err)
	}
	for _, c := range in.Consents {
		if _, err := q.GrantConsent(ctx, db.GrantConsentParams{
			ID:      c.ID,
			UserID:  in.ID,
			Kind:    c.Kind,
			Version: c.Version,
			Now:     in.Now,
		}); err != nil {
			return db.User{}, fmt.Errorf("insert consent: %w", err)
		}
	}
	return user, nil
}
