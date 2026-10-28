//! What a review runs: `ws review start` for a pull request's head, with the
//! profiles, model, and permission mode from Settings (ADRs 0032, 0038,
//! 0039).

use super::github::PullRequest;
use super::names;
use super::profiles::{self, ProfileChoice};
use super::store::ReviewSettings;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReviewPlan {
    /// The workspace's `--name`; `ws` picks the same one by default.
    pub name: String,
    pub repo: String,
    pub number: u64,
    /// The pull request's head commit, which `ws` reviews.
    pub head: String,
    /// Empty leaves the choice to `ws` and `config.toml`.
    pub profiles: Vec<ProfileChoice>,
    pub model: String,
    pub permission_mode: String,
    /// The requester's focus, added to the review's prompts.
    #[serde(default)]
    pub focus: String,
}

pub fn plan(
    repo_id: &str,
    pr: &PullRequest,
    settings: &ReviewSettings,
    profiles: Vec<ProfileChoice>,
) -> ReviewPlan {
    ReviewPlan {
        name: names::review_name(repo_id, pr.number, &pr.head_ref_oid),
        repo: repo_id.to_string(),
        number: pr.number,
        head: pr.head_ref_oid.clone(),
        profiles,
        model: settings.model.clone(),
        permission_mode: settings.permission_mode.clone(),
        focus: String::new(),
    }
}

/// The plan with Settings' profiles, which needs `ws review profiles` only
/// when the table was changed.
pub async fn build(
    runner: &Runner,
    repo_id: &str,
    pr: &PullRequest,
    settings: &ReviewSettings,
) -> Result<ReviewPlan, AppError> {
    let choices = if settings.profiles.is_empty() {
        Vec::new()
    } else {
        profiles::choices(&profiles::list(runner).await?, &settings.profiles)?
    };
    Ok(plan(repo_id, pr, settings, choices))
}

fn non_empty(value: &str) -> Option<&str> {
    Some(value.trim()).filter(|s| !s.is_empty())
}

/// `ws review start --json --pr <n> [--name N] <repo> [--profile …]…
/// [--auto …]… -- [--model=M] [--permission-mode=P] [focus]`. Options
/// after `--` carry their value with `=`, since `ws` takes the last
/// argument there for the focus unless it starts with `-`.
pub fn start_args(plan: &ReviewPlan) -> Result<Vec<String>, AppError> {
    let mut args: Vec<String> = ["review", "start", "--json", "--pr"]
        .map(str::to_string)
        .to_vec();
    args.push(plan.number.to_string());
    let name = plan.name.trim();
    if name.is_empty() {
        return Err(AppError::new("usage", "the review needs a workspace name"));
    }
    if name != names::review_name(&plan.repo, plan.number, &plan.head) {
        args.extend(["--name".to_string(), name.to_string()]);
    }
    args.push(plan.repo.clone());
    args.extend(plan.profiles.iter().flat_map(ProfileChoice::args));
    let mut after = Vec::new();
    if let Some(model) = non_empty(&plan.model) {
        after.push(format!("--model={model}"));
    }
    if let Some(mode) = non_empty(&plan.permission_mode) {
        after.push(format!("--permission-mode={mode}"));
    }
    if let Some(focus) = non_empty(&plan.focus) {
        if focus.starts_with('-') {
            return Err(AppError::new(
                "usage",
                "the focus can't start with \"-\": ws review start would take it for an option to claude",
            ));
        }
        after.push(focus.to_string());
    }
    if !after.is_empty() {
        args.push("--".to_string());
        args.extend(after);
    }
    Ok(args)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::view::tests::{pr, HEAD, REPO};

    fn default_plan() -> ReviewPlan {
        plan(REPO, &pr(42), &ReviewSettings::default(), Vec::new())
    }

    #[test]
    fn plan_names_the_review_after_the_pull_request_and_head() {
        let plan = default_plan();
        assert_eq!(plan.name, "review-buff-bear-42-3f2a9c1");
        assert_eq!((plan.repo.as_str(), plan.number), (REPO, 42));
        assert_eq!(plan.head, HEAD);
    }

    #[test]
    fn default_plan_starts_ws_review_with_the_pull_request_only() {
        assert_eq!(
            start_args(&default_plan()).unwrap(),
            ["review", "start", "--json", "--pr", "42", REPO]
        );
    }

    #[test]
    fn profiles_go_before_the_dash_and_agent_options_after_it() {
        let plan = ReviewPlan {
            profiles: vec![
                ProfileChoice {
                    name: "security".to_string(),
                    model: "opus".to_string(),
                    auto: false,
                },
                ProfileChoice {
                    name: "go".to_string(),
                    model: String::new(),
                    auto: true,
                },
            ],
            model: "sonnet".to_string(),
            permission_mode: "plan".to_string(),
            focus: "  Look hard at the SQL.  ".to_string(),
            ..default_plan()
        };
        assert_eq!(
            start_args(&plan).unwrap(),
            [
                "review",
                "start",
                "--json",
                "--pr",
                "42",
                REPO,
                "--profile",
                "security:opus",
                "--auto",
                "go",
                "--",
                "--model=sonnet",
                "--permission-mode=plan",
                "Look hard at the SQL."
            ]
        );
    }

    #[test]
    fn model_without_focus_keeps_its_value_attached() {
        let plan = ReviewPlan {
            model: "opus".to_string(),
            ..default_plan()
        };
        let args = start_args(&plan).unwrap();
        assert_eq!(args[args.len() - 2..], ["--", "--model=opus"]);
    }

    #[test]
    fn edited_name_is_passed() {
        let plan = ReviewPlan {
            name: "careful-look".to_string(),
            ..default_plan()
        };
        let args = start_args(&plan).unwrap();
        assert_eq!(args[5..8], ["--name", "careful-look", REPO]);
    }

    #[test]
    fn focus_starting_with_a_dash_is_refused() {
        let plan = ReviewPlan {
            focus: "- the SQL".to_string(),
            ..default_plan()
        };
        assert_eq!(start_args(&plan).unwrap_err().code, "usage");
    }

    #[test]
    fn empty_name_is_refused() {
        let plan = ReviewPlan {
            name: " ".to_string(),
            ..default_plan()
        };
        assert_eq!(start_args(&plan).unwrap_err().code, "usage");
    }
}
