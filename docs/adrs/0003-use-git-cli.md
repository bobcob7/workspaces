# 0003. Shell out to the git binary

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

The tool clones into a cache, fetches, creates workspace repos from the
cache, configures remotes, checks for uncommitted and unpushed work, and
locks repos during updates. It must authenticate the way the user's own git
does, so that whatever works in a terminal also works here.

Agents working inside a workspace run `git` themselves to commit and push.
Dropping the git binary from this tool therefore wouldn't remove it from the
system.

Within Go ([0002](0002-implementation-language.md)), the options are to run
the `git` binary, to use go-git (pure Go), or to bind libgit2 through cgo.
go-git v5.19.3 was measured against git 2.50.1 doing the same work, in the
same session[^bench-gogit]:

|                                          | go-git    | git binary |
| ---------------------------------------- | --------: | ---------: |
| Create workspace from cache — tokio      | 1.94 s    | 0.19 s     |
| Create workspace from cache — cargo      | **8.43 s** | 0.39 s    |
| Extra disk per workspace — cargo         | **82 MB** | 1.3 MB     |
| Initial cache clone — cargo              | 12.7 s    | 4.1 s      |
| No-op fetch — cargo                      | 0.63 s    | 0.45 s     |
| Dirty check (`status`) — cargo           | 235 ms    | 39 ms      |
| Prototype binary size                    | 9.3 MB    | 2.6 MB¹    |

¹ The standard-library prototype from [0002](0002-implementation-language.md).

On cargo, creating a workspace with go-git (8.4 s) took longer than a full
clone straight from GitHub with the git binary (4.1 s). The difference comes
from cloning a local path: git hard-links object files "when
possible"[^git-clone], while go-git copied all 82 MB through its file
transport.

Authentication and config gaps in go-git v5.19.3, from its source and tests:

- **No credential helpers.** No code path reads `credential.helper` or runs
  `git credential`[^gogit-src]. HTTPS logins through `gh auth`, osxkeychain,
  and similar helpers don't work.
- **Partial `~/.ssh/config`.** Only `Hostname` and `Port` are read[^gogit-ssh].
  The maintainer's own SSH config uses `User`, `IdentityFile`, and
  `ProxyCommand`, all of which go-git ignores.
- **SSH auth defaults to ssh-agent only**[^gogit-ssh].
- **Global `insteadOf` rules ignored.** With
  `url."https://github.com/".insteadOf gh:` in the global gitconfig, git
  cloned `gh:BurntSushi/ripgrep`. go-git treated `gh` as an SSH host and
  failed[^bench-insteadof]. A third-party bug report found the same
  thing[^git-bug] (secondary).
- **Missing operations.** go-git lists `stash`, `gc`, and `prune` as
  unsupported[^gogit-compat]. The cache could never be compacted, and the
  stash check in the cleanup safety checks would have to be built by hand.

Two things go-git does well: git accepted the go-git workspaces as valid
(`fsck` clean, `@{upstream}` and `remote.pushDefault` correct), and
concurrent unlocked fetches failed less often than with git (24 of 400
vs. 348 of 400) and never left refs wrong[^bench-gogit]. A lock is still
needed either way.

## Decision

We will run the `git` binary on `PATH` for every git operation, through one
package, `internal/git`, which:

- runs `exec.CommandContext(ctx, "git", "-C", dir, …)` and never depends on
  the process working directory
- sets `GIT_TERMINAL_PROMPT=0` so a missing credential fails instead of
  hanging[^git-env]
- wraps failures with the command and git's stderr:
  `fmt.Errorf("git %s: %w: %s", args, err, stderr)`
- parses only machine-readable output: `status --porcelain=v1`,
  `for-each-ref --format`, `rev-parse`, `rev-list`
- checks `git --version` once at startup and requires **≥ 2.23**, the release
  that added `git switch`[^git-223]. Raise the minimum only together with an
  ADR naming the feature that needs it.

## Alternatives

**go-git.** It made workspace creation 10–22× slower and added 82 MB per
cargo workspace. It also loses credential helpers, most of `~/.ssh/config`,
and global `insteadOf` rules, so it doesn't work with the maintainer's own
setup.

**go-git plus a hand-written hard-link clone.** This would fix speed and disk
use, but means re-implementing part of `git clone --local`. The auth gaps
would remain.

**libgit2 through cgo** (e.g. git2go). It needs cgo, which breaks the static
`CGO_ENABLED=0` build from [0002](0002-implementation-language.md).

