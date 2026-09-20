package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

func TestUserSettings(t *testing.T) {
	t.Parallel()

	t.Run("넘긴 항목만 바꾸고 나머지는 그대로 둔다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		ctx := t.Context()
		user := createUser(t, st, "mina@example.com")

		off := false
		later := baseTime.Add(time.Hour)
		updated, err := st.Queries().UpdateUserSettings(ctx, db.UpdateUserSettingsParams{
			UserID:          user.ID,
			AnalysisEnabled: &off,
			ReminderTime:    clock(21, 30),
			Now:             later,
		})
		require.NoError(t, err)

		assert.False(t, updated.AnalysisEnabled)
		assert.Equal(t, clock(21, 30), updated.ReminderTime)
		assertInstant(t, later, updated.UpdatedAt)
		assert.True(t, updated.ReminderEnabled)
		assert.Equal(t, "voice", updated.DefaultMode)
		assert.True(t, updated.MemoryEnabled)
		assert.False(t, updated.MoodPickEnabled)

		read, err := st.Queries().GetUserSettings(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, updated, read)
	})

	t.Run("모든 항목을 한 번에 바꿀 수 있다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")

		on, off, chat := true, false, "chat"
		updated, err := st.Queries().UpdateUserSettings(t.Context(), db.UpdateUserSettingsParams{
			UserID:          user.ID,
			ReminderEnabled: &off,
			ReminderTime:    clock(7, 0),
			DefaultMode:     &chat,
			AnalysisEnabled: &off,
			MemoryEnabled:   &off,
			MoodPickEnabled: &on,
			Now:             baseTime,
		})
		require.NoError(t, err)
		assert.False(t, updated.ReminderEnabled)
		assert.Equal(t, clock(7, 0), updated.ReminderTime)
		assert.Equal(t, "chat", updated.DefaultMode)
		assert.False(t, updated.AnalysisEnabled)
		assert.False(t, updated.MemoryEnabled)
		assert.True(t, updated.MoodPickEnabled)
	})

	t.Run("정해지지 않은 대화 방식은 받지 않는다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)
		user := createUser(t, st, "mina@example.com")

		video := "video"
		_, err := st.Queries().UpdateUserSettings(t.Context(), db.UpdateUserSettingsParams{
			UserID: user.ID, DefaultMode: &video, Now: baseTime,
		})
		requireViolation(t, err, sqlStateCheckViolation, "user_settings_default_mode_check")
	})

	t.Run("없는 사용자의 설정은 ErrNotFound다", func(t *testing.T) {
		t.Parallel()
		st, _ := newStore(t)

		_, err := st.Queries().GetUserSettings(t.Context(), newID(t))
		require.ErrorIs(t, err, store.ErrNotFound)

		_, err = st.Queries().UpdateUserSettings(t.Context(), db.UpdateUserSettingsParams{UserID: newID(t), Now: baseTime})
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}
