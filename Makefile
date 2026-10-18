BIN := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test lint fmt generate dist clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/ws ./cmd/ws

test:
	go test ./...

lint:
	go tool golangci-lint run ./...
	@# ADR 0003: git runs only through internal/git.
	@! grep -rnE 'exec\.Command(Context)?\([^)]*"git"' --include='*.go' . | grep -v '^\./internal/git/'

fmt:
	go tool golangci-lint fmt ./...

# generate is the single entry point for code generation.
generate:
	go generate ./...

dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/ws-$$os-$$arch ./cmd/ws || exit 1; \
	done

clean:
	rm -rf $(BIN) dist
