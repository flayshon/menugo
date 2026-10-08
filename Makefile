# Settings come from .envrc (written by scripts/setup-db.sh); see .envrc.example.
-include .envrc
export ENV PORT DB_DSN TEST_DB_DSN

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## run/api: run the cmd/api application
.PHONY: run/api
run/api:
	go run ./cmd/api

## db/setup: create the local databases and users (uses sudo)
.PHONY: db/setup
db/setup:
	./scripts/setup-db.sh

## db/migrate: apply all pending database migrations
.PHONY: db/migrate
db/migrate:
	go run ./cmd/api -migrate

## db/migrations/new name=$1: create a new migration file
.PHONY: db/migrations/new
db/migrations/new:
	@test -n "${name}" || (echo 'usage: make db/migrations/new name=create_things' && exit 1)
	@last=$$(ls migrations/*.sql | sed 's|migrations/0*\([0-9]*\)_.*|\1|' | sort -n | tail -1); \
	file=$$(printf 'migrations/%06d_%s.sql' $$((last + 1)) ${name}); \
	touch $$file && echo "created $$file"

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## test: run all tests (integration tests are skipped unless TEST_DB_DSN is set)
.PHONY: test
test:
	go test -race ./...

## audit: tidy dependencies, format, vet and test all code
.PHONY: audit
audit:
	go mod tidy -diff
	go mod verify
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	go test -race ./...

# ==================================================================================== #
# BUILD
# ==================================================================================== #

## build/api: build the cmd/api application
.PHONY: build/api
build/api:
	go build -ldflags='-s' -o=./bin/api ./cmd/api
