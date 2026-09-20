// Package params는 계산에 쓰는 조정 값을 한곳에 모은다.
//
// 기간, 구간의 경계, 한계값 같은 숫자를 계산 코드 곳곳에 적어 두면
// 값을 바꿔 가며 돌려 볼 수 없고, 어떤 값으로 계산한 결과인지도 남길 수 없다.
// 그래서 계산 함수는 숫자를 직접 들고 있지 않고 언제나 Params를 받아서 쓴다.
//
// Default의 값은 초기값이다. 가상 기록으로 돌려 본 결과와 전문가의 의견을 보고 바꾼다.
// 소수는 식 자체가 소수를 쓰는 곳(신뢰도의 구간, 변화 탐지)에만 둔다.
// 나머지는 정수여서 같은 기록에서 언제나 같은 값이 나온다.
package params

import (
	"errors"
	"fmt"
	"math"

	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
)

const (
	// 항목 하나가 받을 수 있는 가장 높은 점수다. 점수 척도가 항목마다 0부터 3까지로 정해져 있어서 조정 값이 아니다.
	maxItemScore = 3
	// 추정 점수가 닿을 수 있는 가장 높은 값(24)이다. 이보다 큰 경계는 영영 닿지 않으므로 오타로 본다.
	maxTotalScore = signal.ItemCount * maxItemScore
	// 두 비율이 가장 멀리 벌어질 수 있는 거리다. 한쪽이 0퍼센트이고 다른 쪽이 100퍼센트일 때다.
	maxDifferencePercent = 100
	// 달력 날짜로 세는 기간(창, 기준선 기간, 단계가 이어진 일수)의 상한이다.
	// 계산은 이 일수만큼의 칸을 만들고 일수끼리 곱하기도 한다. 오타로 들어온 터무니없는 값이 메모리를 다 쓰거나
	// 곱셈을 넘치게 하지 않도록 막는다. 한 해를 넘는 "최근 기간"은 이 서비스에서 뜻이 없다.
	maxPeriodDays = 366
)

// Params는 계산 코어의 조정 값 전부다. 값 타입이라 복사해서 고쳐도 다른 곳에 영향이 없다.
type Params struct {
	Window     Window
	Score      Score
	Confidence Confidence
	Baseline   Baseline
	CUSUM      CUSUM
	Stage      Stage
	Crisis     Crisis
	Trend      Trend
}

// Window는 점수, 신뢰도, 개입 단계가 함께 보는 "최근 기간"이다.
type Window struct {
	// Days는 창의 길이다. 기준일을 포함해 거슬러 센다. 기본 14. 366을 넘을 수 없다.
	//
	// 점수 척도가 "지난 2주 동안 얼마나 자주"를 묻기 때문에 2주다.
	// 관찰된 일수를 환산할 때의 기준 일수, 기록 충실도의 분모, 점 달력의 칸 수도 이 값이다.
	//
	// 이 값 하나가 여러 곳에 물려 있으므로 혼자만 바꾸면 안 된다. 항목 점수의 경계(Score.ItemScore*MinDays)는
	// 비율이 아니라 환산 일수라서, 창을 28일로 늘리면 "12일 이상"은 더 이상 "거의 매일"이 아니다.
	// 창을 10일로 줄이면 12일이라는 경계는 영영 닿지 않아 Validate가 막는다.
	// 창을 바꿀 때는 MinConversationDays와 항목 점수의 경계를 같은 비율로 함께 옮긴다.
	Days int

	// MinConversationDays는 창 안에서 대화한 날이 이보다 적으면 "기록 부족"으로 두는 값이다. 기본 7.
	//
	// 사흘 치 기록을 2주로 늘려 읽으면 하루의 기복이 2주의 상태처럼 보인다.
	// 기록 부족일 때는 점수도 신뢰도도 계산하지 않고, 개입 단계는 올리지도 내리지도 않고 전날의 값을 이어 간다.
	// 대화한 일수로 나누는 계산이 있으므로 1보다 작을 수 없다.
	MinConversationDays int
}

