package gate_test

// 이 파일의 평가는 실제 판별 모델을 부른다. GEMINI_API_KEY가 있을 때만 돌고, 없으면 건너뛴다.
// 키가 있어도 돌리고 싶지 않으면 GEMINI_LIVE_TESTS=0을 준다. 문장마다 여러 번 돌려 보려면 GATE_LIVE_EVAL_RUNS에 횟수를 준다.
//
// 보는 것은 지시문이 제 몫을 하는지다. 문장마다 받아들일 수 있는 단계의 범위가 있고, 그 가운데 두 가지는 어기면 시험이 실패한다.
//   - 대응 단계 이상이어야 하는 문장을 낮춰 잡는 것. 놓치면 사람이 위험해진다.
//   - 해당 없음이어야 하는 문장을 대응 단계 이상으로 올려 잡는 것. 과한 반응을 겪은 사람은 말을 고르기 시작한다.
// 나머지 어긋남(한 단계 높거나 낮은 확인 단계)은 세어서 보여 주기만 한다. 모델의 답은 부를 때마다 조금씩 달라지기 때문이다.
//
// 문장은 모두 시험용으로 지은 것이라 출력에 그대로 적는다. -v로 돌리면 문장별 결과와 걸린 시간이 보인다.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/gemini"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/gate"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/gate/gatetest"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
)

const (
	// 평가에서는 모델의 응답 시간을 있는 그대로 보려고 기한을 넉넉히 주고 다시 부르지 않는다.
	// 서버의 기한을 넘긴 호출은 따로 센다.
	liveEvalTimeout     = 30 * time.Second
	liveEvalConcurrency = 6
	defaultGateBudget   = 2500 * time.Millisecond
)

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

type liveOutcome struct {
	c      gatetest.Case
	rule   rules.Result
	ai     classifier.Result
	run    int
	merged int
}

