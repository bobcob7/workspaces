import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type JSX,
  type ReactNode,
} from "react";
import { placePopover, MARGIN } from "./popoverPosition";
import styles from "./CommentPopover.module.css";

export interface CommentPopoverProps {
  /** The element the popover opens beside; null, or detached from the document, docks it at the bottom. */
  readonly anchorEl: HTMLElement | null;
  readonly onDismiss: () => void;
  readonly children: ReactNode;
}

/** Where the popover sits. Above the anchor it is pinned by its bottom edge, so content that grows extends upward. */
type View =
  | { readonly mode: "docked" }
  | {
      readonly mode: "anchored";
      readonly top?: number;
      readonly bottom?: number;
      readonly left: number;
      readonly maxHeight: number;
    };

const POPOVER_MAX_WIDTH = 360;
const DOCKED: View = { mode: "docked" };

const sameView = (a: View, b: View): boolean => {
  if (a.mode === "docked" || b.mode === "docked") {
    return a.mode === b.mode;
  }
  return a.top === b.top && a.bottom === b.bottom && a.left === b.left && a.maxHeight === b.maxHeight;
};

const computeView = (anchorEl: HTMLElement | null, popover: HTMLElement | null): View => {
  if (anchorEl === null || !anchorEl.isConnected) {
    return DOCKED;
  }
  const viewport = { width: window.innerWidth, height: window.innerHeight };
  const size = {
    width: Math.min(POPOVER_MAX_WIDTH, viewport.width - 2 * MARGIN),
    height: popover?.getBoundingClientRect().height ?? 0,
  };
  const anchor = anchorEl.getBoundingClientRect();
  const placement = placePopover(anchor, size, viewport);
  if (placement.mode === "docked") {
    return DOCKED;
  }
  if (placement.top < anchor.top) {
    return {
      mode: "anchored",
      bottom: viewport.height - (anchor.top - MARGIN),
      left: placement.left,
      maxHeight: anchor.top - 2 * MARGIN,
    };
  }
  return { mode: "anchored", top: placement.top, left: placement.left, maxHeight: placement.maxHeight };
};

const anchoredStyle = (view: View): CSSProperties | undefined =>
  view.mode === "anchored"
    ? { top: view.top, bottom: view.bottom, left: view.left, maxHeight: view.maxHeight }
    : undefined;

/** Escape inside another dialog (such as a MUI dialog) belongs to that dialog. */
const isInOtherDialog = (target: EventTarget | null, popover: HTMLElement | null): boolean => {
  if (!(target instanceof Element)) {
    return false;
  }
  const dialog = target.closest('[role="dialog"]');
  return dialog !== null && dialog !== popover;
};

/** A floating dialog beside the anchor, or docked at the bottom of the viewport. Holds the composer while it is open. */
export const CommentPopover = ({ anchorEl, onDismiss, children }: CommentPopoverProps): JSX.Element => {
  const popoverRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View>(DOCKED);

  useLayoutEffect(() => {
    const reposition = (): void => {
      const next = computeView(anchorEl, popoverRef.current);
      setView((current) => (sameView(current, next) ? current : next));
    };
    reposition();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(reposition);
    if (popoverRef.current !== null) {
      observer?.observe(popoverRef.current);
    }
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [anchorEl]);

  useEffect(() => {
    popoverRef.current?.querySelector("textarea")?.focus();
  }, []);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key !== "Escape" || event.defaultPrevented || isInOtherDialog(event.target, popoverRef.current)) {
        return;
      }
      if (anchorEl?.isConnected) {
        anchorEl.focus();
      }
      onDismiss();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [anchorEl, onDismiss]);

  return (
    <div
      ref={popoverRef}
      role="dialog"
      aria-label="New comment"
      className={`${styles.popover} ${view.mode === "docked" ? styles.docked : styles.anchored}`}
      style={anchoredStyle(view)}
    >
      {children}
    </div>
  );
};
