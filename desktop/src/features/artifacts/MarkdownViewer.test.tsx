import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Thread } from "../../api";
import { MarkdownViewer, type MarkdownViewerProps } from "./MarkdownViewer";

const thread = (overrides: Partial<Thread> = {}): Thread => ({
  id: "t1",
  anchor: { rev: 2, start: 0, end: 16, lineStart: 3, lineEnd: 3, quote: "cache is shared", prefix: "", suffix: "", state: "anchored" },
  resolved: false,
  resolvedAt: null,
  resolvedBy: null,
  comments: [],
  ...overrides,
});

const renderViewer = (overrides: Partial<MarkdownViewerProps> = {}) => {
  const props: MarkdownViewerProps = {
    source: "# Pitch\n\nToday the cache is shared.\n",
    highlights: [],
    revision: 2,
    onComment: null,
    activeThread: null,
    onThreadClick: vi.fn(),
    ...overrides,
  };
  return render(<MarkdownViewer {...props} />);
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe("MarkdownViewer", () => {
  it("gives each block its source lines, as the selection anchors need", () => {
    const { container } = renderViewer({ source: "# A\n\nB **c**\n" });

    expect(container.querySelector("p")).toHaveAttribute("data-line-start", "3");
    expect(container.querySelector("h1")).toHaveAttribute("data-line-start", "1");
  });

  it("renders GFM tables and task lists", () => {
    renderViewer({ source: "| a | b |\n| - | - |\n| 1 | 2 |\n\n- [x] done\n" });

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "2" })).toBeInTheDocument();
  });

  it("drops raw HTML and keeps the text around it", () => {
    const { container } = renderViewer({ source: "<img src=x onerror=alert(1)>\n\n<b>bold</b> text\n" });

    expect(container.querySelector("img, b")).toBeNull();
    expect(screen.getByText(/text/)).toBeInTheDocument();
  });

  it("renders a javascript link with an empty href", () => {
    const { container } = renderViewer({ source: "[run](javascript:alert(1))\n" });

    expect(container.querySelector("a")).toHaveAttribute("href", "");
    expect(screen.getByText("run")).toBeInTheDocument();
  });

  it("opens a web link outside the app instead of navigating", async () => {
    const openExternal = vi.spyOn(api, "openExternal").mockResolvedValue(undefined);
    renderViewer({ source: "[docs](https://example.com/docs)\n" });

    await userEvent.click(screen.getByRole("link", { name: "docs" }));

    expect(openExternal).toHaveBeenCalledWith("https://example.com/docs");
  });

  it("shows an image as its alt text and address, not as a picture", () => {
    const { container } = renderViewer({ source: "![logo](https://example.com/logo.png)\n" });

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("[image: logo](https://example.com/logo.png)")).toBeInTheDocument();
  });

  it("wraps the quote of an open thread on the latest revision in a mark", () => {
    const { container } = renderViewer({ highlights: [thread()] });

    expect(container.querySelector('mark[data-thread="t1"]')).toHaveTextContent("cache is shared");
  });

  it("offers a Comment button for a selection and sends its anchor", async () => {
    const onComment = vi.fn();
    const { container } = renderViewer({ source: "Today the cache is shared.\n", onComment });
    const paragraph = container.querySelector("p");
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(paragraph as HTMLParagraphElement);
    selection?.removeAllRanges();
    selection?.addRange(range);

    fireEvent.mouseUp(screen.getByRole("article", { name: "Document" }));
    await userEvent.click(screen.getByRole("button", { name: "Comment" }));

    expect(onComment).toHaveBeenCalledWith(
      { quote: "Today the cache is shared.", line: 1, prefix: "", suffix: "" },
      false,
    );
  });

  it("anchors the whole paragraph when the selection crosses bold markup", async () => {
    const onComment = vi.fn();
    const { container } = renderViewer({ source: "**Bold** text\n", onComment });
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(container.querySelector("p") as HTMLParagraphElement);
    selection?.removeAllRanges();
    selection?.addRange(range);

    fireEvent.mouseUp(screen.getByRole("article", { name: "Document" }));
    await userEvent.click(screen.getByRole("button", { name: "Comment" }));

    expect(onComment).toHaveBeenCalledWith({ quote: "**Bold** text", line: 1, prefix: "", suffix: "" }, true);
  });
});
