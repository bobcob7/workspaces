import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type Loop,
  type LoopListing,
  type LoopStarted,
  type NewLoop,
} from "../../api";
import type { RootState } from "../../store";
import { createAppThunk } from "../../thunk";
import { offlineReason } from "../environments/environments";
import { noticeShown } from "../ui/uiSlice";
import { loopKey } from "./loopDataSlice";

/** What one environment's last listing held. */
export interface EnvironmentLoops {
  readonly items: readonly Loop[];
  /** When the listing last succeeded (ms since the epoch); null before the first. */
  readonly loadedAt: number | null;
  /** Why the last listing failed; the items are then the last good ones. */
  readonly error: string | null;
  /** The environment hasn't reported its loops yet. */
  readonly loopsUnknown: boolean;
}

export interface LoopsState {
  /** By environment: "local", then each enrolled environment's name. */
  readonly byEnvironment: Readonly<Record<string, EnvironmentLoops>>;
  /** This machine's warnings from the last listing. */
  readonly warnings: readonly string[];
  /** A loop's name, in `selectedEnvironment`. */
  readonly selected: string | null;
  readonly selectedEnvironment: string;
  /** The form is shown in the pane. */
  readonly creating: boolean;
  /** The selected loop's edit form is shown in the pane. */
  readonly editing: boolean;
  /** An add or edit is running. */
  readonly submitting: boolean;
  /** The last add or edit failed. */
  readonly formError: AppError | null;
  /** The `loopKey` of the loop an action is running on. */
  readonly busy: string | null;
  /** The request id of the newest fetch; an older fetch that settles later is ignored. */
  readonly latestRequestId: string | null;
}

const emptyEnvironment: EnvironmentLoops = { items: [], loadedAt: null, error: null, loopsUnknown: false };

const initialState: LoopsState = {
  byEnvironment: {},
  warnings: [],
  selected: null,
  selectedEnvironment: "local",
  creating: false,
  editing: false,
  submitting: false,
  formError: null,
  busy: null,
  latestRequestId: null,
};

/** One environment's listing, or why it failed. */
export interface EnvironmentListing {
  readonly environment: string;
  readonly listing: LoopListing | null;
  readonly error: string | null;
}

export interface LoopsFetched {
  readonly results: readonly EnvironmentListing[];
  /** When the listings arrived (ms since the epoch). */
  readonly at: number;
}

