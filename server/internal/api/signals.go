package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sirin-interact/tmrlife/server/internal/auth"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/crypto"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/sealing"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// signalService는 마음 신호 경로가 저장소와 계산 코어를 다루는 자리다.
//
// 핸들러는 요청을 옮기고 응답을 만들 뿐이고, 숫자는 모두 계산 코어(assess)가 만든 값을 그대로 옮긴다.
// 여기서 세거나 나누는 곳은 없다. 화면에 보이는 숫자와 위기 관문이 판단에 쓴 숫자가 어긋나지 않게 하려는 것이다.
type signalService struct {
	store   *store.Store
	sealers *sealing.Sealers
	clock   clock.Clock
	logger  *slog.Logger
	// params는 이 서버가 계산에 쓰는 조정 값이다. 응답의 params가 이 값이고, 화면은 경계를 코드에 박아 두지 않는다.
	params params.Params
}

// errSignalNotFound는 그 신호가 없다는 뜻이다. 남의 신호도 이것으로 답한다.
var errSignalNotFound = newProblem(http.StatusNotFound, ProblemCodeNotFound)

// errReviewForbidden은 내부 확인 화면을 볼 수 없는 계정이라는 뜻이다.
//
// 명세로는 막을 수 없는 조건("시연 계정이거나 관리자")이라 여기서 가린다.
// 추정 점수와 개입 단계를 사용자에게 숫자로 보여주지 않는 것은 화면의 선택이 아니라 서버가 지키는 규칙이다.
var errReviewForbidden = newProblem(http.StatusForbidden, ProblemCodeForbidden)

// GetTrend는 추세 화면이 그대로 그릴 점 달력을 돌려준다.
func (h *handlers) GetTrend(ctx context.Context, _ GetTrendRequestObject) (GetTrendResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	evaluation, err := h.signals.evaluate(ctx, principal.User)
	if err != nil {
		return nil, err
	}
	trend, err := trendResponse(evaluation)
	if err != nil {
		return nil, err
	}
	return GetTrend200JSONResponse(trend), nil
}

// GetDaySignals는 그날 여덟 항목을 어떻게 보았는지와 그 근거가 된 사용자의 말을 돌려준다.
// 대화하지 않은 날도 200이다. 그날 분석이 끝난 대화가 있는지는 analysed로 알린다.
func (h *handlers) GetDaySignals(ctx context.Context, request GetDaySignalsRequestObject) (GetDaySignalsResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	date, err := recordDateOf(request.Date)
	if err != nil {
		return nil, err
	}
	day, err := h.signals.day(ctx, principal.User.ID, date)
	if err != nil {
		return nil, err
	}
	return GetDaySignals200JSONResponse(day), nil
}

// CancelSignal은 그 판단을 계산에서 뺀다. 행은 지우지 않는다.
//
// 뺀 뒤에 무엇도 미리 계산해 두지 않는다. 추세와 추정은 읽을 때마다 남은 기록으로 다시 계산되므로,
// 취소가 곧 다시 계산이다. 미리 계산해 저장하면 취소와 그 결과가 어긋날 자리가 생긴다.
func (h *handlers) CancelSignal(ctx context.Context, request CancelSignalRequestObject) (CancelSignalResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	day, err := h.signals.setCancelled(ctx, principal.User.ID, request.SignalID, true)
	if err != nil {
		return nil, err
	}
	return CancelSignal200JSONResponse(day), nil
}

// UncancelSignal은 뺀 판단을 다시 계산에 넣는다. 뺀 적이 없는 신호에 불러도 성공한다.
func (h *handlers) UncancelSignal(ctx context.Context, request UncancelSignalRequestObject) (UncancelSignalResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	day, err := h.signals.setCancelled(ctx, principal.User.ID, request.SignalID, false)
	if err != nil {
		return nil, err
	}
	return UncancelSignal200JSONResponse(day), nil
}

// GetInternalReview는 계산이 그 값에 이른 과정 전부를 돌려준다. 시연 계정이나 관리자가 아니면 403이다.
func (h *handlers) GetInternalReview(ctx context.Context, _ GetInternalReviewRequestObject) (GetInternalReviewResponseObject, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	if !principal.User.IsDemo && !principal.User.IsAdmin() {
		return nil, errReviewForbidden
	}
	evaluation, err := h.signals.evaluate(ctx, principal.User)
	if err != nil {
		return nil, err
	}
	extractors, err := h.signals.extractors(ctx, principal.User.ID)
	if err != nil {
		return nil, err
	}
	review, err := reviewResponse(evaluation, extractors)
	if err != nil {
		return nil, err
	}
	return GetInternalReview200JSONResponse(review), nil
}

