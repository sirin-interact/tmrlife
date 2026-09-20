-- name: CreateConversation :one
-- 열린 대화는 사용자마다 하나뿐이다. 이미 있으면 유일 제약(conversations_user_id_active_key)에 걸린다.
INSERT INTO conversations (id, user_id, day_id, status, started_mode, started_at, created_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    sqlc.arg(day_id),
    'active',
    sqlc.arg(started_mode),
    sqlc.arg(now),
    sqlc.arg(now)
)
RETURNING *;

-- name: GetActiveConversation :one
-- 기록 날짜를 함께 돌려준다. 새벽의 경계를 넘긴 채 열려 있는 대화는 이어가지 않고 먼저 끝내야 하는데,
-- 그 판단은 대화가 매달린 하루의 기록 날짜와 지금의 기록 날짜를 견줘서 한다.
SELECT sqlc.embed(c), d.record_date
FROM conversations AS c
JOIN days AS d ON d.id = c.day_id
WHERE c.user_id = sqlc.arg(user_id)
  AND c.status = 'active';

-- name: GetConversation :one
SELECT sqlc.embed(c), d.record_date
FROM conversations AS c
JOIN days AS d ON d.id = c.day_id
WHERE c.id = sqlc.arg(id)
  AND c.user_id = sqlc.arg(user_id);

-- name: LockConversationForAppend :one
-- 발화를 더하기 전에 대화를 잡아 둔다. 같은 대화에 발화를 더하는 트랜잭션들이 여기서 한 줄로 선다.
-- 다음 순번은 이 잠금을 얻은 뒤의 새 문장에서 읽어야 한다. 한 문장 안에서 잠그고 읽으면,
-- 기다리기 전에 뜬 스냅샷으로 읽기 때문에 앞선 트랜잭션이 방금 넣은 발화를 보지 못하고 같은 순번을 고른다.
-- 키를 바꾸지 않는 잠금이라 이 대화에 관문 기록이나 신호가 매달리는 것은 막지 않는다.
SELECT id, status FROM conversations
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
FOR NO KEY UPDATE;

-- name: EndConversation :one
-- 열려 있는 대화만 끝낸다. 끝내기 버튼과 무응답 타이머가 겹쳐도 한쪽만 행을 돌려받고, 다른 쪽은 찾지 못함이 된다.
-- 일기 초안 작업은 행을 돌려받은 쪽만 같은 트랜잭션에서 등록한다. 그래야 작업이 두 번 등록되지 않는다.
--
-- quiet_since를 주면 그 시각 뒤로 발화가 하나도 없을 때만 끝낸다. 연결이 끊긴 대화를 한참 뒤에 닫을 때 쓴다.
-- 타이머는 연결이 끊긴 프로세스에 있는데, 사용자는 그 사이에 다른 프로세스로 다시 붙어 이야기를 이어갔을 수 있다.
UPDATE conversations AS c
SET status            = 'ended',
    ended_at          = sqlc.arg(now)::timestamptz,
    end_reason        = sqlc.arg(end_reason)::text,
    processing_status = sqlc.arg(processing_status)
WHERE c.id = sqlc.arg(id)
  AND c.user_id = sqlc.arg(user_id)
  AND c.status = 'active'
  AND (
      sqlc.narg(quiet_since)::timestamptz IS NULL
      OR NOT EXISTS (
          SELECT 1 FROM utterances AS u
          WHERE u.conversation_id = c.id
            AND u.created_at > sqlc.narg(quiet_since)::timestamptz
      )
  )
RETURNING *;

-- name: AdvanceConversationCheckState :one
-- 확인 상태는 앞으로만 간다(none, reflected, asked 순). 같은 값을 다시 적는 것은 받아 준다.
-- 뒤로 돌리려는 요청과 끝난 대화에 대한 요청은 찾지 못함이 된다. 직접 묻기는 한 대화에서 한 번뿐이어야 하므로,
-- 같은 대화를 두 연결이 함께 다루더라도 "직접 물었다"가 "되물었다"로 덮이지 않게 한다.
-- 'asked'로 옮기는 일은 ClaimDirectAsk만 한다. 여기서는 그 자리를 넘겨받지 않는다.
UPDATE conversations
SET check_state = sqlc.arg(check_state)::text
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'active'
  AND array_position(ARRAY['none', 'reflected', 'asked'], check_state)
      <= array_position(ARRAY['none', 'reflected', 'asked'], sqlc.arg(check_state)::text)
