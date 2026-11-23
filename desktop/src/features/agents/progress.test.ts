import { describe, expect, it } from "vitest";
import type { Entry, EntryProgress } from "../../api";
import { entry } from "../../test/fixtures";
import { isFailed, latestNote } from "./progress";

const record = (overrides: Partial<EntryProgress> = {}): EntryProgress => ({
  name: "build",
  total: 2,
  done: 1,
  note: null,
  updatedAt: "2026-11-22T20:00:00Z",
  ...overrides,
});

const root = entry({ key: "root", workspaceId: "root", name: "fix-login" });
const childOf = (key: string, overrides: Partial<Entry> = {}): Entry =>
  entry({ key, workspaceId: key, name: key, parentId: "root", ...overrides });

describe("isFailed", () => {
  it("is true for a FAIL or CHANGES_REQUESTED verdict", () => {
    expect(isFailed(entry({ result: "FAIL" }))).toBe(true);
    expect(isFailed(entry({ result: " changes_requested " }))).toBe(true);
  });

  it("is false for a passing verdict, a sentence, or no result", () => {
    expect(isFailed(entry({ result: "PASS" }))).toBe(false);
    expect(isFailed(entry({ result: "FAIL: the limiter drops requests" }))).toBe(false);
    expect(isFailed(entry({ result: null }))).toBe(false);
  });
});

describe("latestNote", () => {
  it("is the note of the most recently updated step record below the entry", () => {
    const entries = [
      root,
      childOf("older", { progress: record({ note: "lint", updatedAt: "2026-11-22T19:00:00Z" }) }),
      childOf("newer", { progress: record({ note: "tests", updatedAt: "2026-11-22T21:00:00Z" }) }),
    ];
    expect(latestNote(entries, root)).toBe("tests");
  });

  it("is null when the most recently updated record has no note, even if an older one does", () => {
    const entries = [
      root,
      childOf("older", { progress: record({ note: "lint", updatedAt: "2026-11-22T19:00:00Z" }) }),
      childOf("newer", { progress: record({ note: null, updatedAt: "2026-11-22T21:00:00Z" }) }),
    ];
    expect(latestNote(entries, root)).toBeNull();
  });

  it("ignores entries outside the tree and children without a record", () => {
    const entries = [root, childOf("bare"), entry({ key: "other", workspaceId: "other", name: "other", parentId: "elsewhere", progress: record({ note: "stray" }) })];
    expect(latestNote(entries, root)).toBeNull();
  });
});
