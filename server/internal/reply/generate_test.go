package reply_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

const openingText = "오늘 하루는 어땠어요?"

type fixture struct {
	llm       *fake.LLM
	generator *reply.Generator
	phrases   *phrases.Catalogue
	prompts   reply.Prompts
}

func newFixture(t *testing.T, opts reply.Options) fixture {
	t.Helper()
	reg, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	ps, err := reply.PromptsFrom(reg)
	require.NoError(t, err)
	catalogue, err := phrases.Load()
	require.NoError(t, err)
	llm := fake.New("fake-conversation")
	generator, err := reply.New(llm, ps, catalogue, opts)
	require.NoError(t, err)
	return fixture{llm: llm, generator: generator, phrases: catalogue, prompts: ps}
}

func u(text string) reply.Turn { return reply.Turn{Speaker: reply.SpeakerUser, Text: text} }
func a(text string) reply.Turn { return reply.Turn{Speaker: reply.SpeakerAI, Text: text} }

func normalInput(said string) reply.Input {
	return reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{a(openingText), u(said)}}
}

func TestGenerate_ModelReply(t *testing.T) {
	t.Run("검사를 통과한 답은 모델의 말로 나간다", func(t *testing.T) {
		f := newFixture(t, reply.Options{MaxOutputTokens: 3000, Thinking: ai.ThinkingLow})
		f.llm.Enqueue(fake.Reply("  오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?\n"))

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 친구랑 놀러갔다왔어"))
		require.NoError(t, err)

		assert.Equal(t, "오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?", res.Text, "앞뒤 공백은 떼고 내보낸다")
		assert.Equal(t, res.Text, res.Speech)
		assert.Equal(t, reply.OriginModel, res.Origin)
		assert.Empty(t, res.Phrase)
		assert.False(t, res.NoQuestion)
		require.Len(t, res.Attempts, 1)
		assert.Equal(t, "fake-conversation", res.Attempts[0].Model)
		assert.Empty(t, res.Violations())
		assert.Equal(t, reply.TaskNormal, res.PromptTask)
		assert.Equal(t, f.prompts.Normal.Version, res.PromptVersion)

		req, ok := f.llm.LastRequest()
		require.True(t, ok)
		assert.Equal(t, reply.TaskNormal, req.Task)
		assert.Equal(t, f.prompts.Normal.System, req.System, "덧붙일 지시가 없으면 지시문 그대로 보낸다")
		assert.Equal(t, 3000, req.MaxOutputTokens)
		assert.Equal(t, ai.ThinkingLow, req.Thinking)
		assert.Nil(t, req.JSONSchema)
		assert.Equal(t, []ai.Message{
			{Role: ai.RoleModel, Text: openingText},
			{Role: ai.RoleUser, Text: "오늘 친구랑 놀러갔다왔어"},
		}, req.Messages)
	})

	t.Run("방식마다 다른 지시문으로 부른다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		cases := []struct {
			mode  reply.Mode
			task  string
			reply string
		}{
			{reply.ModeNormal, reply.TaskNormal, "무슨 일 있으셨어요?"},
			{reply.ModeCheck, reply.TaskCheck, "다 사라졌으면 싶을 만큼요. 오늘 무슨 일 있었어요?"},
			{reply.ModeCrisisFollow, reply.TaskCrisisFollow, "여기서 계속 듣고 있을게요."},
		}
		for _, tc := range cases {
			f.llm.Enqueue(fake.Reply(tc.reply))
			res, err := f.generator.Generate(context.Background(), reply.Input{Mode: tc.mode, Turns: []reply.Turn{a(openingText), u("그냥 다 사라졌으면 좋겠어")}})
			require.NoError(t, err)
			assert.Equal(t, reply.OriginModel, res.Origin, string(tc.mode))
			req, _ := f.llm.LastRequest()
			assert.Equal(t, tc.task, req.Task)
			assert.Equal(t, tc.task, res.PromptTask)
		}
	})
}

