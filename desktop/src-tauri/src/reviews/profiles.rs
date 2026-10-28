//! Reviewer profiles: what `ws review profiles` lists, and the `--profile`
//! and `--auto` choices the Settings table makes of them (ADR 0038).

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
            }]
        );
        let out = serde_json::to_value(&list.profiles[0]).unwrap();
        assert_eq!(out["patterns"][1], "go.mod");
    }
}
