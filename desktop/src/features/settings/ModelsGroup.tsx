import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, TextField } from "@mui/material";
import { useCallback, useEffect, useState, type JSX } from "react";
import type { AppError, DaemonStatus, EndpointSettings, ModelSettings, RoleSettings, RunnerCheck, RunnerSettings } from "../../api";
import { useAppDispatch, type AppDispatch } from "../../store";
import {
  checkRunner,
  fetchDaemonStatus,
  fetchRunnerSettings,
  restartDaemon,
  setConfig,
  setEndpointToken,
  unsetConfig,
} from "./settingsSlice";
import dialogStyles from "./ProfileEditor.module.css";
import styles from "./ModelsGroup.module.css";

/** One config.toml key to set, or to unset when no values are given. */
interface ConfigWrite {
  readonly key: string;
  readonly values: readonly string[];
}

interface Choice {
  readonly value: string;
  readonly label: string;
  readonly disabled?: boolean;
}

const endpointKeys = ["url", "api", "probe", "request_timeout", "idle_timeout"] as const;
type EndpointKey = (typeof endpointKeys)[number];
type EndpointDraft = Readonly<Record<EndpointKey, string>>;

/** The config defaults for a new endpoint (reference.md, `[endpoints.<name>]`). */
const endpointDefaults: EndpointDraft = { url: "", api: "openai", probe: "auto", request_timeout: "10m", idle_timeout: "90s" };

const endpointNamePattern = /^[a-z][a-z0-9-]{0,31}$/;

const apiChoices: readonly Choice[] = [
  { value: "openai", label: "OpenAI-compatible" },
  { value: "ollama", label: "Ollama native (next)", disabled: true },
  { value: "openai-responses", label: "OpenAI Responses (later)", disabled: true },
  { value: "anthropic", label: "Anthropic (later)", disabled: true },
];

const probeChoices: readonly Choice[] = [
  { value: "auto", label: "auto" },
  { value: "ollama", label: "ollama" },
  { value: "none", label: "none" },
];

const reasoningChoices: readonly Choice[] = [
  { value: "", label: "Server default" },
  { value: "none", label: "none" },
  { value: "low", label: "low" },
  { value: "medium", label: "medium" },
  { value: "high", label: "high" },
];

const runnerChoices: readonly Choice[] = [
  { value: "claude", label: "Claude Code" },
  { value: "ws", label: "ws runner" },
];

const builtInRoles: readonly string[] = ["implementer", "reviewer", "verifier"];

const builtInMaxSteps: Readonly<Record<string, number>> = { implementer: 80, reviewer: 40, verifier: 40 };

const originNames: Readonly<Record<string, string>> = {
  app: "this app",
  transient: "a terminal",
  managed: "a service manager",
};

const checkErrorTitles: Readonly<Record<string, string>> = {
  endpoint_unreachable: "The endpoint didn't answer",
  model_not_served: "The endpoint doesn't serve that model",
  model_without_tools: "That model can't call tools",
};

/** The Runner service line: state, who started it, pids and runner count. */
export const daemonSummary = (status: DaemonStatus): string => {
  if (!status.running) {
    return "not running";
  }
  const parts = ["running"];
  if (status.origin !== null) {
    parts.push(`started by ${originNames[status.origin] ?? status.origin}`);
  }
  if (status.pid !== null) {
    parts.push(`pid ${status.pid}`);
  }
  const count = status.runners.length;
  parts.push(`${count} ${count === 1 ? "runner" : "runners"}`);
  return parts.join(" · ");
};

/** A passing check, in one line: the models served, the model, its context, and the probe. */
export const checkSummary = (check: RunnerCheck): string => {
  const count = check.models.length;
  const parts = [`✓ ${count} ${count === 1 ? "model" : "models"}`, check.model, `context ${check.context_tokens}`];
  if (check.probe === "none") {
    parts.push("no probe");
  } else if (check.capabilities?.includes("tools") === true) {
    parts.push("supports tools");
  }
  return parts.join(" · ");
};

