import type { ProductivityRow, TokenCounts } from "../../api";
import { formatMinutes, formatPercent, formatRate } from "./format";

export type TokenKind = "Input" | "Output" | "Cache write" | "Cache read";

const minimumLinesForInsight = 20;
const otherLanguage = "Other";

/** Productivity rows grouped by language, in the order they arrive. */
export const rowsByLanguage = (rows: readonly ProductivityRow[]): ReadonlyMap<string, readonly ProductivityRow[]> => {
  const groups = new Map<string, ProductivityRow[]>();
  for (const row of rows) {
    const group = groups.get(row.language);
    if (group === undefined) {
      groups.set(row.language, [row]);
    } else {
      group.push(row);
    }
  }
  return groups;
};

/** A cost per line with 4 decimals, "$0.0003"; "—" when there is none. */
export const formatCostPerLine = (costPerLoc: number | null): string =>
  costPerLoc === null ? "—" : `$${costPerLoc.toFixed(4)}`;

const leadClause = (row: ProductivityRow): string => {
  const parts: string[] = [];
  if (row.shareOfLanguage !== null) {
    parts.push(`wrote ${formatPercent(row.shareOfLanguage)} of lines`);
  }
  if (row.locPerMinute !== null) {
    parts.push(`at ${formatRate(row.locPerMinute, 1)} lines/min`);
  }
  if (row.costPerLoc !== null) {
    parts.push(`(${formatCostPerLine(row.costPerLoc)}/line)`);
  }
  return [row.label, ...parts].join(" ");
};

const comparisonClause = (row: ProductivityRow): string | null => {
  if (row.linesAdded < minimumLinesForInsight || row.minutesPer100Loc === null) {
    return null;
  }
  return `${row.label} takes ${formatMinutes(row.minutesPer100Loc)} per 100 lines`;
};

const languageInsight = (language: string, rows: readonly ProductivityRow[]): string | null => {
  const linesAdded = rows.reduce((total, row) => total + row.linesAdded, 0);
  if (language === otherLanguage || linesAdded < minimumLinesForInsight) {
    return null;
  }
  const [leader, ...others] = [...rows].sort((a, b) => (b.shareOfLanguage ?? 0) - (a.shareOfLanguage ?? 0));
  if (leader === undefined) {
    return null;
  }
  const comparisons = others.map(comparisonClause).filter((clause): clause is string => clause !== null);
  const clauses = [leadClause(leader), ...comparisons];
  return `${language}: ${clauses.join("; ")}.`;
};

/** One sentence per language: the leader's share and speed, then how long each other model takes per 100 lines. */
export const languageInsights = (rows: readonly ProductivityRow[]): readonly string[] =>
  [...rowsByLanguage(rows)].flatMap(([language, group]) => {
    const insight = languageInsight(language, group);
    return insight === null ? [] : [insight];
  });

/** The four token kinds with their share of totalTokens, for the bar chart. */
export const tokenKinds = (
  totals: TokenCounts,
): readonly { readonly kind: TokenKind; readonly tokens: number; readonly share: number }[] => {
  const kinds: readonly { readonly kind: TokenKind; readonly tokens: number }[] = [
    { kind: "Input", tokens: totals.inputTokens },
    { kind: "Output", tokens: totals.outputTokens },
    { kind: "Cache write", tokens: totals.cacheWriteTokens },
    { kind: "Cache read", tokens: totals.cacheReadTokens },
  ];
  return kinds.map(({ kind, tokens }) => ({
    kind,
    tokens,
    share: totals.totalTokens === 0 ? 0 : tokens / totals.totalTokens,
  }));
};
