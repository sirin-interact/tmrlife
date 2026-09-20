package phrases

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func load(t *testing.T) *Catalogue {
	t.Helper()
	c, err := Load()
	require.NoError(t, err)
	return c
}

func TestLoad(t *testing.T) {
	c := load(t)

	t.Run("정해 둔 문구가 모두 있고 화면용 글과 음성용 글을 가진다", func(t *testing.T) {
		for _, id := range IDs() {
			p, ok := c.Get(id)
			require.True(t, ok, string(id))
			assert.Equal(t, id, p.ID)
			assert.NotEmpty(t, p.Display, string(id))
			assert.NotEmpty(t, p.Speech, string(id))
		}
	})

	t.Run("모르는 이름에는 없다고 답한다", func(t *testing.T) {
		_, ok := c.Get("farewell")
		assert.False(t, ok)
	})

	t.Run("음성용 글에는 숫자가 없다", func(t *testing.T) {
		for _, id := range IDs() {
			p, _ := c.Get(id)
			assert.NotRegexp(t, `[0-9]`, p.Speech, string(id))
		}
	})

	t.Run("이름으로 꺼내는 길과 메서드로 꺼내는 길이 같은 글을 준다", func(t *testing.T) {
		byMethod := map[ID]Phrase{
			Opening:       c.Opening(),
			DirectAsk:     c.DirectAsk(),
			CrisisRespond: c.CrisisRespond(),
			CrisisUrgent:  c.CrisisUrgent(),
			MishearCheck:  c.MishearCheck(),
			IdleCheck:     c.IdleCheck(),
		}
		for id, got := range byMethod {
			want, _ := c.Get(id)
			assert.Equal(t, want, got, string(id))
		}
	})
}

func TestFixedCopy(t *testing.T) {
	c := load(t)

	t.Run("직접 묻기는 단어를 피하지 않고 정해진 문장 그대로다", func(t *testing.T) {
		p := c.DirectAsk()
		assert.Equal(t, "그런 생각이 들 만큼 힘들었네요. 혹시 죽고 싶다는 생각도 들어요?", p.Display)
		assert.Equal(t, p.Display, p.Speech)
	})

	t.Run("죽고 싶다는 말에 대한 첫 응답은 109를 알리고 음성으로는 일공구라고 읽는다", func(t *testing.T) {
		p := c.CrisisRespond()
		assert.Contains(t, p.Display, "109")
		assert.Contains(t, p.Speech, "일공구")
		assert.Contains(t, p.Display, "1577-0199")
		assert.Contains(t, p.Speech, "일오칠칠에 공일구구")
	})

	t.Run("가장 급한 응답은 109와 119를 맨 앞 문장에 둔다", func(t *testing.T) {
		p := c.CrisisUrgent()
		first, _, _ := strings.Cut(p.Display, ".")
		assert.Contains(t, first, "109")
		assert.Contains(t, first, "119")
		firstSpoken, _, _ := strings.Cut(p.Speech, ".")
		assert.Contains(t, firstSpoken, "일공구")
		assert.Contains(t, firstSpoken, "일일구")
	})

	t.Run("가장 급한 응답은 곁에 있어 줄 사람을 지금 부르도록 권한다", func(t *testing.T) {
		assert.Regexp(t, `사람을 지금 불러`, c.CrisisUrgent().Display)
	})

	t.Run("어느 문구도 허락을 구하거나 안내문처럼 말하지 않는다", func(t *testing.T) {
		banned := regexp.MustCompile(`여쭤|물어봐도|드려도 될까요|당신|권장|바랍니다|하시기 바|전문가|극단적|나쁜 생각`)
		for _, id := range IDs() {
			p, _ := c.Get(id)
			assert.NotRegexp(t, banned, p.Display, string(id))
		}
		assert.NotRegexp(t, banned, c.Reflect("다 사라졌으면 좋겠어").Display)
	})

	t.Run("말이 없을 때 묻는 말은 그만해도 된다는 것을 함께 알린다", func(t *testing.T) {
		assert.Contains(t, c.IdleCheck().Display, "여기까지 해도 괜찮아요")
	})
}

