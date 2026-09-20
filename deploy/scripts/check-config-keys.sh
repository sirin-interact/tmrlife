#!/usr/bin/env bash
# 서버가 읽는 설정 값이 배포 구성에도 모두 있는지 본다. 클러스터에 닿지 않는다.
#
#   deploy/scripts/check-config-keys.sh
#
# 값을 새로 하나 더해 놓고 ConfigMap에 적는 것을 잊으면, 파드는 코드의 기본값으로 조용히 뜬다.
# 그러면 운영에서만 다른 값으로 도는데 로그에도 화면에도 그런 티가 나지 않는다.
# 기준은 저장소 루트의 .env.example이다(서버 테스트가 그 파일에 모든 변수가 적혀 있는지 본다).
# 거기 적힌 이름은 아래 넷 가운데 하나에 들어 있어야 한다.
#
#   1. ConfigMap naeil-config  (base와 overlay의 configMapGenerator)
#   2. Secret 예시             (비밀 값)
#   3. 파드 정의의 env         (DATABASE_URL처럼 다른 Secret에서 받아 오는 값)
#   4. 아래 DEV_ONLY 목록      (개발과 로컬 도구만 읽는 값)
#
# 대화 채널(/ws)이 API 서버로 가는지도 함께 본다. 그 경로가 빠지면 화면은 멀쩡히 뜨고 대화만 열리지 않는다.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
K8S="$ROOT/deploy/k8s"
ENV_EXAMPLE="$ROOT/.env.example"
BASE_CONFIG="$K8S/base/kustomization.yaml"
LOCAL_CONFIG="$K8S/overlays/local/kustomization.yaml"
PROD_CONFIG="$K8S/overlays/prod/kustomization.yaml"
SECRET_EXAMPLE="$K8S/base/secret.example.yaml"
INGRESS="$K8S/base/ingress.yaml"
LOCAL_PROXY="$K8S/overlays/local/local-proxy.caddy"

# 개발 도구만 읽는 값이다. 배포 구성에는 넣지 않는다.
#   API_PROXY_TARGET   프런트 개발 서버가 /api와 /ws를 넘기는 곳
#   MIGRATE_ALLOW_DOWN 스키마를 내리는 명령의 허락. 운영에서는 값과 상관없이 거부된다
DEV_ONLY="API_PROXY_TARGET MIGRATE_ALLOW_DOWN"

failures=0

ok() { echo "  ok   $1"; }
fail() {
  echo "  FAIL $1"
  failures=$((failures + 1))
}

# configMapGenerator의 literals에 적힌 이름을 모은다.
config_keys() {
  grep -Eho '^[[:space:]]*- [A-Z][A-Z0-9_]*=' "$BASE_CONFIG" "$LOCAL_CONFIG" "$PROD_CONFIG" |
    sed -E 's/^[[:space:]]*- //; s/=$//' | sort -u
}

# Secret 예시의 stringData 키를 모은다.
secret_keys() {
  grep -Eho '^[[:space:]]{2}[A-Z][A-Z0-9_]*:' "$SECRET_EXAMPLE" | sed -E 's/^[[:space:]]+//; s/:$//' | sort -u
}

# 파드 정의에서 직접 넘기는 환경 변수의 이름을 모은다.
pod_env_keys() {
  grep -Eho '^[[:space:]]*- name: [A-Z][A-Z0-9_]*$' "$K8S"/base/*.yaml |
    sed -E 's/^.*- name: //' | sort -u
}

# 값을 그대로 적은 literal이 있는지 본다.
has_literal() {
  grep -Eq "^[[:space:]]*- $1=$2\$" "$3"
}

echo "== 서버가 읽는 값이 배포 구성에 있다"
# 이름을 앞뒤에 빈칸을 둔 한 줄로 만들어 둔다. " 이름 "으로 찾으면 앞머리만 같은 이름에 걸리지 않는다.
known=" $(
  {
    config_keys
    secret_keys
    pod_env_keys
    echo "$DEV_ONLY" | tr ' ' '\n'
  } | sort -u | tr '\n' ' '
) "
missing=""
# 값을 채워 둔 줄(NAME=...)과 주석으로 둔 줄(# NAME=...)을 모두 적은 것으로 친다.
while IFS= read -r name; do
  case "$known" in
  *" $name "*) ;;
  *) missing="$missing $name" ;;
  esac
done < <(grep -Eo '^#? ?[A-Z][A-Z0-9_]*=' "$ENV_EXAMPLE" | sed -E 's/^#? ?//; s/=$//')
if [ -n "$missing" ]; then
  fail "ConfigMap에도 Secret에도 없다:${missing}"
  echo "       비밀이 아닌 값은 k8s/base/kustomization.yaml의 naeil-config에 뜻과 함께 적는다."
  echo "       개발 도구만 읽는 값이면 이 스크립트의 DEV_ONLY에 까닭과 함께 적는다."
else
  ok ".env.example의 변수가 모두 배포 구성에 있다"
fi

echo "== 언어 모델 공급자"
if has_literal AI_PROVIDER gemini "$BASE_CONFIG"; then
  ok "운영은 실제 모델을 부른다 (AI_PROVIDER=gemini)"
else
  fail "base의 naeil-config에 AI_PROVIDER=gemini가 없다"
fi
if has_literal AI_PROVIDER scripted "$LOCAL_CONFIG"; then
  ok "로컬은 정해 둔 답으로 돈다 (AI_PROVIDER=scripted)"
else
  fail "overlays/local의 naeil-config에 AI_PROVIDER=scripted가 없다"
fi
if grep -Eq '^[[:space:]]{2}GEMINI_API_KEY:' "$SECRET_EXAMPLE"; then
  ok "Secret 예시에 GEMINI_API_KEY 자리가 있다"
else
  fail "Secret 예시에 GEMINI_API_KEY가 없다. AI_PROVIDER=gemini면 이 키 없이는 서버가 뜨지 않는다"
fi

echo "== 대화 채널(/ws)이 API 서버로 간다"
if awk '
  $0 ~ /^[[:space:]]*- path: \/ws$/ { in_ws = 1; next }
  in_ws && $0 ~ /^[[:space:]]*- path: / { in_ws = 0 }
  in_ws && $0 ~ /name: naeil-server/ { found = 1 }
  END { exit found ? 0 : 1 }
' "$INGRESS"; then
  ok "인그레스의 /ws가 naeil-server로 간다"
else
  fail "k8s/base/ingress.yaml의 /ws가 naeil-server로 가지 않는다"
fi
if grep -q 'reverse_proxy @local_backend naeil-server:8080' "$LOCAL_PROXY" &&
  grep -q 'path /api /api/\* /ws /ws/\*' "$LOCAL_PROXY"; then
  ok "로컬 구성의 웹 파드가 /ws를 naeil-server로 넘긴다"
else
  fail "overlays/local/local-proxy.caddy가 /ws를 naeil-server로 넘기지 않는다"
fi

if [ "$failures" -gt 0 ]; then
  echo "실패 ${failures}건"
  exit 1
fi
echo "모두 통과"
