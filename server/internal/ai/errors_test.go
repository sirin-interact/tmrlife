package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userText는 오류 문구에 절대 나오면 안 되는 글이다.
const userText = "오늘은 아무것도 하기 싫었어"

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		kind      Kind
		retryable bool
	}{
		{"오류가 없으면 실패가 아니다", nil, KindNone, false},
		{"요청이 거절되면 다시 보내도 같으므로 다시 부르지 않는다", ErrBlocked, KindBlocked, false},
		{"잘못된 요청은 다시 부르지 않는다", ErrInvalidRequest, KindInvalidRequest, false},
		{"사유 없이 후보가 없으면 다시 부른다", ErrNoCandidate, KindNoCandidate, true},
		{"비정상 종료는 출력 쪽의 일이라 다시 부른다", ErrAbnormalFinish, KindAbnormalFinish, true},
		{"빈 답은 다시 부른다", ErrEmpty, KindEmpty, true},
		{"잘린 답은 다시 부른다", ErrTruncated, KindTruncated, true},
		{"JSON이 아닌 답은 다시 부른다", ErrInvalidJSON, KindInvalidJSON, true},
		{"시간 초과는 다시 부른다", ErrTimeout, KindTimeout, true},
		{"공급자의 일시적인 실패는 다시 부른다", ErrProvider, KindProvider, true},
		{"설명이 붙어 있어도 종류를 알아본다", NewError(ErrTruncated, Detail{Task: "conversation"}), KindTruncated, true},
		{"다른 오류로 한 번 더 감싸도 종류를 알아본다", fmt.Errorf("turn 3: %w", NewError(ErrBlocked, Detail{})), KindBlocked, false},
		{"부른 쪽이 취소한 것은 다시 부르지 않는다", context.Canceled, KindCanceled, false},
		{"취소를 설명으로 감싸도 취소다", ContextError(context.Canceled, Detail{Task: "gate"}), KindCanceled, false},
		{"기한이 지난 것은 시간 초과다", context.DeadlineExceeded, KindTimeout, true},
		{"기한을 설명으로 감싸도 시간 초과다", ContextError(context.DeadlineExceeded, Detail{}), KindTimeout, true},
		{"모르는 오류는 다시 부르지 않는다", errors.New("boom"), KindUnknown, false},
		{
			"둘 다 실패하면 종류는 주 모델을 따르고, 한쪽이라도 일시적이면 다시 부른다",
			&HedgeError{Primary: ErrBlocked, Fallback: ErrTimeout}, KindBlocked, true,
		},
		{
			"둘 다 다시 불러도 소용없는 실패면 다시 부르지 않는다",
			&HedgeError{Primary: ErrBlocked, Fallback: ErrInvalidRequest}, KindBlocked, false,
		},
		{
			"둘 다 실패한 오류를 한 번 더 감싸도 같게 본다",
			fmt.Errorf("reply: %w", &HedgeError{Primary: ErrProvider, Fallback: ErrBlocked}), KindProvider, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			assert.Equal(t, tt.kind, got.Kind)
			assert.Equal(t, tt.retryable, got.Retryable)
		})
	}
}

func TestError_Message(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"설명이 없으면 종류만 적는다",
			NewError(ErrEmpty, Detail{}),
			"ai: empty response",
		},
		{
			"지시문, 모델, 사유, 토큰 수를 적는다",
			NewError(ErrTruncated, Detail{
				Task: "conversation", Model: "gemini-3.8-flash", Reason: "MAX_TOKENS",
				Usage: Usage{InputTokens: 812, OutputTokens: 12, ReasoningTokens: 388},
			}),
			"ai: response truncated (task=conversation model=gemini-3.8-flash reason=MAX_TOKENS input_tokens=812 output_tokens=12 reasoning_tokens=388)",
		},
		{
			"상태 코드를 적는다",
			StatusError(503, Detail{Task: "gate", Model: "models/gemini-3.5-flash"}),
			"ai: provider error (task=gate model=models/gemini-3.5-flash status=503)",
		},
		{
			"취소는 고정 문구로 적는다",
			ContextError(context.Canceled, Detail{Task: "gate"}),
			"ai: canceled (task=gate)",
		},
		{
			"둘 다 실패하면 양쪽을 모두 적는다",
			&HedgeError{
				Primary:  NewError(ErrTimeout, Detail{Model: "a"}),
				Fallback: NewError(ErrProvider, Detail{Model: "b", Status: 500}),
			},
			"ai: primary and fallback both failed: primary: ai: timeout (model=a); fallback: ai: provider error (model=b status=500)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestError_NeverCarriesText(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			"설명의 모든 자리에 문장이 잘못 들어와도 옮기지 않는다",
			NewError(ErrAbnormalFinish, Detail{Task: userText, Model: userText, Reason: userText}),
		},
		{
			"공백이 든 영어 문장도 옮기지 않는다",
			NewError(ErrAbnormalFinish, Detail{Reason: "i do not want to do anything"}),
		},
		{
			"너무 긴 값은 옮기지 않는다",
			NewError(ErrAbnormalFinish, Detail{Reason: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}),
		},
		{
			"실패 종류 자리에 모르는 오류가 들어와도 그 문구를 옮기지 않는다",
			NewError(errors.New(userText), Detail{}),
		},
		{
			"구현이 다른 오류를 그대로 돌려줘도 둘 다 실패한 오류에는 그 문구가 없다",
			&HedgeError{Primary: errors.New(userText), Fallback: fmt.Errorf("wrapped %s: %w", userText, ErrTimeout)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			assert.NotContains(t, msg, userText)
			assert.NotContains(t, msg, "anything")
			assert.NotContains(t, msg, "aaaa")
		})
	}

	t.Run("둘 다 실패한 오류는 구현이 그대로 돌려준 오류도 종류로만 적는다", func(t *testing.T) {
		err := &HedgeError{Primary: errors.New(userText), Fallback: fmt.Errorf("wrapped %s: %w", userText, ErrTimeout)}
		assert.Equal(t, "ai: primary and fallback both failed: primary: ai: error; fallback: ai: timeout", err.Error())
	})
}

