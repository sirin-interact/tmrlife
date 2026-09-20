package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
)

func request(text string) ai.Request {
	return ai.Request{
		Task:     "conversation",
		System:   "짧게 답한다.",
		Messages: []ai.Message{{Role: ai.RoleUser, Text: text}},
	}
}

func TestLLM_Script(t *testing.T) {
	t.Run("대본을 넣은 순서대로 하나씩 쓴다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("첫 번째 답"), fake.Reply("두 번째 답"))

		first, err := llm.Generate(context.Background(), request("하나"))
		require.NoError(t, err)
		second, err := llm.Generate(context.Background(), request("둘"))
		require.NoError(t, err)

		assert.Equal(t, ai.Response{Text: "첫 번째 답", FinishReason: ai.FinishStop, Model: "fake-model"}, first)
		assert.Equal(t, "두 번째 답", second.Text)
		assert.Zero(t, llm.Pending())
		assert.Equal(t, 2, llm.Calls())
	})

	t.Run("대본에 적은 모델 이름과 토큰 수를 그대로 돌려준다", func(t *testing.T) {
		llm := fake.New("fake-model")
		usage := ai.Usage{InputTokens: 120, OutputTokens: 9, ReasoningTokens: 40}
		llm.Enqueue(fake.Step{Text: "답", Model: "other-model", Usage: usage})

		resp, err := llm.Generate(context.Background(), request("하나"))

		require.NoError(t, err)
		assert.Equal(t, "other-model", resp.Model)
		assert.Equal(t, usage, resp.Usage)
	})

	t.Run("대본이 비면 Handler가 요청을 보고 답한다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("대본의 답"))
		llm.SetHandler(func(_ context.Context, req ai.Request) fake.Step {
			return fake.Reply("지시문 " + req.Task + "의 답")
		})

		scripted, err := llm.Generate(context.Background(), request("하나"))
		require.NoError(t, err)
		handled, err := llm.Generate(context.Background(), request("둘"))
		require.NoError(t, err)

		assert.Equal(t, "대본의 답", scripted.Text)
		assert.Equal(t, "지시문 conversation의 답", handled.Text)
	})

	t.Run("대본도 Handler도 없으면 모델의 실패가 아닌 오류로 알린다", func(t *testing.T) {
		llm := fake.New("fake-model")

		_, err := llm.Generate(context.Background(), request("하나"))

		require.ErrorIs(t, err, fake.ErrNoScript)
		assert.Equal(t, ai.KindUnknown, ai.Classify(err).Kind)
	})

	t.Run("대본의 오류를 그대로 돌려준다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Fail(ai.ErrBlocked))

		resp, err := llm.Generate(context.Background(), request("하나"))

		require.ErrorIs(t, err, ai.ErrBlocked)
		assert.Equal(t, ai.Response{}, resp)
	})
}

