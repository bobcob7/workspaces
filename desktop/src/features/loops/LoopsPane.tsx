import { Button, Chip, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useId, useState, type JSX, type ReactNode } from "react";
import type { Loop } from "../../api";
import { age, until } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen, selected } from "../ui/uiSlice";
import { gateText } from "./gate";
import { LoopForm } from "./LoopForm";
import {
  addLoop,
  creatingCancelled,
  editLoop,
  editingCancelled,
  editingStarted,
  removeLoop,
  runLoopNow,
  setLoopPaused,
} from "./loopsSlice";
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

/** A titled card in the detail view; its heading labels the region. */
const Card = ({ title, children }: { readonly title: string; readonly children: ReactNode }): JSX.Element => {
  const id = useId();
  return (
    <section className={styles.card} aria-labelledby={id}>
      <h2 id={id} className={styles.cardTitle}>
        {title}
      </h2>
      {children}
    </section>
  );
};

/** A labelled, monospace, scrollable block of text. */
const CodeBlock = ({ label, text }: { readonly label: string; readonly text: string }): JSX.Element => (
  <pre className={styles.code} aria-label={label} tabIndex={0}>
    {text}
  </pre>
);

const ScheduleCard = ({ loop, busy }: { readonly loop: Loop; readonly busy: boolean }): JSX.Element => (
  <Card title="Schedule">
    <dl className={styles.rows}>
      <dt>Every</dt>
      <dd>{loop.every}</dd>
      <dt>Next run</dt>
      <dd>{nextRun(loop)}</dd>
      <dt>Last run</dt>
      <dd>
        <LastRun loop={loop} disabled={busy} />
      </dd>
      <dt>Keep</dt>
      <dd>{`${loop.keep} runs' workspaces`}</dd>
    </dl>
  </Card>
);

const PromptCard = ({ loop }: { readonly loop: Loop }): JSX.Element => (
  <Card title="Prompt">
    <CodeBlock label="Prompt" text={loop.prompt} />
  </Card>
);

const ReposCard = ({ loop }: { readonly loop: Loop }): JSX.Element => (
  <Card title="Repos">
    {loop.specs.length === 0 ? (
      <p className={styles.muted}>No repos: each run gets an empty workspace</p>
    ) : (
      <div className={styles.chips}>
        {loop.specs.map((spec) => (
          <Chip key={spec} size="small" label={spec} />
        ))}
      </div>
    )}
  </Card>
);

const AgentCard = ({ loop }: { readonly loop: Loop }): JSX.Element => (
  <Card title="Agent">
    <dl className={styles.rows}>
      <dt>Model</dt>
      <dd>{loop.model ?? "Default"}</dd>
      <dt>Permission mode</dt>
      <dd>{loop.permissionMode ?? "Default"}</dd>
    </dl>
  </Card>
);

const GateCard = ({ loop }: { readonly loop: Loop }): JSX.Element => (
  <Card title="Gate script">
    {loop.script === null ? (
      <p className={styles.muted}>No gate script: every due run starts.</p>
    ) : (
      <>
        <CodeBlock label="Gate script" text={loop.script} />
        <dl className={styles.rows}>
          <dt>Timeout</dt>
          <dd>{loop.scriptTimeout ?? "1m"}</dd>
          <dt>Last check</dt>
          <dd>{gateText(loop.lastGate)}</dd>
        </dl>
      </>
    )}
  </Card>
);

const VarsCard = ({ loop }: { readonly loop: Loop }): JSX.Element => {
  const entries = Object.entries(loop.vars).sort(([a], [b]) => a.localeCompare(b));
  return (
    <Card title="Variables">
      {entries.length === 0 ? (
        <p className={styles.muted}>No variables</p>
      ) : (
        <table className={styles.varsTable}>
          <thead>
            <tr>
              <th scope="col">Key</th>
              <th scope="col">Value</th>
            </tr>
          </thead>
          <tbody>
            {entries.map(([key, value]) => (
              <tr key={key}>
                <td className={styles.mono}>{key}</td>
                <td className={styles.mono}>{value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
};

/** One loop: its header actions and a card per setting group, with Pause/Resume, Run now, and Delete. */
const LoopDetail = ({ loop }: { readonly loop: Loop }): JSX.Element => {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((s) => s.loops.busy === loop.name);
  const [confirming, setConfirming] = useState(false);
  return (
    <section className={styles.root} aria-label={loop.name}>
      <header className={styles.header}>
        <h1 className={styles.title}>{loop.name}</h1>
        <Chip size="small" label={loop.paused ? "Paused" : "Active"} />
        <span className={styles.spacer} />
        <Button size="small" disabled={busy} onClick={() => dispatch(editingStarted())}>
          Edit
        </Button>
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
      <div className={styles.body}>
        <ScheduleCard loop={loop} busy={busy} />
        <PromptCard loop={loop} />
        <ReposCard loop={loop} />
        <AgentCard loop={loop} />
        <GateCard loop={loop} />
        <VarsCard loop={loop} />
      </div>
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

/** The new-loop form, the loop's edit form, the selected loop, or what Loops is for when there's none. */
export const LoopsPane = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const creating = useAppSelector((s) => s.loops.creating);
  const editing = useAppSelector((s) => s.loops.editing);
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
  if (loop !== undefined && editing) {
    return (
      <LoopForm
        key={loop.name}
        loop={loop}
        error={formError}
        submitting={submitting}
        onSubmit={(request) => void dispatch(editLoop(request))}
        onCancel={() => dispatch(editingCancelled())}
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
