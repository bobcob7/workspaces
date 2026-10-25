import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type PullRequest, type ReviewPlan, type ReviewsView, type Started } from "../../api";
import { createAppThunk } from "../../thunk";
import { fetchEntries } from "../agents/agentsSlice";
import { noticeShown } from "../ui/uiSlice";

/** The Edit before starting dialog: the pull request and the plan being edited. */
export interface StartDialog {
  readonly key: string;
  readonly plan: ReviewPlan | null;
  readonly error: AppError | null;
}

export interface ReviewsState {
  readonly view: ReviewsView | null;
  readonly loading: boolean;
  readonly error: AppError | null;
  readonly selectedKey: string | null;
  readonly filter: string;
  readonly dismissedOpen: boolean;
  readonly watchedOpen: boolean;
  readonly startDialog: StartDialog | null;
  /** The default plan for each pull request, for the Review will run card. */
  readonly plans: Readonly<Record<string, ReviewPlan>>;
  /** Keys of pull requests with an action in flight. */
  readonly busy: readonly string[];
}

const initialState: ReviewsState = {
  view: null,
  loading: false,
  error: null,
  selectedKey: null,
  filter: "",
  dismissedOpen: false,
  watchedOpen: false,
  startDialog: null,
  plans: {},
  busy: [],
};

const failed = (err: unknown, dispatch: (action: ReturnType<typeof noticeShown>) => unknown): AppError => {
  const error = toAppError(err);
  dispatch(noticeShown({ severity: "error", message: error.message }));
  return error;
};

/** Reads the last check, joined with the latest agent listing. */
export const fetchReviews = createAppThunk<ReviewsView, void>(
  "reviews/fetch",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewsView();
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  { condition: (_arg, { getState }) => !getState().reviews.loading },
);

export const checkNow = createAppThunk<ReviewsView, void>("reviews/checkNow", async (_arg, { extra, dispatch, rejectWithValue }) => {
  try {
    return await extra.api.reviewsCheckNow();
  } catch (err: unknown) {
    return rejectWithValue(failed(err, dispatch));
  }
});

export const loadRepos = createAppThunk<ReviewsView, void>("reviews/loadRepos", async (_arg, { extra, dispatch, rejectWithValue }) => {
  try {
    return await extra.api.reviewsLoadRepos();
  } catch (err: unknown) {
    return rejectWithValue(failed(err, dispatch));
  }
});

export interface WatchChange {
  readonly repo: string;
  readonly watch: boolean;
  readonly autoReview: boolean;
}

export const setWatch = createAppThunk<ReviewsView, WatchChange>(
  "reviews/setWatch",
  async ({ repo, watch, autoReview }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.reviewsSetWatch(repo, watch, autoReview);
    } catch (err: unknown) {
      return rejectWithValue(failed(err, dispatch));
    }
  },
);

export const dismissPr = createAppThunk<ReviewsView, PullRequest>(
  "reviews/dismiss",
  async (pr, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.reviewsDismiss(pr.repo, pr.number, pr.headRefOid);
    } catch (err: unknown) {
      return rejectWithValue(failed(err, dispatch));
    }
  },
);

export const restorePr = createAppThunk<ReviewsView, PullRequest>(
  "reviews/restore",
  async (pr, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.reviewsRestore(pr.repo, pr.number);
    } catch (err: unknown) {
      return rejectWithValue(failed(err, dispatch));
    }
  },
);

