BINARY    := llama-launcher
VERSION   := $(shell cat VERSION)
LDFLAGS   := -ldflags "-X github.com/airiclenz/llama-launcher/internal/launcher.Version=$(VERSION)"

.PHONY: build install clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

install:
	@echo "Install via Homebrew:  brew upgrade llama-launcher"
	@echo "For local testing:     make build  &&  ./$(BINARY)"
	@exit 1

clean:
	@rm -f $(BINARY)
