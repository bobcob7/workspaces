import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type JSX,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { placePopover, MARGIN, type Placement } from "./popoverPosition";
import styles from "./CommentPopover.module.css";

export interface CommentPopoverProps {
  /** The element the popover opens beside; null, or detached from the document, docks it at the bottom. */
  readonly anchorEl: HTMLElement | null;
  readonly onDismiss: () => void;
  readonly children: ReactNode;
}

const POPOVER_MAX_WIDTH = 360;
const DOCKED: Placement = { mode: "docked" };

const samePlacement = (a: Placement, b: Placement): boolean => {
  if (a.mode === "anchored" && b.mode === "anchored") {
    return a.top === b.top && a.left === b.left && a.maxHeight === b.maxHeight;
  }
  return a.mode === b.mode;
};

const computePlacement = (anchorEl: HTMLElement | null, popover: HTMLElement | null): Placement => {
  if (anchorEl === null || !anchorEl.isConnected) {
    return DOCKED;
  }
  const viewport = { width: window.innerWidth, height: window.innerHeight };
  const size = {
    width: Math.min(POPOVER_MAX_WIDTH, viewport.width - 2 * MARGIN),
    height: popover?.getBoundingClientRect().height ?? 0,
  };
  return placePopover(anchorEl.getBoundingClientRect(), size, viewport);
};

const anchoredStyle = (placement: Placement): CSSProperties | undefined =>
  placement.mode === "anchored"
    ? { top: placement.top, left: placement.left, maxHeight: placement.maxHeight }
    : undefined;

/** A floating dialog beside the anchor, or docked at the bottom of the viewport. Holds the composer while it is open. */
export const CommentPopover = ({ anchorEl, onDismiss, children }: CommentPopoverProps): JSX.Element => {
  const popoverRef = useRef<HTMLDivElement>(null);
  const [placement, setPlacement] = useState<Placement>(DOCKED);

  useLayoutEffect(() => {
    const reposition = (): void => {
      const next = computePlacement(anchorEl, popoverRef.current);
      setPlacement((current) => (samePlacement(current, next) ? current : next));
    };
    reposition();
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [anchorEl]);

  useEffect(() => {
    popoverRef.current?.querySelector("textarea")?.focus();
  }, []);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    if (event.key !== "Escape") {
      return;
    }
    if (anchorEl?.isConnected) {
      anchorEl.focus();
    }
    onDismiss();
  };

  return (
    <div
      ref={popoverRef}
      role="dialog"
      aria-label="New comment"
      className={`${styles.popover} ${placement.mode === "docked" ? styles.docked : styles.anchored}`}
      style={anchoredStyle(placement)}
      onKeyDown={handleKeyDown}
    >
      {children}
    </div>
  );
};
