BINARY    := llama-launcher
INSTALL   := $(HOME)/.local/bin
SHELL_RC  := $(HOME)/.zshrc
VERSION   := $(shell cat VERSION)
LDFLAGS   := -ldflags "-X github.com/airiclenz/llama-launcher/internal/launcher.Version=$(VERSION)"

.PHONY: build install clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

install: build
	@mkdir -p $(INSTALL)
	@cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "Installed $(INSTALL)/$(BINARY)"
	@if ! echo "$$PATH" | tr ':' '\n' | grep -qx "$(INSTALL)"; then \
		echo 'export PATH="$$HOME/.local/bin:$$PATH"' >> $(SHELL_RC); \
		echo "Added $(INSTALL) to PATH in $(SHELL_RC) — restart your shell or run: source $(SHELL_RC)"; \
	fi

clean:
	@rm -f $(BINARY)
