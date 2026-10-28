import { vi } from "vitest";
import type {
  Api,
  CachedRepo,
  Entry,
  ListedProfile,
  PullRequest,
  RepoRef,
  ReviewPlan,
  ReviewRun,
  ReviewSettings,
  ReviewsView,
} from "../api";
import type { TerminalHost } from "../features/terminals/host";
import { createStore, type RootState } from "../store";
import { reviewReport } from "./reportFixture";

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
  review: null,
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

export const head = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

export const pullRequest = (overrides: Partial<PullRequest> = {}): PullRequest => ({
  key: "github.com/bobcob7/buff-bear#42",
  repo: "github.com/bobcob7/buff-bear",
  number: 42,
  title: "Add rate limiting to login",
  author: { login: "alice" },
  url: "https://github.com/bobcob7/buff-bear/pull/42",
  body: "Adds a **token bucket**.",
  isDraft: false,
  headRefOid: head,
  headRefName: "feat/rate-limit",
  baseRefName: "main",
  additions: 214,
  deletions: 37,
  changedFiles: 9,
  createdAt: "2026-10-24T09:00:00Z",
  updatedAt: "2026-10-24T09:00:00Z",
  group: "toReview",
  outOfDate: false,
  requested: true,
  mine: false,
  reviews: [],
  newCommits: null,
  ...overrides,
});

export const reviewRun = (overrides: Partial<ReviewRun> = {}): ReviewRun => ({
  workspaceId: "review-buff-bear-42-3f2a9c1",
  sessionId: "7be1d0c2",
  sha: head,
  status: "done",
  createdAt: "2026-10-24T10:00:00Z",
  detail: null,
  result: "Two problems worth fixing before merge, one nit.",
  links: [],
  auto: false,
  recorded: true,
  requester: null,
  ...overrides,
});

export const reviewsView = (overrides: Partial<ReviewsView> = {}): ReviewsView => ({
  checking: false,
  checkedAtMs: Date.parse("2026-10-24T10:00:00Z"),
  error: null,
  repos: [
    { id: "github.com/bobcob7/buff-bear", github: true, watch: true, autoReview: false, open: 1, checkedAtMs: 1, error: null },
    { id: "gitlab.example.com/team/svc", github: false, watch: false, autoReview: false, open: null, checkedAtMs: null, error: null },
  ],
  prs: [pullRequest()],
  warnings: [],
  ...overrides,
});

export const reviewPlan = (overrides: Partial<ReviewPlan> = {}): ReviewPlan => ({
  name: "review-buff-bear-42-3f2a9c1",
  repo: "github.com/bobcob7/buff-bear",
  number: 42,
  head,
  profiles: [],
  model: "",
  permissionMode: "",
  focus: "",
  ...overrides,
});

export const listedProfile = (overrides: Partial<ListedProfile> = {}): ListedProfile => ({
  name: "security",
  description: "Security: injection, secrets, and authorization",
  when: "always",
  patterns: [],
  model: "",
  builtin: true,
  ...overrides,
});

export const listedProfiles = (): readonly ListedProfile[] => [
  listedProfile(),
  listedProfile({ name: "tests", description: "Tests", model: "haiku" }),
  listedProfile({ name: "go", description: "Go", when: "auto", patterns: ["*.go", "go.mod"] }),
  listedProfile({ name: "efficiency", description: "Efficiency", when: "off" }),
];

export const reviewSettings = (overrides: Partial<ReviewSettings> = {}): ReviewSettings => ({
  checkIntervalMinutes: 5,
  skipDrafts: true,
  skipMine: true,
  onlyRequested: false,
  maxAutoReviews: 2,
  model: "",
  permissionMode: "",
  profiles: {},
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
  renameWorkspace: vi.fn(async (id: string, name: string) => ({ id, name })),
  stopAgent: vi.fn(async () => undefined),
  restoreAgents: vi.fn(async () => []),
  openExternal: vi.fn(async () => undefined),
  readTranscript: vi.fn(async () => ({ items: [], next: 0, reset: false, dropped: 0, found: true })),
  terminalOpen: vi.fn(async () => 1),
  terminalWrite: vi.fn(async () => undefined),
  terminalResize: vi.fn(async () => undefined),
  terminalClose: vi.fn(async () => undefined),
  reviewsView: vi.fn(async () => reviewsView()),
  reviewsCheckNow: vi.fn(async () => reviewsView()),
  reviewsLoadRepos: vi.fn(async () => reviewsView()),
  reviewsSetWatch: vi.fn(async () => reviewsView()),
  reviewsDismiss: vi.fn(async () => reviewsView()),
  reviewsRestore: vi.fn(async () => reviewsView()),
  reviewsPlan: vi.fn(async () => reviewPlan()),
  reviewsProfiles: vi.fn(async () => listedProfiles()),
  reviewsStart: vi.fn(async () => ({ id: "review-buff-bear-42-3f2a9c1", already: false })),
  reviewReport: vi.fn(async () => reviewReport()),
  reviewDecide: vi.fn(async () => reviewReport()),
  reviewOpenReport: vi.fn(async () => "/cache/reviews/review-buff-bear-3f2a9c1.html"),
  settingsGet: vi.fn(async () => ({ reviews: reviewSettings() })),
  settingsSetReviews: vi.fn(async (reviews: ReviewSettings) => ({ reviews })),
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