func TestLiveEvaluation(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY가 없어 실제 판별 모델을 부르는 평가를 건너뛴다")
	}
	if os.Getenv("GEMINI_LIVE_TESTS") == "0" {
		t.Skip("GEMINI_LIVE_TESTS=0이라 실제 판별 모델을 부르는 평가를 건너뛴다")
	}
	runs, err := strconv.Atoi(envOr("GATE_LIVE_EVAL_RUNS", "1"))
	require.NoError(t, err)
	require.Positive(t, runs)
	budget, err := time.ParseDuration(envOr("GATE_AI_TIMEOUT", defaultGateBudget.String()))
	require.NoError(t, err)

	ctx := context.Background()
	client, err := gemini.New(ctx, gemini.Config{APIKey: config.NewSecret(key), Clock: clock.Real{}})
	require.NoError(t, err)
	model := envOr("LLM_MODEL_GATE", "gemini-3.5-flash")
	llm, err := client.LLM(gemini.Model{Name: model, Thinking: strings.ToLower(envOr("LLM_THINKING_GATE", ai.ThinkingMinimal))})
	require.NoError(t, err)

	registry, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	prompt, err := registry.Get(classifier.Task)
	require.NoError(t, err)
	cls, err := classifier.New(llm, prompt, clock.Real{}, classifier.Config{Timeout: liveEvalTimeout, MaxAttempts: 1})
	require.NoError(t, err)
	lexicon, err := rules.LoadEmbedded()
	require.NoError(t, err)
	detector, err := gate.New(lexicon, cls)
	require.NoError(t, err)

	cases := gatetest.Cases()
	outcomes := make([]liveOutcome, 0, len(cases)*runs)
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		slot = make(chan struct{}, liveEvalConcurrency)
	)
	for run := 1; run <= runs; run++ {
		for _, c := range cases {
			wg.Add(1)
			slot <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-slot }()

				in := classifier.Input{Utterance: c.Utterance}
				for _, turn := range c.Context {
					in.Context = append(in.Context, classifier.Turn{Speaker: classifier.Speaker(turn.Speaker), Text: turn.Text})
				}
				detection := detector.Detect(ctx, in)

				merged := int(detection.Rule.Stage)
				if detection.AI.Answered && int(detection.AI.Stage) > merged {
					merged = int(detection.AI.Stage)
				}
				mu.Lock()
				outcomes = append(outcomes, liveOutcome{c: c, rule: detection.Rule, ai: detection.AI, run: run, merged: merged})
				mu.Unlock()
			}()
		}
	}
	wg.Wait()

	order := map[string]int{}
	for i, c := range cases {
		order[c.Group+"\x00"+c.Utterance+"\x00"+fmt.Sprint(len(c.Context))] = i
	}
	slices.SortStableFunc(outcomes, func(a, b liveOutcome) int {
		ka := order[a.c.Group+"\x00"+a.c.Utterance+"\x00"+fmt.Sprint(len(a.c.Context))]
		kb := order[b.c.Group+"\x00"+b.c.Utterance+"\x00"+fmt.Sprint(len(b.c.Context))]
		if ka != kb {
			return ka - kb
		}
		return a.run - b.run
	})

	var (
		latencies                                     []time.Duration
		failed, inRange, under, over, dropped         int
		mergedInRange, mergedUnder, mergedOver        int
		criticalUnder, idiomOver, mergedCriticalUnder []string
		mergedIdiomOver, overBudget                   []string
		describe                                      = func(o liveOutcome) string {
			ctxMark := ""
			if len(o.c.Context) > 0 {
				ctxMark = " (문맥 있음)"
			}
			return fmt.Sprintf("[%d-%d] 규칙 %d, 판별 %d, 합친 단계 %d | %s%s", o.c.Min, o.c.Max, o.rule.Stage, o.ai.Stage, o.merged, o.c.Utterance, ctxMark)
		}
	)
	t.Logf("모델 %s, 지시문 %s, 사전 %s, 문장 %d개 x %d회", model, prompt.Version, lexicon.Version(), len(cases), runs)
	for _, o := range outcomes {
		verdict := "범위 안"
		switch {
		case !o.ai.Answered:
			verdict = "실패:" + string(o.ai.Failure)
			failed++
		case int(o.ai.Stage) < o.c.Min:
			verdict = "낮게"
			under++
			if o.c.Critical() {
				criticalUnder = append(criticalUnder, describe(o))
			}
		case int(o.ai.Stage) > o.c.Max:
			verdict = "높게"
			over++
			if o.c.Idiom() && o.ai.Stage >= 2 {
				idiomOver = append(idiomOver, describe(o))
			}
		default:
			inRange++
		}
		if o.ai.Answered {
			latencies = append(latencies, o.ai.Latency)
			if o.ai.Latency > budget {
				overBudget = append(overBudget, fmt.Sprintf("%v | %s", o.ai.Latency.Round(time.Millisecond), o.c.Utterance))
			}
		}
		if o.ai.EvidenceDropped {
			dropped++
		}

		switch {
		case o.merged < o.c.Min:
			mergedUnder++
			if o.c.Critical() {
				mergedCriticalUnder = append(mergedCriticalUnder, describe(o))
			}
		case o.merged > o.c.Max:
			mergedOver++
			if o.c.Idiom() && o.merged >= 2 {
				mergedIdiomOver = append(mergedIdiomOver, describe(o))
			}
		default:
			mergedInRange++
		}

		t.Logf("%-16s %-8s %7v  %s  근거=%q", o.c.Group, verdict, o.ai.Latency.Round(10*time.Millisecond), describe(o), o.ai.Evidence)
	}

	total := len(outcomes)
	t.Logf("판별 모델만: 범위 안 %d/%d, 낮게 %d, 높게 %d, 실패 %d, 버린 근거 %d", inRange, total, under, over, failed, dropped)
	t.Logf("규칙과 합친 단계: 범위 안 %d/%d, 낮게 %d, 높게 %d", mergedInRange, total, mergedUnder, mergedOver)
	if len(latencies) > 0 {
		slices.Sort(latencies)
		t.Logf("판별 시간: 중앙값 %v, 90%% 지점 %v, 최대 %v, 기한(%v)을 넘긴 호출 %d/%d",
			latencies[len(latencies)/2].Round(time.Millisecond),
			latencies[len(latencies)*9/10].Round(time.Millisecond),
			latencies[len(latencies)-1].Round(time.Millisecond),
			budget, len(overBudget), len(latencies))
		for _, line := range overBudget {
			t.Logf("  기한을 넘김: %s", line)
		}
	}

	assert.Empty(t, criticalUnder, "판별 모델이 대응 단계 이상인 문장을 낮춰 잡았다")
	assert.Empty(t, mergedCriticalUnder, "두 겹을 합쳐도 대응 단계 이상인 문장을 낮춰 잡았다")
	assert.Empty(t, idiomOver, "판별 모델이 해당 없음인 문장을 대응 단계 이상으로 올려 잡았다")
	assert.Empty(t, mergedIdiomOver, "두 겹을 합친 단계가 해당 없음인 문장을 대응 단계 이상으로 올렸다")
	assert.LessOrEqual(t, failed*20, total, "판별 실패가 스무 번에 한 번을 넘는다")
}
