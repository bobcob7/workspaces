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

/** How many runs one history fetch asks for: all of a loop's retained history (ws keeps 200). */
export const historyFetchLimit = 200;

export interface LoopDataState {
  /** Whose data is held; reset when it changes. */
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
const holding = (state: LoopDataState, name: string): LoopDataState => (state.loop === name ? state : { ...initialState, loop: name });

const failure = (error: AppError | undefined, message: string): AppError => error ?? { code: "internal", message };

export const fetchLoopHistory = createAppThunk<LoopHistory, { name: string; limit: number }>(
  "loopData/fetchHistory",
  async ({ name, limit }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.loopsHistory(name, limit);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const fetchLoopRun = createAppThunk<LoopRunDetail, { name: string; seq: number }>(
  "loopData/fetchRun",
  async ({ name, seq }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.loopsHistoryRun(name, seq);
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Clears a loop's history, then loads the history again. */
export const clearLoopHistory = createAppThunk<LoopCleared, string>(
  "loopData/clearHistory",
  async (name, { extra, dispatch, rejectWithValue }) => {
    try {
      const cleared = await extra.api.loopsHistoryClear(name);
      await dispatch(fetchLoopHistory({ name, limit: historyFetchLimit }));
      return cleared;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

export const fetchLoopMemory = createAppThunk<LoopMemory, string>("loopData/fetchMemory", async (name, { extra, rejectWithValue }) => {
  try {
    return await extra.api.loopsMemory(name);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Sets one memory value, then loads the memory again. */
export const saveLoopMemory = createAppThunk<LoopMemorySet, { name: string; key: string; value: string; expectRev: number | null }>(
  "loopData/saveMemory",
  async ({ name, key, value, expectRev }, { extra, dispatch, rejectWithValue }) => {
    try {
      const saved = await extra.api.loopsMemorySet(name, key, value, expectRev);
      await dispatch(fetchLoopMemory(name));
      return saved;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Removes one memory value, then loads the memory again. */
export const removeLoopMemory = createAppThunk<LoopMemoryRemoved, { name: string; key: string }>(
  "loopData/removeMemory",
  async ({ name, key }, { extra, dispatch, rejectWithValue }) => {
    try {
      const removed = await extra.api.loopsMemoryRemove(name, key);
      await dispatch(fetchLoopMemory(name));
      return removed;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

/** Removes all of a loop's memory, then loads the memory again. */
export const clearLoopMemory = createAppThunk<LoopCleared, string>("loopData/clearMemory", async (name, { extra, dispatch, rejectWithValue }) => {
  try {
    const cleared = await extra.api.loopsMemoryClear(name);
    await dispatch(fetchLoopMemory(name));
    return cleared;
  } catch (err: unknown) {
    const error = toAppError(err);
    dispatch(noticeShown({ severity: "error", message: error.message }));
    return rejectWithValue(error);
  }
});

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
        ...holding(state, action.payload.loop),
        history: action.payload,
        historyError: null,
      }))
      .addCase(fetchLoopHistory.rejected, (state, action): LoopDataState => ({
        ...holding(state, action.meta.arg.name),
        historyError: failure(action.payload, action.error.message ?? "loading history failed"),
      }))
      .addCase(fetchLoopRun.pending, (state) => {
        state.runLoading = true;
      })
      .addCase(fetchLoopRun.fulfilled, (state, action): LoopDataState => ({
        ...holding(state, action.payload.loop),
        run: action.payload,
        runLoading: false,
      }))
      .addCase(fetchLoopRun.rejected, (state) => {
        state.runLoading = false;
      })
      .addCase(fetchLoopMemory.fulfilled, (state, action): LoopDataState => ({
        ...holding(state, action.payload.loop),
        memory: action.payload,
        memoryError: null,
      }))
      .addCase(fetchLoopMemory.rejected, (state, action): LoopDataState => ({
        ...holding(state, action.meta.arg),
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
