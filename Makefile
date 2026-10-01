GO ?= go

.PHONY: build test check
build:
	$(GO) build -buildvcs=false -o atto .
test:
	$(GO) test ./...
check:
	$(GO) vet ./...
	$(GO) test -race ./...
