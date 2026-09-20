package stage

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// yesterday는 전날에서 넘어오는 값만 채운 Point다.
func yesterday(stage Stage, elevatedDays int) Point {
	return Point{Stage: stage, ElevatedDays: elevatedDays}
}

// scored는 기록이 충분해 추정 점수가 나온 날이다. 그날도 대화했고, 창 안의 열흘에 대화했다고 둔다.
func scored(total int, level confidence.Level, detected bool) input {
	return input{hasRecord: true, conversationDays: 10, score: total, hasScore: true, confidence: level, detected: detected}
}

// unscored는 기록 부족인 날이다. 그날도 대화했고, 창 안의 나흘에만 대화했다고 둔다. 기록 부족이면 신뢰도는 언제나 낮음이다.
func unscored(detected bool) input {
	return input{hasRecord: true, conversationDays: 4, confidence: confidence.Low, detected: detected}
}

// silent는 같은 값을 그날 대화하지 않은 날로 바꾼다. 창 안의 다른 날들의 기록으로 점수와 신뢰도는 그대로 나온다.
func silent(in input) input {
	in.hasRecord = false
	return in
}

type stepCase struct {
	name     string
	previous Point
	in       input

	wantRaw      Stage
	wantStage    Stage
	wantHeld     bool
	wantElevated int
	wantReasons  []Reason
}

func runStepCases(t *testing.T, p params.Stage, tests []stepCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := step(tt.previous, tt.in, p)

			assert.Equal(t, tt.wantRaw, got.Raw, "그날의 값만으로 본 단계")
			assert.Equal(t, tt.wantStage, got.Stage, "그날의 단계")
			assert.Equal(t, tt.wantHeld, got.Held, "묶였는지")
			assert.Equal(t, tt.wantElevated, got.ElevatedDays, "2단계 이상이 이어진 일수")
			assert.Equal(t, tt.wantReasons, got.Reasons, "단계를 움직인 조건")
			assert.Equal(t, tt.in.hasRecord, got.HasRecord, "그날의 기록이 있는지")
		})
	}
}

func TestStepScoreSetsTheStage(t *testing.T) {
	none := []Reason{}
	byScore := []Reason{ReasonScore}

	// 하루에 한 단계만 오르므로, 점수의 경계만 보려고 전날을 바로 아래 단계에 둔다.
	runStepCases(t, params.Default().Stage, []stepCase{
		{"추정 점수 0이면 0단계다", yesterday(Everyday, 0), scored(0, confidence.Medium, false), Everyday, Everyday, false, 0, none},
		{"추정 점수 4까지 0단계다", yesterday(Everyday, 0), scored(4, confidence.Medium, false), Everyday, Everyday, false, 0, none},
		{"추정 점수 5부터 1단계다", yesterday(Everyday, 0), scored(5, confidence.Medium, false), Reflection, Reflection, false, 0, byScore},
		{"추정 점수 9까지 1단계다", yesterday(Everyday, 0), scored(9, confidence.Medium, false), Reflection, Reflection, false, 0, byScore},
		{"추정 점수 10부터 2단계다", yesterday(Reflection, 0), scored(10, confidence.Medium, false), Suggestion, Suggestion, false, 1, byScore},
		{"추정 점수 14까지 2단계다", yesterday(Reflection, 0), scored(14, confidence.Medium, false), Suggestion, Suggestion, false, 1, byScore},
		{"추정 점수 15부터 3단계다", yesterday(Suggestion, 3), scored(15, confidence.Medium, false), Recommendation, Recommendation, false, 4, byScore},
		{"추정 점수 24도 3단계다", yesterday(Suggestion, 3), scored(24, confidence.High, false), Recommendation, Recommendation, false, 4, byScore},
		{"점수가 내려가면 단계도 그날 내려간다. 내려가는 것은 몇 단계든 막지 않는다", yesterday(Recommendation, 9), scored(6, confidence.High, false), Reflection, Reflection, false, 0, byScore},
		{"점수가 0단계 구간이면 3단계에서도 그날 0단계다", yesterday(Recommendation, 9), scored(2, confidence.High, false), Everyday, Everyday, false, 0, none},
	})
}

