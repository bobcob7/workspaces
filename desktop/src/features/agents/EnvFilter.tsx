import type { JSX } from "react";
import type { RemoteEnvironment } from "../../api";
import styles from "./EnvFilter.module.css";

export interface EnvFilterProps {
  readonly environments: readonly RemoteEnvironment[];
  /** The chosen environment, `local` or a name; null for all. */
  readonly value: string | null;
  readonly onChange: (environment: string | null) => void;
}

const dots: Readonly<Record<RemoteEnvironment["status"], string>> = {
  online: "●",
  offline: "○",
  unknown: "○",
  pending: "◌",
};

interface ChipProps {
  readonly label: string;
  readonly dot?: string;
  readonly status?: string;
  readonly pressed: boolean;
  readonly onClick: () => void;
}

const Chip = ({ label, dot, status, pressed, onClick }: ChipProps): JSX.Element => (
  <button type="button" className={styles.chip} aria-pressed={pressed} title={status} onClick={onClick}>
    {dot !== undefined && (
      <span aria-hidden="true" data-status={status}>
        {dot}
      </span>
    )}
    {label}
  </button>
);

/** The Agents list's environment chips: All, this machine, and each environment with its status dot (ADR 0075). */
export const EnvFilter = ({ environments, value, onChange }: EnvFilterProps): JSX.Element => (
  <div role="group" aria-label="Environment" className={styles.row}>
    <Chip label="All" pressed={value === null} onClick={() => onChange(null)} />
    <Chip label="local" dot="●" status="online" pressed={value === "local"} onClick={() => onChange("local")} />
    {environments.map((env) => (
      <Chip
        key={env.name}
        label={env.name}
        dot={dots[env.status]}
        status={env.status}
        pressed={value === env.name}
        onClick={() => onChange(env.name)}
      />
    ))}
  </div>
);
