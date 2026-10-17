# 0005. Use cobra for command parsing

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

[0004](0004-single-binary-with-subcommands.md) gives `ws` two levels of
commands (`ws cache search …`), along with per-command flags, positional
argument checks, and `--help` at every level. Agents also benefit from shell
completion when a human drives them interactively.

The same command tree (`init`, `list`, `cleanup`, and `cache` with 7
operations) was built twice[^bench-cli]:

|                        | stdlib `flag` + `switch` | cobra v1.10.2      |
| ---------------------- | ------------------------ | ------------------ |
| Third-party modules    | 0                        | 2 (cobra, pflag)   |
| Binary size (stripped) | 1.66 MB                  | 2.52 MB            |
| Startup (`ws list`)    | 2.11 ms                  | 2.33 ms            |
| Nested help, arg validation, shell completion | written by hand | built in |

cobra generated a working zsh completion script (`ws completion zsh`) and
rejected `ws init` with no arguments ("requires at least 1 arg(s)") with no
extra code[^bench-cli]. Its README lists "Nested subcommands", "Fully
POSIX-compliant flags (including short & long versions)", "Automatic help
generation for commands and flags", and shell autocompletion for "bash, zsh,
fish, powershell"[^cobra].

## Decision

We will build the command tree with `github.com/spf13/cobra`:

- One file per command under `internal/cli`.
- `SilenceUsage: true` and `SilenceErrors: true` on the root command, so
  error output and exit codes stay under our control rather than cobra's
  default of exit 1 for every error[^bench-cli].
- The `RunE` functions only parse input and call into the domain packages.
  They contain no business logic.

## Alternatives

**stdlib `flag` with a hand-written dispatcher.** It has no dependencies and
is 0.86 MB smaller, but two-level help, argument validation, and completions
would all be ours to write and maintain. The 0.22 ms startup difference is
noise next to a single git call (8–9 ms,
[0003](0003-use-git-cli.md)).

**urfave/cli.** It is comparable in scope, but cobra "is used in many Go
projects such as Kubernetes, Hugo, and GitHub CLI"[^cobra]. Agents and users
are likely to have seen its help format and flag behavior before.

## Consequences

- Adds 2 modules and about 0.9 MB to the binary.
- Completion scripts for bash, zsh, and fish come at no extra cost.
- cobra's default exit code (1 for all errors) must be overridden once exit
  codes are specified. That is a later ADR on the output contract.
- Conformance: `cobra` is imported only by `internal/cli` and `cmd/ws`.

## References

[^bench-cli]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, Go 1.27.1. Two prototypes with the same commands and flags (`init --name --offline`, `list`, `cleanup`, `cache <7 ops>`), built with `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'`. Modules counted with `go list -deps` (non-standard). Startup: `ws list`, 200 runs after 20 warm-up runs, median reported, timed from Python `time.perf_counter`. The cobra prototype exited 1 for both an unknown command and a missing argument.
[^cobra]: spf13/cobra README, Overview. <https://github.com/spf13/cobra> (accessed 2026-10-17).
