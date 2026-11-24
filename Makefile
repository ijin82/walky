BIN       := bin/walky
UI_BIN    := bin/walky-ui
GO_DIR    := core
UI_DIR    := ui
PREFIX    ?= /usr
DESTDIR   ?=
VERSION   ?= $(shell cat VERSION 2>/dev/null || echo 0.1.0-dev)
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
	-X walky/internal/version.Version=$(VERSION) \
	-X walky/internal/version.Commit=$(COMMIT) \
	-X walky/internal/version.Date=$(DATE)

.PHONY: all build build-core build-ui test cover vet fmt fmt-check check clean \
	install install-data install-local uninstall uninstall-local run-ui

all: build

build: build-core build-ui

build-core:
	cd $(GO_DIR) && go build -trimpath -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/walky

build-ui:
	$(MAKE) -C $(UI_DIR) build
	install -Dm755 $(UI_DIR)/build/walky-ui $(UI_BIN)

test:
	cd $(GO_DIR) && go test ./...

cover:
	cd $(GO_DIR) && go test -cover ./...

vet:
	cd $(GO_DIR) && go vet ./...

fmt:
	cd $(GO_DIR) && gofmt -w $$(find . -name '*.go')

fmt-check:
	@out=$$(cd $(GO_DIR) && gofmt -l $$(find . -name '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test build

run-ui: build-ui
	./$(UI_BIN)

clean:
	rm -rf bin
	$(MAKE) -C $(UI_DIR) clean

install: build
	install -Dm755 $(BIN) $(DESTDIR)$(PREFIX)/bin/walky
	install -Dm755 $(UI_BIN) $(DESTDIR)$(PREFIX)/bin/walky-ui
	install -Dm644 data/walky.desktop $(DESTDIR)$(PREFIX)/share/applications/walky.desktop

# Per-user deployment without root: binaries and the desktop entry land in
# ~/.local, so the freshly built build is what the user runs.
LOCAL_BIN   := $(HOME)/.local/bin
LOCAL_APPS  := $(HOME)/.local/share/applications

install-local: build
	install -Dm755 $(BIN) $(LOCAL_BIN)/walky
	install -Dm755 $(UI_BIN) $(LOCAL_BIN)/walky-ui
	install -Dm644 data/walky.desktop $(LOCAL_APPS)/walky.desktop
	install -Dm644 data/walky.service $(HOME)/.config/systemd/user/walky.service
	sed -i 's|/usr/bin/walky|%h/.local/bin/walky|' $(HOME)/.config/systemd/user/walky.service
	@command -v update-desktop-database >/dev/null 2>&1 && \
		update-desktop-database $(LOCAL_APPS) || true
	@systemctl --user daemon-reload 2>/dev/null || true
	@echo "installed: $(LOCAL_BIN)/walky $(LOCAL_BIN)/walky-ui $(HOME)/.config/systemd/user/walky.service"

uninstall-local:
	rm -f $(LOCAL_BIN)/walky $(LOCAL_BIN)/walky-ui
	rm -f $(LOCAL_APPS)/walky.desktop
	rm -f $(HOME)/.config/systemd/user/walky.service
	@systemctl --user daemon-reload 2>/dev/null || true

install-data:
	install -Dm644 data/walky.service $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	install -Dm644 data/90-walky-inotify.conf $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	install -Dm644 data/99-walky-usb.rules $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/walky $(DESTDIR)$(PREFIX)/bin/walky-ui
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	rm -f $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	rm -f $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules
