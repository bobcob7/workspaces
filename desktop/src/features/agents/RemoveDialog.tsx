import {
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  FormControlLabel,
} from "@mui/material";
import { useState, type JSX } from "react";
import type { Entry } from "../../api";
import { repoLabel } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { removeEntry } from "./agentsSlice";
import { descendantsOf, statusLabels } from "./status";
import styles from "./RemoveDialog.module.css";

const removeWarning = (entry: Entry): string =>
  entry.workspaceId === null
    ? `This deletes the Claude Code session ${entry.sessionId ?? ""}. Its transcript stays available through claude --resume.`
    : `This stops the agent and deletes ${entry.path}, including uncommitted changes, unpushed commits, and stashes.`;

const plural = (n: number, word: string): string => `${n} ${word}${n === 1 ? "" : "s"}`;

const describe = (entry: Entry): string => {
  const repos = entry.repos.map((r) => repoLabel(r.id, r.ref)).join(", ");
  const kind = entry.review !== null ? "review · its findings are kept" : statusLabels[entry.status].toLowerCase();
  return repos === "" ? kind : `${repos} · ${kind}`;
};

export interface RemoveDialogProps {
  readonly entry: Entry;
  readonly open: boolean;
  readonly onClose: () => void;
}

/**
 * Confirms removing an entry. A workspace that started others lists them,
 * since ws rm removes them first, and offers to keep them (ADR 0044).
 */
export const RemoveDialog = ({ entry, open, onClose }: RemoveDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const descendants = useAppSelector((s) => descendantsOf(s.agents.entries, entry));
  const [keepChildren, setKeepChildren] = useState(false);
  const removing = keepChildren ? 1 : descendants.length + 1;
  const title =
    descendants.length === 0
      ? `Remove ${entry.name}?`
      : `Remove ${entry.name} and ${plural(descendants.length, "workspace")} it started?`;
  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>{title}</DialogTitle>
      <DialogContent>
        {descendants.length === 0 ? (
          <DialogContentText>{removeWarning(entry)}</DialogContentText>
        ) : (
          <>
            <DialogContentText>
              Their agents are stopped and deleted, and their workspaces removed with anything not pushed:
            </DialogContentText>
            <ul className={styles.tree} aria-label="Removed with it">
              <li>
                <b>{entry.name}</b> <span className={styles.muted}>{describe(entry)}</span>
              </li>
              {descendants.map(({ entry: d, depth }) => (
                <li key={d.key} style={{ paddingLeft: `${depth * 14}px` }} className={keepChildren ? styles.kept : undefined}>
                  ↳ <b>{d.name}</b> <span className={styles.muted}>{describe(d)}</span>
                </li>
              ))}
            </ul>
            <FormControlLabel
              control={<Checkbox checked={keepChildren} onChange={(e) => setKeepChildren(e.target.checked)} />}
              label="Keep the workspaces it started; they become top-level"
            />
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          color="error"
          variant="contained"
          onClick={() => {
            onClose();
            void dispatch(removeEntry({ ...entry, keepChildren }));
          }}
        >
          {descendants.length === 0 ? "Remove" : `Remove ${plural(removing, "workspace")}`}
        </Button>
      </DialogActions>
    </Dialog>
  );
};
