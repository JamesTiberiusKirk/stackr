.PHONY: help install build build-cli build-site test test-integration lint templint generate db-sh clean docker-build check-templ \
        sandbox-up sandbox-down sandbox-update sandbox-status sandbox-network \
        sandbox-stack-up sandbox-stack-down sandbox-stack-update sandbox-stack-vars \
        sandbox-set-password sandbox-issue-invite

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

# ----------------------------------------------------------------------------
# Sandbox configuration — single source of truth for the local dev env. All
# sandbox-* targets below derive their behaviour from these. Switch envs by
# setting SANDBOX_ENV on the command line (e.g. `make sandbox-up SANDBOX_ENV=staging`)
# once we add more under sandbox/.
# ----------------------------------------------------------------------------
SANDBOX_ENV       ?= minimal
SANDBOX_REPO_ROOT := ./sandbox/$(SANDBOX_ENV)
SANDBOX_TOKEN     ?= dev-token-123
SANDBOX_ADMIN     ?= admin@stackr.local
SANDBOX_NETWORK   := stackr-sandbox

# go-run wrapper for invoking the stackr CLI against the sandbox. Use this
# everywhere a sandbox-* recipe needs the CLI; never duplicate the env-var
# prefix per-target. `go run` (rather than ./bin/stackr) means recipes work
# on a clean checkout without `make build` first.
SANDBOX_CLI := STACKR_REPO_ROOT=$(SANDBOX_REPO_ROOT) STACKR_TOKEN=$(SANDBOX_TOKEN) go run ./cmd/stackr

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
	@echo ""
	@echo "Sandbox (operates on sandbox/$(SANDBOX_ENV)):"
	@echo "  sandbox-up           - Bring up every stack in the sandbox (stackr all)"
	@echo "  sandbox-down         - Tear down every stack in the sandbox"
	@echo "  sandbox-update       - Pull images + restart only changed stacks"
	@echo "  sandbox-status       - Print discovered stacks (compose config) for the sandbox"
	@echo "  sandbox-stack-up STACK=<n>     - Bring up one stack"
	@echo "  sandbox-stack-down STACK=<n>   - Tear down one stack"
	@echo "  sandbox-stack-update STACK=<n> - Update one stack (pull + restart)"
	@echo "  sandbox-stack-vars STACK=<n>   - Print resolved env vars for one stack"
	@echo "  sandbox-set-password           - Set the sandbox admin password (interactive)"
	@echo "  sandbox-issue-invite [EMAIL=]  - Issue an invite token for a sandbox user"

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

# ----------------------------------------------------------------------------
# Sandbox targets
#
# All recipes below invoke the stackr CLI via go run with SANDBOX_REPO_ROOT
# pre-set, so they work on a clean checkout without a prior `make build` and
# stay coherent with whatever .stackr.yaml the sandbox env declares.
# ----------------------------------------------------------------------------

## sandbox-network: Ensure the shared docker network exists. Idempotent —
##                  every stack in the sandbox joins this network as
##                  external so traefik can route to them.
sandbox-network:
	@docker network inspect $(SANDBOX_NETWORK) >/dev/null 2>&1 || docker network create $(SANDBOX_NETWORK)

## sandbox-up: Bring up every stack declared under sandbox/$(SANDBOX_ENV)
##             Uses bare `stackr all` so a stopped stack with locally-cached
##             images still gets `up -d`. Use sandbox-update to also pull.
sandbox-up: sandbox-network
	$(SANDBOX_CLI) all
	@echo ""
	@echo "Sandbox up. Run the daemon natively in another shell: hamr dev"
	@echo "Then visit http://localhost:8080 and log in as $(SANDBOX_ADMIN)."
	@echo "Demo app routes through traefik: http://nginx.localhost"

## sandbox-down: Tear down every stack in the sandbox
sandbox-down:
	$(SANDBOX_CLI) all tear-down

## sandbox-update: Pull images and restart any stacks whose images changed.
##                 Skips stacks whose images haven't changed — does NOT
##                 reliably "make sure everything is up". Use sandbox-up
##                 for that.
sandbox-update: sandbox-network
	$(SANDBOX_CLI) all update

## sandbox-status: Print docker compose config for every discovered stack
sandbox-status:
	$(SANDBOX_CLI) all compose config | head -200

## sandbox-stack-up: Bring up one stack — STACK=<name> required
sandbox-stack-up: sandbox-network
	@if [ -z "$(STACK)" ]; then echo "STACK is required: make $@ STACK=nginx" >&2; exit 1; fi
	$(SANDBOX_CLI) $(STACK)

## sandbox-stack-down: Tear down one stack — STACK=<name> required
sandbox-stack-down:
	@if [ -z "$(STACK)" ]; then echo "STACK is required: make $@ STACK=nginx" >&2; exit 1; fi
	$(SANDBOX_CLI) $(STACK) tear-down

## sandbox-stack-update: Pull images and restart one stack — STACK=<name> required
sandbox-stack-update: sandbox-network
	@if [ -z "$(STACK)" ]; then echo "STACK is required: make $@ STACK=nginx" >&2; exit 1; fi
	$(SANDBOX_CLI) $(STACK) update

## sandbox-stack-vars: Print resolved env vars for one stack — STACK=<name> required
sandbox-stack-vars:
	@if [ -z "$(STACK)" ]; then echo "STACK is required: make $@ STACK=nginx" >&2; exit 1; fi
	$(SANDBOX_CLI) $(STACK) get-vars

## sandbox-set-password: Set the sandbox admin password (interactive, two prompts)
sandbox-set-password:
	$(SANDBOX_CLI) set-password $(SANDBOX_ADMIN)

## sandbox-issue-invite: Issue a one-time invite token; defaults to SANDBOX_ADMIN
sandbox-issue-invite:
	$(SANDBOX_CLI) issue-invite $(or $(EMAIL),$(SANDBOX_ADMIN))
