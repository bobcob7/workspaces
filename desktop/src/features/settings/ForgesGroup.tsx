import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, MenuItem, TextField } from "@mui/material";
import { useEffect, useState, type JSX, type ReactNode } from "react";
import type { ConfiguredHost, ForgeChoice, ForgeKind, HostView } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchReviews, setHosts, setToken } from "../reviews/reviewsSlice";
import dialogStyles from "./ProfileEditor.module.css";
import styles from "./ForgesGroup.module.css";
import { fetchConfig } from "./settingsSlice";

const choices: readonly { readonly value: ForgeChoice; readonly label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "github", label: "GitHub" },
  { value: "forgejo", label: "Forgejo" },
];

const hostNamePattern = /^[a-z0-9.-]+(:[0-9]{1,5})?$/;

/** The configured hosts as config.toml holds them, in order. */
export const configuredHosts = (hosts: readonly HostView[]): readonly ConfiguredHost[] =>
  hosts.filter((host) => host.configured).map((host) => ({ name: host.host, kind: host.choice }));

/** The list with the item at index moved by one place. */
export const moved = <T,>(list: readonly T[], index: number, by: -1 | 1): readonly T[] => {
  const target = index + by;
  const item = list[index];
  if (item === undefined || target < 0 || target >= list.length) {
    return list;
  }
  const rest = list.filter((_, i) => i !== index);
  return [...rest.slice(0, target), item, ...rest.slice(target)];
};

/** "" when name can be added, else the helper text. */
export const hostProblem = (name: string, configured: readonly ConfiguredHost[]): string => {
  if (!hostNamePattern.test(name)) {
    return "Not a host name";
  }
  return configured.some((host) => host.name === name) ? "Already in the list" : "";
};

const tokenStatus = (host: HostView): string => {
  if (host.token === "env") {
    return `from $${host.tokenEnv ?? ""}`;
  }
  return host.token === "file" ? "saved" : "none";
};

const detectedName = (forge: ForgeKind | null): string => {
  if (forge === "forgejo") {
    return "Forgejo";
  }
  return forge === "github" ? "GitHub" : "none";
};

const choiceLabel = (choice: { readonly value: ForgeChoice; readonly label: string }, host: HostView): string => {
  if (choice.value === "auto" && host.choice === "auto") {
    return `Auto (detected: ${detectedName(host.forge)})`;
  }
  return choice.label;
};

const accountOf = (host: HostView): ReactNode => {
  if (host.error !== null) {
    return <span className={styles.error}>{host.error.message}</span>;
  }
  return host.login === null ? null : `signed in as ${host.login}`;
};

interface TokenDialogProps {
  readonly host: string;
  readonly onClose: () => void;
}

/** Takes a token for a host. The value lives in this dialog only, and is gone once it closes. */
const TokenDialog = ({ host, onClose }: TokenDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (): Promise<void> => {
    setSaving(true);
    setError(null);
    const result = await dispatch(setToken({ host, token: value }));
    setSaving(false);
    if (setToken.fulfilled.match(result)) {
      onClose();
      return;
    }
    setError(result.payload?.message ?? result.error.message ?? "saving the token failed");
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>Set token for {host}</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        <TextField
          autoFocus
          fullWidth
          type="password"
          label={`Token for ${host}`}
          autoComplete="off"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
      </DialogContent>
      <DialogActions className={styles.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={saving || value.trim() === ""} onClick={() => void save()}>
          Save
        </Button>
      </DialogActions>
    </Dialog>
  );
};

interface HostRowProps {
  readonly host: HostView;
  readonly first: boolean;
  readonly last: boolean;
  readonly onSetToken: (host: string) => void;
  readonly onKind: (kind: ForgeChoice) => void;
  readonly onMove: (by: -1 | 1) => void;
  readonly onRemove: () => void;
}

const HostRow = ({ host, first, last, onSetToken, onKind, onMove, onRemove }: HostRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const forgejo = host.forge === "forgejo";
  return (
    <tr>
      <td className={styles.host}>
        {host.host}
        {host.primary && <span className={styles.primary}>primary</span>}
      </td>
      <td>
        <TextField
          select
          size="small"
          value={host.choice}
          onChange={(e) => onKind(e.target.value as ForgeChoice)}
          slotProps={{ htmlInput: { "aria-label": `Forge for ${host.host}` } }}
        >
          {choices.map((choice) => (
            <MenuItem key={choice.value} value={choice.value}>
              {choiceLabel(choice, host)}
            </MenuItem>
          ))}
        </TextField>
      </td>
      <td>
        {forgejo && (
          <div className={styles.token}>
            <span className={styles.muted}>{tokenStatus(host)}</span>
            <Button size="small" onClick={() => onSetToken(host.host)}>
              Set token
            </Button>
            {host.token === "file" && (
              <Button size="small" onClick={() => void dispatch(setToken({ host: host.host, token: "" }))}>
                Remove token
              </Button>
            )}
          </div>
        )}
      </td>
      <td className={styles.muted}>{accountOf(host)}</td>
      <td>
        <div className={styles.order}>
          {!first && (
            <IconButton size="small" aria-label={`Move ${host.host} up`} onClick={() => onMove(-1)}>
              ↑
            </IconButton>
          )}
          {!last && (
            <IconButton size="small" aria-label={`Move ${host.host} down`} onClick={() => onMove(1)}>
              ↓
            </IconButton>
          )}
          <IconButton size="small" aria-label={`Remove ${host.host}`} onClick={onRemove}>
            ×
          </IconButton>
        </div>
      </td>
    </tr>
  );
};

interface OtherHostRowProps {
  readonly host: HostView;
  readonly onAdd: () => void;
}

const OtherHostRow = ({ host, onAdd }: OtherHostRowProps): JSX.Element => (
  <tr>
    <td className={styles.host}>{host.host}</td>
    <td className={styles.muted}>{`Auto (detected: ${detectedName(host.forge)})`}</td>
    <td className={styles.muted}>{accountOf(host)}</td>
    <td>
      <Button size="small" onClick={onAdd}>
        {`Add ${host.host}`}
      </Button>
    </td>
  </tr>
);

interface AddHostProps {
  readonly configured: readonly ConfiguredHost[];
  readonly onAdd: (host: ConfiguredHost) => void;
}

const AddHost = ({ configured, onAdd }: AddHostProps): JSX.Element => {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ForgeChoice>("auto");
  const candidate = name.trim().toLowerCase();
  const problem = candidate === "" ? "" : hostProblem(candidate, configured);
  const add = (): void => {
    if (candidate === "" || problem !== "") {
      return;
    }
    onAdd({ name: candidate, kind });
    setName("");
  };
  return (
    <div className={styles.add}>
      <TextField
        size="small"
        value={name}
        placeholder="host or host:port"
        error={problem !== ""}
        helperText={problem === "" ? undefined : problem}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add();
          }
        }}
        slotProps={{ htmlInput: { "aria-label": "New host" } }}
      />
      <TextField
        select
        size="small"
        value={kind}
        onChange={(e) => setKind(e.target.value as ForgeChoice)}
        slotProps={{ htmlInput: { "aria-label": "Forge for new host" } }}
      >
        {choices.map((choice) => (
          <MenuItem key={choice.value} value={choice.value}>
            {choice.label}
          </MenuItem>
        ))}
      </TextField>
      <Button size="small" disabled={candidate === ""} onClick={add}>
        Add
      </Button>
    </div>
  );
};

