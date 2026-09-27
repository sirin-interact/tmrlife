package analysis_test

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/scripted"
	"github.com/sirin-interact/tmrlife/server/internal/analysis"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/core/signal"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

// 키 없이 띄운 서버와 브라우저 흐름 테스트는 정해 둔 답을 내는 모델로 돈다.
// 그 길에서도 여덟 항목의 행이 빠짐없이 들어가고, 말한 항목에는 사용자의 말이 근거로 남아야 한다.
// 근거가 없으면 근거 화면은 판단만 보여 주고, 심사에서 "왜 그렇게 봤는가"를 따라갈 길이 사라진다.
//
// 그 모델은 낱말 표로 가릴 뿐 문맥을 보지 못한다. 실제 모델과 같은 판단을 낸다는 뜻이 아니다.
// 여기서 보는 것은 같은 코드 길을 지나간다는 것뿐이다.
func TestExtractWithScriptedModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	model, err := scripted.New(config.AIProviderScripted, scripted.Options{})
	require.NoError(t, err)
	service := f.newService(func(o *analysis.Options) { o.LLM = model })

	const (
		sleepLine    = "어젯밤에 네 번 깼어"
		appetiteLine = "밥은 잘 먹었어"
	)
	c := f.ended(assistant("오늘 하루는 어땠어요?"), user(sleepLine), user(appetiteLine))

	result, err := service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	require.Equal(t, analysis.OutcomeSaved, result.Outcome)
	assert.Zero(t, result.Dropped, "근거는 사용자의 줄을 그대로 옮긴 것이라 대조를 지나간다")
	assert.Equal(t, store.AnalysisDone, f.analysisStatus(c))

	rows := f.assertEightRows(c)
	assert.Equal(t, "observed", rows["sleep"].status)
	assert.Equal(t, sleepLine, rows["sleep"].evidence, "근거는 사용자가 한 말 그대로여야 한다")
	assert.Equal(t, "not_observed", rows["appetite"].status)
	assert.Equal(t, appetiteLine, rows["appetite"].evidence)

	// 이야기가 나오지 않은 항목은 언급 없음이고 근거가 없다. 그 자리를 "괜찮았다"로 채우지 않는다.
	for _, item := range []string{"interest", "mood", "self_blame", "concentration", "psychomotor"} {
		assert.Equal(t, "not_mentioned", rows[item].status, item)
		assert.False(t, rows[item].hasEvidence, item)
	}
	assert.Equal(t, 1, result.Counts.Observed)
	assert.Equal(t, 1, result.Counts.NotObserved)
	assert.Equal(t, signal.ItemCount-2, result.Counts.NotMentioned)
}

// 스키마의 모양만 채우는 규칙(일기)으로 답하게 하면 여덟 항목이 모두 언급 없음이 된다.
// 열거의 첫 값이 가장 보수적인 값이라, 글을 읽지 않는 규칙도 올바른 "언급 없음" 답을 낸다.
func TestExtractWithSchemaFillingModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tasks := maps.Clone(scripted.DefaultTasks())
	tasks[analysis.PromptTask] = scripted.Diary
	model, err := scripted.New(config.AIProviderScripted, scripted.Options{Tasks: tasks})
	require.NoError(t, err)
	service := f.newService(func(o *analysis.Options) { o.LLM = model })

	c := f.ended(user("어젯밤에 네 번 깼어"), user("밥은 잘 먹었어"))

	result, err := service.Extract(t.Context(), c.target(f.userID))
	require.NoError(t, err)

	require.Equal(t, analysis.OutcomeSaved, result.Outcome)
	assert.Equal(t, signal.ItemCount, result.Counts.NotMentioned)
	assert.Zero(t, result.Dropped, "언급 없음뿐이라 버릴 근거도 없다")

	rows := f.assertEightRows(c)
	for _, item := range signal.AllItems() {
		assert.Equal(t, "not_mentioned", rows[item.String()].status)
		assert.False(t, rows[item.String()].hasEvidence)
	}
}

// 정해 둔 답을 내는 모델이 이 일을 모르면 요청 자체가 거절된다.
// 그 실패는 다시 시도해도 같으므로 분석은 곧바로 failed로 닫힌다. 행은 남지 않으므로 계산은 온전하다.
func TestExtractWithScriptedModelMissingTask(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tasks := maps.Clone(scripted.DefaultTasks())
	delete(tasks, analysis.PromptTask)
	model, err := scripted.New(config.AIProviderScripted, scripted.Options{Tasks: tasks})
	require.NoError(t, err)
	service := f.newService(func(o *analysis.Options) { o.LLM = model })
	c := f.ended(user("어젯밤에 네 번 깼어"))

	_, err = service.Extract(t.Context(), c.target(f.userID))

	require.ErrorIs(t, err, ai.ErrInvalidRequest)
	assert.True(t, analysis.Permanent(err), "다시 보내도 같은 답이 온다")
	assert.Empty(t, f.signals(c))
}