## Consequences

- Authentication, `insteadOf`, proxies, and signing behave exactly as they do
  in the user's terminal, because it is the same git.
- `git` ≥ 2.23 must be installed. Agents already need it inside workspaces,
  so this adds no requirement for them.
- Each git call is a process spawn: about 8–9 ms per round trip, of which
  `git --version` alone is 5.8 ms ([0002](0002-implementation-language.md)).
  That's small next to a no-op fetch (0.45 s).
- Output parsing is limited to porcelain and format strings that git keeps
  stable.
- Conformance: `go-git` and `git2go` must not appear in `go.mod`. No
  `exec.Command("git"` outside `internal/git` (check with grep in CI).
  Integration tests use local bare repos as upstreams, so they need no
  network.

## References

[^bench-gogit]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, Go 1.27.1, git 2.50.1, HTTPS to github.com, using tokio-rs/tokio and rust-lang/cargo. go-git v5.19.3 prototype built with `CGO_ENABLED=0 -ldflags='-s -w'`. Cache: init a bare repo with `+refs/heads/*:refs/heads/*` and `+refs/tags/*:refs/tags/*` and fetch it, vs `git clone --bare` plus the same refspecs (1 run each). No-op fetch with prune: 3 runs. Workspace: go-git `PlainClone` from the cache path, add `origin`, copy branch refs to `refs/remotes/origin/*`, set branch tracking and `remote.pushDefault`; vs the git sequence `clone -o cache --no-checkout` / `remote add` / `fetch cache` / `config` / `switch -C --track` (3 runs, medians). Disk: `du -sk` over the cache and the workspace `.git` together, minus the cache alone. `status`: go-git `Worktree.Status()` vs `git status --porcelain=v1`, 3 runs. Go-git workspaces checked with real git (`status -sb`, `@{upstream}`, `fsck --connectivity-only`). Concurrency: 50 trials, each force-pushing 5 branches to a local upstream and then running 8 parallel go-git fetch processes into one cache; afterwards, cache refs were compared with upstream and `git fsck --full` was run. Upstream timings depend on the network at the time of the run.
[^bench-insteadof]: Measured 2026-10-17, same machine. A temporary `HOME` whose `.gitconfig` contains only `[url "https://github.com/"] insteadOf = gh:`, with `GIT_CONFIG_NOSYSTEM=1`. `git clone --bare gh:BurntSushi/ripgrep` succeeded. The go-git prototype failed with `cannot create known hosts callback`, showing it treated `gh:` as an SSH host.
[^gogit-src]: Searched go-git v5.19.3 non-test `.go` sources for `credential` (case-insensitive) on 2026-10-17. The only matches are doc comments on `Auth` option fields and HTTP redirect handling. Nothing reads `credential.helper`.
[^gogit-ssh]: go-git v5.19.3, `plumbing/transport/ssh/common.go`. <https://github.com/go-git/go-git/blob/v5.19.3/plumbing/transport/ssh/common.go> (accessed 2026-10-17). `DefaultAuthBuilder` returns `NewSSHAgentAuth(user)`. `doGetHostWithPortFromSSHConfig` reads only `DefaultSSHConfig.Get(host, "Hostname")` and `"Port"`.
[^gogit-compat]: go-git, `COMPATIBILITY.md` (main). <https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md> (accessed 2026-10-17). `stash`, `gc`, and `prune` are marked ❌; partial clone is ⚠️ ("withheld objects are not fetched on demand").
[^git-bug]: (secondary) git-bug, "'git bug push' fails … when using insteadOf." <https://git.secluded.site/git-bug/bug/2f3489a094433817a282ad29e436630f80fd7000e6d57dc34307b7ebe4e4aedd> (accessed 2026-10-17).
[^git-clone]: git-clone(1), `--local`. <https://git-scm.com/docs/git-clone> (accessed 2026-10-17). "The files under `.git/objects/` directory are hardlinked to save space when possible."
[^git-env]: git(1), `GIT_TERMINAL_PROMPT`. <https://git-scm.com/docs/git> (accessed 2026-10-17). "If this Boolean environment variable is set to false, git will not prompt on the terminal (e.g., when asking for HTTP authentication)."
[^git-223]: GitHub Blog, "Highlights from Git 2.23," 2019-08-16. <https://github.blog/open-source/git/highlights-from-git-2-23/> (accessed 2026-10-17).
