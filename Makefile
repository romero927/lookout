BINARY  := lookout
PKG     := ./cmd/lookout
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: screenshot all build install test lint fmt snapshot clean

all: lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

install:
	CGO_ENABLED=0 go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

test:
	go test -race ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found; running go vet + gofmt"; \
		go vet ./... && test -z "$$(gofmt -l . | tee /dev/stderr)"; \
	fi

fmt:
	gofmt -w .

screenshot:
	LOOKOUT_SCREENSHOT=$(CURDIR)/bin/shot.ansi go test ./internal/ui -run Screenshot -count=1
	python3 -I scripts/ansi2png.py bin/shot.ansi docs/screenshot.png
	python3 -I scripts/ansi2png.py bin/shot-activity.ansi docs/activity.png

snapshot:
	goreleaser release --snapshot --clean

clean:
	go clean
	rm -f bin/$(BINARY)
