package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 대화 채널의 메시지는 명세의 어느 경로에도 매달려 있지 않다. 그래서 경로를 거치는 시험으로는 닿지 않는다.
// 만들어진 타입이 실제로 type으로 갈리는지, 명세의 스키마로 들어오는 메시지를 거를 수 있는지를 여기서 확인해 둔다.
func TestWsMessages(t *testing.T) {
	clientMessageID := uuid.MustParse("01996f3a-7c1e-7b3a-9d2e-5f8a1c2b3d4e")

	t.Run("클라이언트의 메시지는 type으로 갈린다", func(t *testing.T) {
		tests := []struct {
			name string
			in   string
			want any
		}{
			{"start", `{"type":"start","mode":"chat"}`, WsStart{Type: WsStartTypeStart, Mode: ConversationModeChat}},
			{"user_text", `{"type":"user_text","client_message_id":"` + clientMessageID.String() + `","text":"오늘은 좀 피곤했어"}`,
				WsUserText{Type: WsUserTextTypeUserText, ClientMessageID: clientMessageID, Text: "오늘은 좀 피곤했어"}},
			{"end", `{"type":"end"}`, WsEnd{Type: WsEndTypeEnd}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var msg WsClientMessage
				require.NoError(t, json.Unmarshal([]byte(tt.in), &msg))
				got, err := msg.ValueByDiscriminator()
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("모르는 type은 오류다", func(t *testing.T) {
		var msg WsClientMessage
		require.NoError(t, json.Unmarshal([]byte(`{"type":"shutdown"}`), &msg))
		_, err := msg.ValueByDiscriminator()
		require.Error(t, err)
	})

	t.Run("서버의 메시지는 From으로 담으면 type이 채워진다", func(t *testing.T) {
		tests := []struct {
			name     string
			fill     func(*WsServerMessage) error
			wantType string
		}{
			{"ready", func(m *WsServerMessage) error { return m.FromWsReady(WsReady{Utterances: []WsUtterance{}}) }, "ready"},
			{"thinking", func(m *WsServerMessage) error { return m.FromWsThinking(WsThinking{ClientMessageID: clientMessageID}) }, "thinking"},
			{"ai_text", func(m *WsServerMessage) error {
				return m.FromWsAIText(WsAIText{Origin: WsOriginFixed, Text: "오늘 하루는 어땠어요?"})
			}, "ai_text"},
			{"resources", func(m *WsServerMessage) error { return m.FromWsResources(WsResources{Items: []Resource{}}) }, "resources"},
			{"ended", func(m *WsServerMessage) error { return m.FromWsEnded(WsEnded{Reason: WsEndReasonUser}) }, "ended"},
			{"diary_ready", func(m *WsServerMessage) error { return m.FromWsDiaryReady(WsDiaryReady{}) }, "diary_ready"},
			{"error", func(m *WsServerMessage) error { return m.FromWsError(WsError{Code: WsErrorCodeRateLimited}) }, "error"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var msg WsServerMessage
				require.NoError(t, tt.fill(&msg))
				out, err := json.Marshal(msg)
				require.NoError(t, err)

				var decoded map[string]any
				require.NoError(t, json.Unmarshal(out, &decoded))
				assert.Equal(t, tt.wantType, decoded["type"], "보내는 쪽이 Type 필드를 채우지 않아도 된다")
			})
		}
	})

	t.Run("서버의 메시지에 단계나 판정이 실릴 자리가 없다", func(t *testing.T) {
		// 화면이 알아야 하는 것은 나가는 말과 도움 자원뿐이다. 판정은 클라이언트에 보내지 않는다.
		spec, err := GetSpec()
		require.NoError(t, err)
		for name, ref := range spec.Components.Schemas {
			if !strings.HasPrefix(name, "Ws") {
				continue
			}
			for property := range ref.Value.Properties {
				assert.NotContains(t, property, "stage", "%s.%s", name, property)
				assert.NotContains(t, property, "gate", "%s.%s", name, property)
			}
		}
	})

	t.Run("들어온 메시지는 명세의 스키마로 거를 수 있다", func(t *testing.T) {
		spec, err := GetSpec()
		require.NoError(t, err)
		schema := spec.Components.Schemas["WsClientMessage"].Value
		require.NotNil(t, schema)

		validate := func(in string) error {
			var decoded any
			require.NoError(t, json.Unmarshal([]byte(in), &decoded))
			return schema.VisitJSON(decoded, openapi3.EnableFormatValidation())
		}
		userText := func(id, text string) string {
			out, err := json.Marshal(map[string]any{"type": "user_text", "client_message_id": id, "text": text})
			require.NoError(t, err)
			return string(out)
		}

		accepted := map[string]string{
			"start":      `{"type":"start","mode":"chat"}`,
			"end":        `{"type":"end"}`,
			"글":          userText(clientMessageID.String(), "오늘은 좀 피곤했어"),
			"한도를 꽉 채운 글": userText(clientMessageID.String(), strings.Repeat("가", 2000)),
		}
		for name, in := range accepted {
			require.NoError(t, validate(in), name)
		}

		rejected := map[string]string{
			"모르는 type":  `{"type":"shutdown"}`,
			"type이 없다":  `{"mode":"chat"}`,
			"모르는 대화 방식": `{"type":"start","mode":"telepathy"}`,
			"모르는 필드":    `{"type":"end","reason":"user"}`,
			"식별자가 없는 글": `{"type":"user_text","text":"안녕"}`,
			"빈 글":       userText(clientMessageID.String(), ""),
			"한도를 넘긴 글":  userText(clientMessageID.String(), strings.Repeat("가", 2001)),
		}
		for name, in := range rejected {
			require.Error(t, validate(in), name)
		}
	})

	t.Run("식별자가 UUID인지는 스키마가 아니라 타입으로 풀 때 걸러진다", func(t *testing.T) {
		// 스키마 검증기는 uuid 꼴을 보지 않는다. 보게 하려면 프로세스 전체가 함께 쓰는 등록부를 고쳐야 한다.
		// 그러지 않아도 타입으로 푸는 단계가 같은 일을 한다. 두 단계를 모두 거쳐야 명세대로 거른 것이다.
		var msg WsClientMessage
		require.NoError(t, json.Unmarshal([]byte(`{"type":"user_text","client_message_id":"not-a-uuid","text":"안녕"}`), &msg))
		_, err := msg.ValueByDiscriminator()
		require.Error(t, err)
	})
}
