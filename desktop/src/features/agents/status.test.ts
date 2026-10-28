import { describe, expect, it } from "vitest";
import { entry } from "../../test/fixtures";
import { childrenOf, descendantsOf, groupEntries, groupOf, hiddenEntries, needsAttention } from "./status";

describe("groupOf", () => {
  it("puts agents waiting for an answer in the attention group", () => {
    expect(groupOf(entry({ status: "needsInput" }))).toBe("attention");
  });

  it("puts idle agents in the attention group", () => {
    expect(groupOf(entry({ status: "idle" }))).toBe("attention");
  });

  it("puts starting agents with working ones", () => {
    expect(groupOf(entry({ status: "starting" }))).toBe("working");
  });

  it("puts shells in their own group whatever their status", () => {
    expect(groupOf(entry({ kind: "shell", status: "running" }))).toBe("shells");
  });

  it("puts workspaces without a session in the other group", () => {
    expect(groupOf(entry({ kind: "pending", status: "pending" }))).toBe("other");
  });
});

describe("groupEntries", () => {
  it("orders groups attention first and drops empty ones", () => {
    const groups = groupEntries([
      entry({ key: "a", status: "done" }),
      entry({ key: "b", status: "needsInput" }),
    ]);
    expect(groups.map((g) => g.id)).toEqual(["attention", "done"]);
  });

  it("sorts entries by most recent activity", () => {
    const groups = groupEntries([
      entry({ key: "old", name: "old", status: "working", updatedAt: "2026-10-18T08:00:00Z" }),
      entry({ key: "new", name: "new", status: "working", updatedAt: "2026-10-18T09:00:00Z" }),
    ]);
    expect(groups[0]?.entries.map((e) => e.key)).toEqual(["new", "old"]);
  });
});

describe("needsAttention", () => {
  it("is false for shells", () => {
    expect(needsAttention(entry({ kind: "shell", status: "idle" }))).toBe(false);
  });
});

describe("hidden entries", () => {
  it("aren't grouped", () => {
    const groups = groupEntries([entry({ key: "a" }), entry({ key: "b", hidden: true })]);
    expect(groups.flatMap((g) => g.entries.map((e) => e.key))).toEqual(["a"]);
  });

  it("are listed apart, most recent first", () => {
    const hidden = hiddenEntries([
      entry({ key: "a" }),
      entry({ key: "old", hidden: true, updatedAt: "2026-10-28T08:00:00Z" }),
      entry({ key: "new", hidden: true, updatedAt: "2026-10-28T09:00:00Z" }),
    ]);
    expect(hidden.map((e) => e.key)).toEqual(["new", "old"]);
  });

  it("never need attention", () => {
    expect(needsAttention(entry({ status: "needsInput", hidden: true }))).toBe(false);
    expect(needsAttention(entry({ status: "idle", hidden: true }))).toBe(false);
  });
});

describe("trees", () => {
  const root = entry({ key: "root", workspaceId: "root" });
  const a = entry({ key: "a", workspaceId: "a", parentId: "root", updatedAt: "2026-10-28T08:00:00Z" });
  const b = entry({ key: "b", workspaceId: "b", parentId: "root", updatedAt: "2026-10-28T09:00:00Z" });
  const a1 = entry({ key: "a1", workspaceId: "a1", parentId: "a" });
  const other = entry({ key: "other", workspaceId: "other" });
  const all = [root, a, b, a1, other];

  it("children are the direct ones, most recent first", () => {
    expect(childrenOf(all, root).map((e) => e.key)).toEqual(["b", "a"]);
    expect(childrenOf(all, other)).toEqual([]);
  });

  it("descendants come after their parent, with their depth", () => {
    expect(descendantsOf(all, root).map(({ entry: e, depth }) => [e.key, depth])).toEqual([
      ["b", 1],
      ["a", 1],
      ["a1", 2],
    ]);
  });

  it("a session outside any workspace has no children", () => {
    expect(childrenOf(all, entry({ key: "s", workspaceId: null }))).toEqual([]);
  });

  it("a cycle is visited once", () => {
    const x = entry({ key: "x", workspaceId: "x", parentId: "y" });
    const y = entry({ key: "y", workspaceId: "y", parentId: "x" });
    expect(descendantsOf([x, y], x).map(({ entry: e }) => e.key)).toEqual(["y"]);
  });
});