func TestGenerate_Regenerate(t *testing.T) {
	t.Run("검사에 걸리면 걸린 까닭을 알려주고 한 번 다시 만든다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		rejected := "오 재밌게 놀고 오셨어요? 어디서 놀았어요? 상담도 받아 보세요."
		f.llm.Enqueue(fake.Reply(rejected), fake.Reply("오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?"))

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 친구랑 놀러갔다왔어"))
		require.NoError(t, err)

		assert.Equal(t, reply.OriginModel, res.Origin)
		assert.Equal(t, "오, 재밌게 놀고 오셨나 봐요. 어디서 놀았어요?", res.Text)
		require.Len(t, res.Attempts, 2)
		assert.ElementsMatch(t, []string{"too_many_sentences:3", "too_many_questions:2", "referral:counsel"}, names(res.Attempts[0].Violations))
		assert.Empty(t, res.Attempts[1].Violations)
		assert.Equal(t, res.Attempts[0].Violations, res.Violations())

		reqs := f.llm.Requests()
		require.Len(t, reqs, 2)
		assert.Equal(t, f.prompts.Normal.System, reqs[0].System)
		assert.True(t, strings.HasPrefix(reqs[1].System, f.prompts.Normal.System))
		hint := strings.TrimPrefix(reqs[1].System, f.prompts.Normal.System)
		assert.Contains(t, hint, "문장이 셋 이상이었다")
		assert.Contains(t, hint, "물음표가 둘 이상이었다")
		assert.Contains(t, hint, "기관, 전문가, 상담")
		assert.NotContains(t, hint, "허락을 구했다", "걸리지 않은 검사는 말하지 않는다")
		assert.NotContains(t, reqs[1].System, "재밌게 놀고 오셨어요", "버린 답의 글은 돌려주지 않는다")
		assert.Equal(t, reqs[0].Messages, reqs[1].Messages, "대화는 그대로 다시 보낸다")
	})

	t.Run("두 번 다 걸리면 평소의 대화에서는 안전한 말이 나간다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("병원에 가 보세요."), fake.Reply("당신에겐 전문가가 필요해요."))

		res, err := f.generator.Generate(context.Background(), normalInput("요즘 잠이 안 와"))
		require.NoError(t, err)

		want := f.phrases.SafeReply("")
		assert.Equal(t, reply.OriginFixed, res.Origin)
		assert.Equal(t, phrases.SafeReply, res.Phrase)
		assert.Equal(t, want.Display, res.Text)
		assert.Equal(t, want.Speech, res.Speech)
		require.Len(t, res.Attempts, 2)
		assert.Equal(t, []string{"referral:hospital", "referral:expert", "second_person"}, names(res.Violations()))
		assert.Equal(t, 2, f.llm.Calls(), "세 번째로 부르지 않는다")
	})

	t.Run("두 번 다 걸리면 위기 응답 뒤의 대화에서도 안전한 말이 나간다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("109에 전화해 보세요."), fake.Reply("지금 바로 상담을 받아야 해요."))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCrisisFollow, Turns: []reply.Turn{
			a(openingText), u("죽고 싶다"), a(f.phrases.CrisisRespond().Display), u("응 집이야"), a("집에 있네요. 계속 듣고 있을게요."), u("전화는 하기 싫어"),
		}})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		assert.Equal(t, f.phrases.SafeReply("").Display, res.Text)
		assert.NotContains(t, res.Text, "109")
	})

	t.Run("바로 앞에 안전한 말이 나갔으면 같은 말을 되풀이하지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step {
			return fake.Fail(ai.NewError(ai.ErrProvider, ai.Detail{}))
		})
		first := f.phrases.SafeReply("")

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{
			a(openingText), u("오늘 좀 힘들었어"), a(first.Display), u("회사에서 혼났거든"),
		}})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		assert.NotEqual(t, first.Display, res.Text)
		assert.Equal(t, f.phrases.SafeReply(first.Display).Display, res.Text)
	})
}

