import { isWeb } from "./platform";
import { transport } from "./transport";

export type Status =
  | "working"
  | "needsInput"
  | "idle"
  | "done"
  | "starting"
  | "stopped"
  | "running"
  | "pending"
  | "unknown";

export type EntryKind = "agent" | "shell" | "pending" | "unknown";

export interface Repo {
  readonly id: string;
  readonly path: string;
  readonly ref: string | null;
  readonly refKind: string | null;
}

export interface Link {
  readonly href: string;
  readonly kind: string;
}

/** A workspace, or a background session outside any workspace. */
export interface Entry {
  readonly key: string;
  readonly kind: EntryKind;
  readonly name: string;
  readonly workspaceId: string | null;
  readonly sessionId: string | null;
  readonly path: string;
  readonly status: Status;
  readonly stale: boolean | null;
  readonly createdAt: string | null;
  readonly updatedAt: string | null;
  readonly detail: string | null;
  readonly intent: string | null;
  readonly result: string | null;
  readonly links: readonly Link[];
  readonly repos: readonly Repo[];
  /** What a workspace `ws review start` made reviews (ADR 0039). */
  readonly review: ReviewRecord | null;
  /** Left out of the Agents list unless Show hidden is on, and not counted as waiting (ADR 0043, 0058).
   * Never true for a raised loop run (ADR 0057). */
  readonly hidden: boolean;
  /** The workspace this one was started from, and is removed with (ADR 0044). */
  readonly parentId: string | null;
  /** Set for a loop run's workspace (ADR 0057). */
  readonly loopRun: EntryLoopRun | null;
}

/** What a loop run asks of a person (ADR 0057). */
export type LoopAttention = "blocked" | "needs_attention";

/** A workspace that is a run of a loop, from ws list's loop_run. */
export interface EntryLoopRun {
  readonly loop: string;
  readonly attention: LoopAttention | null;
}

export interface ReviewRecord {
  readonly repo: string;
  /** The full commit reviewed. */
  readonly ref: string;
  readonly requester: string;
  readonly profiles: readonly { readonly name: string; readonly model: string; readonly auto: boolean }[];
  readonly pr: { readonly number: number; readonly url: string } | null;
}

export interface Listing {
  readonly entries: readonly Entry[];
  readonly warnings: readonly string[];
}

export interface AppError {
  readonly code: string;
  readonly message: string;
  readonly details?: Readonly<Record<string, unknown>>;
}

export interface NewAgent {
  readonly name: string;
  readonly specs: readonly string[];
  readonly prompt: string;
  readonly model: string;
  readonly permissionMode: string;
  readonly offline: boolean;
}

export interface Created {
  readonly id: string;
  readonly session: string;
  readonly path: string;
}

/** A workspace's new display name, from ws rename (ADR 0036). */
export interface Renamed {
  readonly id: string;
  readonly name: string;
}

/** Whether a workspace is hidden, from ws hide or ws unhide (ADR 0043). */
export interface HiddenSet {
  readonly id: string;
  readonly hidden: boolean;
}

/** A program the app runs, as Settings > About shows it. */
export interface ToolVersion {
  readonly version: string | null;
  /** Where it is on the app's PATH, if it is. */
  readonly path: string | null;
  /** Why the version couldn't be read. */
  readonly error: string | null;
}

/** The app's version, and the ws and claude it runs. */
export interface Versions {
  readonly app: {
    readonly version: string;
    /** `git describe` of the commit the app was built from, if known. */
    readonly commit: string | null;
  };
  readonly ws: ToolVersion;
  readonly claude: ToolVersion;
}

export type TermSpec =
  | { readonly kind: "attach"; readonly sessionId: string; readonly cwd: string }
  | { readonly kind: "shell"; readonly name: string; readonly specs: readonly string[] };

export type TermEvent =
  | { readonly event: "data"; readonly data: string }
  | { readonly event: "exit"; readonly code: number | null };

interface ItemBase {
  readonly id: string;
  readonly at: string | null;
}

/** One thing shown in an agent's conversation, read from its transcript. */
export type ConversationItem =
  | (ItemBase & { readonly kind: "user" | "assistant" | "thinking" | "notice"; readonly text: string })
  | (ItemBase & {
      readonly kind: "toolUse";
      readonly toolUseId: string;
      readonly name: string;
      readonly summary: string;
      readonly input: string;
    })
  | (ItemBase & { readonly kind: "toolResult"; readonly toolUseId: string; readonly text: string; readonly isError: boolean });

/** The transcript lines written since an offset. */
export interface TranscriptChunk {
  readonly items: readonly ConversationItem[];
  readonly next: number;
  readonly reset: boolean;
  readonly dropped: number;
  readonly found: boolean;
}

/** A cached repo, from ws cache list. */
export interface CachedRepo {
  readonly id: string;
  readonly path: string;
  readonly upstream: string;
  readonly defaultBranch: string;
  readonly lastFetchedAt: string | null;
  readonly sizeBytes: number;
  readonly branches: number;
  readonly tags: number;
}

export interface CacheListing {
  readonly repos: readonly CachedRepo[];
  readonly warnings: readonly string[];
}

