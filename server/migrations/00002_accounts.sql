-- +goose Up
-- 계정에 딸린 테이블이다. 이 파일과 뒤따르는 스키마 전체가 같은 규칙을 따른다.
--
-- 1. 기본 키는 애플리케이션이 만든 UUIDv7이고 DB 기본값이 없다.
--    암호문에 컬럼 이름과 행 ID를 함께 묶어 다른 행으로 옮겨 붙일 수 없게 하는데,
--    그러려면 행을 쓰기 전에 ID를 알고 있어야 한다.
-- 2. 시각 컬럼에도 DB 기본값이 없다. created_at, updated_at을 포함한 모든 시각은 애플리케이션이 주입받은 시계에서 넣는다.
--    now()를 쓰면 가짜 시계로 몇 주를 몇 분에 돌리는 시험에서 실제 시각과 가짜 시각이 한 테이블에 섞이고,
--    새벽 경계로 나뉘는 기록 날짜와 어긋난 행이 생긴다. 쿼리에서도 now()를 쓰지 않고 시각을 인자로 받는다.
-- 3. 열거 값은 enum 타입이 아니라 text와 CHECK로 둔다. enum은 값을 뺄 수 없고 값을 더하는 것도 트랜잭션 안에서 제약이 있다.
--    CHECK는 제약 하나를 갈아 끼우면 된다.
-- 4. 사용자의 글(발화, 일기, 근거 발화, 기억, 검진 결과)과 사용자가 직접 고른 기분 값은 애플리케이션이 암호화한 bytea로만 들어온다.
--    그런 컬럼의 이름은 _enc로 끝난다. DB는 평문을 보지 못한다.
--
--    글에서 나온 판단 값은 일부러 평문으로 둔다. 신호의 항목, 상태, 명시성, 위기 관문의 단계와 판정 경로,
--    대화가 끝난 이유, 기억의 종류와 민감 표시가 그렇다. 평문이어야 DB가 지켜 줄 수 있는 규칙이 있기 때문이다.
--    근거 없는 판단은 기록되지 않는다는 CHECK, 같은 대화에서 같은 항목이 두 번 쌓여 점수가 부풀지 않게 하는 유일 키,
--    최근에 단계가 오른 발화만 담는 부분 인덱스가 모두 이 값을 읽는다.
--    상태만 암호화하는 식의 절충은 하지 않는다. 암호문은 길이를 숨기지 않고, 근거 컬럼이 비었는지만 봐도 언급 여부가 드러나서
--    가린 것처럼 보일 뿐 가려지지 않는다. 가리기로 한다면 고정 길이로 담고, 근거를 늘 채우고, 끝난 이유까지 함께 가려야 한다.
--
--    그래서 이 값들은 암호화가 아니라 저장소의 통제로 지킨다. DB만 새어 나가도 이메일 옆에 날짜별 단계와 신호 상태가 읽힌다는 뜻이므로,
--    DB의 디스크와 백업은 마스터 키가 없어도 건강에 관한 민감한 정보를 담은 것으로 다룬다.
--    새 평문 컬럼은 스키마 시험의 허용 목록에 올려야 들어온다. 민감한 값이 모르는 사이에 평문으로 더해지지 않게 하려는 것이다.
-- 5. 지울 때는 행을 실제로 지운다. 지운 표시만 남기는 방식은 쓰지 않는다. 기록의 주인은 사용자이고, 지웠다면 없어져야 한다.
--    지우는 범위는 외래 키의 ON DELETE CASCADE가 보장한다.
-- 6. 자식 테이블이 user_id를 함께 들고 있으면, 부모의 (id, user_id)를 가리키는 복합 외래 키로 두 값이 어긋나지 못하게 한다.
--    한 사용자의 행이 다른 사용자의 하루나 대화에 매달리는 일이 구조적으로 생기지 않는다.
-- 7. 외래 키 컬럼에는 그 컬럼으로 시작하는 인덱스를 둔다. 없으면 부모를 지울 때마다 자식 테이블 전체를 훑는다.
-- 8. 추정 점수, 신뢰도, 기준선, 변화 탐지의 누적값을 담는 테이블은 만들지 않는다. 언제나 신호 기록에서 다시 계산한다.
--    하루를 지우거나 신호를 취소했을 때 저장해 둔 값이 없으면 어긋날 일도 없다.

CREATE TABLE users (
    id                uuid        NOT NULL,
    email             citext      NOT NULL,
    email_verified_at timestamptz,
    -- 소셜 로그인만 쓰는 계정은 비밀번호가 없다.
    password_hash     text,
    display_name      text,
    -- IANA 시간대 이름이다. 기록 날짜의 경계와 알림 시각을 이 시간대로 계산한다.
    timezone          text        NOT NULL DEFAULT 'Asia/Seoul',
    role              text        NOT NULL DEFAULT 'user',
    -- 가상 인물의 기록으로 채운 시연용 계정이다. 점수와 단계를 숫자로 보여주는 내부 화면은 이 계정에서만 열린다.
    is_demo           boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT users_pkey PRIMARY KEY (id),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_role_check CHECK (role IN ('user', 'admin')),
    CONSTRAINT users_timezone_check CHECK (timezone <> '')
);

