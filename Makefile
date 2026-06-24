# env-sync Makefile
#
# Common targets:
#   make build           build the binary into ./bin/env-sync
#   make test            run the test suite
#   make install         install binary, config and systemd unit for the user
#   make uninstall       remove the installed user service and binary
#   make enable          enable + start the systemd --user service

BINARY      := env-sync
PKG         := ./cmd/env-sync
BIN_DIR     := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

PREFIX      := $(HOME)/.local
BINDEST     := $(PREFIX)/bin
CONFDEST    := $(HOME)/.config/env-sync
UNITDEST    := $(HOME)/.config/systemd/user

.PHONY: all build test vet fmt clean install uninstall enable disable

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf $(BIN_DIR)

# Install the binary, an initial config (kept if it already exists), and the
# systemd user unit.
install: build
	@install -Dm755 $(BIN_DIR)/$(BINARY) $(BINDEST)/$(BINARY)
	@mkdir -p $(CONFDEST)
	@if [ ! -f $(CONFDEST)/config.yaml ]; then \
		install -Dm644 config/config.example.yaml $(CONFDEST)/config.yaml; \
		echo "installed default config to $(CONFDEST)/config.yaml"; \
	else \
		echo "kept existing config at $(CONFDEST)/config.yaml"; \
	fi
	@install -Dm644 systemd/env-sync.service $(UNITDEST)/env-sync.service
	@systemctl --user daemon-reload || true
	@echo "Installed env-sync to $(BINDEST)/$(BINARY)"
	@echo "Enable with: make enable"

uninstall: disable
	@rm -f $(BINDEST)/$(BINARY)
	@rm -f $(UNITDEST)/env-sync.service
	@systemctl --user daemon-reload || true
	@echo "Uninstalled env-sync (config at $(CONFDEST) left in place)"

enable:
	systemctl --user enable --now env-sync.service
	@echo "Tip: run 'loginctl enable-linger $(USER)' to keep running after logout"

disable:
	-systemctl --user disable --now env-sync.service
