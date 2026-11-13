# Review filters and forges: command, API and JSON reference

The contract for [ADR 0060](../../adrs/0060-review-filters-and-forges.md).
Every task in [tasks.md](tasks.md) implements part of it; when they
disagree, this file wins. JSON keys sent to the frontend are camelCase.
Optional values are `null`, never omitted. Rust paths are under
`desktop/src-tauri/src/`.

## Modules

| Module | Owns | Runs |
| --- | --- | --- |
| `reviews::merge` | `MergeStatus`, `MergeSignals`, `MergeInfo`, `merge_info()` | nothing (pure) |
| `reviews::github` | `gh` arguments, parsing, GitHub → `MergeSignals` | `gh` (the only module that does) |
| `reviews::http` | `Http` trait, `ReqwestHttp`, `FakeHttp` (tests) | HTTPS (the only module that does) |
| `reviews::forgejo` | Forgejo REST calls, parsing, Forgejo → `PullRequest` + `MergeSignals` | through `Http` only |
| `reviews::forge` | `ForgeKind`, `ForgeChoice`, `resolve_kind()`, `host_of()` | nothing (pure) |
| `reviews::tokens` | `TokenStore`: env lookup and `forge-tokens.json` | file I/O |
| `reviews::store` | `ForgeSettings` in `desktop.json` | file I/O (existing) |
| `reviews::checker`, `reviews::view` | per-host check, `HostView`, `forge` on repos and pull requests | via the above |

## Merge status (`reviews::merge`)

```rust
/// Declaration order is precedence: earlier wins.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum MergeStatus {
    Draft, Conflicting, Failing, Behind, ChangesRequested,
    AwaitingApproval, Blocked, Pending,
    #[default] Unknown,
    Ready,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ReviewState { #[default] NotRequired, Approved, Required, ChangesRequested }

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Checks { pub total: u32, pub passed: u32, pub failed: u32, pub pending: u32 }

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct MergeSignals {
    pub draft: bool,
    /// None: the forge hasn't computed it yet.
    pub conflicting: Option<bool>,
    pub checks: Checks,
    /// The forge requires the branch to be updated from its base.
    pub behind: bool,
    pub review: ReviewState,
    /// GitHub's `mergeStateStatus` is `BLOCKED`.
    pub blocked: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct MergeInfo { pub status: MergeStatus, pub reasons: Vec<MergeStatus>, pub checks: Checks }

pub fn merge_info(signals: &MergeSignals) -> MergeInfo;
```

`merge_info` rules. A reason is added when its condition holds:

| Reason | Condition |
| --- | --- |
| `draft` | `draft` |
| `conflicting` | `conflicting == Some(true)` |
| `failing` | `checks.failed > 0` |
| `behind` | `behind` |
| `changesRequested` | `review == ChangesRequested` |
| `awaitingApproval` | `review == Required` |
| `blocked` | `blocked` and `checks.failed == 0` and `checks.pending == 0` and `review` is `NotRequired` or `Approved` |
| `pending` | `checks.pending > 0` |
| `unknown` | `conflicting == None` |

