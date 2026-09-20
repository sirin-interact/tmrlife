package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 대화와 발화의 열거 값이다. 테이블의 CHECK 제약과 같은 값이어야 하고, 시험이 둘을 견준다.
const (
	ConversationActive    = "active"
	ConversationEnded     = "ended"
	ConversationAbandoned = "abandoned"

	ModeVoice = "voice"
	ModeChat  = "chat"

	SpeakerUser = "user"
	SpeakerAI   = "ai"

	// OriginModel은 대화 AI의 출력, OriginFixed는 미리 써 둔 문구,
	// OriginTemplate은 AI의 출력이 검사에서 걸려 사용자의 표현을 넣은 고정 문형으로 바꾼 것이다.
	OriginUser     = "user"
	OriginModel    = "model"
	OriginFixed    = "fixed"
	OriginTemplate = "template"

	// 애매한 표현을 확인하는 두 걸음 가운데 어디까지 왔는지다. 앞으로만 간다.
	CheckStateNone      = "none"
	CheckStateReflected = "reflected"
	CheckStateAsked     = "asked"

	// EndReasonIdle은 한동안 말이 없어 닫은 경우다. 끊긴 연결이 돌아오지 않은 경우와
	// 기록 날짜가 지나도록 열려 있던 대화를 닫는 경우도 여기에 든다.
	EndReasonUser   = "user"
	EndReasonIdle   = "idle"
	EndReasonCrisis = "crisis"
	EndReasonError  = "error"

	// 대화가 끝난 뒤에 도는 작업의 진행 상태다.
	ProcessingNone    = "none"
	ProcessingPending = "pending"
	ProcessingRunning = "running"
	ProcessingDone    = "done"
	ProcessingFailed  = "failed"
)

// NewConversation은 새 대화 하나를 여는 데 필요한 값이다.
type NewConversation struct {
	ID uuid.UUID
	// NewDayID는 그 기록 날짜의 하루가 아직 없을 때 새 하루의 ID로 쓴다. 이미 있으면 쓰이지 않는다.
	NewDayID uuid.UUID
	UserID   uuid.UUID
	// RecordDate는 대화를 시작한 시각을 recorddate.Of로 바꾼 기록 날짜다.
	RecordDate  recorddate.Date
	StartedMode string
	Now         time.Time
}

// OpenConversation은 기록 날짜의 하루를 찾거나 만들고 그 위에 새 대화를 연다. 한 트랜잭션으로 돈다.
// 그 사용자에게 열린 대화가 이미 있으면 ErrActiveConversationExists를 돌려준다.
//
// 열린 대화가 있는지는 부르는 쪽이 GetActiveConversation으로 먼저 본다. 있으면 이어가고, 기록 날짜가 지난 것이면 먼저 끝낸다.
// 그렇게 보고 왔는데도 이 오류가 나왔다면 그 사이에 다른 연결이 대화를 연 것이다. 다시 읽어 그 대화를 이어간다.
func (s *Store) OpenConversation(ctx context.Context, in NewConversation) (db.Conversation, error) {
	var conversation db.Conversation
	err := s.InTx(ctx, func(q *db.Queries) error {
		var err error
		conversation, err = OpenConversation(ctx, q, in)
		return err
	})
	if err != nil {
		return db.Conversation{}, err
	}
	return conversation, nil
}

// OpenConversation은 Store.OpenConversation과 같은 일을, 부르는 쪽이 이미 연 트랜잭션 안에서 한다.
// 기록 날짜가 지난 대화를 끝내는 일과 새 대화를 여는 일을 한데 묶을 때 쓴다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 오류가 나면 트랜잭션은 이미 깨져 있으므로 부르는 쪽은 그 오류를 그대로 돌려줘 되돌린다.
func OpenConversation(ctx context.Context, q *db.Queries, in NewConversation) (db.Conversation, error) {
	recordDate, err := PGDate(in.RecordDate)
	if err != nil {
		return db.Conversation{}, err
	}
	dayID, err := q.UpsertDay(ctx, db.UpsertDayParams{
		ID: in.NewDayID, UserID: in.UserID, RecordDate: recordDate, Now: in.Now,
	})
	if err != nil {
		return db.Conversation{}, fmt.Errorf("upsert day: %w", err)
	}
	conversation, err := q.CreateConversation(ctx, db.CreateConversationParams{
		ID: in.ID, UserID: in.UserID, DayID: dayID, StartedMode: in.StartedMode, Now: in.Now,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) && ConstraintName(err) == constraintActiveConversation {
			return db.Conversation{}, ErrActiveConversationExists
		}
		return db.Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return conversation, nil
}

// NewUtterance는 대화에 더할 발화 하나다. 순번은 받지 않는다. 저장할 때 대화의 다음 순번이 붙는다.
type NewUtterance struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	UserID         uuid.UUID
	Speaker        string
	Modality       string
	Origin         string
	// TextEnc는 ID를 행 ID로 묶어 잠근 암호문이다.
	TextEnc          []byte
	STTMinConfidence *float32
	// ClientMessageID는 클라이언트가 글을 보낼 때 붙인 식별자다. 사용자의 발화에만 줄 수 있다.
	// nil이면 같은 글을 다시 보낸 것인지 가리지 않는다.
	ClientMessageID *uuid.UUID
	Now             time.Time
}

