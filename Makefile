BIN       := bin/walky
UI_BIN    := bin/walky-ui
GO_DIR    := core
UI_DIR    := ui
PREFIX    ?= /usr
DESTDIR   ?=
VERSION   ?= 0.1.0-dev
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
	-X walky/internal/version.Version=$(VERSION) \
	-X walky/internal/version.Commit=$(COMMIT) \
	-X walky/internal/version.Date=$(DATE)

.PHONY: all build build-core build-ui test vet fmt fmt-check check clean \
	install install-data uninstall run-ui

all: build

build: build-core build-ui

build-core:
	cd $(GO_DIR) && go build -trimpath -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/walky

build-ui:
	$(MAKE) -C $(UI_DIR) build
	install -Dm755 $(UI_DIR)/build/walky-ui $(UI_BIN)

test:
	cd $(GO_DIR) && go test ./...

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

install-data:
	install -Dm644 data/walky.service $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	install -Dm644 data/90-walky-inotify.conf $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	install -Dm644 data/99-walky-usb.rules $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/walky $(DESTDIR)$(PREFIX)/bin/walky-ui
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	rm -f $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	rm -f $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules
