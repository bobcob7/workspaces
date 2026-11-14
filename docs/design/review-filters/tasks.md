# Review filters and forges: implementation tasks

Implements [ADR 0060](../../adrs/0060-review-filters-and-forges.md) to the
contract in [reference.md](reference.md). Each task builds, passes its
tests, and can be merged on its own. Each task is one commit on its own
branch (conventional commit, imperative mood, for example
`feat: add merge status to pull requests (ADR 0060)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | Merge status model, GitHub signals, module stubs | nothing | medium | — |
| T2 | Forgejo REST provider behind an `Http` seam | T1 | medium | T3 |
| T3 | Hosts, tokens, `forges` setting, Forgejo review start | T1 | medium | T2 |
| T4 | Checker and view: per-host check, `hosts`, commands | T2, T3 | large | — |
| T5 | Frontend data: API types, filters, merge badge, Tauri mock | T4 | medium | — |
| T6 | Frontend UI: filter bar, badges, Settings › Forges | T5 | medium | — |

T1 creates every new Rust module as a stub and registers it in
`reviews/mod.rs`, so T2 and T3 touch disjoint files: **T2 ∥ T3**. No task
is `small` (each touches more than two files). Backend (T1–T4) comes
before the UI (T5, T6). T5 is the first UI task and carries the Tauri mock
for screenshots.

**Read before any task:** the "Decision" section of ADR 0060 and all of
`reference.md`. When this file and those two disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style (a `///` doc comment on every `pub` item, `//!` at the top of a new
  module, no other comments unless the logic isn't obvious), test style.
  Prefer early returns and small functions.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean; clippy clean with
  `-D warnings` for both feature sets (see Verify); no `unwrap()`/`expect()`
  outside tests; errors are `crate::runner::AppError::new(code, message)`.
  Unit tests go in the file's own `#[cfg(test)] mod tests { use super::*; … }`,
  one behaviour per `#[test]`/`#[tokio::test]`, named as a snake_case
  sentence. Async tests use `#[tokio::test]`.
- **Dead code until T4.** A `pub` item that only a later task uses gets
  `#[allow(dead_code)] // used by T4` on the item (or on the `mod` line in
  `reviews/mod.rs`). T4 removes every one of them
  (`grep -rn "used by T4" desktop/src-tauri/src` must print nothing after T4).
- **Secrets:** never put a token in an `AppError`, a log line, a warning,
  or anything serialized to the frontend.
- **TypeScript** (`desktop/src/`): strict, `readonly` fields, no `any`, no
  `!`, no `enum`; components are `export const X = (props): JSX.Element =>`;
  styles in a CSS module; MUI components as the files already use them.
  Tests: vitest, `@testing-library/react`, `user-event`; helpers in
  `desktop/src/test/fixtures.ts` (`pullRequest`, `reviewsView`, `testStore`,
  `fakeApi`) and `desktop/src/test/render.tsx` (`renderWithStore`). Query by
  role and accessible name.
- **No new npm or cargo dependencies.** (T2 moves the existing `reqwest`
  out of the `online` feature; that is not a new dependency.)
- **Phone width:** every new button, select and row action is at least
  40px tall under `@media (max-width: 720px)`, as
  `SettingsPane.module.css` does.
- Don't touch files a task doesn't list, except to fix a compile error the
  task caused (say so in your report).

### Verify (Rust tasks)

```sh
cd desktop/src-tauri && cargo fmt --check \
  && cargo clippy --all-targets -- -D warnings && cargo test \
  && cargo clippy --no-default-features --features online --all-targets -- -D warnings \
  && cargo test --no-default-features --features online
```

### Verify (TypeScript tasks)

```sh
cd desktop && npm ci && npm run typecheck && npm test -- --testTimeout=60000
```

---

## T1. Merge status model, GitHub signals, module stubs

**Depends on:** nothing. **Parallel with:** nothing. **Size:** medium.

### Files

- `desktop/src-tauri/src/reviews/merge.rs` (new)
- `desktop/src-tauri/src/reviews/github.rs`
- `desktop/src-tauri/src/reviews/view.rs` (only the test helper `pr()`)
- `desktop/src-tauri/src/reviews/mod.rs` (module lines only)
- `desktop/src-tauri/src/reviews/forge.rs`, `forgejo.rs`, `http.rs`,
  `tokens.rs` (new stubs: one `//!` line each)

### What to do

1. **`merge.rs`**: write exactly the types and `merge_info` from
   reference.md › "Merge status": `MergeStatus` (declaration order is
   precedence; derive `Ord`), `ReviewState`, `Checks`, `MergeSignals`,
   `MergeInfo`, and

   ```rust
   /// The merge status, every reason, and the check counts (ADR 0060).
   pub fn merge_info(signals: &MergeSignals) -> MergeInfo
   ```

   Build `reasons` by pushing in precedence order following the reasons
   table, so it is already sorted; `status = reasons.first().copied().unwrap_or(MergeStatus::Ready)`.
   `Checks` and `MergeInfo` serialize camelCase (`#[serde(rename_all = "camelCase")]`).
2. **`github.rs`**:
   - Append `,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup` to `PR_FIELDS`.
   - Add `#[derive(Default)]` to `Author`, `ReviewRequest`, `PullRequest`.
   - Add to `PullRequest` the field `merge` (`#[serde(default)] pub merge: MergeInfo`)
     and the four parse-only fields, exactly as in reference.md › "GitHub"
     (`#[serde(default, skip_serializing)]`), and the `RollupItem` struct.
   - Add `pub fn github_signals(pr: &PullRequest) -> MergeSignals` with the
     mapping in reference.md.
   - Add
     ```rust
     /// Parses `gh pr list --json` output and fills each pull request's merge status.
     pub fn parse_prs(stdout: &[u8]) -> Result<Vec<PullRequest>, AppError>
     ```
     (`runner::parse_json(stdout, "gh pr list")`, then set
     `pr.merge = merge_info(&github_signals(&pr))` on each), and make
     `list_prs` return `parse_prs(&outcome.stdout)`.
3. **`view.rs`**: in `tests::pr()`, end the `PullRequest { … }` literal with
   `..PullRequest::default()` so it compiles. Change nothing else.
4. **`mod.rs`**: add, in alphabetical order with the existing `pub mod`
   lines: `pub mod forge;`, `pub mod forgejo;`, `pub mod http;`,
   `pub mod merge;`, `pub mod tokens;`. Create the four stub files with one
   line each:
   - `forge.rs`: `//! Which forge each host runs, and how that's decided (ADR 0060).`
   - `forgejo.rs`: `//! Reading pull requests from a Forgejo host's REST API (ADR 0060).`
   - `http.rs`: `//! HTTPS GET for forge APIs, behind a trait tests replace (ADR 0060).`
   - `tokens.rs`: `//! Forgejo access tokens: environment first, then forge-tokens.json (ADR 0060).`

### Tests

`merge.rs` (`fn signals() -> MergeSignals` helper returning a clean state:
`conflicting: Some(false)`, all else default):

- `ready_when_nothing_applies` → status `Ready`, reasons empty.
- `unknown_when_mergeability_is_not_computed` (`conflicting: None`) → `Unknown`.
- `draft_wins_over_conflicts_and_failures`.
- `conflicting_wins_over_failing`.
- `failing_wins_over_behind`.
- `behind_wins_over_awaiting_approval`.
- `changes_requested_wins_over_pending_checks` (review `ChangesRequested`, pending 1) → status `ChangesRequested`, reasons `[ChangesRequested, Pending]`.
- `blocked_needs_no_other_explanation`: `blocked` alone → `Blocked`; `blocked` with `failed: 1` → reasons `[Failing]` only; with `pending: 1` → `[Pending]` only; with review `Required` → `[AwaitingApproval]` only.
- `reasons_never_include_ready_and_are_in_precedence_order` (draft + behind + pending + unknown).
- `checks_are_copied`.
- `merge_info_serializes_camel_case`: `serde_json::to_value(&info)` equals
  `json!({"status":"awaitingApproval","reasons":["awaitingApproval"],"checks":{"total":2,"passed":2,"failed":0,"pending":0}})`.

`github.rs`:

- `pr_list_args_ask_for_merge_fields`: the last argument ends with
  `mergeable,mergeStateStatus,reviewDecision,statusCheckRollup` and still
  doesn't contain `commits`.
- `parse_prs_fills_merge_status`: the fixture in reference.md › "GitHub"
  gives `merge` = `{status: Failing, reasons: [Failing, AwaitingApproval, Pending], checks: {3,1,1,1}}`.
- `pull_request_without_merge_fields_is_unknown`: the existing
  `parses_gh_pr_list_output` JSON through `parse_prs` → status `Unknown`,
  reasons `[Unknown]`.
- `github_signals_maps_conflicts`: `CONFLICTING` → `Some(true)`;
  `MERGEABLE` with `DIRTY` → `Some(true)`; `MERGEABLE` → `Some(false)`;
  `UNKNOWN` → `None`.
- `github_signals_maps_reviews_and_behind`: `APPROVED`,
  `CHANGES_REQUESTED`, `REVIEW_REQUIRED`, `""`; `BEHIND` → `behind`; `BLOCKED` → `blocked`; `DRAFT` → `draft`.
- `github_signals_counts_checks`: a `CheckRun` `IN_PROGRESS` → pending;
  `COMPLETED`/`TIMED_OUT` → failed; `COMPLETED`/`NEUTRAL` and `SKIPPED` →
  passed; `StatusContext` `ERROR` → failed, `EXPECTED` → pending, `SUCCESS` → passed.
- `merge_is_serialized_but_raw_fields_are_not`: `serde_json::to_value(&pr)`
  has key `merge` and no `mergeable`, `mergeStateStatus`,
  `reviewDecision`, `statusCheckRollup`.

### Acceptance

- `gh pr list` is asked for the four new fields; every `PullRequest` from
  `list_prs` has `merge` set; the JSON the frontend gets gains only `merge`.
- Existing tests pass unchanged (except `view.rs`'s helper line).
- Verify (Rust tasks) passes.

---

## T2. Forgejo REST provider behind an `Http` seam

**Depends on:** T1. **Parallel with:** T3. **Size:** medium.

### Files

- `desktop/src-tauri/src/reviews/http.rs` (replace the stub)
- `desktop/src-tauri/src/reviews/forgejo.rs` (replace the stub)
- `desktop/src-tauri/Cargo.toml`, `desktop/src-tauri/Cargo.lock`

### What to do

1. **`Cargo.toml`**: make `reqwest` a regular dependency
   (`reqwest = { version = "0.13", default-features = false, features = ["rustls"] }`,
   no `optional`), and remove `"dep:reqwest"` from the `online` feature
   list. Let cargo update `Cargo.lock` if it needs to.
2. **`http.rs`**: `BoxFuture`, `HttpResponse`, `Http`, `ReqwestHttp`, and
   (`#[cfg(test)]`) `FakeHttp` exactly as in reference.md › "HTTP seam".
   - `ReqwestHttp::new()` builds a `reqwest::Client` with
     `.timeout(Duration::from_secs(20))` and a redirect policy that follows
     at most 3 redirects and only to the first request's host:
     ```rust
     reqwest::redirect::Policy::custom(|attempt| {
         let same_host = attempt.previous().first().and_then(|u| u.host_str()) == attempt.url().host_str();
         if attempt.previous().len() > 3 || !same_host { attempt.stop() } else { attempt.follow() }
     })
     ```
     If `build()` fails, fall back to `reqwest::Client::new()`. Implement
     `Default` by calling `new()`.
   - `get` sends `Accept: application/json`, plus
     `Authorization: token <token>` when given; returns the status code and
     body bytes for any HTTP answer (including 4xx/5xx). A transport error
     is `AppError::new("forge_failed", format!("{host}: {}", e.without_url()))`
     where `host` is the URL's host (and port), from `reqwest::Url::parse`.
   - `FakeHttp`: a `Mutex<Vec<(String, Result<(u16, String), ()>)>>` of
     routes and a `Mutex<Vec<(String, Option<String>)>>` of calls. `route`
     answers `HttpResponse { status, body }`; `fail` answers
     `Err(AppError::new("forge_failed", "<host>: connection refused"))`; an
     unknown URL answers `404` with body `{}`. `calls()` returns a clone.
3. **`forgejo.rs`**: the parsed shapes (private structs, `Deserialize`,
   unknown fields ignored; the shapes in reference.md › "Parsed shapes"
   with `#[serde(default)]` on every `Option`, `bool` and number), and:

   ```rust
   pub enum Probe { Forgejo, NotForgejo, Unreachable }
   pub async fn probe(http: &dyn Http, host: &str) -> Probe;
   pub async fn login(http: &dyn Http, host: &str, token: &str) -> Result<String, AppError>;
   pub async fn list_prs(http: Arc<dyn Http>, repo_id: &str, token: Option<&str>) -> Result<Vec<PullRequest>, AppError>;
   pub fn http_error(host: &str, status: u16, body: &[u8], had_token: bool) -> AppError;
   pub struct Protection { pub required_approvals: u32, pub block_on_outdated_branch: bool }
   pub fn protection_for(rules: &[FjProtection], base: &str) -> Option<Protection>;
   pub fn forgejo_signals(pull: &FjPull, status: Option<&FjStatus>, reviews: &[FjReview],
                          protections: Result<&[FjProtection], ()>) -> MergeSignals;
   fn base_url(host: &str) -> String; // "https://{host}/api/v1"
   fn glob_match(pattern: &str, text: &str) -> bool; // '*' = any run of characters
   ```

   Make `FjPull`, `FjStatus`, `FjReview`, `FjProtection` `pub` (with doc
   comments) since `forgejo_signals` and `protection_for` take them.
   - URLs, paging, error mapping and the mapping to `PullRequest` exactly as
     reference.md › "Requests", "Error mapping" and "Mapping to
     `PullRequest`". `list_prs` returns `Err(AppError::new("usage", "<id> isn't host/owner/name"))`
     when `github::split_repo` fails.
   - `list_prs` order: pulls pages; then protections (one call; any non-2xx
     → `Err(())`); then, four pull requests at a time (`Arc<Semaphore>` of
     4 and a `JoinSet`, as `checker::list_all` does), each pull request's
     status and reviews (a non-2xx or unparsable answer → `None` / empty);
     then build each `PullRequest` with `merge = merge_info(&forgejo_signals(…))`.
     Keep the pulls' order in the result (spawn with the pull's index and
     sort the joined results by it).
   - Each `JoinSet` task owns what it uses (`'static`): an
     `Arc::clone(&http)`, the semaphore `Arc`, the two URLs as `String`s
     and the token as `Option<String>`; it awaits the status call, then the
     reviews call, and returns `(index, Option<FjStatus>, Vec<FjReview>)`.

### Tests (in `forgejo.rs`, with `FakeHttp`; fixtures are the JSON in reference.md › "Fixtures")

- `probe_finds_forgejo_by_version`: 200 `{"version":"17.0.0"}` → `Forgejo`.
- `probe_rejects_other_answers`: 404, 410, and 200 `<html>` → `NotForgejo`.
- `probe_reports_unreachable`: `fail(url)` → `Unreachable`.
- `login_reads_user_and_sends_token`: 200 `{"login":"bob"}` → `"bob"`; the
  recorded call has `Some(token)`.
- `login_rejected_token_is_forge_auth`: 401 → code `forge_auth`, message
  contains `rejected the token` and doesn't contain the token.
- `list_prs_maps_fixture`: the four fixtures → one `PullRequest` with every
  field of the mapping table, `review_requests[0].login == Some("Gusted")`,
  and `merge` = `{Failing, [Failing, AwaitingApproval], {3,2,1,0}}`.
- `list_prs_reads_two_pages_at_most`: page 1 has 50 pulls, page 2 has 3 →
  53 results and exactly two `pulls?` calls; page 1 with 10 → one call.
- `list_prs_sends_token_on_every_call` and `list_prs_without_token_sends_none`.
- `list_prs_errors`: pulls 401 without token → `forge_auth` with `needs a token`;
  429 → `forge_rate_limited`; 500 `{"message":"boom"}` → `forge_failed`
  with message `git.example.com: HTTP 500: boom`.
- `failed_status_call_leaves_checks_empty`: status 500 → `checks.total == 0`, no error.
- `unreadable_protections_require_one_approval`: protections 403, no
  approvals → reasons include `AwaitingApproval`; not `Behind` even when
  `merge_base` differs from `base.sha`.
- `protection_for_matches_exact_then_glob`: `forgejo` exact; `release/*`
  matches `release/1.0`; `*` matches anything; no match → `None`;
  `rule_name` empty uses `branch_name`.
- `forgejo_signals_uses_latest_official_review_per_user`: user A
  `REQUEST_CHANGES` (id 1) then `APPROVED` (id 3) → not changes requested;
  stale, dismissed and non-official reviews are ignored; `COMMENT` and
  `REQUEST_REVIEW` are ignored.
- `forgejo_signals_behind_only_when_protection_blocks_outdated`.
- `no_protection_rule_means_no_approval_required`: readable, empty rules,
  zero approvals → review `NotRequired`; one approval → `Approved`.
- `forgejo_status_values_map_to_checks`: `success`, `warning`, `skipped` →
  passed; `failure`, `error` → failed; `pending` → pending.
- `mergeable_false_is_conflicting`.

### Acceptance

- No function in `forgejo.rs` runs a process or calls `reqwest` directly;
  only `http.rs` uses `reqwest`.
- Tokens appear only in `Authorization` headers.
- Verify (Rust tasks) passes for both feature sets.

---

## T3. Hosts, tokens, `forges` setting, Forgejo review start

**Depends on:** T1. **Parallel with:** T2. **Size:** medium.

### Files

- `desktop/src-tauri/src/reviews/forge.rs` (replace the stub)
- `desktop/src-tauri/src/reviews/tokens.rs` (replace the stub)
- `desktop/src-tauri/src/reviews/store.rs`
- `desktop/src-tauri/src/reviews/plan.rs`
- `desktop/src-tauri/src/reviews/view.rs` (only `is_review_of` and new tests)

### What to do

1. **`forge.rs`**: `ForgeKind`, `ForgeChoice`, `host_of`, `resolve_kind`,
   `needs_probe`, `is_host` exactly as in reference.md › "Hosts and
   tokens". `resolve_kind`: explicit choice wins (`Github`/`Forgejo` even
   for github.com); else `github.com` or a member of `gh_hosts` → `Github`;
   else `probed == Some(true)` → `Forgejo`; else `None`.
