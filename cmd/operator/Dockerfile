# check=error=true
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS builder

WORKDIR /workspace

COPY apis/go.mod apis/go.sum ./apis/
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,id=gomod-aggregated-apiserver-operator \
    go mod download && go mod verify

COPY . ./

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod,id=gomod-aggregated-apiserver-operator \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-aggregated-apiserver-operator \
    CGO_ENABLED=0 \
    GOCACHE=/root/.cache/go-build \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build -o ./bin/operator ./cmd/operator

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /workspace/bin/operator /operator
USER 65532:65532

ENTRYPOINT ["/operator"]
