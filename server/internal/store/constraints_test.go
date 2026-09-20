package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// tryExec는 문장 하나를 트랜잭션 안에서 돌려 보고 되돌린다. 받아들여지는 경우에도 다음 경우에 흔적을 남기지 않는다.
func tryExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), sql, args...)
	return err
}

func TestSignalEvidence(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	day := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))
	utterance := day.utteranceIDs[0]

	const insert = `
		INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, evidence_enc, evidence_utterance_id, extractor_version, created_at)
		VALUES ($1, $2, $3, $4, 'fatigue', $5, $6, $7, $8, 'test-1', $9)`

	tests := []struct {
		name         string
		status       string
		explicitness string
		evidence     []byte
		utteranceID  *uuid.UUID
		rejected     bool
	}{
		{"관찰됨에 근거와 직접 언급이 있으면 받는다", "observed", "direct", fakeCiphertext, &utterance, false},
		{"관찰되지 않음에 근거와 간접 추론이 있으면 받는다", "not_observed", "indirect", fakeCiphertext, &utterance, false},
		{"근거 글만 있고 가리키는 발화가 없어도 받는다", "observed", "direct", fakeCiphertext, nil, false},
		{"언급 없음은 근거도 명시성도 없이 받는다", "not_mentioned", "none", nil, nil, false},

		{"관찰됨인데 근거가 없으면 거부한다", "observed", "direct", nil, nil, true},
		{"관찰됨인데 가리키는 발화만 있고 근거 글이 없으면 거부한다", "observed", "direct", nil, &utterance, true},
		{"관찰되지 않음인데 근거가 없으면 거부한다", "not_observed", "indirect", nil, nil, true},
		{"관찰됨인데 명시성이 none이면 거부한다", "observed", "none", fakeCiphertext, &utterance, true},
		{"언급 없음에 근거 글이 있으면 거부한다", "not_mentioned", "none", fakeCiphertext, nil, true},
		{"언급 없음에 가리키는 발화가 있으면 거부한다", "not_mentioned", "none", nil, &utterance, true},
		{"언급 없음인데 명시성이 있으면 거부한다", "not_mentioned", "direct", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tryExec(t, pool, insert,
				newID(t), mina.ID, day.dayID, day.conversationID, tt.status, tt.explicitness, tt.evidence, tt.utteranceID, baseTime)
			if tt.rejected {
				requireViolation(t, err, sqlStateCheckViolation, "signals_evidence_check")
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("근거 발화가 사라지면 가리키는 값만 비고 신호와 근거 글은 남는다", func(t *testing.T) {
		ctx := t.Context()
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		_, err = tx.Exec(ctx, `DELETE FROM utterances WHERE id = $1`, utterance)
		require.NoError(t, err)

		var pointer *uuid.UUID
		var evidence []byte
		var conversationID uuid.UUID
		err = tx.QueryRow(ctx,
			`SELECT evidence_utterance_id, evidence_enc, conversation_id FROM signals WHERE conversation_id = $1 AND item = 'sleep'`,
			day.conversationID).Scan(&pointer, &evidence, &conversationID)
		require.NoError(t, err)
		assert.Nil(t, pointer)
		assert.Equal(t, fakeCiphertext, evidence)
		assert.Equal(t, day.conversationID, conversationID, "복합 외래 키의 다른 컬럼까지 비우면 안 된다")
	})

	t.Run("자해나 자살에 관한 항목은 신호로 쌓을 수 없다", func(t *testing.T) {
		err := tryExec(t, pool, `
			INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, extractor_version, created_at)
			VALUES ($1, $2, $3, $4, 'self_harm', 'not_mentioned', 'none', 'test-1', $5)`,
			newID(t), mina.ID, day.dayID, day.conversationID, baseTime)
		requireViolation(t, err, sqlStateCheckViolation, "signals_item_check")
	})

	t.Run("한 대화에서 같은 항목은 한 번만 기록한다", func(t *testing.T) {
		// seedDay가 이미 sleep 신호를 넣어 두었다.
		err := tryExec(t, pool, `
			INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, extractor_version, created_at)
			VALUES ($1, $2, $3, $4, 'sleep', 'not_mentioned', 'none', 'test-1', $5)`,
			newID(t), mina.ID, day.dayID, day.conversationID, baseTime)
		requireViolation(t, err, sqlStateUniqueViolation, "signals_conversation_id_item_key")
	})
}

// 자식 행의 user_id, day_id, conversation_id는 부모의 값과 어긋날 수 없다.
func TestOwnership(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	joon := createUser(t, st, "joon@example.com")
	minaDay := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))
	minaOtherDay := seedDay(t, st, pool, mina.ID, date(2026, time.September, 21))
	joonDay := seedDay(t, st, pool, joon.ID, date(2026, time.September, 20))
	// 일기는 하루에 하나라, 아직 일기가 없는 날이어야 유일 제약이 아니라 소유자 확인에 걸린다.
	joonBlankDayID, err := st.Queries().UpsertDay(t.Context(), db.UpsertDayParams{
		ID: newID(t), UserID: joon.ID, RecordDate: date(2026, time.September, 21), Now: baseTime,
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		constraint string
		sql        string
		args       []any
	}{
		{
			"남의 하루에 대화를 매달 수 없다",
			"conversations_day_id_user_id_fkey",
			`INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, created_at)
			 VALUES ($1, $2, $3, 'active', 'chat', $4, $4)`,
			[]any{newID(t), joon.ID, minaDay.dayID, baseTime},
		},
		{
			"남의 대화에 발화를 매달 수 없다",
			"utterances_conversation_id_user_id_fkey",
			`INSERT INTO utterances (id, conversation_id, user_id, seq, speaker, modality, origin, text_enc, created_at)
			 VALUES ($1, $2, $3, 99, 'user', 'chat', 'user', $4, $5)`,
			[]any{newID(t), minaDay.conversationID, joon.ID, fakeCiphertext, baseTime},
		},
		{
			"남의 대화에 위기 관문 기록을 매달 수 없다",
			"gate_events_conversation_id_user_id_fkey",
			`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, final_stage, detected_by, created_at)
			 VALUES ($1, $2, $3, $4, 0, 'none', $5)`,
			[]any{newID(t), joon.ID, minaDay.conversationID, minaDay.utteranceIDs[1], baseTime},
		},
		{
			"위기 관문 기록의 발화는 같은 대화의 것이어야 한다",
			"gate_events_utterance_id_conversation_id_fkey",
			`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, final_stage, detected_by, created_at)
			 VALUES ($1, $2, $3, $4, 0, 'none', $5)`,
			[]any{newID(t), mina.ID, minaDay.conversationID, minaOtherDay.utteranceIDs[1], baseTime},
		},
		{
			"남의 하루에 일기를 매달 수 없다",
			"diaries_day_id_user_id_fkey",
			`INSERT INTO diaries (id, day_id, user_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'draft', $4, $4)`,
			[]any{newID(t), joonBlankDayID, mina.ID, baseTime},
		},
		{
			"남의 대화에 신호를 매달 수 없다",
			"signals_conversation_id_user_id_fkey",
			`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, extractor_version, created_at)
			 VALUES ($1, $2, $3, $4, 'appetite', 'not_mentioned', 'none', 'test-1', $5)`,
			[]any{newID(t), joon.ID, minaDay.dayID, minaDay.conversationID, baseTime},
		},
		{
			"신호는 그 대화가 속한 날에만 매달 수 있다",
			"signals_conversation_id_day_id_fkey",
			`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, extractor_version, created_at)
			 VALUES ($1, $2, $3, $4, 'appetite', 'not_mentioned', 'none', 'test-1', $5)`,
			[]any{newID(t), mina.ID, minaOtherDay.dayID, minaDay.conversationID, baseTime},
		},
		{
			"신호의 근거는 같은 대화의 발화여야 한다",
			"signals_evidence_utterance_id_conversation_id_fkey",
			`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, evidence_enc, evidence_utterance_id, extractor_version, created_at)
			 VALUES ($1, $2, $3, $4, 'appetite', 'observed', 'direct', $5, $6, 'test-1', $7)`,
			[]any{newID(t), mina.ID, minaDay.dayID, minaDay.conversationID, fakeCiphertext, minaOtherDay.utteranceIDs[0], baseTime},
		},
		{
			"남의 하루에서 기억을 만들 수 없다",
			"memories_source_day_id_user_id_fkey",
			`INSERT INTO memories (id, user_id, source_day_id, kind, content_enc, created_at) VALUES ($1, $2, $3, 'person', $4, $5)`,
			[]any{newID(t), mina.ID, joonDay.dayID, fakeCiphertext, baseTime},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireViolation(t, tryExec(t, pool, tt.sql, tt.args...), sqlStateForeignKeyViolation, tt.constraint)
		})
	}
}

