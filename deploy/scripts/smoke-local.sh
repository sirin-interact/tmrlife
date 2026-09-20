#!/usr/bin/env bash
# 로컬 클러스터에 올린 앱을 웹 진입점 하나로 확인한다. 브라우저가 다니는 길과 같은 길이다.
#
#   앱 껍데기와 보안 머리글 -> /healthz -> 가입에 필요한 동의 -> 가입 -> 내 정보 -> 로그아웃
#
# 끝으로 방금 쓴 이메일과 비밀번호가 서버, 작업자, 웹, DB 어느 로그에도 남지 않았는지 본다.
# 포트 포워딩이 열려 있지 않으면 검사하는 동안만 직접 연다.
# 검사용 계정이 DB에 하나 남는다. 로컬 DB는 `make -C deploy local-down`으로 통째로 지워진다.
#
#   deploy/scripts/smoke-local.sh [컨텍스트] [네임스페이스]

set -euo pipefail

context="${1:-orbstack}"
namespace="${2:-naeil-local}"
failures=0
forward_pid=""
workdir="$(mktemp -d)"

kube() { kubectl --context "$context" -n "$namespace" "$@"; }

cleanup() {
  if [ -n "$forward_pid" ]; then
    kill "$forward_pid" >/dev/null 2>&1 || true
    # 끝난 것을 직접 거둬야 셸이 "Terminated"라는 줄을 따로 찍지 않는다.
    wait "$forward_pid" 2>/dev/null || true
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

ok() { echo "  ok   $1"; }
fail() {
  echo "  FAIL $1"
  failures=$((failures + 1))
}

# 서버가 실제로 받은 설정에서 출처를 읽는다. 상태를 바꾸는 요청은 이 출처에서 온 것만 받아들여지므로,
# 여기서 읽은 값으로 접속해서 통과하면 "주소창의 출처와 PUBLIC_ORIGIN이 같다"는 것까지 함께 확인된다.
config_name="$(kube get deployment naeil-server \
  -o jsonpath='{.spec.template.spec.containers[0].envFrom[0].configMapRef.name}')"
origin="$(kube get configmap "$config_name" -o jsonpath='{.data.PUBLIC_ORIGIN}')"
if [ -z "$origin" ]; then
  echo "!! PUBLIC_ORIGIN을 읽지 못했다. 먼저 올린다: make -C deploy local-up"
  exit 1
fi

hostport="${origin#*://}"
host="${hostport%%:*}"
if [ "$hostport" = "$host" ]; then port=80; else port="${hostport##*:}"; fi
echo "==> ${origin}"

# *.localhost를 스스로 풀지 못하는 curl도 있다. 포트 포워딩은 늘 이 컴퓨터에 열리므로 주소를 직접 알려 준다.
request() { curl -sS --max-time 10 --resolve "${host}:${port}:127.0.0.1" "$@"; }

if ! request -o /dev/null "${origin}/" 2>/dev/null; then
  echo "-- 포트 포워딩이 없어 검사하는 동안만 연다 (${port})"
  # kube 함수를 거치지 않고 kubectl을 직접 부른다. 함수를 뒤에서 돌리면 $!가 함수를 감싼 셸의 PID가 되어,
  # 끝낼 때 그 셸만 죽고 kubectl은 남아 포트를 계속 붙잡는다. 그러면 다음 local-forward가 포트를 열지 못한다.
  kubectl --context "$context" -n "$namespace" port-forward service/naeil-web "${port}:8080" >/dev/null 2>&1 &
  forward_pid=$!
  for _ in $(seq 1 50); do
    if request -o /dev/null "${origin}/" 2>/dev/null; then break; fi
    sleep 0.2
  done
fi

# 상태 코드는 돌려주고, 본문과 머리글(소문자)은 파일에 남긴다.
call() {
  local method="$1" path="$2"
  shift 2
  request -X "$method" -o "$workdir/body" -D "$workdir/headers.raw" -w '%{http_code}' "$@" "${origin}${path}"
  tr -d '\r' <"$workdir/headers.raw" | tr '[:upper:]' '[:lower:]' >"$workdir/headers"
}

expect_status() {
  local label="$1" want="$2" got="$3" code
  if [ "$got" = "$want" ]; then
    ok "${label} -> ${want}"
  else
    # 오류 응답은 요청에 담겨 온 값을 되돌려 보내지 않는다. code는 그대로 찍어도 된다.
    code="$(sed -nE 's/.*"code":"([a-z_]+)".*/\1/p' "$workdir/body" 2>/dev/null | head -n 1)"
    fail "${label}: 상태 ${got}${code:+ (${code})}, 기대 ${want}"
  fi
}

expect_header() {
  local label="$1" pattern="$2"
  if grep -Eq -- "$pattern" "$workdir/headers"; then ok "${label} 머리글: ${pattern}"; else fail "${label}: 머리글에 '${pattern}' 없음"; fi
}

expect_body() {
  local label="$1" pattern="$2"
  if grep -Eq -- "$pattern" "$workdir/body"; then ok "${label} 본문: ${pattern}"; else fail "${label}: 본문에 '${pattern}' 없음"; fi
}

echo "== 모두 떠 있다"
for deployment in naeil-server naeil-worker naeil-web; do
  ready="$(kube get deployment "$deployment" -o jsonpath='{.status.readyReplicas}')"
  if [ "${ready:-0}" -ge 1 ]; then ok "${deployment} 준비된 파드 ${ready}"; else fail "${deployment}: 준비된 파드가 없다"; fi
done

echo "== 앱 껍데기는 웹 서버가 답한다"
expect_status "GET /" 200 "$(call GET /)"
expect_header "GET /" '^content-type: text/html'
expect_header "GET /" '^cache-control: no-cache$'
expect_header "GET /" "^content-security-policy: .*script-src 'self';"
expect_header "GET /" "^content-security-policy: .*frame-ancestors 'none'"
expect_body "GET /" '<div id="root">'
expect_status "GET /login" 200 "$(call GET /login)"
expect_body "GET /login" '<div id="root">'

echo "== /healthz와 /api는 같은 출처에서 API 서버가 답한다"
expect_status "GET /healthz" 200 "$(call GET /healthz)"
expect_body "GET /healthz" '"status":"ok"'
expect_status "GET /api/does-not-exist" 404 "$(call GET /api/does-not-exist)"
expect_header "GET /api/does-not-exist" '^content-type: application/problem\+json'

echo "== 대화 채널(/ws)도 같은 출처에서 API 서버가 답한다"
# 연결을 여는 손잡기만 보낸다. 로그인하지 않았으므로 101이 아니라 401이 와야 한다.
# 여기서 404나 HTML이 오면 요청이 API 서버에 닿지 못하고 정적 파일 서버에서 끝난 것이다.
upgrade=(-H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13'
  -H 'Sec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==')
expect_status "GET /ws/v1/conversation (쿠키 없음)" 401 "$(call GET /ws/v1/conversation "${upgrade[@]}")"
expect_header "GET /ws/v1/conversation (쿠키 없음)" '^content-type: application/problem\+json'
# /api와 같은 미들웨어를 거치는지 본다. 연결을 여는 요청은 GET이지만 상태를 바꾸는 요청과 같은 검사를 받아야 한다.
expect_status "GET /ws/v1/conversation (다른 출처)" 403 "$(call GET /ws/v1/conversation "${upgrade[@]}" \
  -H 'Origin: https://other.example' -H 'Sec-Fetch-Site: cross-site')"

echo "== 가입에 필요한 동의"
expect_status "GET /api/v1/auth/requirements" 200 "$(call GET /api/v1/auth/requirements)"
# 동의 목록은 객체만 담긴 배열이라 첫 ']'까지가 배열 전체다. 받은 그대로 가입 요청에 넣는다.
consents="$(sed -E 's/.*"consents":(\[[^]]*\]).*/\1/' "$workdir/body")"
case "$consents" in
  \[\{*\}\]) ok "동의 목록을 받았다" ;;
  *) fail "동의 목록을 읽지 못했다"; consents="[]" ;;
