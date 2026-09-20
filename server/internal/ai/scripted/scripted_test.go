package scripted_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/config"
)

const gateSchema = `{"type":"object","properties":{"stage":{"type":"integer","minimum":0,"maximum":3},"evidence":{"type":"string"},"reason":{"type":"string"}},"required":["stage","evidence","reason"]}`

func newLLM(t *testing.T, opts scripted.Options) *scripted.LLM {
	t.Helper()
	llm, err := scripted.New(config.AIProviderScripted, opts)
	require.NoError(t, err)
	return llm
}

func user(text string) ai.Message  { return ai.Message{Role: ai.RoleUser, Text: text} }
func model(text string) ai.Message { return ai.Message{Role: ai.RoleModel, Text: text} }

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		provider config.AIProvider
		opts     scripted.Options
		ok       bool
	}{
		{"설정이 scripted를 골랐을 때만 만들어진다", config.AIProviderScripted, scripted.Options{}, true},
		{"설정이 실제 공급자를 골랐으면 만들어지지 않는다", config.AIProviderGemini, scripted.Options{}, false},
		{"설정값이 비어 있어도 만들어지지 않는다", "", scripted.Options{}, false},
		{"모르는 규칙은 받지 않는다", config.AIProviderScripted, scripted.Options{Tasks: map[string]scripted.Behavior{"gate": 99}}, false},
		{"지시문 ID의 꼴이 아닌 이름은 받지 않는다", config.AIProviderScripted, scripted.Options{Tasks: map[string]scripted.Behavior{"Gate Task": scripted.Gate}}, false},
		{"음수인 지연은 받지 않는다", config.AIProviderScripted, scripted.Options{Latency: -time.Second}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm, err := scripted.New(tt.provider, tt.opts)
			if tt.ok {
				require.NoError(t, err)
				require.NotNil(t, llm)
				return
			}
			require.Error(t, err)
			assert.Nil(t, llm)
		})
	}
}

func TestTasks(t *testing.T) {
	llm := newLLM(t, scripted.Options{Tasks: map[string]scripted.Behavior{
		"conversation": scripted.Conversation,
		"safety":       scripted.Gate,
		"safety-deep":  scripted.Diary,
	}})
	tests := []struct {
		name string
		task string
		want string // 답이 어떤 규칙에서 나왔는지 가릴 수 있는 조각. 비어 있으면 모르는 일이다.
	}{
		{"표에 있는 ID는 그 규칙으로 답한다", "conversation", "요"},
		{"표의 ID에 '-'로 이름을 덧붙인 ID는 같은 규칙을 따른다", "conversation-crisis", "요"},
		{"표의 ID에 '_'로 이름을 덧붙인 ID도 같은 규칙을 따른다", "conversation_reflect", "요"},
		{"앞머리가 같은 ID가 여럿이면 가장 길게 맞는 쪽을 따른다", "safety-deep-v2", "오늘 나는"},
		{"짧은 쪽에만 맞으면 짧은 쪽을 따른다", "safety-fast", `"stage"`},
		{"구분 글자 없이 앞머리만 같은 ID는 모르는 일이다", "conversational", ""},
		{"표에 없는 ID는 모르는 일이다", "extraction", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := llm.Generate(context.Background(), ai.Request{Task: tt.task, Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}})
			if tt.want == "" {
				require.ErrorIs(t, err, ai.ErrInvalidRequest)
				var aiErr *ai.Error
				require.ErrorAs(t, err, &aiErr)
				assert.Equal(t, "unknown_task", aiErr.Detail.Reason)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, resp.Text, tt.want)
			assert.Equal(t, scripted.ModelName, resp.Model)
			assert.Equal(t, ai.FinishStop, resp.FinishReason)
		})
	}

	t.Run("틀린 요청은 실제 구현과 같은 기준으로 거른다", func(t *testing.T) {
		_, err := llm.Generate(context.Background(), ai.Request{Task: "conversation"})
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
	})

	t.Run("표를 주지 않으면 서버의 지시문 ID를 모두 안다", func(t *testing.T) {
		defaults := newLLM(t, scripted.Options{})
		for _, task := range []string{"conversation", "conversation_check", "conversation_crisis", "gate", "diary_draft"} {
			_, err := defaults.Generate(context.Background(), ai.Request{Task: task, Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}})
			require.NoError(t, err, task)
		}
	})
}

