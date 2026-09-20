-- name: InsertGateEvent :one
-- 발화 하나에 판정 하나다. 같은 발화의 판정을 두 번 넣으면 유일 제약(gate_events_utterance_id_key)에 걸린다.
-- 해당 없음(0단계)도 넣는다. 근거 발화는 확인 단계 이상일 때만 남길 수 있다.
INSERT INTO gate_events (
    id, user_id, conversation_id, utterance_id,
    rule_stage, ai_stage, final_stage, detected_by, adjustments,
    ai_failed, ai_latency_ms, evidence_enc, created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    sqlc.arg(conversation_id),
    sqlc.arg(utterance_id),
    sqlc.narg(rule_stage),
    sqlc.narg(ai_stage),
    sqlc.arg(final_stage),
    sqlc.arg(detected_by),
    sqlc.arg(adjustments),
    sqlc.arg(ai_failed),
    sqlc.narg(ai_latency_ms),
    sqlc.narg(evidence_enc),
    sqlc.arg(now)
)
RETURNING *;

-- name: ListFlaggedGateEventsSince :many
-- 관문에 걸린(확인 단계 이상) 지난 판정이다. "최근에 몇 번 있었나"와 "대응 단계가 있은 지 얼마 안 됐나"를 보는 규칙이 읽는다.
-- since는 판정 시각에서 그 규칙들이 보는 가장 긴 기간만큼 거슬러 올라간 시각이고, 경계의 판정도 담는다.
-- 어느 대화의 판정인지 함께 돌려준다. 쌓임은 발화가 아니라 대화를 단위로 센다.
-- 해당 없음인 판정은 그 규칙들이 세지 않으므로 읽지 않는다. 걸린 판정만 담는 부분 인덱스를 그대로 탄다.
SELECT conversation_id, final_stage, created_at
FROM gate_events
WHERE user_id = sqlc.arg(user_id)
  AND created_at >= sqlc.arg(since)
  AND final_stage >= 1
ORDER BY created_at, id;

-- name: ConversationHasCrisisGateEvent :one
-- 그 대화에 대응 단계 이상의 판정이 있었는지다. 있었다면 도움 자원을 화면에 고정해 두고, 일기 초안을 자동으로 만들지 않는다.
SELECT EXISTS (
    SELECT 1 FROM gate_events
    WHERE conversation_id = sqlc.arg(conversation_id)
      AND user_id = sqlc.arg(user_id)
      AND final_stage >= 2
)::boolean AS crisis;

-- name: GetGateEventByUtterance :one
-- 그 발화의 판정이 이미 남아 있는지 본다. 같은 글이 다시 들어왔을 때, 먼저 저장된 발화의 처리가 어디까지 갔는지 알아야 한다.
-- 발화만 저장하고 판정을 남기기 전에 프로세스가 죽었다면 찾지 못함이 되고, 그 발화로 관문부터 다시 돌린다.
SELECT * FROM gate_events
WHERE utterance_id = sqlc.arg(utterance_id)
  AND user_id = sqlc.arg(user_id);