2. **`tokens.rs`**: `TokenSource`, `TOKENS_FILE`, `TokenStore` exactly as in
   reference.md. File format `{"version":1,"tokens":{…}}` (private
   `TokenFile` struct, `#[serde(default)]`). Write like `store::write`:
   create the directory, write `forge-tokens.json.tmp` with
   `std::fs::OpenOptions` and `std::os::unix::fs::OpenOptionsExt::mode(0o600)`,
   then rename over the file. A file whose `version` > 1 is read but
   `set` returns `AppError::new("state_newer", "forge-tokens.json was written by a newer version of the app; it isn't changed")`.
   `set` with a token containing whitespace after trimming its ends →
   `setting_invalid` (`a token has no spaces`); `set` on a host failing
   `forge::is_host` → `setting_invalid`. With no path, `set` returns
   `AppError::new("internal", "no place to keep tokens")`.
   `TokenStore` must be `Send + Sync`.
3. **`store.rs`**: add `ForgeHost`, `ForgeSettings` (with `choice` and
   `set_choice`) as in reference.md › "`desktop.json`", and
   `pub forges: ForgeSettings` as the last field of `Settings`.
4. **`plan.rs`**: add `#[serde(default)] pub forge: ForgeKind` as the last
   field of `ReviewPlan`; `plan()` sets `ForgeKind::Github`. In
   `start_args`, for `ForgeKind::Forgejo` build
   `["review","start","--json","--name",<name>,"<repo>@<head>", …profiles…]`
   then the same `--` part as today; GitHub stays byte-for-byte as now.
   An empty name is still refused for both.
