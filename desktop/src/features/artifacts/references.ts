/** `@artifact:<id>` references in prompts, the same as `internal/artifact` finds and hints them (ADR 0078). */

const inTextPattern = /@artifact:(art-[0-9a-f]{8})(?:\/r[1-9]\d{0,2})?/g;

const hintFormat = (ids: string): string =>
  `(Artifacts ${ids}: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)`;

/** The text a hint already carries, so a second hint isn't added. */
const hintMarker = 'run "ws machine artifact get <id>"';

const hintSeparator = "\n\n";

/** The distinct artifact ids a text references, in first-seen order. */
export const findRefs = (text: string): string[] => {
  const ids = new Set<string>();
  for (const match of text.matchAll(inTextPattern)) {
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
