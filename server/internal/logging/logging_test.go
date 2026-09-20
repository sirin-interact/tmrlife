package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const spoken = "오늘은 아무것도 하기 싫었어요"

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m), "로그는 한 줄에 JSON 하나여야 한다")
		lines = append(lines, m)
	}
	return lines
}

func TestNew_Level(t *testing.T) {
	tests := []struct {
		name      string
		level     slog.Level
		wantLines []string
	}{
		{"debug면 모두 찍힌다", slog.LevelDebug, []string{"d", "i", "w", "e"}},
		{"info면 debug가 빠진다", slog.LevelInfo, []string{"i", "w", "e"}},
		{"warn이면 warn과 error만 찍힌다", slog.LevelWarn, []string{"w", "e"}},
		{"error면 error만 찍힌다", slog.LevelError, []string{"e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(&buf, tt.level)
			logger.Debug("d")
			logger.Info("i")
			logger.Warn("w")
			logger.Error("e")

			var got []string
			for _, line := range decodeLines(t, &buf) {
				got = append(got, line["msg"].(string))
			}
			assert.Equal(t, tt.wantLines, got)
		})
	}
}

func TestNew_JSONShape(t *testing.T) {
	t.Run("시각, 수준, 메시지와 넘긴 식별자가 JSON으로 찍힌다", func(t *testing.T) {
		var buf bytes.Buffer
		New(&buf, slog.LevelInfo).Info("turn finished", "conversation_id", "c-1", "stage", "gate")

		lines := decodeLines(t, &buf)
		require.Len(t, lines, 1)
		assert.Equal(t, "INFO", lines[0]["level"])
		assert.Equal(t, "turn finished", lines[0]["msg"])
		assert.Equal(t, "c-1", lines[0]["conversation_id"])
		assert.Equal(t, "gate", lines[0]["stage"])
		assert.NotEmpty(t, lines[0]["time"])
	})
}

func TestRedacted(t *testing.T) {
	r := Redacted(spoken)
	const want = "[REDACTED len=16]"

	t.Run("fmt의 어떤 서식으로 찍어도 내용이 나오지 않는다", func(t *testing.T) {
		for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d"} {
			out := fmt.Sprintf(verb, r)
			assert.Equal(t, want, out, "서식 %s", verb)
		}
	})

	t.Run("구조체나 슬라이스 안에 있어도 내용이 나오지 않는다", func(t *testing.T) {
		type turn struct {
			ID   string
			Text Redacted
		}
		out := fmt.Sprintf("%+v %v", turn{ID: "t-1", Text: r}, []Redacted{r})
		assert.NotContains(t, out, "오늘")
		assert.Contains(t, out, want)
	})

	t.Run("오류로 감싸도 내용이 나오지 않는다", func(t *testing.T) {
		err := fmt.Errorf("save turn %q: %w", r, errors.New("db down"))
		assert.NotContains(t, err.Error(), "오늘")
	})

	t.Run("slog로 넘기면 표시와 글자 수만 찍힌다", func(t *testing.T) {
		var buf bytes.Buffer
		New(&buf, slog.LevelInfo).Info("turn", "spoken", r, slog.Group("turn", slog.Any("spoken", r)))
		assert.NotContains(t, buf.String(), "오늘")

		lines := decodeLines(t, &buf)
		require.Len(t, lines, 1)
		assert.Equal(t, want, lines[0]["spoken"])
		assert.Equal(t, want, lines[0]["turn"].(map[string]any)["spoken"])
	})

	t.Run("JSON으로 직렬화해도 내용이 나오지 않는다", func(t *testing.T) {
		out, err := json.Marshal(map[string]any{"text": r})
		require.NoError(t, err)
		assert.JSONEq(t, `{"text":"[REDACTED len=16]"}`, string(out))
	})

	t.Run("글자 수는 바이트가 아니라 글자로 센다", func(t *testing.T) {
		assert.Equal(t, "[REDACTED len=2]", Redacted("마음").String())
		assert.Equal(t, "[REDACTED len=0]", Redacted("").String())
	})

	t.Run("명시적으로 string으로 바꾸면 내용을 꺼낼 수 있다", func(t *testing.T) {
		assert.Equal(t, spoken, string(r))
	})
}

func TestHandler_RedactsBannedKeys(t *testing.T) {
	tests := []struct {
		name string
		attr slog.Attr
		want any
	}{
		{"내용을 담을 법한 이름이면 일반 문자열도 가린다", slog.String("utterance", spoken), keyRedactedMarker},
		{"이름의 대소문자를 가리지 않는다", slog.String("Diary", spoken), keyRedactedMarker},
		{"문자열이 아닌 값도 가린다", slog.Any("body", []byte(spoken)), keyRedactedMarker},
		{"이메일 주소를 가린다", slog.String("email", "someone@example.com"), keyRedactedMarker},
		{"쿼리 문자열을 가린다", slog.String("query", "q=오늘"), keyRedactedMarker},
		{"Redacted로 감싼 값은 글자 수 표시를 그대로 둔다", slog.Any("utterance", Redacted(spoken)), "[REDACTED len=16]"},
		{"표시를 흉내 낸 앞머리로는 가리기를 피하지 못한다", slog.String("utterance", "[REDACTED len=1] "+spoken), keyRedactedMarker},
		{"식별자는 그대로 찍힌다", slog.String("user_id", "u-123"), "u-123"},
		{"단계는 그대로 찍힌다", slog.String("stage", "gate"), "gate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, slog.LevelInfo).LogAttrs(t.Context(), slog.LevelInfo, "event", tt.attr)

			lines := decodeLines(t, &buf)
			require.Len(t, lines, 1)
			assert.Equal(t, tt.want, lines[0][tt.attr.Key])
			if tt.want == keyRedactedMarker {
				assert.NotContains(t, buf.String(), "오늘")
				assert.NotContains(t, buf.String(), "someone@example.com")
			}
		})
	}

	t.Run("묶음 안쪽의 이름도 가린다", func(t *testing.T) {
		var buf bytes.Buffer
		New(&buf, slog.LevelInfo).Info("event", slog.Group("turn", slog.String("id", "t-1"), slog.String("text", spoken)))

		lines := decodeLines(t, &buf)
		require.Len(t, lines, 1)
		group := lines[0]["turn"].(map[string]any)
		assert.Equal(t, "t-1", group["id"])
		assert.Equal(t, keyRedactedMarker, group["text"])
	})

	t.Run("With로 미리 붙인 속성도 가린다", func(t *testing.T) {
		var buf bytes.Buffer
		New(&buf, slog.LevelInfo).With("transcript", spoken).Info("event")
		assert.NotContains(t, buf.String(), "오늘")
	})

	t.Run("메시지 자체는 건드리지 않는다", func(t *testing.T) {
		var buf bytes.Buffer
		New(&buf, slog.LevelInfo).Info("request finished")
		lines := decodeLines(t, &buf)
		require.Len(t, lines, 1)
		assert.Equal(t, "request finished", lines[0]["msg"])
	})
}
