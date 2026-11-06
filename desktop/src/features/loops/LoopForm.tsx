import { Alert, Button, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { addLoop, creatingCancelled } from "./loopsSlice";
import styles from "./LoopsPane.module.css";

/** New loop: the fields ws add takes. ws checks the period and the specs. */
export const LoopForm = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const formError = useAppSelector((s) => s.loops.formError);
  const [name, setName] = useState("");
  const [every, setEvery] = useState("");
  const [prompt, setPrompt] = useState("");
  const [repos, setRepos] = useState("");
  const ready = name.trim() !== "" && every.trim() !== "" && prompt.trim() !== "";
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    void dispatch(
      addLoop({
        name: name.trim(),
        every: every.trim(),
        prompt: prompt.trim(),
        specs: repos.split(/\s+/).filter(Boolean),
      }),
    );
  };
  return (
    <form className={styles.form} onSubmit={submit} aria-label="New loop">
      <h2 className={styles.formTitle}>New loop</h2>
      <TextField label="Name" required value={name} onChange={(e) => setName(e.target.value)} size="small" fullWidth />
      <TextField
        label="Every"
        required
        value={every}
        onChange={(e) => setEvery(e.target.value)}
        placeholder="15m, 1h, 24h"
        helperText="Whole minutes, 5m to 168h"
        size="small"
        fullWidth
      />
      <TextField
        label="Prompt"
        required
        multiline
        rows={4}
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        size="small"
        fullWidth
      />
      <TextField
        label="Repos"
        value={repos}
        onChange={(e) => setRepos(e.target.value)}
        helperText="Repo specs, separated by spaces"
        size="small"
        fullWidth
      />
      {formError !== null && <Alert severity="error">{formError.message}</Alert>}
      <div className={styles.formButtons}>
        <Button onClick={() => dispatch(creatingCancelled())}>Cancel</Button>
        <Button type="submit" variant="contained" disabled={!ready}>
          Add loop
        </Button>
      </div>
    </form>
  );
};