esac

# 로그에서 찾을 수 있게 다른 곳에 나올 리 없는 값을 쓴다.
marker="smoke$(date +%s)x${RANDOM}"
email="${marker}@example.com"
password="pw-${marker}-${RANDOM}${RANDOM}"
# 본문은 모두 변수에 먼저 담는다. 명령 줄에 {a,b} 꼴을 그대로 적으면 macOS의 옛 bash가 중괄호를 펼쳐 요청을 둘로 쪼갠다.
signup_body="{\"email\":\"${email}\",\"password\":\"${password}\",\"timezone\":\"Asia/Seoul\",\"consents\":${consents}}"
foreign_body="{\"email\":\"x${email}\",\"password\":\"${password}\",\"consents\":${consents}}"
login_body="{\"email\":\"${email}\",\"password\":\"${password}\"}"

echo "== 다른 출처의 페이지가 보낸 가입은 거부한다"
expect_status "POST /api/v1/auth/signup (다른 출처)" 403 "$(call POST /api/v1/auth/signup \
  -H 'Content-Type: application/json' -H 'Origin: https://other.example' -H 'Sec-Fetch-Site: cross-site' \
  --data "$foreign_body")"

echo "== 가입"
expect_status "POST /api/v1/auth/signup" 201 "$(call POST /api/v1/auth/signup -c "$workdir/cookies" \
  -H 'Content-Type: application/json' -H "Origin: ${origin}" -H 'Sec-Fetch-Site: same-origin' --data "$signup_body")"
