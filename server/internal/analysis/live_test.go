package analysis_test

// 이 파일의 시험은 실제 Gemini API를 부른다. GEMINI_API_KEY가 있을 때만 돌고, 없으면 건너뛴다.
// 키가 있어도 돌리고 싶지 않으면 GEMINI_LIVE_TESTS=0을 준다.
//
// 가짜 모델로는 확인할 수 없는 것을 본다. 지시문과 스키마가 실제 모델에서 통하는지, 여덟 항목의 판단이 기대와 맞는지,
// 그리고 글자 대조로는 가릴 수 없는 함정(남의 말 옮기기, 관용 표현, 제3자의 어려움, 비꼼, 부인)을 지시문이 막는지다.
//
// 모델의 답은 부를 때마다 달라진다. 특히 고른 근거 문장은 같은 대화에서도 실행마다 다르다. 그래서 근거 글 자체는 단정하지 않고,
// 판단이 맞는지와 근거가 사용자의 발화에서 글자 그대로 왔는지만 본다.
//
// 대화는 모두 시험용으로 지어낸 것이다. 항목별 결과와 근거를 눈으로 읽어 보려면 SIGNAL_LIVE_PRINT=1과 -v를 준다.

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/gemini"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
)

// liveAttempts는 작업 큐가 하는 만큼 다시 불러 본다. 잘린 답이나 느린 응답 한 번에 시험이 흔들리지 않게 한다.
const liveAttempts = 4

// minItemAccuracy는 항목별 판단이 기대와 맞아야 하는 최소 비율이다.
// 한 항목이 어긋나는 것으로는 시험이 깨지지 않지만, 여러 항목이 어긋나면 지시문이나 모델이 바뀐 것이다.
const minItemAccuracy = 0.90

// minVerbatimRate는 모델이 내놓은 근거 가운데 사용자의 발화에 글자 그대로 있던 것의 최소 비율이다.
const minVerbatimRate = 0.90

func liveLLM(t *testing.T, f *fixture) ai.LLM {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY가 없어 실제 API를 부르는 시험을 건너뛴다")
	}
	if os.Getenv("GEMINI_LIVE_TESTS") == "0" {
		t.Skip("GEMINI_LIVE_TESTS=0이라 실제 API를 부르는 시험을 건너뛴다")
	}

	client, err := gemini.New(context.Background(), gemini.Config{
		APIKey: config.NewSecret(key),
		Clock:  clock.Real{},
		Logger: logging.New(f.logs, slog.LevelDebug),
	})
	require.NoError(t, err)

	// 서버의 설정과 같은 변수에서 읽는다. 기본값도 설정과 같다.
	name := strings.TrimSpace(os.Getenv("LLM_MODEL_ANALYSIS"))
	if name == "" {
		name = "gemini-3.8-flash"
	}
	llm, err := client.LLM(gemini.Model{Name: name})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NotContains(t, f.logs.String(), key, "키가 로그에 남으면 안 된다") })
	return llm
}

func liveThinking() string {
	if level := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_THINKING_ANALYSIS"))); level != "" {
		return level
	}
	return ai.ThinkingLow
}

// extractLive는 큐가 하듯이, 다시 해 볼 만한 실패면 다시 부른다.
func extractLive(t *testing.T, service *analysis.Service, target analysis.Target) analysis.Result {
	t.Helper()
	var (
		result analysis.Result
		err    error
	)
	for attempt := 1; attempt <= liveAttempts; attempt++ {
		result, err = service.Extract(t.Context(), target)
		if err == nil || analysis.Permanent(err) {
			break
		}
		t.Logf("시도 %d 실패: %v", attempt, err)
	}
	require.NoError(t, err)
	return result
}

const (
	statusObserved     = "observed"
	statusNotObserved  = "not_observed"
	statusNotMentioned = "not_mentioned"
)

type liveCase struct {
	name  string
	turns []turn
	// want는 채점에 쓰는 기대값이다. 적지 않은 항목은 언급 없음을 기대한다.
	want map[signal.Item]string
	// strict는 반드시 이 값들 가운데 하나여야 하는 항목이다. 어긋나면 그 자리에서 시험이 깨진다.
	// 함정을 못 가렸거나 뚜렷한 신호를 놓친 것이라 통계로 뭉개지 말아야 한다.
	strict map[signal.Item][]string
	// forbidEvidence는 어떤 항목의 근거로도 나오면 안 되는 토막이다.
	forbidEvidence []string
	// gateFlagged는 위기 관문이 확인 단계 이상으로 판정한 말의 자리(turns의 자리)다. 실제 대화에서 관문이 하는 일을 그대로 둔다.
	gateFlagged map[int]int16
}

var liveCases = []liveCase{
	{
		name: "뚜렷한 신호와 괜찮았던 항목이 함께 있는 하루",
		turns: []turn{
			assistant("오늘 하루는 어땠어요?"),
			user("어젯밤에 두 시간밖에 못 잤어. 새벽 네 시에 눈이 떠져서 그냥 천장만 봤어"),
			assistant("잠을 설치면 하루가 길게 느껴지죠. 낮에는 어땠어요?"),
			user("하루 종일 기운이 하나도 없더라. 계단 오르는 것도 힘들었어"),
			assistant("몸이 많이 무거우셨네요. 밥은 챙겨 드셨어요?"),
			user("밥은 세 끼 다 잘 먹었어. 점심에 국밥도 한 그릇 비웠고"),
		},
		want: map[signal.Item]string{
			signal.Sleep:    statusObserved,
			signal.Fatigue:  statusObserved,
			signal.Appetite: statusNotObserved,
		},
		strict: map[signal.Item][]string{
			signal.Sleep:    {statusObserved},
			signal.Fatigue:  {statusObserved},
			signal.Appetite: {statusNotObserved},
		},
	},
	{
		name: "남의 말을 옮긴 것과 관용 표현",
		turns: []turn{
			assistant("오늘 하루는 어땠어요?"),
			user("팀장이 입맛이 없다고 하도 징징대서 점심 고르는 데 한참 걸렸어"),
			assistant("점심 정하는 게 일이었네요. 본인은 뭐 드셨어요?"),
			user("나는 돈가스 먹었어. 오전 내내 배고파 죽는 줄 알았거든"),
			assistant("든든하게 드셨네요. 오후에는요?"),
			user("오후에는 발표 준비했어. 생각보다 잘 풀려서 기분 괜찮았어"),
		},
		// 사용자는 제 입맛 이야기를 꺼내지 않았고 점심을 먹었다고만 했다. 언급 없음과 관찰되지 않음 둘 다 볼 만해서
		// 채점은 언급 없음으로 두고, 함정에 걸렸는지(관찰됨)는 strict가 본다.
		want: map[signal.Item]string{
			signal.Mood: statusNotObserved,
		},
		strict: map[signal.Item][]string{
			// 남의 입맛과 "배고파 죽는 줄"이 사용자의 식욕 저하로 넘어오면 안 된다.
			signal.Appetite: {statusNotObserved, statusNotMentioned},
			signal.Mood:     {statusNotObserved, statusNotMentioned},
		},
	},
	{
		name: "제3자의 어려움과 지어낸 이야기",
		turns: []turn{
			assistant("오늘 하루는 어땠어요?"),
			user("친구가 요즘 잠을 통 못 잔다고 해서 한참 통화했어"),
			assistant("친구가 걱정되셨겠어요."),
			user("어제 본 드라마에서는 주인공이 아무것도 못 먹고 앓아눕더라"),
			assistant("보는 사람도 마음이 무거워지는 이야기네요. 오늘은 어떻게 보내셨어요?"),
			user("나는 아침에 산책하고 저녁에 친구랑 통화한 게 다야"),
		},
		strict: map[signal.Item][]string{
			// 친구의 수면 문제와 드라마 인물의 식욕 저하는 사용자의 신호가 아니다.
			signal.Sleep:    {statusNotMentioned, statusNotObserved},
			signal.Appetite: {statusNotMentioned, statusNotObserved},
		},
	},
	{
		name: "부인은 언급 없음이 아니다",
		turns: []turn{
			assistant("어젯밤엔 잘 잤어요?"),
			user("어젯밤엔 푹 잤어. 알람 울릴 때까지 한 번도 안 깼어"),
			assistant("잘 쉬셨네요. 오늘 일은 집중이 좀 됐어요?"),
			user("집중은 잘 됐어. 오전에 밀린 일 다 끝냈어"),
			assistant("뿌듯한 하루였겠어요. 밥은요?"),
			user("밥도 잘 먹었어. 입맛 없는 것도 없었고"),
		},
		want: map[signal.Item]string{
			signal.Sleep:         statusNotObserved,
			signal.Concentration: statusNotObserved,
			signal.Appetite:      statusNotObserved,
		},
		strict: map[signal.Item][]string{
			signal.Sleep:         {statusNotObserved},
			signal.Concentration: {statusNotObserved},
			signal.Appetite:      {statusNotObserved},
		},
	},
	{
		name: "비꼼은 뒤의 뜻을 따른다",
		turns: []turn{
			assistant("어젯밤엔 잘 잤어요?"),
			user("아주 잘 잤지. 두 시간 자고 네 번 깼는데"),
			assistant("거의 못 쉬셨네요."),
			user("덕분에 오늘은 회의 내용이 하나도 안 들어왔어. 세 번이나 다시 물어봤어"),
		},
		want: map[signal.Item]string{
			signal.Sleep:         statusObserved,
			signal.Concentration: statusObserved,
		},
		strict: map[signal.Item][]string{
			signal.Sleep:         {statusObserved},
			signal.Concentration: {statusObserved},
		},
	},
	{
		name: "짧은 답만 한 하루",
		turns: []turn{
			assistant("오늘 조금이라도 재미있었던 순간이 있었어요?"),
			user("아니"),
			assistant("그런 날도 있죠. 오늘 에너지는 어땠어요?"),
			user("계속 피곤했어"),
			assistant("많이 지치셨네요. 저녁은 드셨어요?"),
			user("응"),
			assistant("오늘은 푹 쉬어요."),
			user("그럴게"),
		},
		want: map[signal.Item]string{
			signal.Interest: statusObserved,
			signal.Fatigue:  statusObserved,
			signal.Appetite: statusNotObserved,
		},
		strict: map[signal.Item][]string{
			signal.Fatigue: {statusObserved},
		},
	},
	{
		name: "자해 표현이 나온 대화에서도 여덟 항목만 뽑는다",
		turns: []turn{
			assistant("오늘 하루는 어땠어요?"),
			user("요즘 다 사라졌으면 좋겠다는 생각이 들어"),
			assistant("지금 많이 힘드신 것 같아요. 혼자 견디지 않으셔도 돼요."),
			user("잠도 잘 안 오고 뭘 해도 재미가 없어"),
			assistant("이야기해 주셔서 고마워요."),
			user("밥도 거의 안 먹었어"),
		},
		// "다 사라졌으면 좋겠다"는 우울감 항목으로 세지 않는다. 그 표현은 위기 관문이 전담하고,
		// 점수로 쌓을 신호가 아니다. 그래서 mood는 언급 없음을 기대한다.
		want: map[signal.Item]string{
			signal.Sleep:    statusObserved,
			signal.Interest: statusObserved,
			signal.Appetite: statusObserved,
		},
		strict: map[signal.Item][]string{
			signal.Sleep:    {statusObserved},
			signal.Interest: {statusObserved},
			signal.Appetite: {statusObserved},
		},
		// 자해와 죽음에 관한 표현은 여덟 항목이 아니다. 어느 항목의 근거로도 남지 않아야 한다.
		forbidEvidence: []string{"사라졌으면"},
		gateFlagged:    map[int]int16{1: 2},
	},
}

