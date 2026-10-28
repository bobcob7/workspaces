import { createSlice, type PayloadAction } from "@reduxjs/toolkit";

export type Dialog = "agent" | "shell";

/** The app's sections, in rail order (ADR 0031). */
export type Section = "agents" | "repos" | "reviews" | "settings";

/** How an agent is shown: its conversation, or the claude attach terminal. */
export type AgentView = "conversation" | "terminal";

/** A notice's button: a link to open in the browser, or an agent to select. */
export type NoticeAction =
  | { readonly label: string; readonly href: string }
  | { readonly label: string; readonly agent: string };

export interface Notice {
  readonly severity: "success" | "error" | "info";
  readonly message: string;
  readonly action?: NoticeAction;
}

export interface UiState {
  readonly section: Section;
  readonly selectedKey: string | null;
  readonly dialog: Dialog | null;
  /** Repo specs to start the open dialog with, such as a repo's from Repos. */
  readonly dialogSpecs: readonly string[];
  readonly notice: Notice | null;
  readonly agentView: AgentView;
}

const initialState: UiState = {
  section: "agents",
  selectedKey: null,
  dialog: null,
  dialogSpecs: [],
  notice: null,
  agentView: "conversation",
};

const uiSlice = createSlice({
  name: "ui",
  initialState,
  reducers: {
    sectionChosen: (state, action: PayloadAction<Section>) => {
      state.section = action.payload;
    },
    selected: (state, action: PayloadAction<string | null>) => {
      state.selectedKey = action.payload;
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
  },
});

export const { sectionChosen, selected, dialogOpened, dialogClosed, noticeShown, noticeDismissed, agentViewChosen } =
  uiSlice.actions;
export const uiReducer = uiSlice.reducer;
