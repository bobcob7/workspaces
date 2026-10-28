import type { DecisionKind, ReportAnchor, ReportComment, ReportProfile, ReviewReport, Verdict } from "../../api";

/** The review format's priority ladder (report.md, Findings). */
export type BandId = "must" | "should" | "worth" | "optional";

export interface Band {
  readonly id: BandId;
  readonly title: string;
  /** The lowest priority in the band. */
  readonly min: number;
}

export const bands: readonly Band[] = [
  { id: "must", title: "Must fix", min: 9 },
  { id: "should", title: "Should fix", min: 7 },
  { id: "worth", title: "Worth fixing", min: 4 },
  { id: "optional", title: "Optional", min: 1 },
];

export const bandOf = (priority: number): BandId => bands.find((b) => priority >= b.min)?.id ?? "optional";

/** A finding as the list shows it; superseded ones are read-only. */
export interface Finding {
  readonly comment: ReportComment;
  readonly readOnly: boolean;
}

/** Profile and category filters, kept per pull request for the session. */
export interface ReportFilter {
  readonly profile: string | null;
  readonly category: string | null;
  readonly hideDecided: boolean;
  readonly showSuperseded: boolean;
}

export const noFilter: ReportFilter = { profile: null, category: null, hideDecided: false, showSuperseded: false };

/** Whether the list shows fewer findings than the review has. */
export const filterActive = (filter: ReportFilter): boolean =>
  filter.profile !== null || filter.category !== null || filter.hideDecided;

export const matchesFilter = (comment: ReportComment, filter: ReportFilter): boolean =>
  (filter.profile === null || comment.profile === filter.profile) &&
  (filter.category === null || comment.category === filter.category) &&
  !(filter.hideDecided && comment.decision !== null);

export interface BandGroup {
  readonly band: Band;
  /** The band's current findings, before filtering. */
  readonly total: number;
  /** What the list shows: filtered current findings, then superseded ones when shown. */
  readonly findings: readonly Finding[];
}

const byPriorityThenProfile = (a: Finding, b: Finding): number =>
  b.comment.priority - a.comment.priority ||
  a.comment.profile.localeCompare(b.comment.profile) ||
  Number(a.readOnly) - Number(b.readOnly) ||
  a.comment.id.localeCompare(b.comment.id);

/** Findings by band, highest priority first within each, then by profile. */
export const groupFindings = (report: ReviewReport, filter: ReportFilter): readonly BandGroup[] => {
  const current: Finding[] = report.comments.map((comment) => ({ comment, readOnly: false }));
  const superseded: Finding[] = filter.showSuperseded
    ? report.superseded.map((comment) => ({ comment, readOnly: true }))
    : [];
  return bands
    .map((band) => {
      const inBand = (f: Finding): boolean => bandOf(f.comment.priority) === band.id;
      return {
        band,
        total: current.filter(inBand).length,
        findings: [...current, ...superseded]
          .filter((f) => inBand(f) && matchesFilter(f.comment, filter))
          .toSorted(byPriorityThenProfile),
      };
    })
    .filter((g) => g.total > 0 || g.findings.length > 0);
};

/** `2 of 4` with a filter on, so a filtered list doesn't read as a short review; else the count. */
export const bandCount = (group: BandGroup, filter: ReportFilter): string => {
  const shown = group.findings.filter((f) => !f.readOnly).length;
  return filterActive(filter) ? `${shown} of ${group.total}` : String(group.total);
};

/** The findings in list order, skipping a collapsed Optional band, for ↑ and ↓. */
export const visibleFindings = (groups: readonly BandGroup[], optionalOpen: boolean): readonly Finding[] =>
  groups.filter((g) => g.band.id !== "optional" || optionalOpen).flatMap((g) => g.findings);