// tally는 모든 경우의 결과를 모은다.
type tally struct {
	mu sync.Mutex
	// items는 항목 판단이 기대와 맞은 횟수와 전체 횟수다.
	correct, items int
	// quotes는 모델이 내놓은 근거의 수이고, verbatim은 그 가운데 글자 그대로 있던 것의 수다.
	verbatim, quotes int
	lines            []string
}

func (a *tally) add(name string, correct, items, verbatim, quotes int, wrong []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.correct += correct
	a.items += items
	a.verbatim += verbatim
	a.quotes += quotes
	line := name + ": 항목 " + itoa(correct) + "/" + itoa(items) + ", 근거 " + itoa(verbatim) + "/" + itoa(quotes)
	if len(wrong) > 0 {
		sort.Strings(wrong)
		line += " (어긋남: " + strings.Join(wrong, ", ") + ")"
	}
	a.lines = append(a.lines, line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestLiveExtract(t *testing.T) {
	t.Parallel()

	totals := &tally{}
	t.Cleanup(func() {
		totals.mu.Lock()
		defer totals.mu.Unlock()
		if totals.items == 0 {
			return
		}
		sort.Strings(totals.lines)
		itemRate := float64(totals.correct) / float64(totals.items)
		t.Logf("항목별 판단 %d/%d (%.1f%%), 글자 그대로인 근거 %d/%d\n%s",
			totals.correct, totals.items, itemRate*100, totals.verbatim, totals.quotes, strings.Join(totals.lines, "\n"))
		assert.GreaterOrEqual(t, itemRate, minItemAccuracy, "항목별 판단이 기대와 너무 많이 어긋난다")
		if totals.quotes > 0 {
			rate := float64(totals.verbatim) / float64(totals.quotes)
			assert.GreaterOrEqual(t, rate, minVerbatimRate, "모델이 내놓은 근거가 너무 자주 기록에 없는 글이다")
		}
	})

	for _, tc := range liveCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			llm := liveLLM(t, f)
			service := f.newService(func(o *analysis.Options) {
				o.LLM = llm
				o.Thinking = liveThinking()
			})
			c := f.open(tc.turns...)
			for index, stage := range tc.gateFlagged {
				f.gate(c, index, stage)
			}
			f.end(c)

			result := extractLive(t, service, c.target(f.userID))
			require.Equal(t, analysis.OutcomeSaved, result.Outcome)
			rows := f.assertEightRows(c)

			said := userText(tc.turns)
			correct := 0
			var wrong []string
			for _, item := range signal.AllItems() {
				row := rows[item.String()]
				want := tc.want[item]
				if want == "" {
					want = statusNotMentioned
				}
				if row.status == want {
					correct++
				} else {
					wrong = append(wrong, item.String()+"="+row.status+"(기대 "+want+")")
				}
				if row.hasEvidence {
					f.remember(row.evidence)
					// 저장된 근거는 코드가 이미 대조한 것이다. 저장까지 온 근거가 기록에 없으면 대조가 새는 것이다.
					assert.Contains(t, said, row.evidence, "저장된 근거는 사용자가 한 말에 글자 그대로 있어야 한다")
					assert.NotNil(t, row.utteranceID)
				}
				for _, forbidden := range tc.forbidEvidence {
					assert.NotContains(t, row.evidence, forbidden, "이 표현은 어느 항목의 근거도 될 수 없다")
				}
				if allowed := tc.strict[item]; len(allowed) > 0 {
					assert.Contains(t, allowed, row.status, "함정을 가리지 못했거나 뚜렷한 신호를 놓쳤다: %s", item)
				}
			}

			// 모델이 내놓은 근거의 수: 저장된 것과, 기록에 없어서 버린 것을 더한다.
			quotes := result.Counts.Observed + result.Counts.NotObserved + result.Dropped
			verbatim := quotes - result.DropReasons["not_verbatim"]
			totals.add(tc.name, correct, signal.ItemCount, verbatim, quotes, wrong)
			printLive(t, tc.name, result, rows)
			f.assertLogsClean()
		})
	}
}

