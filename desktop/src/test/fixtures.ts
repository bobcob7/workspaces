import { vi } from "vitest";
import type {
  Api,
  CachedRepo,
  CommentPlan,
  ConfigChange,
  ConfigListing,
  ConfigSetting,
  Entry,
  FixPlan,
  HostView,
  ListedProfile,
  Loop,
  LoopHistoryRun,
  LoopMemoryEntry,
  NewLoop,
  NotificationSettings,
  OpenSettings,
  OpenWith,
  ListedRole,
  ProfileDetail,
  PullRequest,
  RepoRef,
  ReviewPlan,
  ReviewRun,
  ReviewSettings,
  ReviewsView,
  RoleDetail,
  Stats,
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
  hidden: false,
  parentId: null,
  loopRun: null,
  ...overrides,
});

/** A sample tree for the Agents list (ADR 0058); keys equal workspace ids. */
export const treeEntries = (): readonly Entry[] => [
  entry({ key: "orch", workspaceId: "orch", name: "orchestrate", status: "working", updatedAt: "2026-11-11T09:00:00Z" }),
  entry({ key: "t1", workspaceId: "t1", name: "t1-impl", parentId: "orch", status: "done", updatedAt: "2026-11-11T08:00:00Z" }),
  entry({ key: "t1r", workspaceId: "t1r", name: "t1-review", parentId: "t1", status: "needsInput", updatedAt: "2026-11-11T08:30:00Z" }),
  entry({ key: "rev", workspaceId: "rev", name: "review-42", parentId: "orch", hidden: true, status: "working", updatedAt: "2026-11-11T08:45:00Z" }),
  entry({ key: "solo", workspaceId: "solo", name: "solo", status: "stopped", updatedAt: "2026-11-11T07:00:00Z" }),
  entry({ key: "hid", workspaceId: "hid", name: "lint-sweep", hidden: true, status: "idle", updatedAt: "2026-11-11T06:00:00Z" }),
  entry({ key: "hidkid", workspaceId: "hidkid", name: "lint-fix", parentId: "hid", status: "working", updatedAt: "2026-11-11T06:30:00Z" }),
  entry({ key: "shell", workspaceId: "shell", name: "scratch", kind: "shell", status: "running", updatedAt: "2026-11-11T05:00:00Z" }),
];

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
  forge: "github",
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
  merge: { status: "ready", reasons: [], checks: { total: 2, passed: 2, failed: 0, pending: 0 } },
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
  removed: false,
  ...overrides,
});

export const hostView = (overrides: Partial<HostView> = {}): HostView => ({
  host: "github.com",
  forge: "github",
  choice: "auto",
  detected: false,
  login: null,
  token: null,
  tokenEnv: null,
  error: null,
  ...overrides,
});

export const reviewsView = (overrides: Partial<ReviewsView> = {}): ReviewsView => ({
  checking: false,
  checkedAtMs: Date.parse("2026-10-24T10:00:00Z"),
  error: null,
  repos: [
    { id: "github.com/bobcob7/buff-bear", github: true, forge: "github", watch: true, autoReview: false, open: 1, checkedAtMs: 1, error: null },
    { id: "gitlab.example.com/team/svc", github: false, forge: null, watch: false, autoReview: false, open: null, checkedAtMs: null, error: null },
  ],
  hosts: [hostView({ host: "github.com", forge: "github", login: "bobcob7" }), hostView({ host: "gitlab.example.com", forge: null })],
  prs: [pullRequest()],
  warnings: [],
  ...overrides,
});

export const reviewPlan = (overrides: Partial<ReviewPlan> = {}): ReviewPlan => ({
  name: "review-buff-bear-42-3f2a9c1",
  repo: "github.com/bobcob7/buff-bear",
  forge: "github",
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
  source: "builtin",
  ...overrides,
});

export const profileDetail = (overrides: Partial<ProfileDetail> = {}): ProfileDetail => ({
  ...listedProfile(),
  guide: "Anchor a finding where untrusted input crosses a boundary.",
  path: null,
  ...overrides,
});

export const listedProfiles = (): readonly ListedProfile[] => [
  listedProfile(),
  listedProfile({ name: "tests", description: "Tests", model: "haiku" }),
  listedProfile({ name: "go", description: "Go", when: "auto", patterns: ["*.go", "go.mod"] }),
  listedProfile({ name: "efficiency", description: "Efficiency", when: "off" }),
];

