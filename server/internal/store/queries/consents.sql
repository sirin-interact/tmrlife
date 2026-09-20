-- name: GrantConsent :one
-- 동의 이력은 쌓기만 한다. 철회했던 동의에 다시 동의하면 앞선 행을 되살리지 않고 새 행을 더한다.
-- 그래야 언제 동의했고 언제 철회했는지가 그대로 남는다.
-- 같은 판에 유효한 동의가 이미 있으면 그 행을 그대로 돌려준다. 처음 동의한 시각이 바뀌지 않고 행도 늘지 않아,
-- 같은 요청이 되풀이되어도 결과가 같다.
-- DO NOTHING은 이미 있는 행을 돌려주지 않는다. 값을 바꾸지 않는 갱신을 걸어 두어야 어느 경우에도 행이 나온다.
INSERT INTO consents (id, user_id, kind, version, granted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(version), sqlc.arg(now))
ON CONFLICT (user_id, kind, version) WHERE withdrawn_at IS NULL DO UPDATE
SET version = EXCLUDED.version
RETURNING *;

-- name: ListActiveConsents :many
SELECT * FROM consents
WHERE user_id = sqlc.arg(user_id)
  AND withdrawn_at IS NULL
ORDER BY kind, version;

-- name: ListConsentHistory :many
-- 철회한 것까지 모두, 동의한 순서대로 돌려준다.
SELECT * FROM consents
WHERE user_id = sqlc.arg(user_id)
ORDER BY granted_at, id;

-- name: WithdrawConsent :execrows
-- 판을 가리지 않고 그 종류의 유효한 동의를 모두 철회한다. 이미 철회한 행의 철회 시각은 건드리지 않는다.
UPDATE consents
SET withdrawn_at = sqlc.arg(now)::timestamptz
WHERE user_id = sqlc.arg(user_id)
  AND kind = sqlc.arg(kind)
  AND withdrawn_at IS NULL;