func TestStepRisesOneStepPerDay(t *testing.T) {
	oneStep := []Reason{ReasonScore, ReasonHeldOneStepPerDay}

	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"0단계에서 점수가 2단계 구간이면 그날은 1단계까지만 오르고, 덜 올랐다고 표시한다",
			yesterday(Everyday, 0), scored(12, confidence.Medium, false),
			Suggestion, Reflection, true, 0, oneStep,
		},
		{
			"0단계에서 점수가 3단계 구간이어도 그날은 1단계다. 신뢰도가 높아도 같다",
			yesterday(Everyday, 0), scored(20, confidence.High, false),
			Recommendation, Reflection, true, 0, oneStep,
		},
		{
			"1단계에서 점수가 3단계 구간이면 그날은 2단계이고, 이어진 일수를 세기 시작한다",
			yesterday(Reflection, 0), scored(20, confidence.High, false),
			Recommendation, Suggestion, true, 1, oneStep,
		},
		{
			"2단계에서 3단계는 한 단계라 그대로 오른다",
			yesterday(Suggestion, 1), scored(20, confidence.High, false),
			Recommendation, Recommendation, false, 2, []Reason{ReasonScore},
		},
		{
			"변화 감지로 오르는 1단계는 한 단계라 걸리지 않는다",
			yesterday(Everyday, 0), scored(3, confidence.High, true),
			Reflection, Reflection, false, 0, []Reason{ReasonChangeDetected},
		},
	})
}

func TestStepRisesOnlyOnDaysWithARecord(t *testing.T) {
	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"대화하지 않은 날에는 창이 움직여 점수가 올라도 단계가 오르지 않는다",
			yesterday(Reflection, 0), silent(scored(12, confidence.Medium, false)),
			Suggestion, Reflection, true, 0, []Reason{ReasonScore, ReasonHeldNoRecordToday},
		},
		{
			"0단계에서도 같다",
			yesterday(Everyday, 0), silent(scored(7, confidence.High, false)),
			Reflection, Everyday, true, 0, []Reason{ReasonScore, ReasonHeldNoRecordToday},
		},
		{
			"두 단계를 오르려던 날이어도 까닭은 그날의 기록이 없다는 것 하나만 적는다",
			yesterday(Everyday, 0), silent(scored(20, confidence.High, false)),
			Recommendation, Everyday, true, 0, []Reason{ReasonScore, ReasonHeldNoRecordToday},
		},
		{
			"변화 감지로 오르려던 1단계도 대화하지 않은 날에는 묶인다",
			yesterday(Everyday, 0), silent(scored(2, confidence.Medium, true)),
			Reflection, Everyday, true, 0, []Reason{ReasonChangeDetected, ReasonHeldNoRecordToday},
		},
		{
			"대화하지 않은 날에도 같은 단계는 그대로이고, 2단계 이상이 이어진 일수도 센다",
			yesterday(Suggestion, 5), silent(scored(12, confidence.Medium, false)),
			Suggestion, Suggestion, false, 6, []Reason{ReasonScore},
		},
		{
			"대화하지 않은 날에도 내려가는 것은 막지 않는다",
			yesterday(Suggestion, 5), silent(scored(7, confidence.Medium, false)),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"대화하지 않았고 신뢰도도 낮으면 기록이 더 쌓여야 풀리는 쪽인 신뢰도를 까닭으로 적는다",
			yesterday(Everyday, 0), silent(scored(7, confidence.Low, false)),
			Reflection, Everyday, true, 0, []Reason{ReasonScore, ReasonHeldLowConfidence},
		},
	})
}

func TestStepChangeDetectedFloorsAtOne(t *testing.T) {
	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"점수가 0이어도 변화 감지 상태면 1단계다",
			yesterday(Everyday, 0), scored(0, confidence.Medium, true),
			Reflection, Reflection, false, 0, []Reason{ReasonChangeDetected},
		},
		{
			"점수가 4여도 변화 감지 상태면 1단계다",
			yesterday(Everyday, 0), scored(4, confidence.High, true),
			Reflection, Reflection, false, 0, []Reason{ReasonChangeDetected},
		},
		{
			"점수만으로 이미 1단계면 변화 감지는 단계를 바꾸지 않으므로 적지 않는다",
			yesterday(Everyday, 0), scored(5, confidence.Medium, true),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"변화 감지는 2단계로 올리지 않는다",
			yesterday(Reflection, 0), scored(9, confidence.High, true),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"점수로 2단계인 날에는 변화 감지가 보탤 것이 없다",
			yesterday(Reflection, 0), scored(12, confidence.High, true),
			Suggestion, Suggestion, false, 1, []Reason{ReasonScore},
		},
		{
			"변화 감지가 풀리고 점수도 낮으면 0단계로 돌아간다",
			yesterday(Reflection, 0), scored(3, confidence.Medium, false),
			Everyday, Everyday, false, 0, []Reason{},
		},
	})
}