func TestLLM_ProviderBehaviour(t *testing.T) {
	jsonRequest := request("하나")
	jsonRequest.JSONSchema = json.RawMessage(`{"type":"object"}`)

	tests := []struct {
		name string
		req  ai.Request
		step fake.Step
		want error
	}{
		{"정상 종료인데 글이 빈 답", request("하나"), fake.Step{Text: ""}, ai.ErrEmpty},
		{"출력 한도에 걸려 잘린 답", request("하나"), fake.Step{Text: "오늘 많이", FinishReason: ai.FinishMaxTokens}, ai.ErrTruncated},
		{"생각 토큰이 한도를 다 쓴 답", request("하나"), fake.Step{FinishReason: ai.FinishMaxTokens, Usage: ai.Usage{ReasoningTokens: 400}}, ai.ErrTruncated},
		{"안전 필터에 걸려 끝난 답", request("하나"), fake.Step{Text: "오늘", FinishReason: ai.FinishSafety}, ai.ErrAbnormalFinish},
		{"JSON을 요구했는데 글로 온 답", jsonRequest, fake.Step{Text: "네."}, ai.ErrInvalidJSON},
		{"거절된 요청", request("하나"), fake.Fail(ai.ErrBlocked), ai.ErrBlocked},
		{"후보가 없는 답", request("하나"), fake.Fail(ai.ErrNoCandidate), ai.ErrNoCandidate},
		{"공급자의 일시적인 실패", request("하나"), fake.Fail(ai.StatusError(429, ai.Detail{})), ai.ErrProvider},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(tt.step)

			resp, err := llm.Generate(context.Background(), tt.req)

			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, ai.Response{}, resp)
		})
	}

	t.Run("답의 모양에서 나온 실패에는 지시문과 모델이 남는다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{FinishReason: ai.FinishMaxTokens, Usage: ai.Usage{ReasoningTokens: 400}})

		_, err := llm.Generate(context.Background(), request("하나"))

		var detailed *ai.Error
		require.ErrorAs(t, err, &detailed)
		assert.Equal(t, "conversation", detailed.Detail.Task)
		assert.Equal(t, "fake-model", detailed.Detail.Model)
		assert.Equal(t, 400, detailed.Detail.Usage.ReasoningTokens)
	})

	t.Run("JSON을 요구했고 JSON이 오면 통과한다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply(`{"ok":true}`))

		resp, err := llm.Generate(context.Background(), jsonRequest)

		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true}`, resp.Text)
	})
}

func TestLLM_Requests(t *testing.T) {
	t.Run("받은 요청을 순서대로 기록한다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("답") })

		for _, text := range []string{"하나", "둘", "셋"} {
			_, err := llm.Generate(context.Background(), request(text))
			require.NoError(t, err)
		}

		got := llm.Requests()
		require.Len(t, got, 3)
		assert.Equal(t, request("하나"), got[0])
		assert.Equal(t, request("셋"), got[2])

		last, ok := llm.LastRequest()
		require.True(t, ok)
		assert.Equal(t, "짧게 답한다.", last.System)
		assert.Equal(t, "셋", last.Messages[0].Text)
	})

	t.Run("아무것도 받지 않았으면 마지막 요청이 없다", func(t *testing.T) {
		_, ok := fake.New("fake-model").LastRequest()
		assert.False(t, ok)
	})

	t.Run("부른 쪽이 요청을 나중에 고쳐도 기록은 그대로다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply(`{}`))
		req := request("처음 한 말")
		req.JSONSchema = json.RawMessage(`{"a":1}`)

		_, err := llm.Generate(context.Background(), req)
		require.NoError(t, err)
		req.Messages[0].Text = "바뀐 말"
		req.JSONSchema[2] = 'b'

		got := llm.Requests()[0]
		assert.Equal(t, "처음 한 말", got.Messages[0].Text)
		assert.JSONEq(t, `{"a":1}`, string(got.JSONSchema))
	})

	t.Run("돌려받은 기록을 고쳐도 다음에 받는 기록은 그대로다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("답"))
		_, err := llm.Generate(context.Background(), request("처음 한 말"))
		require.NoError(t, err)

		llm.Requests()[0].Messages[0].Text = "바뀐 말"

		assert.Equal(t, "처음 한 말", llm.Requests()[0].Messages[0].Text)
	})

	t.Run("잘못된 요청은 기록하되 대본을 쓰지 않고 거절한다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("답"))
		bad := request("하나")
		bad.Messages[0].Role = "system"

		_, err := llm.Generate(context.Background(), bad)

		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.Equal(t, 1, llm.Calls())
		assert.Len(t, llm.Requests(), 1)
		assert.Equal(t, 1, llm.Pending())
	})

	t.Run("기록 한도를 두면 가장 최근 것만 남고 횟수는 모두 센다", func(t *testing.T) {
		llm := fake.New("fake-model", fake.WithRecordLimit(2))
		llm.SetHandler(func(context.Context, ai.Request) fake.Step { return fake.Reply("답") })

		for _, text := range []string{"하나", "둘", "셋"} {
			_, err := llm.Generate(context.Background(), request(text))
			require.NoError(t, err)
		}

		got := llm.Requests()
		require.Len(t, got, 2)
		assert.Equal(t, "둘", got[0].Messages[0].Text)
		assert.Equal(t, "셋", got[1].Messages[0].Text)
		assert.Equal(t, 3, llm.Calls())
	})

	t.Run("기록 한도가 0이면 요청을 남기지 않는다", func(t *testing.T) {
		llm := fake.New("fake-model", fake.WithRecordLimit(0))
		llm.Enqueue(fake.Reply("답"))

		_, err := llm.Generate(context.Background(), request("하나"))
		require.NoError(t, err)

		assert.Empty(t, llm.Requests())
		assert.Equal(t, 1, llm.Calls())
	})
}

func TestLLM_Latency(t *testing.T) {
	t.Run("정해 둔 시간이 지나야 답한다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(fake.Step{Text: "답", Latency: 5 * time.Second})

			done := make(chan struct{})
			var resp ai.Response
			var err error
			go func() {
				defer close(done)
				resp, err = llm.Generate(context.Background(), request("하나"))
			}()

			time.Sleep(5*time.Second - time.Nanosecond)
			synctest.Wait()
			select {
			case <-done:
				require.Fail(t, "너무 일찍 답했다")
			default:
			}

			time.Sleep(time.Nanosecond)
			synctest.Wait()
			select {
			case <-done:
			default:
				require.Fail(t, "제때 답하지 않았다")
			}
			require.NoError(t, err)
			assert.Equal(t, "답", resp.Text)
			assert.Zero(t, llm.Interrupted())
		})
	})

	t.Run("기다리는 동안 취소되면 바로 돌아온다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(fake.Step{Text: "답", Latency: time.Minute})
			ctx, cancel := context.WithCancel(context.Background())

			done := make(chan error, 1)
			go func() {
				_, err := llm.Generate(ctx, request("하나"))
				done <- err
			}()
			time.Sleep(time.Second)
			cancel()

			err := <-done
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1, llm.Interrupted())
		})
	})

	t.Run("기다리는 동안 기한이 지나면 시간 초과다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(fake.Step{Text: "답", Latency: time.Minute})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := llm.Generate(ctx, request("하나"))

			require.ErrorIs(t, err, ai.ErrTimeout)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, 1, llm.Interrupted())
		})
	})

	t.Run("지연이 없어도 이미 끝난 컨텍스트에는 답하지 않는다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("답"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := llm.Generate(ctx, request("하나"))

		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("Handler는 컨텍스트를 받아 스스로 기다릴 수 있다", func(t *testing.T) {
		llm := fake.New("fake-model")
		release := make(chan struct{})
		llm.SetHandler(func(ctx context.Context, _ ai.Request) fake.Step {
			select {
			case <-release:
				return fake.Reply("풀려난 뒤의 답")
			case <-ctx.Done():
				return fake.Fail(ctx.Err())
			}
		})

		close(release)
		resp, err := llm.Generate(context.Background(), request("하나"))

		require.NoError(t, err)
		assert.Equal(t, "풀려난 뒤의 답", resp.Text)
	})
}

func TestLLM_GenerateStream(t *testing.T) {
	collect := func(deltas *[]string) ai.DeltaFunc {
		return func(delta string) error {
			*deltas = append(*deltas, delta)
			return nil
		}
	}

	t.Run("조각을 순서대로 넘기고 이은 글을 답으로 돌려준다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{Chunks: []string{"오늘 ", "하루 ", "어땠어요?"}})

		var deltas []string
		resp, err := llm.GenerateStream(context.Background(), request("하나"), collect(&deltas))

		require.NoError(t, err)
		assert.Equal(t, []string{"오늘 ", "하루 ", "어땠어요?"}, deltas)
		assert.Equal(t, "오늘 하루 어땠어요?", resp.Text)
		assert.Equal(t, ai.FinishStop, resp.FinishReason)
	})

	t.Run("조각을 정하지 않으면 답 전체를 한 조각으로 넘긴다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("오늘 하루 어땠어요?"))

		var deltas []string
		resp, err := llm.GenerateStream(context.Background(), request("하나"), collect(&deltas))

		require.NoError(t, err)
		assert.Equal(t, []string{"오늘 하루 어땠어요?"}, deltas)
		assert.Equal(t, "오늘 하루 어땠어요?", resp.Text)
	})

	t.Run("같은 대본을 스트리밍 없이 부르면 이은 글이 온다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{Chunks: []string{"오늘 ", "어땠어요?"}})

		resp, err := llm.Generate(context.Background(), request("하나"))

		require.NoError(t, err)
		assert.Equal(t, "오늘 어땠어요?", resp.Text)
	})

	t.Run("첫 조각은 지연 뒤에, 나머지는 간격을 두고 나온다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(fake.Step{
				Chunks:        []string{"하나", "둘", "셋"},
				Latency:       2 * time.Second,
				ChunkInterval: 500 * time.Millisecond,
			})

			var mu sync.Mutex
			var deltas []string
			received := func() int {
				mu.Lock()
				defer mu.Unlock()
				return len(deltas)
			}
			done := make(chan error, 1)
			go func() {
				_, err := llm.GenerateStream(context.Background(), request("하나"), func(delta string) error {
					mu.Lock()
					defer mu.Unlock()
					deltas = append(deltas, delta)
					return nil
				})
				done <- err
			}()

			time.Sleep(2*time.Second - time.Nanosecond)
			synctest.Wait()
			assert.Zero(t, received())

			time.Sleep(time.Nanosecond)
			synctest.Wait()
			assert.Equal(t, 1, received())

			time.Sleep(500 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, 2, received())

			time.Sleep(500 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, 3, received())
			require.NoError(t, <-done)
		})
	})

	t.Run("조각을 넘긴 뒤에 실패하는 흐름을 흉내 낸다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{Chunks: []string{"오늘 ", "많이"}, Err: ai.ErrProvider})

		var deltas []string
		resp, err := llm.GenerateStream(context.Background(), request("하나"), collect(&deltas))

		require.ErrorIs(t, err, ai.ErrProvider)
		assert.Equal(t, []string{"오늘 ", "많이"}, deltas)
		assert.Equal(t, ai.Response{}, resp)
	})

	t.Run("조각을 넘긴 뒤에 잘린 것으로 끝나는 흐름을 흉내 낸다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{Chunks: []string{"오늘 ", "많이"}, FinishReason: ai.FinishMaxTokens})

		var deltas []string
		_, err := llm.GenerateStream(context.Background(), request("하나"), collect(&deltas))

		require.ErrorIs(t, err, ai.ErrTruncated)
		assert.Len(t, deltas, 2)
	})

	t.Run("받는 쪽이 오류를 돌려주면 거기서 멈춘다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Step{Chunks: []string{"하나", "둘", "셋"}})
		stop := errors.New("stop")

		var deltas []string
		_, err := llm.GenerateStream(context.Background(), request("하나"), func(delta string) error {
			deltas = append(deltas, delta)
			if len(deltas) == 2 {
				return stop
			}
			return nil
		})

		require.ErrorIs(t, err, stop)
		assert.Equal(t, []string{"하나", "둘"}, deltas)
	})

	t.Run("조각 사이에 취소되면 남은 조각을 넘기지 않는다", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(fake.Step{Chunks: []string{"하나", "둘", "셋"}, ChunkInterval: time.Second})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var deltas []string
			_, err := llm.GenerateStream(ctx, request("하나"), func(delta string) error {
				deltas = append(deltas, delta)
				if len(deltas) == 2 {
					cancel()
				}
				return nil
			})

			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, []string{"하나", "둘"}, deltas)
			assert.Equal(t, 1, llm.Interrupted())
		})
	})

	t.Run("조각을 받을 함수가 없으면 거절한다", func(t *testing.T) {
		llm := fake.New("fake-model")
		llm.Enqueue(fake.Reply("답"))

		_, err := llm.GenerateStream(context.Background(), request("하나"), nil)

		require.ErrorIs(t, err, ai.ErrInvalidRequest)
		assert.Equal(t, 1, llm.Pending())
	})
}

func TestLLM_Concurrent(t *testing.T) {
	t.Run("여러 고루틴이 함께 불러도 대본을 한 번씩만 쓰고 요청을 모두 기록한다", func(t *testing.T) {
		const n = 50
		llm := fake.New("fake-model")
		for range n {
			llm.Enqueue(fake.Reply("답"))
		}

		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() {
				_, err := llm.Generate(context.Background(), request("하나"))
				errs <- err
			})
		}
		wg.Wait()
		close(errs)

		for err := range errs {
			require.NoError(t, err)
		}
		assert.Equal(t, n, llm.Calls())
		assert.Len(t, llm.Requests(), n)
		assert.Zero(t, llm.Pending())
	})
}

func TestErrorsNeverCarryRequestText(t *testing.T) {
	const spoken = "아무한테도 말 못 한 얘기야"

	tests := []struct {
		name     string
		step     fake.Step
		canceled bool
	}{
		{"빈 답", fake.Step{Text: ""}, false},
		{"사용자의 말을 되풀이하다 잘린 답", fake.Step{Text: spoken, FinishReason: ai.FinishMaxTokens}, false},
		{"사용자의 말을 되풀이하다 안전 필터에 걸린 답", fake.Step{Text: spoken, FinishReason: ai.FinishSafety}, false},
		{"취소된 호출", fake.Step{Text: "답", Latency: time.Minute}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := fake.New("fake-model")
			llm.Enqueue(tt.step)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			_, err := llm.Generate(ctx, request(spoken))

			require.Error(t, err)
			assert.NotContains(t, err.Error(), spoken)
		})
	}
}
