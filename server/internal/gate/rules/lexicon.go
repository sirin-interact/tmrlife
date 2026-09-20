package rules

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // 사전 파일을 실행 파일에 담는다.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
)

// 사전은 파일 하나에 모은다. 표현을 더하고 빼는 일이 코드 검토가 아니라 표현 검토가 되게 하려는 것이다.
//
//go:embed lexicon.json
var embeddedLexicon []byte

// versionLength는 사전 버전 표시의 글자 수다.
const versionLength = 12

// ErrInvalidLexicon은 사전 파일이 약속한 모양이 아니라는 뜻이다. 서버가 뜰 때 드러나야 한다.
var ErrInvalidLexicon = errors.New("rules: invalid lexicon")

// 제외 패턴의 갈래다.
const (
	// kindIdiom은 죽음에 관한 낱말이 상태를 강조하는 데 쓰인 관용 표현이다("배고파 죽겠다").
	kindIdiom = "idiom"
	// kindHomonym은 소리만 같은 다른 낱말이다(먹는 죽).
	kindHomonym = "homonym"
	// kindContext는 다른 사람, 작품, 뉴스, 노랫말처럼 말하는 사람 자신의 이야기가 아닌 경우다.
	// 앞뒤 낱말로 짐작하는 것이라 가장 틀리기 쉽다. 그래서 구간 안에 "나도", "내가" 같은 말이 있으면 쓰지 않는다.
	kindContext = "context"
)

// lexiconFile은 사전 파일의 모양이다.
type lexiconFile struct {
	// Fragments는 패턴에서 <이름>으로 불러 쓰는 조각이다. 바람, 의도, 계획을 나타내는 어미처럼 여러 항목이 함께 쓰는 것을 둔다.
	Fragments map[string]string `json:"fragments"`
	// FirstPerson은 말하는 사람 자신을 가리키는 말이다. 남의 이야기로 본 구간 안에 이것이 있으면 그 판단을 거둔다.
	FirstPerson string `json:"first_person"`
	// MeToo는 남의 이야기에 자기를 겹쳐 놓는 말이다("나도"). 남의 이야기로 본 구간 바로 뒤에 이것이 있어도 그 판단을 거둔다.
	MeToo string `json:"me_too"`
	// Entries는 단계가 정해진 표현이다.
	Entries []entryFile `json:"entries"`
	// CatchAll은 죽음과 자해에 관한 낱말을 넓게 잡는 패턴이다. 제외 패턴에 가려지지 않으면 확인 단계다.
	CatchAll []entryFile `json:"catch_all"`
	// Exclusions는 포괄 패턴에 걸린 말을 가려내는 패턴이다. 항목의 unless에서 불러 쓸 수도 있다.
	Exclusions []exclusionFile `json:"exclusions"`
	// Guards는 항목의 unless에서만 쓰는 패턴이다. 부정, 지난 일, 남의 말을 옮긴 것처럼
	// 직접 표현이 직접 표현이 아니게 되는 자리다. 포괄 패턴은 가리지 않으므로 그 말은 확인 단계로 남는다.
	// 남의 이야기로 짐작하는 가드에는 kind를 context로 적는다.
	Guards []exclusionFile `json:"guards"`
}

type entryFile struct {
	ID      string   `json:"id"`
	Stage   int      `json:"stage"`
	Pattern string   `json:"pattern"`
	Unless  []string `json:"unless"`
	Note    string   `json:"note"`
}

type exclusionFile struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

type entry struct {
	id     string
	stage  crisis.Stage
	re     *regexp.Regexp
	unless []*exclusion
}

type exclusion struct {
	id   string
	kind string
	re   *regexp.Regexp
	// index는 한 번의 판정 안에서 이 패턴의 결과를 다시 쓰기 위한 자리다.
	index int
}

// Lexicon은 읽어 들인 사전이다. 만든 뒤에는 바뀌지 않으므로 여러 고루틴에서 함께 써도 된다.
type Lexicon struct {
	version     string
	entries     []entry
	catchAll    []entry
	exclusions  []*exclusion
	guards      []*exclusion
	firstPerson *regexp.Regexp
	meToo       *regexp.Regexp
}

