package gemini

import (
	"context"
	"errors"
	"net"
	"strings"

	"google.golang.org/genai"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
)

// missingFinishReason은 공급자가 종료 사유를 주지 않은 채 답이 끝났을 때 남기는 사유 코드다.
// 흘려 받던 연결이 중간에 조용히 끝난 경우가 여기에 든다. 정상 종료로 보지 않는다.
const missingFinishReason = "NO_FINISH_REASON"

// accumulator는 응답 하나, 또는 흘려 받은 조각들에서 판정에 필요한 것만 모은다.
type accumulator struct {
	text         strings.Builder
	sawCandidate bool
	finish       genai.FinishReason
	blockReason  genai.BlockedReason
	usage        ai.Usage
	modelVersion string
}

// add는 조각 하나를 더하고, 그 조각에서 새로 나온 글을 돌려준다.
func (a *accumulator) add(chunk *genai.GenerateContentResponse) string {
	if chunk == nil {
		return ""
	}
	if chunk.ModelVersion != "" {
		a.modelVersion = chunk.ModelVersion
	}
	if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
		a.blockReason = chunk.PromptFeedback.BlockReason
	}
	if u := chunk.UsageMetadata; u != nil {
		// 흘려 받을 때는 조각마다 그때까지의 누계가 온다. 마지막 값이 전체다.
		a.usage = ai.Usage{
			InputTokens:     int(u.PromptTokenCount),
			OutputTokens:    int(u.CandidatesTokenCount),
			ReasoningTokens: int(u.ThoughtsTokenCount),
		}
	}

	// 후보는 하나만 청한다. 여럿이 와도 첫 번째만 본다.
	if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
		return ""
	}
	candidate := chunk.Candidates[0]
	a.sawCandidate = true
	if candidate.FinishReason != "" {
		a.finish = candidate.FinishReason
	}
	if candidate.Content == nil {
		return ""
	}

	var delta strings.Builder
	for _, part := range candidate.Content.Parts {
		// 생각을 옮긴 조각은 답이 아니다. 청하지 않았으니 오지 않아야 하지만, 와도 사용자에게 나가면 안 된다.
		if part == nil || part.Thought {
			continue
		}
		delta.WriteString(part.Text)
	}
	a.text.WriteString(delta.String())
	return delta.String()
}

func (a *accumulator) providerReason() string {
	switch {
	case a.blockReason != "":
		return reasonToken(string(a.blockReason))
	case !a.sawCandidate:
		return ""
	case a.finish == "":
		return missingFinishReason
	default:
		return reasonToken(string(a.finish))
	}
}

// result는 공급자의 결과를 실패 종류로 가린다. 오류 없이 돌려준 답만 쓸 수 있는 답이다.
//
//	요청이 막힘(차단 사유가 있음)            -> ai.ErrBlocked
//	후보가 하나도 없음                       -> ai.ErrNoCandidate
//	출력 한도에 걸림                         -> ai.ErrTruncated
//	정상 종료가 아님(안전 필터, 인용 제한 등) -> ai.ErrAbnormalFinish
//	정상 종료인데 글이 빔                    -> ai.ErrEmpty
//	JSON으로 청했는데 JSON이 아님            -> ai.ErrInvalidJSON
func (a *accumulator) result(req ai.Request, model string) (ai.Response, error) {
	detail := ai.Detail{Task: req.Task, Model: model, Reason: a.providerReason(), Usage: a.usage}

	// 차단 사유가 있으면 후보가 함께 왔더라도 막힌 것으로 본다. 그 후보는 공급자가 내보내지 않으려던 글이다.
	if a.blockReason != "" {
		return ai.Response{}, ai.NewError(ai.ErrBlocked, detail)
	}
	if !a.sawCandidate {
		return ai.Response{}, ai.NewError(ai.ErrNoCandidate, detail)
	}

	resp := ai.Response{
		Text:         a.text.String(),
		FinishReason: finishReason(a.finish),
		Usage:        a.usage,
		// 공급자가 알려주는 판 이름이 아니라 설정에 적은 이름을 쓴다. 예비 모델이 답했는지를 설정값과 견주어 알 수 있어야 한다.
		Model: model,
	}
	if err := ai.CheckResponse(req, resp, detail.Reason); err != nil {
		return ai.Response{}, err
	}
	return resp, nil
}

func finishReason(reason genai.FinishReason) ai.FinishReason {
	switch reason {
	case genai.FinishReasonStop:
		return ai.FinishStop
	case genai.FinishReasonMaxTokens:
		return ai.FinishMaxTokens
	case genai.FinishReasonSafety,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonBlocklist,
		genai.FinishReasonSPII:
		return ai.FinishSafety
	default:
		// 모르는 사유와 빈 사유도 여기로 온다. 공급자가 사유를 더해도 정상 종료로 새지 않는다.
		return ai.FinishOther
	}
}

// classifyError는 SDK가 돌려준 오류를 실패 종류로 바꾼다.
// 원래 오류는 감싸지 않는다. SDK의 오류 문구에는 요청 본문이나 받은 답의 일부가 들어가는 경우가 있다.
func classifyError(ctx context.Context, err error, detail ai.Detail) error {
	// 기한이나 취소로 끊긴 연결은 전송 오류의 모습으로 올라오기도 한다. 컨텍스트가 끝났다면 그 사실이 먼저다.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ai.ContextError(ctxErr, detail)
	}
	if ctxErr := ai.ContextError(err, detail); ctxErr != nil {
		return ctxErr
	}

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		detail.Reason = reasonToken(apiErr.Status)
		return ai.StatusError(apiErr.Code, detail)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			detail.Reason = "transport_timeout"
			return ai.NewError(ai.ErrTimeout, detail)
		}
		detail.Reason = "transport"
		return ai.NewError(ai.ErrProvider, detail)
	}

	// 남는 것은 읽을 수 없는 응답(깨진 JSON, 중간에 끊긴 흐름)이다. 공급자 쪽의 일이고 다시 부르면 풀릴 수 있다.
	detail.Reason = "unreadable_response"
	return ai.NewError(ai.ErrProvider, detail)
}

const maxReasonLength = 64

// reasonToken은 공급자가 준 사유 코드가 코드의 꼴(대문자, 숫자, 밑줄)일 때만 통과시킨다.
// 코드 자리에 사람이 읽는 문장이 오면 버린다. 그 문장에 무엇이 인용되어 있을지 모른다.
func reasonToken(s string) string {
	if s == "" || len(s) > maxReasonLength {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return ""
		}
	}
	return s
}
