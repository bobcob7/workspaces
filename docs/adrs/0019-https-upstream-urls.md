# 0019. Build HTTPS upstream URLs; SSH through git's `insteadOf`

- Status: Accepted (2026-10-18)
- Date: 2026-10-18

## Context

### Problem

A spec without a URL, such as `buff-bear` or `gitlab.com/team/svc`, resolves
to a canonical id, `host/owner/name`
([0007](0007-repo-spec-syntax-and-resolution.md)). To probe, clone, and
fetch it, and to set the workspace's `origin`
([0009](0009-workspace-repository-remotes.md)), `ws` needs a URL. No ADR
says how that URL is built. An early README draft had a
`defaults.protocol = "ssh"` key that no ADR backed.

Which protocol does `ws` use for upstream URLs, and how does a user choose
another?

### What a solution needs

1. **Works with no setup** for public repos, on any host 0007 accepts.
2. **Private repos and pushing work** with the credentials the user already
   has for git.
3. **One way to choose**, not a `ws` setting that disagrees with the user's
   git config.
4. **Changing the choice later** reaches existing caches and workspaces.
5. **Fast** probes and fetches, since every workspace creation fetches.

### Relevant facts

- **Without credentials.** Over HTTPS with no credential helper,
  `git ls-remote` of a public GitHub repo succeeded. Over SSH with no key or
  agent, it failed: "git@github.com: Permission denied
  (publickey)"[^bench-proto]. GitHub: HTTPS URLs "are available on all
  repositories, regardless of visibility"; SSH requires that "you must
  generate an SSH keypair on your computer and add the public key to your
  account on GitHub"[^gh-remotes].
- **A missing repo.** Over HTTPS with no credential helper, `ls-remote` of a
  repo that doesn't exist failed with "could not read Username for
  'https://github.com': terminal prompts disabled"; over SSH with a key,
  "ERROR: Repository not found."[^bench-proto]. Both exit 128.
- **Other hosts.** HTTPS URLs built as `https://<id>.git` worked without
  credentials on gitlab.com, including a nested group
  (`gitlab-org/api/client-go`), and codeberg.org[^bench-hosts].
- **Firewalls.** "Companies often permit outgoing traffic only on the
  standard TCP ports 80 (HTTP) and 443 (HTTPS), blocking … TCP port 22
  (SSH)", which is why `go get` uses HTTPS[^go-faq]. GitHub: HTTPS URLs
  "work even if you are behind a firewall or proxy"[^gh-remotes].
- **Speed.** `ls-remote` of rust-lang/cargo from this machine, 5 alternating
  runs: HTTPS median 0.57 s, SSH median 0.93 s[^bench-proto].
- **git's own rewriting.** `url.<base>.insteadOf` rewrites any URL that
  starts with a prefix, "even for a never-before-seen repository on the
  site"; `pushInsteadOf` does it for pushes only[^git-config]. With
  `url."git@github.com:".insteadOf = https://github.com/` in the global
  config, `ls-remote` of an `https://github.com/…` URL ran `ssh
  git@github.com`. `remote.origin.url` kept the HTTPS URL, while
  `git remote -v` and fetches used SSH. That included a workspace-style repo
  made before the setting existed. With `pushInsteadOf` instead, fetch used
  HTTPS and push SSH[^bench-proto]. Go's FAQ gives the same recipe to use SSH
  for `go get`[^go-faq].
- **Existing choices.** `gh` has a `git_protocol` setting, `{https | ssh}`,
  "default `https`"[^gh-config]; it is `https` on this machine.
  `gh auth setup-git` makes `gh` git's credential helper for the hosts it's
  logged in to[^gh-setup-git]. macOS's git ships with
  `credential.helper = osxkeychain`[^bench-proto].

## Decision

We will build every upstream URL as HTTPS and leave the protocol to git's
own configuration:

- **A spec without a URL** resolves to `https://<host>/<owner>/<name>.git`,
  with nested groups kept (`https://gitlab.com/a/b/c.git`). This is used for
  0007's `ls-remote` probe, the cache clone, and the workspace `origin`.
- **A URL spec** is used as given, whatever its scheme.
- **The cache's `remote.origin.url` is the repo's upstream URL.** It is set
  when the repo is first cached, and every workspace's `origin` copies it.
  A later URL spec for the same id that differs from it doesn't change it.
  `ws` uses the cached URL and adds a warning naming both. To change it,
  run `git -C "$(ws cache path <spec>)" remote set-url origin <url>`.