// Score는 환산 일수를 항목 점수로, 항목 점수의 합을 구간으로 옮기는 경계다.
type Score struct {
	// 환산 일수가 이 값 이상이면 그 항목 점수를 준다. 기본 1, 7, 12.
	// 0일은 0점(전혀 없음), 1~6일은 1점(며칠 동안), 7~11일은 2점(일주일 이상), 12~14일은 3점(거의 매일)이다.
	// 비율이 아니라 Window.Days를 기준으로 한 일수다. 창의 길이를 바꾸면 함께 옮긴다.
	//
	// ItemScore1MinDays를 1보다 크게 잡으면 한두 번 관찰된 항목이 0점이 된다.
	// 기본값에서는 "한 번이라도 관찰된 항목은 적어도 1점"이 지켜지고, 값을 올리면 그 성질을 일부러 버리는 것이다.
	ItemScore1MinDays int
	ItemScore2MinDays int
	ItemScore3MinDays int

	// 추정 점수(0~24)가 이 값 이상이면 그 구간이다. 기본 5, 10, 15, 20.
	// 0~4 최소, 5~9 가벼움, 10~14 중간, 15~19 다소 심함, 20 이상 심함.
	// 여덟 항목 척도에서 널리 쓰는 구간을 그대로 가져온 것이다.
	MildMin             int
	ModerateMin         int
	ModeratelySevereMin int
	SevereMin           int
}

// Confidence는 신뢰도 값(0부터 1)을 낮음, 보통, 높음으로 나누는 경계다.
type Confidence struct {
	// MediumMin 미만이면 낮음이다. 기본 0.4.
	// 신뢰도가 낮으면 개입 단계를 올리지 않고, 빠진 항목을 묻거나 자가 점검을 제안한다.
	MediumMin float64
	// HighMin 이상이면 높음이고, 그 사이는 보통이다. 기본 0.7.
	HighMin float64
}

// Baseline은 그 사람의 "평소"를 어느 기간의 기록으로 잡을지다.
type Baseline struct {
	// WindowDays는 첫 대화 날부터(첫날 포함) 기준선으로 삼는 기간이다. 기본 14. 366을 넘을 수 없다.
	// 한번 잡은 기준선은 고정한다. 기간에 든 날을 사용자가 지웠을 때만 다시 계산된다.
	WindowDays int

	// MinConversationDays는 기준선에 필요한 대화 일수다. 기본 7.
	// 기간 안에 대화한 날이 이보다 적으면 이만큼 찰 때까지 기간을 늘린다.
	// 며칠 안 되는 기록으로 평소를 정하면 말수가 적은 사람의 평소가 0에 가깝게 잡히고,
	// 그 뒤의 평범한 날이 모두 "평소보다 나쁨"으로 읽힌다.
	MinConversationDays int
}

