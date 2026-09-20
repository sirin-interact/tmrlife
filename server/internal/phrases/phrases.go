// Package phrases는 모델을 거치지 않고 나가는 말과 도움 자원의 목록을 한곳에 모은다.
//
// 가장 무거운 순간의 말은 모델의 출력이나 장애에 맡기지 않는다. 그래서 그 말은 미리 써 두고,
// 글을 고칠 때 코드를 건드리지 않도록 파일 하나(catalogue.json)에 모아 실행 파일에 담는다.
//
// 말마다 화면용 글(Display)과 음성용 글(Speech)을 따로 둔다. 음성 합성은 숫자를 제멋대로 읽기 때문에
// ("109"를 "백구"로 읽는다) 음성용 글에는 전화번호를 한글로 풀어 적는다.
// 파일을 읽을 때 이 약속이 지켜졌는지 확인하고, 어긋나면 서버가 뜨지 않는다.
package phrases

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed catalogue.json
var embedded []byte

// ID는 고정 문구를 가리키는 이름이다.
type ID string

const (
	// Opening은 대화를 여는 첫 안부다.
	Opening ID = "opening"
	// ReflectFallback은 여러 뜻으로 읽히는 말을 받아 되묻는 문형이다. Catalogue.Reflect로 만든다.
	ReflectFallback ID = "reflect_fallback"
	// DirectAsk는 되물은 뒤에도 뜻이 풀리지 않았을 때 한 번 직접 묻는 말이다.
	DirectAsk ID = "direct_ask"
	// CrisisRespond는 죽고 싶다는 생각이나 자해를 직접 말했을 때의 첫 응답이다.
	CrisisRespond ID = "crisis_respond"
	// CrisisUrgent는 방법, 계획, 시점, 준비, 작별 인사가 있었을 때의 첫 응답이다.
	CrisisUrgent ID = "crisis_urgent"
	// MishearCheck는 음성 인식이 불확실할 때 되묻는 말이다.
	MishearCheck ID = "mishear_check"
	// IdleCheck는 한동안 말이 없을 때 한 번 묻는 말이다.
	IdleCheck ID = "idle_check"
	// SafeReply는 모델의 답을 쓸 수 없을 때 대신 나가는 말이다.
	SafeReply ID = "safe_reply"
)

// IDs는 파일에 반드시 있어야 하는 문구를 모두 돌려준다.
func IDs() []ID {
	return []ID{Opening, ReflectFallback, DirectAsk, CrisisRespond, CrisisUrgent, MishearCheck, IdleCheck, SafeReply}
}

// Phrase는 나갈 말 하나다.
type Phrase struct {
	ID ID
	// Display는 화면에 보이는 글이다. 발화로 저장하는 것도 이 글이다.
	Display string
	// Speech는 음성 합성에 넘기는 글이다. 숫자가 한글로 풀려 있다.
	Speech string
}

// Resource는 사용자가 바로 전화할 수 있는 곳이다.
type Resource struct {
	// ID는 화면이 아이콘을 고르거나 목록을 견줄 때 쓰는 고정된 이름이다.
	ID   string
	Name string
	// Phone은 화면에 그대로 보여주는 전화번호다.
	Phone       string
	Description string
}

// Catalogue는 읽어 들인 문구와 자원이다. 읽은 뒤에는 바뀌지 않으므로 여러 고루틴에서 함께 써도 된다.
type Catalogue struct {
	phrases    map[ID]entry
	resources  []Resource
	urgentRank []string
}

type text struct {
	Display string `json:"display"`
	Speech  string `json:"speech"`
}

type template struct {
	Display string `json:"display"`
	Speech  string `json:"speech"`
	// MaxWordsRunes는 문형에 넣을 수 있는 표현의 최대 길이다. 넘으면 표현 없이 말한다.
	MaxWordsRunes int `json:"max_words_runes"`
}

type entry struct {
	ID ID `json:"id"`
	// Use는 이 말이 언제 나가는지에 대한 설명이다. 글을 검토하는 사람을 위한 것이고 코드는 읽지 않는다.
	Use        string    `json:"use"`
	Display    string    `json:"display"`
	Speech     string    `json:"speech"`
	Template   *template `json:"template"`
	Alternates []text    `json:"alternates"`
}

type resourceEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Description string `json:"description"`
}

type file struct {
	Phrases     []entry         `json:"phrases"`
	Resources   []resourceEntry `json:"resources"`
	UrgentOrder []string        `json:"urgent_order"`
}

// Load는 실행 파일에 담긴 문구를 읽는다.
func Load() (*Catalogue, error) {
	return Parse(embedded)
}