RETURNING *;

-- name: ClaimDirectAsk :one
-- 직접 묻기의 자리를 한 번만 내준다. 되물은 뒤('reflected')에서만 '물었다'로 옮겨지고, 옮긴 쪽만 행을 돌려받는다.
--
-- 단순히 앞으로만 가는 규칙으로는 모자란다. 두 연결이 같은 대화를 함께 다루면 둘 다 'reflected'를 읽고
-- 둘 다 직접 묻기로 가서, 사람이 같은 질문을 연달아 두 번 받는다. 여기서 진 쪽은 코어의 규칙대로
-- "이미 물었다"로 보고 대응 단계로 올라간다.
UPDATE conversations
SET check_state = 'asked'
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'active'
  AND check_state = 'reflected'
RETURNING *;

-- name: AdvanceConversationCrisisStage :one
-- 위기 대응의 고정 문구가 실제로 나간 단계를 적는다. 뒤로 돌리지 않고, 같은 값을 다시 적는 것은 받아 준다.
-- 말이 나간 트랜잭션 안에서만 부른다. 말이 나가기 전에 적으면, 판정만 남기고 끊긴 턴을 다시 보냈을 때
-- 고정 문구를 이미 말한 것으로 보고 건너뛴다.
UPDATE conversations
SET crisis_spoken_stage = sqlc.arg(crisis_spoken_stage)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'active'
  AND crisis_spoken_stage <= sqlc.arg(crisis_spoken_stage)
RETURNING *;

-- name: SetConversationProcessingStatus :execrows
-- 대화가 끝난 뒤에 도는 작업이 제 진행 상태를 적는다.
UPDATE conversations
SET processing_status = sqlc.arg(processing_status)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id);

-- name: ListConversationsByDay :many
-- 그날의 대화를 시작한 순서대로 돌려준다. crisis는 그 대화에 대응 단계 이상의 관문 기록이 있는지다.
-- 그런 대화에서는 일기 초안을 자동으로 만들지 않고, 끝날 때까지 도움 자원을 화면에 고정해 둔다.
SELECT sqlc.embed(c),
       EXISTS (
           SELECT 1 FROM gate_events AS g
           WHERE g.conversation_id = c.id
             AND g.final_stage >= 2
       )::boolean AS crisis
FROM conversations AS c
WHERE c.day_id = sqlc.arg(day_id)
  AND c.user_id = sqlc.arg(user_id)
ORDER BY c.started_at, c.id;

-- name: ListStaleActiveConversations :many
-- 시작한 지도, 마지막 발화가 있은 지도 idle_before보다 오래된 열린 대화다. 모든 사용자를 통틀어 찾는다.
-- 끊긴 연결을 기다리는 타이머는 프로세스의 메모리에 있어서 프로세스가 다시 뜨면 사라진다.
-- 주기 작업이 이 목록을 보고 EndConversation(quiet_since)으로 닫는다. 그 사이에 이어진 대화는 그 조건에서 걸러진다.
-- 열린 대화만 담는 부분 인덱스(conversations_user_id_active_key)를 읽으므로 끝난 대화가 쌓여도 느려지지 않는다.
SELECT c.id, c.user_id, c.day_id, c.started_at
FROM conversations AS c
WHERE c.status = 'active'
  AND c.started_at < sqlc.arg(idle_before)
  AND NOT EXISTS (
      SELECT 1 FROM utterances AS u
      WHERE u.conversation_id = c.id
        AND u.created_at >= sqlc.arg(idle_before)
  )
ORDER BY c.started_at, c.id
LIMIT sqlc.arg(max_rows);