export type RefKind = "branch" | "tag";

/** A branch or tag and the commit it points at, from ws cache refs (ADR 0033). */
export interface RepoRef {
  readonly name: string;
  readonly kind: RefKind;
  readonly commit: string;
  readonly subject: string;
  readonly author: string;
  readonly date: string | null;
}

export interface RefListing {
  readonly id: string;
  readonly defaultBranch: string;
  readonly refs: readonly RepoRef[];
}

/** How ws cache resolve found a repo (ADR 0007). */
export type ResolveSource = "spec" | "cache" | "default" | "probe";

export interface Resolution {
  readonly id: string;
  readonly ref: string | null;
  readonly upstream: string;
  readonly cached: boolean;
  readonly source: ResolveSource;
}

/** One repo's outcome in ws cache update. */
export interface RepoResult {
  readonly id: string;
  readonly error: AppError | null;
}

export interface AddedRepo {
  readonly id: string;
  readonly path: string;
  readonly upstream: string;
  readonly added: boolean;
}

/** A cached repo that matches a search, from ws cache search. */
export interface SearchMatch {
  readonly id: string;
  readonly ref: string | null;
}

export type ReviewGroup = "toReview" | "reviewing" | "reviewed" | "dismissed";

/** A review workspace of a pull request (ADR 0032). */
export interface ReviewRun {
  readonly workspaceId: string;
  readonly sessionId: string | null;
  /** The commit it checked out. */
  readonly sha: string;
  readonly status: Status;
  readonly createdAt: string | null;
  readonly detail: string | null;
  readonly result: string | null;
  readonly links: readonly Link[];
  readonly auto: boolean;
  /** Made by `ws review start`, so it has a report (ADR 0039). */
  readonly recorded: boolean;
  /** The workspace its record names as having asked for it. */
  readonly requester: string | null;
  /** ws removed its agent and workspace once it finished (ADR 0045). */
  readonly removed: boolean;
}

export interface NewCommits {
  readonly count: number;
  readonly subjects: readonly string[];
}

/** An open pull request in a watched repo, with its reviews. */
export interface PullRequest {
  readonly key: string;
  readonly repo: string;
  readonly number: number;
  readonly title: string;
  readonly author: { readonly login: string };
  readonly url: string;
  readonly body: string;
  readonly isDraft: boolean;
  readonly headRefOid: string;
  readonly headRefName: string;
  readonly baseRefName: string;
  readonly additions: number;
  readonly deletions: number;
  readonly changedFiles: number;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly group: ReviewGroup;
  readonly outOfDate: boolean;
  readonly requested: boolean;
  readonly mine: boolean;
  /** Newest first. */
  readonly reviews: readonly ReviewRun[];
  readonly newCommits: NewCommits | null;
}

/** A cached repo, and whether its pull requests are watched. */
export interface WatchRepo {
  readonly id: string;
  readonly github: boolean;
  readonly watch: boolean;
  readonly autoReview: boolean;
  readonly open: number | null;
  readonly checkedAtMs: number | null;
  readonly error: string | null;
}

export interface ReviewsView {
  readonly checking: boolean;
  readonly checkedAtMs: number | null;
  readonly error: AppError | null;
  readonly repos: readonly WatchRepo[];
  readonly prs: readonly PullRequest[];
  readonly warnings: readonly string[];
}

/** A profile a review starts with: `--profile NAME[:MODEL]`, or `--auto` when auto. */
export interface ProfileChoice {
  readonly name: string;
  readonly model: string;
  readonly auto: boolean;
}

/**
 * What starting a review runs: `ws review start --pr <number> <repo>
 * [--profile …]… -- [--model=…] [--permission-mode=…] [focus]`.
 */
export interface ReviewPlan {
  readonly name: string;
  readonly repo: string;
  readonly number: number;
  readonly head: string;
  /** Empty leaves the choice to ws and config.toml. */
  readonly profiles: readonly ProfileChoice[];
  readonly model: string;
  readonly permissionMode: string;
  readonly focus: string;
}

/** When a reviewer profile joins a review: ws's `when`. */
export type Runs = "always" | "auto" | "off";

/** A profile as `ws review profiles` lists it, with config.toml applied. */
export interface ListedProfile {
  readonly name: string;
  readonly description: string;
  readonly when: Runs;
  /** File patterns an auto profile matches. */
  readonly patterns: readonly string[];
  /** "" for the review agent's. */
  readonly model: string;
  readonly builtin: boolean;
  readonly source: ProfileSource;
}

/** Built into ws, a user's file, or a user's file replacing a built-in (ADR 0042). */
export type ProfileSource = "builtin" | "user" | "override";

/** A profile with its guide, from `ws review profiles show`, `add`, or `edit`. */
export interface ProfileDetail extends ListedProfile {
  /** The reviewer's instructions, in Markdown. */
  readonly guide: string;
  /** The user's file; null for a built-in. */
  readonly path: string | null;
}

/** The fields the editor saves; a field left out isn't changed. */
export interface ProfileDraft {
  readonly description?: string;
  readonly when?: Runs;
  /** `[]` removes every pattern. */
  readonly patterns?: readonly string[];
  readonly guide?: string;
}