// extractors는 그 계정의 신호 행을 남긴 추출기들을 읽는다.
//
// 판단과 함께 읽지 않고 따로 읽는다. 추세 화면은 이 값이 필요하지 않고, 세는 일은 데이터베이스가 하는 것이
// 신호 행 전부를 다시 훑는 것보다 싸다.
func (s *signalService) extractors(ctx context.Context, userID uuid.UUID) ([]ReviewExtractor, error) {
	rows, err := s.store.Queries().ListSignalExtractors(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list signal extractors: %w", err)
	}
	out := make([]ReviewExtractor, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReviewExtractor{
			Version: row.ExtractorVersion,
			Rows:    int(row.RowCount),
			LastAt:  row.LastAt,
		})
	}
	return out, nil
}

// evaluate는 그 사용자의 신호 행 전부를 읽어 계산 코어의 평가를 구한다.
//
// 기간을 잘라 읽지 않는다. 개인 기준선과 개입 단계는 첫 대화 날부터 다시 돌려야 나오는 값이고,
// 추세 화면의 "평소와 견준 결과"도 그 기준선에서 온다. 창으로 자른 목록으로는 둘 다 구할 수 없다.
//
// 기준일은 EvaluateLive가 고른다. 오늘의 분석이 아직 없으면 어제가 기준일이다.
// 오늘을 그대로 기준일로 넣으면 창의 가장 오래된 하루가 빠지고 그 자리에 빈 오늘이 들어와,
// 대화를 막 끝낸 사람의 화면이 분석이 끝나기 전까지만 기록 부족으로 보인다.
func (s *signalService) evaluate(ctx context.Context, user auth.User) (assess.Evaluation, error) {
	today := recorddate.Of(s.clock.Now(), s.location(ctx, user))
	if today.IsZero() {
		return assess.Evaluation{}, errors.New("api: cannot resolve the record date")
	}

	rows, err := s.store.Queries().ListSignalRowsByUser(ctx, user.ID)
	if err != nil {
		return assess.Evaluation{}, fmt.Errorf("list signal rows: %w", err)
	}
	byDate, err := store.SignalDaysByUser(rows)
	if err != nil {
		return assess.Evaluation{}, fmt.Errorf("read signal rows: %w", err)
	}
	days, err := signal.MergeDays(byDate)
	if err != nil {
		return assess.Evaluation{}, fmt.Errorf("merge signal days: %w", err)
	}
	evaluation, err := assess.EvaluateLive(days, today, s.params)
	if err != nil {
		return assess.Evaluation{}, fmt.Errorf("evaluate signals: %w", err)
	}
	return evaluation, nil
}

// day는 하루의 신호와 근거를 돌려준다. 그날 분석이 끝난 대화가 없으면 analysed가 거짓이고 items는 비어 있다.
func (s *signalService) day(ctx context.Context, userID uuid.UUID, date recorddate.Date) (DaySignals, error) {
	on, err := pgDate(date)
	if err != nil {
		return DaySignals{}, err
	}
	out := DaySignals{Date: apiDate(date), Items: []DaySignalItem{}}

	q := s.store.Queries()
	analysed, err := q.HasAnalysedConversationOnDate(ctx, db.HasAnalysedConversationOnDateParams{
		UserID: userID, RecordDate: on,
	})
	if err != nil {
		return DaySignals{}, fmt.Errorf("check analysed conversation: %w", err)
	}
	if !analysed {
		return out, nil
	}

	rows, err := q.ListSignalsByDate(ctx, db.ListSignalsByDateParams{UserID: userID, RecordDate: on})
	if err != nil {
		return DaySignals{}, fmt.Errorf("list day signals: %w", err)
	}
	if len(rows) == 0 {
		// 분석이 끝난 대화는 여덟 항목의 행과 함께 닫히므로 여기까지 오지 않는다.
		// 그래도 행이 없으면 보여줄 것이 없는 날이다. 빈 여덟 항목을 만들어 "말하지 않은 하루"로 꾸미지 않는다.
		return out, nil
	}

	sealer, err := s.sealerFor(ctx, userID)
	if err != nil {
		return DaySignals{}, err
	}

	coreRows := make([]signal.Row, 0, len(rows))
	evidence := make(map[signal.Item][]SignalEvidence, signal.ItemCount)
	for _, row := range rows {
		item, judgement, err := store.ParseSignalJudgement(row.Item, row.Status, row.Explicitness)
		if err != nil {
			return DaySignals{}, fmt.Errorf("read day signals: %w", err)
		}
		coreRows = append(coreRows, signal.Row{
			ConversationID: row.ConversationID.String(),
			Item:           item,
			Status:         judgement.Status,
			Explicitness:   judgement.Explicitness,
			Cancelled:      row.Cancelled,
		})
		one, err := s.evidenceResponse(ctx, sealer, userID, row, judgement)
		if err != nil {
			return DaySignals{}, err
		}
		evidence[item] = append(evidence[item], one)
	}

	// 하루의 판단으로 합치는 일은 계산 코어가 한다. 취소한 행을 빼는 규칙도 거기 한 곳에만 있다.
	merged, err := signal.MergeDay(date, coreRows)
	if err != nil {
		return DaySignals{}, fmt.Errorf("merge day signals: %w", err)
	}

	out.Analysed = true
	for _, item := range signal.AllItems() {
		one, err := dayItemResponse(item, merged.Judgement(item), evidence[item])
		if err != nil {
			return DaySignals{}, err
		}
		out.Items = append(out.Items, one)
	}
	return out, nil
}

