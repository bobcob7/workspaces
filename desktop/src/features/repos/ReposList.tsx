import { useEffect, type JSX, type KeyboardEvent } from "react";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { SearchBox } from "./SearchBox";
import { filterRepos, groupRepos, listOrder, nameOf, repoSummary, workspacesWith } from "./repos";
import { fetchRepos, filterChanged, selectRepo } from "./reposSlice";
import { reviewsSlot } from "./reviewsSlot";
import styles from "./ReposList.module.css";

/** The cached repos, grouped by host/owner, with a filter box. Reads ws cache list when shown. */
export const ReposList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const repos = useAppSelector((s) => s.repos.repos);
  const loaded = useAppSelector((s) => s.repos.loaded);
  const filter = useAppSelector((s) => s.repos.filter);
  const selectedId = useAppSelector((s) => s.repos.selectedId);
  const fetching = useAppSelector((s) => s.repos.fetching);
  const fetchingAll = useAppSelector((s) => s.repos.fetchingAll);
  const entries = useAppSelector((s) => s.agents.entries);
  useEffect(() => {
    void dispatch(fetchRepos());
  }, [dispatch]);
  const groups = groupRepos(filterRepos(repos, filter));
  const Icons = reviewsSlot.RepoReviewsIcons;
  const onKeyDown = (event: KeyboardEvent<HTMLElement>): void => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") {
      return;
    }
    const order = listOrder(repos, filter);
    const at = order.findIndex((r) => r.id === selectedId);
    const next = order[event.key === "ArrowDown" ? Math.min(order.length - 1, at + 1) : Math.max(0, at - 1)];
    if (next === undefined) {
      return;
    }
    event.preventDefault();
    void dispatch(selectRepo(next.id));
    event.currentTarget.querySelector<HTMLElement>(`[data-repo="${CSS.escape(next.id)}"]`)?.focus();
  };
  return (
    <div className={styles.root}>
      <SearchBox label="Filter repos" value={filter} onChange={(value) => dispatch(filterChanged(value))} />
      <nav className={styles.list} aria-label="Repos" onKeyDown={onKeyDown}>
        {loaded && repos.length === 0 && <p className={styles.empty}>No cached repos yet.</p>}
        {loaded && repos.length > 0 && groups.length === 0 && <p className={styles.empty}>No repo matches “{filter}”.</p>}
        {groups.map((group) => (
          <section key={group.id} className={styles.group} aria-label={group.id}>
            <h2 className={styles.heading}>
              {group.id}
              <span className={styles.count}>{group.repos.length}</span>
            </h2>
            <ul className={styles.items}>
              {group.repos.map((repo) => (
                <li key={repo.id}>
                  <button
                    type="button"
                    className={styles.item}
                    data-repo={repo.id}
                    aria-current={repo.id === selectedId ? "true" : undefined}
                    onClick={() => void dispatch(selectRepo(repo.id))}
                  >
                    <span className={styles.row}>
                      <span className={styles.name}>{nameOf(repo.id)}</span>
                      {Icons !== undefined && <Icons repoId={repo.id} />}
                      <span className={styles.age}>
                        {fetchingAll || fetching.includes(repo.id) ? "fetching…" : age(repo.lastFetchedAt)}
                      </span>
                    </span>
                    <span className={styles.summary}>{repoSummary(repo, workspacesWith(entries, repo.id).length)}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </nav>
    </div>
  );
};