func TestStepSustainedElevation(t *testing.T) {
	sustained := []Reason{ReasonScore, ReasonSustained}

	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"2단계 열사흘째는 아직 2단계다",
			yesterday(Suggestion, 12), scored(12, confidence.Medium, false),
			Suggestion, Suggestion, false, 13, []Reason{ReasonScore},
		},
		{
			"2단계 열나흘째가 되는 날부터 3단계다",
			yesterday(Suggestion, 13), scored(12, confidence.Medium, false),
			Recommendation, Recommendation, false, 14, sustained,
		},
		{
			"그렇게 3단계가 된 뒤에도 점수가 2단계 구간에 머무는 동안은 3단계다",
			yesterday(Recommendation, 30), scored(11, confidence.Medium, false),
			Recommendation, Recommendation, false, 31, sustained,
		},
		{
			"점수만으로 이미 3단계인 날에는 이어진 일수를 조건으로 적지 않는다",
			yesterday(Recommendation, 30), scored(17, confidence.Medium, false),
			Recommendation, Recommendation, false, 31, []Reason{ReasonScore},
		},
		{
			"3단계로 보낸 날도 2단계 이상이 이어진 날로 센다",
			yesterday(Recommendation, 13), scored(12, confidence.Medium, false),
			Recommendation, Recommendation, false, 14, sustained,
		},
		{
			"점수로 3단계였다가 2단계 구간으로 내려왔고 아직 열나흘이 안 됐으면 2단계다",
			yesterday(Recommendation, 5), scored(12, confidence.Medium, false),
			Suggestion, Suggestion, false, 6, []Reason{ReasonScore},
		},
		{
			"점수가 1단계 구간으로 내려가면 오래 이어졌어도 1단계다",
			yesterday(Recommendation, 40), scored(8, confidence.Medium, false),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"이어진 일수가 끊기면 다시 처음부터 센다",
			yesterday(Reflection, 0), scored(12, confidence.Medium, false),
			Suggestion, Suggestion, false, 1, []Reason{ReasonScore},
		},
		{
			"변화 감지로 받친 1단계는 이어진 일수와 상관없다",
			yesterday(Suggestion, 40), scored(3, confidence.Medium, true),
			Reflection, Reflection, false, 0, []Reason{ReasonChangeDetected},
		},
		{
			"열나흘째가 대화하지 않은 날이면 2단계에 머물고, 그날도 이어진 일수로는 센다",
			yesterday(Suggestion, 13), silent(scored(12, confidence.Medium, false)),
			Recommendation, Suggestion, true, 14, []Reason{ReasonScore, ReasonSustained, ReasonHeldNoRecordToday},
		},
		{
			"그 뒤에 대화한 날 3단계가 된다",
			yesterday(Suggestion, 16), scored(12, confidence.Medium, false),
			Recommendation, Recommendation, false, 17, sustained,
		},
	})

	t.Run("이어진 일수의 기준은 조정 값에서 받는다", func(t *testing.T) {
		p := params.Default().Stage
		p.SustainedStage2Days = 7

		runStepCases(t, p, []stepCase{
			{
				"엿새째는 2단계다",
				yesterday(Suggestion, 5), scored(12, confidence.Medium, false),
				Suggestion, Suggestion, false, 6, []Reason{ReasonScore},
			},
			{
				"이레째에 3단계가 된다",
				yesterday(Suggestion, 6), scored(12, confidence.Medium, false),
				Recommendation, Recommendation, false, 7, sustained,
			},
		})
	})

	t.Run("기준을 1로 두면 점수가 2단계 구간에 든 날부터 곧바로 3단계를 향한다", func(t *testing.T) {
		// 조정 값 검사가 받아 주는 가장 작은 값이다. 2단계에 머무는 날이 거의 없어지지만 하루에 한 단계는 그대로 지킨다.
		// 아직 2단계가 아닌 날에도 "이어졌다"는 조건이 적히는 것은 이 값에서만 생기는 일이다. 단계의 흐름은 달라지지 않는다.
		p := params.Default().Stage
		p.SustainedStage2Days = 1
		oneStep := []Reason{ReasonScore, ReasonSustained, ReasonHeldOneStepPerDay}

		runStepCases(t, p, []stepCase{
			{
				"0단계에서는 그날 1단계까지만 오른다",
				yesterday(Everyday, 0), scored(12, confidence.Medium, false),
				Recommendation, Reflection, true, 0, oneStep,
			},
			{
				"1단계에서는 그날 2단계가 되고 이어진 일수를 세기 시작한다",
				yesterday(Reflection, 0), scored(12, confidence.Medium, false),
				Recommendation, Suggestion, true, 1, oneStep,
			},
			{
				"2단계에서는 그날 3단계가 된다",
				yesterday(Suggestion, 1), scored(12, confidence.Medium, false),
				Recommendation, Recommendation, false, 2, sustained,
			},
		})
	})
}

