import type { JSX } from "react";
import type { Loop } from "../../api";
import { until } from "../../format";
import { EnvBadge } from "../agents/EnvBadge";
import { loopLabel } from "./formValues";
import type { LoopGroup as LoopGroupData } from "./loopsSlice";
import listStyles from "./LoopsList.module.css";
import styles from "./LoopGroup.module.css";

const nextText = (loop: Loop): string => {
  const next = until(loop.nextRunAt);
  return next === "" ? "—" : `next ${next}`;
};

export interface LoopGroupProps {
  readonly group: LoopGroupData;
  /** Shown above the loops; false for the plain list when no environment is enrolled. */
  readonly heading: boolean;
  readonly selected: string | null;
  readonly onSelect: (name: string, environment: string) => void;
}

/** One environment's loops: a heading with its state, a note when it can't be used, and a row per loop. */
export const LoopGroup = ({ group, heading, selected, onSelect }: LoopGroupProps): JSX.Element => {
  const { environment, online, reason, stale, loopsUnknown, items } = group;
  const remote = environment !== "local";
  const list = (
    <ul className={heading ? styles.items : listStyles.list} aria-label={heading ? undefined : "Loops"}>
      {items.map((loop) => (
        <li key={loop.name}>
          <button
            type="button"
            className={listStyles.item}
            aria-current={loop.name === selected ? "true" : undefined}
            onClick={() => onSelect(loop.name, environment)}
          >
            <span className={listStyles.name}>{loopLabel(loop)}</span>
            <span className={listStyles.meta}>
              every {loop.every}
              {loop.paused && <span className={listStyles.paused}> · Paused</span>}
            </span>
            {!loop.paused && <span className={listStyles.next}>{nextText(loop)}</span>}
          </button>
        </li>
      ))}
    </ul>
  );
  if (!heading) {
    return list;
  }
  return (
    <section role="group" aria-label={`${environment} loops`} className={styles.group} data-offline={!online}>
      <h2 className={styles.heading}>
        <span aria-hidden="true">{online ? "●" : "○"}</span>
        {environment}
        {remote && stale && online && <EnvBadge label="stale" warn />}
      </h2>
      {!online && <p className={styles.note}>{reason}</p>}
      {!online && (
        <p className={styles.note}>{`Loops on ${environment} don't run while it is offline; missed runs are skipped, not made up`}</p>
      )}
      {remote && loopsUnknown && online && (
        <p className={styles.note}>{`${environment} runs an older ws: its loops can't run from here`}</p>
      )}
      {items.length === 0 && !loopsUnknown ? <p className={styles.empty}>No loops yet.</p> : list}
    </section>
  );
};
