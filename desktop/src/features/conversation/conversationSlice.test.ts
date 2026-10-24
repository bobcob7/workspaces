import { describe, expect, it, vi } from "vitest";
import type { ConversationItem, TranscriptChunk } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { appendChunk, conversationClosed, fetchConversation, maxItems, type Conversation } from "./conversationSlice";

const user = (id: string): ConversationItem => ({ kind: "user", id, at: null, text: id });

const chunk = (overrides: Partial<TranscriptChunk> = {}): TranscriptChunk => ({
  items: [],
  next: 0,
  reset: false,
  dropped: 0,
  found: true,
  ...overrides,
});

const conversation = (items: readonly ConversationItem[]): Conversation => ({
  items,
  next: 10,
  dropped: 0,
  loaded: true,
  loading: false,
  error: null,
});

describe("appendChunk", () => {
  it("adds new items after the ones read before", () => {
    const next = appendChunk(conversation([user("a")]), chunk({ items: [user("b")], next: 20 }));
    expect(next.items.map((i) => i.id)).toEqual(["a", "b"]);
  });

  it("replaces what was read before when the transcript started over", () => {
    const next = appendChunk(conversation([user("a")]), chunk({ items: [user("b")], reset: true }));
    expect(next.items.map((i) => i.id)).toEqual(["b"]);
  });

  it("keeps at most maxItems and counts the rest as dropped", () => {
    const many = Array.from({ length: maxItems }, (_, i) => user(String(i)));
    const next = appendChunk(conversation(many), chunk({ items: [user("new")] }));
    expect([next.items.length, next.dropped]).toEqual([maxItems, 1]);
  });
});

describe("fetchConversation", () => {
  it("reads from where the last read ended", async () => {
    const readTranscript = vi.fn(async () => chunk({ items: [user("a")], next: 42 }));
    const store = testStore({ api: fakeApi({ readTranscript }) });
    await store.dispatch(fetchConversation("s1"));
    await store.dispatch(fetchConversation("s1"));
    expect(readTranscript).toHaveBeenLastCalledWith("s1", 42);
  });

  it("ignores a read that started before the conversation was reopened", async () => {
    let finish: (value: TranscriptChunk) => void = () => undefined;
    const readTranscript = vi
      .fn()
      .mockImplementationOnce(async () => new Promise<TranscriptChunk>((resolve) => (finish = resolve)))
      .mockImplementation(async () => chunk({ items: [user("a")], next: 42 }));
    const store = testStore({ api: fakeApi({ readTranscript }) });
    const first = store.dispatch(fetchConversation("s1"));
    store.dispatch(conversationClosed("s1"));
    await store.dispatch(fetchConversation("s1"));
    finish(chunk({ items: [user("a")], next: 42 }));
    await first;
    expect(store.getState().conversations.bySession["s1"]?.items).toHaveLength(1);
  });

  it("keeps the error when the transcript can't be read", async () => {
    const readTranscript = vi.fn(async () => {
      throw { code: "transcript_missing", message: "no transcript" };
    });
    const store = testStore({ api: fakeApi({ readTranscript }) });
    await store.dispatch(fetchConversation("s1"));
    expect(store.getState().conversations.bySession["s1"]?.error?.code).toBe("transcript_missing");
  });

  it("forgets a conversation once it's closed", async () => {
    const store = testStore();
    await store.dispatch(fetchConversation("s1"));
    store.dispatch(conversationClosed("s1"));
    expect(store.getState().conversations.bySession["s1"]).toBeUndefined();
  });
});
