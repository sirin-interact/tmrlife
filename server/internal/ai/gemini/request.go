package gemini

import (
	"encoding/json"
	"math"
	"strings"

	"google.golang.org/genai"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

var thinkingLevels = map[string]genai.ThinkingLevel{
	ai.ThinkingMinimal: genai.ThinkingLevelMinimal,
	ai.ThinkingLow:     genai.ThinkingLevelLow,
	ai.ThinkingMedium:  genai.ThinkingLevelMedium,
	ai.ThinkingHigh:    genai.ThinkingLevelHigh,
}

// adjustableHarmCategories는 부르는 쪽이 문턱을 정할 수 있는 안전 범주 전부다.
var adjustableHarmCategories = []genai.HarmCategory{
	genai.HarmCategoryHarassment,
	genai.HarmCategoryHateSpeech,
	genai.HarmCategorySexuallyExplicit,
	genai.HarmCategoryDangerousContent,
}

// safetyOff는 공급자의 안전 필터를 범주마다 명시적으로 끈다.
//
// 이 제품에서 가장 위험한 실패는 사용자가 죽음이나 자해를 말한 바로 그 순간에 답이 막히거나 비어서 돌아오는 것이다.
// 공급자의 필터는 그런 말일수록 걸릴 가능성이 높고, 걸리면 사유만 남기고 글을 주지 않는다.
// 위기 판별 모델이 막히면 판정이 한 겹 줄고, 대화 모델이 막히면 가장 힘든 말에 아무 대답도 못 한다.
//
// 안전은 공급자의 필터가 아니라 우리 쪽 위기 관문이 맡는다. 모든 발화가 관문을 먼저 거치고, 위험한 단계에서는
// 모델의 글 대신 미리 써 둔 문구가 나가며, 모델의 글은 나가기 전에 출력 검사를 거친다.
//
// 기본값에 기대지 않고 네 범주를 모두 적는 이유: 기본 문턱은 모델 판마다 다르고 예고 없이 바뀐다.
// 끌 수 없는 공급자 쪽 차단은 여전히 남아 있으므로, 막힌 요청과 빈 답은 계속 실패로 다룬다.
func safetyOff() []*genai.SafetySetting {
	settings := make([]*genai.SafetySetting, 0, len(adjustableHarmCategories))
	for _, category := range adjustableHarmCategories {
		settings = append(settings, &genai.SafetySetting{
			Category:  category,
			Threshold: genai.HarmBlockThresholdOff,
		})
	}
	return settings
}

// buildRequest는 ai.Request를 SDK의 꼴로 옮긴다. req는 고치지 않는다.
//
// temperature는 보내지 않는다. 0으로 두어도 같은 입력에 다른 답이 나오므로 재현성을 위해 낮출 이유가 없고,
// 공급자는 이 계열 모델에서 기본값을 바꾸지 말라고 권한다.
func (l *LLM) buildRequest(req ai.Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	invalid := func(reason string) error {
		detail := l.detail(req)
		detail.Reason = reason
		return ai.NewError(ai.ErrInvalidRequest, detail)
	}

	contents := make([]*genai.Content, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := genai.RoleUser
		if m.Role == ai.RoleModel {
			role = genai.RoleModel
		}
		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: m.Text}},
		})
	}

	maxOutputTokens := req.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = l.maxOutputTokens
	}
	if maxOutputTokens == 0 {
		maxOutputTokens = DefaultMaxOutputTokens
	}
	maxOutputTokens = max(maxOutputTokens, OutputTokenFloor)
	if maxOutputTokens > math.MaxInt32 {
		return nil, nil, invalid("max_output_tokens_too_large")
	}

	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxOutputTokens),
		SafetySettings:  safetyOff(),
	}

	if strings.TrimSpace(req.System) != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: req.System}}}
	}

	thinking := req.Thinking
	if thinking == "" || l.pinThinking {
		thinking = l.thinking
	}
	if thinking != "" {
		level, ok := thinkingLevels[thinking]
		if !ok {
			return nil, nil, invalid("bad_thinking_level")
		}
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	if req.JSONSchema != nil {
		// 지시문 옆의 스키마 파일은 표준 JSON Schema로 쓴다. 공급자 고유의 스키마 꼴로 바꾸지 않고 그대로 보낸다.
		var schema map[string]any
		if err := json.Unmarshal(req.JSONSchema, &schema); err != nil || schema == nil {
			return nil, nil, invalid("json_schema_not_object")
		}
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseJsonSchema = schema
	}

	return contents, cfg, nil
}