/** The config writes that change an endpoint's keys from before to draft; all of them when it is new. */
export const endpointWrites = (name: string, before: EndpointSettings | null, draft: EndpointDraft): readonly ConfigWrite[] =>
  endpointKeys.flatMap((key) =>
    before !== null && before[key] === draft[key] ? [] : [{ key: `endpoints.${name}.${key}`, values: [draft[key]] }],
  );

const draftOf = (endpoint: EndpointSettings | null): EndpointDraft => {
  if (endpoint === null) {
    return endpointDefaults;
  }
  return {
    url: endpoint.url,
    api: endpoint.api,
    probe: endpoint.probe,
    request_timeout: endpoint.request_timeout,
    idle_timeout: endpoint.idle_timeout,
  };
};

/** The role as config.toml has it, or the built-in defaults for a role it doesn't name yet. */
export const roleOf = (roles: readonly RoleSettings[], name: string): RoleSettings =>
  roles.find((role) => role.role === name) ?? {
    role: name,
    runner: "claude",
    model: "",
    max_steps: builtInMaxSteps[name] ?? 60,
    timeout: "",
    max_tokens: 0,
    mcp_servers: [],
  };

/** The built-in roles, then any other role config.toml names. */
const roleNames = (roles: readonly RoleSettings[]): readonly string[] => [
  ...builtInRoles,
  ...roles.map((role) => role.role).filter((name) => !builtInRoles.includes(name)),
];

/** The first failure message of a write, or null when it saved. */
const writeOne = async (dispatch: AppDispatch, write: ConfigWrite): Promise<string | null> => {
  if (write.values.length === 0) {
    const result = await dispatch(unsetConfig(write.key));
    if (unsetConfig.fulfilled.match(result)) {
      return null;
    }
    return result.payload?.message ?? result.error.message ?? "saving the setting failed";
  }
  const result = await dispatch(setConfig({ key: write.key, values: write.values }));
  if (setConfig.fulfilled.match(result)) {
    return null;
  }
  return result.payload?.message ?? result.error.message ?? "saving the setting failed";
};

/** Writes each key in order, stopping at the first failure. */
const writeAll = async (dispatch: AppDispatch, writes: readonly ConfigWrite[]): Promise<string | null> => {
  for (const write of writes) {
    const failure = await writeOne(dispatch, write);
    if (failure !== null) {
      return failure;
    }
  }
  return null;
};

/** Saves an endpoint's token to secrets.env; an empty token removes it. */
const saveToken = async (dispatch: AppDispatch, name: string, token: string): Promise<string | null> => {
  const result = await dispatch(setEndpointToken({ name, token }));
  if (setEndpointToken.fulfilled.match(result)) {
    return null;
  }
  return result.payload?.message ?? result.error.message ?? "saving the token failed";
};

interface CellProps {
  readonly label: string;
  readonly value: string;
  readonly onCommit: (value: string) => void;
  readonly type?: "text" | "number";
  readonly placeholder?: string;
}

/** A text or number control that saves when it loses focus, if the text changed. */
const TextCell = ({ label, value, onCommit, type = "text", placeholder }: CellProps): JSX.Element => {
  const [draft, setDraft] = useState(value);
  useEffect(() => {
    setDraft(value);
  }, [value]);
  return (
    <TextField
      size="small"
      type={type}
      value={draft}
      placeholder={placeholder}
      className={styles.target}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        if (draft !== value) {
          onCommit(draft);
        }
      }}
      slotProps={{ htmlInput: { "aria-label": label } }}
    />
  );
};

interface SelectCellProps {
  readonly label: string;
  readonly value: string;
  readonly choices: readonly Choice[];
  readonly onCommit: (value: string) => void;
}

const SelectCell = ({ label, value, choices, onCommit }: SelectCellProps): JSX.Element => (
  <TextField
    select
    size="small"
    value={value}
    className={styles.target}
    onChange={(e) => onCommit(e.target.value)}
    slotProps={{ htmlInput: { "aria-label": label } }}
  >
    {choices.map((choice) => (
      <MenuItem key={choice.value} value={choice.value} disabled={choice.disabled === true}>
        {choice.label}
      </MenuItem>
    ))}
  </TextField>
);

