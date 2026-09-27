package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/core/assess"
	"github.com/sirin-interact/tmrlife/server/internal/core/confidence"
	"github.com/sirin-interact/tmrlife/server/internal/core/params"
	"github.com/sirin-interact/tmrlife/server/internal/core/score"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
	"github.com/sirin-interact/tmrlife/server/prompts"
)

const seedPassword = "dawn-over-the-quiet-harbor-27"

// seedEnv는 심는 명령이 기대는 설정을 걸고 그 데이터베이스의 접속 풀을 돌려준다.
// 언어 모델은 정해 둔 답으로 돈다. 심는 길에서 실제 모델을 부르지 않는다.
func seedEnv(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testdb.New(t)
	t.Setenv("DATABASE_URL", pool.Config().ConnString())
	t.Setenv("DATA_KEK_V1", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32)))
	t.Setenv("APP_ENV", "dev")
	t.Setenv("AI_PROVIDER", "scripted")
	t.Setenv("LOG_LEVEL", "error")
	// 설정이 받아 주는 가장 싼 값이다. 계정을 만들 때 해시를 한 번 계산한다.
	t.Setenv("PASSWORD_ARGON2_MEMORY_KIB", "7168")
	t.Setenv("PASSWORD_ARGON2_TIME", "1")
	return pool
}

func TestRun_SeedDemo(t *testing.T) {
	t.Run("시연 계정과 지난 며칠치 기록을 만들고 신호까지 뽑아 둔다", func(t *testing.T) {
		const days = 3
		pool := seedEnv(t)
		stdout, stderr := &syncBuffer{}, &syncBuffer{}

		code := run(t.Context(), []string{
			"seed", "demo", "--email=demo@example.com", "--password=" + seedPassword, "--days=3",
		}, stdout, stderr)
		require.Equal(t, exitOK, code, "%s%s", stdout, stderr)
		// stdout에는 만든 계정의 ID만 적힌다. 다른 명령에 넘길 수 있게 둔 값이다.
		assert.Len(t, strings.TrimSpace(stdout.String()), 36)

		ctx := t.Context()
		var isDemo bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT is_demo FROM users WHERE email = 'demo@example.com'`).Scan(&isDemo))
		assert.True(t, isDemo, "시연 계정으로 표시해야 내부 확인 화면이 열린다")

		var (
			dayCount          int
			conversationCount int
			analysedCount     int
			signalCount       int
			observedCount     int
			withEvidence      int
			diaryCount        int
		)
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM days),
			       (SELECT count(*) FROM conversations),
			       (SELECT count(*) FROM conversations WHERE analysis_status = 'done'),
			       (SELECT count(*) FROM signals),
			       (SELECT count(*) FROM signals WHERE status = 'observed'),
			       (SELECT count(*) FROM signals WHERE evidence_enc IS NOT NULL),
			       (SELECT count(*) FROM diaries)
		`).Scan(&dayCount, &conversationCount, &analysedCount, &signalCount,
			&observedCount, &withEvidence, &diaryCount))

		assert.Equal(t, days, dayCount, "하루에 대화 하나씩")
		assert.Equal(t, days, conversationCount)
		assert.Equal(t, days, analysedCount, "심는 명령이 끝나면 분석도 끝나 있어야 한다")
		// 분석이 끝난 대화에는 여덟 항목의 행이 모두 있다. 언급된 항목만 남기면 조용한 날이 대화한 일수에서 빠진다.
		assert.Equal(t, days*8, signalCount)
		assert.Positive(t, observedCount, "심는 이야기에서 신호가 하나도 읽히지 않으면 시연 화면이 비어 보인다")
		assert.Positive(t, withEvidence, "언급된 항목에는 사용자가 한 말이 근거로 남아야 한다")
		assert.Equal(t, days, diaryCount, "그날의 일기 초안도 함께 써 둔다")

		// 마지막으로 심은 하루(오늘)를 실제 경로가 어떻게 읽었는지 본다.
		// 화면에 나오는 숫자는 TestSeedScriptEvaluation이 이 읽기를 전제로 못 박아 두므로,
		// 저장까지 지난 결과가 그 전제와 같아야 두 시험이 같은 것을 말한다.
		rows, err := pool.Query(ctx, `
			SELECT s.item, s.status
			FROM signals AS s
			JOIN days AS d ON d.id = s.day_id
			WHERE d.record_date = (SELECT max(record_date) FROM days)
			ORDER BY s.item
		`)
		require.NoError(t, err)
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var item, status string
			require.NoError(t, rows.Scan(&item, &status))
			got[item] = status
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, map[string]string{
			"interest": "observed", "mood": "observed", "sleep": "observed", "self_blame": "observed",
			"appetite": "not_observed", "fatigue": "not_mentioned",
			"concentration": "not_mentioned", "psychomotor": "not_mentioned",
		}, got)
	})

	// 지어낸 기록을 심고 비밀번호를 인자로 받는 명령이다. 운영 데이터베이스에 둘 일이 아니다.
	// 나머지 설정은 운영에서도 통하는 값으로 채운다. 설정이 먼저 걸러 주는 것에 기대지 않고
	// "설정은 멀쩡한데도 거부한다"를 본다.
	t.Run("운영에서는 돌지 않는다", func(t *testing.T) {
		seedEnv(t)
		t.Setenv("APP_ENV", "prod")
		t.Setenv("PUBLIC_ORIGIN", "https://naeil.example")
		t.Setenv("SESSION_COOKIE_SECURE", "true")
		t.Setenv("AI_PROVIDER", "gemini")
		t.Setenv("GEMINI_API_KEY", "not-a-real-key")
		t.Setenv("DATA_KEK_V1", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5c}, 32)))
		stdout, stderr := &syncBuffer{}, &syncBuffer{}

		code := run(t.Context(), []string{
			"seed", "demo", "--email=demo@example.com", "--password=" + seedPassword,
		}, stdout, stderr)

		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), "refused when APP_ENV=prod")
	})

	t.Run("주소와 비밀번호가 없으면 쓰임새만 알려준다", func(t *testing.T) {
		stdout, stderr := &syncBuffer{}, &syncBuffer{}
		assert.Equal(t, exitUsage, run(t.Context(), []string{"seed", "demo"}, stdout, stderr))
		assert.Contains(t, stderr.String(), "server seed demo")
	})

	t.Run("심을 수 없는 날 수는 받지 않는다", func(t *testing.T) {
		for _, days := range []string{"0", "-1", "9999"} {
			stdout, stderr := &syncBuffer{}, &syncBuffer{}
			code := run(t.Context(), []string{
				"seed", "demo", "--email=demo@example.com", "--password=" + seedPassword, "--days=" + days,
			}, stdout, stderr)
			assert.Equal(t, exitUsage, code, days)
			assert.Contains(t, stderr.String(), "--days must be between")
		}
	})

	t.Run("demo 말고는 심지 않는다", func(t *testing.T) {
		stdout, stderr := &syncBuffer{}, &syncBuffer{}
		assert.Equal(t, exitUsage, run(t.Context(), []string{"seed", "everything"}, stdout, stderr))
		assert.Equal(t, exitUsage, run(t.Context(), []string{"seed"}, stdout, stderr))
	})
}

