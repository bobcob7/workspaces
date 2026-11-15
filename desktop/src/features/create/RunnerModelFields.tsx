import { MenuItem, TextField } from "@mui/material";
import type { JSX } from "react";
import type { RunnerSettings } from "../../api";
import styles from "./RunnerModelFields.module.css";

export const models = ["", "opus", "sonnet", "haiku", "fable"] as const;
export const permissionModes = ["", "manual", "acceptEdits", "auto", "plan", "dontAsk", "bypassPermissions"] as const;

/** What runs an agent: Claude Code, or ws's own runner (ADR 0070). */
export type Runner = "claude" | "ws";

/** The fields a runner, model and endpoint choice is made of. */
export interface RunnerModelValues {
  readonly runner: Runner;
  /** A Claude model alias for Claude Code; a `[models]` alias for ws, "" for the default. */
  readonly model: string;
  /** Only for ws: "" for the model's own endpoint. */
  readonly endpoint: string;
  /** Only for Claude Code. */
  readonly permissionMode: string;
}

export interface RunnerModelFieldsProps extends RunnerModelValues {
  /** The runner's keys, for ws's models and endpoints; null until they load. */
  readonly settings: RunnerSettings | null;
  /** Receives only the fields that changed. Switching the runner clears the model, endpoint and permission mode. */
  readonly onChange: (change: Partial<RunnerModelValues>) => void;
}

const choiceLabel = (choice: string): string => (choice === "" ? "Default" : choice);

const modelEndpoint = "The model's endpoint";

const runnerOf = (value: string): Runner => (value === "ws" ? "ws" : "claude");

/** The choices, with a current value that is not among them added, so a setting is never lost. */
const choicesWith = (choices: readonly string[], current: string): readonly string[] =>
  choices.includes(current) ? choices : [...choices, current];

/** The Runner, Model, Endpoint and Permission mode fields for an agent (ADR 0070). */
export const RunnerModelFields = (props: RunnerModelFieldsProps): JSX.Element => {
  const { runner, model, endpoint, permissionMode, settings, onChange } = props;
  const aliases = settings?.models ?? [];
  const endpoints = settings?.endpoints ?? [];
  const wsAvailable = aliases.length > 0;
  const defaultModel = settings === null ? "Default" : `Default (runner.model: ${settings.runner.model})`;
  const switchRunner = (value: string): void => {
    const next = runnerOf(value);
    if (next !== runner) {
      onChange({ runner: next, model: "", endpoint: "", permissionMode: "" });
    }
  };
  return (
    <div className={styles.fields}>
      <TextField
        select
        label="Runner"
        value={runner}
        onChange={(e) => switchRunner(e.target.value)}
        helperText={settings !== null && !wsAvailable ? "Add a model in Settings › Models" : undefined}
        size="small"
        fullWidth
      >
        <MenuItem value="claude">Claude Code</MenuItem>
        <MenuItem value="ws" disabled={!wsAvailable}>
          ws runner
        </MenuItem>
      </TextField>
      {runner === "claude" ? (
        <>
          <TextField
            select
            label="Model"
            value={model}
            onChange={(e) => onChange({ model: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            size="small"
            fullWidth
          >
            {choicesWith(models, model).map((choice) => (
              <MenuItem key={choice} value={choice}>
                {choiceLabel(choice)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            label="Permission mode"
            value={permissionMode}
            onChange={(e) => onChange({ permissionMode: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            size="small"
            fullWidth
          >
            {choicesWith(permissionModes, permissionMode).map((mode) => (
              <MenuItem key={mode} value={mode}>
                {choiceLabel(mode)}
              </MenuItem>
            ))}
          </TextField>
        </>
      ) : (
        <>
          <TextField
            select
            label="Model"
            value={model}
            onChange={(e) => onChange({ model: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            size="small"
            fullWidth
          >
            <MenuItem value="">{defaultModel}</MenuItem>
            {aliases.map((entry) => (
              <MenuItem key={entry.alias} value={entry.alias}>
                {`${entry.alias} · ${entry.id}`}
              </MenuItem>
            ))}
            {model !== "" && !aliases.some((entry) => entry.alias === model) && <MenuItem value={model}>{model}</MenuItem>}
          </TextField>
          <TextField
            select
            label="Endpoint"
            value={endpoint}
            onChange={(e) => onChange({ endpoint: e.target.value })}
            slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}
            size="small"
            fullWidth
          >
            <MenuItem value="">{modelEndpoint}</MenuItem>
            {endpoints.map((entry) => (
              <MenuItem key={entry.name} value={entry.name}>
                {entry.name}
              </MenuItem>
            ))}
            {endpoint !== "" && !endpoints.some((entry) => entry.name === endpoint) && (
              <MenuItem value={endpoint}>{endpoint}</MenuItem>
            )}
          </TextField>
        </>
      )}
    </div>
  );
};
