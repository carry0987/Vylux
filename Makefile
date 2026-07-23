GO ?= go

.PHONY: fmt tidy upgrade lint test run build docker-build

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy
	$(GO) mod verify

verify:
	$(GO) mod verify

upgrade:
	$(GO) get -u ./...

fix:
	$(GO) fix ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test ./...

run:
	$(GO) run ./cmd/vylux

build:
	$(GO) build ./cmd/vylux
