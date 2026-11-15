import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type ConversationItem, type TranscriptChunk } from "../../api";
import { createAppThunk } from "../../thunk";

export interface Conversation {
  readonly items: readonly ConversationItem[];
  /** The transcript offset to read from next. */
  readonly next: number;
  /** How many earlier items aren't kept. */
  readonly dropped: number;
  readonly loaded: boolean;
  readonly loading: boolean;
  readonly error: AppError | null;
}

export interface ConversationsState {
  readonly bySession: Readonly<Record<string, Conversation>>;
}

const initialState: ConversationsState = { bySession: {} };

/** Most items kept per conversation; older ones are dropped as new ones come. */
export const maxItems = 2000;

const empty: Conversation = { items: [], next: 0, dropped: 0, loaded: false, loading: false, error: null };

interface Read {
  readonly offset: number;
  readonly chunk: TranscriptChunk;
}

/**
 * Where a conversation is read from: a Claude agent's session, or, with a
 * workspace id, a `ws` runner's transcript in that workspace.
 */
export interface TranscriptSource {
  readonly sessionId: string;
  readonly workspaceId: string | null;
}

/** Reads what the session's transcript gained since the last read. */
export const fetchConversation = createAppThunk<Read, TranscriptSource>(
  "conversations/fetch",
  async (source, { extra, getState, rejectWithValue }) => {
    const offset = getState().conversations.bySession[source.sessionId]?.next ?? 0;
    try {
      return { offset, chunk: await extra.api.readTranscript(source.sessionId, offset, source.workspaceId) };
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  {
    condition: (source, { getState }) => getState().conversations.bySession[source.sessionId]?.loading !== true,
  },
);

/** Adds a chunk to what was read before, keeping at most maxItems. */
export const appendChunk = (current: Conversation, chunk: TranscriptChunk): Conversation => {
  const base = chunk.reset ? empty : current;
  const items = [...base.items, ...chunk.items];
  const over = Math.max(0, items.length - maxItems);
  return {
    items: over > 0 ? items.slice(over) : items,
    next: chunk.next,
    dropped: base.dropped + chunk.dropped + over,
    loaded: true,
    loading: false,
    error: null,
  };
};

const put = (
  state: ConversationsState,
  sessionId: string,
  update: (current: Conversation) => Conversation,
): ConversationsState => ({
  bySession: { ...state.bySession, [sessionId]: update(state.bySession[sessionId] ?? empty) },
});

const conversationsSlice = createSlice({
  name: "conversations",
  initialState,
  reducers: {
    conversationClosed: (state, action: PayloadAction<string>) => {
      delete state.bySession[action.payload];
    },
  },
  extraReducers: (builder) => {
    builder
      .addCase(fetchConversation.pending, (state, action) =>
        put(state, action.meta.arg.sessionId, (current) => ({ ...current, loading: true })),
      )
      // A read that started from somewhere else, such as one still running
      // when the conversation was closed and opened again, is stale.
      .addCase(fetchConversation.fulfilled, (state, action) =>
        put(state, action.meta.arg.sessionId, (current) =>
          current.next === action.payload.offset
            ? appendChunk(current, action.payload.chunk)
            : { ...current, loading: false },
        ),
      )
      .addCase(fetchConversation.rejected, (state, action) => {
        const error = action.payload ?? { code: "internal", message: action.error.message ?? "reading failed" };
        return put(state, action.meta.arg.sessionId, (current) => ({
          ...current,
          loading: false,
          loaded: true,
          error,
        }));
      });
  },
});

export const { conversationClosed } = conversationsSlice.actions;
export const conversationsReducer = conversationsSlice.reducer;
