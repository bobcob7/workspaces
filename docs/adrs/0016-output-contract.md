# 0016. Output contract: one JSON object on stdout, errors as JSON on stderr, five exit codes

- Status: Accepted (2026-10-18); shell exit status amended by [0023](0023-shell-session-keeps-creation-lock.md) (2026-10-19)
- Date: 2026-10-18

## Context

### Problem

Agents call `ws` as often as people do, and one agent uses it to start
another ([0014](0014-command-set.md)). They need output they can parse and
failures they can tell apart: a repo that doesn't exist should be handled
differently from Claude Code refusing an untrusted folder. Until now the
tool docs carried a working proposal, and
[0005](0005-cli-framework.md) left cobra's "exit 1 for every error" to be
replaced by a later ADR.

What do `--json` output, error output, and exit codes look like, and which
parts are a stable contract?

### What a solution needs

1. **Machine output is stable**: a field an agent relies on doesn't move or
   change type between versions.
2. **Failures are distinguishable** by kind, both from a shell script (exit
   code) and from an agent reading text (a named error).
3. **Easy to consume**: one parse, with `jq` or any JSON library, and no
   mixing of results and diagnostics on one stream.
4. **Cheap for agents to read**: agents pay per token for everything they
   read.
5. **Conventional**: no surprises for someone who knows other CLIs.

### Relevant facts

- **Streams.** clig.dev: "The primary output for your command should go to
  `stdout`." "Anything that is machine readable should also go to
  `stdout`." "Log messages, errors, and so on should all be sent to
  `stderr`"[^clig]. It also says "Changing output for humans is usually OK"
  and recommends `--json` "to keep output stable" in scripts[^clig].
- **Exit codes in other tools.**
  - Bash: "All builtins return an exit status of 2 to indicate incorrect
    usage." A command killed by signal N gives 128+N; not found gives 127,
    not executable 126[^bash].
  - `gh`: 0 success, 1 "fails for any reason", 2 cancelled, 4 needs
    authentication, and "a particular command may have more exit
    codes"[^gh-exit].
  - sysexits(3) defines 64–78 (`EX_USAGE` 64, `EX_NOINPUT` 66,
    `EX_UNAVAILABLE` 69, `EX_NOPERM` 77, `EX_CONFIG` 78, …), starting at 64
    "to reduce the possibility of clashing with other exit statuses", and
    notes "The choice of an appropriate exit value is often
    ambiguous"[^sysexits].
  - cobra exits 1 for every error unless the program overrides
    it[^bench-cli].
- **JSON shapes in other tools.** `gh … --json <fields>` prints a top-level
  array for lists (`[{"number":14727,"title":"…"}]`), pretty-printed only
  "when connected to a terminal"[^gh-json].
- **Size.** A `ws list` result for 20 workspaces with two repos each was
  8,010 bytes compact and 12,742 bytes indented with two spaces, 1.59×
  larger[^bench-size].
- **`ws shell`** exits with the shell's own exit code
  ([0014](0014-command-set.md)), which can be any value from 0 to 255.

## Decision

We will treat `--json` output, error codes, and exit codes as a stable
contract, and human output as free to change.

**Output with `--json`.**

- Every command that produces results takes `--json`: `ws claude`,
  `ws list`, `ws rm`, `ws restore`, and the `ws cache` operations.
  `ws shell` and `ws llm` don't.
- stdout gets exactly one JSON **object** followed by a newline, compact
  unless stdout is a terminal, where it's indented. Lists sit under a
  plural key, such as `{"workspaces": [...]}`.
- Every object has a `warnings` array, empty when there are none, for
  problems that didn't stop the command, such as `claude agents` failing
  during `ws list`.
- Field names are `snake_case`. Times are RFC 3339 in UTC with second
  precision (`2026-10-18T08:12:00Z`). Paths are absolute. IDs are strings.
  A field with no value is present as `null`, never omitted.
- Enum values are lowercase strings. Consumers must accept values they
  don't know.

**Errors.**

- A failure prints nothing on stdout. On stderr it prints `ws: <message>`,
  and lines of advice where there is a fix (for example the folder to
  trust). With `--json`, stderr gets one line instead:
  `{"error": {"code": "<code>", "message": "<text>", "details": {...}}}`.
- Commands that act on several workspaces (`ws rm`, `ws restore`) report
  each one in the stdout result, with an `error` object (or `null`) per
  item. If any item failed, the exit code is that of the first failure.

**Error codes and exit codes.**

| Exit | Meaning                          | Error codes |
| ---- | -------------------------------- | ----------- |
| 0    | Success                          |             |
| 1    | Failed                           | `fetch_failed`, `clone_failed`, `git_failed`, `claude_missing`, `claude_untrusted`, `claude_failed`, `internal` |
| 2    | Usage error                      | `usage`, `spec_invalid`, `name_invalid` |
| 3    | Not found or ambiguous           | `repo_not_found`, `repo_ambiguous`, `ref_not_found`, `workspace_not_found` |
| 4    | Refused, to protect something    | `workspace_exists`, `shell_running`, `sessions_busy` |

New error codes may be added under an existing exit code. Adding an exit
code, or moving an error code to another one, needs a new ADR.

`ws shell` is the exception: once the shell has started, `ws` exits with the
shell's exit code, so a value from 1 to 4 after that point is the shell's,
not `ws`'s. Errors before the shell starts use the table.

**Stability.** Fields and error codes are only ever added. Removing or
renaming a field, changing its type, or changing an exit code needs a new
ADR. Tables, messages, and other human output may change at any time;
`ws list -q` (one id per line) is stable. Progress goes to stderr only
when stderr is a terminal and `--quiet` isn't set. Color is used only on a
terminal and never when `NO_COLOR` is set.

