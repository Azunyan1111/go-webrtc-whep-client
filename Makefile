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

.PHONY: all whep-go whip-go clean fmt vet test help

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
	@echo "  clean               Remove built binaries"
	@echo "  fmt                 Format Go code"
	@echo "  vet                 Run go vet"
	@echo "  test                Run tests"
	@echo ""
	@echo "Platform: $(UNAME_S) $(UNAME_M)"

# Build WHEP client (pion/webrtc)
whep-go:
	$(GO) build $(GOFLAGS) -o $(WHEP_GO) ./cmd/whep-go

# Build WHIP client
whip-go:
	$(GO) build $(GOFLAGS) -o $(WHIP_GO) ./cmd/whip-go

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
