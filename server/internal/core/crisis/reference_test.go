package crisis_test

// 이 파일은 위기 관문의 판정 규칙을 일부러 단순하게 다시 적은 참조 구현과, 그것을 실제 구현과 견주는 시험이다.
//
// 참조 구현은 단계를 패키지의 타입이 아니라 맨 숫자로 들고, 규칙을 적힌 순서대로 한 줄씩 적용한다.
// 기간은 시간 단위의 소수로 견준다. 관문의 입력은 씨앗이 정해진 난수로 만들어서 언제 돌려도 같은 입력이 나온다.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
)

// ---------------------------------------------------------------------------
// 참조 구현
// ---------------------------------------------------------------------------

type refEvent struct {
	ago   time.Duration // 지금보다 얼마나 전인가. 음수면 지금보다 뒤다
	stage int
	// conv는 그 사건이 나온 대화의 번호다. 0번은 지금 판정하는 발화가 속한 대화다.
	conv int
}

// refThisConv는 지금 판정하는 발화가 속한 대화의 번호다.
const refThisConv = 0

type refGateInput struct {
	rule        int
	ruleMatched bool
	aiAnswered  bool
	ai          int
	scoreHigh   bool
	changed     bool
	directAsked bool
	history     []refEvent
	now         time.Time
}

type refDecision struct {
	stage       int
	detectedBy  string
	adjustments []string
}

// refDecide는 규칙을 적힌 순서대로 옮긴다.
//
//  1. 두 겹 가운데 높은 쪽을 따른다.
//  2. AI 판별이 실패했거나 제때 오지 않았고 규칙에 걸린 발화면 적어도 1단계다.
//  3. 추정 점수가 기준 이상이거나 변화 감지 상태면 한 단계 올린다. 0단계는 올리지 않는다. 2번만으로 1단계가 된 발화도 올리지 않는다.
//  4. 아직 1단계이고 이 대화에서 직접 묻기를 이미 했으면 2단계다.
//  5. 아직 1단계이고 정해진 기간 안에 1단계 이상이 있었던 대화가 이번 대화를 포함해 정해진 수에 닿았으면 2단계다.
//  6. 아직 1단계이고 정해진 기간 안에 2단계 이상이 있었으면 2단계다.
//
// 기간은 지금부터 거슬러 센 시간이다. 경계에 딱 걸린 사건은 기간 안으로 치고, 지금보다 뒤의 사건은 치지 않는다.
func refDecide(in refGateInput, p params.Crisis) refDecision {
	d := refDecision{adjustments: []string{}}

	d.stage = in.rule
	if in.aiAnswered && in.ai > d.stage {
		d.stage = in.ai
	}
	ruleDetected := in.rule >= 1
	aiDetected := in.aiAnswered && in.ai >= 1

	liftedOnlyByFloor := false
	if !in.aiAnswered && in.ruleMatched && d.stage < 1 {
		d.stage = 1
		ruleDetected = true
		liftedOnlyByFloor = true
		d.adjustments = append(d.adjustments, "ai_failed_floor")
	}

	if (in.scoreHigh || in.changed) && d.stage >= 1 && d.stage < 3 && !liftedOnlyByFloor {
		d.stage++
		d.adjustments = append(d.adjustments, "bad_state_plus_one")
	}

	if d.stage == 1 && in.directAsked {
		d.stage = 2
		d.adjustments = append(d.adjustments, "repeat_after_direct_ask")
	}

	within := func(e refEvent, days int) bool {
		hours := e.ago.Hours()
		return e.ago >= 0 && hours <= float64(days)*24
	}

	if d.stage == 1 {
		// 대화의 번호를 모은다. 이번 대화는 처음부터 들어 있고, 같은 번호는 한 번만 들어간다.
		conversations := []int{refThisConv}
		for _, e := range in.history {
			if e.stage >= 1 && within(e, p.RepeatWindowDays) && !slices.Contains(conversations, e.conv) {
				conversations = append(conversations, e.conv)
			}
		}
		if len(conversations) >= p.RepeatCount {
			d.stage = 2
			d.adjustments = append(d.adjustments, "repeated_in_window")
		}
	}

	if d.stage == 1 {
		for _, e := range in.history {
			if e.stage >= 2 && within(e, p.SensitiveWindowDays) {
				d.stage = 2
				d.adjustments = append(d.adjustments, "sensitive_window")
				break
			}
		}
	}

	switch {
	case ruleDetected && aiDetected:
		d.detectedBy = "both"
	case ruleDetected:
		d.detectedBy = "rule"
	case aiDetected:
		d.detectedBy = "ai"
	default:
		d.detectedBy = "none"
	}
	return d
}

