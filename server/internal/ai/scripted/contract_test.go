package scripted_test

// 이 파일은 대본의 답이 서버의 실제 출력 검사를 통과하는지 본다.
//
// 대본의 답이 검사에 걸리면 서버는 말없이 미리 써 둔 말로 바꿔 내보낸다. 그러면 브라우저 흐름 테스트는 여전히 통과하지만
// "모델의 답이 검사를 거쳐 나가는 길"은 더 이상 지나가지 않게 된다. 지시문이나 검사를 고쳤을 때 그 어긋남을 여기서 잡는다.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/reply"
)

func newGenerator(t *testing.T) (*reply.Generator, *phrases.Catalogue) {
	t.Helper()
	reg, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	ps, err := reply.PromptsFrom(reg)
	require.NoError(t, err)
	catalogue, err := phrases.Load()
	require.NoError(t, err)
	generator, err := reply.New(newLLM(t, scripted.Options{}), ps, catalogue, reply.Options{})
	require.NoError(t, err)
	return generator, catalogue
}

func userTurn(text string) reply.Turn { return reply.Turn{Speaker: reply.SpeakerUser, Text: text} }
func aiTurn(text string) reply.Turn   { return reply.Turn{Speaker: reply.SpeakerAI, Text: text} }

func requireModelReply(t *testing.T, result reply.Result) {
	t.Helper()
	require.Equal(t, reply.OriginModel, result.Origin, "대본의 답이 버려지고 미리 써 둔 말이 나갔다: %v", result.Violations())
	require.Len(t, result.Attempts, 1, "첫 시도에 통과해야 한다: %v", result.Violations())
	assert.Equal(t, scripted.ModelName, result.Attempts[0].Model)
}

func TestContract_RepliesPassTheOutputCheck(t *testing.T) {
	generator, catalogue := newGenerator(t)
	utterances := []string{
		"오늘 친구랑 놀러갔다왔어", "한강에서 자전거 탔어", "응", "몰라 그냥 피곤해", "엄마랑 또 싸웠어. 맨날 내 탓만 해",
		"오늘 상담 받고 왔어", "나 우울증인 것 같아", "ㅋㅋ 그러게", "I'm so tired", "109에 전화해볼까",
		"그냥 그래", "별일 없었어",
	}

	t.Run("평소의 대화", func(t *testing.T) {
		turns := []reply.Turn{aiTurn(catalogue.Opening().Display)}
		for _, u := range utterances {
			turns = append(turns, userTurn(u))
			result, err := generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: turns})
			require.NoError(t, err)
			requireModelReply(t, result)
			turns = append(turns, aiTurn(result.Text))
		}
	})

	t.Run("한동안 말이 없어 한 번 물은 뒤의 대화", func(t *testing.T) {
		turns := []reply.Turn{aiTurn(catalogue.Opening().Display), aiTurn(catalogue.IdleCheck().Display), userTurn("응 있어")}
		result, err := generator.Generate(context.Background(), reply.Input{Mode: reply.ModeNormal, Turns: turns})
		require.NoError(t, err)
		requireModelReply(t, result)
	})

	t.Run("위기 응답이 나간 뒤의 대화", func(t *testing.T) {
		turns := []reply.Turn{aiTurn(catalogue.Opening().Display), userTurn("죽고 싶다"), aiTurn(catalogue.CrisisRespond().Display)}
		for _, u := range utterances {
			turns = append(turns, userTurn(u))
			result, err := generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCrisisFollow, Turns: turns})
			require.NoError(t, err)
			requireModelReply(t, result)
			turns = append(turns, aiTurn(result.Text))
		}
	})

	t.Run("되묻는 턴", func(t *testing.T) {
		ambiguous := []string{
			"그냥 다 사라졌으면 좋겠어",
			"이제 그만하고 싶다",
			"자고 일어나지 않았으면 좋겠어",
			"예전에 죽고 싶다는 생각 한 적 있어",
			"몰라, 그냥 내가 없어도 아무도 모를 것 같아",
		}
		for _, u := range ambiguous {
			turns := []reply.Turn{
				aiTurn(catalogue.Opening().Display), userTurn("오늘 회사에서 크게 혼났어"),
				aiTurn("무슨 일 있었어요?"), userTurn(u),
			}
			result, err := generator.Generate(context.Background(), reply.Input{Mode: reply.ModeCheck, Turns: turns})
			require.NoError(t, err)
			requireModelReply(t, result)
		}
	})
}
