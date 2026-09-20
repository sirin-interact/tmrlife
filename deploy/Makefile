# 이미지 빌드와 쿠버네티스 배포 명령 모음. 저장소 루트에서 `make -C deploy help`로 목록을 본다.
# 설명은 deploy/README.md에 있다.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
K8S := $(ROOT)/deploy/k8s

TAG ?= dev
# 비워 두면 이 컴퓨터의 아키텍처로 만든다. 운영 노드가 amd64라면 PLATFORM=linux/amd64
PLATFORM ?=

# 로컬 명령은 현재 선택된 컨텍스트가 무엇이든 항상 이 컨텍스트에만 적용한다.
# 운영 클러스터를 보고 있는 터미널에서 실수로 로컬 구성을 올리는 일을 막는다.
LOCAL_CONTEXT ?= orbstack
LOCAL_NAMESPACE := naeil-local
# overlays/local의 PUBLIC_ORIGIN에 적힌 포트와 같아야 한다.
LOCAL_PORT ?= 8088
# overlays/local의 images에 적힌 태그다. `make images`의 기본 TAG와 같다.
LOCAL_TAG := dev

# 운영 명령은 컨텍스트를 반드시 직접 적게 한다. 기본값을 두지 않는다.
PROD_CONTEXT ?=
PROD_NAMESPACE := naeil
PROD_KUSTOMIZATION := $(K8S)/overlays/prod/kustomization.yaml
# 가입 화면의 동의 문구다. 초안 표시가 켜져 있는 동안에는 운영 배포를 멈춘다.
CONSENT_COPY := $(ROOT)/web/src/content/consentCopy.ts
# 검토 전의 문구로 띄워도 되는 자리(실제 가입을 받지 않는 시연)에서만 1로 준다.
ALLOW_DRAFT_CONSENT ?=

# CI와 로컬이 같은 결과를 내도록 버전을 고정한다.
KUBECONFORM_IMAGE ?= ghcr.io/yannh/kubeconform:v0.8.0
# 설치된 실행 파일을 쓰려면 KUBECONFORM=kubeconform 으로 바꾼다.
KUBECONFORM ?= docker run --rm -i $(KUBECONFORM_IMAGE)
# 이 버전의 API에 없는 필드를 쓰면 검사가 실패한다. 운영 클러스터보다 높게 잡지 않는다.
K8S_SCHEMA_VERSION ?= 1.33.0
# 기본 스키마에 없는 종류(CloudNativePG, cert-manager, Traefik)는 공개된 CRD 스키마 모음에서 찾는다.
# 모음은 커밋으로 고정한다. 움직이는 가지(main)를 보면 구성을 고치지 않았는데도 어느 날 결과가 달라진다.
# CloudNativePG, cert-manager, Traefik을 올려서 새 필드를 쓰게 되면 이 값을 그때의 커밋으로 올린다.
CRD_CATALOG_REF ?= ad3b08c5045129d7bb1eeffd8e61719b2c8dd1e2
# 스키마를 찾지 못한 종류가 있으면 실패한다(-ignore-missing-schemas를 주지 않는다).
# 건너뛰게 두면 검사하지 않은 자원이 있어도 "Invalid: 0"으로 끝나서 다 검사한 것으로 읽힌다.
KUBECONFORM_FLAGS := -strict -summary \
	-kubernetes-version $(K8S_SCHEMA_VERSION) \
	-schema-location default \
	-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/$(CRD_CATALOG_REF)/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'

# rollout status는 한 번에 하나만 받는다. 이 목록을 돌면서 부른다.
# 도는 자리마다 `|| exit 1`을 붙인다. 셸은 반복문의 결과로 마지막 명령의 것만 돌려주므로,
# 없으면 앞의 Deployment가 뜨지 못해도 마지막 것만 뜨면 성공으로 끝난다.
# 위의 .SHELLFLAGS(-e)에 기대지 않는다. macOS에 들어 있는 make 3.81은 그 설정을 읽지 않는다.
DEPLOYMENTS := naeil-server naeil-worker naeil-web

# 마이그레이션 Job은 DB가 뜨기 전에 시작하면 실패한 파드를 남기고 다시 시도한다.
# `kubectl logs job/naeil-migrate`는 그 가운데 아무 파드나 하나 골라서, 성공한 Job에서도 실패한 시도의 오류를 보여줄 수 있다.
# 레이블로 고르면 시도마다의 로그를 파드 이름과 함께 모두 볼 수 있다.
MIGRATE_SELECTOR := app.kubernetes.io/name=naeil-migrate

LOCAL_KUBECTL := kubectl --context $(LOCAL_CONTEXT) -n $(LOCAL_NAMESPACE)
PROD_KUBECTL = kubectl --context $(PROD_CONTEXT) -n $(PROD_NAMESPACE)

.PHONY: help
help: ## 이 도움말을 보여준다
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(firstword $(MAKEFILE_LIST))

# ---- 이미지 -------------------------------------------------------------------

