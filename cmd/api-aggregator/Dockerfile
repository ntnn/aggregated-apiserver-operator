# check=error=true
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd AS builder

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
    go build -o ./bin/api-aggregator ./cmd/api-aggregator

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
WORKDIR /
COPY --from=builder /workspace/bin/api-aggregator /api-aggregator
USER 65532:65532

ENTRYPOINT ["/api-aggregator"]
