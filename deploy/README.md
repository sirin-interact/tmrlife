# 배포

이미지를 만들고, 내 컴퓨터의 쿠버네티스에서 확인하고, K3s에 올리는 방법을 적는다.
명령은 모두 저장소 루트에서 실행한다. 자주 쓰는 명령은 `make -C deploy help`에 모여 있다.

## 구성

```
                 ┌──────────── Ingress (Traefik) ────────────┐
  브라우저 ──▶   │  /api, /ws, /healthz ──▶ naeil-server     │ ──▶ PostgreSQL 18
                 │  그 밖의 모든 경로    ──▶ naeil-web       │        ▲
                 └────────────────────────────────────────────┘        │
                                             naeil-worker ─────────────┘
```

| 이름 | 하는 일 | 이미지 |
| --- | --- | --- |
| `naeil-server` | REST(`/api`)와 WebSocket(`/ws`)을 받는 API 서버 | `server/Dockerfile` |
| `naeil-worker` | 대화가 끝난 뒤의 일을 큐에서 꺼내 실행하는 작업자 | 서버와 같은 이미지, 실행 명령만 다르다 |
| `naeil-web` | 빌드된 웹앱을 내보내는 정적 파일 서버(Caddy) | `web/Dockerfile` |
| `naeil-migrate` | DB 스키마를 올리는 Job | 서버와 같은 이미지 |

웹앱과 API는 한 도메인 아래에 둔다. 출처가 같으면 세션 쿠키가 단순해지고 CORS 설정이 필요 없다.

```
deploy/
  Makefile                  이 문서의 명령 모음
  scripts/                  이미지 확인 스크립트
  k8s/base/                 환경과 무관한 공통 구성 (그대로 배포하지 않는다)
  k8s/overlays/local/       내 컴퓨터(OrbStack)용. DB 포함, 값은 모두 공개된 개발용
  k8s/overlays/prod/        K3s 운영용. CloudNativePG, cert-manager, Traefik 설정 포함
```

모든 파드는 같은 제약으로 뜬다. 비루트 사용자, 읽기 전용 루트 파일 시스템, 권한 상승 금지, 모든 리눅스 권한 제거, 기본 seccomp 프로필.
이미지에는 셸이 없다. 문제를 볼 때는 `kubectl logs`와 `kubectl describe`를 쓴다.

세 Deployment 모두 상태 검사(startup, liveness, readiness)가 있다. 서버는 `/healthz`와 `/readyz`, 웹은 자기 `/healthz`로 답한다.
작업자는 받을 요청이 없어서 상태 확인 전용 포트(8081)를 따로 열고 `/healthz`에서 큐가 돌고 있는지 답한다. 이 포트는 Service로 열지 않는다.
작업자의 준비 검사는 배포를 위한 것이다. 새 작업자가 큐에 붙은 것을 확인한 뒤에만 옛 작업자를 내린다.
설정이 틀려 뜨자마자 죽는 작업자를 배포해도 옛 작업자가 계속 큐를 돌린다.

## 이미지 만들기

```sh
make -C deploy images          # naeil-server:dev, naeil-web:dev
make -C deploy smoke-web       # 웹 이미지를 운영과 같은 제약으로 띄워 응답을 검사한다
```

직접 만들 때는 각 디렉터리를 빌드 컨텍스트로 준다.

```sh
docker build -t naeil-server:dev server
docker build -t naeil-web:dev web
```

서버 이미지에는 실행 파일이 둘 들어 있다. 기본 명령은 서버를 띄우고, 마이그레이션과 작업자는 이렇게 부른다.

```sh
docker run --rm -e DATABASE_URL=... naeil-server:dev migrate status
docker run --rm --entrypoint /app/worker -e ... naeil-server:dev
```

`smoke-web`이 확인하는 것: 화면 경로(`/login` 등)는 앱 껍데기로 답하는지, 없는 파일은 404인지,
해시가 붙은 파일만 영구 캐시되는지, `index.html`과 `sw.js`와 매니페스트는 매번 서버에 확인하게 되어 있는지,
보안 헤더(CSP, 마이크 권한 정책)가 붙는지, `/api`와 `/ws`에 HTML로 답하지 않는지, 압축(zstd, gzip)이 되는지.
`web/Caddyfile`을 고쳤으면 이 검사를 다시 돌린다.

### 운영용 이미지 올리기

운영 구성은 `ghcr.io/sirin-interact/tmrlife-server`와 `ghcr.io/sirin-interact/tmrlife-web`을 받는다.
태그는 커밋 해시처럼 한번 정하면 바뀌지 않는 값을 쓴다. 운영 노드의 아키텍처에 맞춰 만든다.

```sh
TAG="$(git rev-parse --short HEAD)"
docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/sirin-interact/tmrlife-server:"$TAG" --push server
docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/sirin-interact/tmrlife-web:"$TAG" --push web
```

