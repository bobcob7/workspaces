//! The background check of watched repos, and auto-review (ADR 0035).

use super::auto;
use super::forge::{self, ForgeChoice, ForgeKind};
use super::forgejo::{self, Probe};
use super::github::{self, PullRequest};
use super::hosts::{self, ConfiguredHost};
use super::http::Http;
use super::plan::{self, ReviewPlan};
use super::profiles;
use super::runs;
use super::store::{AutoStarted, Store};
use super::tokens::{TokenSource, TokenStore};
use super::view::{self, CheckState, CompareKey, HostState, RepoCheck};
use crate::commands::{self, Created};
use crate::model::Entry;
use crate::runner::{AppError, Runner};
use serde::Deserialize;
use std::collections::{BTreeMap, BTreeSet};
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
    /// Forgejo's REST API; tests replace it.
    pub http: Arc<dyn Http>,
}

#[derive(Default)]
pub struct Checker {
    state: Mutex<CheckState>,
    entries: Mutex<Vec<Entry>>,
    /// Reviews `ws` removed once finished, from `ws review list`, and any
    /// that left the listing since (ADR 0045).
    removed: Mutex<Vec<Entry>>,
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
    /// A review workspace that leaves the listing is kept as removed until
    /// the next check reads `ws review list`, so its pull request stays
    /// Reviewed meanwhile (ADR 0045).
    pub fn set_entries(&self, entries: Vec<Entry>) {
        let Ok(mut current) = self.entries.lock() else {
            return;
        };
        let gone: Vec<Entry> = current
            .iter()
            .filter(|old| !entries.iter().any(|e| e.workspace_id == old.workspace_id))
            .filter_map(runs::removed_from)
            .collect();
        *current = entries;
        drop(current);
        if let Ok(mut removed) = self.removed.lock() {
            for e in gone {
                if !removed.iter().any(|r| r.workspace_id == e.workspace_id) {
                    removed.push(e);
                }
            }
        }
    }

    fn set_removed(&self, removed: Vec<Entry>) {
        if let Ok(mut current) = self.removed.lock() {
            *current = removed;
        }
    }

