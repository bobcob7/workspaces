import { Button, Dialog, DialogActions, DialogContent, DialogTitle, Switch } from "@mui/material";
import { useEffect, type JSX } from "react";
import type { WatchRepo } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen } from "../ui/uiSlice";
import { loadRepos, setWatch, watchedClosed } from "./reviewsSlice";
import styles from "./WatchedReposDialog.module.css";

const RepoRow = ({ repo }: { readonly repo: WatchRepo }): JSX.Element => {
  const dispatch = useAppDispatch();
  if (!repo.github) {
    return (
      <tr className={styles.other}>
        <td className={styles.id}>{repo.id}</td>
        <td colSpan={2} className={styles.note}>
          GitHub repos only
        </td>
      </tr>
    );
  }
  return (
    <tr>
      <td className={styles.id}>{repo.id}</td>
      <td className={styles.switch}>
        <Switch
          checked={repo.watch}
          slotProps={{ input: { "aria-label": `Watch ${repo.id}` } }}
          onChange={(e) => void dispatch(setWatch({ repo: repo.id, watch: e.target.checked, autoReview: repo.autoReview }))}
        />
      </td>
      <td className={styles.switch}>
        <Switch
          checked={repo.autoReview}
          disabled={!repo.watch}
          slotProps={{ input: { "aria-label": `Auto-review ${repo.id}` } }}
          onChange={(e) => void dispatch(setWatch({ repo: repo.id, watch: repo.watch, autoReview: e.target.checked }))}
        />
      </td>
    </tr>
  );
};

/** Which cached repos are watched, and which auto-review. */
export const WatchedReposDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const open = useAppSelector((s) => s.reviews.watchedOpen);
  const view = useAppSelector((s) => s.reviews.view);
  const repos = view?.repos ?? [];
  useEffect(() => {
    if (open) {
      void dispatch(loadRepos());
    }
  }, [open, dispatch]);
  const sorted = repos.toSorted((a, b) => Number(b.github) - Number(a.github) || a.id.localeCompare(b.id));
  return (
    <Dialog open={open} onClose={() => dispatch(watchedClosed())} fullWidth maxWidth="sm">
      <DialogTitle>Watched repos</DialogTitle>
      <DialogContent>
        <p className={styles.intro}>
          Open pull requests in watched repos are listed in Reviews. Auto-review starts an agent for each new one that
          matches your review rules.
        </p>
        {sorted.length === 0 ? (
          <p className={styles.intro}>No cached repos. Repos are cached when a workspace first uses them.</p>
        ) : (
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Repo</th>
                <th>Watch</th>
                <th>Auto-review</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((repo) => (
                <RepoRow key={repo.id} repo={repo} />
              ))}
            </tbody>
          </table>
        )}
      </DialogContent>
      <DialogActions className={styles.actions}>
        <Button
          onClick={() => {
            dispatch(watchedClosed());
            dispatch(sectionChosen("settings"));
          }}
        >
          Review rules…
        </Button>
        <span className={styles.spacer} />
        <Button variant="contained" onClick={() => dispatch(watchedClosed())}>
          Done
        </Button>
      </DialogActions>
    </Dialog>
  );
};
