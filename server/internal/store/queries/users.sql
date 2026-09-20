-- name: CreateUser :one
-- role은 넣지 않는다. 가입으로는 일반 사용자만 만들어진다.
INSERT INTO users (id, email, password_hash, display_name, timezone, is_demo, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(email),
    sqlc.narg(password_hash),
    sqlc.narg(display_name),
    sqlc.arg(timezone),
    sqlc.arg(is_demo),
    sqlc.arg(now),
    sqlc.arg(now)
)
RETURNING *;

-- name: CreateUserKey :exec
INSERT INTO user_keys (user_id, wrapped_dek, kek_version, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(wrapped_dek), sqlc.arg(kek_version), sqlc.arg(now));

-- name: GetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
-- email이 citext라 대소문자를 가리지 않고 찾는다.
SELECT * FROM users WHERE email = sqlc.arg(email);

-- name: UpdateUserPasswordHash :execrows
-- 로그인에 성공한 김에, 옛 설정으로 만든 해시를 지금 설정으로 다시 만들어 바꿔 넣을 때 쓴다.
-- 읽었던 해시가 그대로일 때만 바꾼다. 그 사이에 비밀번호가 바뀌었다면 새 비밀번호를 옛 비밀번호로 되돌리게 된다.
UPDATE users
SET password_hash = sqlc.arg(new_hash)::text,
    updated_at    = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND password_hash = sqlc.arg(old_hash)::text;

-- name: GetUserKey :one
SELECT * FROM user_keys WHERE user_id = sqlc.arg(user_id);

-- name: DeleteUser :one
-- 외래 키의 ON DELETE CASCADE가 그 사용자의 모든 기록을 함께 지운다.
-- 지운 행을 돌려받아, 없는 사용자를 지우려 한 경우를 찾지 못함 오류로 구분한다.
DELETE FROM users WHERE id = sqlc.arg(id)
RETURNING id;
