#!/usr/bin/env bash
# 웹 이미지를 운영과 같은 제약(읽기 전용 루트, 권한 없음, 비루트 사용자)으로 띄우고 응답을 확인한다.
# 캐시 헤더나 앱 껍데기 대체 규칙은 설정 한 줄로 조용히 깨지므로, 이미지를 만들 때마다 실제 응답으로 검사한다.
#
#   deploy/scripts/smoke-web-image.sh naeil-web:dev

set -euo pipefail

image="${1:?사용법: smoke-web-image.sh <이미지>}"
name="naeil-web-smoke-$$"
failures=0

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$name" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --user 65532:65532 \
  -p 127.0.0.1::8080 \
  "$image" >/dev/null

port="$(docker port "$name" 8080/tcp | head -n 1 | sed 's/.*://')"
base="http://127.0.0.1:${port}"

for _ in $(seq 1 50); do
  if curl -fsS -o /dev/null "${base}/healthz" 2>/dev/null; then
    break
  fi
  sleep 0.2
done

ok() { echo "  ok   $1"; }
fail() {
  echo "  FAIL $1"
  failures=$((failures + 1))
}

# 응답 머리글을 소문자로 바꿔 돌려준다. 나머지 인자는 curl에 그대로 넘긴다.
headers_of() {
  local path="$1"
  shift
  curl -sS -o /dev/null -D - "$@" "${base}${path}" | tr -d '\r' | tr '[:upper:]' '[:lower:]'
}

expect_status() {
  local path="$1" want="$2" got
  got="$(curl -sS -o /dev/null -w '%{http_code}' "${base}${path}")"
  if [ "$got" = "$want" ]; then ok "${path} -> ${want}"; else fail "${path}: 상태 ${got}, 기대 ${want}"; fi
}

expect_header() {
  local path="$1" pattern="$2"
  shift 2
  if headers_of "$path" "$@" | grep -Eq -- "$pattern"; then
    ok "${path} 머리글: ${pattern}"
  else
    fail "${path}: 머리글에 '${pattern}' 없음"
  fi
}

reject_header() {
  local path="$1" pattern="$2"
  if headers_of "$path" | grep -Eq -- "$pattern"; then
    fail "${path}: 머리글에 '${pattern}'가 있으면 안 된다"
  else
    ok "${path} 머리글에 없음: ${pattern}"
  fi
}

echo "== 앱 껍데기"
expect_status / 200
expect_header / '^content-type: text/html'
expect_header / '^cache-control: no-cache$'
expect_header / "^content-security-policy: .*script-src 'self';"
expect_header / "^content-security-policy: .*connect-src 'self';"
expect_header / "^content-security-policy: .*frame-ancestors 'none'"
expect_header / '^permissions-policy: .*microphone=\(self\)'
expect_header / '^x-content-type-options: nosniff$'
expect_header / '^x-frame-options: deny$'
expect_header / '^referrer-policy: no-referrer$'
reject_header / '^server:'

echo "== 화면 경로는 앱 껍데기로 답한다"
expect_status /login 200
expect_header /login '^content-type: text/html'
expect_header /login '^cache-control: no-cache$'
expect_status /some/deep/route 200

echo "== 해시가 붙은 파일"
asset="$(curl -fsS "${base}/" | grep -o '/assets/[^"]*\.js' | head -n 1)"
if [ -z "$asset" ]; then
  fail "index.html에서 /assets/*.js를 찾지 못했다"
else
  expect_status "$asset" 200
  expect_header "$asset" '^cache-control: public, max-age=31536000, immutable$'
  expect_header "$asset" '^content-encoding: zstd$' -H 'Accept-Encoding: zstd'
  expect_header "$asset" '^content-encoding: gzip$' -H 'Accept-Encoding: gzip'
fi

echo "== 없는 파일은 404이고 오래 캐시되지 않는다"
expect_status /assets/missing-0000.js 404
reject_header /assets/missing-0000.js 'immutable'
expect_status /.env 404
expect_status /robots.txt 404

echo "== 서비스 워커와 매니페스트"
expect_status /sw.js 200
expect_header /sw.js '^content-type: (text|application)/javascript'
expect_header /sw.js '^cache-control: no-cache$'
expect_status /manifest.webmanifest 200
expect_header /manifest.webmanifest '^content-type: application/manifest\+json'
expect_header /manifest.webmanifest '^cache-control: no-cache$'

echo "== 아이콘"
expect_status /icons/icon-192.png 200
expect_header /icons/icon-192.png '^cache-control: public, max-age=86400$'

echo "== API 경로는 앱 껍데기로 답하지 않는다"
expect_status /api/anything 404
expect_status /api 404
expect_status /ws 404

echo "== 종료 신호를 받으면 스스로 내려간다"
docker stop --timeout 20 "$name" >/dev/null
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$name")"
if [ "$exit_code" = "0" ]; then ok "종료 코드 0"; else fail "종료 코드 ${exit_code}"; fi

echo "== 뜨고 내려가는 동안 오류 로그가 없다"
if docker logs "$name" 2>&1 | grep -q '"level":"error"'; then
  fail "오류 수준의 로그가 있다"
else
  ok "오류 로그 없음"
fi

if [ "$failures" -gt 0 ]; then
  echo "실패 ${failures}건"
  docker logs "$name" 2>&1 | tail -n 20
  exit 1
fi
echo "모두 통과"
