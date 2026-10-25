import { vi } from "vitest";
import type { Api, CachedRepo, Entry, RepoRef } from "../api";
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

export const cachedRepo = (overrides: Partial<CachedRepo> = {}): CachedRepo => ({
  id: "github.com/bobcob7/buff-bear",
  path: "/c/cache/repos/github.com/bobcob7/buff-bear.git",
  upstream: "https://github.com/bobcob7/buff-bear.git",
  defaultBranch: "main",
  lastFetchedAt: "2026-10-24T09:00:00Z",
  sizeBytes: 18_432_000,
  branches: 3,
  tags: 1,
  ...overrides,
});

export const repoRef = (overrides: Partial<RepoRef> = {}): RepoRef => ({
  name: "main",
  kind: "branch",
  commit: "9c04e2a7d1f3",
  subject: "Merge pull request #39",
  author: "bobcob7",
  date: "2026-10-24T07:00:00Z",
  ...overrides,
});

export const fakeApi = (overrides: Partial<Api> = {}): Api => ({
  listEntries: vi.fn(async () => ({ entries: [], warnings: [] })),
  cacheRepos: vi.fn(async () => []),
  cacheList: vi.fn(async () => ({ repos: [], warnings: [] })),
  cacheRefs: vi.fn(async (id: string) => ({ id, defaultBranch: "main", refs: [] })),
  cacheResolve: vi.fn(async (spec: string) => ({
    id: `github.com/bobcob7/${spec}`,
    ref: null,
    upstream: `https://github.com/bobcob7/${spec}.git`,
    cached: false,
    source: "probe" as const,
  })),
  cacheFetch: vi.fn(async (ids: readonly string[]) => ids.map((id) => ({ id, error: null }))),
  cacheAdd: vi.fn(async (spec: string) => ({
    id: `github.com/bobcob7/${spec}`,
    path: `/c/${spec}.git`,
    upstream: `https://github.com/bobcob7/${spec}.git`,
    added: true,
  })),
  cacheRemove: vi.fn(async () => undefined),
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
