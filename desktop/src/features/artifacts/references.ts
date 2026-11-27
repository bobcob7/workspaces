/** `@artifact:<id>` references in prompts, the same as `internal/artifact` finds and hints them (ADR 0078). */

/** A reference in text, as `internal/artifact` matches it: the id, and the revision digits if any. */
const refPattern = /@artifact:(art-[0-9a-f]{8})(?:\/r([1-9]\d{0,2}))?/g;

const hintFormat = (ids: string): string =>
  `(Artifacts ${ids}: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)`;

/** The text a hint already carries, so a second hint isn't added. */
const hintMarker = 'run "ws machine artifact get <id>"';

const hintSeparator = "\n\n";

/** The distinct artifact ids a text references, in first-seen order. */
export const findRefs = (text: string): string[] => {
  const ids = new Set<string>();
  for (const match of text.matchAll(refPattern)) {
    const id = match[1];
    if (id) {
      ids.add(id);
    }
  }
  return [...ids];
};

/** A line telling an agent how to fetch the artifacts `ids`. */
export const hintLine = (ids: readonly string[]): string => hintFormat(ids.join(", "));

/** The text with a hint line appended when it references artifacts and has none yet. */
export const appendHint = (text: string): string => {
  const ids = findRefs(text);
  if (ids.length === 0 || text.includes(hintMarker)) {
    return text;
  }
  return text + hintSeparator + hintLine(ids);
};

/** One reference in a text: its artifact, the revision it names (null: none), and where it is. */
export interface FoundRef {
  readonly id: string;
  readonly rev: number | null;
  readonly start: number;
  readonly end: number;
}

const blankRun = (text: string): string => text.replace(/[^\n]/g, " ");

/** The fence character and length a line opens with, or null (up to three spaces of indent). */
const fenceOf = (line: string): { readonly char: string; readonly length: number } | null => {
  const match = /^ {0,3}(`{3,}|~{3,})/.exec(line);
  const run = match?.[1];
  return run === undefined ? null : { char: run.charAt(0), length: run.length };
};

/** Blanks every line of a fenced code block, fences included, as the desktop's transcript scan does (ADR 0084). */
const blankFences = (text: string): string => {
  let open: { readonly char: string; readonly length: number } | null = null;
  return text
    .split(/(?<=\n)/)
    .map((line) => {
      const fence = fenceOf(line);
      if (open === null) {
        open = fence;
        return fence === null ? line : blankRun(line);
      }
      const closes = fence !== null && fence.char === open.char && fence.length >= open.length && line.trim().replace(/^[`~]+/, "") === "";
      if (closes) {
        open = null;
      }
      return blankRun(line);
    })
    .join("");
};

/** Blanks inline code spans: a run of N backticks up to the next run of exactly N. */
const blankSpans = (text: string): string => {
  let out = "";
  let i = 0;
  while (i < text.length) {
    if (text[i] !== "`") {
      out += text[i];
      i += 1;
      continue;
    }
    const run = /^`+/.exec(text.slice(i))?.[0] ?? "`";
    const close = new RegExp(`(?<!\`)${run}(?!\`)`, "g");
    close.lastIndex = i + run.length;
    const found = close.exec(text);
    if (found === null) {
      out += run;
      i += run.length;
      continue;
    }
    const end = found.index + run.length;
    out += blankRun(text.slice(i, end));
    i = end;
  }
  return out;
};

/** The text with code (fences and inline spans) replaced by spaces, so references in it aren't found. */
export const blankCode = (text: string): string => blankSpans(blankFences(text));

/** Every reference in text outside code, in order. */
export const refsIn = (text: string): FoundRef[] => {
  const plain = blankCode(text);
  const found: FoundRef[] = [];
  for (const match of plain.matchAll(refPattern)) {
    const id = match[1];
    if (id !== undefined) {
      found.push({ id, rev: match[2] === undefined ? null : Number(match[2]), start: match.index, end: match.index + match[0].length });
    }
  }
  return found;
};

/** The distinct references a message cites outside code, as `@artifact:<id>[/r<n>]`, for `artifact_cite`. */
export const citationsIn = (text: string): string[] => [
  ...new Set(refsIn(text).map((r) => `@artifact:${r.id}${r.rev === null ? "" : `/r${r.rev}`}`)),
];

const hintPattern = /\n\n\(Artifacts ((?:art-[0-9a-f]{8}(?:, )?)+): run "ws machine artifact get <id>" for each, then read the file at the "path" it prints\.\)\s*$/;

/** A message split from the hint line `ws` appended to it: the text before, and the hint's ids, or null without one. */
export const splitHint = (text: string): { readonly body: string; readonly ids: readonly string[] | null } => {
  const match = hintPattern.exec(text);
  if (match?.[1] === undefined) {
    return { body: text, ids: null };
  }
  return { body: text.slice(0, match.index), ids: match[1].split(", ") };
};

/** The in-app link for a reference (ADR 0084). Only the conversation's own plugin makes these. */
export const artifactHref = (id: string, rev: number | null): string =>
  rev === null ? `ws-artifact:${id}` : `ws-artifact:${id}?rev=${rev}`;

/** The artifact and revision an in-app link names, parsed strictly; null for anything else. */
export const parseArtifactHref = (href: string): { readonly id: string; readonly rev: number | null } | null => {
  const match = /^ws-artifact:(art-[0-9a-f]{8})(?:\?rev=([1-9]\d{0,2}))?$/.exec(href);
  if (match?.[1] === undefined) {
    return null;
  }
  return { id: match[1], rev: match[2] === undefined ? null : Number(match[2]) };
};
