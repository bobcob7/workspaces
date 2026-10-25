import { Alert, Button, CircularProgress } from "@mui/material";
import { useState, type JSX } from "react";
import type { CachedRepo } from "../../api";
import { age, formatSize } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { EntryRow } from "../agents/EntryRow";
import { dialogOpened, noticeShown } from "../ui/uiSlice";
import { RefsTable } from "./RefsTable";
import { RemoveRepoDialog } from "./RemoveRepoDialog";
import { nameOf, ownerOf, specFor, workspacesWith } from "./repos";
import { addOpened, fetchRepo, openInAgents, tabChosen, type RepoTab } from "./reposSlice";
import { reviewsSlot } from "./reviewsSlot";
import { SearchBox } from "./SearchBox";
import styles from "./ReposPane.module.css";

const tabLabels: Readonly<Record<RepoTab, string>> = { branches: "Branches", tags: "Tags", workspaces: "Workspaces" };

const tabs: readonly RepoTab[] = ["branches", "tags", "workspaces"];

const matches = (text: string, filter: string): boolean => text.toLowerCase().includes(filter.trim().toLowerCase());

const RepoDetail = ({ repo }: { readonly repo: CachedRepo }): JSX.Element => {
  const dispatch = useAppDispatch();
  const refsState = useAppSelector((s) => s.repos.refs[repo.id]);
  const fetching = useAppSelector((s) => s.repos.fetching.includes(repo.id) || s.repos.fetchingAll);
  const fetchError = useAppSelector((s) => s.repos.fetchErrors[repo.id]);
  const removing = useAppSelector((s) => s.repos.removing.includes(repo.id));
  const tab = useAppSelector((s) => s.repos.tab);
  const entries = useAppSelector((s) => s.agents.entries);
  const [filter, setFilter] = useState("");
  const [confirming, setConfirming] = useState(false);
  const refs = refsState?.listing?.refs ?? [];
  const branches = refs.filter((r) => r.kind === "branch");
  const tags = refs.filter((r) => r.kind === "tag");
  const workspaces = workspacesWith(entries, repo.id);
  const counts: Readonly<Record<RepoTab, number>> = {
    branches: refsState?.listing === null || refsState === undefined ? repo.branches : branches.length,
    tags: refsState?.listing === null || refsState === undefined ? repo.tags : tags.length,
    workspaces: workspaces.length,
  };
  const Card = reviewsSlot.RepoReviewsCard;
  const copyPath = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(repo.path);
      dispatch(noticeShown({ severity: "info", message: "Copied the cache path" }));
    } catch {
      dispatch(noticeShown({ severity: "error", message: "Couldn't copy the cache path" }));
    }
  };
  return (
    <section className={styles.root} aria-label={repo.id}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>
            <span className={styles.prefix}>{ownerOf(repo.id)}/</span>
            {nameOf(repo.id)}
          </h1>
          <span className={styles.spacer} />
          <Button
            size="small"
            disabled={fetching}
            startIcon={fetching ? <CircularProgress size={12} color="inherit" aria-label="Fetching" /> : undefined}
            onClick={() => void dispatch(fetchRepo(repo.id))}
          >
            Fetch
          </Button>
          <Button size="small" onClick={() => dispatch(dialogOpened("agent", [specFor(repo.id)]))}>
            New agent
          </Button>
          <Button size="small" onClick={() => dispatch(dialogOpened("shell", [specFor(repo.id)]))}>
            New shell
          </Button>
          <Button size="small" color="error" disabled={removing} onClick={() => setConfirming(true)}>
            Remove
          </Button>
        </div>
        <div className={styles.meta}>
          <button
            type="button"
            className={styles.upstream}
            title={`${repo.path}\nClick to copy the cache path`}
            onClick={() => void copyPath()}
          >
            {repo.upstream}
          </button>
          <span>·</span>
          <span>
            default <span className={styles.mono}>{repo.defaultBranch}</span>
          </span>
          <span>·</span>
          <span>{formatSize(repo.sizeBytes)}</span>
          <span>·</span>
          <span>{fetching ? "fetching…" : `fetched ${age(repo.lastFetchedAt)}`}</span>
        </div>
      </header>
      <div className={styles.content}>
        {Card !== undefined && <Card repoId={repo.id} />}
        {fetchError !== undefined && (
          <Alert severity="error">
            Fetching {nameOf(repo.id)} failed: {fetchError.message}
          </Alert>
        )}
        {refsState?.error != null && <Alert severity="error">{refsState.error.message}</Alert>}
        <div>
          <div className={styles.tabs} role="tablist" aria-label="Refs">
            {tabs.map((t) => (
              <button
                key={t}
                type="button"
                role="tab"
                aria-selected={t === tab}
                className={styles.tab}
                onClick={() => dispatch(tabChosen(t))}
              >
                {tabLabels[t]}
                <span className={styles.n}>{counts[t]}</span>
              </button>
            ))}
            <SearchBox compact label={`Filter ${tabLabels[tab].toLowerCase()}`} value={filter} onChange={setFilter} />
          </div>
          {tab === "workspaces" ? (
            <ul className={styles.workspaces} aria-label="Workspaces">
              {workspaces.length === 0 && <li className={styles.none}>No workspace has this repo.</li>}
              {workspaces
                .filter((e) => matches(e.name, filter))
                .map((entry) => (
                  <li key={entry.key}>
                    <EntryRow entry={entry} current={false} onClick={() => void dispatch(openInAgents(entry.key))} />
                  </li>
                ))}
            </ul>
          ) : (
            <RefsTable
              repoId={repo.id}
              kind={tab === "branches" ? "branch" : "tag"}
              refs={(tab === "branches" ? branches : tags).filter((r) => matches(r.name, filter))}
              defaultBranch={repo.defaultBranch}
              entries={entries}
            />
          )}
          {tab === "tags" && refsState?.listing != null && tags.length === 0 && (
            <p className={styles.none}>No tags.</p>
          )}
        </div>
      </div>
      <RemoveRepoDialog repo={repo} workspaces={workspaces} open={confirming} onClose={() => setConfirming(false)} />
    </section>
  );
};

/** The selected repo's detail, or the section's empty and error states. */
export const ReposPane = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const repo = useAppSelector((s) => s.repos.repos.find((r) => r.id === s.repos.selectedId));
  const loaded = useAppSelector((s) => s.repos.loaded);
  const count = useAppSelector((s) => s.repos.repos.length);
  const error = useAppSelector((s) => s.repos.error);
  if (repo !== undefined) {
    return <RepoDetail key={repo.id} repo={repo} />;
  }
  if (error !== null) {
    return (
      <div className={styles.empty}>
        <Alert severity="error">
          {error.message}
          {error.code === "ws_missing" && <div>Install ws (make build in this repo) and put it on your PATH.</div>}
        </Alert>
      </div>
    );
  }
  if (loaded && count === 0) {
    return (
      <div className={styles.empty}>
        <div className={styles.teach}>
          <p>Repos you use in workspaces are cached here so new workspaces start fast. Add one to get started.</p>
          <Button variant="contained" onClick={() => dispatch(addOpened())}>
            Add repo
          </Button>
        </div>
      </div>
    );
  }
  return <div className={styles.empty}>{loaded ? "Select a repo to see its branches." : ""}</div>;
};