func TestGenerate_CheckMode(t *testing.T) {
	said := "오늘도 혼났어. 그냥 다 사라졌으면 좋겠어"

	t.Run("두 번 다 걸리면 사용자의 표현을 넣은 문형으로 되묻는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("많이 힘드셨겠어요. 오늘은 푹 쉬어요."), fake.Reply("힘든 하루였나 봐요. 무슨 일 있었어요?"))

		res, err := f.generator.Generate(context.Background(), reply.Input{
			Mode: reply.ModeCheck, Turns: []reply.Turn{a(openingText), u(said)}, UserWords: "다 사라졌으면 좋겠어",
		})
		require.NoError(t, err)

		assert.Equal(t, reply.OriginTemplate, res.Origin)
		assert.Equal(t, phrases.ReflectFallback, res.Phrase)
		assert.Equal(t, "“다 사라졌으면 좋겠어”라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?", res.Text)
		assert.Equal(t, "다 사라졌으면 좋겠어라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?", res.Speech)
		require.Len(t, res.Attempts, 2)
		assert.ElementsMatch(t, []string{"missing_question", "not_mirrored"}, names(res.Attempts[0].Violations))
		assert.Equal(t, []string{"not_mirrored"}, names(res.Attempts[1].Violations))

		reqs := f.llm.Requests()
		require.Len(t, reqs, 2)
		assert.Contains(t, reqs[1].System, "묻는 말이 없었다")
		assert.Contains(t, reqs[1].System, "사용자의 표현을 받지 않았다")
	})

	t.Run("받을 표현을 주지 않으면 사용자의 마지막 말을 넣는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{})) })

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCheck, Turns: []reply.Turn{a(openingText), u("이제 그만하고 싶다")}})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginTemplate, res.Origin)
		assert.Equal(t, "“이제 그만하고 싶다”라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?", res.Text)
	})

	t.Run("넣기에 너무 긴 말이면 표현 없이 되묻는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Fail(ai.NewError(ai.ErrBlocked, ai.Detail{})) })
		long := strings.Repeat("회사에서도 집에서도 다 나만 탓하고 ", 4) + "그냥 다 그만하고 싶다"

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCheck, Turns: []reply.Turn{a(openingText), u(long)}})
		require.NoError(t, err)
		plain, _ := f.phrases.Get(phrases.ReflectFallback)
		assert.Equal(t, reply.OriginTemplate, res.Origin)
		assert.Equal(t, plain.Display, res.Text)
	})

	t.Run("직전에 두 번 물었어도 되물어야 하는 턴에는 질문을 막지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("없어지고 싶을 만큼, 오늘 무슨 일이 있었어요?"))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCheck, Turns: []reply.Turn{
			a(openingText), u("오늘도 야근했어"), a("오늘도요. 몇 시에 끝났어요?"), u("이렇게 살 바엔 그냥 없어지고 싶다"),
		}})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginModel, res.Origin)
		assert.False(t, res.NoQuestion)
		req, _ := f.llm.LastRequest()
		assert.NotContains(t, req.System, "이번에는 묻지 않는다")
	})
}

func TestGenerate_NoQuestionTurn(t *testing.T) {
	twoQuestions := []reply.Turn{a(openingText), u("오늘 친구 만났어"), a("누구 만났어요?"), u("대학 친구")}

	t.Run("직전 두 번을 모두 질문으로 끝냈으면 이번에는 묻지 말라고 알린다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("오랜만에 대학 친구를 만났네요."))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: twoQuestions})
		require.NoError(t, err)
		assert.True(t, res.NoQuestion)
		assert.Equal(t, reply.OriginModel, res.Origin)

		req, _ := f.llm.LastRequest()
		assert.Contains(t, req.System, "이번에는 묻지 않는다")
		assert.NotContains(t, req.System, "방금 만든 답은", "첫 시도에는 고쳐 말할 것이 없다")
	})

	t.Run("그래도 물으면 다시 만들고 또 물으면 안전한 말로 바꾼다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("대학 친구랑 뭐 했어요?"), fake.Reply("어떤 얘기를 나눴는지 궁금해요."))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: twoQuestions})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		assert.NotContains(t, res.Text, "?")
		assert.Equal(t, []string{"question_not_allowed", "question_not_allowed"}, names(res.Violations()))

		reqs := f.llm.Requests()
		require.Len(t, reqs, 2)
		assert.Contains(t, reqs[1].System, "이번에는 묻지 않는다")
		assert.Contains(t, reqs[1].System, "묻지 않아야 하는데 물었다")
	})

	t.Run("직전 두 번 가운데 하나라도 질문이 아니었으면 막지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("대학 친구랑 뭐 했어요?"))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{
			a(openingText), u("오늘 친구 만났어"), a("오랜만에 반가웠겠어요."), u("대학 친구"),
		}})
		require.NoError(t, err)
		assert.False(t, res.NoQuestion)
		assert.Equal(t, reply.OriginModel, res.Origin)
	})

	t.Run("미리 써 둔 말도 직전의 AI 말로 센다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("여기서 계속 듣고 있을게요."))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCrisisFollow, Turns: []reply.Turn{
			a(openingText), u("죽고 싶다"), a(f.phrases.CrisisRespond().Display), u("응 집이야"),
		}})
		require.NoError(t, err)
		assert.True(t, res.NoQuestion, "첫 안부와 위기 응답이 모두 질문으로 끝났다")
	})
}

