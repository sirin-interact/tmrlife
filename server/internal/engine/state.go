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
			slog.Any("conversation", s), slog.String("error", err.Error()))
		return crisis.State{}
	}
	if len(rows) == 0 {
		return crisis.State{}
	}

	byDate := make(map[recorddate.Date][]signal.Row, len(rows))
	for _, row := range rows {
		date, err := store.RecordDate(row.RecordDate)
		if err != nil {
			e.logger.LogAttrs(ctx, slog.LevelError, "signal row has an unreadable record date",
				slog.Any("conversation", s), slog.String("error", err.Error()))
			return crisis.State{}
		}
		item, itemErr := signal.ParseItem(row.Item)
		status, statusErr := signal.ParseStatus(row.Status)
		explicitness, explicitnessErr := signal.ParseExplicitness(row.Explicitness)
		if itemErr != nil || statusErr != nil || explicitnessErr != nil {
			e.logger.LogAttrs(ctx, slog.LevelError, "signal row cannot be read",
				slog.Any("conversation", s), slog.String("record_date", date.String()))
			return crisis.State{}
		}
		byDate[date] = append(byDate[date], signal.Row{
			ConversationID: row.ConversationID.String(),
			Item:           item,
			Status:         status,
			Explicitness:   explicitness,
			Cancelled:      row.Cancelled,
		})
	}

	days, err := signal.MergeDays(byDate)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "signal days cannot be merged",
			slog.Any("conversation", s), slog.String("error", err.Error()))
		return crisis.State{}
	}
	evaluation, err := assess.EvaluateLive(days, today, e.params)
	if err != nil {
		e.logger.LogAttrs(ctx, slog.LevelError, "live evaluation failed",
			slog.Any("conversation", s), slog.String("error", err.Error()))
		return crisis.State{}
	}
	return evaluation.GateState()
}
