import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type Loop,
  type LoopListing,
  type LoopStarted,
  type NewLoop,
} from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown } from "../ui/uiSlice";

export interface LoopsState {
  readonly loops: readonly Loop[];
  readonly warnings: readonly string[];
  /** The listing failed. */
  readonly error: AppError | null;
  /** A loop's name. */
  readonly selected: string | null;
  /** The form is shown in the pane. */
  readonly creating: boolean;
  /** The selected loop's edit form is shown in the pane. */
  readonly editing: boolean;
  /** An add or edit is running. */
  readonly submitting: boolean;
  /** The last add or edit failed. */
  readonly formError: AppError | null;
  /** The name of the loop an action is running on. */
  readonly busy: string | null;
}

const initialState: LoopsState = {
  loops: [],
  warnings: [],
  error: null,
  selected: null,
  creating: false,
  editing: false,
  submitting: false,
  formError: null,
  busy: null,
};

export const fetchLoops = createAppThunk<LoopListing, void>("loops/fetch", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.loopsList();
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Adds a loop, then lists the loops again. ws checks the period and specs. */
export const addLoop = createAppThunk<Loop, NewLoop>("loops/add", async (request, { extra, dispatch, rejectWithValue }) => {
  try {
    const added = await extra.api.loopsAdd(request);
    void dispatch(fetchLoops());
    return added;
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Edits a loop, then lists the loops again. ws checks the period, specs, and vars. */
export const editLoop = createAppThunk<Loop, NewLoop>("loops/edit", async (request, { extra, dispatch, rejectWithValue }) => {
  try {
    const edited = await extra.api.loopsEdit(request);
    void dispatch(fetchLoops());
    return edited;
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export const setLoopPaused = createAppThunk<Loop, { name: string; paused: boolean }>(
  "loops/setPaused",
  async ({ name, paused }, { extra, dispatch, rejectWithValue }) => {
    try {
      const updated = await extra.api.loopsSetPaused(name, paused);
      return updated;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Deletes a loop, then lists the loops again. Its runs' workspaces stay. */
export const removeLoop = createAppThunk<Loop, string>("loops/remove", async (name, { extra, dispatch, rejectWithValue }) => {
  try {
    const removed = await extra.api.loopsRemove(name);
    void dispatch(fetchLoops());
    return removed;
  } catch (err: unknown) {
    const error = toAppError(err);
    dispatch(noticeShown({ severity: "error", message: error.message }));
    return rejectWithValue(error);
  }
});

/** Starts a run now, then lists the loops again. */
export const runLoopNow = createAppThunk<LoopStarted, string>("loops/runNow", async (name, { extra, dispatch, rejectWithValue }) => {
  try {
    const started = await extra.api.loopsRunNow(name);
    dispatch(noticeShown({ severity: "success", message: `Started ${started.workspace}`, action: { label: "Open", agent: started.workspace } }));
    void dispatch(fetchLoops());
    return started;
  } catch (err: unknown) {
    const error = toAppError(err);
    dispatch(noticeShown({ severity: "error", message: error.message }));
    return rejectWithValue(error);
  }
});

/** The loop an action is running on, for the busy marker. */
const busyName = (arg: { readonly name: string } | string): string => (typeof arg === "string" ? arg : arg.name);

const loopsSlice = createSlice({
  name: "loops",
  initialState,
  reducers: {
    loopSelected: (state, action: PayloadAction<string | null>) => {
      state.selected = action.payload;
      state.creating = false;
      state.editing = false;
    },
    creatingStarted: (state) => {
      state.creating = true;
      state.editing = false;
      state.formError = null;
    },
    creatingCancelled: (state) => {
      state.creating = false;
      state.formError = null;
    },
    editingStarted: (state) => {
      state.editing = true;
      state.creating = false;
      state.formError = null;
    },
    editingCancelled: (state) => {
      state.editing = false;
      state.formError = null;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchLoops.fulfilled, (state, action): LoopsState => {
        const { loops, warnings } = action.payload;
        const gone = state.selected !== null && !loops.some((loop) => loop.name === state.selected);
        return { ...state, loops, warnings, error: null, selected: gone ? null : state.selected };
      })
      .addCase(fetchLoops.rejected, (state, action) => {
        state.error = action.payload ?? { code: "internal", message: action.error.message ?? "listing loops failed" };
      })
      .addCase(addLoop.pending, (state) => {
        state.submitting = true;
      })
      .addCase(addLoop.fulfilled, (state, action) => {
        state.submitting = false;
        state.creating = false;
        state.formError = null;
        state.selected = action.payload.name;
      })
      .addCase(addLoop.rejected, (state, action) => {
        state.submitting = false;
        state.formError = action.payload ?? { code: "internal", message: action.error.message ?? "adding loop failed" };
      })
      .addCase(editLoop.pending, (state) => {
        state.submitting = true;
      })
      .addCase(editLoop.fulfilled, (state, action): LoopsState => ({
        ...state,
        submitting: false,
        editing: false,
        formError: null,
        loops: state.loops.map((loop) => (loop.name === action.payload.name ? action.payload : loop)),
      }))
      .addCase(editLoop.rejected, (state, action) => {
        state.submitting = false;
        state.formError = action.payload ?? { code: "internal", message: action.error.message ?? "editing loop failed" };
      })
      .addCase(setLoopPaused.pending, (state, action) => {
        state.busy = busyName(action.meta.arg);
      })
      .addCase(setLoopPaused.fulfilled, (state, action): LoopsState => ({
        ...state,
        busy: null,
        loops: state.loops.map((loop) => (loop.name === action.payload.name ? action.payload : loop)),
      }))
      .addCase(setLoopPaused.rejected, (state) => {
        state.busy = null;
      })
      .addCase(removeLoop.pending, (state, action) => {
        state.busy = busyName(action.meta.arg);
      })
      .addCase(removeLoop.fulfilled, (state) => {
        state.busy = null;
        state.selected = null;
      })
      .addCase(removeLoop.rejected, (state) => {
        state.busy = null;
      })
      .addCase(runLoopNow.pending, (state, action) => {
        state.busy = busyName(action.meta.arg);
      })
      .addCase(runLoopNow.fulfilled, (state) => {
        state.busy = null;
      })
      .addCase(runLoopNow.rejected, (state) => {
        state.busy = null;
      });
  },
});

export const { loopSelected, creatingStarted, creatingCancelled, editingStarted, editingCancelled } = loopsSlice.actions;
export const loopsReducer = loopsSlice.reducer;