// Parse는 문구 파일을 읽고 약속을 확인한다. 어긋난 곳은 한 번에 모아 알려준다.
func Parse(data []byte) (*Catalogue, error) {
	var f file
	dec := json.NewDecoder(bytes.NewReader(data))
	// 이름을 잘못 적은 필드는 조용히 무시되고, 그 말은 빈 글로 나가게 된다. 그래서 모르는 필드는 받지 않는다.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("phrases: catalogue is not valid: %w", err)
	}

	c := &Catalogue{phrases: make(map[ID]entry, len(f.Phrases))}
	var problems []string
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	known := make(map[ID]bool, len(IDs()))
	for _, id := range IDs() {
		known[id] = true
	}

	phones := make(map[string]bool, len(f.Resources))
	seenResource := make(map[string]bool, len(f.Resources))
	for i, r := range f.Resources {
		switch {
		case !resourceIDPattern.MatchString(r.ID):
			problem("resources[%d]: id must use only lowercase letters, digits and '_'", i)
			continue
		case seenResource[r.ID]:
			problem("resources[%d]: duplicate id %s", i, r.ID)
			continue
		}
		seenResource[r.ID] = true
		if !phonePattern.MatchString(r.Phone) {
			problem("resource %s: phone must be digits separated by '-'", r.ID)
		}
		if reason := plainTextProblem(r.Name); reason != "" {
			problem("resource %s: name %s", r.ID, reason)
		}
		if reason := plainTextProblem(r.Description); reason != "" {
			problem("resource %s: description %s", r.ID, reason)
		}
		phones[r.Phone] = true
		c.resources = append(c.resources, Resource(r))
	}
	if len(c.resources) == 0 {
		problem("resources: at least one resource is required")
	}

	// 급한 순서는 같은 자원을 다르게 늘어놓은 것이어야 한다. 하나라도 빠지면 가장 급한 순간에 번호 하나가 화면에서 사라진다.
	seenUrgent := make(map[string]bool, len(f.UrgentOrder))
	for _, id := range f.UrgentOrder {
		if !seenResource[id] || seenUrgent[id] {
			problem("urgent_order: %q is unknown or repeated", id)
			continue
		}
		seenUrgent[id] = true
	}
	if len(seenUrgent) != len(seenResource) {
		problem("urgent_order: must list every resource exactly once")
	}
	c.urgentRank = append([]string(nil), f.UrgentOrder...)

	for i, e := range f.Phrases {
		switch {
		case !known[e.ID]:
			problem("phrases[%d]: unknown id %q", i, string(e.ID))
			continue
		case hasPhrase(c.phrases, e.ID):
			problem("phrase %s: defined more than once", e.ID)
			continue
		}
		c.phrases[e.ID] = e

		texts := []text{{Display: e.Display, Speech: e.Speech}}
		texts = append(texts, e.Alternates...)
		for n, t := range texts {
			for _, reason := range textProblems(t, phones) {
				problem("phrase %s (text %d): %s", e.ID, n, reason)
			}
			if strings.ContainsAny(t.Display+t.Speech, "{}") {
				problem("phrase %s (text %d): braces are only allowed in a template", e.ID, n)
			}
		}

		if e.ID != SafeReply && len(e.Alternates) > 0 {
			problem("phrase %s: only %s may have alternates", e.ID, SafeReply)
		}
		switch {
		case e.ID == ReflectFallback && e.Template == nil:
			problem("phrase %s: template is required", e.ID)
		case e.ID != ReflectFallback && e.Template != nil:
			problem("phrase %s: only %s may have a template", e.ID, ReflectFallback)
		case e.Template != nil:
			for _, reason := range templateProblems(*e.Template, phones) {
				problem("phrase %s template: %s", e.ID, reason)
			}
		}
	}
	for _, id := range IDs() {
		if !hasPhrase(c.phrases, id) {
			problem("phrase %s: is missing", id)
		}
	}

	if len(problems) > 0 {
		return nil, errors.New("phrases: invalid catalogue: " + strings.Join(problems, "; "))
	}
	return c, nil
}

func hasPhrase(m map[ID]entry, id ID) bool {
	_, ok := m[id]
	return ok
}

var (
	resourceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)
	phonePattern      = regexp.MustCompile(`^[0-9]{2,4}(-[0-9]{3,4}){0,2}$`)
	// 글 안의 전화번호 꼴이다. 세 자리 이상의 숫자이거나 '-'로 이어진 숫자 묶음이다.
	phoneInText = regexp.MustCompile(`[0-9]{2,4}(?:-[0-9]{3,4})+|[0-9]{3,}`)
)

