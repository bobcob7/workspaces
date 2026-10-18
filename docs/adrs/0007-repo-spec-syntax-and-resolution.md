# 0007. `repo@ref` spec syntax and cache-first resolution

- Status: Accepted
- Date: 2026-10-17

## Context

People and agents name repos inconsistently: `buff-bear`,
`bobcob7/buff-bear`, `github.com/bobcob7/buff-bear`, or a clone URL. They
also often want several repos at different refs in one command. Every tool
needs one syntax and one resolution algorithm. Resolution must never quietly
pick the wrong repo: an agent editing the wrong repo is worse than an error.

These facts constrain the syntax:

- **`@` never appears in owner or repo names on the common forges.** GitHub
  owners allow only alphanumerics and hyphens, and repo names only
  alphanumerics, `-`, `_`, and `.`[^gh-limits]. This comes from a secondary
  source, as GitHub doesn't publish these rules. GitLab usernames and
  group and project slugs allow only letters, digits, `_`, `.`, and
  `-`[^gitlab-names]. Gitea's validation patterns allow the same characters
  in usernames and repo names[^gitea-names].
- **Hosts ignore case in owner and repo names.** `git ls-remote` returned the
  same `HEAD` for lower- and upper-case URLs on github.com, gitlab.com, and
  codeberg.org (Forgejo, a Gitea fork). GitHub's API reports the canonical
  lower-case name for an upper-case request[^bench-case].
- **`@` *can* appear in a git ref.** git only forbids the sequence `@{` and
  the bare ref `@`[^check-ref]. So the ref must be everything after the
  separator; splitting on the last `@` would be wrong.
- **`:` is forbidden in refs, but it is part of scp-style URLs**
  (`git@host:owner/name`)[^check-ref].
- **`#` starts a shell comment** at the beginning of a word[^posix-token].
- **git can only fetch a commit from a remote by its full hash.** Fetching a
  PR-only commit by its 40-character hash succeeded; the same commit
  abbreviated to 12 or 7 characters failed with `couldn't find remote
  ref`[^bench-fetch-sha].
- **Prior art:** `go get pkg@version`[^go] and `npm install pkg@version`[^npm].

## Decision

We will use this syntax:

```
spec = repo [ "@" ref ]
repo = name | owner "/" name | host "/" owner "/" name | url
```

- The separator is the **first `@` after the repo portion**. In scp-style
  URLs the leading `user@` belongs to the URL, so
  `git@github.com:bobcob7/buff-bear@feat/x` parses as ref `feat/x`.
- A trailing `.git` is stripped. When the first of three or more segments
  contains a `.`, that segment is the host, and any extra segments are nested
  owner groups.
- Every spec resolves to a canonical id, `host/owner/name`, in lowercase.
  That id determines the cache path ([0008](0008-repository-cache.md)).

Resolution uses two keys from `config.toml` ([0011](0011-config-dir.md)):
`defaults.host` (built-in default `github.com`) and `defaults.owners` (a list,
empty by default). It works as follows:

1. **URL, or `host/owner/name`:** use it directly.
2. **`owner/name`:** search the cache for `*/owner/name`. If there is one
   match, use it. If there are several, prefer `defaults.host`; otherwise
   report it as ambiguous. If there are none, use `defaults.host/owner/name`.
3. **`name`:** search the cache for `*/*/name`. If there is one match, use
   it. If there are several, prefer owners in `defaults.owners`; otherwise
   report it as ambiguous and list every candidate. If there are none, probe
   `defaults.host/<owner>/name` for each configured owner with
   `git ls-remote`; the first that succeeds wins. If none succeed, report not
   found.

Refs are resolved against the freshly fetched cache in this order: branch,
then tag, then `rev-parse <ref>^{commit}` (which also accepts an abbreviated
hash). With no ref, the default branch is used.

If none of those matches and the ref is a full 40-character hash, it is
fetched from upstream into the cache as `refs/ws/commits/<hash>`. This covers
PR-only commits, which the cache doesn't otherwise store. The ref is outside
the cache's refspecs, so `fetch --prune` leaves it alone, and the workspace
checks out the commit detached[^bench-fetch-sha]. An abbreviated hash that
isn't in the cache is an error that asks for the full hash.

## Alternatives