interface HostListsProps {
  readonly hosts: readonly HostView[];
  readonly onSetToken: (host: string) => void;
  readonly onSave: (next: readonly ConfiguredHost[]) => void;
}

/** The configured hosts, the cached hosts that aren't configured, and the form that adds one. */
const HostLists = ({ hosts, onSetToken, onSave }: HostListsProps): JSX.Element => {
  const configured = configuredHosts(hosts);
  const configuredViews = hosts.filter((host) => host.configured);
  const others = hosts.filter((host) => !host.configured);
  return (
    <>
      <table className={styles.table} aria-label="Hosts">
        <thead>
          <tr>
            <th>Host</th>
            <th>Forge</th>
            <th>Token</th>
            <th>Account</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {configuredViews.map((host, index) => (
            <HostRow
              key={host.host}
              host={host}
              first={index === 0}
              last={index === configuredViews.length - 1}
              onSetToken={onSetToken}
              onKind={(kind) => onSave(configured.map((item) => (item.name === host.host ? { ...item, kind } : item)))}
              onMove={(by) => onSave(moved(configured, index, by))}
              onRemove={() => onSave(configured.filter((_, i) => i !== index))}
            />
          ))}
        </tbody>
      </table>
      {others.length > 0 && (
        <table className={styles.table} aria-label="Other hosts">
          <thead>
            <tr>
              <th>Host</th>
              <th>Forge</th>
              <th>Account</th>
              <th>Add</th>
            </tr>
          </thead>
          <tbody>
            {others.map((host) => (
              <OtherHostRow key={host.host} host={host} onAdd={() => onSave([...configured, { name: host.host, kind: "auto" }])} />
            ))}
          </tbody>
        </table>
      )}
      <p className={styles.caption}>
        Short specs such as owner/name are tried on these hosts in order; the first is the primary. Saved in config.toml as [[hosts]]. GitHub
        hosts use gh&apos;s login. Forgejo tokens come from {"WS_FORGEJO_TOKEN_<HOST>"} or from the token saved here, which stays on this
        machine and is never shown again.
      </p>
      <AddHost configured={configured} onAdd={(host) => onSave([...configured, host])} />
    </>
  );
};

/** Settings › Forges: the ordered hosts in config.toml, their forges, and the token a Forgejo host reads. */
export const ForgesGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const view = useAppSelector((s) => s.reviews.view);
  const fetchError = useAppSelector((s) => s.reviews.error);
  const [tokenHost, setTokenHost] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const needsLoad = view === null;
  useEffect(() => {
    if (needsLoad) {
      void dispatch(fetchReviews());
    }
  }, [dispatch, needsLoad]);
  const saveHosts = async (next: readonly ConfiguredHost[]): Promise<void> => {
    setSaveError(null);
    const result = await dispatch(setHosts(next));
    if (setHosts.fulfilled.match(result)) {
      void dispatch(fetchConfig());
      return;
    }
    setSaveError(result.payload?.message ?? result.error.message ?? "saving the hosts failed");
  };
  return (
    <section className={styles.group} aria-label="Forges">
      <h1 className={styles.title}>Forges</h1>
      {view === null ? (
        fetchError === null ? (
          <CircularProgress size={20} aria-label="Loading forges" />
        ) : (
          <Alert severity="error">{fetchError.message}</Alert>
        )
      ) : (
        <>
          {saveError !== null && <Alert severity="error">{saveError}</Alert>}
          <HostLists hosts={view.hosts} onSetToken={setTokenHost} onSave={(next) => void saveHosts(next)} />
        </>
      )}
      {tokenHost !== null && <TokenDialog host={tokenHost} onClose={() => setTokenHost(null)} />}
    </section>
  );
};
