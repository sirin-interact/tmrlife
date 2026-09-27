-- name: ListSignalRowsByUser :many
-- 그 사용자의 신호 판단 전부다. 글(근거)은 읽지 않고 계산에 쓰는 값만 읽는다.
-- 대화 도중에 그 사람의 최근 상태를 읽을 때와, 첫 대화 날부터 다시 돌리는 계산(개입 단계)에 쓴다.
-- 단계는 첫 대화 날부터 다시 돌려 구하므로 기간을 자르지 않는다.
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

-- name: ListSignalExtractors :many
-- 그 사용자의 신호 행이 어떤 추출기에서 나왔는지다. 내부 확인 화면이 읽는다.
--
-- 이 값을 화면에 띄우는 까닭: 키 없이 띄운 서버는 낱말 표로 답하는 모델로도 여덟 항목의 행을 채운다.
-- 저장된 판단만 보고는 실제 모델이 읽은 것과 구별할 수 없고, 화면에도 아무 표시가 없다.
-- 행에 남은 이 표시가 둘을 가르는 유일한 자리다.
-- 판단이 아니라 어디서 나왔는지를 세는 것이라 취소된 행도 함께 센다.
SELECT s.extractor_version,
       count(*)::bigint AS row_count,
       max(s.created_at)::timestamptz AS last_at
FROM signals AS s
WHERE s.user_id = sqlc.arg(user_id)
GROUP BY s.extractor_version
ORDER BY max(s.created_at) DESC, s.extractor_version;

-- name: ListSignalRowsByDateRange :many
-- 기간 안(from_date와 to_date를 모두 포함)의 신호 판단이다. 추세 화면이 읽는다.
--
-- 취소된 행도 돌려준다. 계산 코어가 취소된 행을 스스로 빼기 때문에, 화면에 보여줄 것과 계산에 넣을 것을
-- 한 번 읽어 나눠 쓸 수 있다. 걸러서 돌려주면 취소한 판단을 취소된 것으로 보여줄 방법이 없어진다.
-- 근거 글의 암호문도 함께 온다. 점만 그리는 추세 화면은 읽지 않고 두면 되고, 기간의 근거를 훑는 쪽은 여기서 읽는다.
-- 근거가 어느 발화에서 왔는지까지 필요하면 ListSignalsByDate로 하루씩 읽는다.
SELECT s.id,
       d.record_date,
       s.conversation_id,
       s.item,
       s.status,
       s.explicitness,
       s.evidence_enc,
       (s.cancelled_at IS NOT NULL)::boolean AS cancelled
FROM signals AS s
JOIN days AS d ON d.id = s.day_id
WHERE s.user_id = sqlc.arg(user_id)
  AND d.user_id = sqlc.arg(user_id)
  AND d.record_date >= sqlc.arg(from_date)
  AND d.record_date <= sqlc.arg(to_date)
ORDER BY d.record_date, s.conversation_id, s.item;

-- name: ListSignalsByDate :many
-- 하루의 신호 판단 전부와, 근거가 된 발화다. 근거 화면이 읽는다.
-- 하루에 대화를 여러 번 했으면 같은 항목의 행이 대화마다 하나씩 있다. 하루의 판단으로 합치는 일은 계산 쪽이 한다.
--
-- 발화는 왼쪽 조인이다. 발화가 지워지면 가리키는 값만 비워지고(ON DELETE SET NULL) 신호 행은 남으므로,
-- 근거 글은 있는데 어느 발화에서 왔는지는 모르는 행이 생길 수 있다.
SELECT s.id,
       s.conversation_id,
       s.item,
       s.status,
       s.explicitness,
       s.evidence_enc,
       (s.cancelled_at IS NOT NULL)::boolean AS cancelled,
       s.created_at,
       u.id AS utterance_id,
       u.seq AS utterance_seq,
       u.text_enc AS utterance_text_enc,
       u.created_at AS utterance_created_at
FROM signals AS s
JOIN days AS d ON d.id = s.day_id
LEFT JOIN utterances AS u ON u.id = s.evidence_utterance_id
WHERE s.user_id = sqlc.arg(user_id)
  AND d.user_id = sqlc.arg(user_id)
  AND d.record_date = sqlc.arg(record_date)
ORDER BY s.conversation_id, s.item;

-- name: InsertSignal :one
-- 신호 행 하나다. 직접 부르지 않고 store.SaveConversationSignals를 쓴다.
-- 여덟 항목은 한 트랜잭션에서 함께 들어가야 한다. 낱개로 넣으면 일부 항목만 든 하루가 계산에 들어가서,
-- 그날 관찰되지 않은 항목과 아직 뽑히지 않은 항목이 구별되지 않는다.
INSERT INTO signals (
    id, user_id, day_id, conversation_id, item, status, explicitness,
    evidence_enc, evidence_utterance_id, extractor_version, created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    sqlc.arg(day_id),
    sqlc.arg(conversation_id),
    sqlc.arg(item),
    sqlc.arg(status),
    sqlc.arg(explicitness),
    sqlc.arg(evidence_enc),
    sqlc.arg(evidence_utterance_id),
    sqlc.arg(extractor_version),
    sqlc.arg(now)
)
RETURNING *;

-- name: CancelSignal :one
-- 사용자가 "이건 아니에요"로 신호 하나를 취소한다. 행은 남고 계산에서만 빠진다.
-- 이미 취소한 행을 다시 취소해도 처음 취소한 시각을 그대로 두고 성공한다. 같은 버튼을 두 번 눌러도 결과가 같다.
-- 남의 행은 ID를 알아도 취소할 수 없고, 그때는 없는 것과 똑같이 찾지 못함이 된다.
-- 그 행이 매달린 기록 날짜를 돌려준다. 취소하면 그날의 판단이 달라지므로 부르는 쪽이 그날을 다시 읽는다.
UPDATE signals AS s
SET cancelled_at = COALESCE(s.cancelled_at, sqlc.arg(now)::timestamptz)
WHERE s.id = sqlc.arg(id)
  AND s.user_id = sqlc.arg(user_id)
RETURNING (SELECT d.record_date FROM days AS d WHERE d.id = s.day_id) AS record_date;

-- name: UncancelSignal :one
-- 취소를 되돌린다. 취소한 적이 없는 행에 불러도 성공한다.
UPDATE signals AS s
SET cancelled_at = NULL
WHERE s.id = sqlc.arg(id)
  AND s.user_id = sqlc.arg(user_id)
RETURNING (SELECT d.record_date FROM days AS d WHERE d.id = s.day_id) AS record_date;