// 서버의 출력 검사가 보는 것들이다. 대본의 답은 이 검사에 걸리면 안 된다.
var (
	sentenceEnd    = regexp.MustCompile(`[.!?…]+`)
	bannedInReply  = regexp.MustCompile(`[0-9]|상담|전문가|센터|당신|우울증|증상|될까요|여쭤|물어봐도`)
	allowedLetters = regexp.MustCompile(`^[가-힣 .?]+$`)
)

func TestConversation(t *testing.T) {
	llm := newLLM(t, scripted.Options{})

	t.Run("대화가 이어지면 답이 차례로 바뀌고, 어느 답도 출력 검사에 걸리지 않는다", func(t *testing.T) {
		messages := []ai.Message{model("오늘 하루는 어땠어요?")}
		seen := map[string]bool{}
		questionsInARow := 1
		for i := range 12 {
			messages = append(messages, user("오늘 친구랑 놀러갔다왔어"))
			resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation", System: "짧게 답한다.", Messages: messages})
			require.NoError(t, err)

			reply := resp.Text
			seen[reply] = true
			assert.LessOrEqual(t, len(sentenceEnd.FindAllString(reply, -1)), 2, "두 문장 이하: %s", reply)
			assert.LessOrEqual(t, strings.Count(reply, "?"), 1, "물음표는 하나 이하: %s", reply)
			assert.False(t, bannedInReply.MatchString(reply), "안내문, 허락 구하기, 평가하는 말이 없어야 한다: %s", reply)
			assert.True(t, allowedLetters.MatchString(reply), "한글, 마침표, 물음표만 쓴다: %s", reply)

			if strings.HasSuffix(reply, "?") {
				questionsInARow++
			} else {
				questionsInARow = 0
			}
			assert.LessOrEqual(t, questionsInARow, 2, "%d번째 답: 질문이 세 번 이어지면 안 된다", i+1)
			messages = append(messages, model(reply))
		}
		assert.GreaterOrEqual(t, len(seen), 4, "같은 답만 되풀이하지 않는다")
	})

	t.Run("직전 두 번의 AI 말이 모두 질문이면 질문 없이 답한다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation", Messages: []ai.Message{
			model("오늘 하루는 어땠어요?"), user("그냥 그랬어"),
			model("무슨 일 있었어요?"), user("몰라"),
		}})
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "?")
	})

	t.Run("직전 두 번 가운데 하나라도 질문이 아니면 물어도 된다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation", Messages: []ai.Message{
			model("오늘 하루는 어땠어요?"), user("그냥 그랬어"),
			model("그런 날도 있죠."), user("응"),
		}})
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "?")
	})

	t.Run("질문하지 말라는 지시를 알아보는 글귀를 주면 그 지시를 따른다", func(t *testing.T) {
		const hint = "이번 답은 질문으로 끝내지 않는다."
		hinted := newLLM(t, scripted.Options{NoQuestionHints: []string{hint, "  "}})
		base := ai.Request{Task: "conversation", System: "짧게 답한다.", Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}}

		resp, err := hinted.Generate(context.Background(), base)
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "?", "지시가 없으면 물어도 된다")

		inSystem := base
		inSystem.System += "\n" + hint
		resp, err = hinted.Generate(context.Background(), inSystem)
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "?")

		inMessage := base
		inMessage.Messages = []ai.Message{user("오늘 친구랑 놀러갔다왔어\n\n" + hint)}
		resp, err = hinted.Generate(context.Background(), inMessage)
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "?")
	})

	t.Run("연달아 나간 AI의 말이 한 메시지로 이어져 와도 줄마다 말 하나로 센다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation", Messages: []ai.Message{
			model("오늘 하루는 어땠어요?\n아직 거기 있어요?"), user("응 있어"),
		}})
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "?")
	})

	t.Run("글귀를 주지 않아도 서버가 질문을 막을 때 덧붙이는 말을 알아본다", func(t *testing.T) {
		req := ai.Request{Task: "conversation", Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}}
		req.System = "한두 문장으로 답한다. 매번 묻지 않는다.\n\n# 이번 답에서 특히 지킬 것\n- 바로 앞의 두 번을 모두 질문으로 끝냈다. 이번에는 묻지 않는다."
		resp, err := llm.Generate(context.Background(), req)
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "?")

		req.System = "한두 문장으로 답한다. 매번 묻지 않는다. 캐묻지 않는다."
		resp, err = llm.Generate(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "?", "평소의 지시문에 있는 말에는 걸리지 않는다")
	})

	t.Run("같은 요청에는 늘 같은 답을 낸다", func(t *testing.T) {
		req := ai.Request{Task: "conversation", Messages: []ai.Message{model("오늘 하루는 어땠어요?"), user("좋았어")}}
		first, err := llm.Generate(context.Background(), req)
		require.NoError(t, err)
		for range 5 {
			again, err := llm.Generate(context.Background(), req)
			require.NoError(t, err)
			assert.Equal(t, first, again)
		}
	})
}

