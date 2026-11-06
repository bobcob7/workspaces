import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useState, type JSX } from "react";
import type { Loop } from "../../api";
import { age, until } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen, selected } from "../ui/uiSlice";
import { gateText } from "./gate";
import { LoopForm } from "./LoopForm";
import { addLoop, creatingCancelled, removeLoop, runLoopNow, setLoopPaused } from "./loopsSlice";
import styles from "./LoopsPane.module.css";

const nextRun = (loop: Loop): string => {
  if (loop.paused || loop.nextRunAt === null) {
    return "paused";
  }
  return `${new Date(loop.nextRunAt).toLocaleString()} (${until(loop.nextRunAt)})`;
};

const LastRun = ({ loop, disabled }: { readonly loop: Loop; readonly disabled: boolean }): JSX.Element => {
  const dispatch = useAppDispatch();
  const run = loop.lastRun;
  if (run === null) {
    return <>never</>;
  }
  const started = age(run.startedAt);
  if (run.error !== null) {
    return (
      <>
        {started} · {run.error.message}
      </>
    );
  }
  if (run.workspace === null) {
    return <>{started}</>;
  }
  const workspace = run.workspace;
  return (
    <>
      {started} ·{" "}
      <button
        type="button"
        className={styles.link}
        disabled={disabled}
        onClick={() => {
          dispatch(sectionChosen("agents"));
          dispatch(selected(workspace));
        }}
      >
        {workspace}
      </button>
    </>
  );
};

/** One loop: its settings and last run, with Pause/Resume, Run now, and Delete. */
const LoopDetail = ({ loop }: { readonly loop: Loop }): JSX.Element => {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((s) => s.loops.busy === loop.name);
  const [confirming, setConfirming] = useState(false);
  return (
    <section className={styles.root} aria-label={loop.name}>
      <header className={styles.header}>
        <h1 className={styles.title}>{loop.name}</h1>
        <span className={styles.spacer} />
        <Button
          size="small"
          disabled={busy}
          onClick={() => void dispatch(setLoopPaused({ name: loop.name, paused: !loop.paused }))}
        >
          {loop.paused ? "Resume" : "Pause"}
        </Button>
        <Button size="small" disabled={busy} onClick={() => void dispatch(runLoopNow(loop.name))}>
          Run now
        </Button>
        <Button size="small" color="error" disabled={busy} onClick={() => setConfirming(true)}>
          Delete
        </Button>
      </header>
      <dl className={styles.rows}>
        <dt>Every</dt>
        <dd>{loop.every}</dd>
        <dt>Prompt</dt>
        <dd className={styles.prompt}>{loop.prompt}</dd>
        <dt>Repos</dt>
        <dd>{loop.specs.length === 0 ? "none" : loop.specs.join(" ")}</dd>
        <dt>Keep</dt>
        <dd>{loop.keep}</dd>
        <dt>Script</dt>
        <dd className={styles.prompt}>{loop.script ?? "none"}</dd>
        {loop.script !== null && (
          <>
            <dt>Script timeout</dt>
            <dd>{loop.scriptTimeout ?? "default"}</dd>
          </>
        )}
        <dt>Variables</dt>
        <dd className={styles.prompt}>
          {Object.keys(loop.vars).length === 0
            ? "none"
            : Object.entries(loop.vars)
                .map(([key, value]) => `${key}=${value}`)
                .join("\n")}
        </dd>
        <dt>Next run</dt>
        <dd>{nextRun(loop)}</dd>
        <dt>Last run</dt>
        <dd>
          <LastRun loop={loop} disabled={busy} />
        </dd>
        {loop.script !== null && (
          <>
            <dt>Last check</dt>
            <dd>{gateText(loop.lastGate)}</dd>
          </>
        )}
      </dl>
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>Delete loop {loop.name}?</DialogTitle>
        <DialogContent>
          <DialogContentText>Its runs' workspaces stay.</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button
            color="error"
            onClick={() => {
              setConfirming(false);
              void dispatch(removeLoop(loop.name));
            }}
          >
            Delete
          </Button>
        </DialogActions>
      </Dialog>
    </section>
  );
};

/** The new-loop form, the selected loop, or what Loops is for when there's none. */
export const LoopsPane = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const creating = useAppSelector((s) => s.loops.creating);
  const formError = useAppSelector((s) => s.loops.formError);
  const submitting = useAppSelector((s) => s.loops.submitting);
  const selectedName = useAppSelector((s) => s.loops.selected);
  const loop = useAppSelector((s) => s.loops.loops.find((l) => l.name === selectedName));
  if (creating) {
    return (
      <LoopForm
        error={formError}
        submitting={submitting}
        onSubmit={(request) => void dispatch(addLoop(request))}
        onCancel={() => dispatch(creatingCancelled())}
      />
    );
  }
  if (loop !== undefined) {
    return <LoopDetail key={loop.name} loop={loop} />;
  }
  return (
    <div className={styles.empty}>
      <p>Select a loop, or add one.</p>
    </div>
  );
};
