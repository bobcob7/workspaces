//! The background check of watched repos, and auto-review (ADR 0035).

use super::auto;
use super::github::{self, PullRequest};
use super::plan::{self, ReviewPlan};
use super::profiles;
use super::store::{AutoStarted, Store};
use super::view::{self, CheckState, CompareKey, RepoCheck};
use crate::commands::{self, Created};
use crate::model::Entry;
use crate::runner::{AppError, Runner};
use serde::Deserialize;
use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::sync::{Notify, Semaphore};
use tokio::task::JoinSet;

/// How often the task wakes to see whether a check is due.
const WAKE: Duration = Duration::from_secs(30);
/// How many repos are checked at once.
const CONCURRENCY: usize = 4;
/// The longest a rate limit stretches the wait between checks.
const MAX_BACKOFF: Duration = Duration::from_secs(60 * 60);

/// The wait after a check: the interval, doubled for each check in a row
/// that hit a rate limit, but never stretched past an hour.
pub fn next_delay(interval_minutes: u32, backoff: u32) -> Duration {
    let interval = Duration::from_secs(u64::from(interval_minutes) * 60);
    if backoff == 0 {
        return interval;
    }
    let stretched = interval.saturating_mul(1 << backoff.min(16));
    stretched.min(MAX_BACKOFF.max(interval))
}

#[derive(Deserialize)]
struct CacheList {
    repos: Vec<CacheRepo>,
}

#[derive(Deserialize)]
struct CacheRepo {
    id: String,
}

/// What the checker needs to run commands and keep state.
pub struct Deps {
    pub runner: Runner,
    pub store: Arc<Store>,
    pub jobs_dir: Option<PathBuf>,
}

#[derive(Default)]
pub struct Checker {
    state: Mutex<CheckState>,
    entries: Mutex<Vec<Entry>>,
    /// Names of reviews whose `ws review start` is running, so starting one twice
    /// at once starts it once: `ws` no longer refuses a name used twice
    /// (ADR 0036).
    starting: Mutex<BTreeSet<String>>,
    forced: AtomicBool,
    wake: Notify,
    running: tokio::sync::Mutex<()>,
}

fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| i64::try_from(d.as_millis()).unwrap_or(i64::MAX))
}

impl Checker {
    /// Asks for a check as soon as the current one, if any, is done.
    pub fn check_now(&self) {
        self.forced.store(true, Ordering::SeqCst);
        self.wake.notify_one();
    }

    /// Keeps the latest agent listing, so the view shows review status as
    /// fresh as the Agents poll.
    pub fn set_entries(&self, entries: Vec<Entry>) {
        if let Ok(mut current) = self.entries.lock() {
            *current = entries;
        }
    }

    /// Starts a review with `ws review start`, unless the latest listing
    /// has a workspace made from its name or recorded as reviewing its pull
    /// request at its head, or one is being started.
    pub async fn start_review(
        &self,
        runner: &Runner,
        plan: &ReviewPlan,
    ) -> Result<Started, AppError> {
        let args = plan::start_args(plan)?;
        if let Some(id) = self.existing(plan) {
            return Ok(Started { id, already: true });
        }
        let Some(_claim) = Claim::take(&self.starting, &plan.name) else {
            return Ok(Started {
                id: plan.name.clone(),
                already: true,
            });
        };
        let created = runner.ws_json_as_given::<Created>(&args).await?;
        Ok(Started {
            id: created.id,
            already: false,
        })
    }

    /// The listed workspace `ws` made from the plan's `--name`, or whose
    /// review record has the plan's pull request and head, if any.
    fn existing(&self, plan: &ReviewPlan) -> Option<String> {
        let entries = self.entries.lock().ok()?;
        entries
            .iter()
            .find(|e| {
                let named = e
                    .workspace_id
                    .as_deref()
                    .is_some_and(|id| super::names::is_named(id, &plan.name));
                let recorded = e.review.as_ref().is_some_and(|r| {
                    r.repo == plan.repo
                        && r.pr.as_ref().is_some_and(|p| p.number == plan.number)
                        && super::names::same_commit(&r.git_ref, &plan.head)
                });
                named || recorded
            })
            .and_then(|e| e.workspace_id.clone())
    }

