import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  TextField,
} from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import type { ReviewPlan } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { models, permissionModes } from "../create/NewAgentDialog";
import { ownerRepo, shortSha } from "./groups";
import { startDialogClosed, startReview } from "./reviewsSlice";
import styles from "./StartReviewDialog.module.css";

const PlanForm = ({ plan, prKey }: { readonly plan: ReviewPlan; readonly prKey: string }): JSX.Element => {
  const dispatch = useAppDispatch();
  const pr = useAppSelector((s) => s.reviews.view?.prs.find((p) => p.key === prKey));
  const error = useAppSelector((s) => s.reviews.startDialog?.error ?? null);
  const starting = useAppSelector((s) => s.reviews.busy.includes(prKey));
  const [name, setName] = useState(plan.name);
  const [focus, setFocus] = useState(plan.focus);
  const [model, setModel] = useState(plan.model);
  const [permissionMode, setPermissionMode] = useState(plan.permissionMode);
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (pr !== undefined) {
      void dispatch(startReview({ pr, plan: { ...plan, name, focus, model, permissionMode } }));
    }
  };
  return (
    <form onSubmit={submit}>
      <DialogContent className={styles.content}>
        <TextField
          label="Workspace name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          slotProps={{ htmlInput: { className: styles.mono } }}
          helperText={
            name === plan.name
              ? "Named after the pull request and its head commit."
              : `With another name, the app won't connect the agent to this pull request; it shows only in Agents.`
          }
        />
        <div className={styles.repo}>
          <span className={styles.repoLabel}>Repo</span>
          <span className={styles.repoValue}>
            <Chip
              size="small"
              label={`${plan.repo}@${shortSha(plan.head)}`}
              title={`${plan.repo}@${plan.head}`}
              className={styles.mono}
            />
            <span className={styles.muted}>PR head, detached</span>
          </span>
        </div>
        <TextField
          label="Focus"
          placeholder="Optional: what this review should look at hardest"
          helperText="Added to every reviewer's prompt. The reviewers and their models come from Settings."
          value={focus}
          onChange={(e) => setFocus(e.target.value)}
          multiline
          minRows={3}
          maxRows={10}
          slotProps={{ inputLabel: { shrink: true } }}
        />
        <div className={styles.row}>
          <TextField
            select
            label="Review agent model"
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            value={model}
            onChange={(e) => setModel(e.target.value)}
            fullWidth
          >
            {models.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "Default" : m}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            label="Permission mode"
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            value={permissionMode}
            onChange={(e) => setPermissionMode(e.target.value)}
            fullWidth
          >
            {permissionModes.map((m) => (
              <MenuItem key={m} value={m}>
                {m === "" ? "default" : m}
              </MenuItem>
            ))}
          </TextField>
        </div>
        {error !== null && <Alert severity="error">{error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={() => dispatch(startDialogClosed())} disabled={starting}>
          Cancel
        </Button>
        <Button type="submit" variant="contained" disabled={starting || pr === undefined || name.trim() === ""}>
          {starting ? "Starting…" : "Start review"}
        </Button>
      </DialogActions>
    </form>
  );
};

/** Edit before starting: the review's name, focus, and the review agent's model and permission mode. */
export const StartReviewDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((s) => s.reviews.startDialog);
  const pr = useAppSelector((s) => s.reviews.view?.prs.find((p) => p.key === dialog?.key));
  const title = pr === undefined ? "Review" : `Review ${ownerRepo(pr.repo)}#${pr.number}`;
  return (
    <Dialog open={dialog !== null} onClose={() => dispatch(startDialogClosed())} fullWidth maxWidth="sm">
      <DialogTitle>{title}</DialogTitle>
      {dialog !== null && dialog.plan !== null ? (
        <PlanForm key={dialog.key} plan={dialog.plan} prKey={dialog.key} />
      ) : (
        <DialogContent className={styles.loading}>
          {dialog !== null && dialog.error !== null ? (
            <Alert severity="error">{dialog.error.message}</Alert>
          ) : (
            <CircularProgress size={20} />
          )}
        </DialogContent>
      )}
    </Dialog>
  );
};
