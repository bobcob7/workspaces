# 0004. One `ws` binary with subcommands

- Status: Superseded by [0014](0014-command-set.md) (2026-10-18)
- Date: 2026-10-17

## Context

[0002](0002-implementation-language.md) settled on a single Go binary. The
project has four jobs to expose:

- create a workspace
- list workspaces
- clean workspaces up
- manage the repo cache

The cache job itself has several operations (add, update, list, search,
resolve, path, remove). The open question was how one binary presents all of
these.

## Decision

We will ship one binary named `ws`, organized as subcommands:

```
ws init <spec>...          create a workspace
ws list                    list workspaces
ws cleanup <ws>...         delete workspaces safely
ws cache <op> [args]       add | update | list | search | resolve | path | remove
```

This is the shape for now. Separate command names can be added later as
shell aliases or symlinks without changing the binary.

## Alternatives

**Multi-call binary** (one binary dispatched on `argv[0]`, installed as
`init-workspace`, `cleanup-workspace`, and so on). It needs a symlink per
command after `go install`, which installs exactly one file per main package.
It also splits `--help` across four names.

**Separate binaries.** This violates the single-binary requirement in
[0002](0002-implementation-language.md).

## Consequences

- Install is one `go install`, or one file copied onto `PATH`.
- Agents type `ws init …` rather than `init-workspace …`. Docs and examples
  use the subcommand form.
- `ws` is a short, generic name that could collide with another tool on some
  systems. **Unverified:** check for conflicts in Homebrew and common Linux
  package repositories before the first release.
- Conformance: `ws --help` lists exactly these subcommands. Any new top-level
  subcommand needs an ADR.
