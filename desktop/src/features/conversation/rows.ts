import type { ConversationItem } from "../../api";

type Of<K extends ConversationItem["kind"]> = Extract<ConversationItem, { kind: K }>;

export type ToolResult = Of<"toolResult">;

/** What the conversation shows: messages, and each tool call with its result. */
export type Row =
  | { readonly kind: "message"; readonly item: Of<"user" | "assistant" | "thinking" | "notice"> }
  | { readonly kind: "tool"; readonly use: Of<"toolUse">; readonly result: ToolResult | null };

/**
 * Pairs each tool call with its result, which the transcript records later.
 * A result whose call was dropped is left out.
 */
export const toRows = (items: readonly ConversationItem[]): readonly Row[] => {
  const results = new Map<string, ToolResult>();
  for (const item of items) {
    if (item.kind === "toolResult") {
      results.set(item.toolUseId, item);
    }
  }
  const rows: Row[] = [];
  for (const item of items) {
    if (item.kind === "toolResult") {
      continue;
    }
    if (item.kind === "toolUse") {
      rows.push({ kind: "tool", use: item, result: results.get(item.toolUseId) ?? null });
      continue;
    }
    rows.push({ kind: "message", item });
  }
  return rows;
};