// LoadEmbedded는 실행 파일에 담긴 사전을 읽는다. 서버가 뜰 때 한 번 부른다.
func LoadEmbedded() (*Lexicon, error) {
	return Parse(embeddedLexicon)
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_.]{0,63}$`)
	fragmentName    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
	fragmentRef     = regexp.MustCompile(`<([A-Z][A-Z0-9_]*)>`)
	greedyGap       = regexp.MustCompile(`\.(?:\*|\+|\{\d*,?\d*\})(?:[^?]|$)`)
	unboundedRepeat = regexp.MustCompile(`\.(?:\*|\+|\{\d+,\})`)
)

// Parse는 사전 파일을 읽고 확인한다. 틀린 곳은 한 번에 모아 알려준다.
//
// 패턴은 Go의 regexp로 돌린다. 이 엔진은 되돌아가며 찾지 않아서 어떤 패턴과 어떤 글에서도
// 글의 길이에 비례하는 시간 안에 끝난다. 일부러 만든 긴 글로 관문을 멈춰 세울 수 없다.
func Parse(data []byte) (*Lexicon, error) {
	var file lexiconFile
	dec := json.NewDecoder(bytes.NewReader(data))
	// 이름을 잘못 적은 필드는 조용히 무시되고, 그 항목은 조건 없이 돌게 된다. 그래서 모르는 필드는 받지 않는다.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: not valid json", ErrInvalidLexicon)
	}

	var problems []string
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	fragments, fragmentProblems := expandFragments(file.Fragments)
	problems = append(problems, fragmentProblems...)

	compile := func(id, pattern string) *regexp.Regexp {
		if !idPattern.MatchString(id) {
			problem("%q: id must use lowercase letters, digits, '_' and '.'", id)
		}
		source, err := expand(pattern, fragments)
		if err != nil {
			problem("%s: %v", id, err)
			return nil
		}
		if reason := patternProblem(source); reason != "" {
			problem("%s: %s", id, reason)
			return nil
		}
		re, err := regexp.Compile(source)
		if err != nil {
			problem("%s: pattern does not compile", id)
			return nil
		}
		if re.MatchString("") {
			problem("%s: pattern matches empty text", id)
			return nil
		}
		return re
	}

	lex := &Lexicon{version: versionOf(data)}
	seen := map[string]bool{}
	unique := func(id string) {
		if seen[id] {
			problem("%s: duplicate id", id)
		}
		seen[id] = true
	}

	byID := map[string]*exclusion{}
	addExclusions := func(items []exclusionFile, excluding bool) []*exclusion {
		out := make([]*exclusion, 0, len(items))
		for _, item := range items {
			unique(item.ID)
			if strings.TrimSpace(item.Note) == "" {
				problem("%s: note is required", item.ID)
			}
			kind := item.Kind
			switch {
			case !excluding && (kind == "" || kind == kindContext):
			case excluding && (kind == kindIdiom || kind == kindHomonym || kind == kindContext):
			default:
				problem("%s: unexpected kind %q", item.ID, kind)
			}
			ex := &exclusion{id: item.ID, kind: kind, re: compile(item.ID, item.Pattern), index: len(byID)}
			byID[item.ID] = ex
			out = append(out, ex)
		}
		return out
	}
	lex.exclusions = addExclusions(file.Exclusions, true)
	lex.guards = addExclusions(file.Guards, false)

	addEntries := func(items []entryFile, catchAll bool) []entry {
		out := make([]entry, 0, len(items))
		for _, item := range items {
			unique(item.ID)
			if strings.TrimSpace(item.Note) == "" {
				problem("%s: note is required", item.ID)
			}
			stage, err := crisis.StageFromInt(item.Stage)
			switch {
			case err != nil || stage == crisis.StageNone:
				problem("%s: stage must be 1, 2 or 3", item.ID)
			case catchAll && stage != crisis.StageCheck:
				problem("%s: catch-all stage must be 1", item.ID)
			}
			e := entry{id: item.ID, stage: stage, re: compile(item.ID, item.Pattern)}
			if catchAll && len(item.Unless) > 0 {
				problem("%s: catch-all is cancelled by every exclusion and takes no unless", item.ID)
			}
			for _, ref := range item.Unless {
				ex, ok := byID[ref]
				if !ok {
					problem("%s: unless refers to unknown id %q", item.ID, ref)
					continue
				}
				e.unless = append(e.unless, ex)
			}
			out = append(out, e)
		}
		return out
	}
	lex.entries = addEntries(file.Entries, false)
	lex.catchAll = addEntries(file.CatchAll, true)

	if len(lex.entries) == 0 {
		problem("entries: at least one entry is required")
	}
	if len(lex.catchAll) == 0 {
		problem("catch_all: at least one pattern is required")
	}
	if strings.TrimSpace(file.FirstPerson) == "" {
		problem("first_person: is required")
	} else {
		lex.firstPerson = compile("first_person", file.FirstPerson)
	}
	if strings.TrimSpace(file.MeToo) == "" {
		problem("me_too: is required")
	} else {
		lex.meToo = compile("me_too", file.MeToo)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidLexicon, strings.Join(problems, "; "))
	}

	// 단계가 높은 항목부터 본다. 같은 단계 안에서는 파일에 적힌 순서다.
	ordered := make([]entry, 0, len(lex.entries))
	for stage := crisis.StageUrgent; stage >= crisis.StageCheck; stage-- {
		for _, e := range lex.entries {
			if e.stage == stage {
				ordered = append(ordered, e)
			}
		}
	}
	lex.entries = ordered
	return lex, nil
}

// expandFragments는 조각 안의 조각까지 풀어 둔다. 서로를 부르는 조각은 받지 않는다.
func expandFragments(raw map[string]string) (map[string]string, []string) {
	var problems []string
	out := make(map[string]string, len(raw))
	for name := range raw {
		if !fragmentName.MatchString(name) {
			problems = append(problems, fmt.Sprintf("fragment %q: name must be upper case", name))
		}
	}

	var resolve func(name string, path []string) (string, error)
	resolve = func(name string, path []string) (string, error) {
		if done, ok := out[name]; ok {
			return done, nil
		}
		source, ok := raw[name]
		if !ok {
			return "", fmt.Errorf("unknown fragment <%s>", name)
		}
		for _, p := range path {
			if p == name {
				return "", fmt.Errorf("fragment <%s> refers to itself", name)
			}
		}
		var firstErr error
		expanded := fragmentRef.ReplaceAllStringFunc(source, func(ref string) string {
			inner, err := resolve(ref[1:len(ref)-1], append(path, name))
			if err != nil && firstErr == nil {
				firstErr = err
			}
			return inner
		})
		if firstErr != nil {
			return "", firstErr
		}
		// 조각이 패턴 어디에 놓여도 한 덩어리로 읽히게 묶는다.
		expanded = "(?:" + expanded + ")"
		out[name] = expanded
		return expanded, nil
	}
	for name := range raw {
		if _, err := resolve(name, nil); err != nil {
			problems = append(problems, fmt.Sprintf("fragment %q: %v", name, err))
		}
	}
	return out, problems
}

func expand(pattern string, fragments map[string]string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", errors.New("pattern is required")
	}
	var firstErr error
	source := fragmentRef.ReplaceAllStringFunc(pattern, func(ref string) string {
		expanded, ok := fragments[ref[1:len(ref)-1]]
		if !ok && firstErr == nil {
			firstErr = fmt.Errorf("unknown fragment %s", ref)
		}
		return expanded
	})
	return source, firstErr
}

// patternProblem은 컴파일은 되지만 뜻대로 돌지 않을 패턴을 걸러낸다.
func patternProblem(source string) string {
	// 다듬은 글에는 같은 글자가 세 번 이어지는 일이 없다. 그런 패턴은 영영 걸리지 않는다.
	var prev rune = -1
	run := 0
	for _, r := range source {
		if r == prev {
			run++
		} else {
			run = 1
		}
		if run > maxRun && r >= 0xAC00 && r <= 0xD7A3 {
			return "pattern repeats a syllable three times, which never survives normalization"
		}
		prev = r
	}
	// 사이에 낀 글자를 넘기는 자리(.{0,8}?)는 짧은 쪽부터 찾아야 한다. 길게 잡으면 앞의 표현을 가리려던 구간이
	// 뒤에 다시 나온 같은 표현까지 덮어서, 두 번째로 한 말까지 함께 가려진다.
	if greedyGap.MatchString(source) {
		return "gap must be lazy, write .{0,n}? instead of .{0,n}"
	}
	if unboundedRepeat.MatchString(source) {
		return "gap must be bounded"
	}
	return ""
}

func versionOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:versionLength]
}

// Version은 사전의 내용에서 뽑은 표시다. 사전이 바뀌면 달라진다.
// 판정을 돌아볼 때 어느 판의 사전으로 내린 판정인지 가리는 데 쓴다.
func (l *Lexicon) Version() string {
	return l.version
}

// EntryIDs는 사전의 항목 식별자를 판정에 쓰는 순서대로 돌려준다. 포괄 패턴이 맨 뒤다.
func (l *Lexicon) EntryIDs() []string {
	ids := make([]string, 0, len(l.entries)+len(l.catchAll))
	for _, e := range l.entries {
		ids = append(ids, e.id)
	}
	for _, e := range l.catchAll {
		ids = append(ids, e.id)
	}
	return ids
}
