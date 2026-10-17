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

**Context.** The problem and the forces at play: requirements, constraints,
and the facts that make the choice non-obvious. This is where most of the
evidence goes. Write it neutrally, as if you hadn't decided yet. A few
paragraphs, or a table of measurements if the decision turns on numbers.

**Decision.** Start with "We will…". Be specific enough that someone could
implement it without guessing: commands, formats, names, defaults. Leave out
justification; that belongs in Context and Alternatives.

**Alternatives.** One short paragraph per serious option that wasn't chosen,
each ending with the concrete reason it lost, backed by a citation or a
measurement where one exists. "Worse" isn't a reason; "corrupts the
workspace after `git gc`", with a footnote saying how that was reproduced, is. Two or three options is
typical.

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
| [0004](0004-single-binary-with-subcommands.md) | One `ws` binary with subcommands | Accepted |
| [0005](0005-cli-framework.md) | Use cobra for command parsing | Accepted |

[^aws-process]: AWS Prescriptive Guidance, "ADR process." <https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/adr-process.html> (accessed 2026-10-17). "When the team accepts an ADR, it becomes immutable. If new insights require a different decision, the team proposes a new ADR." On rejection: "the ADR owner adds a reason for the rejection to prevent future discussions on the same topic."
[^aws-blog]: C. Kappey, D. Goby, D. Kunce, "Master architecture decision records (ADRs): Best practices for effective decision-making," AWS Architecture Blog, 2025-03-20. <https://aws.amazon.com/blogs/architecture/master-architecture-decision-records-adrs-best-practices-for-effective-decision-making/> (accessed 2026-10-17). "Keep ADRs concise by focusing on a single decision."
