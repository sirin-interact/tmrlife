package scripted_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
)

// signalAnswer는 마음 신호 추출의 답에서 항목 하나다.
type signalAnswer struct {
	Status       string `json:"status"`
	Explicitness string `json:"explicitness"`
	Line         int    `json:"line"`
	Evidence     string `json:"evidence"`
}

// signalItems는 서버가 답에서 찾는 여덟 항목이다. 하나라도 빠지면 서버는 답 전체를 버린다.
var signalItems = []string{
	"interest", "mood", "sleep", "fatigue", "appetite", "self_blame", "concentration", "psychomotor",
}

// transcript는 서버가 보내는 글의 꼴이다(analysis 패키지의 buildMessage와 같다).
// 사용자의 말에만 번호가 붙고, 상대의 말에는 [상대]가 붙는다.
func transcript(lines ...string) ai.Message {
	return user("대화 기록:\n" + strings.Join(lines, "\n"))
}

func extract(t *testing.T, llm *scripted.LLM, message ai.Message) map[string]signalAnswer {
	t.Helper()
	schema, err := os.ReadFile("../../../prompts/signal_extract/schema.json")
	require.NoError(t, err, "지시문의 스키마를 그대로 보낸다")

	resp, err := llm.Generate(t.Context(), ai.Request{
		Task: "signal_extract", System: "신호를 뽑는다", Messages: []ai.Message{message}, JSONSchema: schema,
	})
	require.NoError(t, err)

	var out map[string]signalAnswer
	require.NoError(t, json.Unmarshal([]byte(resp.Text), &out))
	for _, item := range signalItems {
		require.Contains(t, out, item, "여덟 항목이 모두 있어야 한다")
	}
	require.Len(t, out, len(signalItems), "모르는 항목을 더하지 않는다")
	return out
}

func TestSignalExtract(t *testing.T) {
	llm := newLLM(t, scripted.Options{})

	t.Run("걸린 줄을 글자 그대로 근거로 옮긴다", func(t *testing.T) {
		const (
			sleep    = "어젯밤에 세 번 깼어"
			appetite = "밥은 잘 먹었어"
		)
		got := extract(t, llm, transcript(
			"[상대] 오늘 하루는 어땠어요?",
			"1. "+sleep,
			"[상대] 잠을 설치셨네요.",
			"2. "+appetite,
		))

		assert.Equal(t, signalAnswer{Status: "observed", Explicitness: "direct", Line: 1, Evidence: sleep}, got["sleep"])
		assert.Equal(t, signalAnswer{Status: "not_observed", Explicitness: "direct", Line: 2, Evidence: appetite}, got["appetite"])
		assert.Equal(t, signalAnswer{Status: "not_mentioned", Explicitness: "none"}, got["mood"])
	})

	t.Run("상대의 말은 근거가 되지 않는다", func(t *testing.T) {
		// 번호가 없는 줄은 보지 않는다. 서버의 근거 대조도 같은 것을 막지만, 여기서 아예 고르지 않는다.
		got := extract(t, llm, transcript("[상대] 요즘 잠을 못 자셨어요?", "1. 그건 아니야"))
		assert.Equal(t, "not_mentioned", got["sleep"].Status)
		assert.Empty(t, got["sleep"].Evidence)
	})

	t.Run("미루어 본 근거에는 그렇게 적는다", func(t *testing.T) {
		const line = "괜히 속상해서 한참 앉아 있었어"
		got := extract(t, llm, transcript("1. "+line))
		assert.Equal(t, signalAnswer{Status: "observed", Explicitness: "indirect", Line: 1, Evidence: line}, got["mood"])
	})

	// 부정을 보지 못하면 "피로 신호 관찰됨" 옆에 "피곤하지도 않아"가 근거로 걸린다.
	// 근거 화면은 판단과 그 판단의 까닭을 나란히 보여주는 화면이라, 정반대인 문장이 걸리는 것이 가장 나쁘다.
	t.Run("낱말 뒤에 붙은 부정은 관찰됨으로 읽지 않는다", func(t *testing.T) {
		const (
			fatigue   = "오늘은 피곤하지도 않았어"
			selfBlame = "내 탓이라고 생각하진 않아"
		)
		got := extract(t, llm, transcript("1. "+fatigue, "2. "+selfBlame))

		assert.Equal(t, signalAnswer{Status: "not_observed", Explicitness: "direct", Line: 1, Evidence: fatigue},
			got["fatigue"])
		assert.Equal(t, signalAnswer{Status: "not_observed", Explicitness: "direct", Line: 2, Evidence: selfBlame},
			got["self_blame"])
	})

	t.Run("부정이 멀리 있는 다른 자리는 앞의 낱말을 뒤집지 않는다", func(t *testing.T) {
		const line = "우울한데 아무것도 하고 싶지 않아"
		got := extract(t, llm, transcript("1. "+line))

		assert.Equal(t, "observed", got["mood"].Status, "부정은 '아무것도 하고 싶지'에 붙은 것이다")
		assert.Equal(t, line, got["mood"].Evidence)
		assert.Equal(t, "observed", got["interest"].Status)
	})

	t.Run("같은 항목을 뒤집은 줄과 그렇지 않은 줄이 함께 있으면 관찰됨이다", func(t *testing.T) {
		const observed = "저녁에는 많이 피곤했어"
		got := extract(t, llm, transcript("1. 아침에는 피곤하지 않았어", "2. "+observed))

		assert.Equal(t, "observed", got["fatigue"].Status)
		assert.Equal(t, observed, got["fatigue"].Evidence)
	})

	t.Run("사용자의 말이 없으면 여덟 항목이 모두 언급 없음이다", func(t *testing.T) {
		got := extract(t, llm, transcript("[상대] 오늘 하루는 어땠어요?"))
		for _, item := range signalItems {
			assert.Equal(t, "not_mentioned", got[item].Status, item)
			assert.Equal(t, "none", got[item].Explicitness, item)
			assert.Zero(t, got[item].Line, item)
			assert.Empty(t, got[item].Evidence, item)
		}
	})
}
