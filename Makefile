BIN := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
# The packages whose tests run on windows-latest (ADR 0081), and the tests
# filtered from packages that also have unix-only tests.
WINDOWS_TESTS := ./internal/lockfile ./internal/privfile ./internal/procgroup ./internal/envproto ./internal/redact ./internal/power ./internal/llm ./internal/telemetry

.PHONY: build windows-exe test test-windows fakes e2e-runner lint fmt generate dist clean desktop desktop-dev desktop-test online online-image online-smoke

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)/ws ./cmd/ws

# windows-exe builds ws.exe for one Windows architecture with the release
# flags, for trying a branch on Windows (ADR 0086):
# make windows-exe WINARCH=arm64 -> dist/windows-arm64/ws.exe
WINARCH ?= amd64
windows-exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=$(WINARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/windows-$(WINARCH)/ws.exe ./cmd/ws

test:
	go test ./...

# Runs on windows-latest in Git Bash (CI's windows job). Every line runs even
# when an earlier one fails, so one run shows all the failures; the target
# exits nonzero if any failed.
test-windows:
	rc=0; \
	go test -p 2 -v ./internal/ptyproc || rc=1; \
	go test -p 2 $(WINDOWS_TESTS) || rc=1; \
	go test -p 2 -run 'TestSocketBaseDir|TestPrepareSocketDir' ./internal/daemon || rc=1; \
	go test -p 2 -v -run 'TestHasPathPrefix|TestTerminals_conpty' ./internal/envagent || rc=1; \
	go test -p 2 -run TestFindSh ./internal/gate || rc=1; \
	go test -p 2 -run TestUserBaseDir ./internal/config || rc=1; \
	go test -p 2 -run TestRunner_program ./internal/shell || rc=1; \
	go test -p 2 -v -run 'TestUserHome|TestClient_Send_windows|TestIsBatchFile' ./internal/claude || rc=1; \
	go test -p 2 -run TestTerminalSignals ./internal/cli || rc=1; \
	go test -p 2 -run 'TestTerminalEnvAllowed_windows|TestExecutableNames|TestLookPathIn_windows|TestAgentOperations_TerminalCommand_windows|TestHomeOf_windows' ./internal/cli || rc=1; \
	exit $$rc

# The fakes (ADR 0067) are test tools, built here and never shipped.
fakes:
	go build -o $(BIN)/fake-ollama ./cmd/fake-ollama
	go build -o $(BIN)/fake-mcp ./cmd/fake-mcp

# The runner's end-to-end scenarios (ADR 0067): the real ws, fake-ollama and
# fake-mcp on loopback. No network beyond loopback.
e2e-runner: build fakes
	go test -tags e2e -p 1 -timeout 20m ./e2e/runner/

lint:
	go tool golangci-lint run ./...
	go tool golangci-lint run --build-tags e2e ./e2e/...
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
	@# ADR 0067: the fakes are imported only by themselves, cmd/fake-* and e2e/.
	@! grep -rnE '"github.com/bobcob7/workspaces/internal/fake(ollama|mcp)' --include='*.go' . | grep -vE '^\./(internal/fake(ollama|mcp)|cmd/fake-[a-z]+|e2e)/' | grep -vE '^\./internal/mcp/[^:]*_test\.go:'
	@# envtest is a test broker: imported only from _test.go files.
	@! grep -rn '"github.com/bobcob7/workspaces/internal/environment/envtest"' --include='*.go' . | grep -v '_test\.go:'
	@# ADR 0062: only the openai provider and the fake requests chat/completions.
	@! grep -rn 'chat/completions' --include='*.go' . | grep -vE '^\./internal/(llm/openai|fakeollama)/' | grep -v '_test\.go:'
	@# ADR 0069: the SDK's stdio transport is built in internal/mcp only.
	@! grep -rn 'mcp.CommandTransport' --include='*.go' . | grep -vE '^\./internal/mcp/'
	@# ADR 0065: the OpenTelemetry SDK is imported by internal/telemetry only.
	@! grep -rn '"go.opentelemetry.io/' --include='*.go' . | grep -vE '^\./internal/telemetry/'
	@# The daemon supervises runners: ws runner run is started only from internal/daemon.
	@! grep -rn '"runner", "run"' --include='*.go' . | grep -vE '^\./internal/(daemon|cli)/' | grep -v '_test\.go:'
	@# The owner's rule: no blank lines inside function bodies (internal/blanklines).
	go test -count=1 -run '^TestRepository$$' ./internal/blanklines/

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
		exe=$$out; if [ $$os = windows ]; then exe=dist/ws.exe; fi; \
		echo "$$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$exe ./cmd/ws || exit 1; \
		case $$os in \
		linux) file $$out | grep -q 'statically linked' || { echo "$$out: not statically linked" >&2; exit 1; };; \
		windows) (cd dist && zip -q ws-$$os-$$arch.zip ws.exe && rm ws.exe) || exit 1;; \
		esac; \
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
IMAGE_PLATFORMS ?= linux/amd64,linux/arm64
online-image-push:
	@test -n "$(IMAGE)" && test -n "$(TAG)" || { echo "usage: make online-image-push IMAGE=<repo> TAG=<tag>"; exit 1; }
	docker buildx build --platform $(IMAGE_PLATFORMS) -f deploy/docker/Dockerfile \
		--build-arg VERSION=$(VERSION) -t $(IMAGE):$(TAG) --push .

online-smoke: online-image
	deploy/docker/smoke-test.sh ws-online

clean:
	rm -rf $(BIN) dist
