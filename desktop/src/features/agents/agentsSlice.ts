import { createSlice, isAnyOf, type PayloadAction } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type Created,
  type Entry,
  type Listing,
  type NewAgent,
  type Opened,
  type OpenWith,
} from "../../api";
import { createAppThunk } from "../../thunk";
import { closeTerminal } from "../terminals/terminalsSlice";
import { agentFocused, dialogClosed, noticeShown, sectionChosen, selected } from "../ui/uiSlice";
import { ancestorKeys } from "./tree";
import type { StatusFilter } from "./status";

export interface AgentsState {
  readonly entries: readonly Entry[];
  readonly warnings: readonly string[];
  readonly loaded: boolean;
  readonly loading: boolean;
  readonly error: AppError | null;
  /** Keys of entries with an action in flight. */
  readonly busy: readonly string[];
  readonly creating: boolean;
  readonly createError: AppError | null;
  /** The Show hidden switch is on (ADR 0058); not persisted. */
  readonly showHidden: boolean;
  /** The user's last open (true) or closed (false) choice per entry key in the tree (ADR 0058); not persisted. */
  readonly expanded: Readonly<Record<string, boolean>>;
  /** The Agents list's search text (ADR 0058); not persisted. */
  readonly query: string;
  /** The Agents list's status filter (ADR 0058); not persisted. */
  readonly statusFilter: StatusFilter;
}

const initialState: AgentsState = {
  entries: [],
  warnings: [],
  loaded: false,
  loading: false,
  error: null,
  busy: [],
  creating: false,
  createError: null,
  showHidden: false,
  expanded: {},
  query: "",
  statusFilter: "all",
};

/** The record without its false values: the user's closed choices are dropped so new matches show open. */
const withoutClosed = (expanded: Readonly<Record<string, boolean>>): Readonly<Record<string, boolean>> =>
  Object.fromEntries(Object.entries(expanded).filter(([, open]) => open));

/** The record without the keys of entries that are no longer listed. */
const retainListed = (
  expanded: Readonly<Record<string, boolean>>,
  entries: readonly Entry[],
): Readonly<Record<string, boolean>> => {
  const listed = new Set(entries.map((listedEntry) => listedEntry.key));
  return Object.fromEntries(Object.entries(expanded).filter(([key]) => listed.has(key)));
};

export const fetchEntries = createAppThunk<Listing, void>(
  "agents/fetch",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      return await extra.api.listEntries();
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  { condition: (_arg, { getState }) => !getState().agents.loading },
);

export const createAgent = createAppThunk<Created, NewAgent>(
  "agents/create",
  async (request, { extra, dispatch, rejectWithValue }) => {
    try {
      const created = await extra.api.createAgent(request);
      dispatch(dialogClosed());
      dispatch(selected(created.id));
      dispatch(sectionChosen("agents"));
      dispatch(noticeShown({ severity: "success", message: `Started agent in ${created.id}` }));
      void dispatch(fetchEntries());
      return created;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const stopAgent = createAppThunk<void, Entry>(
  "agents/stop",
  async (entry, { extra, dispatch, rejectWithValue }) => {
    try {
      if (entry.sessionId !== null) {
        await extra.api.stopAgent(entry.sessionId);
        await dispatch(closeTerminal(`attach:${entry.sessionId}`));
      }
      dispatch(noticeShown({ severity: "info", message: `Stopped ${entry.name}` }));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchEntries());
    }
  },
);

/** The directory the editor opens: the repo's own when a workspace has exactly one, so VS Code gets its git root and .vscode/ (ADR 0051). */
export const editorDir = (entry: Entry): string => {
  const [onlyRepo] = entry.repos;
  return entry.repos.length === 1 && onlyRepo !== undefined ? `${entry.path}/${onlyRepo.path}` : entry.path;
};

export const openWorkspace = createAppThunk<Opened, { readonly entry: Entry; readonly with: OpenWith }>(
  "agents/open",
  async ({ entry, with: openWith }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.workspaceOpen(
        openWith,
        entry.path,
        entry.repos.map((repo) => repo.path),
      );
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: `Couldn't open ${entry.name}: ${error.message}` }));
      return rejectWithValue(error);
    }
  },
);

/** An entry to remove; keepChildren makes the workspaces it started top-level instead (ADR 0044). */
export type RemoveRequest = Entry & { readonly keepChildren?: boolean };

export const removeEntry = createAppThunk<void, RemoveRequest>(
  "agents/remove",
  async (entry, { extra, dispatch, getState, rejectWithValue }) => {
    try {
      if (entry.sessionId !== null) {
        await dispatch(closeTerminal(`attach:${entry.sessionId}`));
      }
      await extra.api.removeEntry(entry.workspaceId, entry.sessionId, entry.keepChildren ?? false);
      if (getState().ui.selectedKey === entry.key) {
        dispatch(selected(null));
      }
      dispatch(noticeShown({ severity: "info", message: `Removed ${entry.name}` }));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchEntries());
    }
  },
);

