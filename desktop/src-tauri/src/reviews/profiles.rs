//! Reviewer profiles: what `ws review profiles` lists, the `--profile`
//! and `--auto` choices the Settings table makes of them (ADR 0038), and
//! adding, editing, and removing them (ADR 0042).

use super::store::{ProfileSetting, Runs};
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// The subagent model that is the review agent's own, for a profile whose
/// `config.toml` model the table set back to Default.
const INHERIT: &str = "inherit";

/// One profile as `ws review profiles --json` lists it, with `config.toml`
/// applied.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ListedProfile {
    pub name: String,
    #[serde(default)]
    pub description: String,
    pub when: Runs,
    #[serde(default, rename(deserialize = "match"))]
    pub patterns: Vec<String>,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub builtin: bool,
    #[serde(default)]
    pub source: Source,
}

/// Where a profile comes from: built into `ws`, a user's file, or a
/// user's file replacing a built-in (ADR 0042).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Source {
    #[default]
    Builtin,
    User,
    Override,
}

/// One profile with its guide, as `ws review profiles show`, `add`, and
/// `edit --json` print it. `path` is null for a built-in.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ProfileDetail {
    #[serde(flatten)]
    pub profile: ListedProfile,
    #[serde(default)]
    pub guide: String,
    #[serde(default)]
    pub path: Option<String>,
}

/// What the editor saves: only the fields set are passed to `ws`.
/// `patterns` of `[]` removes every pattern on `edit`.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ProfileDraft {
    pub description: Option<String>,
    pub when: Option<Runs>,
    pub patterns: Option<Vec<String>>,
    pub guide: Option<String>,
}

/// What `ws review profiles rm --json` prints. `restored` is true when the
/// removed file was an override, so the built-in is back.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Removed {
    pub name: String,
    #[serde(default)]
    pub path: String,
    pub restored: bool,
}

#[derive(Deserialize)]
struct ProfilesList {
    profiles: Vec<ListedProfile>,
}

/// Runs `ws review profiles --json`.
pub async fn list(runner: &Runner) -> Result<Vec<ListedProfile>, AppError> {
    let args = ["review", "profiles", "--json"].map(str::to_string);
    let list: ProfilesList = runner.ws_json_as_given(&args).await?;
    Ok(list.profiles)
}

/// Runs `ws review profiles show <name> --json`.
pub async fn show(runner: &Runner, name: &str) -> Result<ProfileDetail, AppError> {
    runner.ws_json_as_given(&op_args("show", name, &[])).await
}

/// Runs `ws review profiles add`, with the guide on stdin.
pub async fn add(
    runner: &Runner,
    name: &str,
    draft: &ProfileDraft,
) -> Result<ProfileDetail, AppError> {
    write(runner, "add", name, draft).await
}

/// Runs `ws review profiles edit`, with the guide, if it changed, on stdin.
pub async fn edit(
    runner: &Runner,
    name: &str,
    draft: &ProfileDraft,
) -> Result<ProfileDetail, AppError> {
    write(runner, "edit", name, draft).await
}

/// Runs `ws review profiles rm <name> --json`.
pub async fn remove(runner: &Runner, name: &str) -> Result<Removed, AppError> {
    runner.ws_json_as_given(&op_args("rm", name, &[])).await
}

/// Drops the Settings row of a profile that `rm` removed for good. A
/// restored built-in keeps its row. Returns whether a row was dropped.
pub fn forget_removed(changed: &mut BTreeMap<String, ProfileSetting>, removed: &Removed) -> bool {
    !removed.restored && changed.remove(&removed.name).is_some()
}

async fn write(
    runner: &Runner,
    op: &str,
    name: &str,
    draft: &ProfileDraft,
) -> Result<ProfileDetail, AppError> {
    let args = write_args(op, name, draft);
    runner
        .ws_json_with_input(&args, draft.guide.as_deref().map(str::as_bytes))
        .await
}

/// `ws review profiles <op> --json [flags] -- <name> [extra]`. The name
/// goes after `--`, so a name that starts with a hyphen isn't a flag.
fn op_args(op: &str, name: &str, flags: &[String]) -> Vec<String> {
    let mut args: Vec<String> = ["review", "profiles", op, "--json"]
        .map(str::to_string)
        .to_vec();
    args.extend_from_slice(flags);
    args.push("--".to_string());
    args.push(name.to_string());
    args
}