export const listedRole = (overrides: Partial<ListedRole> = {}): ListedRole => ({
  name: "implementer",
  description: "Implements one task from a tasks file, pushes a branch, and records PASS or FAIL",
  model: "haiku",
  pass: ["PASS"],
  fail: ["FAIL"],
  vars: ["TASK", "TASKS", "BASE", "BRANCH"],
  claudeArgs: ["--permission-mode", "auto"],
  source: "builtin",
  ...overrides,
});

/** ws's three built-in roles. */
export const listedRoles = (): readonly ListedRole[] => [
  listedRole(),
  listedRole({
    name: "reviewer",
    description: "Reviews one task's diff against its tasks file and records APPROVED or CHANGES_REQUESTED",
    model: "sonnet",
    pass: ["APPROVED"],
    fail: ["CHANGES_REQUESTED"],
    vars: ["TASK", "TASKS", "BASE", "KNOWN_ISSUES"],
  }),
  listedRole({
    name: "verifier",
    description: "Runs the checks on a branch and records PASS, FLAKY, or FAIL",
    model: "haiku",
    pass: ["PASS", "FLAKY"],
    fail: ["FAIL"],
    vars: ["BRANCH", "CHECKS"],
  }),
];

export const roleDetail = (overrides: Partial<RoleDetail> = {}): RoleDetail => ({
  ...listedRole(),
  prompt: "You implement exactly one task, {{.Vars.TASK}}, in this repo.",
  path: null,
  ...overrides,
});

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

export const commentPlan = (overrides: Partial<CommentPlan> = {}): CommentPlan => ({
  number: 42,
  commitId: head,
  head,
  outdated: false,
  login: "bobcob7",
  pendingReview: null,
  prUrl: "https://github.com/bobcob7/buff-bear/pull/42",
  body: "2 findings from an agent review (go, security). Details inline.",
  comments: [
    {
      finding: "go:leaked-goroutine-1",
      kind: "line",
      path: "ratelimit/limiter.go",
      line: 58,
      startLine: null,
      body: "**Should fix · correctness** — The sweeper goroutine never exits.\n\n<sub>ws review · `go:leaked-goroutine-1`</sub>",
    },
    {
      finding: "security:retry-after-zero-1",
      kind: "file",
      path: "ratelimit/middleware.go",
      line: null,
      startLine: null,
      body: "**Should fix · security** — Retry-After is computed with integer seconds; cc `@alice`.\n\n<sub>ws review · `security:retry-after-zero-1`</sub>",
    },
  ],
  findings: ["go:leaked-goroutine-1", "security:retry-after-zero-1"],
  inBody: 0,
  ...overrides,
});

export const fixPlan = (overrides: Partial<FixPlan> = {}): FixPlan => ({
  findings: ["security:spoofable-client-ip-1"],
  requester: {
    workspaceId: "fix-login-rate",
    sessionId: "e8cdf5d8",
    path: "/w/fix-login-rate",
    status: "idle",
    available: true,
    reason: null,
  },
  target: "requester",
  agent: {
    name: "fix-buff-bear-42-3f2a9c1",
    spec: "github.com/bobcob7/buff-bear@feat/rate-limit",
    branch: null,
    command: "ws claude --json --name fix-buff-bear-42-3f2a9c1 github.com/bobcob7/buff-bear@feat/rate-limit",
  },
  requesterMessage: "Fix the 1 finding decided \"fix\"…\n\nWhen done, push, and start a new review with: ws review start --pr 42 github.com/bobcob7/buff-bear",
  agentMessage: "Fix the 1 finding decided \"fix\"… (agent)",
  ...overrides,
});

export const loop = (overrides: Partial<Loop> = {}): Loop => ({
  name: "deps",
  title: "",
  every: "1h",
  prompt: "Update the dependencies and open a PR.",
  specs: ["buff-bear"],
  model: null,
  permissionMode: null,
  keep: 5,
  paused: false,
  createdAt: "2026-10-24T08:00:00Z",
  nextRunAt: "2026-10-24T11:00:00Z",
  lastRun: null,
  script: null,
  scriptTimeout: null,
  vars: {},
  lastGate: null,
  hidden: true,
  ...overrides,
});

export const loopHistoryRun = (overrides: Partial<LoopHistoryRun> = {}): LoopHistoryRun => ({
  seq: 1,
  status: "succeeded",
  trigger: "tick",
  startedAt: "2026-10-24T08:00:00Z",
  finishedAt: "2026-10-24T08:14:30Z",
  durationSeconds: 870,
  workspace: "loop-deps-1a2b3c4d",
  workspaceExists: true,
  session: "e8cdf5d8",
  error: null,
  gate: null,
  repeats: 1,
  verdict: null,
  ...overrides,
});

