# mediaserver build
#   make          build both sides' pages and the program (./dist/mediaserver)
#   make test     run the Go tests
#   make run      build, then run it with its data in ./dev-data
#
# Not named LDFLAGS/GOFLAGS on purpose: build tools export C linker flags in
# LDFLAGS (e.g. -Wl,-O1) which the Go linker doesn't understand.
GO ?= go
NPM ?= npm
GO_BUILDFLAGS ?= -trimpath
VERSION ?= 1.0.0
COMMIT ?= $(shell git rev-parse HEAD 2> /dev/null)
GO_LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: all web embed server test run clean

all: server

# Each side's pages: Aniko's web UI (the dashboard; Aniko's code is the
# aniko submodule: git submodule update --init) and Moviko's.
ANIME_DIST = aniko/server/internal/webui/dist
MOVIES_DIST = movies/internal/webui/dist

web:
	cd aniko/web && $(NPM) ci --no-audit --no-fund && $(NPM) run build
	cd web/movies && $(NPM) ci --no-audit --no-fund && $(NPM) run build

embed: web
	rm -rf $(ANIME_DIST) $(MOVIES_DIST)
	mkdir -p $(ANIME_DIST) $(MOVIES_DIST)
	cp -r aniko/web/dist/. $(ANIME_DIST)/ && touch $(ANIME_DIST)/.keep
	cp -r web/movies/dist/. $(MOVIES_DIST)/ && touch $(MOVIES_DIST)/.keep

server: embed
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build $(GO_BUILDFLAGS) -ldflags "$(GO_LDFLAGS)" -o dist/mediaserver ./cmd/mediaserver

test:
	$(GO) test ./...

run: server
	./dist/mediaserver --data-dir ./dev-data

clean:
	rm -rf dist aniko/web/dist web/movies/dist
