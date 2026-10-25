import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import {
  toAppError,
  type AddedRepo,
  type AppError,
  type CachedRepo,
  type CacheListing,
  type RefListing,
  type RepoResult,
  type Resolution,
} from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown, sectionChosen, selected } from "../ui/uiSlice";
import { nameOf } from "./repos";

export type RepoTab = "branches" | "tags" | "workspaces";

export interface RefsState {
  readonly listing: RefListing | null;
  readonly loading: boolean;
  readonly error: AppError | null;
}

/** The Add repo dialog's preview of what its spec resolves to. */
export interface Preview {
  readonly spec: string;
  readonly loading: boolean;
  readonly resolution: Resolution | null;
  readonly error: AppError | null;
}

export interface ReposState {
  readonly repos: readonly CachedRepo[];
  readonly warnings: readonly string[];
  readonly loaded: boolean;
  readonly loading: boolean;
  readonly error: AppError | null;
  readonly selectedId: string | null;
  readonly filter: string;
  readonly tab: RepoTab;
  /** Each repo's branches and tags, read when it's selected. */
  readonly refs: Readonly<Record<string, RefsState>>;
  /** Repos being fetched from upstream. */
  readonly fetching: readonly string[];
  readonly fetchingAll: boolean;
  /** The last fetch failure of each repo, shown above its tabs. */
  readonly fetchErrors: Readonly<Record<string, AppError>>;
  readonly removing: readonly string[];
  readonly addOpen: boolean;
  readonly adding: boolean;
  readonly addError: AppError | null;
  readonly preview: Preview | null;
}

const initialState: ReposState = {
  repos: [],
  warnings: [],
  loaded: false,
  loading: false,
  error: null,
  selectedId: null,
  filter: "",
  tab: "branches",
  refs: {},
  fetching: [],
  fetchingAll: false,
  fetchErrors: {},
  removing: [],
  addOpen: false,
  adding: false,
  addError: null,
  preview: null,
};

/** Reads ws cache list: on entering the section and after every action, not on the agents poll. */
export const fetchRepos = createAppThunk<CacheListing, void>(
  "repos/fetch",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      return await extra.api.cacheList();
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  { condition: (_arg, { getState }) => !getState().repos.loading },
);

