package scripted

import (
	"encoding/json"
	"strings"
)

// fillValues는 스키마의 속성 이름에 맞춰 넣을 값이다.
type fillValues struct {
	ints    map[string]int
	strings map[string]string
	// text는 이 답의 본문이다. textNames 가운데 하나의 이름을 가진 문자열 속성에 들어간다.
	// 그런 속성이 없고 문자열 속성이 하나뿐이면 그 속성에 들어간다.
	text string
}

// textNames는 본문이 들어갈 법한 속성 이름이다.
var textNames = []string{"text", "diary", "draft", "body", "content", "entry", "paragraph"}

// maxSchemaDepth는 스키마를 따라 내려가는 깊이의 한도다. 스스로를 가리키는 스키마에서 멈추기 위한 것이다.
const maxSchemaDepth = 6

// fillSchema는 요청의 스키마를 읽어 그 모양대로 JSON을 만든다.
//
// 스키마는 지시문과 함께 바뀐다. 속성이 늘거나 이름이 바뀌어도 이 패키지가 깨지지 않도록
// 아는 이름에는 값을 넣고 모르는 속성은 타입에 맞는 빈 값으로 채운다.
// 스키마가 없거나 객체가 아니면 아는 값만 담은 객체를 돌려준다.
func fillSchema(raw json.RawMessage, values fillValues) string {
	var schema map[string]any
	if len(raw) > 0 {
		// 읽지 못한 스키마는 없는 것으로 본다. 요청 검증이 JSON인지는 이미 확인했다.
		_ = json.Unmarshal(raw, &schema)
	}

	var out any
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		out = fillObject(props, values, 0)
	} else {
		flat := map[string]any{}
		for k, v := range values.ints {
			flat[k] = v
		}
		for k, v := range values.strings {
			flat[k] = v
		}
		if values.text != "" {
			flat[textNames[0]] = values.text
		}
		out = flat
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func fillObject(props map[string]any, values fillValues, depth int) map[string]any {
	textTarget := ""
	if values.text != "" && depth == 0 {
		textTarget = pickTextProperty(props)
	}

	out := make(map[string]any, len(props))
	for name, rawProp := range props {
		prop, _ := rawProp.(map[string]any)
		key := strings.ToLower(name)
		switch {
		case name == textTarget:
			out[name] = values.text
		case depth == 0 && hasInt(values.ints, key) && isType(prop, "integer", "number"):
			out[name] = values.ints[key]
		case depth == 0 && hasString(values.strings, key) && isType(prop, "string"):
			out[name] = values.strings[key]
		default:
			out[name] = zeroValue(prop, depth)
		}
	}
	return out
}

func pickTextProperty(props map[string]any) string {
	for _, want := range textNames {
		for name, rawProp := range props {
			prop, _ := rawProp.(map[string]any)
			if strings.ToLower(name) == want && isType(prop, "string") {
				return name
			}
		}
	}
	only := ""
	for name, rawProp := range props {
		prop, _ := rawProp.(map[string]any)
		if !isType(prop, "string") || prop["enum"] != nil {
			continue
		}
		if only != "" {
			return ""
		}
		only = name
	}
	return only
}

func zeroValue(prop map[string]any, depth int) any {
	if enum, ok := prop["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch {
	case isType(prop, "string"):
		return ""
	case isType(prop, "integer", "number"):
		if minimum, ok := prop["minimum"].(float64); ok {
			return minimum
		}
		return 0
	case isType(prop, "boolean"):
		return false
	case isType(prop, "array"):
		return []any{}
	case isType(prop, "object"):
		props, ok := prop["properties"].(map[string]any)
		if !ok || depth >= maxSchemaDepth {
			return map[string]any{}
		}
		return fillObject(props, fillValues{}, depth+1)
	default:
		return nil
	}
}

// isType은 표준 JSON Schema의 소문자 이름과 공급자 고유의 대문자 이름("STRING")을 함께 받는다.
func isType(prop map[string]any, names ...string) bool {
	t, _ := prop["type"].(string)
	t = strings.ToLower(t)
	for _, name := range names {
		if t == name {
			return true
		}
	}
	return false
}

func hasInt(m map[string]int, key string) bool {
	_, ok := m[key]
	return ok
}

func hasString(m map[string]string, key string) bool {
	_, ok := m[key]
	return ok
}
