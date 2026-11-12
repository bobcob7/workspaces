// Stands in for the Tauri runtime when the app runs in a plain browser, so
// screenshots can be taken without the desktop shell. Load it with
// page.addInitScript({ path }) before the app's scripts. Not a test: it's
// outside src/, and its name doesn't match the test pattern.
(() => {
  const head = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
  const buffBear = "github.com/bobcob7/buff-bear";
  const api = "github.com/bobcob7/api";
  const workspaces = "github.com/bobcob7/workspaces";

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
    loopRun: null,
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

  const roleDir = "/Users/me/Library/Application Support/workspaces/machine/roles";
  const builtins = [
    {
      name: "implementer",
      description: "Implements one task from a tasks file, pushes a branch, and records PASS or FAIL",
      model: "haiku",
      pass: ["PASS"],
      fail: ["FAIL"],
      vars: ["TASK", "TASKS", "BASE", "BRANCH"],
      claudeArgs: ["--permission-mode", "auto"],
      source: "builtin",
      prompt: "You implement exactly one task, {{.Vars.TASK}}, in this repo. Push the branch {{.Vars.BRANCH}}.",
      path: null,
    },
    {
      name: "reviewer",
      description: "Reviews one task's diff against its tasks file and records APPROVED or CHANGES_REQUESTED",
      model: "sonnet",
      pass: ["APPROVED"],
      fail: ["CHANGES_REQUESTED"],
      vars: ["TASK", "TASKS", "BASE", "KNOWN_ISSUES"],
      claudeArgs: ["--permission-mode", "auto"],
      source: "builtin",
      prompt: "Review {{.Vars.TASK}} against {{.Vars.TASKS}}, diffed against {{.Vars.BASE}}.",
      path: null,
    },
    {
      name: "verifier",
      description: "Runs the checks on a branch and records PASS, FLAKY, or FAIL",
      model: "haiku",
      pass: ["PASS", "FLAKY"],
      fail: ["FAIL"],
      vars: ["BRANCH", "CHECKS"],
      claudeArgs: ["--permission-mode", "auto"],
      source: "builtin",
      prompt: "Run {{.Vars.CHECKS}} on {{.Vars.BRANCH}} and report what fails.",
      path: null,
    },
  ];
  const initialRoles = () => [
    builtins[0],
    { ...builtins[1], model: "opus", source: "override", path: `${roleDir}/reviewer.md` },
    builtins[2],
  ];

  const data = {
    roles: initialRoles(),
    // Run history per loop: runs newest first, reports by seq (reportText).
    loopHistory: {
      "deps-bump": {
        total: 59,
        runs: [
          {
            seq: 59,
            status: "blocked",
            trigger: "tick",
            startedAt: "2026-10-25T08:00:00Z",
            finishedAt: null,
            durationSeconds: null,
            workspace: "loop-deps-bump-2b3c4d5e",
            workspaceExists: true,
            session: "2b3c4d5e",
            error: null,
            gate: null,
            repeats: 1,
            verdict: {
              verdict: "BLOCKED",
              outcome: "blocked",
              recordedAt: "2026-10-25T08:06:12Z",
              report: "/w/.loops/deps-bump/reports/59.md",
            },
          },
          {
            seq: 58,
            status: "needs_attention",
            trigger: "tick",
            startedAt: "2026-10-25T07:00:00Z",
            finishedAt: "2026-10-25T07:03:40Z",
            durationSeconds: 220,
            workspace: null,
            workspaceExists: false,
            session: null,
            error: null,
            gate: null,
            repeats: 1,
            verdict: null,
          },
          {
            seq: 57,
            status: "running",
            trigger: "tick",
            startedAt: "2026-10-24T08:00:00Z",
            finishedAt: null,
            durationSeconds: null,
            workspace: "loop-deps-bump-1a2b3c4d",
            workspaceExists: true,
            session: "e8cdf5d8",
            error: null,
            gate: null,
            repeats: 1,
            verdict: null,
          },
          {
            seq: 56,
            status: "succeeded",
            trigger: "tick",
            startedAt: "2026-10-23T08:00:00Z",
            finishedAt: "2026-10-23T08:14:30Z",
            durationSeconds: 870,
            workspace: "loop-deps-bump-9f8e7d6c",
            workspaceExists: true,
            session: "b41c0e7a",
            error: null,
            gate: { at: "2026-10-23T07:59:58Z", outcome: "passed", exitCode: 0, message: null },
            repeats: 1,
            verdict: {
              verdict: "PASS",
              outcome: "pass",
              recordedAt: "2026-10-23T08:14:30Z",
              report: "/w/.loops/deps-bump/reports/56.md",
            },
          },
          {
            seq: 55,
            status: "failed",
            trigger: "tick",
            startedAt: "2026-10-22T08:00:00Z",
            finishedAt: "2026-10-22T08:25:20Z",
            durationSeconds: 1520,
            workspace: "loop-deps-bump-5a4b3c2d",
            workspaceExists: false,
            session: "7c2d9e1f",
            error: null,
            gate: { at: "2026-10-22T07:59:57Z", outcome: "passed", exitCode: 0, message: null },
            repeats: 1,
            verdict: {
              verdict: "FAIL",
              outcome: "fail",
              recordedAt: "2026-10-22T08:25:20Z",
              report: "/w/.loops/deps-bump/reports/55.md",
            },
          },
          {
            seq: 54,
            status: "skipped",
            trigger: "tick",
            startedAt: "2026-10-21T08:00:00Z",
            finishedAt: null,
            durationSeconds: null,
            workspace: null,
            workspaceExists: false,
            session: null,
            error: null,
            gate: { at: "2026-10-21T08:00:02Z", outcome: "skipped", exitCode: 1, message: "no outdated modules" },
            repeats: 12,
            verdict: null,
          },
          {
            seq: 53,
            status: "done",
            trigger: "tick",
            startedAt: "2026-10-20T08:00:00Z",
            finishedAt: "2026-10-20T08:10:40Z",
            durationSeconds: 640,
            workspace: "loop-deps-bump-0e1f2a3b",
            workspaceExists: false,
            session: "3a4b5c6d",
            error: null,
            gate: null,
            repeats: 1,
            verdict: null,
          },
          {
            seq: 52,
            status: "start_failed",
            trigger: "tick",
            startedAt: "2026-10-19T08:00:00Z",
            finishedAt: null,
            durationSeconds: null,
            workspace: null,
            workspaceExists: false,
            session: null,
            error: { code: "claude_missing", message: "Claude Code isn't installed" },
            gate: null,
            repeats: 1,
            verdict: null,
          },
          {
            seq: 51,
            status: "gone",
            trigger: "tick",
            startedAt: "2026-10-18T08:00:00Z",
            finishedAt: "2026-10-18T08:05:00Z",
            durationSeconds: 300,
            workspace: "loop-deps-bump-77aa88bb",
            workspaceExists: false,
            session: "0f1e2d3c",
            error: null,
            gate: null,
            repeats: 1,
            verdict: null,
          },
        ],
        reports: {
          59: "# Question\n\nWhich major version of eslint should I target? v9 needs a flat config.\n",
          56: [
            "## Bumped 3 modules",
            "",
            "- `github.com/spf13/cobra` v1.8.1 to v1.9.0",
            "- `golang.org/x/net` v0.30.0 to v0.32.0",
            "- `typescript` 5.6.3 to 5.7.2",
            "",
            "PR: https://github.com/bobcob7/buff-bear/pull/88",
          ].join("\n"),
          55: [
            "## Tests failed after the bump",
            "",
            "- `internal/loop`: `TestDataStore_ReadHistory` panics on an empty file.",
            "- No PR was opened.",
          ].join("\n"),
        },
      },
    },
    // Memory per loop, keyed by name; entries are listed sorted by key.
    loopMemory: {
      "deps-bump": {
        entries: [
          { key: "seen", value: "#412 #415 #418", rev: 3, updatedAt: "2026-10-23T08:14:00Z", updatedBy: "loop-deps-bump-9f8e7d6c" },
          { key: "last_pr", value: "https://github.com/bobcob7/buff-bear/pull/88", rev: 1, updatedAt: "2026-10-23T08:14:00Z", updatedBy: "loop-deps-bump-9f8e7d6c" },
          {
            key: "notes",
            value: [
              "## Deps notes",
              "- Skip `golang.org/x/tools`: pinned for the lint toolchain.",
              "- Bump Go modules first, then npm.",
              "- Run `make desktop-test` before opening the PR.",
              "- Rebase on main if the bot PR conflicts.",
              "- Ask before major version bumps.",
            ].join("\n"),
            rev: 7,
            updatedAt: "2026-10-21T08:00:03Z",
            updatedBy: null,
          },
        ],
      },
    },
    loops: [
      {
        name: "deps-bump",
        title: "Dependency bumps",
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
        hidden: true,
      },
      {
        name: "triage",
        title: "",
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
        hidden: false,
      },
    ],
    entries: [
      entry({
        workspaceId: "fix-login-d8efb24f",
        sessionId: "e8cdf5d8",
        path: "/w/fix-login-d8efb24f",
        status: "needsInput",
        intent: "Fix the login rate limit",
        updatedAt: "2026-10-24T09:40:00Z",
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
      entry({
        workspaceId: "fix-login-t1-impl-2b7c9e10",
        sessionId: "2b7c9e10",
        parentId: "fix-login-d8efb24f",
        path: "/w/fix-login-t1-impl-2b7c9e10",
        status: "working",
        intent: "Implement T1: token bucket",
        updatedAt: "2026-10-24T09:35:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "rate-limit-tests-4e5f6a7b",
        sessionId: "4e5f6a7b",
        parentId: "fix-login-t1-impl-2b7c9e10",
        path: "/w/rate-limit-tests-4e5f6a7b",
        status: "needsInput",
        intent: "Write rate limit tests",
        updatedAt: "2026-10-24T09:32:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "fix-login-t1-review-6d0a4f21",
        sessionId: "6d0a4f21",
        parentId: "fix-login-d8efb24f",
        path: "/w/fix-login-t1-review-6d0a4f21",
        status: "done",
        intent: "Review T1",
        updatedAt: "2026-10-24T09:20:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "review-buff-bear-42-3f2a9c1-1f0c33d8",
        sessionId: "1f0c33d8",
        parentId: "fix-login-d8efb24f",
        path: "/w/review-buff-bear-42-3f2a9c1-1f0c33d8",
        status: "working",
        hidden: true,
        intent: "Review pull request #42",
        updatedAt: "2026-10-24T09:25:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "lint-sweep-5b1e93aa",
        sessionId: "5b1e93aa",
        path: "/w/lint-sweep-5b1e93aa",
        status: "idle",
        hidden: true,
        intent: "Fix every lint warning",
        updatedAt: "2026-10-24T08:00:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "lint-fix-api-6e7f8a9b",
        sessionId: "6e7f8a9b",
        parentId: "lint-sweep-5b1e93aa",
        path: "/w/lint-fix-api-6e7f8a9b",
        status: "working",
        intent: "Fix lint in api",
        updatedAt: "2026-10-24T08:10:00Z",
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "loop-deps-bump-2b3c4d5e",
        sessionId: "2b3c4d5e",
        path: "/w/loop-deps-bump-2b3c4d5e",
        status: "needsInput",
        intent: "Dependency bumps",
        detail: "Which major version of eslint should I target?",
        hidden: false,
        loopRun: { loop: "deps-bump", attention: "blocked" },
      }),
      entry({
        workspaceId: "loop-status-check-7c8d9e0f",
        sessionId: "7c8d9e0f",
        path: "/w/loop-status-check-7c8d9e0f",
        status: "needsInput",
        intent: "Status check",
        hidden: false,
        loopRun: { loop: "status-check", attention: "needs_attention" },
      }),
      entry({
        workspaceId: "loop-deps-bump-9f8e7d6c",
        sessionId: "9f8e7d6c",
        path: "/w/loop-deps-bump-9f8e7d6c",
        status: "done",
        intent: "Dependency bumps",
        hidden: true,
        loopRun: { loop: "deps-bump", attention: null },
      }),
    ],
    settings: defaultSettings(),
    openDefaults: { fileManager: "open", editor: "code" },
    config: {
      path: "/Users/me/Library/Application Support/workspaces/config.toml",
      settings: [
        { key: "defaults.host", type: "string", value: "github.com", default: "github.com", set: true },
        { key: "defaults.owners", type: "strings", value: ["bobcob7", "acme"], default: [], set: true },
        { key: "review.timeout", type: "duration", value: "1h", default: "60m", set: false },
        { key: "review.remove_finished", type: "bool", value: true, default: true, set: false },
        { key: "review.profiles.security.when", type: "string", value: "", default: "", set: false },
        { key: "review.profiles.security.model", type: "string", value: "opus", default: "", set: true },
      ],
    },
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

  const findRole = (name) => {
    const role = data.roles.find((candidate) => candidate.name === name);
    if (role === undefined) {
      throw { code: "role_unknown", message: `no role named ${name}` };
    }
    return role;
  };

  const findLoop = (name) => {
    const found = data.loops.find((loop) => loop.name === name);
    if (found === undefined) {
      throw new Error(`no loop named ${name}`);
    }
    return found;
  };

  // Older runs so the history has more than one page.
  const bump = data.loopHistory["deps-bump"];
  for (let seq = 50; seq >= 36; seq -= 1) {
    const day = String(seq - 33).padStart(2, "0");
    bump.runs.push({
      seq,
      status: seq % 7 === 0 ? "failed" : "succeeded",
      trigger: "tick",
      startedAt: `2026-10-${day}T08:00:00Z`,
      finishedAt: `2026-10-${day}T08:10:00Z`,
      durationSeconds: 600,
      workspace: `loop-deps-bump-${seq}`,
      workspaceExists: false,
      session: null,
      error: null,
      gate: null,
      repeats: 1,
      verdict: null,
    });
  }
  bump.total = bump.runs.length;

  const historyOf = (name) => data.loopHistory[name] ?? { total: 0, runs: [], reports: {} };

  const memoryOf = (name) => data.loopMemory[name] ?? { entries: [] };

  const sortedEntries = (name) => [...memoryOf(name).entries].sort((a, b) => (a.key < b.key ? -1 : 1));

  // The fields a NewLoop request sets. Absent script means no script; a script
  // without a timeout gets the default; "" model and permission mode mean default.
  const loopFields = (request) => {
    const script = request.script ?? null;
    return {
      title: request.title ?? "",
      every: request.every,
      prompt: request.prompt,
      specs: request.specs,
      keep: request.keep ?? 5,
      model: request.model || null,
      permissionMode: request.permissionMode || null,
      script,
      scriptTimeout: script === null ? null : request.scriptTimeout ?? "1m",
      vars: Object.fromEntries((request.vars ?? []).map(({ key, value }) => [key, value])),
      hidden: request.hidden ?? true,
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

  const cachedRepoIds = [buffBear, api, workspaces, "github.com/acme/api"];

  const configKinds = {
    "defaults.host": "string",
    "defaults.owners": "strings",
    "review.timeout": "duration",
    "review.remove_finished": "bool",
  };
  const profileKey = /^review\.profiles\.[^.]+\.(when|model)$/;

  const isConfigKey = (key) => key in configKinds || profileKey.test(key);

  const unknownConfigKey = (key) => ({ code: "config_key_unknown", message: "unknown config key " + key });

  // A profile's two rows appear together, as ws config set adds them.
  const withProfileRows = (key, settings) => {
    const name = key.split(".")[2];
    const missing = [`review.profiles.${name}.when`, `review.profiles.${name}.model`]
      .filter((rowKey) => !settings.some((setting) => setting.key === rowKey))
      .map((rowKey) => ({ key: rowKey, type: "string", value: "", default: "", set: false }));
    return [...settings, ...missing];
  };

  const configValue = (key, values) => {
    switch (configKinds[key] ?? "string") {
      case "strings":
        return values.map((value) => value.toLowerCase());
      case "bool":
        return values[0] === "true";
      case "duration":
        return values[0] === "90m" ? "1h30m" : values[0];
      default:
        return key === "defaults.host" ? values[0].toLowerCase() : values[0];
    }
  };

  const replaceSetting = (updated) => {
    data.config = {
      ...data.config,
      settings: data.config.settings.map((setting) => (setting.key === updated.key ? updated : setting)),
    };
  };

  const searchCache = ({ query, refs, limit }) => {
    const needle = query.trim().toLowerCase();
    if (needle === "") {
      return [];
    }
    const found = cachedRepoIds.flatMap((id) => {
      if (!id.toLowerCase().includes(needle)) {
        return [];
      }
      const repoMatch = { id, ref: null };
      return refs && id === buffBear ? [repoMatch, { id, ref: "feat/rate-limit" }] : [repoMatch];
    });
    return found.slice(0, limit);
  };

  Object.assign(mock.handlers, {
    list_entries: () => ({ entries: data.entries, warnings: [] }),
    cache_repos: () => [buffBear, api, workspaces],
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
    cache_search: searchCache,
    cache_resolve: ({ spec }) => {
      if (spec === "api") {
        throw {
          code: "repo_ambiguous",
          message: "api matches 2 cached repos",
          details: { candidates: ["github.com/bobcob7/api", "github.com/acme/api"] },
        };
      }
      if (spec === "buff-bear") {
        return { id: buffBear, ref: null, upstream: `https://${buffBear}.git`, cached: true, source: "cache" };
      }
      return { id: `github.com/bobcob7/${spec}`, ref: null, upstream: `https://github.com/bobcob7/${spec}.git`, cached: false, source: "probe" };
    },
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
    config_list: () => data.config,
    config_set: ({ key, values }) => {
      if (!isConfigKey(key)) {
        throw unknownConfigKey(key);
      }
      if (key === "defaults.host" && /[/:@ ]/.test(values[0] ?? "")) {
        throw { code: "config_value_invalid", message: `${values[0]} isn't a valid host` };
      }
      const settings = profileKey.test(key) ? withProfileRows(key, data.config.settings) : data.config.settings;
      data.config = { ...data.config, settings };
      const current = settings.find((setting) => setting.key === key);
      const updated = { ...current, value: configValue(key, values), set: true };
      replaceSetting(updated);
      return { path: data.config.path, setting: updated, changed: true };
    },
    config_unset: ({ key }) => {
      const current = data.config.settings.find((setting) => setting.key === key);
      if (current === undefined) {
        throw unknownConfigKey(key);
      }
      const updated = { ...current, value: current.default, set: false };
      replaceSetting(updated);
      return { path: data.config.path, setting: updated, changed: current.set };
    },
    settings_environment: () => ({
      stateFile: "/Users/me/Library/Application Support/com.bobcob7.workspaces/desktop.json",
      wsConfigDir: null,
      claudeConfigDir: null,
      path: "/opt/homebrew/bin:/usr/bin:/bin",
      online: null,
    }),
    versions: () => data.versions,
    roles_list: () => data.roles.map(({ prompt, path, ...role }) => role),
    roles_show: ({ name }) => findRole(name),
    roles_add: ({ name, draft }) => {
      if (data.roles.some((role) => role.name === name)) {
        throw { code: "role_exists", message: `a role named ${name} already exists` };
      }
      const added = { ...draft, name, source: "user", path: `${roleDir}/${name}.md` };
      data.roles = [...data.roles, added];
      return added;
    },
    roles_edit: ({ name, draft }) => {
      const current = findRole(name);
      const edited = { ...current, ...draft, source: current.source === "user" ? "user" : "override", path: `${roleDir}/${name}.md` };
      data.roles = data.roles.map((role) => (role.name === name ? edited : role));
      return edited;
    },
    roles_remove: ({ name }) => {
      const current = findRole(name);
      if (current.source === "builtin") {
        throw { code: "usage", message: `${name} is built in; there is no file to remove` };
      }
      const builtin = builtins.find((role) => role.name === name);
      const restored = current.source === "override" && builtin !== undefined;
      data.roles = restored ? data.roles.map((role) => (role.name === name ? builtin : role)) : data.roles.filter((role) => role.name !== name);
      return { name, path: `${roleDir}/${name}.md`, restored };
    },
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
    loops_history: ({ name, limit }) => {
      const history = historyOf(name);
      return { loop: name, runs: history.runs.slice(0, limit ?? 20), total: history.total, warnings: [] };
    },
    loops_history_run: ({ name, seq }) => {
      const run = historyOf(name).runs.find((candidate) => candidate.seq === seq);
      if (run === undefined) {
        throw { code: "loop_not_found", message: `run ${seq} of loop ${name} not found` };
      }
      return { loop: name, run, reportText: historyOf(name).reports[seq] ?? null, warnings: [] };
    },
    loops_history_clear: ({ name }) => {
      const removed = historyOf(name).total;
      data.loopHistory[name] = { total: 0, runs: [], reports: {} };
      return { loop: name, removed, warnings: [] };
    },
    loops_memory: ({ name }) => {
      const entries = sortedEntries(name);
      const bytes = entries.reduce((sum, entry) => sum + entry.value.length, 0);
      return { loop: name, entries, bytes, warnings: [] };
    },
    loops_memory_set: ({ name, key, value, expectRev }) => {
      const current = memoryOf(name).entries.find((entry) => entry.key === key);
      const rev = current?.rev ?? 0;
      if (expectRev !== null && expectRev !== undefined && expectRev !== rev) {
        throw { code: "memory_conflict", message: `${key} changed (rev ${rev})` };
      }
      const entry = { key, value, rev: rev + 1, updatedAt: "2026-10-24T09:00:00Z", updatedBy: null };
      const others = memoryOf(name).entries.filter((candidate) => candidate.key !== key);
      data.loopMemory[name] = { entries: [...others, entry] };
      return { loop: name, entry, warnings: [] };
    },
    loops_memory_remove: ({ name, key }) => {
      const entries = memoryOf(name).entries;
      if (!entries.some((entry) => entry.key === key)) {
        throw { code: "memory_not_found", message: `no memory key ${key}` };
      }
      data.loopMemory[name] = { entries: entries.filter((entry) => entry.key !== key) };
      return { loop: name, key, removed: true, warnings: [] };
    },
    loops_memory_clear: ({ name }) => {
      const removed = memoryOf(name).entries.length;
      data.loopMemory[name] = { entries: [] };
      return { loop: name, removed, warnings: [] };
    },
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
