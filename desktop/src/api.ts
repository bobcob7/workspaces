import { Channel, invoke } from "@tauri-apps/api/core";

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
  /** Shown only in the Hidden group and not counted as waiting (ADR 0043). */
  readonly hidden: boolean;
  /** The workspace this one was started from, and is removed with (ADR 0044). */
  readonly parentId: string | null;
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

export interface Settings {
  readonly reviews: ReviewSettings;
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
    return await invoke<T>(command, args);
  } catch (err: unknown) {
    throw toAppError(err);
  }
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
  createAgent: async (request: NewAgent): Promise<Created> => call<Created>("create_agent", { request }),
  removeEntry: async (workspaceId: string | null, sessionId: string | null, keepChildren = false): Promise<void> =>
    call<void>("remove_entry", { workspaceId, sessionId, keepChildren }),
  renameWorkspace: async (workspaceId: string, name: string): Promise<Renamed> =>
    call<Renamed>("rename_workspace", { workspaceId, name }),
  setWorkspaceHidden: async (workspaceId: string, hidden: boolean): Promise<HiddenSet> =>
    call<HiddenSet>("set_workspace_hidden", { workspaceId, hidden }),
  stopAgent: async (sessionId: string): Promise<void> => call<void>("stop_agent", { sessionId }),
  restoreAgents: async (): Promise<readonly string[]> => call<string[]>("restore_agents"),
  openExternal: async (target: string): Promise<void> => call<void>("open_external", { target }),
  readTranscript: async (sessionId: string, offset: number): Promise<TranscriptChunk> =>
    call<TranscriptChunk>("read_transcript", { sessionId, offset }),
  terminalOpen: async (
    spec: TermSpec,
    cols: number,
    rows: number,
    onEvent: (event: TermEvent) => void,
  ): Promise<number> => {
    const channel = new Channel<TermEvent>();
    channel.onmessage = onEvent;
    return call<number>("terminal_open", { spec, cols, rows, onEvent: channel });
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
  reviewsStart: async (plan: ReviewPlan): Promise<Started> => call<Started>("reviews_start", { plan }),
  reviewReport: async (repo: string, gitRef: string, refresh: boolean): Promise<ReviewReport> =>
    call<ReviewReport>("review_report", { repo, gitRef, refresh }),
  reviewDecide: async (repo: string, gitRef: string, request: DecideRequest): Promise<ReviewReport> =>
    call<ReviewReport>("review_decide", { repo, gitRef, request }),
  reviewOpenReport: async (repo: string, gitRef: string): Promise<string> =>
    call<string>("review_open_report", { repo, gitRef }),
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
};

export type Api = typeof api;
