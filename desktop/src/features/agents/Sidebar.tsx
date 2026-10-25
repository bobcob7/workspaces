import type { JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { EntryRow } from "./EntryRow";
import { groupEntries } from "./status";
import styles from "./Sidebar.module.css";

export const Sidebar = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const entries = useAppSelector((s) => s.agents.entries);
  const loaded = useAppSelector((s) => s.agents.loaded);
  const selectedKey = useAppSelector((s) => s.ui.selectedKey);
  const groups = groupEntries(entries);
  return (
    <nav className={styles.root} aria-label="Workspaces">
      {loaded && groups.length === 0 && <p className={styles.empty}>No workspaces yet.</p>}
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
    </nav>
  );
};
