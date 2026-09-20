package prompts_test

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/prompts"
)

const systemText = "두 문장 이하로 답한다."

func file(content string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(content)}
}

func TestLoad(t *testing.T) {
	t.Run("디렉터리마다 지시문 하나를 읽는다", func(t *testing.T) {
		reg, err := prompts.Load(fstest.MapFS{
			"conversation/system.md": file(systemText + "\n"),
			"gate/system.md":         file("위기 단계를 판별한다.\n"),
			"gate/schema.json":       file("{\n  \"type\": \"object\"\n}\n"),
		})
		require.NoError(t, err)

		assert.Equal(t, []string{"conversation", "gate"}, reg.Tasks())

		conversation, err := reg.Get("conversation")
		require.NoError(t, err)
		assert.Equal(t, "conversation", conversation.Task)
		assert.Equal(t, systemText, conversation.System, "파일 끝의 줄바꿈은 지시문에 넣지 않는다")
		assert.Nil(t, conversation.Schema)

		gate, err := reg.Get("gate")
		require.NoError(t, err)
		assert.Equal(t, "위기 단계를 판별한다.", gate.System)
		assert.JSONEq(t, `{"type":"object"}`, string(gate.Schema))
		assert.NotRegexp(t, `\s`, string(gate.Schema), "스키마는 공백 없는 꼴로 보낸다")
	})

	t.Run("지시문이 아닌 것은 건너뛴다", func(t *testing.T) {
		reg, err := prompts.Load(fstest.MapFS{
			"prompts.go":             file("package prompts\n"),
			"README.md":              file("설명\n"),
			".git/config":            file("x"),
			"_drafts/system.md":      file(""),
			"conversation/system.md": file(systemText),
			"conversation/.DS_Store": file("\x00\x01"),
			"conversation/_notes.md": file("메모"),
		})
		require.NoError(t, err)

		assert.Equal(t, []string{"conversation"}, reg.Tasks())
	})

	failures := []struct {
		name  string
		fsys  fstest.MapFS
		paths []string
	}{
		{
			"지시문 본문이 없다",
			fstest.MapFS{"gate/schema.json": file(`{"type":"object"}`)},
			[]string{"gate/system.md"},
		},
		{
			"지시문 본문이 비었다",
			fstest.MapFS{"gate/system.md": file("")},
			[]string{"gate/system.md"},
		},
		{
			"지시문 본문이 공백뿐이다",
			fstest.MapFS{"gate/system.md": file(" \r\n\t\n")},
			[]string{"gate/system.md"},
		},
		{
			"지시문 본문이 UTF-8이 아니다",
			fstest.MapFS{"gate/system.md": file("\xff\xfe\xfd")},
			[]string{"gate/system.md"},
		},
		{
			"스키마가 비었다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/schema.json": file("\n")},
			[]string{"gate/schema.json"},
		},
		{
			"스키마가 JSON이 아니다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/schema.json": file(`{"type":`)},
			[]string{"gate/schema.json"},
		},
		{
			"스키마가 객체가 아니다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/schema.json": file(`["object"]`)},
			[]string{"gate/schema.json"},
		},
		{
			"파일 이름을 잘못 적었다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/shema.json": file(`{"type":"object"}`)},
			[]string{"gate/shema.json"},
		},
		{
			"지시문 디렉터리 안에 디렉터리가 있다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/examples/one.md": file("예시")},
			[]string{"gate/examples"},
		},
		{
			"디렉터리 이름을 지시문 ID로 쓸 수 없다",
			fstest.MapFS{"Gate V2/system.md": file(systemText)},
			[]string{"Gate V2"},
		},
		{
			"지시문이 하나도 없다",
			fstest.MapFS{"README.md": file("설명")},
			[]string{"."},
		},
		{
			"틀린 파일을 한 번에 모두 알린다",
			fstest.MapFS{
				"conversation/system.md": file(""),
				"gate/system.md":         file(systemText),
				"gate/schema.json":       file("not json"),
				"memory/notes.txt":       file("메모"),
			},
			[]string{"conversation/system.md", "gate/schema.json", "memory/notes.txt", "memory/system.md"},
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := prompts.Load(tt.fsys)

			require.Error(t, err)
			assert.Nil(t, reg)

			var loadErr *prompts.LoadError
			require.ErrorAs(t, err, &loadErr)
			paths := make([]string, 0, len(loadErr.Problems))
			for _, p := range loadErr.Problems {
				paths = append(paths, p.Path)
				assert.NotEmpty(t, p.Reason)
				assert.Contains(t, err.Error(), p.Path)
			}
			assert.ElementsMatch(t, tt.paths, paths)
		})
	}
}