**`repo:ref`.** `:` already separates host and path in scp-style
URLs[^check-ref], so specs like `git@github.com:o/r:main` read ambiguously.

**`repo#ref`.** `#` turns the rest of the word into a comment when it starts
a word[^posix-token]. That makes it fragile in generated shell commands.

**`--ref` flags.** With several repos per command, each flag would have to be
paired with a repo by position, which is easy to get wrong.

**Fuzzy matching for bare names.** This violates the never-a-wrong-repo
requirement. Fuzzy matching belongs only in `ws cache search`.

## Consequences

- Once a repo is cached, its short name resolves offline.
- Short names grow more ambiguous as the cache grows. That is deliberate,
  and the error lists the candidates.
- `refs/ws/commits/*` refs accumulate in the cache. They are small, and
  `ws cache` can prune them later.
- A forge other than GitHub, GitLab, or Gitea/Forgejo needs its naming and
  case rules cited before it's supported.
- Open (deferred to a later ADR): a syntax for creating a new branch from a
  ref. One candidate is `buff-bear@main:feat/new`; `:` is unambiguous there
  because refs can't contain it[^check-ref].
- Conformance: table-driven tests for every example here, including refs
  containing `@`, `/`, and `.`.

## References

[^gh-limits]: (secondary) dead-claudia, "github-limits." <https://github.com/dead-claudia/github-limits> (accessed 2026-10-17). Usernames: "All characters must be either a hyphen (`-`) or alphanumeric". Repos: "All code points must be either a hyphen (`-`), an underscore (`_`), a period (`.`), or an ASCII alphanumeric code point."
[^gitlab-names]: GitLab Docs, "Reserved project and group names: Rules for usernames, project and group names, and slugs." <https://docs.gitlab.com/user/reserved_names/> (accessed 2026-10-17). Usernames and project or group slugs "Must contain only letters (a-zA-Z), digits (0-9), underscores (_), dots (.), or dashes (-)."
[^gitea-names]: Gitea source, `main` branch (accessed 2026-10-17). `modules/validation/helpers.go`: `` validUsernamePattern: regexp.MustCompile(`^[\da-zA-Z][-.\w]*$`) ``. `models/repo/repo.go`: `` validRepoNamePattern: regexp.MustCompile(`^[-.\w]+$`) ``. <https://github.com/go-gitea/gitea>
[^bench-case]: Measured 2026-10-17, git 2.50.1, `GIT_TERMINAL_PROMPT=0`. `git ls-remote <url> HEAD` gave identical hashes for `github.com/rust-lang/cargo` and `github.com/RUST-LANG/CARGO`, `gitlab.com/gitlab-org/cli` and `gitlab.com/GITLAB-ORG/CLI`, and `codeberg.org/forgejo/forgejo` and `codeberg.org/FORGEJO/FORGEJO`. `curl https://api.github.com/repos/RUST-LANG/CARGO` returned `"full_name": "rust-lang/cargo"`.
[^bench-fetch-sha]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1, HTTPS to github.com. A `refs/pull/*/head` commit of rust-lang/cargo absent from a bare cache (refspecs as in [0008](0008-repository-cache.md)) was fetched into empty bare repos with `git fetch --depth=1 <url> <hash>`: the 40-character hash succeeded (1.2 s); 12- and 7-character prefixes failed with `fatal: couldn't find remote ref`. Into the cache, `git fetch origin +<hash>:refs/ws/commits/<hash>` succeeded, and the ref was still present after `git fetch --prune origin`. A workspace built with the [0009](0009-workspace-repository-remotes.md) sequence then ran `git fetch cache refs/ws/commits/<hash>` and `git switch --detach <hash>`, ending with `HEAD` at the hash and a clean `git status`.
[^check-ref]: git-check-ref-format(1). <https://git-scm.com/docs/git-check-ref-format> (accessed 2026-10-17). Refs "cannot contain a sequence `@{`", "cannot be the single character `@`", and "cannot have … colon `:` anywhere."
[^posix-token]: POSIX.1-2017, Shell Command Language §2.3 "Token Recognition." <https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html> (accessed 2026-10-17).
[^go]: Go Modules Reference, "Version queries." <https://go.dev/ref/mod#version-queries> (accessed 2026-10-17).
[^npm]: npm docs, `npm install`. <https://docs.npmjs.com/cli/commands/npm-install> (accessed 2026-10-17).