// plainTextProblem은 화면에 그대로 나갈 글이 비었거나 보이지 않는 문자를 품고 있는지 본다.
func plainTextProblem(s string) string {
	switch {
	case strings.TrimSpace(s) == "":
		return "is empty"
	case s != strings.TrimSpace(s):
		return "has leading or trailing space"
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "contains a control character"
		}
	}
	return ""
}

// textProblems는 화면용 글과 음성용 글이 서로 맞는지 본다.
func textProblems(t text, phones map[string]bool) []string {
	var out []string
	if reason := plainTextProblem(t.Display); reason != "" {
		out = append(out, "display "+reason)
	}
	if reason := plainTextProblem(t.Speech); reason != "" {
		out = append(out, "speech "+reason)
	}
	if strings.ContainsFunc(t.Speech, func(r rune) bool { return r >= '0' && r <= '9' }) {
		out = append(out, "speech must spell numbers out in Hangul")
	}
	for _, phone := range phoneInText.FindAllString(t.Display, -1) {
		// 미리 써 둔 말이 화면에 고정되지 않는 번호를 부르면 사용자는 그 번호를 누를 곳이 없다.
		if !phones[phone] {
			out = append(out, "display mentions a number that is not a resource")
			continue
		}
		if !strings.Contains(t.Speech, SpellPhone(phone)) {
			out = append(out, "speech must read "+phone+" as "+SpellPhone(phone))
		}
	}
	return out
}

const wordsSlot = "{words}"

var particleGroup = regexp.MustCompile(`\{([^{}|]+)\|([^{}|]+)\}`)

func templateProblems(t template, phones map[string]bool) []string {
	var out []string
	if t.MaxWordsRunes <= 0 {
		out = append(out, "max_words_runes must be greater than zero")
	}
	for _, side := range []struct{ name, value string }{{"display", t.Display}, {"speech", t.Speech}} {
		if strings.Count(side.value, wordsSlot) != 1 {
			out = append(out, side.name+" must contain "+wordsSlot+" exactly once")
			continue
		}
		rest := particleGroup.ReplaceAllString(strings.Replace(side.value, wordsSlot, "", 1), "")
		if strings.ContainsAny(rest, "{}|") {
			out = append(out, side.name+" has a malformed slot")
		}
	}
	// 자리를 채운 꼴로 나머지 약속을 확인한다.
	filled := text{Display: fill(t.Display, "말"), Speech: fill(t.Speech, "말")}
	return append(out, textProblems(filled, phones)...)
}

// Get은 글이 정해져 있는 문구를 돌려준다. 사용자의 표현을 넣어 만드는 ReflectFallback은 Reflect로 만든다.
func (c *Catalogue) Get(id ID) (Phrase, bool) {
	e, ok := c.phrases[id]
	if !ok {
		return Phrase{}, false
	}
	return Phrase{ID: e.ID, Display: e.Display, Speech: e.Speech}, true
}

func (c *Catalogue) must(id ID) Phrase {
	// Parse가 모든 ID가 있는지 확인했으므로 여기서 빈 값이 나가는 일은 없다.
	p, _ := c.Get(id)
	return p
}

// Opening은 대화를 여는 첫 안부다.
func (c *Catalogue) Opening() Phrase { return c.must(Opening) }

// DirectAsk는 한 대화에서 한 번만 하는 직접 묻기다.
func (c *Catalogue) DirectAsk() Phrase { return c.must(DirectAsk) }

// CrisisRespond는 죽고 싶다는 생각이나 자해를 직접 말했을 때의 첫 응답이다. 자원을 화면에 고정하는 일과 함께 나간다.
func (c *Catalogue) CrisisRespond() Phrase { return c.must(CrisisRespond) }

// CrisisUrgent는 방법, 계획, 시점, 준비, 작별 인사가 있었을 때의 첫 응답이다.
func (c *Catalogue) CrisisUrgent() Phrase { return c.must(CrisisUrgent) }

// MishearCheck는 음성 인식이 불확실할 때 되묻는 말이다.
func (c *Catalogue) MishearCheck() Phrase { return c.must(MishearCheck) }

// IdleCheck는 한동안 말이 없을 때 한 번 묻는 말이다.
func (c *Catalogue) IdleCheck() Phrase { return c.must(IdleCheck) }

