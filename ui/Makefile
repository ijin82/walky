VALAC ?= valac
PKGS  := gtk4 libadwaita-1 gio-2.0
SRC   := $(wildcard src/*.vala)
OUT   := build/walky-ui

.PHONY: all build clean run

all: build

build: $(OUT)

$(OUT): $(SRC) Makefile
	mkdir -p build
	$(VALAC) --pkg=gtk4 --pkg=libadwaita-1 --pkg=gio-2.0 -o $@ $(SRC)

run: build
	./$(OUT)

clean:
	rm -rf build
