# 내일 — 개발 명령 모음. `make help`로 목록을 본다.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# .env가 있으면 읽는다. 없으면 조용히 넘어간다. make는 읽은 내용을 출력하지 않는다.
# make 문법으로 읽기 때문에 .env의 값은 따옴표 없이 KEY=value 꼴로 적는다.
# .env에 적힌 값이 아래 기본값보다 우선한다.
-include .env

# ---- 로컬 개발 기본값 --------------------------------------------------------
# .env 없이도 로컬에서 바로 돌아가게 하려는 값이다. docker-compose.yml의 DB와 짝을 이룬다.
DATABASE_URL ?= postgres://naeil:naeil@localhost:54329/naeil?sslmode=disable

# 개발 전용 키다. 저장소에 공개된 값이므로 실제 데이터를 절대 이 키로 암호화하지 않는다.
# APP_ENV=prod에서는 서버가 이 값을 거부한다. 운영 키는 `openssl rand -base64 32`로 만든다.
DATA_KEK_V1 ?= bmFlaWwtZGV2LW9ubHkta2VrLWRvLW5vdC11c2UhISE=

# .env와 위 기본값을 하위 프로세스(go run, pnpm)에 환경 변수로 넘긴다.
# .env에 새 변수를 더해도 Makefile을 고칠 필요가 없도록 이름을 나열하지 않고 전부 넘긴다.
export

# 시험 실행 파일 하나의 제한 시간을 직접 적는다. 기본값은 10분인데, 계산 코어의 시험은 경합 검사(-race)를 켜면
# 빠른 컴퓨터에서도 1분 30초쯤 걸리고 CI에서는 그 몇 배가 든다. 기본값에 기대면 느린 날에만 실패하는 시험이 된다.
GOTESTFLAGS ?= -race -timeout 20m

# CI와 로컬이 같은 결과를 내도록 버전을 고정한다.
GOLANGCI_LINT_IMAGE ?= golangci/golangci-lint:v2.13.2

# sqlc는 만든 파일의 머리말에 제 버전을 적는다. 다른 버전으로 돌리면 SQL을 고치지 않았어도 생성 코드가 바뀐 것으로 나온다.
SQLC_VERSION ?= 1.31.1

# web/package.json에 해당 스크립트가 있는지 본다. web/가 아직 없거나 만들어지는 중이어도 실패하지 않게 한다.
# 괄호로 묶어 두어야 `! $(call has_web_script,x)`처럼 부정했을 때 전체가 부정된다.
has_web_script = ( [ -f web/package.json ] && node -e 'const s = require("./web/package.json").scripts || {}; process.exit(s[process.argv[1]] ? 0 : 1)' $(1) )

# web 스크립트를 조건부로 돌린다. 스크립트가 없으면 건너뛴다.
# 의존성이 설치되지 않았으면 로컬에서는 건너뛰고, CI에서는 실패시킨다.
define run_web_script
@if ! [ -f web/package.json ]; then \
	echo "-- web/package.json 없음: web $(1) 건너뜀"; \
elif ! $(call has_web_script,$(1)); then \
	echo "-- web에 $(1) 스크립트 없음: 건너뜀"; \
elif ! [ -d web/node_modules ]; then \
	if [ -n "$${CI:-}" ]; then echo "!! web/node_modules 없음: pnpm install을 먼저 돌린다"; exit 1; fi; \
	echo "-- web/node_modules 없음: web $(1) 건너뜀 (cd web && pnpm install)"; \
else \
	echo "==> web: pnpm run $(1)"; \
	cd web && pnpm run $(1); \
fi
endef

# 위 도우미는 make 안에서만 쓰는 값이라 환경 변수로 내보내지 않는다.
unexport has_web_script run_web_script GOTESTFLAGS GOLANGCI_LINT_IMAGE SQLC_VERSION

.PHONY: help
help: ## 이 도움말을 보여준다
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(firstword $(MAKEFILE_LIST))

# ---- 준비 ---------------------------------------------------------------------

