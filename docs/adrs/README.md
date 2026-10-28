# Architecture Decision Records

An ADR records one significant decision: why it was needed, what was chosen,
and what follows from it. Read them in order to understand why the code is the
way it is.

The lifecycle is borrowed from AWS's ADR guidance[^aws-process][^aws-blog]:
one decision per ADR, immutable once accepted, replaced rather than edited,
and rejected decisions kept along with the reason. This project adds one rule
of its own: **claims are backed by sources or measurements, not assertion.**

## When to write one

Write an ADR when a decision is expensive to reverse or becomes a contract
with users:

- CLI flags, output, exit codes, spec syntax, config keys
- on-disk formats and layouts (cache, workspace metadata)
- dependencies and core approaches (e.g. "shell out to git")

Skip it for refactors, bug fixes, and anything cheap to undo. If you'd ask
"why is it like this?" six months from now, write the ADR.

## Lifecycle

```
Proposed ──► Accepted ──► Superseded by NNNN
    │
    └──────► Rejected
```

1. Copy [`template.md`](template.md) to `NNNN-kebab-title.md`, using the next
   number. Numbers are never reused.
2. Write it with `Status: Proposed` and open a PR.
3. On merge, change the status to `Accepted (YYYY-MM-DD)`. If you decide
   against it, set it to `Rejected`, add a sentence saying why, and merge it
   anyway. The reason stops the question from being reopened later[^aws-process].
4. Add it to the index below.

Accepted and rejected ADRs don't change, apart from status lines, typos, and
dead links. To change a decision, write a new ADR. In the new one, say which
ADR it supersedes and what changed. Then set the old one's status to
`Superseded by NNNN`[^aws-process].

## Writing each section

Keep the whole ADR to one or two screens. If it's longer, it probably covers
more than one decision, or it contains design exploration that belongs in a
PR description or issue.

**Title.** Name the decision, not the topic. "Clone workspaces from a
hard-linked local cache", not "Cloning".

**Context.** Write it as if you hadn't decided yet: a reader should be able
to reach their own conclusion from it. Cover three things, using
sub-headings when they help:

- *Problem:* what forces a decision, stated as a question.
- *What a solution needs:* the criteria the options will be judged on, in
  priority order.
- *Relevant facts:* measurements and citations. State facts without saying
  which option they favor. Leave out "so option X fails"; that judgment
  belongs in Decision.

**Decision.** Start with the recommendation ("We will…" or "We recommend…").
Be specific enough that someone could implement it without guessing:
commands, formats, names, defaults. Then explain *why this option*: walk
through the criteria from Context, and say where the chosen option is weak
as well as where it's strong.

**Alternatives.** Give each serious option a fair hearing, the way its
strongest advocate would make the case. List its **pros** and **cons**, back
both with citations or measurements where possible, and say whether it
could complement the decision later. "Worse" isn't a con; "corrupts the
workspace after `git gc`", with a footnote saying how that was reproduced,
is.

**Consequences.** The trade-offs you're accepting, negatives included. Also
include:

- how conformance is checked (a test, or a rule for code review)
- anything still **unverified**, marked as such, with how it will be checked
- open questions that must be settled before the ADR is accepted

**References.** Footnote definitions only (see below).

## Citing sources

### When to cite

Cite whenever the ADR states something you didn't establish yourself:

- **How a tool, library, or platform behaves:** "`git clone --shared` can
  corrupt the clone", "macOS purges `$TMPDIR`". Cite the official docs.
- **Any number** that came from somewhere else.
- **Prior art** used as an argument: "`go get` and `npm` use `pkg@version`".

Don't cite your own reasoning or judgment. Phrase it as reasoning instead
("we think an extra 0.4 s is acceptable because…"). Common knowledge needs no
citation either.

If you can't find a source, don't drop the claim silently. Mark it
**unverified** in Consequences, and say how you'll check it.

### How to cite

Use Markdown footnotes with short descriptive names. GitHub renders them as
links.

```markdown
Local clones hard-link object files[^git-clone].

[^git-clone]: git-clone(1), `--local`. <https://git-scm.com/docs/git-clone>
    (accessed 2026-10-17). "The files under `.git/objects/` directory are
    hardlinked to save space when possible."
```

