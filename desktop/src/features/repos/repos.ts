import type { CachedRepo, Entry, RepoRef } from "../../api";
import { count, formatSize } from "../../format";

export interface RepoGroup {
  /** host/owner, such as github.com/bobcob7. */
  readonly id: string;
  readonly repos: readonly CachedRepo[];
}

/** host/owner of a repo id: everything before its last segment. */
export const ownerOf = (id: string): string => id.slice(0, Math.max(0, id.lastIndexOf("/")));

export const nameOf = (id: string): string => id.slice(id.lastIndexOf("/") + 1);

/** Repos whose id contains the filter, ignoring case, as ws cache search matches. */
export const filterRepos = (repos: readonly CachedRepo[], filter: string): readonly CachedRepo[] => {
  const q = filter.trim().toLowerCase();
  return q === "" ? repos : repos.filter((r) => r.id.includes(q));
};

/** Repos grouped by host/owner, groups by id and repos by name. */
export const groupRepos = (repos: readonly CachedRepo[]): readonly RepoGroup[] => {
  const groups = new Map<string, CachedRepo[]>();
  for (const repo of repos) {
    const owner = ownerOf(repo.id);
    groups.set(owner, [...(groups.get(owner) ?? []), repo]);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([id, members]) => ({ id, repos: members.sort((a, b) => nameOf(a.id).localeCompare(nameOf(b.id))) }));
};

/** Repos in the order the list shows them, for moving through it with the arrow keys. */
export const listOrder = (repos: readonly CachedRepo[], filter: string): readonly CachedRepo[] =>
  groupRepos(filterRepos(repos, filter)).flatMap((g) => g.repos);

/** Workspaces with this repo in them. */
export const workspacesWith = (entries: readonly Entry[], repoId: string): readonly Entry[] =>
  entries.filter((e) => e.workspaceId !== null && e.repos.some((r) => r.id === repoId));

/**
 * Workspaces that checked out this branch or tag when they were created,
 * from the ref ws list recorded. A workspace that switched branches since
 * still shows under the one it started on.
 */
export const workspacesOn = (entries: readonly Entry[], repoId: string, ref: RepoRef): readonly Entry[] =>
  entries.filter(
    (e) => e.workspaceId !== null && e.repos.some((r) => r.id === repoId && r.ref === ref.name && r.refKind === ref.kind),
  );

/** Above this, a list row shows the repo's size instead of its workspaces. */
export const largeRepoBytes = 100_000_000;

/** A list row's second line: default branch, branches, then workspaces or size. */
export const repoSummary = (repo: CachedRepo, workspaces: number): string => {
  const parts = [repo.defaultBranch, count(repo.branches, "branch", "branches")];
  if (repo.sizeBytes > largeRepoBytes) {
    parts.push(formatSize(repo.sizeBytes));
  } else if (workspaces > 0) {
    parts.push(count(workspaces, "workspace"));
  }
  return parts.join(" · ");
};

/** A spec for ws claude or ws shell: the repo, at a ref if one is given. */
export const specFor = (repoId: string, ref?: string): string => (ref === undefined ? repoId : `${repoId}@${ref}`);

/** A cache path shortened to start at the cache directory, as the remove dialog shows it. */
export const shortCachePath = (path: string): string => {
  const at = path.indexOf("/cache/repos/");
  return at < 0 ? path : `…${path.slice(at)}`;
};