func TestStepHoldsWhenConfidenceIsLow(t *testing.T) {
	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"신뢰도가 낮으면 1단계로 올라가지 못하고 묶였다고 표시한다",
			yesterday(Everyday, 0), scored(7, confidence.Low, false),
			Reflection, Everyday, true, 0, []Reason{ReasonScore, ReasonHeldLowConfidence},
		},
		{
			"신뢰도가 보통이면 올라간다",
			yesterday(Everyday, 0), scored(7, confidence.Medium, false),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"신뢰도가 높아도 올라간다",
			yesterday(Everyday, 0), scored(7, confidence.High, false),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"신뢰도가 낮으면 1단계에서 2단계로도 올라가지 못한다",
			yesterday(Reflection, 0), scored(12, confidence.Low, false),
			Suggestion, Reflection, true, 0, []Reason{ReasonScore, ReasonHeldLowConfidence},
		},
		{
			"신뢰도가 낮으면 점수가 아무리 높아도 전날의 단계에 머문다. 한 단계도 오르지 않는다",
			yesterday(Everyday, 0), scored(20, confidence.Low, false),
			Recommendation, Everyday, true, 0, []Reason{ReasonScore, ReasonHeldLowConfidence},
		},
		{
			"전날과 같은 단계는 올리는 것이 아니므로 신뢰도가 낮아도 그대로다",
			yesterday(Suggestion, 5), scored(12, confidence.Low, false),
			Suggestion, Suggestion, false, 6, []Reason{ReasonScore},
		},
		{
			"신뢰도가 낮아도 내려가는 것은 막지 않는다",
			yesterday(Suggestion, 5), scored(7, confidence.Low, false),
			Reflection, Reflection, false, 0, []Reason{ReasonScore},
		},
		{
			"신뢰도가 낮아도 0단계까지 내려갈 수 있다",
			yesterday(Recommendation, 5), scored(2, confidence.Low, false),
			Everyday, Everyday, false, 0, []Reason{},
		},
		{
			"변화 감지로 올라가려던 1단계도 신뢰도가 낮으면 묶인다",
			yesterday(Everyday, 0), scored(2, confidence.Low, true),
			Reflection, Everyday, true, 0, []Reason{ReasonChangeDetected, ReasonHeldLowConfidence},
		},
		{
			"열나흘째에 3단계가 되려던 날도 신뢰도가 낮으면 2단계에 묶이고 이어진 일수는 계속 센다",
			yesterday(Suggestion, 13), scored(12, confidence.Low, false),
			Recommendation, Suggestion, true, 14, []Reason{ReasonScore, ReasonSustained, ReasonHeldLowConfidence},
		},
		{
			"묶여 있던 다음 날 신뢰도가 보통이 되면 그날 3단계가 된다",
			yesterday(Suggestion, 14), scored(12, confidence.Medium, false),
			Recommendation, Recommendation, false, 15, []Reason{ReasonScore, ReasonSustained},
		},
	})
}