// CUSUM은 기준선에서 벗어난 정도를 날마다 쌓아 가는 변화 탐지의 값이다.
// 대화한 날마다 S = max(0, S + (그날 관찰된 항목 수 − 평소의 하루 평균 − K))로 쌓는다.
// 하루에 더하는 값은 MaxStep까지만 인정하고, 더한 뒤의 S는 H의 MaxS배를 넘지 못한다.
//
// 네 값은 적은 십진수 그대로 읽힌다. 0.3은 이진 소수로 딱 떨어지지 않지만 계산에서는 10분의 3이다.
// 누적값이 H와 딱 같은지가 끝자리 오차로 갈리지 않게 하려는 것이다.
type CUSUM struct {
	// K는 허용 여유다. 평소보다 이만큼 많은 것까지는 흔한 기복으로 보고 쌓지 않는다. 기본 0.5.
	K float64

	// H는 한계값이다. 누적값이 이 값을 넘으면(같으면 아니다) 변화 감지다. 기본 4.0.
	// 넘은 뒤에도 누적값을 0으로 되돌리지 않는다. 나아지면 누적값이 줄어 H 아래로 내려가고 그때 감지가 풀린다.
	H float64

	// MaxStep은 하루에 늘 수 있는 누적값의 상한이다. 기본 2.0. 0이면 상한이 없다.
	//
	// 상한이 없으면 평소의 하루 평균이 0.3인 사람에게 항목 여섯 개가 관찰된 하루만으로 누적값이 5.2가 되어
	// 그 하루 때문에 감지가 켜진다. 하루 크게 나빴다가 회복하는 기복에는 울리지 않아야 한다.
	// H를 높여서 거르면 서서히 나빠지는 흐름을 늦게 잡는다. 하루 증가량을 묶으면 H를 그대로 둔 채
	// 하루짜리 급등만 걸러 낼 수 있다. 서서히 나빠지는 흐름은 하루 증가량이 작아서 상한에 닿지 않는다.
	// 줄어드는 쪽은 묶지 않는다.
	MaxStep float64

	// MaxS는 누적값의 천장을 H의 몇 배로 둘지다. 기본 2.0(누적값은 H의 두 배를 넘지 못한다). 0이면 천장이 없다.
	//
	// 천장이 없으면 힘든 몇 주 동안 누적값이 끝없이 쌓이고, 회복한 뒤에도 그만큼을 다 덜어 낼 때까지
	// 몇 달 동안 감지가 켜져 있다. 천장을 두면 나빴던 기간이 아무리 길어도 회복한 뒤 대화 몇 번이면 감지가 풀린다.
	// H의 배수로 받는 것은 H를 조정할 때 천장이 함께 따라가게 하기 위해서다.
	// 1 이하이면 누적값이 H를 넘을 수 없어 변화를 영영 감지하지 못하므로 Validate가 막는다.
	MaxS float64
}

// Stage는 개입 단계를 정하는 값이다.
type Stage struct {
	// 추정 점수가 이 값 이상이면 그 단계다. 기본 5, 10, 15.
	// 0~4는 0단계(일상), 5~9는 1단계(회고), 10~14는 2단계(제안), 15 이상은 3단계(권유)다.
	// 점수 구간의 경계와 기본값이 같지만 따로 둔다. 구간은 척도의 해석이고 단계는 이 서비스의 행동이라 따로 조정한다.
	Stage1MinScore int
	Stage2MinScore int
	Stage3MinScore int

	// SustainedStage2Days는 2단계 이상이 달력 날짜로 이 일수째 이어지는 날부터 3단계로 올리는 값이다. 기본 14. 366을 넘을 수 없다.
	// 점수가 더 오르지 않아도 가볍지 않은 상태가 두 주째 이어지면 사람에게 연결하는 쪽으로 옮긴다.
	// 기록 부족이어서 단계를 그대로 이어 간 날은 이 일수에 세지 않는다.
	// 1이면 점수로 2단계가 되는 날부터 곧바로 3단계를 향하므로 2단계에 머무는 날이 거의 없어진다.
	SustainedStage2Days int
}

// Crisis는 위기 관문이 단계를 올려 잡는 조건의 값이다.
// 발화에서 표현을 찾는 일은 코어 밖에서 하고, 여기 값은 찾은 뒤의 판정에만 쓴다.
type Crisis struct {
	// EscalationMinScore는 추정 점수가 이 값 이상이면 관문의 판정을 한 단계 올리는 값이다. 기본 10.
	// 상태가 나쁜 동안에는 같은 말도 더 무겁게 듣는다. 변화 감지 상태일 때도 똑같이 올린다.
	// 해당 없음(0단계)으로 본 발화는 올리지 않는다. 그러지 않으면 상태가 나쁜 동안
	// "배고파 죽겠다" 같은 관용 표현까지 모두 확인 대상이 된다.
	// AI 판별이 답하지 못해서 1단계가 되었을 뿐인 발화도 같은 까닭으로 올리지 않는다.
	EscalationMinScore int

	// RepeatWindowDays 안에 관문에 걸린(최종 1단계 이상) 대화가 이번 대화를 포함해 RepeatCount개째면
	// 1단계 표현도 2단계로 대응한다. 기본 14일, 3개.
	// 애매한 표현도 여러 대화에 걸쳐 되풀이되면 그때그때의 답과 상관없이 무겁게 본다.
	//
	// 발화가 아니라 대화를 센다. 1단계의 확인은 되묻고 직접 묻는 두 걸음이라 한 대화 안에서 애매한 말이
	// 두 번 나오는 것이 정상이다. 발화를 세면 한 번의 확인이 두 번으로 세어져, 다음 대화의 첫 애매한 표현이
	// 되묻는 걸음 없이 곧바로 2단계가 된다.
	RepeatWindowDays int
	RepeatCount      int

	// SensitiveWindowDays는 2단계 이상이 있었던 뒤 이 일수 동안 1단계 표현도 2단계로 대응하는 기간이다. 기본 7.
	SensitiveWindowDays int
}

