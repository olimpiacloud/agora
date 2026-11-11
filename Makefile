VERSION ?= $(or $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null | sed 's/^v//'),dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG := github.com/olimpiacloud/agora/internal/core
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: build web test vet fmt check race

build:
	PKG_CONFIG=$(or $(PKG_CONFIG),pkg-config) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/agora ./cmd/agora

web:
	cd web && bun install --frozen-lockfile && bun run build

test:
	go test ./internal/... ./e2e/ -count=1

race:
	go test -race ./internal/... -count=1

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal e2e web

check:
	@out=$$(gofmt -l cmd internal e2e web); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	go test -race ./internal/... -count=1
	cd web && bun install --frozen-lockfile && npx tsc --noEmit -p tsconfig.json
	cd paseo-plugin && bun install --frozen-lockfile && bun run typecheck && bun test
