import type { JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { statsWindows, windowChosen, windowLabels } from "./statsSlice";
import styles from "./StatsList.module.css";

/** The windows the Stats section shows, in list order. */
export const StatsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const current = useAppSelector((s) => s.stats.window);
  return (
    <nav className={styles.root} aria-label="Stats windows">
      <ul className={styles.list}>
        {statsWindows.map((option) => (
          <li key={option}>
            <button
              type="button"
              className={styles.item}
              aria-current={option === current ? "page" : undefined}
              onClick={() => dispatch(windowChosen(option))}
            >
              {windowLabels[option]}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
};