// Trend는 추세 화면에서 최근 기간의 빈도를 평소의 빈도와 견줄 때 쓰는 값이다.
type Trend struct {
	// MinDifferencePercent는 최근 기간의 관찰 비율과 평소의 관찰 비율이 퍼센트포인트로 이만큼 이상 벌어져야
	// "평소보다 잦음"이나 "평소보다 드묾"으로 말하는 값이다. 기본 20. 그보다 덜 벌어지면 "평소와 비슷함"이다.
	//
	// 대화한 날이 열흘에서 두 주면 하루가 7에서 10퍼센트포인트쯤이다. 하루의 차이는 흔한 기복이라
	// 그것만으로 평소와 다르다고 말하지 않도록, 이틀에서 사흘은 벌어져야 닿는 값으로 잡았다.
	// 0이면 비율이 똑같아도 "잦음"이 되므로 1보다 작을 수 없다.
	MinDifferencePercent int
}

// Default는 초기값을 담은 Params를 돌려준다. 부를 때마다 새 값이라 고쳐 써도 된다.
func Default() Params {
	return Params{
		Window: Window{
			Days:                14,
			MinConversationDays: 7,
		},
		Score: Score{
			ItemScore1MinDays:   1,
			ItemScore2MinDays:   7,
			ItemScore3MinDays:   12,
			MildMin:             5,
			ModerateMin:         10,
			ModeratelySevereMin: 15,
			SevereMin:           20,
		},
		Confidence: Confidence{
			MediumMin: 0.4,
			HighMin:   0.7,
		},
		Baseline: Baseline{
			WindowDays:          14,
			MinConversationDays: 7,
		},
		CUSUM: CUSUM{
			K:       0.5,
			H:       4.0,
			MaxStep: 2.0,
			MaxS:    2.0,
		},
		Stage: Stage{
			Stage1MinScore:      5,
			Stage2MinScore:      10,
			Stage3MinScore:      15,
			SustainedStage2Days: 14,
		},
		Crisis: Crisis{
			EscalationMinScore:  10,
			RepeatWindowDays:    14,
			RepeatCount:         3,
			SensitiveWindowDays: 7,
		},
		Trend: Trend{
			MinDifferencePercent: 20,
		},
	}
}

// FieldError는 어느 값이 왜 틀렸는지 알려준다.
type FieldError struct {
	// Field는 Params에서의 경로다. 예: "Score.ItemScore2MinDays".
	Field string
	// Reason은 지켜야 할 조건과 실제 값이다.
	Reason string
}

func (e *FieldError) Error() string {
	return "params: " + e.Field + " " + e.Reason
}

