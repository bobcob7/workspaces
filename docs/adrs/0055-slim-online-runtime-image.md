# 0055. The Workspace Online runtime image ships no Node.js, npm or make; a derived dev image adds the toolchains

- Status: Proposed
- Date: 2026-11-08

## Context

### Problem

[0053](0053-workspace-online.md) put `make`, Node.js and npm in the runtime image. Should the base image carry developer toolchains, now that a separate image for them exists?

### What a solution needs

1. The runtime image holds only what `ws-online`, its entrypoint and the agents' basic work need.
2. Developer toolchains (Node, make, Go, Rust, a C toolchain, kubectl, the docker CLI) can change without rebuilding the runtime image.
3. Nothing in the image or repo breaks when they are gone.

### Relevant facts

- A derived image, `sudo-core/ws-dev-image` (Forgejo), layers those toolchains on top of this image.
- `git grep` of the Dockerfile, `entrypoint.sh`, `smoke-test.sh`, `docker-compose.yml`, `docs/`, `.github/workflows`, `desktop/src-tauri` and the Go code found nothing that runs `node`, `npm`, `npx` or `make` inside the runtime image. The Dockerfile's `web` stage uses `node:22-trixie-slim` and the CI and release workflows run `make` on their own runners; neither is the runtime stage. The smoke test asserts only `claude`, `gh` and `ws`.
- `NPM_CONFIG_PREFIX=/home/ws/.local` is only an environment variable; it does nothing without npm.

## Decision

The final stage no longer installs `nodejs`, `npm` or `make`. The derived dev image layers the toolchains on top. The runtime image deliberately keeps `git`, `openssh-client`, `jq`, `ripgrep`, `gh`, `curl` and `tini`, with `ca-certificates`, `procps`, `less`, `bash` and `gnupg`.

`NPM_CONFIG_PREFIX` stays, so `npm install -g` in the dev image still lands in the home volume. This changes the image list in 0053's Decision and the `npm install -g` row in `docs/online.md`: in the runtime image there is no npm to install with.

## Alternatives

### Keep the toolchains in the runtime image

**Pros**: one image. **Cons**: a larger image, rebuilt whenever a toolchain changes.

## Consequences

The runtime image is smaller. Agents that need `npm` or `make` (including MCP servers installed with `npm install -g`) need the dev image. Checked by building the image, running `deploy/docker/smoke-test.sh` on it, and confirming `command -v node npm make` finds nothing.