    /// The listing with the removed reviews it doesn't have, for the
    /// Reviews view and auto-review.
    pub fn review_entries(&self) -> Vec<Entry> {
        let entries = self.entries();
        let removed = self.removed.lock().map(|r| r.clone()).unwrap_or_default();
        runs::merge(&entries, &removed)
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
        let entries = self.review_entries();
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
        let entries = self.review_entries();
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

    /// The forge the last check found for a repo's host.
    pub fn forge_of(&self, repo: &str) -> Option<ForgeKind> {
        self.with_state(|s| view::repo_forge(&s.forges, repo))
            .flatten()
    }

    /// Forgets a host's probe answer and error, after its setting or token changed.
    pub fn forget_host(&self, host: &str) {
        self.with_state(|s| {
            s.probed.remove(host);
            if let Some(known) = s.forges.get_mut(host) {
                known.error = None;
            }
        });
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
            let due = last.is_none_or(|t| t.elapsed() >= next_delay(interval, backoff));
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
            s.error = match result {
                Ok(top_error) => top_error,
                Err(err) => Some(err),
            };
        });
    }

    async fn check_steps(&self, deps: &Deps) -> Result<Option<AppError>, AppError> {
        let mut warnings = Vec::new();
        self.load_cached(&deps.runner).await?;
        self.read_hosts(deps, &mut warnings).await;
        let (gh_hosts, gh_error) = match github::signed_in_hosts(&deps.runner).await {
            Ok(hosts) => (hosts, None),
            Err(err) => (BTreeMap::new(), Some(err)),
        };
        let unreachable = self.probe_hosts(deps, &gh_hosts).await;
        let decisions = self
            .decide_hosts(deps, &gh_hosts, gh_error.as_ref(), &unreachable)
            .await;
        let state = deps.store.get();
        let watched = self
            .with_state(|s| {
                s.hosts = decisions.logins.clone();
                s.forges = decisions.forges.clone();
                view::watched_repos(s, &state)
            })
            .unwrap_or_default();
        let results = list_all(deps, list_jobs(&watched, &decisions)).await;
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
                    rate_limited |=
                        err.code == "gh_rate_limited" || err.code == "forge_rate_limited";
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
        match runs::removed(&deps.runner).await {
            Ok(removed) => self.set_removed(removed),
            Err(err) => warnings.push(format!("ws review list: {}", err.message)),
        }
        let entries = self.review_entries();
        self.compare_out_of_date(&deps.runner, &entries).await;
        warnings.extend(self.auto_review(deps, &entries).await);
        self.with_state(|s| s.warnings = warnings);
        let all_github = !watched.is_empty()
            && watched
                .iter()
                .all(|repo| self.forge_of(repo) == Some(ForgeKind::Github));
        Ok(gh_error.filter(|_| all_github))
    }

    /// Moves the legacy forge settings into config.toml, then reads the
    /// configured hosts (ADR 0061). A failure is a warning.
    async fn read_hosts(&self, deps: &Deps, warnings: &mut Vec<String>) {
        if let Err(err) = hosts::migrate(&deps.runner, &deps.store).await {
            warnings.push(format!(
                "moving forge settings to config.toml: {}",
                err.message
            ));
        }
        let configured = match hosts::read(&deps.runner).await {
            Ok(list) => list.hosts,
            Err(err) => {
                warnings.push(format!("ws config get hosts: {}", err.message));
                Vec::new()
            }
        };
        self.with_state(|s| s.configured = configured);
    }

    /// config.toml's effective hosts, as the last check read them.
    pub fn configured(&self) -> Vec<ConfiguredHost> {
        self.with_state(|s| s.configured.clone())
            .unwrap_or_default()
    }

    /// Records the hosts just saved in Settings › Forges.
    pub fn set_configured(&self, hosts: Vec<ConfiguredHost>) {
        self.with_state(|s| s.configured = hosts);
    }

    /// Asks each host that needs it whether it runs Forgejo, once per run
    /// (ADR 0060, rule 3). Returns the hosts that gave no answer.
    async fn probe_hosts(
        &self,
        deps: &Deps,
        gh_hosts: &BTreeMap<String, String>,
    ) -> BTreeSet<String> {
        let configured = self.configured();
        let gh_host_set = host_set(gh_hosts);
        let mut unreachable = BTreeSet::new();
        for host in self.known_hosts(&configured) {
            let choice = hosts::choice(&configured, &host);
            if self.is_probed(&host) || !forge::needs_probe(&host, choice, &gh_host_set) {
                continue;
            }
            match forgejo::probe(&*deps.http, &host).await {
                Probe::Forgejo => self.record_probe(&host, true),
                Probe::NotForgejo => self.record_probe(&host, false),
                Probe::Unreachable => {
                    unreachable.insert(host);
                }
            }
        }
        unreachable
    }

    fn is_probed(&self, host: &str) -> bool {
        self.with_state(|s| s.probed.contains_key(host))
            .unwrap_or(true)
    }

    fn record_probe(&self, host: &str, forgejo: bool) {
        self.with_state(|s| s.probed.insert(host.to_string(), forgejo));
    }

    /// The configured hosts in list order, then the cached hosts that
    /// aren't configured, sorted.
    fn known_hosts(&self, configured: &[ConfiguredHost]) -> Vec<String> {
        let cached: BTreeSet<String> = self
            .with_state(|s| {
                s.cached
                    .iter()
                    .filter_map(|id| forge::host_of(id))
                    .map(str::to_string)
                    .collect()
            })
            .unwrap_or_default();
        let mut known: Vec<String> = configured.iter().map(|h| h.name.clone()).collect();
        known.extend(
            cached
                .into_iter()
                .filter(|host| !configured.iter().any(|h| h.name == *host)),
        );
        known
    }

    /// Decides each cached host's forge, login, token and error (ADR 0060).
    async fn decide_hosts(
        &self,
        deps: &Deps,
        gh_hosts: &BTreeMap<String, String>,
        gh_error: Option<&AppError>,
        unreachable: &BTreeSet<String>,
    ) -> Decisions {
        let state = deps.store.get();
        let configured = self.configured();
        let (probes, cached) = self
            .with_state(|s| (s.probed.clone(), s.cached.clone()))
            .unwrap_or_default();
        let gh_host_set = host_set(gh_hosts);
        let watched_hosts: BTreeSet<&str> = cached
            .iter()
            .filter(|id| state.reviews.watch(id).watch)
            .filter_map(|id| forge::host_of(id))
            .collect();
        let tokens = TokenStore::beside(deps.store.path());
        let mut decisions = Decisions {
            logins: gh_hosts.clone(),
            ..Decisions::default()
        };
        for host in self.known_hosts(&configured) {
            let choice = hosts::choice(&configured, &host);
            let probed = probes.get(&host).copied();
            let kind = forge::resolve_kind(&host, choice, &gh_host_set, probed);
            let mut known = HostState {
                kind,
                detected: kind == Some(ForgeKind::Forgejo) && choice == ForgeChoice::Auto,
                ..HostState::default()
            };
            let account = match kind {
                Some(ForgeKind::Github) => {
                    github_login(&host, gh_hosts, gh_error, &mut known).map(|login| (login, None))
                }
                Some(ForgeKind::Forgejo) => {
                    forgejo_login(&*deps.http, &host, tokens.token(&host), &mut known)
                        .await
                        .map(|(login, token)| (login, Some(token)))
                }
                None => {
                    if watched_hosts.contains(host.as_str()) {
                        known.error = unresolved_error(&host, unreachable.contains(&host), probed);
                    }
                    None
                }
            };
            match account {
                Some((login, token)) => {
                    decisions.logins.insert(host.clone(), login);
                    if let Some(token) = token {
                        decisions.tokens.insert(host.clone(), token);
                    }
                }
                None => {
                    decisions.logins.remove(&host);
                }
            }
            decisions.forges.insert(host, known);
        }
        decisions
    }

    /// Counts the commits pushed since each out-of-date review, once per
    /// pair of commits.
    async fn compare_out_of_date(&self, runner: &Runner, entries: &[Entry]) {
        let wanted: Vec<CompareKey> = self
            .with_state(|s| {
                s.repos
                    .iter()
                    .filter(|(repo, _)| {
                        view::repo_forge(&s.forges, repo) == Some(ForgeKind::Github)
                    })
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
            let mut plan = plan::plan(repo, pr, settings, choices.clone());
            plan.forge = self.forge_of(repo).unwrap_or_default();
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

/// What a check decided per cached host.
#[derive(Default)]
struct Decisions {
    /// The login on each host: `gh`'s, and Forgejo's from its token.
    logins: BTreeMap<String, String>,
    /// The token that reached each Forgejo host.
    tokens: BTreeMap<String, String>,
    forges: BTreeMap<String, HostState>,
}

/// A repo to list, with the forge it runs and the token for its host.
struct ListJob {
    repo: String,
    forge: ForgeKind,
    token: Option<String>,
}

fn host_set(gh_hosts: &BTreeMap<String, String>) -> BTreeSet<String> {
    gh_hosts.keys().cloned().collect()
}

/// The repos to list: those whose host has a forge and no error.
fn list_jobs(watched: &[String], decisions: &Decisions) -> Vec<ListJob> {
    watched
        .iter()
        .filter_map(|repo| {
            let host = forge::host_of(repo)?;
            let known = decisions.forges.get(host)?;
            if known.error.is_some() {
                return None;
            }
            Some(ListJob {
                repo: repo.clone(),
                forge: known.kind?,
                token: decisions.tokens.get(host).cloned(),
            })
        })
        .collect()
}

/// A GitHub host's login from `gh`, or its error: `gh` is missing, or it
/// isn't signed in to the host.
fn github_login(
    host: &str,
    gh_hosts: &BTreeMap<String, String>,
    gh_error: Option<&AppError>,
    known: &mut HostState,
) -> Option<String> {
    if let Some(login) = gh_hosts.get(host) {
        return Some(login.clone());
    }
    known.error = Some(match gh_error {
        Some(err) if err.code == "gh_missing" => err.clone(),
        _ => AppError::new(
            "gh_auth",
            format!(
                "gh isn't signed in to {host}. Run gh auth login --hostname {host} in a terminal, then Check now."
            ),
        ),
    });
    None
}

/// A Forgejo host's login with the token that reaches it. A token the host
/// rejects, or a call that fails, is the host's error.
async fn forgejo_login(
    http: &dyn Http,
    host: &str,
    found: Option<(String, TokenSource)>,
    known: &mut HostState,
) -> Option<(String, String)> {
    let (token, source) = found?;
    known.token = Some(source);
    match forgejo::login(http, host, &token).await {
        Ok(login) => Some((login, token)),
        Err(err) => {
            known.error = Some(err);
            None
        }
    }
}

/// Why a watched host has no forge: the probe gave no answer, or it said
/// the host isn't Forgejo.
fn unresolved_error(host: &str, unreachable: bool, probed: Option<bool>) -> Option<AppError> {
    if unreachable {
        return Some(AppError::new(
            "forge_failed",
            format!("{host}: no answer from https://{host}/api/v1/version"),
        ));
    }
    (probed == Some(false)).then(|| {
        AppError::new(
            "forge_unknown",
            format!(
                "{host} isn't a GitHub or Forgejo host. Choose its forge in Settings › Forges."
            ),
        )
    })
}

/// Lists each repo's open pull requests with its forge, a few at a time.
async fn list_all(
    deps: &Deps,
    jobs: Vec<ListJob>,
) -> Vec<(String, Result<Vec<PullRequest>, AppError>)> {
    let permits = Arc::new(Semaphore::new(CONCURRENCY));
    let mut tasks = JoinSet::new();
    for job in jobs {
        let (runner, http, permits) = (deps.runner.clone(), deps.http.clone(), permits.clone());
        tasks.spawn(async move {
            let _permit = permits.acquire_owned().await;
            let result = match job.forge {
                ForgeKind::Github => github::list_prs(&runner, &job.repo).await,
                ForgeKind::Forgejo => {
                    forgejo::list_prs(http, &job.repo, job.token.as_deref()).await
                }
            };
            (job.repo, result)
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
    use crate::reviews::http::FakeHttp;
    use crate::reviews::merge::MergeStatus;
    use crate::reviews::store::{ProfileSetting, RepoWatch, Runs};
    use crate::reviews::tokens::TOKENS_FILE;
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
    /// What the fake `ws review list` lists: one removed review of
    /// buff-bear#38.
    /// `ws config get -- hosts` for a config.toml with no [[hosts]].
    const CONFIG_DEFAULT: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"github.com","kind":"auto"}],"default":[{"name":"github.com","kind":"auto"}],"set":false}}"#;

    /// `ws config get -- hosts` with git.example.com as a Forgejo host.
    const CONFIG_FORGEJO: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"git.example.com","kind":"forgejo"}],"default":[{"name":"github.com","kind":"auto"}],"set":true}}"#;

    /// `ws config get -- hosts` listing github.com before git.example.com.
    const CONFIG_TWO_HOSTS: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"github.com","kind":"github"},{"name":"git.example.com","kind":"forgejo"}],"default":[{"name":"github.com","kind":"auto"}],"set":true}}"#;

    /// `ws config set` or `unset`'s answer.
    const CONFIG_CHANGE: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[],"default":[{"name":"github.com","kind":"auto"}],"set":true},"changed":true,"warnings":[]}"#;

    fn config_answer(json: &str) -> String {
        format!("echo '{json}'")
    }

    const REMOVED: &str = r#"{"reviews":[{"id":"review-buff-bear-38-a41c9f2-d8efb24f","review":{"repo":"github.com/bobcob7/buff-bear","ref":"a41c9f2d0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","requester":"","started_at":"2026-10-28T09:00:00Z","profiles":[],"pr":{"number":38,"url":"u"}},"agent":null}],"warnings":[]}"#;

    fn fake_programs(gh: &str, ws_review: &str) -> tempfile::TempDir {
        fake_programs_cached(gh, ws_review, WATCHED_CACHE)
    }

    /// The cache of the two GitHub repos most tests watch.
    const WATCHED_CACHE: &str =
        r#"{"repos":[{"id":"github.com/bobcob7/buff-bear"},{"id":"github.com/bobcob7/api"}]}"#;

    /// `fake_programs`, with `cache` as `ws cache list`'s answer.
    fn fake_programs_cached(gh: &str, ws_review: &str, cache: &str) -> tempfile::TempDir {
        fake_programs_configured(gh, ws_review, cache, &config_answer(CONFIG_DEFAULT))
    }

    /// `fake_programs_cached`, with `config_get` as the shell body that
    /// answers `ws config get`; other config calls succeed with a change.
    fn fake_programs_configured(
        gh: &str,
        ws_review: &str,
        cache: &str,
        config_get: &str,
    ) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        let log = dir.path().join("ws.log");
        let ws = format!(
            r#"echo "$@" >> '{}'
case "$1" in
  cache) echo '{cache}' ;;
  list) echo '{{"workspaces":[],"warnings":[]}}' ;;
  config) case "$3" in get) {config_get} ;; *) echo '{CONFIG_CHANGE}' ;; esac ;;
  review) case "$2 $3" in profiles*) echo '{PROFILES}' ;; "--json list") echo '{REMOVED}' ;; *) {ws_review} ;; esac ;;
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
        deps_with(dir, auto_review, Arc::new(FakeHttp::new()), None)
    }

    /// Deps whose Forgejo calls go to `http`, and whose desktop.json is
    /// `desktop_json`, so its forge-tokens.json sits beside it.
    fn deps_with(
        dir: &tempfile::TempDir,
        auto_review: bool,
        http: Arc<FakeHttp>,
        desktop_json: Option<PathBuf>,
    ) -> Deps {
        let store = Store::open(desktop_json);
        store
            .update(|d| {
                for repo in [
                    "github.com/bobcob7/buff-bear",
                    "github.com/bobcob7/api",
                    SVC,
                ] {
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
            http,
        }
    }

    fn ws_log(dir: &tempfile::TempDir) -> String {
        std::fs::read_to_string(dir.path().join("ws.log")).unwrap_or_default()
    }

    #[tokio::test]
    async fn check_reads_removed_reviews() {
        let dir = fake_programs(&gh_script(&format!("echo '{PRS}'")), "exit 1");
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        let removed: Vec<Entry> = checker
            .review_entries()
            .into_iter()
            .filter(|e| e.kind == crate::model::Kind::Removed)
            .collect();
        assert_eq!(removed.len(), 1);
        assert_eq!(
            removed[0].workspace_id.as_deref(),
            Some("review-buff-bear-38-a41c9f2-d8efb24f")
        );
        assert!(checker.entries().is_empty(), "never in the agent listing");
    }

    #[test]
    fn a_review_that_leaves_the_listing_is_kept_as_removed() {
        let checker = Checker::default();
        let review = crate::reviews::view::tests::recorded(
            41,
            crate::reviews::view::tests::HEAD,
            crate::model::Status::Idle,
            "2026-10-24T10:00:00Z",
        );
        checker.set_entries(vec![review.clone()]);
        checker.set_entries(vec![]);
        let entries = checker.review_entries();
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].kind, crate::model::Kind::Removed);
        assert_eq!(entries[0].workspace_id, review.workspace_id);
        checker.set_entries(vec![review]);
        assert_eq!(checker.review_entries().len(), 1, "a listed workspace wins");
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
            http: Arc::new(crate::reviews::http::ReqwestHttp::new()),
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

    const SVC_HOST: &str = "git.example.com";
    const SVC: &str = "git.example.com/team/svc";
    const SVC_HEAD: &str = "8db6637666e157ddd3af5d5033ac7edc347f2c10";
    const FORGEJO_CACHE: &str =
        r#"{"repos":[{"id":"github.com/bobcob7/buff-bear"},{"id":"git.example.com/team/svc"}]}"#;

    const FJ_PULLS: &str = r#"[{"number":15262,"title":"refactor: move RemoveTeamMember","body":"Moves it.","html_url":"https://git.example.com/team/svc/pulls/15262","draft":false,"mergeable":true,"merge_base":"7aa74e4f83288716cbe38f58f06a12f77995d38c","user":{"login":"wetneb","id":94198},"head":{"ref":"remove_member","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","label":"remove_member"},"base":{"ref":"forgejo","sha":"7aa74e4f83288716cbe38f58f06a12f77995d38c"},"additions":428,"deletions":461,"changed_files":16,"requested_reviewers":[{"login":"Gusted","id":26734}],"requested_reviewers_teams":[],"created_at":"2026-11-13T22:14:22+01:00","updated_at":"2026-11-13T22:14:30+01:00","state":"open"}]"#;
    const FJ_STATUS: &str = r#"{"state":"failure","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","total_count":3,"statuses":[{"context":"testing / unit","status":"success"},{"context":"testing / lint","status":"failure"},{"context":"issue-labels / cascade","status":"skipped"}]}"#;
    const FJ_REVIEWS: &str = r#"[{"id":1,"user":{"login":"Gusted"},"state":"REQUEST_REVIEW","stale":false,"official":true,"dismissed":false,"commit_id":""},{"id":2,"user":{"login":"Cyborus"},"state":"APPROVED","stale":false,"official":true,"dismissed":false,"commit_id":"8db6637666e157ddd3af5d5033ac7edc347f2c10"}]"#;
    const FJ_PROTECTIONS: &str = r#"[{"rule_name":"forgejo","branch_name":"forgejo","required_approvals":2,"block_on_outdated_branch":true}]"#;

    /// Forgejo's answers for git.example.com/team/svc, with `pulls` as the
    /// open pull requests call's status and body.
    fn forgejo_http(pulls: (u16, &str)) -> FakeHttp {
        let api = "https://git.example.com/api/v1";
        FakeHttp::new()
            .route(&format!("{api}/version"), 200, r#"{"version":"17.0.0"}"#)
            .route(
                &format!("{api}/repos/team/svc/pulls?state=open&sort=recentupdate&limit=50&page=1"),
                pulls.0,
                pulls.1,
            )
            .route(
                &format!("{api}/repos/team/svc/branch_protections"),
                200,
                FJ_PROTECTIONS,
            )
            .route(
                &format!("{api}/repos/team/svc/commits/{SVC_HEAD}/status"),
                200,
                FJ_STATUS,
            )
            .route(
                &format!("{api}/repos/team/svc/pulls/15262/reviews"),
                200,
                FJ_REVIEWS,
            )
    }

    fn version_calls(http: &FakeHttp) -> usize {
        http.calls()
            .iter()
            .filter(|(url, _)| url.ends_with("/version"))
            .count()
    }

    /// Writes forge-tokens.json beside the desktop.json it returns, with
    /// `token` for the Forgejo host.
    fn write_token(dir: &tempfile::TempDir, token: &str) -> PathBuf {
        std::fs::write(
            dir.path().join(TOKENS_FILE),
            format!(r#"{{"version":1,"tokens":{{"{SVC_HOST}":"{token}"}}}}"#),
        )
        .unwrap();
        dir.path().join("desktop.json")
    }

    #[tokio::test]
    async fn forgejo_repo_is_listed_with_merge_status() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert_eq!(view.error, None);
        assert_eq!(view.hosts.len(), 2);
        let forgejo = view
            .prs
            .iter()
            .find(|p| p.repo == SVC)
            .expect("the Forgejo pull request is listed");
        assert_eq!(forgejo.forge, ForgeKind::Forgejo);
        assert_eq!(forgejo.pr.merge.status, MergeStatus::Failing);
    }

    #[tokio::test]
    async fn signed_out_gh_does_not_hide_forgejo() {
        let dir = fake_programs_cached(r#"echo '{"hosts":{}}'; exit 1"#, "exit 1", FORGEJO_CACHE);
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert_eq!(view.error, None);
        let github = view.hosts.iter().find(|h| h.host == "github.com").unwrap();
        let error = github
            .error
            .as_ref()
            .expect("github.com reports the sign-in");
        assert_eq!(error.code, "gh_auth");
        assert!(error.message.contains("--hostname github.com"));
        assert!(view.prs.iter().any(|p| p.repo == SVC));
    }

    #[tokio::test]
    async fn all_github_signed_out_keeps_the_top_level_error() {
        let dir = fake_programs(r#"echo '{"hosts":{}}'; exit 1"#, "exit 1");
        let deps = deps(&dir, false);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert_eq!(view.error.map(|e| e.code), Some("gh_auth".to_string()));
        assert!(view.warnings.is_empty());
    }

    #[tokio::test]
    async fn forgejo_token_gives_login_and_mine() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let desktop = write_token(&dir, "secret");
        let http = Arc::new(forgejo_http((200, FJ_PULLS)).route(
            "https://git.example.com/api/v1/user",
            200,
            r#"{"login":"wetneb"}"#,
        ));
        let deps = deps_with(&dir, false, http.clone(), Some(desktop));
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        let host = view.hosts.iter().find(|h| h.host == SVC_HOST).unwrap();
        assert_eq!(host.login.as_deref(), Some("wetneb"));
        assert_eq!(host.token, Some(TokenSource::File));
        let forgejo = view.prs.iter().find(|p| p.repo == SVC).unwrap();
        assert!(forgejo.mine);
        assert!(http.calls().contains(&(
            "https://git.example.com/api/v1/user".to_string(),
            Some("secret".to_string())
        )));
    }

    #[tokio::test]
    async fn rejected_token_is_a_host_error_and_skips_its_repos() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let desktop = write_token(&dir, "stale");
        let http = Arc::new(forgejo_http((200, FJ_PULLS)).route(
            "https://git.example.com/api/v1/user",
            401,
            "{}",
        ));
        let deps = deps_with(&dir, false, http.clone(), Some(desktop));
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        let host = view.hosts.iter().find(|h| h.host == SVC_HOST).unwrap();
        assert_eq!(
            host.error.as_ref().map(|e| e.code.as_str()),
            Some("forge_auth")
        );
        assert!(!view.prs.iter().any(|p| p.repo == SVC));
        assert!(view.prs.iter().any(|p| p.repo.contains("buff-bear")));
        assert!(!http.calls().iter().any(|(url, _)| url.contains("/pulls?")));
    }

    #[tokio::test]
    async fn unknown_host_cannot_be_watched() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, false, Arc::new(FakeHttp::new()), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        let host = view.hosts.iter().find(|h| h.host == SVC_HOST).unwrap();
        assert_eq!(host.forge, None);
        assert_eq!(
            host.error.as_ref().map(|e| e.code.as_str()),
            Some("forge_unknown")
        );
        let repo = view.repos.iter().find(|r| r.id == SVC).unwrap();
        assert_eq!(repo.forge, None);
        assert!(!view.prs.iter().any(|p| p.repo == SVC));
        let state = deps.store.get();
        let watched = checker
            .with_state(|s| view::watched_repos(s, &state))
            .unwrap();
        assert!(!watched.contains(&SVC.to_string()));
    }

    #[tokio::test]
    async fn hosts_come_from_config_toml() {
        let dir = fake_programs_configured(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
            &config_answer(CONFIG_FORGEJO),
        );
        let http = Arc::new(forgejo_http((200, FJ_PULLS)));
        let deps = deps_with(&dir, false, http.clone(), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert_eq!(version_calls(&http), 0);
        assert!(checker.view(&deps.store).prs.iter().any(|p| p.repo == SVC));
    }

    #[tokio::test]
    async fn configured_hosts_come_first_in_the_view() {
        let cache = r#"{"repos":[{"id":"git.example.com/team/svc"},{"id":"github.com/bobcob7/buff-bear"},{"id":"a.example.org/x/y"}]}"#;
        let dir = fake_programs_configured(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            cache,
            &config_answer(CONFIG_TWO_HOSTS),
        );
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        let rows: Vec<(String, bool, bool)> = checker
            .view(&deps.store)
            .hosts
            .iter()
            .map(|h| (h.host.clone(), h.configured, h.primary))
            .collect();
        assert_eq!(
            rows,
            [
                ("github.com".to_string(), true, true),
                ("git.example.com".to_string(), true, false),
                ("a.example.org".to_string(), false, false),
            ]
        );
    }

    #[tokio::test]
    async fn config_failure_is_a_warning() {
        let failing =
            r#"echo '{"error":{"code":"config_invalid","message":"bad file"}}' >&2; exit 1"#;
        let dir = fake_programs_configured(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
            failing,
        );
        let deps = deps_with(&dir, false, Arc::new(FakeHttp::new()), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        let view = checker.view(&deps.store);
        assert!(view
            .warnings
            .iter()
            .any(|w| w.starts_with("ws config get hosts: ")));
        assert!(view.hosts.iter().all(|h| h.choice == ForgeChoice::Auto));
    }

    #[tokio::test]
    async fn migration_runs_in_the_check() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        deps.store
            .update(|d| {
                d.settings.forges.hosts.insert(
                    SVC_HOST.to_string(),
                    crate::reviews::store::ForgeHost {
                        kind: ForgeChoice::Forgejo,
                    },
                );
            })
            .unwrap();
        let checker = Checker::default();
        checker.check(&deps).await;
        assert!(deps.store.get().settings.forges.hosts.is_empty());
        let log = std::fs::read_to_string(dir.path().join("ws.log")).unwrap();
        assert!(
            log.contains("config --json set -- hosts github.com=auto git.example.com=forgejo"),
            "{log}"
        );
    }

    #[tokio::test]
    async fn probe_answer_is_kept_until_forgotten() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let http = Arc::new(forgejo_http((200, FJ_PULLS)));
        let deps = deps_with(&dir, false, http.clone(), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        checker.check(&deps).await;
        assert_eq!(version_calls(&http), 1);
        checker.forget_host(SVC_HOST);
        checker.check(&deps).await;
        assert_eq!(version_calls(&http), 2);
    }

    #[tokio::test]
    async fn forge_rate_limit_backs_off() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((429, "{}"))), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert_eq!(checker.with_state(|s| s.backoff), Some(1));
    }

    #[tokio::test]
    async fn auto_review_starts_forgejo_review_at_head() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            r#"echo '{"id":"review-svc-15262-8db6637","session":"s","path":"/w"}'"#,
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, true, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        Checker::default().check(&deps).await;
        assert!(ws_log(&dir).contains(&format!(
            "review start --json --name review-svc-15262-8db6637 {SVC}@{SVC_HEAD}\n"
        )));
    }

    #[tokio::test]
    async fn forge_of_names_the_hosts_forge() {
        let dir = fake_programs_cached(
            &gh_script(&format!("echo '{PRS}'")),
            "exit 1",
            FORGEJO_CACHE,
        );
        let deps = deps_with(&dir, false, Arc::new(forgejo_http((200, FJ_PULLS))), None);
        let checker = Checker::default();
        checker.check(&deps).await;
        assert_eq!(checker.forge_of(SVC), Some(ForgeKind::Forgejo));
        assert_eq!(
            checker.forge_of("github.com/bobcob7/buff-bear"),
            Some(ForgeKind::Github)
        );
        assert_eq!(checker.forge_of("gitlab.example.com/team/svc"), None);
    }
}
