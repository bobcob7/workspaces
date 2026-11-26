import type { Element, Root } from "hast";
import { describe, expect, it } from "vitest";
import { artifactLinkMark, rehypeArtifactLinks } from "./rehypeArtifactLinks";

const text = (value: string) => ({ type: "text" as const, value });
const el = (tagName: string, children: Element["children"]): Element => ({ type: "element", tagName, properties: {}, children });
const run = (children: Root["children"]): Root => {
  const tree: Root = { type: "root", children };
  rehypeArtifactLinks()(tree);
  return tree;
};
const links = (node: Root | Element): Element[] =>
  node.children.flatMap((c) => (c.type !== "element" ? [] : c.tagName === "a" ? [c] : links(c)));

describe("rehypeArtifactLinks", () => {
  it("links references at the start, middle and end of a text node", () => {
    const tree = run([el("p", [text("@artifact:art-1a2b3c4d mid @artifact:art-5e6f7a8b/r2@artifact:art-0badf00d")])]);
    const found = links(tree);
    expect(found.map((a) => a.properties.href)).toEqual([
      "ws-artifact:art-1a2b3c4d",
      "ws-artifact:art-5e6f7a8b?rev=2",
      "ws-artifact:art-0badf00d",
    ]);
    expect(found.every((a) => a.properties[artifactLinkMark] !== undefined)).toBe(true);
    const p = tree.children[0] as Element;
    expect(p.children.filter((c) => c.type === "text").map((c) => (c.type === "text" ? c.value : ""))).toEqual([" mid "]);
  });

  it("leaves text without references alone", () => {
    const tree = run([el("p", [text("plain")])]);
    expect(tree.children[0]).toEqual(el("p", [text("plain")]));
  });

  it.each(["code", "pre", "a"])("skips text inside %s", (tag) => {
    const made = links(run([el(tag, [text("@artifact:art-1a2b3c4d")])])).filter((a) => a.properties[artifactLinkMark] !== undefined);
    expect(made).toEqual([]);
  });

  it("links inside nested elements", () => {
    const strong = el("strong", [text("@artifact:art-1a2b3c4d")]);
    const tree = run([el("ul", [el("li", [strong])])]);
    expect(links(tree)).toHaveLength(1);
  });
});
