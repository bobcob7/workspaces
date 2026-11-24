import { Button, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, Select, Switch } from "@mui/material";
import { useEffect, type JSX } from "react";
import type { RemoteEnvironment, WatchRepo } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen } from "../ui/uiSlice";
import { loadRepos, setWatch, watchedClosed } from "./reviewsSlice";
import styles from "./WatchedReposDialog.module.css";

const RepoRow = ({ repo, environments }: { readonly repo: WatchRepo; readonly environments: readonly RemoteEnvironment[] }): JSX.Element => {
  const dispatch = useAppDispatch();
  const environment = repo.environment ?? "local";
  if (repo.forge === null) {
    return (
      <tr className={styles.other}>
        <td className={styles.id}>{repo.id}</td>
        <td colSpan={environments.length > 0 ? 3 : 2} className={styles.note}>
          Choose its forge in Settings › Forges
        </td>
      </tr>
    );
  }
  return (
    <tr>
      <td className={styles.id}>{repo.id}</td>
      {environments.length > 0 && (
        <td>
          <Select
            size="small"
            value={environment}
            SelectDisplayProps={{ "aria-label": `Environment for ${repo.id}` }}
            onChange={(e) => void dispatch(setWatch({ repo: repo.id, watch: repo.watch, autoReview: repo.autoReview, environment: e.target.value }))}
          >
            <MenuItem value="local">local</MenuItem>
            {environments.map((env) => (
              <MenuItem key={env.name} value={env.name}>
                {env.status === "online" ? env.name : `${env.name} (offline)`}
              </MenuItem>
            ))}
          </Select>
        </td>
      )}
      <td className={styles.switch}>
        <Switch
          checked={repo.watch}
          slotProps={{ input: { "aria-label": `Watch ${repo.id}` } }}
          onChange={(e) => void dispatch(setWatch({ repo: repo.id, watch: e.target.checked, autoReview: repo.autoReview, environment }))}
        />
      </td>
      <td className={styles.switch}>
        <Switch
          checked={repo.autoReview}
          disabled={!repo.watch}
          slotProps={{ input: { "aria-label": `Auto-review ${repo.id}` } }}
          onChange={(e) => void dispatch(setWatch({ repo: repo.id, watch: repo.watch, autoReview: e.target.checked, environment }))}
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
  const items = useAppSelector((s) => s.environments.items);
  const environments = items.filter((env) => env.state === "enrolled");
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
                {environments.length > 0 && <th>Environment</th>}
                <th>Watch</th>
                <th>Auto-review</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((repo) => (
                <RepoRow key={repo.id} repo={repo} environments={environments} />
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
