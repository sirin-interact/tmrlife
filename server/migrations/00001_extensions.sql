-- +goose Up
-- 이메일처럼 대소문자를 가리지 않고 비교해야 하는 컬럼에 쓴다.
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose Down
-- 확장은 내리지 않는다. 다른 스키마나 같은 DB를 쓰는 다른 객체가 기대고 있을 수 있고,
-- 지우면 citext 컬럼을 가진 테이블이 함께 깨진다. 남겨 두어도 해가 없다.
SELECT 1;
