// Stands in for the Tauri runtime when the app runs in a plain browser, so
// screenshots can be taken without the desktop shell. Load it with
// page.addInitScript({ path }) before the app's scripts. Not a test: it's
// outside src/, and its name doesn't match the test pattern.
(() => {
  const head = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
  const buffBear = "github.com/bobcob7/buff-bear";
  const api = "github.com/bobcob7/api";
  const workspaces = "github.com/bobcob7/workspaces";
  const edge = "github.com/bobcob7/edge";
  const rig = "github.com/bobcob7/rig";
  const edgeReviewId = "review-edge-7-9b1d2c3-4e5f6a7b";

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
    runner: overrides.kind === "shell" ? "" : "claude",
    model: overrides.kind === "shell" ? null : "sonnet-5-5",
    wsRunner: null,
    progress: null,
    rollup: leafRollup(false),
    environment: "local",
    environmentOnline: true,
    snapshotStale: false,
    ...overrides,
  });

  /** An entry on a remote environment: keyed env:<name>:<id>, as the backend keys it. */
  const remoteEntry = (environment, overrides) =>
    entry({
      key: `env:${environment}:${overrides.workspaceId}`,
      environment,
      path: `/home/me/.ws/${overrides.workspaceId}`,
      ...overrides,
    });

  /** A workspace with no step record counts as one step, done if it passed (ws progress). */
  function leafRollup(done) {
    return { total: 1, done: done ? 1 : 0, fraction: done ? 1 : 0 };
  }

  const wsRun = (overrides) => ({
    modelAlias: "qwen9b-think",
    endpoint: "gpu",
    url: "http://gpu-a.inference.svc:1234",
    api: "openai",
    contextTokens: 131072,
    step: 12,
    maxSteps: 40,
    restarts: 0,
    usage: { requests: 12, promptTokens: 210000, completionTokens: 9100 },
    mcp: [],
    exit: null,
    endpointChanges: 0,
    ...overrides,
  });

  const checks = (passed, failed, pending) => ({ total: passed + failed + pending, passed, failed, pending });

  // Hosts' forges and tokens; reviews_set_hosts and reviews_set_token change them.
  const hosts = {
    "ghe.example.com": { forge: "github", choice: "auto", detected: false, login: "bob", token: null, tokenEnv: null, error: null },
    "git.example.com": {
      forge: "forgejo",
      choice: "forgejo",
      detected: true,
      login: "bob",
      token: "env",
      tokenEnv: "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM",
      error: null,
    },
    "github.com": { forge: "github", choice: "auto", detected: false, login: "bobcob7", token: null, tokenEnv: null, error: null },
    "gitlab.example.com": {
      forge: null,
      choice: "auto",
      detected: false,
      login: null,
      token: null,
      tokenEnv: null,
      error: {
        code: "forge_unknown",
        message: "gitlab.example.com isn't a GitHub or Forgejo host. Choose its forge in Settings › Forges.",
      },
    },
  };

  // The hosts in config.toml come first, in order; the other cached hosts follow, sorted.
  const configuredHosts = () => data.config.settings.find((setting) => setting.key === "hosts")?.value ?? [];

  const hostsView = () => {
    const configured = configuredHosts();
    const others = Object.keys(hosts)
      .filter((host) => !configured.some((item) => item.name === host))
      .sort();
    return [...configured.map((item) => item.name), ...others].map((host, index) => ({
      host,
      ...hosts[host],
      configured: index < configured.length,
      primary: configured.length > 0 && index === 0,
    }));
  };

  const pullRequest = (overrides) => ({
    repo: buffBear,
    forge: "github",
    number: 42,
    title: "Add rate limiting to login",
    author: { login: "alice" },
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
    merge: { status: "ready", reasons: [], checks: checks(2, 0, 0) },
    ...overrides,
  });

  const pullRequests = () =>
    [
      pullRequest(),
      pullRequest({
        number: 57,
        title: "Bump the session cache TTL",
        author: { login: "carol" },
        isDraft: true,
        requested: false,
        merge: { status: "draft", reasons: ["draft"], checks: checks(0, 0, 0) },
      }),
      pullRequest({
        number: 81,
        title: "Upgrade the Go toolchain",
        author: { login: "alice" },
        requested: false,
        merge: { status: "pending", reasons: ["pending"], checks: checks(2, 0, 2) },
      }),
      pullRequest({
        repo: "ghe.example.com/team/api",
        forge: "github",
        number: 311,
        title: "Move retries into the client",
        author: { login: "bob" },
        requested: false,
        mine: true,
        merge: { status: "conflicting", reasons: ["conflicting"], checks: checks(3, 0, 0) },
      }),
      pullRequest({
        repo: "git.example.com/team/svc",
        forge: "forgejo",
        number: 15262,
        title: "refactor: move RemoveTeamMember",
        author: { login: "alice" },
        merge: { status: "failing", reasons: ["failing", "awaitingApproval"], checks: checks(2, 1, 0) },
      }),
      pullRequest({
        repo: "git.example.com/team/svc",
        forge: "forgejo",
        number: 15301,
        title: "Add audit log export",
        author: { login: "carol" },
        requested: false,
        merge: { status: "behind", reasons: ["behind"], checks: checks(2, 0, 0) },
      }),
      pullRequest({
        number: 43,
        title: "Require a second reviewer for billing",
        author: { login: "bob" },
        requested: false,
        merge: { status: "awaitingApproval", reasons: ["awaitingApproval"], checks: checks(2, 0, 0) },
      }),
    ].map((pr) => ({
      key: `${pr.repo}#${pr.number}`,
      url: `https://${pr.repo}/${pr.forge === "forgejo" ? "pulls" : "pull"}/${pr.number}`,
      ...pr,
    }));

  /** The environment each watched repo's reviews run on. */
  const repoEnvironments = { [edge]: "linux-server", [rig]: "win-rig" };

  const repoRow = (id, watch) => {
    const host = id.split("/")[0];
    const forge = hosts[host]?.forge ?? null;
    const open = pullRequests().filter((pr) => pr.repo === id).length;
    return {
      id,
      environment: repoEnvironments[id] ?? null,
      github: forge === "github",
      forge,
      watch,
      autoReview: false,
      open: watch ? open : null,
      checkedAtMs: watch ? Date.parse("2026-10-24T10:00:00Z") : null,
      error: null,
    };
  };

  /** A review run on a remote environment, as the Reviews view lists it. */
  const remoteRun = (environment, workspaceId, sha, stale) => ({
    workspaceId,
    sessionId: null,
    sha,
    status: "done",
    createdAt: "2026-10-24T09:30:00Z",
    detail: null,
    result: "2 findings",
    links: [],
    auto: false,
    recorded: true,
    requester: null,
    removed: true,
    environment,
    stale,
  });

  const remotePullRequests = () => [
    pullRequest({
      repo: edge,
      number: 7,
      title: "Cache the edge config",
      group: "reviewed",
      reviews: [
        remoteRun("linux-server", edgeReviewId, "9b1d2c3e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c", false),
        remoteRun("win-rig", "review-edge-7-9b1d2c3-77aa88bb", "9b1d2c3e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c", true),
      ],
      merge: { status: "ready", reasons: [], checks: checks(1, 0, 0) },
    }),
    pullRequest({
      repo: rig,
      number: 3,
      title: "Pin the rig toolchain",
      group: "reviewed",
      reviews: [remoteRun("win-rig", "review-rig-3-1a2b3c4-5d6e7f80", "1a2b3c4e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c", true)],
      merge: { status: "ready", reasons: [], checks: checks(1, 0, 0) },
    }),
  ].map((pr) => ({ key: `${pr.repo}#${pr.number}`, url: `https://${pr.repo}/pull/${pr.number}`, ...pr }));

  const reviewsView = () => ({
    checking: false,
    checkedAtMs: Date.parse("2026-10-24T10:00:00Z"),
    error: null,
    repos: [
      repoRow(buffBear, true),
      repoRow(api, false),
      repoRow("ghe.example.com/team/api", true),
      repoRow("git.example.com/team/svc", true),
      repoRow("gitlab.example.com/team/web", true),
      repoRow(edge, true),
      repoRow(rig, true),
    ],
    hosts: hostsView(),
    prs: [...pullRequests(), ...remotePullRequests()],
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

  // What `ws loop list --env <name> --cached` reads from the snapshot: no
  // prompt, script or vars (a summary), and stale when offline.
  const remoteLoops = (environment) => {
    const offline = environment === "win-rig";
    const summary = (name, title, every, paused, nextRunAt, hasScript) => ({
      name, title, every, prompt: null, specs: null, model: null, permissionMode: null, keep: null,
      paused, createdAt: null, nextRunAt, lastRun: null, script: hasScript ? "" : null, scriptTimeout: null, vars: null,
      lastGate: hasScript ? { at: "2026-11-23T08:00:02Z", outcome: "passed", exitCode: 0, message: null } : null,
      hidden: true, runner: "claude", endpoint: null, summary: true, environment, stale: offline,
    });
    const loops = offline
      ? [summary("rig-nightly", "Nightly build", "24h", false, null, false)]
      : [summary("disk-watch", "Disk watch", "1h", false, "2026-11-23T09:00:00Z", true), summary("log-triage", "", "6h", true, null, false)];
    return { loops, warnings: [], environmentStatus: offline ? "offline" : "online", loopsUnknown: false };
  };

  const data = {
    roles: initialRoles(),
    environments: [
      {
        id: "env_7f3a", name: "linux-server", state: "enrolled", status: "online",
        lastSeen: "2026-11-23T08:10:00Z", createdAt: "2026-10-02T09:00:00Z", enrolledAt: "2026-10-02T09:04:00Z",
        certNotAfter: "2027-10-02T09:04:00Z", certCa: "current",
        hello: { wsVersion: "0.9.0", os: "linux", arch: "amd64", claude: "present", claudeVersion: "2.1.0", certNotAfter: "2027-10-02T09:04:00Z" },
        workspaces: 2,
      },
      {
        id: "env_b20d", name: "win-rig", state: "enrolled", status: "offline",
        lastSeen: "2026-11-22T14:02:00Z", createdAt: "2026-10-20T16:30:00Z", enrolledAt: "2026-10-20T16:35:00Z",
        certNotAfter: "2027-10-20T16:35:00Z", certCa: "current",
        hello: null, workspaces: 1,
      },
    ],
    environmentCa: {
      current: { fingerprint: "sha256:4c1e0a7d", notAfter: "2036-10-02T00:00:00Z" },
      old: null,
      environmentsOnCurrent: 2,
      environmentsOnOld: 0,
      warnings: [],
    },
    runnerSettings: {
      runner: { model: "qwen9b" },
      endpoints: [
        { name: "gpu", url: "http://gpu-a.inference.svc:1234", api: "openai", probe: "none", request_timeout: "10m", idle_timeout: "90s" },
        { name: "ollama", url: "http://localhost:11434", api: "ollama", probe: "ollama", request_timeout: "10m", idle_timeout: "90s" },
      ],
      models: [
        { alias: "qwen9b", endpoint: "gpu", id: "qwen/qwen3.5-9b", context_tokens: 65536, max_output: 8192, temperature: 0.2, reasoning: "none" },
        { alias: "qwen9b-think", endpoint: "gpu", id: "qwen/qwen3.5-9b", context_tokens: 131072, max_output: 8192, temperature: 0.2, reasoning: "" },
        { alias: "qwen27b", endpoint: "gpu", id: "qwen/qwen3.5-27b", context_tokens: 32768, max_output: 4096, temperature: 0, reasoning: "" },
      ],
      roles: [{ role: "implementer", runner: "ws", model: "qwen9b", max_steps: 80, timeout: "60m", max_tokens: 8192, mcp_servers: [] }],
    },
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
      {
        name: "issue-triage",
        title: "Issue triage",
        every: "6h",
        prompt: "Label new issues and ask for missing details.",
        specs: ["buff-bear"],
        model: "qwen9b",
        permissionMode: null,
        keep: 5,
        paused: false,
        createdAt: "2026-10-20T09:00:00Z",
        nextRunAt: "2026-10-25T12:00:00Z",
        lastRun: null,
        script: null,
        scriptTimeout: null,
        vars: {},
        lastGate: null,
        hidden: true,
        runner: "ws",
        endpoint: "gpu",
      },
    ],
    entries: [
      entry({
        workspaceId: "fix-login-d8efb24f",
        model: "opus-5-5",
        sessionId: "e8cdf5d8",
        path: "/w/fix-login-d8efb24f",
        status: "needsInput",
        intent: "Fix the login rate limit",
        updatedAt: "2026-10-24T09:40:00Z",
        progress: { name: "fix login", total: 4, done: 1, note: null, updatedAt: "2026-10-24T09:00:00Z" },
        rollup: { total: 12, done: 3, fraction: 0.25 },
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
        progress: { name: "token bucket", total: 3, done: 1, note: "tests", updatedAt: "2026-10-24T09:35:00Z" },
        rollup: { total: 4, done: 1, fraction: 0.25 },
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
        rollup: leafRollup(false),
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "fix-login-t1-review-6d0a4f21",
        model: "haiku-5-5",
        sessionId: "6d0a4f21",
        parentId: "fix-login-d8efb24f",
        path: "/w/fix-login-t1-review-6d0a4f21",
        status: "done",
        intent: "Review T1",
        result: "FAIL",
        updatedAt: "2026-10-24T09:20:00Z",
        rollup: leafRollup(false),
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
      }),
      entry({
        workspaceId: "fix-login-t2-impl-41aa0b7c",
        sessionId: "rn-5d2e8b10",
        parentId: "fix-login-d8efb24f",
        path: "/w/fix-login-t2-impl-41aa0b7c",
        status: "working",
        intent: "Implement T2: login throttle",
        updatedAt: "2026-10-24T09:38:00Z",
        rollup: leafRollup(false),
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
        runner: "ws",
        model: "qwen/qwen3.5-9b",
        wsRunner: wsRun({}),
      }),
      entry({
        workspaceId: "fix-login-t3-verify-0d9e2f61",
        sessionId: "rn-91b0c7aa",
        parentId: "fix-login-d8efb24f",
        path: "/w/fix-login-t3-verify-0d9e2f61",
        status: "done",
        intent: "Verify T2",
        result: "PASS",
        updatedAt: "2026-10-24T09:10:00Z",
        rollup: leafRollup(true),
        repos: [repo(buffBear, "buff-bear", "main", "branch")],
        runner: "ws",
        model: "qwen/qwen3.5-9b",
        wsRunner: wsRun({ modelAlias: "qwen9b", contextTokens: 65536, step: 14, maxSteps: 30, exit: "done" }),
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
        rollup: leafRollup(false),
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
      remoteEntry("linux-server", {
        workspaceId: "api-fix-1a2b3c4d",
        sessionId: "1a2b3c4d",
        status: "needsInput",
        intent: "Fix the API timeout",
      }),
      remoteEntry("linux-server", {
        workspaceId: "api-fix-t1-impl-5e6f7a8b",
        sessionId: "5e6f7a8b",
        status: "working",
        parentId: "api-fix-1a2b3c4d",
        intent: "Implement the timeout",
      }),
      remoteEntry("win-rig", {
        workspaceId: "build-rig-9c0d1e2f",
        sessionId: "9c0d1e2f",
        status: "idle",
        environmentOnline: false,
        snapshotStale: true,
        intent: "Build the Windows installer",
      }),
    ],
    settings: defaultSettings(),
    openDefaults: { fileManager: "open", editor: "code" },
    config: {
      path: "/Users/me/Library/Application Support/workspaces/config.toml",
      settings: [
        { key: "defaults.host", type: "string", value: "github.com", default: "github.com", set: false },
        { key: "defaults.owners", type: "strings", value: ["bobcob7", "acme"], default: [], set: true },
        {
          key: "hosts",
          type: "hosts",
          value: [
            { name: "github.com", kind: "auto" },
            { name: "git.example.com", kind: "forgejo" },
          ],
          default: [{ name: "github.com", kind: "auto" }],
          set: true,
        },
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
    forge: "github",
    environment: null,
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
    hosts: "hosts",
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

  // A host as ws config set hosts takes it: NAME or NAME=KIND.
  const hostEntry = (value) => {
    const [name, kind = "auto"] = value.split("=");
    return { name: name.toLowerCase(), kind };
  };

  const configValue = (key, values) => {
    switch (configKinds[key] ?? "string") {
      case "hosts":
        return values.map(hostEntry);
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

  const dayMs = 86_400_000;

  // Scales the all-time numbers to the window: 1 for today, 5 for 7 days,
  // 18 for 30 days, 40 for all time, so the screenshots differ by window.
  const statsScale = (sinceMs) => {
    if (sinceMs === null) {
      return 40;
    }
    const age = Date.now() - sinceMs;
    if (age < 2 * dayMs) {
      return 1;
    }
    return age < 14 * dayMs ? 5 : 18;
  };

  const statsModels = {
    "claude-opus-5-5": { label: "Opus 5.5", family: "opus", sessions: 3, requests: 74, inputTokens: 28000, outputTokens: 21000, thinkingTokens: 6000, cacheWriteTokens: 150000, cacheReadTokens: 1100000 },
    "claude-sonnet-5-5": { label: "Sonnet 5.5", family: "sonnet", sessions: 4, requests: 58, inputTokens: 22000, outputTokens: 19000, thinkingTokens: 5000, cacheWriteTokens: 120000, cacheReadTokens: 870000 },
    "claude-haiku-5-5": { label: "Haiku 5.5", family: "haiku", sessions: 5, requests: 96, inputTokens: 40000, outputTokens: 30000, thinkingTokens: 7000, cacheWriteTokens: 190000, cacheReadTokens: 1600000 },
  };

  // [model, language, linesAdded, linesRemoved, edits, activeMinutes, tokens, costUsd], all-time.
  const statsRows = [
    ["claude-opus-5-5", "Go", 300, 80, 16, 30, 560000, 4.6],
    ["claude-opus-5-5", "TypeScript", 200, 60, 12, 18, 420000, 3],
    ["claude-opus-5-5", "Rust", 100, 40, 8, 13, 319000, 1.8],
    ["claude-sonnet-5-5", "Go", 120, 40, 6, 14, 300000, 1.6],
    ["claude-sonnet-5-5", "TypeScript", 150, 40, 10, 20, 431000, 2.8],
    ["claude-sonnet-5-5", "Markdown", 126, 50, 7, 14, 300000, 1.7],
    ["claude-haiku-5-5", "Go", 180, 60, 14, 20, 1100000, 1.4],
    ["claude-haiku-5-5", "Python", 120, 50, 9, 13, 760000, 0.8],
  ];

  const sumOf = (list, key) => list.reduce((total, item) => total + item[key], 0);
  const ratio = (numerator, denominator) => (denominator === 0 ? null : numerator / denominator);

  const statsFor = (sinceMs) => {
    const k = statsScale(sinceMs);
    const rows = statsRows.map(([model, language, linesAdded, linesRemoved, edits, activeMinutes, tokens, costUsd]) => ({
      model,
      language,
      linesAdded: linesAdded * k,
      linesRemoved: linesRemoved * k,
      edits: edits * k,
      activeMinutes: activeMinutes * k,
      tokens: tokens * k,
      costUsd: Number((costUsd * k).toFixed(2)),
    }));
    const byModel = Object.entries(statsModels)
      .map(([model, base]) => {
        const own = rows.filter((row) => row.model === model);
        const inputTokens = base.inputTokens * k;
        const cacheReadTokens = base.cacheReadTokens * k;
        const cacheWriteTokens = base.cacheWriteTokens * k;
        return {
          model,
          label: base.label,
          family: base.family,
          priced: true,
          sessions: base.sessions,
          activeMinutes: sumOf(own, "activeMinutes"),
          linesAdded: sumOf(own, "linesAdded"),
          linesRemoved: sumOf(own, "linesRemoved"),
          requests: base.requests * k,
          inputTokens,
          outputTokens: base.outputTokens * k,
          thinkingTokens: base.thinkingTokens * k,
          cacheWriteTokens,
          cacheReadTokens,
          totalTokens: inputTokens + base.outputTokens * k + cacheWriteTokens + cacheReadTokens,
          costUsd: sumOf(own, "costUsd"),
          cacheHitRatio: ratio(cacheReadTokens, inputTokens + cacheReadTokens + cacheWriteTokens),
        };
      })
      .sort((a, b) => b.totalTokens - a.totalTokens || a.model.localeCompare(b.model));
    const countKeys = ["requests", "inputTokens", "outputTokens", "thinkingTokens", "cacheWriteTokens", "cacheReadTokens", "totalTokens", "costUsd"];
    const totals = Object.fromEntries(countKeys.map((key) => [key, sumOf(byModel, key)]));
    const cacheBase = totals.inputTokens + totals.cacheReadTokens + totals.cacheWriteTokens;
    const languages = [...new Set(rows.map((row) => row.language))];
    const languageLines = (language) => sumOf(rows.filter((row) => row.language === language), "linesAdded");
    const byLanguage = languages
      .map((language) => {
        const own = rows.filter((row) => row.language === language);
        return { language, linesAdded: sumOf(own, "linesAdded"), linesRemoved: sumOf(own, "linesRemoved"), edits: sumOf(own, "edits") };
      })
      .sort((a, b) => b.linesAdded - a.linesAdded || a.language.localeCompare(b.language));
    const productivity = rows
      .map((row) => ({
        model: row.model,
        label: statsModels[row.model].label,
        family: statsModels[row.model].family,
        language: row.language,
        linesAdded: row.linesAdded,
        linesRemoved: row.linesRemoved,
        edits: row.edits,
        activeMinutes: row.activeMinutes,
        tokens: row.tokens,
        costUsd: row.costUsd,
        shareOfLanguage: ratio(row.linesAdded, languageLines(row.language)),
        locPerMinute: ratio(row.linesAdded, row.activeMinutes),
        minutesPer100Loc: ratio(100 * row.activeMinutes, row.linesAdded),
        tokensPerLoc: ratio(row.tokens, row.linesAdded),
        costPerLoc: ratio(row.costUsd, row.linesAdded),
      }))
      .sort((a, b) => languageLines(b.language) - languageLines(a.language) || b.linesAdded - a.linesAdded || a.model.localeCompare(b.model));
    return {
      generatedAtMs: Date.now(),
      sinceMs: sinceMs ?? null,
      system: {
        cpuPercent: 12.5,
        cpuCount: 8,
        loadAverage: [1.42, 1.1, 0.97],
        memoryTotalBytes: 17_179_869_184,
        memoryUsedBytes: 10_737_418_240,
        disk: { path: "/home/me/.config/workspaces", totalBytes: 499_963_174_912, usedBytes: 201_863_462_912 },
      },
      usage: {
        totals: { ...totals, cacheHitRatio: ratio(totals.cacheReadTokens, cacheBase) },
        sessions: sumOf(byModel, "sessions"),
        activeMinutes: sumOf(rows, "activeMinutes"),
        byModel,
        byLanguage,
      },
      productivity,
      scan: { files: 412, filesRead: 3, bytesRead: 184_320 * k, durationMs: 42, cached: false },
      warnings: [],
    };
  };

  // Artifacts (ADR 0078): a Markdown pitch with two revisions and four threads, a screenshot and a build log.
  const utf8Base64 = (text) => btoa(String.fromCharCode(...new TextEncoder().encode(text)));
  const sizeOf = (dataBase64) => atob(dataBase64).length;
  const person = { kind: "person", workspace: null, name: null };
  const workspaceActor = (workspace, name) => ({ kind: "agent", workspace, name });
  const noSha = "0".repeat(64);
  const loginErrorPng =
    "iVBORw0KGgoAAAANSUhEUgAAAEAAAAAYCAIAAADF1mwTAAAARUlEQVR4nO3PUQ0AIBDD0PODAtxjim9QcSkLbSZgr9Yc0Sv8gQD6gQD6QQvgvJoAum8AQRNATwC9fMAOTwCdADoBdPGAC5ipx5L47eDaAAAAAElFTkSuQmCC";
  const pitchRev1 =
    "# Pitch: cache sharing\n\nToday the cache is per clone. Each workspace fetches its own copy.\n\n## Design\n\nWe keep one lock per repo.\n";
  const pitchRev2 =
    "# Pitch: cache sharing\n\nToday the cache is shared across workspaces. Each clone reads from it.\n\n## Design\n\nWe keep one lock per repo and share the object store.\n\n## Risks\n\nStale entries after a force push.\n";
  const buildLog = "fix-ci: run 1 failed\n  go test ./internal/artifact\n  FAIL internal/artifact 2.1s\n";

  const anchorIn = (rev, text, quote, line, prefix, suffix, state = "anchored") => {
    const start = text.indexOf(quote);
    return { rev, start, end: start + quote.length, lineStart: line, lineEnd: line, quote, prefix, suffix, state };
  };
  const comment = (id, author, body, createdAt) => ({ id, author, body, createdAt });
  const pitchAuthor = workspaceActor("pitch-ab12cd34", "pitch");

  const threadCounts = (threads) => ({
    open: threads.filter((t) => !t.resolved && t.anchor?.state !== "detached").length,
    resolved: threads.filter((t) => t.resolved).length,
    detached: threads.filter((t) => !t.resolved && t.anchor?.state === "detached").length,
  });

  const artifactStore = [
    {
      id: "art-00000001",
      name: "pitch.md",
      title: "Pitch: cache sharing",
      kind: "markdown",
      createdAt: "2026-11-23T18:00:00Z",
      updatedAt: "2026-11-23T18:40:00Z",
      createdBy: pitchAuthor,
      revs: [
        { contentType: "text/markdown; charset=utf-8", dataBase64: utf8Base64(pitchRev1), createdAt: "2026-11-23T18:00:00Z", createdBy: pitchAuthor, note: null },
        { contentType: "text/markdown; charset=utf-8", dataBase64: utf8Base64(pitchRev2), createdAt: "2026-11-23T18:30:00Z", createdBy: pitchAuthor, note: "Per user; see §2" },
      ],
      threads: [
        {
          id: "t1",
          anchor: anchorIn(2, pitchRev2, "the cache is shared", 3, "Today ", " across workspaces."),
          resolved: false,
          resolvedAt: null,
          resolvedBy: null,
          comments: [
            comment("c1", person, "Shared how?", "2026-11-23T18:20:00Z"),
            comment("c2", pitchAuthor, "Per user; see §2. Done in r2.", "2026-11-23T18:31:00Z"),
          ],
        },
        {
          id: "t2",
          anchor: anchorIn(2, pitchRev2, "share the object store", 7, "We keep one lock per repo and ", "."),
          resolved: false,
          resolvedAt: null,
          resolvedBy: null,
          comments: [comment("c3", person, "Does the lock still apply?", "2026-11-23T18:35:00Z")],
        },
        {
          id: "t3",
          anchor: anchorIn(1, pitchRev1, "fetches its own copy", 3, "Each workspace ", ".", "detached"),
          resolved: false,
          resolvedAt: null,
          resolvedBy: null,
          comments: [comment("c4", person, "Is this still true?", "2026-11-23T18:22:00Z")],
        },
        {
          id: "t4",
          anchor: anchorIn(2, pitchRev2, "Stale entries after a force push", 11, "", "."),
          resolved: true,
          resolvedAt: "2026-11-23T18:40:00Z",
          resolvedBy: person,
          comments: [comment("c5", pitchAuthor, "Covered in §4.", "2026-11-23T18:38:00Z")],
        },
      ],
    },
    {
      id: "art-00000002",
      name: "login-error.png",
      title: null,
      kind: "image",
      createdAt: "2026-11-23T17:50:00Z",
      updatedAt: "2026-11-23T17:50:00Z",
      createdBy: person,
      revs: [{ contentType: "image/png", dataBase64: loginErrorPng, createdAt: "2026-11-23T17:50:00Z", createdBy: person, note: null }],
      threads: [],
    },
    {
      id: "art-00000003",
      name: "build.log",
      title: null,
      kind: "text",
      createdAt: "2026-11-23T16:10:00Z",
      updatedAt: "2026-11-23T16:10:00Z",
      createdBy: workspaceActor("fix-ci-77aa01bc", "fix-ci"),
      revs: [{ contentType: "text/plain; charset=utf-8", dataBase64: utf8Base64(buildLog), createdAt: "2026-11-23T16:10:00Z", createdBy: workspaceActor("fix-ci-77aa01bc", "fix-ci"), note: null }],
      threads: [],
    },
  ];
  let nextArtifact = 4;
  let nextThread = 5;
  let nextComment = 6;

  const findArtifact = (id) => {
    const found = artifactStore.find((a) => a.id === id);
    if (found === undefined) {
      throw { code: "artifact_not_found", message: `no artifact ${id}` };
    }
    return found;
  };
  const latestRev = (a) => a.revs.length;
  const revisionOf = (a, rev) => {
    const found = a.revs[(rev ?? latestRev(a)) - 1];
    if (found === undefined) {
      throw { code: "artifact_not_found", message: `${a.id} has no revision ${rev}` };
    }
    return found;
  };
  const summaryOf = (a) => {
    const latest = revisionOf(a, null);
    return {
      id: a.id,
      reference: `@artifact:${a.id}`,
      name: a.name,
      title: a.title,
      kind: a.kind,
      contentType: latest.contentType,
      latest: latestRev(a),
      size: sizeOf(latest.dataBase64),
      createdAt: a.createdAt,
      updatedAt: a.updatedAt,
      createdBy: a.createdBy,
      threads: threadCounts(a.threads),
    };
  };
  const detailOf = (a) => ({
    ...summaryOf(a),
    revisions: a.revs.map((r, index) => ({
      rev: index + 1,
      contentType: r.contentType,
      size: sizeOf(r.dataBase64),
      sha256: noSha,
      createdAt: r.createdAt,
      createdBy: r.createdBy,
      note: r.note,
    })),
  });
  const kindOfName = (name) => {
    const ext = name.split(".").pop().toLowerCase();
    if (["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(ext)) {
      return "image";
    }
    return ext === "md" || ext === "markdown" ? "markdown" : "text";
  };
  const contentTypeOf = (name) => {
    const ext = name.split(".").pop().toLowerCase();
    const types = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", svg: "image/svg+xml", md: "text/markdown; charset=utf-8", markdown: "text/markdown; charset=utf-8" };
    return types[ext] ?? "text/plain; charset=utf-8";
  };
  const touch = (a) => {
    a.updatedAt = new Date().toISOString();
  };

  const artifactHandlers = {
    artifact_list: () => artifactStore.map(summaryOf),
    artifact_show: ({ id }) => detailOf(findArtifact(id)),
    artifact_read: ({ id, rev }) => {
      const found = revisionOf(findArtifact(id), rev);
      return { rev: artifactStore.find((a) => a.id === id).revs.indexOf(found) + 1, contentType: found.contentType, size: sizeOf(found.dataBase64), dataBase64: found.dataBase64 };
    },
    artifact_add: ({ name, title, dataBase64 }) => {
      const now = new Date().toISOString();
      const a = {
        id: `art-${String(nextArtifact++).padStart(8, "0")}`,
        name,
        title: title ?? null,
        kind: kindOfName(name),
        createdAt: now,
        updatedAt: now,
        createdBy: person,
        revs: [{ contentType: contentTypeOf(name), dataBase64, createdAt: now, createdBy: person, note: null }],
        threads: [],
      };
      artifactStore.push(a);
      return detailOf(a);
    },
    artifact_remove: ({ id }) => {
      artifactStore.splice(artifactStore.indexOf(findArtifact(id)), 1);
      return null;
    },
    artifact_comments: ({ id }) => {
      const a = findArtifact(id);
      return { artifactId: a.id, latest: latestRev(a), threads: a.threads };
    },
    artifact_comment_add: ({ id, body, reply, anchor }) => {
      const a = findArtifact(id);
      const now = new Date().toISOString();
      const author = person;
      const entry = comment(`c${nextComment++}`, author, body, now);
      touch(a);
      if (reply !== null && reply !== undefined) {
        const thread = a.threads.find((t) => t.id === reply);
        thread.comments.push(entry);
        return { thread, comment: entry };
      }
      const latest = revisionOf(a, null);
      const thread = {
        id: `t${nextThread++}`,
        anchor:
          anchor === null || anchor === undefined
            ? null
            : anchorIn(latestRev(a), atob(latest.dataBase64), anchor.quote, anchor.line, anchor.prefix, anchor.suffix),
        resolved: false,
        resolvedAt: null,
        resolvedBy: null,
        comments: [entry],
      };
      a.threads.push(thread);
      return { thread, comment: entry };
    },
    artifact_comment_resolve: ({ id, thread, reopen }) => {
      const a = findArtifact(id);
      const found = a.threads.find((t) => t.id === thread);
      touch(a);
      found.resolved = !reopen;
      found.resolvedAt = reopen ? null : new Date().toISOString();
      found.resolvedBy = reopen ? null : person;
      return found;
    },
  };
  Object.assign(mock.handlers, artifactHandlers);

  Object.assign(mock.handlers, {
    list_entries: () => ({ entries: data.entries, warnings: [] }),
    runner_settings_get: () => data.runnerSettings,
    daemon_status: () => ({
      running: true,
      pid: 40211,
      origin: "app",
      started_at: "2026-11-14T08:00:03Z",
      ws_version: "v0.31.0",
      socket: "/run/ws.sock",
      config_dir: "/c",
      runners: [{ workspace: "fix-login-t2-impl-41aa0b7c", session: "rn-5d2e8b10", pid: 51234, state: "working", restarts: 0, adopted: false }],
      warnings: [],
    }),
    daemon_restart: () => ({ stopped: true, pid: 40211, runners_stopped: [], runners_kept: ["rn-5d2e8b10"], warnings: [] }),
    runner_check: ({ endpoint }) => ({
      endpoint,
      url: "http://gpu-a.inference.svc:1234",
      api: "openai",
      probe: "none",
      model: "qwen/qwen3.5-9b",
      version: null,
      capabilities: null,
      context_tokens: 65536,
      models: ["qwen/qwen3.5-9b", "qwen/qwen3.5-27b"],
      token_source: null,
      warnings: [],
    }),
    endpoint_token_set: () => null,
    runner_stop: ({ id }) => ({ id, session: "rn-5d2e8b10", stopped: true, warnings: [] }),
    create_agent: () => ({ id: "new-agent-1a2b3c4d", session: "1a2b3c4d", path: "/w/new-agent-1a2b3c4d" }),

    stats_get: ({ sinceMs }) => statsFor(sinceMs),
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
    reviews_set_hosts: ({ hosts: list }) => {
      for (const { name, kind } of list) {
        hosts[name] ??= { forge: null, choice: "auto", detected: false, login: null, token: null, tokenEnv: null, error: null };
        hosts[name].choice = kind;
        if (kind !== "auto") {
          hosts[name].forge = kind;
        }
      }
      const current = data.config.settings.find((setting) => setting.key === "hosts");
      replaceSetting({ ...current, value: list, set: true });
      return reviewsView();
    },
    reviews_set_token: ({ host, token }) => {
      hosts[host].token = token === "" ? null : "file";
      return reviewsView();
    },
    reviews_dismiss: reviewsView,
    reviews_restore: reviewsView,
    reviews_plan: () => reviewPlan(),
    reviews_profiles: () => [
      profile({ name: "efficiency", description: "Efficiency: wasted work and allocations", when: "always" }),
      profile({ name: "go", description: "Go idioms, concurrency, and error handling", when: "auto", patterns: ["**/*.go"] }),
      profile({ name: "security", description: "Security: injection, secrets, and authorization", when: "always" }),
    ],
    review_report: ({ environment, reviewId }) =>
      environment === "linux-server"
        ? {
            repo: edge,
            ref: "9b1d2c3e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c",
            review: { requester: "none", pr: { number: 7, url: `https://${edge}/pull/7` } },
            agent: { workspace: reviewId ?? edgeReviewId, status: "done" },
            profiles: [
              { name: "go", model: "sonnet-5-5", verdict: "request_changes", severity: { max: 7, must_fix: 0, should_fix: 1, worth_fixing: 0, optional: 0 }, missing: false },
              { name: "security", model: "sonnet-5-5", verdict: "comment", severity: { max: 5, must_fix: 0, should_fix: 0, worth_fixing: 1, optional: 0 }, missing: false },
            ],
            comments: [
              {
                id: "go:1", profile: "go", priority: 7, category: "correctness",
                body: "The cache is read without the lock that guards writes, so a concurrent refresh can return a half-built config.",
                code: null,
                anchors: [{ file: "internal/edge/cache.go", line: 48, end_line: 61 }],
                suggestions: [{ summary: "Read through the same RWMutex the writer takes", effort: "small", scope: "local", pros: ["No new state"], cons: [], code: null }],
                decision: null,
              },
              {
                id: "security:1", profile: "security", priority: 5, category: "secrets",
                body: "The cached config includes the upstream token and is written world-readable, so any local user can read it. The modes the store sets on the directory and the file are",
                code: null,
                anchors: [{ file: "internal/edge/store.go", line: 22, end_line: null }],
                suggestions: [],
                decision: null,
                truncated: true,
              },
            ],
            superseded: [],
            warnings: [],
            environment,
            reviewId: reviewId ?? edgeReviewId,
            redacted: 1,
          }
        : {
            repo: environment ? edge : buffBear,
            ref: head,
            review: null,
            agent: null,
            profiles: [],
            comments: [],
            superseded: [],
            warnings: [],
            ...(environment ? { environment, reviewId, redacted: 0 } : {}),
          },
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
    environments_list: () => ({ environments: data.environments, warnings: [] }),
    environment_add: ({ name }) => {
      if (data.environments.some((env) => env.name === name)) {
        throw { code: "environment_exists", message: `an environment named ${name} already exists` };
      }
      const added = { id: `env_${name}`, name, state: "pending", status: "pending", lastSeen: null, createdAt: "2026-11-23T08:00:00Z", enrolledAt: null, certNotAfter: null, certCa: null, hello: null, workspaces: null };
      data.environments = [...data.environments, added];
      return { id: added.id, name, state: "pending", code: "ENRL-7KQ2-M9XD-4TPL", caCreated: false, revokedSerials: [], warnings: [] };
    },
    environment_rotate: ({ name }) => {
      const env = data.environments.find((candidate) => candidate.name === name);
      if (env === undefined) {
        throw { code: "environment_missing", message: `no environment named ${name}` };
      }
      return { id: env.id, name, state: "pending", code: "ENRL-2HPA-8VLZ-6WQN", caCreated: false, revokedSerials: [], warnings: [] };
    },
    environment_remove: ({ name }) => {
      const env = data.environments.find((candidate) => candidate.name === name);
      if (env === undefined) {
        throw { code: "environment_missing", message: `no environment named ${name}` };
      }
      data.environments = data.environments.filter((candidate) => candidate !== env);
      return { id: env.id, name, revokedSerials: ["3f9a21c0"], warnings: [] };
    },
    environments_ca_status: () => data.environmentCa,
    environments_ca_rotate: ({ finish }) => {
      const { current, old } = data.environmentCa;
      if (finish) {
        data.environmentCa = { ...data.environmentCa, old: null, environmentsOnOld: 0 };
        return { current, old: null, dropped: old, stranded: [], warnings: [] };
      }
      const next = { fingerprint: "sha256:9b72e41c", notAfter: "2037-11-23T00:00:00Z" };
      data.environmentCa = { current: next, old: current, environmentsOnCurrent: 0, environmentsOnOld: data.environments.length, warnings: [] };
      return { current: next, old: current, dropped: null, stranded: [], warnings: [] };
    },
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
    loops_list: ({ environment } = {}) => {
      if (environment === "linux-server" || environment === "win-rig") {
        return remoteLoops(environment);
      }
      return { loops: data.loops.map((loop) => ({ runner: "claude", endpoint: null, ...loop })), warnings: [] };
    },
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
    read_transcript: ({ offset, sessionId }) => ({
      items: offset > 0 ? [] : sessionId?.startsWith("rn-") ? [
        { id: "r1", at: "2026-10-24T09:30:00Z", kind: "user", text: "Implement T2: throttle repeated failed logins per account." },
        { id: "r2", at: "2026-10-24T09:30:20Z", kind: "assistant", text: "I'll start by reading the limiter in internal/auth." },
        { id: "r3", at: "2026-10-24T09:30:30Z", kind: "toolUse", toolUseId: "t1", name: "read_file", summary: "internal/auth/limiter.go", input: "{\"path\":\"internal/auth/limiter.go\"}" },
        { id: "r4", at: "2026-10-24T09:30:31Z", kind: "toolResult", toolUseId: "t1", text: "package auth\n\n// Limiter is a token bucket per IP.", isError: false },
        { id: "r5", at: "2026-10-24T09:31:10Z", kind: "toolUse", toolUseId: "t2", name: "run", summary: "make test", input: "{\"cmd\":\"make test\"}" },
        { id: "r6", at: "2026-10-24T09:31:40Z", kind: "toolResult", toolUseId: "t2", text: "FAIL internal/auth TestThrottle", isError: true },
      ] : [
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