/** The body's first sentence, without Markdown marks or a closing period. */
export const findingTitle = (body: string): string => {
  const paragraph = body.split(/\n\s*\n/).find((p) => p.trim() !== "") ?? "";
  const plain = paragraph.replace(/\s+/g, " ").replace(/[`*_]/g, "").replace(/^#+\s*/, "").trim();
  const sentence = /^.*?[.!?](?=\s|$)/.exec(plain)?.[0] ?? plain;
  return sentence.replace(/\.$/, "");
};

/** `ratelimit/ip.go:31-34`, or the file alone for a whole-file anchor. */
export const anchorLabel = (anchor: ReportAnchor): string => {
  if (anchor.line <= 0) {
    return anchor.file;
  }
  const end = anchor.end_line !== null && anchor.end_line > anchor.line ? `-${anchor.end_line}` : "";
  return `${anchor.file}:${anchor.line}${end}`;
};

/** The anchor's file and lines at the reviewed commit on GitHub. */
export const anchorUrl = (repo: string, ref: string, anchor: ReportAnchor): string => {
  const path = anchor.file.split("/").map(encodeURIComponent).join("/");
  const base = `https://${repo}/blob/${ref}/${path}`;
  if (anchor.line <= 0) {
    return base;
  }
  const end = anchor.end_line !== null && anchor.end_line > anchor.line ? `-L${anchor.end_line}` : "";
  return `${base}#L${anchor.line}${end}`;
};

export const verdictLabels: Readonly<Record<Verdict, string>> = {
  request_changes: "request changes",
  comment: "comment",
  approve: "approve",
};

/** `1 must · 1 should`, or `no findings`. */
export const severityText = (profile: ReportProfile): string => {
  const s = profile.severity;
  if (s === null) {
    return "";
  }
  const parts = [
    [s.must_fix, "must"],
    [s.should_fix, "should"],
    [s.worth_fixing, "worth"],
    [s.optional, "optional"],
  ] as const;
  const text = parts.filter(([n]) => n > 0).map(([n, word]) => `${n} ${word}`);
  return text.length === 0 ? "no findings" : text.join(" · ");
};

/** The finding's decision, unless it has been acted on. */
export const pendingDecision = (comment: ReportComment): DecisionKind | null =>
  comment.decision !== null && comment.decision.done === null ? comment.decision.decision : null;

/** What clicking a segment of the Comment · Fix · Skip control does: the selected one clears. */
export const clickedDecision = (current: DecisionKind | null, clicked: DecisionKind): DecisionKind | "clear" =>
  current === clicked ? "clear" : clicked;

export interface Tally {
  readonly comment: number;
  readonly fix: number;
  readonly skip: number;
  readonly undecided: number;
  readonly done: number;
  readonly readOnly: number;
  readonly optionalHidden: number;
}

/** The action bar's counts over the review's current findings. */
export const tally = (report: ReviewReport, groups: readonly BandGroup[], optionalOpen: boolean): Tally => {
  const count = (kind: DecisionKind): number => report.comments.filter((c) => pendingDecision(c) === kind).length;
  const optional = groups.find((g) => g.band.id === "optional");
  return {
    comment: count("comment"),
    fix: count("fix"),
    skip: count("skip"),
    undecided: report.comments.filter((c) => c.decision === null).length,
    done: report.comments.filter((c) => c.decision?.done != null).length,
    readOnly: groups.flatMap((g) => g.findings).filter((f) => f.readOnly).length,
    optionalHidden: optionalOpen || optional === undefined ? 0 : optional.findings.length,
  };
};

export interface TallyPart {
  readonly key: keyof Tally;
  readonly count: number;
  readonly label: string;
}

const tallyLabels: readonly (readonly [keyof Tally, string])[] = [
  ["comment", "to comment"],
  ["fix", "to fix"],
  ["skip", "skipped"],
  ["undecided", "undecided"],
  ["done", "done"],
  ["readOnly", "read-only (superseded)"],
  ["optionalHidden", "optional hidden"],
];

/** The tally's non-zero parts, in the action bar's order. */
export const tallyParts = (t: Tally): readonly TallyPart[] =>
  tallyLabels.filter(([key]) => t[key] > 0).map(([key, label]) => ({ key, count: t[key], label }));

/** How many of the review's profiles have submitted. */
export const profilesIn = (report: ReviewReport): { readonly submitted: number; readonly total: number } => ({
  submitted: report.profiles.filter((p) => !p.missing).length,
  total: report.profiles.length,
});

const bandWords: Readonly<Record<BandId, string>> = {
  must: "must fix",
  should: "should fix",
  worth: "worth fixing",
  optional: "optional",
};

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

/**
 * A reviewed pull request's list row summary: the worst band and the
 * findings (`2 must fix · 7 findings`), then the decisions left once you've
 * decided (`2 to fix`), then `all done`. While the agent works, the profiles
 * that are in.
 */
export const reportSummary = (report: ReviewReport, working: boolean): string => {
  if (working) {
    const { submitted, total } = profilesIn(report);
    return `${submitted} of ${total} profiles in`;
  }
  const comments = report.comments;
  if (comments.length === 0) {
    return "no findings";
  }
  if (comments.every((c) => c.decision === null)) {
    const worst = bands.find((b) => comments.some((c) => bandOf(c.priority) === b.id)) ?? bands[3];
    const inWorst = comments.filter((c) => bandOf(c.priority) === worst?.id).length;
    const head = `${inWorst} ${bandWords[worst?.id ?? "optional"]}`;
    return inWorst === comments.length ? head : `${head} · ${plural(comments.length, "finding")}`;
  }
  const left = [
    [comments.filter((c) => pendingDecision(c) === "comment").length, "to comment"],
    [comments.filter((c) => pendingDecision(c) === "fix").length, "to fix"],
    [comments.filter((c) => c.decision === null).length, "undecided"],
  ] as const;
  const parts = left.filter(([n]) => n > 0).map(([n, word]) => `${n} ${word}`);
  return parts.length === 0 ? "all done" : parts.join(" · ");
};

/**
 * Qualified ids decided in older reviews of the same pull request, with the
 * commit they were decided at; the newest older review wins. Decisions
 * aren't carried over (ADR 0040), only pointed back to.
 */
export const decidedEarlier = (older: readonly ReviewReport[]): ReadonlyMap<string, string> => {
  const found = new Map<string, string>();
  for (const report of older) {
    for (const comment of report.comments) {
      if (comment.decision !== null && !found.has(comment.id)) {
        found.set(comment.id, report.ref.slice(0, 7));
      }
    }
  }
  return found;
};

/** What a key does in the findings list (report.md, Keyboard). */
export type KeyAction =
  | { readonly kind: "move"; readonly by: 1 | -1 }
  | { readonly kind: "toggle" }
  | { readonly kind: "decide"; readonly decision: DecisionKind }
  | { readonly kind: "suggest"; readonly index: number }
  | { readonly kind: "clear" };

const decisionKeys: Readonly<Record<string, DecisionKind>> = { c: "comment", f: "fix", s: "skip" };

export const keyAction = (key: string): KeyAction | null => {
  switch (key) {
    case "ArrowDown":
      return { kind: "move", by: 1 };
    case "ArrowUp":
      return { kind: "move", by: -1 };
    case "Enter":
    case " ":
      return { kind: "toggle" };
    case "Backspace":
      return { kind: "clear" };
  }
  const decision = decisionKeys[key.toLowerCase()];
  if (decision !== undefined) {
    return { kind: "decide", decision };
  }
  if (/^[1-9]$/.test(key)) {
    return { kind: "suggest", index: Number(key) - 1 };
  }
  return null;
};
