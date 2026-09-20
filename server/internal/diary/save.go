package diary

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// save는 초안을 저장하고, 그 초안이 다룬 대화의 진행 상태를 닫는다. 둘은 한 트랜잭션이다.
// 초안만 저장되고 상태가 남으면 다음 실행이 같은 대화를 또 붙이고, 상태만 닫히고 초안이 없으면 그 대화는 영영 일기에 들어가지 못한다.
//
// 그날을 잡아 둔 뒤에 읽은 것이 아직 맞는지 본다. 같은 날의 다른 실행, 사용자의 저장, 하루 삭제와 여기서 차례가 갈린다.
func (s *Service) save(ctx context.Context, target Target, snap *snapshot, entry logging.Redacted) (Outcome, error) {
	newID, err := store.NewID()
	if err != nil {
		return "", fmt.Errorf("diary: %w", err)
	}
	now := s.clock.Now()

	outcome := OutcomeNothing
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		rows, err := lockDay(ctx, q, target)
		if err != nil {
			return err
		}
		current := make(map[uuid.UUID]string, len(rows))
		for _, row := range rows {
			current[row.Conversation.ID] = row.Conversation.ProcessingStatus
		}
		for _, id := range snap.considered {
			status, ok := current[id]
			if !ok || covered(status) {
				// 다른 실행이 먼저 담았다. 여기서 또 붙이면 같은 내용이 두 번 들어간다.
				return errStale
			}
		}

		switch {
		case entry != "":
			if err := saveDraft(ctx, q, target, snap, newID, joinEntry(snap.base, entry), now); err != nil {
				return err
			}
			outcome = OutcomeDrafted
		case snap.diary == nil && snap.lineCount() > 0:
			// 사용자는 말을 했는데 일기로 옮길 만한 것이 없었다. 직접 쓸 수 있게 빈 초안을 남긴다.
			if err := saveDraft(ctx, q, target, snap, newID, "", now); err != nil {
				return err
			}
			outcome = OutcomeEmpty
		}
		// 보탤 글이 없으면 있는 일기는 건드리지 않는다. 확인한 일기를 까닭 없이 "확인 필요"로 되돌리지 않는다.

		return closeConversations(ctx, q, target, snap.considered, store.ProcessingDone)
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

// saveDraft는 store.SaveDiaryDraft를 부르고 그 오류를 이 패키지의 뜻으로 바꾼다.
func saveDraft(ctx context.Context, q *db.Queries, target Target, snap *snapshot, newID uuid.UUID, text string, now time.Time) error {
	var seen *time.Time
	if snap.diary != nil {
		seen = &snap.diary.UpdatedAt
	}
	_, err := store.SaveDiaryDraft(ctx, q, store.DiaryDraftWrite{
		NewID:         newID,
		DayID:         target.DayID,
		UserID:        target.UserID,
		SeenUpdatedAt: seen,
		Seal:          sealDraft(snap.sealer, text),
		Now:           now,
	})
	switch {
	case errors.Is(err, store.ErrDiaryChanged):
		// 모델을 부르는 동안 사용자가 일기를 고쳤다. 고치기 전의 글에 붙인 초안을 올리면 고친 것이 사라진다.
		return errStale
	case errors.Is(err, store.ErrNotFound):
		return errGone
	case err != nil:
		return fmt.Errorf("diary: save draft: %w", err)
	}
	return nil
}

func sealDraft(sealer *crypto.Sealer, text string) store.SealFunc {
	return func(rowID uuid.UUID) ([]byte, error) {
		return sealer.SealString(text, sealing.DiaryDraft(rowID))
	}
}

// lockDay는 그날을 잡아 두고 그날의 대화를 다시 읽는다. 잠근 뒤에 읽은 것이라 트랜잭션이 끝날 때까지 다른 실행이 바꾸지 못한다.
func lockDay(ctx context.Context, q *db.Queries, target Target) ([]db.ListConversationsByDayRow, error) {
	if _, err := q.LockDay(ctx, db.LockDayParams{ID: target.DayID, UserID: target.UserID}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errGone
		}
		return nil, fmt.Errorf("diary: lock day: %w", err)
	}
	rows, err := q.ListConversationsByDay(ctx, db.ListConversationsByDayParams{DayID: target.DayID, UserID: target.UserID})
	if err != nil {
		return nil, fmt.Errorf("diary: list conversations: %w", err)
	}
	return rows, nil
}

