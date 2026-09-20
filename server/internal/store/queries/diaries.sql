-- name: CreateDiaryDraft :one
-- 그날의 첫 초안이다. 일기는 하루에 하나라서, 이미 있으면 유일 제약(diaries_day_id_key)에 걸린다.
-- 직접 부르지 않고 store.SaveDiaryDraft를 쓴다. 암호문에 행 ID가 묶이므로 있는 행인지 새 행인지를 먼저 알아야 잠글 수 있다.
INSERT INTO diaries (id, day_id, user_id, status, draft_enc, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(day_id),
    sqlc.arg(user_id),
    'draft',
    sqlc.arg(draft_enc)::bytea,
    sqlc.arg(now),
    sqlc.arg(now)
)
RETURNING *;

-- name: ReplaceDiaryDraft :one
-- 그날 다시 대화해서 새 초안이 생겼다. 상태가 draft로 돌아가 사용자가 다시 확인한다.
-- 사용자가 확인한 글(body_enc)과 확인한 시각은 건드리지 않는다.
UPDATE diaries
SET draft_enc  = sqlc.arg(draft_enc)::bytea,
    status     = 'draft',
    updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: CreateConfirmedDiary :one
-- 초안 없이 사용자가 직접 쓴 일기다. 초안을 자동으로 만들지 않은 날에도 일기는 쓸 수 있다.
INSERT INTO diaries (id, day_id, user_id, status, body_enc, confirmed_at, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(day_id),
    sqlc.arg(user_id),
    'confirmed',
    sqlc.arg(body_enc)::bytea,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz
)
RETURNING *;

-- name: ConfirmDiary :one
-- 글을 고치고 확인한다. 바뀌는 것은 일기 글뿐이고 신호는 그대로다.
-- 확인한 글이 초안을 대신하므로 초안은 비운다. 쓰이지 않는 사용자의 글을 남겨 두지 않는다.
UPDATE diaries
SET body_enc     = sqlc.arg(body_enc)::bytea,
    draft_enc    = NULL,
    status       = 'confirmed',
    confirmed_at = sqlc.arg(now)::timestamptz,
    updated_at   = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: GetDiaryByDayID :one
SELECT * FROM diaries
WHERE day_id = sqlc.arg(day_id)
  AND user_id = sqlc.arg(user_id);

-- name: GetDiaryByDate :one
SELECT dr.*
FROM diaries AS dr
JOIN days AS d ON d.id = dr.day_id
WHERE d.user_id = sqlc.arg(user_id)
  AND d.record_date = sqlc.arg(record_date);

-- name: ListDiariesByDateRange :many
-- from_date부터 to_date 앞까지의 일기를 날짜순으로 돌려준다. 한 달을 볼 때는 그 달 1일과 다음 달 1일을 준다.
SELECT sqlc.embed(dr), d.record_date
FROM diaries AS dr
JOIN days AS d ON d.id = dr.day_id
WHERE d.user_id = sqlc.arg(user_id)
  AND d.record_date >= sqlc.arg(from_date)
  AND d.record_date < sqlc.arg(to_date)
ORDER BY d.record_date;

-- name: ListDiariesByUser :many
-- 그 사용자의 일기 전부를 최근 날짜부터 돌려준다. 일기 글은 암호문이라 DB에서 찾을 수 없다.
-- 검색은 본인의 일기를 모두 읽어 풀어서 메모리에서 한다.
SELECT sqlc.embed(dr), d.record_date
FROM diaries AS dr
JOIN days AS d ON d.id = dr.day_id
WHERE d.user_id = sqlc.arg(user_id)
ORDER BY d.record_date DESC;