- **`ws` has no protocol setting.** Users who want SSH configure git, for
  example:

  ```
  git config --global url."git@github.com:".insteadOf https://github.com/
  # or SSH for pushes only:
  git config --global url."git@github.com:".pushInsteadOf https://github.com/
  ```

- `ws` runs git with `GIT_TERMINAL_PROMPT=0`, so a missing credential fails
  instead of waiting for input. The failure maps to `repo_not_found` for a
  probe and to `fetch_failed` or `clone_failed` otherwise
  ([0016](0016-output-contract.md)), with git's message in `details`.

### Why this option

1. **No setup.** HTTPS reached public repos on GitHub, GitLab, and Codeberg
   with no credentials. SSH needs a registered key even for public repos.
2. **Existing credentials.** Private repos and pushes use whatever the user
   set up for git: a credential helper, `gh auth setup-git`, or SSH via
   `insteadOf`.
3. **One place to choose.** The choice lives in git's config, which also
   covers `git clone`, `go get`, and every other tool, so they can't
   disagree.
4. **Reaches existing workspaces.** `insteadOf` is applied each time git
   connects, so it changed a repo that already existed.
5. **Fast.** HTTPS was faster in this measurement; one machine and network,
   so we read it only as "not slower".

Weak points: SSH users must know about `insteadOf`, so the README and Q&A
show it. A private repo that isn't cached, probed over HTTPS without a
credential helper, is reported as not found, the same as a missing one.
The URL a user sees in `remote.origin.url` may differ from the one git
uses.

## Alternatives

### A `defaults.protocol` key (`ssh` or `https`)

**Pros**

- Discoverable in `config.toml`, next to `defaults.host` and
  `defaults.owners`.
- Makes the stored `origin` URL match what git uses.

**Cons**

- A second setting next to git's `insteadOf`, which still overrides
  it[^bench-proto].
- Changing it doesn't change caches that already exist, since their URL is
  fixed at first clone.
- One value for every host, unless it grows per-host settings.
- Could be added later on top of this decision if `insteadOf` proves too
  obscure.

### SSH by default

**Pros**

- Works for private repos and pushing with an SSH key and no token, as on
  this machine[^bench-proto].
- The URL form most people paste from GitHub after setting up a key.

**Cons**

- Fails for public repos without a key registered on that
  host[^bench-proto][^gh-remotes].
- Blocked where only ports 80 and 443 are allowed[^go-faq].
- Slower in this measurement[^bench-proto].
- Anyone wanting HTTPS can't use `insteadOf` the other way without
  rewriting every SSH URL.

### Follow `gh`'s `git_protocol`

**Pros**

- Reuses a choice the user may already have made[^gh-config].

**Cons**

- Applies only to hosts `gh` knows, and `gh` may not be installed.
- Its default is `https`, so for most users it gives the same result as
  this decision.

### Try SSH, then fall back to HTTPS

**Pros**

- Uses SSH where a key works, without configuration.

**Cons**

- Two connections whenever SSH fails, and git exits 128 both for "no key"
  and "not found"[^bench-proto], so `ws` can't tell why it fell back.
- The protocol a repo ends up with depends on the machine it was first
  cached on.

## Consequences

- The README's config example has no `protocol` key, and the README and
  Q&A show the `insteadOf` lines for SSH users.
- `ws cache list --json` and the workspace's `origin` show the HTTPS URL
  unless the spec was an SSH URL.
- Conformance: a unit test that ids map to
  `https://<host>/<owner…>/<name>.git`; an integration test that, with a
  temporary `GIT_CONFIG_GLOBAL` holding an `insteadOf` rule to a local
  path, `ws` fetches from the rewritten URL; a test that a differing URL
  spec leaves the cache's URL alone and adds a warning; and a review rule
  that `ws` never reads protocol settings of its own.
- **Unverified:** pushing from a Claude Code background session, under
  each protocol. Its environment comes from the daemon
  ([0018](0018-restart-claude-daemon.md)), so SSH needs an agent socket or
  an unencrypted key, and HTTPS a credential helper that works without a
  terminal. Check by having a test agent push a branch to a scratch repo
  with each setup.
