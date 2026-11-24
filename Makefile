BIN      := walky
PREFIX   ?= /usr
DESTDIR  ?=
VERSION  ?= 0.1.0-dev
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
	-X walky/internal/version.Version=$(VERSION) \
	-X walky/internal/version.Commit=$(COMMIT) \
	-X walky/internal/version.Date=$(DATE)

.PHONY: all build test vet fmt check clean install install-data uninstall

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/walky

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: fmt vet test

clean:
	rm -rf bin

# Binary only. Data files need root, see install-data.
install: build
	install -Dm755 bin/$(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)

install-data:
	install -Dm644 data/walky.service $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	install -Dm644 data/90-walky-inotify.conf $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	install -Dm644 data/99-walky-usb.rules $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)
	rm -f $(DESTDIR)$(PREFIX)/lib/systemd/user/walky.service
	rm -f $(DESTDIR)/etc/sysctl.d/90-walky-inotify.conf
	rm -f $(DESTDIR)/etc/udev/rules.d/99-walky-usb.rules
