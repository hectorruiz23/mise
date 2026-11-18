GO ?= go
PANEL_DIR ?= ../web
DIST_DIR := internal/panel/dist
BUILD_DIR := build

.PHONY: verify test accept dev build panel stage-panel build-all build-arm64 release install-hooks

REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null)
REVISION := $(shell git rev-parse HEAD 2>/dev/null)
TREE_STATE := $(shell if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then if [ -n "$$(git status --porcelain 2>/dev/null)" ]; then echo dirty; else echo clean; fi; else echo unknown; fi)
REPOSITORY := github.com/hectorruiz23/mise
LDFLAGS := -X mise/api/internal/buildinfo.revision=$(REVISION) -X mise/api/internal/buildinfo.treeState=$(TREE_STATE) -X mise/api/internal/buildinfo.repository=$(REPOSITORY)

install-hooks:
	@test -n "$(REPO_ROOT)" || { echo "install-hooks: run this inside the git repository" >&2; exit 1; }
	@mkdir -p "$(REPO_ROOT)/.git/hooks"
	@chmod +x "$(CURDIR)/scripts/pre-commit"
	@ln -sfn "$(CURDIR)/scripts/pre-commit" "$(REPO_ROOT)/.git/hooks/pre-commit"
	@echo "pre-commit installed: $(REPO_ROOT)/.git/hooks/pre-commit -> $(CURDIR)/scripts/pre-commit"

verify:
	@unformatted="$$(gofmt -l $$($(GO) list -f '{{.Dir}}' ./...))"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	$(GO) vet ./...
	$(GO) vet -tags dev ./...
	$(GO) test ./...

test:
	$(GO) test ./...

accept:
	bash scripts/accept.sh

dev: panel stage-panel
	@bash scripts/dev.sh

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/mise ./cmd/mise

panel:
	cd $(PANEL_DIR) && npm ci && npm run build

stage-panel:
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp -R $(PANEL_DIR)/dist/. $(DIST_DIR)/
	touch $(DIST_DIR)/.gitkeep

build-all: panel stage-panel
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/mise ./cmd/mise

build-arm64:
	mkdir -p $(BUILD_DIR)/.tmp $(BUILD_DIR)/.gocache
	TMPDIR="$(CURDIR)/$(BUILD_DIR)/.tmp" GOCACHE="$(CURDIR)/$(BUILD_DIR)/.gocache" \
		CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(BUILD_DIR)/mise-linux-arm64 ./cmd/mise

release: build-arm64
	cd $(BUILD_DIR) && sha256sum mise-linux-arm64 > mise-linux-arm64.sha256
	cd $(BUILD_DIR) && LC_ALL=C sha256sum -c mise-linux-arm64.sha256
