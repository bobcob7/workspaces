import {
  Alert,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  TextField,
} from "@mui/material";
import { useRef, useState, type FormEvent, type JSX } from "react";
import type { AppError } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { createAgent, createErrorCleared } from "../agents/agentsSlice";
import { AttachmentBar } from "../artifacts/AttachButton";
import { insertAtSelection, useAttachments } from "../artifacts/useAttachments";
import { dialogClosed } from "../ui/uiSlice";
import { RepoSpecsField } from "./RepoSpecsField";
import { RunnerModelFields, type RunnerModelValues } from "./RunnerModelFields";
import { useRunnerSettings } from "./useRunnerSettings";
import { usePrefilledSpecs } from "./usePrefilledSpecs";
import styles from "./NewDialog.module.css";

export { models, permissionModes } from "./RunnerModelFields";

const emptyRunner: RunnerModelValues = { runner: "claude", model: "", endpoint: "", permissionMode: "" };

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
  const [runnerFields, setRunnerFields] = useState<RunnerModelValues>(emptyRunner);
  const [offline, setOffline] = useState(false);
  const promptRef = useRef<HTMLTextAreaElement | null>(null);
  const attachments = useAttachments(
    (reference, selection) => setPrompt((current) => insertAtSelection(current, selection, reference)),
    () => promptRef.current,
  );
  const runnerSettings = useRunnerSettings(open);
  const promptMissing = runnerFields.runner === "ws" && prompt.trim() === "";
  usePrefilledSpecs(open, setSpecs);
  const close = (): void => {
    dispatch(dialogClosed());
    dispatch(createErrorCleared());
  };
  const changeRunner = (change: Partial<RunnerModelValues>): void => {
    setRunnerFields((current) => ({ ...current, ...change }));
  };
  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const { runner, model, endpoint, permissionMode } = runnerFields;
    const result = await dispatch(
      createAgent({ name, specs, prompt, model, permissionMode, offline, runner, endpoint }),
    );
    if (createAgent.fulfilled.match(result)) {
      setName("");
      setSpecs([]);
      setPrompt("");
    }
  };
  const hint = error === null ? null : errorHint(error);
  return (
    <Dialog open={open} onClose={creating ? undefined : close} fullWidth maxWidth="sm">
      <form className={styles.form} onSubmit={(e) => void submit(e)}>
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
            inputRef={promptRef}
            onPaste={attachments.onPaste}
            onDrop={attachments.onDrop}
            onDragOver={attachments.onDragOver}
            multiline
            minRows={4}
            placeholder={
              runnerFields.runner === "ws"
                ? "What should the agent do?"
                : "What should the agent do? Leave empty to start it waiting for you."
            }
            helperText={promptMissing ? "Required for the ws runner" : undefined}
          />
          <AttachmentBar text={prompt} onTextChange={setPrompt} attachments={attachments} />
          <RunnerModelFields {...runnerFields} settings={runnerSettings} onChange={changeRunner} />
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
          <Button type="submit" variant="contained" disabled={creating || promptMissing || attachments.busy}>
            {creating ? "Starting…" : "Start agent"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
};