// 심는 이야기의 길이와 자리가 바뀌면 브라우저 흐름 테스트가 칸 번호로 세어 둔 기대값이 어긋난다.
// 그 테스트(web/e2e/trend.spec.ts)는 "오늘부터 며칠 전"으로 칸을 찾고, 이야기의 마지막 날이 오늘이다.
func TestSeedScript(t *testing.T) {
	assert.Len(t, seedScript, defaultSeedDays,
		"기본 날 수로 심으면 이야기가 처음부터 끝까지 한 번 들어가야 한다")
	assert.LessOrEqual(t, defaultSeedDays, maxSeedDays)

	for i, day := range seedScript {
		assert.GreaterOrEqual(t, len(day.said), 5,
			"이야기 %d: 하루에 대여섯 마디는 있어야 여덟 항목 가운데 다섯 이상이 언급된다", i)
		assert.NotEmpty(t, day.diary, "이야기 %d에는 그날의 일기가 있어야 한다", i)
		// 발화를 이어 붙인 글은 사람이 쓴 일기로 읽히지 않는다. 달력에서 지난 하루를 열면 나오는 글이다.
		assert.NotContains(t, day.diary, "이런 이야기를 했다", "이야기 %d", i)
		for _, said := range day.said {
			assert.NotContains(t, day.diary, said,
				"이야기 %d: 일기는 발화를 그대로 옮긴 글이 아니라 일기체로 다시 쓴 글이다", i)
		}
	}

	t.Run("이야기의 마지막 날이 오늘이고 하루씩 거슬러 짝지어진다", func(t *testing.T) {
		assert.Equal(t, seedScript[len(seedScript)-1], seedDayAt(0))
		assert.Equal(t, seedScript[0], seedDayAt(len(seedScript)-1))
		// 이야기보다 길게 심으면 가장 앞의 평범한 하루를 되풀이한다.
		assert.Equal(t, seedScript[0], seedDayAt(len(seedScript)))
		assert.Equal(t, seedScript[0], seedDayAt(maxSeedDays))
	})
}

