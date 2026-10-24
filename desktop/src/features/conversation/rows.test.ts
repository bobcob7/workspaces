import { describe, expect, it } from "vitest";
import type { ConversationItem } from "../../api";
import { toRows } from "./rows";

const use = (toolUseId: string): ConversationItem => ({
  kind: "toolUse",
  id: `use-${toolUseId}`,
  at: null,
  toolUseId,
  name: "Bash",
  summary: "ls",
  input: "ls",
});

const result = (toolUseId: string): ConversationItem => ({
  kind: "toolResult",
  id: `result-${toolUseId}`,
  at: null,
  toolUseId,
  text: "out",
  isError: false,
});

describe("toRows", () => {
  it("pairs a tool call with its later result", () => {
    const rows = toRows([use("t1"), { kind: "assistant", id: "a", at: null, text: "hi" }, result("t1")]);
    expect(rows.map((r) => (r.kind === "tool" ? `tool:${r.result?.text ?? "none"}` : r.item.kind))).toEqual([
      "tool:out",
      "assistant",
    ]);
  });

  it("leaves a call without a result running", () => {
    const [row] = toRows([use("t1")]);
    expect(row?.kind === "tool" && row.result).toBeNull();
  });

  it("leaves out a result whose call isn't kept", () => {
    expect(toRows([result("gone")])).toEqual([]);
  });
});