    pub fn view(&self, store: &Store) -> view::ReviewsView {
        let entries = self.entries.lock().map(|e| e.clone()).unwrap_or_default();
        let state = self.state.lock();
        match state {
            Ok(check) => view::build(&check, &entries, &store.get()),
            Err(_) => view::build(&CheckState::default(), &entries, &store.get()),
        }
    }

    fn with_state<T>(&self, f: impl FnOnce(&mut CheckState) -> T) -> Option<T> {
        self.state.lock().ok().map(|mut s| f(&mut s))
    }

    /// The latest agent listing.
    pub fn entries(&self) -> Vec<Entry> {
        self.entries.lock().map(|e| e.clone()).unwrap_or_default()
    }

    /// The login `gh` is signed in as on a host, as last checked.
    pub fn login(&self, host: &str) -> Option<String> {
        self.with_state(|s| s.hosts.get(host).cloned()).flatten()
    }

    /// The pull request as last checked.
    pub fn pull_request(&self, repo: &str, number: u64) -> Option<PullRequest> {
        self.with_state(|s| {
            s.repos
                .get(repo)?
                .prs
                .iter()
                .find(|p| p.number == number)
                .cloned()
        })
        .flatten()
    }

    /// Runs checks until the app exits.
    pub async fn run(self: Arc<Self>, deps: Deps) {
        let mut last: Option<Instant> = None;
        loop {
            let (interval, backoff) = (
                deps.store.get().settings.reviews.check_interval_minutes,
                self.with_state(|s| s.backoff).unwrap_or(0),
            );
            let due = last.map_or(true, |t| t.elapsed() >= next_delay(interval, backoff));
            if self.forced.swap(false, Ordering::SeqCst) || due {
                last = Some(Instant::now());
                self.check(&deps).await;
                continue;
            }
            tokio::select! {
                () = tokio::time::sleep(WAKE) => {}
                () = self.wake.notified() => {}
            }
        }
    }

    /// Re-reads the cached repos, for the Watched repos dialog.
    pub async fn load_cached(&self, runner: &Runner) -> Result<(), AppError> {
        let list: CacheList = runner
            .ws_json(&["cache".to_string(), "list".to_string()])
            .await?;
        self.with_state(|s| s.cached = list.repos.into_iter().map(|r| r.id).collect());
        Ok(())
    }

    /// One check: cached repos, `gh` sign-in, each watched repo's pull
    /// requests, the review workspaces, then auto-review.
    pub async fn check(&self, deps: &Deps) {
        let Ok(_guard) = self.running.try_lock() else {
            return;
        };
        self.with_state(|s| s.checking = true);
        let result = self.check_steps(deps).await;
        self.with_state(|s| {
            s.checking = false;
            s.checked_at_ms = Some(now_ms());
            s.error = result.err();
        });
    }

    async fn check_steps(&self, deps: &Deps) -> Result<(), AppError> {
        self.load_cached(&deps.runner).await?;
        let hosts = github::signed_in_hosts(&deps.runner).await?;
        let state = deps.store.get();
        let watched = self
            .with_state(|s| {
                s.hosts = hosts.clone();
                view::watched_repos(s, &state)
            })
            .unwrap_or_default();
        let results = list_all(&deps.runner, &watched).await;
        let mut warnings = Vec::new();
        let mut rate_limited = false;
        for (repo, result) in &results {
            match result {
                Ok(prs) => {
                    let open: Vec<(u64, &str)> = prs
                        .iter()
                        .map(|p| (p.number, p.head_ref_oid.as_str()))
                        .collect();
                    deps.store.update(|d| d.reviews.prune(repo, &open)).ok();
                }
                Err(err) => {
                    rate_limited |= err.code == "gh_rate_limited";
                    warnings.push(format!(
                        "{}: {}",
                        super::names::repo_name(repo),
                        err.message
                    ));
                }
            }
        }
        let checked_at = now_ms();
        self.with_state(|s| {
            s.repos.retain(|id, _| watched.contains(id));
            for (repo, result) in results {
                let entry = s.repos.entry(repo).or_default();
                match result {
                    Ok(prs) => {
                        *entry = RepoCheck {
                            prs,
                            checked_at_ms: Some(checked_at),
                            error: None,
                        };
                    }
                    Err(err) => entry.error = Some(err.message),
                }
            }
            s.backoff = if rate_limited { s.backoff + 1 } else { 0 };
        });
        let listing = commands::list(&deps.runner, deps.jobs_dir.as_deref()).await?;
        self.set_entries(listing.entries.clone());
        self.compare_out_of_date(&deps.runner, &listing.entries)
            .await;
        warnings.extend(self.auto_review(deps, &listing.entries).await);
        self.with_state(|s| s.warnings = warnings);
        Ok(())
    }

