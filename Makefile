BINARY     := llama-launcher
MCP_BINARY := llama-launcher-mcp
VERSION    := $(shell cat VERSION)
LDFLAGS    := -ldflags "-X github.com/airiclenz/llama-launcher/internal/launcher.Version=$(VERSION)"
MCP_LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

.PHONY: build build-mcp install clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

build-mcp:
	go build $(MCP_LDFLAGS) -o $(MCP_BINARY) ./cmd/llama-launcher-mcp

install:
	@echo "Install via Homebrew:  brew upgrade llama-launcher"
	@echo "For local testing:     make build  &&  ./$(BINARY)"
	@exit 1

clean:
	@rm -f $(BINARY) $(MCP_BINARY)
