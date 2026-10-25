import { Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import { Fragment, type JSX } from "react";
import type { CachedRepo, Entry } from "../../api";
import { count, formatSize } from "../../format";
import { useAppDispatch } from "../../store";
import { nameOf, shortCachePath } from "./repos";
import { removeRepo } from "./reposSlice";
import { reviewsSlot } from "./reviewsSlot";
import styles from "./RepoDialogs.module.css";

export interface RemoveRepoDialogProps {
  readonly repo: CachedRepo;
  readonly workspaces: readonly Entry[];
  readonly open: boolean;
  readonly onClose: () => void;
}

const names = (workspaces: readonly Entry[]): JSX.Element[] =>
  workspaces.map((w, i) => (
    <Fragment key={w.key}>
      {i > 0 && (i === workspaces.length - 1 ? " and " : ", ")}
      <code>{w.name}</code>
    </Fragment>
  ));

/** Confirms ws cache remove, saying what is deleted and what keeps working. */
export const RemoveRepoDialog = ({ repo, workspaces, open, onClose }: RemoveRepoDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const Note = reviewsSlot.RemoveRepoReviewsNote;
  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>Remove {nameOf(repo.id)} from the cache?</DialogTitle>
      <DialogContent className={styles.prose}>
        <p>
          Deletes the cached copy at <code title={repo.path}>{shortCachePath(repo.path)}</code> ({formatSize(repo.sizeBytes)}).
          The next workspace that needs it clones it again from upstream.
        </p>
        {workspaces.length > 0 && (
          <p>
            Its {count(workspaces.length, "workspace")}, {names(workspaces)}, keep{workspaces.length === 1 ? "s" : ""} working:{" "}
            {workspaces.length === 1 ? "its repo doesn't" : "their repos don't"} depend on the cache's copy.
          </p>
        )}
        {Note !== undefined && (
          <p>
            <Note repoId={repo.id} />
          </p>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          color="error"
          variant="contained"
          onClick={() => {
            onClose();
            void dispatch(removeRepo(repo.id));
          }}
        >
          Remove
        </Button>
      </DialogActions>
    </Dialog>
  );
};
