import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CommentPopover } from "./CommentPopover";

interface Rect {
  readonly top: number;
  readonly left: number;
  readonly width: number;
  readonly height: number;
}

const toDomRect = (rect: Rect): DOMRect => ({
  ...rect,
  x: rect.left,
  y: rect.top,
  right: rect.left + rect.width,
  bottom: rect.top + rect.height,
  toJSON: () => ({}),
});

const addAnchor = (rect: Rect): HTMLButtonElement => {
  const anchor = document.createElement("button");
  anchor.textContent = "Comment on paragraph";
  document.body.append(anchor);
  vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(toDomRect(rect));
  return anchor;
};

const setViewport = (width: number, height: number): void => {
  vi.stubGlobal("innerWidth", width);
  vi.stubGlobal("innerHeight", height);
};

const renderPopover = (anchorEl: HTMLElement | null, onDismiss = vi.fn()): ReturnType<typeof render> =>
  render(
    <CommentPopover anchorEl={anchorEl} onDismiss={onDismiss}>
      <textarea aria-label="Comment" />
    </CommentPopover>,
  );

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe("CommentPopover", () => {
  it("moves focus into the comment textarea when it opens", () => {
    setViewport(1280, 800);
    renderPopover(addAnchor({ top: 100, left: 50, width: 200, height: 20 }));

    expect(screen.getByRole("textbox", { name: "Comment" })).toHaveFocus();
  });

  it("calls onDismiss when Escape is pressed in the textarea", async () => {
    setViewport(1280, 800);
    const onDismiss = vi.fn();
    renderPopover(addAnchor({ top: 100, left: 50, width: 200, height: 20 }), onDismiss);

    await userEvent.keyboard("{Escape}");

    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it("returns focus to the anchor when Escape dismisses it", async () => {
    setViewport(1280, 800);
    const anchor = addAnchor({ top: 100, left: 50, width: 200, height: 20 });
    renderPopover(anchor);

    await userEvent.keyboard("{Escape}");

    expect(anchor).toHaveFocus();
  });

  it("repositions when the page scrolls", () => {
    setViewport(1280, 800);
    const anchor = addAnchor({ top: 100, left: 50, width: 200, height: 20 });
    renderPopover(anchor);
    const dialog = screen.getByRole("dialog", { name: "New comment" });
    expect(dialog.style.top).toBe("128px");

    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(
      toDomRect({ top: 300, left: 50, width: 200, height: 20 }),
    );
    fireEvent.scroll(window);

    expect(dialog.style.top).toBe("328px");
  });

  it("repositions when a scrolling container above the anchor scrolls", () => {
    setViewport(1280, 800);
    const pane = document.createElement("div");
    document.body.append(pane);
    const anchor = addAnchor({ top: 100, left: 50, width: 200, height: 20 });
    pane.append(anchor);
    renderPopover(anchor);
    const dialog = screen.getByRole("dialog", { name: "New comment" });

    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(
      toDomRect({ top: 300, left: 50, width: 200, height: 20 }),
    );
    fireEvent.scroll(pane);

    expect(dialog.style.top).toBe("328px");
  });

  it("repositions when the window resizes", () => {
    setViewport(1280, 800);
    const anchor = addAnchor({ top: 300, left: 50, width: 200, height: 20 });
    renderPopover(anchor);
    const dialog = screen.getByRole("dialog", { name: "New comment" });
    expect(dialog.style.top).toBe("328px");
    vi.spyOn(dialog, "getBoundingClientRect").mockReturnValue(
      toDomRect({ top: 0, left: 0, width: 360, height: 200 }),
    );

    setViewport(1280, 400);
    fireEvent.resize(window);

    expect(dialog.style.top).toBe("92px");
  });

  it("docks at the bottom of a narrow viewport", () => {
    setViewport(500, 800);
    renderPopover(addAnchor({ top: 100, left: 50, width: 200, height: 20 }));

    expect(screen.getByRole("dialog", { name: "New comment" }).style.top).toBe("");
  });

  it("docks when there is no anchor element", () => {
    setViewport(1280, 800);
    renderPopover(null);

    expect(screen.getByRole("dialog", { name: "New comment" }).style.top).toBe("");
  });

  it("docks when the anchor has been removed from the document", () => {
    setViewport(1280, 800);
    const anchor = addAnchor({ top: 100, left: 50, width: 200, height: 20 });
    anchor.remove();
    renderPopover(anchor);

    expect(screen.getByRole("dialog", { name: "New comment" }).style.top).toBe("");
  });

  it("removes its scroll and resize listeners on unmount", () => {
    setViewport(1280, 800);
    const removeListener = vi.spyOn(window, "removeEventListener");
    const { unmount } = renderPopover(addAnchor({ top: 100, left: 50, width: 200, height: 20 }));

    unmount();

    expect(removeListener).toHaveBeenCalledWith("scroll", expect.any(Function), true);
    expect(removeListener).toHaveBeenCalledWith("resize", expect.any(Function));
  });
});
