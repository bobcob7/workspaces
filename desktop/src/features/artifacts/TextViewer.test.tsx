import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TextViewer } from "./TextViewer";

describe("TextViewer", () => {
  it("shows the text in a preformatted block, keeping its line breaks", () => {
    render(<TextViewer text={"line 1\nline 2"} />);

    const block = screen.getByText(/line 1/);
    expect(block.tagName).toBe("PRE");
    expect(block.textContent).toBe("line 1\nline 2");
  });
});