5. **`view.rs`**: change `is_review_of` to the three cases in reference.md
   › "Starting reviews" (record with `pr`; record without `pr`: same repo
   and `workspace_id` starts with `names::review_prefix(repo, number)`;
   no record: unchanged).

Mark items nothing outside tests uses yet with `#[allow(dead_code)] // used by T4`
(for example `TokenStore`, `ForgeSettings::choice`, `resolve_kind`).

### Tests

`forge.rs`:
- `host_of_keeps_the_port` (`git.example.com:3000/team/svc` → `git.example.com:3000`; `"github.com"` alone → `None`).
- `explicit_choice_wins`, `github_com_and_gh_hosts_are_github`,
  `probed_forgejo_is_forgejo`, `unprobed_or_rejected_host_has_no_forge`.
- `needs_probe_only_for_auto_unknown_hosts`.
- `is_host_accepts_names_and_ports` / `is_host_rejects_paths_users_spaces_and_empty`.
- `forge_choice_serializes_lowercase` (`"auto"`, `"github"`, `"forgejo"`).

`tokens.rs` (use `tempfile::tempdir()` and `TokenStore::with_env`):
- `env_name_upper_cases_and_replaces_punctuation` (`git.example.com:3000` → `WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM_3000`).
- `env_wins_over_file`; `blank_env_is_ignored`.
- `set_then_token_reads_from_file`; `empty_token_removes_host`.
- `file_is_private` (mode `0o600` via `std::os::unix::fs::PermissionsExt`).
- `token_with_spaces_is_refused`; `bad_host_is_refused`.
- `newer_file_is_not_written` (state_newer, file bytes unchanged).
- `missing_or_unreadable_file_has_no_tokens`.