두 Dockerfile 모두 빌드는 빌드하는 기계의 아키텍처로 돌리고 결과물만 대상 아키텍처로 만든다. 에뮬레이션 없이 여러 아키텍처를 만들 수 있다.

## 로컬에서 확인하기 (OrbStack)

OrbStack의 쿠버네티스는 도커와 이미지 저장소를 함께 쓴다. 로컬에서 만든 이미지를 어디에도 올리지 않고 바로 쓸 수 있다.
로컬 명령은 지금 선택된 컨텍스트와 상관없이 항상 `orbstack` 컨텍스트에만 적용된다.

```sh
make -C deploy images          # 이미지 만들기
make -C deploy validate        # 구성을 펼쳐서 스키마 검사 (클러스터에 닿지 않는다)
make -C deploy local-dry-run   # 클러스터에 만들지 않고 검증만
make -C deploy local-up        # naeil-local 네임스페이스에 올리고 모두 뜰 때까지 기다린다 (20~40초)
make -C deploy local-status
make -C deploy local-smoke     # 웹 진입점으로 가입부터 로그아웃까지 돌려 본다
```

`local-smoke`는 브라우저가 다니는 길 그대로 확인한다. 앱 껍데기와 보안 머리글, `/healthz`, 가입에 필요한 동의 목록, 가입, 내 정보, 중복 가입(409),
로그아웃, 다시 로그인. 다른 출처에서 보낸 가입이 403으로 막히는지도 본다. 끝으로 가입에 쓴 이메일과 비밀번호가 서버, 작업자, 웹, DB의 로그 어디에도 없는지 확인한다.
접속 주소는 서버가 받은 `PUBLIC_ORIGIN`에서 읽으므로, 통과하면 그 값이 실제 주소와 맞다는 것까지 확인된다.
포트 포워딩이 열려 있지 않으면 검사하는 동안만 직접 연다. 검사용 계정이 로컬 DB에 하나씩 남는다.

처음 올릴 때는 DB가 뜨기 전에 시작한 마이그레이션이 한두 번 실패하고 다시 시도한다. `local-status`에 `Error`로 남은 `naeil-migrate` 파드와,
서버와 작업자 파드가 뜨는 동안 잠깐 보이는 `Init:Error`는 그 흔적이다. Job이 `Complete`이고 세 Deployment가 떴으면 정상이다.

OrbStack의 쿠버네티스에는 인그레스 컨트롤러가 없다. 그래서 웹 파드 하나를 포트 포워딩으로 연다.
로컬 구성에서는 웹 파드가 `/api`, `/ws`, 그리고 밖에서 부른 `/healthz`를 API 서버로 넘겨 주므로, 포트 하나만 열어도 브라우저는 운영과 똑같이 한 출처로 앱 전체를 쓴다.
(운영에서는 이 일을 Ingress가 한다. 운영 이미지에는 이 프록시 설정이 들어가지 않는다.)

```sh
make -C deploy local-forward   # http://naeil.localhost:8088
```

포트 포워딩은 열 때 고른 웹 파드 하나에 묶인다. `local-up`이나 `local-restart`로 웹 파드가 새로 뜨면 `lost connection to pod`와 함께 끊긴다. 다시 연다.

다른 터미널에서 확인한다.

```sh
curl -i http://naeil.localhost:8088/                 # 200, 앱 껍데기, Cache-Control: no-cache
curl -i http://naeil.localhost:8088/login            # 200, 같은 앱 껍데기
curl -i http://naeil.localhost:8088/healthz          # 200, API 서버의 {"status":"ok"}
curl -i http://naeil.localhost:8088/api/does-not-exist   # 404, API 서버의 JSON 응답
make -C deploy local-migrate-logs                    # 마이그레이션 시도마다의 마지막 로그. "migrations applied"가 보이면 됐다
kubectl --context orbstack -n naeil-local logs deployment/naeil-worker   # "expired sessions deleted"가 보이면 큐가 끝까지 돈다 (뜨고 나서 몇 초 걸린다)
```

마이그레이션 로그를 `kubectl logs job/naeil-migrate`로 보지 않는다. Job에 파드가 여럿이면 kubectl이 그 가운데 하나를 골라 보여주는데,
DB가 뜨기 전에 실패한 첫 시도가 골라지면 성공한 Job에서도 `migration failed`만 보인다.

상태를 바꾸는 요청(가입, 로그인, 로그아웃)을 curl로 직접 보낼 때는 `-H 'Origin: http://naeil.localhost:8088'`을 붙인다.
서버는 다른 출처의 페이지가 보낸 요청을 403으로 거부하고, `PUBLIC_ORIGIN`과 글자 그대로 같은 출처만 믿는다.

서버의 `/readyz`는 밖으로 열지 않는다. 직접 보고 싶으면 서버 쪽 포트를 따로 연다.

