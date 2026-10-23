import { describe, expect, it } from "vitest";
import { entry } from "../../test/fixtures";
import { groupEntries, groupOf, needsAttention } from "./status";

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
