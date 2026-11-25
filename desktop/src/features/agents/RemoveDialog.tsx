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
  const swept = (d: Entry, depth: number): boolean => depth === 1 && d.parentId === null && d.scopeParent !== null;
  const removing = keepChildren ? 1 + descendants.filter(({ entry: d, depth }) => swept(d, depth)).length : descendants.length + 1;
  const otherEnvironments = [
    ...new Set(descendants.map((d) => d.entry.environment).filter((env) => env !== entry.environment)),
  ].sort();
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
                <li key={d.key} style={{ paddingLeft: `${depth * 14}px` }} className={keepChildren && !swept(d, depth) ? styles.kept : undefined}>
                  ↳ <b>{d.name}</b> <span className={styles.muted}>{describe(d)}</span>
                </li>
              ))}
            </ul>
            <FormControlLabel
              control={<Checkbox checked={keepChildren} onChange={(e) => setKeepChildren(e.target.checked)} />}
              label="Keep the workspaces it started; they become top-level"
            />
            {otherEnvironments.length > 0 && (
              <DialogContentText>
                {`Workspaces on other environments (${otherEnvironments.join(", ")}) are removed by the controller about a minute after this one, once their environment is online, even if you keep the others.`}
              </DialogContentText>
            )}
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
