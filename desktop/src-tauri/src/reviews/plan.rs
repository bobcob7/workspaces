//! What a review runs: its workspace name, repo spec, and task, from the
//! Settings template (ADR 0032).

use super::github::{self, PullRequest};
use super::names;
use super::store::ReviewSettings;
use crate::commands::NewAgent;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ReviewPlan {
    pub name: String,
    pub spec: String,
    pub prompt: String,
    pub model: String,
    pub permission_mode: String,
}

/// Fills `{repo}`, `{number}`, `{title}`, `{author}`, `{base}`, `{head}`, and
/// `{body}`. `{repo}` is `owner/name`; `{head}` the short commit.
pub fn fill_template(template: &str, repo_id: &str, pr: &PullRequest) -> String {
    let repo = github::split_repo(repo_id).map_or_else(
        || repo_id.to_string(),
        |(_, owner, name)| format!("{owner}/{name}"),
    );
    let body = if pr.body.trim().is_empty() {
        "(no description)"
    } else {
        pr.body.trim()
    };
    // {body} goes last so text in the description is never substituted.
    template
        .replace("{repo}", &repo)
        .replace("{number}", &pr.number.to_string())
        .replace("{title}", &pr.title)
        .replace("{author}", &pr.author.login)
        .replace("{base}", &pr.base_ref_name)
        .replace("{head}", names::short_sha(&pr.head_ref_oid))
        .replace("{body}", body)
}

pub fn plan(repo_id: &str, pr: &PullRequest, settings: &ReviewSettings) -> ReviewPlan {
    ReviewPlan {
        name: names::review_name(repo_id, pr.number, &pr.head_ref_oid),
        spec: format!("{repo_id}@{}", pr.head_ref_oid),
        prompt: fill_template(&settings.task_template, repo_id, pr),
        model: settings.model.clone(),
        permission_mode: settings.permission_mode.clone(),
    }
}

impl From<&ReviewPlan> for NewAgent {
    fn from(plan: &ReviewPlan) -> Self {
        NewAgent {
            name: Some(plan.name.clone()),
            specs: vec![plan.spec.clone()],
            prompt: Some(plan.prompt.clone()),
            model: Some(plan.model.clone()),
            permission_mode: Some(plan.permission_mode.clone()),
            offline: false,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::view::tests::{pr, HEAD, REPO};

    #[test]
    fn default_template_names_the_pull_request() {
        let mut p = pr(42);
        p.title = "Add rate limiting to login".to_string();
        p.body = "Closes #35.".to_string();
        let prompt = fill_template(&ReviewSettings::default().task_template, REPO, &p);
        assert!(prompt.starts_with(
            "Review pull request bobcob7/buff-bear#42, \"Add rate limiting to login\", by alice, at 3f2a9c1 against main."
        ));
        assert!(prompt.ends_with("Closes #35."));
    }

    #[test]
    fn body_placeholders_are_not_filled() {
        let mut p = pr(1);
        p.body = "literal {title}".to_string();
        assert_eq!(fill_template("{body}", REPO, &p), "literal {title}");
    }

    #[test]
    fn plan_checks_out_full_head_under_review_name() {
        let plan = plan(REPO, &pr(42), &ReviewSettings::default());
        assert_eq!(plan.name, "review-buff-bear-42-3f2a9c1");
        assert_eq!(plan.spec, format!("{REPO}@{HEAD}"));
    }
}