export const renameEntry = createAppThunk<void, { readonly entry: Entry; readonly name: string }>(
  "agents/rename",
  async ({ entry, name }, { extra, dispatch, rejectWithValue }) => {
    if (entry.workspaceId === null) {
      return;
    }
    try {
      const renamed = await extra.api.renameWorkspace(entry.workspaceId, name);
      dispatch(noticeShown({ severity: "success", message: `Renamed to ${renamed.name}` }));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchEntries());
    }
  },
);

export const setEntryHidden = createAppThunk<void, { readonly entry: Entry; readonly hidden: boolean }>(
  "agents/setHidden",
  async ({ entry, hidden }, { extra, dispatch, rejectWithValue }) => {
    if (entry.workspaceId === null) {
      return;
    }
    try {
      await extra.api.setWorkspaceHidden(entry.workspaceId, hidden);
      dispatch(noticeShown({ severity: "info", message: hidden ? `Hid ${entry.name}` : `Unhid ${entry.name}` }));
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchEntries());
    }
  },
);

export const restoreAgents = createAppThunk<readonly string[], void>(
  "agents/restore",
  async (_arg, { extra, dispatch, rejectWithValue }) => {
    try {
      const restored = await extra.api.restoreAgents();
      const message = restored.length === 0 ? "No stopped agents to restore" : `Restored ${restored.join(", ")}`;
      dispatch(noticeShown({ severity: "info", message }));
      return restored;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      void dispatch(fetchEntries());
    }
  },
);

const agentsSlice = createSlice({
  name: "agents",
  initialState,
  reducers: {
    createErrorCleared: (state) => {
      state.createError = null;
    },
    hiddenShown: (state, action: PayloadAction<boolean>) => {
      state.showHidden = action.payload;
    },
    nodeToggled: (state, action: PayloadAction<{ readonly key: string; readonly open: boolean }>) => {
      state.expanded = { ...state.expanded, [action.payload.key]: action.payload.open };
    },
    queryChanged: (state, action: PayloadAction<string>) => {
      state.query = action.payload;
      state.expanded = withoutClosed(state.expanded);
    },
    statusFilterChanged: (state, action: PayloadAction<StatusFilter>) => {
      state.statusFilter = action.payload;
      state.expanded = withoutClosed(state.expanded);
    },
    filtersCleared: (state) => {
      state.query = "";
      state.statusFilter = "all";
      state.expanded = withoutClosed(state.expanded);
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchEntries.pending, (state) => {
        state.loading = true;
      })
      .addCase(fetchEntries.fulfilled, (state, action): AgentsState => ({
        ...state,
        loading: false,
        loaded: true,
        error: null,
        entries: action.payload.entries,
        warnings: action.payload.warnings,
        expanded: retainListed(state.expanded, action.payload.entries),
      }))
      .addCase(fetchEntries.rejected, (state, action) => {
        state.loading = false;
        state.loaded = true;
        state.error = action.payload ?? { code: "internal", message: action.error.message ?? "listing failed" };
      })
      .addCase(createAgent.pending, (state) => {
        state.creating = true;
        state.createError = null;
      })
      .addCase(createAgent.fulfilled, (state) => {
        state.creating = false;
      })
      .addCase(createAgent.rejected, (state, action) => {
        state.creating = false;
        state.createError = action.payload ?? { code: "internal", message: action.error.message ?? "failed" };
      })
      .addMatcher(isAnyOf(stopAgent.pending, removeEntry.pending), (state, action) => {
        state.busy = [...state.busy, action.meta.arg.key];
      })
      .addMatcher(
        isAnyOf(stopAgent.fulfilled, stopAgent.rejected, removeEntry.fulfilled, removeEntry.rejected),
        (state, action) => {
          state.busy = state.busy.filter((key) => key !== action.meta.arg.key);
        },
      )
      .addMatcher(isAnyOf(renameEntry.pending, setEntryHidden.pending), (state, action) => {
        state.busy = [...state.busy, action.meta.arg.entry.key];
      })
      .addMatcher(
        isAnyOf(renameEntry.fulfilled, renameEntry.rejected, setEntryHidden.fulfilled, setEntryHidden.rejected),
        (state, action) => {
          state.busy = state.busy.filter((key) => key !== action.meta.arg.entry.key);
        },
      )
      .addMatcher(isAnyOf(selected, agentFocused), (state, action) => {
        if (action.payload === null) {
          return;
        }
        const opened = Object.fromEntries(ancestorKeys(state.entries, action.payload).map((key) => [key, true]));
        state.expanded = { ...state.expanded, ...opened };
      });
  },
});

export const { createErrorCleared, hiddenShown, nodeToggled, queryChanged, statusFilterChanged, filtersCleared } =
  agentsSlice.actions;
export const agentsReducer = agentsSlice.reducer;
