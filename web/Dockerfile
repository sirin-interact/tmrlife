# syntax=docker/dockerfile:1

# 웹앱을 빌드해 정적 파일 서버(Caddy)에 담는다.
#
#   docker build -t naeil-web:dev web
#   docker run --rm -p 8088:8080 naeil-web:dev

ARG NODE_VERSION=24
ARG CADDY_VERSION=2.11.4

# ---- 빌드 -----------------------------------------------------------------------
# 결과물이 정적 파일이라 아키텍처와 무관하다. 빌드하는 기계의 아키텍처로 한 번만 돌린다.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm-slim AS build

ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0

WORKDIR /app

# 의존성 목록이 바뀌지 않았으면 설치 층을 다시 만들지 않도록 이 두 파일만 먼저 복사한다.
# pnpm 버전은 package.json의 packageManager 값을 corepack이 읽어 맞춘다.
COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,id=naeil-pnpm-store,target=/pnpm/store \
    corepack enable \
    && pnpm install --frozen-lockfile --store-dir /pnpm/store

COPY . .
RUN pnpm run build

# ---- 설정 확인 ------------------------------------------------------------------
# 문법이 틀린 설정이 이미지에 들어가지 않게 빌드 중에 검사한다.
# 정렬이 어긋난 설정은 서버가 뜰 때마다 경고를 남기므로 함께 막는다. 고치는 법: caddy fmt --overwrite Caddyfile
# 검사만 하는 단계라 빌드하는 기계의 아키텍처로 돌린다.
FROM --platform=$BUILDPLATFORM caddy:${CADDY_VERSION}-alpine AS caddy-check
COPY Caddyfile /etc/caddy/Caddyfile
RUN caddy fmt --diff /etc/caddy/Caddyfile \
    && caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

# ---- 실행 파일 ------------------------------------------------------------------
FROM caddy:${CADDY_VERSION}-alpine AS caddy
# 공식 이미지는 1024번 아래 포트를 열 수 있도록 실행 파일에 파일 권한(cap_net_bind_service)을 붙여 둔다.
# 모든 권한을 버리고 권한 상승까지 막은 컨테이너에서는 커널이 그런 파일의 실행 자체를 거부한다.
# 8080번만 쓰므로 그 권한이 필요 없다. 확장 속성을 옮기지 않는 cp로 복사해 떼어 낸다.
RUN cp /usr/bin/caddy /caddy

# ---- 최종 이미지 ----------------------------------------------------------------
# Caddy는 정적으로 링크된 실행 파일이라 셸 없는 바탕 이미지에서 그대로 돈다.
FROM gcr.io/distroless/static-debian13:nonroot

LABEL org.opencontainers.image.title="naeil-web" \
      org.opencontainers.image.source="https://github.com/sirin-interact/tmrlife"

COPY --from=caddy /caddy /usr/bin/caddy
# 확장자로 Content-Type을 정할 때 쓰는 표다. 없으면 .webmanifest 같은 파일이 text/plain으로 나간다.
COPY --from=caddy /etc/mime.types /etc/mime.types
COPY --from=caddy-check /etc/caddy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv

# Caddy는 인증서를 다루지 않아도 시작할 때 데이터 디렉터리에 식별자와 잠금 파일을 만든다.
# 기본 위치는 홈 디렉터리인데 루트 파일 시스템이 읽기 전용이면 쓰지 못해 뜰 때마다 오류를 남긴다.
# /tmp로 옮겨 두고, 배포할 때 /tmp에만 쓰기 가능한 임시 볼륨을 붙인다.
ENV XDG_DATA_HOME=/tmp

# 쿠버네티스의 runAsNonRoot 검사는 숫자 UID만 확인할 수 있다. 이름이 아니라 숫자로 적는다.
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/usr/bin/caddy"]
CMD ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
