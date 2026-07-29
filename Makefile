# tedmcp — an MCP server over the EU public procurement journal.
#
# Everything here works from the project directory with no absolute paths, so
# the repository can live anywhere.

BINARY  ?= tedmcp
ADDR    ?= 127.0.0.1:8080
IMAGE   ?= tedmcp
CACHE   ?= .tenders/cache

GOBIN   := $(shell go env GOPATH)/bin
AIR     := $(GOBIN)/air

.DEFAULT_GOAL := help

## help: list the available targets
help:
	@echo "tedmcp — make targets"
	@echo
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | awk -F': ' '{printf "  %-13s %s\n", $$1, $$2}'
	@echo
	@echo "  variables:   ADDR=$(ADDR)  IMAGE=$(IMAGE)  CACHE=$(CACHE)"

## dev: run the server with live reload (air), serving MCP over HTTP
dev: $(AIR)
	$(AIR)

## run: run the server over HTTP, without live reload
run:
	go run . -http $(ADDR)

## stdio: run the server over stdio, the way an MCP client launches it
stdio:
	go run .

## build: compile the binary
build:
	go build -o $(BINARY) .

## test: run the test suite
test:
	go test ./...

## race: run the test suite under the race detector
race:
	go test -race ./...

## check: format, vet and test — what CI should agree with
check: fmt vet test

## fmt: rewrite sources with gofmt, reporting what it touched
fmt:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "reformatted:"; echo "$$out"; gofmt -w .; fi

## vet: run go vet
vet:
	go vet ./...

## tidy: prune and verify go.mod / go.sum
tidy:
	go mod tidy
	go mod verify

## tools: install the development tools this project uses (air)
tools:
	go install github.com/air-verse/air@latest

## image: build the container image
image:
	docker build -t $(IMAGE) .

## up: run the container, publishing HTTP on the loopback interface only
up:
	docker run --rm -p 127.0.0.1:8080:8080 -v $(IMAGE)-cache:/var/cache/tedmcp $(IMAGE)

## cache-clean: drop the cached notice documents
cache-clean:
	rm -rf $(CACHE)

## clean: remove build output and the live-reload directory
clean:
	rm -rf $(BINARY) tmp

$(AIR):
	@echo "air is not installed; run 'make tools'" >&2
	@exit 1

.PHONY: help dev run stdio build test race check fmt vet tidy tools image up cache-clean clean