    /// Counts the commits pushed since each out-of-date review, once per
    /// pair of commits.
    async fn compare_out_of_date(&self, runner: &Runner, entries: &[Entry]) {
        let wanted: Vec<CompareKey> = self
            .with_state(|s| {
                s.repos
                    .iter()
                    .flat_map(|(repo, r)| r.prs.iter().map(move |p| (repo, p)))
                    .filter_map(|(repo, pr)| {
                        let sha = view::out_of_date_sha(entries, repo, pr)?;
                        let key = (repo.clone(), sha, pr.head_ref_oid.clone());
                        (!s.compares.contains_key(&key)).then_some(key)
                    })
                    .collect()
            })
            .unwrap_or_default();
        for key in wanted {
            if let Ok(commits) = github::new_commits(runner, &key.0, &key.1, &key.2).await {
                self.with_state(|s| s.compares.insert(key, commits));
            }
        }
    }

    /// Starts the reviews auto-review selects, and records each. Returns a
    /// warning for each that failed to start.
    async fn auto_review(&self, deps: &Deps, entries: &[Entry]) -> Vec<String> {
        let state = deps.store.get();
        let (prs, logins) = self
            .with_state(|s| {
                let watched = view::watched_repos(s, &state);
                let prs: Vec<(String, PullRequest)> = watched
                    .iter()
                    .filter_map(|repo| Some((repo, s.repos.get(repo)?)))
                    .flat_map(|(repo, r)| r.prs.iter().map(move |p| (repo.clone(), p.clone())))
                    .collect();
                (prs, s.hosts.clone())
            })
            .unwrap_or_default();
        let pairs: Vec<(&str, &PullRequest)> = prs.iter().map(|(r, p)| (r.as_str(), p)).collect();
        let selected = auto::select(&pairs, entries, &state, &logins);
        if selected.is_empty() {
            return Vec::new();
        }
        let settings = &state.settings.reviews;
        let choices = if settings.profiles.is_empty() {
            Vec::new()
        } else {
            match profiles::list(&deps.runner)
                .await
                .and_then(|listed| profiles::choices(&listed, &settings.profiles))
            {
                Ok(choices) => choices,
                Err(err) => return vec![format!("auto-review: {}", err.message)],
            }
        };
        let mut warnings = Vec::new();
        for (repo, pr) in selected {
            let plan = plan::plan(repo, pr, settings, choices.clone());
            match self.start_review(&deps.runner, &plan).await {
                Ok(started) if started.already => {}
                Ok(started) => {
                    let record = AutoStarted {
                        repo: repo.to_string(),
                        number: pr.number,
                        head: pr.head_ref_oid.clone(),
                        workspace: started.id,
                    };
                    if let Err(err) = deps.store.update(|d| d.reviews.auto_started.push(record)) {
                        warnings.push(err.message);
                    }
                }
                Err(err) => warnings.push(format!(
                    "auto-review of {}#{}: {}",
                    super::names::repo_name(repo),
                    pr.number,
                    err.message
                )),
            }
        }
        warnings
    }
}

/// Lists each repo's open pull requests, a few at a time.
async fn list_all(
    runner: &Runner,
    repos: &[String],
) -> Vec<(String, Result<Vec<PullRequest>, AppError>)> {
    let permits = Arc::new(Semaphore::new(CONCURRENCY));
    let mut tasks = JoinSet::new();
    for repo in repos {
        let (runner, repo, permits) = (runner.clone(), repo.clone(), permits.clone());
        tasks.spawn(async move {
            let _permit = permits.acquire_owned().await;
            let result = github::list_prs(&runner, &repo).await;
            (repo, result)
        });
    }
    let mut results = Vec::new();
    while let Some(done) = tasks.join_next().await {
        if let Ok(result) = done {
            results.push(result);
        }
    }
    results.sort_by(|a, b| a.0.cmp(&b.0));
    results
}