func TestStepCarriesTheStageWhenRecordsAreInsufficient(t *testing.T) {
	carried := []Reason{ReasonCarriedInsufficientRecords}

	runStepCases(t, params.Default().Stage, []stepCase{
		{
			"0단계에서 기록 부족이면 0단계 그대로다. 움직인 것이 없으니 적을 조건도 없다",
			yesterday(Everyday, 0), unscored(false),
			Everyday, Everyday, false, 0, []Reason{},
		},
		{
			"기록 부족이면 변화 감지 상태여도 올라가지 못하고, 까닭은 기록 부족으로 적는다",
			yesterday(Everyday, 0), unscored(true),
			Reflection, Everyday, true, 0, []Reason{ReasonChangeDetected, ReasonHeldInsufficientRecords},
		},
		{
			"기록 부족이 되어도 2단계를 그대로 이어 간다. 이어진 일수는 그날을 세지 않고 끊지도 않는다",
			yesterday(Suggestion, 9), unscored(false),
			Everyday, Suggestion, false, 9, carried,
		},
		{
			"3단계도 그대로 이어 간다",
			yesterday(Recommendation, 20), unscored(false),
			Everyday, Recommendation, false, 20, carried,
		},
		{
			"1단계도 그대로 이어 간다",
			yesterday(Reflection, 0), unscored(false),
			Everyday, Reflection, false, 0, carried,
		},
		{
			"변화 감지 상태여도 전날의 2단계에서 내려가지 않는다",
			yesterday(Suggestion, 9), unscored(true),
			Reflection, Suggestion, false, 9, []Reason{ReasonChangeDetected, ReasonCarriedInsufficientRecords},
		},
		{
			"변화 감지로 받친 1단계와 전날의 1단계가 같으면 이어 간 것이라고 따로 적지 않는다",
			yesterday(Reflection, 0), unscored(true),
			Reflection, Reflection, false, 0, []Reason{ReasonChangeDetected},
		},
		{
			"이어진 일수가 열사흘인 채로 기록 부족이면 3단계가 되지 않는다. 일수도 열사흘 그대로다",
			yesterday(Suggestion, 13), unscored(false),
			Everyday, Suggestion, false, 13, carried,
		},
		{
			"기록 부족인 날은 그날 대화했든 안 했든 같다",
			yesterday(Suggestion, 9), silent(unscored(false)),
			Everyday, Suggestion, false, 9, carried,
		},
		{
			"기록 부족이고 그날 대화하지도 않았으면 까닭은 기록 부족으로 적는다",
			yesterday(Everyday, 0), silent(unscored(true)),
			Reflection, Everyday, true, 0, []Reason{ReasonChangeDetected, ReasonHeldInsufficientRecords},
		},
		{
			"기록 부족인 날에는 점수 자리에 무엇이 들어와도 보지 않는다",
			yesterday(Everyday, 0),
			input{hasRecord: true, conversationDays: 4, score: 20, hasScore: false, confidence: confidence.Low},
			Everyday, Everyday, false, 0, []Reason{},
		},
	})

	t.Run("기록 부족인 날의 점수는 0으로 비워 둔다", func(t *testing.T) {
		got := step(Point{}, input{conversationDays: 4, score: 20, hasScore: false}, params.Default().Stage)

		assert.True(t, got.Insufficient)
		assert.Zero(t, got.Score)
	})
}

func TestStepReturnsToZeroWithoutRecentRecords(t *testing.T) {
	empty := func(detected bool) input {
		return input{conversationDays: 0, confidence: confidence.Low, detected: detected}
	}
	onlyThis := []Reason{ReasonNoRecentRecords}

	runStepCases(t, params.Default().Stage, []stepCase{
		{"3단계였어도 창이 비면 0단계이고 이어진 일수도 0이 된다", yesterday(Recommendation, 40), empty(false), Everyday, Everyday, false, 0, onlyThis},
		{"변화 감지가 켜진 채여도 창이 비면 0단계다", yesterday(Reflection, 0), empty(true), Everyday, Everyday, false, 0, onlyThis},
		{"0단계였다면 그대로 0단계다", yesterday(Everyday, 0), empty(false), Everyday, Everyday, false, 0, onlyThis},
		{"묶였다는 표시는 켜지 않는다. 오르려던 것이 아니라 돌아간 것이다", yesterday(Everyday, 0), empty(true), Everyday, Everyday, false, 0, onlyThis},
	})

	t.Run("창 안에 하루라도 있으면 이 규칙은 적용하지 않고, 기록 부족이라 전날의 단계를 이어 간다", func(t *testing.T) {
		got := step(yesterday(Suggestion, 8), input{conversationDays: 1, confidence: confidence.Low}, params.Default().Stage)

		assert.Equal(t, Suggestion, got.Stage)
		assert.Equal(t, 8, got.ElevatedDays)
		assert.Equal(t, []Reason{ReasonCarriedInsufficientRecords}, got.Reasons)
	})
}