/** The first message of a failed save, shown under its section. */
const SectionError = ({ message }: { readonly message: string | null }): JSX.Element | null =>
  message === null ? null : <Alert severity="error">{message}</Alert>;

interface RunnerServiceProps {
  readonly status: DaemonStatus | null;
  readonly error: string | null;
  readonly onRestart: () => Promise<void>;
  readonly restarting: boolean;
}

const RunnerService = ({ status, error, onRestart, restarting }: RunnerServiceProps): JSX.Element => (
  <section className={styles.section} aria-label="Runner service">
    <h2 className={styles.heading}>Runner service</h2>
    {error !== null && <Alert severity="error">{error}</Alert>}
    {status !== null && (
      <div className={styles.service}>
        <span className={status.running ? styles.dotOn : styles.dotOff} aria-hidden="true" />
        <span className={styles.serviceText}>{daemonSummary(status)}</span>
        {status.running && (
          <Button className={styles.target} variant="outlined" disabled={restarting} onClick={() => void onRestart()}>
            Restart
          </Button>
        )}
      </div>
    )}
    {status?.warnings.map((warning) => (
      <p key={warning} className={styles.caption}>
        {warning}
      </p>
    ))}
  </section>
);

/** The message of a failed check, with the title its error code has. */
const checkFailure = (error: AppError): string => {
  const title = checkErrorTitles[error.code];
  return title === undefined ? error.message : `${title}. ${error.message}`;
};

type CheckOutcome = { readonly result: RunnerCheck } | { readonly error: AppError };

const tokenNote = (outcome: CheckOutcome | undefined): string => {
  if (outcome === undefined || "error" in outcome) {
    return "not checked";
  }
  return outcome.result.token_source === null ? "no token" : "token saved";
};

interface EndpointDialogProps {
  readonly endpoint: EndpointSettings | null;
  readonly onClose: () => void;
  readonly onSaved: () => Promise<void>;
}

/** Adds an endpoint, or edits one. Each changed key is one ws config set; the token is written only when typed. */
const EndpointDialog = ({ endpoint, onClose, onSaved }: EndpointDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [name, setName] = useState(endpoint?.name ?? "");
  const [draft, setDraft] = useState<EndpointDraft>(draftOf(endpoint));
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const update = (key: EndpointKey, value: string): void => setDraft((current) => ({ ...current, [key]: value }));
  const save = async (): Promise<void> => {
    const target = endpoint?.name ?? name.trim();
    if (!endpointNamePattern.test(target)) {
      setError("A name is lowercase letters, digits and dashes, starting with a letter");
      return;
    }
    if (draft.url.trim() === "") {
      setError("A URL is needed");
      return;
    }
    setSaving(true);
    setError(null);
    const failure =
      (await writeAll(dispatch, endpointWrites(target, endpoint, { ...draft, url: draft.url.trim() }))) ??
      (token === "" ? null : await saveToken(dispatch, target, token));
    setSaving(false);
    if (failure !== null) {
      setError(failure);
      return;
    }
    await onSaved();
    onClose();
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{endpoint === null ? "Add an endpoint" : `Edit ${endpoint.name}`}</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        <TextField
          fullWidth
          label="Name"
          value={endpoint?.name ?? name}
          disabled={endpoint !== null}
          onChange={(e) => setName(e.target.value)}
        />
        <TextField fullWidth label="URL" value={draft.url} onChange={(e) => update("url", e.target.value)} />
        <TextField select fullWidth label="API" value={draft.api} onChange={(e) => update("api", e.target.value)}>
          {apiChoices.map((choice) => (
            <MenuItem key={choice.value} value={choice.value} disabled={choice.disabled === true}>
              {choice.label}
            </MenuItem>
          ))}
        </TextField>
        <TextField select fullWidth label="Probe" value={draft.probe} onChange={(e) => update("probe", e.target.value)}>
          {probeChoices.map((choice) => (
            <MenuItem key={choice.value} value={choice.value}>
              {choice.label}
            </MenuItem>
          ))}
        </TextField>
        <TextField fullWidth label="Request timeout" value={draft.request_timeout} onChange={(e) => update("request_timeout", e.target.value)} />
        <TextField fullWidth label="Idle timeout" value={draft.idle_timeout} onChange={(e) => update("idle_timeout", e.target.value)} />
        <TextField
          fullWidth
          type="password"
          label="Token"
          autoComplete="off"
          value={token}
          helperText="Write-only: saved to secrets.env and never shown again. Empty keeps the saved token."
          onChange={(e) => setToken(e.target.value)}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={saving} onClick={() => void save()}>
          Save
        </Button>
      </DialogActions>
    </Dialog>
  );
};