// seedEvaluation은 심는 이야기를 실제 추출 경로와 같은 낱말 표에 그대로 통과시켜 계산 코어의 평가를 구한다.
//
// 데이터베이스를 쓰지 않는다. 여기서 보려는 것은 "심은 이야기가 화면에 어떤 숫자로 나오는가"이고,
// 그 숫자는 판단과 조정 값만으로 정해진다. 저장과 근거 대조를 지나가는지는 TestRun_SeedDemo가 본다.
func seedEvaluation(t *testing.T, days int) assess.Evaluation {
	t.Helper()
	llm, err := scripted.New(config.AIProviderScripted, scripted.Options{})
	require.NoError(t, err)
	schema, err := prompts.FS.ReadFile(analysis.PromptTask + "/schema.json")
	require.NoError(t, err, "지시문의 스키마를 그대로 보낸다")

	// 고정한 기준일이다. 날마다 다른 결과가 나오면 이 시험이 무엇을 재는지 알 수 없다.
	today, err := recorddate.New(2026, time.September, 27)
	require.NoError(t, err)

	signalDays := make([]signal.Day, 0, days)
	for offset := days - 1; offset >= 0; offset-- {
		date := today.AddDays(-offset)
		day, err := signal.MergeDay(date, seedRowsFor(t, llm, schema, date, seedDayAt(offset)))
		require.NoError(t, err)
		signalDays = append(signalDays, day)
	}

	evaluation, err := assess.EvaluateLive(signalDays, today, params.Default())
	require.NoError(t, err)
	return evaluation
}

