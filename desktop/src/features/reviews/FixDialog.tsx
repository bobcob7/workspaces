import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, Radio, TextField } from "@mui/material";
import { useState, type JSX, type ReactNode } from "react";
import type { FixPlan, FixTarget } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { StatusDot } from "../agents/StatusDot";
import { fixDialogClosed, sendFix } from "./actionsSlice";
import styles from "./ActionDialogs.module.css";

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

interface TargetProps {
  readonly value: FixTarget;
  readonly chosen: boolean;
  readonly disabled: boolean;
  readonly label: string;
  readonly onChoose: (target: FixTarget) => void;
  readonly children: ReactNode;
}

const TargetOption = ({ value, chosen, disabled, label, onChoose, children }: TargetProps): JSX.Element => (
  <label className={styles.target} data-chosen={chosen} aria-disabled={disabled}>
    <Radio
      size="small"
      checked={chosen}
      disabled={disabled}
      onChange={() => onChoose(value)}
      slotProps={{ input: { "aria-label": label } }}
    />
    <span className={styles.targetText}>{children}</span>
  </label>
);

const messageFor = (plan: FixPlan, target: FixTarget): string =>
  target === "requester" ? plan.requesterMessage : plan.agentMessage;

const Form = ({ plan }: { readonly plan: FixPlan }): JSX.Element => {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((s) => s.actions.fix);
  const [target, setTarget] = useState<FixTarget>(plan.target);
  const [message, setMessage] = useState(messageFor(plan, plan.target));
  const sending = dialog?.sending ?? false;
  const requester = plan.requester;
  const choose = (next: FixTarget): void => {
    // An edited message stays; an untouched one follows the target.
    if (message === messageFor(plan, target)) {
      setMessage(messageFor(plan, next));
    }
    setTarget(next);
  };
  const send = (): void => {
    if (dialog !== null) {
      void dispatch(sendFix({ prKey: dialog.prKey, repo: dialog.repo, ref: dialog.ref, plan, target, message }));
    }
  };
  const sendLabel = target === "requester" && requester !== null ? `Send to ${requester.workspaceId}` : "Start fix agent";
  return (
    <>
      <DialogContent className={styles.content}>
        {requester !== null && (
          <TargetOption
            value="requester"
            chosen={target === "requester"}
            disabled={!requester.available}
            label={`Send to ${requester.workspaceId}`}
            onChoose={choose}
          >
            <span className={styles.targetName}>
              Send to {requester.workspaceId}
              {requester.status !== null && <StatusDot status={requester.status} withLabel />}
              {requester.reason !== null && <span className={styles.muted}>{requester.reason}</span>}
            </span>
            <span className={styles.muted}>
              The agent that requested this review. It reads your decisions with{" "}
              <code className={styles.mono}>ws review report --decided fix</code>.
            </span>
          </TargetOption>
        )}
        <TargetOption value="agent" chosen={target === "agent"} disabled={false} label="Start a fix agent" onChoose={choose}>
          <span className={styles.targetName}>Start a fix agent</span>
          <span className={styles.command}>{plan.agent.command}</span>
          {plan.agent.branch !== null && (
            <span className={styles.muted}>
              The pull request comes from a fork, so the agent starts at the reviewed commit and pushes{" "}
              <code className={styles.mono}>{plan.agent.branch}</code> to origin.
            </span>
          )}
        </TargetOption>
        <TextField
          label="Message"
          multiline
          minRows={6}
          maxRows={16}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          slotProps={{ htmlInput: { className: styles.mono } }}
        />
        {dialog?.error != null && <Alert severity="error">{dialog.error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={() => dispatch(fixDialogClosed())} disabled={sending}>
          Cancel
        </Button>
        <Button
          variant="contained"
          onClick={send}
          disabled={sending || message.trim() === "" || (target === "requester" && requester?.available !== true)}
          startIcon={sending ? <CircularProgress size={14} color="inherit" /> : undefined}
        >
          {sendLabel}
        </Button>
      </DialogActions>
    </>
  );
};

/** Fix N…: whom to send the findings decided fix to, and the short message (report.md). */
export const FixDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((s) => s.actions.fix);
  const count = dialog?.plan?.findings.length;
  const close = (): void => {
    if (dialog?.sending !== true) {
      dispatch(fixDialogClosed());
    }
  };
  return (
    <Dialog open={dialog !== null} onClose={close} fullWidth maxWidth="md">
      <DialogTitle>{count === undefined ? "Fix findings" : `Fix ${plural(count, "finding")}`}</DialogTitle>
      {dialog !== null && dialog.plan !== null ? (
        <Form key={`${dialog.prKey}@${dialog.ref}`} plan={dialog.plan} />
      ) : (
        <DialogContent className={styles.loading}>
          {dialog?.error != null ? (
            <Alert severity="error">{dialog.error.message}</Alert>
          ) : (
            <CircularProgress size={20} aria-label="Reading the review" />
          )}
        </DialogContent>
      )}
    </Dialog>
  );
};
