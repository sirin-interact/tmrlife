# syntax=docker/dockerfile:1

# API 서버와 작업자를 한 이미지에 담는다.
# 둘은 같은 코드와 같은 마이그레이션을 쓰므로 이미지를 나누면 버전이 어긋날 틈만 생긴다.
# 어느 쪽을 띄울지는 실행할 때 명령으로 고른다.
#
#   docker build -t naeil-server:dev server
#   docker run --rm naeil-server:dev migrate status
#   docker run --rm --entrypoint /app/worker naeil-server:dev

# 패치 버전까지 고정한다. 올릴 때는 이 줄만 바꾼다.
# CI도 이 줄을 읽어 같은 버전으로 시험하고 취약점을 검사한다. 그래야 검사한 표준 라이브러리가 이미지에 실린 것과 같다.
# CI가 줄 첫머리의 `ARG GO_VERSION=x.y.z` 꼴을 찾으므로 따옴표나 뒤따르는 주석을 붙이지 않는다.
ARG GO_VERSION=1.26.8

# 빌드는 빌드하는 기계의 아키텍처로 돌리고 결과물만 대상 아키텍처로 만든다.
# CGO를 끄면 Go가 스스로 교차 컴파일하므로 에뮬레이션 없이 amd64와 arm64 이미지를 만들 수 있다.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# GOTOOLCHAIN=local: go.mod이 더 높은 버전을 요구하면 다른 툴체인을 몰래 내려받지 말고 빌드를 실패시킨다.
# 그래야 위에 고정한 버전이 실제로 빌드에 쓰인 버전이다.
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false

COPY . .

# `go mod download`를 따로 돌리지 않는다. 그 명령은 테스트에만 쓰는 의존성까지 전부 내려받는다.
# 빌드에 필요한 모듈만 받게 두고, 받은 모듈과 컴파일 결과는 캐시 마운트로 다음 빌드에 넘긴다.
#
# -tags timetzdata: 시간대 데이터를 실행 파일 안에 넣는다.
# 기록 날짜의 경계를 Asia/Seoul 기준으로 계산하는데, 이 데이터가 없으면 time.LoadLocation이 실패한다.
# 바탕 이미지의 zoneinfo를 복사해 쓰는 방법도 있지만, 그러면 바탕 이미지를 바꿀 때마다
# 시간대 파일이 함께 들어 있는지 다시 확인해야 한다. 실행 파일에 넣어 두면 어디서 띄워도 같다.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata -ldflags='-s -w' -o /out/ ./cmd/server ./cmd/worker

# 셸도 패키지 관리자도 없는 바탕 이미지다. 외부 API를 부를 때 필요한 CA 인증서는 들어 있다.
FROM gcr.io/distroless/static-debian13:nonroot

LABEL org.opencontainers.image.title="naeil-server" \
      org.opencontainers.image.source="https://github.com/sirin-interact/tmrlife"

COPY --from=build /out/server /out/worker /app/

# 쿠버네티스의 runAsNonRoot 검사는 숫자 UID만 확인할 수 있다. 이름이 아니라 숫자로 적는다.
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/app/server"]
CMD ["serve"]
