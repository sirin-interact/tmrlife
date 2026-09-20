package diary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// snapshot은 초안을 만들려고 읽어 둔 그날의 모습이다.
// 잠그지 않고 읽는다. 모델을 부르는 동안 잠금을 쥐고 있을 수 없기 때문이다. 읽은 것이 아직 맞는지는 저장할 때 그날을 잡아 두고 확인한다.
type snapshot struct {
	sealer *crypto.Sealer
	// diary는 그날의 일기다. 없으면 nil이다.
	diary *db.Diary
	// base는 이미 있는 글이다. 확인을 기다리는 초안이 있으면 그 초안이고, 아니면 사용자가 확인한 글이다.
	base logging.Redacted
	mode Mode
	// considered는 끝났고 아직 일기에 담기지 않은 대화다. 저장할 때 이 대화들의 진행 상태를 닫는다.
	considered []uuid.UUID
	// included는 그 가운데 초안의 재료가 되는 대화다. 위기 대응이 있었던 대화는 빠진다.
	included []uuid.UUID
	// lines는 included의 대화마다 사용자가 한 말을 순서대로 담는다.
	lines [][]logging.Redacted
	// unreadable은 열리지 않아 뺀 발화의 수다.
	unreadable int
}

func (s *snapshot) lineCount() int {
	n := 0
	for _, conversation := range s.lines {
		n += len(conversation)
	}
	return n
}

// materialKey는 모델에 보낼 재료가 같은지 가리는 값이다.
// 끝난 대화에는 발화가 더해지지 않으므로 방식과 대화의 ID가 같으면 재료도 같다.
func (s *snapshot) materialKey() string {
	var b strings.Builder
	b.WriteString(string(s.mode))
	for _, id := range s.included {
		b.WriteByte('|')
		b.WriteString(id.String())
	}
	return b.String()
}

// Finished는 대화의 진행 상태(conversations.processing_status)를 보고 그 대화의 초안 작업이 끝났는지 알려준다.
// 초안을 기다리는 쪽(대화 채널)이 이 값으로 기다림을 끝낸다. 끝났다고 일기가 있는 것은 아니다.
// 위기 대응이 있었던 대화나 말이 없었던 대화는 일기 없이 끝난다. 일기가 생겼는지는 그날의 일기 행으로 본다.
func Finished(processingStatus string) bool {
	return covered(processingStatus)
}

// covered는 그 대화가 이미 일기에 담겼거나 담기를 포기했는지다. 어느 쪽이든 다시 재료로 삼지 않는다.
func covered(processingStatus string) bool {
	return processingStatus == store.ProcessingDone || processingStatus == store.ProcessingFailed
}

// pending은 그날의 대화 가운데 이번에 다룰 것을 고른다.
// 열려 있는 대화는 뺀다. 끝날 때 제 작업이 따로 등록되고, 그 전에 담으면 그 뒤에 한 말이 빠진다.
func pending(rows []db.ListConversationsByDayRow) (considered, included []uuid.UUID) {
	for _, row := range rows {
		c := row.Conversation
		if c.Status == store.ConversationActive || covered(c.ProcessingStatus) {
			continue
		}
		considered = append(considered, c.ID)
		if !row.Crisis {
			included = append(included, c.ID)
		}
	}
	return considered, included
}

func (s *Service) targetByDate(ctx context.Context, userID uuid.UUID, date recorddate.Date) (Target, bool, error) {
	recordDate, err := store.PGDate(date)
	if err != nil {
		return Target{}, false, fmt.Errorf("diary: %w", err)
	}
	day, err := s.store.Queries().GetDayByDate(ctx, db.GetDayByDateParams{UserID: userID, RecordDate: recordDate})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Target{}, false, nil
	case err != nil:
		return Target{}, false, fmt.Errorf("diary: get day: %w", err)
	}
	return Target{UserID: userID, DayID: day.ID}, true, nil
}

