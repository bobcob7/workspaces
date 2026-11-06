import { Alert, Button, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import type { LoopVar, NewLoop } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { addLoop, creatingCancelled } from "./loopsSlice";
import { VarsEditor } from "./VarsEditor";
import styles from "./LoopsPane.module.css";

/** New loop: the fields ws add takes. ws checks the period, the specs, and the script timeout. */
export const LoopForm = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const formError = useAppSelector((s) => s.loops.formError);
  const [name, setName] = useState("");
  const [every, setEvery] = useState("");
  const [prompt, setPrompt] = useState("");
  const [repos, setRepos] = useState("");
  const [script, setScript] = useState("");
  const [scriptTimeout, setScriptTimeout] = useState("");
  const [vars, setVars] = useState<readonly LoopVar[]>([]);
  const ready = name.trim() !== "" && every.trim() !== "" && prompt.trim() !== "";
  const hasScript = script.trim() !== "";
  const namedVars = vars
    .filter((row) => row.key.trim() !== "")
    .map((row) => ({ key: row.key.trim(), value: row.value }));
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    const request: NewLoop = {
      name: name.trim(),
      every: every.trim(),
      prompt: prompt.trim(),
      specs: repos.split(/\s+/).filter(Boolean),
      ...(hasScript ? { script } : {}),
      ...(hasScript && scriptTimeout.trim() !== "" ? { scriptTimeout: scriptTimeout.trim() } : {}),
      ...(namedVars.length > 0 ? { vars: namedVars } : {}),
    };
    void dispatch(addLoop(request));
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
        helperText="A Go template: {{.Vars.KEY}}, {{.Stdout}}, {{.Stderr}}, {{.Name}}"
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
      <TextField
        label="Script"
        multiline
        rows={3}
        value={script}
        onChange={(e) => setScript(e.target.value)}
        helperText="Runs with /bin/sh -c at each run; the run starts only if it exits 0. Its output is {{.Stdout}} and {{.Stderr}} in the prompt"
        size="small"
        fullWidth
      />
      {hasScript && (
        <TextField
          label="Script timeout"
          value={scriptTimeout}
          onChange={(e) => setScriptTimeout(e.target.value)}
          placeholder="1m"
          helperText="1s to 10m"
          size="small"
          fullWidth
        />
      )}
      <VarsEditor rows={vars} onChange={setVars} />
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