.PHONY: images
images: image-server image-web ## 두 이미지를 만든다 (TAG=dev, PLATFORM=)

.PHONY: image-server
image-server:
	docker build $(if $(PLATFORM),--platform $(PLATFORM)) -t naeil-server:$(TAG) $(ROOT)/server

.PHONY: image-web
image-web:
	docker build $(if $(PLATFORM),--platform $(PLATFORM)) -t naeil-web:$(TAG) $(ROOT)/web

.PHONY: smoke-web
smoke-web: ## 웹 이미지를 운영과 같은 제약으로 띄워 응답을 검사한다
	@bash $(ROOT)/deploy/scripts/smoke-web-image.sh naeil-web:$(TAG)

# ---- 구성 확인 ----------------------------------------------------------------

.PHONY: render-local
render-local: ## 로컬 구성을 펼쳐서 출력한다
	@kubectl kustomize $(K8S)/overlays/local

.PHONY: render-prod
render-prod: ## 운영 구성을 펼쳐서 출력한다
	@kubectl kustomize $(K8S)/overlays/prod

.PHONY: check-config
check-config: ## 서버가 읽는 설정 값이 배포 구성에 모두 있는지 본다
	@bash $(ROOT)/deploy/scripts/check-config-keys.sh

.PHONY: validate
validate: check-config ## 두 구성을 펼쳐서 스키마에 맞는지 검사한다 (클러스터에 닿지 않는다)
	@# pipefail을 여기서 직접 켠다. 없으면 구성을 펼치다 실패해도 검사기가 빈 입력을 받고 "0개 모두 통과"로 끝난다.
	@set -o pipefail; for overlay in local prod; do \
		echo "==> overlays/$$overlay"; \
		kubectl kustomize $(K8S)/overlays/$$overlay | $(KUBECONFORM) $(KUBECONFORM_FLAGS) - || exit 1; \
	done
	@echo "==> base/secret.example.yaml"
	@$(KUBECONFORM) $(KUBECONFORM_FLAGS) - < $(K8S)/base/secret.example.yaml

# ---- 로컬 (OrbStack) ----------------------------------------------------------

.PHONY: local-dry-run
local-dry-run: ## 로컬 구성을 클러스터에 만들지 않고 검증만 한다
	@kubectl --context $(LOCAL_CONTEXT) apply --dry-run=client -k $(K8S)/overlays/local

.PHONY: local-images-check
local-images-check:
	@# 이미지가 없으면 파드가 받을 수 없는 이미지를 기다리다가 3분 뒤에야 실패한다. 올리기 전에 알려 준다.
	@for image in naeil-server:$(LOCAL_TAG) naeil-web:$(LOCAL_TAG); do \
		if ! docker image inspect $$image >/dev/null 2>&1; then \
			echo "!! $$image 이미지가 없다. 먼저 만든다: make -C deploy images"; exit 1; \
		fi; \
	done

.PHONY: local-up
local-up: local-images-check ## 로컬 구성을 올리고 모두 뜰 때까지 기다린다
	@# Job의 파드 정의는 바꿀 수 없다. 구성이 바뀐 뒤에 다시 올려도 거부되지 않게 옛 Job을 먼저 지운다.
	@$(LOCAL_KUBECTL) delete job naeil-migrate --ignore-not-found
	@kubectl --context $(LOCAL_CONTEXT) apply -k $(K8S)/overlays/local
	@$(LOCAL_KUBECTL) rollout status statefulset/naeil-postgres --timeout=180s
	@if ! $(LOCAL_KUBECTL) wait --for=condition=complete job/naeil-migrate --timeout=180s; then \
		echo "!! 마이그레이션이 끝나지 않았다. 시도마다의 로그:"; \
		$(LOCAL_KUBECTL) logs -l $(MIGRATE_SELECTOR) --prefix --tail=5 || true; \
		exit 1; \
	fi
	@for d in $(DEPLOYMENTS); do $(LOCAL_KUBECTL) rollout status deployment/$$d --timeout=180s || exit 1; done
	@echo "==> 떴다. 확인: make -C deploy local-smoke   브라우저로 보기: make -C deploy local-forward"

.PHONY: local-status
local-status: ## 로컬에 뜬 것들의 상태를 본다
	@$(LOCAL_KUBECTL) get pods,jobs,services,ingress,pvc,networkpolicy

.PHONY: local-migrate-logs
local-migrate-logs: ## 마이그레이션 Job이 시도마다 남긴 로그의 끝을 본다
	@$(LOCAL_KUBECTL) get pods -l $(MIGRATE_SELECTOR) --sort-by=.metadata.creationTimestamp
	@$(LOCAL_KUBECTL) logs -l $(MIGRATE_SELECTOR) --prefix --tail=2

.PHONY: local-forward
local-forward: ## 웹 파드를 http://naeil.localhost:8088 로 연다 (Ctrl+C로 끝낸다)
	@echo "==> http://naeil.localhost:$(LOCAL_PORT)"
	@$(LOCAL_KUBECTL) port-forward service/naeil-web $(LOCAL_PORT):8080