// Validate는 값들이 계산에 넣어도 되는 범위인지, 서로 앞뒤가 맞는지 본다.
//
// 틀린 값을 하나 찾고 멈추지 않고 전부 모아서 돌려준다. 값을 여러 개 바꿔 가며 돌려 보는 쪽에서
// 한 번에 고칠 수 있게 하기 위해서다. 오류는 *FieldError를 errors.Join으로 묶은 것이다.
// errors.As로 첫 번째를 꺼내거나, Unwrap() []error로 전부 꺼낸다.
func (p Params) Validate() error {
	var c checker

	c.atLeast("Window.Days", p.Window.Days, 1)
	c.atMost("Window.Days", p.Window.Days, "the longest supported period", maxPeriodDays)
	c.atLeast("Window.MinConversationDays", p.Window.MinConversationDays, 1)
	c.atMost("Window.MinConversationDays", p.Window.MinConversationDays, "Window.Days", p.Window.Days)

	// 환산 일수 0은 언제나 0점이어야 한다. 관찰된 적 없는 항목에 점수가 붙으면 안 된다.
	c.atLeast("Score.ItemScore1MinDays", p.Score.ItemScore1MinDays, 1)
	c.above("Score.ItemScore2MinDays", p.Score.ItemScore2MinDays, "Score.ItemScore1MinDays", p.Score.ItemScore1MinDays)
	c.above("Score.ItemScore3MinDays", p.Score.ItemScore3MinDays, "Score.ItemScore2MinDays", p.Score.ItemScore2MinDays)
	// 환산 일수는 창의 길이를 넘지 못한다. 그보다 큰 경계는 영영 닿지 않는다.
	c.atMost("Score.ItemScore3MinDays", p.Score.ItemScore3MinDays, "Window.Days", p.Window.Days)

	c.atLeast("Score.MildMin", p.Score.MildMin, 1)
	c.above("Score.ModerateMin", p.Score.ModerateMin, "Score.MildMin", p.Score.MildMin)
	c.above("Score.ModeratelySevereMin", p.Score.ModeratelySevereMin, "Score.ModerateMin", p.Score.ModerateMin)
	c.above("Score.SevereMin", p.Score.SevereMin, "Score.ModeratelySevereMin", p.Score.ModeratelySevereMin)
	c.atMost("Score.SevereMin", p.Score.SevereMin, "the maximum score", maxTotalScore)

	mediumOK := c.finite("Confidence.MediumMin", p.Confidence.MediumMin)
	highOK := c.finite("Confidence.HighMin", p.Confidence.HighMin)
	// 0이면 낮음 구간이 사라져서, 신뢰도가 개입을 막는 장치로 일하지 못한다.
	if mediumOK && p.Confidence.MediumMin <= 0 {
		c.fail("Confidence.MediumMin", "must be greater than 0, got %v", p.Confidence.MediumMin)
	}
	if mediumOK && highOK && p.Confidence.HighMin <= p.Confidence.MediumMin {
		c.fail("Confidence.HighMin",
			"must be greater than Confidence.MediumMin (%v), got %v", p.Confidence.MediumMin, p.Confidence.HighMin)
	}
	// 신뢰도는 1을 넘지 못한다. 그보다 큰 경계는 영영 닿지 않는다.
	if highOK && p.Confidence.HighMin > 1 {
		c.fail("Confidence.HighMin", "must be at most 1, got %v", p.Confidence.HighMin)
	}

	c.atLeast("Baseline.WindowDays", p.Baseline.WindowDays, 1)
	c.atMost("Baseline.WindowDays", p.Baseline.WindowDays, "the longest supported period", maxPeriodDays)
	// 평소의 하루 평균은 대화한 일수로 나눠 구한다. 0일로는 평균을 낼 수 없다.
	c.atLeast("Baseline.MinConversationDays", p.Baseline.MinConversationDays, 1)
	c.atMost("Baseline.MinConversationDays", p.Baseline.MinConversationDays, "Baseline.WindowDays", p.Baseline.WindowDays)

	if c.finite("CUSUM.K", p.CUSUM.K) && p.CUSUM.K < 0 {
		c.fail("CUSUM.K", "must be at least 0, got %v", p.CUSUM.K)
	}
	if c.finite("CUSUM.H", p.CUSUM.H) && p.CUSUM.H <= 0 {
		c.fail("CUSUM.H", "must be greater than 0, got %v", p.CUSUM.H)
	}
	if c.finite("CUSUM.MaxStep", p.CUSUM.MaxStep) && p.CUSUM.MaxStep < 0 {
		c.fail("CUSUM.MaxStep", "must be at least 0 (0 means no cap), got %v", p.CUSUM.MaxStep)
	}
	// 천장이 H 이하이면 누적값이 H를 넘는 날이 오지 않아, 오류 없이 변화 탐지가 꺼진다.
	if c.finite("CUSUM.MaxS", p.CUSUM.MaxS) && p.CUSUM.MaxS != 0 && p.CUSUM.MaxS <= 1 {
		c.fail("CUSUM.MaxS", "must be 0 (no ceiling) or greater than 1 (a multiple of CUSUM.H), got %v", p.CUSUM.MaxS)
	}

	c.atLeast("Stage.Stage1MinScore", p.Stage.Stage1MinScore, 1)
	c.above("Stage.Stage2MinScore", p.Stage.Stage2MinScore, "Stage.Stage1MinScore", p.Stage.Stage1MinScore)
	c.above("Stage.Stage3MinScore", p.Stage.Stage3MinScore, "Stage.Stage2MinScore", p.Stage.Stage2MinScore)
	c.atMost("Stage.Stage3MinScore", p.Stage.Stage3MinScore, "the maximum score", maxTotalScore)
	c.atLeast("Stage.SustainedStage2Days", p.Stage.SustainedStage2Days, 1)
	c.atMost("Stage.SustainedStage2Days", p.Stage.SustainedStage2Days, "the longest supported period", maxPeriodDays)

	c.atLeast("Crisis.EscalationMinScore", p.Crisis.EscalationMinScore, 1)
	c.atMost("Crisis.EscalationMinScore", p.Crisis.EscalationMinScore, "the maximum score", maxTotalScore)
	c.atLeast("Crisis.RepeatWindowDays", p.Crisis.RepeatWindowDays, 1)
	// 1이면 애매한 표현이 나올 때마다 곧바로 2단계가 되어, 먼저 되묻고 듣는 1단계가 사라진다.
	c.atLeast("Crisis.RepeatCount", p.Crisis.RepeatCount, 2)
	c.atLeast("Crisis.SensitiveWindowDays", p.Crisis.SensitiveWindowDays, 1)

	c.atLeast("Trend.MinDifferencePercent", p.Trend.MinDifferencePercent, 1)
	// 두 비율의 차이는 100퍼센트포인트를 넘지 못한다. 그보다 큰 값은 영영 닿지 않는다.
	c.atMost("Trend.MinDifferencePercent", p.Trend.MinDifferencePercent, "the largest possible difference", maxDifferencePercent)

	return errors.Join(c.errs...)
}