```sh
kubectl --context orbstack -n naeil-local port-forward service/naeil-server 8089:8080
curl -i http://localhost:8089/readyz
```

포트를 8088이 아닌 값으로 바꾸려면 `make -C deploy local-forward LOCAL_PORT=<포트>`로 열고,
`k8s/overlays/local/kustomization.yaml`의 `PUBLIC_ORIGIN`도 같은 값으로 고친다. 둘은 글자 그대로 같아야 한다.

음성과 대화 기능까지 확인하려면 API 키를 셸의 환경 변수에서 바로 Secret으로 만든다. 파일에 적지 않는다.

```sh
kubectl --context orbstack -n naeil-local create secret generic naeil-provider-keys \
  --from-literal=GEMINI_API_KEY="$GEMINI_API_KEY" \
  --from-literal=SONIOX_API_KEY="$SONIOX_API_KEY" \
  --from-literal=ELEVENLABS_API_KEY="$ELEVENLABS_API_KEY"
make -C deploy local-restart
```

코드를 고친 뒤에는 이미지를 다시 만들고 파드를 새로 띄운다. 태그가 같아서 `apply`만으로는 바뀌지 않는다.

```sh
make -C deploy images && make -C deploy local-restart
```

OrbStack의 쿠버네티스에서 확인된 것:

- **네트워크 정책이 집행된다.** DB에는 서버, 작업자, 마이그레이션 파드만 닿는다. 직접 보려면 허용되지 않은 레이블로 파드를 하나 띄워 본다.
  `no response`가 나오면 막힌 것이다. `component=web`을 `component=server`로 바꾸면 `accepting connections`가 나온다.
  함께 나오는 PodSecurity 경고는 이 확인용 파드가 엄격한 기준(restricted)을 채우지 않아서다. 경고일 뿐이고 실행은 된다.

  ```sh
  kubectl --context orbstack -n naeil-local run np-check --rm -i --restart=Never --image=postgres:18 \
    --labels='app.kubernetes.io/part-of=naeil,app.kubernetes.io/component=web' \
    --command -- pg_isready -h naeil-postgres -p 5432 -t 5
  ```

- **DB의 볼륨이 기본 스토리지 클래스(local-path)로 바로 붙는다.** `local-status`의 `persistentvolumeclaim/data-naeil-postgres-0`이 `Bound`다.
- **파드가 내려갈 때 요청이 끊기지 않는다.** 요청을 계속 보내면서 `kubectl rollout restart deployment/naeil-server`를 해도 실패한 요청이 없다.
  새 파드가 준비된 뒤에 옛 파드가 5초를 더 받고(preStop), 종료 신호를 받으면 처리 중인 요청을 마친 뒤 내려간다.
  서버 파드를 `kubectl delete pod`로 직접 지우면 이야기가 다르다. 로컬 구성은 서버가 하나라서, 옛 파드가 내려간 뒤 새 파드가 준비될 때까지 5초쯤 502가 난다.
  운영 구성처럼 둘을 띄운 상태(`kubectl scale deployment/naeil-server --replicas=2`)에서는 하나를 지워도 실패한 요청이 없다.

다 봤으면 지운다. DB 데이터까지 함께 지워진다.

```sh
make -C deploy local-down
```

로컬 구성의 DB 비밀번호와 마스터 키는 저장소에 공개된 개발용 값이다. 실제 기록을 넣지 않는다.

## K3s에 배포하기

### 한 번만 하는 준비

K3s에는 Traefik이 기본으로 들어 있다. 나머지 둘은 직접 설치한다.

```sh
# PostgreSQL을 관리하는 CloudNativePG
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.30/releases/cnpg-1.30.0.yaml

# 인증서를 발급하고 갱신하는 cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml
```

cert-manager에 발급자를 하나 만든다. 이름은 `k8s/overlays/prod/params.yaml`의 `clusterIssuer`와 같아야 한다.

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: <알림을 받을 메일 주소>
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
      - http01:
          ingress:
            ingressClassName: traefik
```

도메인의 DNS가 K3s 노드(또는 그 앞의 로드 밸런서)를 가리키고 80, 443 포트가 열려 있어야 인증서가 발급된다.

### 비밀 값 만들기

비밀 값은 저장소에도, 파일에도 적지 않는다. 셸 변수에 담아 바로 클러스터에 만든다.
어떤 키가 필요한지는 `k8s/base/secret.example.yaml`에 있다.

```sh
kubectl apply -f deploy/k8s/overlays/prod/namespace.yaml

# 마스터 키를 새로 만든다. 화면에 찍히지 않는다.
DATA_KEK_V1="$(openssl rand -base64 32)"

# API 키는 입력을 받아 담는다. 화면과 셸 기록에 남지 않는다.
printf 'Gemini API 키: ';     read -rs GEMINI_API_KEY;     echo
printf 'Soniox API 키: ';     read -rs SONIOX_API_KEY;     echo
printf 'ElevenLabs API 키: '; read -rs ELEVENLABS_API_KEY; echo

