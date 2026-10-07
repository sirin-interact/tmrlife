# 내일

## 로컬에서 실행하기

### 준비물

- Docker (로컬 DB와, DB가 필요한 테스트에 씁니다)
- Go 1.26 이상
- Node.js 22 이상과 pnpm 9 (`corepack enable`을 한 번 돌리면 맞는 pnpm이 준비됩니다)

`.env` 파일은 없어도 됩니다. 로컬 개발에 필요한 값은 `Makefile`에 기본값으로 들어 있습니다. 바꾸고 싶은 값이 있을 때만 `.env.example`을 `.env`로 복사해 고칩니다.

### 띄우기

```sh
make setup        # 처음 한 번. 웹 의존성을 설치합니다
make db-up        # PostgreSQL을 띄웁니다 (127.0.0.1:54329)
make migrate      # 스키마를 올립니다
make dev-server   # 터미널 1. API 서버 (http://localhost:8080)
make dev-worker   # 터미널 2. 작업자. 대화가 끝나면 일기 초안과 마음 신호를 만듭니다
make dev-web      # 터미널 3. 웹앱 (http://localhost:5173)
```

브라우저에서 http://localhost:5173 을 엽니다. 웹 개발 서버가 `/api` 요청을 8080의 API 서버로 넘겨줍니다.

| 무엇 | 주소 |
|---|---|
| 웹앱 | http://localhost:5173 |
| API 서버 | http://localhost:8080 (`/healthz`로 떠 있는지 확인합니다) |
| PostgreSQL | 127.0.0.1:54329 |

포트가 이미 쓰이고 있으면 값을 make의 인자로 줍니다.

```sh
make dev-server HTTP_ADDR=:18080
make dev-web API_PROXY_TARGET=http://localhost:18080
```

`make help`를 돌리면 쓸 수 있는 명령이 모두 나옵니다.

### API 명세

API는 명세를 먼저 쓰고 코드를 거기서 만듭니다. 명세는 [`server/openapi.yaml`](server/openapi.yaml)에 있습니다. 서버의 경로와 타입, 웹앱의 API 타입이 모두 이 파일에서 만들어집니다.

명세나 SQL을 고친 뒤에는 `make generate`로 생성 코드를 다시 만듭니다. SQL 쪽 생성에는 sqlc 1.31.1이 필요합니다. 버전이 다르면 고치지 않은 파일까지 바뀐 것으로 나옵니다.

### 테스트

```sh
make check        # 정적 검사, 타입 검사, 서버와 웹의 테스트, 웹 빌드
make lint-docker  # golangci-lint를 설치하지 않고 Docker로 돌립니다
make e2e          # 브라우저로 가입, 대화, 일기, 음성까지 밟아 봅니다
```

- `make check`는 Docker가 돌고 있어야 DB가 필요한 테스트까지 돌립니다. 도구나 의존성이 없어서 건너뛴 단계가 있으면 마지막 줄에 알려줍니다.
- `make e2e`는 DB, API 서버, 작업자, 빌드한 웹앱을 직접 띄우고 끝나면 내립니다. 테스트용 데이터베이스를 따로 만들어 쓰므로 개발용 데이터는 건드리지 않습니다. 필요한 브라우저(Chromium)도 알아서 받습니다.

### 클러스터에 올리기

컨테이너 이미지, 로컬 Kubernetes 클러스터, 운영 배포는 [`deploy/README.md`](deploy/README.md)에 있습니다.
