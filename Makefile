GO ?= go
GQLGEN = github.com/99designs/gqlgen
GOLANGCI = github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVULNCHECK = golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: help bootstrap generate tidy verify-deps build run run-db test unit race vet lint vuln \
        fmt-check coverage check integration migrate migrate-up migrate-down \
        migrate-status migrate-version migrate-new docker-up docker-down backup loadtest

.DEFAULT_GOAL := help

help:
	@printf '%s\n' \
	  'commeng — Go/GraphQL comment service' \
	  '' \
	  'make run              Start in-memory API on 127.0.0.1:8080' \
	  'make run-db           Start PostgreSQL API (DATABASE_URL + migrations required)' \
	  'make bootstrap        Explicitly regenerate gqlgen, tidy and verify dependencies' \
	  'make generate         Explicitly regenerate GraphQL code' \
	  'make build            Reproducible build, does NOT modify go.mod or generated files' \
	  'make test / race      Run tests; race uses -race' \
	  'make check            Build, fmt, vet, test, race, lint and coverage' \
	  'make docker-up/down   Start/stop PostgreSQL and API (persistent volume)' \
	  'make integration      Run PostgreSQL -race tests with TEST_DATABASE_URL' \
	  'make migrate-up/down/status/version' \
	  'make loadtest         Rate-limited end-to-end GraphQL benchmark'

# Dependencies and checked-in gqlgen outputs only change when explicitly asked.
bootstrap: generate tidy

generate:
	GOFLAGS=-mod=mod $(GO) run $(GQLGEN) generate

tidy:
	$(GO) mod tidy
	$(GO) mod verify

verify-deps:
	$(GO) mod verify

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -o bin/commeng ./cmd/commeng

run: build
	./bin/commeng serve --storage memory --addr 127.0.0.1:8080

run-db: build
	./bin/commeng serve --storage postgres --addr 127.0.0.1:8080

test unit:
	$(GO) test -mod=readonly -count=1 ./...

race:
	$(GO) test -mod=readonly -race -count=1 ./...

vet:
	$(GO) vet -mod=readonly ./...

lint:
	$(GO) run $(GOLANGCI) run ./...

vuln:
	$(GO) run $(GOVULNCHECK) ./...

fmt-check:
	@files="$$(find cmd internal migrations tests tools -name '*.go')"; \
	 unformatted="$$(gofmt -l $$files)"; \
	 if test -n "$$unformatted"; then echo "$$unformatted"; exit 1; fi

coverage:
	@packages="$$(go list ./internal/... | grep -v '/internal/graph/generated$$' | grep -v '/internal/graph/model$$')"; \
	if test -n "$$TEST_DATABASE_URL"; then tags="-tags=integration"; minimum=75; else tags=""; minimum=60; packages="$$(printf '%s\n' "$$packages" | grep -v '/internal/repository/postgres$$')"; fi; \
	packages="$$(printf '%s\n' "$$packages" | paste -sd, -)"; \
	$(GO) test -mod=readonly $$tags -coverpkg="$$packages" -coverprofile=coverage.out ./... && \
	$(GO) tool cover -func=coverage.out | awk -v minimum="$$minimum" '/^total:/ { gsub(/%/, "", $$3); printf "Backend coverage: %.1f%% (min %.1f%%)\n", $$3, minimum; if ($$3 + 0 < minimum) { print "Coverage gate failed" > "/dev/stderr"; exit 1 } }'

check: fmt-check build vet test race lint coverage

integration: build
	@test -n "$$TEST_DATABASE_URL" || { echo 'TEST_DATABASE_URL is required (use a disposable PostgreSQL database)' >&2; exit 1; }
	$(GO) test -mod=readonly -tags=integration -race -count=1 ./tests/integration

migrate: migrate-up

migrate-up:
	$(GO) run -mod=readonly ./cmd/commeng migrate up

migrate-down:
	@test "$(CONFIRM)" = "yes" || { echo 'Refusing rollback. Use make migrate-down CONFIRM=yes after a backup.' >&2; exit 1; }
	$(GO) run -mod=readonly ./cmd/commeng migrate down --yes

migrate-new:
	@sh ./scripts/new-migration.sh "$(NAME)"

migrate-status:
	$(GO) run -mod=readonly ./cmd/commeng migrate status

migrate-version:
	$(GO) run -mod=readonly ./cmd/commeng migrate version

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

backup:
	bash ./scripts/db-backup.sh

loadtest:
	$(GO) run -mod=readonly ./tools/loadtest --url http://127.0.0.1:8080/graphql
