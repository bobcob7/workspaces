import { Alert, Button, FormHelperText, MenuItem, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import type { AppError, Loop, NewLoop } from "../../api";
import { models, permissionModes } from "../create/NewAgentDialog";
import { RepoSpecsField } from "../create/RepoSpecsField";
import { AmountInput } from "./AmountInput";
import {
  emptyValues,
  formErrors,
  isValid,
  loopLabel,
  requestOf,
  slugOf,
  valuesOf,
  type LoopFormErrors,
  type LoopFormValues,
} from "./formValues";
import { periodUnits, timeoutUnits } from "./period";
import styles from "./LoopsPane.module.css";
import { VarsEditor } from "./VarsEditor";

/** The value field each error belongs to, so an error shows once that field is changed. */
const errorFields: Readonly<Record<keyof LoopFormErrors, keyof LoopFormValues>> = {
  title: "title",
  every: "period",
  keep: "keep",
  prompt: "prompt",
  scriptTimeout: "scriptTimeout",
  vars: "vars",
};

/** Adds the current value to the choices, so a loop's own setting is never lost. */
const choicesWith = (choices: readonly string[], current: string): readonly string[] =>
  choices.includes(current) ? choices : [...choices, current];

const choiceLabel = (choice: string): string => (choice === "" ? "Default" : choice);

export interface LoopFormProps {
  /** The loop to edit; absent for a new loop. */
  readonly loop?: Loop;
  readonly error: AppError | null;
  readonly submitting: boolean;
  readonly onSubmit: (request: NewLoop) => void;
  readonly onCancel: () => void;
}

/** The loop form for add and edit: sections for schedule, prompt, repos, agent, gate script, and variables. */
export const LoopForm = (props: LoopFormProps): JSX.Element => {
  const { loop, error, submitting, onSubmit, onCancel } = props;
  const [values, setValues] = useState<LoopFormValues>(() => (loop === undefined ? emptyValues : valuesOf(loop)));
  const [touched, setTouched] = useState<ReadonlySet<keyof LoopFormValues>>(new Set());
  const [submitted, setSubmitted] = useState(false);
  const errors = formErrors(values);
  const hasScript = values.script.trim() !== "";
  const slug = slugOf(values.title);
  const idHelp = loop ? `ID: ${loop.name}; the ID can't be changed` : slug === "" ? undefined : `ID: ${slug}`;
  const patch = <K extends keyof LoopFormValues>(field: K, value: LoopFormValues[K]): void => {
    setValues((current) => ({ ...current, [field]: value }));
    setTouched((current) => new Set(current).add(field));
  };
  const messageFor = (field: keyof LoopFormErrors): string | undefined =>
    submitted || touched.has(errorFields[field]) ? errors[field] : undefined;
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    setSubmitted(true);
    if (!isValid(values)) {
      return;
    }
    onSubmit(requestOf(values));
  };
  return (
    <form className={styles.form} onSubmit={submit} aria-label={loop ? "Edit loop" : "New loop"} noValidate>
      <h2 className={styles.formTitle}>{loop ? `Edit ${loopLabel(loop)}` : "New loop"}</h2>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Schedule</legend>
        <TextField
          label="Name"
          required
          placeholder="Nightly deps bump"
          value={values.title}
          onChange={(e) => patch("title", e.target.value)}
          error={messageFor("title") !== undefined}
          helperText={messageFor("title") ?? idHelp}
          size="small"
          fullWidth
        />
        <AmountInput
          label="Every"
          value={values.period}
          units={periodUnits}
          onChange={(period) => patch("period", period)}
          error={messageFor("every")}
          helperText="5 minutes to 7 days"
        />
        <TextField
          label="Keep runs"
          type="number"
          value={values.keep}
          onChange={(e) => patch("keep", e.target.value)}
          error={messageFor("keep") !== undefined}
          helperText={messageFor("keep") ?? "How many runs' workspaces to keep"}
          slotProps={{ htmlInput: { min: 1, max: 50 } }}
          size="small"
          fullWidth
        />
      </fieldset>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Prompt</legend>
        <TextField
          label="Prompt"
          required
          multiline
          minRows={6}
          maxRows={16}
          value={values.prompt}
          onChange={(e) => patch("prompt", e.target.value)}
          error={messageFor("prompt") !== undefined}
          helperText={messageFor("prompt") ?? "A Go template: {{.Vars.KEY}}, {{.Stdout}}, {{.Stderr}}, {{.Name}}, {{.Title}}"}
          slotProps={{ htmlInput: { className: styles.mono } }}
          size="small"
          fullWidth
        />
      </fieldset>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Repos</legend>
        <RepoSpecsField value={values.specs} onChange={(specs) => patch("specs", specs)} />
      </fieldset>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Agent</legend>
        <TextField
          select
          label="Model"
          value={values.model}
          onChange={(e) => patch("model", e.target.value)}
          slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
          size="small"
          fullWidth
        >
          {choicesWith(models, values.model).map((model) => (
            <MenuItem key={model} value={model}>
              {choiceLabel(model)}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          label="Permission mode"
          value={values.permissionMode}
          onChange={(e) => patch("permissionMode", e.target.value)}
          slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
          size="small"
          fullWidth
        >
          {choicesWith(permissionModes, values.permissionMode).map((mode) => (
            <MenuItem key={mode} value={mode}>
              {choiceLabel(mode)}
            </MenuItem>
          ))}
        </TextField>
      </fieldset>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Gate script</legend>
        <TextField
          label="Script"
          multiline
          minRows={4}
          maxRows={12}
          value={values.script}
          onChange={(e) => patch("script", e.target.value)}
          helperText="Runs with /bin/sh -c at each run; the run starts only if it exits 0. Its output is {{.Stdout}} and {{.Stderr}} in the prompt"
          slotProps={{ htmlInput: { className: styles.mono } }}
          size="small"
          fullWidth
        />
        {hasScript && (
          <AmountInput
            label="Timeout"
            value={values.scriptTimeout}
            units={timeoutUnits}
            onChange={(timeout) => patch("scriptTimeout", timeout)}
            error={messageFor("scriptTimeout")}
            helperText="1 second to 10 minutes"
          />
        )}
      </fieldset>
      <fieldset className={styles.section}>
        <legend className={styles.legend}>Variables</legend>
        <VarsEditor rows={values.vars} onChange={(vars) => patch("vars", vars)} />
        {messageFor("vars") !== undefined && <FormHelperText error>{messageFor("vars")}</FormHelperText>}
      </fieldset>
      {error !== null && <Alert severity="error">{error.message}</Alert>}
      <div className={styles.formButtons}>
        <Button onClick={onCancel}>Cancel</Button>
        <Button type="submit" variant="contained" disabled={submitting}>
          {loop ? "Save changes" : "Add loop"}
        </Button>
      </div>
    </form>
  );
};