func TestGenerate_ModelFailure(t *testing.T) {
	timeout := ai.NewError(ai.ErrTimeout, ai.Detail{})
	blocked := ai.NewError(ai.ErrBlocked, ai.Detail{})

	t.Run("다시 부를 만한 실패면 한 번 더 부른다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Fail(timeout), fake.Reply("무슨 일 있으셨어요?"))

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err)
		assert.Equal(t, reply.OriginModel, res.Origin)
		require.Len(t, res.Attempts, 2)
		assert.Equal(t, ai.KindTimeout, res.Attempts[0].Failure)
		assert.Empty(t, res.Attempts[1].Failure)

		reqs := f.llm.Requests()
		assert.Equal(t, reqs[0].System, reqs[1].System, "답을 받지 못했으므로 고쳐 말할 것도 없다")
	})

	t.Run("빈 답과 잘린 답도 실패다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Step{Text: "  "}, fake.Step{Text: "그랬", FinishReason: ai.FinishMaxTokens})

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		require.Len(t, res.Attempts, 2)
		assert.Equal(t, ai.KindEmpty, res.Attempts[0].Failure)
		assert.Equal(t, ai.KindTruncated, res.Attempts[1].Failure)
	})

	t.Run("다시 불러도 소용없는 실패면 바로 미리 써 둔 말로 바꾼다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Fail(blocked), fake.Reply("쓰이지 않을 답"))

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		require.Len(t, res.Attempts, 1)
		assert.Equal(t, ai.KindBlocked, res.Attempts[0].Failure)
		assert.Equal(t, 1, f.llm.Calls())
	})

	t.Run("실패한 뒤에 받은 답이 검사에 걸리면 더 부르지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Fail(timeout), fake.Reply("병원에 가 보세요."), fake.Reply("쓰이지 않을 답"))

		res, err := f.generator.Generate(context.Background(), normalInput("요즘 잠이 안 와"))
		require.NoError(t, err)
		assert.Equal(t, reply.OriginFixed, res.Origin)
		assert.Equal(t, 2, f.llm.Calls())
	})

	t.Run("무엇인지 모르는 오류도 말이 끊기게 두지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err, "대본이 없는 가짜 모델은 실패 종류가 아닌 오류를 돌려준다")
		assert.Equal(t, reply.OriginFixed, res.Origin)
		require.Len(t, res.Attempts, 1)
		assert.Equal(t, ai.KindUnknown, res.Attempts[0].Failure)
	})

	t.Run("기다리는 시간을 넘기면 미리 써 둔 말로 바꾸고 더 부르지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{Budget: 20 * time.Millisecond})
		f.llm.Enqueue(fake.Step{Text: "무슨 일 있으셨어요?", Latency: 5 * time.Second}, fake.Reply("쓰이지 않을 답"))

		res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err, "부른 쪽의 컨텍스트는 살아 있다")
		assert.Equal(t, reply.OriginFixed, res.Origin)
		require.Len(t, res.Attempts, 1)
		assert.Equal(t, ai.KindTimeout, res.Attempts[0].Failure)
		assert.Equal(t, 1, f.llm.Calls())
	})
}

func TestGenerate_Cancellation(t *testing.T) {
	t.Run("이미 끝난 컨텍스트로는 모델을 부르지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := f.generator.Generate(ctx, normalInput("오늘 좀 그랬어"))
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, f.llm.Calls())
	})

	t.Run("부르는 도중에 취소되면 미리 써 둔 말도 내보내지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Step{Text: "무슨 일 있으셨어요?", Latency: 5 * time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			for f.llm.Calls() == 0 {
				time.Sleep(time.Millisecond)
			}
			cancel()
		}()

		res, err := f.generator.Generate(ctx, normalInput("오늘 좀 그랬어"))
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, res.Text)
		assert.Equal(t, 1, f.llm.Interrupted())
	})

	t.Run("부른 쪽의 기한이 지나도 오류로 끝난다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Step{Text: "무슨 일 있으셨어요?", Latency: 5 * time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := f.generator.Generate(ctx, normalInput("오늘 좀 그랬어"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestGenerate_InvalidInput(t *testing.T) {
	cases := []struct {
		name string
		in   reply.Input
	}{
		{"오간 말이 없다", reply.Input{Mode: reply.ModeNormal}},
		{"마지막 말이 AI의 말이다", reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{a(openingText)}}},
		{"사용자의 마지막 말이 비었다", reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{a(openingText), u("  ")}}},
		{"모르는 방식이다", reply.Input{Mode: "advice", Turns: []reply.Turn{u("안녕")}}},
		{"모르는 화자가 있다", reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{{Speaker: "system", Text: "규칙"}, u("안녕")}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, reply.Options{})
			_, err := f.generator.Generate(context.Background(), tc.in)
			require.ErrorIs(t, err, reply.ErrInvalidInput)
			assert.Zero(t, f.llm.Calls(), "잘못 부른 것을 미리 써 둔 말로 덮지 않는다")
		})
	}
}