// ---------------------------------------------------------------------------
// 두 구현을 잇는 다리
// ---------------------------------------------------------------------------

func refToStage(t *testing.T, n int) crisis.Stage {
	t.Helper()
	s, err := crisis.StageFromInt(n)
	require.NoError(t, err)
	return s
}

func refToInput(t *testing.T, in refGateInput) crisis.Input {
	t.Helper()
	out := crisis.Input{
		Rule:           crisis.RuleResult{Stage: refToStage(t, in.rule), Matched: in.ruleMatched},
		AI:             crisis.AIFailed(),
		State:          crisis.State{ScoreElevated: in.scoreHigh, ChangeDetected: in.changed},
		DirectAskDone:  in.directAsked,
		ConversationID: refConvID(refThisConv),
		Now:            in.now,
	}
	if in.aiAnswered {
		out.AI = crisis.AIAnswered(refToStage(t, in.ai))
	}
	for _, e := range in.history {
		out.History = append(out.History, crisis.Event{
			At: in.now.Add(-e.ago), Stage: refToStage(t, e.stage), ConversationID: refConvID(e.conv),
		})
	}
	return out
}

// refConvID는 대화의 번호를 실제 구현이 받는 식별자로 옮긴다.
func refConvID(conv int) string {
	return fmt.Sprintf("conversation-%d", conv)
}

func (in refGateInput) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  규칙 %d단계(사전에 걸림 %v), AI ", in.rule, in.ruleMatched)
	if in.aiAnswered {
		fmt.Fprintf(&b, "%d단계", in.ai)
	} else {
		b.WriteString("실패")
	}
	fmt.Fprintf(&b, ", 점수 기준 이상 %v, 변화 감지 %v, 직접 묻기를 이미 함 %v\n", in.scoreHigh, in.changed, in.directAsked)
	for _, e := range in.history {
		fmt.Fprintf(&b, "  지난 사건: %v 전, %d단계, %d번 대화(0번이 이번 대화)\n", e.ago, e.stage, e.conv)
	}
	return b.String()
}

// refGateDisagreement는 입력 하나를 두 구현으로 판정해 어긋난 점을 돌려준다. 같으면 빈 문자열이다.
func refGateDisagreement(t *testing.T, in refGateInput, p params.Crisis) string {
	t.Helper()
	want := refDecide(in, p)
	got, err := crisis.Decide(refToInput(t, in), p)
	if err != nil {
		return "실제 구현이 오류를 돌려줬다: " + err.Error()
	}

	var diffs []string
	if int(got.Stage) != want.stage {
		diffs = append(diffs, fmt.Sprintf("최종 단계: 참조 %d, 실제 %d", want.stage, int(got.Stage)))
	}
	if got.DetectedBy.String() != want.detectedBy {
		diffs = append(diffs, fmt.Sprintf("감지한 쪽: 참조 %s, 실제 %s", want.detectedBy, got.DetectedBy))
	}
	if got.Adjustments == nil || !slices.Equal(got.AdjustmentIDs(), want.adjustments) {
		diffs = append(diffs, fmt.Sprintf("단계를 바꾼 규칙: 참조 %v, 실제 %v", want.adjustments, got.AdjustmentIDs()))
	}
	return strings.Join(diffs, "\n")
}