`reasons` is sorted by precedence (the enum's `Ord`) with no duplicates and
never contains `ready`. `status` is `reasons[0]`, or `ready` when `reasons`
is empty. `checks` is copied from the signals.

JSON (`MergeInfo`, field `merge` of every pull request):

```json
{ "status": "failing", "reasons": ["failing", "awaitingApproval"],
  "checks": { "total": 7, "passed": 5, "failed": 1, "pending": 1 } }
```

`MergeInfo::default()` is `{"status":"unknown","reasons":[],"checks":{"total":0,"passed":0,"failed":0,"pending":0}}`.

## GitHub (`reviews::github`)

`PR_FIELDS` gains four fields, at the end:

```
number,title,author,url,body,isDraft,headRefOid,headRefName,baseRefName,additions,deletions,changedFiles,reviewRequests,createdAt,updatedAt,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup
```

`PullRequest` (the normalized pull request for both forges) gains one
serialized field and four parse-only fields:

```rust
/// Filled by `list_prs` (GitHub) or `forgejo::list_prs`; absent in old JSON.
#[serde(default)]
pub merge: MergeInfo,
#[serde(default, skip_serializing)] pub mergeable: String,          // "MERGEABLE" | "CONFLICTING" | "UNKNOWN" | ""
#[serde(default, skip_serializing)] pub merge_state_status: String, // "BEHIND" | "BLOCKED" | "CLEAN" | "DIRTY" | "DRAFT" | "HAS_HOOKS" | "UNKNOWN" | "UNSTABLE" | ""
#[serde(default, skip_serializing)] pub review_decision: String,    // "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | ""
#[serde(default, skip_serializing)] pub status_check_rollup: Vec<RollupItem>,
```

`PullRequest`, `Author`, `ReviewRequest` and `RollupItem` derive `Default`.

```rust
#[derive(Debug, Clone, PartialEq, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RollupItem {
    #[serde(rename = "__typename", default)] pub typename: String, // "CheckRun" | "StatusContext"
    #[serde(default)] pub status: String,     // CheckRun: "QUEUED" | "IN_PROGRESS" | "COMPLETED" | …
    #[serde(default)] pub conclusion: String, // CheckRun: "SUCCESS" | "FAILURE" | …
    #[serde(default)] pub state: String,      // StatusContext: "SUCCESS" | "FAILURE" | "ERROR" | "PENDING" | "EXPECTED"
}

pub fn github_signals(pr: &PullRequest) -> MergeSignals;
```

Mapping (`github_signals`):

- `draft`: `is_draft || merge_state_status == "DRAFT"`.
- `conflicting`: `Some(true)` if `mergeable == "CONFLICTING"` or
  `merge_state_status == "DIRTY"`; `Some(false)` if `mergeable == "MERGEABLE"`;
  else `None`.
- `checks`, one per rollup item. `CheckRun`: `status != "COMPLETED"` →
  pending; else `conclusion` in `FAILURE`, `TIMED_OUT`, `CANCELLED`,
  `ACTION_REQUIRED`, `STARTUP_FAILURE` → failed; else passed.
  `StatusContext`: `PENDING`/`EXPECTED` → pending; `FAILURE`/`ERROR` →
  failed; else passed. Any other `typename`: counted as passed.
- `behind`: `merge_state_status == "BEHIND"`.
- `review`: `APPROVED` → `Approved`, `CHANGES_REQUESTED` →
  `ChangesRequested`, `REVIEW_REQUIRED` → `Required`, else `NotRequired`.
- `blocked`: `merge_state_status == "BLOCKED"`.

`list_prs` sets `pr.merge = merge_info(&github_signals(&pr))` on every pull
request it returns.

Fixture (one open pull request, as `gh` prints it):

```json
[{"additions":3,"author":{"login":"alice"},"baseRefName":"main","body":"","changedFiles":1,"createdAt":"2026-11-13T09:00:00Z","deletions":1,"headRefName":"feat/x","headRefOid":"3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","isDraft":false,"mergeStateStatus":"BLOCKED","mergeable":"MERGEABLE","number":7,"reviewDecision":"REVIEW_REQUIRED","reviewRequests":[{"__typename":"User","login":"bobcob7"}],"statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","workflowName":"CI"},{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE","workflowName":"CI"},{"__typename":"StatusContext","context":"ci/woodpecker/pr/ci","state":"PENDING"}],"title":"Add x","updatedAt":"2026-11-13T09:30:00Z","url":"https://github.com/bobcob7/buff-bear/pull/7"}]
```

→ `merge`: `{"status":"failing","reasons":["failing","awaitingApproval","pending"],"checks":{"total":3,"passed":1,"failed":1,"pending":1}}`.

## Forgejo (`reviews::forgejo`, `reviews::http`)

### HTTP seam

```rust
pub type BoxFuture<'a, T> = std::pin::Pin<Box<dyn std::future::Future<Output = T> + Send + 'a>>;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HttpResponse { pub status: u16, pub body: Vec<u8> }

pub trait Http: Send + Sync {
    /// GET `url` with `Accept: application/json` and, given a token,
    /// `Authorization: token <token>`. A network or TLS failure is
    /// `AppError { code: "forge_failed", message: "<host>: <error>" }`.
    fn get<'a>(&'a self, url: &'a str, token: Option<&'a str>) -> BoxFuture<'a, Result<HttpResponse, AppError>>;
}

/// reqwest 0.13, rustls, 20 s timeout per request, no redirects followed
/// to another host.
pub struct ReqwestHttp { /* reqwest::Client */ }
impl ReqwestHttp { pub fn new() -> Self; }

/// Tests: canned responses by exact URL; unknown URLs answer 404 `{}`.
#[cfg(test)]
pub struct FakeHttp { /* … */ }
#[cfg(test)]
impl FakeHttp {
    pub fn new() -> Self;
    pub fn route(self, url: &str, status: u16, body: &str) -> Self;
    /// `url` fails as a dead host would: Err(forge_failed "<host>: connection refused").
    pub fn fail(self, url: &str) -> Self;
    /// Every (url, token) requested, in order.
    pub fn calls(&self) -> Vec<(String, Option<String>)>;
}
```

### Requests

`base = https://<host>/api/v1`; `<host>` may carry a port. All are GET.

| Call | URL | Token | Use |
| --- | --- | --- | --- |
| probe | `{base}/version` | none | 200 and a JSON object with a string `version` → Forgejo |
| me | `{base}/user` | yes | `login` |
| pulls | `{base}/repos/{owner}/{name}/pulls?state=open&sort=recentupdate&limit=50&page={1,2}` | if any | stop after a page with fewer than 50 items, or after page 2 |
| protections | `{base}/repos/{owner}/{name}/branch_protections` | if any | once per repo per check; 401/403/404 → unreadable |
| status | `{base}/repos/{owner}/{name}/commits/{head_sha}/status` | if any | per pull request |
| reviews | `{base}/repos/{owner}/{name}/pulls/{number}/reviews` | if any | per pull request |

Status and reviews run four pull requests at a time per repo (a
`tokio::sync::Semaphore` of 4, `JoinSet`).

### Error mapping (`forgejo::http_error(host, status, body, had_token) -> AppError`)

| HTTP | Code | Message |
| --- | --- | --- |
| 401, 403 | `forge_auth` | `<host> rejected the token (HTTP <n>). Set a new one in Settings › Forges.`; without a token: `<host> needs a token (HTTP <n>). Set one in Settings › Forges.` |
| 429 | `forge_rate_limited` | `<host>: rate limited (HTTP 429)` |
| any other non-2xx | `forge_failed` | `<host>: HTTP <n>`, then `: <message>` when the body is JSON with a string `message` |

A status or reviews call that fails leaves that pull request's signals at
their defaults for the failed part (checks all zero; review `NotRequired`)
and adds no error; a failed pulls call fails the repo.

### Parsed shapes (serde, snake_case as Forgejo sends them; unknown fields ignored)

```rust
struct FjPull { number: u64, title: String, body: Option<String>, html_url: String, draft: bool,
    mergeable: bool, merge_base: Option<String>, user: FjUser, head: FjRef, base: FjRef,
    additions: Option<u64>, deletions: Option<u64>, changed_files: Option<u64>,
    requested_reviewers: Option<Vec<FjUser>>, created_at: String, updated_at: String }
struct FjUser { login: String }
struct FjRef { #[serde(rename = "ref")] name: String, sha: String }
struct FjStatus { state: String, statuses: Option<Vec<FjStatusItem>> }
struct FjStatusItem { status: String }
struct FjReview { id: u64, user: Option<FjUser>, state: String, stale: bool, official: bool, dismissed: bool }
struct FjProtection { rule_name: String, branch_name: Option<String>, required_approvals: u32, block_on_outdated_branch: bool }
```

### Mapping to `PullRequest`

| `PullRequest` | Forgejo |
| --- | --- |
| `number`, `title` | same |
| `body` | `body` or `""` |
| `url` | `html_url` |
| `author.login` | `user.login` |
| `is_draft` | `draft` |
| `head_ref_oid`, `head_ref_name` | `head.sha`, `head.ref` |
| `base_ref_name` | `base.ref` |
| `additions`, `deletions`, `changed_files` | same, `0` when absent |
| `review_requests` | `requested_reviewers[].login` as `ReviewRequest { login: Some(..) }` |
| `created_at`, `updated_at` | same strings |
| `merge` | `merge_info(&forgejo_signals(..))` |

```rust
pub struct Protection { pub required_approvals: u32, pub block_on_outdated_branch: bool }
/// The rule for `base`: `rule_name` (or `branch_name`) equal to it, else
/// the first whose `rule_name` is a glob (`*` matches any run of
/// characters, including `/`) matching it; None when no rule applies.
pub fn protection_for(rules: &[FjProtection], base: &str) -> Option<Protection>;

/// `protections`: Ok(rules) when readable, Err(()) when not.
pub fn forgejo_signals(pull: &FjPull, status: Option<&FjStatus>, reviews: &[FjReview],
                       protections: Result<&[FjProtection], ()>) -> MergeSignals;
```

`forgejo_signals`:

- `draft`: `draft`. `conflicting`: `Some(!mergeable)`. `blocked`: false.
- `checks`, one per `statuses[]` item: `pending` → pending; `failure`,
  `error` → failed; anything else (`success`, `warning`, `skipped`) →
  passed. No status → all zero.
- Effective protection: readable and a rule applies → that rule;
  readable and none applies → `{required_approvals: 0, block_on_outdated_branch: false}`;
  unreadable → `{required_approvals: 1, block_on_outdated_branch: false}`.
- `behind`: `block_on_outdated_branch && merge_base != Some(base.sha)`.
- `review`: keep reviews with `official && !stale && !dismissed` and state
  `APPROVED` or `REQUEST_CHANGES`; for each user keep only the one with the
  highest `id`. Any `REQUEST_CHANGES` → `ChangesRequested`; else
  `approvals < required_approvals` → `Required`; else `approvals > 0` →
  `Approved`; else `NotRequired`.

### Public functions

```rust
pub enum Probe { Forgejo, NotForgejo, Unreachable }
pub async fn probe(http: &dyn Http, host: &str) -> Probe;            // 200 + version → Forgejo; other HTTP status or bad JSON → NotForgejo; Err → Unreachable
pub async fn login(http: &dyn Http, host: &str, token: &str) -> Result<String, AppError>;
pub async fn list_prs(http: Arc<dyn Http>, repo_id: &str, token: Option<&str>) -> Result<Vec<PullRequest>, AppError>;
```

`list_prs` takes a cache id `host/owner/name` (`github::split_repo`) and
returns pull requests in the order Forgejo sent them.

Fixtures (trimmed from codeberg.org, 2026-11-13):

```json
[{"number":15262,"title":"refactor: move RemoveTeamMember","body":"Moves it.","html_url":"https://git.example.com/team/svc/pulls/15262","draft":false,"mergeable":true,"merge_base":"7aa74e4f83288716cbe38f58f06a12f77995d38c","user":{"login":"wetneb","id":94198},"head":{"ref":"remove_member","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","label":"remove_member"},"base":{"ref":"forgejo","sha":"7aa74e4f83288716cbe38f58f06a12f77995d38c"},"additions":428,"deletions":461,"changed_files":16,"requested_reviewers":[{"login":"Gusted","id":26734}],"requested_reviewers_teams":[],"created_at":"2026-11-13T22:14:22+01:00","updated_at":"2026-11-13T22:14:30+01:00","state":"open"}]
```

```json
{"state":"failure","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","total_count":3,"statuses":[{"context":"testing / unit","status":"success"},{"context":"testing / lint","status":"failure"},{"context":"issue-labels / cascade","status":"skipped"}]}
```

```json
[{"id":1,"user":{"login":"Gusted"},"state":"REQUEST_REVIEW","stale":false,"official":true,"dismissed":false,"commit_id":""},
 {"id":2,"user":{"login":"Cyborus"},"state":"APPROVED","stale":false,"official":true,"dismissed":false,"commit_id":"8db6637666e157ddd3af5d5033ac7edc347f2c10"}]
```

```json
[{"rule_name":"forgejo","branch_name":"forgejo","required_approvals":2,"block_on_outdated_branch":true}]
```

With those four: `merge` = `{"status":"failing","reasons":["failing","awaitingApproval"],"checks":{"total":3,"passed":2,"failed":1,"pending":0}}`
(`merge_base` equals `base.sha`, so not behind; one approval of two).

## Hosts and tokens (`reviews::forge`, `reviews::tokens`)

```rust
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ForgeKind { #[default] Github, Forgejo }

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ForgeChoice { #[default] Auto, Github, Forgejo }

/// The host of a cache id: "git.example.com:3000" from "git.example.com:3000/team/svc".
pub fn host_of(repo_id: &str) -> Option<&str>;

/// Rules 1–4 of ADR 0060. `probed`: None = not probed yet (or unreachable),
/// Some(true) = Forgejo, Some(false) = not Forgejo.
pub fn resolve_kind(host: &str, choice: ForgeChoice, gh_hosts: &BTreeSet<String>, probed: Option<bool>) -> Option<ForgeKind>;

/// Whether `resolve_kind` needs a probe first: choice Auto, not github.com,
/// not in gh_hosts.
pub fn needs_probe(host: &str, choice: ForgeChoice, gh_hosts: &BTreeSet<String>) -> bool;

/// A host name Settings accepts: non-empty; letters, digits, '.', '-', and
/// one optional ':<port>' (digits); no '/', '@' or whitespace.
pub fn is_host(host: &str) -> bool;
```

```rust
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum TokenSource { Env, File }

pub const TOKENS_FILE: &str = "forge-tokens.json";

pub struct TokenStore { /* path: Option<PathBuf>, env: Box<dyn Fn(&str) -> Option<String> + Send + Sync> */ }
impl TokenStore {
    /// The file beside `desktop_json` (None: no file), reading `std::env::var`.
    pub fn beside(desktop_json: Option<&Path>) -> Self;
    /// Same, with an injected environment (tests).
    pub fn with_env(path: Option<PathBuf>, env: impl Fn(&str) -> Option<String> + Send + Sync + 'static) -> Self;
    /// "WS_FORGEJO_TOKEN_" + host upper-cased, non-alphanumerics as '_'.
    pub fn env_name(host: &str) -> String;
    /// Env first (non-empty after trim), then the file.
    pub fn token(&self, host: &str) -> Option<(String, TokenSource)>;
    /// Empty `token` removes the host. Rejects whitespace inside a token
    /// (`setting_invalid`). Writes temp + rename, mode 0600; a file with a
    /// newer version is never written (`state_newer`).
    pub fn set(&self, host: &str, token: &str) -> Result<(), AppError>;
}
```

`forge-tokens.json`, beside `desktop.json`, mode 0600:

```json
{ "version": 1, "tokens": { "git.example.com": "0123456789abcdef0123456789abcdef01234567" } }
```

Missing or unreadable file: no tokens. A token is never logged, never put
in an `AppError`, and never serialized to the frontend.

## `desktop.json` (`reviews::store`)

`Settings` gains `forges` (version stays 1; a missing key is the default):

```rust
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ForgeHost { pub kind: ForgeChoice }

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ForgeSettings { pub hosts: BTreeMap<String, ForgeHost> }

impl ForgeSettings {
    pub fn choice(&self, host: &str) -> ForgeChoice;           // Auto when absent
    pub fn set_choice(&mut self, host: &str, choice: ForgeChoice); // Auto removes the entry
}
// Settings { reviews, notifications, open, forges: ForgeSettings }
```

```json
{ "version": 1,
  "settings": { "reviews": { "…": "…" }, "notifications": { "…": "…" }, "open": { "…": "…" },
                "forges": { "hosts": { "git.example.com": { "kind": "forgejo" } } } },
  "reviews": { "…": "…" } }
```

## The view (`reviews_view` and every command that returns `ReviewsView`)

`ReviewsView` gains `hosts`, `RepoView` gains `forge`, `PrView` gains
`forge` (and `merge`, through the flattened `PullRequest`). Nothing is
removed or renamed; `RepoView.github` is still `forge == "github"`.

```json
{
  "checking": false,
  "checkedAtMs": 1763047800000,
  "error": null,
  "hosts": [
    { "host": "git.example.com", "forge": "forgejo", "choice": "auto", "detected": true,
      "login": "bob", "token": "file", "tokenEnv": "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM", "error": null },
    { "host": "github.com", "forge": "github", "choice": "auto", "detected": false,
      "login": "bobcob7", "token": null, "tokenEnv": null, "error": null },
    { "host": "gitlab.example.com", "forge": null, "choice": "auto", "detected": false,
      "login": null, "token": null, "tokenEnv": null,
      "error": { "code": "forge_unknown", "message": "gitlab.example.com isn't a GitHub or Forgejo host. Choose its forge in Settings › Forges." } }
  ],
  "repos": [
    { "id": "git.example.com/team/svc", "github": false, "forge": "forgejo", "watch": true, "autoReview": false,
      "open": 1, "checkedAtMs": 1763047800000, "error": null }
  ],
  "prs": [
    { "key": "git.example.com/team/svc#15262", "repo": "git.example.com/team/svc", "forge": "forgejo",
      "number": 15262, "title": "refactor: move RemoveTeamMember", "author": { "login": "wetneb" },
      "url": "https://git.example.com/team/svc/pulls/15262", "body": "Moves it.", "isDraft": false,
      "headRefOid": "8db6637666e157ddd3af5d5033ac7edc347f2c10", "headRefName": "remove_member", "baseRefName": "forgejo",
      "additions": 428, "deletions": 461, "changedFiles": 16, "reviewRequests": [{ "login": "Gusted" }],
      "createdAt": "2026-11-13T22:14:22+01:00", "updatedAt": "2026-11-13T22:14:30+01:00",
      "merge": { "status": "failing", "reasons": ["failing", "awaitingApproval"],
                 "checks": { "total": 3, "passed": 2, "failed": 1, "pending": 0 } },
      "group": "toReview", "outOfDate": false, "requested": false, "mine": false,
      "reviews": [], "newCommits": null }
  ],
  "warnings": []
}
```

`HostView` (one per distinct host of the cached repos, sorted by host):

| Field | Value |
| --- | --- |
| `host` | the host, with port if any |
| `forge` | `"github"`, `"forgejo"`, or `null` when unresolved |
| `choice` | the setting: `"auto"`, `"github"`, `"forgejo"` |
| `detected` | `true` when `forge` came from the probe (rule 3) |
| `login` | "me" on the host, or `null` |
| `token` | `"env"`, `"file"`, or `null`; always `null` for GitHub hosts |
| `tokenEnv` | `TokenStore::env_name(host)` for Forgejo hosts, else `null` |
| `error` | the host's `AppError` from the last check, or `null` |

Host errors:

| Code | When | Message |
| --- | --- | --- |
| `gh_missing` | a GitHub host, `gh` not on PATH | existing text |
| `gh_auth` | a GitHub host that `gh auth status` doesn't list as signed in | `gh isn't signed in to <host>. Run gh auth login --hostname <host> in a terminal, then Check now.` |
| `forge_auth`, `forge_rate_limited`, `forge_failed` | Forgejo `user` or a repo's pulls call | as in the Forgejo table |
| `forge_unknown` | a host with a watched repo that resolves to no forge after the probe answered | `<host> isn't a GitHub or Forgejo host. Choose its forge in Settings › Forges.` |
| `forge_failed` | a host with a watched repo whose probe got no answer (`Probe::Unreachable`) | `<host>: no answer from https://<host>/api/v1/version` |

Top-level `error` (unchanged meaning): `ws cache list` failing; or
`gh_missing`/`gh_auth` when every watched repo's host resolves to
`github` (with the existing message, `gh isn't signed in. Run gh auth login in a terminal, then Check now.`).
A `forge_rate_limited` repo counts toward the backoff like
`gh_rate_limited`.

Watched repos are cached repos with `watch` on whose host resolves to a
forge. `mine` and `requested` use the host's `login` (case-insensitive),
for both forges.

## Commands

All are Tauri commands in `reviews/mod.rs`, registered in `lib.rs`
`generate_handler!` and in `online/dispatch.rs` (`COMMANDS` and the
`match`), and wrapped in `desktop/src/api.ts`.

| Command | Args (JSON) | Returns | Errors |
| --- | --- | --- | --- |
| `reviews_view` | — | `ReviewsView` (with `hosts`) | — |
| `reviews_set_forge` | `{ "host": "git.example.com", "kind": "auto" \| "github" \| "forgejo" }` | `ReviewsView` | `setting_invalid` (not `is_host`), `state_newer`, `state_write_failed` |
| `reviews_set_token` | `{ "host": "git.example.com", "token": "…" }` (`""` removes) | `ReviewsView` | `setting_invalid`, `state_newer`, `state_write_failed` |
| `settings_get` | — | `Settings`, now with `forges` (ignored by the frontend) | — |
| `review_comment_plan` | unchanged | unchanged | new: `forge_unsupported` (`Posting findings is GitHub-only for now.`) when the repo's host isn't `github` |

`reviews_set_forge` and `reviews_set_token` save, forget the host's probe
result and error, call `check_now()`, and return the view.

TypeScript (`desktop/src/api.ts`):

```ts
export type ForgeKind = "github" | "forgejo";
export type ForgeChoice = "auto" | ForgeKind;
export type MergeStatus = "draft" | "conflicting" | "failing" | "behind" | "changesRequested"
  | "awaitingApproval" | "blocked" | "pending" | "unknown" | "ready";
export interface Checks { readonly total: number; readonly passed: number; readonly failed: number; readonly pending: number }
export interface MergeInfo { readonly status: MergeStatus; readonly reasons: readonly MergeStatus[]; readonly checks: Checks }
export interface HostView {
  readonly host: string; readonly forge: ForgeKind | null; readonly choice: ForgeChoice; readonly detected: boolean;
  readonly login: string | null; readonly token: "env" | "file" | null; readonly tokenEnv: string | null;
  readonly error: AppError | null;
}
// PullRequest gains: readonly forge: ForgeKind; readonly merge: MergeInfo;
// WatchRepo gains:   readonly forge: ForgeKind | null;
// ReviewsView gains: readonly hosts: readonly HostView[];
// Settings is unchanged in TypeScript: the backend sends `forges`, but the
// frontend reads each host's choice from HostView.choice.
// api gains:
//   reviewsSetForge(host: string, kind: ForgeChoice): Promise<ReviewsView>   -> "reviews_set_forge", { host, kind }
//   reviewsSetToken(host: string, token: string): Promise<ReviewsView>       -> "reviews_set_token", { host, token }
```

## Starting reviews (`reviews::plan`)

`ReviewPlan` gains `forge: ForgeKind` (`#[serde(default)]`, default
`Github`, serialized `"forge": "github"`). `plan::plan` keeps its signature
and sets `Github`; callers that know the host's kind overwrite it
(`plan.forge = kind`). `start_args`:

- `Github`: unchanged, `review start --json --pr <n> [--name <name>] <repo> …`.
- `Forgejo`: `review start --json --name <name> <repo>@<head> …`, always
  with `--name` (the name the plan holds, by default ADR 0032's
  `names::review_name(repo, number, head)`), and no `--pr`. Profiles and
  the part after `--` are the same as for GitHub.

`view::is_review_of(entry, repo, number)`:

- record with `pr`: `record.repo == repo && pr.number == number` (unchanged);
- record without `pr` (a Forgejo review): `record.repo == repo` and the
  workspace id starts with `names::review_prefix(repo, number)`;
- no record: `names::is_review_of(id, &entry.repos, repo, number)` (unchanged).

So a Forgejo review groups under its pull request, also after `ws` removed
its workspace (ADR 0045), since `ws review list` keeps the record.

The frontend's `commandPreview(plan)` shows, for `forge === "forgejo"`,
`ws review start --name <name> <repo>@<sha7> …` instead of `--pr <n> <repo>`.

## Filters (frontend only, `reviewsSlice` and `groups.ts`)

```ts
export type Involves = "anyone" | "meAny" | "mine" | "requested";
export interface PrFilters {
  readonly author: string | null;        // exact login, case-insensitive
  readonly involves: Involves;
  readonly merge: MergeStatus | null;    // matches merge.status
}
export const noFilters: PrFilters = { author: null, involves: "anyone", merge: null };
export const matchesFilters = (pr: PullRequest, filters: PrFilters): boolean;
export const authorsOf = (prs: readonly PullRequest[]): readonly string[]; // distinct logins, sorted case-insensitively
export const groupPrs = (prs: readonly PullRequest[], filter: string, filters: PrFilters = noFilters): readonly PrGroup[];
export const activeFilterCount = (filters: PrFilters): number;
```

`involves`: `meAny` → `pr.mine || pr.requested`; `mine` → `pr.mine`;
`requested` → `pr.requested`. Slice state: `filters: PrFilters` (initial
`noFilters`); actions `authorFiltered(string | null)`,
`involvesFiltered(Involves)`, `mergeFiltered(MergeStatus | null)`,
`filtersCleared()`. Not persisted.

Merge status labels (the badge and the Merge select):

| Status | Label | Tone |
| --- | --- | --- |
| `ready` | Ready to merge | success |
| `draft` | Draft | neutral |
| `conflicting` | Conflicts | error |
| `failing` | Checks failing | error |
| `behind` | Needs update | warning |
| `changesRequested` | Changes requested | warning |
| `awaitingApproval` | Awaiting approval | warning |
| `blocked` | Blocked | warning |
| `pending` | Checks running | info |
| `unknown` | Status unknown | neutral |
