//! `desktop.json`: watched repos, dismissals, auto-review records, and
//! settings (ADR 0034). The backend is its only writer.

use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub const FILE_NAME: &str = "desktop.json";
const VERSION: u32 = 1;

pub const DEFAULT_TEMPLATE: &str = "Review pull request {repo}#{number}, \"{title}\", by {author}, at {head} against {base}.
The PR's description is below. Check the diff against {base} for bugs, security problems, and missing tests. Report findings ranked by severity, with file and line. Don't push or comment on GitHub.

{body}";

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ReviewSettings {
    pub check_interval_minutes: u32,
    pub skip_drafts: bool,
    pub skip_mine: bool,
    pub only_requested: bool,
    pub max_auto_reviews: u32,
    pub task_template: String,
    pub model: String,
    pub permission_mode: String,
}

impl Default for ReviewSettings {
    fn default() -> Self {
        Self {
            check_interval_minutes: 5,
            skip_drafts: true,
            skip_mine: true,
            only_requested: false,
            max_auto_reviews: 2,
            task_template: DEFAULT_TEMPLATE.to_string(),
            model: String::new(),
            permission_mode: String::new(),
        }
    }
}

impl ReviewSettings {
    /// Rejects values the app can't use, so a bad one never reaches the file.
    pub fn validate(&self) -> Result<(), AppError> {
        if !(1..=1440).contains(&self.check_interval_minutes) {
            return Err(AppError::new(
                "setting_invalid",
                "the check interval must be 1 to 1440 minutes",
            ));
        }
        if !(1..=20).contains(&self.max_auto_reviews) {
            return Err(AppError::new(
                "setting_invalid",
                "at most 1 to 20 auto-reviews can run at once",
            ));
        }
        if self.task_template.trim().is_empty() {
            return Err(AppError::new(
                "setting_invalid",
                "the review task can't be empty",
            ));
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    pub reviews: ReviewSettings,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RepoWatch {
    pub watch: bool,
    pub auto_review: bool,
}

/// A pull request at one head commit.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PrHead {
    pub repo: String,
    pub number: u64,
    pub head: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AutoStarted {
    pub repo: String,
    pub number: u64,
    pub head: String,
    pub workspace: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ReviewsState {
    pub repos: BTreeMap<String, RepoWatch>,
    pub dismissed: Vec<PrHead>,
    pub auto_started: Vec<AutoStarted>,
}

impl ReviewsState {
    pub fn watch(&self, repo: &str) -> RepoWatch {
        self.repos.get(repo).copied().unwrap_or_default()
    }

    pub fn set_watch(&mut self, repo: &str, watch: RepoWatch) {
        let watch = RepoWatch {
            watch: watch.watch,
            auto_review: watch.watch && watch.auto_review,
        };
        if watch == RepoWatch::default() {
            self.repos.remove(repo);
        } else {
            self.repos.insert(repo.to_string(), watch);
        }
    }

    pub fn is_dismissed(&self, repo: &str, number: u64, head: &str) -> bool {
        self.dismissed
            .iter()
            .any(|d| d.repo == repo && d.number == number && d.head == head)
    }

    pub fn dismiss(&mut self, pr: PrHead) {
        self.restore(&pr.repo, pr.number);
        self.dismissed.push(pr);
    }

    pub fn restore(&mut self, repo: &str, number: u64) {
        self.dismissed
            .retain(|d| !(d.repo == repo && d.number == number));
    }

    pub fn was_auto_started(&self, repo: &str, number: u64, head: &str) -> bool {
        self.auto_started
            .iter()
            .any(|a| a.repo == repo && a.number == number && a.head == head)
    }

    pub fn is_auto_workspace(&self, workspace: &str) -> bool {
        self.auto_started.iter().any(|a| a.workspace == workspace)
    }

    /// Drops dismissals and auto-review records for `repo` whose pull
    /// request is no longer open at that head. `open` is every open pull
    /// request's number and head, from a successful check.
    pub fn prune(&mut self, repo: &str, open: &[(u64, &str)]) {
        let is_open = |number: u64, head: &str| open.iter().any(|&(n, h)| n == number && h == head);
        self.dismissed
            .retain(|d| d.repo != repo || is_open(d.number, &d.head));
        self.auto_started
            .retain(|a| a.repo != repo || is_open(a.number, &a.head));
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StateFile {
    pub version: u32,
    pub settings: Settings,
    pub reviews: ReviewsState,
}

impl Default for StateFile {
    fn default() -> Self {
        Self {
            version: VERSION,
            settings: Settings::default(),
            reviews: ReviewsState::default(),
        }
    }
}

/// Holds the file in memory and writes each change through to disk.
#[derive(Debug)]
pub struct Store {
    path: Option<PathBuf>,
    data: Mutex<StateFile>,
}

impl Store {
    /// Loads the file at `path`, or defaults when it's missing or unreadable.
    /// With no path, changes are kept in memory only.
    pub fn open(path: Option<PathBuf>) -> Self {
        let data = path.as_deref().and_then(load).unwrap_or_default();
        Self {
            path,
            data: Mutex::new(data),
        }
    }

    pub fn get(&self) -> StateFile {
        self.data.lock().map(|d| d.clone()).unwrap_or_default()
    }

    /// Applies `change` and writes the result; on a failed write, keeps the
    /// old contents and returns the error.
    pub fn update<T>(&self, change: impl FnOnce(&mut StateFile) -> T) -> Result<T, AppError> {
        let mut data = self
            .data
            .lock()
            .map_err(|_| AppError::new("internal", "state lock poisoned"))?;
        if data.version > VERSION {
            return Err(AppError::new(
                "state_newer",
                "desktop.json was written by a newer version of the app; it isn't changed",
            ));
        }
        let mut next = data.clone();
        let out = change(&mut next);
        if next != *data {
            if let Some(path) = &self.path {
                write(path, &next)?;
            }
            *data = next;
        }
        Ok(out)
    }
}

fn load(path: &Path) -> Option<StateFile> {
    let bytes = std::fs::read(path).ok()?;
    serde_json::from_slice(&bytes).ok()
}

/// Writes a temp file beside `path` and renames it into place, so a crash
/// leaves the old file or the new one.
fn write(path: &Path, data: &StateFile) -> Result<(), AppError> {
    let fail = |e: std::io::Error| {
        AppError::new(
            "state_write_failed",
            format!("writing {}: {e}", path.display()),
        )
    };
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(fail)?;
    }
    let tmp = path.with_extension("json.tmp");
    let bytes =
        serde_json::to_vec_pretty(data).map_err(|e| AppError::new("internal", e.to_string()))?;
    std::fs::write(&tmp, bytes).map_err(fail)?;
    std::fs::rename(&tmp, path).map_err(fail)
}

#[cfg(test)]
mod tests {
    use super::*;

    const REPO: &str = "github.com/bobcob7/buff-bear";

    fn temp_store() -> (tempfile::TempDir, Store) {
        let dir = tempfile::tempdir().unwrap();
        let store = Store::open(Some(dir.path().join("app").join(FILE_NAME)));
        (dir, store)
    }

    fn pr(number: u64, head: &str) -> PrHead {
        PrHead {
            repo: REPO.to_string(),
            number,
            head: head.to_string(),
        }
    }

    #[test]
    fn missing_file_gives_defaults() {
        let (_dir, store) = temp_store();
        assert_eq!(store.get(), StateFile::default());
    }

    #[test]
    fn missing_keys_take_defaults() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        std::fs::write(
            &path,
            r#"{"version":1,"settings":{"reviews":{"skipDrafts":false}}}"#,
        )
        .unwrap();
        let data = Store::open(Some(path)).get();
        assert!(!data.settings.reviews.skip_drafts);
        assert_eq!(data.settings.reviews.check_interval_minutes, 5);
    }

    #[test]
    fn unreadable_file_gives_defaults() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        std::fs::write(&path, "{not json").unwrap();
        assert_eq!(Store::open(Some(path)).get(), StateFile::default());
    }

    #[test]
    fn update_writes_file_that_reopens_the_same() {
        let (dir, store) = temp_store();
        store.update(|d| d.reviews.dismiss(pr(40, "abc"))).unwrap();
        let path = dir.path().join("app").join(FILE_NAME);
        assert_eq!(Store::open(Some(path)).get(), store.get());
        assert!(!dir.path().join("app").join("desktop.json.tmp").exists());
    }

    #[test]
    fn update_refuses_to_write_newer_version() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        std::fs::write(&path, r#"{"version":2}"#).unwrap();
        let store = Store::open(Some(path.clone()));
        let err = store
            .update(|d| d.settings.reviews.skip_mine = false)
            .unwrap_err();
        assert_eq!(err.code, "state_newer");
        assert_eq!(std::fs::read_to_string(path).unwrap(), r#"{"version":2}"#);
    }

    #[test]
    fn failed_write_keeps_old_value() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("file"), "").unwrap();
        let store = Store::open(Some(dir.path().join("file").join(FILE_NAME)));
        let result = store.update(|d| d.settings.reviews.skip_mine = false);
        assert_eq!(result.unwrap_err().code, "state_write_failed");
        assert!(store.get().settings.reviews.skip_mine);
    }

    #[test]
    fn auto_review_needs_watch() {
        let mut state = ReviewsState::default();
        state.set_watch(
            REPO,
            RepoWatch {
                watch: false,
                auto_review: true,
            },
        );
        assert_eq!(state.watch(REPO), RepoWatch::default());
        assert!(state.repos.is_empty());
    }

    #[test]
    fn dismissal_is_per_head() {
        let mut state = ReviewsState::default();
        state.dismiss(pr(40, "abc"));
        assert!(state.is_dismissed(REPO, 40, "abc"));
        assert!(!state.is_dismissed(REPO, 40, "def"));
    }

    #[test]
    fn restore_removes_dismissal() {
        let mut state = ReviewsState::default();
        state.dismiss(pr(40, "abc"));
        state.restore(REPO, 40);
        assert!(state.dismissed.is_empty());
    }

    #[test]
    fn prune_drops_closed_and_pushed_pull_requests_of_that_repo_only() {
        let mut state = ReviewsState::default();
        state.dismiss(pr(1, "a"));
        state.dismiss(pr(2, "b"));
        state.dismiss(pr(3, "c"));
        state.dismiss(PrHead {
            repo: "github.com/o/other".to_string(),
            number: 9,
            head: "z".to_string(),
        });
        state.prune(REPO, &[(1, "a"), (2, "moved")]);
        let left: Vec<_> = state.dismissed.iter().map(|d| d.number).collect();
        assert_eq!(left, [1, 9]);
    }

    #[test]
    fn validate_rejects_zero_interval() {
        let settings = ReviewSettings {
            check_interval_minutes: 0,
            ..ReviewSettings::default()
        };
        assert_eq!(settings.validate().unwrap_err().code, "setting_invalid");
    }
}
