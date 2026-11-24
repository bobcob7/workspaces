import { describe, expect, it } from "vitest";
import { dataSource, decodeText } from "./content";

describe("decodeText", () => {
  it("decodes UTF-8 bytes, including multi-byte characters", () => {
    expect(decodeText("w6k=")).toBe("é");
  });

  it("decodes plain ASCII", () => {
    expect(decodeText(btoa("line 1\nline 2"))).toBe("line 1\nline 2");
  });
});

describe("dataSource", () => {
  it("builds a base64 data URL with the content type", () => {
    expect(dataSource("image/png", "AAAA")).toBe("data:image/png;base64,AAAA");
  });
});
