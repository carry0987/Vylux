GO ?= go

.PHONY: fmt tidy upgrade lint test run build docker-build

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

upgrade:
	$(GO) get -u ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test ./...

run:
	$(GO) run ./cmd/server

build:
	$(GO) build ./cmd/server