### Why this option

1. **Stable.** The additive-only rule and "always present, `null` when
   empty" mean a consumer that worked once keeps working. The top-level
   object leaves room to add fields like `warnings` without breaking
   anyone.
2. **Distinguishable.** Five exit codes cover what a script branches on.
   The error code names the exact problem for agents and scripts that need
   more, such as telling `claude_untrusted` from `claude_failed`.
3. **Easy to consume.** stdout holds only the result; a failure leaves it
   empty, so `ws claude --json … | jq -r .session` never sees an error
   object in place of a result.
4. **Cheap.** Compact JSON for pipes and agents; indentation only on a
   terminal, where a person reads it.
5. **Conventional.** 2 for usage matches bash builtins; 1 for general
   failure matches `gh` and cobra; streams follow clig.dev.

The weak points: the per-command result shapes still have to be written
down (in `ws llm <command>`), and exit codes 3 and 4 mean something
different from `gh`'s 4 ("needs authentication").

## Alternatives

### sysexits codes (64–78)

**Pros**

- A documented, shared vocabulary, chosen to avoid clashing with
  program-specific codes[^sysexits].

**Cons**

- The categories don't match `ws`'s: "not found" could be `EX_NOINPUT` or
  `EX_NOUSER`, a running shell `EX_TEMPFAIL` or `EX_NOPERM`. The man page
  itself calls the choice "often ambiguous"[^sysexits].
- Few people or agents know them, so `ws` would document them anyway.

### Only 0 and 1, with the error code in JSON

**Pros**

- One rule, like `gh`'s default[^gh-exit]; all detail in the named error.

**Cons**

- Shell scripts without `--json` can't tell "not found" from "failed"
  without parsing stderr text.

### Errors as JSON on stdout

**Pros**

- Only one stream to read; some agents capture stdout only.

**Cons**

- A script that skips the exit-code check reads an error object as the
  result. clig.dev puts errors on stderr[^clig].

### Top-level arrays for lists, as `gh` does

**Pros**

- One less level for `jq` (`.[]` instead of `.workspaces[]`); matches
  `gh`[^gh-json].

**Cons**

- Nowhere to add `warnings` or other metadata later without breaking
  consumers.

### Always indented JSON

**Pros**

- Readable in a log or an agent transcript without reformatting.

**Cons**

- 1.59× the bytes for a 20-workspace list[^bench-size], paid by every
  agent that reads it.

### A schema version field

**Pros**

- A consumer can check it and refuse an incompatible version.

**Cons**

- With additive-only changes, the number would never change; breaking
  changes are already gated by an ADR.

## Consequences

- The tool docs change: list results move under plural keys, every result
  gains `warnings`, and each page's error table lists error codes.
- Each command's guide in `ws llm` documents its JSON fields and error
  codes, extending the coverage test from [0014](0014-command-set.md).
- `internal/cli` maps errors to codes in one place, as
  [0005](0005-cli-framework.md) anticipated.
- Conformance: golden-file tests of each command's `--json` output; a test
  that every error code exits with the code in the table; a test that
  stdout is empty on failure and holds exactly one JSON object on success,
  including when there are warnings; and a review rule that no field is
  removed or renamed.

## References

[^clig]: A. Prasad, B. Firshman, C. Tashian, E. Parish, "Command Line Interface Guidelines." <https://clig.dev/> (accessed 2026-10-18) (secondary). "The primary output for your command should go to `stdout`." "Anything that is machine readable should also go to `stdout`—this is where piping sends things by default." "Log messages, errors, and so on should all be sent to `stderr`." "Changing output for humans is usually OK." "Encourage your users to use `--plain` or `--json` in scripts to keep output stable".
[^bash]: bash(1), section "EXIT STATUS", macOS 26.6.2. "When a command terminates on a fatal signal N, bash uses the value of 128+N as the exit status. If a command is not found, the child process created to execute it returns a status of 127. If a command is found but is not executable, the return status is 126." "All builtins return an exit status of 2 to indicate incorrect usage."
[^gh-exit]: GitHub CLI 2.81.0, `gh help exit-codes` (2026-10-18). "If a command fails for any reason, the exit code will be 1"; "If a command is running but gets cancelled, the exit code will be 2"; "If a command requires authentication, the exit code will be 4"; "It is possible that a particular command may have more exit codes".
[^gh-json]: GitHub CLI 2.81.0, `gh help formatting` (2026-10-18): "When connected to a terminal, the output is automatically pretty-printed." Observed: `gh pr list --repo cli/cli --limit 1 --json number,title` piped to a file printed `[{"number":14727,"title":"fix(issue create): resolve --type before creating the issue"}]`.
[^sysexits]: sysexits(3), macOS 26.6.2 (man page dated March 31, 1996), and `sysexits.h` from the macOS SDK. "Error numbers begin at EX__BASE to reduce the possibility of clashing with other exit statuses that random programs may already return." BUGS: "The choice of an appropriate exit value is often ambiguous."
[^bench-cli]: See [0005](0005-cli-framework.md), footnote `bench-cli`: without `SilenceErrors` and an explicit exit, cobra exits 1 for every error.
[^bench-size]: Measured 2026-10-18 with Python 3 `json.dumps` on a `ws list`-shaped object: 20 workspaces, each with id, path, creation time, an agent session, and two repos, plus an empty `warnings`. `separators=(",", ":")`: 8,010 bytes; `indent=2`: 12,742 bytes. Deterministic, one run.
