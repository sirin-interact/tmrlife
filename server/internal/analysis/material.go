package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// material은 대화 하나에서 신호를 뽑는 데 필요한 모든 것이다.
type material struct {
	dayID  uuid.UUID
	sealer *crypto.Sealer
	// transcript는 대화를 순번대로 담는다. 사용자의 말에는 번호가 붙어 있다.
	transcript []turn
	// lines는 사용자가 한 말만 번호순으로 담는다. 근거를 대조할 곳이다.
	lines []userLine
	// unreadable은 열리지 않아 뺀 사용자 발화의 수다.
	unreadable int

	// 아래 셋은 모델을 부르지 않고 끝나는 경우다.
	alreadyDone bool
	disabled    bool
	takeover    bool
}

// turn은 모델에 보낼 한 줄이다.
type turn struct {
	// number는 사용자의 말이면 1부터 세는 줄 번호이고, 상대의 말이면 0이다.
	number int
	text   logging.Redacted
}

// userLine은 사용자가 한 말 하나다. text는 공백을 줄인 꼴이고, 근거는 이 글자와 대조한다.
type userLine struct {
	number      int
	utteranceID uuid.UUID
	text        logging.Redacted
	// gateFlagged는 위기 관문이 확인 단계 이상으로 판정한 발화인지다. 그런 발화는 어느 항목의 근거도 될 수 없다.
	gateFlagged bool
}

// load는 대화를 맡고, 사용자의 설정을 보고, 발화를 읽어 연다.
//
// 맡기(ClaimConversationAnalysis)는 이미 끝난 분석을 두 번 하지 않게 하는 문이다.
// 저장이 어긋나지 않게 지키는 것은 (대화, 항목)의 유일 제약이므로, 맡지 못했다고 곧바로 포기하지 않고 왜 맡지 못했는지 본다.
func (s *Service) load(ctx context.Context, target Target) (*material, error) {
	q := s.store.Queries()

	settings, err := q.GetUserSettings(ctx, target.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// 설정은 계정과 함께 만들어진다. 없으면 계정이 지워진 것이다.
		return nil, errGone
	case err != nil:
		return nil, fmt.Errorf("analysis: get user settings: %w", err)
	case !settings.AnalysisEnabled:
		return &material{disabled: true}, nil
	}

	conversation, takeover, err := s.claim(ctx, target)
	if err != nil {
		return nil, err
	}
	if conversation.AnalysisStatus == store.AnalysisDone {
		return &material{alreadyDone: true}, nil
	}

	m := &material{dayID: conversation.DayID, takeover: takeover}
	m.sealer, err = s.sealers.For(ctx, target.UserID)
	if errors.Is(err, sealing.ErrNoKey) {
		return nil, errGone
	}
	if err != nil {
		return nil, fmt.Errorf("analysis: %w", err)
	}

	if err := s.loadTranscript(ctx, target, m); err != nil {
		return nil, err
	}
	return m, nil
}

// claim은 대화를 맡는다. 맡지 못했으면 지금 어떤 상태인지 다시 읽어 갈림길을 정한다.
func (s *Service) claim(ctx context.Context, target Target) (conversation db.Conversation, takeover bool, err error) {
	q := s.store.Queries()

	conversation, err = q.ClaimConversationAnalysis(ctx, db.ClaimConversationAnalysisParams{
		ID: target.ConversationID, UserID: target.UserID,
	})
	if err == nil {
		return conversation, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return db.Conversation{}, false, fmt.Errorf("analysis: claim conversation: %w", err)
	}

	current, err := q.GetConversation(ctx, db.GetConversationParams{ID: target.ConversationID, UserID: target.UserID})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return db.Conversation{}, false, errGone
	case err != nil:
		return db.Conversation{}, false, fmt.Errorf("analysis: get conversation: %w", err)
	}

	switch row := current.Conversation; {
	case row.AnalysisStatus == store.AnalysisDone:
		return row, false, nil
	case row.Status == store.ConversationActive:
		// 아직 이어질 말이 남았다. 지금 뽑으면 일부만 본 결과가 그날의 판단으로 굳는다.
		return db.Conversation{}, false, ErrStillActive
	case row.AnalysisStatus == store.AnalysisRunning:
		// 맡아 둔 실행이 끝맺지 못했다. 프로세스가 내려가면 상태만 남고 아무도 그 대화를 다시 맡을 수 없다.
		// 이어받아도 행이 두 번 들어갈 수는 없으므로(유일 제약), 멈춘 채로 두지 않고 이어받는다.
		return row, true, nil
	default:
		return db.Conversation{}, false, ErrBusy
	}
}

