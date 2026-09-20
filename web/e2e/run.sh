#!/usr/bin/env bash
# 브라우저 흐름 테스트를 처음부터 끝까지 돌린다. 저장소 루트에서 `make e2e`로 부른다.
#
#   1. 로컬 DB(docker compose)를 띄우고, 테스트 전용 데이터베이스를 새로 만들어 마이그레이션을 올린다.
#   2. API 서버를 빌드해 빈 포트에 띄운다.
#   3. 웹앱을 운영용으로 빌드하고, /api를 그 서버로 넘기는 미리보기 서버로 띄운다.
#      브라우저가 보기에 화면과 API가 같은 출처라서 세션 쿠키와 서버의 같은 출처 확인이 운영과 똑같이 동작한다.
#   4. Playwright(chromium)를 화면 없이 돌린다.
#   5. 성공하든 실패하든 띄운 서버를 모두 내린다. DB 컨테이너는 개발에도 쓰는 것이라 내리지 않는다.
#
# 뒤에 붙인 인자는 Playwright에 그대로 넘어간다. 예: make e2e ARGS="--headed"

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HOST=127.0.0.1
# 개발용 데이터베이스(naeil)를 건드리지 않으려고 같은 DB 서버 안에 따로 만든다. 돌릴 때마다 지우고 새로 만든다.
E2E_DATABASE=naeil_e2e
READY_TIMEOUT_SECONDS=60

WORK="$(mktemp -d "${TMPDIR:-/tmp}/naeil-e2e.XXXXXX")"
SERVER_PID=""
WEB_PID=""

log() { printf '==> e2e: %s\n' "$*"; }
fail() {
  printf '!! e2e: %s\n' "$*" >&2
  exit 1
}

