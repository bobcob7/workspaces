import { describe, expect, it } from "vitest";
import { entry, treeEntries } from "../../test/fixtures";
import {
  attentionEntries,
  childrenOf,
  descendantsOf,
  groupOf,
  needsAttention,
  statusFilterOptions,
} from "./status";

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

describe("needsAttention", () => {
  it("is false for shells", () => {
    expect(needsAttention(entry({ kind: "shell", status: "idle" }))).toBe(false);
  });

  it("is false for hidden entries", () => {
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

describe("attentionEntries", () => {
  it("lists a needs-input child and leaves out a hidden one", () => {
    expect(attentionEntries(treeEntries()).map((e) => e.key)).toEqual(["t1r"]);
  });

  it("lists the most recent first", () => {
    const entries = [
      ...treeEntries(),
      entry({ key: "w", status: "idle", updatedAt: "2026-11-11T10:00:00Z" }),
    ];
    expect(attentionEntries(entries).map((e) => e.key)).toEqual(["w", "t1r"]);
  });
});

describe("statusFilterOptions", () => {
  it("lists every status filter in display order", () => {
    expect(statusFilterOptions.map((o) => o.value)).toEqual([
      "all",
      "attention",
      "working",
      "done",
      "stopped",
      "shells",
      "other",
    ]);
  });

  it("labels the first option All statuses", () => {
    expect(statusFilterOptions[0]?.label).toBe("All statuses");
  });
});