kubectl -n naeil create secret generic naeil-secrets \
  --from-literal=DATA_KEK_V1="$DATA_KEK_V1" \
  --from-literal=GEMINI_API_KEY="$GEMINI_API_KEY" \
  --from-literal=SONIOX_API_KEY="$SONIOX_API_KEY" \
  --from-literal=ELEVENLABS_API_KEY="$ELEVENLABS_API_KEY"

# 들어간 키의 이름만 확인한다. 값은 찍지 않는다.
kubectl -n naeil get secret naeil-secrets -o json | jq '.data | keys'
```

**마스터 키(`DATA_KEK_V1`)는 클러스터 밖의 안전한 곳에 따로 보관한다.** 이 키를 잃으면 저장된 기록을 영영 풀 수 없다.
옮길 때도 화면에 찍지 말고 클립보드로 보낸다(macOS: `printf %s "$DATA_KEK_V1" | pbcopy`). 비밀번호 관리자에 넣은 뒤
`unset DATA_KEK_V1 GEMINI_API_KEY SONIOX_API_KEY ELEVENLABS_API_KEY`로 셸에서 지운다.

DB 접속 주소는 직접 만들지 않는다. CloudNativePG가 DB를 띄우면서 Secret `naeil-db-app`을 만들고,
서버와 작업자와 마이그레이션은 그 안의 `uri`를 `DATABASE_URL`로 받는다.

이미지가 비공개라면 받을 권한을 네임스페이스의 기본 서비스 계정에 붙인다. 구성 파일은 고치지 않아도 된다.

```sh
printf 'GHCR 토큰(read:packages): '; read -rs GHCR_TOKEN; echo
kubectl -n naeil create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io --docker-username=<깃허브 계정> --docker-password="$GHCR_TOKEN"
kubectl -n naeil patch serviceaccount default -p '{"imagePullSecrets":[{"name":"ghcr-pull"}]}'
```

### 설정 값

비밀이 아닌 설정은 ConfigMap `naeil-config`에 있다. 공통 값은 `k8s/base/kustomization.yaml`에, 환경마다 다른 값은 각 overlay에 적는다.
서버가 읽는 변수는 빠짐없이 적어 두었고, 값은 서버의 기본값과 같다. 값을 바꾸면 ConfigMap의 이름이 바뀌어 파드가 새 값으로 다시 뜬다.
서버는 뜰 때 값을 검사한다. 틀린 값이 있으면 어느 변수가 왜 틀렸는지 한꺼번에 알려주고 뜨지 않는다(값 자체는 찍지 않는다).

같은 이름을 Secret `naeil-secrets`에도 넣으면 Secret의 값이 이긴다. 비밀이 아닌 설정은 Secret에 넣지 않는다.

| 변수 | 기본값 | 뜻 |
| --- | --- | --- |
| `WORKER_HEALTH_ADDR` | `:8081` | 작업자가 상태 확인 요청을 받는 주소. 작업자만 읽는다. 바꾸면 `k8s/base/worker.yaml`의 `health` 포트도 같은 번호로 바꾼다 |
| `TRUSTED_PROXIES` | 비어 있음 (운영 overlay는 `10.42.0.0/16`) | 서버 앞에 있는 프록시의 주소 범위. 쉼표로 나눈 CIDR 또는 주소. 아래 "클라이언트 주소"를 본다 |
| `SESSION_COOKIE_NAME` | `naeil_session` | 세션 쿠키 이름. `SESSION_COOKIE_SECURE=true`면 서버가 앞에 `__Host-`를 붙인다. `__Secure-`로 시작하는 이름은 받지 않는다 |
| `SESSION_ABSOLUTE_LIFETIME` | `720h` | 로그인한 때부터 재는 세션의 전체 수명. 쿠키의 수명도 이 값이다 |
| `SESSION_IDLE_LIFETIME` | `336h` | 이 시간 동안 쓰지 않으면 세션이 끝난다. 전체 수명보다 길 수 없다 |
| `SESSION_TOUCH_INTERVAL` | `5m` | 마지막으로 쓴 시각을 DB에 다시 적는 최소 간격. 위의 값보다 짧아야 한다 |
| `RATE_LIMIT_SIGNUP_PER_IP` | `20/1h` | 한 주소에서 가입을 시도할 수 있는 횟수. `횟수/주기` 꼴이고 횟수는 1~1000000, 주기는 24시간까지다 |
| `RATE_LIMIT_LOGIN_PER_IP` | `30/5m` | 한 주소에서 로그인을 시도할 수 있는 횟수. IPv6는 /64 단위로 센다 |
| `RATE_LIMIT_LOGIN_PER_EMAIL` | `10/10m` | 한 주소에서 한 계정에 로그인을 시도할 수 있는 횟수. 주소마다 따로 세므로, 남이 틀린 비밀번호를 계속 넣어도 그 사람의 주소만 막힌다. 성공한 로그인도 센다 |
| `RATE_LIMIT_LOGIN_PER_EMAIL_TOTAL` | `60/1h` | 한 계정에 대한 로그인 시도를 주소를 가리지 않고 모두 센 것. 주소를 바꿔 가며 대입해 보는 것을 늦춘다. 위의 값보다 한꺼번에 받는 횟수가 많고 다시 차는 속도도 같거나 빨라야 한다. 아니면 서버가 뜨지 않는다 |
| `RATE_LIMIT_MAX_KEYS` | `50000` | 한도 하나가 기억하는 주소나 계정의 최대 수(1~10000000). 하나에 200바이트쯤 쓴다 |
| `PASSWORD_ARGON2_MEMORY_KIB` | `19456` | 비밀번호 해시 하나에 쓰는 메모리(7168~1048576 KiB) |
| `PASSWORD_ARGON2_TIME` | `2` | 해시가 메모리를 훑는 횟수(1~16) |
| `PASSWORD_ARGON2_PARALLELISM` | `1` | 해시 계산을 나눠 맡는 갈래의 수(1~64) |
| `PASSWORD_HASH_CONCURRENCY` | `4` | 동시에 계산하는 해시의 수. 해시에 쓰는 메모리는 `MEMORY_KIB` x 이 값을 넘지 않는다 |
| `DATA_KEY_CACHE_SIZE` | `1024` | 풀어 둔 사용자별 데이터 키를 파드 하나가 몇 명분까지 들고 있을지 |
| `DATA_KEY_CACHE_MAX_AGE` | `10m` | 풀어 둔 데이터 키를 다시 확인하지 않고 쓰는 최대 시간 |

시도 한도는 파드마다 따로 센다. 운영 구성은 서버를 둘 띄우므로 실제 한도는 적힌 값의 두 배까지 늘어나고, 파드가 다시 뜨면 처음부터 센다.
적힌 값 그대로를 원하면 한도를 파드 수로 나눠 적는다.

해시에 쓰는 메모리(기본값으로 76MiB쯤)와 시도 한도 넷이 기억하는 키(가득 차면 40MB쯤)는 서버 파드의 메모리 상한(`512Mi`) 안에 들어가야 한다.
이 값들을 올릴 때는 `k8s/base/server.yaml`의 `limits.memory`와 `GOMEMLIMIT`을 함께 본다.

### 클라이언트 주소

주소별 시도 한도는 서버가 클라이언트의 주소를 알아야 동작한다. 운영에서는 연결의 상대가 늘 Traefik이므로 두 가지가 모두 맞아야 한다.
하나라도 어긋나면 모든 요청이 한 주소에서 온 것으로 보여서, 주소별 한도(5분에 로그인 30번, 한 시간에 가입 20번)를 모든 사용자가 함께 나눠 쓰게 된다.

1. **서버가 Traefik을 믿는다.** `TRUSTED_PROXIES`에 Traefik 파드가 서버에 붙을 때 쓰는 주소 범위를 적는다.
   운영 overlay에는 K3s의 기본 파드 대역(`10.42.0.0/16`)이 적혀 있다. `--cluster-cidr`를 바꿔 설치했다면 그 값으로 고친다.
   서버는 이 범위에서 온 연결일 때만 `X-Forwarded-For`를 읽고, 헤더 안에서도 이 범위의 주소가 적어 준 부분만 믿는다.
   범위를 넓게 잡으면 그 안의 누구나 주소를 지어내 한도를 피할 수 있다. 파드 대역보다 넓히지 않는다.
2. **Traefik이 클라이언트의 주소를 받는다.** K3s의 기본 설정에서는 밖에서 들어온 연결의 주소가 노드 안에서 바뀌어 Traefik에는 클러스터 안쪽 주소만 보인다.
   Traefik 서비스가 주소를 그대로 받도록 `kube-system` 네임스페이스에 아래 설정을 만든다.

```yaml
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    service:
      spec:
        externalTrafficPolicy: Local