/** What `ws review profiles rm` did, and the settings it left. */
export interface ProfileRemoved {
  /** The removed file was an edited built-in, which is back. */
  readonly restored: boolean;
  readonly settings: Settings;
}

/** Where an agent role comes from: built into `ws`, a user's file, or a user's file replacing a built-in (ADR 0050). */
export type RoleSource = "builtin" | "user" | "override";

/** A role as `ws machine roles` lists it. */
export interface ListedRole {
  readonly name: string;
  readonly description: string;
  /** "" for claude's default. */
  readonly model: string;
  readonly pass: readonly string[];
  readonly fail: readonly string[];
  readonly vars: readonly string[];
  readonly claudeArgs: readonly string[];
  readonly source: RoleSource;
}

/** A role with its prompt template, from `ws machine roles show`, `add`, or `edit`. */
export interface RoleDetail extends ListedRole {
  readonly prompt: string;
  /** The user's file; null for a built-in. */
  readonly path: string | null;
}

/** What the role editor saves; every field is sent. */
export interface RoleDraft {
  readonly description: string;
  readonly model: string;
  readonly pass: readonly string[];
  readonly fail: readonly string[];
  readonly vars: readonly string[];
  readonly claudeArgs: readonly string[];
  readonly prompt: string;
}

/** What `ws machine roles rm` did. `restored` is true when a built-in is back. */
export interface RoleRemoved {
  readonly name: string;
  readonly path: string;
  readonly restored: boolean;
}

/** A Reviewer profiles row changed from what ws lists. */
export interface ProfileSetting {
  readonly runs: Runs;
  readonly model: string;
}

export interface Started {
  readonly id: string;
  /** The review was already running under this name. */
  readonly already: boolean;
}

export interface ReviewSettings {
  readonly checkIntervalMinutes: number;
  readonly skipDrafts: boolean;
  readonly skipMine: boolean;
  readonly onlyRequested: boolean;
  readonly maxAutoReviews: number;
  /** The review agent's model and permission mode. */
  readonly model: string;
  readonly permissionMode: string;
  /** Changed Reviewer profiles rows, by profile name; empty for ws's defaults. */
  readonly profiles: Readonly<Record<string, ProfileSetting>>;
}

/** A finding's decision, from `ws review decide` (ADR 0040). */
export type DecisionKind = "comment" | "fix" | "skip";

/** What was done about a decided finding, from `ws review acted`. */
export interface DecisionDone {
  readonly commented?: string;
  readonly fix_sent?: string;
  readonly at: string;
}

export interface FindingDecision {
  readonly decision: DecisionKind;
  /** An index into the finding's suggestions, or null for the fixer's choice. */
  readonly suggestion: number | null;
  readonly note: string;
  readonly at: string;
  readonly done: DecisionDone | null;
}

/** A file and lines at the reviewed commit; line 0 is the whole file. */
export interface ReportAnchor {
  readonly file: string;
  readonly line: number;
  readonly end_line: number | null;
}

export interface ReportSuggestion {
  readonly summary: string;
  readonly effort: string;
  readonly scope: string;
  readonly pros: readonly string[];
  readonly cons: readonly string[];
  readonly code: string | null;
}

/** One finding: `<profile>:<id>`, or `#<n>:<id>` when superseded. */
export interface ReportComment {
  readonly id: string;
  readonly profile: string;
  readonly priority: number;
  readonly category: string;
  readonly body: string;
  readonly code: string | null;
  readonly anchors: readonly ReportAnchor[];
  readonly suggestions: readonly ReportSuggestion[];
  readonly decision: FindingDecision | null;
}

export type Verdict = "approve" | "request_changes" | "comment";

export interface ReportProfile {
  readonly name: string;
  readonly model: string;
  readonly verdict: Verdict | null;
  readonly severity: {
    readonly max: number | null;
    readonly must_fix: number;
    readonly should_fix: number;
    readonly worth_fixing: number;
    readonly optional: number;
  } | null;
  /** No current submission. */
  readonly missing: boolean;
}

/** `ws review report --json`, as ws prints it (ADR 0039). */
export interface ReviewReport {
  readonly repo: string;
  readonly ref: string;
  readonly review: { readonly requester: string; readonly pr: { readonly number: number; readonly url: string } | null } | null;
  readonly agent: { readonly workspace: string; readonly status: string } | null;
  readonly profiles: readonly ReportProfile[];
  readonly comments: readonly ReportComment[];
  readonly superseded: readonly ReportComment[];
  readonly warnings: readonly string[];
}

/** One click's decision on one or more findings. */
export interface DecideRequest {
  readonly findings: readonly string[];
  readonly decision: DecisionKind | "clear";
  readonly suggestion: number | null;
  readonly note: string;
}

/** Where a posted finding lands: on lines of the diff, or on the whole file. */
export type CommentKind = "line" | "file";

/** One comment of the review to post, as the preview shows and edits it (ADR 0041). */
export interface DraftComment {
  readonly finding: string;
  readonly kind: CommentKind;
  readonly path: string;
  readonly line: number | null;
  readonly startLine: number | null;
  readonly body: string;
}

