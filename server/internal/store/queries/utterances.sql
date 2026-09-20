-- name: AppendUtteranceUnderLock :one
-- 대화의 다음 순번으로 발화를 더한다. 직접 부르지 않고 store.AppendUtterance를 쓴다.
-- 같은 트랜잭션에서 LockConversationForAppend를 먼저 불러야 순번이 겹치지 않는다.
-- 잠금 없이 불러도 순번이 겹친 발화가 저장되지는 않는다. (conversation_id, seq)의 유일 제약에 걸려 한쪽이 실패한다.
INSERT INTO utterances (
    id, conversation_id, user_id, seq, speaker, modality, origin, text_enc, stt_min_confidence, client_message_id, created_at
)
SELECT sqlc.arg(id)::uuid,
       sqlc.arg(conversation_id)::uuid,
       sqlc.arg(user_id)::uuid,
       COALESCE(MAX(u.seq) + 1, 0),
       sqlc.arg(speaker)::text,
       sqlc.arg(modality)::text,
       sqlc.arg(origin)::text,
       sqlc.arg(text_enc)::bytea,
       sqlc.narg(stt_min_confidence)::real,
       sqlc.narg(client_message_id)::uuid,
       sqlc.arg(now)::timestamptz
FROM utterances AS u
WHERE u.conversation_id = sqlc.arg(conversation_id)::uuid
RETURNING *;

-- name: GetUtteranceByClientMessageID :one
-- 같은 글을 다시 보낸 것인지 알아본다.
SELECT * FROM utterances
WHERE conversation_id = sqlc.arg(conversation_id)
  AND client_message_id = sqlc.arg(client_message_id)::uuid;

-- name: ListUtterancesByConversation :many
SELECT * FROM utterances
WHERE conversation_id = sqlc.arg(conversation_id)
  AND user_id = sqlc.arg(user_id)
ORDER BY seq;

-- name: ListLastUtterances :many
-- 가장 최근 발화부터 거꾸로 돌려준다. 위기 판별에 넘길 직전 몇 턴을 읽을 때 대화 전체를 읽지 않으려는 것이다.
SELECT * FROM utterances
WHERE conversation_id = sqlc.arg(conversation_id)
  AND user_id = sqlc.arg(user_id)
ORDER BY seq DESC
LIMIT sqlc.arg(max_rows);

-- name: ListUtterancesByDay :many
-- 그날의 모든 대화의 발화를 대화를 시작한 순서, 그 안에서는 순번대로 돌려준다. 일기 초안의 재료다.
SELECT u.*
FROM utterances AS u
JOIN conversations AS c ON c.id = u.conversation_id
WHERE c.day_id = sqlc.arg(day_id)
  AND c.user_id = sqlc.arg(user_id)
ORDER BY c.started_at, c.id, u.seq;
