package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// errNotActive는 끝내려는 대화가 이미 끝나 있었다는 뜻이다.
// 끝내기 버튼과 무응답 타이머가 겹쳐도 한쪽만 대화를 끝내고, 다른 쪽은 이 오류를 받아 조용히 물러난다.
var errNotActive = errors.New("engine: conversation is not active")

// endTarget은 끝낼 대화 하나다.
type endTarget struct {
	userID         uuid.UUID
	conversationID uuid.UUID
	dayID          uuid.UUID
}

// closer는 대화를 끝내고 뒤이어 돌 작업(일기 초안, 마음 신호 추출)을 등록하는 자리다.
// 엔진과 쓸어 담는 작업이 함께 쓴다.
type closer struct {
	store    *store.Store
	diary    DiaryEnqueuer
	analysis AnalysisEnqueuer
	clock    clock.Clock
}

// endResult는 대화를 끝낸 결과다.
type endResult struct {
	// DiaryExpected가 참이면 같은 트랜잭션에서 일기 초안 작업을 등록했다.
	DiaryExpected bool
	// AnalysisExpected가 참이면 같은 트랜잭션에서 마음 신호 추출 작업을 등록했다.
	AnalysisExpected bool
	// crisis는 그 대화에 대응 단계 이상의 판정이 있었는지다.
	crisis bool
}

// end는 대화를 끝내고, 뒤이어 돌 작업을 같은 트랜잭션에서 등록한다.
//
// 한 트랜잭션인 이유: 따로 하면 대화는 끝났는데 작업이 없거나(초안도 신호도 영영 생기지 않는다),
// 작업자가 아직 끝나지 않은 대화를 보게 된다.
//
// # 두 작업의 차이
//
// 대응 단계 이상의 판정이 있었던 대화에서는 초안을 만들지 않는다. 그런 대화의 말로 일기를 쓰지 않는 것은
// 위기 즉시 대응 경로가 일상 흐름을 멈춘다는 규칙이다. 그날의 다른 대화는 평소대로 담긴다.
//
// 신호 추출은 그런 대화에서도 돈다. 뽑는 것이 여덟 항목의 정해진 값과 사용자가 한 말 그대로의 토막뿐이어서
// 힘든 순간에 새로 지어내 들려줄 글이 없고, 그 하루를 비워 두면 그날이 "대화하지 않은 날"이 되어
// 점수를 나누는 일수까지 하나 줄어든다. 가장 무거운 날의 기록이 상태를 가볍게 보이게 만드는 셈이다.
//
// # 넣는 순서
//
// 초안을 먼저, 추출을 뒤에 넣는다. 둘은 한 트랜잭션이라 어느 쪽이 먼저든 함께 들어가지만, 큐는 넣은 순서로 집는다.
// 초안은 사용자가 화면에서 기다리고 있는 결과이고(대화를 끝내면 일기 화면이 열린다),
// 추출은 아무도 기다리지 않는 뒷일이다. 작업자 자리가 하나뿐일 때 기다리는 쪽이 먼저 끝난다.
//
// quietSince를 주면 그 시각 뒤로 발화가 하나도 없을 때만 끝낸다. 끊긴 연결을 한참 뒤에 닫을 때 쓴다.
func (c closer) end(ctx context.Context, t endTarget, reason string, quietSince *time.Time) (endResult, error) {
	var result endResult
	err := c.store.InTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		crisis, err := q.ConversationHasCrisisGateEvent(ctx, db.ConversationHasCrisisGateEventParams{
			ConversationID: t.conversationID, UserID: t.userID,
		})
		if err != nil {
			return fmt.Errorf("read crisis flag: %w", err)
		}
		result.crisis = crisis

		// 작업을 넣지 않는 대화의 진행 상태는 열어 두지 않는다. pending으로 두면 초안을 기다리는 쪽이 끝없이 기다린다.
		enqueue := !crisis && c.diary != nil
		processing := store.ProcessingNone
		if enqueue {
			processing = store.ProcessingPending
		}

		conversation, err := q.EndConversation(ctx, db.EndConversationParams{
			Now: c.clock.Now(), EndReason: reason, ProcessingStatus: processing,
			ID: t.conversationID, UserID: t.userID, QuietSince: quietSince,
		})
		switch {
		case errors.Is(err, store.ErrNotFound):
			return errNotActive
		case err != nil:
			return fmt.Errorf("end conversation: %w", err)
		}
		if enqueue {
			if err := c.diary.EnqueueTx(ctx, tx, diary.ArgsFor(conversation)); err != nil {
				return fmt.Errorf("enqueue diary draft: %w", err)
			}
			result.DiaryExpected = true
		}
		if c.analysis != nil {
			if err := c.analysis.EnqueueTx(ctx, tx, q, analysis.ArgsFor(conversation)); err != nil {
				return fmt.Errorf("enqueue signal extraction: %w", err)
			}
			result.AnalysisExpected = true
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errNotActive) {
			return endResult{}, errNotActive
		}
		return endResult{}, fmt.Errorf("engine: %w", err)
	}
	return result, nil
}

// Nudge는 한동안 말이 없을 때 한 번 묻는 말을 내보낸다.
//
// 무응답 타이머는 채널이 들고 있다. 엔진은 시간을 재지 않고, 그 말을 대화 기록에 남기고 내보내는 일만 한다.
// 기록에 남겨야 다음 턴의 문맥과 일기의 재료가 화면에 보인 것과 같아진다.
// 한 번만 묻는 것과 그 뒤에 대화를 끝내는 것은 채널이 정한다.
func (e *Engine) Nudge(ctx context.Context, s *Session) error {
	if s == nil {
		return errors.New("engine: session is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return ErrConversationEnded
	}
	// 사용자의 말에 대한 답이 아니므로 관문을 거칠 것이 없다. 미리 써 둔 말이 그대로 나간다.
	return e.emitReply(ctx, s, fixed(e.phrases.IdleCheck()), store.CheckStateNone)
}

// End는 대화를 끝낸다. reason은 store.EndReason*이다.
//
// 끝내기 버튼은 언제나 있다. AI는 마무리를 제안할 수 있지만 혼자 끝내지 않으므로, 이 길은 부르는 쪽에서만 열린다.
// 끝난 뒤에는 Ended 사건이 나가고, 같은 손잡이로는 더 말할 수 없다.
// 그 사이에 다른 연결이나 주기 작업이 이미 끝냈으면 ErrConversationEnded다.
func (e *Engine) End(ctx context.Context, s *Session, reason string) error {
	if s == nil {
		return errors.New("engine: session is required")
	}
	switch reason {
	case store.EndReasonUser, store.EndReasonIdle, store.EndReasonCrisis, store.EndReasonError:
	default:
		return fmt.Errorf("engine: unknown end reason %q", reason)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return ErrConversationEnded
	}

	result, err := e.end(ctx, endTarget{userID: s.user.ID, conversationID: s.id, dayID: s.dayID}, reason, nil)
	if err != nil {
		if errors.Is(err, errNotActive) {
			s.ended = true
			return ErrConversationEnded
		}
		return err
	}
	s.ended = true

	ended := Ended{Reason: reason, RecordDate: s.recordDate, DiaryExpected: result.DiaryExpected}
	e.logger.LogAttrs(ctx, slog.LevelInfo, "conversation ended",
		slog.Any("conversation", s), slog.Any("ended", ended), slog.Bool("crisis", result.crisis))
	if err := s.sink.Emit(ctx, ended); err != nil {
		return fmt.Errorf("engine: emit ended: %w", err)
	}
	return nil
}
