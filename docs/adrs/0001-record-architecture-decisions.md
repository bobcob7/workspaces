# 0001. Record architecture decisions with cited evidence

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

These tools share on-disk formats (the cache, workspace metadata, config) and
a repo spec syntax that agents script against. Changing any of them breaks
callers. Several choices, such as the clone strategy, locking, and default
paths, have trade-offs that can be measured. Argued from memory, it's easy to
get them wrong. While drafting the first ADRs, measurement caught two bugs
that reasoning had missed: a git command sequence that failed on the default
branch, and a cleanup check that would have approved deleting unpushed work.

This is a solo project, so the process has to be light.

## Decision

We will record significant decisions as ADRs in `docs/adrs/`, following
[`README.md`](README.md) and [`template.md`](template.md):

- five sections: Context, Decision, Alternatives, Consequences, References
- AWS's lifecycle rules: one decision per ADR, immutable once accepted,
  superseded rather than edited, rejected ADRs kept with their reason[^aws]
- claims about external behavior are cited with footnotes; numbers link to
  committed measurement scripts and data

## Alternatives

**AWS's full process**[^aws] (owners, a readout meeting, versioning, a
Compliance section). It is designed for teams of 10–100[^aws-blog]. The
roles and meetings have no purpose with a single maintainer.

**Plain Nygard ADRs**[^nygard]. These are close to what we chose, but they
have no rule about evidence, and that rule is what caught the bugs above.

**No record.** It would leave the reasons for the formats and syntax that
agents depend on undocumented.

## Consequences

- Writing an ADR takes longer when measurements are needed. That is
  acceptable, because ADRs are reserved for decisions that are expensive to
  reverse.
- Decisions can be re-checked by rerunning the committed scripts, for example
  after a git upgrade.
- Conformance: PRs that change behavior, formats, or dependencies without an
  accepted ADR are sent back.

## References

[^aws]: AWS Prescriptive Guidance, "ADR process." <https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/adr-process.html> (accessed 2026-10-17).
[^aws-blog]: C. Kappey, D. Goby, D. Kunce, "Master architecture decision records (ADRs)," AWS Architecture Blog, 2025-03-20. <https://aws.amazon.com/blogs/architecture/master-architecture-decision-records-adrs-best-practices-for-effective-decision-making/> (accessed 2026-10-17). Based on "over 200 ADRs" in teams from under 10 people to 100 people across 10 workstreams.
[^nygard]: M. Nygard, "Documenting Architecture Decisions," 2011-11-15. <https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions>
