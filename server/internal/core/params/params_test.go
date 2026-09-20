package params

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 기본값을 한 줄에 하나씩 적는다. 값을 바꾸면 이 표도 함께 바꿔야 해서, 모르는 사이에 바뀌는 일이 없다.
func TestDefault(t *testing.T) {
	p := Default()

	ints := []struct {
		name string
		got  int
		want int
	}{
		{"점수는 기준일을 포함한 최근 14일을 본다", p.Window.Days, 14},
		{"창 안에서 대화한 날이 7일 미만이면 기록 부족이다", p.Window.MinConversationDays, 7},

		{"환산 일수 1일부터 항목 점수 1점이다", p.Score.ItemScore1MinDays, 1},
		{"환산 일수 7일부터 항목 점수 2점이다", p.Score.ItemScore2MinDays, 7},
		{"환산 일수 12일부터 항목 점수 3점이다", p.Score.ItemScore3MinDays, 12},

		{"추정 점수 5부터 가벼움이다", p.Score.MildMin, 5},
		{"추정 점수 10부터 중간이다", p.Score.ModerateMin, 10},
		{"추정 점수 15부터 다소 심함이다", p.Score.ModeratelySevereMin, 15},
		{"추정 점수 20부터 심함이다", p.Score.SevereMin, 20},

		{"기준선은 첫 대화 날부터 14일의 기록으로 잡는다", p.Baseline.WindowDays, 14},
		{"기준선에는 대화한 날이 7일 있어야 한다", p.Baseline.MinConversationDays, 7},

		{"추정 점수 5부터 1단계다", p.Stage.Stage1MinScore, 5},
		{"추정 점수 10부터 2단계다", p.Stage.Stage2MinScore, 10},
		{"추정 점수 15부터 3단계다", p.Stage.Stage3MinScore, 15},
		{"2단계 이상이 14일째 이어지는 날부터 3단계다", p.Stage.SustainedStage2Days, 14},

		{"추정 점수 10부터 위기 관문의 판정을 한 단계 올린다", p.Crisis.EscalationMinScore, 10},
		{"관문에 걸린 대화는 최근 14일 안에서 센다", p.Crisis.RepeatWindowDays, 14},
		{"관문에 걸린 대화가 이번 대화를 포함해 세 개째면 2단계다", p.Crisis.RepeatCount, 3},
		{"2단계 이상이 있었던 뒤 7일은 민감 기간이다", p.Crisis.SensitiveWindowDays, 7},

		{"평소의 비율과 20퍼센트포인트 이상 벌어져야 잦음이나 드묾으로 말한다", p.Trend.MinDifferencePercent, 20},
	}
	for _, tt := range ints {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}

	floats := []struct {
		name string
		got  float64
		want float64
	}{
		{"신뢰도 0.4 미만은 낮음이다", p.Confidence.MediumMin, 0.4},
		{"신뢰도 0.7 이상은 높음이다", p.Confidence.HighMin, 0.7},
		{"변화 탐지의 허용 여유는 0.5다", p.CUSUM.K, 0.5},
		{"변화 탐지의 한계값은 4.0이다", p.CUSUM.H, 4.0},
		{"누적값은 하루에 2.0까지만 는다", p.CUSUM.MaxStep, 2.0},
		{"누적값은 한계값의 2배를 넘지 못한다", p.CUSUM.MaxS, 2.0},
	}
	for _, tt := range floats {
		t.Run(tt.name, func(t *testing.T) {
			// 계산한 값이 아니라 적어 둔 상수라서 오차 없이 같아야 한다.
			assert.InDelta(t, tt.want, tt.got, 0)
		})
	}

	t.Run("빠뜨린 값 없이 표에 다 적었다", func(t *testing.T) {
		// Score, Stage, Crisis, Trend는 일부러 이름 없이 적는다. 값이 새로 생기면 여기서 컴파일이 깨져 표를 고치게 된다.
		want := Params{
			Window:     Window{Days: 14, MinConversationDays: 7},
			Score:      Score{1, 7, 12, 5, 10, 15, 20},
			Confidence: Confidence{MediumMin: 0.4, HighMin: 0.7},
			Baseline:   Baseline{WindowDays: 14, MinConversationDays: 7},
			CUSUM:      CUSUM{K: 0.5, H: 4.0, MaxStep: 2.0, MaxS: 2.0},
			Stage:      Stage{5, 10, 15, 14},
			Crisis:     Crisis{10, 14, 3, 7},
			Trend:      Trend{20},
		}

		assert.Equal(t, want, p)
	})

	t.Run("기본값은 검사를 통과한다", func(t *testing.T) {
		require.NoError(t, p.Validate())
	})

	t.Run("받은 값을 고쳐도 다음 기본값에 영향이 없다", func(t *testing.T) {
		changed := Default()
		changed.CUSUM.MaxStep = 3
		changed.Window.Days = 21

		assert.Equal(t, p, Default())
	})
}

func TestValidateAccepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *Params)
	}{
		{"창 안의 모든 날에 대화해야 점수를 내는 설정", func(p *Params) { p.Window.MinConversationDays = p.Window.Days }},
		{"대화한 날이 하루만 있어도 점수를 내는 설정", func(p *Params) { p.Window.MinConversationDays = 1 }},
		{"3점의 경계가 창의 길이와 같다", func(p *Params) { p.Score.ItemScore3MinDays = p.Window.Days }},
		{"심함의 경계가 가장 높은 점수와 같다", func(p *Params) { p.Score.SevereMin = 24 }},
		{"3단계의 경계가 가장 높은 점수와 같다", func(p *Params) { p.Stage.Stage3MinScore = 24 }},
		{"높음의 경계가 1이다", func(p *Params) { p.Confidence.HighMin = 1 }},
		{"허용 여유가 없다", func(p *Params) { p.CUSUM.K = 0 }},
		{"하루 증가량을 묶지 않는다", func(p *Params) { p.CUSUM.MaxStep = 0 }},
		{"하루 증가량을 0.75로 묶는다", func(p *Params) { p.CUSUM.MaxStep = 0.75 }},
		{"누적값에 천장을 두지 않는다", func(p *Params) { p.CUSUM.MaxS = 0 }},
		{"누적값의 천장이 한계값을 조금만 넘는다", func(p *Params) { p.CUSUM.MaxS = 1.01 }},
		{"누적값의 천장을 한계값의 다섯 배로 둔다", func(p *Params) { p.CUSUM.MaxS = 5 }},
		{"기준선에 하루만 있어도 된다", func(p *Params) { p.Baseline.MinConversationDays = 1 }},
		{"기준선 기간의 모든 날에 대화해야 한다", func(p *Params) { p.Baseline.MinConversationDays = p.Baseline.WindowDays }},
		{"1단계가 두 번째면 2단계다", func(p *Params) { p.Crisis.RepeatCount = 2 }},
		{"비율이 1퍼센트포인트만 달라도 평소와 다르다고 말한다", func(p *Params) { p.Trend.MinDifferencePercent = 1 }},
		{"비율이 끝에서 끝까지 벌어져야 평소와 다르다고 말한다", func(p *Params) { p.Trend.MinDifferencePercent = 100 }},
		{
			"창을 21일로 늘리고 경계도 함께 옮긴다",
			func(p *Params) {
				p.Window.Days = 21
				p.Window.MinConversationDays = 10
				p.Score.ItemScore2MinDays = 10
				p.Score.ItemScore3MinDays = 18
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default()
			tt.mutate(&p)

			require.NoError(t, p.Validate())
		})
	}
}