// refShrinkGate는 두 구현이 어긋나는 입력을, 어긋남이 남아 있는 한 가장 작게 줄인다.
// 지난 사건을 빼 보고, 켜진 조건을 꺼 보고, 단계를 낮춰 본다.
func refShrinkGate(t *testing.T, in refGateInput, p params.Crisis) refGateInput {
	t.Helper()
	disagrees := func(c refGateInput) bool { return refGateDisagreement(t, c, p) != "" }
	for changed := true; changed; {
		changed = false
		for i := len(in.history) - 1; i >= 0; i-- {
			c := in
			c.history = slices.Delete(slices.Clone(in.history), i, i+1)
			if disagrees(c) {
				in, changed = c, true
			}
		}
		for _, simplify := range []func(*refGateInput) bool{
			func(c *refGateInput) bool { was := c.scoreHigh; c.scoreHigh = false; return was },
			func(c *refGateInput) bool { was := c.changed; c.changed = false; return was },
			func(c *refGateInput) bool { was := c.directAsked; c.directAsked = false; return was },
			func(c *refGateInput) bool { was := c.ruleMatched; c.ruleMatched = false; return was },
			func(c *refGateInput) bool { was := c.rule > 0; c.rule = max(c.rule-1, 0); return was },
			func(c *refGateInput) bool { was := c.ai > 0; c.ai = max(c.ai-1, 0); return was },
			// 지난 사건을 모두 서로 다른 대화로 떼어 놓아 본다. 대화를 묶는 데서 어긋난 것인지 가려낸다.
			func(c *refGateInput) bool {
				changed := false
				for i := range c.history {
					if c.history[i].conv != 100+i {
						c.history[i].conv, changed = 100+i, true
					}
				}
				return changed
			},
		} {
			c := in
			c.history = slices.Clone(in.history)
			if simplify(&c) && disagrees(c) {
				in, changed = c, true
			}
		}
	}
	return in
}

// ---------------------------------------------------------------------------
// 입력 만들기
// ---------------------------------------------------------------------------

const refDayLength = 24 * time.Hour

// refGenGateInput은 씨앗 하나에서 관문의 입력 하나를 만든다.
// 지난 사건의 시각은 기간의 경계에 딱 걸린 값, 경계에서 한 눈금 벗어난 값, 지금과 같은 시각, 지금보다 뒤를 섞는다.
// 대화의 번호는 좁은 범위에서 골라 같은 대화의 사건이 겹치고, 이번 대화(0번)의 앞선 사건도 자주 섞이게 한다.
func refGenGateInput(rng *rand.Rand, p params.Crisis) refGateInput {
	zones := [...]*time.Location{time.UTC, time.FixedZone("KST", 9*60*60), time.FixedZone("W", -5*60*60)}
	now := time.Date(2026, time.Month(1+rng.IntN(12)), 1+rng.IntN(28), rng.IntN(24), rng.IntN(60), rng.IntN(60), 0, zones[rng.IntN(len(zones))])

	in := refGateInput{
		// 대부분의 발화는 0단계나 1단계다. 위쪽 단계도 빠지지 않게 섞는다.
		rule:        [...]int{0, 0, 0, 1, 1, 2, 3}[rng.IntN(7)],
		ruleMatched: rng.IntN(2) == 0,
		aiAnswered:  rng.IntN(5) != 0,
		scoreHigh:   rng.IntN(4) == 0,
		changed:     rng.IntN(4) == 0,
		directAsked: rng.IntN(4) == 0,
		now:         now,
	}
	if in.aiAnswered {
		in.ai = [...]int{0, 0, 0, 1, 1, 2, 3}[rng.IntN(7)]
	}

	repeat, sensitive := time.Duration(p.RepeatWindowDays)*refDayLength, time.Duration(p.SensitiveWindowDays)*refDayLength
	for range rng.IntN(7) {
		var ago time.Duration
		switch rng.IntN(10) {
		case 0:
			ago = repeat
		case 1:
			ago = repeat + time.Microsecond
		case 2:
			ago = repeat - time.Microsecond
		case 3:
			ago = sensitive
		case 4:
			ago = sensitive + time.Microsecond
		case 5:
			ago = sensitive - time.Microsecond
		case 6:
			ago = 0
		case 7:
			ago = -time.Duration(1+rng.IntN(72)) * time.Hour
		default:
			ago = time.Duration(rng.Int64N(int64(repeat + 10*refDayLength)))
		}
		in.history = append(in.history, refEvent{ago: ago, stage: [...]int{0, 1, 1, 1, 2, 3}[rng.IntN(6)], conv: rng.IntN(5)})
	}
	return in
}

func refGenCrisisParams(rng *rand.Rand) params.Crisis {
	p := params.Default().Crisis
	if rng.IntN(2) == 0 {
		return p
	}
	p.RepeatWindowDays = 1 + rng.IntN(30)
	p.RepeatCount = 2 + rng.IntN(4)
	p.SensitiveWindowDays = 1 + rng.IntN(14)
	return p
}

