// Package assess는 기록을 넣으면 기준일의 평가 전부를 돌려주는 입구다.
//
// 추정 점수, 신뢰도, 개인 기준선, 변화 탐지, 개입 단계, 추세 화면의 점 달력은 모두 같은 기록과 같은 기준일,
// 같은 조정 값에서 나와야 서로 앞뒤가 맞는다. 쓰는 쪽이 계산을 하나씩 따로 부르면 기준일이나 조정 값이
// 어긋난 채로 섞일 수 있어서, 한 번에 묶어 계산하는 입구를 하나만 둔다.
// 내부 확인 화면은 Evaluation을 그대로 보여주고, 나머지 코드는 여기서 필요한 값을 골라 쓴다.
//
// # 기준일 고르기
//
// 창은 기준일을 포함해 거슬러 센다. 기준일에 Day가 없으면 그날은 대화하지 않은 날로 센다.
// 끝난 하루를 돌아볼 때(밤사이 계산, 주간 리포트, 지난 기록 다시 돌리기)는 그 날짜를 그대로 Evaluate에 넣는다.
//
// 대화가 진행 중인 오늘은 다르다. 신호는 대화가 끝난 뒤에 뽑으므로 대화 도중에는 오늘의 Day가 아직 없다.
// 이때 오늘을 기준일로 넣으면 창의 가장 오래된 하루가 빠지고 그 자리에 빈 오늘이 들어온다.
// 이틀에 한 번 대화하는 사람처럼 대화한 일수가 기준에 걸쳐 있으면, 평가를 실제로 쓰는 그 순간에만 기록 부족이 되어
// 위기 관문에 전하는 점수 상태가 사라진다. 대화 도중에 읽는 쪽은 EvaluateLive를 쓴다.
//
// 개입 단계는 대화한 날에만 오른다. 그래서 오늘의 대화로 단계가 오를 만해졌더라도 그 단계는 오늘의 신호를 뽑은 뒤에
// 다시 계산한 평가에서 처음 보이고, 대화 도중에 읽는 쪽에는 다음에 대화하는 날부터 전해진다.
//
// # 다시 계산하기
//
// 결과는 어디에도 저장하지 않는다. 사용자가 하루를 지우거나 신호 하나를 취소하면
// 남은 기록으로 Evaluate를 다시 부르는 것이 곧 다시 계산이다. 지난 결과를 고쳐 쓰는 길은 따로 없다.
// 기준선 기간의 날을 지우면 평소가 다시 정해지고, 그 뒤의 변화 탐지와 개입 단계도 첫날부터 다시 정해진다.
package assess

import (
	"errors"
	"fmt"

	"github.com/sirin-interact/tmrlife/server/internal/core/baseline"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/cusum"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/core/stage"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// ErrNoAsOf는 기준일이 빠졌다는 뜻이다. 현재 시각을 읽지 않으므로 기준일은 언제나 받아야 한다.
var ErrNoAsOf = errors.New("assess: as-of date is missing")

// Evaluation은 기준일 하루에 본 평가 전부다.
type Evaluation struct {
	// AsOf는 기준일이다. 아래의 모든 값이 이날을 기준으로 한다.
	AsOf recorddate.Date
	// Params는 이 평가에 쓴 조정 값이다. 어떤 값으로 계산한 결과인지가 결과와 함께 다니게 한다.
	// 누적값 옆에 한계값을 그리는 것처럼, 결과를 읽을 때 조정 값이 함께 필요한 곳이 있다.
	Params params.Params

	// Score는 추정 점수와 항목별 관찰 일수, 환산 일수, 항목 점수다.
	Score score.Result
	// Confidence는 신뢰도의 세 요소와 최종 값이다.
	Confidence confidence.Result
	// Baseline은 개인 기준선이다. 아직 잡히지 않았으면 지금까지 모인 값이 들어 있다.
	Baseline baseline.Baseline
	// Change는 변화 탐지의 날짜별 누적값과 기준일의 상태다.
	Change cusum.Result
	// Stage는 개입 단계의 날짜별 흐름과 기준일의 상태다.
	Stage stage.Result
	// Trend는 추세 화면의 점 달력이다.
	Trend Trend
}

// Evaluate는 asOf를 기준일로 평가 전부를 계산한다.
//
// days는 대화한 날 전부를 날짜순으로 넘긴다(signal.ValidateDays를 통과해야 한다). 고치지 않는다.
// 순서를 모르면 signal.SortDays를 먼저 거치고, 신호 행에서 시작한다면 EvaluateRows를 쓴다.
// asOf보다 뒤의 날은 쓰지 않는다. 지난 날짜를 기준일로 넣으면 그날 알 수 있었던 기록만으로 구한 평가가 나온다.
//
// 기록이 하나도 없어도 오류가 아니다. 기록 부족, 낮은 신뢰도, 0단계인 평가가 나온다.
// 오류는 조정 값이 틀렸을 때(*params.FieldError), 기준일이 비었을 때(ErrNoAsOf),
// days가 날짜순이 아니거나 틀린 하루가 있을 때(*signal.DayError), 창을 만들 수 없을 때(score.ErrInvalidWindow)다.
// 오류일 때는 빈 Evaluation을 돌려준다. 일부만 계산된 평가는 없다.
func Evaluate(days []signal.Day, asOf recorddate.Date, p params.Params) (Evaluation, error) {
	if err := p.Validate(); err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	if asOf.IsZero() {
		return Evaluation{}, ErrNoAsOf
	}
	if err := signal.ValidateDays(days); err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}

	estimated, err := score.Compute(days, asOf, p)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	reliability, err := confidence.Compute(days, asOf, p)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	base, err := baseline.Compute(days, asOf, p)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	// 기준선과 변화 탐지를 같은 목록에서 구한다. 서로 다른 목록을 넘기면 값은 나오지만 뜻이 없다.
	change, err := cusum.Run(days, base, p)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	stages, err := stage.Compute(days, asOf, p)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}

	return Evaluation{
		AsOf:       asOf,
		Params:     p,
		Score:      estimated,
		Confidence: reliability,
		Baseline:   base,
		Change:     change,
		Stage:      stages,
		// 점 달력은 점수와 같은 창을 본다. 사용자가 보는 점과 점수가 센 날이 어긋나지 않는다.
		Trend: buildTrend(days, estimated.Window, base, p),
	}, nil
}

