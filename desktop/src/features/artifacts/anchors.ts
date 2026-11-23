import type { Element, ElementContent, Root, Text } from "hast";
import type { Anchor, NewAnchor, Thread } from "../../api";

/** The longest quote a whole-block comment carries, in code points. */
const maxBlockQuote = 2_000;

/** The context kept before and after a quote, in code points (ADR 0079). */
const maxContext = 32;

const firstCodePoints = (text: string, count: number): string => Array.from(text).slice(0, count).join("");

const lastCodePoints = (text: string, count: number): string => Array.from(text).slice(-count).join("");

/** The source lines `lineStart` to `lineEnd` (1-based, inclusive), joined by newlines. */
const blockSource = (source: string, lineStart: number, lineEnd: number): string =>
  source.split("\n").slice(lineStart - 1, lineEnd).join("\n");

/**
 * The anchor for a selection inside one rendered block. The selected text
 * anchors itself when it appears in the block's source as-is; otherwise
 * (it crosses bold, a link or code) the whole block anchors.
 */
export const selectionAnchor = (source: string, lineStart: number, lineEnd: number, selected: string): NewAnchor => {
  const block = blockSource(source, lineStart, lineEnd);
  const wanted = selected.trim();
  const at = wanted === "" ? -1 : block.indexOf(wanted);
  if (at === -1) {
    return { quote: firstCodePoints(block.trim(), maxBlockQuote), line: lineStart, prefix: "", suffix: "" };
  }
  const linesBefore = block.slice(0, at).split("\n").length - 1;
  return {
    quote: wanted,
    line: lineStart + linesBefore,
    prefix: lastCodePoints(block.slice(0, at), maxContext),
    suffix: firstCodePoints(block.slice(at + wanted.length), maxContext),
  };
};

interface Covering {
  readonly element: Element;
  readonly depth: number;
}

const coversLine = (element: Element, line: number): boolean => {
  const start = element.position?.start.line;
  const end = element.position?.end.line;
  return start !== undefined && end !== undefined && start <= line && line <= end;
};

/** The deepest element whose source lines include `line`, or null. */
const innermostCovering = (parent: Root | Element, line: number, depth = 0): Covering | null => {
  let best: Covering | null = null;
  for (const child of parent.children) {
    if (child.type !== "element") {
      continue;
    }
    const inner = innermostCovering(child, line, depth + 1);
    const candidate = inner ?? (coversLine(child, line) ? { element: child, depth: depth + 1 } : null);
    if (candidate !== null && (best === null || candidate.depth > best.depth)) {
      best = candidate;
    }
  }
  return best;
};

/** Wraps the first text occurrence of `quote` under `element` in a mark for `thread`. */
const markQuote = (element: Element, quote: string, thread: string): boolean => {
  for (const [index, child] of element.children.entries()) {
    if (child.type === "text") {
      if (wrapInText(element, index, child, quote, thread)) {
        return true;
      }
      continue;
    }
    if (child.type === "element" && markQuote(child, quote, thread)) {
      return true;
    }
  }
  return false;
};

const wrapInText = (parent: Element, index: number, node: Text, quote: string, thread: string): boolean => {
  const at = node.value.indexOf(quote);
  if (quote === "" || at === -1) {
    return false;
  }
  const replacement: ElementContent[] = [];
  const before = node.value.slice(0, at);
  const after = node.value.slice(at + quote.length);
  if (before !== "") {
    replacement.push({ type: "text", value: before });
  }
  replacement.push({ type: "element", tagName: "mark", properties: { dataThread: thread }, children: [{ type: "text", value: quote }] });
  if (after !== "") {
    replacement.push({ type: "text", value: after });
  }
  parent.children.splice(index, 1, ...replacement);
  return true;
};

const markBlock = (element: Element, thread: string): void => {
  const existing = element.properties.className;
  const classes = Array.isArray(existing) ? existing.map(String) : [];
  element.properties.className = [...classes, "anchoredBlock"];
  element.properties.dataThread = thread;
};

const highlight = (tree: Root, thread: string, anchor: Anchor): void => {
  const target = innermostCovering(tree, anchor.lineStart);
  if (target === null) {
    return;
  }
  if (!markQuote(target.element, anchor.quote, thread)) {
    markBlock(target.element, thread);
  }
};

export type AnchorTransformer = (tree: Root) => void;

/**
 * A rehype plugin that highlights each open thread anchored to `latest`:
 * its quote in the block at its line, or the block when the quote isn't
 * there as plain text.
 */
export const rehypeAnchors =
  (threads: readonly Thread[], latest: number): AnchorTransformer =>
  (tree) => {
    for (const thread of threads) {
      const anchor = thread.anchor;
      if (thread.resolved || anchor === null || anchor.state !== "anchored" || anchor.rev !== latest) {
        continue;
      }
      highlight(tree, thread.id, anchor);
    }
  };