// 계산 함수마다 검사를 하고, 개입 단계는 첫날부터 달력의 하루마다 점수와 신뢰도를 다시 구한다.
// 한 해 치 기록을 평가하면 검사만 700번 넘게 되풀이되므로, 맞는 값을 검사할 때는 메모리를 새로 잡지 않아야 한다.
func TestValidateDoesNotAllocateForValidParams(t *testing.T) {
	p := Default()
	var err error

	allocs := testing.AllocsPerRun(100, func() { err = p.Validate() })

	require.NoError(t, err)
	assert.Zero(t, allocs, "맞는 값을 검사하면서 오류 메시지에 넣을 값을 미리 묶고 있다")
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(p *Params)
		wantFields []string
	}{
		// 창
		{
			"창의 길이가 0이다",
			func(p *Params) { p.Window.Days = 0 },
			// 창이 없으면 그 안에 들어야 하는 값들도 함께 틀린다.
			[]string{"Window.Days", "Window.MinConversationDays", "Score.ItemScore3MinDays"},
		},
		{
			"창의 길이가 한 해를 넘는다",
			func(p *Params) { p.Window.Days = 367 },
			[]string{"Window.Days"},
		},
		{
			"창의 길이가 터무니없이 크다. 그만큼의 칸을 만들다 멈추지 않도록 계산 전에 막는다",
			func(p *Params) { p.Window.Days = math.MaxInt },
			[]string{"Window.Days"},
		},
		{
			"대화한 일수의 하한이 0이면 0으로 나누게 된다",
			func(p *Params) { p.Window.MinConversationDays = 0 },
			[]string{"Window.MinConversationDays"},
		},
		{
			"대화한 일수의 하한이 음수다",
			func(p *Params) { p.Window.MinConversationDays = -7 },
			[]string{"Window.MinConversationDays"},
		},
		{
			"대화한 일수의 하한이 창보다 길다",
			func(p *Params) { p.Window.MinConversationDays = 15 },
			[]string{"Window.MinConversationDays"},
		},

		// 항목 점수의 경계
		{
			"환산 일수 0일에 점수를 준다",
			func(p *Params) { p.Score.ItemScore1MinDays = 0 },
			[]string{"Score.ItemScore1MinDays"},
		},
		{
			"2점의 경계가 1점의 경계와 같다",
			func(p *Params) { p.Score.ItemScore2MinDays = 1 },
			[]string{"Score.ItemScore2MinDays"},
		},
		{
			"3점의 경계가 2점의 경계보다 낮다",
			func(p *Params) { p.Score.ItemScore3MinDays = 6 },
			[]string{"Score.ItemScore3MinDays"},
		},
		{
			"3점의 경계가 창의 길이를 넘는다",
			func(p *Params) { p.Score.ItemScore3MinDays = 15 },
			[]string{"Score.ItemScore3MinDays"},
		},

		// 구간의 경계
		{
			"가벼움의 경계가 0이다",
			func(p *Params) { p.Score.MildMin = 0 },
			[]string{"Score.MildMin"},
		},
		{
			"중간의 경계가 가벼움의 경계와 같다",
			func(p *Params) { p.Score.ModerateMin = 5 },
			[]string{"Score.ModerateMin"},
		},
		{
			"다소 심함의 경계가 중간의 경계보다 낮다",
			func(p *Params) { p.Score.ModeratelySevereMin = 9 },
			[]string{"Score.ModeratelySevereMin"},
		},
		{
			"심함의 경계가 다소 심함의 경계와 같다",
			func(p *Params) { p.Score.SevereMin = 15 },
			[]string{"Score.SevereMin"},
		},
		{
			"심함의 경계가 가장 높은 점수 24를 넘는다",
			func(p *Params) { p.Score.SevereMin = 25 },
			[]string{"Score.SevereMin"},
		},

		// 신뢰도
		{
			"보통의 경계가 0이면 낮음 구간이 없다",
			func(p *Params) { p.Confidence.MediumMin = 0 },
			[]string{"Confidence.MediumMin"},
		},
		{
			"보통의 경계가 음수다",
			func(p *Params) { p.Confidence.MediumMin = -0.1 },
			[]string{"Confidence.MediumMin"},
		},
		{
			"높음의 경계가 보통의 경계와 같다",
			func(p *Params) { p.Confidence.HighMin = 0.4 },
			[]string{"Confidence.HighMin"},
		},
		{
			"높음의 경계가 1을 넘는다",
			func(p *Params) { p.Confidence.HighMin = 1.01 },
			[]string{"Confidence.HighMin"},
		},
		{
			"보통의 경계가 숫자가 아니다",
			func(p *Params) { p.Confidence.MediumMin = math.NaN() },
			[]string{"Confidence.MediumMin"},
		},
		{
			"높음의 경계가 무한대다",
			func(p *Params) { p.Confidence.HighMin = math.Inf(1) },
			[]string{"Confidence.HighMin"},
		},

		// 기준선
		{
			"기준선 기간이 0일이다",
			func(p *Params) { p.Baseline.WindowDays = 0 },
			[]string{"Baseline.WindowDays", "Baseline.MinConversationDays"},
		},
		{
			"기준선 기간이 한 해를 넘는다",
			func(p *Params) { p.Baseline.WindowDays = 367 },
			[]string{"Baseline.WindowDays"},
		},
		{
			"기준선에 필요한 대화 일수가 0이면 평균을 낼 수 없다",
			func(p *Params) { p.Baseline.MinConversationDays = 0 },
			[]string{"Baseline.MinConversationDays"},
		},
		{
			"기준선에 필요한 대화 일수가 기간보다 길다",
			func(p *Params) { p.Baseline.MinConversationDays = 15 },
			[]string{"Baseline.MinConversationDays"},
		},

		// 변화 탐지
		{
			"허용 여유가 음수다",
			func(p *Params) { p.CUSUM.K = -0.5 },
			[]string{"CUSUM.K"},
		},
		{
			"허용 여유가 숫자가 아니다",
			func(p *Params) { p.CUSUM.K = math.NaN() },
			[]string{"CUSUM.K"},
		},
		{
			"한계값이 0이다",
			func(p *Params) { p.CUSUM.H = 0 },
			[]string{"CUSUM.H"},
		},
		{
			"한계값이 음수다",
			func(p *Params) { p.CUSUM.H = -4 },
			[]string{"CUSUM.H"},
		},
		{
			"한계값이 무한대면 변화를 영영 감지하지 못한다",
			func(p *Params) { p.CUSUM.H = math.Inf(1) },
			[]string{"CUSUM.H"},
		},
		{
			"하루 증가량의 상한이 음수다",
			func(p *Params) { p.CUSUM.MaxStep = -1 },
			[]string{"CUSUM.MaxStep"},
		},
		{
			"하루 증가량의 상한이 숫자가 아니다",
			func(p *Params) { p.CUSUM.MaxStep = math.NaN() },
			[]string{"CUSUM.MaxStep"},
		},
		{
			"누적값의 천장이 한계값과 같으면 누적값이 한계값을 넘는 날이 오지 않는다",
			func(p *Params) { p.CUSUM.MaxS = 1 },
			[]string{"CUSUM.MaxS"},
		},
		{
			"누적값의 천장이 한계값보다 낮다",
			func(p *Params) { p.CUSUM.MaxS = 0.5 },
			[]string{"CUSUM.MaxS"},
		},
		{
			"누적값의 천장이 음수다",
			func(p *Params) { p.CUSUM.MaxS = -2 },
			[]string{"CUSUM.MaxS"},
		},
		{
			"누적값의 천장이 숫자가 아니다",
			func(p *Params) { p.CUSUM.MaxS = math.NaN() },
			[]string{"CUSUM.MaxS"},
		},
		{
			"누적값의 천장이 무한대다. 천장을 두지 않으려면 0을 쓴다",
			func(p *Params) { p.CUSUM.MaxS = math.Inf(1) },
			[]string{"CUSUM.MaxS"},
		},

		// 개입 단계
		{
			"1단계의 경계가 0이면 0단계가 없다",
			func(p *Params) { p.Stage.Stage1MinScore = 0 },
			[]string{"Stage.Stage1MinScore"},
		},
		{
			"2단계의 경계가 1단계의 경계와 같다",
			func(p *Params) { p.Stage.Stage2MinScore = 5 },
			[]string{"Stage.Stage2MinScore"},
		},
		{
			"3단계의 경계가 2단계의 경계보다 낮다",
			func(p *Params) { p.Stage.Stage3MinScore = 9 },
			[]string{"Stage.Stage3MinScore"},
		},
		{
			"3단계의 경계가 가장 높은 점수 24를 넘는다",
			func(p *Params) { p.Stage.Stage3MinScore = 25 },
			[]string{"Stage.Stage3MinScore"},
		},
		{
			"2단계가 이어진 일수의 기준이 0이다",
			func(p *Params) { p.Stage.SustainedStage2Days = 0 },
			[]string{"Stage.SustainedStage2Days"},
		},
		{
			"이어진 일수의 기준이 한 해를 넘는다",
			func(p *Params) { p.Stage.SustainedStage2Days = 367 },
			[]string{"Stage.SustainedStage2Days"},
		},

		// 위기 관문
		{
			"판정을 올리는 점수 기준이 0이면 언제나 올린다",
			func(p *Params) { p.Crisis.EscalationMinScore = 0 },
			[]string{"Crisis.EscalationMinScore"},
		},
		{
			"판정을 올리는 점수 기준이 가장 높은 점수 24를 넘는다",
			func(p *Params) { p.Crisis.EscalationMinScore = 25 },
			[]string{"Crisis.EscalationMinScore"},
		},
		{
			"되풀이를 세는 기간이 0일이다",
			func(p *Params) { p.Crisis.RepeatWindowDays = 0 },
			[]string{"Crisis.RepeatWindowDays"},
		},
		{
			"되풀이 횟수가 1이면 되묻고 듣는 1단계가 사라진다",
			func(p *Params) { p.Crisis.RepeatCount = 1 },
			[]string{"Crisis.RepeatCount"},
		},
		{
			"민감 기간이 0일이다",
			func(p *Params) { p.Crisis.SensitiveWindowDays = 0 },
			[]string{"Crisis.SensitiveWindowDays"},
		},

		// 추세 화면의 비교
		{
			"평소와의 차이 기준이 0이면 비율이 같아도 잦음이 된다",
			func(p *Params) { p.Trend.MinDifferencePercent = 0 },
			[]string{"Trend.MinDifferencePercent"},
		},
		{
			"평소와의 차이 기준이 음수다",
			func(p *Params) { p.Trend.MinDifferencePercent = -20 },
			[]string{"Trend.MinDifferencePercent"},
		},
		{
			"평소와의 차이 기준이 100퍼센트포인트를 넘으면 영영 닿지 않는다",
			func(p *Params) { p.Trend.MinDifferencePercent = 101 },
			[]string{"Trend.MinDifferencePercent"},
		},

		{
			"빈 값은 거의 모든 자리가 틀렸다고 나온다",
			func(p *Params) { *p = Params{} },
			[]string{
				"Window.Days", "Window.MinConversationDays",
				"Score.ItemScore1MinDays", "Score.ItemScore2MinDays", "Score.ItemScore3MinDays",
				"Score.MildMin", "Score.ModerateMin", "Score.ModeratelySevereMin", "Score.SevereMin",
				"Confidence.MediumMin", "Confidence.HighMin",
				"Baseline.WindowDays", "Baseline.MinConversationDays",
				"CUSUM.H",
				"Stage.Stage1MinScore", "Stage.Stage2MinScore", "Stage.Stage3MinScore", "Stage.SustainedStage2Days",
				"Crisis.EscalationMinScore", "Crisis.RepeatWindowDays", "Crisis.RepeatCount", "Crisis.SensitiveWindowDays",
				"Trend.MinDifferencePercent",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default()
			tt.mutate(&p)

			err := p.Validate()

			require.Error(t, err)
			assert.Equal(t, tt.wantFields, fieldsOf(t, err), "틀린 값을 하나도 빠뜨리지 않고, 맞는 값을 끼워 넣지 않는다")
			for _, field := range tt.wantFields {
				assert.Contains(t, err.Error(), field, "메시지에서 어느 값인지 읽을 수 있어야 한다")
			}
		})
	}
}

