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

# Each side's pages: Aniko's web UI (the dashboard) and Moviko's.
web:
	cd web/anime && $(NPM) ci --no-audit --no-fund && $(NPM) run build
	cd web/movies && $(NPM) ci --no-audit --no-fund && $(NPM) run build

embed: web
	for side in anime movies; do \
		rm -rf $$side/internal/webui/dist && mkdir -p $$side/internal/webui/dist && \
		cp -r web/$$side/dist/. $$side/internal/webui/dist/ && touch $$side/internal/webui/dist/.keep; \
	done

server: embed
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build $(GO_BUILDFLAGS) -ldflags "$(GO_LDFLAGS)" -o dist/mediaserver ./cmd/mediaserver

test:
	$(GO) test ./...

run: server
	./dist/mediaserver --data-dir ./dev-data

clean:
	rm -rf dist web/anime/dist web/movies/dist
