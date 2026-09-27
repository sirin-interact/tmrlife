package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/ai/fake"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/engine"
	"github.com/sirin-interact/tmrlife/server/internal/store"
)

func TestNewConversationEngine(t *testing.T) {
	t.Parallel()

	t.Run("설정의 모델과 기한으로 대화 엔진을 만든다", func(t *testing.T) {
		t.Parallel()
		start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
		fakeClock := clock.NewFake(start)
		deps, err := New(t.Context(), testConfig(t, nil), slog.New(slog.DiscardHandler), NameServer, WithClock(fakeClock))
		require.NoError(t, err)
		t.Cleanup(deps.Close)

		conversation, err := deps.NewConversationEngine(fake.New("conversation"), fake.New("gate"), nil, nil)
		require.NoError(t, err)
		require.NotNil(t, conversation)

		// 엔진이 시계와 저장소를 제대로 받았는지는 대화를 하나 열어 보면 드러난다.
		user := createUser(t, deps, fakeClock.Now())
		var events []engine.Event
		session, err := conversation.Start(t.Context(), engine.StartInput{
			User: engine.Participant{ID: user, Timezone: "Asia/Seoul"},
			Mode: store.ModeChat,
			Sink: engine.SinkFunc(func(_ context.Context, e engine.Event) error {
				events = append(events, e)
				return nil
			}),
		})
		require.NoError(t, err)
		assert.Equal(t, "2026-09-20", session.RecordDate().String())
		require.Len(t, events, 2, "대화를 여는 사건과 첫 안부가 나간다")
	})

	t.Run("모델 없이는 만들지 않는다", func(t *testing.T) {
		t.Parallel()
		deps := newDeps(t, testConfig(t, nil), NameServer)
		_, err := deps.NewConversationEngine(nil, fake.New("gate"), nil, nil)
		require.Error(t, err)
	})
}

// createUser는 대화를 열 수 있는 사용자 하나를 만든다. 발화를 잠글 데이터 키도 함께 만든다.
func createUser(t *testing.T, deps *Deps, now time.Time) uuid.UUID {
	t.Helper()
	id, err := store.NewID()
	require.NoError(t, err)
	key, err := deps.KeyRing.NewUserKey(id)
	require.NoError(t, err)
	_, err = deps.Store.CreateUser(t.Context(), store.NewUser{
		ID: id, Email: "mina@example.com", Timezone: "Asia/Seoul",
		WrappedDEK: key.Wrapped, KEKVersion: int16(key.KEKVersion), Now: now,
	})
	require.NoError(t, err)
	return id
}

func TestNewConversationSweeper(t *testing.T) {
	t.Parallel()
	deps := newDeps(t, testConfig(t, nil), NameWorker)

	sweeper, err := deps.NewConversationSweeper(nil, nil)
	require.NoError(t, err)
	result, err := sweeper.Sweep(t.Context())
	require.NoError(t, err)
	assert.Equal(t, engine.SweepResult{}, result, "닫을 대화가 없으면 아무 일도 하지 않는다")
}
