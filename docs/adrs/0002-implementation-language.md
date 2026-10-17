# 0002. Write the tools in Go

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

The project was started assuming Rust. That was never decided on the merits,
so this ADR decides it.

Requirements:

- **One self-contained executable.** No runtime or interpreter to install.
  `git` on `PATH` is the only external dependency.
- **Cheap to invoke repeatedly.** Agents call the tools in loops.
- **Mostly orchestration.** The work is running `git` subprocesses, file
  locking, filesystem operations, TOML/JSON, and some parallelism (fetching
  several repos at once). It involves no heavy computation and no unsafe
  memory handling.
- **Builds for macOS and Linux** on `amd64` and `arm64`. Windows hasn't been
  asked for, but it shouldn't be ruled out by accident.
- **Maintainable by one person.** Go is the maintainer's specialization, and
  they already have established Go conventions (constructor injection, `slog`,
  `moq`, testify).

To compare Go and Rust, the same prototype was built in each: subcommand
dispatch, running `git --version` with `GIT_TERMINAL_PROMPT=0`, an exclusive
`flock`, and JSON output. Go used only the standard library. Rust used `clap`
and `serde_json`, the usual choices, because Rust's standard library has no
argument parser or JSON[^bench-proto].

|                                    | Go 1.27.1         | Rust 1.94.1                      |
| ---------------------------------- | ----------------- | -------------------------------- |
| Third-party dependencies           | 0                 | 22 crates                        |
| Clean release build                | 2.4 s             | 3.4 s                            |
| Rebuild after a one-file change    | 0.08 s            | 0.28 s                           |
| Binary size (stripped)             | 2.6 MB            | 0.8 MB                           |
| Process startup (`version`)        | 2.4 ms            | 1.9 ms                           |
| One `git --version` round trip     | 8.9 ms            | 7.7 ms                           |
| `git --version` run directly       | 5.8 ms            | 5.8 ms                           |
| Cross-build to linux/amd64 from macOS | works, statically linked ELF | fails at link step (`linking with cc failed`) |

Cross-compiling Go needs only environment variables: "`$GOARCH` and `$GOOS`
identify the target environment … In effect, you are always
cross-compiling"[^go-cross]. With `CGO_ENABLED=0`, `file` reported both the
linux/amd64 and linux/arm64 builds as "statically linked"[^bench-cross].
Rust needed the target's standard library installed with `rustup target add`.
Even then, linking failed, because macOS's `cc` can't link Linux
binaries[^bench-cross]. Fixing that takes a cross linker such as Homebrew
`musl-cross`, `cross`, or `cargo-zigbuild`[^rust-cross] (secondary).

File locking is the one spot where Go's standard library falls short.
`syscall.Flock` doesn't exist on Windows, so the windows/amd64 build of the
Go prototype failed[^bench-cross]. Go's docs point to `golang.org/x/sys`,
which "has more system call support than this one"[^go-syscall], and it
provides `unix.Flock` and `windows.LockFileEx`[^x-sys-windows]. Rust's
standard library has had a cross-platform `File::lock` since 1.89.0[^rust-lock].

The non-compiled options were checked for the single-binary requirement:

- **Node.js.** A single executable application is made by injecting a script
  "into a copy of the `node` binary"[^node-sea], and the feature is
  "Stability: 1.1 – Active development". The local `node` 24.14.1 binary is
  114 MB, which is roughly the minimum size of the result. A bare
  `node -e ''` starts in 18 ms[^bench-interp].
- **Python.** It needs a third-party bundler to ship as one file. Starting
  `python3` and importing `json`, `subprocess`, and `argparse` takes
  67 ms[^bench-interp].

## Decision

We will write the tools in **Go**:

- Ship as one binary per platform, built with `CGO_ENABLED=0`, for
  darwin/linux × amd64/arm64.
- Prefer the standard library: `os/exec`, `encoding/json`, `log/slog`,
  `flag`.
- Use `golang.org/x/sys/unix` for `flock` rather than `syscall`, so a Windows
  port only needs a `windows.LockFileEx` implementation.
- Follow the maintainer's existing Go conventions.

How the single binary exposes the four tools (subcommands like
`ws init …`, or one binary invoked under several names) is a separate
decision for a later ADR.

## Alternatives

**Rust.** It measured better on binary size (3.3× smaller) and startup
(0.5 ms faster per call). But one `git` round trip costs 7.7–8.9 ms and a
cache fetch costs hundreds of milliseconds, so those gains don't matter for
this tool. Against that, it needs 22 third-party crates for what Go's
standard library covers, rebuilds 3.6× slower, and can't produce Linux
release builds from macOS without a cross linker. Rust's memory-safety
guarantees protect little in a tool that mostly runs subprocesses. And the
maintainer would be working outside their specialty.