export const loopMemoryEntry = (overrides: Partial<LoopMemoryEntry> = {}): LoopMemoryEntry => ({
  key: "seen",
  value: "#412 #415",
  rev: 1,
  updatedAt: "2026-10-24T08:14:00Z",
  updatedBy: "loop-deps-1a2b3c4d",
  ...overrides,
});

export const notificationSettings = (overrides: Partial<NotificationSettings> = {}): NotificationSettings => ({
  enabled: true,
  needsInput: true,
  done: true,
  ...overrides,
});

export const openSettings = (overrides: Partial<OpenSettings> = {}): OpenSettings => ({
  fileManager: "",
  editor: "",
  ...overrides,
});

const configPath = "/home/me/.config/workspaces/config.toml";

export const configListing = (): ConfigListing => ({
  path: configPath,
  settings: [
    { key: "defaults.host", type: "string", value: "github.com", default: "github.com", set: false },
    { key: "defaults.owners", type: "strings", value: ["bobcob7", "acme"], default: [], set: true },
    { key: "review.timeout", type: "duration", value: "1h", default: "1h", set: false },
    { key: "review.remove_finished", type: "bool", value: true, default: true, set: false },
    { key: "review.profiles.security.when", type: "string", value: "", default: "", set: false },
    { key: "review.profiles.security.model", type: "string", value: "opus", default: "", set: true },
  ],
});

const configSettingFor = (key: string): ConfigSetting =>
  configListing().settings.find((setting) => setting.key === key) ?? { key, type: "string", value: "", default: "", set: false };

/** What `ws config set` answers: the key with its new value, as the file now says. */
export const configSetChange = (key: string, values: readonly string[]): ConfigChange => {
  const base = configSettingFor(key);
  const value = base.type === "strings" ? values : (values[0] ?? "");
  return { path: configPath, setting: { ...base, value, set: true }, changed: true };
};

/** What `ws config unset` answers: the key back at its default. */
export const configUnsetChange = (key: string): ConfigChange => {
  const base = configSettingFor(key);
  return { path: configPath, setting: { ...base, value: base.default, set: false }, changed: true };
};

