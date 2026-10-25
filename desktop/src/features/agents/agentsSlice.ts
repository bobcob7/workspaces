import { createSlice, isAnyOf } from "@reduxjs/toolkit";
import { toAppError, type AppError, type Created, type Entry, type Listing, type NewAgent } from "../../api";
import { createAppThunk } from "../../thunk";
import { closeTerminal } from "../terminals/terminalsSlice";
import { dialogClosed, noticeShown, sectionChosen, selected } from "../ui/uiSlice";

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

export const removeEntry = createAppThunk<void, Entry>(
  "agents/remove",
  async (entry, { extra, dispatch, getState, rejectWithValue }) => {
    try {
      if (entry.sessionId !== null) {
        await dispatch(closeTerminal(`attach:${entry.sessionId}`));
      }
      await extra.api.removeEntry(entry.workspaceId, entry.sessionId);
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
      );
  },
});

export const { createErrorCleared } = agentsSlice.actions;
export const agentsReducer = agentsSlice.reducer;
