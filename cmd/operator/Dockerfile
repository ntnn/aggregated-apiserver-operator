# check=error=true
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:ec47ea3725fea0cc88f6de94919aef19f40f12fc1ec0e2d1b3426dbf15ecca04 AS builder

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

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
WORKDIR /
COPY --from=builder /workspace/bin/operator /operator
USER 65532:65532

ENTRYPOINT ["/operator"]
