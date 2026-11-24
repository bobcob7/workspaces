import { describe, expect, it } from "vitest";
import { appendHint, findRefs, hintLine } from "./references";

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
