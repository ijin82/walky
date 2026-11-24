BIN       := bin/walky
UI_BIN    := bin/walky-ui
APP_ID    := io.github.ijin.Walky
ICON_SRC  := data/walky.svg
ICON_SYMBOLIC := data/walky-symbolic.svg
ICON_PAUSED_SYMBOLIC := data/walky-paused-symbolic.svg
ICON_OFFLINE_SYMBOLIC := data/walky-offline-symbolic.svg
ICON_SIZES := 16 24 32 48 64 128 256
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
	install -Dm644 data/$(APP_ID).desktop $(DESTDIR)$(PREFIX)/share/applications/$(APP_ID).desktop
	rm -f $(DESTDIR)$(PREFIX)/share/applications/walky.desktop
	$(call install_icons,$(ICON_DIR_SYS))

# Per-user deployment without root: binaries and the desktop entry land in
# ~/.local, so the freshly built build is what the user runs.
LOCAL_BIN   := $(HOME)/.local/bin
LOCAL_APPS  := $(HOME)/.local/share/applications
ICON_DIR_SYS   := $(DESTDIR)$(PREFIX)/share/icons/hicolor
ICON_DIR_LOCAL := $(HOME)/.local/share/icons/hicolor

# Install the scalable icon plus raster sizes when ImageMagick is available.
define install_icons
	install -Dm644 $(ICON_SRC) $(1)/scalable/apps/walky.svg
	install -Dm644 $(ICON_SRC) $(1)/scalable/apps/$(APP_ID).svg
	install -Dm644 $(ICON_SYMBOLIC) $(1)/scalable/apps/walky-symbolic.svg
	install -Dm644 $(ICON_PAUSED_SYMBOLIC) $(1)/scalable/apps/walky-paused-symbolic.svg
	install -Dm644 $(ICON_OFFLINE_SYMBOLIC) $(1)/scalable/apps/walky-offline-symbolic.svg
	install -Dm644 $(ICON_SYMBOLIC) $(1)/scalable/apps/$(APP_ID)-symbolic.svg
	@if command -v magick >/dev/null 2>&1; then \
		for s in $(ICON_SIZES); do \
			mkdir -p $(1)/$${s}x$${s}/apps; \
			magick -background none $(ICON_SRC) -resize $${s}x$${s} $(1)/$${s}x$${s}/apps/walky.png; \
			cp $(1)/$${s}x$${s}/apps/walky.png $(1)/$${s}x$${s}/apps/$(APP_ID).png; \
		done; \
	fi
	@command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -f -t $(1) >/dev/null 2>&1 || true
endef

install-local: build
	install -Dm755 $(BIN) $(LOCAL_BIN)/walky
	install -Dm755 $(UI_BIN) $(LOCAL_BIN)/walky-ui
	install -Dm644 data/$(APP_ID).desktop $(LOCAL_APPS)/$(APP_ID).desktop
	rm -f $(LOCAL_APPS)/walky.desktop
	$(call install_icons,$(ICON_DIR_LOCAL))
	install -Dm644 data/walky.service $(HOME)/.config/systemd/user/walky.service
	sed -i 's|/usr/bin/walky|%h/.local/bin/walky|' $(HOME)/.config/systemd/user/walky.service
	@command -v update-desktop-database >/dev/null 2>&1 && \
		update-desktop-database $(LOCAL_APPS) || true
	@systemctl --user daemon-reload 2>/dev/null || true
	@echo "installed: $(LOCAL_BIN)/walky $(LOCAL_BIN)/walky-ui $(HOME)/.config/systemd/user/walky.service"

uninstall-local:
	rm -f $(LOCAL_BIN)/walky $(LOCAL_BIN)/walky-ui
	rm -f $(LOCAL_APPS)/walky.desktop $(LOCAL_APPS)/$(APP_ID).desktop
	rm -f $(ICON_DIR_LOCAL)/scalable/apps/walky.svg $(ICON_DIR_LOCAL)/scalable/apps/$(APP_ID).svg
	rm -f $(ICON_DIR_LOCAL)/scalable/apps/walky-symbolic.svg $(ICON_DIR_LOCAL)/scalable/apps/$(APP_ID)-symbolic.svg $(ICON_DIR_LOCAL)/scalable/apps/walky-paused-symbolic.svg $(ICON_DIR_LOCAL)/scalable/apps/walky-offline-symbolic.svg
	@for s in $(ICON_SIZES); do rm -f $(ICON_DIR_LOCAL)/$${s}x$${s}/apps/walky.png $(ICON_DIR_LOCAL)/$${s}x$${s}/apps/$(APP_ID).png; done
	rm -f $(HOME)/.config/systemd/user/walky.service
	@systemctl --user daemon-reload 2>/dev/null || true

install-data:
	install -Dm644 data/walky.service $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	install -Dm644 data/90-walky-inotify.conf $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	install -Dm644 data/99-walky-usb.rules $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/walky $(DESTDIR)$(PREFIX)/bin/walky-ui
	rm -f $(ICON_DIR_SYS)/scalable/apps/walky.svg $(ICON_DIR_SYS)/scalable/apps/$(APP_ID).svg
	rm -f $(ICON_DIR_SYS)/scalable/apps/walky-symbolic.svg $(ICON_DIR_SYS)/scalable/apps/$(APP_ID)-symbolic.svg $(ICON_DIR_SYS)/scalable/apps/walky-paused-symbolic.svg $(ICON_DIR_SYS)/scalable/apps/walky-offline-symbolic.svg
	@for s in $(ICON_SIZES); do rm -f $(ICON_DIR_SYS)/$${s}x$${s}/apps/walky.png $(ICON_DIR_SYS)/$${s}x$${s}/apps/$(APP_ID).png; done
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	rm -f $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	rm -f $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules
