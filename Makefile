# Developer shortcuts for tm. Everything here is plain Go tooling; no
# extra dependencies are required.

GO      ?= go
BINARY  ?= bin/tm
PACKAGE  = ./cmd/tm

.PHONY: build install test race vet fmt fmt-check check clean

## build: compile the tm binary into bin/
build:
	$(GO) build -o $(BINARY) $(PACKAGE)

## install: install tm into $(go env GOPATH)/bin
install:
	$(GO) install $(PACKAGE)

## test: run the test suite
test:
	$(GO) test ./...

## race: run the test suite with the race detector
race:
	$(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format all Go source files in place
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-formatted
fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files need formatting:"; echo "$$unformatted"; exit 1; \
	fi

## check: everything CI runs — formatting, vet, tests, race tests, build
check: fmt-check vet test race
	$(GO) build ./...

## clean: remove build output
clean:
	rm -rf bin/ tm

screenshots: ## regenerate the README images from a scripted tm run
	go run ./tools/screenshot
