import type { JSX } from "react";
import type { ScopeEnvironment, ScopeState } from "../../api";
import { EnvBadge } from "./EnvBadge";
import styles from "./ScopeBadges.module.css";

export interface ScopeBadgesProps {
  /** The environments an agent may start children on, each with its state (ADR 0082). */
  readonly scope: readonly ScopeEnvironment[];
}

const stateTitles: Readonly<Record<ScopeState, (name: string) => string>> = {
  online: (name) => `${name} is online`,
  offline: (name) => `${name} is offline; a child can't start there now`,
  removed: (name) => `${name} was removed with ws env rm; a child can't start there`,
  unknown: (name) => `${name}'s state is unknown`,
};

const scopeLabel = (environment: ScopeEnvironment): string =>
  environment.state === "online" ? environment.name : `${environment.name} · ${environment.state}`;

const scopeTitle = (environment: ScopeEnvironment): string => {
  const title = stateTitles[environment.state](environment.name);
  return environment.current ? `${title} (its own environment)` : title;
};

/** The environments an agent may start children on, each with its state (ADR 0082). */
export const ScopeBadges = ({ scope }: ScopeBadgesProps): JSX.Element => (
  <span className={styles.scope}>
    <span className={styles.label}>Scope</span>
    <ul aria-label="Environments it may start children on" className={styles.list}>
      {scope.map((environment) => (
        <li key={environment.id}>
          <EnvBadge
            label={scopeLabel(environment)}
            warn={environment.state !== "online"}
            title={scopeTitle(environment)}
          />
        </li>
      ))}
    </ul>
  </span>
);