func TestStepCopiesWhatItSaw(t *testing.T) {
	date := mustDate(t, "2026-09-20")

	got := step(Point{}, input{
		date:             date,
		hasRecord:        true,
		conversationDays: 11,
		score:            13,
		hasScore:         true,
		confidence:       confidence.High,
		detected:         true,
	}, params.Default().Stage)

	want := Point{
		Date:             date,
		HasRecord:        true,
		ConversationDays: 11,
		Score:            13,
		Confidence:       confidence.High,
		Detected:         true,
		Raw:              Suggestion,
		// 첫날의 전날은 0단계라서 그날은 한 단계만 오른다.
		Stage:   Reflection,
		Held:    true,
		Reasons: []Reason{ReasonScore, ReasonHeldOneStepPerDay},
	}
	assert.Equal(t, want, got)
}

func TestPointHeldBy(t *testing.T) {
	p := params.Default().Stage

	tests := []struct {
		name     string
		previous Point
		in       input
		want     Reason
	}{
		{"묶이지 않은 날은 빈 값이다", yesterday(Everyday, 0), scored(7, confidence.Medium, false), 0},
		{"그날의 기록이 없어서", yesterday(Everyday, 0), silent(scored(7, confidence.Medium, false)), ReasonHeldNoRecordToday},
		{"하루에 한 단계라서", yesterday(Everyday, 0), scored(12, confidence.Medium, false), ReasonHeldOneStepPerDay},
		{"신뢰도가 낮아서", yesterday(Everyday, 0), scored(12, confidence.Low, false), ReasonHeldLowConfidence},
		{"기록 부족이어서", yesterday(Everyday, 0), unscored(true), ReasonHeldInsufficientRecords},
		{"기록 부족이어서 이어 간 날은 묶인 날이 아니다", yesterday(Suggestion, 3), unscored(false), 0},
		{"창이 비어 0단계로 돌아간 날도 묶인 날이 아니다", yesterday(Suggestion, 3), input{confidence: confidence.Low, detected: true}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := step(tt.previous, tt.in, p)

			assert.Equal(t, tt.want, got.HeldBy())
			assert.Equal(t, tt.want != 0, got.Held)
		})
	}

	t.Run("빈 Point도 묶인 날이 아니다", func(t *testing.T) {
		assert.Equal(t, Reason(0), Point{}.HeldBy())
	})
}