```

Traefik 앞에 로드 밸런서나 CDN을 따로 둔다면 그 장비가 주소를 넘겨 주는 방식(PROXY 프로토콜이나 `X-Forwarded-For`)에 맞춰 Traefik의 진입점 설정도 함께 고친다.

확인하는 법: 배포한 뒤 클러스터 밖에서(예: 휴대전화 통신망) 앱을 한 번 열고, 서버 로그에서 `client address`를 찾는다.
서버는 `/api`로 들어온 요청을 보고 어느 쪽인지 스스로 알린다. 한도에 걸릴 때까지 로그인을 되풀이해 볼 필요가 없다.

```sh
kubectl -n naeil logs -l app.kubernetes.io/name=naeil-server -c server --tail=-1 | grep 'client address'
```

- `client address forwarding works`(INFO): 주소가 제대로 전해지고 있다.
- `client address is not forwarded by the proxy; per-address limits are shared by all users`(WARN): 위의 둘 가운데 하나가 어긋나 있다.

두 줄 모두 파드 하나가 떠 있는 동안 한 번씩만 찍힌다. 설정을 고친 뒤에 다시 보려면 `kubectl -n naeil rollout restart deployment/naeil-server`로 서버를 새로 띄운다.
`TRUSTED_PROXIES`가 비어 있으면 어느 줄도 찍히지 않는다(로컬 구성이 그렇다). 그때는 서버가 헤더를 아예 보지 않는다.
서버가 뜰 때 찍는 `starting` 로그의 `trusted_proxies`에서 적용된 범위를 볼 수 있다. 로그에는 클라이언트의 주소를 남기지 않는다.

한곳에 모인 사람들이 한꺼번에 가입하는 날(시연, 행사)에는 같은 공인 주소를 쓰는 사람들이 가입 한도를 나눠 쓴다.
그날만 overlay에서 `RATE_LIMIT_SIGNUP_PER_IP`를 (예상 인원 / 서버 파드 수)에 여유를 더한 값으로 올리고, 끝나면 되돌린다.
가입은 이메일이 이미 가입되어 있는지 알려주는 유일한 경로라서, 올려 둔 채로 두면 이메일 목록을 확인해 보기가 그만큼 쉬워진다.
한도에 걸린 사람을 바로 풀어 줘야 하면 서버를 새로 띄운다(`rollout restart`). 한도는 메모리에만 있어서 처음부터 다시 세고, 세션은 DB에 있어서 아무도 로그아웃되지 않는다.

### 배포

고칠 곳은 두 군데다.

1. `k8s/overlays/prod/params.yaml`: 도메인과 발급자 이름. 이 값이 Ingress, 인증서, `PUBLIC_ORIGIN`에 한꺼번에 들어간다.
2. 이미지 태그: `make -C deploy prod-set-tag TAG=<태그>`

```sh
make -C deploy render-prod                                  # 실제로 적용될 내용을 눈으로 본다
make -C deploy prod-diff   PROD_CONTEXT=<컨텍스트>          # 클러스터와 무엇이 달라지는지 본다
make -C deploy prod-deploy PROD_CONTEXT=<컨텍스트>
```

`prod-deploy`가 하는 일: 옛 마이그레이션 Job 지우기 → `kubectl apply -k` → Job이 끝나기를 기다리기 → 세 Deployment의 롤아웃을 기다리기.
컨텍스트를 적지 않았거나, 태그나 도메인이 예시 값 그대로면 아무것도 하지 않고 멈춘다.
가입 화면의 동의 문구가 아직 초안(`web/src/content/consentCopy.ts`의 `CONSENT_COPY_IS_DRAFT`)일 때도 멈춘다.
검토되지 않은 문구로 받은 동의는 나중에 문구를 고쳐도 되돌릴 수 없기 때문이다. 실제 가입을 받지 않는 시연이라면 `ALLOW_DRAFT_CONSENT=1`을 함께 준다.

처음 배포할 때는 DB가 뜨는 데 1~2분이 걸린다. 그동안 다른 파드는 `naeil-db-app` Secret이 생기기를 기다린다. 정상이다.

### 마이그레이션이 보장되는 방식

서버와 작업자 파드는 **initContainer에서 `server migrate up`을 먼저 돌린다.** 이것이 보장의 본체다.

- 파드가 어떤 경로로 뜨든(배포, 재시작, 노드 이동, 복제본 늘리기) 자기 이미지가 요구하는 스키마가 준비된 뒤에만 요청을 받는다.
  배포 순서를 사람이나 스크립트가 지켜 주기를 기대하지 않는다.
- 여러 파드가 동시에 돌려도 안전하다. DB 잠금으로 한 번에 하나만 들어가고, 나머지는 기다렸다가 할 일이 없음을 확인하고 끝난다.
- 마이그레이션이 실패하면 새 파드는 뜨지 않는다. 롤링 업데이트가 거기서 멈추고 옛 파드가 계속 요청을 받는다.

`naeil-migrate` Job은 같은 명령을 한 번 더 돌린다. 역할은 기록과 신호다.
배포마다 무엇이 적용됐는지 아래 명령으로 볼 수 있고, 배포 명령은 이 Job의 성공을 기다렸다가 다음으로 넘어간다.

```sh
kubectl -n naeil logs -l app.kubernetes.io/name=naeil-migrate --prefix --tail=2
```

`logs job/naeil-migrate`로 보지 않는다. DB가 뜨기 전에 시작한 시도는 실패한 파드로 남고(처음 배포할 때 흔하다), kubectl은 Job의 파드 가운데 하나만 골라 보여준다.
실패한 시도가 골라지면 성공한 Job에서도 오류만 보인다. 레이블로 고르면 시도마다의 로그가 파드 이름과 함께 모두 나온다.

Job의 파드 정의는 한번 만들면 바꿀 수 없다. 이미지 태그가 바뀐 채로 `kubectl apply -k`만 하면 이 Job 하나가 거부된다(나머지는 적용된다).
그래서 `prod-deploy`는 apply 앞에 옛 Job을 지운다. 손으로 배포할 때도 `kubectl -n naeil delete job naeil-migrate --ignore-not-found`를 먼저 한다.

배포 중에는 옛 서버와 새 스키마가 잠깐 함께 돈다. 그래서 마이그레이션은 옛 코드가 깨지지 않게 쓴다.
컬럼을 없애거나 이름을 바꿀 때는 "더하기 → 코드 옮기기 → 지우기"를 서로 다른 배포로 나눈다.

### 되돌리기

이전 태그로 다시 배포한다. 스키마는 되돌아가지 않는다. 위의 규칙을 지켰다면 옛 코드는 새 스키마에서도 돈다.

```sh
make -C deploy prod-set-tag TAG=<이전 태그>
make -C deploy prod-deploy PROD_CONTEXT=<컨텍스트>
```

스키마를 내리는 명령(`server migrate down`)은 운영에서 쓰지 않는다. 내리는 구문은 테이블을 그 안의 기록과 함께 지우기 때문이다.
서버는 `APP_ENV=prod`에서 이 명령을 거부하고, 다른 환경에서도 `MIGRATE_ALLOW_DOWN=true`를 직접 줘야만 돌린다.
이 값은 설정(ConfigMap)에 넣지 않는다. 로컬에서는 `make migrate-down`이 그때만 허락을 준다.

### 마스터 키 바꾸기

옛 키는 지우지 않는다. 옛 키로 감싼 데이터 키를 풀 수 있어야 한다.

1. Secret `naeil-secrets`에 `DATA_KEK_V2`를 더한다.
2. `k8s/base/kustomization.yaml`(또는 overlay)의 `DATA_KEK_ACTIVE`를 `2`로 올리고 배포한다. 설정의 이름이 바뀌므로 파드가 새 값으로 다시 뜬다.

Secret만 바꿨을 때는 파드가 저절로 다시 뜨지 않는다. `kubectl -n naeil rollout restart deployment/naeil-server deployment/naeil-worker`를 돌린다.

### 실제 기록을 받기 전에 확인할 것

- **DB만 새어 나가도 읽히는 것이 있다.** 사용자의 글과 직접 고른 기분 값은 암호화되어 있어서 마스터 키 없이는 읽을 수 없다.
  글에서 나온 판단 값(신호의 항목과 상태, 위기 관문의 단계, 대화가 끝난 이유)은 DB가 규칙을 지켜 줄 수 있도록 평문으로 둔다.
  그래서 DB의 덤프 하나만으로도 이메일 옆에 날짜별 단계와 신호 상태가 읽힌다. DB의 디스크와 백업은 마스터 키가 없어도 민감한 정보를 담은 것으로 다룬다.
  - DB 볼륨이 놓이는 노드의 디스크나 스토리지 클래스를 암호화한다. K3s의 기본값(local-path)은 노드의 디스크를 그대로 쓴다.
  - 백업을 붙일 때는 백업도 암호화하고, 읽을 수 있는 사람을 DB와 같은 기준으로 좁힌다.
  - 마스터 키는 Secret으로 들어간다. K3s는 Secret을 기본으로 평문 그대로 노드의 디스크에 적는다. `--secrets-encryption`을 켜서, DB와 같은 디스크에 마스터 키가 평문으로 놓이지 않게 한다.
- **백업.** `k8s/overlays/prod/database.yaml`에는 백업 설정이 없다. 오브젝트 스토리지를 정하고 CloudNativePG의 백업을 붙인 뒤, 복구까지 한 번 해 본다.
  계정을 지우면 그 사용자의 데이터 키(`user_keys`의 행)가 함께 사라져서, 그 키가 없는 곳에 남은 암호문은 더는 읽을 수 없다. DB를 통째로 담은 백업에는 그 키도 들어 있으므로,
  지운 계정의 기록은 그 백업이 남아 있는 동안 되살릴 수 있다. 백업의 보관 기간을 "지운 기록이 그때까지는 남는다"고 말할 수 있는 길이로 정한다.
- **DB 복제본.** 기본값은 노드 하나에 맞춘 `instances: 1`이다. 이때 그 노드의 디스크가 유일한 사본이다. 노드가 셋 이상이면 3으로 올린다.
- **스토리지.** 크기(`10Gi`)와 스토리지 클래스를 환경에 맞춘다. K3s의 기본값(local-path)은 노드의 로컬 디스크다.
- **DB 로그.** PostgreSQL은 제약 위반의 상세(DETAIL)에 값을 그대로 적는다. 이미 가입된 이메일로 다시 가입하면 그 이메일이 DB 로그에 남는다.
  그래서 두 구성 모두 `log_error_verbosity=terse`로 DETAIL을 끈다. DB를 다른 방식으로 띄운다면 같은 설정을 넣고, `log_statement`나 `log_min_duration_statement`처럼 값을 함께 적는 설정을 켜지 않는다.
- **네트워크 정책.** DB에는 서버, 작업자, 마이그레이션, 그리고 CloudNativePG만 닿을 수 있게 해 두었다. K3s는 기본으로 정책을 집행한다. `--disable-network-policy`로 띄운 클러스터에서는 아무 효과가 없다.
- **자원.** 각 파드의 요청량과 상한은 작은 노드에 맞춘 출발 값이다. 실제 사용량을 보고 고친다. 서버와 작업자의 메모리 상한을 바꾸면 같은 파일의 `GOMEMLIMIT`도 그 90%쯤으로 함께 바꾼다.

## WebSocket과 Traefik

- 따로 켤 것은 없다. Traefik은 WebSocket 업그레이드를 알아서 처리한다.
- 긴 대화가 늘 비슷한 시간에 끊긴다면 두 곳을 본다.
  - Traefik 진입점의 시간 제한(`respondingTimeouts`). v3에서는 요청을 읽는 제한의 기본값이 60초다. K3s에서는 `kube-system` 네임스페이스에 `HelmChartConfig`를 만들어 Traefik 설정을 바꾼다.
  - Traefik 앞의 장비. 클라우드 로드 밸런서나 CDN은 조용한 연결을 보통 60~100초에 끊는다.
- 어느 쪽이든 대화 연결은 그보다 짧은 간격으로 ping을 주고받는 것이 안전하다.
- 파드가 내려갈 때 서버는 처리 중인 요청을 20초까지 기다린다. 파드의 종료 유예(`terminationGracePeriodSeconds: 40`)는 여기에 맞춘 값이다. 서버 쪽 대기 시간을 늘리면 이 값도 함께 늘린다.
- HTTP로 들어온 요청은 전용 Ingress(`naeil-http-redirect`)가 HTTPS로 돌려보낸다. HSTS는 Traefik 미들웨어가 모든 HTTPS 응답에 붙인다.

## CI

`.github/workflows/ci.yml`이 main에 들어오는 커밋과 모든 PR에서 돌고, 일주일에 한 번(한국 시간 월요일 아침) main을 다시 검사한다.

| 작업 | 하는 일 |
| --- | --- |
| `server` | `go vet`, golangci-lint, `go test -race`(DB 테스트는 PostgreSQL 컨테이너를 직접 띄운다), govulncheck(배포되는 두 실행 파일 기준) |
| `generated` | `make generate`를 돌린 뒤 커밋된 생성 코드와 달라진 것이 없는지 확인 |
| `web` | lint, typecheck, format 검사, test, build |
| `images` | 두 이미지를 빌드하고(올리지 않는다) 운영과 같은 제약으로 띄워 확인 |
| `manifests` | 두 구성을 `kubectl kustomize`로 펼쳐 kubeconform으로 검사 |
| `e2e` | DB, API 서버, 빌드된 웹앱을 띄우고 브라우저(chromium)로 가입부터 로그아웃까지 확인. 로컬의 `make e2e`와 같다 |

도구 버전은 워크플로 위쪽의 `env`에 모여 있다. 로컬의 버전을 올리면 여기도 함께 올린다.

### Go 버전과 취약점 검사

- Go 버전은 `server/Dockerfile`의 `ARG GO_VERSION` 한 곳에만 적는다. CI는 그 줄을 읽어 같은 버전을 설치한다(`.github/actions/setup-go`).
  시험과 govulncheck가 보는 표준 라이브러리가 이미지에 실리는 것과 같아야 하기 때문이다.
  CI가 더 새로운 패치로 검사하면, 이미지에는 고쳐지지 않은 표준 라이브러리가 남아 있어도 초록불이 된다.
- govulncheck가 표준 라이브러리에서 실패하면 결과의 "Fixed in"에 적힌 패치 이상으로 그 한 줄을 올린다.
  운영에는 이미지를 새로 만들어 배포해야 반영된다. CI가 초록불로 돌아온 것만으로는 떠 있는 파드가 바뀌지 않는다.
- 코드가 그대로여도 새 취약점은 계속 알려진다. 커밋이 없는 동안에도 놓치지 않으려고 주간 실행을 둔다.
  공개 저장소에서는 60일 동안 활동이 없으면 GitHub가 예약 실행을 끈다. 꺼졌으면 Actions 화면에서 다시 켠다.
- 로컬의 Go가 이 버전과 다르면 로컬에서 돌린 govulncheck의 결과도 CI와 다르다. 표준 라이브러리 항목은 CI의 결과를 기준으로 본다.