func TestError_Unwrap(t *testing.T) {
	t.Run("설명을 붙여도 errors.Is와 errors.As로 찾을 수 있다", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", NewError(ErrTruncated, Detail{Task: "diary", Usage: Usage{ReasoningTokens: 400}}))

		require.ErrorIs(t, err, ErrTruncated)
		require.NotErrorIs(t, err, ErrEmpty)

		var detailed *Error
		require.ErrorAs(t, err, &detailed)
		assert.Equal(t, "diary", detailed.Detail.Task)
		assert.Equal(t, 400, detailed.Detail.Usage.ReasoningTokens)
	})

	t.Run("기한 때문에 난 시간 초과는 context.DeadlineExceeded로도 잡힌다", func(t *testing.T) {
		err := ContextError(context.DeadlineExceeded, Detail{Task: "gate"})

		require.ErrorIs(t, err, ErrTimeout)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("공급자가 알려준 시간 초과는 ctx의 기한과 상관없다", func(t *testing.T) {
		err := NewError(ErrTimeout, Detail{})

		require.ErrorIs(t, err, ErrTimeout)
		require.NotErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("취소는 context.Canceled로 남고 시간 초과가 되지 않는다", func(t *testing.T) {
		err := ContextError(context.Canceled, Detail{})

		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrTimeout)
	})

	t.Run("둘 다 실패한 오류는 양쪽의 종류로 모두 잡힌다", func(t *testing.T) {
		err := &HedgeError{Primary: NewError(ErrTimeout, Detail{}), Fallback: NewError(ErrBlocked, Detail{})}

		require.ErrorIs(t, err, ErrTimeout)
		require.ErrorIs(t, err, ErrBlocked)
		require.NotErrorIs(t, err, ErrEmpty)
	})
}

func TestContextError(t *testing.T) {
	t.Run("ctx와 상관없는 오류에는 nil을 돌려준다", func(t *testing.T) {
		assert.NoError(t, ContextError(errors.New("connection reset"), Detail{}))
		assert.NoError(t, ContextError(nil, Detail{}))
	})

	t.Run("감싸인 ctx 오류도 알아본다", func(t *testing.T) {
		err := ContextError(fmt.Errorf("post: %w", context.DeadlineExceeded), Detail{})
		require.ErrorIs(t, err, ErrTimeout)
	})
}

func TestStatusError(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		sentinel  error
		retryable bool
	}{
		{"400은 잘못된 요청이다", 400, ErrInvalidRequest, false},
		{"401은 키가 틀린 것이라 다시 부르지 않는다", 401, ErrInvalidRequest, false},
		{"403은 다시 부르지 않는다", 403, ErrInvalidRequest, false},
		{"404는 모델 이름이 틀린 것이라 다시 부르지 않는다", 404, ErrInvalidRequest, false},
		{"408은 시간 초과다", 408, ErrTimeout, true},
		{"429는 잠시 뒤에 다시 부른다", 429, ErrProvider, true},
		{"500은 다시 부른다", 500, ErrProvider, true},
		{"503은 다시 부른다", 503, ErrProvider, true},
		{"504는 시간 초과다", 504, ErrTimeout, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := StatusError(tt.status, Detail{Task: "gate"})

			require.ErrorIs(t, err, tt.sentinel)
			assert.Equal(t, tt.retryable, Classify(err).Retryable)

			var detailed *Error
			require.ErrorAs(t, err, &detailed)
			assert.Equal(t, tt.status, detailed.Detail.Status)
			assert.Equal(t, "gate", detailed.Detail.Task)
		})
	}
}
