import type { JSX } from "react";
import type { Entry } from "../../api";
import { useAppSelector } from "../../store";
import { isFailed, latestNote } from "./progress";
import { StepProgress } from "./StepProgress";
import styles from "./RootProgress.module.css";

/** The overall bar of a root workspace, which usually has children, with the latest step note (ADR 0077). */
export const RootProgress = ({ entry }: { readonly entry: Entry }): JSX.Element | null => {
  const note = useAppSelector((s) => latestNote(s.agents.entries, entry));
  const rollup = entry.rollup;
  if (entry.parentId !== null || rollup === null) {
    return null;
  }
  return (
    <div className={styles.root}>
      <StepProgress
        label={`${entry.name} progress`}
        done={rollup.done}
        total={rollup.total}
        caption={`${rollup.done}/${rollup.total} steps`}
        failed={isFailed(entry)}
      />
      {note !== null && <p className={styles.note}>{note}</p>}
    </div>
  );
};
