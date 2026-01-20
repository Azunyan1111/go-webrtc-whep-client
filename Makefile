# Makefile for go-webrtc-whep-client
#
# Targets:
#   all              - Build all binaries (whep-go, whip-go, whep-libwebrtc-go)
#   whep-go          - Build WHEP client (pion/webrtc)
#   whip-go          - Build WHIP client
#   whep-libwebrtc   - Build WHEP client (libwebrtc) - requires libwebrtc
#   libwebrtc-wrapper - Build libwebrtc Objective-C++ wrapper
#   download-libwebrtc - Download pre-built libwebrtc binaries
#   clean            - Remove built binaries
#   clean-all        - Remove binaries and libwebrtc downloads
#   fmt              - Format Go code
#   vet              - Run go vet
#   test             - Run tests

.PHONY: all whep-go whip-go whep-libwebrtc libwebrtc-wrapper \
        download-libwebrtc clean clean-all fmt vet test help

# Configuration
GO := go
GOFMT := gofmt
GOFLAGS := -v

# Output binaries
WHEP_GO := whep-go
WHIP_GO := whip-go
WHEP_LIBWEBRTC := whep-libwebrtc-go

# libwebrtc paths
LIBWEBRTC_DIR := webrtc-macos-arm64
LIBWEBRTC_INCLUDE := $(LIBWEBRTC_DIR)/webrtc/include
LIBWEBRTC_LIB := $(LIBWEBRTC_DIR)/webrtc/lib
LIBWEBRTC_CONFIG_SITE := $(LIBWEBRTC_INCLUDE)/buildtools/third_party/libc++/__config_site

# Wrapper paths
WRAPPER_DIR := internal/libwebrtc
WRAPPER_SRC := $(WRAPPER_DIR)/webrtc_objc_wrapper.mm
WRAPPER_OBJ := $(WRAPPER_DIR)/webrtc_objc_wrapper.o
WRAPPER_LIB := $(WRAPPER_DIR)/libwebrtc_objc_wrapper.a

# libwebrtc download URL (shiguredo-webrtc-build)
LIBWEBRTC_VERSION := m144.7559.2.1
LIBWEBRTC_URL_BASE := https://github.com/shiguredo-webrtc-build/webrtc-build/releases/download/$(LIBWEBRTC_VERSION)
LIBWEBRTC_URL_MACOS_ARM64 := $(LIBWEBRTC_URL_BASE)/webrtc.macos_arm64.tar.gz
LIBWEBRTC_URL_LINUX_X64 := $(LIBWEBRTC_URL_BASE)/webrtc.ubuntu-24.04_x86_64.tar.gz

# C++ compiler flags for libwebrtc wrapper
CXX := c++
CXXFLAGS := -std=c++17 -stdlib=libc++ \
            -include $(LIBWEBRTC_CONFIG_SITE) \
            -I$(LIBWEBRTC_INCLUDE) \
            -I$(LIBWEBRTC_INCLUDE)/third_party/abseil-cpp \
            -I$(LIBWEBRTC_INCLUDE)/third_party/libyuv/include \
            -DWEBRTC_MAC -DWEBRTC_POSIX

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
	@echo "  whep-libwebrtc      Build WHEP client (libwebrtc) - requires libwebrtc"
	@echo "  libwebrtc-wrapper   Build libwebrtc Objective-C++ wrapper only"
	@echo "  download-libwebrtc  Download pre-built libwebrtc (macOS arm64)"
	@echo "  clean               Remove built binaries"
	@echo "  clean-all           Remove binaries and libwebrtc downloads"
	@echo "  fmt                 Format Go code"
	@echo "  vet                 Run go vet"
	@echo "  test                Run tests"
	@echo ""
	@echo "libwebrtc version: $(LIBWEBRTC_VERSION)"
	@echo "Platform: $(UNAME_S) $(UNAME_M)"

# Build WHEP client (pion/webrtc)
whep-go:
	$(GO) build $(GOFLAGS) -o $(WHEP_GO) ./cmd/whep-go

# Build WHIP client
whip-go:
	$(GO) build $(GOFLAGS) -o $(WHIP_GO) ./cmd/whip-go

# Build WHEP client (libwebrtc)
whep-libwebrtc: libwebrtc-wrapper
	$(GO) build $(GOFLAGS) -o $(WHEP_LIBWEBRTC) ./cmd/whep-libwebrtc-go

# Build libwebrtc Objective-C++ wrapper
libwebrtc-wrapper: $(WRAPPER_LIB)

$(WRAPPER_LIB): $(WRAPPER_OBJ)
	ar rcs $@ $<
	@echo "Built: $@"