/** The plan Review would run now, with the Settings defaults. */
export const fetchPlan = createAppThunk<ReviewPlan, PullRequest>(
  "reviews/plan",
  async (pr, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewsPlan(pr.repo, pr.number);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const openStartDialog = createAppThunk<ReviewPlan, PullRequest>(
  "reviews/openStart",
  async (pr, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewsPlan(pr.repo, pr.number);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export interface StartRequest {
  readonly pr: PullRequest;
  /** An edited plan; without one, the Settings defaults. */
  readonly plan?: ReviewPlan;
}

/** Starts a review (`ws claude`) and keeps the pull request selected. */
export const startReview = createAppThunk<Started, StartRequest>(
  "reviews/start",
  async ({ pr, plan }, { extra, dispatch, rejectWithValue }) => {
    try {
      const started = await extra.api.reviewsStart(plan ?? (await extra.api.reviewsPlan(pr.repo, pr.number)));
      const message = started.already ? `${started.id} is already reviewing it` : `Started ${started.id}`;
      dispatch(noticeShown({ severity: started.already ? "info" : "success", message }));
      await dispatch(fetchEntries());
      void dispatch(fetchReviews());
      return started;
    } catch (err: unknown) {
      const error = toAppError(err);
      if (plan === undefined) {
        dispatch(noticeShown({ severity: "error", message: error.message }));
      }
      return rejectWithValue(error);
    }
  },
);

const reviewsSlice = createSlice({
  name: "reviews",
  initialState,
  reducers: {
    reviewSelected: (state, action: PayloadAction<string | null>) => {
      state.selectedKey = action.payload;
    },
    filterChanged: (state, action: PayloadAction<string>) => {
      state.filter = action.payload;
    },
    dismissedToggled: (state) => {
      state.dismissedOpen = !state.dismissedOpen;
    },
    watchedOpened: (state) => {
      state.watchedOpen = true;
    },
    watchedClosed: (state) => {
      state.watchedOpen = false;
    },
    startDialogClosed: (state) => {
      state.startDialog = null;
    },
  },
  extraReducers: (builder) => {
    const setView = (state: ReviewsState, action: PayloadAction<ReviewsView>): ReviewsState => ({
      ...state,
      view: action.payload,
    });
    builder
      .addCase(fetchReviews.pending, (state) => {
        state.loading = true;
      })
      .addCase(fetchReviews.fulfilled, (state, action): ReviewsState => ({
        ...state,
        loading: false,
        error: null,
        view: action.payload,
      }))
      .addCase(fetchReviews.rejected, (state, action) => {
        state.loading = false;
        state.error = action.payload ?? { code: "internal", message: action.error.message ?? "reading reviews failed" };
      })
      .addCase(checkNow.fulfilled, setView)
      .addCase(loadRepos.fulfilled, setView)
      .addCase(setWatch.fulfilled, setView)
      .addCase(dismissPr.fulfilled, setView)
      .addCase(restorePr.fulfilled, setView)
      .addCase(fetchPlan.fulfilled, (state, action) => {
        state.plans[action.meta.arg.key] = action.payload;
      })
      .addCase(openStartDialog.pending, (state, action) => {
        state.startDialog = { key: action.meta.arg.key, plan: null, error: null };
      })
      .addCase(openStartDialog.fulfilled, (state, action) => {
        if (state.startDialog?.key === action.meta.arg.key) {
          state.startDialog = { key: action.meta.arg.key, plan: action.payload, error: null };
        }
      })
      .addCase(openStartDialog.rejected, (state, action) => {
        if (state.startDialog?.key === action.meta.arg.key) {
          state.startDialog = {
            key: action.meta.arg.key,
            plan: null,
            error: action.payload ?? { code: "internal", message: action.error.message ?? "failed" },
          };
        }
      })
      .addCase(startReview.pending, (state, action) => {
        state.busy = [...state.busy, action.meta.arg.pr.key];
        if (state.startDialog !== null) {
          state.startDialog = { ...state.startDialog, error: null };
        }
      })
      .addCase(startReview.fulfilled, (state, action) => {
        state.busy = state.busy.filter((key) => key !== action.meta.arg.pr.key);
        state.selectedKey = action.meta.arg.pr.key;
        state.startDialog = null;
      })
      .addCase(startReview.rejected, (state, action) => {
        state.busy = state.busy.filter((key) => key !== action.meta.arg.pr.key);
        if (state.startDialog?.key === action.meta.arg.pr.key) {
          state.startDialog = {
            ...state.startDialog,
            error: action.payload ?? { code: "internal", message: action.error.message ?? "failed" },
          };
        }
      });
  },
});

export const { reviewSelected, filterChanged, dismissedToggled, watchedOpened, watchedClosed, startDialogClosed } =
  reviewsSlice.actions;
export const reviewsReducer = reviewsSlice.reducer;
