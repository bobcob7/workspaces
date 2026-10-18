# 0011. Keep config, cache, and workspaces under one overridable config dir

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

### Problem

`ws` keeps a config file, the repo cache ([0008](0008-repository-cache.md)),
and workspaces ([0012](0012-workspace-layout.md)) on disk. Where should they
live, and how is that overridden?

### What a solution needs

1. **One setting relocates everything**, so tests and sandboxed agents can
   run in isolation.
2. **The cache and workspaces share a filesystem**, so workspace clones
   hard-link ([0009](0009-workspace-repository-remotes.md)).
3. **Workspace paths stay stable** until removal, so `claude --continue`
   finds its history ([0013](0013-cleanup-ignores-git-state.md)).
4. **Not backed up by default.** Cached repos and workspaces can be
   re-created.
5. **The maintainer's stated default:** an environment variable, otherwise
   `UserConfigDir()/workspaces`.

### Relevant facts

- **`os.UserConfigDir()`** returns `$XDG_CONFIG_HOME` or `$HOME/.config` on
  Unix, and `$HOME/Library/Application Support` on Darwin[^go-userconfig].
- **Time Machine** backs up `~/Library/Application Support` and excludes
  `~/Library/Caches`. `tmutil addexclusion` excludes a directory without
  `sudo`[^bench-tm].
- **Spaces in the macOS path are fine for Claude Code.** In a directory under
  `…/space test/Application Support/`, `claude -p --continue` recalled a
  word given in the previous session there[^bench-claude-space].
- **Temp directories can be separate filesystems.** On Debian 13, `/tmp` is
  a tmpfs by default, including on upgraded systems after a
  reboot[^debian-tmp]. A workspace there would copy every object from a
  cache in the home directory instead of hard-linking it.
- **TOML supports comments**[^toml] and JSON doesn't.

## Decision

We will resolve one root, `CONFIG_DIR`, and keep everything under it:

```
CONFIG_DIR = $WS_CONFIG_DIR, else os.UserConfigDir()/workspaces
  config.toml     generated on first run, with comments; never overwritten
  cache/          repo cache (0008)
  workspaces/     workspaces (0012)
  state/          per-workspace metadata and session state (0012)
```

- If `config.toml` is missing, it is written with explanatory comments: first
  to a temp file in `CONFIG_DIR`, then renamed into place.
- On macOS, `ws` runs `tmutil addexclusion` once on `cache/` and on
  `workspaces/` when it creates them. If that fails, it logs a warning and
  continues.

### Why this option

1. **One setting.** `WS_CONFIG_DIR=$(mktemp -d) ws …` isolates a run fully.
2. **Same filesystem.** The cache and workspaces are siblings.
3. **Stable paths.** Nothing outside `ws` cleans this directory.
4. **Backups.** Excluded on macOS by `tmutil`. On Linux it's under
   `~/.config`, which backup and dotfile tools may include. That's the weak
   point.
5. **Matches the stated default.**

## Alternatives

### Split by platform convention

Config in `UserConfigDir`, cache in `UserCacheDir`, workspaces in `$TMPDIR`.

**Pros**

- Each kind of data sits where its platform expects it. Caches would be
  excluded from Time Machine without `tmutil`.

**Cons**

- Isolating a run needs three overrides.
- The cache and workspaces may be on different filesystems: `/tmp`, the
  usual `$TMPDIR` on Linux, is a tmpfs on Debian 13.
- OS temp cleaners delete workspace files by age
  ([0006](0006-session-based-cleanup.md)), which breaks stable paths.

### Everything under `UserCacheDir`

**Pros**

- One root, and `~/Library/Caches` is already excluded from Time Machine.

**Cons**

- Users look for config in the config dir, not a cache dir.
- Anything that treats the cache dir as disposable would remove workspaces
  with live sessions.

### A bare `CONFIG_DIR` environment variable

**Pros**

- The name the maintainer first suggested.

**Cons**

- Generic enough that another tool in the same environment may set it.
  `WS_CONFIG_DIR` is namespaced.

## Consequences

- The default macOS path contains a space (`Application Support`). Paths go
  to `exec.Command` as separate arguments, never through a shell.
- The default workspace path repeats a word:
  `…/workspaces/workspaces/<id>`. Cosmetic.
- Linux users who back up or version `~/.config` need to exclude
  `workspaces/cache` and `workspaces/workspaces`.
- Conformance: tests cover `WS_CONFIG_DIR` precedence, generating the config
  only when it's missing, and never overwriting an existing one.

## References

[^go-userconfig]: Go standard library, `os.UserConfigDir` (Go 1.27.1, `go doc os.UserConfigDir`, 2026-10-17). "On Unix systems, it returns $XDG_CONFIG_HOME … if non-empty, else $HOME/.config. On Darwin, it returns $HOME/Library/Application Support."
[^bench-tm]: Measured 2026-10-17 on macOS 26.6.2. `tmutil isexcluded` reported `[Included]` for `~/Library/Application Support` and `[Excluded]` for `~/Library/Caches`. A new directory under Application Support reported `[Excluded]` after `tmutil addexclusion` (no sudo, exit 0) and was then deleted.
[^bench-claude-space]: Measured 2026-10-17, macOS 26.6.2, Claude Code 2.1.302. In `<scratch>/space test/Application Support/ws-one`: `claude -p 'Remember the code word: pineapple-42. Reply only with OK.'` replied `OK`; then `claude -p -c 'What code word did I ask you to remember? Reply with only the word.'` replied `pineapple-42`. The history directory was named after the path with spaces and `/` replaced by `-`.
[^debian-tmp]: Debian 13 (trixie) release notes, §5.1.6, "The temporary-files directory /tmp is now stored in a tmpfs." <https://www.debian.org/releases/trixie/release-notes/issues.en.html> (accessed 2026-10-17). "From trixie, the default is for the /tmp/ directory to be stored in memory using a tmpfs(5) filesystem." "For systems upgraded from bookworm, the new behavior only starts after a reboot."
[^toml]: TOML v1.0.0, "Comment." <https://toml.io/en/v1.0.0#comment> (accessed 2026-10-17). "A hash symbol marks the rest of the line as a comment, except when inside a string."