**TypeScript as a Node single-executable application.** The binary is at
least as large as `node` itself (114 MB here), the feature is still marked
"Active development"[^node-sea], and startup is 7–8× slower than Go
(18 ms vs 2.4 ms).

**Python with a bundler.** It starts 28× slower than Go (67 ms vs 2.4 ms) with
just the standard modules this tool needs, and it depends on a third-party
bundler to produce one file.

## Consequences

- One `go build` per platform, run from any machine, produces a static
  release binary.
- Binaries are about 2.6 MB rather than 0.8 MB. That's not a concern for a
  developer tool.
- Earlier Rust-based plans (a Cargo workspace; `clap`, `serde`, and `fs4`)
  don't carry over. Later ADRs choose the Go equivalents.
- Windows support would need a `LockFileEx` implementation[^x-sys-windows]
  plus path and permission checks. It isn't a goal today.
- **Unverified:** binary size and startup with the real dependencies (TOML
  parsing, CLI framework if any). Re-measure once the first real build
  exists.
- Conformance: CI builds with `CGO_ENABLED=0` for all four targets. A release
  build that dynamically links anything besides the platform's system
  library fails.

## References

[^bench-proto]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1, Go 1.27.1, Rust 1.94.1. A roughly 60-line prototype in each language, functionally identical. Go: `go build -trimpath -ldflags='-s -w'`. Rust: `cargo build --release` with `strip = true`, `clap` 4 (derive), and `serde_json` 1. Clean builds: 3 runs, each with a fresh `GOCACHE` or `CARGO_TARGET_DIR`, with crates pre-fetched. Rebuilds: touch the main file, 3 runs. Startup, git round trip, and direct `git --version`: 200, 100, and 100 runs after 20 warm-up runs. Medians reported. Timed from Python with `time.perf_counter`, so process-launch overhead is included equally for every row. Dependencies counted with `go list -deps` (non-standard packages) and unique `cargo tree -e normal` entries.
[^bench-cross]: Measured 2026-10-17, same machine. Go: `GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build`. linux/amd64 and linux/arm64 succeeded, and `file` reported "statically linked". windows/amd64 failed with `undefined: syscall.Flock`. Rust: `cargo build --release --target x86_64-unknown-linux-musl` first failed with `can't find crate for core`. After `rustup target add x86_64-unknown-linux-musl` it failed with `linking with cc failed: exit status: 1`. The target was removed afterwards.
[^bench-interp]: Measured 2026-10-17, same machine. Node 24.14.1 `node -e ''` and Python 3.9.6 `python3 -I -c 'import json,subprocess,argparse'`, 100 runs each after 10 warm-up runs, medians reported. Node binary size from `ls -l` on the resolved `node` path: 119,168,512 bytes.
[^go-cross]: Go, "Installing Go from source," Optional environment variables. <https://go.dev/doc/install/source> (accessed 2026-10-17). "Note that `$GOARCH` and `$GOOS` identify the _target_ environment, not the environment you are running on. In effect, you are always cross-compiling."
[^go-syscall]: Go standard library, package `syscall`. <https://pkg.go.dev/syscall> (accessed 2026-10-17). Points to `golang.org/x/sys`: "That package has more system call support than this one, and most new code should prefer that package where possible."
[^x-sys-windows]: `golang.org/x/sys/windows`, `LockFileEx`. <https://pkg.go.dev/golang.org/x/sys/windows#LockFileEx> (accessed 2026-10-17).
[^rust-lock]: Rust standard library, `std::fs::File::lock` (stable since 1.89.0). <https://doc.rust-lang.org/std/fs/struct.File.html> (accessed 2026-10-17). "This function currently corresponds to the `flock` function on Unix with the `LOCK_EX` flag, and the `LockFileEx` function on Windows."
[^rust-cross]: (secondary) "How cross-compile on mac for linux," Rust Users Forum. <https://users.rust-lang.org/t/how-cross-compile-on-mac-for-linux/22859> (accessed 2026-10-17); `cargo-zigbuild` project page, <https://pypi.org/project/cargo-zigbuild> (accessed 2026-10-17).
[^node-sea]: Node.js 24 docs, "Single executable applications." <https://nodejs.org/api/single-executable-applications.html> (accessed 2026-10-17). "the generated blob needs to be injected into a copy of the `node` binary"; "Stability: 1.1 - Active development."