`store.rs`:
- `file_without_forges_loads_defaults` (the JSON from `missing_keys_take_defaults`).
- `set_choice_auto_removes_host`.
- `forges_serialize_as_in_reference` (`{"hosts":{"git.example.com":{"kind":"forgejo"}}}`).

`plan.rs`:
- `plan_defaults_to_github` and `plan_json_without_forge_is_github`.
- `forgejo_plan_starts_at_head_commit_with_name` (exact argument vector,
  including a profile and `-- --model=sonnet`).
- `github_start_args_are_unchanged` (the existing tests still pass).

`view.rs`:
- `record_without_pr_matches_by_name_prefix`: a `recorded()` entry with
  `pr: None` and id `review-buff-bear-42-3f2a9c1-d8efb24f` is a review of
  `REPO#42`, not of `REPO#41`, and not of another repo.

### Acceptance

- Old `desktop.json` files and old serialized `ReviewPlan`s load unchanged.
- No token is ever in a returned `AppError` message.
- Verify (Rust tasks) passes.

---

## T4. Checker and view: per-host check, `hosts`, commands

**Depends on:** T2, T3. **Parallel with:** nothing. **Size:** large.

### Files

- `desktop/src-tauri/src/reviews/checker.rs`
- `desktop/src-tauri/src/reviews/view.rs`
- `desktop/src-tauri/src/reviews/mod.rs`
- `desktop/src-tauri/src/lib.rs`
- `desktop/src-tauri/src/online/server.rs` (only `start_background`)
- `desktop/src-tauri/src/online/dispatch.rs`
- any file with a `// used by T4` attribute (remove the attribute only)