func TestRecordChecks(t *testing.T) {
	t.Parallel()
	st, pool := newStore(t)
	mina := createUser(t, st, "mina@example.com")
	day := seedDay(t, st, pool, mina.ID, date(2026, time.September, 20))

	tests := []struct {
		name       string
		sqlState   string
		constraint string
		sql        string
		args       []any
	}{
		{
			"진행 중인 대화에는 끝난 시각이 있을 수 없다",
			sqlStateCheckViolation, "conversations_ended_check",
			`INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, ended_at, created_at)
			 VALUES ($1, $2, $3, 'active', 'chat', $4, $4, $4)`,
			[]any{newID(t), mina.ID, day.dayID, baseTime},
		},
		{
			"끝난 대화에는 끝난 시각이 있어야 한다",
			sqlStateCheckViolation, "conversations_ended_check",
			`INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, created_at)
			 VALUES ($1, $2, $3, 'ended', 'chat', $4, $4)`,
			[]any{newID(t), mina.ID, day.dayID, baseTime},
		},
		{
			"사용자의 발화에 AI가 만든 말이라는 표시를 붙일 수 없다",
			sqlStateCheckViolation, "utterances_speaker_origin_check",
			`INSERT INTO utterances (id, conversation_id, user_id, seq, speaker, modality, origin, text_enc, created_at)
			 VALUES ($1, $2, $3, 99, 'user', 'chat', 'model', $4, $5)`,
			[]any{newID(t), day.conversationID, mina.ID, fakeCiphertext, baseTime},
		},
		{
			"한 대화 안에서 발화 순서는 겹칠 수 없다",
			sqlStateUniqueViolation, "utterances_conversation_id_seq_key",
			`INSERT INTO utterances (id, conversation_id, user_id, seq, speaker, modality, origin, text_enc, created_at)
			 VALUES ($1, $2, $3, 0, 'user', 'chat', 'user', $4, $5)`,
			[]any{newID(t), day.conversationID, mina.ID, fakeCiphertext, baseTime},
		},
		{
			"한 발화의 위기 관문 판정은 한 번만 남는다",
			sqlStateUniqueViolation, "gate_events_utterance_id_key",
			`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, final_stage, detected_by, created_at)
			 VALUES ($1, $2, $3, $4, 1, 'ai', $5)`,
			[]any{newID(t), mina.ID, day.conversationID, day.utteranceIDs[0], baseTime},
		},
		{
			"위기 관문의 단계는 0에서 3까지다",
			sqlStateCheckViolation, "gate_events_final_stage_check",
			`INSERT INTO gate_events (id, user_id, conversation_id, utterance_id, final_stage, detected_by, created_at)
			 VALUES ($1, $2, $3, $4, 4, 'ai', $5)`,
			[]any{newID(t), mina.ID, day.conversationID, day.utteranceIDs[1], baseTime},
		},
		{
			"일기는 하루에 하나다",
			sqlStateUniqueViolation, "diaries_day_id_key",
			`INSERT INTO diaries (id, day_id, user_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'draft', $4, $4)`,
			[]any{newID(t), day.dayID, mina.ID, baseTime},
		},
		{
			"확인한 일기에는 글과 확인한 시각이 있어야 한다",
			sqlStateCheckViolation, "diaries_confirmed_check",
			`UPDATE diaries SET status = 'confirmed' WHERE day_id = $1`,
			[]any{day.dayID},
		},
		// 기분 값의 범위(1에서 5)는 여기에 없다. 값이 암호문으로 들어오므로 DB는 범위를 볼 수 없고, 애플리케이션이 확인한다.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireViolation(t, tryExec(t, pool, tt.sql, tt.args...), tt.sqlState, tt.constraint)
		})
	}
}