.PHONY: local-smoke
local-smoke: ## 웹 진입점으로 가입, 대화 채널, 로그아웃을 돌리고 로그에 이메일이 없는지 본다
	@bash $(ROOT)/deploy/scripts/smoke-local.sh $(LOCAL_CONTEXT) $(LOCAL_NAMESPACE)

.PHONY: local-restart
local-restart: ## 같은 태그로 이미지를 다시 만든 뒤 파드를 새로 띄운다
	@for d in $(DEPLOYMENTS); do $(LOCAL_KUBECTL) rollout restart deployment/$$d || exit 1; done
	@for d in $(DEPLOYMENTS); do $(LOCAL_KUBECTL) rollout status deployment/$$d --timeout=180s || exit 1; done

.PHONY: local-down
local-down: ## 로컬 구성을 DB 데이터까지 모두 지운다
	@kubectl --context $(LOCAL_CONTEXT) delete namespace $(LOCAL_NAMESPACE) --ignore-not-found

# ---- 운영 (K3s) ---------------------------------------------------------------

.PHONY: prod-set-tag
prod-set-tag: ## 운영 구성의 이미지 태그를 바꾼다 (TAG=<태그>)
	@if [ "$(TAG)" = "dev" ] || [ "$(TAG)" = "latest" ]; then echo "!! 운영에는 고정된 태그를 쓴다: make -C deploy prod-set-tag TAG=<태그>"; exit 1; fi
	@perl -pi -e 's/^(\s*newTag:\s*).*/$${1}"$(TAG)"/' $(PROD_KUSTOMIZATION)
	@grep -n 'newTag:' $(PROD_KUSTOMIZATION)

.PHONY: prod-check
prod-check:
	@if [ -z "$(PROD_CONTEXT)" ]; then echo "!! 어느 클러스터인지 직접 적는다: make -C deploy $(MAKECMDGOALS) PROD_CONTEXT=<컨텍스트>"; exit 1; fi
	@if grep -q 'newTag: *"\{0,1\}CHANGE-ME' $(PROD_KUSTOMIZATION); then echo "!! 이미지 태그를 먼저 정한다: make -C deploy prod-set-tag TAG=<태그>"; exit 1; fi
	@if grep -q 'app\.example\.com' $(K8S)/overlays/prod/params.yaml; then echo "!! overlays/prod/params.yaml의 도메인이 아직 예시 값이다"; exit 1; fi
	@# 동의는 가입할 때 한 번 받고 그 판(version)과 함께 남는다. 검토되지 않은 문구로 받은 동의는 나중에 문구를 고쳐도 되돌릴 수 없다.
	@# 보는 것은 지금 작업 폴더의 파일이다. 배포할 태그의 이미지가 이 상태에서 만들어졌다는 것까지는 확인하지 못한다.
	@if [ "$(ALLOW_DRAFT_CONSENT)" != "1" ] && grep -Eq '^export const CONSENT_COPY_IS_DRAFT *= *true' $(CONSENT_COPY); then \
		echo "!! 가입 화면의 동의 문구가 아직 초안이다 (web/src/content/consentCopy.ts의 CONSENT_COPY_IS_DRAFT)."; \
		echo "   검토된 문구로 바꾸고 false로 둔 뒤 이미지를 다시 만든다. 실제 가입을 받지 않는 시연이라면 ALLOW_DRAFT_CONSENT=1을 준다"; \
		exit 1; \
	fi

.PHONY: prod-diff
prod-diff: prod-check ## 운영 클러스터와 무엇이 달라지는지 본다 (바꾸지 않는다)
	@kubectl --context $(PROD_CONTEXT) diff -k $(K8S)/overlays/prod || [ $$? -eq 1 ]

.PHONY: prod-deploy
prod-deploy: prod-check ## 운영에 배포하고 마이그레이션과 롤아웃이 끝날 때까지 기다린다
	@# Job의 파드 정의는 바꿀 수 없다. 태그가 바뀐 채로 apply하면 거부되므로 옛 Job을 먼저 지운다.
	@$(PROD_KUBECTL) delete job naeil-migrate --ignore-not-found
	@kubectl --context $(PROD_CONTEXT) apply -k $(K8S)/overlays/prod
	@if ! $(PROD_KUBECTL) wait --for=condition=complete job/naeil-migrate --timeout=15m; then \
		echo "!! 마이그레이션이 끝나지 않았다. 새 파드는 스키마가 준비될 때까지 뜨지 않고, 옛 파드가 계속 요청을 받는다."; \
		$(PROD_KUBECTL) logs -l $(MIGRATE_SELECTOR) --prefix --tail=20 || true; \
		exit 1; \
	fi
	@for d in $(DEPLOYMENTS); do $(PROD_KUBECTL) rollout status deployment/$$d --timeout=10m || exit 1; done
	@echo "==> 배포 끝"
