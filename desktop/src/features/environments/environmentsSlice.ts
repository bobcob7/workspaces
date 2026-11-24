import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type CaRotated, type EnvironmentListing, type EnvironmentRemoved, type RemoteEnvironment } from "../../api";
import { createAppThunk } from "../../thunk";

/** The localStorage key of the environment last chosen in a picker. The only thing stored: never an enrollment code (ADR 0075, D5). */
export const lastChoiceKey = "ws.environment.last";

export interface EnvironmentsState {
  readonly items: readonly RemoteEnvironment[];
  /** When the listing last loaded (ms since the epoch), null before the first load. */
  readonly loadedAt: number | null;
  readonly error: string | null;
  /** The listing's own warnings, shown above the environments in Settings. */
  readonly warnings: readonly string[];
  /** The environment last chosen in the New agent or New shell picker; "local" by default. */
  readonly lastChoice: string;
  /** The request id of the newest fetch; an older fetch that settles later is ignored. */
  readonly latestRequestId: string | null;
}

const readLastChoice = (): string => {
  try {
    const stored = window.localStorage.getItem(lastChoiceKey);
    return stored === null || stored === "" ? "local" : stored;
  } catch {
    return "local";
  }
};

const writeLastChoice = (name: string): void => {
  try {
    window.localStorage.setItem(lastChoiceKey, name);
  } catch {
    // Storage may be unavailable; the choice then lasts until the app closes.
  }
};

const initialState = (): EnvironmentsState => ({
  items: [],
  loadedAt: null,
  error: null,
  warnings: [],
  lastChoice: readLastChoice(),
  latestRequestId: null,
});

/** Copies only the declared fields, so nothing extra in a reply (a code, say) is kept in the store. */
const toEnvironment = (env: RemoteEnvironment): RemoteEnvironment => ({
  id: env.id,
  name: env.name,
  state: env.state,
  status: env.status,
  lastSeen: env.lastSeen,
  createdAt: env.createdAt,
  enrolledAt: env.enrolledAt,
  certNotAfter: env.certNotAfter,
  certCa: env.certCa,
  hello:
    env.hello === null
      ? null
      : {
          wsVersion: env.hello.wsVersion,
          os: env.hello.os,
          arch: env.hello.arch,
          claude: env.hello.claude,
          claudeVersion: env.hello.claudeVersion,
          certNotAfter: env.hello.certNotAfter,
        },
  workspaces: env.workspaces,
});

/** The listing and when it arrived. */
export interface LoadedEnvironments {
  readonly listing: EnvironmentListing;
  readonly at: number;
}

export const fetchEnvironments = createAppThunk<LoadedEnvironments, void>(
  "environments/fetch",
  async (_arg, { extra, rejectWithValue }) => {
    try {
      const listing = await extra.api.environmentsList();
      return { listing, at: Date.now() };
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

export const removeEnvironment = createAppThunk<EnvironmentRemoved, string>(
  "environments/remove",
  async (name, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.environmentRemove(name);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    } finally {
      void dispatch(fetchEnvironments());
    }
  },
);

export interface CaRotation {
  readonly finish: boolean;
  readonly force: boolean;
}

export const rotateCa = createAppThunk<CaRotated, CaRotation>(
  "environments/rotateCa",
  async ({ finish, force }, { extra, dispatch, rejectWithValue }) => {
    try {
      return await extra.api.environmentsCaRotate(finish, force);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    } finally {
      void dispatch(fetchEnvironments());
    }
  },
);

const environmentsSlice = createSlice({
  name: "environments",
  initialState,
  reducers: {
    lastChoiceSet(state, action: PayloadAction<string>) {
      state.lastChoice = action.payload;
      writeLastChoice(action.payload);
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchEnvironments.pending, (state, action) => {
        state.latestRequestId = action.meta.requestId;
      })
      .addCase(fetchEnvironments.fulfilled, (state, action) => {
        if (action.meta.requestId !== state.latestRequestId) {
          return;
        }
        state.items = action.payload.listing.environments.map(toEnvironment);
        state.warnings = [...action.payload.listing.warnings];
        state.loadedAt = action.payload.at;
        state.error = null;
      })
      .addCase(fetchEnvironments.rejected, (state, action) => {
        if (action.meta.requestId !== state.latestRequestId) {
          return;
        }
        state.error = action.payload?.message ?? action.error.message ?? "Could not list environments";
      });
  },
});

export const { lastChoiceSet } = environmentsSlice.actions;
export const environmentsReducer = environmentsSlice.reducer;
