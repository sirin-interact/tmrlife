package ai_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
)

const hedgeDelay = 3 * time.Second

func hedgeRequest() ai.Request {
	return ai.Request{
		Task:     "conversation",
		System:   "짧게 답한다.",
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "오늘 좀 피곤했어"}},
	}
}

// pendingCall은 다른 고루틴에서 돌고 있는 Generate 호출 하나다.
type pendingCall struct {
	done chan struct{}
	resp ai.Response
	err  error
}

func startGenerate(ctx context.Context, llm ai.LLM, req ai.Request) *pendingCall {
	c := &pendingCall{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.resp, c.err = llm.Generate(ctx, req)
	}()
	return c
}

func (c *pendingCall) finished() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// bubbleClock은 synctest의 가짜 시계를 정해진 시점까지 돌린다.
// 현재 시각을 읽지 않고도 "몇 초에 무슨 일이 일어났는가"를 확인하려고, 흘려보낸 시간을 직접 센다.
type bubbleClock struct {
	elapsed time.Duration
}

func (c *bubbleClock) advanceTo(at time.Duration) {
	time.Sleep(at - c.elapsed)
	c.elapsed = at
	synctest.Wait()
}

func TestHedged_Generate(t *testing.T) {
	const never = time.Duration(-1)

	tests := []struct {
		name     string
		primary  fake.Step
		fallback fake.Step
		// wantFallbackAt은 예비 모델이 불리는 시점이다.
		wantFallbackAt time.Duration
		wantDoneAt     time.Duration
		wantModel      string
		wantErrs       []error
		wantNotErrs    []error
		// 진 쪽이 컨텍스트로 중단되었는지 본다.
		wantPrimaryInterrupted  int
		wantFallbackInterrupted int
	}{
		{
			name:           "주 모델이 제때 답하면 예비 모델을 부르지 않는다",
			primary:        fake.Step{Text: "주 모델의 답", Latency: 2 * time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: time.Second},
			wantFallbackAt: never,
			wantDoneAt:     2 * time.Second,
			wantModel:      "primary",
		},
		{
			name:                   "주 모델이 늦으면 정해 둔 시간에 예비 모델을 부르고 먼저 온 답을 쓴다",
			primary:                fake.Step{Text: "주 모델의 답", Latency: 12 * time.Second},
			fallback:               fake.Step{Text: "예비 모델의 답", Latency: 2 * time.Second},
			wantFallbackAt:         hedgeDelay,
			wantDoneAt:             hedgeDelay + 2*time.Second,
			wantModel:              "fallback",
			wantPrimaryInterrupted: 1,
		},
		{
			name:                    "예비 모델을 부른 뒤에도 주 모델이 먼저 답하면 주 모델의 답을 쓴다",
			primary:                 fake.Step{Text: "주 모델의 답", Latency: 4 * time.Second},
			fallback:                fake.Step{Text: "예비 모델의 답", Latency: 5 * time.Second},
			wantFallbackAt:          hedgeDelay,
			wantDoneAt:              4 * time.Second,
			wantModel:               "primary",
			wantFallbackInterrupted: 1,
		},
		{
			name:           "주 모델이 다시 부를 만한 실패로 일찍 끝나면 기다리지 않고 바로 예비 모델을 부른다",
			primary:        fake.Step{Err: ai.NewError(ai.ErrProvider, ai.Detail{Status: 503}), Latency: time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: time.Second},
			wantFallbackAt: time.Second,
			wantDoneAt:     2 * time.Second,
			wantModel:      "fallback",
		},
		{
			name:           "주 모델의 답이 비어 있어도 바로 예비 모델을 부른다",
			primary:        fake.Step{Text: "", Latency: time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: time.Second},
			wantFallbackAt: time.Second,
			wantDoneAt:     2 * time.Second,
			wantModel:      "fallback",
		},
		{
			name:           "주 모델의 답이 잘려도 바로 예비 모델을 부른다",
			primary:        fake.Step{Text: "오늘 많이", FinishReason: ai.FinishMaxTokens, Latency: time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: time.Second},
			wantFallbackAt: time.Second,
			wantDoneAt:     2 * time.Second,
			wantModel:      "fallback",
		},
		{
			name:           "요청이 거절되면 예비 모델을 부르지 않고 그 실패를 돌려준다",
			primary:        fake.Step{Err: ai.NewError(ai.ErrBlocked, ai.Detail{Reason: "SAFETY"}), Latency: time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: time.Second},
			wantFallbackAt: never,
			wantDoneAt:     time.Second,
			wantErrs:       []error{ai.ErrBlocked},
		},
		{
			name:           "일찍 실패한 주 모델에 이어 예비 모델도 실패하면 두 실패를 함께 돌려준다",
			primary:        fake.Step{Err: ai.ErrNoCandidate, Latency: time.Second},
			fallback:       fake.Step{Err: ai.ErrBlocked, Latency: time.Second},
			wantFallbackAt: time.Second,
			wantDoneAt:     2 * time.Second,
			wantErrs:       []error{ai.ErrNoCandidate, ai.ErrBlocked},
		},
		{
			name:           "둘 다 늦게 실패하면 나중 것까지 기다렸다가 두 실패를 함께 돌려준다",
			primary:        fake.Step{Err: ai.ErrProvider, Latency: 6 * time.Second},
			fallback:       fake.Step{Err: ai.ErrEmpty, Latency: time.Second},
			wantFallbackAt: hedgeDelay,
			wantDoneAt:     6 * time.Second,
			wantErrs:       []error{ai.ErrProvider, ai.ErrEmpty},
		},
		{
			name:           "예비 모델이 먼저 실패해도 주 모델의 답을 끝까지 기다린다",
			primary:        fake.Step{Text: "주 모델의 답", Latency: 7 * time.Second},
			fallback:       fake.Step{Err: ai.ErrProvider, Latency: time.Second},
			wantFallbackAt: hedgeDelay,
			wantDoneAt:     7 * time.Second,
			wantModel:      "primary",
		},
		{
			name:           "예비 모델이 이미 떠 있으면 주 모델이 거절당해도 예비 모델의 답을 기다린다",
			primary:        fake.Step{Err: ai.ErrBlocked, Latency: 4 * time.Second},
			fallback:       fake.Step{Text: "예비 모델의 답", Latency: 3 * time.Second},
			wantFallbackAt: hedgeDelay,
			wantDoneAt:     6 * time.Second,
			wantModel:      "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				primary := fake.New("primary")
				primary.Enqueue(tt.primary)
				fallback := fake.New("fallback")
				fallback.Enqueue(tt.fallback)
				hedged, err := ai.NewHedged(primary, fallback, hedgeDelay)
				require.NoError(t, err)

				var clock bubbleClock
				call := startGenerate(context.Background(), hedged, hedgeRequest())

				if tt.wantFallbackAt != never {
					clock.advanceTo(tt.wantFallbackAt - time.Nanosecond)
					assert.Zero(t, fallback.Calls(), "예비 모델이 너무 일찍 불렸다")
					clock.advanceTo(tt.wantFallbackAt)
					assert.Equal(t, 1, fallback.Calls(), "예비 모델이 제때 불리지 않았다")
				}
				clock.advanceTo(tt.wantDoneAt - time.Nanosecond)
				require.False(t, call.finished(), "너무 일찍 돌아왔다")
				clock.advanceTo(tt.wantDoneAt)
				require.True(t, call.finished(), "제때 돌아오지 않았다")

				if len(tt.wantErrs) == 0 {
					require.NoError(t, call.err)
					assert.Equal(t, tt.wantModel, call.resp.Model)
					assert.Equal(t, ai.FinishStop, call.resp.FinishReason)
				} else {
					for _, want := range tt.wantErrs {
						require.ErrorIs(t, call.err, want)
					}
					assert.Equal(t, ai.Response{}, call.resp)
				}

				// 진 쪽이 취소되어 끝날 때까지 기다린다.
				synctest.Wait()
				assert.Equal(t, 1, primary.Calls())
				if tt.wantFallbackAt == never {
					assert.Zero(t, fallback.Calls())
				}
				assert.Equal(t, tt.wantPrimaryInterrupted, primary.Interrupted())
				assert.Equal(t, tt.wantFallbackInterrupted, fallback.Interrupted())
			})
		})
	}
}

