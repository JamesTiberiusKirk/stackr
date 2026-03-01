.PHONY: help build test test-integration clean install lint docker-build

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
