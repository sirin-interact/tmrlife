-- +goose Up
-- 대화를 실제로 주고받는 데 필요한 것을 더한다. 컬럼과 제약의 규칙은 앞선 스키마와 같다.

-- 애매한 표현이 나왔을 때의 확인은 두 걸음이다. 먼저 되묻고, 그래도 풀리지 않으면 한 번만 직접 묻는다.
-- 어디까지 왔는지를 대화에 적어 둔다. 연결이 끊겼다 이어지거나 서버가 다시 떠도 직접 묻기를 두 번 하지 않는다.
--   none: 아직 확인한 적이 없다. reflected: 되물었다. asked: 직접 물었다. 뒤로 돌아가지 않는다.
-- 화면에 도움 자원을 고정했는지는 따로 적지 않는다. 그 대화에 대응 단계 이상의 관문 기록이 있는지로 안다.
ALTER TABLE conversations
    ADD COLUMN check_state text NOT NULL DEFAULT 'none',
    ADD CONSTRAINT conversations_check_state_check CHECK (check_state IN ('none', 'reflected', 'asked'));

-- 열린 대화는 사용자마다 하나뿐이다. 열린 대화가 있으면 새로 만들지 않고 그 대화를 이어간다.
-- 두 기기에서 동시에 시작해도 한쪽만 만들어지고, 다른 쪽은 유일 제약에 걸려 이미 있는 대화를 받는다.
CREATE UNIQUE INDEX conversations_user_id_active_key ON conversations (user_id) WHERE status = 'active';

-- 클라이언트가 글을 보낼 때 붙이는 식별자다. 응답을 받지 못해 같은 글을 다시 보내도 발화가 두 번 쌓이지 않는다.
-- 두 번 쌓이면 위기 관문도 두 번 돌고, "최근 몇 번"을 세는 규칙이 부풀어 단계가 잘못 올라간다.
-- AI의 말에는 없다.
ALTER TABLE utterances
    ADD COLUMN client_message_id uuid,
    ADD CONSTRAINT utterances_client_message_id_check CHECK (client_message_id IS NULL OR speaker = 'user');

-- 식별자가 있는 발화만 담는다. AI의 말까지 담으면 인덱스가 두 배가 되는데 찾을 일이 없다.
CREATE UNIQUE INDEX utterances_conversation_id_client_message_id_key
    ON utterances (conversation_id, client_message_id)
    WHERE client_message_id IS NOT NULL;

-- 근거 발화는 관문에 걸린 발화(확인 단계 이상)에만 남긴다. 해당 없음으로 넘긴 말까지 따로 떠 두지 않는다.
ALTER TABLE gate_events
    ADD CONSTRAINT gate_events_evidence_check CHECK (evidence_enc IS NULL OR final_stage >= 1);

-- +goose Down
ALTER TABLE gate_events DROP CONSTRAINT gate_events_evidence_check;
DROP INDEX utterances_conversation_id_client_message_id_key;
ALTER TABLE utterances
    DROP CONSTRAINT utterances_client_message_id_check,
    DROP COLUMN client_message_id;
DROP INDEX conversations_user_id_active_key;
ALTER TABLE conversations
    DROP CONSTRAINT conversations_check_state_check,
    DROP COLUMN check_state;