- **Prefer primary sources:** official docs, specs, man pages, release notes,
  source code. If a blog post, forum thread, or issue comment is all you
  have, mark it `(secondary)` and don't let it carry the decision alone.
- **Quote the sentence you rely on** whenever the claim is about behavior.
  Docs change; the quote keeps the ADR checkable later.
- **Record when you looked:** an access date, or a version or commit for
  sources that have one. For local man pages, give the OS version.

## Measurements

When a decision depends on performance, size, or correctness under some
condition, measure it rather than guess.

Cite a measurement with a footnote, just like a source. The footnote
describes the method well enough for someone to rerun it:

```markdown
Cloning from the cache took 0.28 s versus 4.40 s from upstream[^bench-clone].

[^bench-clone]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1.
    3 runs each of `git clone <upstream>` and `git clone <cache>` on
    rust-lang/cargo, median reported. Upstream time depends on the network.
```

- **Say what was run**, in commands or a precise sentence: the machine, OS,
  and tool versions, and the date.
- **Report the number of runs and the median or range**, never a single run.
- **Compare options measured in the same session**, and prefer ratios to
  absolute times.
- **Note anything that could skew the result**, such as network variance or
  timer overhead.
- **Small experiments count.** A failure reproduced in a few commands beats a
  paragraph arguing that it might happen.
- **Don't commit scripts or raw data for an ADR.** If the result needs
  re-checking, the footnote should be enough to redo it. Behavior that must
  keep holding belongs in a test, named in Consequences.

## Self-check before accepting

- [ ] The title names a single decision.
- [ ] Every claim about external behavior has a footnote, and the claims that
      matter quote their source.
- [ ] Every number has a footnote: a source, or how it was measured.
- [ ] Each rejected alternative has a concrete reason.
- [ ] Consequences include the downsides and how conformance is checked.
- [ ] No open questions are left (or they're explicitly deferred to a future
      ADR).

## Index

