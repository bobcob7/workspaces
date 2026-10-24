import { vi } from "vitest";
import type { Api, Entry } from "../api";
import type { TerminalHost } from "../features/terminals/host";
import { createStore, type RootState } from "../store";

export const entry = (overrides: Partial<Entry> = {}): Entry => ({
  key: "fix-login",
  kind: "agent",
  name: "fix-login",
  workspaceId: "fix-login",
  sessionId: "e8cdf5d8",
  path: "/w/fix-login",
  status: "working",
  stale: false,
  createdAt: "2026-10-18T08:12:00Z",
  updatedAt: null,
  detail: null,
  intent: null,
  result: null,
  links: [],
  repos: [],
  ...overrides,
});

export const fakeApi = (overrides: Partial<Api> = {}): Api => ({
  listEntries: vi.fn(async () => ({ entries: [], warnings: [] })),
  cacheRepos: vi.fn(async () => []),
  createAgent: vi.fn(async () => ({ id: "new", session: "abcd1234", path: "/w/new" })),
  removeEntry: vi.fn(async () => undefined),
  stopAgent: vi.fn(async () => undefined),
  restoreAgents: vi.fn(async () => []),
  openExternal: vi.fn(async () => undefined),
  readTranscript: vi.fn(async () => ({ items: [], next: 0, reset: false, dropped: 0, found: true })),
  terminalOpen: vi.fn(async () => 1),
  terminalWrite: vi.fn(async () => undefined),
  terminalResize: vi.fn(async () => undefined),
  terminalClose: vi.fn(async () => undefined),
  ...overrides,
});

export interface FakeHost extends TerminalHost {
  readonly exits: Map<string, (code: number | null) => void>;
}

export const fakeHost = (): FakeHost => {
  const exits = new Map<string, (code: number | null) => void>();
  return {
    exits,
    open: vi.fn(async (key: string, _spec, onExit: (code: number | null) => void) => {
      exits.set(key, onExit);
    }),
    close: vi.fn(async () => undefined),
    send: vi.fn(async () => undefined),
  };
};

export const testStore = (
  options: { api?: Api; terminals?: TerminalHost; state?: Partial<RootState> } = {},
): ReturnType<typeof createStore> =>
  createStore({ api: options.api ?? fakeApi(), terminals: options.terminals ?? fakeHost() }, options.state);