/** Lists the loops of this machine and of each enrolled environment, in parallel. One that fails doesn't stop the rest. */
export const fetchLoops = createAppThunk<LoopsFetched, void>("loops/fetch", async (_arg, { extra, getState }) => {
  const enrolled = getState().environments.items.filter((env) => env.state === "enrolled");
  const names = ["local", ...enrolled.map((env) => env.name)];
  const results = await Promise.all(
    names.map(async (environment): Promise<EnvironmentListing> => {
      try {
        return { environment, listing: await extra.api.loopsList(environment), error: null };
      } catch (err: unknown) {
        return { environment, listing: null, error: toAppError(err).message };
      }
    }),
  );
  return { results, at: Date.now() };
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

export const setLoopPaused = createAppThunk<Loop, { name: string; paused: boolean; environment?: string }>(
  "loops/setPaused",
  async ({ name, paused, environment = "local" }, { extra, dispatch, rejectWithValue }) => {
    try {
      const updated = await extra.api.loopsSetPaused(name, paused, environment);
      return updated;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Deletes a loop, then lists the loops again. Its runs' workspaces stay. */
export const removeLoop = createAppThunk<Loop, LoopTarget & { confirmed?: boolean }>(
  "loops/remove",
  async ({ name, environment = "local", confirmed = false }, { extra, dispatch, rejectWithValue }) => {
    try {
      const removed = await extra.api.loopsRemove(name, confirmed, environment);
      void dispatch(fetchLoops());
      return removed;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Starts a run now, then lists the loops again. */
export const runLoopNow = createAppThunk<LoopStarted, LoopTarget>(
  "loops/runNow",
  async ({ name, environment = "local" }, { extra, dispatch, rejectWithValue }) => {
    try {
      const started = await extra.api.loopsRunNow(name, environment);
      dispatch(noticeShown({ severity: "success", message: `Started ${started.workspace}`, action: { label: "Open", agent: started.workspace } }));
      void dispatch(fetchLoops());
      return started;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** The loop an action is running on, for the busy marker. */
const busyName = (arg: { readonly name: string; readonly environment?: string }): string => loopKey(arg.name, arg.environment);

/** Whether an action is running on this loop of this environment. */
export const selectLoopBusy = (state: RootState, name: string, environment = "local"): boolean =>
  state.loops.busy === loopKey(name, environment);

/** The state with one loop replaced in its environment's list. */
const withLoop = (state: LoopsState, environment: string, updated: Loop): LoopsState => {
  const held = state.byEnvironment[environment] ?? emptyEnvironment;
  const items = held.items.map((item) => (item.name === updated.name ? updated : item));
  return { ...state, byEnvironment: { ...state.byEnvironment, [environment]: { ...held, items } } };
};

/** One group of the Loops list: an environment and what it holds. */
export interface LoopGroup {
  readonly environment: string;
  readonly online: boolean;
  /** Why it isn't online; empty when it is. */
  readonly reason: string;
  /** Some of its loops come from an old snapshot. */
  readonly stale: boolean;
  /** It hasn't reported its loops yet. */
  readonly loopsUnknown: boolean;
  readonly items: readonly Loop[];
}

/** `local` first, then each enrolled environment by name. */
export const selectLoopGroups = (state: RootState, now: number = Date.now()): readonly LoopGroup[] => {
  const group = (environment: string, online: boolean, reason: string): LoopGroup => {
    const held = state.loops.byEnvironment[environment] ?? emptyEnvironment;
    return { environment, online, reason, stale: held.items.some((item) => item.stale), loopsUnknown: held.loopsUnknown, items: held.items };
  };
  const remote = state.environments.items
    .filter((env) => env.state === "enrolled")
    .toSorted((a, b) => a.name.localeCompare(b.name))
    .map((env) => group(env.name, env.status === "online", offlineReason(env, now)));
  return [group("local", true, ""), ...remote];
};

const loopsSlice = createSlice({
  name: "loops",
  initialState,
  reducers: {
    loopSelected: {
      reducer: (state, action: PayloadAction<{ readonly name: string | null; readonly environment: string }>) => {
        state.selected = action.payload.name;
        state.selectedEnvironment = action.payload.environment;
        state.creating = false;
        state.editing = false;
      },
      prepare: (name: string | null, environment = "local") => ({ payload: { name, environment } }),
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
      .addCase(fetchLoops.pending, (state, action) => {
        state.latestRequestId = action.meta.requestId;
      })
      .addCase(fetchLoops.fulfilled, (state, action): LoopsState => {
        if (state.latestRequestId !== null && action.meta.requestId !== state.latestRequestId) {
          return state;
        }
        const { results, at } = action.payload;
        const byEnvironment = Object.fromEntries(
          results.map(({ environment, listing, error }): [string, EnvironmentLoops] => {
            if (listing === null) {
              return [environment, { ...(state.byEnvironment[environment] ?? emptyEnvironment), error }];
            }
            return [environment, { items: listing.loops, loadedAt: at, error: null, loopsUnknown: listing.loopsUnknown }];
          }),
        );
        const local = results.find((r) => r.environment === "local")?.listing;
        const own = results.find((r) => r.environment === state.selectedEnvironment);
        const gone = state.selected !== null && own?.listing != null && !own.listing.loops.some((loop) => loop.name === state.selected);
        return { ...state, byEnvironment, warnings: local?.warnings ?? state.warnings, selected: gone ? null : state.selected };
      })
      .addCase(addLoop.pending, (state) => {
        state.submitting = true;
      })
      .addCase(addLoop.fulfilled, (state, action) => {
        state.submitting = false;
        state.creating = false;
        state.formError = null;
        state.selected = action.payload.name;
        state.selectedEnvironment = action.meta.arg.environment ?? "local";
      })
      .addCase(addLoop.rejected, (state, action) => {
        state.submitting = false;
        state.formError = action.payload ?? { code: "internal", message: action.error.message ?? "adding loop failed" };
      })
      .addCase(editLoop.pending, (state) => {
        state.submitting = true;
      })
      .addCase(editLoop.fulfilled, (state, action): LoopsState => ({
        ...withLoop(state, action.meta.arg.environment ?? "local", action.payload),
        submitting: false,
        editing: false,
        formError: null,
      }))
      .addCase(editLoop.rejected, (state, action) => {
        state.submitting = false;
        state.formError = action.payload ?? { code: "internal", message: action.error.message ?? "editing loop failed" };
      })
      .addCase(setLoopPaused.pending, (state, action) => {
        state.busy = busyName(action.meta.arg);
      })
      .addCase(setLoopPaused.fulfilled, (state, action): LoopsState => ({
        ...withLoop(state, action.meta.arg.environment ?? "local", action.payload),
        busy: null,
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

/** The loop an action names: its name, and the environment it lives on ("local" when absent). */
export interface LoopTarget {
  readonly name: string;
  readonly environment?: string;
}

export const { loopSelected, creatingStarted, creatingCancelled, editingStarted, editingCancelled } = loopsSlice.actions;
export const loopsReducer = loopsSlice.reducer;
