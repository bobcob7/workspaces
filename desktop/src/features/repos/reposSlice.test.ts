import { describe, expect, it, vi } from "vitest";
import { cachedRepo, fakeApi, testStore } from "../../test/fixtures";
import {
  addRepo,
  fetchAllRepos,
  fetchRepo,
  fetchRepos,
  openInAgents,
  removeRepo,
  reposReducer,
  resolveSpec,
  selectRepo,
  type ReposState,
} from "./reposSlice";

const initial = (): ReposState => reposReducer(undefined, { type: "init" });

const resolution = { id: "github.com/o/n", ref: null, upstream: "u", cached: false, source: "probe" } as const;

describe("reposReducer", () => {
  it("stores the listing", () => {
    const listing = { repos: [cachedRepo()], warnings: ["w"] };
    const state = reposReducer(initial(), fetchRepos.fulfilled(listing, "req"));
    expect(state).toMatchObject({ repos: listing.repos, warnings: ["w"], loaded: true, loading: false });
  });

  it("drops the selection when the repo is gone from the listing", () => {
    const state = reposReducer({ ...initial(), selectedId: "github.com/o/gone" }, fetchRepos.fulfilled({ repos: [], warnings: [] }, "r"));
    expect(state.selectedId).toBeNull();
  });

  it("keeps the last listing when reading it fails", () => {
    const loaded = reposReducer(initial(), fetchRepos.fulfilled({ repos: [cachedRepo()], warnings: [] }, "r1"));
    const error = { code: "ws_missing", message: "no ws" };
    const state = reposReducer(loaded, fetchRepos.rejected(null, "r2", undefined, error));
    expect(state.repos).toHaveLength(1);
    expect(state.error).toEqual(error);
  });

  it("records a failed fetch for the repo", () => {
    const error = { code: "fetch_failed", message: "offline" };
    const state = reposReducer(initial(), fetchRepo.rejected(null, "r", "github.com/o/n", error));
    expect(state.fetchErrors["github.com/o/n"]).toEqual(error);
  });

  it("clears a repo's fetch error when it's fetched again", () => {
    const failed = reposReducer(initial(), fetchRepo.rejected(null, "r", "github.com/o/n", { code: "x", message: "y" }));
    const state = reposReducer(failed, fetchRepo.pending("r2", "github.com/o/n"));
    expect(state.fetchErrors).toEqual({});
  });

  it("marks a repo fetching until its fetch ends", () => {
    const pending = reposReducer(initial(), fetchRepo.pending("r", "github.com/o/n"));
    expect(pending.fetching).toEqual(["github.com/o/n"]);
  });

  it("ignores a preview for a spec that has since changed", () => {
    const pending = reposReducer(initial(), resolveSpec.pending("r2", "o/n2"));
    const state = reposReducer(pending, resolveSpec.fulfilled(resolution, "r1", "o/n"));
    expect(state.preview).toMatchObject({ spec: "o/n2", loading: true });
  });

  it("selects an added repo and closes the dialog", () => {
    const open = { ...initial(), addOpen: true, adding: true };
    const added = { id: "github.com/o/n", path: "/c", upstream: "u", added: true };
    const state = reposReducer(open, addRepo.fulfilled(added, "r", { spec: "o/n" }));
    expect(state).toMatchObject({ selectedId: "github.com/o/n", addOpen: false, adding: false });
  });

  it("keeps the dialog open with the error when adding fails", () => {
    const error = { code: "clone_failed", message: "denied" };
    const state = reposReducer({ ...initial(), addOpen: true }, addRepo.rejected(null, "r", { spec: "o/n" }, error));
    expect(state).toMatchObject({ addOpen: true, addError: error });
  });

  it("deselects a removed repo", () => {
    const state = reposReducer({ ...initial(), selectedId: "github.com/o/n" }, removeRepo.fulfilled(undefined, "r", "github.com/o/n"));
    expect(state.selectedId).toBeNull();
  });
});

describe("thunks", () => {
  it("does not read the listing while a read is in flight", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await Promise.all([store.dispatch(fetchRepos()), store.dispatch(fetchRepos())]);
    expect(api.cacheList).toHaveBeenCalledTimes(1);
  });

  it("reads a repo's refs when it's selected", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(selectRepo("github.com/o/n"));
    expect(store.getState().repos.refs["github.com/o/n"]?.listing?.defaultBranch).toBe("main");
  });

  it("rereads the refs after a fetch", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(fetchRepo("github.com/o/n"));
    expect(api.cacheRefs).toHaveBeenCalledWith("github.com/o/n");
  });

  it("reports a repo's fetch failure from ws's per-repo result", async () => {
    const api = fakeApi({
      cacheFetch: vi.fn(async () => [{ id: "github.com/o/n", error: { code: "fetch_failed", message: "offline" } }]),
    });
    const store = testStore({ api });
    await store.dispatch(fetchRepo("github.com/o/n"));
    expect(store.getState().repos.fetchErrors["github.com/o/n"]?.message).toBe("offline");
  });

  it("says how many repos Fetch all fetched", async () => {
    const api = fakeApi({ cacheFetch: vi.fn(async () => [{ id: "a", error: null }, { id: "b", error: null }]) });
    const store = testStore({ api });
    await store.dispatch(fetchAllRepos());
    expect(store.getState().ui.notice).toEqual({ severity: "success", message: "Fetched 2 repos" });
  });

  it("names the first repo Fetch all couldn't fetch", async () => {
    const api = fakeApi({
      cacheFetch: vi.fn(async () => [
        { id: "github.com/o/a", error: null },
        { id: "github.com/o/b", error: { code: "fetch_failed", message: "offline" } },
      ]),
    });
    const store = testStore({ api });
    await store.dispatch(fetchAllRepos());
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "Fetched 1 of 2; b: offline" });
  });

  it("removes by id", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(removeRepo("github.com/o/n"));
    expect(api.cacheRemove).toHaveBeenCalledWith("github.com/o/n");
  });

  it("opens a workspace in Agents", async () => {
    const store = testStore();
    await store.dispatch(openInAgents("fix-login"));
    expect(store.getState().ui).toMatchObject({ section: "agents", selectedKey: "fix-login" });
  });
});
