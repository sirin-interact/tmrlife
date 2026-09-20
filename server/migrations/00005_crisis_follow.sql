-- +goose Up
-- 위기 대응의 고정 문구가 이 대화에서 어느 단계까지 실제로 나갔는지다. 0은 아직 한 번도 나가지 않았다는 뜻이다.
--
-- 이 값이 따로 필요한 까닭은 두 가지다.
-- 첫째, 같은 대화에서 최종 단계가 다시 대응 단계 이상이 되는 일은 흔하다(앞선 대응 판정이 있으면 뒤따르는
-- 확인 단계의 표현도 대응 단계로 올라간다). 그때마다 같은 고정 문구를 글자 그대로 다시 읽어 주면,
-- "전화는 하기 싫어"라고 답한 사람에게 같은 번호를 다시 읽어 주는 꼴이 된다. 고정 문구는 그 단계의 첫 응답에만 쓰고,
-- 그 뒤로는 위기 뒤 대화 지시문으로 이어간다.
-- 둘째, 관문 기록(gate_events)의 최종 단계로는 이 값을 대신할 수 없다. 판정만 남기고 답이 나가기 전에 끊긴 턴이
-- 있기 때문이다. 그 글을 다시 보내면 고정 문구가 그때 처음으로 나가야 한다.
-- 그래서 말이 실제로 나간 트랜잭션 안에서만 올린다. 뒤로 돌아가지 않는다.
ALTER TABLE conversations
    ADD COLUMN crisis_spoken_stage smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT conversations_crisis_spoken_stage_check
        CHECK (crisis_spoken_stage BETWEEN 0 AND 3);

-- +goose Down
ALTER TABLE conversations
    DROP CONSTRAINT conversations_crisis_spoken_stage_check,
    DROP COLUMN crisis_spoken_stage;
