//! Review workspace names and how a workspace is matched to its pull
//! request (ADR 0032).

use crate::model::Repo;

/// `ws` cuts a `--name` to this many characters before it adds the id's
/// random suffix (ADRs 0012, 0036).
const MAX_NAME_LEN: usize = 55;
const PREFIX: &str = "review-";
const SHA_LEN: usize = 7;

/// Lowercases and replaces every run of characters other than ASCII letters
/// and digits with one "-", as `ws` does for `--name`. GitHub repo names
/// are ASCII, so `ws`'s accent folding never applies.
pub fn sanitize(name: &str) -> String {
    let mut out = String::with_capacity(name.len());
    for c in name.chars() {
        if c.is_ascii_alphanumeric() {
            out.push(c.to_ascii_lowercase());
        } else if !out.is_empty() && !out.ends_with('-') {
            out.push('-');
        }
    }
    out.trim_end_matches('-').to_string()
}

/// The repo's name: the last part of `host/owner/name`.
pub fn repo_name(repo_id: &str) -> &str {
    repo_id.rsplit('/').next().unwrap_or(repo_id)
}

/// The repo's name, sanitized and shortened so the whole review name fits
/// in 55 characters and `ws` never cuts off the number or commit.
fn short_repo(repo_id: &str, number: u64) -> String {
    let digits = number.to_string().len();
    let max = MAX_NAME_LEN - PREFIX.len() - 2 - digits - SHA_LEN;
    let name = sanitize(repo_name(repo_id));
    if name.len() <= max {
        return name;
    }
    name[..max].trim_end_matches('-').to_string()
}

pub fn short_sha(sha: &str) -> &str {
    sha.get(..SHA_LEN).unwrap_or(sha)
}

/// `review-<repo>-<number>-<sha7>`: the `--name` of a review, which `ws`
/// makes the id from by adding a random suffix (ADR 0036).
pub fn review_name(repo_id: &str, number: u64, head: &str) -> String {
    format!(
        "{}{}",
        review_prefix(repo_id, number),
        sanitize(short_sha(head))
    )
}

/// What every review of this pull request's workspace id starts with.
pub fn review_prefix(repo_id: &str, number: u64) -> String {
    format!("{PREFIX}{}-{number}-", short_repo(repo_id, number))
}

/// Whether a workspace is a review of the pull request: its id has the
/// review prefix and it has exactly one repo, the pull request's.
pub fn is_review_of(workspace_id: &str, repos: &[Repo], repo_id: &str, number: u64) -> bool {
    workspace_id.starts_with(&review_prefix(repo_id, number))
        && matches!(repos, [only] if only.id == repo_id)
}

/// Whether `ws` made workspace id from `--name name`: the name, a dash, and
/// 8 hex digits (ADR 0036), or the name itself, as ids were before that.
pub fn is_named(id: &str, name: &str) -> bool {
    let Some(rest) = id.strip_prefix(name) else {
        return false;
    };
    rest.is_empty()
        || rest
            .strip_prefix('-')
            .is_some_and(|s| s.len() == 8 && s.bytes().all(|b| b.is_ascii_hexdigit()))
}

/// Whether a checked-out ref is the commit `sha`. `ws` records the ref as
/// given, which for a review is the full hash, but a review started by hand
/// may name a shorter one.
pub fn same_commit(git_ref: &str, sha: &str) -> bool {
    git_ref.len() >= SHA_LEN && (sha.starts_with(git_ref) || git_ref.starts_with(sha))
}

#[cfg(test)]
mod tests {
    use super::*;

    const HEAD: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

    fn repo(id: &str) -> Repo {
        Repo {
            id: id.to_string(),
            path: "/w/r".to_string(),
            git_ref: Some(HEAD.to_string()),
            ref_kind: Some("commit".to_string()),
        }
    }

    #[test]
    fn review_name_has_repo_number_and_short_sha() {
        assert_eq!(
            review_name("github.com/bobcob7/buff-bear", 42, HEAD),
            "review-buff-bear-42-3f2a9c1"
        );
    }

    #[test]
    fn review_name_sanitizes_repo_name() {
        assert_eq!(
            review_name("github.com/o/My.Repo_Name", 7, HEAD),
            "review-my-repo-name-7-3f2a9c1"
        );
    }

    #[test]
    fn review_name_shortens_long_repo_to_55_characters() {
        let repo_id = format!("github.com/o/{}", "a".repeat(80));
        let name = review_name(&repo_id, 12345, HEAD);
        assert_eq!(name.len(), 55);
        assert!(name.ends_with("-12345-3f2a9c1"));
    }

    #[test]
    fn review_name_drops_dash_left_by_shortening() {
        let repo_id = format!("github.com/o/{}-b", "a".repeat(37));
        assert_eq!(
            review_name(&repo_id, 1, HEAD),
            format!("review-{}-1-3f2a9c1", "a".repeat(37))
        );
    }

    #[test]
    fn is_review_of_matches_prefix_and_single_repo() {
        let id = "github.com/bobcob7/buff-bear";
        assert!(is_review_of(
            "review-buff-bear-42-3f2a9c1",
            &[repo(id)],
            id,
            42
        ));
    }

    #[test]
    fn is_named_matches_name_with_suffix_or_alone() {
        let name = "review-buff-bear-42-3f2a9c1";
        assert!(is_named("review-buff-bear-42-3f2a9c1-d8efb24f", name));
        assert!(is_named(name, name));
        assert!(!is_named("review-buff-bear-42-3f2a9c1e-d8efb24f", name));
        assert!(!is_named("review-buff-bear-42-3f2a9c1-d8efb24", name));
    }

    #[test]
    fn is_review_of_matches_id_with_random_suffix() {
        let id = "github.com/bobcob7/buff-bear";
        assert!(is_review_of(
            "review-buff-bear-42-3f2a9c1-d8efb24f",
            &[repo(id)],
            id,
            42
        ));
    }

    #[test]
    fn is_review_of_rejects_other_number_with_same_digits_prefix() {
        let id = "github.com/bobcob7/buff-bear";
        assert!(!is_review_of(
            "review-buff-bear-420-3f2a9c1",
            &[repo(id)],
            id,
            42
        ));
    }

    #[test]
    fn is_review_of_rejects_same_name_in_other_owner() {
        assert!(!is_review_of(
            "review-buff-bear-42-3f2a9c1",
            &[repo("github.com/alice/buff-bear")],
            "github.com/bobcob7/buff-bear",
            42
        ));
    }

    #[test]
    fn is_review_of_rejects_two_repos() {
        let id = "github.com/bobcob7/buff-bear";
        assert!(!is_review_of(
            "review-buff-bear-42-3f2a9c1",
            &[repo(id), repo("github.com/bobcob7/api")],
            id,
            42
        ));
    }

    #[test]
    fn same_commit_accepts_full_or_abbreviated_hash() {
        assert!(same_commit(HEAD, HEAD));
        assert!(same_commit("3f2a9c1", HEAD));
        assert!(!same_commit("3f2a9c2", HEAD));
        assert!(!same_commit("main", HEAD));
    }
}