/** A stats_get answer for the all-time window: two models, three languages, a productivity row per model and language. */
export const statsSnapshot = (overrides: Partial<Stats> = {}): Stats => ({
  generatedAtMs: 1_794_600_000_000,
  sinceMs: null,
  system: {
    cpuPercent: 12.5,
    cpuCount: 8,
    loadAverage: [1.42, 1.1, 0.97],
    memoryTotalBytes: 17_179_869_184,
    memoryUsedBytes: 10_737_418_240,
    disk: { path: "/home/me/.config/workspaces", totalBytes: 499_963_174_912, usedBytes: 201_863_462_912 },
  },
  usage: {
    totals: {
      requests: 180,
      inputTokens: 67_000,
      outputTokens: 64_000,
      thinkingTokens: 16_000,
      cacheWriteTokens: 340_000,
      cacheReadTokens: 2_880_000,
      totalTokens: 3_351_000,
      costUsd: 17.7,
      cacheHitRatio: 0.8762,
    },
    sessions: 9,
    activeMinutes: 142.5,
    byModel: [
      {
        model: "claude-haiku-5-5",
        label: "Haiku 5.5",
        family: "haiku",
        priced: true,
        sessions: 5,
        activeMinutes: 88,
        linesAdded: 910,
        linesRemoved: 278,
        requests: 120,
        inputTokens: 45_000,
        outputTokens: 38_000,
        thinkingTokens: 9_000,
        cacheWriteTokens: 210_000,
        cacheReadTokens: 1_900_000,
        totalTokens: 2_193_000,
        costUsd: 3.1,
        cacheHitRatio: 0.8817,
      },
      {
        model: "claude-sonnet-5-5",
        label: "Sonnet 5.5",
        family: "sonnet",
        priced: true,
        sessions: 4,
        activeMinutes: 54.5,
        linesAdded: 528,
        linesRemoved: 152,
        requests: 60,
        inputTokens: 22_000,
        outputTokens: 26_000,
        thinkingTokens: 7_000,
        cacheWriteTokens: 130_000,
        cacheReadTokens: 980_000,
        totalTokens: 1_158_000,
        costUsd: 14.6,
        cacheHitRatio: 0.8657,
      },
    ],
    byLanguage: [
      { language: "Go", linesAdded: 812, linesRemoved: 240, edits: 41 },
      { language: "TypeScript", linesAdded: 530, linesRemoved: 160, edits: 28 },
      { language: "Markdown", linesAdded: 96, linesRemoved: 30, edits: 7 },
    ],
  },
  productivity: [
    {
      model: "claude-haiku-5-5",
      label: "Haiku 5.5",
      family: "haiku",
      language: "Go",
      linesAdded: 540,
      linesRemoved: 160,
      edits: 27,
      activeMinutes: 52,
      tokens: 1_260_000,
      costUsd: 1.9,
      shareOfLanguage: 0.665,
      locPerMinute: 10.38,
      minutesPer100Loc: 9.63,
      tokensPerLoc: 2333.3,
      costPerLoc: 0.0035,
    },
    {
      model: "claude-sonnet-5-5",
      label: "Sonnet 5.5",
      family: "sonnet",
      language: "Go",
      linesAdded: 272,
      linesRemoved: 80,
      edits: 14,
      activeMinutes: 30,
      tokens: 700_000,
      costUsd: 8.4,
      shareOfLanguage: 0.335,
      locPerMinute: 9.07,
      minutesPer100Loc: 11.03,
      tokensPerLoc: 2573.5,
      costPerLoc: 0.0309,
    },
    {
      model: "claude-haiku-5-5",
      label: "Haiku 5.5",
      family: "haiku",
      language: "TypeScript",
      linesAdded: 330,
      linesRemoved: 100,
      edits: 16,
      activeMinutes: 36,
      tokens: 933_000,
      costUsd: 1.2,
      shareOfLanguage: 0.6226,
      locPerMinute: 9.17,
      minutesPer100Loc: 10.91,
      tokensPerLoc: 2827.3,
      costPerLoc: 0.0036,
    },
    {
      model: "claude-sonnet-5-5",
      label: "Sonnet 5.5",
      family: "sonnet",
      language: "TypeScript",
      linesAdded: 200,
      linesRemoved: 60,
      edits: 12,
      activeMinutes: 24.5,
      tokens: 458_000,
      costUsd: 6.2,
      shareOfLanguage: 0.3774,
      locPerMinute: 8.16,
      minutesPer100Loc: 12.25,
      tokensPerLoc: 2290,
      costPerLoc: 0.031,
    },
  ],
  scan: { files: 412, filesRead: 3, bytesRead: 184_320, durationMs: 42, cached: false },
  warnings: [],
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
  cacheSearch: vi.fn(async () => []),
  configList: vi.fn(async () => configListing()),
  configSet: vi.fn(async (key: string, values: readonly string[]) => configSetChange(key, values)),
  configUnset: vi.fn(async (key: string) => configUnsetChange(key)),
  settingsEnvironment: vi.fn(async () => ({ stateFile: null, wsConfigDir: null, claudeConfigDir: null, path: "/usr/bin:/bin", online: null })),
  createAgent: vi.fn(async () => ({ id: "new", session: "abcd1234", path: "/w/new" })),
  removeEntry: vi.fn(async () => undefined),
  renameWorkspace: vi.fn(async (id: string, name: string) => ({ id, name })),
  setWorkspaceHidden: vi.fn(async (id: string, hidden: boolean) => ({ id, hidden })),
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
  reviewsSetForge: vi.fn(async () => reviewsView()),
  reviewsSetToken: vi.fn(async () => reviewsView()),
  reviewsDismiss: vi.fn(async () => reviewsView()),
  reviewsRestore: vi.fn(async () => reviewsView()),
  reviewsPlan: vi.fn(async () => reviewPlan()),
  reviewsProfiles: vi.fn(async () => listedProfiles()),
  reviewsProfileShow: vi.fn(async (name: string) => profileDetail({ name })),
  reviewsProfileAdd: vi.fn(async (name: string) => profileDetail({ name, builtin: false, source: "user" })),
  reviewsProfileEdit: vi.fn(async (name: string) => profileDetail({ name })),
  reviewsProfileRemove: vi.fn(async () => ({ restored: false, settings: { reviews: reviewSettings(), notifications: notificationSettings(), open: openSettings() } })),
  rolesList: vi.fn(async () => listedRoles()),
  rolesShow: vi.fn(async (name: string) => roleDetail({ name, ...listedRoles().find((role) => role.name === name) })),
  rolesAdd: vi.fn(async (name: string) => roleDetail({ name, source: "user", path: `/c/machine/roles/${name}.md` })),
  rolesEdit: vi.fn(async (name: string) => roleDetail({ name, source: "override", path: `/c/machine/roles/${name}.md` })),
  rolesRemove: vi.fn(async (name: string) => ({ name, path: `/c/machine/roles/${name}.md`, restored: false })),
  reviewsStart: vi.fn(async () => ({ id: "review-buff-bear-42-3f2a9c1", already: false })),
  reviewReport: vi.fn(async () => reviewReport()),
  reviewDecide: vi.fn(async () => reviewReport()),
  reviewOpenReport: vi.fn(async () => "/cache/reviews/review-buff-bear-3f2a9c1.html"),
  reviewCommentPlan: vi.fn(async () => commentPlan()),
  reviewCommentPost: vi.fn(async () => ({ url: "https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99", report: reviewReport(), warning: null })),
  reviewFixPlan: vi.fn(async () => fixPlan()),
  reviewFixStart: vi.fn(async () => ({ agent: "fix-buff-bear-42-3f2a9c1-0a1b2c3d", session: "5f6e7d8c", report: reviewReport(), warning: null })),
  reviewFixSent: vi.fn(async () => reviewReport()),
  versions: vi.fn(async () => ({
    app: { version: "0.1.0", commit: "v0.3.0-4-gdc313a2" },
    ws: { version: "v0.3.0-4-gdc313a2", path: "/usr/local/bin/ws", error: null },
    claude: { version: "2.1.303", path: "/opt/homebrew/bin/claude", error: null },
  })),
  settingsGet: vi.fn(async () => ({ reviews: reviewSettings(), notifications: notificationSettings(), open: openSettings() })),
  settingsSetReviews: vi.fn(async (reviews: ReviewSettings) => ({ reviews, notifications: notificationSettings(), open: openSettings() })),
  settingsSetNotifications: vi.fn(async (notifications: NotificationSettings) => ({ reviews: reviewSettings(), notifications, open: openSettings() })),
  workspaceOpen: vi.fn(async (_with: OpenWith, path: string) => ({ dir: path, command: ["code", path] })),
  openDefaults: vi.fn(async () => ({ fileManager: "open", editor: "code" })),
  settingsSetOpen: vi.fn(async (open: OpenSettings) => ({ reviews: reviewSettings(), notifications: notificationSettings(), open })),
  reviewsAsk: vi.fn(async () => ({ id: "ask-buff-bear-42-3f2a9c1-0a1b2c3d", session: "5f6e7d8c", path: "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d", already: false })),
  onTraySelect: vi.fn(async () => () => undefined),
  loopsList: vi.fn(async () => ({ loops: [], warnings: [] })),
  loopsAdd: vi.fn(async (request: NewLoop) =>
    loop({ name: request.name, title: request.title ?? "", every: request.every, prompt: request.prompt, specs: request.specs, script: request.script ?? null }),
  ),
  loopsEdit: vi.fn(async (request: NewLoop) =>
    loop({
      name: request.name,
      title: request.title ?? "",
      every: request.every,
      prompt: request.prompt,
      specs: request.specs,
      script: request.script ?? null,
      scriptTimeout: request.scriptTimeout ?? null,
      keep: request.keep ?? 5,
    }),
  ),
  loopsSetPaused: vi.fn(async (name: string, paused: boolean) => loop({ name, paused })),
  loopsRemove: vi.fn(async (name: string) => loop({ name })),
  loopsRunNow: vi.fn(async (name: string) => ({ name, workspace: `loop-${name}-1`, session: "9a8b7c6d", path: `/w/loop-${name}-1` })),
  loopsHistory: vi.fn(async (name: string) => ({ loop: name, runs: [], total: 0, warnings: [] })),
  loopsHistoryRun: vi.fn(async (name: string, seq: number) => ({ loop: name, run: loopHistoryRun({ seq }), reportText: null, warnings: [] })),
  loopsHistoryClear: vi.fn(async (name: string) => ({ loop: name, removed: 0, warnings: [] })),
  loopsMemory: vi.fn(async (name: string) => ({ loop: name, entries: [], bytes: 0, warnings: [] })),
  loopsMemorySet: vi.fn(async (name: string, key: string, value: string) => ({
    loop: name,
    entry: loopMemoryEntry({ key, value, rev: 1 }),
    warnings: [],
  })),
  loopsMemoryRemove: vi.fn(async (name: string, key: string) => ({ loop: name, key, removed: true, warnings: [] })),
  loopsMemoryClear: vi.fn(async (name: string) => ({ loop: name, removed: 0, warnings: [] })),
  statsGet: vi.fn(async () => statsSnapshot()),
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
