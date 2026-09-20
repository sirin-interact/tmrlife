package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 상태가 나쁜 동안에는 같은 말도 더 무겁게 듣는다. 그 상태는 신호 행에서 온다.
// 이 시험은 신호 행을 읽어 코어의 평가로 옮기는 길 전체를 본다. 신호를 쌓는 일은 아직 없어서 행을 직접 넣는다.
func TestGateStateRaisesTheStage(t *testing.T) {
	t.Parallel()

	t.Run("추정 점수가 기준을 넘으면 확인 단계가 대응 단계가 된다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.fillSignals(14)

		s := f.start()
		turn := f.turn(s, sayVague, crisis.StageCheck, sayVague, "")

		assert.Equal(t, crisis.StageRespond, turn.Stage)
		event := f.lastGateEvent(s.ConversationID())
		assert.Contains(t, event.Adjustments, crisis.AdjustBadStatePlusOne.String())
	})

	t.Run("기록이 모자라면 올리지 않는다", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		// 대화한 날이 기록 부족 기준보다 적으면 점수를 내지 않는다.
		f.fillSignals(params.Default().Window.MinConversationDays - 1)

		s := f.start()
		turn := f.turn(s, sayVague, crisis.StageCheck, sayVague, replyMirror)

		assert.Equal(t, crisis.StageCheck, turn.Stage)
		assert.Empty(t, f.lastGateEvent(s.ConversationID()).Adjustments)
	})
}

// fillSignals는 지난 days일 동안 여덟 항목이 모두 관찰된 것으로 신호 행을 넣는다.
// 그런 기록의 추정 점수는 관문이 판정을 올리는 기준을 넘는다.
func (f *fixture) fillSignals(days int) {
	f.t.Helper()
	ctx := f.t.Context()
	today := recorddate.Of(f.clock.Now(), mustLocation(f.t, "Asia/Seoul"))

	for i := 1; i <= days; i++ {
		date := today.AddDays(-i)
		recordDate, err := store.PGDate(date)
		require.NoError(f.t, err)

		dayID, conversationID := newID(f.t), newID(f.t)
		at := f.clock.Now().Add(-time.Duration(i) * 24 * time.Hour)
		_, err = f.pool.Exec(ctx,
			`INSERT INTO days (id, user_id, record_date, created_at) VALUES ($1, $2, $3, $4)`,
			dayID, f.userID, recordDate, at)
		require.NoError(f.t, err)
		_, err = f.pool.Exec(ctx,
			`INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, ended_at, end_reason, processing_status, created_at)
			 VALUES ($1, $2, $3, 'ended', 'chat', $4, $4, 'user', 'done', $4)`,
			conversationID, f.userID, dayID, at)
		require.NoError(f.t, err)

		for _, item := range signal.AllItems() {
			// 근거 없는 판단은 저장할 수 없다. 내용은 보지 않으므로 잠근 글 한 줄이면 된다.
			signalID := newID(f.t)
			evidence, err := f.sealer.SealString(sayTired, sealing.SignalEvidence(signalID))
			require.NoError(f.t, err)
			_, err = f.pool.Exec(ctx,
				`INSERT INTO signals (id, user_id, day_id, conversation_id, item, status, explicitness, evidence_enc, extractor_version, created_at)
				 VALUES ($1, $2, $3, $4, $5, 'observed', 'direct', $6, 'test', $7)`,
				signalID, f.userID, dayID, conversationID, item.String(), evidence, at)
			require.NoError(f.t, err)
		}
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}
