BIN := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test lint fmt generate dist clean desktop desktop-dev desktop-test online online-image online-smoke

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
	@# ADR 0039: gh runs only through internal/gh.
	@! grep -rnE 'exec\.Command(Context)?\([^)]*"gh"' --include='*.go' . | grep -v '^\./internal/gh/'
	@# ADR 0049: loop scripts run only through internal/gate.
	@! grep -rnE 'exec\.Command(Context)?\([^)]*"/bin/sh"' --include='*.go' . | grep -v '_test\.go:' | grep -v '^\./internal/gate/'
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
		case $$os in linux) file $$out | grep -q 'statically linked' || { echo "$$out: not statically linked" >&2; exit 1; };; esac; \
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
	cd desktop/src-tauri && cargo test --no-default-features --features online && cargo clippy --no-default-features --features online --all-targets -- -D warnings

# Workspace Online (ADR 0053): the server binary and the frontend, or the image.
online:
	cd desktop && npm ci && npm run build
	cd desktop/src-tauri && cargo build --release --no-default-features --features online --bin ws-online

online-image:
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) -t ws-online .

# Publish the image for amd64 and arm64 as one multi-arch tag. Needs a buildx
# builder that can build both (Docker Desktop, or QEMU binfmt on Linux).
# make online-image-push IMAGE=registry.example.com/ws-online TAG=$(git rev-parse --short=7 HEAD)
PLATFORMS ?= linux/amd64,linux/arm64
online-image-push:
	@test -n "$(IMAGE)" && test -n "$(TAG)" || { echo "usage: make online-image-push IMAGE=<repo> TAG=<tag>"; exit 1; }
	docker buildx build --platform $(PLATFORMS) -f deploy/docker/Dockerfile \
		--build-arg VERSION=$(VERSION) -t $(IMAGE):$(TAG) --push .

online-smoke: online-image
	deploy/docker/smoke-test.sh ws-online

clean:
	rm -rf $(BIN) dist