# 받은 직후에 한 번 돌린다. 웹 의존성이 없으면 generate와 e2e는 실패하고, check는 웹 단계를 건너뛴 채 끝난다.
# 없어도 되는 도구는 설치하지 않고 있는지만 알려준다. 없을 때 check가 무엇을 건너뛰는지 미리 알 수 있다.
.PHONY: setup
setup: ## 처음 한 번: 웹 의존성을 설치하고, 없어도 되는 도구가 있는지 알려준다
	@command -v pnpm >/dev/null 2>&1 || { echo "!! pnpm이 없다. Node.js 22 이상을 설치한 뒤 corepack enable을 돌린다"; exit 1; }
	@echo "==> web: pnpm install --frozen-lockfile"
	@cd web && pnpm install --frozen-lockfile
	@if command -v sqlc >/dev/null 2>&1; then \
		echo "-- sqlc: $$(sqlc version) (생성 코드는 v$(SQLC_VERSION)으로 만든다)"; \
	else \
		echo "-- sqlc 없음: SQL을 고칠 때만 필요하다 (v$(SQLC_VERSION)). 없으면 check가 sqlc-diff를 건너뛴다"; \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "-- golangci-lint: 있음"; \
	else \
		echo "-- golangci-lint 없음: 설치하지 않아도 make lint-docker로 같은 검사를 돌릴 수 있다"; \
	fi
	@if docker info >/dev/null 2>&1; then \
		echo "-- Docker: 돌고 있음"; \
	else \
		echo "-- Docker가 돌고 있지 않다: 로컬 DB(make db-up)와 DB가 필요한 서버 테스트에 필요하다"; \
	fi

# ---- 생성 ---------------------------------------------------------------------

.PHONY: generate
generate: ## 생성 코드를 다시 만든다 (sqlc, go generate, API 타입)
	@if [ -f server/sqlc.yaml ]; then \
		echo "==> server: sqlc generate"; \
		cd server && sqlc generate; \
	else \
		echo "-- server/sqlc.yaml 없음: sqlc 건너뜀"; \
	fi
	@echo "==> server: go generate ./..."
	@cd server && go generate ./...
	@if ! $(call has_web_script,generate:api); then \
		echo "-- web에 generate:api 스크립트 없음: 건너뜀"; \
	elif ! [ -d web/node_modules ]; then \
		echo "!! web/node_modules 없음: API 타입을 만들지 못했다. make setup을 먼저 돌린다"; exit 1; \
	else \
		echo "==> web: pnpm run generate:api"; \
		cd web && pnpm run generate:api; \
	fi

# ---- DB -----------------------------------------------------------------------

.PHONY: db-up
db-up: ## 로컬 DB를 띄우고 준비될 때까지 기다린다
	@docker compose up -d --wait db

.PHONY: migrate
migrate: ## 마이그레이션을 끝까지 올린다 (앱 스키마, 작업 큐 스키마)
	@cd server && go run ./cmd/server migrate up

# 내리는 구문은 테이블을 통째로 지운다. 그래서 서버는 허락(MIGRATE_ALLOW_DOWN=true)이 없으면 내리지 않고,
# APP_ENV=prod에서는 허락이 있어도 내리지 않는다. 로컬 개발에서는 이 명령이 그 허락을 대신 준다.
.PHONY: migrate-down
migrate-down: ## 앱 스키마 마이그레이션을 한 단계 내린다 (테이블과 데이터가 지워진다. 로컬 개발용)
	@cd server && MIGRATE_ALLOW_DOWN=true go run ./cmd/server migrate down

.PHONY: migrate-status
migrate-status: ## 마이그레이션 적용 상태를 본다
	@cd server && go run ./cmd/server migrate status

# ---- 개발 서버 ----------------------------------------------------------------

# 포트를 바꿀 때는 값을 make의 인자로 준다(환경 변수로 앞에 붙이지 않는다).
#   make dev-server HTTP_ADDR=:18080
#   make dev-web API_PROXY_TARGET=http://localhost:18080
# .env에 같은 변수가 있으면 make는 환경 변수보다 .env의 값을 쓴다. 인자로 준 값은 .env보다도 앞선다.
# 앞에 붙이는 꼴(HTTP_ADDR=:18080 make dev-server)은 .env가 있을 때 아무 말 없이 무시되어,
# 서버는 8080에 그대로 있고 웹만 18080을 보는 어긋난 상태가 된다.
.PHONY: dev-server
dev-server: ## API 서버를 띄운다 (:8080, 바꾸려면 make dev-server HTTP_ADDR=:18080)
	@cd server && go run ./cmd/server serve

.PHONY: dev-worker
dev-worker: ## 작업자를 띄운다
	@cd server && go run ./cmd/worker

.PHONY: dev-web
dev-web: ## 프런트 개발 서버를 띄운다 (:5173, API가 8080이 아니면 API_PROXY_TARGET=http://localhost:포트)
	@if ! [ -f web/package.json ]; then echo "!! web/package.json이 아직 없다"; exit 1; fi
	@cd web && pnpm run dev

# ---- 검사 ---------------------------------------------------------------------

.PHONY: test
test: test-server test-web ## 서버와 프런트의 테스트를 돌린다

.PHONY: test-server
test-server:
	@echo "==> server: go test $(GOTESTFLAGS) ./..."
	@cd server && go test $(GOTESTFLAGS) ./...

.PHONY: test-web
test-web:
	$(call run_web_script,test)

.PHONY: lint
lint: lint-server lint-web ## 정적 검사를 돌린다 (go vet, golangci-lint, ESLint)

