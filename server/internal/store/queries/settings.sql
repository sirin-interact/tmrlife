-- name: CreateUserSettings :one
-- 값은 모두 테이블의 기본값으로 시작한다.
INSERT INTO user_settings (user_id, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(now))
RETURNING *;

-- name: GetUserSettings :one
SELECT * FROM user_settings WHERE user_id = sqlc.arg(user_id);

-- name: UpdateUserSettings :one
-- 넘기지 않은(NULL) 항목은 그대로 둔다. 설정 화면은 한 번에 하나씩 바꾼다.
UPDATE user_settings
SET reminder_enabled  = COALESCE(sqlc.narg(reminder_enabled), reminder_enabled),
    reminder_time     = COALESCE(sqlc.narg(reminder_time), reminder_time),
    default_mode      = COALESCE(sqlc.narg(default_mode), default_mode),
    analysis_enabled  = COALESCE(sqlc.narg(analysis_enabled), analysis_enabled),
    memory_enabled    = COALESCE(sqlc.narg(memory_enabled), memory_enabled),
    mood_pick_enabled = COALESCE(sqlc.narg(mood_pick_enabled), mood_pick_enabled),
    updated_at        = sqlc.arg(now)
WHERE user_id = sqlc.arg(user_id)
RETURNING *;
