# furoshiki — development tasks
#
# Tools that are not part of the Go distribution are run through `go run`
# with pinned versions, so they are built with the same toolchain as the
# project and nothing has to be installed globally.

BIN        := furoshiki
PKG        := ./cmd/furoshiki
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS    := -s -w -X main.version=$(VERSION)

GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@latest
GOSEC         := go run github.com/securego/gosec/v2/cmd/gosec@latest

.PHONY: all build install test e2e cover lint vet vuln sec fmt fmt-check tidy check clean help

all: build

## build: compile the CLI into ./furoshiki
build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

## install: install the CLI into $(go env GOBIN)
install:
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

## test: run unit and integration tests (no browser needed)
test:
	go test -race ./...

## e2e: run the browser tests (needs Chrome or Chromium; FUROSHIKI_CHROME overrides the executable)
e2e:
	go test -tags e2e -count=1 ./e2e/

## cover: write coverage.out and print a per-function summary
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## lint: run golangci-lint (includes gofmt/goimports checks)
lint:
	$(GOLANGCI_LINT) run ./...

## vet: run go vet, including the e2e build tag
vet:
	go vet ./...
	go vet -tags e2e ./e2e/

## vuln: check dependencies against the Go vulnerability database
vuln:
	$(GOVULNCHECK) ./...

## sec: run gosec
sec:
	$(GOSEC) -quiet -exclude-dir=testdata ./...

## fmt: reformat all Go sources
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-formatted
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

## tidy: fail if go.mod/go.sum are not tidy
tidy:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

## check: everything CI runs, except the browser tests
check: fmt-check vet lint test vuln sec

## clean: remove build and test artefacts
clean:
	rm -f $(BIN) $(BIN).exe coverage.out

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
