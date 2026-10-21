# check=error=true
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS builder

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

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
WORKDIR /
COPY --from=builder /workspace/bin/operator /operator
USER 65532:65532

ENTRYPOINT ["/operator"]
