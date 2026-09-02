.PHONY: web web-test build run test verify

VERSION ?= $(shell tr -d '\n' < ../VERSION)
COMMIT ?= $(shell git -C .. rev-parse --short=12 HEAD 2>/dev/null || printf none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_LDFLAGS = -X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

web:
	cd web-src && bun install --frozen-lockfile && bun run build

web-test:
	cd web-src && bun run typecheck && bun run test

build: web
	go build -trimpath -ldflags "$(BUILD_LDFLAGS)" -o localrouter .

run:
	go run .

test: web-test
	go test ./...

verify:
	../tests/verify.sh