// AppendedUtterance는 AppendUtterance의 결과다.
type AppendedUtterance struct {
	Utterance db.Utterance
	// Duplicate는 같은 ClientMessageID의 발화가 이미 있어서 새로 넣지 않았다는 뜻이다.
	// 이때 Utterance는 먼저 저장된 발화이고, 그 암호문은 이번에 넘긴 ID가 아니라 그 발화의 ID에 묶여 있다.
	//
	// 먼저 저장된 발화의 처리가 끝까지 갔다는 보장은 없다. 발화만 저장하고 판정과 답을 남기기 전에 프로세스가 죽었을 수 있다.
	// 부르는 쪽은 그 발화의 관문 기록과 뒤따르는 AI의 말이 있는지 보고, 없으면 그 발화로 턴을 마저 돌린다.
	Duplicate bool
}

// AppendUtterance는 대화의 다음 순번으로 발화 하나를 더한다. 한 트랜잭션으로 돈다.
//
// 같은 대화에 동시에 더해도 순번은 0부터 빈 데 없이, 겹치지 않고 이어진다.
//   - 없는 대화이거나 남의 대화면 ErrNotFound
//   - 이미 끝난 대화면 ErrConversationNotActive
//   - 같은 ClientMessageID의 발화가 이미 있으면 오류 없이 그 발화를 Duplicate와 함께 돌려준다(끝난 대화여도 그렇다)
func (s *Store) AppendUtterance(ctx context.Context, in NewUtterance) (AppendedUtterance, error) {
	var appended AppendedUtterance
	err := s.InTx(ctx, func(q *db.Queries) error {
		var err error
		appended, err = AppendUtterance(ctx, q, in)
		return err
	})
	if err != nil {
		return AppendedUtterance{}, err
	}
	return appended, nil
}

// AppendUtterance는 Store.AppendUtterance와 같은 일을, 부르는 쪽이 이미 연 트랜잭션 안에서 한다.
// 판정 기록과 AI의 말을 함께 남기거나 함께 남기지 않아야 할 때 쓴다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 트랜잭션 밖의 쿼리를 넘기면 대화를 잡아 둔 잠금이 문장이 끝나자마자 풀려서,
// 동시에 들어온 발화가 같은 순번을 고르고 한쪽이 유일 제약에 걸려 실패한다.
// 잠금은 트랜잭션이 끝날 때까지 이어지므로, 그동안 같은 대화에 발화를 더하거나 대화를 끝내려는 쪽은 기다린다.
// 트랜잭션 안에서 AI를 부르는 것처럼 오래 걸리는 일을 하지 않는다.
func AppendUtterance(ctx context.Context, q *db.Queries, in NewUtterance) (AppendedUtterance, error) {
	locked, err := q.LockConversationForAppend(ctx, db.LockConversationForAppendParams{
		ID: in.ConversationID, UserID: in.UserID,
	})
	if err != nil {
		return AppendedUtterance{}, fmt.Errorf("lock conversation: %w", err)
	}

	// 다시 보낸 글인지는 잠근 뒤에 본다. 잠그기 전에 보면 같은 글 둘이 나란히 "없음"을 보고 둘 다 들어간다.
	if in.ClientMessageID != nil {
		existing, err := q.GetUtteranceByClientMessageID(ctx, db.GetUtteranceByClientMessageIDParams{
			ConversationID: in.ConversationID, ClientMessageID: *in.ClientMessageID,
		})
		switch {
		case err == nil:
			return AppendedUtterance{Utterance: existing, Duplicate: true}, nil
		case !errors.Is(err, ErrNotFound):
			return AppendedUtterance{}, fmt.Errorf("look up client message id: %w", err)
		}
	}

	if locked.Status != ConversationActive {
		return AppendedUtterance{}, ErrConversationNotActive
	}

	utterance, err := q.AppendUtteranceUnderLock(ctx, db.AppendUtteranceUnderLockParams{
		ID:               in.ID,
		ConversationID:   in.ConversationID,
		UserID:           in.UserID,
		Speaker:          in.Speaker,
		Modality:         in.Modality,
		Origin:           in.Origin,
		TextEnc:          in.TextEnc,
		STTMinConfidence: in.STTMinConfidence,
		ClientMessageID:  in.ClientMessageID,
		Now:              in.Now,
	})
	if err != nil {
		return AppendedUtterance{}, fmt.Errorf("insert utterance: %w", err)
	}
	return AppendedUtterance{Utterance: utterance}, nil
}