interface EndpointsSectionProps {
  readonly endpoints: readonly EndpointSettings[];
  readonly onChanged: () => Promise<void>;
}

const EndpointsSection = ({ endpoints, onChanged }: EndpointsSectionProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [editing, setEditing] = useState<{ readonly endpoint: EndpointSettings | null } | null>(null);
  const [outcomes, setOutcomes] = useState<Readonly<Record<string, CheckOutcome>>>({});
  const [error, setError] = useState<string | null>(null);
  const test = async (name: string): Promise<void> => {
    const result = await dispatch(checkRunner({ endpoint: name, model: "" }));
    const outcome: CheckOutcome = checkRunner.fulfilled.match(result)
      ? { result: result.payload }
      : { error: result.payload ?? { code: "internal", message: result.error.message ?? "the check failed" } };
    setOutcomes((current) => ({ ...current, [name]: outcome }));
  };
  const remove = async (endpoint: EndpointSettings): Promise<void> => {
    const failure =
      (await writeAll(dispatch, [{ key: `endpoints.${endpoint.name}`, values: [] }])) ?? (await saveToken(dispatch, endpoint.name, ""));
    if (failure !== null) {
      setError(failure);
      return;
    }
    setError(null);
    await onChanged();
  };
  return (
    <section className={styles.section} aria-label="Endpoints">
      <h2 className={styles.heading}>Endpoints</h2>
      <SectionError message={error} />
      <div className={styles.scroll}>
        <table className={styles.table} aria-label="Endpoints table">
          <thead>
            <tr>
              <th>Name</th>
              <th>URL</th>
              <th>API</th>
              <th>Probe</th>
              <th>Timeouts</th>
              <th>Token</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {endpoints.map((endpoint) => {
              const outcome = outcomes[endpoint.name];
              return (
                <EndpointRows
                  key={endpoint.name}
                  endpoint={endpoint}
                  outcome={outcome}
                  onEdit={() => setEditing({ endpoint })}
                  onRemove={() => void remove(endpoint)}
                  onTest={() => void test(endpoint.name)}
                />
              );
            })}
          </tbody>
        </table>
      </div>
      <Button className={styles.target} variant="outlined" onClick={() => setEditing({ endpoint: null })}>
        Add endpoint
      </Button>
      {editing !== null && (
        <EndpointDialog endpoint={editing.endpoint} onClose={() => setEditing(null)} onSaved={onChanged} />
      )}
    </section>
  );
};

interface EndpointRowsProps {
  readonly endpoint: EndpointSettings;
  readonly outcome: CheckOutcome | undefined;
  readonly onEdit: () => void;
  readonly onRemove: () => void;
  readonly onTest: () => void;
}

