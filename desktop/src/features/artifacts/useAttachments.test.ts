import { describe, expect, it } from "vitest";
import { insertAt, insertAtCaret, removeReference } from "./useAttachments";

describe("insertAt", () => {
  it("replaces the selected characters with the inserted text", () => {
    expect(insertAt("see the log", 4, 7, "@artifact:art-00000001 ")).toBe("see @artifact:art-00000001  log");
  });
});

describe("insertAtCaret", () => {
  it("appends when there is no caret to read", () => {
    expect(insertAtCaret("fix it", null, " @artifact:art-00000001 ")).toBe("fix it @artifact:art-00000001 ");
  });

  it("inserts at the caret of the element", () => {
    const element = document.createElement("textarea");
    element.value = "fix it";
    element.setSelectionRange(4, 4);

    expect(insertAtCaret("fix it", element, "@artifact:art-00000001 ")).toBe("fix @artifact:art-00000001 it");
  });
});

describe("removeReference", () => {
  it("removes the reference and the space after it", () => {
    expect(removeReference("see @artifact:art-1a2b3c4d and fix it", "art-1a2b3c4d")).toBe("see and fix it");
  });

  it("removes a revision-pinned reference", () => {
    expect(removeReference("@artifact:art-1a2b3c4d/r2 check", "art-1a2b3c4d")).toBe("check");
  });

  it("leaves other references alone", () => {
    expect(removeReference("@artifact:art-00000001 @artifact:art-00000002", "art-00000001")).toBe("@artifact:art-00000002");
  });
});
