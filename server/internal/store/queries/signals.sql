-- name: ListSignalRowsByUser :many
-- 그 사용자의 신호 판단 전부다. 글(근거)은 읽지 않고 계산에 쓰는 값만 읽는다.
-- 대화 도중에 그 사람의 최근 상태를 읽을 때 쓴다. 단계는 첫 대화 날부터 다시 돌려 구하므로 기간을 자르지 않는다.
-- 신호 행은 대화 하나의 분석이 끝났을 때 여덟 항목이 한꺼번에 들어온다. 분석이 끝나지 않은 대화의 행은 여기에 없다.
SELECT d.record_date,
       s.conversation_id,
       s.item,
       s.status,
       s.explicitness,
       (s.cancelled_at IS NOT NULL)::boolean AS cancelled
FROM signals AS s
JOIN days AS d ON d.id = s.day_id
WHERE s.user_id = sqlc.arg(user_id)
ORDER BY d.record_date, s.conversation_id, s.item;
