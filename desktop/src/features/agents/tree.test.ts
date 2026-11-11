import { describe, expect, it } from "vitest";
import { entry, treeEntries } from "../../test/fixtures";
import {
  ancestorKeys,
  autoExpandedKeys,
  buildForest,
  isFiltering,
  isOpen,
  matchesFilter,
  noFilter,
  parentOf,
  type TreeNode,
} from "./tree";

const shape = (nodes: readonly TreeNode[]): unknown[] =>
  nodes.map((n) => (n.children.length > 0 ? [n.entry.key, shape(n.children)] : n.entry.key));

const flatten = (nodes: readonly TreeNode[]): TreeNode[] => nodes.flatMap((n) => [n, ...flatten(n.children)]);

const contextKeys = (nodes: readonly TreeNode[]): string[] =>
  flatten(nodes)
    .filter((n) => !n.match)
    .map((n) => n.entry.key);

const all = treeEntries();

const cycle = [
  entry({ key: "x", workspaceId: "x", parentId: "y", updatedAt: "2026-11-11T09:00:00Z" }),
  entry({ key: "y", workspaceId: "y", parentId: "x", updatedAt: "2026-11-11T08:00:00Z" }),
];

describe("buildForest", () => {
  it("leaves hidden entries out of the tree by default", () => {
    expect(shape(buildForest(all, noFilter, null))).toEqual([
      ["orch", [["t1", ["t1r"]]]],
      "solo",
      ["hid", ["hidkid"]],
      "shell",
    ]);
  });

  it("shows a hidden parent as a context row", () => {
    expect(contextKeys(buildForest(all, noFilter, null))).toEqual(["hid"]);
  });

  it("shows hidden entries as matches with Show hidden", () => {
    expect(shape(buildForest(all, { ...noFilter, showHidden: true }, null))).toEqual([
      ["orch", ["rev", ["t1", ["t1r"]]]],
      "solo",
      ["hid", ["hidkid"]],
      "shell",
    ]);
  });

  it("has no context rows with Show hidden", () => {
    expect(contextKeys(buildForest(all, { ...noFilter, showHidden: true }, null))).toEqual([]);
  });

  it("keeps ancestors of a search match as context", () => {
    expect(shape(buildForest(all, { ...noFilter, query: "review" }, null))).toEqual([
      ["orch", [["t1", ["t1r"]]]],
    ]);
  });

  it("marks the ancestors of a search match as context rows", () => {
    expect(contextKeys(buildForest(all, { ...noFilter, query: "review" }, null))).toEqual(["orch", "t1"]);
  });

  it("shows a status filter's matches with their context", () => {
    expect(shape(buildForest(all, { ...noFilter, status: "working" }, null))).toEqual(["orch", ["hid", ["hidkid"]]]);
  });

  it("marks the context row of a status filter", () => {
    expect(contextKeys(buildForest(all, { ...noFilter, status: "working" }, null))).toEqual(["hid"]);
  });

  it("shows only shells with the shells filter", () => {
    expect(shape(buildForest(all, { ...noFilter, status: "shells" }, null))).toEqual(["shell"]);
  });

  it("forces the selected entry in with its ancestors", () => {
    expect(shape(buildForest(all, noFilter, "rev"))).toEqual([
      ["orch", ["rev", ["t1", ["t1r"]]]],
      "solo",
      ["hid", ["hidkid"]],
      "shell",
    ]);
  });

  it("marks the selected entry and its ancestors as context rows", () => {
    expect(contextKeys(buildForest(all, noFilter, "rev"))).toEqual(["rev", "hid"]);
  });

  it("returns an empty forest when nothing matches", () => {
    expect(buildForest(all, { ...noFilter, query: "zzz" }, null)).toEqual([]);
  });

  it("gives a child one more depth than its parent", () => {
    expect(buildForest(all, noFilter, null)[0]?.children[0]?.children[0]?.depth).toBe(2);
  });

  it("makes an entry with an unknown parent a root", () => {
    expect(shape(buildForest([entry({ key: "c", workspaceId: "c", parentId: "gone" })], noFilter, null))).toEqual(["c"]);
  });

  it("places each entry of a parent cycle once", () => {
    expect(shape(buildForest(cycle, noFilter, null))).toEqual([["x", ["y"]]]);
  });
});

describe("parentOf and ancestorKeys", () => {
  it("finds the parent of an entry", () => {
    expect(parentOf(all, entry({ key: "c", parentId: "t1" }))?.key).toBe("t1");
  });

  it("lists ancestors nearest first", () => {
    expect(ancestorKeys(all, "t1r")).toEqual(["t1", "orch"]);
  });

  it("has no ancestors for a root", () => {
    expect(ancestorKeys(all, "orch")).toEqual([]);
  });

  it("has no ancestors for an unknown key", () => {
    expect(ancestorKeys(all, "nope")).toEqual([]);
  });

  it("stops listing ancestors at a cycle", () => {
    expect(ancestorKeys(cycle, "x")).toEqual(["y"]);
  });
});

describe("matchesFilter", () => {
  it("trims and ignores the case of the search", () => {
    expect(matchesFilter(all[0] ?? entry(), { ...noFilter, query: "  ORCH " })).toBe(true);
  });

  it("matches a search against a repo id", () => {
    const withRepo = entry({
      repos: [{ id: "github.com/bobcob7/buff-bear", path: "buff-bear", ref: "main", refKind: "branch" }],
    });
    expect(matchesFilter(withRepo, { ...noFilter, query: "buff" })).toBe(true);
  });
});

describe("autoExpandedKeys", () => {
  it("opens the ancestors of the selected entry when not filtering", () => {
    expect(new Set(autoExpandedKeys(all, noFilter, "t1r"))).toEqual(new Set(["t1", "orch"]));
  });

  it("opens the ancestors of every match while filtering", () => {
    expect(new Set(autoExpandedKeys(all, { ...noFilter, query: "lint-fix" }, null))).toEqual(new Set(["hid"]));
  });

  it("does not open anything for Show hidden alone", () => {
    expect(autoExpandedKeys(all, { ...noFilter, showHidden: true }, null).size).toBe(0);
  });
});

describe("isFiltering", () => {
  it("is false with no filter", () => {
    expect(isFiltering(noFilter)).toBe(false);
  });

  it("is false for a blank search", () => {
    expect(isFiltering({ ...noFilter, query: " " })).toBe(false);
  });

  it("is true for a status filter", () => {
    expect(isFiltering({ ...noFilter, status: "done" })).toBe(true);
  });

  it("is false for Show hidden alone", () => {
    expect(isFiltering({ ...noFilter, showHidden: true })).toBe(false);
  });
});

describe("isOpen", () => {
  it("prefers an explicit false over auto-expansion", () => {
    expect(isOpen("a", { a: false }, new Set(["a"]))).toBe(false);
  });

  it("uses an explicit true", () => {
    expect(isOpen("a", { a: true }, new Set())).toBe(true);
  });

  it("uses auto-expansion when there is no choice", () => {
    expect(isOpen("a", {}, new Set(["a"]))).toBe(true);
  });

  it("is closed with no choice and no auto-expansion", () => {
    expect(isOpen("a", {}, new Set())).toBe(false);
  });
});
