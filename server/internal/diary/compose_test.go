package diary_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
	"github.com/sirin-interact/tmrlife/server/internal/diary"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

func TestPromptMatchesMessage(t *testing.T) {
	t.Parallel()
	registry, err := prompts.LoadEmbedded()
	require.NoError(t, err)
	prompt, err := registry.Get(diary.PromptTask)
	require.NoError(t, err)

	// 지시문은 코드와 떨어진 파일이라 따로 바뀐다. 코드가 보내는 글의 낱말을 지시문이 모르면 모델은 방식을 가리지 못한다.
	for _, label := range diary.PromptLabels() {
		assert.Contains(t, prompt.System, label, "지시문이 입력의 %q를 설명하지 않는다", label)
	}
	assert.Contains(t, string(prompt.Schema), `"entry"`)
	assert.NotContains(t, strings.ToLower(prompt.System), "todo")
}

func TestJoinEntry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		base  string
		entry string
		want  string
	}{
		{"앞의 글이 없으면 새 글만 남는다", "", "오늘은 쉬었다.", "오늘은 쉬었다."},
		{"앞의 글이 공백뿐이어도 새 글만 남는다", " \n", "오늘은 쉬었다.", "오늘은 쉬었다."},
		{"단락 사이에 빈 줄 하나를 둔다", "아침에 뛰었다.", "밤에는 쉬었다.", "아침에 뛰었다.\n\n밤에는 쉬었다."},
		{"앞의 글이 줄바꿈으로 끝나면 하나만 보탠다", "아침에 뛰었다.\n", "밤에는 쉬었다.", "아침에 뛰었다.\n\n밤에는 쉬었다."},
		{"앞의 글이 빈 줄로 끝나면 보태지 않는다", "아침에 뛰었다.\n\n", "밤에는 쉬었다.", "아침에 뛰었다.\n\n밤에는 쉬었다."},
		{"앞의 글 끝의 공백도 사용자가 쓴 것이라 지우지 않는다", "아침에 뛰었다.  ", "밤에는 쉬었다.", "아침에 뛰었다.  \n\n밤에는 쉬었다."},
		{"보탤 글이 없으면 앞의 글 그대로다", "아침에 뛰었다.", "", "아침에 뛰었다."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := diary.JoinEntry(tt.base, tt.entry)
			assert.Equal(t, tt.want, got)
			if strings.TrimSpace(tt.base) != "" {
				assert.True(t, strings.HasPrefix(got, tt.base), "앞의 글은 한 글자도 바뀌지 않는다")
			}
		})
	}
}

func TestFinished(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]bool{
		store.ProcessingNone:    false,
		store.ProcessingPending: false,
		store.ProcessingRunning: false,
		store.ProcessingDone:    true,
		store.ProcessingFailed:  true,
	} {
		assert.Equal(t, want, diary.Finished(status), status)
	}
}