/** What Comment would post, read before anything is posted. */
export interface CommentPlan {
  readonly number: number;
  /** The reviewed commit, which the comments are pinned to. */
  readonly commitId: string;
  /** The pull request's head as last checked. */
  readonly head: string;
  readonly outdated: boolean;
  /** Who posts, as gh is signed in; null when the app hasn't checked. */
  readonly login: string | null;
  /** Your unsubmitted review on the pull request, which blocks another. */
  readonly pendingReview: string | null;
  readonly prUrl: string;
  readonly body: string;
  readonly comments: readonly DraftComment[];
  /** Every finding posted, wherever it lands. */
  readonly findings: readonly string[];
  /** How many are only in the summary. */
  readonly inBody: number;
}

export type ReviewEvent = "COMMENT" | "REQUEST_CHANGES" | "APPROVE";

/** The review as confirmed in the preview. */
export interface PostRequest {
  readonly number: number;
  readonly commitId: string;
  readonly body: string;
  readonly event: ReviewEvent;
  readonly comments: readonly DraftComment[];
  readonly findings: readonly string[];
}

export interface Posted {
  readonly url: string;
  readonly report: ReviewReport | null;
  /** Set when the review was posted but ws review acted failed. */
  readonly warning: string | null;
}

/** A finding to fix, with the title the report shows. */
export interface FixFinding {
  readonly id: string;
  readonly title: string;
}

export type FixTarget = "requester" | "agent";

/** The agent that asked for the review, and whether it can take the fixes. */
export interface FixRequester {
  readonly workspaceId: string;
  readonly sessionId: string | null;
  readonly path: string;
  readonly status: Status | null;
  readonly available: boolean;
  /** Why it can't: "removed", "stopped: resume it first". */
  readonly reason: string | null;
}

/** The fix agent ws claude would start. */
export interface FixAgentPlan {
  readonly name: string;
  readonly spec: string;
  /** The branch a fork pull request's fix is pushed to instead. */
  readonly branch: string | null;
  /** The command, without the message. */
  readonly command: string;
}

export interface FixPlan {
  readonly findings: readonly string[];
  readonly requester: FixRequester | null;
  /** The requester when it can take the fixes, else a fix agent. */
  readonly target: FixTarget;
  readonly agent: FixAgentPlan;
  readonly requesterMessage: string;
  readonly agentMessage: string;
}

export interface StartFix {
  readonly findings: readonly string[];
  readonly agent: FixAgentPlan;
  readonly message: string;
}

export interface FixStarted {
  readonly agent: string;
  readonly session: string;
  readonly report: ReviewReport | null;
  readonly warning: string | null;
}

/** Which native notifications the app sends (ADR 0050). */
export interface NotificationSettings {
  readonly enabled: boolean;
  readonly needsInput: boolean;
  readonly done: boolean;
}

/** Which program opens a workspace (ADR 0051). */
export type OpenWith = "fileManager" | "editor";

/** The commands that open a workspace; "" uses the platform default. */
export interface OpenSettings {
  readonly fileManager: string;
  readonly editor: string;
}

/** The directory opened and the command run. */
export interface Opened {
  readonly dir: string;
  readonly command: readonly string[];
}

/** A pull request Q&A agent, started or found (ADR 0051). */
export interface AskStarted {
  readonly id: string;
  readonly session: string | null;
  readonly path: string;
  readonly already: boolean;
}

export interface Settings {
  readonly reviews: ReviewSettings;
  readonly notifications: NotificationSettings;
  readonly open: OpenSettings;
}

export type ConfigType = "string" | "strings" | "duration" | "bool";
export type ConfigValue = string | boolean | readonly string[];

/** One key of config.toml, with its effective value and default (ws config). */
export interface ConfigSetting {
  readonly key: string;
  readonly type: ConfigType;
  readonly value: ConfigValue;
  readonly default: ConfigValue;
  readonly set: boolean;
}

export interface ConfigListing {
  readonly path: string;
  readonly settings: readonly ConfigSetting[];
}

export interface ConfigChange {
  readonly path: string;
  readonly setting: ConfigSetting;
  readonly changed: boolean;
}

/** Workspace Online's WS_ONLINE_* settings; never the password itself. */
export interface OnlineSettings {
  readonly username: string;
  readonly addr: string;
  readonly publicUrl: string | null;
  readonly secureCookie: boolean;
  readonly sessionDays: number;
  readonly trustProxy: boolean;
  readonly dataDir: string;
  readonly webDir: string;
  readonly vapidSubject: string;
  readonly password: "plain" | "hash";
}

export interface Environment {
  /** desktop.json's path; null when the app keeps settings in memory only. */
  readonly stateFile: string | null;
  /** WS_CONFIG_DIR, or null when unset or empty. */
  readonly wsConfigDir: string | null;
  /** CLAUDE_CONFIG_DIR, or null when unset or empty. */
  readonly claudeConfigDir: string | null;
  /** The PATH the app runs ws and claude with. */
  readonly path: string;
  /** Set only by ws-online. */
  readonly online: OnlineSettings | null;
}