func TestReflect(t *testing.T) {
	c := load(t)

	cases := []struct {
		name    string
		words   string
		display string
		speech  string
	}{
		{
			"사용자의 표현을 바꾸지 않고 따옴표 안에 넣는다",
			"다 사라졌으면 좋겠어",
			"“다 사라졌으면 좋겠어”라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"다 사라졌으면 좋겠어라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
		{
			"받침으로 끝나는 표현에는 이라는을 붙인다",
			"이제 그만",
			"“이제 그만”이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"이제 그만이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
		{
			"끝의 문장 부호와 자모는 떼고 받침은 마지막 글자로 본다",
			"  그냥 다 끝났으면…ㅠㅠ ",
			"“그냥 다 끝났으면”이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"그냥 다 끝났으면이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
		{
			"줄바꿈과 겹친 공백은 한 칸으로 줄이고 따옴표는 뺀다",
			"자고\n일어나지   \"않았으면\" 좋겠어.",
			"“자고 일어나지 않았으면 좋겠어”라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"자고 일어나지 않았으면 좋겠어라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
		{
			"문형의 자리 표시와 겹치는 글자는 뺀다",
			"{words} 그만하고 싶다|",
			"“words 그만하고 싶다”라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"words 그만하고 싶다라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
		{
			"숫자로 끝나면 숫자를 읽는 소리의 받침을 따른다",
			"내 점수는 0",
			"“내 점수는 0”이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
			"내 점수는 0이라는 말이 마음에 남아요. 오늘 무슨 일 있었어요?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := c.Reflect(tc.words)
			assert.Equal(t, ReflectFallback, p.ID)
			assert.Equal(t, tc.display, p.Display)
			assert.Equal(t, tc.speech, p.Speech)
		})
	}

	plain, _ := c.Get(ReflectFallback)
	withoutWords := []struct {
		name  string
		words string
	}{
		{"표현이 비었으면 표현 없이 되묻는다", ""},
		{"문장 부호뿐이면 표현 없이 되묻는다", " …?! "},
		{"너무 긴 말은 자르지 않고 표현 없이 되묻는다", strings.Repeat("그냥 다 싫어 ", 8)},
	}
	for _, tc := range withoutWords {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, plain, c.Reflect(tc.words))
		})
	}

	t.Run("되묻는 말은 언제나 질문 하나로 끝난다", func(t *testing.T) {
		for _, p := range []Phrase{plain, c.Reflect("사라지고 싶다")} {
			assert.Equal(t, 1, strings.Count(p.Display, "?"))
			assert.True(t, strings.HasSuffix(p.Display, "?"))
		}
	})
}

func TestSafeReply(t *testing.T) {
	c := load(t)
	first := c.SafeReply("")

	t.Run("평소에는 첫 글을 준다", func(t *testing.T) {
		assert.Equal(t, SafeReply, first.ID)
		assert.Equal(t, first, c.SafeReply("오늘 많이 지치셨나 봐요."))
	})

	t.Run("바로 앞에 같은 말이 나갔으면 다른 글을 고른다", func(t *testing.T) {
		second := c.SafeReply(first.Display)
		assert.NotEqual(t, first.Display, second.Display)
		assert.Equal(t, first, c.SafeReply(second.Display))
	})

	t.Run("어느 글도 묻지 않는다", func(t *testing.T) {
		for _, p := range []Phrase{first, c.SafeReply(first.Display)} {
			assert.NotContains(t, p.Display, "?")
		}
	})
}

func TestResources(t *testing.T) {
	c := load(t)

	phones := func(rs []Resource) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.Phone)
		}
		return out
	}

	t.Run("평소에는 이야기 들어줄 곳이 앞에 온다", func(t *testing.T) {
		rs := c.Resources()
		assert.Equal(t, []string{"109", "1577-0199", "119"}, phones(rs))
		assert.Equal(t, "자살예방상담전화", rs[0].Name)
		assert.Equal(t, "suicide_prevention_109", rs[0].ID)
		for _, r := range rs {
			assert.NotEmpty(t, r.Description, r.ID)
		}
	})

	t.Run("가장 급한 순간에는 바로 걸 번호 둘이 앞에 온다", func(t *testing.T) {
		assert.Equal(t, []string{"109", "119", "1577-0199"}, phones(c.UrgentResources()))
	})

	t.Run("돌려받은 목록을 고쳐도 원본은 그대로다", func(t *testing.T) {
		rs := c.Resources()
		rs[0].Phone = "000"
		assert.Equal(t, "109", c.Resources()[0].Phone)
	})
}

func TestSpellPhone(t *testing.T) {
	cases := []struct {
		name  string
		phone string
		want  string
	}{
		{"세 자리 번호는 한 자씩 읽는다", "109", "일공구"},
		{"119도 한 자씩 읽는다", "119", "일일구"},
		{"줄표는 에로 읽는다", "1577-0199", "일오칠칠에 공일구구"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SpellPhone(tc.phone))
		})
	}
}