func closeConversations(ctx context.Context, q *db.Queries, target Target, ids []uuid.UUID, status string) error {
	for _, id := range ids {
		if _, err := q.SetConversationProcessingStatus(ctx, db.SetConversationProcessingStatusParams{
			ProcessingStatus: status, ID: id, UserID: target.UserID,
		}); err != nil {
			return fmt.Errorf("diary: set processing status: %w", err)
		}
	}
	return nil
}

// GiveUp은 초안 만들기를 포기한다. 그날에 일기가 없으면 빈 초안을 남겨 사용자가 직접 쓸 수 있게 하고,
// 다루지 못한 대화의 진행 상태를 failed로 닫는다. 이미 글이 있으면 그 글은 그대로 둔다.
//
// 위기 대응이 있었던 대화는 애초에 초안을 만들지 않는 대화라 실패가 아니다. done으로 닫는다.
func (s *Service) GiveUp(ctx context.Context, target Target) (Result, error) {
	result := Result{Outcome: OutcomeGaveUp, PromptVersion: s.prompt.Version}

	sealer, err := s.sealers.For(ctx, target.UserID)
	if errors.Is(err, sealing.ErrNoKey) {
		result.Outcome = OutcomeGone
		return result, nil
	}
	// 키를 풀 수 없어도 상태는 닫는다. 빈 초안만 남기지 못한다. 그날에 일기가 없어도 사용자는 직접 써서 저장할 수 있다.
	sealErr := err

	newID, err := store.NewID()
	if err != nil {
		return result, fmt.Errorf("diary: %w", err)
	}
	now := s.clock.Now()

	err = s.store.InTx(ctx, func(q *db.Queries) error {
		rows, err := lockDay(ctx, q, target)
		if err != nil {
			return err
		}
		considered, included := pending(rows)
		result.Conversations = len(included)
		result.CrisisSkipped = len(considered) - len(included)
		if len(considered) == 0 {
			result.Outcome = OutcomeNothing
			return nil
		}

		if len(included) > 0 && sealErr == nil {
			if err := emptyDraftIfNone(ctx, q, target, sealer, newID, now); err != nil {
				return err
			}
		}

		isIncluded := make(map[uuid.UUID]bool, len(included))
		for _, id := range included {
			isIncluded[id] = true
		}
		var skipped []uuid.UUID
		for _, id := range considered {
			if !isIncluded[id] {
				skipped = append(skipped, id)
			}
		}
		if err := closeConversations(ctx, q, target, included, store.ProcessingFailed); err != nil {
			return err
		}
		return closeConversations(ctx, q, target, skipped, store.ProcessingDone)
	})
	switch {
	case errors.Is(err, errGone):
		result.Outcome = OutcomeGone
		return result, nil
	case err != nil:
		return result, err
	case sealErr != nil:
		return result, fmt.Errorf("diary: %w", sealErr)
	}
	return result, nil
}

// emptyDraftIfNone은 그날에 일기가 없을 때만 빈 초안을 만든다.
func emptyDraftIfNone(ctx context.Context, q *db.Queries, target Target, sealer *crypto.Sealer, newID uuid.UUID, now time.Time) error {
	_, err := q.GetDiaryByDayID(ctx, db.GetDiaryByDayIDParams{DayID: target.DayID, UserID: target.UserID})
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("diary: get diary: %w", err)
	}
	_, err = store.SaveDiaryDraft(ctx, q, store.DiaryDraftWrite{
		NewID:  newID,
		DayID:  target.DayID,
		UserID: target.UserID,
		Seal:   sealDraft(sealer, ""),
		Now:    now,
	})
	if err != nil {
		return fmt.Errorf("diary: save empty draft: %w", err)
	}
	return nil
}
