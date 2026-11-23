import type { Element, Root } from "hast";
import { describe, expect, it } from "vitest";
import type { Thread } from "../../api";
import { rehypeAnchors, selectionAnchor } from "./anchors";

const source = "# Pitch\n\nToday the cache is shared across workspaces.\n\n## Design\n\nFirst line\nsecond line\n";

describe("selectionAnchor", () => {
  it("anchors a selection that appears in the block's source as-is", () => {
    expect(selectionAnchor(source, 3, 3, "cache is shared")).toEqual({
      quote: "cache is shared",
      line: 3,
      prefix: "Today the ",
      suffix: " across workspaces.",
    });
  });

  it("counts the lines before the selection inside a multi-line block", () => {
    expect(selectionAnchor(source, 7, 8, "second line").line).toBe(8);
  });

  it("counts code points, not UTF-16 units, for the context", () => {
    const text = "Café ☕ time, then the lattes\n";

    expect(selectionAnchor(text, 1, 1, "lattes")).toEqual({
      quote: "lattes",
      line: 1,
      prefix: "Café ☕ time, then the ",
      suffix: "",
    });
  });

  it("anchors the whole block when the selection crosses markup", () => {
    expect(selectionAnchor("**bold** text\n", 1, 1, "bold text")).toEqual({
      quote: "**bold** text",
      line: 1,
      prefix: "",
      suffix: "",
    });
  });

  it("keeps at most 2,000 code points of a whole-block quote", () => {
    const longBlock = "a".repeat(2_500);

    expect(selectionAnchor(longBlock, 1, 1, "not in the block").quote).toHaveLength(2_000);
  });

  it("keeps at most 32 code points of context on each side", () => {
    const text = `${"x".repeat(40)}target${"y".repeat(40)}\n`;

    const anchor = selectionAnchor(text, 1, 1, "target");

    expect(anchor.prefix).toHaveLength(32);
    expect(anchor.suffix).toHaveLength(32);
  });
});

const position = (line: number) => ({
  start: { line, column: 1, offset: 0 },
  end: { line, column: 20, offset: 20 },
});

const paragraph = (line: number, text: string): Element => ({
  type: "element",
  tagName: "p",
  properties: {},
  position: position(line),
  children: [{ type: "text", value: text }],
});

const tree = (...children: Element[]): Root => ({ type: "root", children });

const openThread = (id: string, quote: string, lineStart: number, rev = 2): Thread => ({
  id,
  anchor: { rev, start: 0, end: quote.length, lineStart, lineEnd: lineStart, quote, prefix: "", suffix: "", state: "anchored" },
  resolved: false,
  resolvedAt: null,
  resolvedBy: null,
  comments: [],
});

const run = (root: Root, threads: readonly Thread[], latest = 2): Root => {
  rehypeAnchors(threads, latest)(root);
  return root;
};

describe("rehypeAnchors", () => {
  it("wraps the quote in a mark in the block at its line", () => {
    const root = run(tree(paragraph(3, "Today the cache is shared.")), [openThread("t1", "cache is shared", 3)]);

    const [first] = root.children;
    expect(first?.type === "element" ? first.children : []).toEqual([
      { type: "text", value: "Today the " },
      { type: "element", tagName: "mark", properties: { dataThread: "t1" }, children: [{ type: "text", value: "cache is shared" }] },
      { type: "text", value: "." },
    ]);
  });

  it("marks the whole block when its text doesn't contain the quote", () => {
    const root = run(tree(paragraph(3, "Today the cache is shared.")), [openThread("t2", "not in here", 3)]);

    const [first] = root.children;
    expect(first).toMatchObject({ properties: { className: ["anchoredBlock"], dataThread: "t2" } });
  });

  it("leaves the tree alone for resolved threads and other revisions", () => {
    const resolved = { ...openThread("t3", "cache is shared", 3), resolved: true };
    const older = openThread("t4", "cache is shared", 3, 1);
    const root = run(tree(paragraph(3, "Today the cache is shared.")), [resolved, older]);

    expect(root.children).toEqual([paragraph(3, "Today the cache is shared.")]);
  });
});