/// The arguments of `add` or `edit` for the draft's fields; a guide is
/// read from stdin (`-`).
fn write_args(op: &str, name: &str, draft: &ProfileDraft) -> Vec<String> {
    let mut flags = Vec::new();
    if let Some(description) = &draft.description {
        flags.extend(["--description".to_string(), description.clone()]);
    }
    if let Some(when) = draft.when {
        flags.extend(["--when".to_string(), when_arg(when).to_string()]);
    }
    match &draft.patterns {
        Some(patterns) if patterns.is_empty() && op == "edit" => {
            flags.push("--no-match".to_string());
        }
        Some(patterns) => {
            for pattern in patterns {
                flags.extend(["--match".to_string(), pattern.clone()]);
            }
        }
        None => {}
    }
    let mut args = op_args(op, name, &flags);
    if draft.guide.is_some() {
        args.push("-".to_string());
    }
    args
}

fn when_arg(when: Runs) -> &'static str {
    match when {
        Runs::Always => "always",
        Runs::Auto => "auto",
        Runs::Off => "off",
    }
}

/// A profile a review is started with: `--profile NAME[:MODEL]`, or
/// `--auto NAME[:MODEL]` when `auto`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ProfileChoice {
    pub name: String,
    pub model: String,
    pub auto: bool,
}

impl ProfileChoice {
    pub fn args(&self) -> [String; 2] {
        let flag = if self.auto { "--auto" } else { "--profile" };
        let value = if self.model.is_empty() {
            self.name.clone()
        } else {
            format!("{}:{}", self.name, self.model)
        };
        [flag.to_string(), value]
    }
}