// load는 그날의 대화, 일기, 사용자 발화를 읽어 연다.
func (s *Service) load(ctx context.Context, target Target) (*snapshot, error) {
	q := s.store.Queries()

	rows, err := q.ListConversationsByDay(ctx, db.ListConversationsByDayParams{DayID: target.DayID, UserID: target.UserID})
	if err != nil {
		return nil, fmt.Errorf("diary: list conversations: %w", err)
	}
	if len(rows) == 0 {
		// 하루는 첫 대화와 함께 만들어지므로 대화 없는 하루는 없다. 하루가 지워진 것이다.
		return nil, errGone
	}

	snap := &snapshot{mode: ModeFirst}
	snap.considered, snap.included = pending(rows)
	if len(snap.considered) == 0 {
		return snap, nil
	}

	snap.sealer, err = s.sealers.For(ctx, target.UserID)
	if errors.Is(err, sealing.ErrNoKey) {
		return nil, errGone
	}
	if err != nil {
		return nil, fmt.Errorf("diary: %w", err)
	}

	diary, err := q.GetDiaryByDayID(ctx, db.GetDiaryByDayIDParams{DayID: target.DayID, UserID: target.UserID})
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("diary: get diary: %w", err)
	default:
		snap.diary = &diary
		snap.base, err = openExisting(snap.sealer, diary)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(snap.base)) != "" {
			snap.mode = ModeAppend
		}
	}

	if len(snap.included) == 0 {
		return snap, nil
	}
	if err := s.loadLines(ctx, target, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// openExisting은 이미 있는 글을 연다. 열리지 않으면 그 뒤에 이어 붙일 수 없고, 덮어쓰면 사용자의 글이 사라진다.
func openExisting(sealer *crypto.Sealer, diary db.Diary) (logging.Redacted, error) {
	var (
		text string
		err  error
	)
	switch {
	case diary.Status == store.DiaryDraft && diary.DraftEnc != nil:
		// 확인을 기다리는 초안은 확인한 글에 앞선 대화의 내용을 이미 붙인 것이다. 그 뒤에 잇는다.
		text, err = sealer.OpenString(diary.DraftEnc, sealing.DiaryDraft(diary.ID))
	case diary.BodyEnc != nil:
		text, err = sealer.OpenString(diary.BodyEnc, sealing.DiaryBody(diary.ID))
	}
	if err != nil {
		// crypto의 오류에는 자리(테이블, 컬럼, 행 ID)만 있고 글은 없다.
		return "", fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return logging.Redacted(text), nil
}

// loadLines는 재료가 될 대화에서 사용자가 한 말만 골라 연다. AI가 한 말은 열지도 않는다.
func (s *Service) loadLines(ctx context.Context, target Target, snap *snapshot) error {
	utterances, err := s.store.Queries().ListUtterancesByDay(ctx, db.ListUtterancesByDayParams{DayID: target.DayID, UserID: target.UserID})
	if err != nil {
		return fmt.Errorf("diary: list utterances: %w", err)
	}

	index := make(map[uuid.UUID]int, len(snap.included))
	for i, id := range snap.included {
		index[id] = i
	}
	snap.lines = make([][]logging.Redacted, len(snap.included))

	userUtterances := 0
	for _, u := range utterances {
		i, ok := index[u.ConversationID]
		if !ok || u.Speaker != store.SpeakerUser {
			continue
		}
		userUtterances++
		text, err := snap.sealer.OpenString(u.TextEnc, sealing.UtteranceText(u.ID))
		if err != nil {
			// 발화 하나가 열리지 않는다고 나머지 말까지 버리지 않는다. 빠진 채로 쓴 초안에는 지어낸 것이 없고, 사용자가 읽고 고친다.
			snap.unreadable++
			s.logger.LogAttrs(ctx, slog.LevelError, "diary draft: utterance cannot be opened",
				slog.String("user_id", target.UserID.String()),
				slog.String("conversation_id", u.ConversationID.String()),
				slog.String("utterance_id", u.ID.String()),
			)
			continue
		}
		if line := oneLine(text); line != "" {
			snap.lines[i] = append(snap.lines[i], logging.Redacted(line))
		}
	}

	if userUtterances > 0 && snap.unreadable == userUtterances {
		// 하나도 열리지 않았다면 키가 맞지 않는 것이다. "할 말이 없던 날"로 넘기지 않는다.
		return fmt.Errorf("%w: none of %d user utterances", ErrUnreadable, userUtterances)
	}
	return nil
}

// oneLine은 발화 하나를 한 줄로 만든다. 줄이 나뉘어 있으면 번호 붙은 목록에서 다른 발화처럼 보인다.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
