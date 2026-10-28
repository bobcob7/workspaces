import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type ListedProfile, type ReviewSettings, type Settings } from "../../api";
import { createAppThunk } from "../../thunk";

/** Settings groups; a group is listed once it has settings. */
export type SettingsGroup = "reviews";

export type ReviewSettingKey = keyof ReviewSettings;

export interface SettingsState {
  readonly settings: Settings | null;
  readonly error: AppError | null;
  readonly group: SettingsGroup;
  /** The error under each control whose last change failed to save. */
  readonly fieldErrors: Readonly<Partial<Record<ReviewSettingKey, string>>>;
  /** Every reviewer profile, from `ws review profiles`. */
  readonly profiles: readonly ListedProfile[] | null;
  readonly profilesError: AppError | null;
}

const initialState: SettingsState = {
  settings: null,
  error: null,
  group: "reviews",
  fieldErrors: {},
  profiles: null,
  profilesError: null,
};

export const fetchSettings = createAppThunk<Settings, void>("settings/fetch", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.settingsGet();
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export const fetchProfiles = createAppThunk<readonly ListedProfile[], void>(
  "settings/fetchProfiles",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewsProfiles();
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/**
 * Saves a change to the Reviews settings as it's made. On failure the old
 * value stays and the error shows under the changed control.
 */
export const saveReviewSettings = createAppThunk<Settings, Partial<ReviewSettings>>(
  "settings/saveReviews",
  async (change, { extra, getState, rejectWithValue }) => {
    const current = getState().settings.settings;
    try {
      const base = current ?? (await extra.api.settingsGet());
      return await extra.api.settingsSetReviews({ ...base.reviews, ...change });
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

const changedKeys = (change: Partial<ReviewSettings>): readonly ReviewSettingKey[] =>
  Object.keys(change).filter((key): key is ReviewSettingKey => key in change);

const settingsSlice = createSlice({
  name: "settings",
  initialState,
  reducers: {
    groupChosen: (state, action: PayloadAction<SettingsGroup>) => {
      state.group = action.payload;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchSettings.fulfilled, (state, action) => {
        state.settings = action.payload;
        state.error = null;
      })
      .addCase(fetchSettings.rejected, (state, action) => {
        state.error = action.payload ?? { code: "internal", message: action.error.message ?? "reading settings failed" };
      })
      .addCase(fetchProfiles.fulfilled, (state, action): SettingsState => ({
        ...state,
        profiles: action.payload,
        profilesError: null,
      }))
      .addCase(fetchProfiles.rejected, (state, action) => {
        state.profilesError = action.payload ?? { code: "internal", message: action.error.message ?? "listing profiles failed" };
      })
      .addCase(saveReviewSettings.fulfilled, (state, action): SettingsState => {
        const fieldErrors = { ...state.fieldErrors };
        for (const key of changedKeys(action.meta.arg)) {
          delete fieldErrors[key];
        }
        return { ...state, settings: action.payload, fieldErrors };
      })
      .addCase(saveReviewSettings.rejected, (state, action) => {
        const message = action.payload?.message ?? action.error.message ?? "saving failed";
        for (const key of changedKeys(action.meta.arg)) {
          state.fieldErrors[key] = message;
        }
      });
  },
});

export const { groupChosen } = settingsSlice.actions;
export const settingsReducer = settingsSlice.reducer;