expect_header "POST /api/v1/auth/signup" '^set-cookie: [^=]+=[^;]+; .*httponly'
expect_header "POST /api/v1/auth/signup" '^set-cookie: .*samesite=lax'
expect_header "POST /api/v1/auth/signup" '^cache-control: no-store$'

echo "== 내 정보"
expect_status "GET /api/v1/me (쿠키 있음)" 200 "$(call GET /api/v1/me -b "$workdir/cookies")"
expect_body "GET /api/v1/me" "\"email\":\"${email}\""
expect_status "GET /api/v1/me (쿠키 없음)" 401 "$(call GET /api/v1/me)"

echo "== 같은 이메일로 다시 가입하면 409"
expect_status "POST /api/v1/auth/signup (중복)" 409 "$(call POST /api/v1/auth/signup \
  -H 'Content-Type: application/json' -H "Origin: ${origin}" --data "$signup_body")"

echo "== 로그아웃"
expect_status "POST /api/v1/auth/logout" 204 "$(call POST /api/v1/auth/logout -b "$workdir/cookies" \
  -H "Origin: ${origin}" -H 'Sec-Fetch-Site: same-origin')"
expect_status "GET /api/v1/me (로그아웃한 쿠키)" 401 "$(call GET /api/v1/me -b "$workdir/cookies")"

echo "== 로그인하고 다시 로그아웃"
expect_status "POST /api/v1/auth/login" 200 "$(call POST /api/v1/auth/login -c "$workdir/cookies" \
  -H 'Content-Type: application/json' -H "Origin: ${origin}" --data "$login_body")"
expect_status "GET /api/v1/me (다시 로그인)" 200 "$(call GET /api/v1/me -b "$workdir/cookies")"
expect_status "POST /api/v1/auth/logout" 204 "$(call POST /api/v1/auth/logout -b "$workdir/cookies" -H "Origin: ${origin}")"

echo "== 이메일과 비밀번호가 어느 로그에도 없다"
# 중복 가입까지 했으므로 DB가 제약 위반을 적는 길도 함께 지나왔다.
for target in deployment/naeil-server deployment/naeil-worker deployment/naeil-web statefulset/naeil-postgres; do
  if ! kube logs "$target" --all-containers --tail=-1 >"$workdir/log" 2>/dev/null; then
    fail "${target}: 로그를 읽지 못했다"
  elif grep -q -- "$marker" "$workdir/log"; then
    fail "${target}: 로그에 가입에 쓴 값이 남았다"
  else
    ok "${target} ($(wc -l <"$workdir/log" | tr -d ' ')줄)"
  fi
done

if [ "$failures" -gt 0 ]; then
  echo "실패 ${failures}건"
  exit 1
fi
echo "모두 통과"