/** One endpoint's row, and under it the result of its last test connection. */
const EndpointRows = ({ endpoint, outcome, onEdit, onRemove, onTest }: EndpointRowsProps): JSX.Element => (
  <>
    <tr>
      <td className={styles.mono}>{endpoint.name}</td>
      <td className={styles.mono}>{endpoint.url}</td>
      <td>{endpoint.api}</td>
      <td>{endpoint.probe}</td>
      <td className={styles.muted}>{`${endpoint.request_timeout} / ${endpoint.idle_timeout}`}</td>
      <td className={styles.muted}>{tokenNote(outcome)}</td>
      <td>
        <div className={styles.actions}>
          <Button className={styles.target} size="small" onClick={onTest}>
            Test connection
          </Button>
          <Button className={styles.target} size="small" onClick={onEdit}>
            Edit
          </Button>
          <Button className={styles.target} size="small" onClick={onRemove}>
            Remove
          </Button>
        </div>
      </td>
    </tr>
    {outcome !== undefined && (
      <tr>
        <td colSpan={7}>
          {"error" in outcome ? (
            <Alert severity="error">{checkFailure(outcome.error)}</Alert>
          ) : (
            <p className={styles.check}>{checkSummary(outcome.result)}</p>
          )}
        </td>
      </tr>
    )}
  </>
);

interface ModelsSectionProps {
  readonly models: readonly ModelSettings[];
  readonly endpoints: readonly EndpointSettings[];
  readonly onChanged: () => Promise<void>;
}

