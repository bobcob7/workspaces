import {
  Alert,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  MenuItem,
  TextField,
} from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import type { AppError } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { createAgent, createErrorCleared } from "../agents/agentsSlice";
import { dialogClosed } from "../ui/uiSlice";
import { RepoSpecsField } from "./RepoSpecsField";
import { usePrefilledSpecs } from "./usePrefilledSpecs";
import styles from "./NewDialog.module.css";

export const models = ["", "opus", "sonnet", "haiku", "fable"] as const;
export const permissionModes = ["", "manual", "acceptEdits", "auto", "plan", "dontAsk", "bypassPermissions"] as const;

const errorHint = (error: AppError): string | null => {
  if (error.code !== "claude_untrusted") {
    return null;
  }
  const directory = typeof error.details?.["directory"] === "string" ? error.details["directory"] : null;
  return directory === null
    ? "Trust the workspace root once in Claude Code, then try again."
    : `Trust it once: cd "${directory}" && claude, accept the prompt, and exit.`;
};

export const NewAgentDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const open = useAppSelector((s) => s.ui.dialog === "agent");
  const creating = useAppSelector((s) => s.agents.creating);
  const error = useAppSelector((s) => s.agents.createError);
  const [name, setName] = useState("");
  const [specs, setSpecs] = useState<readonly string[]>([]);
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("");
  const [permissionMode, setPermissionMode] = useState("");
  const [offline, setOffline] = useState(false);
  usePrefilledSpecs(open, setSpecs);
  const close = (): void => {
    dispatch(dialogClosed());
    dispatch(createErrorCleared());
  };
  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const result = await dispatch(createAgent({ name, specs, prompt, model, permissionMode, offline }));
    if (createAgent.fulfilled.match(result)) {
      setName("");
      setSpecs([]);
      setPrompt("");
    }
  };
  const hint = error === null ? null : errorHint(error);
  return (
    <Dialog open={open} onClose={creating ? undefined : close} fullWidth maxWidth="sm">
      <form onSubmit={(e) => void submit(e)}>
        <DialogTitle>New agent</DialogTitle>
        <DialogContent className={styles.content}>
          <TextField
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Fix login"
            helperText="Shown in the list, and can be changed later; the workspace id is made from it. Leave empty for a generated id."
            autoFocus
          />
          <RepoSpecsField value={specs} onChange={setSpecs} />
          <TextField
            label="Task"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            multiline
            minRows={4}
            placeholder="What should the agent do? Leave empty to start it waiting for you."
          />
          <div className={styles.row}>
            <TextField select label="Model"
              slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }} value={model} onChange={(e) => setModel(e.target.value)} fullWidth>
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
                  {m === "" ? "Default" : m}
                </MenuItem>
              ))}
            </TextField>
          </div>
          <FormControlLabel
            control={<Checkbox checked={offline} onChange={(e) => setOffline(e.target.checked)} />}
            label="Offline: use the cache as it is, without fetching"
          />
          {error !== null && (
            <Alert severity="error">
              {error.message}
              {hint !== null && <div className={styles.hint}>{hint}</div>}
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={close} disabled={creating}>
            Cancel
          </Button>
          <Button type="submit" variant="contained" disabled={creating}>
            {creating ? "Starting…" : "Start agent"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
};
