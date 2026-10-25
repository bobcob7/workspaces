import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type TermSpec } from "../../api";
import { createAppThunk } from "../../thunk";
import { noticeShown } from "../ui/uiSlice";

export type TerminalState = "opening" | "open" | "exited";

export interface TerminalInfo {
  readonly key: string;
  readonly title: string;
  readonly spec: TermSpec;
  readonly state: TerminalState;
  readonly exitCode: number | null;
  /** Increases each time the terminal is shown, to find the least recent. */
  readonly lastUsed: number;
  /** Changes each time the terminal is opened again, so views remount it. */
  readonly generation: number;
  /**
   * The workspace a shell terminal's ws shell made, once ws list shows it;
   * ws picks the id, so the app learns it after the fact (ADR 0036).
   */
  readonly workspaceId: string | null;
  /** When the terminal was opened, in ms since the epoch. */
  readonly openedAt: number;
}

export interface TerminalsState {
  readonly items: readonly TerminalInfo[];
  readonly clock: number;
}

const initialState: TerminalsState = { items: [], clock: 0 };

/**
 * How many `claude attach` terminals stay open at once. Each is a process;
 * hidden ones beyond this are closed, least recently shown first, and
 * reattach when shown again.
 */
export const maxAttached = 4;

export const attachKey = (sessionId: string): string => `attach:${sessionId}`;
/** A new shell terminal's key: shell: and a token the app picks. */
export const shellKey = (token: string): string => `shell:${token}`;

export interface ShellLink {
  readonly key: string;
  readonly workspaceId: string;
}

interface OpenRequest {
  readonly key: string;
  readonly title: string;
  readonly spec: TermSpec;
  readonly openedAt?: number;
}

/** Keys of attach terminals to close so at most maxAttached stay open. */
export const attachedToEvict = (items: readonly TerminalInfo[], keep: string): readonly string[] => {
  const attached = items
    .filter((t) => t.spec.kind === "attach" && t.state !== "exited")
    .toSorted((a, b) => b.lastUsed - a.lastUsed);
  return attached
    .slice(maxAttached)
    .map((t) => t.key)
    .filter((key) => key !== keep);
};

export const closeTerminal = createAppThunk<string, string>("terminals/close", async (key, { extra }) => {
  await extra.terminals.close(key).catch(() => undefined);
  return key;
});

export const openTerminal = createAppThunk<void, OpenRequest>(
  "terminals/open",
  async (request, { extra, dispatch, getState }) => {
    try {
      await extra.terminals.open(request.key, request.spec, (code) => {
        dispatch(terminalExited({ key: request.key, code }));
      });
      dispatch(terminalOpened(request.key));
    } catch (err: unknown) {
      dispatch(terminalExited({ key: request.key, code: null }));
      dispatch(noticeShown({ severity: "error", message: toAppError(err).message }));
      return;
    }
    for (const key of attachedToEvict(getState().terminals.items, request.key)) {
      await dispatch(closeTerminal(key));
    }
  },
  {
    condition: (request, { getState }) => {
      const existing = getState().terminals.items.find((t) => t.key === request.key);
      return existing === undefined || existing.state === "exited";
    },
  },
);

/**
 * How long, in milliseconds, Enter waits after a message's text. Claude
 * Code reads a burst of input as a paste and turns an Enter inside it into
 * a newline, so Enter has to arrive on its own to submit.
 */
export const submitDelay = 200;

const delay = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

/** Types text into a terminal, then presses Enter once the text has settled. */
export const sendMessage = createAppThunk<void, { key: string; text: string }>(
  "terminals/sendMessage",
  async ({ key, text }, { extra, dispatch }) => {
    try {
      await extra.terminals.send(key, text);
      await delay(submitDelay);
      await extra.terminals.send(key, "\r");
    } catch (err: unknown) {
      dispatch(noticeShown({ severity: "error", message: toAppError(err).message }));
    }
  },
);

const terminalsSlice = createSlice({
  name: "terminals",
  initialState,
  reducers: {
    terminalOpened: (state, action: PayloadAction<string>) => {
      const term = state.items.find((t) => t.key === action.payload);
      if (term !== undefined && term.state === "opening") {
        term.state = "open";
      }
    },
    terminalExited: (state, action: PayloadAction<{ key: string; code: number | null }>) => {
      const term = state.items.find((t) => t.key === action.payload.key);
      if (term !== undefined) {
        term.state = "exited";
        term.exitCode = action.payload.code;
      }
    },
    shellsLinked: (state, action: PayloadAction<readonly ShellLink[]>) => {
      for (const link of action.payload) {
        const term = state.items.find((t) => t.key === link.key);
        if (term !== undefined) {
          term.workspaceId = link.workspaceId;
        }
      }
    },
    terminalShown: (state, action: PayloadAction<string>) => {
      const term = state.items.find((t) => t.key === action.payload);
      if (term !== undefined) {
        state.clock += 1;
        term.lastUsed = state.clock;
      }
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(openTerminal.pending, (state, action): TerminalsState => {
        const { key, title, spec, openedAt = 0 } = action.meta.arg;
        const clock = state.clock + 1;
        const info: TerminalInfo = {
          key,
          title,
          spec,
          state: "opening",
          exitCode: null,
          lastUsed: clock,
          generation: clock,
          workspaceId: null,
          openedAt,
        };
        return { clock, items: [...state.items.filter((t) => t.key !== key), info] };
      })
      .addCase(closeTerminal.fulfilled, (state, action) => {
        state.items = state.items.filter((t) => t.key !== action.payload);
      });
  },
});

export const { terminalOpened, terminalExited, terminalShown, shellsLinked } = terminalsSlice.actions;
export const terminalsReducer = terminalsSlice.reducer;