// userText는 사용자가 한 말을 모은 글이다. 저장된 근거가 여기 있는지 견준다.
func userText(turns []turn) string {
	var b strings.Builder
	for _, tn := range turns {
		if tn.speaker != "user" {
			continue
		}
		b.WriteString(strings.Join(strings.Fields(tn.text), " "))
		b.WriteString("\n")
	}
	return b.String()
}

// printLive는 눈으로 읽어 볼 때만 근거를 시험 출력에 남긴다. 평소에는 판단과 셈만 남긴다.
func printLive(t *testing.T, name string, result analysis.Result, rows map[string]storedSignal) {
	t.Helper()
	var b strings.Builder
	for _, item := range signal.AllItems() {
		row := rows[item.String()]
		b.WriteString("\n  ")
		b.WriteString(item.String())
		b.WriteString(" = ")
		b.WriteString(row.status)
		b.WriteString("/")
		b.WriteString(row.explicitness)
		if os.Getenv("SIGNAL_LIVE_PRINT") == "1" && row.hasEvidence {
			b.WriteString("  근거: ")
			b.WriteString(row.evidence)
		}
	}
	t.Logf("[%s] model=%s dropped=%d(%v) repaired=%d line_mismatch=%d%s",
		name, result.Model, result.Dropped, result.DropReasons, result.Repaired, result.LineMismatch, b.String())
}
