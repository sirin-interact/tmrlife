// Package prompts는 지시문 파일을 읽어 일마다 하나씩 내준다.
//
// 지시문은 코드와 떨어진 파일이라 코드 검토 없이도 바뀐다. 그래서 서버가 뜰 때 한 번에 모두 읽고,
// 빠지거나 빈 파일이 하나라도 있으면 뜨지 않는다. 첫 대화에서야 지시문이 없다는 것을 알게 되는 일을 막는다.
//
// 지시문마다 내용에서 뽑은 짧은 버전 표시를 붙인다. 모델이 만든 결과를 저장할 때 이 표시를 함께 남기면
// 그 결과가 어느 지시문에서 나왔는지 나중에 가릴 수 있다.
package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	promptfiles "github.com/sirin-interact/tmrlife/server/prompts"
)

const (
	SystemFile = "system.md"
	SchemaFile = "schema.json"

	// versionLength는 버전 표시의 글자 수다. 지시문 하나의 판을 가리는 데는 48비트면 넉넉하다.
	versionLength = 12
)

var ErrUnknownTask = errors.New("prompts: unknown task")

// Prompt는 일 하나의 지시문이다.
type Prompt struct {
	// Task는 지시문 ID다.
	Task   string
	System string
	// Schema는 답의 JSON 스키마다. 없으면 nil이다.
	Schema json.RawMessage
	// Version은 System과 Schema의 내용에서 뽑은 표시다. 둘 중 하나라도 바뀌면 달라진다.
	Version string
}

// Request는 이 지시문으로 보낼 요청의 뼈대를 만든다. 출력 한도와 생각하기 수준은 부르는 쪽이 설정에서 채운다.
func (p Prompt) Request(messages []ai.Message) ai.Request {
	return ai.Request{
		Task:       p.Task,
		System:     p.System,
		Messages:   messages,
		JSONSchema: bytes.Clone(p.Schema),
	}
}

// Problem은 지시문 파일 하나가 왜 틀렸는지다.
type Problem struct {
	Path   string
	Reason string
}

// LoadError는 틀린 파일을 한 번에 모아 알려준다.
type LoadError struct {
	Problems []Problem
}

func (e *LoadError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		parts = append(parts, p.Path+": "+p.Reason)
	}
	return "invalid prompt files: " + strings.Join(parts, "; ")
}

type Registry struct {
	prompts map[string]Prompt
}

// LoadEmbedded는 실행 파일에 담긴 지시문을 읽는다.
func LoadEmbedded() (*Registry, error) {
	return Load(promptfiles.FS)
}

// Load는 fsys의 맨 위 디렉터리를 하나씩 지시문으로 읽는다.
// 맨 위의 파일과, 이름이 '.'이나 '_'로 시작하는 것은 지시문이 아니므로 건너뛴다.
func Load(fsys fs.FS) (*Registry, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read prompt directory: %w", err)
	}

	reg := &Registry{prompts: map[string]Prompt{}}
	var problems []Problem
	for _, entry := range entries {
		if !entry.IsDir() || hidden(entry.Name()) {
			continue
		}
		task := entry.Name()
		if !ai.ValidTaskID(task) {
			problems = append(problems, Problem{Path: task, Reason: "directory name must use only lowercase letters, digits, '_' and '-'"})
			continue
		}
		prompt, taskProblems := loadTask(fsys, task)
		if len(taskProblems) > 0 {
			problems = append(problems, taskProblems...)
			continue
		}
		reg.prompts[task] = prompt
	}

	if len(problems) == 0 && len(reg.prompts) == 0 {
		problems = append(problems, Problem{Path: ".", Reason: "no prompt directories found"})
	}
	if len(problems) > 0 {
		return nil, &LoadError{Problems: problems}
	}
	return reg, nil
}

