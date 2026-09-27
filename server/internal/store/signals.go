package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// 대화 하나의 신호 추출이 어디까지 왔는지다. conversations.analysis_status의 CHECK 제약과 같은 값이어야 하고, 시험이 둘을 견준다.
//
// 일기 초안의 진행 상태(Processing*)와 값은 같지만 컬럼이 다르다. 두 작업은 대화가 끝난 뒤에 나란히 돌기 때문에
// 한 컬럼을 함께 쓰면 서로의 상태를 덮는다. 까닭은 마이그레이션에 적혀 있다.
const (
	// AnalysisNone은 뽑을 것이 없거나 분석을 꺼 둔 대화다. 행의 기본값이다.
	AnalysisNone = "none"
	// AnalysisPending은 추출 작업을 등록했다는 뜻이다.
	AnalysisPending = "pending"
	// AnalysisRunning은 작업자가 맡았다는 뜻이다.
	AnalysisRunning = "running"
	// AnalysisDone은 여덟 항목의 신호 행이 모두 들어갔다는 뜻이다. SaveConversationSignals만 적는다.
	AnalysisDone = "done"
	// AnalysisFailed는 다 시도하고도 뽑지 못했다는 뜻이다.
	AnalysisFailed = "failed"
)

const constraintSignalConversationItem = "signals_conversation_id_item_key"

var (
	// ErrSignalsAlreadySaved는 그 대화의 신호 행이 이미 들어 있다는 뜻이다.
	// 같은 대화를 두 번 분석했을 때다. 먼저 들어간 행이 그대로 남고 이번 결과는 하나도 저장되지 않는다.
	ErrSignalsAlreadySaved = fmt.Errorf("%w: conversation is already analysed", ErrConflict)

	// ErrEvidenceMismatch는 판단과 근거가 맞지 않는다는 뜻이다.
	// 언급된 항목(관찰됨, 관찰되지 않음)에는 근거가 있어야 하고, 언급 없음인 항목에는 근거가 있을 수 없다.
	// 테이블의 CHECK 제약과 같은 규칙이고, 저장하기 전에 여기서 먼저 걸러 어느 항목이 틀렸는지 알려준다.
	ErrEvidenceMismatch = errors.New("store: signal judgement and its evidence do not match")
)

// SignalJudgement는 대화 하나에서 항목 하나에 대해 나온 판단과 그 근거다.
//
// 판단과 명시성은 계산 코어의 타입을 그대로 쓴다. 근거 없는 관찰됨이나 근거가 딸린 언급 없음을
// 저장하기 전에 코어의 규칙으로 걸러 낼 수 있어서, 같은 규칙을 이 패키지에 다시 적지 않는다.
type SignalJudgement struct {
	Judgement signal.Judgement
	// SealEvidence는 판단의 근거가 된 사용자의 발화를 글자 그대로 옮긴 글을 행 ID에 묶어 잠근다.
	// 행 ID는 저장할 때 정해지므로 글이 아니라 잠그는 함수를 받는다. 이 패키지는 평문을 보지 않는다.
	// 언급 없음인 항목은 nil이고, 언급된 항목에는 반드시 있어야 한다.
	SealEvidence SealFunc
	// EvidenceUtteranceID는 근거가 어느 발화에서 왔는지다. 그 대화 안의 발화여야 한다.
	// 모르면 nil로 둔다. 근거 글만 있고 발화를 가리키지 않는 행도 받아 준다.
	EvidenceUtteranceID *uuid.UUID
}

// ConversationAnalysis는 대화 하나의 분석 결과 전부다.
//
// 판단을 목록이 아니라 여덟 자리 배열로 받는다. 목록으로 받으면 언급된 항목만 담긴 결과가 들어올 수 있는데,
// 그러면 별말 없이 지나간 날이 대화한 일수에서 통째로 빠져 점수가 실제보다 높게 나오고 평소도 높게 잡힌다.
// 자리는 signal.Item.Index이고, 채우지 않은 자리는 "언급 없음, 근거 없음"이다.
type ConversationAnalysis struct {
	UserID uuid.UUID
	// DayID와 ConversationID는 서로 맞아야 한다. 어긋나면 복합 외래 키에 걸린다.
	DayID          uuid.UUID
	ConversationID uuid.UUID
	Judgements     [signal.ItemCount]SignalJudgement
	// ExtractorVersion은 어떤 지시문과 모델로 뽑았는지다. 추출 방식을 바꾼 앞뒤를 견주는 데 쓴다. 빈 값일 수 없다.
	ExtractorVersion string
	Now              time.Time
}