export interface LoopError {
  readonly code: string;
  readonly message: string;
}

/** A loop's run: the workspace and session it started, or why it couldn't. */
export interface LoopRun {
  readonly startedAt: string;
  readonly workspace: string | null;
  readonly session: string | null;
  readonly error: LoopError | null;
}

/** An agent loop, from `ws loop list --json` (ADR 0048). */
export interface Loop {
  readonly name: string;
  /** The display name; "" when the loop has none. */
  readonly title: string;
  readonly every: string;
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly model: string | null;
  readonly permissionMode: string | null;
  readonly keep: number;
  readonly paused: boolean;
  readonly createdAt: string;
  readonly nextRunAt: string | null;
  readonly lastRun: LoopRun | null;
  readonly script: string | null;
  readonly scriptTimeout: string | null;
  readonly vars: Readonly<Record<string, string>>;
  readonly lastGate: LoopGate | null;
  /** Whether runs start hidden (ADR 0057). */
  readonly hidden: boolean;
}

/** A loop's last gate check, from `ws loop list --json` (ADR 0049). */
export interface LoopGate {
  readonly at: string;
  readonly outcome: "passed" | "skipped" | "timed_out" | "failed";
  readonly exitCode: number | null;
  readonly message: string | null;
}

export interface LoopVar {
  readonly key: string;
  readonly value: string;
}

export interface LoopListing {
  readonly loops: readonly Loop[];
  readonly warnings: readonly string[];
}

export interface NewLoop {
  readonly name: string;
  readonly title?: string;
  readonly every: string;
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly keep?: number;
  readonly model?: string;
  readonly permissionMode?: string;
  readonly script?: string;
  readonly scriptTimeout?: string;
  readonly vars?: readonly LoopVar[];
  /** Left out to keep ws's default or the loop's setting (ADR 0057). */
  readonly hidden?: boolean;
}

/** The workspace a loop run started. */
export interface LoopStarted {
  readonly name: string;
  readonly workspace: string;
  readonly session: string;
  readonly path: string;
}

export type LoopRunStatus = "running" | "done" | "succeeded" | "failed" | "start_failed" | "skipped" | "gone" | "blocked" | "needs_attention";

/** A run's verdict, from its report (ADR 0056). */
export interface LoopRunVerdict {
  readonly verdict: string;
  readonly outcome: "pass" | "fail" | "blocked";
  readonly recordedAt: string;
  readonly report: string | null;
}

/** One run in a loop's history, from `ws loop history --json`. */
export interface LoopHistoryRun {
  readonly seq: number;
  readonly status: LoopRunStatus;
  readonly trigger: string;
  readonly startedAt: string;
  readonly finishedAt: string | null;
  readonly durationSeconds: number | null;
  readonly workspace: string | null;
  readonly workspaceExists: boolean;
  readonly session: string | null;
  readonly error: LoopError | null;
  readonly gate: LoopGate | null;
  readonly repeats: number;
  readonly verdict: LoopRunVerdict | null;
}

export interface LoopHistory {
  readonly loop: string;
  readonly runs: readonly LoopHistoryRun[];
  readonly total: number;
  readonly warnings: readonly string[];
}

export interface LoopRunDetail {
  readonly loop: string;
  readonly run: LoopHistoryRun;
  readonly reportText: string | null;
  readonly warnings: readonly string[];
}

/** One key of a loop's memory (ADR 0056). */
export interface LoopMemoryEntry {
  readonly key: string;
  readonly value: string;
  readonly rev: number;
  readonly updatedAt: string;
  readonly updatedBy: string | null;
}

export interface LoopMemory {
  readonly loop: string;
  readonly entries: readonly LoopMemoryEntry[];
  readonly bytes: number;
  readonly warnings: readonly string[];
}

export interface LoopMemorySet {
  readonly loop: string;
  readonly entry: LoopMemoryEntry;
  readonly warnings: readonly string[];
}

export interface LoopMemoryRemoved {
  readonly loop: string;
  readonly key: string;
  readonly removed: boolean;
  readonly warnings: readonly string[];
}

export interface LoopCleared {
  readonly loop: string;
  readonly removed: number;
  readonly warnings: readonly string[];
}

export interface DiskStats {
  readonly path: string;
  readonly totalBytes: number;
  readonly usedBytes: number;
}

export interface HostStats {
  /** Busy % since the previous stats_get; null on the first call. */
  readonly cpuPercent: number | null;
  readonly cpuCount: number | null;
  readonly loadAverage: readonly [number, number, number] | null;
  readonly memoryTotalBytes: number | null;
  readonly memoryUsedBytes: number | null;
  /** Filesystem of $WS_CONFIG_DIR, else $HOME. */
  readonly disk: DiskStats | null;
}

export interface TokenCounts {
  readonly requests: number;
  /** Uncached input. */
  readonly inputTokens: number;
  readonly outputTokens: number;
  /** Part of outputTokens. */
  readonly thinkingTokens: number;
  /** 5m + 1h. */
  readonly cacheWriteTokens: number;
  readonly cacheReadTokens: number;
  /** input + output + cacheWrite + cacheRead. */
  readonly totalTokens: number;
  /** Estimate at API rates. */
  readonly costUsd: number;
  /** cacheRead / (input + cacheRead + cacheWrite). */
  readonly cacheHitRatio: number | null;
}

