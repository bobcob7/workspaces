# syntax=docker/dockerfile:1.7

# Workspace Online (ADR 0053): ws, the frontend and ws-online, then one runtime image.

FROM golang:1.27-trixie AS ws
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ws ./cmd/ws

FROM node:22-trixie-slim AS web
WORKDIR /src/desktop
COPY desktop/package.json desktop/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY desktop/ ./
RUN npm run build

FROM rust:1.94-trixie AS server
WORKDIR /src/desktop/src-tauri
COPY desktop/src-tauri ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/desktop/src-tauri/target \
    cargo build --release --locked --no-default-features --features online --bin ws-online \
    && mkdir -p /out && cp target/release/ws-online /out/

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl git openssh-client jq ripgrep make nodejs npm tini procps less bash gnupg \
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
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && /home/ws/.local/bin/claude --version \
    && mv /home/ws/.local /opt/ws-online/seed/home/.local

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