func TestGenerate_Messages(t *testing.T) {
	t.Run("빈 말은 빼고 같은 쪽의 말이 이어지면 하나로 잇는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("네, 듣고 있어요."))

		_, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{
			a(openingText), a("아직 거기 있어요? 오늘은 여기까지 해도 괜찮아요."), u(" "), u("응 있어"), u("그냥 멍하니 있었어 "),
		}})
		require.NoError(t, err)

		req, _ := f.llm.LastRequest()
		assert.Equal(t, []ai.Message{
			{Role: ai.RoleModel, Text: openingText + "\n아직 거기 있어요? 오늘은 여기까지 해도 괜찮아요."},
			{Role: ai.RoleUser, Text: "응 있어\n그냥 멍하니 있었어"},
		}, req.Messages)
	})

	t.Run("긴 대화는 최근의 말만 보내되 검사에는 앞의 말도 쓴다", func(t *testing.T) {
		f := newFixture(t, reply.Options{MaxTurns: 3})
		f.llm.Enqueue(fake.Reply("상담 다녀온 뒤로 계속 그랬나 봐요."))

		res, err := f.generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: []reply.Turn{
			a(openingText), u("오늘 처음으로 상담 받고 왔어"), a("다녀오고 나니 어때요?"), u("그냥 멍해"), a("멍할 만해요."), u("아무 생각이 안 나"),
		}})
		require.NoError(t, err)
		assert.Equal(t, reply.OriginModel, res.Origin, "보내지 않은 앞의 말에서 사용자가 꺼낸 낱말도 받아 쓸 수 있다")

		req, _ := f.llm.LastRequest()
		assert.Equal(t, []ai.Message{
			{Role: ai.RoleUser, Text: "그냥 멍해"},
			{Role: ai.RoleModel, Text: "멍할 만해요."},
			{Role: ai.RoleUser, Text: "아무 생각이 안 나"},
		}, req.Messages)
	})

	t.Run("요청을 만들 때 읽어 둔 지시문을 고치지 않는다", func(t *testing.T) {
		f := newFixture(t, reply.Options{})
		f.llm.Enqueue(fake.Reply("병원에 가 보세요."), fake.Reply("무슨 일 있으셨어요?"), fake.Reply("무슨 일 있으셨어요?"))

		_, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err)
		_, err = f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
		require.NoError(t, err)

		req, _ := f.llm.LastRequest()
		assert.Equal(t, f.prompts.Normal.System, req.System, "앞 턴에서 덧붙인 지시가 다음 턴에 남으면 안 된다")
	})
}

func TestResult_LogValueCarriesNoText(t *testing.T) {
	f := newFixture(t, reply.Options{})
	f.llm.Enqueue(fake.Reply("병원에 꼭 가 보세요."), fake.Reply("무슨 일 있으셨어요?"))
	res, err := f.generator.Generate(context.Background(), normalInput("요즘 잠이 통 안 와"))
	require.NoError(t, err)

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("reply generated", slog.Any("reply", res))
	logged := buf.String()

	assert.Contains(t, logged, `"origin":"model"`)
	assert.Contains(t, logged, "referral:hospital")
	for _, secret := range []string{"병원", "무슨 일", "잠이", "오늘 하루"} {
		assert.NotContains(t, logged, secret, "사용자의 말과 모델의 답은 로그에 남지 않는다")
	}
}