// checker는 틀린 값을 모은다. 하나 찾고 멈추지 않으려고 둔다.
type checker struct {
	errs []error
}

// fail은 틀린 값 하나를 적는다. 조건은 부르는 쪽에서 먼저 보고, 틀렸을 때만 부른다.
//
// 메시지에 넣을 값을 넘기는 것만으로도 메모리를 새로 잡는다. 계산 함수마다 검사를 하고, 개입 단계는 첫날부터
// 달력의 하루마다 점수와 신뢰도를 다시 구하므로 검사는 평가 한 번에 수백 번 되풀이된다.
// 맞는 값을 검사할 때는 아무것도 잡지 않도록, 값을 넘기는 일을 틀렸을 때로 미룬다.
func (c *checker) fail(field, format string, args ...any) {
	c.errs = append(c.errs, &FieldError{Field: field, Reason: fmt.Sprintf(format, args...)})
}

func (c *checker) atLeast(field string, got, floor int) {
	if got < floor {
		c.fail(field, "must be at least %d, got %d", floor, got)
	}
}

func (c *checker) atMost(field string, got int, limitName string, limit int) {
	if got > limit {
		c.fail(field, "must be at most %s (%d), got %d", limitName, limit, got)
	}
}

func (c *checker) above(field string, got int, otherName string, other int) {
	if got <= other {
		c.fail(field, "must be greater than %s (%d), got %d", otherName, other, got)
	}
}

// finite는 NaN과 무한대를 거른다. NaN은 어떤 비교에서도 거짓이라 구간과 한계값 판정을 조용히 망가뜨린다.
// 통과하지 못하면 그 값의 나머지 검사는 건너뛴다. 같은 값으로 오류가 두 번 나오지 않게 한다.
func (c *checker) finite(field string, got float64) bool {
	if math.IsNaN(got) || math.IsInf(got, 0) {
		c.fail(field, "must be a finite number, got %v", got)
		return false
	}
	return true
}
