// Stands in for the Tauri runtime when the app runs in a plain browser, so
// screenshots can be taken without the desktop shell. Load it with
// page.addInitScript({ path }) before the app's scripts. Not a test: it's
// outside src/, and its name doesn't match the test pattern.
(() => {
  const head = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
  const buffBear = "github.com/bobcob7/buff-bear";
  const api = "github.com/bobcob7/api";

  const defaultSettings = () => ({
    reviews: {
      checkIntervalMinutes: 5,
      skipDrafts: true,
      skipMine: true,
      onlyRequested: false,
      maxAutoReviews: 2,
      model: "",
      permissionMode: "",
      profiles: {},
    },
    notifications: { enabled: true, needsInput: true, done: true },
    open: { fileManager: "", editor: "" },
  });

  const repo = (id, path, ref, refKind) => ({ id, path, ref, refKind });

  const entry = (overrides) => ({
    key: overrides.workspaceId ?? overrides.sessionId,
    kind: "agent",
    name: overrides.workspaceId,
    workspaceId: overrides.workspaceId,
    sessionId: null,
    path: "",
    status: "idle",
    stale: false,
    createdAt: "2026-10-18T08:12:00Z",
    updatedAt: "2026-10-24T09:30:00Z",
    detail: null,
    intent: null,
    result: null,
    links: [],
    repos: [],
    review: null,
    hidden: false,
    parentId: null,
    ...overrides,
  });

  const pullRequest = () => ({
    key: `${buffBear}#42`,
    repo: buffBear,
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
  });

  const reviewsView = () => ({
    checking: false,
    checkedAtMs: Date.parse("2026-10-24T10:00:00Z"),
    error: null,
    repos: [
      { id: buffBear, github: true, watch: true, autoReview: false, open: 1, checkedAtMs: 1, error: null },
      { id: api, github: true, watch: false, autoReview: false, open: null, checkedAtMs: null, error: null },
    ],
    prs: [pullRequest()],
    warnings: [],
  });

  const profile = (overrides) => ({
    name: "security",
    description: "Security: injection, secrets, and authorization",
    when: "always",
    patterns: [],
    model: "",
    builtin: true,
    source: "builtin",
    ...overrides,
  });

  const data = {
    entries: [
      entry({
        workspaceId: "fix-login-d8efb24f",
        sessionId: "e8cdf5d8",
        path: "/w/fix-login-d8efb24f",
        status: "needsInput",
        intent: "Fix the login rate limit",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "ask-buff-bear-42-3f2a9c1-0a1b2c3d",
        sessionId: "5f6e7d8c",
        path: "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d",
        status: "idle",
        intent: "Questions about pull request #42",
        repos: [repo(buffBear, "buff-bear", head, "commit")],
      }),
      entry({
        workspaceId: "docs-sweep-1a2b3c4d",
        sessionId: "1a2b3c4d",
        path: "/w/docs-sweep-1a2b3c4d",
        status: "working",
        intent: "Sweep the docs for stale links",
        repos: [repo(buffBear, "buff-bear", "main", "branch"), repo(api, "api", "main", "branch")],
      }),
      entry({
        kind: "shell",
        workspaceId: "scratch-91b3f0c4",
        sessionId: "91b3f0c4",
        path: "/w/scratch-91b3f0c4",
        status: "running",
      }),
    ],
    settings: defaultSettings(),
    openDefaults: { fileManager: "open", editor: "code" },
    versions: {
      app: { version: "0.1.0", commit: "v0.3.0-4-gdc313a2" },
      ws: { version: "v0.3.0-4-gdc313a2", path: "/usr/local/bin/ws", error: null },
      claude: { version: "2.1.303", path: "/opt/homebrew/bin/claude", error: null },
    },
  };

  const mock = {
    calls: [],
    data,
    handlers: {},
  };

  let nextId = 1;
  const callbacks = {};

  const reviewPlan = () => ({
    name: "review-buff-bear-42-3f2a9c1",
    repo: buffBear,
    number: 42,
    head,
    profiles: [],
    model: "",
    permissionMode: "",
    focus: "",
  });

  const cachedRepo = (id, path, upstream, branches, tags, sizeBytes) => ({
    id,
    path,
    upstream,
    defaultBranch: "main",
    lastFetchedAt: "2026-10-24T09:00:00Z",
    sizeBytes,
    branches,
    tags,
  });

  const askStarted = () => ({
    id: "ask-buff-bear-42-3f2a9c1-0a1b2c3d",
    session: "5f6e7d8c",
    path: "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d",
    already: false,
  });

  Object.assign(mock.handlers, {
    list_entries: () => ({ entries: data.entries, warnings: [] }),
    cache_repos: () => [buffBear, api],
    cache_list: () => ({
      repos: [
        cachedRepo(buffBear, `/c/cache/repos/${buffBear}.git`, `https://${buffBear}.git`, 3, 1, 18432000),
        cachedRepo(api, `/c/cache/repos/${api}.git`, `https://${api}.git`, 5, 0, 9216000),
      ],
      warnings: [],
    }),
    cache_refs: ({ id }) => ({
      id,
      defaultBranch: "main",
      refs: [
        { name: "main", kind: "branch", commit: "9c04e2a7d1f3", subject: "Merge pull request #39", author: "bobcob7", date: "2026-10-24T07:00:00Z" },
        { name: "feat/rate-limit", kind: "branch", commit: head.slice(0, 12), subject: "Add rate limiting", author: "alice", date: "2026-10-24T09:00:00Z" },
      ],
    }),
    reviews_view: reviewsView,
    reviews_check_now: reviewsView,
    reviews_load_repos: reviewsView,
    reviews_set_watch: reviewsView,
    reviews_dismiss: reviewsView,
    reviews_restore: reviewsView,
    reviews_plan: () => reviewPlan(),
    reviews_profiles: () => [
      profile({ name: "efficiency", description: "Efficiency: wasted work and allocations", when: "always" }),
      profile({ name: "go", description: "Go idioms, concurrency, and error handling", when: "auto", patterns: ["**/*.go"] }),
      profile({ name: "security", description: "Security: injection, secrets, and authorization", when: "always" }),
    ],
    review_report: () => ({
      repo: buffBear,
      ref: head,
      review: null,
      agent: null,
      profiles: [],
      comments: [],
      superseded: [],
      warnings: [],
    }),
    settings_get: () => data.settings,
    settings_set_reviews: ({ reviews }) => {
      data.settings = { ...data.settings, reviews };
      return data.settings;
    },
    settings_set_notifications: ({ notifications }) => {
      data.settings = { ...data.settings, notifications };
      return data.settings;
    },
    settings_set_open: ({ open }) => {
      data.settings = { ...data.settings, open };
      return data.settings;
    },
    open_defaults: () => data.openDefaults,
    versions: () => data.versions,
    loops_list: () => ({ loops: [], warnings: [] }),
    read_transcript: ({ offset }) => ({
      items: offset > 0 ? [] : [
        { id: "u1", at: "2026-10-24T09:01:00Z", kind: "user", text: "Check the login rate limit." },
        { id: "a1", at: "2026-10-24T09:01:20Z", kind: "assistant", text: "The limiter is per-IP; I'll look at the middleware." },
      ],
      next: 2,
      reset: false,
      dropped: 0,
      found: true,
    }),
    terminal_open: () => 1,
    terminal_write: () => null,
    terminal_resize: () => null,
    terminal_close: () => null,
    stop_agent: () => null,
    remove_entry: () => null,
    open_external: () => null,
    workspace_open: ({ with: openWith, path, repos }) => {
      const dir = openWith === "editor" && repos.length === 1 ? `${path}/${repos[0]}` : path;
      const program = (data.settings.open[openWith] || data.openDefaults[openWith]).trim();
      return { dir, command: [...program.split(/\s+/), dir] };
    },
    reviews_ask: () => askStarted(),
    "plugin:event|listen": () => nextId++,
    "plugin:event|unlisten": () => null,
  });

  window.__TAURI_MOCK__ = mock;

  window.__TAURI_INTERNALS__ = {
    metadata: {
      currentWindow: { label: "main" },
      currentWebview: { windowLabel: "main", label: "main" },
    },
    transformCallback(cb) {
      const id = nextId++;
      callbacks[id] = cb;
      window["_" + id] = cb;
      return id;
    },
    unregisterCallback(id) {
      delete callbacks[id];
      delete window["_" + id];
    },
    convertFileSrc(p) {
      return p;
    },
    invoke(cmd, args) {
      mock.calls.push({ cmd, args });
      return new Promise((resolve) => {
        const handler = mock.handlers[cmd];
        if (handler === undefined) {
          console.warn("tauri-mock: unhandled command", cmd, args);
          resolve(null);
          return;
        }
        resolve(handler(args ?? {}));
      });
    },
  };
})();
