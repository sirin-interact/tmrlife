package sealing

import (
	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/crypto"
)

// slot은 암호문이 놓이는 컬럼 하나다.
//
// 이름은 한번 쓰기 시작하면 바꾸지 않는 꼬리표다. 마이그레이션으로 테이블이나 컬럼의 이름을 바꾸더라도 여기의 값은 그대로 둔다.
// 함께 바꾸면 그전에 잠근 글이 모두 열리지 않는다.
type slot struct {
	table  string
	column string
}

func (s slot) at(rowID uuid.UUID) crypto.AAD {
	return crypto.AAD{Table: s.table, Column: s.column, RowID: rowID}
}

var (
	slotUtteranceText   = slot{"utterances", "text_enc"}
	slotGateEvidence    = slot{"gate_events", "evidence_enc"}
	slotDiaryDraft      = slot{"diaries", "draft_enc"}
	slotDiaryBody       = slot{"diaries", "body_enc"}
	slotSignalEvidence  = slot{"signals", "evidence_enc"}
	slotMemoryContent   = slot{"memories", "content_enc"}
	slotMoodPickValue   = slot{"mood_picks", "value_enc"}
	slotSelfCheckResult = slot{"self_checks", "result_enc"}
)

// Columns는 자리가 정해진 컬럼을 "테이블.컬럼" 꼴로 돌려준다. 스키마의 _enc 컬럼과 빠짐없이 맞는지 시험이 견준다.
func Columns() []string {
	slots := []slot{
		slotUtteranceText, slotGateEvidence, slotDiaryDraft, slotDiaryBody,
		slotSignalEvidence, slotMemoryContent, slotMoodPickValue, slotSelfCheckResult,
	}
	columns := make([]string, 0, len(slots))
	for _, s := range slots {
		columns = append(columns, s.table+"."+s.column)
	}
	return columns
}

// UtteranceText는 발화의 글(utterances.text_enc)이 놓이는 자리다.
func UtteranceText(utteranceID uuid.UUID) crypto.AAD { return slotUtteranceText.at(utteranceID) }

// GateEvidence는 위기 관문의 근거 발화(gate_events.evidence_enc)가 놓이는 자리다.
func GateEvidence(gateEventID uuid.UUID) crypto.AAD { return slotGateEvidence.at(gateEventID) }

// DiaryDraft는 일기 초안(diaries.draft_enc)이 놓이는 자리다.
// 초안과 확인한 글은 같은 행에 있지만 자리가 다르다. 초안의 암호문을 확인한 글의 컬럼으로 옮겨 놓아도 열리지 않는다.
func DiaryDraft(diaryID uuid.UUID) crypto.AAD { return slotDiaryDraft.at(diaryID) }

// DiaryBody는 사용자가 확인한 일기 글(diaries.body_enc)이 놓이는 자리다.
func DiaryBody(diaryID uuid.UUID) crypto.AAD { return slotDiaryBody.at(diaryID) }

// SignalEvidence는 신호의 근거 발화(signals.evidence_enc)가 놓이는 자리다.
func SignalEvidence(signalID uuid.UUID) crypto.AAD { return slotSignalEvidence.at(signalID) }

// MemoryContent는 기억의 내용(memories.content_enc)이 놓이는 자리다.
func MemoryContent(memoryID uuid.UUID) crypto.AAD { return slotMemoryContent.at(memoryID) }

// MoodPickValue는 직접 고른 기분 값(mood_picks.value_enc)이 놓이는 자리다.
// 그 테이블에는 id가 없고 하루에 한 행이라서 하루의 ID에 묶는다.
func MoodPickValue(dayID uuid.UUID) crypto.AAD { return slotMoodPickValue.at(dayID) }

// SelfCheckResult는 자가 검진의 응답과 결과(self_checks.result_enc)가 놓이는 자리다.
func SelfCheckResult(selfCheckID uuid.UUID) crypto.AAD { return slotSelfCheckResult.at(selfCheckID) }