// 하루씩 이어 돌렸을 때의 흐름을 본다. 한 줄이 달력의 하루다.
func TestStepChain(t *testing.T) {
	run := func(inputs []input) []Point {
		var previous Point
		series := make([]Point, 0, len(inputs))
		for _, in := range inputs {
			previous = step(previous, in, params.Default().Stage)
			series = append(series, previous)
		}
		return series
	}
	repeat := func(n int, in input) []input {
		out := make([]input, n)
		for i := range out {
			out[i] = in
		}
		return out
	}

	t.Run("점수가 처음부터 2단계 구간이면 하루에 한 단계씩 올라 이틀째에 2단계가 되고, 거기서 열나흘째에 3단계가 된다", func(t *testing.T) {
		series := run(repeat(16, scored(12, confidence.Medium, false)))

		want := append([]Stage{Reflection}, repeatStage(13, Suggestion)...)
		want = append(want, Recommendation, Recommendation)
		assert.Equal(t, want, stagesOf(series))
		assert.Equal(t, 13, series[13].ElevatedDays)
		assert.Equal(t, 14, series[14].ElevatedDays, "3단계가 된 날이 2단계 이상의 열나흘째다")
		assert.Equal(t, 15, series[15].ElevatedDays)
	})

	t.Run("점수가 처음부터 3단계 구간이어도 0, 1, 2, 3의 순서를 거친다", func(t *testing.T) {
		series := run(repeat(4, scored(20, confidence.High, false)))

		assert.Equal(t, []Stage{Reflection, Suggestion, Recommendation, Recommendation}, stagesOf(series))
		assert.Equal(t, ReasonHeldOneStepPerDay, series[0].HeldBy())
		assert.Equal(t, ReasonHeldOneStepPerDay, series[1].HeldBy())
		assert.False(t, series[2].Held)
	})

	t.Run("중간에 하루라도 1단계로 내려가면 14일을 다시 센다", func(t *testing.T) {
		inputs := append(repeat(10, scored(12, confidence.Medium, false)), scored(8, confidence.Medium, false))
		inputs = append(inputs, repeat(15, scored(12, confidence.Medium, false))...)

		series := run(inputs)

		want := append([]Stage{Reflection}, repeatStage(9, Suggestion)...)
		want = append(want, Reflection)
		want = append(want, repeatStage(13, Suggestion)...)
		want = append(want, Recommendation, Recommendation)
		assert.Equal(t, want, stagesOf(series))
	})

	t.Run("신뢰도가 낮은 동안 묶여 있다가 보통이 되는 날부터 하루에 한 단계씩 올라간다", func(t *testing.T) {
		inputs := append(repeat(5, scored(12, confidence.Low, false)), repeat(2, scored(12, confidence.Medium, false))...)

		series := run(inputs)

		assert.Equal(t, append(repeatStage(5, Everyday), Reflection, Suggestion), stagesOf(series))
		for i := range 5 {
			assert.Equal(t, ReasonHeldLowConfidence, series[i].HeldBy(), "%d번째 날", i+1)
			assert.Equal(t, Suggestion, series[i].Raw)
		}
		assert.Equal(t, ReasonHeldOneStepPerDay, series[5].HeldBy())
		assert.False(t, series[6].Held)
	})

	t.Run("올라간 뒤에 신뢰도가 낮아져도 그 단계에 머문다", func(t *testing.T) {
		inputs := append(repeat(2, scored(12, confidence.Medium, false)), repeat(3, scored(12, confidence.Low, false))...)

		series := run(inputs)

		assert.Equal(t, append([]Stage{Reflection}, repeatStage(4, Suggestion)...), stagesOf(series))
		for _, pt := range series[1:] {
			assert.False(t, pt.Held)
		}
	})

	t.Run("묶여서 2단계가 되지 못한 날은 이어진 일수로 세지 않는다", func(t *testing.T) {
		inputs := append(repeat(20, scored(12, confidence.Low, false)), repeat(2, scored(12, confidence.Medium, false))...)

		series := run(inputs)

		last := series[len(series)-1]
		assert.Equal(t, Suggestion, last.Stage, "점수로 본 단계가 2단계였던 날이 스무 날이어도 실제 단계는 0이었다")
		assert.Equal(t, 1, last.ElevatedDays)
	})

	t.Run("기록 부족인 날이 끼어도 단계와 이어진 일수가 끊기지 않고, 그날은 세지 않는다", func(t *testing.T) {
		inputs := append(repeat(5, scored(12, confidence.Medium, false)), repeat(2, unscored(false))...)
		inputs = append(inputs, scored(12, confidence.Medium, false))

		series := run(inputs)

		assert.Equal(t, append([]Stage{Reflection}, repeatStage(7, Suggestion)...), stagesOf(series))
		elevated := make([]int, 0, len(series))
		for _, pt := range series {
			elevated = append(elevated, pt.ElevatedDays)
		}
		assert.Equal(t, []int{0, 1, 2, 3, 4, 4, 4, 5}, elevated)
	})

	t.Run("대화하지 않은 날에는 머물다가 다음에 대화한 날 오른다", func(t *testing.T) {
		talk, quiet := scored(12, confidence.Medium, false), silent(scored(12, confidence.Medium, false))

		series := run([]input{talk, quiet, quiet, talk, talk})

		assert.Equal(t, []Stage{Reflection, Reflection, Reflection, Suggestion, Suggestion}, stagesOf(series))
		assert.Equal(t, ReasonHeldNoRecordToday, series[1].HeldBy())
		assert.Equal(t, ReasonHeldNoRecordToday, series[2].HeldBy())
	})

	t.Run("창이 비면 이어진 일수도 0에서 다시 센다", func(t *testing.T) {
		inputs := append(repeat(6, scored(12, confidence.Medium, false)), input{confidence: confidence.Low})
		inputs = append(inputs, repeat(3, scored(12, confidence.Medium, false))...)

		series := run(inputs)

		assert.Equal(t, 5, series[5].ElevatedDays)
		assert.Equal(t, Everyday, series[6].Stage)
		assert.Zero(t, series[6].ElevatedDays)
		assert.Equal(t, []Stage{Reflection, Suggestion, Suggestion}, stagesOf(series[7:]))
		assert.Equal(t, 2, series[9].ElevatedDays)
	})
}

func repeatStage(n int, s Stage) []Stage {
	out := make([]Stage, n)
	for i := range out {
		out[i] = s
	}
	return out
}