// validate는 저장하기 전에 여덟 항목이 모두 앞뒤가 맞는지 본다.
// 오류 메시지에는 항목 이름만 담는다. 사용자의 글은 어느 메시지에도 들어가지 않는다.
func (a ConversationAnalysis) validate() error {
	switch {
	case a.UserID == uuid.Nil || a.DayID == uuid.Nil || a.ConversationID == uuid.Nil:
		return errors.New("store: conversation analysis needs user, day and conversation ids")
	case a.ExtractorVersion == "":
		return errors.New("store: conversation analysis needs an extractor version")
	}
	for _, item := range signal.AllItems() {
		judgement := a.Judgements[item.Index()]
		if err := judgement.Judgement.Validate(); err != nil {
			return fmt.Errorf("store: signal %s: %w", item, err)
		}
		if judgement.Judgement.Status.Mentioned() != (judgement.SealEvidence != nil) {
			return fmt.Errorf("%w: %s", ErrEvidenceMismatch, item)
		}
		if judgement.SealEvidence == nil && judgement.EvidenceUtteranceID != nil {
			return fmt.Errorf("%w: %s points at an utterance without evidence", ErrEvidenceMismatch, item)
		}
	}
	return nil
}

// SaveConversationSignals는 대화 하나의 분석 결과를 신호 행 여덟 개로 저장하고 그 대화의 분석을 done으로 닫는다.
//
// 여덟 행과 done은 한 트랜잭션에서 함께 들어가거나 하나도 들어가지 않는다. 그래서 계산 코어는 일부 항목만 본 하루를
// 받을 수 없고, "분석이 끝난 대화"는 언제나 여덟 항목이 갖춰진 대화다.
//
//   - 대화가 없거나 남의 대화이거나 아직 열려 있으면 ErrNotFound
//   - 그 대화의 신호 행이 이미 있으면 ErrSignalsAlreadySaved. 먼저 들어간 행은 그대로 남는다
//   - 판단과 근거가 맞지 않으면 ErrEvidenceMismatch
func (s *Store) SaveConversationSignals(ctx context.Context, in ConversationAnalysis) error {
	return s.InTx(ctx, func(q *db.Queries) error {
		return SaveConversationSignals(ctx, q, in)
	})
}

// SaveConversationSignals는 Store.SaveConversationSignals와 같은 일을, 부르는 쪽이 이미 연 트랜잭션 안에서 한다.
// 기억 추출처럼 같은 대화에서 나온 다른 결과와 한데 묶을 때 쓴다.
//
// q는 Store.InTx가 넘겨준 것이어야 한다. 트랜잭션 밖의 쿼리를 넘기면 여덟 행이 낱개로 들어가서,
// 도중에 실패한 대화가 "일부 항목만 있는 분석이 끝난 대화"로 남는다.
func SaveConversationSignals(ctx context.Context, q *db.Queries, in ConversationAnalysis) error {
	if err := in.validate(); err != nil {
		return err
	}

	// 먼저 대화의 행을 잡는다. 같은 대화를 동시에 저장하려는 트랜잭션들이 여기서 한 줄로 서고,
	// 뒤에 선 쪽은 앞선 쪽이 넣은 행을 보고 유일 제약에 걸린다.
	if _, err := q.SetConversationAnalysisStatus(ctx, db.SetConversationAnalysisStatusParams{
		AnalysisStatus: AnalysisDone, ID: in.ConversationID, UserID: in.UserID,
	}); err != nil {
		return fmt.Errorf("close conversation analysis: %w", err)
	}

	for _, item := range signal.AllItems() {
		judgement := in.Judgements[item.Index()]
		id, err := NewID()
		if err != nil {
			return fmt.Errorf("new signal id: %w", err)
		}
		var evidence []byte
		if judgement.SealEvidence != nil {
			evidence, err = sealEvidence(judgement.SealEvidence, id)
			if err != nil {
				return err
			}
		}
		if _, err := q.InsertSignal(ctx, db.InsertSignalParams{
			ID:                  id,
			UserID:              in.UserID,
			DayID:               in.DayID,
			ConversationID:      in.ConversationID,
			Item:                item.String(),
			Status:              judgement.Judgement.Status.String(),
			Explicitness:        judgement.Judgement.Explicitness.String(),
			EvidenceEnc:         evidence,
			EvidenceUtteranceID: judgement.EvidenceUtteranceID,
			ExtractorVersion:    in.ExtractorVersion,
			Now:                 in.Now,
		}); err != nil {
			if errors.Is(err, ErrConflict) && ConstraintName(err) == constraintSignalConversationItem {
				return ErrSignalsAlreadySaved
			}
			return fmt.Errorf("insert signal: %w", err)
		}
	}
	return nil
}

