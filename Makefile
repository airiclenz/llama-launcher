BINARY     := llama-launcher
MCP_BINARY := llama-launcher-mcp
VERSION    := $(shell cat VERSION)
LDFLAGS    := -ldflags "-X github.com/airiclenz/llama-launcher/internal/launcher.Version=$(VERSION)"
MCP_LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

# The platforms ADR-0012 puts under contract: the package must compile on all
# three from whichever host runs the gate, so a portability regression fails
# here instead of in an importing client's CI.
GOOSES := darwin linux windows

.PHONY: build build-mcp test test-integration test-all cross check dist release-status release-smoke install clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

build-mcp:
	go build $(MCP_LDFLAGS) -o $(MCP_BINARY) ./cmd/llama-launcher-mcp

test:
	go test ./...

test-integration:
	go test -tags=integration -count=1 -timeout 5m -v ./internal/launcher/

test-all: test test-integration

# Cross-compile gate (ADR-0012). Per GOOS: the production build, then vet over
# the package *and its tests* — vet type-checks test files, which is what keeps
# the Layer-1 and Layer-2 suites free of unix-only calls. Nothing here starts a
# process, so it is safe for agents and hosts alike.
cross:
	@for goos in $(GOOSES); do \
		echo "==> GOOS=$$goos"; \
		GOOS=$$goos go build ./... || exit 1; \
		GOOS=$$goos go vet ./... || exit 1; \
		GOOS=$$goos go vet -tags=integration ./internal/launcher/ || exit 1; \
	done

check: test cross

# Release wrappers over cmd/release. RELEASE= is passed through verbatim and,
# left empty, the verb defaults to the VERSION file; it is a separate variable
# because VERSION= on the make line would override the one read above.

# Build the four darwin/linux archives and SHA256SUMS into dist/.
dist:
	go run ./cmd/release dist

# Report the release gate, the baseline and each publish step (read-only apart
# from fetching origin): make release-status [RELEASE=vX.Y.Z]
release-status:
	go run ./cmd/release status $(RELEASE)

# Verify a published release from the outside: make release-smoke [RELEASE=vX.Y.Z]
release-smoke:
	go run ./cmd/release smoke $(RELEASE)

install:
	@echo "Install via Homebrew:  brew upgrade llama-launcher"
	@echo "For local testing:     make build  &&  ./$(BINARY)"
	@exit 1

clean:
	@rm -f $(BINARY) $(MCP_BINARY)
