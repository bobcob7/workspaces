import { Alert, Button, CircularProgress } from "@mui/material";
import { useEffect, type JSX } from "react";
import type { ConfigListing, Environment, OnlineSettings, ToolVersion } from "../../api";
import { isWeb } from "../../platform";
import { logout } from "../../web/session";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchConfig, fetchEnvironment, fetchVersions } from "./settingsSlice";
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

const envHelp = "Set in the environment the app starts with; restart the app to change it.";

interface FactRowProps {
  readonly label: string;
  readonly value: string;
  readonly help?: string;
  readonly copy?: boolean;
}

/** A read-only fact, with a Copy button for a path. */
const FactRow = ({ label, value, help, copy = false }: FactRowProps): JSX.Element => (
  <div className={styles.row} role="group" aria-label={label}>
    <div className={styles.text}>
      <span className={styles.label}>{label}</span>
      {help !== undefined && <span className={styles.help}>{help}</span>}
    </div>
    <div className={styles.control}>
      <Value>{value}</Value>
      {copy && (
        <Button size="small" aria-label={`Copy ${label}`} onClick={() => void navigator.clipboard.writeText(value)}>
          Copy
        </Button>
      )}
    </div>
  </div>
);

interface LocationsProps {
  readonly config: ConfigListing;
  readonly environment: Environment;
}

const Locations = ({ config, environment }: LocationsProps): JSX.Element => (
  <>
    <h2 className={styles.subtitle}>Locations</h2>
    <FactRow label="ws config file" value={config.path} help="Change it with Settings or ws config set" copy />
    <FactRow label="App settings file" value={environment.stateFile ?? "Kept in memory only"} copy />
    <FactRow label="WS_CONFIG_DIR" value={environment.wsConfigDir ?? "not set (the platform default)"} help={envHelp} copy />
    <FactRow label="CLAUDE_CONFIG_DIR" value={environment.claudeConfigDir ?? "not set (~/.claude)"} help={envHelp} copy />
    <FactRow label="PATH" value={environment.path} help="The PATH the app runs ws and claude with" copy />
  </>
);

const yesNo = (value: boolean): string => (value ? "Yes" : "No");

const ServerRows = ({ online }: { readonly online: OnlineSettings }): JSX.Element => (
  <>
    <h2 className={styles.subtitle}>Server</h2>
    <p className={styles.help}>From WS_ONLINE_* variables; change them and restart the container.</p>
    <FactRow label="Username" value={online.username} />
    <FactRow label="Address" value={online.addr} />
    <FactRow label="Public URL" value={online.publicUrl ?? "Not set"} />
    <FactRow label="Secure cookie" value={yesNo(online.secureCookie)} />
    <FactRow label="Session length" value={`${online.sessionDays} days`} />
    <FactRow label="Trust proxy" value={yesNo(online.trustProxy)} />
    <FactRow label="Data directory" value={online.dataDir} />
    <FactRow label="Web directory" value={online.webDir} />
    <FactRow label="Push contact" value={online.vapidSubject} />
    <FactRow label="Password" value={online.password === "hash" ? "Set as a hash" : "Set in plain text"} />
  </>
);

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
  const config = useAppSelector((s) => s.settings.config);
  const configError = useAppSelector((s) => s.settings.configError);
  const environment = useAppSelector((s) => s.settings.environment);
  const environmentError = useAppSelector((s) => s.settings.environmentError);
  useEffect(() => {
    void dispatch(fetchVersions());
    void dispatch(fetchEnvironment());
    void dispatch(fetchConfig());
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
      {configError !== null && <Alert severity="error">{configError.message}</Alert>}
      {environmentError !== null && <Alert severity="error">{environmentError.message}</Alert>}
      {config !== null && environment !== null && <Locations config={config} environment={environment} />}
      {environment?.online && <ServerRows online={environment.online} />}
      {isWeb() && (
        <div>
          <Button size="small" onClick={() => void logout()}>
            Log out
          </Button>
        </div>
      )}
    </section>
  );
};