// SafeReply는 모델의 답을 쓸 수 없을 때 대신 나가는 말이다.
// previous는 바로 앞에 나간 AI의 말이다. 같은 말이 연달아 나가면 기계와 말하는 느낌이 커지므로, 같으면 다른 글을 고른다.
func (c *Catalogue) SafeReply(previous string) Phrase {
	e := c.phrases[SafeReply]
	previous = strings.TrimSpace(previous)
	if e.Display != previous {
		return Phrase{ID: SafeReply, Display: e.Display, Speech: e.Speech}
	}
	for _, alt := range e.Alternates {
		if alt.Display != previous {
			return Phrase{ID: SafeReply, Display: alt.Display, Speech: alt.Speech}
		}
	}
	return Phrase{ID: SafeReply, Display: e.Display, Speech: e.Speech}
}

// Reflect는 사용자의 표현을 그대로 넣어 되묻는 말을 만든다.
//
// words는 사용자가 한 말이다. 다른 말로 바꾸지 않고 따옴표 안에 그대로 넣는다.
// 표현이 비었거나 문형에 넣기에 너무 길면 표현 없이 되묻는다. 긴 말을 중간에서 자르면 사용자가 하지 않은 말이 된다.
func (c *Catalogue) Reflect(words string) Phrase {
	e := c.phrases[ReflectFallback]
	cleaned := cleanWords(words)
	if cleaned == "" || e.Template == nil || utf8.RuneCountInString(cleaned) > e.Template.MaxWordsRunes {
		return Phrase{ID: ReflectFallback, Display: e.Display, Speech: e.Speech}
	}
	return Phrase{
		ID:      ReflectFallback,
		Display: fill(e.Template.Display, cleaned),
		Speech:  fill(e.Template.Speech, cleaned),
	}
}

// Resources는 도움 자원을 평소의 순서로 돌려준다. 돌려준 값을 고쳐도 목록은 바뀌지 않는다.
func (c *Catalogue) Resources() []Resource {
	return append([]Resource(nil), c.resources...)
}

// UrgentResources는 같은 자원을 가장 급한 순간의 순서로 돌려준다. 바로 걸어야 하는 번호가 앞에 온다.
func (c *Catalogue) UrgentResources() []Resource {
	byID := make(map[string]Resource, len(c.resources))
	for _, r := range c.resources {
		byID[r.ID] = r
	}
	out := make([]Resource, 0, len(c.resources))
	for _, id := range c.urgentRank {
		out = append(out, byID[id])
	}
	return out
}

// fill은 문형의 자리에 표현을 넣고, 바로 뒤의 조사를 받침에 맞춰 고른다.
func fill(tmpl, words string) string {
	first := endsWithFinalConsonant(words)
	out := particleGroup.ReplaceAllStringFunc(tmpl, func(group string) string {
		m := particleGroup.FindStringSubmatch(group)
		if first {
			return m[1]
		}
		return m[2]
	})
	return strings.Replace(out, wordsSlot, words, 1)
}

// cleanWords는 사용자의 말을 따옴표 안에 넣을 수 있는 꼴로 다듬는다. 낱말은 바꾸지 않는다.
func cleanWords(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), unicode.IsSpace(r):
			return ' '
		case strings.ContainsRune(`"'“”‘’「」『』{}|`, r):
			// 따옴표는 문형의 따옴표와 겹치고, 중괄호는 문형의 자리 표시와 겹친다.
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	// 끝의 문장 부호, "ㅠㅠ" 같은 자모, 이모지는 조사 앞에 오면 어색하고 음성으로 읽을 수도 없다.
	return strings.TrimFunc(s, func(r rune) bool { return !isWordRune(r) })
}

func isWordRune(r rune) bool {
	return isHangulSyllable(r) || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

const (
	hangulBase  = 0xAC00
	hangulLast  = 0xD7A3
	finalsCount = 28
)

func isHangulSyllable(r rune) bool { return r >= hangulBase && r <= hangulLast }

// endsWithFinalConsonant는 마지막 글자에 받침이 있는지 본다. "이라는"과 "라는"처럼 받침에 따라 갈리는 조사를 고르는 데 쓴다.
func endsWithFinalConsonant(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	switch {
	case isHangulSyllable(r):
		return (r-hangulBase)%finalsCount != 0
	case r >= '0' && r <= '9':
		// 영, 일, 삼, 육, 칠, 팔은 받침으로 끝난다.
		return strings.ContainsRune("013678", r)
	default:
		return false
	}
}

var digitNames = [...]string{"공", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"}

// SpellPhone은 전화번호를 사람이 번호를 부르는 대로 한글로 푼다. "1577-0199"는 "일오칠칠에 공일구구"가 된다.
// 음성 합성에 숫자를 그대로 넘기면 "109"를 "백구"로 읽는다.
func SpellPhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		switch {
		case r >= '0' && r <= '9':
			b.WriteString(digitNames[r-'0'])
		case r == '-':
			b.WriteString("에 ")
		}
	}
	return b.String()
}
