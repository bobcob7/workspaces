import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import {
  toAppError,
  type AppError,
  type ConfigChange,
  type ConfigListing,
  type ConfigSetting,
  type Environment,
  type ListedProfile,
  type NotificationSettings,
  type OpenSettings,
  type ProfileDetail,
  type ProfileDraft,
  type ProfileRemoved,
  type Resolution,
  type ReviewSettings,
  type SearchMatch,
  type Settings,
  type Versions,
} from "../../api";
import { createAppThunk } from "../../thunk";

/** Settings groups, in list order. */
export type SettingsGroup = "repos" | "reviews" | "roles" | "notifications" | "workspaces" | "about";

export type ReviewSettingKey = keyof ReviewSettings;

export interface ResolveState {
  readonly spec: string;
  readonly resolution: Resolution | null;
  readonly error: AppError | null;
  readonly pending: boolean;
}

export interface SettingsState {
  readonly settings: Settings | null;
  readonly error: AppError | null;
  readonly group: SettingsGroup;
  /** The error under each control whose last change failed to save. */
  readonly fieldErrors: Readonly<Partial<Record<ReviewSettingKey, string>>>;
  /** The error under the Notifications group when its last change failed to save. */
  readonly notificationsError: string | null;
  /** Where Settings > Workspaces gets its placeholders; null until fetched. */
  readonly openDefaults: OpenSettings | null;
  /** The error under each command whose last change failed to save. */
  readonly openErrors: Readonly<Partial<Record<keyof OpenSettings, string>>>;
  /** Every reviewer profile, from `ws review profiles`. */
  readonly profiles: readonly ListedProfile[] | null;
  readonly profilesError: AppError | null;
  /** The app's, ws's, and claude's versions, for About. */
  readonly versions: Versions | null;
  readonly versionsError: AppError | null;
  /** config.toml's keys, from `ws config list`. */
  readonly config: ConfigListing | null;
  readonly configError: AppError | null;
  /** The error under each config.toml control whose last change failed, by key. */
  readonly configErrors: Readonly<Record<string, string>>;
  readonly resolve: ResolveState;
  readonly searchMatches: readonly SearchMatch[];
  readonly environment: Environment | null;
  readonly environmentError: AppError | null;
}

const initialState: SettingsState = {
  settings: null,
  error: null,
  group: "reviews",
  fieldErrors: {},
  notificationsError: null,
  openDefaults: null,
  openErrors: {},
  profiles: null,
  profilesError: null,
  versions: null,
  versionsError: null,
  config: null,
  configError: null,
  configErrors: {},
  resolve: { spec: "", resolution: null, error: null, pending: false },
  searchMatches: [],
  environment: null,
  environmentError: null,
};