### What to do

1. **`Deps`** gains `pub http: Arc<dyn Http>`. `lib.rs` and
   `online/server.rs` pass `Arc::new(ReqwestHttp::new())`.
2. **`CheckState`** (view.rs) gains:
   ```rust
   /// Probe answers kept for the app's run: true = Forgejo.
   pub probed: BTreeMap<String, bool>,
   /// What the last check decided per host.
   pub forges: BTreeMap<String, HostState>,
   ```
   ```rust
   #[derive(Debug, Clone, Default)]
   pub struct HostState { pub kind: Option<ForgeKind>, pub detected: bool,
                          pub token: Option<TokenSource>, pub error: Option<AppError> }
   ```
   `hosts` keeps its meaning (login per host) and now holds Forgejo logins too.
3. **`Checker::check_steps`** becomes, in order:
   1. `load_cached` (an error ends the check as today).
   2. `github::signed_in_hosts`: `Ok(map)` → `gh_hosts = map`;
      `Err(e)` → `gh_error = Some(e)`, `gh_hosts` empty. Never return early.
   3. For each distinct `forge::host_of` of the cached ids: `choice =
      state.settings.forges.choice(host)`; if `forge::needs_probe(..)` and
      `probed` has no entry, `forgejo::probe(&*deps.http, host)` and store
      `Forgejo → true`, `NotForgejo → false` (`Unreachable` stores nothing);
      `kind = forge::resolve_kind(host, choice, &gh_host_set, probed.get(host).copied())`;
      `detected = kind == Some(Forgejo) && choice == Auto`.
   4. Per host error and login:
      - `Github`: login `gh_hosts.get(host)`; error: `gh_missing` from
        `gh_error` as is; otherwise, when there's no login,
        `AppError::new("gh_auth", format!("gh isn't signed in to {host}. Run gh auth login --hostname {host} in a terminal, then Check now."))`.
      - `Forgejo`: `TokenStore::beside(deps.store.path()).token(host)`; with a
        token, `forgejo::login` → login, or its error; without, no login,
        no error. Record the token source.
      - `None` and the host has a watched repo: `forge_unknown` when
        `probed == Some(false)`, `forge_failed` (`{host}: no answer from https://{host}/api/v1/version`)
        when the probe was unreachable; else no error.
   5. Watched repos (`view::watched_repos`): cached, `watch` on, host kind
      `Some`. Repos to list: watched and their host has no error.
   6. `list_all` dispatches per kind: `Github` → `github::list_prs`;
      `Forgejo` → `forgejo::list_prs(Arc::clone(&http), repo, token.as_deref())` (clone the
      `Arc<dyn Http>` and the token `String` into each task).
   7. Results, warnings, pruning as today; `rate_limited` also for
      `forge_rate_limited`. Store `hosts`, `forges`.
   8. Top-level `error`: `gh_error` (the existing generic message) only
      when there is at least one watched repo and every watched repo's
      host kind is `Github`; else `None`. A host error is not repeated in
      `warnings`.
   9. `ws list`, removed reviews, compares, auto-review as today. In
      `auto_review`, set `plan.forge` to the repo's host kind before
      `start_review`. Compares (`github::new_commits`) only for `Github` repos.
4. **New `Checker` methods**:
   ```rust
   /// The forge the last check found for a repo's host.
   pub fn forge_of(&self, repo: &str) -> Option<ForgeKind>;
   /// Forgets a host's probe answer and error, after its setting or token changed.
   pub fn forget_host(&self, host: &str);
   ```