func TestHedged_SameRequestToBoth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := fake.New("primary")
		primary.Enqueue(fake.Step{Text: "주 모델의 답", Latency: 10 * time.Second})
		fallback := fake.New("fallback")
		fallback.Enqueue(fake.Step{Text: "예비 모델의 답", Latency: time.Second})
		hedged, err := ai.NewHedged(primary, fallback, hedgeDelay)
		require.NoError(t, err)

		req := hedgeRequest()
		req.MaxOutputTokens = 2048
		req.Thinking = ai.ThinkingLow

		_, err = hedged.Generate(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, []ai.Request{req}, primary.Requests())
		assert.Equal(t, []ai.Request{req}, fallback.Requests())
	})
}

func TestHedged_BothFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := fake.New("primary")
		primary.Enqueue(fake.Step{Err: ai.NewError(ai.ErrBlocked, ai.Detail{Model: "primary"}), Latency: 5 * time.Second})
		fallback := fake.New("fallback")
		fallback.Enqueue(fake.Step{Err: ai.NewError(ai.ErrTimeout, ai.Detail{Model: "fallback"}), Latency: time.Second})
		hedged, err := ai.NewHedged(primary, fallback, hedgeDelay)
		require.NoError(t, err)

		_, err = hedged.Generate(context.Background(), hedgeRequest())

		var both *ai.HedgeError
		require.ErrorAs(t, err, &both)
		require.ErrorIs(t, both.Primary, ai.ErrBlocked)
		require.ErrorIs(t, both.Fallback, ai.ErrTimeout)
		assert.Equal(t, ai.Failure{Kind: ai.KindBlocked, Retryable: true}, ai.Classify(err))
	})
}