export interface ModelUsage extends TokenCounts {
  /** As in the transcript, e.g. "claude-haiku-5-5". */
  readonly model: string;
  /** "Haiku 5.5"; the model id when it can't be parsed. */
  readonly label: string;
  readonly family: "opus" | "sonnet" | "haiku" | "fable" | "mythos" | "other";
  /** False: no rate known, costUsd is 0. */
  readonly priced: boolean;
  readonly sessions: number;
  readonly activeMinutes: number;
  readonly linesAdded: number;
  readonly linesRemoved: number;
}

export interface LanguageLines {
  readonly language: string;
  readonly linesAdded: number;
  readonly linesRemoved: number;
  readonly edits: number;
}

export interface Usage {
  readonly totals: TokenCounts;
  /** Transcript files with a request in the window. */
  readonly sessions: number;
  readonly activeMinutes: number;
  /** totalTokens descending, then model ascending. */
  readonly byModel: readonly ModelUsage[];
  /** linesAdded descending, then language ascending. */
  readonly byLanguage: readonly LanguageLines[];
}

export interface ProductivityRow {
  readonly model: string;
  readonly label: string;
  readonly family: ModelUsage["family"];
  readonly language: string;
  readonly linesAdded: number;
  readonly linesRemoved: number;
  readonly edits: number;
  /** Allocated by lines changed, see ADR 0059. */
  readonly activeMinutes: number;
  /** Allocated totalTokens. */
  readonly tokens: number;
  /** Allocated cost. */
  readonly costUsd: number;
  /** 0..1. */
  readonly shareOfLanguage: number | null;
  readonly locPerMinute: number | null;
  readonly minutesPer100Loc: number | null;
  readonly tokensPerLoc: number | null;
  readonly costPerLoc: number | null;
}

export interface ScanInfo {
  /** Transcript files known after the refresh. */
  readonly files: number;
  /** Files read (wholly or from an offset) by this refresh. */
  readonly filesRead: number;
  readonly bytesRead: number;
  readonly durationMs: number;
  /** True: within MIN_REFRESH of the last refresh, nothing scanned. */
  readonly cached: boolean;
}

export interface Stats {
  readonly generatedAtMs: number;
  readonly sinceMs: number | null;
  readonly system: HostStats;
  readonly usage: Usage;
  /** Language by total linesAdded desc, then linesAdded desc, then model asc. */
  readonly productivity: readonly ProductivityRow[];
  readonly scan: ScanInfo;
  readonly warnings: readonly string[];
}

const isAppError = (value: unknown): value is AppError =>
  typeof value === "object" && value !== null && "code" in value && "message" in value;

/** Normalizes whatever a failed invoke rejected with into an AppError. */
export const toAppError = (value: unknown): AppError => {
  if (isAppError(value)) {
    return value;
  }
  if (value instanceof Error) {
    return { code: "internal", message: value.message };
  }
  return { code: "internal", message: String(value) };
};

const call = async <T>(command: string, args?: Record<string, unknown>): Promise<T> => {
  try {
    return await transport().invoke<T>(command, args);
  } catch (err: unknown) {
    throw toAppError(err);
  }
};

const selectedKey = (data: unknown): string | null => {
  if (typeof data !== "object" || data === null) {
    return null;
  }
  const message = data as { readonly type?: unknown; readonly key?: unknown };
  return message.type === "select" && typeof message.key === "string" ? message.key : null;
};

/** Agent choices in web mode: a ?agent= link on load (once) and notification clicks from the service worker. */
const webTraySelect = (handler: (key: string) => void): (() => void) => {
  const url = new URL(location.href);
  const agent = url.searchParams.get("agent");
  if (agent !== null) {
    url.searchParams.delete("agent");
    history.replaceState(null, "", url);
    handler(agent);
  }
  if (navigator.serviceWorker === undefined) {
    return () => undefined;
  }
  const onMessage = (event: MessageEvent<unknown>): void => {
    const key = selectedKey(event.data);
    if (key !== null) {
      handler(key);
    }
  };
  navigator.serviceWorker.addEventListener("message", onMessage);
  return () => navigator.serviceWorker.removeEventListener("message", onMessage);
};