/// Whether a review was started: `true` when it already was, since the same
/// pull request at the same commit always has the same name (ADR 0032),
/// which its workspace's id starts with (ADR 0036).
#[derive(Debug, Clone, PartialEq, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Started {
    pub id: String,
    pub already: bool,
}

/// A review name held in `Checker::starting` until dropped.
struct Claim<'a> {
    set: &'a Mutex<BTreeSet<String>>,
    name: String,
}

impl<'a> Claim<'a> {
    /// Holds name, or `None` when it's already held.
    fn take(set: &'a Mutex<BTreeSet<String>>, name: &str) -> Option<Self> {
        let mut held = set.lock().ok()?;
        held.insert(name.to_string()).then(|| Claim {
            set,
            name: name.to_string(),
        })
    }
}

impl Drop for Claim<'_> {
    fn drop(&mut self) {
        if let Ok(mut held) = self.set.lock() {
            held.remove(&self.name);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::store::{ProfileSetting, RepoWatch, Runs};
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn next_delay_is_interval_without_backoff() {
        assert_eq!(next_delay(5, 0), Duration::from_secs(300));
    }

    #[test]
    fn next_delay_doubles_per_rate_limited_check() {
        assert_eq!(next_delay(5, 2), Duration::from_secs(1200));
    }

    #[test]
    fn next_delay_backoff_stops_at_an_hour() {
        assert_eq!(next_delay(5, 10), Duration::from_secs(3600));
    }

    #[test]
    fn next_delay_keeps_intervals_over_an_hour() {
        assert_eq!(next_delay(120, 3), Duration::from_secs(7200));
    }

    const PRS: &str = r#"[{"number":42,"title":"Add rate limiting","author":{"login":"alice"},"url":"u","body":"b","isDraft":false,"headRefOid":"3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","headRefName":"feat","baseRefName":"main","additions":1,"deletions":1,"changedFiles":1,"reviewRequests":[],"createdAt":"2026-10-24T09:00:00Z","updatedAt":"2026-10-24T09:00:00Z"}]"#;

    const PROFILES: &str = r#"{"profiles":[{"name":"security","description":"s","when":"always","match":[],"model":"","builtin":true},{"name":"go","description":"g","when":"auto","match":["*.go"],"model":"","builtin":true}],"warnings":[]}"#;

    /// A PATH directory with fake `ws`, `claude`, and `gh` scripts. `ws`
    /// logs its arguments to `ws.log`; `ws_review` is `ws review start`.
    fn fake_programs(gh: &str, ws_review: &str) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        let log = dir.path().join("ws.log");
        let ws = format!(
            r#"echo "$@" >> '{}'
case "$1" in
  cache) echo '{{"repos":[{{"id":"github.com/bobcob7/buff-bear"}},{{"id":"github.com/bobcob7/api"}}]}}' ;;
  list) echo '{{"workspaces":[],"warnings":[]}}' ;;
  review) case "$2" in profiles) echo '{PROFILES}' ;; *) {ws_review} ;; esac ;;
esac"#,
            log.display()
        );
        let scripts = [("ws", ws.as_str()), ("claude", "echo '[]'"), ("gh", gh)];
        for (name, body) in scripts {
            let path = dir.path().join(name);
            std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        dir
    }

    fn gh_script(pr_list: &str) -> String {
        format!(
            r#"case "$1" in
  auth) echo '{{"hosts":{{"github.com":[{{"state":"success","active":true,"login":"bobcob7"}}]}}}}' ;;
  pr) case "$*" in *buff-bear*) {pr_list} ;; *) echo 'GraphQL: API rate limit exceeded for user ID 1.' >&2; exit 1 ;; esac ;;