const refInputCount = 200_000

// refCount는 -short로 돌릴 때 돌려 볼 수를 십분의 일로 줄인다. 손으로 자주 돌릴 때를 위한 것이고, 평소에는 전부 돌린다.
func refCount(n int) uint64 {
	if testing.Short() {
		n = max(n/10, 1)
	}
	return uint64(n)
}

// ---------------------------------------------------------------------------
// 시험
// ---------------------------------------------------------------------------

func TestReferenceDecide(t *testing.T) {
	t.Run("아무렇게나 만든 입력 수십만 개에서 참조 구현과 같은 판정이 나온다", func(t *testing.T) {
		for seed := range refCount(refInputCount) {
			rng := rand.New(rand.NewPCG(seed, 0x9a7e))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			if refGateDisagreement(t, in, p) == "" {
				continue
			}
			small := refShrinkGate(t, in, p)
			require.Failf(t, "두 구현이 어긋난다", "씨앗 %d, 조정 값 %+v\n%s\n가장 작게 줄인 입력:\n%s",
				seed, p, refGateDisagreement(t, small, p), small.describe())
		}
	})

	t.Run("두 겹, 상태, 직접 묻기, 지난 사건의 모든 조합에서 참조 구현과 같은 판정이 나온다", func(t *testing.T) {
		p := params.Default().Crisis
		now := time.Date(2026, time.September, 20, 22, 30, 0, 0, time.UTC)
		histories := [][]refEvent{
			nil,
			{{ago: 2 * refDayLength, stage: 1, conv: 1}},
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 13 * refDayLength, stage: 1, conv: 2}},
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 14 * refDayLength, stage: 1, conv: 2}},
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 14*refDayLength + time.Microsecond, stage: 1, conv: 2}},
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 3 * refDayLength, stage: 1, conv: 2}, {ago: 4 * refDayLength, stage: 1, conv: 3}},
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 3 * refDayLength, stage: 0, conv: 2}},
			{{ago: 6 * refDayLength, stage: 2, conv: 1}},
			{{ago: 7 * refDayLength, stage: 3, conv: 1}},
			{{ago: 7*refDayLength + time.Microsecond, stage: 3, conv: 1}},
			{{ago: 8 * refDayLength, stage: 2, conv: 1}, {ago: 9 * refDayLength, stage: 2, conv: 2}},
			{{ago: 8 * refDayLength, stage: 2, conv: 1}},
			{{ago: -time.Hour, stage: 2, conv: 1}, {ago: -time.Hour, stage: 1, conv: 2}, {ago: -2 * time.Hour, stage: 1, conv: 3}},
			{{ago: 0, stage: 1, conv: 1}, {ago: 0, stage: 1, conv: 2}},
			{{ago: 0, stage: 2, conv: 1}},
			// 같은 대화의 사건 여럿, 이번 대화의 앞선 사건
			{{ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 2*refDayLength - time.Minute, stage: 1, conv: 1}, {ago: 2*refDayLength - 2*time.Minute, stage: 1, conv: 1}},
			{{ago: 2 * time.Minute, stage: 1, conv: refThisConv}, {ago: time.Minute, stage: 1, conv: refThisConv}},
			{{ago: 2 * time.Minute, stage: 1, conv: refThisConv}, {ago: 2 * refDayLength, stage: 1, conv: 1}},
			{{ago: 2 * time.Minute, stage: 1, conv: refThisConv}, {ago: 2 * refDayLength, stage: 1, conv: 1}, {ago: 9 * refDayLength, stage: 2, conv: 2}},
			{{ago: 9 * refDayLength, stage: 0, conv: 1}, {ago: 9*refDayLength - time.Minute, stage: 1, conv: 1}, {ago: 3 * refDayLength, stage: 3, conv: 2}},
		}
		checked := 0
		for rule := 0; rule <= 3; rule++ {
			for ai := -1; ai <= 3; ai++ {
				for flags := range 16 {
					for _, history := range histories {
						in := refGateInput{
							rule: rule, ruleMatched: flags&1 != 0,
							aiAnswered: ai >= 0, ai: max(ai, 0),
							scoreHigh: flags&2 != 0, changed: flags&4 != 0, directAsked: flags&8 != 0,
							history: history, now: now,
						}
						require.Empty(t, refGateDisagreement(t, in, p), in.describe())
						checked++
					}
				}
			}
		}
		assert.Equal(t, 4*5*16*len(histories), checked)
	})

	t.Run("아주 먼 과거와 아주 먼 미래의 사건은 세지 않는다", func(t *testing.T) {
		// 흐른 시간을 나타낼 수 없을 만큼 먼 시각이다. 계산이 넘쳐서 기간 안으로 잘못 들어오는 일이 없어야 한다.
		p := params.Default().Crisis
		now := time.Date(2026, time.September, 20, 22, 30, 0, 0, time.UTC)
		var history []crisis.Event
		for _, at := range []time.Time{
			time.Date(1, time.January, 1, 0, 0, 0, 1, time.UTC),
			time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
			time.Unix(0, 0),
		} {
			for _, stage := range []crisis.Stage{crisis.StageCheck, crisis.StageCheck, crisis.StageCheck, crisis.StageUrgent} {
				history = append(history, crisis.Event{At: at, Stage: stage, ConversationID: refConvID(len(history) + 1)})
			}
		}
		got, err := crisis.Decide(crisis.Input{
			Rule: crisis.RuleResult{Stage: crisis.StageCheck, Matched: true},
			AI:   crisis.AIAnswered(crisis.StageCheck), ConversationID: refConvID(refThisConv), History: history, Now: now,
		}, p)
		require.NoError(t, err)
		assert.Equal(t, crisis.StageCheck, got.Stage)
		assert.Empty(t, got.Adjustments)
	})

	t.Run("점수가 기준 이상인지는 기준과 같은 점수부터 참이다", func(t *testing.T) {
		for minScore := 1; minScore <= 24; minScore++ {
			p := params.Default().Crisis
			p.EscalationMinScore = minScore
			for score := 0; score <= 24; score++ {
				assert.Equal(t, score >= minScore, crisis.ScoreElevated(score, p), "기준 %d, 점수 %d", minScore, score)
			}
		}
	})
}