5. **`view::build`**: `ReviewsView.hosts: Vec<HostView>` (one per distinct
   cached host, sorted; fields and rules in reference.md › "The view";
   `choice` from `state.settings.forges`; `tokenEnv` only for Forgejo),
   `RepoView.forge`, `RepoView.github = forge == Some(Github)`,
   `PrView.forge` (the repo's host kind, `Github` if unknown). `HostView`
   is `#[serde(rename_all = "camelCase")]`.
6. **`mod.rs`** commands, each `#[cfg_attr(feature = "desktop", tauri::command)]`:
   ```rust
   pub fn reviews_set_forge(state: State<'_, AppState>, host: String, kind: ForgeChoice) -> CmdResult<ReviewsView>;
   pub fn reviews_set_token(state: State<'_, AppState>, host: String, token: String) -> CmdResult<ReviewsView>;
   ```
   Both check `forge::is_host` (`setting_invalid`, `"<host>" isn't a host name`),
   save (`store.update(|d| d.settings.forges.set_choice(..))` /
   `TokenStore::beside(state.store.path()).set(..)`), call
   `forget_host` and `check_now`, and return `view(&state)`.
   `reviews_plan`: set `plan.forge` from `forge_of(&repo)` (default `Github`).
   `review_comment_plan`: if `forge_of(&repo)` isn't `Some(Github)`, return
   `AppError::new("forge_unsupported", "Posting findings is GitHub-only for now.")`.
7. **Register** both commands in `lib.rs` `generate_handler!` (after
   `reviews::reviews_set_watch`) and in `online/dispatch.rs`: the
   `COMMANDS` list (after `"reviews_set_watch"`) and the `match`
   (`args!(args, { host: String, kind: reviews::forge::ForgeChoice })`,
   `args!(args, { host: String, token: String })`).
8. Remove every `#[allow(dead_code)] // used by T4`.

### Tests (checker.rs; extend the existing fakes)

Change `fake_programs(gh, ws_review)` into a call to a new
`fake_programs_cached(gh, ws_review, cache_json)`, keeping today's cache
JSON as the default. `deps()` gets `http: Arc::new(FakeHttp::new())`;
add `deps_with(dir, auto_review, http: FakeHttp, desktop_json: Option<PathBuf>)`.
Forgejo fixtures: the four in reference.md › "Fixtures", served for
`git.example.com/team/svc`, plus `/version` → `{"version":"17.0.0"}`.

- `forgejo_repo_is_listed_with_merge_status`: cached
  `github.com/bobcob7/buff-bear` and `git.example.com/team/svc`, both
  watched → two pull requests; the Forgejo one has `forge` `Forgejo` and
  `merge.status` `Failing`; `hosts` has both hosts.
- `signed_out_gh_does_not_hide_forgejo`: `gh auth status` prints
  `{"hosts":{}}` and exits 1 → `view.error` is `None`; host `github.com`
  has error code `gh_auth` naming `--hostname github.com`; the Forgejo pull
  request is listed.
- `all_github_signed_out_keeps_the_top_level_error` (the existing
  `check_reports_gh_signed_out` must still pass).
- `forgejo_token_gives_login_and_mine`: `forge-tokens.json` beside a temp
  `desktop.json` holds a token; `/user` → `{"login":"wetneb"}` → host
  `login` `wetneb`, `token` `File`, and the pull request has `mine: true`.
- `rejected_token_is_a_host_error_and_skips_its_repos`: `/user` 401 →
  host error `forge_auth`; no Forgejo pull requests; GitHub ones still listed.
- `unknown_host_cannot_be_watched`: `/version` 404 → host error
  `forge_unknown`, repo `forge: None`, not in `watched_repos`.
- `explicit_forge_skips_the_probe`: `settings.forges` says `forgejo` → no
  `/version` call recorded.
- `probe_answer_is_kept_until_forgotten`: two checks → one `/version`
  call; after `forget_host`, a third check probes again.
- `forge_rate_limit_backs_off`: pulls 429 → `backoff == 1`.
- `auto_review_starts_forgejo_review_at_head`: auto-review on for the
  Forgejo repo → `ws.log` contains
  `review start --json --name review-svc-15262-8db6637 git.example.com/team/svc@8db6637666e157ddd3af5d5033ac7edc347f2c10`.
- `forge_of_names_the_hosts_forge`.

view.rs:
- `hosts_list_every_cached_host_sorted_with_token_env_for_forgejo`.
- `repo_view_github_flag_follows_forge`.

dispatch.rs: the existing `commands_match_generate_handler` test passes.

### Acceptance

- With only GitHub repos watched, the view is what it was before T4 plus
  `hosts`, `forge` fields and `merge`.
- A failing host never hides another host's pull requests.
- `grep -rn "used by T4" desktop/src-tauri/src` prints nothing.
- Verify (Rust tasks) passes for both feature sets.

---

## T5. Frontend data: API types, filters, merge badge, Tauri mock

**Depends on:** T4. **Parallel with:** nothing. **Size:** medium. First UI
task: it carries the Tauri mock used for screenshots.

### Files

- `desktop/src/api.ts`
- `desktop/src/features/reviews/reviewsSlice.ts`, `reviewsSlice.test.ts`
- `desktop/src/features/reviews/groups.ts`, `groups.test.ts`
- `desktop/src/features/reviews/MergeBadge.tsx` (new),
  `MergeBadge.module.css` (new), `MergeBadge.test.tsx` (new)
- `desktop/src/features/reviews/RepoSlots.tsx`, `RepoReviewsCard.tsx`
- `desktop/src/test/fixtures.ts`
- `desktop/test/tauri-mock.js`

### What to do

1. **`api.ts`**: the types and fields in reference.md › "Commands"
   (TypeScript block): `ForgeKind`, `ForgeChoice`, `MergeStatus`,
   `Checks`, `MergeInfo`, `HostView`; `PullRequest.forge`,
   `PullRequest.merge`; `WatchRepo.forge`; `ReviewsView.hosts`;
   `ReviewPlan.forge: ForgeKind`. Add `reviewsSetForge` and
   `reviewsSetToken` to the API object next to `reviewsSetWatch`.
   `Settings` doesn't change.
2. **`groups.ts`**: `Involves`, `PrFilters`, `noFilters`,
   `matchesFilters`, `authorsOf`, `activeFilterCount` and the new
   `groupPrs` third parameter, as in reference.md › "Filters". Also:
   - `mergeLabels: Readonly<Record<MergeStatus, string>>` and
     `mergeTones: Readonly<Record<MergeStatus, "success" | "neutral" | "error" | "warning" | "info">>`
     from the labels table;
   - `checksSummary(checks: Checks): string | null` → `null` when
     `total === 0`, else the non-zero parts of
     `"<passed> passed, <failed> failed, <pending> running"` joined by `", "`;
   - `watchedCount`: `r.watch && r.forge !== null`;
   - `commandPreview`: for `plan.forge === "forgejo"`, replace
     `"--pr", String(plan.number), plan.repo` with
     `"--name", plan.name, \`${plan.repo}@${shortSha(plan.head)}\``.
3. **`reviewsSlice.ts`**: `filters: PrFilters` in state (initial
   `noFilters`), reducers `authorFiltered`, `involvesFiltered`,
   `mergeFiltered`, `filtersCleared`; thunks
   `setForge({host, kind})` and `setToken({host, token})` like `setWatch`
   (call the API, `setView` on fulfilled, notice on error).
4. **`MergeBadge.tsx`**:
   ```tsx
   export interface MergeBadgeProps { readonly merge: MergeInfo; readonly compact?: boolean }
   export const MergeBadge = ({ merge, compact = false }: MergeBadgeProps): JSX.Element
   ```
   A `<span className={styles.root} data-tone={mergeTones[status]} title={…}>` with
   the label (`mergeLabels[merge.status]`). `title`: the labels of all
   `reasons` joined by `" · "` (or the status label when `reasons` is
   empty), then `" · "` and `checksSummary` when not null. `compact`
   uses a smaller font (`styles.compact`). Colours come from the
   variables in `src/index.css`: `success` → `var(--status-done)`,
   `error` → `var(--danger)`, `warning` → `var(--status-needs-input)`,
   `info` → `var(--status-working)`, `neutral` → `var(--status-unknown)`;
   set `--tone` per `data-tone` as `PrDot.module.css` sets
   `--status-color`, and use it for the text and a 1px border.
5. **`RepoSlots.tsx`** (`isGitHubRepo`: rename nothing, but use
   `known.forge !== null` and `r.forge !== null`) and
   **`RepoReviewsCard.tsx`** (`repo.forge === null` instead of `!repo.github`).
6. **`fixtures.ts`**: `pullRequest()` gains `forge: "github"` and
   `merge: { status: "ready", reasons: [], checks: { total: 2, passed: 2, failed: 0, pending: 0 } }`;
   `reviewsView()` repos gain `forge` (`"github"` / `null`) and
   `hosts: [hostView({ host: "github.com", forge: "github", login: "bobcob7" }), hostView({ host: "gitlab.example.com", forge: null })]`;
   add `export const hostView = (overrides: Partial<HostView> = {}): HostView`
   (defaults: `host "github.com"`, `forge "github"`, `choice "auto"`,
   `detected false`, `login null`, `token null`, `tokenEnv null`,
   `error null`); `reviewPlan()` gains `forge: "github"`; `fakeApi` gains
   `reviewsSetForge` and `reviewsSetToken` (`vi.fn(async () => reviewsView())`).
   Fix every other literal `npm run typecheck` flags in `*.test.ts(x)` files
   by adding the new fields (list them in your report).
7. **`tauri-mock.js`** (screenshot data): repos and hosts for
   `github.com/bobcob7/buff-bear` (github), `ghe.example.com/team/api`
   (github, `login: "bob"`), `git.example.com/team/svc` (forgejo,
   `detected: true`, `token: "env"`,
   `tokenEnv: "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM"`, `login: "bob"`), and
   `gitlab.example.com/team/web` (forge null, error `forge_unknown`); at
   least seven pull requests across them covering `ready`, `draft`,
   `conflicting`, `failing`, `behind`, `awaitingApproval`, `pending`, with
   three authors, one `mine: true` and two `requested: true`; handlers
   `reviews_set_forge` (updates that host's `choice`, and `forge` unless
   `auto`) and `reviews_set_token` (sets `token` to `"file"`, or `null`
   for `""`), both returning the view. Keep every existing handler.

### Tests

`groups.test.ts`:
- `matchesFilters` by author (case-insensitive), by each `involves`, by
  merge status; `noFilters` matches everything.
- `groupPrs` with filters drops non-matching pull requests and empty groups;
  without the third argument behaves as before.
- `authorsOf` returns sorted distinct logins.
- `activeFilterCount` counts author, involves ≠ anyone, merge.
- `checksSummary` (`null` for none; `"5 passed, 1 failed, 1 running"`; `"2 passed"`).
- `commandPreview` for a Forgejo plan shows `--name … repo@3f2a9c1`.
- `watchedCount` ignores watched repos with `forge: null`.

`reviewsSlice.test.ts`: each filter reducer and `filtersCleared`;
`setForge.fulfilled` replaces the view.

`MergeBadge.test.tsx`: renders the label for `ready`, `failing`, `behind`;
`title` lists every reason and the checks summary; `data-tone` is `error`
for `conflicting`.

### Acceptance

- Typecheck and every existing test pass; no component outside the files
  listed changes.
- Opening the app with `test/tauri-mock.js` shows pull requests from four
  hosts (checked by the orchestrator's screenshots after T6).
- Verify (TypeScript tasks) passes.

---

## T6. Frontend UI: filter bar, badges, Settings › Forges

**Depends on:** T5. **Parallel with:** nothing. **Size:** medium.

### Files

- `desktop/src/features/reviews/ReviewsList.tsx`, `ReviewsList.module.css`, `ReviewsList.test.tsx`
- `desktop/src/features/reviews/ReviewsPane.tsx`, `ReviewsPane.test.tsx`
- `desktop/src/features/reviews/WatchedReposDialog.tsx`
- `desktop/src/features/settings/ForgesGroup.tsx` (new), `ForgesGroup.module.css` (new), `ForgesGroup.test.tsx` (new)
- `desktop/src/features/settings/SettingsPane.tsx`, `SettingsList.tsx`, `settingsSlice.ts` (the `SettingsGroup` union only)

### What to do

1. **ReviewsList filter bar**, under the search box: a row
   (`styles.filters`, wraps on narrow widths) of three MUI `TextField
   select size="small"` controls and a Clear button:
   - **Author** (`label="Author"`): "All authors" (value `""` → `null`),
     then `authorsOf(view.prs)`.
   - **Involves** (`label="Involves"`): Anyone (`anyone`), "Me: mine or
     requested" (`meAny`), "Opened by me" (`mine`), "Review requested from
     me" (`requested`).
   - **Merge** (`label="Merge status"`): "Any status" (`""` → `null`),
     then every `MergeStatus` with `mergeLabels`, in precedence order with
     `ready` first.
   - **Clear filters** button, shown only when `activeFilterCount(filters) > 0`.
   Pass `filters` to `groupPrs(view.prs, filter, filters)` and use the same
   for `visibleOrder`. When `involves !== "anyone"` and some host with
   pull requests has `login === null`, show under the bar
   `<p role="status">` with "Mine and requested don't include
   <host, host>: no login there." (hosts joined by ", ").
2. **ReviewsList rows**: after the age, `<MergeBadge merge={pr.merge} compact />`.
   The warnings above the groups also list each host error:
   `${host.host}: ${host.error.message}` for hosts with an error, and the
   per-repo warnings use `r.forge !== null` instead of `r.github`.
3. **ReviewsPane**: in `PrHeader`'s meta row, `<MergeBadge merge={pr.merge} />`
   right after the `+/−` span; the "Open on GitHub" button reads "Open on
   Forgejo" when `pr.forge === "forgejo"`. `EmptyWatch` says "your watched
   repos" instead of "your watched GitHub repos".
4. **WatchedReposDialog**: a repo with `forge === null` shows the note
   "Choose its forge in Settings › Forges" (instead of "GitHub repos
   only"); other rows unchanged.
5. **Settings › Forges** (`ForgesGroup`), listed after "Reviews" in
   `SettingsList` (`{ id: "forges", title: "Forges" }`), rendered by
   `SettingsPane` for `group === "forges"` (before the `settings === null`
   check, like `repos`). It reads `state.reviews.view?.hosts` (dispatch
   `fetchReviews()` on mount when the view is `null`) and shows a table
   `aria-label="Forges"` with one row per host:
   - host (monospace);
   - a `select` `aria-label={\`Forge for ${host}\`}` with Auto (detected:
     Forgejo/GitHub/none), GitHub, Forgejo → `setForge({host, kind})`;
   - for Forgejo hosts: token status ("from $<tokenEnv>" for `env`,
     "saved" for `file`, "none" otherwise), a "Set token" button opening a
     dialog with a password `TextField` (`label="Token for <host>"`) and
     Save/Cancel → `setToken({host, token})`; and "Remove token" when
     `token === "file"` → `setToken({host, token: ""})`. The token value
     is kept only in the dialog's local state and cleared on close;
   - the host's `login` ("signed in as bob") or its `error.message` in
     error colour.
   A caption under the table: "GitHub hosts use gh's login. Forgejo
   tokens come from WS_FORGEJO_TOKEN_<HOST> or from the token saved here,
   which stays on this machine and is never shown again."

### Tests

`ReviewsList.test.tsx`:
- "filters by author": choosing `carol` leaves only #17.
- "filters to pull requests requested from me".
- "filters by merge status" (a `failing` pull request among `ready` ones).
- "Clear filters resets every filter" (button disappears afterwards).
- "says which hosts have no login when filtering by me".
- "shows a merge badge on each row" (the label text is in the row).
- "lists host errors above the groups".

`ReviewsPane.test.tsx`:
- "shows the merge status in the header".
- "opens Forgejo pull requests on Forgejo" (button name).

`ForgesGroup.test.tsx` (store from `testStore()` with
`fetchReviews.fulfilled(reviewsView({ hosts: [...] }), "req")`, `fakeApi`):
- "lists every host with its forge choice".
- "changing the forge calls reviewsSetForge" with `("git.example.com", "github")`.
- "saving a token calls reviewsSetToken and clears the field".
- "Remove token calls reviewsSetToken with an empty token".
- "shows a host's error".

### Acceptance

- At 375px wide the filter bar wraps with no horizontal scroll and no
  clipped select (checked in the orchestrator's screenshots).
- No token is ever kept in Redux.
- Verify (TypeScript tasks) passes.