// loadTranscript는 대화의 발화를 순번대로 읽어 연다.
//
// 상대가 한 말도 함께 연다. 사용자가 "응", "아니"로만 답한 말은 그 앞의 물음이 없으면 무슨 뜻인지 알 수 없어서,
// 물음을 빼면 그런 대화의 항목이 통째로 언급 없음이 된다. 상대의 말이 근거로 새어 들어오는 길은
// 지시문이 아니라 근거 대조가 막는다. 근거는 번호 붙은 사용자의 줄에서 글자 그대로 찾지 못하면 버려진다.
//
// 상대의 말이 열리지 않으면 그 줄만 빼고 이어 간다. 흐름을 조금 잃을 뿐 판단이 틀려지지는 않는다.
// 사용자의 말이 열리지 않으면 세어 둔다. 하나도 열리지 않으면 키가 맞지 않는 것이므로 "말이 없던 대화"로 넘기지 않는다.
func (s *Service) loadTranscript(ctx context.Context, target Target, m *material) error {
	utterances, err := s.store.Queries().ListUtterancesByConversation(ctx, db.ListUtterancesByConversationParams{
		ConversationID: target.ConversationID, UserID: target.UserID,
	})
	if err != nil {
		return fmt.Errorf("analysis: list utterances: %w", err)
	}

	userUtterances := 0
	for _, u := range utterances {
		isUser := u.Speaker == store.SpeakerUser
		if isUser {
			userUtterances++
		}
		text, err := m.sealer.OpenString(u.TextEnc, sealing.UtteranceText(u.ID))
		if err != nil {
			if isUser {
				m.unreadable++
			}
			// crypto의 오류에는 자리(테이블, 컬럼, 행 ID)만 있고 글은 없다.
			s.logger.LogAttrs(ctx, slog.LevelError, "signal extraction: utterance cannot be opened",
				slog.String("user_id", target.UserID.String()),
				slog.String("conversation_id", target.ConversationID.String()),
				slog.String("utterance_id", u.ID.String()),
				slog.String("speaker", u.Speaker),
			)
			continue
		}
		line := oneLine(text)
		if line == "" {
			continue
		}
		if !isUser {
			m.transcript = append(m.transcript, turn{text: logging.Redacted(line)})
			continue
		}
		flagged, err := s.gateFlagged(ctx, target, u.ID)
		if err != nil {
			return err
		}
		number := len(m.lines) + 1
		m.lines = append(m.lines, userLine{
			number: number, utteranceID: u.ID, text: logging.Redacted(line), gateFlagged: flagged,
		})
		m.transcript = append(m.transcript, turn{number: number, text: logging.Redacted(line)})
	}

	if userUtterances > 0 && m.unreadable == userUtterances {
		return fmt.Errorf("%w: none of %d user utterances", ErrUnreadable, userUtterances)
	}
	return nil
}

// gateFlagged는 위기 관문이 그 발화를 확인 단계 이상으로 판정했는지다.
//
// 자해와 죽음에 관한 표현은 여덟 항목이 아니다. 그런데 "다 사라졌으면 좋겠어"는 절망감을 말한 것이기도 해서,
// 지시문으로 "어느 항목에도 넣지 말라"고 시키는 것만으로는 막히지 않는다(실제로 실행마다 갈렸다).
// 그래서 관문이 걸러 낸 발화는 근거로 쓸 수 없게 코드가 막는다. 판정은 관문이 이미 내렸으므로
// 이 패키지가 자해 표현의 목록을 따로 들지 않는다. 목록이 둘이 되면 서로 어긋난다.
//
// 걸린 발화도 모델에는 보낸다. 흐름을 알아야 나머지 항목을 제대로 볼 수 있고, 그 글은 이미 관문의 모델이 본 글이다.
// 다만 그 줄에서만 찾히는 근거는 버려지고 그 항목은 언급 없음이 된다.
//
// 판정이 없는 발화는 걸리지 않은 것으로 본다. 사용자의 발화는 모두 관문을 거치므로 판정이 없는 행은
// 관문이 돌기 전에 저장된 옛 기록이다. 없는 판정을 "걸렸다"로 보면 그런 기록의 신호가 통째로 사라진다.
//
// 발화마다 한 번씩 묻는다. 대화가 끝난 뒤에 도는 작업이고 대화 하나의 발화는 수십 개를 넘지 않는다.
// 대화의 판정을 한 번에 읽는 쿼리가 생기면 그것으로 바꾼다.
func (s *Service) gateFlagged(ctx context.Context, target Target, utteranceID uuid.UUID) (bool, error) {
	event, err := s.store.Queries().GetGateEventByUtterance(ctx, db.GetGateEventByUtteranceParams{
		UtteranceID: utteranceID, UserID: target.UserID,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("analysis: get gate event: %w", err)
	}
	return event.FinalStage >= gateStageCheck, nil
}

// gateStageCheck는 위기 관문의 확인 단계다. 이 단계 이상으로 걸린 발화는 근거가 될 수 없다.
const gateStageCheck = 1

// oneLine은 발화 하나를 한 줄로 만든다. 이어진 공백과 줄바꿈은 한 칸으로 줄인다.
// 모델에 보내는 글과 근거를 대조하는 글이 모두 이 꼴이라, 모델이 본 글자와 대조하는 글자가 같다.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
