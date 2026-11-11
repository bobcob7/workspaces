import { Button, Chip, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { Loop, LoopHistoryRun } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector, useAppStore } from "../../store";
import { selected, sectionChosen } from "../ui/uiSlice";
import { Card } from "./Card";
import { formatDuration, runSummary, statusColor, statusLabel } from "./history";
import { clearLoopHistory, fetchLoopHistory, historyPageSize, runClosed } from "./loopDataSlice";
import { loopLabel } from "./formValues";
import { RunDialog } from "./RunDialog";
import styles from "./HistoryCard.module.css";

const WorkspaceLink = ({ run, disabled }: { readonly run: LoopHistoryRun; readonly disabled: boolean }): JSX.Element | null => {
  const dispatch = useAppDispatch();
  if (run.workspace === null) {
    return null;
  }
  const workspace = run.workspace;
  if (!run.workspaceExists) {
    return <span className={styles.muted}>{workspace} (removed)</span>;
  }
  return (
    <button
      type="button"
      className={styles.link}
      disabled={disabled}
      onClick={() => {
        dispatch(sectionChosen("agents"));
        dispatch(selected(workspace));
      }}
    >
      Open {workspace}
    </button>
  );
};

const RunRow = ({ run, disabled, onOpen }: { readonly run: LoopHistoryRun; readonly disabled: boolean; readonly onOpen: () => void }): JSX.Element => (
  <li className={styles.run}>
    <button type="button" className={styles.runButton} onClick={onOpen}>
      <span className={styles.runTitle}>Run {run.seq}</span>
      <Chip component="span" size="small" label={statusLabel(run)} color={statusColor(run.status)} />
      <span className={styles.muted}>
        {new Date(run.startedAt).toLocaleString()} · {age(run.startedAt)}
      </span>
      <span>{formatDuration(run.durationSeconds)}</span>
      <span className={styles.summary}>{runSummary(run)}</span>
    </button>
    <WorkspaceLink run={run} disabled={disabled} />
  </li>
);

/** The loop's recent runs, newest first, with a dialog per run and the history's own actions. */
export const HistoryCard = ({ loop }: { readonly loop: Loop }): JSX.Element => {
  const dispatch = useAppDispatch();
  const store = useAppStore();
  const data = useAppSelector((s) => s.loopData);
  const busy = useAppSelector((s) => s.loops.busy === loop.name);
  const [openSeq, setOpenSeq] = useState<number | null>(null);
  const [confirming, setConfirming] = useState(false);
  const lastStarted = loop.lastRun?.startedAt ?? null;
  const held = data.loop === loop.name;
  const history = held ? data.history : null;
  const error = held ? data.historyError : null;

  useEffect(() => {
    void dispatch(fetchLoopHistory({ name: loop.name, limit: store.getState().loopData.historyLimit }));
  }, [dispatch, store, loop.name, lastStarted]);

  const refresh = (): void => {
    void dispatch(fetchLoopHistory({ name: loop.name, limit: data.historyLimit }));
  };

  return (
    <Card title="History">
      <div className={styles.content}>
        <div className={styles.toolbar}>
          <Button size="small" onClick={refresh}>
            Refresh
          </Button>
          <Button size="small" color="error" disabled={busy} onClick={() => setConfirming(true)}>
            Clear history
          </Button>
        </div>
        {error !== null && (
          <div className={styles.error}>
            <p role="alert" className={styles.muted}>
              {error.message}
            </p>
            <Button size="small" onClick={refresh}>
              Retry
            </Button>
          </div>
        )}
        {history !== null && history.runs.length === 0 && <p className={styles.muted}>No runs yet.</p>}
        {history !== null && history.runs.length > 0 && (
          <ul aria-label="Run history" className={styles.runs}>
            {history.runs.map((run) => (
              <RunRow key={run.seq} run={run} disabled={busy} onOpen={() => setOpenSeq(run.seq)} />
            ))}
          </ul>
        )}
        {history !== null && history.runs.length < history.total && (
          <Button
            size="small"
            onClick={() => void dispatch(fetchLoopHistory({ name: loop.name, limit: data.historyLimit + historyPageSize }))}
          >
            Show more
          </Button>
        )}
      </div>
      {openSeq !== null && (
        <RunDialog
          name={loop.name}
          seq={openSeq}
          onClose={() => {
            dispatch(runClosed());
            setOpenSeq(null);
          }}
        />
      )}
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>Clear the history of {loopLabel(loop)}?</DialogTitle>
        <DialogContent>
          <DialogContentText>Saved reports are deleted too.</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button
            color="error"
            onClick={() => {
              setConfirming(false);
              void dispatch(clearLoopHistory(loop.name));
            }}
          >
            Clear history
          </Button>
        </DialogActions>
      </Dialog>
    </Card>
  );
};
