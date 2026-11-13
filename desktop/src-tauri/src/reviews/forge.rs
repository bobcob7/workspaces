//! Which forge each host runs, and how that's decided (ADR 0060).

use serde::{Deserialize, Serialize};
use std::collections::BTreeSet;

const GITHUB_COM: &str = "github.com";

/// The forge a host runs.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ForgeKind {
    #[default]
    Github,
    Forgejo,
}

/// A host's setting in Settings › Forges; `Auto` detects the forge.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ForgeChoice {
    #[default]
    Auto,
    Github,
    Forgejo,
}

/// The host of a cache id: "git.example.com:3000" from "git.example.com:3000/team/svc".
#[allow(dead_code)] // used by T4
pub fn host_of(repo_id: &str) -> Option<&str> {
    let (host, _) = repo_id.split_once('/')?;
    Some(host).filter(|h| !h.is_empty())
}

/// Rules 1–4 of ADR 0060. `probed`: None = not probed yet (or unreachable),
/// Some(true) = Forgejo, Some(false) = not Forgejo.
#[allow(dead_code)] // used by T4
pub fn resolve_kind(
    host: &str,
    choice: ForgeChoice,
    gh_hosts: &BTreeSet<String>,
    probed: Option<bool>,
) -> Option<ForgeKind> {
    match choice {
        ForgeChoice::Github => Some(ForgeKind::Github),
        ForgeChoice::Forgejo => Some(ForgeKind::Forgejo),
        ForgeChoice::Auto if host == GITHUB_COM || gh_hosts.contains(host) => {
            Some(ForgeKind::Github)
        }
        ForgeChoice::Auto if probed == Some(true) => Some(ForgeKind::Forgejo),
        ForgeChoice::Auto => None,
    }
}

/// Whether `resolve_kind` needs a probe first: choice Auto, not github.com,
/// not in gh_hosts.
#[allow(dead_code)] // used by T4
pub fn needs_probe(host: &str, choice: ForgeChoice, gh_hosts: &BTreeSet<String>) -> bool {
    choice == ForgeChoice::Auto && host != GITHUB_COM && !gh_hosts.contains(host)
}

/// A host name Settings accepts: non-empty; letters, digits, '.', '-', and
/// one optional ':<port>' (digits); no '/', '@' or whitespace.
#[allow(dead_code)] // used by T4
pub fn is_host(host: &str) -> bool {
    let (name, port) = match host.split_once(':') {
        Some((name, port)) => (name, Some(port)),
        None => (host, None),
    };
    let name_ok = !name.is_empty()
        && name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-');
    let port_ok = port.is_none_or(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_digit()));
    name_ok && port_ok
}

#[cfg(test)]
mod tests {
    use super::*;

    fn gh_hosts(hosts: &[&str]) -> BTreeSet<String> {
        hosts.iter().map(|h| h.to_string()).collect()
    }

    #[test]
    fn host_of_keeps_the_port() {
        assert_eq!(
            host_of("git.example.com:3000/team/svc"),
            Some("git.example.com:3000")
        );
        assert_eq!(host_of("github.com"), None);
    }

    #[test]
    fn explicit_choice_wins() {
        let none = gh_hosts(&[]);
        assert_eq!(
            resolve_kind("github.com", ForgeChoice::Forgejo, &none, None),
            Some(ForgeKind::Forgejo)
        );
        assert_eq!(
            resolve_kind("git.example.com", ForgeChoice::Github, &none, Some(true)),
            Some(ForgeKind::Github)
        );
    }

    #[test]
    fn github_com_and_gh_hosts_are_github() {
        let ghe = gh_hosts(&["ghe.example.com"]);
        assert_eq!(
            resolve_kind("github.com", ForgeChoice::Auto, &ghe, None),
            Some(ForgeKind::Github)
        );
        assert_eq!(
            resolve_kind("ghe.example.com", ForgeChoice::Auto, &ghe, None),
            Some(ForgeKind::Github)
        );
    }

    #[test]
    fn probed_forgejo_is_forgejo() {
        assert_eq!(
            resolve_kind(
                "git.example.com",
                ForgeChoice::Auto,
                &gh_hosts(&[]),
                Some(true)
            ),
            Some(ForgeKind::Forgejo)
        );
    }

    #[test]
    fn unprobed_or_rejected_host_has_no_forge() {
        let none = gh_hosts(&[]);
        assert_eq!(
            resolve_kind("gitlab.example.com", ForgeChoice::Auto, &none, None),
            None
        );
        assert_eq!(
            resolve_kind("gitlab.example.com", ForgeChoice::Auto, &none, Some(false)),
            None
        );
    }

    #[test]
    fn needs_probe_only_for_auto_unknown_hosts() {
        let ghe = gh_hosts(&["ghe.example.com"]);
        assert!(needs_probe("git.example.com", ForgeChoice::Auto, &ghe));
        assert!(!needs_probe("github.com", ForgeChoice::Auto, &ghe));
        assert!(!needs_probe("ghe.example.com", ForgeChoice::Auto, &ghe));
        assert!(!needs_probe("git.example.com", ForgeChoice::Forgejo, &ghe));
    }

    #[test]
    fn is_host_accepts_names_and_ports() {
        assert!(is_host("git.example.com"));
        assert!(is_host("git.example.com:3000"));
        assert!(is_host("localhost"));
    }

    #[test]
    fn is_host_rejects_paths_users_spaces_and_empty() {
        assert!(!is_host(""));
        assert!(!is_host("git.example.com/team"));
        assert!(!is_host("user@git.example.com"));
        assert!(!is_host("git example.com"));
        assert!(!is_host("git.example.com:"));
        assert!(!is_host("git.example.com:30a0"));
    }

    #[test]
    fn forge_choice_serializes_lowercase() {
        assert_eq!(
            serde_json::to_value([ForgeChoice::Auto, ForgeChoice::Github, ForgeChoice::Forgejo])
                .unwrap(),
            serde_json::json!(["auto", "github", "forgejo"])
        );
    }
}
