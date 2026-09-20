package rules_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
)

func TestLoadEmbedded(t *testing.T) {
	lex, err := rules.LoadEmbedded()
	require.NoError(t, err)

	t.Run("사전의 버전 표시는 내용에서 뽑는다", func(t *testing.T) {
		assert.Regexp(t, `^[0-9a-f]{12}$`, lex.Version())

		again, err := rules.LoadEmbedded()
		require.NoError(t, err)
		assert.Equal(t, lex.Version(), again.Version())
	})

	t.Run("단계가 높은 항목부터 보고 포괄 패턴은 맨 뒤다", func(t *testing.T) {
		ids := lex.EntryIDs()
		require.NotEmpty(t, ids)

		order := map[byte]int{'3': 0, '2': 1, '1': 2}
		last := 0
		sawCatchAll := false
		for _, id := range ids {
			if strings.HasPrefix(id, "c.") {
				sawCatchAll = true
				continue
			}
			require.False(t, sawCatchAll, "포괄 패턴 뒤에 항목이 오면 안 된다: %s", id)
			require.True(t, strings.HasPrefix(id, "s"), id)
			rank, ok := order[id[1]]
			require.True(t, ok, "항목 식별자는 단계로 시작한다: %s", id)
			assert.GreaterOrEqual(t, rank, last, "단계 순서가 뒤집혔다: %s", id)
			last = rank
		}
		assert.True(t, sawCatchAll)
	})
}

