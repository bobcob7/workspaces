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

/** A config name for a model id: lowercase letters, digits and dashes, starting with a letter, at most 32 characters. */
export const modelAliasFor = (id: string): string => {
  const dashed = id.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const named = /^[a-z]/.test(dashed) ? dashed : `m-${dashed}`;
  return named.slice(0, 32).replace(/-+$/, "");
};

/** The least context a model may be given (reference.md, `[models.<alias>]`). */
const minContextTokens = 16384;

/** What a new model starts with: a context most local servers can be set to. */
const defaultContextTokens = 65536;

/** A passing check, in one line: the models served, the model, its context, and the probe. An endpoint-only check has no model to name. */
export const checkSummary = (check: RunnerCheck): string => {
  const count = check.models.length;
  const head = `✓ ${count} ${count === 1 ? "model" : "models"}`;
  const parts =
    check.model === null || check.context_tokens === null
      ? [head, "no model checked"]
      : [head, check.model, `context ${check.context_tokens}`];
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
  /** Room for a model id such as qwen/qwen3.5-27b, which the default width clips. */
  readonly wide?: boolean;
}

/** A text or number control that saves when it loses focus, if the text changed. */
const TextCell = ({ label, value, onCommit, type = "text", placeholder, wide = false }: CellProps): JSX.Element => {
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
      className={wide ? `${styles.target} ${styles.wide}` : styles.target}
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
    slotProps={{ htmlInput: { "aria-label": label }, select: { displayEmpty: true } }}
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
      // Earlier writes may have landed, so refresh before showing the error.
      await onSaved();
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

type CheckOutcomes = Readonly<Record<string, CheckOutcome>>;

/** The models an endpoint's last passing check says it serves; none when it wasn't tested or failed. */
const servedBy = (outcomes: CheckOutcomes, endpoint: string): readonly string[] => {
  const outcome = outcomes[endpoint];
  return outcome === undefined || "error" in outcome ? [] : outcome.result.models;
};

interface EndpointsSectionProps {
  readonly endpoints: readonly EndpointSettings[];
  readonly models: readonly ModelSettings[];
  readonly outcomes: CheckOutcomes;
  readonly onTest: (endpoint: string) => Promise<void>;
  readonly onAddModel: (endpoint: string, id: string) => void;
  readonly onChanged: () => Promise<void>;
}

const EndpointsSection = ({ endpoints, models, outcomes, onTest, onAddModel, onChanged }: EndpointsSectionProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [editing, setEditing] = useState<{ readonly endpoint: EndpointSettings | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const remove = async (endpoint: EndpointSettings): Promise<void> => {
    const failure =
      (await writeAll(dispatch, [{ key: `endpoints.${endpoint.name}`, values: [] }])) ?? (await saveToken(dispatch, endpoint.name, ""));
    await onChanged();
    setError(failure);
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
                  models={models}
                  onEdit={() => setEditing({ endpoint })}
                  onRemove={() => void remove(endpoint)}
                  onTest={() => void onTest(endpoint.name)}
                  onAddModel={(id) => onAddModel(endpoint.name, id)}
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
  readonly models: readonly ModelSettings[];
  readonly onEdit: () => void;
  readonly onRemove: () => void;
  readonly onTest: () => void;
  readonly onAddModel: (id: string) => void;
}

interface ServedModelsProps {
  readonly endpoint: string;
  readonly served: readonly string[];
  readonly models: readonly ModelSettings[];
  readonly onAddModel: (id: string) => void;
}

/** The models an endpoint serves, each with a shortcut that pre-fills Add model, or the alias that already uses it. */
const ServedModels = ({ endpoint, served, models, onAddModel }: ServedModelsProps): JSX.Element => (
  <ul className={styles.served} aria-label={`Models served by ${endpoint}`}>
    {served.map((id) => {
      const alias = models.find((model) => model.endpoint === endpoint && model.id === id)?.alias;
      return (
        <li key={id} className={styles.servedRow}>
          <span className={styles.mono}>{id}</span>
          {alias === undefined ? (
            <Button className={styles.target} size="small" onClick={() => onAddModel(id)}>
              Add as model
            </Button>
          ) : (
            <span className={styles.muted}>{`added as ${alias}`}</span>
          )}
        </li>
      );
    })}
  </ul>
);

/** One endpoint's row, and under it the result of its last test connection. */
const EndpointRows = ({ endpoint, outcome, models, onEdit, onRemove, onTest, onAddModel }: EndpointRowsProps): JSX.Element => (
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
            <>
              <p className={styles.check}>{checkSummary(outcome.result)}</p>
              <ServedModels endpoint={endpoint.name} served={outcome.result.models} models={models} onAddModel={onAddModel} />
            </>
          )}
        </td>
      </tr>
    )}
  </>
);

