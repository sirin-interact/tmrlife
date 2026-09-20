package diary_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 키 없이 띄운 서버와 화면 흐름 테스트는 정해 둔 답을 내는 모델로 돈다.
// 그 모델이 이 패키지가 보내는 글을 알아듣고, 그 답이 이 패키지의 출력 검사를 통과해야 그 길에서도 초안이 생긴다.
func TestDraftWithScriptedModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	model, err := scripted.New(config.AIProviderScripted, scripted.Options{})
	require.NoError(t, err)
	service := f.newService(func(o *diary.Options) { o.LLM = model })

	first := f.ended(assistant("제주도는 어떠셨어요?"), user("오늘 친구랑 한강 갔다왔어"), user("치킨 먹었어"))
	result, err := service.Draft(t.Context(), first.target(f.userID))
	require.NoError(t, err)
	require.Equal(t, diary.OutcomeDrafted, result.Outcome)
	firstDraft := f.mustDiary(first.dayID).draft
	assert.Contains(t, firstDraft, "한강")
	assert.Contains(t, firstDraft, "치킨")
	assert.NotContains(t, firstDraft, "제주도")

	f.confirm(first.dayID, "친구랑 한강에서 치킨을 먹었다.")
	second := f.ended(user("집에 와서 바로 잤어"))
	result, err = service.Draft(t.Context(), second.target(f.userID))
	require.NoError(t, err)
	require.Equal(t, diary.OutcomeDrafted, result.Outcome)
	require.Equal(t, diary.ModeAppend, result.Mode)

	got := f.mustDiary(second.dayID)
	assert.Equal(t, store.DiaryDraft, got.row.Status)
	require.True(t, strings.HasPrefix(got.draft, "친구랑 한강에서 치킨을 먹었다.\n\n"))
	continuation := strings.TrimPrefix(got.draft, "친구랑 한강에서 치킨을 먹었다.\n\n")
	assert.Contains(t, continuation, "바로 잤어")
	assert.NotContains(t, continuation, "한강", "이미 담긴 대화는 다시 나오지 않는다")
}