# golangci-lint는 이 저장소에서 다른 데 없는 규칙을 혼자 지킨다. 로그에 사용자의 글이 섞이지 못하게 하는 sloglint,
# time.Now를 internal/clock 밖에서 막는 forbidigo, 계산 코어의 의존을 막는 depguard가 여기에만 있다.
# 그래서 없다고 조용히 넘어가지 않는다. 설치된 것이 있으면 그것을, 없고 Docker가 돌면 CI와 같은 이미지를 쓴다.
.PHONY: lint-server
lint-server:
	@echo "==> server: go vet ./..."
	@cd server && go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "==> server: golangci-lint run"; \
		cd server && golangci-lint run ./...; \
	elif docker info >/dev/null 2>&1; then \
		echo "==> server: golangci-lint (Docker, $(GOLANGCI_LINT_IMAGE))"; \
		$(MAKE) --no-print-directory lint-docker; \
	else \
		echo "!! golangci-lint도 Docker도 없다. 이 검사는 CI에서 반드시 돈다 (sloglint, forbidigo, depguard가 여기에만 있다)."; \
		exit 1; \
	fi

.PHONY: lint-docker
lint-docker: ## golangci-lint를 설치하지 않고 Docker로 돌린다
	@docker run --rm \
		-v "$(CURDIR)/server":/app:ro -w /app \
		-v naeil-golangci-gomod:/go/pkg/mod \
		-v naeil-golangci-cache:/root/.cache \
		$(GOLANGCI_LINT_IMAGE) golangci-lint run ./...

# 낡은 캐시가 없는 문제를 알리는 일이 있다. 특히 "//nolint 지시문이 쓰이지 않았다"(nolintlint)는,
# 그 지시문이 막고 있는 검사의 결과가 캐시에서 올 때 잘못 뜬다. CI는 언제나 빈 캐시로 돌기 때문에 로컬에서만 겪는다.
# 고친 데가 없는데 로컬에서만 lint가 실패하면 이 명령으로 캐시를 버리고 다시 돌린다. 오래 걸리는 것 말고는 잃는 것이 없다.
.PHONY: lint-docker-reset
lint-docker-reset: ## Docker로 돌리는 lint의 캐시를 버리고 다시 돌린다 (고친 데 없이 lint가 실패할 때)
	@docker volume rm -f naeil-golangci-cache >/dev/null
	@$(MAKE) --no-print-directory lint-docker

.PHONY: lint-web
lint-web:
	$(call run_web_script,lint)

.PHONY: typecheck
typecheck: ## 프런트 타입 검사를 돌린다
	$(call run_web_script,typecheck)

.PHONY: format-check
format-check: ## 프런트 코드의 서식이 맞는지 본다 (고치지는 않는다)
	$(call run_web_script,format:check)

.PHONY: build-web
build-web: ## 프런트를 운영용으로 빌드해 본다
	$(call run_web_script,build)

# 쿼리나 마이그레이션을 고치고 생성 코드를 다시 만들지 않은 경우를 잡는다. 파일을 고치지 않고 견주기만 한다.
# sqlc가 없으면 로컬에서는 건너뛰고, CI에서는 실패시킨다. CI는 `make generate` 뒤에 바뀐 파일이 없는지로 같은 것을 본다.
.PHONY: sqlc-diff
sqlc-diff: ## sqlc 생성 코드가 SQL 원본과 어긋나지 않았는지 본다
	@if command -v sqlc >/dev/null 2>&1; then \
		echo "==> server: sqlc diff"; \
		cd server && sqlc diff; \
	elif [ -n "$${CI:-}" ]; then \
		echo "!! sqlc가 설치되어 있지 않다"; exit 1; \
	else \
		echo "-- sqlc가 설치되어 있지 않아 건너뜀 (생성 코드가 최신인지 확인하지 못했다)"; \
	fi

# 명세(server/openapi.yaml)를 고치고 서버 코드를 다시 만들지 않은 경우를 잡는다. 파일을 고치지 않고 견주기만 한다.
# 임시 폴더에 새로 만들어 저장소의 파일과 견준다. 도구는 go.mod에 등록되어 있어서 따로 설치할 것이 없다.
.PHONY: oapi-diff
oapi-diff: ## 명세에서 만든 서버 코드가 명세와 어긋나지 않았는지 본다
	@echo "==> server: oapi-codegen diff"
	@cd server/internal/api && tmp="$$(mktemp -d)" && trap 'rm -rf "$$tmp"' EXIT && \
		sed "s|^output: .*|output: $$tmp/api.gen.go|" oapi-codegen.yaml > "$$tmp/config.yaml" && \
		go tool oapi-codegen -config "$$tmp/config.yaml" ../../openapi.yaml && \
		if ! diff -q api.gen.go "$$tmp/api.gen.go" >/dev/null; then \
			echo "!! server/internal/api/api.gen.go가 명세와 다르다. make generate를 돌린다"; exit 1; \
		fi