$(WRAPPER_OBJ): $(WRAPPER_SRC) $(LIBWEBRTC_CONFIG_SITE)
	$(CXX) -c $< -o $@ $(CXXFLAGS)
	@echo "Compiled: $@"
	@echo "Verifying ABI namespace..."
	@nm $@ | grep " U " | grep "__1" | wc -l | xargs -I {} sh -c '[ {} -eq 0 ] && echo "OK: No std::__1 symbols found" || (echo "ERROR: Found std::__1 symbols - ABI mismatch!" && exit 1)'

# Check if libwebrtc exists
$(LIBWEBRTC_CONFIG_SITE):
	@echo "ERROR: libwebrtc not found at $(LIBWEBRTC_DIR)"
	@echo "Run 'make download-libwebrtc' to download it"
	@exit 1

# __config_site content for ABI compatibility
define CONFIG_SITE_CONTENT
//===----------------------------------------------------------------------===//
//
// Part of the LLVM Project, under the Apache License v2.0 with LLVM Exceptions.
// See https://llvm.org/LICENSE.txt for license information.
// SPDX-License-Identifier: Apache-2.0 WITH LLVM-exception
//
//===----------------------------------------------------------------------===//

#ifndef _LIBCPP___CONFIG_SITE
#define _LIBCPP___CONFIG_SITE

#define _LIBCPP_ABI_VERSION 2
#define _LIBCPP_ABI_NAMESPACE __Cr

#define _LIBCPP_DISABLE_VISIBILITY_ANNOTATIONS
#define _LIBCPP_HAS_NO_VENDOR_AVAILABILITY_ANNOTATIONS

#endif // _LIBCPP___CONFIG_SITE
endef
export CONFIG_SITE_CONTENT

# Download libwebrtc for all supported platforms
download-libwebrtc:
	@echo "Downloading libwebrtc $(LIBWEBRTC_VERSION) for macOS arm64..."
	@mkdir -p $(LIBWEBRTC_DIR)
	curl -L -o /tmp/libwebrtc-macos.tar.gz $(LIBWEBRTC_URL_MACOS_ARM64)
	tar xzf /tmp/libwebrtc-macos.tar.gz -C $(LIBWEBRTC_DIR)
	rm /tmp/libwebrtc-macos.tar.gz
	@rm -rf $(LIBWEBRTC_DIR)/webrtc/Frameworks
	@mkdir -p $(LIBWEBRTC_DIR)/webrtc/include/buildtools/third_party/libc++
	@echo "$$CONFIG_SITE_CONTENT" > $(LIBWEBRTC_DIR)/webrtc/include/buildtools/third_party/libc++/__config_site
	@echo "Downloaded to $(LIBWEBRTC_DIR)"
	@echo ""
	@echo "Downloading libwebrtc $(LIBWEBRTC_VERSION) for Linux x86_64..."
	@mkdir -p webrtc-ubuntu-x86_64
	curl -L -o /tmp/libwebrtc-linux.tar.gz $(LIBWEBRTC_URL_LINUX_X64)
	tar xzf /tmp/libwebrtc-linux.tar.gz -C webrtc-ubuntu-x86_64
	rm /tmp/libwebrtc-linux.tar.gz
	@mkdir -p webrtc-ubuntu-x86_64/webrtc/include/buildtools/third_party/libc++
	@echo "$$CONFIG_SITE_CONTENT" > webrtc-ubuntu-x86_64/webrtc/include/buildtools/third_party/libc++/__config_site
	@echo "Downloaded to webrtc-ubuntu-x86_64"

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
	rm -f $(WHEP_GO) $(WHIP_GO) $(WHEP_LIBWEBRTC)
	rm -f $(WRAPPER_OBJ) $(WRAPPER_LIB)
	rm -f go-webrtc-whep-client

# Clean everything including libwebrtc downloads
clean-all: clean
	rm -rf webrtc-macos-arm64 webrtc-ubuntu-x86_64
	rm -f /tmp/libwebrtc.tar.gz

# Rebuild libwebrtc wrapper from scratch
rebuild-wrapper: clean-wrapper libwebrtc-wrapper

clean-wrapper:
	rm -f $(WRAPPER_OBJ) $(WRAPPER_LIB)

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
	@echo "Checking libwebrtc..."
	@test -f $(LIBWEBRTC_LIB)/libwebrtc.a && echo "OK: libwebrtc.a found" || echo "MISSING: libwebrtc.a"
	@test -f $(LIBWEBRTC_CONFIG_SITE) && echo "OK: __config_site found" || echo "MISSING: __config_site"
	@test -f $(WRAPPER_LIB) && echo "OK: libwebrtc_objc_wrapper.a found" || echo "MISSING: libwebrtc_objc_wrapper.a (run 'make libwebrtc-wrapper')"
