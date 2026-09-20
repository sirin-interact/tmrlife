package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 일기의 상태다. diaries.status의 CHECK 제약과 같은 값이어야 한다.
const (
	DiaryDraft     = "draft"
	DiaryConfirmed = "confirmed"
)

// SealFunc는 글을 rowID에 묶어 잠근 암호문을 돌려준다.
//
// 일기는 하루에 하나라서, 저장하려는 글이 새 행에 들어갈지 이미 있는 행에 들어갈지는 그날을 잡아 둔 뒤에야 안다.
// 암호문에는 행 ID가 묶이므로 그 전에는 잠글 수 없다. 그래서 글 대신 잠그는 함수를 받아, 행이 정해진 뒤에 부른다.
// 이 패키지는 여전히 평문을 보지 않는다.
type SealFunc func(rowID uuid.UUID) ([]byte, error)

// DiaryDraftWrite는 대화에서 만든 일기 초안을 저장하는 데 필요한 값이다.
type DiaryDraftWrite struct {
	// NewID는 그날의 일기가 아직 없을 때 새 행의 ID로 쓴다. 이미 있으면 쓰이지 않는다.
	NewID  uuid.UUID
	DayID  uuid.UUID
	UserID uuid.UUID
	// SeenUpdatedAt은 초안을 만들려고 그날의 일기를 읽었을 때 본 updated_at이다. 그때 일기가 없었으면 nil이다.
	// 초안은 이미 있는 글에 새 대화의 내용을 이어 붙여 만든다. AI를 부르는 동안 사용자가 그 글을 고쳤다면
	// 고치기 전의 글로 만든 초안을 올리면 안 되므로, 저장할 때 본 것과 지금 것이 같은지 확인한다.
	SeenUpdatedAt *time.Time
	Seal          SealFunc
	Now           time.Time
}

// DiaryBodyWrite는 사용자가 고쳐서 확인한 일기 글을 저장하는 데 필요한 값이다.
type DiaryBodyWrite struct {
	// NewID는 그날의 일기가 아직 없을 때 새 행의 ID로 쓴다. 이미 있으면 쓰이지 않는다.
	NewID  uuid.UUID
	DayID  uuid.UUID
	UserID uuid.UUID
	Seal   SealFunc
	Now    time.Time
}

// SaveDiaryDraft는 그날의 일기 초안을 만들거나 바꾼다. 상태는 draft가 되고 사용자가 확인한 글은 건드리지 않는다.
//
//   - 그날의 기록이 없으면(그 사이에 하루를 지웠으면) ErrNotFound. 초안 작업은 거기서 그만둔다.
//   - 읽었을 때와 일기가 달라졌으면 ErrDiaryChanged. 일기를 다시 읽고 초안을 다시 만든다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 그날을 잡아 둔 잠금이 트랜잭션이 끝날 때까지 이어져야
// 같은 날의 다른 저장과 차례가 갈린다. 트랜잭션 안에서 AI를 부르지 않는다. 초안은 미리 만들어 두고 Seal에서는 잠그기만 한다.
func SaveDiaryDraft(ctx context.Context, q *db.Queries, in DiaryDraftWrite) (db.Diary, error) {
	existing, found, err := lockDayAndGetDiary(ctx, q, in.DayID, in.UserID)
	if err != nil {
		return db.Diary{}, err
	}

	switch {
	case !found && in.SeenUpdatedAt != nil:
		// 읽을 때는 있었는데 지금은 없다. 하루를 지웠다가 그날 다시 대화한 경우다.
		return db.Diary{}, ErrDiaryChanged
	case found && (in.SeenUpdatedAt == nil || !existing.UpdatedAt.Equal(*in.SeenUpdatedAt)):
		return db.Diary{}, ErrDiaryChanged
	}

	if !found {
		sealed, err := seal(in.Seal, in.NewID)
		if err != nil {
			return db.Diary{}, err
		}
		diary, err := q.CreateDiaryDraft(ctx, db.CreateDiaryDraftParams{
			ID: in.NewID, DayID: in.DayID, UserID: in.UserID, DraftEnc: sealed, Now: in.Now,
		})
		if err != nil {
			return db.Diary{}, fmt.Errorf("insert diary draft: %w", err)
		}
		return diary, nil
	}

	sealed, err := seal(in.Seal, existing.ID)
	if err != nil {
		return db.Diary{}, err
	}
	diary, err := q.ReplaceDiaryDraft(ctx, db.ReplaceDiaryDraftParams{
		ID: existing.ID, UserID: in.UserID, DraftEnc: sealed, Now: in.Now,
	})
	if err != nil {
		return db.Diary{}, fmt.Errorf("replace diary draft: %w", err)
	}
	return diary, nil
}

