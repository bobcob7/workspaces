import { Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import { useEffect, type JSX } from "react";
import type { LoopHistoryRun } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { MarkdownText } from "../conversation/ConversationView";
import { formatDuration, statusLabel } from "./history";
import { fetchLoopRun } from "./loopDataSlice";
import { gateText } from "./gate";
import styles from "./HistoryCard.module.css";

const dash = "–";

const RunDetails = ({ run }: { readonly run: LoopHistoryRun }): JSX.Element => (
  <dl className={styles.details}>
    <dt>Trigger</dt>
    <dd>{run.trigger}</dd>
    <dt>Started</dt>
    <dd>{new Date(run.startedAt).toLocaleString()}</dd>
    <dt>Finished</dt>
    <dd>{run.finishedAt === null ? dash : new Date(run.finishedAt).toLocaleString()}</dd>
    <dt>Duration</dt>
    <dd>{formatDuration(run.durationSeconds)}</dd>
    <dt>Workspace</dt>
    <dd>{run.workspace ?? dash}</dd>
    <dt>Session</dt>
    <dd>{run.session ?? dash}</dd>
    <dt>Gate</dt>
    <dd>{gateText(run.gate)}</dd>
    <dt>Error</dt>
    <dd>{run.error === null ? dash : `${run.error.code}: ${run.error.message}`}</dd>
    <dt>Verdict</dt>
    <dd>{run.verdict === null ? dash : `${run.verdict.verdict} (${run.verdict.outcome})`}</dd>
  </dl>
);

/** One run's details and report, fetched when the dialog opens. */
export const RunDialog = ({ name, seq, onClose }: { readonly name: string; readonly seq: number; readonly onClose: () => void }): JSX.Element => {
  const dispatch = useAppDispatch();
  const detail = useAppSelector((s) => s.loopData.run);
  const loaded = detail !== null && detail.loop === name && detail.run.seq === seq ? detail : null;
  const title = loaded === null ? `Run ${seq}` : `Run ${seq} · ${statusLabel(loaded.run)}`;

  useEffect(() => {
    void dispatch(fetchLoopRun({ name, seq }));
  }, [dispatch, name, seq]);

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md" className={styles.dialog}>
      <DialogTitle>{title}</DialogTitle>
      <DialogContent>
        {loaded !== null && <RunDetails run={loaded.run} />}
        <h3 className={styles.reportTitle}>Report</h3>
        {loaded === null && <p className={styles.muted}>Loading…</p>}
        {loaded !== null && loaded.reportText === null && <p className={styles.muted}>No report was recorded.</p>}
        {loaded !== null && loaded.reportText !== null && (
          <div className={styles.report} tabIndex={0}>
            <MarkdownText text={loaded.reportText} />
          </div>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
};
