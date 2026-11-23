import { useEffect, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { sidebarToggled, writeSidebarCollapsed } from "./uiSlice";
import styles from "./SidebarToggle.module.css";

/** Elements where B is text or terminal input, so Ctrl/⌘+B must not toggle the sidebar. */
const typingSelector = "input, textarea, select, [contenteditable='true'], .xterm";

export const isSidebarShortcut = (event: Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">): boolean =>
  (event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "b";

const isTyping = (target: EventTarget | null): boolean => target instanceof Element && target.closest(typingSelector) !== null;

/** Hides or shows the section sidebar on wide screens; the button is hidden on phone widths. */
export const SidebarToggle = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const collapsed = useAppSelector((s) => s.ui.sidebarCollapsed);
  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (!isSidebarShortcut(event) || isTyping(event.target)) {
        return;
      }
      event.preventDefault();
      dispatch(sidebarToggled());
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dispatch]);
  useEffect(() => {
    writeSidebarCollapsed(collapsed);
  }, [collapsed]);
  const label = collapsed ? "Expand sidebar" : "Collapse sidebar";
  return (
    <button
      type="button"
      className={styles.toggle}
      aria-label={label}
      aria-expanded={!collapsed}
      title={`${label} (⌘B)`}
      onClick={() => dispatch(sidebarToggled())}
    >
      <svg className={styles.icon} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <rect x="3" y="4" width="18" height="16" rx="2.5" />
        <path d="M9 4v16" />
      </svg>
    </button>
  );
};
