GOLANGCI_LINT_VERSION := v2.15.0
BIN := bin/api

# Race detection needs cgo (a C compiler). Enable with: make test RACE=1
RACE ?= 0
ifeq ($(RACE),1)
TEST_FLAGS := -race
endif

.PHONY: build run test lint

build:
	go build -o $(BIN) ./cmd/api

run:
	go run ./cmd/api

test:
	go test $(TEST_FLAGS) ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...
