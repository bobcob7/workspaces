import { Button, CircularProgress } from "@mui/material";
import type { JSX } from "react";
import { count, formatSize } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { addOpened, fetchAllRepos } from "./reposSlice";
import styles from "./ReposActions.module.css";

/** The Repos section's top-bar actions: the cache's size, Fetch all, and Add repo. */
export const ReposActions = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const repos = useAppSelector((s) => s.repos.repos);
  const loaded = useAppSelector((s) => s.repos.loaded);
  const fetchingAll = useAppSelector((s) => s.repos.fetchingAll);
  const total = repos.reduce((sum, r) => sum + r.sizeBytes, 0);
  return (
    <>
      {loaded && repos.length > 0 && (
        <span className={styles.summary}>
          {count(repos.length, "repo")} · {formatSize(total)}
        </span>
      )}
      <Button
        size="small"
        disabled={fetchingAll || repos.length === 0}
        startIcon={fetchingAll ? <CircularProgress size={12} color="inherit" /> : undefined}
        onClick={() => void dispatch(fetchAllRepos())}
      >
        {fetchingAll ? "Fetching…" : "Fetch all"}
      </Button>
      <Button size="small" variant="contained" onClick={() => dispatch(addOpened())}>
        Add repo
      </Button>
    </>
  );
};
