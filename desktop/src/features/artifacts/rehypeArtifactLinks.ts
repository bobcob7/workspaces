import type { Element, ElementContent, Root, RootContent } from "hast";
import { artifactHref, refsIn } from "./references";

/** The property that marks a link this plugin made; the `a` component honours `ws-artifact:` only on such links. */
export const artifactLinkMark = "dataWsArtifact";

/** Elements whose text is never linked: code, and links already. */
const skipped = new Set(["code", "pre", "a"]);

const linkFor = (id: string, rev: number | null, text: string): Element => ({
  type: "element",
  tagName: "a",
  properties: { href: artifactHref(id, rev), [artifactLinkMark]: id, dataWsRev: rev === null ? undefined : String(rev) },
  children: [{ type: "text", value: text }],
});

/** A text node split into text and links at each `@artifact:` reference. */
const linkText = (value: string): ElementContent[] => {
  const found = refsIn(value);
  if (found.length === 0) {
    return [{ type: "text", value }];
  }
  const parts: ElementContent[] = [];
  let at = 0;
  for (const ref of found) {
    if (ref.start > at) {
      parts.push({ type: "text", value: value.slice(at, ref.start) });
    }
    parts.push(linkFor(ref.id, ref.rev, value.slice(ref.start, ref.end)));
    at = ref.end;
  }
  if (at < value.length) {
    parts.push({ type: "text", value: value.slice(at) });
  }
  return parts;
};

const visit = (node: Root | Element): void => {
  const children: (RootContent | ElementContent)[] = [];
  for (const child of node.children) {
    if (child.type === "text") {
      children.push(...linkText(child.value));
      continue;
    }
    if (child.type === "element" && !skipped.has(child.tagName)) {
      visit(child);
    }
    children.push(child);
  }
  node.children = children as typeof node.children;
};

/** A rehype plugin that links `@artifact:<id>[/r<n>]` references outside code to the artifact, in-app (ADR 0084). */
export const rehypeArtifactLinks = () => (tree: Root): void => {
  visit(tree);
};