// setCancelled는 신호 하나를 계산에서 빼거나 되돌리고, 그 신호가 속한 하루를 다시 읽어 돌려준다.
//
// 쿼리가 그 행의 기록 날짜를 함께 돌려주므로 하루를 찾으려고 다시 묻지 않는다.
// 바뀐 결과를 항목의 합친 판단까지 담아 돌려주기 때문에 화면도 두 번 읽지 않는다.
func (s *signalService) setCancelled(
	ctx context.Context, userID, signalID uuid.UUID, cancelled bool,
) (DaySignals, error) {
	var (
		on  pgtype.Date
		err error
	)
	q := s.store.Queries()
	if cancelled {
		on, err = q.CancelSignal(ctx, db.CancelSignalParams{Now: s.clock.Now(), ID: signalID, UserID: userID})
	} else {
		on, err = q.UncancelSignal(ctx, db.UncancelSignalParams{ID: signalID, UserID: userID})
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		// 없는 신호이거나 남의 신호다. 둘을 가리지 않는다.
		return DaySignals{}, errSignalNotFound
	case err != nil:
		return DaySignals{}, fmt.Errorf("change signal: %w", err)
	}

	date, err := store.RecordDate(on)
	if err != nil {
		return DaySignals{}, fmt.Errorf("read record date: %w", err)
	}
	return s.day(ctx, userID, date)
}

// evidenceResponse는 신호 행 하나를 응답에 실을 꼴로 옮긴다.
func (s *signalService) evidenceResponse(
	ctx context.Context, sealer *crypto.Sealer, userID uuid.UUID,
	row db.ListSignalsByDateRow, judgement signal.Judgement,
) (SignalEvidence, error) {
	status := SignalStatus(judgement.Status.String())
	explicitness := SignalExplicitness(judgement.Explicitness.String())
	if !status.Valid() || !explicitness.Valid() {
		return SignalEvidence{}, fmt.Errorf("api: signal judgement %q/%q is missing from the API spec",
			judgement.Status, judgement.Explicitness)
	}
	out := SignalEvidence{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		Status:         status,
		Explicitness:   explicitness,
		Cancelled:      row.Cancelled,
		Evidence:       s.openEvidence(ctx, sealer, userID, row),
	}
	if row.UtteranceSeq != nil {
		seq := int(*row.UtteranceSeq)
		out.UtteranceSeq = &seq
	}
	return out, nil
}

// openEvidence는 근거가 된 사용자의 말을 푼다. 없으면 null이고, 열리지 않아도 null이다.
//
// 열리지 않는 한 줄 때문에 그날 화면을 아예 막지 않는다. 그 행은 근거 없는 판단으로 보이고,
// 사용자는 그래도 "이건 아니에요"로 뺄 수 있다. 로그에는 행 ID만 남는다.
func (s *signalService) openEvidence(
	ctx context.Context, sealer *crypto.Sealer, userID uuid.UUID, row db.ListSignalsByDateRow,
) *string {
	if len(row.EvidenceEnc) == 0 {
		return nil
	}
	text, err := sealer.OpenString(row.EvidenceEnc, sealing.SignalEvidence(row.ID))
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelError, "signal evidence cannot be opened",
			slog.String("user_id", userID.String()),
			slog.String("signal_id", row.ID.String()),
			slog.String("item", row.Item),
		)
		return nil
	}
	return &text
}

