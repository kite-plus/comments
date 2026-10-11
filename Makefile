GO       ?= go
BINARY   ?= comments
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)
LDFLAGS  := -s -w \
	-X github.com/kite-plus/comments/internal/buildinfo.Version=$(VERSION) \
	-X github.com/kite-plus/comments/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/kite-plus/comments/internal/buildinfo.Date=$(DATE)

.PHONY: all build test test-race cover fmt vet lint check-imports check-tidy check tidy clean \
	db-up db-down migrate dev dev-worker dev-seed dev-blogs web web-check web-test e2e e2e-real docker

all: check build

# The binary embeds web/widget/dist and web/console/dist; run make web
# first for a binary with the frontends.
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/comments

DEV_COMPOSE      := docker compose -f deploy/docker-compose.dev.yaml
DEV_DATABASE_URL ?= postgres://comments:comments@127.0.0.1:5434/comments?sslmode=disable
# Integration tests use COMMENTS_TEST_DATABASE_URL, else the development
# database when it answers (make db-up), and skip otherwise.
TEST_DATABASE_URL ?= $(or $(COMMENTS_TEST_DATABASE_URL),$(shell nc -z 127.0.0.1 5434 >/dev/null 2>&1 && echo '$(DEV_DATABASE_URL)'))

test:
	COMMENTS_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' $(GO) test ./...

test-race:
	COMMENTS_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' $(GO) test -race ./...

cover:
	COMMENTS_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' $(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

LINT_VERSION ?= v2.13.2

lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION) run ./...

check-imports:
	@sh scripts/check-imports.sh

# Compares against the files before tidy, not HEAD, so uncommitted edits are
# not reported as untidiness.
check-tidy:
	@cp go.mod .go.mod.check && cp go.sum .go.sum.check
	@$(GO) mod tidy
	@if ! cmp -s go.mod .go.mod.check || ! cmp -s go.sum .go.sum.check; then \
		rm -f .go.mod.check .go.sum.check; \
		echo "go.mod or go.sum was not tidy; tidy has fixed them in place"; \
		exit 1; \
	fi
	@rm -f .go.mod.check .go.sum.check

check: fmt vet check-imports check-tidy lint test

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin coverage.out

db-up:
	$(DEV_COMPOSE) up -d --wait postgres

db-down:
	$(DEV_COMPOSE) down

# Local development reads deploy/.env.dev (copy deploy/.env.dev.example);
# see the README.
DEV_ENV := set -a; [ -f deploy/.env.dev ] && . deploy/.env.dev; set +a;

migrate:
	$(DEV_ENV) COMMENTS_DATABASE_URL='$(DEV_DATABASE_URL)' $(GO) run ./cmd/comments migrate up

dev: migrate
	$(DEV_ENV) COMMENTS_DATABASE_URL='$(DEV_DATABASE_URL)' $(GO) run ./cmd/comments serve

dev-worker:
	$(DEV_ENV) COMMENTS_DATABASE_URL='$(DEV_DATABASE_URL)' $(GO) run ./cmd/comments worker

dev-seed: migrate
	$(DEV_ENV) COMMENTS_DATABASE_URL='$(DEV_DATABASE_URL)' $(GO) run ./cmd/comments dev seed $(SEED_ARGS)

dev-blogs:
	$(DEV_ENV) $(GO) run ./cmd/comments dev blogs

PNPM ?= pnpm

# The widget needs Node, which a Go-only contributor should not have to
# install, so it has its own targets. Vite empties dist, so the placeholder
# that keeps go build working is put back afterwards.
web:
	cd web/widget && $(PNPM) install --frozen-lockfile && $(PNPM) build
	@touch web/widget/dist/.gitkeep

web-check:
	cd web/widget && $(PNPM) check

web-test:
	cd web/widget && $(PNPM) test

# Browser tests with the installed Google Chrome against the harness; see
# the README. e2e-real runs against the local identity stack and make dev.
e2e: web
	cd e2e && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 $(PNPM) install --frozen-lockfile && COMMENTS_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' $(PNPM) test

e2e-real:
	cd e2e && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 $(PNPM) install --frozen-lockfile && $(PNPM) test:real

IMAGE ?= ghcr.io/kite-plus/comments

docker:
	docker build -f deploy/Dockerfile \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest .
