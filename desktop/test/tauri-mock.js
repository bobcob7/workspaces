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

  const depsPrompt = [
    "Update the dependencies of every repo in the workspace and open one pull request per repo.",
    "",
    "Work in the checked-out branch named by BRANCH. Keep each change small and focused:",
    "one module or one major bump per commit, so a reviewer can read them one at a time.",
    "",
    "Before you change anything, read the lock files and the CI configuration, and note which",
    "tests run on pull requests. Do not bump a dependency that CI cannot exercise without asking.",
    "",
    "For each outdated module:",
    "1. Read its changelog and release notes for breaking changes since the current version.",
    "2. Bump it to the newest release that keeps the public API the repo uses.",
    "3. Run the test suite and the linter. Fix what the bump breaks, in the same commit.",
    "4. If a bump needs a code change you cannot make safely, leave that module alone and say why.",
    "",
    "Open the pull request against main with a title starting \"deps:\" and a body that lists",
    "each bump with its old and new version, the changelog link, and any code you changed.",
    "Label the pull request LABEL so the weekly review can find it.",
    "",
    "Do not merge anything. Do not touch the release configuration or the Go toolchain version.",
    "When the run is done, reply with the list of pull requests you opened and any module you skipped.",
  ].join("\n");

  const data = {
    loops: [
      {
        name: "deps-bump",
        every: "24h",
        prompt: depsPrompt,
        specs: ["buff-bear", "api"],
        model: null,
        permissionMode: "acceptEdits",
        keep: 5,
        paused: false,
        createdAt: "2026-10-24T08:00:00Z",
        nextRunAt: "2026-10-25T08:00:00Z",
        lastRun: {
          startedAt: "2026-10-24T08:00:00Z",
          workspace: "loop-deps-bump-1a2b3c4d",
          session: "e8cdf5d8",
          error: null,
        },
        script: [
          "go list -m -u -f '{{if .Update}}{{.Path}}{{end}}' all | grep .",
          "# Prints one module per outdated dependency; no output skips the run.",
          "exit 0",
        ].join("\n"),
        scriptTimeout: "2m",
        vars: { BRANCH: "main", LABEL: "deps" },
        lastGate: {
          at: "2026-10-24T08:00:02Z",
          outcome: "passed",
          exitCode: 0,
          message: null,
        },
      },
      {
        name: "triage",
        every: "1h",
        prompt: "Label new issues.",
        specs: [],
        model: null,
        permissionMode: null,
        keep: 5,
        paused: true,
        createdAt: "2026-10-23T09:00:00Z",
        nextRunAt: null,
        lastRun: {
          startedAt: "2026-10-24T07:00:00Z",
          workspace: null,
          session: null,
          error: { code: "claude_untrusted", message: "the workspace is not trusted by Claude Code" },
        },
        script: null,
        scriptTimeout: null,
        vars: {},
        lastGate: null,
      },
    ],
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

  const findLoop = (name) => {
    const found = data.loops.find((loop) => loop.name === name);
    if (found === undefined) {
      throw new Error(`no loop named ${name}`);
    }
    return found;
  };

  // The fields a NewLoop request sets. Absent script means no script; a script
  // without a timeout gets the default; "" model and permission mode mean default.
  const loopFields = (request) => {
    const script = request.script ?? null;
    return {
      every: request.every,
      prompt: request.prompt,
      specs: request.specs,
      keep: request.keep ?? 5,
      model: request.model || null,
      permissionMode: request.permissionMode || null,
      script,
      scriptTimeout: script === null ? null : request.scriptTimeout ?? "1m",
      vars: Object.fromEntries((request.vars ?? []).map(({ key, value }) => [key, value])),
    };
  };

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
    loops_list: () => ({ loops: data.loops, warnings: [] }),
    loops_add: ({ request }) => {
      const added = { ...loopFields(request), name: request.name, paused: false, createdAt: "2026-10-24T12:00:00Z", nextRunAt: "2026-10-24T13:00:00Z", lastRun: null, lastGate: null };
      data.loops = [...data.loops, added];
      return added;
    },
    loops_edit: ({ request }) => {
      const edited = { ...findLoop(request.name), ...loopFields(request) };
      data.loops = data.loops.map((loop) => (loop.name === request.name ? edited : loop));
      return edited;
    },
    loops_set_paused: ({ name, paused }) => {
      const updated = { ...findLoop(name), paused };
      data.loops = data.loops.map((loop) => (loop.name === name ? updated : loop));
      return updated;
    },
    loops_remove: ({ name }) => {
      const removed = findLoop(name);
      data.loops = data.loops.filter((loop) => loop.name !== name);
      return removed;
    },
    loops_run_now: ({ name }) => ({
      name,
      workspace: `loop-${name}-1a2b3c4d`,
      session: "e8cdf5d8",
      path: `/w/loop-${name}-1a2b3c4d`,
    }),
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