-- 소셜 로그인으로 이어 붙인 바깥 계정이다.
--
-- 이메일이 확인되지 않은 사용자(users.email_verified_at IS NULL)에는 바깥 계정을 붙이지 않는다.
-- 가입은 이메일의 주인인지 확인하지 않으므로, 남의 주소로 먼저 가입해 둔 사람이 있을 수 있다.
-- 나중에 진짜 주인이 같은 이메일의 소셜 계정으로 들어왔을 때 그 계정에 그대로 붙이면,
-- 먼저 가입해 둔 사람이 자기가 정한 비밀번호로 주인의 기록에 계속 들어올 수 있다.
-- 붙이기를 거부하거나, 붙이기 전에 password_hash를 비우고 그 사용자의 세션을 모두 끊는다.
-- 비밀번호 재설정도 같은 이유로 세션을 모두 끊어야 한다.
CREATE TABLE user_identities (
    id         uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    provider   text        NOT NULL,
    -- 공급자가 준 사용자 식별자다. 이메일은 바뀔 수 있어 계정을 찾는 열쇠로 쓰지 않는다.
    subject    text        NOT NULL,
    email      citext,
    created_at timestamptz NOT NULL,
    CONSTRAINT user_identities_pkey PRIMARY KEY (id),
    CONSTRAINT user_identities_provider_subject_key UNIQUE (provider, subject),
    CONSTRAINT user_identities_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT user_identities_provider_check CHECK (provider IN ('google', 'kakao'))
);

CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

-- 사용자마다 따로 두는 데이터 키를 마스터 키로 감싼 값이다.
-- 계정을 지우면 이 행이 함께 사라지므로, 이 키가 들어 있지 않은 백업에 남은 암호문은 더는 읽을 수 없다.
-- 이 테이블까지 담긴 백업에는 그 성질이 없다. 그래서 이 테이블은 글과 같은 장기 백업에 넣지 않거나 짧은 보관 기간으로 따로 백업한다.
-- users와 나눠 두어, 사용자 정보를 읽는 흔한 쿼리에 키가 딸려 나오지 않게 한다.
CREATE TABLE user_keys (
    user_id     uuid        NOT NULL,
    wrapped_dek bytea       NOT NULL,
    -- 어느 마스터 키로 감쌌는지다. 마스터 키를 바꿀 때 아직 옮기지 않은 행을 찾는 데 쓴다.
    kek_version smallint    NOT NULL,
    created_at  timestamptz NOT NULL,
    CONSTRAINT user_keys_pkey PRIMARY KEY (user_id),
    CONSTRAINT user_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT user_keys_kek_version_check CHECK (kek_version >= 1)
);

-- 세션을 사용자에 묶어 두어야 전체 삭제나 동의 철회 때 한꺼번에 끊을 수 있다.
CREATE TABLE sessions (
    id           uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    -- 토큰 원문은 저장하지 않는다. DB가 새어 나가도 그 값으로 로그인할 수 없다.
    token_hash   bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    user_agent   text,
    ip           inet,
    CONSTRAINT sessions_pkey PRIMARY KEY (id),
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
-- 만료된 세션을 주기적으로 지우는 작업이 쓴다.
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- 동의와 철회의 이력이다. 행을 고쳐 쓰지 않고 쌓는다.
-- 동의하면 행이 하나 생기고, 철회하면 그 행에 철회 시각이 찍힌다. 철회한 뒤에 다시 동의하면 새 행이 생긴다.
-- 언제 동의했고 언제 철회했는지를 나중에 그대로 보여줄 수 있어야 하므로, 다시 동의했다고 앞선 철회 기록을 덮어쓰지 않는다.
-- 동의 문서가 바뀌면 새 판(version)으로 다시 동의받는다.
CREATE TABLE consents (
    id           uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    kind         text        NOT NULL,
    version      text        NOT NULL,
    granted_at   timestamptz NOT NULL,
    withdrawn_at timestamptz,
    CONSTRAINT consents_pkey PRIMARY KEY (id),
    CONSTRAINT consents_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    -- sensitive_data: 마음 건강에 관한 기록을 다루는 데 대한 동의.
    -- overseas_transfer: 음성과 대화 내용이 외부 AI 서비스로 전송되어 해외에서 처리될 수 있다는 데 대한 동의.
    CONSTRAINT consents_kind_check CHECK (kind IN ('terms', 'privacy', 'sensitive_data', 'overseas_transfer')),
    CONSTRAINT consents_version_check CHECK (version <> '')
);

-- 철회하지 않은 동의는 사용자, 종류, 판마다 하나뿐이다. 철회한 행은 몇 개든 이력으로 남는다.
CREATE UNIQUE INDEX consents_active_key ON consents (user_id, kind, version) WHERE withdrawn_at IS NULL;
-- 위의 부분 인덱스는 사용자를 지울 때 자식 행을 찾는 데 쓰이지 못한다. 이력을 시간순으로 읽는 데도 같은 인덱스를 쓴다.
CREATE INDEX consents_user_id_granted_at_idx ON consents (user_id, granted_at);

CREATE TABLE user_settings (
    user_id           uuid        NOT NULL,
    reminder_enabled  boolean     NOT NULL DEFAULT true,
    -- 사용자 시간대(users.timezone) 기준의 벽시계 시각이다.
    reminder_time     time        NOT NULL DEFAULT '20:00',
    default_mode      text        NOT NULL DEFAULT 'voice',
    -- 끄면 대화와 일기만 쓰고 신호를 뽑지 않는다.
    analysis_enabled  boolean     NOT NULL DEFAULT true,
    memory_enabled    boolean     NOT NULL DEFAULT true,
    mood_pick_enabled boolean     NOT NULL DEFAULT false,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT user_settings_pkey PRIMARY KEY (user_id),
    CONSTRAINT user_settings_user_id_fkey FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT user_settings_default_mode_check CHECK (default_mode IN ('voice', 'chat'))
);

-- +goose Down
DROP TABLE user_settings;
DROP TABLE consents;
DROP TABLE sessions;
DROP TABLE user_keys;
DROP TABLE user_identities;
DROP TABLE users;