const ModelsSection = ({ models, endpoints, onChanged }: ModelsSectionProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [error, setError] = useState<string | null>(null);
  const write = async (alias: string, key: string, value: string): Promise<void> => {
    const failure = await writeAll(dispatch, [{ key: `models.${alias}.${key}`, values: [value] }]);
    setError(failure);
    if (failure === null) {
      await onChanged();
    }
  };
  const endpointChoices: readonly Choice[] = endpoints.map((endpoint) => ({ value: endpoint.name, label: endpoint.name }));
  return (
    <section className={styles.section} aria-label="Models">
      <h2 className={styles.heading}>Models</h2>
      <SectionError message={error} />
      <div className={styles.scroll}>
        <table className={styles.table} aria-label="Models table">
          <thead>
            <tr>
              <th>Alias</th>
              <th>Endpoint</th>
              <th>ID</th>
              <th>Context</th>
              <th>Max output</th>
              <th>Temperature</th>
              <th>Reasoning</th>
            </tr>
          </thead>
          <tbody>
            {models.map((model) => (
              <tr key={model.alias}>
                <td className={styles.mono}>{model.alias}</td>
                <td>
                  <SelectCell
                    label={`Endpoint for ${model.alias}`}
                    value={model.endpoint}
                    choices={endpointChoices}
                    onCommit={(value) => void write(model.alias, "endpoint", value)}
                  />
                </td>
                <td>
                  <TextCell
                    label={`ID for ${model.alias}`}
                    value={model.id}
                    onCommit={(value) => void write(model.alias, "id", value)}
                  />
                </td>
                <td>
                  <TextCell
                    label={`Context for ${model.alias}`}
                    type="number"
                    value={String(model.context_tokens)}
                    onCommit={(value) => void write(model.alias, "context_tokens", value)}
                  />
                </td>
                <td>
                  <TextCell
                    label={`Max output for ${model.alias}`}
                    type="number"
                    value={String(model.max_output)}
                    onCommit={(value) => void write(model.alias, "max_output", value)}
                  />
                </td>
                <td>
                  <TextCell
                    label={`Temperature for ${model.alias}`}
                    type="number"
                    value={String(model.temperature)}
                    onCommit={(value) => void write(model.alias, "temperature", value)}
                  />
                </td>
                <td>
                  <SelectCell
                    label={`Reasoning for ${model.alias}`}
                    value={model.reasoning}
                    choices={reasoningChoices}
                    onCommit={(value) => void write(model.alias, "reasoning", value)}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
};

/** The comma-separated server names typed; none means the empty list. */
export const splitNames = (text: string): readonly string[] =>
  text
    .split(",")
    .map((name) => name.trim())
    .filter((name) => name !== "");

interface RolesSectionProps {
  readonly settings: RunnerSettings;
  readonly onChanged: () => Promise<void>;
}

const RolesSection = ({ settings, onChanged }: RolesSectionProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [error, setError] = useState<string | null>(null);
  const write = async (role: string, key: string, values: readonly string[]): Promise<void> => {
    const failure = await writeAll(dispatch, [{ key: `runner.roles.${role}.${key}`, values }]);
    setError(failure);
    if (failure === null) {
      await onChanged();
    }
  };
  const modelChoices: readonly Choice[] = [
    { value: "", label: `Default (runner.model: ${settings.runner.model})` },
    ...settings.models.map((model) => ({ value: model.alias, label: model.alias })),
  ];
  return (
    <section className={styles.section} aria-label="Roles">
      <h2 className={styles.heading}>Roles</h2>
      <SectionError message={error} />
      <div className={styles.scroll}>
        <table className={styles.table} aria-label="Roles table">
          <thead>
            <tr>
              <th>Role</th>
              <th>Runner</th>
              <th>Model</th>
              <th>MCP servers</th>
              <th>Budget (steps)</th>
            </tr>
          </thead>
          <tbody>
            {roleNames(settings.roles).map((name) => {
              const role = roleOf(settings.roles, name);
              return (
                <tr key={name}>
                  <td className={styles.mono}>{name}</td>
                  <td>
                    <SelectCell
                      label={`Runner for ${name}`}
                      value={role.runner}
                      choices={runnerChoices}
                      onCommit={(value) => void write(name, "runner", [value])}
                    />
                  </td>
                  <td>
                    <SelectCell
                      label={`Model for ${name}`}
                      value={role.model}
                      choices={modelChoices}
                      onCommit={(value) => void write(name, "model", [value])}
                    />
                  </td>
                  <td>
                    <TextCell
                      label={`MCP servers for ${name}`}
                      value={role.mcp_servers.join(", ")}
                      placeholder="none"
                      onCommit={(value) => void write(name, "mcp_servers", splitNames(value))}
                    />
                  </td>
                  <td>
                    <TextCell
                      label={`Budget for ${name}`}
                      type="number"
                      value={String(role.max_steps)}
                      onCommit={(value) => void write(name, "max_steps", [value])}
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
};

/** Settings › Models: the runner service, the endpoints and their tests, the models, and the role routing (ADR 0068, ADR 0071). */
export const ModelsGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const [settings, setSettings] = useState<RunnerSettings | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [status, setStatus] = useState<DaemonStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const refreshSettings = useCallback(async (): Promise<void> => {
    const result = await dispatch(fetchRunnerSettings());
    if (fetchRunnerSettings.fulfilled.match(result)) {
      setSettings(result.payload);
      setLoadError(null);
      return;
    }
    setLoadError(result.payload?.message ?? result.error.message ?? "reading the runner settings failed");
  }, [dispatch]);
  const refreshStatus = useCallback(async (): Promise<void> => {
    const result = await dispatch(fetchDaemonStatus());
    if (fetchDaemonStatus.fulfilled.match(result)) {
      setStatus(result.payload);
      setStatusError(null);
      return;
    }
    setStatusError(result.payload?.message ?? result.error.message ?? "reading the runner service failed");
  }, [dispatch]);
  const restart = async (): Promise<void> => {
    setRestarting(true);
    const result = await dispatch(restartDaemon());
    setRestarting(false);
    if (!restartDaemon.fulfilled.match(result)) {
      setStatusError(result.payload?.message ?? result.error.message ?? "restarting the runner service failed");
      return;
    }
    await refreshStatus();
  };
  useEffect(() => {
    void refreshStatus();
    void refreshSettings();
  }, [refreshStatus, refreshSettings]);
  return (
    <section className={styles.group} aria-label="Models">
      <h1 className={styles.title}>Models</h1>
      <RunnerService status={status} error={statusError} onRestart={restart} restarting={restarting} />
      {loadError !== null && <Alert severity="error">{loadError}</Alert>}
      {settings === null ? (
        loadError === null && <CircularProgress size={20} aria-label="Loading models" />
      ) : (
        <>
          <EndpointsSection endpoints={settings.endpoints} onChanged={refreshSettings} />
          <ModelsSection models={settings.models} endpoints={settings.endpoints} onChanged={refreshSettings} />
          <RolesSection settings={settings} onChanged={refreshSettings} />
        </>
      )}
    </section>
  );
};