func TestVersion(t *testing.T) {
	load := func(t *testing.T, fsys fstest.MapFS) prompts.Prompt {
		t.Helper()
		reg, err := prompts.Load(fsys)
		require.NoError(t, err)
		p, err := reg.Get("gate")
		require.NoError(t, err)
		return p
	}

	base := load(t, fstest.MapFS{
		"gate/system.md":   file(systemText + "\n"),
		"gate/schema.json": file(`{"type":"object"}`),
	})

	t.Run("저장된 표시와 이어지도록 계산이 고정되어 있다", func(t *testing.T) {
		// printf '두 문장 이하로 답한다.\0{"type":"object"}' | shasum -a 256
		assert.Equal(t, "b19bcedbd388", base.Version)

		// printf '두 문장 이하로 답한다.\0' | shasum -a 256
		noSchema := load(t, fstest.MapFS{"gate/system.md": file(systemText)})
		assert.Equal(t, "88c9a487800e", noSchema.Version)
	})

	same := []struct {
		name string
		fsys fstest.MapFS
	}{
		{
			"줄 끝이 CRLF여도 같다",
			fstest.MapFS{"gate/system.md": file(systemText + "\r\n"), "gate/schema.json": file("{\"type\":\"object\"}\r\n")},
		},
		{
			"앞뒤 빈 줄이 달라도 같다",
			fstest.MapFS{"gate/system.md": file("\n\n" + systemText + "\n\n\n"), "gate/schema.json": file(`{"type":"object"}`)},
		},
		{
			"스키마의 들여쓰기만 달라도 같다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/schema.json": file("{\n\t\"type\":   \"object\"\n}\n")},
		},
	}
	for _, tt := range same {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, base.Version, load(t, tt.fsys).Version)
		})
	}

	different := []struct {
		name string
		fsys fstest.MapFS
	}{
		{
			"본문이 한 글자라도 바뀌면 달라진다",
			fstest.MapFS{"gate/system.md": file("세 문장 이하로 답한다."), "gate/schema.json": file(`{"type":"object"}`)},
		},
		{
			"스키마가 바뀌면 달라진다",
			fstest.MapFS{"gate/system.md": file(systemText), "gate/schema.json": file(`{"type":"object","required":["level"]}`)},
		},
		{
			"스키마가 없어지면 달라진다",
			fstest.MapFS{"gate/system.md": file(systemText)},
		},
		{
			"본문 안의 줄바꿈이 바뀌면 달라진다",
			fstest.MapFS{"gate/system.md": file("두 문장 이하로\n답한다."), "gate/schema.json": file(`{"type":"object"}`)},
		},
	}
	for _, tt := range different {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotEqual(t, base.Version, load(t, tt.fsys).Version)
		})
	}

	t.Run("내용이 같으면 다른 일의 지시문이어도 표시가 같다", func(t *testing.T) {
		reg, err := prompts.Load(fstest.MapFS{"a/system.md": file(systemText), "b/system.md": file(systemText)})
		require.NoError(t, err)
		a, err := reg.Get("a")
		require.NoError(t, err)
		b, err := reg.Get("b")
		require.NoError(t, err)

		assert.Equal(t, a.Version, b.Version)
	})
}

func TestRegistry_Get(t *testing.T) {
	reg, err := prompts.Load(fstest.MapFS{
		"gate/system.md":   file(systemText),
		"gate/schema.json": file(`{"type":"object"}`),
	})
	require.NoError(t, err)

	t.Run("없는 지시문은 오류다", func(t *testing.T) {
		_, err := reg.Get("diary")

		require.ErrorIs(t, err, prompts.ErrUnknownTask)
		assert.Contains(t, err.Error(), "diary")
	})

	t.Run("받은 스키마를 고쳐도 다음에 받는 스키마는 그대로다", func(t *testing.T) {
		first, err := reg.Get("gate")
		require.NoError(t, err)
		first.Schema[2] = 'X'

		second, err := reg.Get("gate")
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"object"}`, string(second.Schema))
	})
}

func TestRegistry_Require(t *testing.T) {
	reg, err := prompts.Load(fstest.MapFS{
		"conversation/system.md": file(systemText),
		"gate/system.md":         file(systemText),
	})
	require.NoError(t, err)

	t.Run("모두 있으면 통과한다", func(t *testing.T) {
		require.NoError(t, reg.Require("conversation", "gate"))
	})

	t.Run("없는 것을 모두 알린다", func(t *testing.T) {
		err := reg.Require("conversation", "diary", "memory")

		require.ErrorIs(t, err, prompts.ErrUnknownTask)
		assert.Contains(t, err.Error(), "diary")
		assert.Contains(t, err.Error(), "memory")
		assert.NotContains(t, err.Error(), "conversation")
	})
}

func TestPrompt_Request(t *testing.T) {
	reg, err := prompts.Load(fstest.MapFS{
		"gate/system.md":   file(systemText),
		"gate/schema.json": file(`{"type":"object"}`),
	})
	require.NoError(t, err)
	p, err := reg.Get("gate")
	require.NoError(t, err)

	t.Run("지시문 ID, 본문, 스키마가 채워진 보낼 수 있는 요청을 만든다", func(t *testing.T) {
		messages := []ai.Message{{Role: ai.RoleUser, Text: "오늘 좀 피곤했어"}}

		req := p.Request(messages)

		assert.Equal(t, ai.Request{
			Task:       "gate",
			System:     systemText,
			Messages:   messages,
			JSONSchema: json.RawMessage(`{"type":"object"}`),
		}, req)
		require.NoError(t, req.Validate())
	})

	t.Run("요청의 스키마를 고쳐도 지시문은 그대로다", func(t *testing.T) {
		req := p.Request(nil)
		req.JSONSchema[2] = 'X'

		assert.JSONEq(t, `{"type":"object"}`, string(p.Schema))
	})
}

func TestLoadEmbedded(t *testing.T) {
	reg, err := prompts.LoadEmbedded()
	require.NoError(t, err, "저장소의 지시문 파일이 모두 읽혀야 서버가 뜬다")

	t.Run("확인용 지시문이 들어 있다", func(t *testing.T) {
		require.NoError(t, reg.Require("smoke"))

		smoke, err := reg.Get("smoke")
		require.NoError(t, err)
		assert.NotEmpty(t, smoke.System)
		assert.True(t, json.Valid(smoke.Schema))
		assert.Regexp(t, `^[0-9a-f]{12}$`, smoke.Version)
	})

	t.Run("담긴 지시문은 모두 그대로 보낼 수 있다", func(t *testing.T) {
		for _, task := range reg.Tasks() {
			p, err := reg.Get(task)
			require.NoError(t, err)

			req := p.Request([]ai.Message{{Role: ai.RoleUser, Text: "안녕"}})
			assert.NoError(t, req.Validate(), task)
		}
	})
}
