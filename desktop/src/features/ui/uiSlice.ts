import { createSlice, type PayloadAction } from "@reduxjs/toolkit";

export type Dialog = "agent" | "shell";

/** How an agent is shown: its conversation, or the claude attach terminal. */
export type AgentView = "conversation" | "terminal";

export interface Notice {
  readonly severity: "success" | "error" | "info";
  readonly message: string;
}

export interface UiState {
  readonly selectedKey: string | null;
  readonly dialog: Dialog | null;
  readonly notice: Notice | null;
  readonly agentView: AgentView;
}

const initialState: UiState = { selectedKey: null, dialog: null, notice: null, agentView: "conversation" };

const uiSlice = createSlice({
  name: "ui",
  initialState,
  reducers: {
    selected: (state, action: PayloadAction<string | null>) => {
      state.selectedKey = action.payload;
    },
    dialogOpened: (state, action: PayloadAction<Dialog>) => {
      state.dialog = action.payload;
    },
    dialogClosed: (state) => {
      state.dialog = null;
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

export const { selected, dialogOpened, dialogClosed, noticeShown, noticeDismissed, agentViewChosen } = uiSlice.actions;
export const uiReducer = uiSlice.reducer;