export const fetchSettings = createAppThunk<Settings, void>("settings/fetch", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.settingsGet();
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export const fetchVersions = createAppThunk<Versions, void>("settings/fetchVersions", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.versions();
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

/** One profile with its guide, for the editor. */
export const fetchProfileDetail = createAppThunk<ProfileDetail, string>(
  "settings/fetchProfileDetail",
  async (name, { extra, rejectWithValue }) => {
    try {
      return await extra.api.reviewsProfileShow(name);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export interface SaveProfile {
  readonly name: string;
  readonly draft: ProfileDraft;
  /** `ws review profiles add`, rather than `edit`. */
  readonly create: boolean;
}

/** Adds or edits a profile, then lists the profiles again. */
export const saveProfile = createAppThunk<ProfileDetail, SaveProfile>(
  "settings/saveProfile",
  async ({ name, draft, create }, { extra, dispatch, rejectWithValue }) => {
    try {
      const saved = create ? await extra.api.reviewsProfileAdd(name, draft) : await extra.api.reviewsProfileEdit(name, draft);
      void dispatch(fetchProfiles());
      return saved;
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/**
 * Deletes a custom profile, or resets an edited built-in, then lists the
 * profiles again. A profile that's gone loses its Runs and Model row.
 */
export const removeProfile = createAppThunk<ProfileRemoved, string>(
  "settings/removeProfile",
  async (name, { extra, dispatch, rejectWithValue }) => {
    try {
      const removed = await extra.api.reviewsProfileRemove(name);
      void dispatch(fetchProfiles());
      return removed;
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

/** Saves a change to the Notifications settings as it's made. */
export const saveNotificationSettings = createAppThunk<Settings, Partial<NotificationSettings>>(
  "settings/saveNotifications",
  async (change, { extra, getState, rejectWithValue }) => {
    const current = getState().settings.settings;
    try {
      const base = current ?? (await extra.api.settingsGet());
      return await extra.api.settingsSetNotifications({ ...base.notifications, ...change });
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const fetchOpenDefaults = createAppThunk<OpenSettings, void>(
  "settings/fetchOpenDefaults",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      return await extra.api.openDefaults();
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Saves a change to the Workspaces commands as it's made. */
export const saveOpenSettings = createAppThunk<Settings, Partial<OpenSettings>>(
  "settings/saveOpen",
  async (change, { extra, getState, rejectWithValue }) => {
    const current = getState().settings.settings;
    try {
      const base = current ?? (await extra.api.settingsGet());
      return await extra.api.settingsSetOpen({ ...base.open, ...change });
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const fetchConfig = createAppThunk<ConfigListing, void>("settings/fetchConfig", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.configList();
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Sets one config.toml key to the values given, in order. */
export const setConfig = createAppThunk<ConfigChange, { key: string; values: readonly string[] }>(
  "settings/setConfig",
  async ({ key, values }, { extra, rejectWithValue }) => {
    try {
      return await extra.api.configSet(key, values);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Removes one config.toml key, so its default applies. */
export const unsetConfig = createAppThunk<ConfigChange, string>("settings/unsetConfig", async (key, { extra, rejectWithValue }) => {
  try {
    return await extra.api.configUnset(key);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export const resolveSpec = createAppThunk<Resolution, string>("settings/resolveSpec", async (spec, { extra, rejectWithValue }) => {
  try {
    return await extra.api.cacheResolve(spec);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** Cached repos that match the text typed, with their refs. */
export const searchSpecs = createAppThunk<readonly SearchMatch[], string>("settings/searchSpecs", async (query, { extra, rejectWithValue }) => {
  try {
    return await extra.api.cacheSearch(query, true, 8);
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

export const fetchEnvironment = createAppThunk<Environment, void>("settings/fetchEnvironment", async (_arg, { extra, rejectWithValue }) => {
  try {
    return await extra.api.settingsEnvironment();
  } catch (err: unknown) {
    return rejectWithValue(toAppError(err));
  }
});

/** The listing's setting for key, or undefined. */
export const settingOf = (config: ConfigListing | null, key: string): ConfigSetting | undefined =>
  config?.settings.find((setting) => setting.key === key);

const withoutError = (errors: Readonly<Record<string, string>>, key: string): Readonly<Record<string, string>> =>
  Object.fromEntries(Object.entries(errors).filter(([errorKey]) => errorKey !== key));

/** Puts a changed setting into the listing, replacing or appending it. */
const withChange = (config: ConfigListing | null, change: ConfigChange): ConfigListing | null => {
  if (config === null) {
    return null;
  }
  const { key } = change.setting;
  const settings = config.settings.some((setting) => setting.key === key)
    ? config.settings.map((setting) => (setting.key === key ? change.setting : setting))
    : [...config.settings, change.setting];
  return { path: change.path, settings };
};

const changedKeys = (change: Partial<ReviewSettings>): readonly ReviewSettingKey[] =>
  Object.keys(change).filter((key): key is ReviewSettingKey => key in change);

const changedOpenKeys = (change: Partial<OpenSettings>): readonly (keyof OpenSettings)[] =>
  Object.keys(change) as (keyof OpenSettings)[];

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
      .addCase(fetchVersions.fulfilled, (state, action): SettingsState => ({
        ...state,
        versions: action.payload,
        versionsError: null,
      }))
      .addCase(fetchVersions.rejected, (state, action) => {
        state.versionsError = action.payload ?? { code: "internal", message: action.error.message ?? "reading versions failed" };
      })
      .addCase(fetchProfiles.fulfilled, (state, action): SettingsState => ({
        ...state,
        profiles: action.payload,
        profilesError: null,
      }))
      .addCase(fetchProfiles.rejected, (state, action) => {
        state.profilesError = action.payload ?? { code: "internal", message: action.error.message ?? "listing profiles failed" };
      })
      .addCase(removeProfile.fulfilled, (state, action): SettingsState => ({
        ...state,
        settings: action.payload.settings,
      }))
      .addCase(saveReviewSettings.fulfilled, (state, action): SettingsState => {
        const fieldErrors = { ...state.fieldErrors };
        for (const key of changedKeys(action.meta.arg)) {
          delete fieldErrors[key];
        }
        return { ...state, settings: action.payload, fieldErrors };
      })
      .addCase(saveNotificationSettings.fulfilled, (state, action): SettingsState => ({
        ...state,
        settings: action.payload,
        notificationsError: null,
      }))
      .addCase(saveNotificationSettings.rejected, (state, action) => {
        state.notificationsError = action.payload?.message ?? action.error.message ?? "saving failed";
      })
      .addCase(fetchOpenDefaults.fulfilled, (state, action) => {
        state.openDefaults = action.payload;
      })
      .addCase(saveOpenSettings.fulfilled, (state, action): SettingsState => {
        const openErrors = { ...state.openErrors };
        for (const key of changedOpenKeys(action.meta.arg)) {
          delete openErrors[key];
        }
        return { ...state, settings: action.payload, openErrors };
      })
      .addCase(saveOpenSettings.rejected, (state, action) => {
        const message = action.payload?.message ?? action.error.message ?? "saving failed";
        for (const key of changedOpenKeys(action.meta.arg)) {
          state.openErrors[key] = message;
        }
      })
      .addCase(fetchConfig.fulfilled, (state, action): SettingsState => ({
        ...state,
        config: action.payload,
        configError: null,
      }))
      .addCase(fetchConfig.rejected, (state, action) => {
        state.configError = action.payload ?? { code: "internal", message: action.error.message ?? "reading config failed" };
      })
      .addCase(setConfig.fulfilled, (state, action): SettingsState => ({
        ...state,
        config: withChange(state.config, action.payload),
        configErrors: withoutError(state.configErrors, action.meta.arg.key),
      }))
      .addCase(setConfig.rejected, (state, action) => {
        state.configErrors[action.meta.arg.key] = action.payload?.message ?? action.error.message ?? "saving failed";
      })
      .addCase(unsetConfig.fulfilled, (state, action): SettingsState => ({
        ...state,
        config: withChange(state.config, action.payload),
        configErrors: withoutError(state.configErrors, action.meta.arg),
      }))
      .addCase(unsetConfig.rejected, (state, action) => {
        state.configErrors[action.meta.arg] = action.payload?.message ?? action.error.message ?? "saving failed";
      })
      .addCase(resolveSpec.pending, (state, action) => {
        state.resolve = { spec: action.meta.arg, resolution: null, error: null, pending: true };
      })
      .addCase(resolveSpec.fulfilled, (state, action) => {
        if (action.meta.arg === state.resolve.spec) {
          state.resolve = { spec: action.meta.arg, resolution: action.payload, error: null, pending: false };
        }
      })
      .addCase(resolveSpec.rejected, (state, action) => {
        if (action.meta.arg === state.resolve.spec) {
          const error = action.payload ?? { code: "internal", message: action.error.message ?? "resolving failed" };
          state.resolve = { spec: action.meta.arg, resolution: null, error, pending: false };
        }
      })
      .addCase(searchSpecs.fulfilled, (state, action): SettingsState => ({
        ...state,
        searchMatches: action.payload,
      }))
      .addCase(searchSpecs.rejected, (state): SettingsState => ({
        ...state,
        searchMatches: [],
      }))
      .addCase(fetchEnvironment.fulfilled, (state, action) => {
        state.environment = action.payload;
        state.environmentError = null;
      })
      .addCase(fetchEnvironment.rejected, (state, action) => {
        state.environmentError = action.payload ?? { code: "internal", message: action.error.message ?? "reading the environment failed" };
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