// mutate는 실제 문구 파일을 읽어 한 곳만 고친 파일을 만든다.
func mutate(t *testing.T, change func(doc map[string]any, phrase func(id ID) map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(embedded, &doc))
	phrase := func(id ID) map[string]any {
		for _, raw := range doc["phrases"].([]any) {
			if p := raw.(map[string]any); p["id"] == string(id) {
				return p
			}
		}
		t.Fatalf("phrase %s not found", id)
		return nil
	}
	change(doc, phrase)
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name   string
		change func(doc map[string]any, phrase func(id ID) map[string]any)
		want   string
	}{
		{
			"문구 하나가 빠졌다",
			func(doc map[string]any, _ func(ID) map[string]any) {
				doc["phrases"] = doc["phrases"].([]any)[1:]
			},
			"phrase opening: is missing",
		},
		{
			"모르는 이름의 문구가 있다",
			func(_ map[string]any, phrase func(ID) map[string]any) { phrase(IdleCheck)["id"] = "idle_chek" },
			"unknown id",
		},
		{
			"같은 문구가 두 번 있다",
			func(doc map[string]any, phrase func(ID) map[string]any) {
				doc["phrases"] = append(doc["phrases"].([]any), phrase(Opening))
			},
			"defined more than once",
		},
		{
			"화면용 글이 비었다",
			func(_ map[string]any, phrase func(ID) map[string]any) { phrase(Opening)["display"] = " " },
			"display is empty",
		},
		{
			"글 앞에 공백이 있다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(Opening)["speech"] = " 오늘 어땠어요?"
			},
			"speech has leading or trailing space",
		},
		{
			"음성용 글에 숫자가 그대로 있다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				p := phrase(CrisisRespond)
				p["speech"] = p["display"]
			},
			"speech must spell numbers out in Hangul",
		},
		{
			"음성용 글이 번호를 다르게 읽는다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				p := phrase(CrisisRespond)
				p["speech"] = strings.ReplaceAll(p["speech"].(string), "일공구", "백구")
			},
			"speech must read 109 as 일공구",
		},
		{
			"자원 목록에 없는 번호를 말한다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				p := phrase(CrisisRespond)
				p["display"] = strings.ReplaceAll(p["display"].(string), "109", "1393")
			},
			"display mentions a number that is not a resource",
		},
		{
			"되묻는 문형에 표현을 넣을 자리가 없다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(ReflectFallback)["template"].(map[string]any)["display"] = "오늘 무슨 일 있었어요?"
			},
			"display must contain {words} exactly once",
		},
		{
			"되묻는 문형의 조사 자리가 깨졌다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(ReflectFallback)["template"].(map[string]any)["speech"] = "{words}{이라는 말이 마음에 남아요."
			},
			"speech has a malformed slot",
		},
		{
			"되묻는 문형이 없다",
			func(_ map[string]any, phrase func(ID) map[string]any) { delete(phrase(ReflectFallback), "template") },
			"template is required",
		},
		{
			"다른 문구에 문형이 붙었다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(Opening)["template"] = phrase(ReflectFallback)["template"]
			},
			"only reflect_fallback may have a template",
		},
		{
			"다른 문구에 바꿔 쓸 글이 붙었다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(Opening)["alternates"] = phrase(SafeReply)["alternates"]
			},
			"only safe_reply may have alternates",
		},
		{
			"문형이 아닌 글에 중괄호가 있다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(Opening)["display"] = "{words} 어땠어요?"
			},
			"braces are only allowed in a template",
		},
		{
			"필드 이름을 잘못 적었다",
			func(_ map[string]any, phrase func(ID) map[string]any) {
				phrase(Opening)["speach"] = "오늘 어땠어요?"
			},
			"unknown field",
		},
		{
			"자원의 전화번호가 숫자가 아니다",
			func(doc map[string]any, _ func(ID) map[string]any) {
				doc["resources"].([]any)[2].(map[string]any)["phone"] = "일일구"
			},
			"phone must be digits",
		},
		{
			"급한 순서에 자원 하나가 빠졌다",
			func(doc map[string]any, _ func(ID) map[string]any) {
				doc["urgent_order"] = doc["urgent_order"].([]any)[:2]
			},
			"urgent_order: must list every resource exactly once",
		},
		{
			"급한 순서에 모르는 자원이 있다",
			func(doc map[string]any, _ func(ID) map[string]any) {
				doc["urgent_order"].([]any)[0] = "police_112"
			},
			"is unknown or repeated",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(mutate(t, tc.change))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("JSON이 아니다", func(t *testing.T) {
		_, err := Parse([]byte("phrases:"))
		require.Error(t, err)
	})

	t.Run("고치지 않은 파일은 통과한다", func(t *testing.T) {
		_, err := Parse(mutate(t, func(map[string]any, func(ID) map[string]any) {}))
		require.NoError(t, err)
	})
}
