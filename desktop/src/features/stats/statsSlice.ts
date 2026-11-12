import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type Stats } from "../../api";
import { createAppThunk } from "../../thunk";

export type StatsWindow = "today" | "7d" | "30d" | "all";

/** In list order. */
export const statsWindows: readonly StatsWindow[] = ["today", "7d", "30d", "all"];

export const windowLabels: Readonly<Record<StatsWindow, string>> = {
  today: "Today",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
  all: "All",
};

export interface WindowResult {
  readonly data: Stats | null;
  readonly error: AppError | null;
  readonly loading: boolean;
}

export interface StatsState {
  /** The window the Stats section shows; the bar always shows today. */
  readonly window: StatsWindow;
  readonly results: Readonly<Record<StatsWindow, WindowResult>>;
}

const emptyResult: WindowResult = { data: null, error: null, loading: false };

const initialState: StatsState = {
  window: "today",
  results: { today: emptyResult, "7d": emptyResult, "30d": emptyResult, all: emptyResult },
};

const dayMs = 86_400_000;

/** sinceMs for a window at `now`: local midnight, now − 7 or 30 days, or null. */
export const sinceFor = (window: StatsWindow, now: Date): number | null => {
  switch (window) {
    case "today":
      return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
    case "7d":
      return now.getTime() - 7 * dayMs;
    case "30d":
      return now.getTime() - 30 * dayMs;
    case "all":
      return null;
  }
};

export const fetchStats = createAppThunk<Stats, { readonly window: StatsWindow; readonly sinceMs: number | null }>(
  "stats/fetch",
  async ({ sinceMs }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.statsGet(sinceMs);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

const withResult = (state: StatsState, window: StatsWindow, change: Partial<WindowResult>): StatsState => ({
  ...state,
  results: { ...state.results, [window]: { ...state.results[window], ...change } },
});

const statsSlice = createSlice({
  name: "stats",
  initialState,
  reducers: {
    windowChosen: (state, action: PayloadAction<StatsWindow>): StatsState => ({ ...state, window: action.payload }),
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchStats.pending, (state, action): StatsState => withResult(state, action.meta.arg.window, { loading: true }))
      .addCase(
        fetchStats.fulfilled,
        (state, action): StatsState => withResult(state, action.meta.arg.window, { data: action.payload, error: null, loading: false }),
      )
      .addCase(
        fetchStats.rejected,
        (state, action): StatsState =>
          withResult(state, action.meta.arg.window, {
            error: action.payload ?? { code: "internal", message: action.error.message ?? "loading stats failed" },
            loading: false,
          }),
      );
  },
});

export const { windowChosen } = statsSlice.actions;
export const statsReducer = statsSlice.reducer;
