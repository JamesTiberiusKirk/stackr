.PHONY: help build test test-integration clean install lint docker-build dev dev-down dev-rebuild dev-logs

# Default target
help:
	@echo "Available targets:"
	@echo "  build            - Build CLI and daemon binaries"
	@echo "  test             - Run unit tests"
	@echo "  test-integration - Run integration tests (requires Docker)"
	@echo "  clean            - Remove build artifacts"
	@echo "  install          - Install binaries to GOPATH/bin"
	@echo "  lint             - Run golangci-lint"
	@echo "  docker-build     - Build Docker image"
	@echo ""
	@echo "  dev              - Start dev environment (build + run in Docker)"
	@echo "  dev-down         - Stop dev environment"
	@echo "  dev-rebuild      - Rebuild and restart dev environment"
	@echo "  dev-logs         - Tail dev container logs"

# Build binaries
build:
	@echo "Building stackr CLI..."
	go build -o bin/stackr ./cmd/stackr
	@echo "Building stackrd daemon..."
	go build -o bin/stackrd ./cmd/stackrd

# Run unit tests
test:
	go test -v ./...

# Run integration tests (requires Docker)
test-integration:
	go test -v -tags=integration -timeout=10m ./...

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f stackr stackrd

# Install binaries
install:
	go install ./cmd/stackr

# Run linter
lint:
	golangci-lint run

# Build Docker image
docker-build:
	docker build -t ghcr.io/jamestiberiuskirk/stackrd:latest .

# Dev environment
dev:
	docker compose -f docker-compose.dev.yml up --build -d
	@echo ""
	@echo "stackrd running at http://localhost:9090"
	@echo "  Health: curl http://localhost:9090/healthz"
	@echo "  Deploy: curl -X POST http://localhost:9090/deploy -H 'Authorization: Bearer dev-token-123' -H 'Content-Type: application/json' -d '{\"stack\":\"nginx\",\"tag\":\"alpine\"}'"
	@echo "  Logs:   make dev-logs"

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-rebuild:
	docker compose -f docker-compose.dev.yml up --build -d --force-recreate
	@echo "Rebuilt and restarted."

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f