// EvaluateLive는 대화가 진행 중인 날(today)에 읽을 평가를 돌려준다. 대화 엔진과 위기 관문처럼 대화 도중에 상태를 읽는 쪽이 쓴다.
//
// 오늘의 Day가 이미 있으면(오늘 앞서 한 대화의 분석이 끝났으면) 오늘을 기준일로 삼고, 없으면 어제를 기준일로 삼는다.
// 아직 끝나지 않았고 기록도 없는 오늘을 "대화하지 않은 날"로 세지 않기 위해서다. 까닭은 패키지 설명에 있다.
// 어제를 기준일로 삼은 평가는 어제 하루가 끝난 뒤에 구한 평가와 같은 값이다. 어느 날짜가 쓰였는지는 Evaluation.AsOf에 있다.
//
// 오류는 Evaluate와 같다. today가 비어 있으면 ErrNoAsOf다.
func EvaluateLive(days []signal.Day, today recorddate.Date, p params.Params) (Evaluation, error) {
	if today.IsZero() {
		return Evaluation{}, ErrNoAsOf
	}
	return Evaluate(days, liveAsOf(days, today), p)
}

// liveAsOf는 대화가 진행 중인 날에 기준일로 삼을 날짜를 고른다.
func liveAsOf(days []signal.Day, today recorddate.Date) recorddate.Date {
	for _, day := range days {
		if day.Date == today {
			return today
		}
	}
	// 지원하는 날짜 범위의 첫날에는 어제가 없다. 그때는 오늘을 그대로 쓴다.
	if yesterday := today.AddDays(-1); !yesterday.IsZero() {
		return yesterday
	}
	return today
}

// EvaluateRows는 날짜별로 모은 신호 행을 하루씩 합친 다음 Evaluate를 부른다.
//
// 취소된 행은 합칠 때 빠지고, 행이 하나도 없는 날짜는 대화하지 않은 날로 다뤄진다.
// 그래서 하루를 지웠거나 신호 하나를 취소한 뒤에는 남은 행을 그대로 다시 넘기면 된다.
// 행이 틀렸으면 *signal.DayError를 감싼 오류다.
//
// 그날이 대화한 날인지는 행이 있는지로만 안다. 분석이 끝난 대화는 아무 항목도 나오지 않았어도
// 언급 없음 행을 남겨야 그날이 대화한 일수에 든다. 넘기는 쪽이 지킬 것은 signal.MergeDays에 적혀 있다.
func EvaluateRows(rowsByDate map[recorddate.Date][]signal.Row, asOf recorddate.Date, p params.Params) (Evaluation, error) {
	days, err := signal.MergeDays(rowsByDate)
	if err != nil {
		return Evaluation{}, fmt.Errorf("assess: %w", err)
	}
	return Evaluate(days, asOf, p)
}

// GateState는 위기 관문이 그 사람의 최근 상태에 대해 묻는 두 가지에 답한다:
// 추정 점수가 관문의 판정을 올릴 기준 이상인지, 변화 감지 상태인지.
// 기준은 이 평가에 쓴 조정 값(Params.Crisis)에서 가져온다.
//
// 관문은 대화 도중에 이 값을 읽는다. 그때의 평가는 EvaluateLive로 구한 것이어야 한다.
// 오늘의 기록이 아직 없는데 오늘을 기준일로 구한 평가에서는 점수 쪽이 기록 부족으로 꺼져 있을 수 있다.
//
// 기록이 모자라 점수를 내지 않았으면 점수 쪽은 거짓이다. 점수가 없는 것을 높은 점수로도 0점으로도 읽지 않는다.
// 변화 감지는 점수와 따로 돌기 때문에 기록 부족이어도 그대로 전한다.
//
// 창 안에 대화한 날이 하나도 없으면 변화 감지도 꺼진 것으로 전한다. 누적값은 대화한 날에만 움직이고 저절로 줄지 않아서,
// 오래 쉬는 동안에도 Change.State.Detected는 켜진 채로 남는다. 그 흔적을 그대로 전하면 40일 만에 돌아온 사용자의
// 첫마디가 한 달도 더 된 기록 때문에 한 단계 무겁게 다뤄진다. 개입 단계가 같은 때에 0단계로 돌아가는 것과 같은 까닭이다.
// 돌아와서 대화한 날의 기록이 생기면 그때의 누적값으로 다시 본다.
//
// Evaluate가 돌려준 값이 아니면(빈 Evaluation) 둘 다 거짓이다.
// 평가가 오류로 끝났더라도 관문은 멈추지 않아야 하므로, 그때는 빈 crisis.State로 관문을 평소대로 돌린다.
func (e Evaluation) GateState() crisis.State {
	if e.AsOf.IsZero() {
		return crisis.State{}
	}
	state := crisis.State{ChangeDetected: e.Change.State.Detected && e.Score.ConversationDays > 0}
	if total, ok := e.Score.Score(); ok {
		state.ScoreElevated = crisis.ScoreElevated(total, e.Params.Crisis)
	}
	return state
}