func TestReflect(t *testing.T) {
	llm := newLLM(t, scripted.Options{})
	tests := []struct {
		name      string
		utterance string
		want      string
	}{
		{"사용자의 표현을 그대로 받아 하나만 묻는다", "그냥 다 사라졌으면 좋겠어", "방금 그냥 다 사라졌으면 좋겠어 하고 말했죠. 오늘 무슨 일 있었어요?"},
		{"첫 토막만 받는다. 문장 부호를 옮기면 세 문장이 된다", "몰라, 그냥 내가 없어도 아무도 모를 것 같아", "방금 몰라 하고 말했죠. 오늘 무슨 일 있었어요?"},
		{"긴 말은 낱말 경계에서 자른다", "요즘은 자고 일어나지 않았으면 좋겠다는 생각을 자주 하게 되는 것 같아서 걱정이야", "방금 요즘은 자고 일어나지 않았으면 좋겠다는 하고 말했죠. 오늘 무슨 일 있었어요?"},
		{"받을 한글이 없으면 묻기만 한다", "...", "오늘 무슨 일 있었어요?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation_check", Messages: []ai.Message{
				model("오늘 하루는 어땠어요?"), user("오늘 회사에서 크게 혼났어"), model("무슨 일 있었어요?"), user(tt.utterance),
			}})
			require.NoError(t, err)
			assert.Equal(t, tt.want, resp.Text)
			assert.Equal(t, 1, strings.Count(resp.Text, "?"), "직전에 두 번 물었어도 되묻는 턴에는 묻는다")
		})
	}
}

func TestCrisisFollow(t *testing.T) {
	llm := newLLM(t, scripted.Options{})
	messages := []ai.Message{model("말해줘서 고마워요. 지금은 안전한 곳에 있어요?")}
	for range 8 {
		messages = append(messages, user("몰라"))
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "conversation_crisis", Messages: messages})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(sentenceEnd.FindAllString(resp.Text, -1)), 2)
		assert.LessOrEqual(t, strings.Count(resp.Text, "?"), 1)
		assert.False(t, bannedInReply.MatchString(resp.Text), resp.Text)
		assert.NotRegexp(t, `푹 쉬|여기까지|잘 자`, resp.Text, "대화를 닫는 말을 하지 않는다")
		messages = append(messages, model(resp.Text))
	}
}

