GO ?= go
TOOLS_DIR = hack/tools
MODULES = . ./apis

IMAGE_REGISTRY ?= ghcr.io/ntnn/aggregated-apiserver-operator
IMAGE_TAG ?= latest
IMAGE_PLATFORMS ?= linux/amd64,linux/arm64
COMMANDS = api-aggregator operator

GOLANGCI_LINT_VER := 2.13.2
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint-$(GOLANGCI_LINT_VER)

.PHONY: all
all: lint-fix test test-integration

.PHONY: build
build:
	@for mod in $(MODULES); do \
		echo "build $$mod"; \
		(cd $$mod && $(GO) build ./...) || exit 1; \
	done
	@for cmd in $(COMMANDS); do \
		echo "build bin/$$cmd"; \
		$(GO) build -o bin/$$cmd ./cmd/$$cmd || exit 1; \
	done

.PHONY: images
images:
	@for cmd in $(COMMANDS); do \
		echo "image $(IMAGE_REGISTRY)/$$cmd:$(IMAGE_TAG)"; \
		docker buildx build -f cmd/$$cmd/Dockerfile -t $(IMAGE_REGISTRY)/$$cmd:$(IMAGE_TAG) --load . || exit 1; \
	done

.PHONY: images-push
images-push:
	@for cmd in $(COMMANDS); do \
		echo "push $(IMAGE_REGISTRY)/$$cmd:$(IMAGE_TAG) [$(IMAGE_PLATFORMS)]"; \
		docker buildx build -f cmd/$$cmd/Dockerfile -t $(IMAGE_REGISTRY)/$$cmd:$(IMAGE_TAG) --platform $(IMAGE_PLATFORMS) --push . || exit 1; \
	done

.PHONY: test
test:
	$(GO) test ./cmd/... ./pkg/...
	cd apis && $(GO) test ./...

.PHONY: test-integration
test-integration:
	$(GO) test -timeout 30m ./test/integration/...

.PHONY: test-e2e
test-e2e:
	$(GO) test -timeout 30m ./test/e2e/...

.PHONY: tidy
tidy:
	@for mod in $(MODULES); do \
		echo "tidy $$mod"; \
		(cd $$mod && $(GO) mod tidy) || exit 1; \
	done

.PHONY: lint
lint: $(GOLANGCI_LINT)
	@for mod in $(MODULES); do \
		echo "lint $$mod"; \
		(cd $$mod && $(CURDIR)/$(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) ./...) || exit 1; \
	done

.PHONY: lint-fix
lint-fix: override GOLANGCI_LINT_FLAGS := $(GOLANGCI_LINT_FLAGS) --fix
lint-fix: lint

.PHONY: generate
generate:
	cd apis && $(GO) tool controller-gen object paths=./...
	cd apis && $(GO) tool controller-gen crd paths=./... output:crd:dir=../config/crd

$(GOLANGCI_LINT):
	mkdir -p $(TOOLS_DIR)
	$(GO) tool codeberg.org/ntnn/mindl download -common -out $@ -tool golangci-lint -version $(GOLANGCI_LINT_VER)
	ln -sf $(notdir $@) $(TOOLS_DIR)/golangci-lint
