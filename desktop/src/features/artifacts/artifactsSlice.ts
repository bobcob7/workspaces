import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type Artifact, type ArtifactSummary } from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown } from "../ui/uiSlice";
import { uploadFiles } from "./upload";

export type ArtifactFilter = "all" | "image" | "markdown" | "text";

export interface ArtifactsState {
  items: ArtifactSummary[];
  readonly loading: boolean;
  readonly error: AppError | null;
  warnings: string[];
  readonly filter: ArtifactFilter;
  readonly search: string;
  readonly selected: string | null;
  /** Files being uploaded, one row each until their upload settles. */
  uploads: { name: string }[];
}

const initialState: ArtifactsState = {
  items: [],
  loading: false,
  error: null,
  warnings: [],
  filter: "all",
  search: "",
  selected: null,
  uploads: [],
};

/** `uploads` without one entry per name in `names`. */
const settledUploads = (uploads: ArtifactsState["uploads"], names: readonly string[]): ArtifactsState["uploads"] =>
  names.reduce((left, name) => {
    const index = left.findIndex((upload) => upload.name === name);
    return index === -1 ? left : [...left.slice(0, index), ...left.slice(index + 1)];
  }, uploads);

export const loadArtifacts = createAppThunk<readonly ArtifactSummary[], void>(
  "artifacts/load",
  async (_, { extra, rejectWithValue }) => {
    try {
      return await extra.api.artifactList(null, null);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
);

/** Uploads files, then loads the list again, even when a file fails. Resolves to the artifacts made. */
export const uploadArtifacts = createAppThunk<readonly Artifact[], readonly File[]>(
  "artifacts/upload",
  async (files, { extra, dispatch, rejectWithValue }) => {
    try {
      return await uploadFiles(files, extra.api);
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    } finally {
      await dispatch(loadArtifacts());
    }
  },
);

export const removeArtifact = createAppThunk<string, string>(
  "artifacts/remove",
  async (id, { extra, dispatch, rejectWithValue }) => {
    try {
      await extra.api.artifactRemove(id);
      return id;
    } catch (err: unknown) {
      const error = toAppError(err);
      dispatch(noticeShown({ severity: "error", message: error.message }));
      return rejectWithValue(error);
    }
  },
);

const artifactsSlice = createSlice({
  name: "artifacts",
  initialState,
  reducers: {
    artifactFilterChosen: (state, action: PayloadAction<ArtifactFilter>) => {
      state.filter = action.payload;
    },
    artifactSearchChanged: (state, action: PayloadAction<string>) => {
      state.search = action.payload;
    },
    artifactSelected: (state, action: PayloadAction<string | null>) => {
      state.selected = action.payload;
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(loadArtifacts.pending, (state) => {
        state.loading = true;
        state.error = null;
      })
      .addCase(loadArtifacts.fulfilled, (state, action) => {
        state.loading = false;
        state.items = [...action.payload];
      })
      .addCase(loadArtifacts.rejected, (state, action) => {
        state.loading = false;
        state.error = action.payload ?? null;
      })
      .addCase(uploadArtifacts.pending, (state, action) => {
        state.uploads = [...state.uploads, ...action.meta.arg.map((file) => ({ name: file.name }))];
      })
      .addCase(uploadArtifacts.fulfilled, (state, action) => {
        state.uploads = settledUploads(state.uploads, action.meta.arg.map((file) => file.name));
        state.selected = action.payload.at(-1)?.id ?? state.selected;
      })
      .addCase(uploadArtifacts.rejected, (state, action) => {
        state.uploads = settledUploads(state.uploads, action.meta.arg.map((file) => file.name));
      })
      .addCase(removeArtifact.fulfilled, (state, action) => {
        state.items = state.items.filter((item) => item.id !== action.payload);
        state.selected = state.selected === action.payload ? null : state.selected;
      });
  },
});

export const { artifactFilterChosen, artifactSearchChanged, artifactSelected } = artifactsSlice.actions;
export const artifactsReducer = artifactsSlice.reducer;
