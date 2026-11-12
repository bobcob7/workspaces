# syntax=docker/dockerfile:1.7

# Workspace Online (ADR 0053): ws, the frontend and ws-online, then one runtime image.

# The ws, web and server stages run on the build machine's own architecture and
# cross-compile for TARGETARCH, so a multi-arch build never runs rustc or the Go
# toolchain under emulation (rustc crashes under QEMU).
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS ws
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ws ./cmd/ws

FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
WORKDIR /src/desktop
COPY desktop/package.json desktop/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY desktop/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM rust:1.99-trixie AS server
ARG TARGETARCH
WORKDIR /src/desktop/src-tauri
COPY desktop/src-tauri ./
# Pick the Rust target and a C compiler/linker for it (ring and other build
# scripts compile C). The native architecture uses the stock gcc.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/desktop/src-tauri/target \
    set -eu; \
    case "${TARGETARCH}" in \
      amd64) triple=x86_64-unknown-linux-gnu;  cross=x86_64-linux-gnu;  pkg=x86-64-linux-gnu;  deb=amd64 ;; \
      arm64) triple=aarch64-unknown-linux-gnu; cross=aarch64-linux-gnu; pkg=aarch64-linux-gnu; deb=arm64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    if [ "$(dpkg --print-architecture)" = "${deb}" ]; then cc=gcc; else \
      apt-get update; \
      apt-get install -y --no-install-recommends "gcc-${pkg}" "libc6-dev-${deb}-cross"; \
      rm -rf /var/lib/apt/lists/*; cc="${cross}-gcc"; fi; \
    rustup target add "${triple}"; \
    up="$(echo "${triple}" | tr 'a-z-' 'A-Z_')"; \
    export "CARGO_TARGET_${up}_LINKER=${cc}" "CC_$(echo "${triple}" | tr - _)=${cc}"; \
    cargo build --release --locked --target "${triple}" --no-default-features --features online --bin ws-online \
    && mkdir -p /out && cp "target/${triple}/release/ws-online" /out/

# Claude Code for the target architecture. The native installer runs the
# downloaded binary, which crashes under QEMU (Bun), so fetch and verify the
# release for TARGETARCH on the build machine and lay it out as the installer
# would: the binary in ~/.local/share/claude/versions and a symlink in ~/.local/bin.
FROM --platform=$BUILDPLATFORM debian:trixie-slim AS claude
ARG TARGETARCH
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl jq \
    && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) platform=linux-x64 ;; \
      arm64) platform=linux-arm64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    base=https://downloads.claude.ai/claude-code-releases; \
    version="$(curl -fsSL "${base}/latest")"; \
    printf '%s' "${version}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; \
    checksum="$(curl -fsSL "${base}/${version}/manifest.json" | jq -er ".platforms[\"${platform}\"].checksum")"; \
    dest=/out/.local/share/claude/versions; \
    mkdir -p "${dest}" /out/.local/bin; \
    curl -fsSL -o "${dest}/${version}" "${base}/${version}/${platform}/claude"; \
    echo "${checksum}  ${dest}/${version}" | sha256sum -c -; \
    chmod 0755 "${dest}/${version}"; \
    ln -s "/home/ws/.local/share/claude/versions/${version}" /out/.local/bin/claude

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl git openssh-client jq ripgrep tini procps less bash gnupg \
    && install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 1000 --shell /bin/bash ws \
    && mkdir -p /data /opt/ws-online/seed/home \
    && chown ws:ws /data /opt/ws-online/seed/home

COPY --from=ws /out/ws /usr/local/bin/ws
COPY --from=server /out/ws-online /usr/local/bin/ws-online
COPY --from=web /src/desktop/dist /opt/ws-online/web
COPY --chmod=755 deploy/docker/entrypoint.sh /usr/local/bin/ws-online-entrypoint

USER ws
ENV HOME=/home/ws
COPY --from=claude --chown=ws:ws /out/.local /opt/ws-online/seed/home/.local

ENV PATH=/home/ws/.local/bin:/usr/local/bin:/usr/bin:/bin \
    SHELL=/bin/bash \
    NPM_CONFIG_PREFIX=/home/ws/.local \
    WS_CONFIG_DIR=/data/ws \
    WS_ONLINE_DATA_DIR=/data/online \
    WS_ONLINE_WEB_DIR=/opt/ws-online/web \
    WS_ONLINE_ADDR=0.0.0.0:8080

WORKDIR /home/ws
VOLUME ["/home/ws", "/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
    CMD curl -fsS http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/ws-online-entrypoint"]