- **Unverified:** whether a probe through a credential helper that has no
  entry for the host (such as `osxkeychain`) fails without a prompt. Check
  on macOS with an empty keychain entry and `GIT_TERMINAL_PROMPT=0`.

## References

[^bench-proto]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, git 2.50.1 (Apple Git-155), home network, `GIT_TERMINAL_PROMPT=0`. Timing: `git ls-remote <url> HEAD` for `https://github.com/rust-lang/cargo.git` and `git@github.com:rust-lang/cargo.git`, 5 alternating runs each, timed with Python `time.time()`: HTTPS 0.628, 0.588, 0.542, 0.558, 0.568 s; SSH 1.106, 0.822, 0.926, 0.955, 0.873 s. No-key SSH: `SSH_AUTH_SOCK` unset, `GIT_SSH_COMMAND="ssh -F /dev/null -o IdentitiesOnly=yes -o IdentityFile=/dev/null -o BatchMode=yes"`: "git@github.com: Permission denied (publickey).", exit 128. No-helper HTTPS: `git -c credential.helper= ls-remote`, public repo exit 0; `bobcob7/does-not-exist-ws-test` "fatal: could not read Username for 'https://github.com': terminal prompts disabled", exit 128. SSH with the user's key, same missing repo: "ERROR: Repository not found.", exit 128. `insteadOf`: a temporary `GIT_CONFIG_GLOBAL` with `[url "git@github.com:"] insteadOf = https://github.com/`; `GIT_TRACE=1 git ls-remote https://github.com/octocat/Hello-World.git` showed `run_command: … ssh … git@github.com 'git-upload-pack …'`; after `git clone` of the HTTPS URL, `config remote.origin.url` printed the HTTPS URL and `remote -v` the SSH one. A repo cloned from a local bare copy, with `origin` then added as the HTTPS URL, showed SSH in `remote -v` and fetched over SSH. With `pushInsteadOf` instead, `remote -v` showed HTTPS for fetch and SSH for push. `git config --show-origin` listed `credential.helper osxkeychain` from `/Library/Developer/CommandLineTools/usr/share/git-core/gitconfig`; `gh config get git_protocol` printed `https`.
[^bench-hosts]: Measured 2026-10-18, same machine. `git -c credential.helper= ls-remote <url> HEAD` returned a hash for `https://gitlab.com/gitlab-org/cli.git`, `https://gitlab.com/gitlab-org/api/client-go.git`, and `https://codeberg.org/forgejo/forgejo.git`.
[^gh-remotes]: GitHub Docs, "About remote repositories." <https://docs.github.com/en/get-started/git-basics/about-remote-repositories> (accessed 2026-10-18). "The `https://` clone URLs are available on all repositories, regardless of visibility. `https://` clone URLs work even if you are behind a firewall or proxy." For SSH, "you must generate an SSH keypair on your computer and add the public key to your account on GitHub."
[^go-faq]: The Go Programming Language, "Frequently Asked Questions," section "Why does "go get" use HTTPS when cloning a repository?" <https://go.dev/doc/faq> (accessed 2026-10-18). "Companies often permit outgoing traffic only on the standard TCP ports 80 (HTTP) and 443 (HTTPS), blocking outgoing traffic on other ports, including TCP port 9418 (git) and TCP port 22 (SSH)." "`Git` can also be configured to use SSH in place of HTTPS for URLs matching a given prefix," followed by an `insteadOf = https://github.com/` example.
[^git-config]: git-config(1), git 2.50.1, `url.<base>.insteadOf`: "Any URL that starts with this value will be rewritten to start, instead, with <base>. … this feature allows people to specify any of the equivalent URLs and have Git automatically rewrite the URL to the best alternative for the particular user, even for a never-before-seen repository on the site." `url.<base>.pushInsteadOf`: "Any URL that starts with this value will not be pushed to; instead, it will be rewritten to start with <base>, and the resulting URL will be pushed to."
[^gh-config]: GitHub CLI 2.81.0, `gh config --help` (2026-10-18). "`git_protocol`: the protocol to use for git clone and push operations `{https | ssh}` (default `https`)".
[^gh-setup-git]: GitHub CLI 2.81.0, `gh auth setup-git --help` (2026-10-18). "This command configures `git` to use GitHub CLI as a credential helper." "By default, GitHub CLI will be set as the credential helper for all authenticated hosts."
