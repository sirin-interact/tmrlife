-- +goose Up
-- 대화 하나에서 마음 신호를 뽑는 일이 어디까지 왔는지다. 컬럼과 제약의 규칙은 앞선 스키마와 같다.
--
-- 일기 초안의 진행 상태(processing_status)와 따로 둔다. 두 작업은 대화가 끝난 뒤에 나란히 돌고 서로를 기다리지 않는다.
-- 한 컬럼을 함께 쓰면 초안 작업이 done을 적은 뒤에 추출 작업이 running을 적는 일이 생기는데,
-- 그러면 초안 작업이 "아직 다루지 않은 대화"로 다시 읽어 같은 대화의 내용을 일기에 두 번 넣는다.
-- 반대로 추출이 먼저 done을 적으면 초안 작업이 그 대화를 재료에서 빼 버린다.
--
--   none: 뽑을 것이 없거나 분석을 꺼 둔 대화다. pending: 작업을 등록했다. running: 작업자가 맡았다.
--   done: 여덟 항목의 신호 행이 모두 들어갔다. failed: 다 시도하고도 뽑지 못했다.
--
-- done은 신호 행 여덟 개와 같은 트랜잭션에서만 적힌다(store.SaveConversationSignals).
-- 그래서 "분석이 끝난 대화"는 언제나 여덟 항목이 갖춰진 대화다. 계산 코어는 일부 항목만 본 하루를 받지 않는다.
ALTER TABLE conversations
    ADD COLUMN analysis_status text NOT NULL DEFAULT 'none',
    ADD CONSTRAINT conversations_analysis_status_check
        CHECK (analysis_status IN ('none', 'pending', 'running', 'done', 'failed'));

-- 근거 화면이 "그날 분석이 끝난 대화가 있는가"를 하루마다 묻는다. 대부분의 행은 done이 아니거나 오래된 날이라
-- 부분 인덱스로 작게 유지한다. 하루를 지우면 그날의 대화가 함께 지워지므로 이 인덱스도 따라 비워진다.
CREATE INDEX conversations_day_id_analysed_idx ON conversations (day_id) WHERE analysis_status = 'done';

-- 추세 화면과 근거 화면은 기간으로 신호 행을 읽는다. 그때 하루를 먼저 좁히고(days의 유일 키)
-- 그 하루의 신호 행을 항목 순서로 읽는다. 기존 인덱스(day_id만)로는 정렬이 남는다.
CREATE INDEX signals_day_id_conversation_id_item_idx ON signals (day_id, conversation_id, item);
DROP INDEX signals_day_id_idx;

-- +goose Down
CREATE INDEX signals_day_id_idx ON signals (day_id);
DROP INDEX signals_day_id_conversation_id_item_idx;
DROP INDEX conversations_day_id_analysed_idx;
ALTER TABLE conversations
    DROP CONSTRAINT conversations_analysis_status_check,
    DROP COLUMN analysis_status;
