import { createSlice, type PayloadAction } from "@reduxjs/toolkit";
import { toAppError, type AppError, type ArtifactSummary, type ConversationItem, type TranscriptChunk } from "../../api";
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

/** The artifacts a workspace's conversation was part of, for the strip (ADR 0084). */
export interface LinkedArtifacts {
  readonly items: ArtifactSummary[];
  readonly loaded: boolean;
  /** A read is in flight, so another isn't started and an older answer can't replace a newer one. */
  readonly loading: boolean;
}

export interface ConversationsState {
  readonly bySession: Readonly<Record<string, Conversation>>;
  readonly artifactsByWorkspace: Readonly<Record<string, LinkedArtifacts>>;
}

const initialState: ConversationsState = { bySession: {}, artifactsByWorkspace: {} };

/** How often the strip's list is read again while the conversation is shown. */
export const linkedArtifactsInterval = 15_000;

/** Reads the artifacts linked to a workspace's conversation. A failure keeps the last list. */
export const fetchLinkedArtifacts = createAppThunk<readonly ArtifactSummary[], string>(
  "conversations/linkedArtifacts",
  async (workspaceId, { extra, rejectWithValue }) => {
    try {
      return await extra.api.artifactList(null, null, workspaceId);
    } catch (err: unknown) {
      return rejectWithValue(toAppError(err));
    }
  },
  { condition: (workspaceId, { getState }) => getState().conversations.artifactsByWorkspace[workspaceId]?.loading !== true },
);

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

/**
 * Records that a message sent to a workspace's agent cited artifacts, then
 * reads the strip again. A failure is dropped: the transcript scan records
 * the citation later (ADR 0084).
 */
export const citeArtifacts = createAppThunk<void, { readonly workspaceId: string; readonly refs: readonly string[] }>(
  "conversations/cite",
  async ({ workspaceId, refs }, { extra, dispatch }) => {
    try {
      await extra.api.artifactCite(workspaceId, refs);
    } catch {
      return;
    }
    await dispatch(fetchLinkedArtifacts(workspaceId));
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
  ...state,
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
      .addCase(fetchLinkedArtifacts.pending, (state, action) => {
        const current = state.artifactsByWorkspace[action.meta.arg];
        state.artifactsByWorkspace[action.meta.arg] = { items: current?.items ?? [], loaded: current?.loaded ?? false, loading: true };
      })
      .addCase(fetchLinkedArtifacts.fulfilled, (state, action) => {
        state.artifactsByWorkspace[action.meta.arg] = { items: [...action.payload], loaded: true, loading: false };
      })
      .addCase(fetchLinkedArtifacts.rejected, (state, action) => {
        const current = state.artifactsByWorkspace[action.meta.arg];
        if (current !== undefined) {
          state.artifactsByWorkspace[action.meta.arg] = { ...current, loading: false };
        }
      })
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
