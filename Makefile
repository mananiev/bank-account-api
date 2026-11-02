GOLANGCI_LINT_VERSION := v2.15.0
BIN := bin/api

# Race detection needs cgo (a C compiler). Enable with: make test RACE=1
RACE ?= 0
ifeq ($(RACE),1)
TEST_FLAGS := -race
endif

.PHONY: build run test test-unit lint demo

build:
	go build -o $(BIN) ./cmd/api

run:
	go run ./cmd/api

# All tests. Integration tests start PostgreSQL via testcontainers (Docker required).
test:
	go test $(TEST_FLAGS) ./...

# Unit tests only; no Docker needed.
test-unit:
	go test $(TEST_FLAGS) -short ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

# End-to-end walkthrough against a running API (docker compose up). API_URL overrides the target.
demo:
	bash scripts/demo.sh
