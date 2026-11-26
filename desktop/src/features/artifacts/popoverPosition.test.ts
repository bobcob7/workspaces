import { describe, expect, it } from "vitest";
import { placePopover, type Box, type Viewport } from "./popoverPosition";

const viewport: Viewport = { width: 1280, height: 800 };
const size = { width: 360, height: 200 };
const anchorAt = (top: number, left = 50, height = 20): Box => ({ top, left, width: 200, height });

describe("placePopover", () => {
  it("places the popover below the anchor when there is room", () => {
    expect(placePopover(anchorAt(100), size, viewport)).toEqual({
      mode: "anchored",
      top: 128,
      left: 50,
      maxHeight: 672,
    });
  });

  it("flips the popover above the anchor when there is no room below", () => {
    expect(placePopover(anchorAt(700), size, viewport)).toEqual({
      mode: "anchored",
      top: 492,
      left: 50,
      maxHeight: 200,
    });
  });

  it("clamps the popover inside the right edge of the viewport", () => {
    expect(placePopover(anchorAt(100, 1200, 20), size, viewport)).toMatchObject({ left: 912 });
  });

  it("clamps the popover inside the left edge of the viewport", () => {
    expect(placePopover(anchorAt(100, 2), size, viewport)).toMatchObject({ left: 8 });
  });

  it("docks on a phone-width viewport even when the anchor is visible", () => {
    expect(placePopover(anchorAt(100), size, { width: 390, height: 844 })).toEqual({ mode: "docked" });
  });

  it("docks at exactly the phone breakpoint", () => {
    expect(placePopover(anchorAt(100), size, { width: 720, height: 800 })).toEqual({ mode: "docked" });
  });

  it("anchors just above the phone breakpoint", () => {
    expect(placePopover(anchorAt(100), size, { width: 721, height: 800 })).toMatchObject({ mode: "anchored" });
  });

  it("docks when the anchor is entirely above the viewport", () => {
    expect(placePopover(anchorAt(-50), size, viewport)).toEqual({ mode: "docked" });
  });

  it("docks when the anchor is entirely below the viewport", () => {
    expect(placePopover(anchorAt(820), size, viewport)).toEqual({ mode: "docked" });
  });

  it("docks when the anchor is entirely left of the viewport", () => {
    expect(placePopover(anchorAt(100, -220), size, viewport)).toEqual({ mode: "docked" });
  });

  it("docks when the anchor is entirely right of the viewport", () => {
    expect(placePopover(anchorAt(100, 1280), size, viewport)).toEqual({ mode: "docked" });
  });

  it("stays anchored when the anchor is only partly visible", () => {
    expect(placePopover(anchorAt(-10), size, viewport)).toMatchObject({ mode: "anchored" });
  });

  it("docks when the room above is one pixel short of the margin-inclusive popover height", () => {
    expect(placePopover(anchorAt(207), size, { width: 1280, height: 300 })).toEqual({ mode: "docked" });
  });

  it("docks when neither side has room for the popover", () => {
    expect(placePopover(anchorAt(140), size, { width: 1280, height: 300 })).toEqual({ mode: "docked" });
  });

  it("needs at least 160px of room even for a short popover", () => {
    expect(placePopover(anchorAt(50), { width: 360, height: 40 }, { width: 1280, height: 250 })).toEqual({
      mode: "anchored",
      top: 78,
      left: 50,
      maxHeight: 172,
    });
    expect(placePopover(anchorAt(50), { width: 360, height: 40 }, { width: 1280, height: 230 })).toEqual({
      mode: "docked",
    });
  });
});
