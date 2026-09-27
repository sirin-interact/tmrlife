package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// gateState는 발화를 둘러싼 그 사람의 최근 상태를 읽는다. 상태가 나쁜 동안에는 같은 말도 더 무겁게 듣는다.
//
// 대화가 진행 중이므로 EvaluateLive로 기준일을 고른다. 아직 열려 있는 오늘을 기준일로 주면
// 창에서 하루가 밀려나 관찰된 일수가 하나 줄어든다.
//
// 무엇 하나라도 읽지 못하면 빈 상태를 돌려준다. 관문은 그래도 평소대로 돌아야 한다.
// 신호가 한 줄도 없는 동안(아직 분석을 붙이지 않은 단계)에도 빈 상태가 나온다.
func (e *Engine) gateState(ctx context.Context, s *Session, now time.Time) crisis.State {
	today := recorddate.Of(now, s.loc)
	if today.IsZero() {
		return crisis.State{}
	}

	rows, err := e.store.Queries().ListSignalRowsByUser(ctx, s.user.ID)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "signal rows cannot be read",
			slog.Any("conversation", s), slog.String("failure", failureName(err)))
		return crisis.State{}
	}
	if len(rows) == 0 {
		return crisis.State{}
	}

	// 저장된 행을 계산 코어의 행으로 옮기는 일은 저장소 쪽 한 곳에만 둔다.
	// 마음 신호 경로(internal/api)가 같은 쿼리에 같은 함수를 쓴다. 관문이 제 몫을 따로 들고 있으면
	// 행을 읽는 규칙이 바뀔 때 한쪽만 고쳐도 아무 시험이 실패하지 않는다.
	byDate, err := store.SignalDaysByUser(rows)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "signal row cannot be read",
			slog.Any("conversation", s), slog.String("failure", failureName(err)))
		return crisis.State{}
	}

	days, err := signal.MergeDays(byDate)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "signal days cannot be merged",
			slog.Any("conversation", s), slog.String("failure", failureName(err)))
		return crisis.State{}
	}
	evaluation, err := assess.EvaluateLive(days, today, e.params)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "live evaluation failed",
			slog.Any("conversation", s), slog.String("failure", failureName(err)))
		return crisis.State{}
	}
	return evaluation.GateState()
}
