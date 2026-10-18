# 0020. Repo specs: no local paths, ports stay in ids, passwords are rejected

- Status: Accepted (2026-10-18)
- Date: 2026-10-18

## Context

### Problem

[0007](0007-repo-spec-syntax-and-resolution.md) defines specs and canonical
ids for repos on forges, `host/owner/name`. Implementing it left three
cases it doesn't settle:

1. **Local repos.** git can clone `/srv/repos/x.git` or
   `file:///srv/repos/x.git`, which have no host to build an id from.
   [0003](0003-use-git-cli.md) wants integration tests to use local bare
   repos as upstreams.
2. **Ports.** `ssh://git@h.com:2222/o/r` and `ssh://git@h.com/o/r` could be
   one repo or two.
3. **Passwords.** `https://user:pass@h.com/o/r` is a valid clone URL.

Which of these are specs, and what ids do they get?

### What a solution needs

1. **Ids never collide** between different repos, and a repo always gets
   the same id.
2. **Ids work as cache paths** ([0008](0008-repository-cache.md)).
3. **No secrets end up on disk or in messages** that `ws` writes.
4. **Integration tests can use local bare repos** through the same code
   path as real upstreams.
5. **Only what's needed.** The maintainer doesn't need local repos as
   specs.

### Relevant facts

- **git accepts local repos** as `/path/to/repo.git/` or
  `file:///path/to/repo.git/`[^git-urls].
- **`insteadOf` reaches local repos.** With a temporary global config
  holding `url."<dir>/up/".insteadOf = https://example.test/`,
  `git ls-remote`, `git clone --bare`, and `git fetch --prune` of
  `https://example.test/o/r.git` all used the local bare repo, and the
  clone's `remote.origin.url` stayed
  `https://example.test/o/r.git`[^bench-insteadof].
- **A colon in a cache path is safe.** git reads `host:path` as scp-style
  only "if there are no slashes before the first colon"[^git-urls]. A bare
  clone into `…/cache/h.com:2222/o/n.git` and a clone from it both worked
  without starting `ssh`[^bench-colon].
- **URLs are stored and echoed.** [0019](0019-https-upstream-urls.md)
  stores a URL spec in the cache's `remote.origin.url`, every workspace
  copies it ([0009](0009-workspace-repository-remotes.md)), and spec errors
  quote the spec.

## Decision

We will extend 0007's specs as follows.

**No local repos.** A spec that starts with `/`, `./`, `../`, or `~`, is
`.` or `..`, or is a `file://` URL is rejected as `spec_invalid`: "local
paths and file:// URLs aren't repo specs; use a URL or host/owner/name".
Integration tests use specs such as `example.test/o/r` with a temporary
`GIT_CONFIG_GLOBAL` whose `insteadOf` rule points `https://example.test/`
at a directory of local bare repos.

**Ports.** A scheme URL's port is part of the id's host unless it is the
scheme's default (https 443, http 80, ssh 22, git 9418):
`ssh://git@h.com:2222/o/r` has id `h.com:2222/o/r`, and
`ssh://git@h.com:22/o/r` has id `h.com/o/r`. scp-style URLs have no port.

**Passwords.** A URL with a password, `https://user:pass@…`, is rejected as
`spec_invalid`, telling the user to use a git credential helper. A user
name alone is allowed. Error messages show the URL with the password
masked.

### Why this option

1. **No collisions.** Every id has a real host. Two ports on one host get
   separate ids, since nothing guarantees they serve the same repos.
2. **Cache paths.** `h.com:2222/o/r` is a valid path, and git still treats
   it as local.
3. **No secrets.** A rejected password is never stored or cloned with, and
   the error masks it.
4. **Tests.** Test upstreams go through the same spec, id, and URL code as
   real ones, plus the `insteadOf` rewriting 0019 relies on for SSH users.
5. **Only what's needed.** No id scheme for local paths to design and
   maintain.

Weak points: users with local mirrors can't name them directly, though an
`insteadOf` rule works for them as it does for tests. One repo reached on a
non-default port and through a port-forward on the default port gets two
cache entries.

## Alternatives

### Local repos as specs, with `_local/<path>` ids

An absolute path or `file://` URL gets the id `_local` followed by the
path. `_` can't appear in a host name[^rfc952], so the id can't collide
with a forge's.

**Pros**

- Tests and local mirrors name local repos directly, with no git config.

**Cons**

- A fifth kind of spec, with case-sensitive ids unlike 0007's lowercase
  ones, and a rule needed for whether bare names match local repos.
- Moving a local repo changes its id.
- Not needed by the maintainer.

### Drop ports from ids

**Pros**

- The same repo on two ports shares one cache entry.

**Cons**

- Two different servers on one host share an id, and one's fetch would
  overwrite the other's refs in the cache.

### Accept passwords, masking them in output

**Pros**

- URLs that work with `git clone` work with `ws`.

**Cons**

- The password is still written to the cache's and every workspace's git
  config ([0009](0009-workspace-repository-remotes.md)).

## Consequences

- `internal/spec` rejects local paths and `file://` URLs, and ids may
  contain a port.
- Integration tests for the cache and workspaces need a helper that creates
  local bare repos and a temporary `GIT_CONFIG_GLOBAL` with the `insteadOf`
  rule, and run git with it. Tests that touch the real network aren't
  needed.
- Conformance: unit tests in `internal/spec` for each rule above, including
  that a spec error never contains the password.

## References

[^git-urls]: git-clone(1), section "GIT URLS." <https://git-scm.com/docs/git-clone> (accessed 2026-10-18). The scp-like syntax "is only recognized if there are no slashes before the first colon. This helps differentiate a local path that contains a colon." Local repos may be given as "`/path/to/repo.git/`" or "`file:///path/to/repo.git/`".
[^bench-insteadof]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, git 2.50.1, `GIT_TERMINAL_PROMPT=0`. A local bare repo `<dir>/up/o/r.git` with one commit on `main`; `GIT_CONFIG_GLOBAL` pointing at a file holding only `[url "<dir>/up/"] insteadOf = https://example.test/`. `git ls-remote https://example.test/o/r.git` listed `HEAD` and `refs/heads/main`; `git clone --bare https://example.test/o/r.git <dir>/cache.git` succeeded, and its `remote.origin.url` printed `https://example.test/o/r.git`; `git -C <dir>/cache.git fetch --prune origin` succeeded.
[^bench-colon]: Measured 2026-10-18, same machine. `git clone --bare <src> <dir>/cache/h.com:2222/o/n.git` exited 0; `GIT_TRACE=1 git clone <dir>/cache/h.com:2222/o/n.git <dir>/ws` ran no `ssh` command, and the clone had the source's commit.
[^rfc952]: RFC 952, "DOD Internet Host Table Specification," section "ASSUMPTIONS." <https://www.rfc-editor.org/rfc/rfc952> (accessed 2026-10-18). A name "is a text string up to 24 characters drawn from the alphabet (A-Z), digits (0-9), minus sign (-), and period (.)."
