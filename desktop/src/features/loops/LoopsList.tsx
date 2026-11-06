import type { JSX } from "react";
import { useEffect } from "react";
import type { Loop } from "../../api";
import { until } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchLoops, loopSelected } from "./loopsSlice";
import styles from "./LoopsList.module.css";

/** How often the list is refreshed while it's shown. */
export const loopsPollInterval = 10_000;

const nextText = (loop: Loop): string => {
  const next = until(loop.nextRunAt);
  return next === "" ? "—" : `next ${next}`;
};

/** The agent loops, one row each, refreshed while shown. */
export const LoopsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const loops = useAppSelector((s) => s.loops.loops);
  const selected = useAppSelector((s) => s.loops.selected);
  const error = useAppSelector((s) => s.loops.error);
  useEffect(() => {
    void dispatch(fetchLoops());
    const timer = window.setInterval(() => void dispatch(fetchLoops()), loopsPollInterval);
    return () => window.clearInterval(timer);
  }, [dispatch]);
  return (
    <div className={styles.root}>
      {error !== null && (
        <p className={styles.error} role="status">
          {error.message}
        </p>
      )}
      {loops.length === 0 ? (
        <p className={styles.empty}>No loops yet.</p>
      ) : (
        <ul className={styles.list} aria-label="Loops">
          {loops.map((loop) => (
            <li key={loop.name}>
              <button
                type="button"
                className={styles.item}
                aria-current={loop.name === selected ? "true" : undefined}
                onClick={() => dispatch(loopSelected(loop.name))}
              >
                <span className={styles.name}>{loop.name}</span>
                <span className={styles.meta}>
                  every {loop.every}
                  {loop.paused && <span className={styles.paused}> · Paused</span>}
                </span>
                {!loop.paused && <span className={styles.next}>{nextText(loop)}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};
