import { Alert, Button, CircularProgress } from "@mui/material";
import { useEffect, type JSX } from "react";
import type { ToolVersion } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchVersions } from "./settingsSlice";
import styles from "./SettingsPane.module.css";

/**
 * The commit a version names: the hash `git describe` ends with
 * (`v0.3.0-4-gdc313a2`, `dc313a2`), or a Go pseudo-version's
 * (`v0.0.0-20261028223004-c9fbb6607371`), shortened to 7, or null.
 */
export const commitOf = (version: string | null): string | null => {
  const m = /(?:^|-g|-)([0-9a-f]{7,40})(?:[-+]dirty)?$/.exec(version ?? "");
  return m?.[1]?.slice(0, 7) ?? null;
};

const Value = ({ children }: { readonly children: string }): JSX.Element => <span className={styles.mono}>{children}</span>;

const ToolRow = ({ name, tool }: { readonly name: string; readonly tool: ToolVersion }): JSX.Element => (
  <div className={styles.row} role="group" aria-label={name}>
    <div className={styles.text}>
      <span className={styles.label}>{name}</span>
      <span className={styles.help}>{tool.path ?? "Not found on the app's PATH"}</span>
    </div>
    <div className={styles.control}>
      {tool.version !== null ? <Value>{tool.version}</Value> : <span className={styles.error}>{tool.error ?? "unknown"}</span>}
    </div>
  </div>
);

/** Settings > About: what the app is, and which ws and claude it runs. */
export const AboutGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const versions = useAppSelector((s) => s.settings.versions);
  const error = useAppSelector((s) => s.settings.versionsError);
  useEffect(() => {
    void dispatch(fetchVersions());
  }, [dispatch]);
  const appCommit = commitOf(versions?.app.commit ?? null);
  const wsCommit = commitOf(versions?.ws.version ?? null);
  const mismatch = appCommit !== null && wsCommit !== null && appCommit !== wsCommit;
  return (
    <section className={styles.group} aria-label="About">
      <h1 className={styles.title}>About</h1>
      {error !== null && <Alert severity="error">{error.message}</Alert>}
      {versions === null && error === null && <CircularProgress size={20} aria-label="Reading versions" />}
      {versions !== null && (
        <>
          <div className={styles.row} role="group" aria-label="Workspaces app">
            <div className={styles.text}>
              <span className={styles.label}>Workspaces app</span>
              <span className={styles.help}>{versions.app.commit === null ? "Built outside a git checkout" : "Built from"}</span>
            </div>
            <div className={styles.control}>
              <Value>{versions.app.version}</Value>
              {versions.app.commit !== null && <Value>{versions.app.commit}</Value>}
            </div>
          </div>
          <ToolRow name="ws" tool={versions.ws} />
          <ToolRow name="claude" tool={versions.claude} />
          {mismatch && (
            <Alert severity="warning">
              ws ({wsCommit}) and the app ({appCommit}) were built from different commits. Rebuild the older one: make build
              for ws, make desktop for the app.
            </Alert>
          )}
          <div>
            <Button size="small" onClick={() => void dispatch(fetchVersions())}>
              Check again
            </Button>
          </div>
        </>
      )}
    </section>
  );
};
