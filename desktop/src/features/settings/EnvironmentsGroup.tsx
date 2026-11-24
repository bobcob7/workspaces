import { Alert, Button } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { RemoteEnvironment } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { statusIsOld } from "../environments/environments";
import { fetchEnvironments } from "../environments/environmentsSlice";
import { AddEnvironmentDialog } from "./AddEnvironmentDialog";
import { CaCard } from "./CaCard";
import { EnvironmentSettingsCard } from "./EnvironmentSettingsCard";
import { RemoveEnvironmentDialog } from "./RemoveEnvironmentDialog";
import styles from "./EnvironmentsGroup.module.css";

const pad = (n: number): string => String(n).padStart(2, "0");

const clockOf = (iso: string): string => {
  const at = new Date(iso);
  return Number.isNaN(at.getTime()) ? iso : `${pad(at.getHours())}:${pad(at.getMinutes())}`;
};

/** The status text of a row: `online`, `offline since 14:02 (last seen)`, `waiting for enrollment`. */
export const statusText = (env: RemoteEnvironment): string => {
  if (env.status === "online") {
    return "online";
  }
  if (env.status === "pending" || env.state === "pending") {
    return "waiting for enrollment";
  }
  if (env.status === "unknown") {
    return "offline (the ws daemon isn't running)";
  }
  return env.lastSeen === null ? "offline" : `offline since ${clockOf(env.lastSeen)} (last seen)`;
};

const dotOf = (env: RemoteEnvironment): { readonly glyph: string; readonly className: string } => {
  if (env.status === "online") {
    return { glyph: "●", className: styles.online ?? "" };
  }
  if (env.status === "pending" || env.state === "pending") {
    return { glyph: "◌", className: styles.pending ?? "" };
  }
  return { glyph: "○", className: styles.offline ?? "" };
};

const factsOf = (env: RemoteEnvironment): string => {
  const hello = env.hello;
  const parts: string[] = [];
  if (hello !== null) {
    parts.push(`ws ${hello.wsVersion}`, `${hello.os}/${hello.arch}`);
    parts.push(hello.claude === "missing" ? "claude: missing" : `claude ${hello.claudeVersion ?? ""}`.trim());
  }
  if (env.workspaces !== null) {
    parts.push(`${env.workspaces} workspace${env.workspaces === 1 ? "" : "s"}`);
  }
  return parts.join(" · ");
};

interface RowProps {
  readonly env: RemoteEnvironment;
  readonly onRotate: () => void;
  readonly onRemove: () => void;
}

const EnvironmentRow = ({ env, onRotate, onRemove }: RowProps): JSX.Element => {
  const dot = dotOf(env);
  const pending = env.state === "pending";
  return (
    <li className={styles.item} aria-label={env.name}>
      <span className={`${styles.dot} ${dot.className}`} aria-hidden="true">
        {dot.glyph}
      </span>
      <span className={styles.name}>{env.name}</span>
      <span>{statusText(env)}</span>
      <span className={styles.facts}>{factsOf(env)}</span>
      <span className={styles.buttons}>
        <Button size="small" aria-label={`Rotate ${env.name}`} onClick={onRotate}>
          Rotate
        </Button>
        <Button size="small" color="error" aria-label={`${pending ? "Cancel" : "Remove"} ${env.name}`} onClick={onRemove}>
          {pending ? "Cancel" : "Remove"}
        </Button>
      </span>
      {env.hello?.claude === "missing" && <span className={styles.hint}>Install Claude Code on this machine, then try again.</span>}
    </li>
  );
};

type Dialog = { readonly kind: "add" } | { readonly kind: "rotate"; readonly name: string } | { readonly kind: "remove"; readonly env: RemoteEnvironment };

/** Settings › Environments: the machines this one controls, enrolling them, and the CA and keys behind that (ADRs 0072, 0075). */
export const EnvironmentsGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const { items, loadedAt, error, warnings } = useAppSelector((s) => s.environments);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  useEffect(() => {
    void dispatch(fetchEnvironments());
  }, [dispatch]);
  return (
    <section className={styles.group} aria-label="Environments">
      <h1 className={styles.title}>Environments</h1>
      <p className={styles.help}>Other machines whose workspaces and agents show up here. Each one runs <code>ws env agent run</code>.</p>
      {error !== null && <Alert severity="error">{error}</Alert>}
      {warnings.map((warning) => (
        <Alert key={warning} severity="warning">
          {warning}
        </Alert>
      ))}
      {loadedAt !== null && statusIsOld(loadedAt) && (
        <Alert severity="warning">Environment status as of {clockOf(new Date(loadedAt).toISOString())}</Alert>
      )}
      <ul className={styles.list} aria-label="Environments list">
        <li className={styles.item} aria-label="local">
          <span className={`${styles.dot} ${styles.online}`} aria-hidden="true">
            ●
          </span>
          <span className={styles.name}>local</span>
          <span className={styles.facts}>this machine</span>
        </li>
        {items.map((env) => (
          <EnvironmentRow
            key={env.id}
            env={env}
            onRotate={() => setDialog({ kind: "rotate", name: env.name })}
            onRemove={() => setDialog({ kind: "remove", env })}
          />
        ))}
      </ul>
      <div className={styles.buttons}>
        <Button size="small" variant="outlined" onClick={() => setDialog({ kind: "add" })}>
          Add environment
        </Button>
      </div>
      <CaCard />
      <EnvironmentSettingsCard />
      {dialog?.kind === "add" && <AddEnvironmentDialog onClose={() => setDialog(null)} />}
      {dialog?.kind === "rotate" && <AddEnvironmentDialog rotate={dialog.name} onClose={() => setDialog(null)} />}
      {dialog?.kind === "remove" && <RemoveEnvironmentDialog environment={dialog.env} onClose={() => setDialog(null)} />}
    </section>
  );
};