/** Where an Add model form starts: the endpoint and model id to pre-fill, when a served model was picked. */
interface ModelDraft {
  readonly endpoint: string;
  readonly id: string;
}

const otherModel = "\u0000other";

interface AddModelDialogProps {
  readonly endpoints: readonly EndpointSettings[];
  readonly models: readonly ModelSettings[];
  readonly start: ModelDraft;
  readonly outcomes: CheckOutcomes;
  readonly onTest: (endpoint: string) => Promise<void>;
  readonly onClose: () => void;
  readonly onSaved: () => Promise<void>;
}

/** Adds a model: one ws config set of models.<alias> with its endpoint, id and context, which ws checks together. */
const AddModelDialog = ({ endpoints, models, start, outcomes, onTest, onClose, onSaved }: AddModelDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [alias, setAlias] = useState(start.id === "" ? "" : modelAliasFor(start.id));
  const [endpoint, setEndpoint] = useState(start.endpoint);
  const [id, setId] = useState(start.id);
  const [typing, setTyping] = useState(false);
  const [context, setContext] = useState(String(defaultContextTokens));
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const served = servedBy(outcomes, endpoint);
  const choosing = served.length > 0 && !typing && (id === "" || served.includes(id));
  const test = async (): Promise<void> => {
    setTesting(true);
    await onTest(endpoint);
    setTesting(false);
  };
  const save = async (): Promise<void> => {
    const contextTokens = Number(context);
    const problem = ((): string | null => {
      if (!endpointNamePattern.test(alias)) {
        return "An alias is lowercase letters, digits and dashes, starting with a letter";
      }
      if (models.some((model) => model.alias === alias)) {
        return `A model named ${alias} already exists`;
      }
      if (endpoint === "") {
        return "Pick an endpoint";
      }
      if (id.trim() === "") {
        return "A model id is needed";
      }
      if (!Number.isInteger(contextTokens) || contextTokens < minContextTokens) {
        return `Context tokens must be at least ${minContextTokens}`;
      }
      return null;
    })();
    if (problem !== null) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError(null);
    const failure = await writeAll(dispatch, [
      { key: `models.${alias}`, values: [`endpoint=${endpoint}`, `id=${id.trim()}`, `context_tokens=${contextTokens}`] },
    ]);
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
      <DialogTitle>Add a model</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        <TextField fullWidth label="Alias" value={alias} onChange={(e) => setAlias(e.target.value)} />
        <TextField
          select
          fullWidth
          label="Endpoint"
          value={endpoint}
          onChange={(e) => {
            setEndpoint(e.target.value);
            setTyping(false);
            setId("");
          }}
        >
          {endpoints.map((choice) => (
            <MenuItem key={choice.name} value={choice.name}>
              {choice.name}
            </MenuItem>
          ))}
        </TextField>
        {choosing ? (
          <TextField
            select
            fullWidth
            label="Model ID"
            value={id}
            onChange={(e) => {
              if (e.target.value === otherModel) {
                setTyping(true);
                setId("");
                return;
              }
              setId(e.target.value);
            }}
          >
            {served.map((name) => (
              <MenuItem key={name} value={name}>
                {name}
              </MenuItem>
            ))}
            <MenuItem value={otherModel}>Other…</MenuItem>
          </TextField>
        ) : (
          <TextField
            fullWidth
            label="Model ID"
            value={id}
            helperText="As the endpoint lists it; on Ollama: qwen3.5:9b"
            onChange={(e) => setId(e.target.value)}
          />
        )}
        {served.length === 0 && (
          <Button className={styles.target} size="small" disabled={testing || endpoint === ""} onClick={() => void test()}>
            List the models this endpoint serves
          </Button>
        )}
        <TextField
          fullWidth
          type="number"
          label="Context tokens"
          value={context}
          helperText="The server's context length for this model; at least 16384"
          onChange={(e) => setContext(e.target.value)}
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

interface ModelsSectionProps {
  readonly models: readonly ModelSettings[];
  readonly endpoints: readonly EndpointSettings[];
  readonly defaultModel: string;
  readonly outcomes: CheckOutcomes;
  readonly adding: ModelDraft | null;
  readonly onAdd: (start: ModelDraft | null) => void;
  readonly onTest: (endpoint: string) => Promise<void>;
  readonly onChanged: () => Promise<void>;
}

const ModelsSection = ({ models, endpoints, defaultModel, outcomes, adding, onAdd, onTest, onChanged }: ModelsSectionProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [error, setError] = useState<string | null>(null);
  const write = async (alias: string, key: string, value: string): Promise<void> => {
    const failure = await writeAll(dispatch, [{ key: `models.${alias}.${key}`, values: [value] }]);
    setError(failure);
    if (failure === null) {
      await onChanged();
    }
  };
  const setDefault = async (alias: string): Promise<void> => {
    const failure = await writeAll(dispatch, [{ key: "runner.model", values: alias === "" ? [] : [alias] }]);
    setError(failure);
    if (failure === null) {
      await onChanged();
    }
  };
  /** ws refuses to remove the default model, so it is cleared first and put back if the removal fails. */
  const remove = async (model: ModelSettings): Promise<void> => {
    const wasDefault = model.alias === defaultModel;
    let failure = wasDefault ? await writeAll(dispatch, [{ key: "runner.model", values: [] }]) : null;
    failure ??= await writeAll(dispatch, [{ key: `models.${model.alias}`, values: [] }]);
    if (failure !== null && wasDefault) {
      await writeAll(dispatch, [{ key: "runner.model", values: [model.alias] }]);
    }
    setError(failure);
    await onChanged();
  };
  const endpointChoices: readonly Choice[] = endpoints.map((endpoint) => ({ value: endpoint.name, label: endpoint.name }));
  const defaultChoices: readonly Choice[] = [{ value: "", label: "None" }, ...models.map((model) => ({ value: model.alias, label: model.alias }))];
  const firstEndpoint = endpoints[0]?.name ?? "";
  return (
    <section className={styles.section} aria-label="Models">
      <h2 className={styles.heading}>Models</h2>
      <SectionError message={error} />
      {models.length === 0 ? (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>No models yet</p>
          <p className={styles.caption}>
            {endpoints.length === 0
              ? "Add an endpoint first, test it, then add a model from the models it serves."
              : "Test an endpoint above to see the models it serves, then add one. A model is what a ws runner agent runs on; set one as the default model so agents can start without naming one."}
          </p>
        </div>
      ) : (
        <>
          <div className={styles.scroll}>
            <table className={`${styles.table} ${styles.modelsTable}`} aria-label="Models table">
              <thead>
                <tr>
                  <th>Alias</th>
                  <th>Endpoint</th>
                  <th>ID</th>
                  <th>Context</th>
                  <th>Max output</th>
                  <th>Temperature</th>
                  <th>Reasoning</th>
                  <th>Actions</th>
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
                    wide
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
                <td>
                  <Button
                    className={styles.target}
                    size="small"
                    aria-label={`Remove ${model.alias}`}
                    onClick={() => void remove(model)}
                  >
                    Remove
                  </Button>
                </td>
              </tr>
            ))}
              </tbody>
            </table>
          </div>
          <div className={styles.defaultModel}>
            <span className={styles.defaultLabel}>Default model</span>
            <SelectCell label="Default model" value={defaultModel} choices={defaultChoices} onCommit={(value) => void setDefault(value)} />
          </div>
          {defaultModel === "" && (
            <p className={styles.caption}>No default model: ws runner agents need a model named until one is set here.</p>
          )}
        </>
      )}
      <Button className={`${styles.target} ${styles.add}`} variant="outlined" disabled={endpoints.length === 0} onClick={() => onAdd({ endpoint: firstEndpoint, id: "" })}>
        Add model
      </Button>
      {adding !== null && (
        <AddModelDialog
          endpoints={endpoints}
          models={models}
          start={adding}
          outcomes={outcomes}
          onTest={onTest}
          onClose={() => onAdd(null)}
          onSaved={onChanged}
        />
      )}
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
    { value: "", label: settings.runner.model === "" ? "Default (none set)" : `Default (runner.model: ${settings.runner.model})` },
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
  const [outcomes, setOutcomes] = useState<CheckOutcomes>({});
  const [adding, setAdding] = useState<ModelDraft | null>(null);
  const test = useCallback(
    async (name: string): Promise<void> => {
      const result = await dispatch(checkRunner({ endpoint: name, model: "" }));
      const outcome: CheckOutcome = checkRunner.fulfilled.match(result)
        ? { result: result.payload }
        : { error: result.payload ?? { code: "internal", message: result.error.message ?? "the check failed" } };
      setOutcomes((current) => ({ ...current, [name]: outcome }));
    },
    [dispatch],
  );
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
          <EndpointsSection
            endpoints={settings.endpoints}
            models={settings.models}
            outcomes={outcomes}
            onTest={test}
            onAddModel={(endpoint, id) => setAdding({ endpoint, id })}
            onChanged={refreshSettings}
          />
          <ModelsSection
            models={settings.models}
            endpoints={settings.endpoints}
            defaultModel={settings.runner.model}
            outcomes={outcomes}
            adding={adding}
            onAdd={setAdding}
            onTest={test}
            onChanged={refreshSettings}
          />
          <RolesSection settings={settings} onChanged={refreshSettings} />
        </>
      )}
    </section>
  );
};
