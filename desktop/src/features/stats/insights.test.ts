import { describe, expect, it } from "vitest";
import type { ProductivityRow } from "../../api";
import { statsSnapshot } from "../../test/fixtures";
import { languageInsights, rowsByLanguage, tokenKinds } from "./insights";

const productivityRow = (overrides: Partial<ProductivityRow>): ProductivityRow => ({
  model: "claude-haiku-5-5",
  label: "Haiku 5.5",
  family: "haiku",
  language: "Go",
  linesAdded: 62,
  linesRemoved: 10,
  edits: 5,
  activeMinutes: 6.8,
  tokens: 1_000_000,
  costUsd: 0.2,
  shareOfLanguage: 0.62,
  locPerMinute: 9.1,
  minutesPer100Loc: 11,
  tokensPerLoc: 16_129,
  costPerLoc: 0.00034,
  ...overrides,
});

const sonnetRow = (overrides: Partial<ProductivityRow> = {}): ProductivityRow =>
  productivityRow({
    model: "claude-sonnet-5-5",
    label: "Sonnet 5.5",
    family: "sonnet",
    linesAdded: 38,
    shareOfLanguage: 0.38,
    locPerMinute: 2.5,
    minutesPer100Loc: 15,
    ...overrides,
  });

describe("rowsByLanguage", () => {
  it("keeps the languages in the order they first arrive", () => {
    const rows = [
      productivityRow({ language: "Go" }),
      productivityRow({ language: "TypeScript" }),
      sonnetRow({ language: "Go" }),
    ];
    expect([...rowsByLanguage(rows).keys()]).toEqual(["Go", "TypeScript"]);
  });

  it("keeps each language's rows in the order they arrive", () => {
    const haiku = productivityRow({ language: "Go" });
    const sonnet = sonnetRow({ language: "Go" });
    expect(rowsByLanguage([haiku, sonnet]).get("Go")).toEqual([haiku, sonnet]);
  });
});

describe("languageInsights", () => {
  it("names the leading model's share, speed and cost, then the other model's time per 100 lines", () => {
    expect(languageInsights([productivityRow({}), sonnetRow()])).toEqual([
      "Go: Haiku 5.5 wrote 62% of lines at 9.1 lines/min ($0.0003/line); Sonnet 5.5 takes 15 min per 100 lines.",
    ]);
  });

  it("gives no sentence for a language with fewer than 20 lines", () => {
    expect(languageInsights([productivityRow({ linesAdded: 10, shareOfLanguage: 1 })])).toEqual([]);
  });

  it("gives no sentence for Other", () => {
    expect(languageInsights([productivityRow({ language: "Other" })])).toEqual([]);
  });

  it("leaves out a model's comparison when its minutes per 100 lines is null", () => {
    expect(languageInsights([productivityRow({}), sonnetRow({ minutesPer100Loc: null })])).toEqual([
      "Go: Haiku 5.5 wrote 62% of lines at 9.1 lines/min ($0.0003/line).",
    ]);
  });

  it("leaves out a clause whose ratio is null rather than printing a dash", () => {
    expect(languageInsights([productivityRow({ locPerMinute: null })])).toEqual([
      "Go: Haiku 5.5 wrote 62% of lines ($0.0003/line).",
    ]);
  });
});

describe("tokenKinds", () => {
  it("gives the four kinds with their share of the total tokens", () => {
    const totals = statsSnapshot().usage.totals;
    const kinds = tokenKinds(totals);
    expect(kinds.map((kind) => kind.kind)).toEqual(["Input", "Output", "Cache write", "Cache read"]);
    expect(kinds.reduce((sum, kind) => sum + kind.share, 0)).toBeCloseTo(1);
  });

  it("gives every share as zero when there are no tokens", () => {
    const totals = {
      ...statsSnapshot().usage.totals,
      totalTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      cacheWriteTokens: 0,
      cacheReadTokens: 0,
    };
    expect(tokenKinds(totals).map((kind) => kind.share)).toEqual([0, 0, 0, 0]);
  });
});
