# Makefile for go-webrtc-whep-client
#
# Targets:
#   all              - Build all binaries (whep-go, whip-go)
#   whep-go          - Build WHEP client (pion/webrtc)
#   whip-go          - Build WHIP client
#   clean            - Remove built binaries
#   fmt              - Format Go code
#   vet              - Run go vet
#   test             - Run tests

.PHONY: all whep-go whip-go clean fmt vet test help docker-linux-amd64

# Configuration
GO := go
GOFMT := gofmt
GOFLAGS := -v

# Output binaries
WHEP_GO := whep-go
WHIP_GO := whip-go

# Detect architecture
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Default target
all: whep-go whip-go

# Help target
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all                 Build whep-go and whip-go (pion/webrtc based)"
	@echo "  whep-go             Build WHEP client (pion/webrtc)"
	@echo "  whip-go             Build WHIP client"
	@echo "  docker-linux-amd64  Build for Ubuntu/Linux AMD64 using Docker"
	@echo "  clean               Remove built binaries"
	@echo "  fmt                 Format Go code"
	@echo "  vet                 Run go vet"
	@echo "  test                Run tests"
	@echo ""
	@echo "Platform: $(UNAME_S) $(UNAME_M)"

# Build WHEP client (pion/webrtc)
build:
	$(GO) build $(GOFLAGS) -o $(WHEP_GO) ./cmd/whep-go
	$(GO) build $(GOFLAGS) -o $(WHIP_GO) ./cmd/whip-go

# Build for Ubuntu/Linux AMD64 using Docker
docker-linux-amd64:
	docker build --platform linux/amd64 --target test -t go-webrtc-whep-client-builder -f Dockerfile.build .
	docker create --name go-webrtc-whep-client-tmp go-webrtc-whep-client-builder
	docker cp go-webrtc-whep-client-tmp:/app/whep-go-linux-amd64 ./
	docker cp go-webrtc-whep-client-tmp:/app/whip-go-linux-amd64 ./
	docker rm go-webrtc-whep-client-tmp

# Format Go code
fmt:
	$(GO) fmt ./...

# Run go vet
vet:
	$(GO) vet ./...

# Run tests
test:
	$(GO) test -v ./...

# Clean built binaries
clean:
	rm -f $(WHEP_GO) $(WHIP_GO)
	rm -f $(WHEP_GO)-linux-amd64 $(WHIP_GO)-linux-amd64
	rm -f go-webrtc-whep-client

# Development helpers
.PHONY: run-whep run-whip

run-whep:
	@echo "Usage: make run-whep URL=https://example.com/whep"
	@test -n "$(URL)" || (echo "ERROR: URL is required" && exit 1)
	$(GO) run ./cmd/whep-go $(URL)

run-whip:
	@echo "Usage: cat video.mkv | make run-whip URL=https://example.com/whip"
	@test -n "$(URL)" || (echo "ERROR: URL is required" && exit 1)
	$(GO) run ./cmd/whip-go $(URL)

# Check dependencies
.PHONY: deps check-deps

deps:
	$(GO) mod tidy
	$(GO) mod download

check-deps:
	@echo "Checking Go dependencies..."
	$(GO) mod verify