// SaveDiaryBody는 사용자가 고친 글을 그날의 일기로 확인한다. 상태는 confirmed가 되고 초안은 비워진다.
// 그날의 일기가 아직 없으면 새로 만든다. 초안을 자동으로 만들지 않은 날에도 사용자는 일기를 직접 쓸 수 있다.
//
// 그날의 기록이 없으면 ErrNotFound다. 대화하지 않은 날에는 일기를 쓸 수 없다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 까닭은 SaveDiaryDraft와 같다.
func SaveDiaryBody(ctx context.Context, q *db.Queries, in DiaryBodyWrite) (db.Diary, error) {
	existing, found, err := lockDayAndGetDiary(ctx, q, in.DayID, in.UserID)
	if err != nil {
		return db.Diary{}, err
	}

	if !found {
		sealed, err := seal(in.Seal, in.NewID)
		if err != nil {
			return db.Diary{}, err
		}
		diary, err := q.CreateConfirmedDiary(ctx, db.CreateConfirmedDiaryParams{
			ID: in.NewID, DayID: in.DayID, UserID: in.UserID, BodyEnc: sealed, Now: in.Now,
		})
		if err != nil {
			return db.Diary{}, fmt.Errorf("insert confirmed diary: %w", err)
		}
		return diary, nil
	}

	sealed, err := seal(in.Seal, existing.ID)
	if err != nil {
		return db.Diary{}, err
	}
	diary, err := q.ConfirmDiary(ctx, db.ConfirmDiaryParams{
		ID: existing.ID, UserID: in.UserID, BodyEnc: sealed, Now: in.Now,
	})
	if err != nil {
		return db.Diary{}, fmt.Errorf("confirm diary: %w", err)
	}
	return diary, nil
}

// lockDayAndGetDiary는 그날을 잡아 두고 그날의 일기를 읽는다.
// 일기가 없는 것은 오류가 아니다. 새로 만들 차례라는 뜻이고 found가 false다.
func lockDayAndGetDiary(ctx context.Context, q *db.Queries, dayID, userID uuid.UUID) (diary db.Diary, found bool, err error) {
	if _, err := q.LockDay(ctx, db.LockDayParams{ID: dayID, UserID: userID}); err != nil {
		return db.Diary{}, false, fmt.Errorf("lock day: %w", err)
	}
	diary, err = q.GetDiaryByDayID(ctx, db.GetDiaryByDayIDParams{DayID: dayID, UserID: userID})
	switch {
	case errors.Is(err, ErrNotFound):
		return db.Diary{}, false, nil
	case err != nil:
		return db.Diary{}, false, fmt.Errorf("get diary: %w", err)
	}
	return diary, true, nil
}

func seal(fn SealFunc, rowID uuid.UUID) ([]byte, error) {
	if fn == nil {
		return nil, errors.New("store: diary write needs a seal function")
	}
	sealed, err := fn(rowID)
	if err != nil {
		return nil, fmt.Errorf("seal diary text: %w", err)
	}
	if len(sealed) == 0 {
		// 빈 글도 잠그면 길이가 있는 암호문이 된다. 빈 값이 왔다면 잠그지 않은 것이다.
		return nil, errors.New("store: seal function returned no ciphertext")
	}
	return sealed, nil
}
