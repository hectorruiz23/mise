GO ?= go
PANEL_DIR ?= ../web
DIST_DIR := internal/panel/dist
BUILD_DIR := build

.PHONY: verify test accept dev build panel stage-panel build-all build-arm64 release install-hooks

REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null)

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
	CGO_ENABLED=0 $(GO) build ./...

panel:
	cd $(PANEL_DIR) && npm ci && npm run build

stage-panel:
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp -R $(PANEL_DIR)/dist/. $(DIST_DIR)/
	touch $(DIST_DIR)/.gitkeep

build-all: panel stage-panel
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BUILD_DIR)/mise ./cmd/mise

build-arm64:
	mkdir -p $(BUILD_DIR)/.tmp $(BUILD_DIR)/.gocache
	TMPDIR="$(CURDIR)/$(BUILD_DIR)/.tmp" GOCACHE="$(CURDIR)/$(BUILD_DIR)/.gocache" \
		CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/mise-linux-arm64 ./cmd/mise

release: build-arm64
	cd $(BUILD_DIR) && sha256sum mise-linux-arm64 > mise-linux-arm64.sha256
	cd $(BUILD_DIR) && LC_ALL=C sha256sum -c mise-linux-arm64.sha256