// minimal은 고쳐 쓸 수 있는 가장 작은 사전이다.
func minimal() map[string]any {
	return map[string]any{
		"fragments":    map[string]any{"WANT": "고싶"},
		"first_person": "나도",
		"me_too":       "나도",
		"entries": []any{
			map[string]any{"id": "s2.die", "stage": 2, "pattern": "죽<WANT>", "unless": []any{"g.not"}, "note": "직접 표현"},
		},
		"catch_all": []any{
			map[string]any{"id": "c.die", "stage": 1, "pattern": "죽", "note": "포괄"},
		},
		"exclusions": []any{
			map[string]any{"id": "x.idiom", "kind": "idiom", "pattern": "죽겠", "note": "관용 표현"},
		},
		"guards": []any{
			map[string]any{"id": "g.not", "pattern": "죽고싶지않", "note": "부정"},
		},
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func TestParse(t *testing.T) {
	t.Run("가장 작은 사전으로 판정한다", func(t *testing.T) {
		lex, err := rules.Parse(encode(t, minimal()))
		require.NoError(t, err)

		assert.Equal(t, crisis.StageRespond, lex.Scan("죽고 싶어").Stage)
		assert.Equal(t, crisis.StageCheck, lex.Scan("죽고 싶지 않아").Stage, "가드에 가려진 직접 표현은 포괄 패턴이 받는다")

		idiom := lex.Scan("배고파 죽겠다")
		assert.Equal(t, crisis.StageNone, idiom.Stage)
		assert.False(t, idiom.Matched)
		assert.Equal(t, []string{"x.idiom"}, idiom.ExcludedBy)
	})

	entry := func(f map[string]any) map[string]any { return f["entries"].([]any)[0].(map[string]any) }
	catchAll := func(f map[string]any) map[string]any { return f["catch_all"].([]any)[0].(map[string]any) }
	exclusion := func(f map[string]any) map[string]any { return f["exclusions"].([]any)[0].(map[string]any) }
	guard := func(f map[string]any) map[string]any { return f["guards"].([]any)[0].(map[string]any) }

	failures := []struct {
		name   string
		mutate func(f map[string]any)
		want   string
	}{
		{"모르는 필드", func(f map[string]any) { entry(f)["patern"] = "죽" }, "not valid json"},
		{"같은 식별자가 둘", func(f map[string]any) { catchAll(f)["id"] = "s2.die" }, "duplicate id"},
		{"식별자의 꼴이 틀림", func(f map[string]any) { entry(f)["id"] = "S2 Die" }, "id must use"},
		{"단계가 0", func(f map[string]any) { entry(f)["stage"] = 0 }, "stage must be 1, 2 or 3"},
		{"단계가 범위 밖", func(f map[string]any) { entry(f)["stage"] = 4 }, "stage must be 1, 2 or 3"},
		{"포괄 패턴의 단계가 1이 아님", func(f map[string]any) { catchAll(f)["stage"] = 2 }, "catch-all stage must be 1"},
		{"포괄 패턴에 unless", func(f map[string]any) { catchAll(f)["unless"] = []any{"g.not"} }, "takes no unless"},
		{"설명이 없음", func(f map[string]any) { entry(f)["note"] = " " }, "note is required"},
		{"패턴이 없음", func(f map[string]any) { entry(f)["pattern"] = "" }, "pattern is required"},
		{"패턴이 컴파일되지 않음", func(f map[string]any) { entry(f)["pattern"] = "죽(" }, "does not compile"},
		{"빈 글에도 맞는 패턴", func(f map[string]any) { entry(f)["pattern"] = "죽?" }, "matches empty text"},
		{"없는 조각을 부름", func(f map[string]any) { entry(f)["pattern"] = "죽<WISH>" }, "unknown fragment"},
		{"조각이 저를 부름", func(f map[string]any) { f["fragments"].(map[string]any)["WANT"] = "고<WANT>" }, "refers to itself"},
		{"조각 이름이 소문자", func(f map[string]any) { f["fragments"].(map[string]any)["want"] = "고싶" }, "must be upper case"},
		{"없는 가드를 가리킴", func(f map[string]any) { entry(f)["unless"] = []any{"g.none"} }, "unknown id"},
		{"길게 잡는 틈", func(f map[string]any) { entry(f)["pattern"] = "예전.{0,8}죽" }, "gap must be lazy"},
		{"끝없는 틈", func(f map[string]any) { entry(f)["pattern"] = "예전.*?죽" }, "gap must be bounded"},
		{"같은 글자를 세 번 적음", func(f map[string]any) { entry(f)["pattern"] = "하하하" }, "repeats a syllable three times"},
		{"제외 패턴의 갈래가 없음", func(f map[string]any) { exclusion(f)["kind"] = "" }, "unexpected kind"},
		{"제외 패턴의 갈래가 틀림", func(f map[string]any) { exclusion(f)["kind"] = "joke" }, "unexpected kind"},
		{"가드의 갈래가 틀림", func(f map[string]any) { guard(f)["kind"] = "idiom" }, "unexpected kind"},
		{"항목이 없음", func(f map[string]any) { f["entries"] = []any{} }, "at least one entry"},
		{"포괄 패턴이 없음", func(f map[string]any) { f["catch_all"] = []any{} }, "at least one pattern"},
		{"말하는 사람을 가리키는 말이 없음", func(f map[string]any) { f["first_person"] = "" }, "first_person: is required"},
		{"자기를 겹쳐 놓는 말이 없음", func(f map[string]any) { delete(f, "me_too") }, "me_too: is required"},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			f := minimal()
			tt.mutate(f)
			_, err := rules.Parse(encode(t, f))
			require.ErrorIs(t, err, rules.ErrInvalidLexicon)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	t.Run("JSON이 아닌 파일", func(t *testing.T) {
		_, err := rules.Parse([]byte("entries:\n  - id: s2"))
		require.ErrorIs(t, err, rules.ErrInvalidLexicon)
	})

	t.Run("틀린 곳을 한 번에 모아 알려준다", func(t *testing.T) {
		f := minimal()
		entry(f)["stage"] = 9
		catchAll(f)["note"] = ""
		_, err := rules.Parse(encode(t, f))
		require.ErrorIs(t, err, rules.ErrInvalidLexicon)
		assert.Contains(t, err.Error(), "stage must be")
		assert.Contains(t, err.Error(), "note is required")
	})
}

func TestContextGuardIsVetoedByTheSpeaker(t *testing.T) {
	f := minimal()
	f["exclusions"] = append(f["exclusions"].([]any),
		map[string]any{"id": "x.drama", "kind": "context", "pattern": "드라마.{0,10}?죽", "note": "작품 이야기"})
	lex, err := rules.Parse(encode(t, f))
	require.NoError(t, err)

	tests := []struct {
		name  string
		text  string
		stage crisis.Stage
	}{
		{"작품 이야기는 가려낸다", "드라마에서 주인공이 죽었어", 0},
		{"구간 안에 말하는 사람이 있으면 가려내지 않는다", "드라마 보는데 나도 죽을 것 같았어", 1},
		{"구간 바로 뒤에 자기를 겹쳐 놓으면 가려내지 않는다", "드라마에서 죽는 걸 보니 나도 그러고 싶어", 1},
		{"관용 표현에는 이 규칙을 쓰지 않는다", "나도 배고파 죽겠어", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.stage, lex.Scan(tt.text).Stage)
		})
	}
}
