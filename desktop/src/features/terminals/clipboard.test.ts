import { describe, expect, it } from "vitest";
import { extractUrls } from "./clipboard";

describe("extractUrls", () => {
  it("finds a URL with a query string", () => {
    const url = "https://claude.ai/oauth/authorize?code=true&client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A54545%2Fcallback";
    expect(extractUrls(`Open this link:\n${url}\nthen paste the code`)).toEqual([url]);
  });

  it("stops at whitespace, brackets and quotes", () => {
    expect(extractUrls('(https://a.test/x) "https://b.test/y" [https://c.test/z] https://d.test/w next')).toEqual([
      "https://a.test/x",
      "https://b.test/y",
      "https://c.test/z",
      "https://d.test/w",
    ]);
  });

  it("drops trailing punctuation and repeats", () => {
    expect(extractUrls("see https://a.test/x. and https://a.test/x, ok")).toEqual(["https://a.test/x"]);
  });

  it("finds nothing in plain text", () => {
    expect(extractUrls("no links, ftp://x.test")).toEqual([]);
  });
});