// sealerFor는 그 사용자의 Sealer를 받아 온다. 계정이 지워졌으면 로그인한 사람이 없는 것과 같다.
func (s *signalService) sealerFor(ctx context.Context, userID uuid.UUID) (*crypto.Sealer, error) {
	sealer, err := s.sealers.For(ctx, userID)
	switch {
	case errors.Is(err, sealing.ErrNoKey):
		return nil, errUnauthenticated
	case err != nil:
		return nil, fmt.Errorf("open user key: %w", err)
	}
	return sealer, nil
}

// location은 사용자의 시간대를 읽는다. 읽을 수 없으면 가입할 때의 기본 시간대를 쓴다.
// 기록 날짜의 경계가 이 시간대에서 나오므로, 여기서 UTC로 물러나면 밤에 읽는 화면의 기준일이 하루 밀린다.
func (s *signalService) location(ctx context.Context, user auth.User) *time.Location {
	if user.Timezone != "" && user.Timezone != "Local" {
		if loc, err := time.LoadLocation(user.Timezone); err == nil {
			return loc
		}
		// 시간대 이름은 가입할 때 확인한 값이라 여기서 다시 적을 까닭이 없다.
		s.logger.LogAttrs(ctx, slog.LevelWarn, "unknown user timezone, falling back to the default",
			slog.String("user_id", user.ID.String()),
			slog.String("fallback", auth.DefaultTimezone),
		)
	}
	if loc, err := time.LoadLocation(auth.DefaultTimezone); err == nil {
		return loc
	}
	// 실행 파일에 시간대 자료를 함께 묶으므로 여기까지 오지 않는다.
	return time.UTC
}

// ---- 응답 만들기 -------------------------------------------------------------------
// 아래는 계산 코어의 값을 응답의 타입으로 옮기기만 한다. 셈은 하나도 하지 않는다.

