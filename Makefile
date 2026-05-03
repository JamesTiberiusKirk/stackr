.PHONY: help install build build-cli build-site test test-integration lint templint generate db-sh clean docker-build sandbox-up sandbox-down check-templ

# Force bash so the ENV_LOAD eval works cross-shell.
SHELL := bash
.SHELLFLAGS := -ec

# Tool versions are pinned in go.mod so `make install` matches the repo's deps.
HAMR_VERSION  := $(shell grep 'github.com/FyrmForge/hamr ' go.mod | awk '{print $$2}')
TEMPL_VERSION := $(shell grep 'github.com/a-h/templ ' go.mod | awk '{print $$2}')

# Build version stamp for the daemon (set via -ldflags).
VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")

# ENV_LOAD: prefix targets that need hamr dev's port-walked .env values.
# `hamr env --export` reads .hamr/walks.json + .env and emits export lines for
# values that were rewritten. Empty output = no-op when hamr dev isn't running.
ENV_LOAD := eval "$$(hamr env --export 2>/dev/null || true)";

## help: Show this help
help:
	@echo "Available targets:"
	@echo "  install            - Install dev tools (templ, hamr) at versions pinned in go.mod"
	@echo "  build              - Build both CLI and daemon binaries"
	@echo "  build-cli          - Build the stackr CLI"
	@echo "  build-site         - Build the daemon (templ generate + go build)"
	@echo "  test               - Run unit tests (templ generate first)"
	@echo "  test-integration   - Run integration tests (requires Docker)"
	@echo "  lint               - Run golangci-lint"
	@echo "  templint           - Lint .templ files"
	@echo "  generate           - Run the daemon's static page generator"
	@echo "  db-sh              - Open sqlite3 shell to local dev DB"
	@echo "  clean              - Remove build artifacts"
	@echo "  docker-build       - Build the Docker image"
	@echo "  sandbox-up         - Start traefik in front of the local sandbox/minimal env"
	@echo "  sandbox-down       - Stop traefik"

## install: Install development dependencies pinned in go.mod
install:
	go install github.com/FyrmForge/hamr/cmd/hamr@$(HAMR_VERSION)
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

## check-templ: Verify templ is installed
check-templ:
	@command -v templ >/dev/null 2>&1 || { echo "templ not found. Run: make install" >&2; exit 1; }

## build: Build both binaries
build: build-cli build-site

## build-cli: Build the stackr CLI
build-cli:
	@echo "Building stackr CLI..."
	go build -o bin/stackr ./cmd/stackr

## build-site: Build the daemon (formerly stackrd) — generates templ + static first
build-site: check-templ
	@echo "Building daemon (cmd/stackrd)..."
	templ generate
	hamr gen static
	go build -ldflags "-X main.version=$(VERSION)" -o bin/stackrd ./cmd/stackrd

## generate: Run the daemon's static page generator
generate:
	$(ENV_LOAD) ./bin/stackrd --generate

## test: Run unit tests
test: check-templ
	templ generate
	go test ./...

## test-integration: Run integration tests (requires Docker)
test-integration:
	go test -v -tags=integration -timeout=10m ./...

## lint: Run golangci-lint
lint:
	golangci-lint run

## templint: Lint .templ files for common issues
templint:
	hamr lint templ

## db-sh: Open an interactive shell to the local dev database
db-sh:
	$(ENV_LOAD) ./scripts/db-shell.sh

## clean: Remove build artifacts
clean:
	rm -rf bin/ dist/ generated/ coverage.out coverage.html
	rm -f stackr stackrd

## docker-build: Build Docker image
docker-build:
	docker build -t ghcr.io/jamestiberiuskirk/stackrd:latest .

# Path to the local sandbox compose used by the targets below. Targets a
# single environment (sandbox/minimal); duplicate these with a different
# SANDBOX var if you add more sandbox envs later.
SANDBOX_COMPOSE := sandbox/minimal/stacks/stackr/docker-compose.yml

## sandbox-up: Start traefik in front of the local sandbox/minimal env
sandbox-up:
	docker compose -f $(SANDBOX_COMPOSE) up -d traefik
	@echo ""
	@echo "Traefik listening on http://localhost (dashboard: http://localhost:8081)."
	@echo "Run the daemon natively in another shell: hamr dev"
	@echo "Then add 'stackr.localhost' to /etc/hosts (or use --resolve) and try:"
	@echo "  curl -H 'Host: stackr.localhost' -H 'Authorization: Bearer dev-token-123' http://localhost/api/stacks"

## sandbox-down: Stop the local sandbox traefik
sandbox-down:
	docker compose -f $(SANDBOX_COMPOSE) down
