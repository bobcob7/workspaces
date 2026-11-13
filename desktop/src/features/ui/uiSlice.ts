import { createSlice, type PayloadAction } from "@reduxjs/toolkit";

/**
 * Action types of other slices, matched by string: importing their action
 * creators would make a cycle, since those slices import from this one.
 */
const pickedTypes: readonly string[] = ["repos/repoSelected", "loops/loopSelected", "reviews/reviewSelected"];
const openedTypes: readonly string[] = ["loops/creatingStarted", "settings/groupChosen", "stats/windowChosen"];

export type Dialog = "agent" | "shell";

/** The app's sections, in rail order (ADR 0031). */
export type Section = "agents" | "repos" | "reviews" | "loops" | "stats" | "settings";

/** How an agent is shown: its conversation, or the claude attach terminal. */
export type AgentView = "conversation" | "terminal" | "shell";

/** A notice's button: a link to open in the browser, or an agent to select. */
export type NoticeAction =
  | { readonly label: string; readonly href: string }
  | { readonly label: string; readonly agent: string };

export interface Notice {
  readonly severity: "success" | "error" | "info";
  readonly message: string;
  readonly action?: NoticeAction;
}

/** Which pane shows below the narrow breakpoint: the section's list or the picked item. */
export type NarrowPane = "list" | "detail";

export interface UiState {
  readonly section: Section;
  readonly selectedKey: string | null;
  readonly dialog: Dialog | null;
  /** Repo specs to start the open dialog with, such as a repo's from Repos. */
  readonly dialogSpecs: readonly string[];
  readonly notice: Notice | null;
  readonly agentView: AgentView;
  readonly narrowPane: NarrowPane;
}

const initialState: UiState = {
  section: "agents",
  selectedKey: null,
  dialog: null,
  dialogSpecs: [],
  notice: null,
  agentView: "conversation",
  narrowPane: "list",
};

const paneFor = (key: string | null): NarrowPane => (key === null ? "list" : "detail");

const uiSlice = createSlice({
  name: "ui",
  initialState,
  reducers: {
    sectionChosen: (state, action: PayloadAction<Section>) => {
      state.section = action.payload;
      state.narrowPane = "list";
    },
    selected: (state, action: PayloadAction<string | null>) => {
      state.selectedKey = action.payload;
      state.narrowPane = paneFor(action.payload);
    },
    narrowBack: (state) => {
      state.narrowPane = "list";
    },
    dialogOpened: {
      reducer: (state, action: PayloadAction<{ readonly dialog: Dialog; readonly specs: readonly string[] }>): UiState => ({
        ...state,
        dialog: action.payload.dialog,
        dialogSpecs: action.payload.specs,
      }),
      prepare: (dialog: Dialog, specs: readonly string[] = []) => ({ payload: { dialog, specs } }),
    },
    dialogClosed: (state) => {
      state.dialog = null;
      state.dialogSpecs = [];
    },
    noticeShown: (state, action: PayloadAction<Notice>) => {
      state.notice = action.payload;
    },
    noticeDismissed: (state) => {
      state.notice = null;
    },
    agentViewChosen: (state, action: PayloadAction<AgentView>) => {
      state.agentView = action.payload;
    },
    /** Shows Agents with key selected, as the tray menu asks (ADR 0050). */
    agentFocused: (state, action: PayloadAction<string>) => {
      state.section = "agents";
      state.selectedKey = action.payload;
      state.narrowPane = "detail";
    },
  },
  extraReducers: (builder) => {
    builder
      .addMatcher(
        (action) => pickedTypes.includes(action.type),
        (state, action: PayloadAction<string | null>) => {
          state.narrowPane = paneFor(action.payload);
        },
      )
      .addMatcher(
        (action) => openedTypes.includes(action.type),
        (state) => {
          state.narrowPane = "detail";
        },
      );
  },
});

export const {
  sectionChosen,
  selected,
  narrowBack,
  dialogOpened,
  dialogClosed,
  noticeShown,
  noticeDismissed,
  agentViewChosen,
  agentFocused,
} = uiSlice.actions;
export const uiReducer = uiSlice.reducer;