type gateAnswer struct {
	Stage    *int    `json:"stage"`
	Evidence *string `json:"evidence"`
	Reason   *string `json:"reason"`
}

func decodeGate(t *testing.T, text string) gateAnswer {
	t.Helper()
	var got gateAnswer
	require.NoError(t, json.Unmarshal([]byte(text), &got))
	require.NotNil(t, got.Stage)
	require.NotNil(t, got.Evidence)
	return got
}

func TestGate(t *testing.T) {
	llm := newLLM(t, scripted.Options{})

	stages := []struct {
		utterance string
		stage     int
	}{
		{"오늘 친구랑 놀러갔다왔어", 0},
		{"배고파 죽겠다", 0},
		{"죽을 만큼 피곤해", 0},
		{"드라마에서 주인공이 죽었어", 0},
		{"그냥 다 사라졌으면 좋겠어", 1},
		{"이제 그만하고 싶다", 1},
		{"자고 일어나지 않았으면 좋겠어", 1},
		{"예전에 죽고 싶다는 생각 한 적 있어", 1},
		{"몰라, 그냥 내가 없어도 아무도 모를 것 같아", 1},
		{"죽고 싶다", 2},
		{"어제 자해했어", 2},
		{"살 이유를 모르겠어", 2},
		{"약을 모아뒀어", 3},
		{"오늘 밤에 끝내려고", 3},
		{"그동안 고마웠어. 이게 마지막 일기일 거야", 3},
	}
	for _, tt := range stages {
		t.Run("낱말 표: "+tt.utterance, func(t *testing.T) {
			resp, err := llm.Generate(context.Background(), ai.Request{
				Task: "gate", JSONSchema: json.RawMessage(gateSchema), Messages: []ai.Message{user(tt.utterance)},
			})
			require.NoError(t, err)
			got := decodeGate(t, resp.Text)
			assert.Equal(t, tt.stage, *got.Stage)
			if tt.stage == 0 {
				assert.Empty(t, *got.Evidence, "0단계에는 근거가 없다")
				return
			}
			assert.NotEmpty(t, *got.Evidence)
			assert.Contains(t, tt.utterance, *got.Evidence, "근거는 발화에 글자 그대로 있어야 한다")
		})
	}

	t.Run("표시가 있으면 그 뒤의 발화만 판정한다. 앞선 대화의 낱말에 다시 걸리지 않는다", func(t *testing.T) {
		prompt := "직전 대화:\n사용자: 죽고 싶다\nAI: 말해 줘서 고마워요.\n\n마지막 발화:\n고마워. 이제 좀 괜찮아"
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "gate", JSONSchema: json.RawMessage(gateSchema), Messages: []ai.Message{user(prompt)}})
		require.NoError(t, err)
		assert.Equal(t, 0, *decodeGate(t, resp.Text).Stage)
	})

	t.Run("표시를 서버의 지시문에 맞춰 바꿀 수 있다", func(t *testing.T) {
		custom := newLLM(t, scripted.Options{UtteranceMarkers: []string{"[판정할 발화]"}})
		prompt := "사용자: 죽고 싶다\n[판정할 발화]\n약을 모아뒀어"
		resp, err := custom.Generate(context.Background(), ai.Request{Task: "gate", Messages: []ai.Message{user(prompt)}})
		require.NoError(t, err)
		got := decodeGate(t, resp.Text)
		assert.Equal(t, 3, *got.Stage)
		assert.Equal(t, "약을 모아", *got.Evidence)
	})

	const directAsk = "그런 생각이 들 만큼 힘들었네요. 혹시 죽고 싶다는 생각도 들어요?"
	afterDirectAsk := []struct {
		name      string
		utterance string
		stage     int
	}{
		{"직접 물은 뒤의 '응'은 2단계다", "응", 2},
		{"직접 물은 뒤의 '가끔'은 2단계다", "음.. 가끔 그래", 2},
		{"직접 물은 뒤에 아니라고 하면 0단계다", "그 정도는 아니야. 그냥 지친 거야", 0},
		{"직접 물은 뒤에도 다른 이야기는 0단계다", "잘 모르겠어", 0},
		{"직접 물은 뒤에도 낱말 표가 먼저다", "사실 약을 모아뒀어", 3},
	}
	for _, tt := range afterDirectAsk {
		t.Run(tt.name+" (대화를 메시지로 받은 경우)", func(t *testing.T) {
			resp, err := llm.Generate(context.Background(), ai.Request{Task: "gate", JSONSchema: json.RawMessage(gateSchema), Messages: []ai.Message{
				user("몰라, 그냥 내가 없어도 아무도 모를 것 같아"), model(directAsk), user(tt.utterance),
			}})
			require.NoError(t, err)
			assert.Equal(t, tt.stage, *decodeGate(t, resp.Text).Stage)
		})
		t.Run(tt.name+" (대화를 한 덩어리로 받은 경우)", func(t *testing.T) {
			prompt := "직전 대화:\n사용자: 몰라, 그냥 내가 없어도 아무도 모를 것 같아\nAI: " + directAsk + "\n\n마지막 발화:\n" + tt.utterance
			resp, err := llm.Generate(context.Background(), ai.Request{Task: "gate", JSONSchema: json.RawMessage(gateSchema), Messages: []ai.Message{user(prompt)}})
			require.NoError(t, err)
			got := decodeGate(t, resp.Text)
			assert.Equal(t, tt.stage, *got.Stage)
			if tt.stage > 0 {
				assert.Contains(t, tt.utterance, *got.Evidence)
			}
		})
	}

	t.Run("직접 묻지 않았으면 '응'은 0단계다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "gate", Messages: []ai.Message{
			user("오늘 좀 피곤했어"), model("푹 쉬었으면 좋겠어요."), user("응"),
		}})
		require.NoError(t, err)
		assert.Equal(t, 0, *decodeGate(t, resp.Text).Stage)
	})
}