esac"#
        )
    }

    fn deps(dir: &tempfile::TempDir, auto_review: bool) -> Deps {
        let store = Store::open(None);
        store
            .update(|d| {
                for repo in ["github.com/bobcob7/buff-bear", "github.com/bobcob7/api"] {
                    d.reviews.set_watch(
                        repo,
                        RepoWatch {
                            watch: true,
                            auto_review,
                        },
                    );
                }
            })
            .unwrap();
        Deps {
            runner: Runner::new(dir.path().display().to_string()),
            store: Arc::new(store),
            jobs_dir: None,
        }
    }

    fn ws_log(dir: &tempfile::TempDir) -> String {
        std::fs::read_to_string(dir.path().join("ws.log")).unwrap_or_default()
    }

    #[tokio::test]
    async fn check_lists_watched_pull_requests_and_warns_per_repo() {
        let dir = fake_programs(&gh_script(&format!("echo '{PRS}'")), "exit 1");
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert_eq!(view.error, None);
        assert_eq!(view.prs.len(), 1);
        assert_eq!(
            view.warnings,
            ["api: GraphQL: API rate limit exceeded for user ID 1."]
        );
        assert_eq!(checker.with_state(|s| s.backoff), Some(1));
    }

    #[tokio::test]
    async fn failed_repo_keeps_last_result() {
        let dir = fake_programs(&gh_script(&format!("echo '{PRS}'")), "exit 1");
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        std::fs::write(
            dir.path().join("gh"),
            format!("#!/bin/sh\n{}\n", gh_script("echo 'HTTP 502' >&2; exit 1")),
        )
        .unwrap();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert_eq!(view.prs.len(), 1);
        let repo = view
            .repos
            .iter()
            .find(|r| r.id.ends_with("buff-bear"))
            .unwrap();
        assert_eq!(repo.error.as_deref(), Some("HTTP 502"));
    }

    #[tokio::test]
    async fn check_reports_gh_missing() {
        let dir = fake_programs("exit 0", "exit 1");
        std::fs::remove_file(dir.path().join("gh")).unwrap();
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert_eq!(checker.view(&deps.store).error.unwrap().code, "gh_missing");
    }

    #[tokio::test]
    async fn check_reports_gh_signed_out() {
        let dir = fake_programs(r#"echo '{"hosts":{}}'; exit 1"#, "exit 1");
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert_eq!(checker.view(&deps.store).error.unwrap().code, "gh_auth");
    }

    #[tokio::test]
    async fn auto_review_starts_review_and_records_it() {
        let dir = fake_programs(
            &gh_script(&format!("echo '{PRS}'")),
            r#"echo '{"id":"review-buff-bear-42-3f2a9c1-d8efb24f","session":"s","path":"/w"}'"#,
        );
        let deps = deps(&dir, true);
        Checker::default().check(&deps).await;
        assert!(ws_log(&dir).contains("review start --json --pr 42 github.com/bobcob7/buff-bear\n"));
        assert!(!ws_log(&dir).contains("review profiles"));
        let state = deps.store.get();
        assert_eq!(
            state.reviews.auto_started[0].workspace,
            "review-buff-bear-42-3f2a9c1-d8efb24f"
        );
    }

    #[tokio::test]
    async fn auto_review_passes_changed_profiles() {
        let dir = fake_programs(
            &gh_script(&format!("echo '{PRS}'")),
            r#"echo '{"id":"review-buff-bear-42-3f2a9c1-d8efb24f","session":"s","path":"/w"}'"#,
        );
        let deps = deps(&dir, true);
        deps.store
            .update(|d| {
                d.settings.reviews.profiles.insert(
                    "security".to_string(),
                    ProfileSetting {
                        runs: Runs::Always,
                        model: "opus".to_string(),
                    },
                );
                d.settings.reviews.model = "sonnet".to_string();
            })
            .unwrap();
        Checker::default().check(&deps).await;
        assert!(ws_log(&dir).contains(
            "review start --json --pr 42 github.com/bobcob7/buff-bear --profile security:opus --auto go -- --model=sonnet\n"
        ));
    }

    fn review_plan() -> ReviewPlan {
        plan::plan(
            "github.com/bobcob7/buff-bear",
            &crate::reviews::view::tests::pr(42),
            &crate::reviews::store::ReviewSettings::default(),
            Vec::new(),
        )
    }

    #[tokio::test]
    async fn start_review_finds_review_recorded_at_head() {
        let dir = fake_programs("exit 1", "exit 1");
        let runner = Runner::new(dir.path().display().to_string());
        let checker = Checker::default();
        checker.set_entries(vec![crate::reviews::view::tests::recorded(
            42,
            crate::reviews::view::tests::HEAD,
            crate::model::Status::Working,
            "2026-10-24T10:00:00Z",
        )]);
        let started = checker.start_review(&runner, &review_plan()).await.unwrap();
        assert_eq!(started.id, "careful-look-d8efb24f");
        assert!(started.already);
        assert!(!ws_log(&dir).contains("review start"));
    }

    #[tokio::test]
    async fn start_review_finds_listed_review() {
        let dir = fake_programs("exit 1", "exit 1");
        let runner = Runner::new(dir.path().display().to_string());
        let checker = Checker::default();
        let listed = crate::reviews::view::tests::review(
            42,
            crate::reviews::view::tests::HEAD,
            crate::model::Status::Done,
            "2026-10-24T10:00:00Z",
        );
        checker.set_entries(vec![Entry {
            workspace_id: Some("review-buff-bear-42-3f2a9c1-d8efb24f".to_string()),
            ..listed
        }]);
        let started = checker.start_review(&runner, &review_plan()).await.unwrap();
        assert_eq!(
            started,
            Started {
                id: "review-buff-bear-42-3f2a9c1-d8efb24f".to_string(),
                already: true
            }
        );
        assert!(!ws_log(&dir).contains("review start"));
    }

    #[tokio::test]
    async fn start_review_twice_at_once_starts_it_once() {
        let dir = fake_programs(
            "exit 1",
            r#"sleep 1; echo '{"id":"review-buff-bear-42-3f2a9c1-d8efb24f","session":"s","path":"/w"}'"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let checker = Checker::default();
        let plan = review_plan();
        let (a, b) = tokio::join!(
            checker.start_review(&runner, &plan),
            checker.start_review(&runner, &plan)
        );
        let mut already = [a.unwrap().already, b.unwrap().already];
        already.sort_unstable();
        assert_eq!(already, [false, true]);
        assert_eq!(ws_log(&dir).matches("review start").count(), 1);
    }

    #[tokio::test]
    async fn start_review_can_start_again_after_a_failure() {
        let dir = fake_programs("exit 1", "echo boom >&2; exit 1");
        let runner = Runner::new(dir.path().display().to_string());
        let checker = Checker::default();
        assert!(checker.start_review(&runner, &review_plan()).await.is_err());
        assert!(checker.start_review(&runner, &review_plan()).await.is_err());
        assert_eq!(ws_log(&dir).matches("review start").count(), 2);
    }

    #[tokio::test]
    async fn auto_review_failure_is_a_warning_and_not_recorded() {
        let dir = fake_programs(
            &gh_script(&format!("echo '{PRS}'")),
            r#"echo '{"error":{"code":"claude_untrusted","message":"not trusted"}}' >&2; exit 1"#,
        );
        let deps = deps(&dir, true);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert!(deps.store.get().reviews.auto_started.is_empty());
        assert!(checker
            .view(&deps.store)
            .warnings
            .contains(&"auto-review of buff-bear#42: not trusted".to_string()));
    }

    /// Checks the repo in `WS_DESKTOP_WATCH` (a cached repo id) with the
    /// installed `gh` and `ws`, without auto-review: `cargo test -- --ignored`.
    #[tokio::test]
    #[ignore = "runs the installed gh and ws"]
    async fn check_live() {
        let repo = std::env::var("WS_DESKTOP_WATCH").expect("WS_DESKTOP_WATCH");
        let store = Store::open(None);
        store
            .update(|d| {
                d.reviews.set_watch(
                    &repo,
                    RepoWatch {
                        watch: true,
                        auto_review: false,
                    },
                )
            })
            .unwrap();
        let deps = Deps {
            runner: Runner::new(crate::env::resolve_path()),
            store: Arc::new(store),
            jobs_dir: crate::jobs::jobs_dir(),
        };
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        println!("error: {:?}; warnings: {:?}", view.error, view.warnings);
        for pr in &view.prs {
            println!(
                "{:<44} {:?} out of date: {} reviews: {}",
                pr.key,
                pr.group,
                pr.out_of_date,
                pr.reviews.len()
            );
        }
    }

    #[tokio::test]
    async fn successful_check_prunes_closed_dismissals() {
        let dir = fake_programs(&gh_script("echo '[]'"), "exit 1");
        let deps = deps(&dir, false);
        deps.store
            .update(|d| {
                d.reviews.dismiss(crate::reviews::store::PrHead {
                    repo: "github.com/bobcob7/buff-bear".to_string(),
                    number: 40,
                    head: "abc".to_string(),
                })
            })
            .unwrap();
        Checker::default().check(&deps).await;
        assert!(deps.store.get().reviews.dismissed.is_empty());
    }
}