func loadTask(fsys fs.FS, task string) (Prompt, []Problem) {
	entries, err := fs.ReadDir(fsys, task)
	if err != nil {
		return Prompt{}, []Problem{{Path: task, Reason: "could not be read"}}
	}

	var problems []Problem
	var hasSystem, hasSchema bool
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case hidden(name):
		case entry.IsDir():
			problems = append(problems, Problem{Path: path.Join(task, name), Reason: "unexpected directory"})
		case name == SystemFile:
			hasSystem = true
		case name == SchemaFile:
			hasSchema = true
		default:
			// 이름을 잘못 적은 파일은 조용히 무시되고, 그 일은 스키마 없이 돌게 된다. 그래서 모르는 파일은 받지 않는다.
			problems = append(problems, Problem{Path: path.Join(task, name), Reason: "unexpected file (expected " + SystemFile + " and optionally " + SchemaFile + ")"})
		}
	}

	prompt := Prompt{Task: task}

	if !hasSystem {
		problems = append(problems, Problem{Path: path.Join(task, SystemFile), Reason: "is missing"})
	} else if system, reason := readSystem(fsys, path.Join(task, SystemFile)); reason != "" {
		problems = append(problems, Problem{Path: path.Join(task, SystemFile), Reason: reason})
	} else {
		prompt.System = system
	}

	if hasSchema {
		if schema, reason := readSchema(fsys, path.Join(task, SchemaFile)); reason != "" {
			problems = append(problems, Problem{Path: path.Join(task, SchemaFile), Reason: reason})
		} else {
			prompt.Schema = schema
		}
	}

	if len(problems) > 0 {
		return Prompt{}, problems
	}
	prompt.Version = version(prompt.System, prompt.Schema)
	return prompt, nil
}

// readSystem은 지시문 본문을 모델에 보낼 꼴로 읽는다.
// 줄 끝과 앞뒤 공백은 편집기와 운영체제에 따라 달라지므로, 그 차이로 버전 표시가 바뀌지 않게 다듬는다.
func readSystem(fsys fs.FS, name string) (text, reason string) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", "could not be read"
	}
	if !utf8.Valid(raw) {
		return "", "must be UTF-8"
	}
	text = strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if text == "" {
		return "", "is empty"
	}
	if strings.ContainsRune(text, 0) {
		return "", "must not contain NUL"
	}
	return text, ""
}

// readSchema는 스키마를 공백 없는 꼴로 읽는다. 들여쓰기만 고친 것으로는 버전 표시가 바뀌지 않는다.
func readSchema(fsys fs.FS, name string) (schema json.RawMessage, reason string) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, "could not be read"
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, "is empty"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, "must be valid JSON"
	}
	if compact.Bytes()[0] != '{' {
		return nil, "must be a JSON object"
	}
	return compact.Bytes(), ""
}

// version은 모델에 실제로 보내는 내용에서 표시를 뽑는다.
// 본문에는 NUL이 없으므로 NUL 하나로 본문과 스키마의 경계가 분명해진다.
// 이 계산을 바꾸면 이미 저장된 표시와 이어지지 않는다.
func version(system string, schema json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write(schema)
	return hex.EncodeToString(h.Sum(nil))[:versionLength]
}

func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Get은 지시문을 돌려준다. 없는 ID에는 ErrUnknownTask를 돌려준다.
func (r *Registry) Get(task string) (Prompt, error) {
	p, ok := r.prompts[task]
	if !ok {
		return Prompt{}, fmt.Errorf("%w: %q", ErrUnknownTask, task)
	}
	// 받은 쪽이 스키마를 고쳐도 다음 호출에 번지지 않게 한다.
	p.Schema = bytes.Clone(p.Schema)
	return p, nil
}

// Require는 서버가 쓰는 지시문이 모두 있는지 뜰 때 확인하는 데 쓴다.
func (r *Registry) Require(tasks ...string) error {
	var missing []string
	for _, task := range tasks {
		if _, ok := r.prompts[task]; !ok {
			missing = append(missing, task)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrUnknownTask, strings.Join(missing, ", "))
	}
	return nil
}

// Tasks는 읽어 들인 지시문 ID를 이름순으로 돌려준다.
func (r *Registry) Tasks() []string {
	tasks := make([]string, 0, len(r.prompts))
	for task := range r.prompts {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	return tasks
}