export const fetchRefs = createAppThunk<RefListing, string>("repos/refs", async (id, { extra, rejectWithValue }) => {
  try {
    return await extra.api.cacheRefs(id);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Fetches one repo from upstream, then rereads the list and its refs. */
export const fetchRepo = createAppThunk<RepoResult, string>(
  "repos/fetchRepo",
  async (id, { extra, dispatch, rejectWithValue }) => {
    try {
      const [result] = await extra.api.cacheFetch([id], false);
      if (result === undefined) {
        return rejectWithValue({ code: "unexpected_output", message: "ws cache update printed no repo" });
      }
      if (result.error !== null) {
        return rejectWithValue(result.error);
      }
      return result;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    } finally {
      void dispatch(fetchRepos());
      void dispatch(fetchRefs(id));
    }
  },
);

const fetchAllMessage = (results: readonly RepoResult[]): string => {
  const failed = results.filter((r) => r.error !== null);
  const first = failed[0];
  if (first === undefined) {
    return `Fetched ${results.length} repo${results.length === 1 ? "" : "s"}`;
  }
  return `Fetched ${results.length - failed.length} of ${results.length}; ${nameOf(first.id)}: ${first.error?.message ?? "failed"}`;
};

/** Fetches every cached repo: ws cache update --all. */
export const fetchAllRepos = createAppThunk<readonly RepoResult[], void>(
  "repos/fetchAll",
  async (_arg, { extra, dispatch, getState, rejectWithValue }) => {
    try {
      const results = await extra.api.cacheFetch([], true);
      const failed = results.some((r) => r.error !== null);
      dispatch(noticeShown({ severity: failed ? "error" : "success", message: fetchAllMessage(results) }));
      return results;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchRepos());
      const selectedId = getState().repos.selectedId;
      if (selectedId !== null) {
        void dispatch(fetchRefs(selectedId));
      }
    }
  },
);

/** Resolves the Add repo dialog's spec, for its preview. */
export const resolveSpec = createAppThunk<Resolution, string>("repos/resolve", async (spec, { extra, rejectWithValue }) => {
  try {
    return await extra.api.cacheResolve(spec);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export interface AddRepoRequest {
  readonly spec: string;
  /** Whether Reviews should watch it; set only when Reviews offers the switch. */
  readonly watch?: boolean;
}

/** Clones a repo into the cache, then selects it. Reviews can follow addRepo.fulfilled for `watch`. */
export const addRepo = createAppThunk<AddedRepo, AddRepoRequest>(
  "repos/add",
  async ({ spec }, { extra, dispatch, rejectWithValue }) => {
    try {
      const added = await extra.api.cacheAdd(spec.trim());
      const message = added.added ? `Added ${added.id}` : `${added.id} was already cached`;
      dispatch(noticeShown({ severity: "success", message }));
      void dispatch(fetchRepos());
      void dispatch(fetchRefs(added.id));
      return added;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Deletes a repo from the cache. Reviews can follow removeRepo.fulfilled to stop watching it. */
export const removeRepo = createAppThunk<void, string>(
  "repos/remove",
  async (id, { extra, dispatch, rejectWithValue }) => {
    try {
      await extra.api.cacheRemove(id);
      dispatch(noticeShown({ severity: "info", message: `Removed ${id} from the cache` }));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchRepos());
    }
  },
);

/** Selects a repo and rereads its branches and tags. */
export const selectRepo = createAppThunk<void, string>("repos/select", async (id, { dispatch }) => {
  dispatch(repoSelected(id));
  await dispatch(fetchRefs(id));
});

/** Switches to Agents with a workspace selected, from a workspace chip or row. */
export const openInAgents = createAppThunk<void, string>("repos/openInAgents", async (key, { dispatch }) => {
  dispatch(selected(key));
  dispatch(sectionChosen("agents"));
});

const without = (items: readonly string[], item: string): readonly string[] => items.filter((i) => i !== item);

const omit = <T>(record: Readonly<Record<string, T>>, key: string): Readonly<Record<string, T>> =>
  Object.fromEntries(Object.entries(record).filter(([k]) => k !== key));

const reposSlice = createSlice({
  name: "repos",
  initialState,
  reducers: {
    repoSelected: (state, action: PayloadAction<string | null>) => {
      state.selectedId = action.payload;
    },
    filterChanged: (state, action: PayloadAction<string>) => {
      state.filter = action.payload;
    },
    tabChosen: (state, action: PayloadAction<RepoTab>) => {
      state.tab = action.payload;
    },
    addOpened: (state) => {
      state.addOpen = true;
      state.addError = null;
      state.preview = null;
    },
    addClosed: (state) => {
      state.addOpen = false;
      state.addError = null;
      state.preview = null;
    },
    previewCleared: (state) => {
      state.preview = null;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchRepos.pending, (state) => {
        state.loading = true;
      })
      .addCase(fetchRepos.fulfilled, (state, action): ReposState => {
        const { repos, warnings } = action.payload;
        const kept = state.selectedId !== null && repos.some((r) => r.id === state.selectedId);
        return { ...state, loading: false, loaded: true, error: null, repos, warnings, selectedId: kept ? state.selectedId : null };
      })
      .addCase(fetchRepos.rejected, (state, action) => {
        state.loading = false;
        state.loaded = true;
        state.error = action.payload ?? { code: "internal", message: action.error.message ?? "listing failed" };
      })
      .addCase(fetchRefs.pending, (state, action): ReposState => {
        const listing = state.refs[action.meta.arg]?.listing ?? null;
        return { ...state, refs: { ...state.refs, [action.meta.arg]: { listing, loading: true, error: null } } };
      })
      .addCase(fetchRefs.fulfilled, (state, action): ReposState => ({
        ...state,
        refs: { ...state.refs, [action.meta.arg]: { listing: action.payload, loading: false, error: null } },
      }))
      .addCase(fetchRefs.rejected, (state, action): ReposState => {
        const listing = state.refs[action.meta.arg]?.listing ?? null;
        const error = action.payload ?? { code: "internal", message: action.error.message ?? "reading refs failed" };
        return { ...state, refs: { ...state.refs, [action.meta.arg]: { listing, loading: false, error } } };
      })
      .addCase(fetchRepo.pending, (state, action): ReposState => ({
        ...state,
        fetching: [...state.fetching, action.meta.arg],
        fetchErrors: omit(state.fetchErrors, action.meta.arg),
      }))
      .addCase(fetchRepo.fulfilled, (state, action): ReposState => ({
        ...state,
        fetching: without(state.fetching, action.meta.arg),
      }))
      .addCase(fetchRepo.rejected, (state, action): ReposState => {
        const error = action.payload ?? { code: "internal", message: action.error.message ?? "fetch failed" };
        return {
          ...state,
          fetching: without(state.fetching, action.meta.arg),
          fetchErrors: { ...state.fetchErrors, [action.meta.arg]: error },
        };
      })
      .addCase(fetchAllRepos.pending, (state) => {
        state.fetchingAll = true;
      })
      .addCase(fetchAllRepos.fulfilled, (state, action): ReposState => {
        const fetchErrors = Object.fromEntries(
          action.payload.flatMap((r) => (r.error === null ? [] : [[r.id, r.error] as const])),
        );
        return { ...state, fetchingAll: false, fetchErrors };
      })
      .addCase(fetchAllRepos.rejected, (state) => {
        state.fetchingAll = false;
      })
      .addCase(resolveSpec.pending, (state, action): ReposState => ({
        ...state,
        preview: { spec: action.meta.arg, loading: true, resolution: null, error: null },
      }))
      .addCase(resolveSpec.fulfilled, (state, action): ReposState => {
        if (state.preview?.spec !== action.meta.arg) {
          return state;
        }
        return { ...state, preview: { spec: action.meta.arg, loading: false, resolution: action.payload, error: null } };
      })
      .addCase(resolveSpec.rejected, (state, action): ReposState => {
        if (state.preview?.spec !== action.meta.arg) {
          return state;
        }
        const error = action.payload ?? { code: "internal", message: action.error.message ?? "resolve failed" };
        return { ...state, preview: { spec: action.meta.arg, loading: false, resolution: null, error } };
      })
      .addCase(addRepo.pending, (state) => {
        state.adding = true;
        state.addError = null;
      })
      .addCase(addRepo.fulfilled, (state, action) => {
        state.adding = false;
        state.addOpen = false;
        state.preview = null;
        state.selectedId = action.payload.id;
      })
      .addCase(addRepo.rejected, (state, action) => {
        state.adding = false;
        state.addError = action.payload ?? { code: "internal", message: action.error.message ?? "adding failed" };
      })
      .addCase(removeRepo.pending, (state, action): ReposState => ({
        ...state,
        removing: [...state.removing, action.meta.arg],
      }))
      .addCase(removeRepo.fulfilled, (state, action): ReposState => ({
        ...state,
        removing: without(state.removing, action.meta.arg),
        refs: omit(state.refs, action.meta.arg),
        selectedId: state.selectedId === action.meta.arg ? null : state.selectedId,
      }))
      .addCase(removeRepo.rejected, (state, action): ReposState => ({
        ...state,
        removing: without(state.removing, action.meta.arg),
      }));
  },
});

export const { repoSelected, filterChanged, tabChosen, addOpened, addClosed, previewCleared } = reposSlice.actions;
export const reposReducer = reposSlice.reducer;
