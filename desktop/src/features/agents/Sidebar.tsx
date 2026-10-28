import type { JSX } from "react";
import type { Entry } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { hiddenShown } from "./agentsSlice";
import { EntryRow } from "./EntryRow";
import { groupEntries, hiddenEntries } from "./status";
import styles from "./Sidebar.module.css";

interface HiddenGroupProps {
  readonly entries: readonly Entry[];
  readonly selectedKey: string | null;
}

/**
 * Hidden workspaces, last and collapsed until asked for (ADR 0043). It
 * opens on its own while one of its entries is selected, as after a
 * cross-link or hiding the selected entry.
 */
const HiddenGroup = ({ entries, selectedKey }: HiddenGroupProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const showHidden = useAppSelector((s) => s.agents.showHidden);
  const open = showHidden || entries.some((e) => e.key === selectedKey);
  return (
    <section className={styles.group} aria-label="Hidden">
      <h2 className={styles.heading}>
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={open}
          onClick={() => dispatch(hiddenShown(!open))}
        >
          <span className={styles.caret} data-open={open} aria-hidden="true">
            ▸
          </span>
          Hidden
          <span className={styles.count}>{entries.length}</span>
          {!open && <span className={styles.show}>Show</span>}
        </button>
      </h2>
      {open && (
        <ul className={`${styles.list} ${styles.hidden}`}>
          {entries.map((entry) => (
            <li key={entry.key}>
              <EntryRow entry={entry} current={entry.key === selectedKey} onClick={() => dispatch(selected(entry.key))} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
};

export const Sidebar = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const entries = useAppSelector((s) => s.agents.entries);
  const loaded = useAppSelector((s) => s.agents.loaded);
  const selectedKey = useAppSelector((s) => s.ui.selectedKey);
  const groups = groupEntries(entries);
  const hidden = hiddenEntries(entries);
  return (
    <nav className={styles.root} aria-label="Workspaces">
      {loaded && entries.length === 0 && <p className={styles.empty}>No workspaces yet.</p>}
      {groups.map((group) => (
        <section key={group.id} className={styles.group} aria-label={group.title}>
          <h2 className={styles.heading}>
            {group.title}
            <span className={styles.count}>{group.entries.length}</span>
          </h2>
          <ul className={styles.list}>
            {group.entries.map((entry) => (
              <li key={entry.key}>
                <EntryRow entry={entry} current={entry.key === selectedKey} onClick={() => dispatch(selected(entry.key))} />
              </li>
            ))}
          </ul>
        </section>
      ))}
      {hidden.length > 0 && <HiddenGroup entries={hidden} selectedKey={selectedKey} />}
    </nav>
  );
};
