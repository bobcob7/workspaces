import { createSlice } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type LoopCleared,
  type LoopHistory,
  type LoopMemory,
  type LoopMemoryRemoved,
  type LoopMemorySet,
  type LoopRunDetail,
} from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown } from "../ui/uiSlice";
import type { LoopTarget } from "./loopsSlice";

/** How many runs one history fetch asks for: all of a loop's retained history (ws keeps 200). */
export const historyFetchLimit = 200;

/** The key a loop's data is held under: its environment and name, since a name is only unique on one environment. */
export const loopKey = (name: string, environment = "local"): string => `${environment}/${name}`;

export interface LoopDataState {
  /** Whose data is held, as `loopKey` makes it; reset when it changes. */
  readonly loop: string | null;
  readonly history: LoopHistory | null;
  readonly historyError: AppError | null;
  /** The run open in the dialog. */
  readonly run: LoopRunDetail | null;
  readonly runLoading: boolean;
  readonly memory: LoopMemory | null;
  readonly memoryError: AppError | null;
  readonly saving: boolean;
  /** memory_conflict means the value changed since it was read; the UI offers a reload. */
  readonly saveError: AppError | null;
}

const initialState: LoopDataState = {
  loop: null,
  history: null,
  historyError: null,
  run: null,
  runLoading: false,
  memory: null,
  memoryError: null,
  saving: false,
  saveError: null,
};

/** The state held for `name`: the current one when it's already that loop's, otherwise a fresh one. */
const holding = (state: LoopDataState, key: string): LoopDataState => (state.loop === key ? state : { ...initialState, loop: key });

const keyOf = ({ name, environment }: LoopTarget): string => loopKey(name, environment);

const failure = (error: AppError | undefined, message: string): AppError => error ?? { code: "internal", message };

export const fetchLoopHistory = createAppThunk<LoopHistory, LoopTarget & { limit: number }>(
  "loopData/fetchHistory",
  async ({ name, limit, environment = "local" }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.loopsHistory(name, limit, environment);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const fetchLoopRun = createAppThunk<LoopRunDetail, LoopTarget & { seq: number }>(
  "loopData/fetchRun",
  async ({ name, seq, environment = "local" }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.loopsHistoryRun(name, seq, environment);
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Clears a loop's history, then loads the history again. */
export const clearLoopHistory = createAppThunk<LoopCleared, LoopTarget & { confirmed?: boolean }>(
  "loopData/clearHistory",
  async ({ name, environment = "local", confirmed = false }, { extra, dispatch, rejectWithValue }) => {
    try {
      const cleared = await extra.api.loopsHistoryClear(name, confirmed, environment);
      await dispatch(fetchLoopHistory({ name, limit: historyFetchLimit, environment }));
      return cleared;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

export const fetchLoopMemory = createAppThunk<LoopMemory, LoopTarget>("loopData/fetchMemory", async ({ name, environment = "local" }, { extra, rejectWithValue }) => {
  try {
    return await extra.api.loopsMemory(name, environment);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Sets one memory value, then loads the memory again. */
export const saveLoopMemory = createAppThunk<LoopMemorySet, LoopTarget & { key: string; value: string; expectRev: number | null }>(
  "loopData/saveMemory",
  async ({ name, key, value, expectRev, environment = "local" }, { extra, dispatch, rejectWithValue }) => {
    try {
      const saved = await extra.api.loopsMemorySet(name, key, value, expectRev, environment);
      await dispatch(fetchLoopMemory({ name, environment }));
      return saved;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Removes one memory value, then loads the memory again. */
export const removeLoopMemory = createAppThunk<LoopMemoryRemoved, LoopTarget & { key: string }>(
  "loopData/removeMemory",
  async ({ name, key, environment = "local" }, { extra, dispatch, rejectWithValue }) => {
    try {
      const removed = await extra.api.loopsMemoryRemove(name, key, environment);
      await dispatch(fetchLoopMemory({ name, environment }));
      return removed;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Removes all of a loop's memory, then loads the memory again. */
export const clearLoopMemory = createAppThunk<LoopCleared, LoopTarget & { confirmed?: boolean }>(
  "loopData/clearMemory",
  async ({ name, environment = "local", confirmed = false }, { extra, dispatch, rejectWithValue }) => {
    try {
      const cleared = await extra.api.loopsMemoryClear(name, confirmed, environment);
      await dispatch(fetchLoopMemory({ name, environment }));
      return cleared;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

const loopDataSlice = createSlice({
  name: "loopData",
  initialState,
  reducers: {
    runClosed: (state) => {
      state.run = null;
      state.runLoading = false;
    },
    saveErrorCleared: (state) => {
      state.saveError = null;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchLoopHistory.fulfilled, (state, action): LoopDataState => ({
        ...holding(state, keyOf(action.meta.arg)),
        history: action.payload,
        historyError: null,
      }))
      .addCase(fetchLoopHistory.rejected, (state, action): LoopDataState => ({
        ...holding(state, keyOf(action.meta.arg)),
        historyError: failure(action.payload, action.error.message ?? "loading history failed"),
      }))
      .addCase(fetchLoopRun.pending, (state) => {
        state.runLoading = true;
      })
      .addCase(fetchLoopRun.fulfilled, (state, action): LoopDataState => ({
        ...holding(state, keyOf(action.meta.arg)),
        run: action.payload,
        runLoading: false,
      }))
      .addCase(fetchLoopRun.rejected, (state) => {
        state.runLoading = false;
      })
      .addCase(fetchLoopMemory.fulfilled, (state, action): LoopDataState => ({
        ...holding(state, keyOf(action.meta.arg)),
        memory: action.payload,
        memoryError: null,
      }))
      .addCase(fetchLoopMemory.rejected, (state, action): LoopDataState => ({
        ...holding(state, keyOf(action.meta.arg)),
        memoryError: failure(action.payload, action.error.message ?? "loading memory failed"),
      }))
      .addCase(saveLoopMemory.pending, (state) => {
        state.saving = true;
        state.saveError = null;
      })
      .addCase(saveLoopMemory.fulfilled, (state) => {
        state.saving = false;
      })
      .addCase(saveLoopMemory.rejected, (state, action) => {
        state.saving = false;
        state.saveError = failure(action.payload, action.error.message ?? "saving memory failed");
      });
  },
});

export const { runClosed, saveErrorCleared } = loopDataSlice.actions;
export const loopDataReducer = loopDataSlice.reducer;
