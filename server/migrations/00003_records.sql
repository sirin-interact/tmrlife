-- +goose Up
-- 하루 단위로 쌓이는 기록이다. 기본 키, 시각, 열거 값, 암호화 컬럼, 삭제, 소유자 확인의 규칙은 앞선 계정 스키마와 같다.
--
-- 지우는 범위는 이렇게 보장된다.
-- - days의 한 행을 지우면 그날의 대화, 발화, 위기 관문 기록, 일기, 신호, 그날에서 나온 기억, 기분 고르기가 함께 지워진다.
-- - users의 한 행을 지우면 그 사용자의 모든 것이 지워진다.
-- - 자가 검진은 하루에 매달리지 않는다. 대화하지 않은 날에도 할 수 있고, 하루의 일기를 지워도 남는다.

-- 기록 날짜 하나에 남긴 모든 것의 부모다.
-- 기록 날짜는 달력 날짜가 아니다. 새벽의 경계 시각과 사용자 시간대로 애플리케이션이 정한 값이며,
-- 자정을 넘겨 한 대화는 전날의 기록이 된다. DB는 시각에서 이 값을 계산하지 않는다.
CREATE TABLE days (
    id          uuid        NOT NULL,
    user_id     uuid        NOT NULL,
    record_date date        NOT NULL,
    created_at  timestamptz NOT NULL,
    CONSTRAINT days_pkey PRIMARY KEY (id),
    CONSTRAINT days_user_id_record_date_key UNIQUE (user_id, record_date),
    -- 자식 테이블의 복합 외래 키가 가리키는 자리다.
    CONSTRAINT days_id_user_id_key UNIQUE (id, user_id),
    CONSTRAINT days_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

-- 하루에 여러 번 대화할 수 있다. 일기는 하루에 하나이고 뒤의 대화는 그날 일기에 이어진다.
CREATE TABLE conversations (
    id                uuid        NOT NULL,
    user_id           uuid        NOT NULL,
    day_id            uuid        NOT NULL,
    status            text        NOT NULL,
    -- 대화 도중에 음성과 채팅을 오갈 수 있어 시작할 때의 방식만 남긴다. 발화마다의 방식은 utterances.modality에 있다.
    started_mode      text        NOT NULL,
    started_at        timestamptz NOT NULL,
    ended_at          timestamptz,
    end_reason        text,
    -- 대화가 끝난 뒤에 도는 작업(일기 초안, 신호 추출, 기억 추출)의 진행 상태다.
    processing_status text        NOT NULL DEFAULT 'none',
    created_at        timestamptz NOT NULL,
    CONSTRAINT conversations_pkey PRIMARY KEY (id),
    CONSTRAINT conversations_id_user_id_key UNIQUE (id, user_id),
    CONSTRAINT conversations_id_day_id_key UNIQUE (id, day_id),
    CONSTRAINT conversations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT conversations_day_id_user_id_fkey FOREIGN KEY (day_id, user_id) REFERENCES days (id, user_id) ON DELETE CASCADE,
    CONSTRAINT conversations_status_check CHECK (status IN ('active', 'ended', 'abandoned')),
    CONSTRAINT conversations_started_mode_check CHECK (started_mode IN ('voice', 'chat')),
    -- idle은 한동안 말이 없어 초안을 저장해 두고 닫은 경우다.
    CONSTRAINT conversations_end_reason_check CHECK (end_reason IN ('user', 'idle', 'crisis', 'error')),
    CONSTRAINT conversations_processing_status_check
        CHECK (processing_status IN ('none', 'pending', 'running', 'done', 'failed')),
    -- 진행 중인 대화에는 끝난 시각과 이유가 없고, 끝난 대화에는 끝난 시각이 있다.
    CONSTRAINT conversations_ended_check CHECK (
        (status = 'active' AND ended_at IS NULL AND end_reason IS NULL)
        OR (status <> 'active' AND ended_at IS NOT NULL)
    )
);

CREATE INDEX conversations_user_id_started_at_idx ON conversations (user_id, started_at);
CREATE INDEX conversations_day_id_idx ON conversations (day_id);

CREATE TABLE utterances (
    id                 uuid        NOT NULL,
    conversation_id    uuid        NOT NULL,
    user_id            uuid        NOT NULL,
    -- 대화 안에서의 순서다. 같은 시각에 들어온 발화도 순서가 갈린다.
    seq                integer     NOT NULL,
    speaker            text        NOT NULL,
    modality           text        NOT NULL,
    -- 말을 만든 쪽이다. model은 대화 AI의 출력, fixed는 미리 써 둔 문구,
    -- template은 AI의 출력이 검사에서 걸려 사용자의 단어를 넣은 고정 문형으로 바꾼 것이다.
    origin             text        NOT NULL,
    text_enc           bytea       NOT NULL,
    -- 음성 인식이 발화 안에서 가장 자신 없어 한 단어의 신뢰도다. 잘못 들었을 수 있는 말을 되묻는 데 쓴다.
    stt_min_confidence real,
    created_at         timestamptz NOT NULL,
    CONSTRAINT utterances_pkey PRIMARY KEY (id),
    CONSTRAINT utterances_conversation_id_seq_key UNIQUE (conversation_id, seq),
    CONSTRAINT utterances_id_conversation_id_key UNIQUE (id, conversation_id),
    CONSTRAINT utterances_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT utterances_conversation_id_user_id_fkey
        FOREIGN KEY (conversation_id, user_id) REFERENCES conversations (id, user_id) ON DELETE CASCADE,
    CONSTRAINT utterances_seq_check CHECK (seq >= 0),
    CONSTRAINT utterances_speaker_check CHECK (speaker IN ('user', 'ai')),
    CONSTRAINT utterances_modality_check CHECK (modality IN ('voice', 'chat')),
    CONSTRAINT utterances_origin_check CHECK (origin IN ('user', 'model', 'fixed', 'template')),
    CONSTRAINT utterances_speaker_origin_check CHECK ((speaker = 'user') = (origin = 'user')),
    CONSTRAINT utterances_stt_min_confidence_check
        CHECK (stt_min_confidence IS NULL OR (stt_min_confidence >= 0 AND stt_min_confidence <= 1))
);

CREATE INDEX utterances_user_id_idx ON utterances (user_id);

-- 사용자의 발화는 모두 위기 관문을 거치고, 그 판정이 발화마다 한 행으로 남는다.
-- 해당 없음(0단계)도 남긴다. 놓친 것이 없는지 나중에 돌아보려면 "보고도 넘긴" 기록이 있어야 한다.
CREATE TABLE gate_events (
    id              uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    conversation_id uuid        NOT NULL,
    utterance_id    uuid        NOT NULL,
    -- 규칙과 AI 판별은 따로 돈다. 돌지 않았거나 답을 받지 못한 쪽은 NULL이다.
    rule_stage      smallint,
    ai_stage        smallint,
    final_stage     smallint    NOT NULL,
    detected_by     text        NOT NULL,
    -- 두 판정을 합친 뒤에 단계를 바꾼 규칙들의 이름이다.
    adjustments     text[]      NOT NULL DEFAULT '{}',
    ai_failed       boolean     NOT NULL DEFAULT false,
    ai_latency_ms   integer,
    evidence_enc    bytea,
    created_at      timestamptz NOT NULL,
    CONSTRAINT gate_events_pkey PRIMARY KEY (id),
    -- 같은 발화의 판정이 두 번 쌓이면 "최근 몇 번"을 세는 규칙이 부풀어 단계가 잘못 올라간다.
    CONSTRAINT gate_events_utterance_id_key UNIQUE (utterance_id),
    CONSTRAINT gate_events_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT gate_events_conversation_id_user_id_fkey
        FOREIGN KEY (conversation_id, user_id) REFERENCES conversations (id, user_id) ON DELETE CASCADE,
    CONSTRAINT gate_events_utterance_id_conversation_id_fkey
        FOREIGN KEY (utterance_id, conversation_id) REFERENCES utterances (id, conversation_id) ON DELETE CASCADE,
    CONSTRAINT gate_events_rule_stage_check CHECK (rule_stage BETWEEN 0 AND 3),
    CONSTRAINT gate_events_ai_stage_check CHECK (ai_stage BETWEEN 0 AND 3),
    CONSTRAINT gate_events_final_stage_check CHECK (final_stage BETWEEN 0 AND 3),
    CONSTRAINT gate_events_detected_by_check CHECK (detected_by IN ('none', 'rule', 'ai', 'both')),
    CONSTRAINT gate_events_ai_latency_ms_check CHECK (ai_latency_ms >= 0)
);

CREATE INDEX gate_events_user_id_idx ON gate_events (user_id);
CREATE INDEX gate_events_conversation_id_idx ON gate_events (conversation_id);
-- 발화마다 "최근에 1단계 이상이 몇 번 있었나"를 묻는다. 대부분의 행은 0단계라 부분 인덱스로 작게 유지한다.
CREATE INDEX gate_events_user_id_created_at_flagged_idx ON gate_events (user_id, created_at) WHERE final_stage >= 1;

-- 일기는 하루에 하나다.
CREATE TABLE diaries (
    id           uuid        NOT NULL,
    day_id       uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    status       text        NOT NULL,
    -- 대화에서 만든 초안이다. 확인한 뒤에 그날 다시 대화하면 새 초안이 생기고 상태가 draft로 돌아간다.
    draft_enc    bytea,
    -- 사용자가 확인한 글이다. 일기를 고치면 이 글만 바뀌고 신호는 바뀌지 않는다.
    body_enc     bytea,
    confirmed_at timestamptz,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    CONSTRAINT diaries_pkey PRIMARY KEY (id),
    CONSTRAINT diaries_day_id_key UNIQUE (day_id),
    CONSTRAINT diaries_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT diaries_day_id_user_id_fkey FOREIGN KEY (day_id, user_id) REFERENCES days (id, user_id) ON DELETE CASCADE,
    CONSTRAINT diaries_status_check CHECK (status IN ('draft', 'confirmed')),
    CONSTRAINT diaries_confirmed_check CHECK (status <> 'confirmed' OR (body_enc IS NOT NULL AND confirmed_at IS NOT NULL))
);

CREATE INDEX diaries_user_id_idx ON diaries (user_id);

-- 대화 하나에서 항목 하나에 대해 나온 판단이다. 하루 단위로 합치는 일은 계산 쪽이 한다.
-- 자해나 자살에 관한 표현은 항목이 아니다. 점수로 쌓지 않고 위기 관문이 맡는다.
CREATE TABLE signals (
    id                    uuid        NOT NULL,
    user_id               uuid        NOT NULL,
    day_id                uuid        NOT NULL,
    conversation_id       uuid        NOT NULL,
    item                  text        NOT NULL,
    status                text        NOT NULL,
    explicitness          text        NOT NULL,
    -- 판단의 근거가 된 사용자의 발화를 글자 그대로 옮긴 것이다.
    evidence_enc          bytea,
    evidence_utterance_id uuid,
    -- 사용자가 "이건 아니에요"로 취소한 신호다. 계산에서는 빠지지만 행은 남긴다.
    -- 추출이 그 사용자의 표현을 과하게 읽고 있지 않은지 돌아보는 데 쓴다.
    cancelled_at          timestamptz,
    -- 어떤 지시문과 모델로 뽑았는지다. 추출 방식을 바꾼 앞뒤를 견줄 수 있어야 한다.
    extractor_version     text        NOT NULL,
    created_at            timestamptz NOT NULL,
    CONSTRAINT signals_pkey PRIMARY KEY (id),
    CONSTRAINT signals_conversation_id_item_key UNIQUE (conversation_id, item),
    CONSTRAINT signals_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT signals_day_id_fkey FOREIGN KEY (day_id) REFERENCES days (id) ON DELETE CASCADE,
    CONSTRAINT signals_conversation_id_user_id_fkey
        FOREIGN KEY (conversation_id, user_id) REFERENCES conversations (id, user_id) ON DELETE CASCADE,
    -- 신호가 대화와 다른 날에 매달리면 그날의 점수가 조용히 틀어진다.
    CONSTRAINT signals_conversation_id_day_id_fkey
        FOREIGN KEY (conversation_id, day_id) REFERENCES conversations (id, day_id) ON DELETE CASCADE,
    -- 근거는 그 대화 안의 발화여야 한다. 발화가 사라지면 가리키는 값만 비운다.
    CONSTRAINT signals_evidence_utterance_id_conversation_id_fkey
        FOREIGN KEY (evidence_utterance_id, conversation_id) REFERENCES utterances (id, conversation_id)
        ON DELETE SET NULL (evidence_utterance_id),
    CONSTRAINT signals_item_check CHECK (item IN (
        'interest', 'mood', 'sleep', 'fatigue', 'appetite', 'self_blame', 'concentration', 'psychomotor'
    )),
    CONSTRAINT signals_status_check CHECK (status IN ('observed', 'not_observed', 'not_mentioned')),
    CONSTRAINT signals_explicitness_check CHECK (explicitness IN ('direct', 'indirect', 'none')),
    -- 근거 없는 판단은 기록하지 않는다. 언급이 없었다면 근거도 명시성도 있을 수 없다.
    CONSTRAINT signals_evidence_check CHECK (
        (status = 'not_mentioned'
            AND explicitness = 'none' AND evidence_enc IS NULL AND evidence_utterance_id IS NULL)
        OR (status IN ('observed', 'not_observed')
            AND explicitness IN ('direct', 'indirect') AND evidence_enc IS NOT NULL)
    ),
    CONSTRAINT signals_extractor_version_check CHECK (extractor_version <> '')
);

CREATE INDEX signals_user_id_idx ON signals (user_id);
CREATE INDEX signals_day_id_idx ON signals (day_id);
CREATE INDEX signals_evidence_utterance_id_idx ON signals (evidence_utterance_id);

-- 다음 대화에 이어가려고 기억해 둔 것이다. 기억 하나를 지우면 그 행만 사라지고 일기와 신호는 남는다.
CREATE TABLE memories (
    id            uuid        NOT NULL,
    user_id       uuid        NOT NULL,
    source_day_id uuid        NOT NULL,
    kind          text        NOT NULL,
    content_enc   bytea       NOT NULL,
    -- 힘들어했던 일이다. AI가 먼저 꺼내지 않는다.
    sensitive     boolean     NOT NULL DEFAULT false,
    -- 다가오는 일의 기록 날짜다. 지나간 뒤에 안부를 물을 때 쓴다.
    due_date      date,
    last_used_at  timestamptz,
    created_at    timestamptz NOT NULL,
    CONSTRAINT memories_pkey PRIMARY KEY (id),
    CONSTRAINT memories_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT memories_source_day_id_user_id_fkey
        FOREIGN KEY (source_day_id, user_id) REFERENCES days (id, user_id) ON DELETE CASCADE,
    CONSTRAINT memories_kind_check CHECK (kind IN ('event', 'person', 'worry', 'upcoming'))
);

CREATE INDEX memories_user_id_created_at_idx ON memories (user_id, created_at);
CREATE INDEX memories_source_day_id_idx ON memories (source_day_id);

-- 사용자가 직접 고른 오늘의 기분이다. 직접 준 값이라 숫자로 보여줄 수 있고, 추정 점수 계산에는 섞지 않는다.
CREATE TABLE mood_picks (
    day_id     uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    -- 1에서 5까지의 값을 암호화한 것이다. 날짜별 기분은 그 자체로 마음 상태의 기록이고, SQL이 이 값을 읽을 일이 없다.
    -- 평문은 언제나 1바이트로 담는다. 암호문은 길이를 숨기지 않아서, 길이가 값마다 다르면 암호화한 뜻이 없다.
    -- 이 테이블에는 id가 없으므로 암호문에 묶는 행 ID는 day_id다. 값의 범위는 DB가 볼 수 없으니 애플리케이션이 확인한다.
    value_enc  bytea       NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT mood_picks_pkey PRIMARY KEY (day_id),
    CONSTRAINT mood_picks_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT mood_picks_day_id_user_id_fkey FOREIGN KEY (day_id, user_id) REFERENCES days (id, user_id) ON DELETE CASCADE
);

CREATE INDEX mood_picks_user_id_idx ON mood_picks (user_id);

CREATE TABLE self_checks (
    id          uuid        NOT NULL,
    user_id     uuid        NOT NULL,
    record_date date        NOT NULL,
    -- 문항별 응답과 총점, 그리고 무엇 때문에 검진을 하게 됐는지다. 사용자가 직접 답한 값이지만 마음 건강에 관한 기록이라 암호화한다.
    -- 계기(사용자가 스스로 시작했는지, 기록이 모자라 추정을 믿기 어려워서 권했는지, 개입 단계에서 권했는지)도 함께 담는다.
    -- 평문 컬럼으로 두면 "이 사람에게 개입 단계의 제안이 나갔다"는 사실이 날짜와 함께 드러나는데, SQL이 그 값을 읽을 일은 없다.
    result_enc  bytea       NOT NULL,
    created_at  timestamptz NOT NULL,
    CONSTRAINT self_checks_pkey PRIMARY KEY (id),
    CONSTRAINT self_checks_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX self_checks_user_id_record_date_idx ON self_checks (user_id, record_date);

-- +goose Down
DROP TABLE self_checks;
DROP TABLE mood_picks;
DROP TABLE memories;
DROP TABLE signals;
DROP TABLE diaries;
DROP TABLE gate_events;
DROP TABLE utterances;
DROP TABLE conversations;
DROP TABLE days;
