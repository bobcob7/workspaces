BIN := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test lint fmt generate dist clean desktop desktop-dev desktop-test

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/ws ./cmd/ws

test:
	go test ./...

lint:
	go tool golangci-lint run ./...
	@# ADR 0003: git runs only through internal/git.
	@! grep -rnE 'exec\.Command(Context)?\([^)]*"git"' --include='*.go' . | grep -v '^\./internal/git/'
	@# ADR 0024: claude runs only through internal/claude.
	@! grep -rnE 'exec\.Command(Context)?\([^)]*"claude"' --include='*.go' . | grep -v '^\./internal/claude/'
	@# ADR 0025: only the Claude Code client replaces ws, with claude attach.
	@! grep -rnE '(syscall|unix)\.Exec\b' --include='*.go' . | grep -v '^\./internal/claude/'

fmt:
	go tool golangci-lint fmt ./...

# generate is the single entry point for code generation.
generate:
	go generate ./...

# dist builds a release: one static binary per platform (ADR 0002) and
# their SHA-256 checksums.
dist:
	rm -rf dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=dist/ws-$$os-$$arch; \
		echo "$$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/ws || exit 1; \
		go version -m $$out | grep -q 'CGO_ENABLED=0' || { echo "$$out: built with cgo" >&2; exit 1; }; \
	done
	cd dist && shasum -a 256 ws-* > SHA256SUMS

# The desktop app (ADR 0029); needs Rust and Node.js.
desktop:
	cd desktop && npm ci && npm run tauri build

desktop-dev:
	cd desktop && npm install && npm run tauri dev

desktop-test:
	cd desktop && npm ci && npm run typecheck && npm test
	cd desktop/src-tauri && cargo test && cargo clippy --all-targets -- -D warnings

clean:
	rm -rf $(BIN) dist
