# Global build configuration variables
BINARY_NAME=edge-telemetry-daemon
GO=go
DOCKER=docker

.PHONY: all build test bench lint docker-build fuzz clean

# Complete default target pipeline
all: test build

# Compiling native daemon executable (with injected git hash)
build:
	$(GO) build -v -ldflags="-w -s -X main.version=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/$(BINARY_NAME) ./cmd/agent

# Running test suite with race detector
test:
	$(GO) test -v -race ./...

# Measuring execution performance and allocations
bench:
	$(GO) test -bench=. -benchmem ./...

# Running static code analysis linters
lint:
	golangci-lint run ./...

# Building minimal container image
docker-build:
	$(DOCKER) build -t $(BINARY_NAME):latest .

# Fuzz testing
fuzz:
	@chmod +x ./fuzz_all.sh && ./fuzz_all.sh

# Cleaning build artifacts
clean:
	rm -rf bin/