| ADR | Title | Status |
| --- | ----- | ------ |
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions with cited evidence | Accepted |
| [0002](0002-implementation-language.md) | Write the tools in Go | Accepted |
| [0003](0003-use-git-cli.md) | Shell out to the git binary | Accepted |
| [0004](0004-single-binary-with-subcommands.md) | One `ws` binary with subcommands | Superseded by [0014](0014-command-set.md) |
| [0005](0005-cli-framework.md) | Use cobra for command parsing | Accepted |
| [0006](0006-session-based-cleanup.md) | Remove workspaces a grace period after their last session ends | Superseded by [0013](0013-cleanup-ignores-git-state.md) |
| [0007](0007-repo-spec-syntax-and-resolution.md) | `repo@ref` spec syntax and cache-first resolution | Accepted |
| [0008](0008-repository-cache.md) | Bare, locked, heads-and-tags repository cache | Accepted; default-branch step amended by [0021](0021-cache-default-branch.md) |
| [0009](0009-workspace-repository-remotes.md) | Hard-linked workspace clones with upstream as `origin` | Accepted |
| [0010](0010-unsaved-work-detection.md) | Detect unsaved git work with three porcelain checks against `origin` | Rejected |
| [0011](0011-config-dir.md) | Keep config, cache, and workspaces under one overridable config dir | Accepted |
| [0012](0012-workspace-layout.md) | Workspace names, state kept outside the workspace, and deletion guard | Accepted; state files amended by [0015](0015-workspace-lifetime-follows-session.md), creation order by [0022](0022-workspace-record-before-rename.md); removal-pass timestamp in `state/` amended by [0026](0026-removal-pass-timing.md); `state/.respawn.lock` and session record version added by [0027](0027-respawn-lock-and-record-versions.md); `review.toml` in state and `reviews/` in `CONFIG_DIR` added by [0039](0039-ws-review-command.md); `hidden` and `workspace.toml` version 2 added by [0043](0043-hidden-workspaces.md); `parent` and version 3 added by [0044](0044-workspace-trees.md) |
| [0013](0013-cleanup-ignores-git-state.md) | Remove workspaces a grace period after their last session ends, whatever their git state | Superseded by [0015](0015-workspace-lifetime-follows-session.md) |
| [0014](0014-command-set.md) | `ws` command set: one command per use case, and an agent guide | Accepted; `ws restart` added by [0018](0018-restart-claude-daemon.md); `ws claude` attach conditions and session name amended by [0025](0025-ws-claude-session-hand-off.md); `ws cache refs` added by [0033](0033-ws-cache-refs.md) (proposed); `ws review` added by [0039](0039-ws-review-command.md); `ws hide`, `ws unhide`, `ws claude --hidden`, and `ws list -a` added by [0043](0043-hidden-workspaces.md); `ws claude --parent`, `ws rm --keep-children`, and `ws list --parent` added by [0044](0044-workspace-trees.md) |
| [0015](0015-workspace-lifetime-follows-session.md) | A workspace lives exactly as long as its one session | Accepted; session reading amended by [0024](0024-claude-code-client.md); agent lock and orphaned-session removal amended by [0025](0025-ws-claude-session-hand-off.md); removal timing and `ws rm` amended by [0026](0026-removal-pass-timing.md); session directory and repo trust amended by [0028](0028-single-repo-session-directory.md) (proposed); removal with a parent added by [0044](0044-workspace-trees.md); removal of finished reviews added by [0045](0045-finished-reviews-are-removed.md) |
| [0016](0016-output-contract.md) | Output contract: one JSON object on stdout, errors as JSON on stderr, five exit codes | Accepted; shell exit status amended by [0023](0023-shell-session-keeps-creation-lock.md); `ws claude` attached exit status amended by [0025](0025-ws-claude-session-hand-off.md); `workspace_busy` and `session_mismatch` added by [0026](0026-removal-pass-timing.md); review error codes added by [0039](0039-ws-review-command.md); profile error codes added by [0042](0042-review-profile-management.md); `child_kept` added by [0044](0044-workspace-trees.md) |
| [0017](0017-clones-not-worktrees.md) | Workspace repos are clones of the cache, not git worktrees | Accepted |
| [0018](0018-restart-claude-daemon.md) | `ws restart` swaps Claude Code's background service and respawns each session once it's idle | Accepted; "working" amended by [0024](0024-claude-code-client.md); stale detection amended by [0026](0026-removal-pass-timing.md); respawn lock added by [0027](0027-respawn-lock-and-record-versions.md) |
| [0019](0019-https-upstream-urls.md) | Build HTTPS upstream URLs; SSH through git's `insteadOf` | Accepted |
| [0020](0020-repo-spec-edge-cases.md) | Repo specs: no local paths, ports stay in ids, passwords are rejected | Accepted |
| [0021](0021-cache-default-branch.md) | The cache follows upstream's default branch through `ls-remote --symref` | Accepted |
| [0022](0022-workspace-record-before-rename.md) | Build workspaces in state, write the record, then move them into place | Accepted |
| [0023](0023-shell-session-keeps-creation-lock.md) | `ws shell` keeps creation's lock for the shell, and waits out the terminal's signals | Accepted |
| [0024](0024-claude-code-client.md) | Read Claude Code sessions by `kind`, `pid`, `status`, and exit status, through one client that requires Claude Code 2.1.302 | Accepted; `claude attach` and `--bg` process group amended by [0025](0025-ws-claude-session-hand-off.md) |
| [0025](0025-ws-claude-session-hand-off.md) | `ws claude` keeps creation's lock until the session is recorded, lets `claude --bg` finish, and becomes `claude attach` for an idle session on a terminal | Accepted; orphaned sessions in a one-repo workspace's repo amended by [0028](0028-single-repo-session-directory.md) (proposed) |
| [0026](0026-removal-pass-timing.md) | The removal pass runs before every command that reads `CONFIG_DIR`, at most once a minute, except in `ws list`, which runs it every time | Accepted |
| [0027](0027-respawn-lock-and-record-versions.md) | `ws restore` and `ws restart` share one respawn lock, and a record from a newer `ws` is left alone | Accepted; the no-agent exception applies only to `ws restore` |
| [0028](0028-single-repo-session-directory.md) | A one-repo workspace's session starts in its repo, which `ws claude` trusts in Claude Code's config for the workspace's lifetime | Proposed |
| [0029](0029-desktop-app.md) | A Tauri desktop app drives agents through `ws --json` and `claude attach` in a pseudo-terminal | Proposed |
| [0030](0030-desktop-conversation-from-transcript.md) | The desktop app shows an agent's conversation by following its transcript file | Proposed |
| [0031](0031-desktop-sections-rail.md) | The desktop app is split into sections chosen from a left rail: Agents, Repos, Reviews, and Settings | Proposed; the Agents list's Hidden group added by [0043](0043-hidden-workspaces.md) |
| [0032](0032-review-agent-per-pr-head.md) | A pull request review is a `ws claude` agent on the PR's head commit, named after the PR and that commit | Proposed; task and matching amended by [0038](0038-review-fans-out-to-profile-subagents.md) and [0039](0039-ws-review-command.md) |
| [0033](0033-ws-cache-refs.md) | `ws cache refs` lists a cached repo's branches and tags with their last commits, and `ws cache list` counts them | Proposed |
| [0034](0034-desktop-state-file.md) | The desktop app keeps watched repos, dismissals, auto-review records, and settings in one `desktop.json` in its own config directory | Proposed |
| [0035](0035-desktop-review-checker.md) | The desktop app checks watched repos for pull requests in its Rust backend, with `gh pr list --repo <cache id>`, four at a time, and starts auto-reviews there | Proposed |
| [0036](0036-workspace-id-suffix-and-rename.md) | An id from `--name` ends with 8 random hex digits, and `ws rename` changes the display name without moving the workspace | Proposed |
| [0037](0037-review-engine-forked-from-loam-refinery.md) | Reviews are a `ws` feature built from a fork of loam-refinery's code and specialized for workspaces; `ws` doesn't run or depend on loam-refinery | Accepted |
| [0038](0038-review-fans-out-to-profile-subagents.md) | A review stays one agent per PR head; `ws` chooses its profiles and defines one Claude Code subagent per profile, with that profile's model | Accepted; profiles managed by [0042](0042-review-profile-management.md) |
| [0039](0039-ws-review-command.md) | `ws review` starts a review with a record of what it reviews and who asked, keeps submissions in `CONFIG_DIR/reviews/`, and `wait` hands the requester the combined report | Accepted; review workspaces hidden by [0043](0043-hidden-workspaces.md); the requester is the review's parent per [0044](0044-workspace-trees.md); `runs/` in the store and `ws review list` added by [0045](0045-finished-reviews-are-removed.md) |
| [0040](0040-review-decisions-kept-with-findings.md) | A decision per finding is kept with the review in `ws`'s store, set with `ws review decide`; the desktop app renders findings itself from `ws review report --json` | Accepted |
| [0041](0041-desktop-acts-on-findings.md) | Selected findings are posted as one GitHub review at the reviewed commit after a preview, and sent to fix to the requesting agent, or else to a new agent on the PR's branch | Accepted |
| [0042](0042-review-profile-management.md) | Review profiles are added, edited, and deleted with `ws review profiles`, as files in `CONFIG_DIR/review/profiles/`; a built-in is edited by overriding it and restored by deleting the override | Accepted |
| [0043](0043-hidden-workspaces.md) | A workspace can be hidden: `hidden = true` in its record, set by `ws review start`, `ws claude --hidden`, and `ws hide`; `ws list` and the desktop app show hidden workspaces only when asked | Accepted |
| [0044](0044-workspace-trees.md) | A workspace started from inside another is its child; removing a workspace removes its descendants first | Accepted |
| [0045](0045-finished-reviews-are-removed.md) | A finished review's workspace and agent are removed; its record is kept in the review store, where `ws review` and the desktop app find it | Accepted |
| [0046](0046-ws-machine.md) | Agents manage their child workspaces with `ws machine`; the top-level commands are for people and the desktop app | Proposed |

[^aws-process]: AWS Prescriptive Guidance, "ADR process." <https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/adr-process.html> (accessed 2026-10-17). "When the team accepts an ADR, it becomes immutable. If new insights require a different decision, the team proposes a new ADR." On rejection: "the ADR owner adds a reason for the rejection to prevent future discussions on the same topic."
[^aws-blog]: C. Kappey, D. Goby, D. Kunce, "Master architecture decision records (ADRs): Best practices for effective decision-making," AWS Architecture Blog, 2025-03-20. <https://aws.amazon.com/blogs/architecture/master-architecture-decision-records-adrs-best-practices-for-effective-decision-making/> (accessed 2026-10-17). "Keep ADRs concise by focusing on a single decision."
