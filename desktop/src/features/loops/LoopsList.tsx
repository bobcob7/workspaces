import type { JSX } from "react";
import { useEffect, useState } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { EnvFilter } from "../agents/EnvFilter";
import { LoopGroup } from "./LoopGroup";
import { fetchLoops, loopSelected, selectLoopGroups, type LoopGroup as LoopGroupData } from "./loopsSlice";
import styles from "./LoopsList.module.css";

/** How often the list is refreshed while it's shown. */
export const loopsPollInterval = 10_000;

const sameGroups = (a: readonly LoopGroupData[], b: readonly LoopGroupData[]): boolean => JSON.stringify(a) === JSON.stringify(b);

/** The agent loops of this machine and of each enrolled environment, grouped, refreshed while shown. */
export const LoopsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const groups = useAppSelector((s) => selectLoopGroups(s), sameGroups);
  const environments = useAppSelector((s) => s.environments.items.filter((env) => env.state === "enrolled"));
  const selected = useAppSelector((s) => s.loops.selected);
  const selectedEnvironment = useAppSelector((s) => s.loops.selectedEnvironment);
  const errors = useAppSelector((s) =>
    Object.entries(s.loops.byEnvironment).flatMap(([name, held]) => (held.error === null ? [] : [{ name, message: held.error }])),
    (a, b) => JSON.stringify(a) === JSON.stringify(b),
  );
  const [filter, setFilter] = useState<string | null>(null);
  useEffect(() => {
    void dispatch(fetchLoops());
    const timer = window.setInterval(() => void dispatch(fetchLoops()), loopsPollInterval);
    return () => window.clearInterval(timer);
  }, [dispatch]);
  const grouped = groups.length > 1;
  const shown = grouped && filter !== null ? groups.filter((g) => g.environment === filter) : groups;
  const local = groups[0];
  return (
    <div className={styles.root}>
      {grouped && <EnvFilter environments={environments} value={filter} onChange={setFilter} />}
      {errors.map(({ name, message }) => (
        <p key={name} className={styles.error} role="status">
          {grouped ? `${name}: ${message}` : message}
        </p>
      ))}
      {!grouped && local !== undefined && local.items.length === 0 ? (
        <p className={styles.empty}>No loops yet.</p>
      ) : (
        <div className={grouped ? styles.groups : styles.plain}>
          {shown.map((group) => (
            <LoopGroup
              key={group.environment}
              group={group}
              heading={grouped}
              selected={group.environment === selectedEnvironment ? selected : null}
              onSelect={(name, environment) => dispatch(loopSelected(name, environment))}
            />
          ))}
        </div>
      )}
    </div>
  );
};