func TestGate_SchemaShapes(t *testing.T) {
	llm := newLLM(t, scripted.Options{})
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{
			"스키마가 없으면 단계와 근거를 담는다", ``,
			`{"stage":2,"evidence":"죽고 싶","reason":"scripted"}`,
		},
		{
			"스키마에 있는 속성만 담는다",
			`{"type":"object","properties":{"stage":{"type":"integer"},"evidence":{"type":"string"}}}`,
			`{"stage":2,"evidence":"죽고 싶"}`,
		},
		{
			"공급자 고유의 대문자 타입 이름도 읽는다",
			`{"type":"OBJECT","properties":{"stage":{"type":"INTEGER"},"evidence":{"type":"STRING"},"reason":{"type":"STRING"}}}`,
			`{"stage":2,"evidence":"죽고 싶","reason":"scripted"}`,
		},
		{
			"모르는 속성은 타입에 맞는 빈 값으로 채운다",
			`{"type":"object","properties":{
				"stage":{"type":"integer"},"evidence":{"type":"string"},
				"confidence":{"type":"number","minimum":0.5},"urgent":{"type":"boolean"},"tags":{"type":"array","items":{"type":"string"}},
				"kind":{"type":"string","enum":["none","wish","plan"]},
				"detail":{"type":"object","properties":{"note":{"type":"string"},"stage":{"type":"integer"}}}}}`,
			`{"stage":2,"evidence":"죽고 싶","confidence":0.5,"urgent":false,"tags":[],"kind":"none","detail":{"note":"","stage":0}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ai.Request{Task: "gate", Messages: []ai.Message{user("죽고 싶다")}}
			if tt.schema != "" {
				req.JSONSchema = json.RawMessage(tt.schema)
			}
			resp, err := llm.Generate(context.Background(), req)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, resp.Text)
		})
	}
}

func TestDiary(t *testing.T) {
	llm := newLLM(t, scripted.Options{})
	const want = "오늘 나는 이런 이야기를 했다. 오늘 친구랑 놀러갔다왔어. 한강에서 자전거 탔어!"
	const entrySchema = `{"type":"object","properties":{"entry":{"type":"string"}},"required":["entry"]}`

	entryOf := func(t *testing.T, text string) string {
		t.Helper()
		var got struct {
			Entry *string `json:"entry"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &got))
		require.NotNil(t, got.Entry)
		return *got.Entry
	}

	t.Run("서버의 일기 작업이 보내는 꼴: 번호가 붙은 줄만 사용자의 말이다", func(t *testing.T) {
		message := "방식: 처음 쓰기\n\n사용자가 한 말:\n1. 오늘 친구랑 놀러갔다왔어\n2. 한강에서 자전거 탔어!"
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary_draft", JSONSchema: json.RawMessage(entrySchema), Messages: []ai.Message{user(message)}})
		require.NoError(t, err)
		assert.Equal(t, want, entryOf(t, resp.Text))
	})

	t.Run("이어 쓰는 요청이면 하루를 다시 여는 말로 시작하지 않는다", func(t *testing.T) {
		message := "방식: 이어 쓰기\n\n사용자가 한 말:\n1. 저녁에는 혼자 영화 봤어\n(시간이 지난 뒤 다시 나눈 대화)\n2. 자기 전에 이어 쓰기 숙제도 했어"
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary_draft", JSONSchema: json.RawMessage(entrySchema), Messages: []ai.Message{user(message)}})
		require.NoError(t, err)
		assert.Equal(t, "그 뒤에 이런 이야기도 했다. 저녁에는 혼자 영화 봤어. 자기 전에 이어 쓰기 숙제도 했어.", entryOf(t, resp.Text))
	})

	t.Run("사용자의 말 속에 같은 글귀가 있어도 이어 쓰는 요청으로 보지 않는다", func(t *testing.T) {
		message := "방식: 처음 쓰기\n\n사용자가 한 말:\n1. 오늘은 이어 쓰기 숙제를 했어"
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary_draft", JSONSchema: json.RawMessage(entrySchema), Messages: []ai.Message{user(message)}})
		require.NoError(t, err)
		assert.Equal(t, "오늘 나는 이런 이야기를 했다. 오늘은 이어 쓰기 숙제를 했어.", entryOf(t, resp.Text))
	})

	t.Run("말이 아주 많아도 초안은 짧다", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("방식: 처음 쓰기\n\n사용자가 한 말:")
		for i := range 200 {
			b.WriteString("\n" + strconv.Itoa(i+1) + ". 오늘은 정말 길고 긴 하루였고 할 말이 아주 많았어")
		}
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary_draft", JSONSchema: json.RawMessage(entrySchema), Messages: []ai.Message{user(b.String())}})
		require.NoError(t, err)
		entry := entryOf(t, resp.Text)
		assert.LessOrEqual(t, utf8.RuneCountInString(entry), 600)
		assert.Greater(t, utf8.RuneCountInString(entry), 300)
	})

	t.Run("대화를 메시지로 받으면 사용자의 말만 이어 붙인다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary", Messages: []ai.Message{
			model("오늘 하루는 어땠어요?"), user("오늘 친구랑 놀러갔다왔어"),
			model("어디서 놀았어요?"), user(" 한강에서 자전거 탔어! "),
		}})
		require.NoError(t, err)
		assert.Equal(t, want, resp.Text)
		assert.NotContains(t, resp.Text, "어디서 놀았어요", "AI의 말은 일기의 재료가 아니다")
	})

	t.Run("기록을 한 덩어리로 받으면 줄 머리의 이름으로 사용자의 말을 가린다", func(t *testing.T) {
		transcript := "아래는 오늘의 대화다.\n\nAI: 오늘 하루는 어땠어요?\n사용자: 오늘 친구랑 놀러갔다왔어\nAI: 어디서 놀았어요?\n사용자: 한강에서 자전거 탔어!"
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary", Messages: []ai.Message{user(transcript)}})
		require.NoError(t, err)
		assert.Equal(t, want, resp.Text)
	})

	t.Run("번호도 이름도 없는 줄뿐이면 줄마다 발화로 본다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary", Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어\n\n한강에서 자전거 탔어!")}})
		require.NoError(t, err)
		assert.Equal(t, want, resp.Text)
	})

	t.Run("사용자의 말을 찾지 못해도 빈 글을 내지 않는다", func(t *testing.T) {
		resp, err := llm.Generate(context.Background(), ai.Request{Task: "diary", Messages: []ai.Message{user("AI: 오늘 하루는 어땠어요?")}})
		require.NoError(t, err)
		assert.NotEmpty(t, strings.TrimSpace(resp.Text))
	})

	t.Run("스키마의 속성 이름이 달라도 본문을 글이 들어갈 속성에 담는다", func(t *testing.T) {
		tests := []struct {
			name   string
			schema string
			key    string
		}{
			{"이름으로 찾는다", `{"type":"object","properties":{"title":{"type":"string"},"text":{"type":"string"}}}`, "text"},
			{"이름으로 못 찾으면 하나뿐인 문자열 속성에 담는다", `{"type":"object","properties":{"appended":{"type":"string"},"mood":{"type":"string","enum":["calm","low"]}}}`, "appended"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp, err := llm.Generate(context.Background(), ai.Request{
					Task: "diary", JSONSchema: json.RawMessage(tt.schema),
					Messages: []ai.Message{user("1. 오늘 친구랑 놀러갔다왔어\n2. 한강에서 자전거 탔어!")},
				})
				require.NoError(t, err)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(resp.Text), &got))
				assert.Equal(t, want, got[tt.key])
			})
		}
	})
}