// seedRowsFor는 하루의 말을 정해 둔 답 모델에 보내 여덟 항목의 신호 행을 받는다.
// 보내는 글의 꼴은 서버가 보내는 것과 같다(사용자의 말에만 번호가 붙는다).
func seedRowsFor(
	t *testing.T, llm *scripted.LLM, schema []byte, date recorddate.Date, day seedDay,
) []signal.Row {
	t.Helper()
	lines := []string{"대화 기록:", "[상대] 오늘 하루는 어땠어요?"}
	for i, said := range day.said {
		lines = append(lines,
			strconv.Itoa(i+1)+". "+said,
			"[상대] 그랬군요. 더 이야기해 주세요.",
		)
	}

	resp, err := llm.Generate(t.Context(), ai.Request{
		Task: analysis.PromptTask, System: "신호를 뽑는다",
		Messages:   []ai.Message{{Role: ai.RoleUser, Text: strings.Join(lines, "\n")}},
		JSONSchema: schema,
	})
	require.NoError(t, err)

	var reply map[string]struct {
		Status       string `json:"status"`
		Explicitness string `json:"explicitness"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Text), &reply))

	rows := make([]signal.Row, 0, signal.ItemCount)
	for _, item := range signal.AllItems() {
		answer, ok := reply[item.String()]
		require.True(t, ok, "%s: 여덟 항목이 모두 있어야 한다", item)
		status, err := signal.ParseStatus(answer.Status)
		require.NoError(t, err)
		explicitness, err := signal.ParseExplicitness(answer.Explicitness)
		require.NoError(t, err)
		rows = append(rows, signal.Row{
			ConversationID: date.String(), Item: item,
			Status: status, Explicitness: explicitness,
		})
	}
	return rows
}

// 심는 이야기가 화면에 어떤 숫자로 나오는지를 못 박는다.
//
// 심사위원이 보는 것은 이 숫자들이다. 되풀이되는 대본으로 심으면 평소 구간과 최근 구간의 빈도가 같아져서
// 세 줄이 모두 "평소와 비슷해요"가 되고, 변화 탐지는 점 두 개짜리 그래프가 되고, 개입 단계는 평평해진다.
// 그러면 이 서비스가 보여주겠다고 하는 것이 시연에서 한 번도 작동하지 않는다.
// 이야기를 고칠 때는 아래 값이 함께 움직이므로, 고친 뒤에 이 시험이 무엇을 말하는지 읽는다.
func TestSeedScriptEvaluation(t *testing.T) {
	e := seedEvaluation(t, defaultSeedDays)
	p := params.Default()

	t.Run("평소는 앞 두 주로 잡히고 최근 두 주와 겹치지 않는다", func(t *testing.T) {
		assert.True(t, e.Baseline.Established, "평소가 잡혀야 견주는 말이 화면에 나온다")
		assert.Equal(t, p.Baseline.WindowDays, e.Baseline.Days)
		assert.False(t, e.Baseline.Extended, "앞 두 주 내내 대화했으므로 기간을 늘릴 일이 없다")
		assert.Equal(t, 7, e.Baseline.ObservedTotal)
		assert.InDelta(t, 0.5, e.Baseline.Mu, 1e-9)
		// 평소 구간의 마지막 날 다음 날부터 최근 창이 시작한다.
		assert.Equal(t, e.Score.Window.From, e.Baseline.End.AddDays(1))
	})

	t.Run("추정 점수는 중간 구간이고 신뢰도는 높음이다", func(t *testing.T) {
		assert.False(t, e.Score.Insufficient)
		assert.Equal(t, p.Window.Days, e.Score.ConversationDays, "창의 날마다 대화가 있다")
		assert.Equal(t, 11, e.Score.Total)
		assert.Equal(t, score.Moderate, e.Score.Band)
		assert.Equal(t, confidence.High, e.Confidence.Level)
		assert.Equal(t, signal.ItemCount, e.Confidence.MentionedItems, "여덟 항목이 모두 언급된다")
	})

	t.Run("항목마다 관찰된 일수가 정해진 만큼이다", func(t *testing.T) {
		want := map[signal.Item]int{
			signal.Interest: 5, signal.Mood: 9, signal.Sleep: 10, signal.Fatigue: 7,
			signal.Appetite: 2, signal.SelfBlame: 5, signal.Concentration: 4, signal.Psychomotor: 2,
		}
		for _, item := range e.Score.Items {
			assert.Equal(t, want[item.Item], item.ObservedDays, "%s", item.Item)
		}
	})

	t.Run("세 줄 모두 평소보다 잦다고 나온다", func(t *testing.T) {
		require.Len(t, e.Trend.Rows, signal.TrendRowCount)
		want := map[signal.TrendRow]struct{ window, usual int }{
			signal.TrendMood:   {window: 13, usual: 2},
			signal.TrendSleep:  {window: 10, usual: 2},
			signal.TrendEnergy: {window: 7, usual: 2},
		}
		for _, line := range e.Trend.Rows {
			assert.Equal(t, assess.MoreOften, line.Comparison, "%s", line.Row)
			assert.Equal(t, want[line.Row].window, line.ObservedDays, "%s 최근", line.Row)
			assert.Equal(t, want[line.Row].usual, line.Usual.ObservedDays, "%s 평소", line.Row)
			assert.Len(t, line.Marks, p.Window.Days)
		}
	})

	t.Run("변화 탐지가 켜지고 흐름을 그릴 점이 창을 채운다", func(t *testing.T) {
		assert.True(t, e.Change.State.Running)
		assert.True(t, e.Change.State.Detected, "평소보다 잦아진 날이 쌓이면 감지가 켜진다")
		assert.Greater(t, e.Change.State.S, p.CUSUM.H)
		assert.Len(t, e.Change.Series, p.Window.Days, "점 하나짜리 그래프는 흐름을 보여주지 못한다")
	})

	t.Run("개입 단계가 0에서 2로 올라간다", func(t *testing.T) {
		assert.Equal(t, 2, int(e.Stage.State.Stage))
		assert.Len(t, e.Stage.Series, defaultSeedDays)
		first, last := e.Stage.Series[0], e.Stage.Series[len(e.Stage.Series)-1]
		assert.Equal(t, 0, int(first.Stage), "평범한 날들로 시작한다")
		assert.Equal(t, 2, int(last.Stage))
	})
}
