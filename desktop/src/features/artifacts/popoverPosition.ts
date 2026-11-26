export interface Box {
  readonly top: number;
  readonly left: number;
  readonly width: number;
  readonly height: number;
}

export interface Viewport {
  readonly width: number;
  readonly height: number;
}

export type Placement =
  | { readonly mode: "anchored"; readonly top: number; readonly left: number; readonly maxHeight: number }
  | { readonly mode: "docked" };

export const PHONE_MAX_WIDTH = 720;
export const MARGIN = 8;

const MIN_ROOM = 160;
const DOCKED: Placement = { mode: "docked" };

const isVisible = (anchor: Box, viewport: Viewport): boolean =>
  anchor.top + anchor.height > 0 &&
  anchor.top < viewport.height &&
  anchor.left + anchor.width > 0 &&
  anchor.left < viewport.width;

/** Below the anchor if it fits, else above, else docked; left-aligned to the anchor then clamped inside the viewport.
 *  Docked when the viewport is phone-width, when the anchor is not visible, or when neither side has room for the
 *  popover (its height, at least MIN_ROOM). */
export const placePopover = (
  anchor: Box,
  size: { readonly width: number; readonly height: number },
  viewport: Viewport,
): Placement => {
  if (viewport.width <= PHONE_MAX_WIDTH || !isVisible(anchor, viewport)) {
    return DOCKED;
  }

  const needed = Math.max(size.height, MIN_ROOM);
  const anchorBottom = anchor.top + anchor.height;
  const spaceBelow = viewport.height - anchorBottom - MARGIN;
  const spaceAbove = anchor.top - MARGIN;
  const left = Math.max(MARGIN, Math.min(anchor.left, viewport.width - size.width - MARGIN));

  if (spaceBelow >= needed) {
    return { mode: "anchored", top: anchorBottom + MARGIN, left, maxHeight: spaceBelow };
  }
  if (spaceAbove >= needed) {
    return { mode: "anchored", top: anchor.top - MARGIN - size.height, left, maxHeight: size.height };
  }
  return DOCKED;
};
