package gemini

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// callRecord는 호출 하나에 대해 로그에 남길 것을 모은다. 글은 담지 않는다.
type callRecord struct {
	req     ai.Request
	stream  bool
	started time.Time

	// 실제로 보낸 값이다. 요청이 비워 둔 자리는 모델의 기본값으로 채워진 뒤의 값이다.
	thinking        string
	maxOutputTokens int

	sawDelta   bool
	firstDelta time.Duration

	providerReason string
	modelVersion   string
}

func (l *LLM) begin(req ai.Request, stream bool) *callRecord {
	return &callRecord{req: req, stream: stream, started: l.client.clock.Now()}
}

// finish는 호출이 어떻게 끝났는지 한 줄로 남긴다.
//
// 남기는 것은 길이, 토큰 수, 모델, 걸린 시간, 실패 종류뿐이다. ai.Request와 ai.Response는 스스로 내용을 가리고 길이만 내놓는다.
// 오류는 문구를 옮기지 않고 종류와 사유 코드만 옮긴다. onDelta가 돌려준 오류처럼 이 패키지가 만들지 않은 오류가 섞여 있을 수 있다.
func (l *LLM) finish(ctx context.Context, call *callRecord, resp ai.Response, err error) {
	attrs := []slog.Attr{
		slog.String("model", l.model),
		slog.Bool("stream", call.stream),
		slog.Int64("latency_ms", l.client.clock.Now().Sub(call.started).Milliseconds()),
		slog.Any("request", call.req),
		slog.String("sent_thinking", call.thinking),
		slog.Int("sent_max_output_tokens", call.maxOutputTokens),
	}
	if call.sawDelta {
		attrs = append(attrs, slog.Int64("first_delta_ms", call.firstDelta.Milliseconds()))
	}
	if call.modelVersion != "" {
		attrs = append(attrs, slog.String("model_version", reasonOrInvalid(call.modelVersion)))
	}
	if call.providerReason != "" {
		attrs = append(attrs, slog.String("provider_reason", call.providerReason))
	}

	if err == nil {
		attrs = append(attrs, slog.Any("response", resp))
		l.client.logger.LogAttrs(ctx, slog.LevelInfo, "llm call succeeded", attrs...)
		return
	}

	failure := ai.Classify(err)
	attrs = append(attrs,
		slog.String("failure_kind", string(failure.Kind)),
		slog.Bool("retryable", failure.Retryable),
	)
	var aiErr *ai.Error
	if errors.As(err, &aiErr) {
		attrs = append(attrs,
			slog.String("reason", reasonOrInvalid(aiErr.Detail.Reason)),
			slog.Int("status", aiErr.Detail.Status),
			slog.Int("input_tokens", aiErr.Detail.Usage.InputTokens),
			slog.Int("output_tokens", aiErr.Detail.Usage.OutputTokens),
			slog.Int("reasoning_tokens", aiErr.Detail.Usage.ReasoningTokens),
		)
	}

	level := slog.LevelWarn
	if failure.Kind == ai.KindCanceled {
		// 사용자가 끼어들거나 연결을 끊어서 그만둔 호출이다. 고칠 것이 없다.
		level = slog.LevelInfo
	}
	l.client.logger.LogAttrs(ctx, level, "llm call failed", attrs...)
}

// reasonOrInvalid는 로그에 넣을 짧은 값이 이름의 꼴일 때만 그대로 둔다.
func reasonOrInvalid(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > maxReasonLength {
		return "invalid"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.', r == '/':
		default:
			return "invalid"
		}
	}
	return s
}
