import { describe, expect, it } from "vitest";
import type { Entry } from "../../api";
import { entry, treeEntries } from "../../test/fixtures";
import {
  attentionEntries,
  childrenOf,
  descendantsOf,
  groupOf,
  isChildOf,
  sweptWithRemoval,
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

describe("isChildOf", () => {
  const local = entry({ key: "p", workspaceId: "p", environment: "local" });

  it("a child with a parentId is a child of the same-environment parent", () => {
    expect(isChildOf(entry({ key: "c", workspaceId: "c", parentId: "p" }), local)).toBe(true);
  });

  it("a child with a scopeParent is a child of the parent it names", () => {
    const remote = entry({ key: "box:c", workspaceId: "c", environment: "box", scopeParent: { environment: "local", workspaceId: "p" } });
    expect(isChildOf(remote, local)).toBe(true);
  });

  it("a scopeParent naming another environment is not a child of that workspace id", () => {
    const remote = entry({ key: "box:c", workspaceId: "c", environment: "box", scopeParent: { environment: "box", workspaceId: "p" } });
    expect(isChildOf(remote, local)).toBe(false);
  });

  it("a parentId wins over a scopeParent", () => {
    const child = entry({ key: "c", workspaceId: "c", parentId: "q", scopeParent: { environment: "local", workspaceId: "p" } });
    expect(isChildOf(child, local)).toBe(false);
  });

  it("a parentId naming the workspace id on another environment is not a child", () => {
    const box = entry({ key: "box:p", workspaceId: "p", environment: "box" });
    expect(isChildOf(entry({ key: "c", workspaceId: "c", environment: "local", parentId: "p" }), box)).toBe(false);
  });

  it("a scopeParent with the same workspaceId on another environment is not a child", () => {
    const remote = entry({ key: "other:c", workspaceId: "c", environment: "other", scopeParent: { environment: "box", workspaceId: "p" } });
    expect(isChildOf(remote, local)).toBe(false);
  });

  it("an entry is never its own parent", () => {
    const self = entry({ key: "p", workspaceId: "p", scopeParent: { environment: "local", workspaceId: "p" } });
    expect(isChildOf(self, self)).toBe(false);
  });
});

describe("childrenOf across environments", () => {
  it("includes a child started from a scopeParent on another environment", () => {
    const parent = entry({ key: "p", workspaceId: "p", environment: "local" });
    const remote = entry({ key: "box:c", workspaceId: "c", environment: "box", scopeParent: { environment: "local", workspaceId: "p" } });
    expect(childrenOf([parent, remote], parent)).toEqual([remote]);
  });
});

describe("descendantsOf across environments", () => {
  it("walks local, then a linux-server child, then a win-rig grandchild", () => {
    const local = entry({ key: "plan", workspaceId: "plan", environment: "local" });
    const linux = entry({
      key: "linux-server:x",
      workspaceId: "x",
      environment: "linux-server",
      scopeParent: { environment: "local", workspaceId: "plan" },
    });
    const win = entry({
      key: "win-rig:y",
      workspaceId: "y",
      environment: "win-rig",
      scopeParent: { environment: "linux-server", workspaceId: "x" },
    });
    const found = descendantsOf([win, local, linux], local).map((d) => [d.entry.key, d.depth]);
    expect(found).toEqual([
      ["linux-server:x", 1],
      ["win-rig:y", 2],
    ]);
  });
});

describe("sweptWithRemoval", () => {
  const root = entry({ key: "plan", workspaceId: "plan", environment: "local" });
  const remote = (name: string, environment: string, parent: Entry, extra: Partial<Entry> = {}): Entry =>
    entry({
      key: `${environment}:${name}`,
      workspaceId: name,
      environment,
      scopeParent: { environment: parent.environment, workspaceId: parent.workspaceId! },
      ...extra,
    });
  const swept = (entries: readonly Entry[]): string[] =>
    [...sweptWithRemoval(descendantsOf(entries, root), root)].sort();

  it("sweeps a cross-environment child of the removed parent", () => {
    const linux = remote("ip-linux", "linux-server", root);
    expect(swept([root, linux])).toEqual(["linux-server:ip-linux"]);
  });

  it("sweeps a cross-environment child of a swept child, transitively", () => {
    const linux = remote("ip-linux", "linux-server", root);
    const probe = remote("ip-probe", "win-rig", linux);
    const deep = remote("ip-deep", "mac", probe);
    expect(swept([root, linux, probe, deep])).toEqual(["linux-server:ip-linux", "mac:ip-deep", "win-rig:ip-probe"]);
  });

  it("keeps a same-environment child, and what only it started on its own environment", () => {
    const linux = remote("ip-linux", "linux-server", root);
    const note = entry({ key: "linux-server:note", environment: "linux-server", workspaceId: "note", parentId: "ip-linux" });
    expect(swept([root, linux, note])).toEqual(["linux-server:ip-linux"]);
  });

  it("keeps a cross-environment child of a kept child", () => {
    const note = entry({ key: "local:note", workspaceId: "note", parentId: "plan" });
    const far = remote("far", "win-rig", note);
    expect(swept([root, note, far])).toEqual([]);
  });

  it("keeps a child that has a parentId, whatever its scopeParent says", () => {
    const linux = remote("ip-linux", "linux-server", root);
    const both = remote("both", "win-rig", linux, { parentId: "other" });
    const listed = [linux, both].map((e) => ({ entry: e, depth: 1 }));
    expect([...sweptWithRemoval(listed, root)]).toEqual(["linux-server:ip-linux"]);
  });

  it("keeps a child that names a parent on its own environment", () => {
    const linux = remote("ip-linux", "linux-server", root);
    const sibling = remote("sib", "linux-server", linux);
    expect(swept([root, linux, sibling])).toEqual(["linux-server:ip-linux"]);
  });
});