func TestHedged_Context(t *testing.T) {
	slowPair := func(t *testing.T) (*ai.Hedged, *fake.LLM, *fake.LLM) {
		t.Helper()
		primary := fake.New("primary")
		primary.Enqueue(fake.Step{Text: "주 모델의 답", Latency: 30 * time.Second})
		fallback := fake.New("fallback")
		fallback.Enqueue(fake.Step{Text: "예비 모델의 답", Latency: 30 * time.Second})
		hedged, err := ai.NewHedged(primary, fallback, hedgeDelay)
		require.NoError(t, err)
		return hedged, primary, fallback
	}

	t.Run("부른 쪽이 취소하면 바로 돌아오고 두 호출을 모두 멈춘다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hedged, primary, fallback := slowPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var clock bubbleClock
			call := startGenerate(ctx, hedged, hedgeRequest())
			clock.advanceTo(5 * time.Second)
			require.False(t, call.finished())

			cancel()
			synctest.Wait()

			require.True(t, call.finished())
			require.ErrorIs(t, call.err, context.Canceled)
			assert.Equal(t, ai.Failure{Kind: ai.KindCanceled}, ai.Classify(call.err))
			assert.Equal(t, 1, primary.Interrupted())
			assert.Equal(t, 1, fallback.Interrupted())
		})
	})

	t.Run("예비 모델을 부르기 전에 취소하면 예비 모델은 불리지 않는다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hedged, primary, fallback := slowPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var clock bubbleClock
			call := startGenerate(ctx, hedged, hedgeRequest())
			clock.advanceTo(time.Second)
			cancel()
			synctest.Wait()
			require.True(t, call.finished())

			clock.advanceTo(10 * time.Second)
			require.ErrorIs(t, call.err, context.Canceled)
			assert.Equal(t, 1, primary.Interrupted())
			assert.Zero(t, fallback.Calls())
		})
	})

	t.Run("기한이 지나면 시간 초과로 돌아온다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hedged, primary, fallback := slowPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			var clock bubbleClock
			call := startGenerate(ctx, hedged, hedgeRequest())
			clock.advanceTo(8*time.Second - time.Nanosecond)
			require.False(t, call.finished())
			clock.advanceTo(8 * time.Second)
			require.True(t, call.finished())

			require.ErrorIs(t, call.err, ai.ErrTimeout)
			require.ErrorIs(t, call.err, context.DeadlineExceeded)
			assert.True(t, ai.Classify(call.err).Retryable)
			assert.Equal(t, 1, primary.Interrupted())
			assert.Equal(t, 1, fallback.Interrupted())
		})
	})

	t.Run("이미 끝난 컨텍스트로 부르면 아무 모델도 부르지 않는다", func(t *testing.T) {
		hedged, primary, fallback := slowPair(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := hedged.Generate(ctx, hedgeRequest())

		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, primary.Calls())
		assert.Zero(t, fallback.Calls())
	})
}

func TestHedged_Panic(t *testing.T) {
	t.Run("구현 안의 패닉은 부른 쪽의 고루틴으로 옮겨진다", func(t *testing.T) {
		primary := fake.New("primary")
		primary.SetHandler(func(context.Context, ai.Request) fake.Step { panic("boom") })
		fallback := fake.New("fallback")
		hedged, err := ai.NewHedged(primary, fallback, hedgeDelay)
		require.NoError(t, err)

		assert.PanicsWithValue(t, "boom", func() {
			_, _ = hedged.Generate(context.Background(), hedgeRequest())
		})
	})
}

func TestNewHedged(t *testing.T) {
	llm := fake.New("m")

	tests := []struct {
		name     string
		primary  ai.LLM
		fallback ai.LLM
		delay    time.Duration
		wantErr  bool
	}{
		{"둘 다 있고 기다리는 시간이 양수면 만든다", llm, llm, time.Second, false},
		{"주 모델이 없다", nil, llm, time.Second, true},
		{"예비 모델이 없다", llm, nil, time.Second, true},
		{"기다리는 시간이 0이다", llm, llm, 0, true},
		{"기다리는 시간이 음수다", llm, llm, -time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hedged, err := ai.NewHedged(tt.primary, tt.fallback, tt.delay)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, hedged)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, hedged)
		})
	}
}
