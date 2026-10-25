import {
  Alert,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  LinearProgress,
  TextField,
} from "@mui/material";
import { useEffect, useState, type FormEvent, type JSX } from "react";
import type { AppError, Resolution } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { addClosed, addRepo, previewCleared, resolveSpec, type Preview } from "./reposSlice";
import { reviewsSlot } from "./reviewsSlot";
import styles from "./RepoDialogs.module.css";

/** How long typing pauses before the spec is resolved. */
export const resolveDelay = 400;

const hostOf = (id: string): string => id.split("/")[0] ?? id;

const sourceText = (r: Resolution): JSX.Element => {
  switch (r.source) {
    case "spec":
      return <>named in full</>;
    case "cache":
      return <>found in the cache</>;
    case "default":
      return (
        <>
          taken to be on {hostOf(r.id)} (<span className={styles.mono}>defaults.host</span>)
        </>
      );
    case "probe":
      return (
        <>
          found on {hostOf(r.id)} via <span className={styles.mono}>defaults.owners</span>
        </>
      );
  }
};

const candidatesOf = (error: AppError): readonly string[] => {
  const candidates = error.details?.["candidates"];
  return Array.isArray(candidates) ? candidates.filter((c): c is string => typeof c === "string") : [];
};

/** Codes that mean the spec can't be added as typed. */
const blocking: ReadonlySet<string> = new Set(["repo_not_found", "repo_ambiguous", "spec_invalid"]);

interface PreviewLineProps {
  readonly preview: Preview | null;
  readonly onPick: (id: string) => void;
}

const PreviewLine = ({ preview, onPick }: PreviewLineProps): JSX.Element | null => {
  if (preview === null) {
    return null;
  }
  if (preview.loading) {
    return <div className={styles.hint}>Resolving…</div>;
  }
  if (preview.resolution !== null) {
    const r = preview.resolution;
    return (
      <div className={styles.hint} data-testid="resolve-preview">
        <span className={styles.mono}>{r.id}</span> · {sourceText(r)} · {r.cached ? "already cached" : "not cached yet"}
      </div>
    );
  }
  if (preview.error === null) {
    return null;
  }
  const candidates = candidatesOf(preview.error);
  if (preview.error.code === "repo_ambiguous" && candidates.length > 0) {
    return (
      <div className={styles.hint}>
        Several repos match; pick one:
        <div className={styles.candidates}>
          {candidates.map((c) => (
            <Chip key={c} size="small" label={c} clickable onClick={() => onPick(c)} />
          ))}
        </div>
      </div>
    );
  }
  return (
    <div className={`${styles.hint} ${styles.error}`} role="alert">
      {preview.error.message}
    </div>
  );
};

/** Adds a repo to the cache, previewing what the spec resolves to as it's typed. */
export const AddRepoDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const open = useAppSelector((s) => s.repos.addOpen);
  const adding = useAppSelector((s) => s.repos.adding);
  const error = useAppSelector((s) => s.repos.addError);
  const preview = useAppSelector((s) => s.repos.preview);
  const [spec, setSpec] = useState("");
  const [watch, setWatch] = useState(true);
  const trimmed = spec.trim();
  useEffect(() => {
    if (trimmed === "") {
      dispatch(previewCleared());
      return undefined;
    }
    const timer = window.setTimeout(() => void dispatch(resolveSpec(trimmed)), resolveDelay);
    return () => window.clearTimeout(timer);
  }, [trimmed, dispatch]);
  useEffect(() => {
    if (!open) {
      setSpec("");
      setWatch(true);
    }
  }, [open]);
  const current = preview?.spec === trimmed ? preview : null;
  const blocked = current?.error != null && blocking.has(current.error.code);
  const Watch = reviewsSlot.AddRepoWatchSwitch;
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (trimmed === "" || blocked || adding) {
      return;
    }
    void dispatch(addRepo(Watch === undefined ? { spec: trimmed } : { spec: trimmed, watch }));
  };
  const close = (): void => {
    if (!adding) {
      dispatch(addClosed());
    }
  };
  return (
    <Dialog open={open} onClose={close} fullWidth maxWidth="sm">
      <form onSubmit={submit}>
        <DialogTitle>Add a repo to the cache</DialogTitle>
        <DialogContent className={styles.content}>
          <TextField
            label="Repo spec or URL"
            value={spec}
            onChange={(e) => setSpec(e.target.value)}
            placeholder="owner/repo, repo, or https://host/owner/repo.git"
            disabled={adding}
            autoFocus
            fullWidth
            slotProps={{ htmlInput: { className: styles.mono, spellCheck: false, autoCapitalize: "off" } }}
          />
          <PreviewLine preview={current} onPick={setSpec} />
          <Alert severity="info" icon={false} className={styles.info}>
            Cloning keeps a bare copy under the cache directory. Workspaces created from it start in under a second and
            work offline.
          </Alert>
          {Watch !== undefined && (
            <Watch repoId={current?.resolution?.id ?? null} checked={watch} onChange={setWatch} />
          )}
          {adding && <LinearProgress aria-label="Cloning" />}
          {error !== null && <Alert severity="error">{error.message}</Alert>}
        </DialogContent>
        <DialogActions>
          <Button onClick={close} disabled={adding}>
            Cancel
          </Button>
          <Button type="submit" variant="contained" disabled={trimmed === "" || blocked || adding}>
            {adding ? "Cloning…" : "Add repo"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
};
