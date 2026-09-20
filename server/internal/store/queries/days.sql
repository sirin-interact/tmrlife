-- name: UpsertDay :one
-- 그날의 첫 대화가 행을 만들고, 뒤의 대화는 같은 행을 받는다.
-- DO NOTHING은 이미 있는 행을 돌려주지 않는다. 값을 바꾸지 않는 갱신을 걸어 두어야 어느 경우에도 id가 나오고,
-- 두 요청이 동시에 들어와도 한 행으로 모인다.
INSERT INTO days (id, user_id, record_date, created_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(record_date), sqlc.arg(now))
ON CONFLICT (user_id, record_date) DO UPDATE
SET record_date = EXCLUDED.record_date
RETURNING id;

-- name: DeleteDay :one
-- 그날의 대화, 발화, 위기 관문 기록, 일기, 신호, 그날에서 나온 기억, 기분 고르기가 외래 키를 따라 함께 지워진다.
-- user_id를 함께 받는다. 남의 하루는 ID를 알아도 지울 수 없고, 그때는 없는 것과 똑같이 찾지 못함이 된다.
DELETE FROM days
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
RETURNING id;

-- name: GetDayByDate :one
SELECT * FROM days
WHERE user_id = sqlc.arg(user_id)
  AND record_date = sqlc.arg(record_date);

-- name: LockDay :one
-- 그날의 일기를 쓰는 동안 하루를 잡아 둔다. 같은 날의 초안 작업 둘이 겹치거나 초안 작업과 사용자의 저장이 겹쳐도 차례로 돈다.
-- 하루를 지우는 요청과도 차례가 갈린다. 지워진 뒤라면 찾지 못함이 되고, 일기를 쓰던 쪽은 거기서 그만둔다.
-- 키를 바꾸지 않는 잠금이라 그날에 새 대화가 매달리는 것은 막지 않는다.
SELECT id FROM days
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
FOR NO KEY UPDATE;

-- name: DeleteDayByDate :one
-- DeleteDay와 같되 기록 날짜로 찾는다. 그날의 기록이 없으면 찾지 못함이 된다.
DELETE FROM days
WHERE user_id = sqlc.arg(user_id)
  AND record_date = sqlc.arg(record_date)
RETURNING id;