# CI의 server, generated, web 검사가 보는 것을 로컬에서 미리 본다. 생성은 하지 않는다. 생성 코드가 낡았으면 고치지 않고 실패한다.
#   서버: sqlc diff, oapi-codegen diff, go vet, golangci-lint, go test
#   웹:   lint, typecheck, format:check, test, build
# golangci-lint가 설치되어 있지 않으면 그 단계는 건너뛴다. 그때는 `make lint-docker`를 따로 돌려야 CI와 같아진다.
# 취약점 검사, 이미지 빌드, 배포 구성 검사는 여기에 없다. 오래 걸리거나 다른 도구가 필요해서 CI에만 둔다.
#
# 도구나 의존성이 없는 단계는 로컬에서 실패하지 않고 건너뛴다. 받은 직후에는 거의 모든 단계가 그렇게 넘어가는데,
# 그대로 "통과"라고만 하면 아무것도 확인하지 않은 채 다 확인한 것으로 읽힌다. 그래서 마지막 줄에 건너뛴 단계를 적는다.
# DB가 필요한 서버 테스트는 Docker가 없으면 go test가 조용히 건너뛰므로(-v 없이는 보이지 않는다) 여기서 따로 본다.
.PHONY: check
check: sqlc-diff oapi-diff lint typecheck format-check test build-web ## 끝내기 전에 돌리는 전체 검사 (CI와 같은 범위)
	@skipped=""; \
	command -v sqlc >/dev/null 2>&1 || skipped="$$skipped sqlc-diff"; \
	[ -d web/node_modules ] || skipped="$$skipped web(lint,typecheck,format,test,build)"; \
	docker info >/dev/null 2>&1 || skipped="$$skipped DB가-필요한-서버-테스트"; \
	if [ -n "$$skipped" ]; then \
		echo "==> check: 돌린 단계는 통과했다. 건너뛴 단계:$$skipped"; \
	else \
		echo "==> check 통과"; \
	fi
	@[ -z "$${GEMINI_API_KEY:-}" ] || echo "-- 참고: 실제 모델을 부르는 평가는 여기서 돌지 않는다. 돌리려면 make eval"

# 실제 모델을 부르는 평가를 한자리에 모은다. 돈과 시간이 들기 때문에 check에서는 돌지 않고, 여기서만 돈다.
# -race를 걸지 않는다. 모델을 기다리는 시간이 대부분이라 경합 검사가 잡을 것이 없고 시간만 배로 든다.
.PHONY: eval
eval: ## 실제 모델을 부르는 평가를 한꺼번에 돌린다 (돈과 시간이 든다)
	@if [ -z "$${GEMINI_API_KEY:-}" ]; then echo "!! GEMINI_API_KEY가 없다"; exit 1; fi
	@echo "==> server: 실제 모델 평가 (gate, engine, reply, diary, analysis, gemini)"
	@cd server && GATE_LIVE_EVAL=1 GATE_INDEP=1 ENGINE_REDTEAM=1 REPLY_LIVE_EVAL=1 \
		SIGNAL_ACCURACY_TESTS=1 GEMINI_LIVE_TESTS=1 GEMINI_LIVE_EVAL=1 \
		go test -count=1 -v -timeout 30m ./internal/gate/ ./internal/engine/ ./internal/reply/ ./internal/diary/ ./internal/analysis/ ./internal/ai/gemini/

# DB, API 서버, 작업자, 빌드된 웹앱을 띄우고 브라우저(chromium)로 가입부터 일기까지 밟아 본 뒤 모두 내린다.
# 일기 초안은 작업자가 만들기 때문에 서버만으로는 대화가 일기까지 가지 않는다.
# 개발용 데이터베이스는 건드리지 않는다. 같은 DB 서버에 테스트용 데이터베이스를 돌릴 때마다 새로 만들고, 통과하면 지운다.
# Playwright에 넘길 인자는 ARGS로 준다. 예: make e2e ARGS="--headed"
#
# 언어 모델은 언제나 정해 둔 답(scripted)으로 돈다. .env에 Gemini 키가 있으면 서버는 실제 모델을 고르는데,
# 그러면 돌릴 때마다 답이 달라지고 돈이 들고 네트워크가 없으면 실패한다. 화면의 흐름을 보는 테스트가 기댈 것이 아니다.
.PHONY: e2e
e2e: ## 브라우저 흐름 테스트 (DB, 서버, 작업자, 웹을 띄우고 Playwright를 돌린 뒤 내린다)
	@AI_PROVIDER=scripted bash web/e2e/run.sh $(ARGS)
