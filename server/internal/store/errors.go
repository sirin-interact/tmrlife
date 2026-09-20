package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrNotFound는 찾는 행이 없다는 뜻이다.
	// 남의 행을 ID로 찾거나 지우려 한 경우도 같은 오류가 된다. 있는지 없는지를 구분해 알려주지 않는다.
	ErrNotFound = errors.New("store: not found")

	// ErrConflict는 유일 제약에 걸렸다는 뜻이다. 어느 제약인지는 ConstraintName으로 본다.
	ErrConflict = errors.New("store: conflict")

	// ErrEmailTaken은 이미 가입된 이메일이라는 뜻이다. 이메일은 대소문자를 가리지 않고 비교한다.
	ErrEmailTaken = fmt.Errorf("%w: email already registered", ErrConflict)

	// ErrActiveConversationExists는 그 사용자에게 열린 대화가 이미 있다는 뜻이다. 열린 대화는 사용자마다 하나뿐이다.
	// 두 기기에서 동시에 시작했을 때 늦은 쪽이 받는다. 받은 쪽은 열린 대화를 다시 읽어 그 대화를 이어간다.
	ErrActiveConversationExists = fmt.Errorf("%w: user already has an active conversation", ErrConflict)

	// ErrConversationNotActive는 이미 끝난 대화에 발화를 더하려 했다는 뜻이다.
	// 끝난 뒤에 들어온 발화를 받아 주면 일기 초안 작업이 보지 못한 말이 기록에 남는다.
	ErrConversationNotActive = errors.New("store: conversation is not active")

	// ErrDiaryChanged는 초안을 만드는 동안 그날의 일기가 바뀌었다는 뜻이다.
	// 그대로 저장하면 그 사이에 사용자가 고친 글이 빠진 초안이 올라간다. 받은 쪽은 일기를 다시 읽고 초안을 다시 만든다.
	ErrDiaryChanged = errors.New("store: diary changed since it was read")
)

// PostgreSQL의 SQLSTATE 값이다.
const sqlStateUniqueViolation = "23505"

const (
	constraintUsersEmail         = "users_email_key"
	constraintActiveConversation = "conversations_user_id_active_key"
)

// ConstraintName은 오류가 제약 위반일 때 그 제약의 이름을 돌려준다. 아니면 빈 문자열이다.
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// mapError는 드라이버의 오류를 이 패키지의 오류 값으로 감싼다.
// 원래 오류도 함께 감싸 두므로 errors.As로 pgconn.PgError를 꺼내 SQLSTATE를 볼 수 있다.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlStateUniqueViolation {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}