func TestNew(t *testing.T) {
	reg, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	ps, err := reply.PromptsFrom(reg)
	require.NoError(t, err)
	catalogue, err := phrases.Load()
	require.NoError(t, err)
	llm := fake.New("fake")

	t.Run("모델이 없으면 만들지 않는다", func(t *testing.T) {
		_, err := reply.New(nil, ps, catalogue, reply.Options{})
		require.Error(t, err)
	})
	t.Run("문구가 없으면 만들지 않는다", func(t *testing.T) {
		_, err := reply.New(llm, ps, nil, reply.Options{})
		require.Error(t, err)
	})
	t.Run("지시문이 하나라도 빠지면 만들지 않는다", func(t *testing.T) {
		missing := ps
		missing.CrisisFollow = prompts.Prompt{}
		_, err := reply.New(llm, missing, catalogue, reply.Options{})
		require.ErrorContains(t, err, "crisis_follow")
	})
	t.Run("음수인 조정 값은 받지 않는다", func(t *testing.T) {
		for _, opts := range []reply.Options{{MaxOutputTokens: -1}, {Budget: -time.Second}, {MaxTurns: -1}} {
			_, err := reply.New(llm, ps, catalogue, opts)
			require.Error(t, err)
		}
	})
	t.Run("지시문 목록이 없으면 꺼내지 못한다", func(t *testing.T) {
		_, err := reply.PromptsFrom(nil)
		require.Error(t, err)
	})
}

func TestPrompts(t *testing.T) {
	reg, err := prompts.LoadEmbedded()
	require.NoError(t, err)

	t.Run("이 패키지가 쓰는 지시문이 실행 파일에 모두 담겨 있다", func(t *testing.T) {
		require.NoError(t, reg.Require(reply.Tasks()...))
		ps, err := reply.PromptsFrom(reg)
		require.NoError(t, err)
		assert.Equal(t, reply.TaskNormal, ps.Normal.Task)
		assert.Equal(t, reply.TaskCheck, ps.Check.Task)
		assert.Equal(t, reply.TaskCrisisFollow, ps.CrisisFollow.Task)
	})

	t.Run("지시문이 없는 목록에서는 오류를 돌려준다", func(t *testing.T) {
		_, err := reply.PromptsFrom(&prompts.Registry{})
		require.ErrorIs(t, err, prompts.ErrUnknownTask)
	})

	t.Run("어느 지시문에도 전화번호가 없고 출력 검사와 같은 규칙을 말한다", func(t *testing.T) {
		for _, task := range reply.Tasks() {
			p, err := reg.Get(task)
			require.NoError(t, err)
			assert.NotRegexp(t, `[0-9]{3}`, p.System, task)
			for _, rule := range []string{"해요체", "당신", "물음표", "전화번호", "진단", "허락", "한글로만"} {
				assert.Contains(t, p.System, rule, task)
			}
		}
	})

	t.Run("지시문의 예시 답은 그 방식의 출력 검사를 통과한다", func(t *testing.T) {
		modes := map[string]reply.Mode{reply.TaskNormal: reply.ModeNormal, reply.TaskCheck: reply.ModeCheck, reply.TaskCrisisFollow: reply.ModeCrisisFollow}
		for task, mode := range modes {
			p, err := reg.Get(task)
			require.NoError(t, err)
			examples := 0
			for _, line := range strings.Split(p.System, "\n") {
				said, answer, ok := strings.Cut(line, "\"라고 하면: ")
				if !ok {
					continue
				}
				examples++
				said = strings.TrimPrefix(said, "사용자가 \"")
				got := reply.Check(reply.Draft{Mode: mode, Text: answer, UserTexts: []string{said}}, reply.DefaultLimits())
				assert.Empty(t, names(got), "%s: %s", task, answer)
			}
			assert.GreaterOrEqual(t, examples, 4, task)
		}
	})
}

// 나가는 말의 출처와 말한 쪽은 발화를 저장할 때 그대로 쓰인다. 값이 어긋나면 저장이 거부된다.
func TestConstantsMatchStoredValues(t *testing.T) {
	assert.Equal(t, store.OriginModel, string(reply.OriginModel))
	assert.Equal(t, store.OriginFixed, string(reply.OriginFixed))
	assert.Equal(t, store.OriginTemplate, string(reply.OriginTemplate))
	assert.Equal(t, store.SpeakerUser, string(reply.SpeakerUser))
	assert.Equal(t, store.SpeakerAI, string(reply.SpeakerAI))
}

func TestGenerate_ConcurrentUse(t *testing.T) {
	f := newFixture(t, reply.Options{})
	f.llm.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("무슨 일 있으셨어요?") })

	errs := make(chan error, 8)
	for range 8 {
		go func() {
			res, err := f.generator.Generate(context.Background(), normalInput("오늘 좀 그랬어"))
			if err == nil && res.Origin != reply.OriginModel {
				err = errors.New("unexpected origin")
			}
			errs <- err
		}()
	}
	for range 8 {
		require.NoError(t, <-errs)
	}
}