func trendResponse(e assess.Evaluation) (Trend, error) {
	t := e.Trend
	out := Trend{
		AsOf:                apiDate(e.AsOf),
		From:                apiDate(t.From),
		To:                  apiDate(t.To),
		ConversationDays:    t.ConversationDays,
		InsufficientRecords: e.Score.Insufficient,
		BaselinePending:     !e.Baseline.Established,
		Rows:                make([]TrendRow, 0, signal.TrendRowCount),
	}
	for _, line := range t.Rows {
		key := TrendRowKey(line.Row.String())
		comparison := TrendComparison(line.Comparison.String())
		if !key.Valid() {
			return Trend{}, fmt.Errorf("api: trend row %q is missing from the API spec", line.Row)
		}
		if !comparison.Valid() {
			return Trend{}, fmt.Errorf("api: trend comparison %q is missing from the API spec", line.Comparison)
		}
		cells := make([]TrendCell, 0, len(t.Dates))
		for i, date := range t.Dates {
			if i >= len(line.Marks) {
				return Trend{}, errors.New("api: the trend calendar has fewer marks than dates")
			}
			mark := TrendMark(line.Marks[i].String())
			if !mark.Valid() {
				return Trend{}, fmt.Errorf("api: trend mark %q is missing from the API spec", line.Marks[i])
			}
			cells = append(cells, TrendCell{Date: apiDate(date), Mark: mark})
		}
		row := TrendRow{
			Row:        key,
			Cells:      cells,
			Window:     SignalRate{ObservedDays: line.ObservedDays, Days: line.ConversationDays},
			Comparison: comparison,
		}
		if e.Baseline.Established {
			// 모으는 중인 값은 아직 평소가 아니다. 잡히기 전에는 아예 내보내지 않는다.
			usual := signalRate(line.Usual)
			row.Usual = &usual
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func reviewResponse(e assess.Evaluation, extractors []ReviewExtractor) (InternalReview, error) {
	score, err := reviewScore(e)
	if err != nil {
		return InternalReview{}, err
	}
	reliability, err := reviewConfidence(e)
	if err != nil {
		return InternalReview{}, err
	}
	base, err := reviewBaseline(e)
	if err != nil {
		return InternalReview{}, err
	}
	stages, err := reviewStage(e)
	if err != nil {
		return InternalReview{}, err
	}
	if extractors == nil {
		extractors = []ReviewExtractor{}
	}
	return InternalReview{
		AsOf:       apiDate(e.AsOf),
		Params:     reviewParams(e.Params),
		Extractors: extractors,
		Score:      score,
		Confidence: reliability,
		Baseline:   base,
		Change:     reviewChange(e),
		Stage:      stages,
	}, nil
}

func reviewParams(p params.Params) ReviewParams {
	return ReviewParams{
		WindowDays:          p.Window.Days,
		MinConversationDays: p.Window.MinConversationDays,
		ItemScoreMinDays: []int{
			p.Score.ItemScore1MinDays, p.Score.ItemScore2MinDays, p.Score.ItemScore3MinDays,
		},
		BandMinScores: []int{
			p.Score.MildMin, p.Score.ModerateMin, p.Score.ModeratelySevereMin, p.Score.SevereMin,
		},
		ConfidenceMediumMin:         p.Confidence.MediumMin,
		ConfidenceHighMin:           p.Confidence.HighMin,
		BaselineWindowDays:          p.Baseline.WindowDays,
		BaselineMinConversationDays: p.Baseline.MinConversationDays,
		CusumK:                      p.CUSUM.K,
		CusumH:                      p.CUSUM.H,
		CusumMaxStep:                p.CUSUM.MaxStep,
		CusumMaxS:                   p.CUSUM.MaxS,
		StageMinScores: []int{
			p.Stage.Stage1MinScore, p.Stage.Stage2MinScore, p.Stage.Stage3MinScore,
		},
		SustainedStage2Days:       p.Stage.SustainedStage2Days,
		TrendMinDifferencePercent: p.Trend.MinDifferencePercent,
	}
}

func reviewScore(e assess.Evaluation) (ReviewScore, error) {
	band := ScoreBand(e.Score.Band.String())
	if !band.Valid() {
		return ReviewScore{}, fmt.Errorf("api: score band %q is missing from the API spec", e.Score.Band)
	}
	items := make([]ReviewScoreItem, 0, signal.ItemCount)
	for _, item := range e.Score.Items {
		name := SignalItem(item.Item.String())
		if !name.Valid() {
			return ReviewScore{}, fmt.Errorf("api: signal item %q is missing from the API spec", item.Item)
		}
		items = append(items, ReviewScoreItem{
			Item:          name,
			ObservedDays:  item.ObservedDays,
			ConvertedDays: item.ConvertedDays,
			Points:        item.Points,
		})
	}
	return ReviewScore{
		WindowFrom:       apiDate(e.Score.Window.From),
		WindowTo:         apiDate(e.Score.Window.To),
		ConversationDays: e.Score.ConversationDays,
		Insufficient:     e.Score.Insufficient,
		Items:            items,
		Total:            e.Score.Total,
		Band:             band,
	}, nil
}

func reviewConfidence(e assess.Evaluation) (ReviewConfidence, error) {
	c := e.Confidence
	level := ConfidenceLevel(c.Level.String())
	limiting := ConfidenceComponent(c.Limiting.String())
	if !level.Valid() {
		return ReviewConfidence{}, fmt.Errorf("api: confidence level %q is missing from the API spec", c.Level)
	}
	if !limiting.Valid() {
		return ReviewConfidence{}, fmt.Errorf("api: confidence component %q is missing from the API spec", c.Limiting)
	}
	missing := make([]SignalItem, 0, signal.ItemCount)
	for _, item := range c.MissingItems() {
		name := SignalItem(item.String())
		if !name.Valid() {
			return ReviewConfidence{}, fmt.Errorf("api: signal item %q is missing from the API spec", item)
		}
		missing = append(missing, name)
	}
	return ReviewConfidence{
		ConversationDays:   c.ConversationDays,
		MentionedItems:     c.MentionedItems,
		MissingItems:       missing,
		ObservedJudgements: c.ObservedJudgements,
		DirectJudgements:   c.DirectJudgements,
		RecordCoverage:     reviewRatio(c.RecordCoverage),
		ItemCoverage:       reviewRatio(c.ItemCoverage),
		Explicitness:       reviewRatio(c.Explicitness),
		Insufficient:       c.Insufficient,
		Value:              reviewRatio(c.Value),
		Limiting:           limiting,
		Level:              level,
	}, nil
}

func reviewBaseline(e assess.Evaluation) (ReviewBaseline, error) {
	b := e.Baseline
	itemRates := make([]ReviewItemRate, 0, signal.ItemCount)
	for _, item := range signal.AllItems() {
		name := SignalItem(item.String())
		if !name.Valid() {
			return ReviewBaseline{}, fmt.Errorf("api: signal item %q is missing from the API spec", item)
		}
		itemRates = append(itemRates, ReviewItemRate{Item: name, Rate: signalRate(b.ItemRate(item))})
	}
	trendRates := make([]ReviewTrendRate, 0, signal.TrendRowCount)
	for _, row := range signal.AllTrendRows() {
		key := TrendRowKey(row.String())
		if !key.Valid() {
			return ReviewBaseline{}, fmt.Errorf("api: trend row %q is missing from the API spec", row)
		}
		trendRates = append(trendRates, ReviewTrendRate{Row: key, Rate: signalRate(b.TrendRate(row))})
	}
	return ReviewBaseline{
		Established:   b.Established,
		Start:         optionalDate(b.Start),
		End:           optionalDate(b.End),
		Extended:      b.Extended,
		Days:          b.Days,
		ObservedTotal: b.ObservedTotal,
		Mu:            b.Mu,
		ItemRates:     itemRates,
		TrendRates:    trendRates,
	}, nil
}

func reviewChange(e assess.Evaluation) ReviewChange {
	series := make([]ReviewChangePoint, 0, len(e.Change.Series))
	for _, point := range e.Change.Series {
		series = append(series, ReviewChangePoint{
			Date:      apiDate(point.Date),
			Observed:  point.Observed,
			Step:      point.Step,
			Capped:    point.Capped,
			AtCeiling: point.AtCeiling,
			S:         point.S,
			Detected:  point.Detected,
		})
	}
	return ReviewChange{
		Running:  e.Change.State.Running,
		Detected: e.Change.State.Detected,
		S:        e.Change.State.S,
		From:     optionalDate(e.Change.From),
		Series:   series,
	}
}

func reviewStage(e assess.Evaluation) (ReviewStage, error) {
	series := make([]ReviewStagePoint, 0, len(e.Stage.Series))
	for _, point := range e.Stage.Series {
		level := ConfidenceLevel(point.Confidence.String())
		if !level.Valid() {
			return ReviewStage{}, fmt.Errorf("api: confidence level %q is missing from the API spec", point.Confidence)
		}
		reasons := make([]StageReason, 0, len(point.Reasons))
		for _, reason := range point.Reasons {
			name := StageReason(reason.String())
			if !name.Valid() {
				return ReviewStage{}, fmt.Errorf("api: stage reason %q is missing from the API spec", reason)
			}
			reasons = append(reasons, name)
		}
		series = append(series, ReviewStagePoint{
			Date:             apiDate(point.Date),
			HasRecord:        point.HasRecord,
			ConversationDays: point.ConversationDays,
			Insufficient:     point.Insufficient,
			Score:            point.Score,
			Confidence:       level,
			Detected:         point.Detected,
			Raw:              int(point.Raw),
			Stage:            int(point.Stage),
			Held:             point.Held,
			ElevatedDays:     point.ElevatedDays,
			Reasons:          reasons,
		})
	}
	return ReviewStage{
		Stage:  int(e.Stage.State.Stage),
		From:   optionalDate(e.Stage.From),
		Series: series,
	}, nil
}

// dayItemResponse는 하루의 한 항목을 응답의 값으로 옮긴다. rows가 비어 있으면 빈 배열로 나간다.
func dayItemResponse(item signal.Item, judgement signal.Judgement, rows []SignalEvidence) (DaySignalItem, error) {
	name := SignalItem(item.String())
	status := SignalStatus(judgement.Status.String())
	explicitness := SignalExplicitness(judgement.Explicitness.String())
	if !name.Valid() {
		return DaySignalItem{}, fmt.Errorf("api: signal item %q is missing from the API spec", item)
	}
	if !status.Valid() || !explicitness.Valid() {
		return DaySignalItem{}, fmt.Errorf("api: signal judgement %q/%q is missing from the API spec",
			judgement.Status, judgement.Explicitness)
	}
	if rows == nil {
		rows = []SignalEvidence{}
	}
	return DaySignalItem{Item: name, Status: status, Explicitness: explicitness, Rows: rows}, nil
}

func signalRate(rate baseline.Rate) SignalRate {
	return SignalRate{ObservedDays: rate.ObservedDays, Days: rate.Days}
}

func reviewRatio(ratio confidence.Ratio) ReviewRatio {
	return ReviewRatio{Num: ratio.Num, Den: ratio.Den}
}

// optionalDate는 빈 날짜를 없는 값으로 옮긴다. 명세에서 null인 자리다.
func optionalDate(d recorddate.Date) *RecordDate {
	if d.IsZero() {
		return nil
	}
	out := apiDate(d)
	return &out
}