stop() {
  local pid="$1"
  [ -n "$pid" ] || return 0
  kill "$pid" 2>/dev/null || return 0
  # 서버는 종료 신호를 받으면 처리 중인 요청을 마치고 내려간다. 잠깐 기다렸다가 그래도 남아 있으면 강제로 끝낸다.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.5
  done
  kill -9 "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  stop "$WEB_PID"
  stop "$SERVER_PID"
  if [ "$status" -ne 0 ]; then
    # 서버 로그에는 식별자와 단계만 남는다. 실패했을 때 원인을 찾을 수 있게 끝부분을 보여 준다.
    for name in server web; do
      if [ -s "$WORK/$name.log" ]; then
        printf '\n---- %s 로그의 끝부분 ----\n' "$name" >&2
        tail -n 40 "$WORK/$name.log" >&2
      fi
    done
  fi
  rm -rf "$WORK"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

free_port() {
  node -e 'const s = require("node:net").createServer(); s.listen(0, "127.0.0.1", () => { console.log(s.address().port); s.close(); });'
}

# wait_for <이름> <주소> <pid>: 주소가 200으로 답할 때까지 기다린다. 기다리는 프로세스가 죽으면 바로 그만둔다.
wait_for() {
  local name="$1" url="$2" pid="$3" waited=0
  until curl -fsS -o /dev/null --max-time 2 "$url" 2>/dev/null; do
    kill -0 "$pid" 2>/dev/null || fail "$name: 프로세스가 뜨다가 끝났다"
    waited=$((waited + 1))
    [ "$waited" -lt "$READY_TIMEOUT_SECONDS" ] || fail "$name: ${READY_TIMEOUT_SECONDS}초 안에 준비되지 않았다 ($url)"
    sleep 1
  done
}

for tool in docker go node pnpm curl; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool 이 필요하다"
done
[ -d "$ROOT/web/node_modules" ] || fail "web/node_modules가 없다. 먼저: cd web && pnpm install"
# 서버가 뜨려면 마스터 키가 있어야 한다. 개발용 값은 Makefile에 있고 make가 환경 변수로 넘겨준다.
[ -n "${DATA_KEK_V1:-}" ] || fail "DATA_KEK_V1이 없다. 저장소 루트에서 make e2e로 돌린다"

# ---- DB -----------------------------------------------------------------------
log "로컬 DB를 띄운다"
cd "$ROOT"
docker compose up -d --wait db

# 접속 정보는 docker-compose.yml에 공개된 로컬 개발용 값이다. 포트는 compose에 물어봐서 설정과 어긋나지 않게 한다.
DB_ADDR="$(docker compose port db 5432)"
[ -n "$DB_ADDR" ] || fail "DB 포트를 알아내지 못했다"
E2E_DATABASE_URL="postgres://naeil:naeil@${DB_ADDR}/${E2E_DATABASE}?sslmode=disable"

log "테스트용 데이터베이스(${E2E_DATABASE})를 새로 만든다"
# 앞선 실행이 비정상으로 끝나 접속이 남아 있어도 지울 수 있게 FORCE를 붙인다.
# 처음 돌릴 때는 지울 것이 없다는 알림이 나온다. 오류가 아니므로 경고보다 낮은 알림은 끈다.
docker compose exec -T -e PGOPTIONS='-c client_min_messages=warning' db \
  psql -U naeil -d naeil -q -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS ${E2E_DATABASE} WITH (FORCE)" \
  -c "CREATE DATABASE ${E2E_DATABASE}"

# ---- API 서버 -----------------------------------------------------------------
log "API 서버를 빌드한다"
# go run은 자식 프로세스를 하나 더 만들어서 끝낼 때 놓치기 쉽다. 실행 파일을 만들어 직접 띄운다.
(cd "$ROOT/server" && go build -o "$WORK/server" ./cmd/server)

log "마이그레이션을 올린다"
DATABASE_URL="$E2E_DATABASE_URL" "$WORK/server" migrate up 2>"$WORK/migrate.log" ||
  {
    cat "$WORK/migrate.log" >&2
    fail "마이그레이션이 실패했다"
  }

API_PORT="$(free_port)"
WEB_PORT="$(free_port)"
WEB_ORIGIN="http://${HOST}:${WEB_PORT}"

log "API 서버를 띄운다 (${HOST}:${API_PORT})"
# 개발 기본값으로 띄운다. make가 .env에서 넘겨준 값 가운데 이 테스트의 전제를 깨는 것만 여기서 덮어쓴다.
# 마스터 키(DATA_KEK_V1)는 make가 넘겨준 개발용 값을 그대로 쓴다.
APP_ENV=dev \
  HTTP_ADDR="${HOST}:${API_PORT}" \
  DATABASE_URL="$E2E_DATABASE_URL" \
  PUBLIC_ORIGIN="$WEB_ORIGIN" \
  TRUSTED_PROXIES="" \
  SESSION_COOKIE_NAME=naeil_session \
  SESSION_COOKIE_SECURE=false \
  "$WORK/server" serve >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
wait_for "API 서버" "http://${HOST}:${API_PORT}/readyz" "$SERVER_PID"

# ---- 웹 -----------------------------------------------------------------------
cd "$ROOT/web"

log "웹앱을 빌드한다"
pnpm run build >"$WORK/web-build.log" 2>&1 ||
  {
    cat "$WORK/web-build.log" >&2
    fail "웹앱 빌드가 실패했다"
  }

log "미리보기 서버를 띄운다 (${WEB_ORIGIN}, /api -> ${HOST}:${API_PORT})"
# pnpm을 거치면 프로세스가 두 겹이 되어 끝낼 때 안쪽이 남는다. vite를 node로 직접 띄운다.
API_PROXY_TARGET="http://${HOST}:${API_PORT}" \
  node node_modules/vite/bin/vite.js preview --host "$HOST" --port "$WEB_PORT" --strictPort \
  >"$WORK/web.log" 2>&1 &
WEB_PID=$!
wait_for "미리보기 서버" "${WEB_ORIGIN}/" "$WEB_PID"
# 미리보기 서버를 거쳐 API까지 닿는지 본다. 여기서 막히면 브라우저 테스트의 실패는 원인을 알기 어렵다.
wait_for "미리보기 서버의 /api 연결" "${WEB_ORIGIN}/api/v1/auth/requirements" "$WEB_PID"

# ---- 브라우저 -----------------------------------------------------------------
log "브라우저(chromium)를 준비한다"
# 이미 받아 두었으면 바로 끝난다. CI에서는 시스템 라이브러리까지 필요해서 워크플로가 먼저 설치한다.
pnpm exec playwright install --only-shell chromium

log "Playwright를 돌린다"
E2E_BASE_URL="$WEB_ORIGIN" pnpm exec playwright test "$@"

log "통과"
