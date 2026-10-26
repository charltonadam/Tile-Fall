# Tile-Fall build and run targets.
#
#   make            install deps if needed, build the frontend, start the server
#   make dev        Go server + Vite dev server (hot reload) together; Ctrl+C stops both
#   make build      build a standalone binary into bin/
#   make test       Go and frontend tests
#   make check      go vet + svelte-check
#
# Pass server flags with ARGS, e.g. `make ARGS="-port 9000 -mcp-lan"`.
#
# Recipes must work in both cmd.exe and sh: GNU make on Windows uses sh only
# if it finds one on PATH. Stick to plain commands (no &&, no cd, no rm -rf).

ifeq ($(OS),Windows_NT)
EXE := .exe
endif

ARGS ?=

# recursive wildcard; make has no built-in one and `find` differs on Windows
rwildcard = $(foreach d,$(wildcard $(1:=/*)),$(call rwildcard,$d,$2) $(filter $(subst *,%,$2),$d))

WEB_DEPS := web/node_modules/.package-lock.json
WEB_SRC := $(call rwildcard,web/src,*) web/index.html web/vite.config.ts web/svelte.config.js web/tsconfig.json
WEB_DIST := web/dist/index.html

.DEFAULT_GOAL := start
.PHONY: start dev dev-server dev-web build frontend deps test check

# npm writes this file on every install, so it tracks package.json changes
$(WEB_DEPS): web/package.json web/package-lock.json
	npm --prefix web install

deps: $(WEB_DEPS)
	go mod download

# the Go binary embeds web/dist, so the frontend must be built before Go compiles
$(WEB_DIST): $(WEB_DEPS) $(WEB_SRC)
	npm --prefix web run build

frontend: $(WEB_DIST)

start: $(WEB_DIST)
	go run ./cmd/tilefall $(ARGS)

dev: $(WEB_DEPS)
	$(MAKE) -j2 dev-server dev-web

dev-server:
	go run ./cmd/tilefall $(ARGS)

dev-web:
	npm --prefix web run dev

build: $(WEB_DIST)
	go build -o bin/tilefall$(EXE) ./cmd/tilefall

test: $(WEB_DEPS)
	go test ./...
	npm --prefix web test

check: $(WEB_DEPS)
	go vet ./...
	npm --prefix web run check