func sealEvidence(fn SealFunc, rowID uuid.UUID) ([]byte, error) {
	sealed, err := fn(rowID)
	if err != nil {
		return nil, fmt.Errorf("seal signal evidence: %w", err)
	}
	if len(sealed) == 0 {
		// 빈 글도 잠그면 길이가 있는 암호문이 된다. 빈 값이 왔다면 잠그지 않은 것이다.
		return nil, errors.New("store: seal function returned no ciphertext")
	}
	return sealed, nil
}

// ParseSignalJudgement는 저장된 식별자를 계산 코어의 값으로 되돌린다.
//
// 오류 메시지에는 읽은 문자열을 담지 않는다. 항목 자리에 다른 글이 들어와도 로그로 새지 않는다.
// 판단과 명시성이 서로 맞는지도 함께 본다. CHECK 제약이 막고 있지만, 제약이 없는 길로 들어온 행이
// 조용히 계산에 섞이면 직접 언급의 비율이 틀어진다.
func ParseSignalJudgement(item, status, explicitness string) (signal.Item, signal.Judgement, error) {
	parsedItem, err := signal.ParseItem(item)
	if err != nil {
		return 0, signal.Judgement{}, fmt.Errorf("store: signal row: %w", err)
	}
	parsedStatus, err := signal.ParseStatus(status)
	if err != nil {
		return 0, signal.Judgement{}, fmt.Errorf("store: signal row: %w", err)
	}
	parsedExplicitness, err := signal.ParseExplicitness(explicitness)
	if err != nil {
		return 0, signal.Judgement{}, fmt.Errorf("store: signal row: %w", err)
	}
	judgement := signal.Judgement{Status: parsedStatus, Explicitness: parsedExplicitness}
	if err := judgement.Validate(); err != nil {
		return 0, signal.Judgement{}, fmt.Errorf("store: signal row: %w", err)
	}
	return parsedItem, judgement, nil
}

// SignalDaysByUser는 ListSignalRowsByUser의 결과를 계산 코어(assess.EvaluateRows)에 바로 넘길 꼴로 모은다.
//
// 취소된 행도 그대로 담는다. 코어가 하루로 합칠 때 스스로 뺀다. 여기서 빼면 행이 모두 취소된 날이
// 대화하지 않은 날로 바뀌어 대화한 일수가 줄어든다.
func SignalDaysByUser(rows []db.ListSignalRowsByUserRow) (map[recorddate.Date][]signal.Row, error) {
	byDate := make(map[recorddate.Date][]signal.Row, len(rows))
	for _, row := range rows {
		date, parsed, err := parseSignalRow(row.RecordDate, row.ConversationID, row.Item, row.Status, row.Explicitness, row.Cancelled)
		if err != nil {
			return nil, err
		}
		byDate[date] = append(byDate[date], parsed)
	}
	return byDate, nil
}

// SignalDaysInRange는 ListSignalRowsByDateRange의 결과를 계산 코어에 넘길 꼴로 모은다.
//
// 기간을 자른 목록으로는 개입 단계와 개인 기준선을 구할 수 없다. 둘은 첫 대화 날부터 다시 돌려야 하므로
// SignalDaysByUser를 쓴다. 이 목록은 그 기간의 점 달력과 근거를 보여주는 데 쓴다.
func SignalDaysInRange(rows []db.ListSignalRowsByDateRangeRow) (map[recorddate.Date][]signal.Row, error) {
	byDate := make(map[recorddate.Date][]signal.Row, len(rows))
	for _, row := range rows {
		date, parsed, err := parseSignalRow(row.RecordDate, row.ConversationID, row.Item, row.Status, row.Explicitness, row.Cancelled)
		if err != nil {
			return nil, err
		}
		byDate[date] = append(byDate[date], parsed)
	}
	return byDate, nil
}

func parseSignalRow(
	on pgtype.Date, conversationID uuid.UUID, item, status, explicitness string, cancelled bool,
) (recorddate.Date, signal.Row, error) {
	date, err := RecordDate(on)
	if err != nil {
		return recorddate.Date{}, signal.Row{}, fmt.Errorf("signal row: %w", err)
	}
	parsedItem, judgement, err := ParseSignalJudgement(item, status, explicitness)
	if err != nil {
		return recorddate.Date{}, signal.Row{}, err
	}
	return date, signal.Row{
		ConversationID: conversationID.String(),
		Item:           parsedItem,
		Status:         judgement.Status,
		Explicitness:   judgement.Explicitness,
		Cancelled:      cancelled,
	}, nil
}
