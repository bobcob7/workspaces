import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, TextField } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { ForgeChoice, ForgeKind, HostView } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchReviews, setForge, setToken } from "../reviews/reviewsSlice";
import dialogStyles from "./ProfileEditor.module.css";
import styles from "./ForgesGroup.module.css";

const choices: readonly { readonly value: ForgeChoice; readonly label: string }[] = [
  { value: "auto", label: "Auto" },
  { value: "github", label: "GitHub" },
  { value: "forgejo", label: "Forgejo" },
];

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
  readonly onSetToken: (host: string) => void;
}

const HostRow = ({ host, onSetToken }: HostRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const forgejo = host.forge === "forgejo";
  const account = host.error !== null ? <span className={styles.error}>{host.error.message}</span> : host.login === null ? null : `signed in as ${host.login}`;
  return (
    <tr>
      <td className={styles.host}>{host.host}</td>
      <td>
        <TextField
          select
          size="small"
          value={host.choice}
          onChange={(e) => void dispatch(setForge({ host: host.host, kind: e.target.value as ForgeChoice }))}
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
      <td className={styles.muted}>{account}</td>
    </tr>
  );
};

/** Settings › Forges: each host's forge, and the token a Forgejo host reads. */
export const ForgesGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const view = useAppSelector((s) => s.reviews.view);
  const fetchError = useAppSelector((s) => s.reviews.error);
  const [tokenHost, setTokenHost] = useState<string | null>(null);
  const needsLoad = view === null;
  useEffect(() => {
    if (needsLoad) {
      void dispatch(fetchReviews());
    }
  }, [dispatch, needsLoad]);
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
          <table className={styles.table} aria-label="Forges">
            <thead>
              <tr>
                <th>Host</th>
                <th>Forge</th>
                <th>Token</th>
                <th>Account</th>
              </tr>
            </thead>
            <tbody>
              {view.hosts.map((host) => (
                <HostRow key={host.host} host={host} onSetToken={setTokenHost} />
              ))}
            </tbody>
          </table>
          {view.hosts.length === 0 && <p className={styles.caption}>No hosts yet. Cache a repo to see its host here.</p>}
          <p className={styles.caption}>
            GitHub hosts use gh&apos;s login. Forgejo tokens come from {"WS_FORGEJO_TOKEN_<HOST>"} or from the token saved here,
            which stays on this machine and is never shown again.
          </p>
        </>
      )}
      {tokenHost !== null && <TokenDialog host={tokenHost} onClose={() => setTokenHost(null)} />}
    </section>
  );
};