export const api = {
  listEntries: async (): Promise<Listing> => call<Listing>("list_entries"),
  cacheRepos: async (): Promise<readonly string[]> => call<string[]>("cache_repos"),
  cacheList: async (): Promise<CacheListing> => call<CacheListing>("cache_list"),
  cacheRefs: async (id: string): Promise<RefListing> => call<RefListing>("cache_refs", { id }),
  cacheResolve: async (spec: string): Promise<Resolution> => call<Resolution>("cache_resolve", { spec }),
  cacheFetch: async (ids: readonly string[], all: boolean): Promise<readonly RepoResult[]> =>
    call<RepoResult[]>("cache_fetch", { ids, all }),
  cacheAdd: async (spec: string): Promise<AddedRepo> => call<AddedRepo>("cache_add", { spec }),
  cacheRemove: async (id: string): Promise<void> => call<void>("cache_remove", { id }),
  cacheSearch: async (query: string, refs: boolean, limit: number): Promise<readonly SearchMatch[]> =>
    call<SearchMatch[]>("cache_search", { query, refs, limit }),
  configList: async (): Promise<ConfigListing> => call<ConfigListing>("config_list"),
  configSet: async (key: string, values: readonly string[]): Promise<ConfigChange> => call<ConfigChange>("config_set", { key, values }),
  configUnset: async (key: string): Promise<ConfigChange> => call<ConfigChange>("config_unset", { key }),
  settingsEnvironment: async (): Promise<Environment> => call<Environment>("settings_environment"),
  createAgent: async (request: NewAgent): Promise<Created> => call<Created>("create_agent", { request }),
  removeEntry: async (workspaceId: string | null, sessionId: string | null, keepChildren = false): Promise<void> =>
    call<void>("remove_entry", { workspaceId, sessionId, keepChildren }),
  renameWorkspace: async (workspaceId: string, name: string): Promise<Renamed> =>
    call<Renamed>("rename_workspace", { workspaceId, name }),
  setWorkspaceHidden: async (workspaceId: string, hidden: boolean): Promise<HiddenSet> =>
    call<HiddenSet>("set_workspace_hidden", { workspaceId, hidden }),
  stopAgent: async (sessionId: string): Promise<void> => call<void>("stop_agent", { sessionId }),
  restoreAgents: async (): Promise<readonly string[]> => call<string[]>("restore_agents"),
  openExternal: async (target: string): Promise<void> => {
    if (!isWeb()) {
      return call<void>("open_external", { target });
    }
    if (/^https?:\/\//.test(target)) {
      window.open(target, "_blank", "noopener");
    }
  },
  readTranscript: async (sessionId: string, offset: number): Promise<TranscriptChunk> =>
    call<TranscriptChunk>("read_transcript", { sessionId, offset }),
  terminalOpen: async (
    spec: TermSpec,
    cols: number,
    rows: number,
    onEvent: (event: TermEvent) => void,
  ): Promise<number> => {
    const channel = transport().channel<TermEvent>(onEvent);
    return call<number>("terminal_open", { spec, cols, rows, onEvent: channel.arg });
  },
  terminalWrite: async (id: number, data: string): Promise<void> => call<void>("terminal_write", { id, data }),
  terminalResize: async (id: number, cols: number, rows: number): Promise<void> =>
    call<void>("terminal_resize", { id, cols, rows }),
  terminalClose: async (id: number): Promise<void> => call<void>("terminal_close", { id }),
  reviewsView: async (): Promise<ReviewsView> => call<ReviewsView>("reviews_view"),
  reviewsCheckNow: async (): Promise<ReviewsView> => call<ReviewsView>("reviews_check_now"),
  reviewsLoadRepos: async (): Promise<ReviewsView> => call<ReviewsView>("reviews_load_repos"),
  reviewsSetWatch: async (repo: string, watch: boolean, autoReview: boolean): Promise<ReviewsView> =>
    call<ReviewsView>("reviews_set_watch", { repo, watch, autoReview }),
  reviewsDismiss: async (repo: string, number: number, head: string): Promise<ReviewsView> =>
    call<ReviewsView>("reviews_dismiss", { repo, number, head }),
  reviewsRestore: async (repo: string, number: number): Promise<ReviewsView> =>
    call<ReviewsView>("reviews_restore", { repo, number }),
  reviewsPlan: async (repo: string, number: number): Promise<ReviewPlan> =>
    call<ReviewPlan>("reviews_plan", { repo, number }),
  reviewsProfiles: async (): Promise<readonly ListedProfile[]> => call<ListedProfile[]>("reviews_profiles"),
  reviewsProfileShow: async (name: string): Promise<ProfileDetail> => call<ProfileDetail>("reviews_profile_show", { name }),
  reviewsProfileAdd: async (name: string, draft: ProfileDraft): Promise<ProfileDetail> =>
    call<ProfileDetail>("reviews_profile_add", { name, draft }),
  reviewsProfileEdit: async (name: string, draft: ProfileDraft): Promise<ProfileDetail> =>
    call<ProfileDetail>("reviews_profile_edit", { name, draft }),
  reviewsProfileRemove: async (name: string): Promise<ProfileRemoved> =>
    call<ProfileRemoved>("reviews_profile_remove", { name }),
  rolesList: async (): Promise<readonly ListedRole[]> => call<ListedRole[]>("roles_list"),
  rolesShow: async (name: string): Promise<RoleDetail> => call<RoleDetail>("roles_show", { name }),
  rolesAdd: async (name: string, draft: RoleDraft): Promise<RoleDetail> => call<RoleDetail>("roles_add", { name, draft }),
  rolesEdit: async (name: string, draft: RoleDraft): Promise<RoleDetail> => call<RoleDetail>("roles_edit", { name, draft }),
  rolesRemove: async (name: string): Promise<RoleRemoved> => call<RoleRemoved>("roles_remove", { name }),
  reviewsStart: async (plan: ReviewPlan): Promise<Started> => call<Started>("reviews_start", { plan }),
  reviewReport: async (repo: string, gitRef: string, refresh: boolean): Promise<ReviewReport> =>
    call<ReviewReport>("review_report", { repo, gitRef, refresh }),
  reviewDecide: async (repo: string, gitRef: string, request: DecideRequest): Promise<ReviewReport> =>
    call<ReviewReport>("review_decide", { repo, gitRef, request }),
  reviewOpenReport: async (repo: string, gitRef: string): Promise<string> => {
    if (!isWeb()) {
      return call<string>("review_open_report", { repo, gitRef });
    }
    const tab = window.open("about:blank", "_blank");
    try {
      const path = await call<string>("review_open_report", { repo, gitRef });
      if (tab !== null) {
        tab.location.href = path;
      }
      return path;
    } catch (err: unknown) {
      tab?.close();
      throw err;
    }
  },
  reviewCommentPlan: async (repo: string, gitRef: string, number: number): Promise<CommentPlan> =>
    call<CommentPlan>("review_comment_plan", { repo, gitRef, number }),
  reviewCommentPost: async (repo: string, gitRef: string, request: PostRequest): Promise<Posted> =>
    call<Posted>("review_comment_post", { repo, gitRef, request }),
  reviewFixPlan: async (repo: string, gitRef: string, review: string, findings: readonly FixFinding[]): Promise<FixPlan> =>
    call<FixPlan>("review_fix_plan", { repo, gitRef, review, findings }),
  reviewFixStart: async (repo: string, gitRef: string, request: StartFix): Promise<FixStarted> =>
    call<FixStarted>("review_fix_start", { repo, gitRef, request }),
  reviewFixSent: async (repo: string, gitRef: string, findings: readonly string[], agent: string): Promise<ReviewReport> =>
    call<ReviewReport>("review_fix_sent", { repo, gitRef, findings, agent }),
  settingsGet: async (): Promise<Settings> => call<Settings>("settings_get"),
  versions: async (): Promise<Versions> => call<Versions>("versions"),
  settingsSetReviews: async (reviews: ReviewSettings): Promise<Settings> =>
    call<Settings>("settings_set_reviews", { reviews }),
  settingsSetNotifications: async (notifications: NotificationSettings): Promise<Settings> =>
    call<Settings>("settings_set_notifications", { notifications }),
  workspaceOpen: async (openWith: OpenWith, path: string, repos: readonly string[]): Promise<Opened> =>
    call<Opened>("workspace_open", { with: openWith, path, repos }),
  openDefaults: async (): Promise<OpenSettings> => call<OpenSettings>("open_defaults"),
  settingsSetOpen: async (open: OpenSettings): Promise<Settings> => call<Settings>("settings_set_open", { open }),
  reviewsAsk: async (repo: string, number: number): Promise<AskStarted> => call<AskStarted>("reviews_ask", { repo, number }),
  /** Calls handler with the agent's key each time the tray menu selects one; resolves to an unsubscribe. */
  onTraySelect: async (handler: (key: string) => void): Promise<() => void> => {
    if (isWeb()) {
      return webTraySelect(handler);
    }
    return transport().listen<{ readonly key: string }>("tray-select", (payload) => handler(payload.key));
  },
  loopsList: async (): Promise<LoopListing> => call<LoopListing>("loops_list"),
  loopsAdd: async (request: NewLoop): Promise<Loop> => call<Loop>("loops_add", { request }),
  loopsEdit: async (request: NewLoop): Promise<Loop> => call<Loop>("loops_edit", { request }),
  loopsSetPaused: async (name: string, paused: boolean): Promise<Loop> => call<Loop>("loops_set_paused", { name, paused }),
  loopsRemove: async (name: string): Promise<Loop> => call<Loop>("loops_remove", { name }),
  loopsRunNow: async (name: string): Promise<LoopStarted> => call<LoopStarted>("loops_run_now", { name }),
  loopsHistory: async (name: string, limit: number | null): Promise<LoopHistory> => call<LoopHistory>("loops_history", { name, limit }),
  loopsHistoryRun: async (name: string, seq: number): Promise<LoopRunDetail> => call<LoopRunDetail>("loops_history_run", { name, seq }),
  loopsHistoryClear: async (name: string): Promise<LoopCleared> => call<LoopCleared>("loops_history_clear", { name }),
  loopsMemory: async (name: string): Promise<LoopMemory> => call<LoopMemory>("loops_memory", { name }),
  loopsMemorySet: async (name: string, key: string, value: string, expectRev: number | null): Promise<LoopMemorySet> =>
    call<LoopMemorySet>("loops_memory_set", { name, key, value, expectRev }),
  loopsMemoryRemove: async (name: string, key: string): Promise<LoopMemoryRemoved> => call<LoopMemoryRemoved>("loops_memory_remove", { name, key }),
  loopsMemoryClear: async (name: string): Promise<LoopCleared> => call<LoopCleared>("loops_memory_clear", { name }),
  statsGet: async (sinceMs: number | null): Promise<Stats> => call<Stats>("stats_get", { sinceMs }),
};

export type Api = typeof api;
