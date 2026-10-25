import { useEffect, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { needsAttention } from "../agents/status";
import { sectionChosen, type Section } from "../ui/uiSlice";
import styles from "./Rail.module.css";

const icon = (paths: JSX.Element): JSX.Element => (
  <svg
    className={styles.icon}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    {paths}
  </svg>
);

const icons: Readonly<Record<Section, JSX.Element>> = {
  agents: icon(
    <>
      <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12Z" />
      <path d="M8.5 12h.01M12 12h.01M15.5 12h.01" />
    </>,
  ),
  repos: icon(
    <>
      <circle cx="6" cy="5" r="2.2" />
      <circle cx="6" cy="19" r="2.2" />
      <circle cx="18" cy="8" r="2.2" />
      <path d="M6 7.2v9.6" />
      <path d="M18 10.2c0 4-6 3.5-11.2 7" />
    </>,
  ),
  reviews: icon(
    <>
      <circle cx="6" cy="5" r="2.2" />
      <circle cx="6" cy="19" r="2.2" />
      <circle cx="18" cy="19" r="2.2" />
      <path d="M6 7.2v9.6" />
      <path d="M18 16.8V9a3 3 0 0 0-3-3h-4" />
      <path d="M13 3.5 10.5 6 13 8.5" />
    </>,
  ),
  settings: icon(
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z" />
    </>,
  ),
};

const labels: Readonly<Record<Section, string>> = {
  agents: "Agents",
  repos: "Repos",
  reviews: "Reviews",
  settings: "Settings",
};

/** Every section in rail order; ⌘1–⌘4 choose them by this position. */
export const railOrder: readonly Section[] = ["agents", "repos", "reviews", "settings"];

/** The sections built so far. A section joins the rail when its PR lands. */
export const shownSections: readonly Section[] = ["agents", "repos"];

/** Settings sits apart, at the bottom of the rail. */
const bottomSections: ReadonlySet<Section> = new Set(["settings"]);

/** The section ⌘<key> chooses, if that key is ⌘1–⌘4 and its section is shown. */
export const shortcutSection = (event: Pick<KeyboardEvent, "key" | "metaKey" | "altKey" | "shiftKey" | "ctrlKey">): Section | null => {
  if (!event.metaKey || event.altKey || event.shiftKey || event.ctrlKey) {
    return null;
  }
  const section = railOrder[Number(event.key) - 1];
  return section !== undefined && shownSections.includes(section) ? section : null;
};

const useBadges = (): Readonly<Partial<Record<Section, number>>> => {
  const attention = useAppSelector((s) => s.agents.entries.filter(needsAttention).length);
  return { agents: attention };
};

export const Rail = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const current = useAppSelector((s) => s.ui.section);
  const badges = useBadges();
  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      const section = shortcutSection(event);
      if (section !== null) {
        event.preventDefault();
        dispatch(sectionChosen(section));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dispatch]);
  const item = (section: Section): JSX.Element => {
    const badge = badges[section] ?? 0;
    return (
      <button
        key={section}
        type="button"
        className={styles.item}
        aria-current={section === current ? "page" : undefined}
        aria-label={badge > 0 ? `${labels[section]}, ${badge}` : labels[section]}
        title={`${labels[section]} (⌘${railOrder.indexOf(section) + 1})`}
        onClick={() => dispatch(sectionChosen(section))}
      >
        {icons[section]}
        <span>{labels[section]}</span>
        {badge > 0 && (
          <span className={styles.badge} data-section={section}>
            {badge}
          </span>
        )}
      </button>
    );
  };
  const shown = railOrder.filter((s) => shownSections.includes(s));
  return (
    <nav className={styles.root} aria-label="Sections">
      {shown.filter((s) => !bottomSections.has(s)).map(item)}
      <span className={styles.spacer} />
      {shown.filter((s) => bottomSections.has(s)).map(item)}
    </nav>
  );
};
