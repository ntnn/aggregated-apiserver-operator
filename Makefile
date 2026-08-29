GO ?= go
TOOLS_DIR = hack/tools
MODULES = . ./apis

GOLANGCI_LINT_VER := 2.13.2
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint-$(GOLANGCI_LINT_VER)

.PHONY: all
all: lint-fix test test-integration test-e2e

.PHONY: build
build:
	@for mod in $(MODULES); do \
		echo "build $$mod"; \
		(cd $$mod && $(GO) build ./...) || exit 1; \
	done
	$(GO) build -o bin/api-aggregator ./cmd/api-aggregator

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
