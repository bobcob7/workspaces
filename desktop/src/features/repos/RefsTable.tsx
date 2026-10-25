import { Button, Chip } from "@mui/material";
import type { JSX } from "react";
import type { Entry, RefKind, RepoRef } from "../../api";
import { age } from "../../format";
import { useAppDispatch } from "../../store";
import { dialogOpened } from "../ui/uiSlice";
import { specFor, workspacesOn } from "./repos";
import { openInAgents } from "./reposSlice";
import styles from "./RefsTable.module.css";

export interface RefsTableProps {
  readonly repoId: string;
  readonly kind: RefKind;
  readonly refs: readonly RepoRef[];
  readonly defaultBranch: string;
  readonly entries: readonly Entry[];
}

/**
 * A repo's branches or tags. Each row lists the workspaces on it; hovering
 * a row puts New agent and New shell for repo@ref in their place.
 */
export const RefsTable = ({ repoId, kind, refs, defaultBranch, entries }: RefsTableProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const branches = kind === "branch";
  return (
    <table className={styles.table} aria-label={branches ? "Branches" : "Tags"}>
      <thead>
        <tr>
          <th className={styles.name}>{branches ? "Branch" : "Tag"}</th>
          <th className={styles.subject}>Last commit</th>
          {branches && <th className={styles.author}>Author</th>}
          <th className={styles.updated}>Updated</th>
          <th className={styles.workspaces}>Workspaces</th>
        </tr>
      </thead>
      <tbody>
        {refs.map((ref) => {
          const spec = specFor(repoId, ref.name);
          const on = workspacesOn(entries, repoId, ref);
          return (
            <tr key={`${ref.kind}:${ref.name}`} className={styles.row}>
              <td className={`${styles.name} ${styles.mono}`} title={ref.name}>
                {ref.name}
                {branches && ref.name === defaultBranch && <span className={styles.defaultChip}>default</span>}
              </td>
              <td className={styles.subject} title={`${ref.commit.slice(0, 7)} ${ref.subject}`}>
                {ref.subject}
              </td>
              {branches && (
                <td className={`${styles.author} ${styles.muted}`} title={ref.author}>
                  {ref.author}
                </td>
              )}
              <td className={`${styles.updated} ${styles.muted}`} title={ref.date ?? undefined}>
                {age(ref.date)}
              </td>
              <td className={styles.workspaces}>
                <span className={styles.chips}>
                  {on.map((entry) => (
                    <Chip
                      key={entry.key}
                      size="small"
                      label={entry.name}
                      clickable
                      className={styles.chip}
                      onClick={() => void dispatch(openInAgents(entry.key))}
                    />
                  ))}
                </span>
                <span className={styles.actions}>
                  <Button size="small" onClick={() => dispatch(dialogOpened("agent", [spec]))} aria-label={`New agent on ${ref.name}`}>
                    New agent
                  </Button>
                  <Button size="small" onClick={() => dispatch(dialogOpened("shell", [spec]))} aria-label={`New shell on ${ref.name}`}>
                    New shell
                  </Button>
                </span>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
};
