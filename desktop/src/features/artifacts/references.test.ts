import { describe, expect, it } from "vitest";
import { appendHint, artifactHref, citationsIn, findRefs, hintLine, parseArtifactHref, refsIn, splitHint } from "./references";

describe("findRefs", () => {
  it("returns distinct ids in first-seen order", () => {
    const text = "see @artifact:art-00000002 and @artifact:art-00000001/r3, then @artifact:art-00000002 again";
    expect(findRefs(text)).toEqual(["art-00000002", "art-00000001"]);
  });

  it("is empty when there are no references", () => {
    expect(findRefs("art-00000001 without the prefix")).toEqual([]);
  });
});

describe("hintLine", () => {
  it("names every id once", () => {
    const want =
      '(Artifacts art-00000001, art-00000002: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)';
    expect(hintLine(["art-00000001", "art-00000002"])).toBe(want);
  });
});

describe("appendHint", () => {
  it("appends the hint to a prompt that references artifacts", () => {
    expect(appendHint("review @artifact:art-00000001")).toBe(
      `review @artifact:art-00000001\n\n${hintLine(["art-00000001"])}`,
    );
  });

  it("leaves a prompt without references alone", () => {
    expect(appendHint("review the diff")).toBe("review the diff");
  });

  it("does not add a second hint", () => {
    const first = appendHint("review @artifact:art-00000001");
    expect(appendHint(first)).toBe(first);
  });
});

describe("refsIn", () => {
  it("finds references with and without a revision", () => {
    expect(refsIn("see @artifact:art-1a2b3c4d/r2 and @artifact:art-5e6f7a8b").map((r) => [r.id, r.rev])).toEqual([
      ["art-1a2b3c4d", 2],
      ["art-5e6f7a8b", null],
    ]);
  });

  it("ignores references in inline code and fences", () => {
    const text = "`@artifact:art-1a2b3c4d` ``a @artifact:art-11111111 ` b``\n```sh\n@artifact:art-22222222\n```\nand @artifact:art-0badf00d";
    expect(refsIn(text).map((r) => r.id)).toEqual(["art-0badf00d"]);
  });

  it("keeps a reference after an unmatched backtick", () => {
    expect(refsIn("a ` b @artifact:art-1a2b3c4d").map((r) => r.id)).toEqual(["art-1a2b3c4d"]);
  });

  it("treats an unclosed fence as code to the end", () => {
    expect(refsIn("~~~\n@artifact:art-1a2b3c4d")).toEqual([]);
  });

  it("reports where each reference is", () => {
    const [ref] = refsIn("x @artifact:art-1a2b3c4d/r3 y");
    expect(ref).toEqual({ id: "art-1a2b3c4d", rev: 3, start: 2, end: 27 });
  });
});

describe("citationsIn", () => {
  it("lists each reference once, outside code", () => {
    expect(citationsIn("@artifact:art-1a2b3c4d `@artifact:art-5e6f7a8b` @artifact:art-1a2b3c4d @artifact:art-1a2b3c4d/r2")).toEqual([
      "@artifact:art-1a2b3c4d",
      "@artifact:art-1a2b3c4d/r2",
    ]);
  });
});

describe("splitHint", () => {
  it("splits the hint line ws appends from the message", () => {
    const text = appendHint("fix @artifact:art-1a2b3c4d and @artifact:art-5e6f7a8b");
    expect(splitHint(text)).toEqual({ body: "fix @artifact:art-1a2b3c4d and @artifact:art-5e6f7a8b", ids: ["art-1a2b3c4d", "art-5e6f7a8b"] });
  });

  it("leaves a message without one alone", () => {
    expect(splitHint("fix it")).toEqual({ body: "fix it", ids: null });
  });
});

describe("parseArtifactHref", () => {
  it("parses the links the plugin makes", () => {
    expect(parseArtifactHref(artifactHref("art-1a2b3c4d", 2))).toEqual({ id: "art-1a2b3c4d", rev: 2 });
    expect(parseArtifactHref(artifactHref("art-1a2b3c4d", null))).toEqual({ id: "art-1a2b3c4d", rev: null });
  });

  it.each(["ws-artifact:art-1A2B3C4D", "ws-artifact:art-1a2b3c4d?rev=0", "ws-artifact:art-1a2b3c4d?rev=1000", "ws-artifact:art-1a2b3c4d/../x", "https://x"])(
    "refuses %s",
    (href) => {
      expect(parseArtifactHref(href)).toBeNull();
    },
  );
});
