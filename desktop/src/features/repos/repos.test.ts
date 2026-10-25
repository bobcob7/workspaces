import { describe, expect, it } from "vitest";
import { cachedRepo, entry, repoRef } from "../../test/fixtures";
import { filterRepos, groupRepos, listOrder, repoSummary, shortCachePath, specFor, workspacesOn, workspacesWith } from "./repos";

const ids = (repos: readonly { readonly id: string }[]): readonly string[] => repos.map((r) => r.id);

const repos = [
  cachedRepo({ id: "gitlab.example.com/team/svc" }),
  cachedRepo({ id: "github.com/bobcob7/doer" }),
  cachedRepo({ id: "github.com/rust-lang/cargo" }),
  cachedRepo({ id: "github.com/bobcob7/buff-bear" }),
];

describe("groupRepos", () => {
  it("groups by host/owner, sorted by id", () => {
    expect(groupRepos(repos).map((g) => g.id)).toEqual([
      "github.com/bobcob7",
      "github.com/rust-lang",
      "gitlab.example.com/team",
    ]);
  });

  it("sorts repos by name within a group", () => {
    expect(ids(groupRepos(repos)[0]?.repos ?? [])).toEqual(["github.com/bobcob7/buff-bear", "github.com/bobcob7/doer"]);
  });
});

describe("filterRepos", () => {
  it("matches a substring of the id, ignoring case", () => {
    expect(ids(filterRepos(repos, "  BOBCOB7 "))).toEqual(["github.com/bobcob7/doer", "github.com/bobcob7/buff-bear"]);
  });

  it("keeps every repo for an empty filter", () => {
    expect(filterRepos(repos, "")).toHaveLength(4);
  });
});

describe("listOrder", () => {
  it("follows the groups as shown", () => {
    expect(ids(listOrder(repos, ""))[0]).toBe("github.com/bobcob7/buff-bear");
  });
});

describe("repoSummary", () => {
  it("shows the default branch, branches, and workspaces", () => {
    expect(repoSummary(cachedRepo({ branches: 14 }), 2)).toBe("main · 14 branches · 2 workspaces");
  });

  it("leaves out workspaces when there are none", () => {
    expect(repoSummary(cachedRepo({ branches: 1 }), 0)).toBe("main · 1 branch");
  });

  it("shows the size instead of workspaces for a repo over 100 MB", () => {
    expect(repoSummary(cachedRepo({ defaultBranch: "master", branches: 41, sizeBytes: 412_000_000 }), 3)).toBe(
      "master · 41 branches · 412 MB",
    );
  });
});

describe("workspaces", () => {
  const id = "github.com/bobcob7/buff-bear";
  const onMain = entry({ key: "a", repos: [{ id, path: "/w/a/buff-bear", ref: "main", refKind: "branch" }] });
  const onTag = entry({ key: "b", repos: [{ id, path: "/w/b/buff-bear", ref: "main", refKind: "tag" }] });
  const other = entry({ key: "c", repos: [{ id: "github.com/o/x", path: "/w/c/x", ref: "main", refKind: "branch" }] });

  it("finds the workspaces with a repo", () => {
    expect(workspacesWith([onMain, onTag, other], id).map((e) => e.key)).toEqual(["a", "b"]);
  });

  it("joins a branch with the workspaces on it, by name and kind", () => {
    expect(workspacesOn([onMain, onTag, other], id, repoRef({ name: "main" })).map((e) => e.key)).toEqual(["a"]);
  });

  it("ignores sessions outside a workspace", () => {
    expect(workspacesWith([entry({ workspaceId: null, repos: onMain.repos })], id)).toEqual([]);
  });
});

describe("specFor", () => {
  it("adds the ref", () => {
    expect(specFor("github.com/o/n", "feat/x")).toBe("github.com/o/n@feat/x");
  });

  it("is the id without a ref", () => {
    expect(specFor("github.com/o/n")).toBe("github.com/o/n");
  });
});

describe("shortCachePath", () => {
  it("starts at the cache directory", () => {
    expect(shortCachePath("/Users/me/Library/Application Support/workspaces/cache/repos/github.com/o/n.git")).toBe(
      "…/cache/repos/github.com/o/n.git",
    );
  });
});