func TestReferenceDecideProperties(t *testing.T) {
	decide := func(t *testing.T, in refGateInput, p params.Crisis) crisis.Decision {
		t.Helper()
		got, err := crisis.Decide(refToInput(t, in), p)
		require.NoError(t, err)
		return got
	}

	t.Run("애매한 표현은 어떤 규칙을 거쳐도 2단계를 넘지 않는다", func(t *testing.T) {
		escalated := 0
		for seed := range refCount(refInputCount) {
			rng := rand.New(rand.NewPCG(seed, 0xa3b1))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			// 두 겹이 모두 1단계 이하로 본 발화만 남기고, 올릴 수 있는 조건은 모두 켠다.
			in.rule, in.ai = min(in.rule, 1), min(in.ai, 1)
			in.scoreHigh, in.changed, in.directAsked = true, true, true
			in.history = append(in.history,
				refEvent{ago: time.Hour, stage: 3, conv: 101}, refEvent{ago: time.Hour, stage: 1, conv: 102}, refEvent{ago: 2 * time.Hour, stage: 1, conv: 103})

			got := decide(t, in, p)
			require.LessOrEqual(t, got.Stage, crisis.StageRespond, "씨앗 %d\n%s", seed, in.describe())
			if got.Stage == crisis.StageRespond {
				escalated++
			}
		}
		require.Positive(t, escalated, "2단계로 올라간 경우가 하나도 없었다면 위의 확인은 아무것도 보지 않은 것이다")
	})

	t.Run("최종 단계는 두 겹의 판정보다 낮지 않고, 0단계로 본 발화는 올리지 않는다", func(t *testing.T) {
		for seed := range refCount(refInputCount) {
			rng := rand.New(rand.NewPCG(seed, 0xf100))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			got := decide(t, in, p)

			layers := in.rule
			if in.aiAnswered {
				layers = max(layers, in.ai)
			}
			require.GreaterOrEqual(t, int(got.Stage), layers, "씨앗 %d\n%s", seed, in.describe())
			require.LessOrEqual(t, int(got.Stage), 3, "씨앗 %d", seed)

			if layers == 0 && (in.aiAnswered || !in.ruleMatched) {
				require.Equal(t, crisis.StageNone, got.Stage, "씨앗 %d: 0단계 발화가 올라갔다\n%s", seed, in.describe())
				require.Empty(t, got.Adjustments, "씨앗 %d", seed)
			}
			if layers == 0 && !in.aiAnswered && in.ruleMatched {
				require.GreaterOrEqual(t, got.Stage, crisis.StageCheck, "씨앗 %d: AI가 실패했고 규칙에 걸렸는데 조용히 통과했다", seed)
			}
			if got.Stage == crisis.StageUrgent {
				require.GreaterOrEqual(t, layers, 2, "씨앗 %d: 두 겹이 2단계 미만으로 본 발화가 3단계가 됐다\n%s", seed, in.describe())
			}

			// 적힌 규칙은 단계를 실제로 바꾼 것뿐이고, 하나가 한 단계씩 올리며, 적용 순서대로다.
			require.Len(t, got.Adjustments, int(got.Stage)-layers, "씨앗 %d\n%s", seed, in.describe())
			require.True(t, slices.IsSorted(got.Adjustments), "씨앗 %d", seed)
			require.Equal(t, got.Stage == crisis.StageNone, got.DetectedBy == crisis.DetectedByNone, "씨앗 %d", seed)
		}
	})

	t.Run("같은 입력에서는 몇 번을 판정해도 같고, 지난 사건의 순서를 섞어도 같고, 받은 목록을 고치지 않는다", func(t *testing.T) {
		for seed := range refCount(refInputCount / 10) {
			rng := rand.New(rand.NewPCG(seed, 0x0de7))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			want := decide(t, in, p)

			input := refToInput(t, in)
			before := slices.Clone(input.History)
			again, err := crisis.Decide(input, p)
			require.NoError(t, err)
			require.Equal(t, want, again, "씨앗 %d", seed)
			require.Equal(t, before, input.History, "씨앗 %d", seed)

			shuffled := in
			shuffled.history = slices.Clone(in.history)
			rng.Shuffle(len(shuffled.history), func(i, j int) {
				shuffled.history[i], shuffled.history[j] = shuffled.history[j], shuffled.history[i]
			})
			require.Equal(t, want, decide(t, shuffled, p), "씨앗 %d\n%s", seed, in.describe())

			// 같은 순간을 다른 시간대로 적어도 같다.
			moved := in
			moved.now = in.now.In(time.FixedZone("E", 13*60*60))
			require.Equal(t, want, decide(t, moved, p), "씨앗 %d", seed)
		}
	})

	t.Run("쌓임은 발화가 아니라 대화를 센다: 같은 대화의 1단계를 더 넣어도 판정이 달라지지 않는다", func(t *testing.T) {
		raisedByRepeat := 0
		for seed := range refCount(refInputCount / 4) {
			rng := rand.New(rand.NewPCG(seed, 0xc0f1))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			want := decide(t, in, p)
			if slices.Contains(want.Adjustments, crisis.AdjustRepeatedInWindow) {
				raisedByRepeat++
			}

			// 이미 관문에 걸린 적이 있는 대화와 이번 대화에, 같은 시각의 1단계 사건을 몇 개씩 더 넣는다.
			// 1단계 사건은 쌓임을 세는 데만 쓰이므로, 대화의 수가 그대로면 판정도 그대로여야 한다.
			more := in
			more.history = slices.Clone(in.history)
			for _, e := range in.history {
				if e.stage >= 1 {
					for range 1 + rng.IntN(3) {
						more.history = append(more.history, refEvent{ago: e.ago, stage: 1, conv: e.conv})
					}
				}
			}
			for range rng.IntN(4) {
				more.history = append(more.history, refEvent{ago: time.Duration(rng.Int64N(int64(time.Hour))), stage: 1, conv: refThisConv})
			}
			require.Equal(t, want, decide(t, more, p), "씨앗 %d\n앞:\n%s뒤:\n%s", seed, in.describe(), more.describe())
		}
		require.Positive(t, raisedByRepeat, "쌓임으로 올라간 경우가 하나도 없었다면 위의 확인은 아무것도 보지 않은 것이다")
	})

	t.Run("AI 판별 실패만으로 1단계가 된 발화는 상태가 나빠도 1단계에 머물고, 직접 묻기와 되풀이와 민감 기간만이 2단계로 올린다", func(t *testing.T) {
		// 참조 구현을 거치지 않고, 되풀이와 민감 기간에 드는지를 시각끼리 견줘서 따로 센다.
		stayed, raisedBy := 0, map[crisis.Adjustment]int{}
		for seed := range refCount(refInputCount / 8) {
			rng := rand.New(rand.NewPCG(seed, 0xf100d))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			// 규칙은 사전에 걸렸지만 0단계로 봤고, AI 판별은 답하지 못했다. 상태는 절반쯤 나쁘게 둔다.
			in.rule, in.ruleMatched, in.aiAnswered, in.ai = 0, true, false, 0
			if rng.IntN(2) == 0 {
				in.scoreHigh, in.changed = rng.IntN(2) == 0, true
			}
			got := decide(t, in, p)

			repeatFrom := in.now.Add(-time.Duration(p.RepeatWindowDays) * refDayLength)
			sensitiveFrom := in.now.Add(-time.Duration(p.SensitiveWindowDays) * refDayLength)
			talks, recentHeavy := map[int]bool{refThisConv: true}, false
			for _, e := range in.history {
				at := in.now.Add(-e.ago)
				if at.After(in.now) {
					continue
				}
				if e.stage >= 1 && !at.Before(repeatFrom) {
					talks[e.conv] = true
				}
				if e.stage >= 2 && !at.Before(sensitiveFrom) {
					recentHeavy = true
				}
			}
			var want []crisis.Adjustment
			switch {
			case in.directAsked:
				want = []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatAfterDirectAsk}
			case len(talks) >= p.RepeatCount:
				want = []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustRepeatedInWindow}
			case recentHeavy:
				want = []crisis.Adjustment{crisis.AdjustAIFailedFloor, crisis.AdjustSensitiveWindow}
			default:
				want = []crisis.Adjustment{crisis.AdjustAIFailedFloor}
			}

			require.Equal(t, want, got.Adjustments, "씨앗 %d\n%s", seed, in.describe())
			require.Equal(t, crisis.Stage(len(want)), got.Stage, "씨앗 %d\n%s", seed, in.describe())
			require.NotContains(t, got.Adjustments, crisis.AdjustBadStatePlusOne, "씨앗 %d\n%s", seed, in.describe())
			require.Equal(t, crisis.DetectedByRule, got.DetectedBy, "씨앗 %d", seed)
			if len(want) == 1 {
				stayed++
			} else {
				raisedBy[want[1]]++
			}

			// 상태를 어떻게 바꿔도 이 발화의 판정은 그대로다.
			for flags := range 4 {
				other := in
				other.scoreHigh, other.changed = flags&1 != 0, flags&2 != 0
				require.Equal(t, got, decide(t, other, p), "씨앗 %d: 상태에 따라 판정이 달라졌다\n%s", seed, in.describe())
			}
		}
		// 한 번도 지나가지 않은 길이 있으면 위의 확인은 그 길에 대해 아무것도 보지 않은 것이다.
		require.Positive(t, stayed, "1단계에 머문 경우")
		for _, adjustment := range []crisis.Adjustment{crisis.AdjustRepeatAfterDirectAsk, crisis.AdjustRepeatedInWindow, crisis.AdjustSensitiveWindow} {
			require.Positive(t, raisedBy[adjustment], "%s로 2단계가 된 경우", adjustment)
		}
	})

	t.Run("같은 말이라도 AI 판별이 0단계라고 답했으면 통과하고, 규칙이 스스로 1단계로 봤으면 상태가 나쁠 때 2단계다", func(t *testing.T) {
		// 바로 위의 확인이 "AI 판별 실패 때의 바닥만으로 1단계가 된 발화"만 가려내고 있는지를 이웃한 두 경우로 본다.
		for seed := range refCount(refInputCount / 20) {
			rng := rand.New(rand.NewPCG(seed, 0xf100e))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			in.rule, in.ruleMatched, in.ai = 0, true, 0
			in.scoreHigh, in.changed, in.directAsked, in.history = true, rng.IntN(2) == 0, false, nil

			in.aiAnswered = true
			passed := decide(t, in, p)
			require.Equal(t, crisis.StageNone, passed.Stage, "씨앗 %d", seed)
			require.Empty(t, passed.Adjustments, "씨앗 %d", seed)

			in.aiAnswered, in.rule = false, 1
			raised := decide(t, in, p)
			require.Equal(t, crisis.StageRespond, raised.Stage, "씨앗 %d", seed)
			require.Equal(t, []crisis.Adjustment{crisis.AdjustBadStatePlusOne}, raised.Adjustments, "씨앗 %d", seed)
		}
	})

	t.Run("대화를 가리키는 값은 같은지만 본다: 이름을 바꿔도 같고, 대화를 쪼개면 내려가지 않고, 합치면 올라가지 않는다", func(t *testing.T) {
		splitRaised, mergeLowered := 0, 0
		for seed := range refCount(refInputCount / 8) {
			rng := rand.New(rand.NewPCG(seed, 0x1d5))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			want := decide(t, in, p)

			// 이번 대화(0번)만 그대로 두고 나머지 번호를 겹치지 않게 다른 번호로 옮긴다.
			renamed, split, merged := in, in, in
			renamed.history, split.history, merged.history = slices.Clone(in.history), slices.Clone(in.history), slices.Clone(in.history)
			shift := 10 + rng.IntN(1000)
			for i, e := range in.history {
				if e.conv == refThisConv {
					continue
				}
				renamed.history[i].conv = shift - e.conv
				split.history[i].conv = shift + i
				merged.history[i].conv = shift
			}
			require.Equal(t, want, decide(t, renamed, p), "씨앗 %d\n%s", seed, in.describe())

			afterSplit, afterMerge := decide(t, split, p), decide(t, merged, p)
			require.GreaterOrEqual(t, afterSplit.Stage, want.Stage, "씨앗 %d: 대화를 쪼갰더니 단계가 내려갔다\n%s", seed, in.describe())
			require.LessOrEqual(t, afterMerge.Stage, want.Stage, "씨앗 %d: 대화를 합쳤더니 단계가 올라갔다\n%s", seed, in.describe())
			if afterSplit.Stage > want.Stage {
				splitRaised++
			}
			if afterMerge.Stage < want.Stage {
				mergeLowered++
			}
		}
		require.Positive(t, splitRaised, "쪼개서 단계가 오른 경우가 없었다면 대화를 묶어 세는지를 보지 못한 것이다")
		require.Positive(t, mergeLowered, "합쳐서 단계가 내린 경우가 없었다면 대화를 묶어 세는지를 보지 못한 것이다")
	})

	t.Run("지난 판정의 최종 단계가 더 높았던 것으로 바꿔도 이번 판정이 내려가지 않는다", func(t *testing.T) {
		// 되풀이는 최종 1단계 이상을 모두 센다. 1단계인 것만 센다면, 지난 대화가 상태 때문에 2단계로 올라갔던 사람일수록 덜 걸린다.
		for seed := range refCount(refInputCount / 8) {
			rng := rand.New(rand.NewPCG(seed, 0x4a15e))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			before := decide(t, in, p)

			heavier := in
			heavier.history = slices.Clone(in.history)
			for i, e := range heavier.history {
				if e.stage >= 1 && rng.IntN(2) == 0 {
					heavier.history[i].stage = e.stage + rng.IntN(4-e.stage)
				}
			}
			after := decide(t, heavier, p)
			require.GreaterOrEqual(t, after.Stage, before.Stage, "씨앗 %d\n앞:\n%s뒤:\n%s", seed, in.describe(), heavier.describe())
		}
	})

	t.Run("조건이 하나 더 나빠지면 최종 단계가 내려가지 않는다", func(t *testing.T) {
		for seed := range refCount(refInputCount / 4) {
			rng := rand.New(rand.NewPCG(seed, 0x3070))
			p := refGenCrisisParams(rng)
			in := refGenGateInput(rng, p)
			before := decide(t, in, p)

			worse := in
			worse.history = slices.Clone(in.history)
			switch rng.IntN(5) {
			case 0:
				worse.scoreHigh = true
			case 1:
				worse.changed = true
			case 2:
				worse.directAsked = true
			case 3:
				worse.history = append(worse.history, refEvent{
					ago: time.Duration(rng.Int64N(int64(20 * refDayLength))), stage: 1 + rng.IntN(3), conv: rng.IntN(7),
				})
			default:
				worse.rule = min(in.rule+1, 3)
			}
			after := decide(t, worse, p)
			require.GreaterOrEqual(t, after.Stage, before.Stage, "씨앗 %d\n앞:\n%s뒤:\n%s", seed, in.describe(), worse.describe())
		}
	})
}
