import type { JSX } from "react";
import type { Entry } from "../../api";
import { age, repoLabel } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { groupEntries } from "./status";
import { StatusDot } from "./StatusDot";
import styles from "./Sidebar.module.css";

const summary = (entry: Entry): string => {
  if (entry.detail !== null) {
    return entry.detail;
  }
  if (entry.intent !== null) {
    return entry.intent;
  }
  if (entry.repos.length > 0) {
    return entry.repos.map((r) => repoLabel(r.id, r.ref)).join(", ");
  }
  return entry.kind === "agent" && entry.workspaceId === null ? "Not in a workspace" : "Empty workspace";
};

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
                <button
                  type="button"
                  className={styles.item}
                  aria-current={entry.key === selectedKey ? "true" : undefined}
                  onClick={() => dispatch(selected(entry.key))}
                >
                  <span className={styles.row}>
                    <StatusDot status={entry.status} />
                    <span className={styles.name}>{entry.name}</span>
                    <span className={styles.age}>{age(entry.updatedAt ?? entry.createdAt)}</span>
                  </span>
                  <span className={styles.summary}>{summary(entry)}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </nav>
  );
};