/// The choices for every listed profile, each with its changed row or
/// else as listed. No changes is no choices, which leaves the choice to
/// `ws`. Changes for profiles `ws` no longer lists are ignored, and a
/// profile without patterns can't run when files match, so it's left out.
pub fn choices(
    listed: &[ListedProfile],
    changed: &BTreeMap<String, ProfileSetting>,
) -> Result<Vec<ProfileChoice>, AppError> {
    if changed.is_empty() {
        return Ok(Vec::new());
    }
    let chosen: Vec<ProfileChoice> = listed
        .iter()
        .filter_map(|p| {
            let (runs, model) = match changed.get(&p.name) {
                Some(row) if row.model.is_empty() && !p.model.is_empty() => (row.runs, INHERIT),
                Some(row) => (row.runs, row.model.as_str()),
                None => (p.when, p.model.as_str()),
            };
            let auto = match runs {
                Runs::Off => return None,
                Runs::Auto if p.patterns.is_empty() => return None,
                Runs::Auto => true,
                Runs::Always => false,
            };
            Some(ProfileChoice {
                name: p.name.clone(),
                model: model.to_string(),
                auto,
            })
        })
        .collect();
    if chosen.is_empty() {
        return Err(AppError::new(
            "setting_invalid",
            "every reviewer profile is Off; turn one on in Settings, or reset them to defaults",
        ));
    }
    Ok(chosen)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn listed(name: &str, when: Runs, patterns: &[&str], model: &str) -> ListedProfile {
        ListedProfile {
            name: name.to_string(),
            description: String::new(),
            when,
            patterns: patterns.iter().map(|s| (*s).to_string()).collect(),
            model: model.to_string(),
            builtin: true,
            source: Source::Builtin,
        }
    }

    fn defaults() -> Vec<ListedProfile> {
        vec![
            listed("security", Runs::Always, &[], ""),
            listed("tests", Runs::Always, &[], "haiku"),
            listed("go", Runs::Auto, &["*.go", "go.mod"], ""),
            listed("efficiency", Runs::Off, &[], ""),
        ]
    }

    fn row(runs: Runs, model: &str) -> ProfileSetting {
        ProfileSetting {
            runs,
            model: model.to_string(),
        }
    }

    fn args(choices: &[ProfileChoice]) -> Vec<String> {
        choices.iter().flat_map(ProfileChoice::args).collect()
    }

    #[test]
    fn no_changes_leaves_the_choice_to_ws() {
        assert!(choices(&defaults(), &BTreeMap::new()).unwrap().is_empty());
    }

    #[test]
    fn a_change_passes_every_profile_that_runs() {
        let changed = BTreeMap::from([("security".to_string(), row(Runs::Always, "opus"))]);
        assert_eq!(
            args(&choices(&defaults(), &changed).unwrap()),
            [
                "--profile",
                "security:opus",
                "--profile",
                "tests:haiku",
                "--auto",
                "go"
            ]
        );
    }

    #[test]
    fn changed_rows_switch_runs() {
        let changed = BTreeMap::from([
            ("go".to_string(), row(Runs::Always, "sonnet")),
            ("efficiency".to_string(), row(Runs::Auto, "")),
            ("tests".to_string(), row(Runs::Off, "")),
        ]);
        assert_eq!(
            args(&choices(&defaults(), &changed).unwrap()),
            ["--profile", "security", "--profile", "go:sonnet"]
        );
    }

    #[test]
    fn default_model_over_a_configured_one_inherits_the_review_agents() {
        let changed = BTreeMap::from([("tests".to_string(), row(Runs::Always, ""))]);
        let chosen = choices(&defaults(), &changed).unwrap();
        assert_eq!(chosen[1].args(), ["--profile", "tests:inherit"]);
    }

    #[test]
    fn changes_for_unlisted_profiles_are_ignored() {
        let changed = BTreeMap::from([("rust".to_string(), row(Runs::Always, ""))]);
        let names: Vec<_> = choices(&defaults(), &changed)
            .unwrap()
            .into_iter()
            .map(|c| c.name)
            .collect();
        assert_eq!(names, ["security", "tests", "go"]);
    }

    #[test]
    fn every_profile_off_is_an_error() {
        let changed: BTreeMap<_, _> = defaults()
            .iter()
            .map(|p| (p.name.clone(), row(Runs::Off, "")))
            .collect();
        assert_eq!(
            choices(&defaults(), &changed).unwrap_err().code,
            "setting_invalid"
        );
    }

    #[test]
    fn listing_reads_ws_review_profiles_json() {
        let json = r#"{"profiles":[{"name":"go","description":"Go","when":"auto","match":["*.go","go.mod"],"model":"sonnet","builtin":true}],"warnings":[]}"#;
        let list: ProfilesList = serde_json::from_str(json).unwrap();
        assert_eq!(
            list.profiles,
            [ListedProfile {
                name: "go".to_string(),
                description: "Go".to_string(),
                when: Runs::Auto,
                patterns: vec!["*.go".to_string(), "go.mod".to_string()],
                model: "sonnet".to_string(),
                builtin: true,
                source: Source::Builtin,
            }]
        );
        let out = serde_json::to_value(&list.profiles[0]).unwrap();
        assert_eq!(out["patterns"][1], "go.mod");
    }

    #[test]
    fn listing_reads_each_source() {
        let json = r#"{"profiles":[{"name":"go","when":"auto","match":["*.go"],"builtin":false,"source":"override"},{"name":"docs","when":"always","builtin":false,"source":"user"}],"warnings":[]}"#;
        let list: ProfilesList = serde_json::from_str(json).unwrap();
        let sources: Vec<_> = list.profiles.iter().map(|p| p.source).collect();
        assert_eq!(sources, [Source::Override, Source::User]);
        let out = serde_json::to_value(&list.profiles[0]).unwrap();
        assert_eq!(out["source"], "override");
    }

    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH. The script writes
    /// its arguments, one per line, to `args` and its stdin to `stdin`.
    fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"{args}\"\ncat > \"{stdin}\"\n{body}\n",
            args = dir.path().join("args").display(),
            stdin = dir.path().join("stdin").display(),
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(format!("{}:/bin:/usr/bin", dir.path().display()));
        (dir, runner)
    }

    fn read(dir: &tempfile::TempDir, file: &str) -> String {
        std::fs::read_to_string(dir.path().join(file)).unwrap()
    }

    fn lines(dir: &tempfile::TempDir) -> Vec<String> {
        read(dir, "args").lines().map(str::to_string).collect()
    }

    const DETAIL: &str = r#"{"name":"docs","description":"Docs","when":"auto","match":["*.md"],"model":"","builtin":false,"source":"user","guide":"Check links.","path":"/c/review/profiles/docs.md","warnings":[]}"#;

    fn draft(description: &str, patterns: &[&str], guide: &str) -> ProfileDraft {
        ProfileDraft {
            description: Some(description.to_string()),
            when: None,
            patterns: Some(patterns.iter().map(|s| (*s).to_string()).collect()),
            guide: Some(guide.to_string()),
        }
    }

    #[tokio::test]
    async fn add_passes_the_fields_as_flags_and_the_guide_on_stdin() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        let mut new = draft("Docs", &["*.md", "docs/**"], "Check links.\n");
        new.when = Some(Runs::Auto);
        let added = add(&runner, "docs", &new).await.unwrap();
        assert_eq!(
            lines(&dir),
            [
                "review",
                "profiles",
                "add",
                "--json",
                "--description",
                "Docs",
                "--when",
                "auto",
                "--match",
                "*.md",
                "--match",
                "docs/**",
                "--",
                "docs",
                "-"
            ]
        );
        assert_eq!(read(&dir, "stdin"), "Check links.\n");
        assert_eq!(added.guide, "Check links.");
        assert_eq!(added.profile.source, Source::User);
        assert_eq!(added.profile.patterns, ["*.md"]);
        assert_eq!(added.path.as_deref(), Some("/c/review/profiles/docs.md"));
    }

    #[tokio::test]
    async fn add_without_patterns_passes_no_match_flags() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        add(&runner, "docs", &draft("Docs", &[], "g"))
            .await
            .unwrap();
        assert!(!lines(&dir).iter().any(|a| a.contains("match")));
    }

    #[tokio::test]
    async fn edit_passes_only_the_changed_fields() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        let changes = ProfileDraft {
            description: Some("Docs, ours".to_string()),
            ..ProfileDraft::default()
        };
        edit(&runner, "security", &changes).await.unwrap();
        assert_eq!(
            lines(&dir),
            [
                "review",
                "profiles",
                "edit",
                "--json",
                "--description",
                "Docs, ours",
                "--",
                "security"
            ]
        );
        assert_eq!(read(&dir, "stdin"), "");
    }

    #[tokio::test]
    async fn edit_removing_every_pattern_passes_no_match() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        let changes = ProfileDraft {
            when: Some(Runs::Always),
            patterns: Some(Vec::new()),
            guide: Some("New guide".to_string()),
            ..ProfileDraft::default()
        };
        edit(&runner, "go", &changes).await.unwrap();
        assert_eq!(
            &lines(&dir)[4..],
            ["--when", "always", "--no-match", "--", "go", "-"]
        );
        assert_eq!(read(&dir, "stdin"), "New guide");
    }

    #[tokio::test]
    async fn a_name_that_starts_with_a_hyphen_isnt_a_flag() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        show(&runner, "-x").await.unwrap();
        assert_eq!(&lines(&dir)[3..], ["--json", "--", "-x"]);
    }

    #[tokio::test]
    async fn show_reads_the_guide_and_a_builtins_null_path() {
        let (dir, runner) = fake_ws(
            r#"echo '{"name":"security","description":"Security","when":"always","match":[],"model":"opus","builtin":true,"source":"builtin","guide":"Anchor it.","path":null,"warnings":[]}'"#,
        );
        let shown = show(&runner, "security").await.unwrap();
        assert_eq!(lines(&dir)[2], "show");
        assert_eq!(shown.guide, "Anchor it.");
        assert_eq!(shown.path, None);
        let out = serde_json::to_value(&shown).unwrap();
        assert_eq!(out["name"], "security");
        assert_eq!(out["guide"], "Anchor it.");
        assert!(out["path"].is_null());
    }

    #[tokio::test]
    async fn a_write_error_carries_wss_code_and_message() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"profile_exists","message":"a profile named security already exists"}}' >&2; exit 4"#,
        );
        let err = add(&runner, "security", &draft("S", &[], "g"))
            .await
            .unwrap_err();
        assert_eq!(err.code, "profile_exists");
        assert_eq!(err.message, "a profile named security already exists");
    }

    #[tokio::test]
    async fn remove_reads_whether_the_builtin_came_back() {
        let (dir, runner) = fake_ws(
            r#"echo '{"name":"go","path":"/c/review/profiles/go.md","restored":true,"warnings":[]}'"#,
        );
        let removed = remove(&runner, "go").await.unwrap();
        assert_eq!(&lines(&dir)[2..], ["rm", "--json", "--", "go"]);
        assert!(removed.restored);
    }

    fn removed(name: &str, restored: bool) -> Removed {
        Removed {
            name: name.to_string(),
            path: String::new(),
            restored,
        }
    }

    #[test]
    fn removing_a_profile_for_good_drops_its_row() {
        let mut changed = BTreeMap::from([
            ("docs".to_string(), row(Runs::Off, "")),
            ("go".to_string(), row(Runs::Always, "opus")),
        ]);
        assert!(forget_removed(&mut changed, &removed("docs", false)));
        assert_eq!(changed.keys().collect::<Vec<_>>(), ["go"]);
    }

    #[test]
    fn restoring_a_builtin_keeps_its_row() {
        let mut changed = BTreeMap::from([("go".to_string(), row(Runs::Always, "opus"))]);
        assert!(!forget_removed(&mut changed, &removed("go", true)));
        assert_eq!(changed.len(), 1);
    }
}