func TestValidateCollectsEveryError(t *testing.T) {
	p := Default()
	p.Window.MinConversationDays = 0
	p.CUSUM.H = -1
	p.Crisis.RepeatCount = 0

	err := p.Validate()

	t.Run("하나 찾고 멈추지 않고 전부 모은다", func(t *testing.T) {
		assert.Equal(t, []string{"Window.MinConversationDays", "CUSUM.H", "Crisis.RepeatCount"}, fieldsOf(t, err))
	})

	t.Run("errors.As로 첫 번째 값을 꺼낼 수 있다", func(t *testing.T) {
		var fieldErr *FieldError

		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, "Window.MinConversationDays", fieldErr.Field)
		assert.Equal(t, "must be at least 1, got 0", fieldErr.Reason)
		assert.Equal(t, "params: Window.MinConversationDays must be at least 1, got 0", fieldErr.Error())
	})
}

// fieldsOf는 Validate가 돌려준 오류에서 값의 이름을 나온 순서대로 꺼낸다. 같은 이름은 한 번만 적는다.
func fieldsOf(t *testing.T, err error) []string {
	t.Helper()

	var joined interface{ Unwrap() []error }
	require.ErrorAs(t, err, &joined, "여러 오류를 묶은 값이어야 한다")

	var fields []string
	for _, each := range joined.Unwrap() {
		var fieldErr *FieldError
		require.ErrorAs(t, each, &fieldErr, "묶인 오류는 모두 FieldError여야 한다")
		if len(fields) == 0 || fields[len(fields)-1] != fieldErr.Field {
			fields = append(fields, fieldErr.Field)
		}
	}
	return fields
}
