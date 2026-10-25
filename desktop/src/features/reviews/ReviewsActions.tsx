import { Button, CircularProgress } from "@mui/material";
import type { JSX } from "react";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { watchedCount } from "./groups";
import { checkNow, watchedOpened } from "./reviewsSlice";
import styles from "./ReviewsActions.module.css";

const checkedAt = (ms: number | null): string => (ms === null ? "not checked yet" : `checked ${age(new Date(ms).toISOString())}`);

/** The Reviews section's top-bar actions: status, Check now, Watched repos. */
export const ReviewsActions = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const view = useAppSelector((s) => s.reviews.view);
  const watched = watchedCount(view);
  const checking = view?.checking === true;
  return (
    <>
      {checking && <CircularProgress size={14} aria-label="Checking" />}
      <span className={styles.status}>
        {watched === 0
          ? "No repos watched"
          : `${watched} repo${watched === 1 ? "" : "s"} watched · ${checking ? "checking…" : checkedAt(view?.checkedAtMs ?? null)}`}
      </span>
      {watched > 0 && (
        <Button size="small" disabled={checking} onClick={() => void dispatch(checkNow())}>
          Check now
        </Button>
      )}
      <Button size="small" variant="outlined" onClick={() => dispatch(watchedOpened())}>
        Watched repos
      </Button>
    </>
  );
};