func TestGenerateStream(t *testing.T) {
	llm := newLLM(t, scripted.Options{})
	req := ai.Request{Task: "conversation", Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}}

	t.Run("조각을 이으면 돌려준 글과 같다", func(t *testing.T) {
		var deltas []string
		resp, err := llm.GenerateStream(context.Background(), req, func(d string) error {
			deltas = append(deltas, d)
			return nil
		})
		require.NoError(t, err)
		assert.Greater(t, len(deltas), 1)
		assert.Equal(t, resp.Text, strings.Join(deltas, ""))

		whole, err := llm.Generate(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, whole.Text, resp.Text, "흘려 받아도 한 번에 받아도 같은 답이다")
	})

	t.Run("받는 쪽이 오류를 돌려주면 멈춘다", func(t *testing.T) {
		stop := errors.New("stop")
		calls := 0
		_, err := llm.GenerateStream(context.Background(), req, func(string) error {
			calls++
			return stop
		})
		require.ErrorIs(t, err, stop)
		assert.Equal(t, 1, calls)
	})

	t.Run("받을 함수가 없으면 잘못된 요청이다", func(t *testing.T) {
		_, err := llm.GenerateStream(context.Background(), req, nil)
		require.ErrorIs(t, err, ai.ErrInvalidRequest)
	})
}

func TestLatency(t *testing.T) {
	slow := newLLM(t, scripted.Options{Latency: time.Hour})
	req := ai.Request{Task: "conversation", Messages: []ai.Message{user("오늘 친구랑 놀러갔다왔어")}}

	t.Run("기다리는 중에 기한이 지나면 시간 초과다", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := slow.Generate(ctx, req)
		require.ErrorIs(t, err, ai.ErrTimeout)
	})

	t.Run("기다리는 중에 취소하면 바로 돌아온다", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := slow.Generate(ctx, req)
		require.ErrorIs(t, err, context.Canceled)
	})
}
