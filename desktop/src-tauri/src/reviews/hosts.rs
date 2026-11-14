//! config.toml's [[hosts]], read and written through `ws config` (ADR 0061).

use super::forge::ForgeChoice;
use super::store::{ForgeHost, ForgeSettings, Store};
use crate::runner::{AppError, Runner};
use crate::ws_config::{self, ConfigSetting};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

const KEY: &str = "hosts";

/// One [[hosts]] entry.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ConfiguredHost {
    pub name: String,
    pub kind: ForgeChoice,
}

/// The effective hosts, and whether config.toml sets them.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct HostList {
    pub hosts: Vec<ConfiguredHost>,
    pub set: bool,
}

/// `ws config get --json hosts`.
pub async fn read(runner: &Runner) -> Result<HostList, AppError> {
    let got = ws_config::get(runner, KEY).await?;
    parse(got.setting)
}

/// `ws config set --json -- hosts name=kind…`; an empty list runs
/// `ws config unset --json -- hosts`. Returns the list as ws reports it.
pub async fn write(runner: &Runner, hosts: &[ConfiguredHost]) -> Result<HostList, AppError> {
    if hosts.is_empty() {
        let change = ws_config::unset(runner, KEY).await?;
        return parse(change.setting);
    }
    let values: Vec<String> = hosts.iter().map(argument).collect();
    let change = ws_config::set(runner, KEY, &values).await?;
    parse(change.setting)
}

/// The host's kind when listed, else Auto.
pub fn choice(hosts: &[ConfiguredHost], host: &str) -> ForgeChoice {
    hosts
        .iter()
        .find(|h| h.name == host)
        .map_or(ForgeChoice::Auto, |h| h.kind)
}

/// Copies desktop.json's settings.forges into config.toml once (ADR 0061).
/// Ok(true) when it moved or cleared anything.
pub async fn migrate(runner: &Runner, store: &Store) -> Result<bool, AppError> {
    let legacy = store.get().settings.forges.hosts;
    if legacy.is_empty() {
        return Ok(false);
    }
    let current = read(runner).await?;
    if !current.set {
        write(runner, &merged(current.hosts, &legacy)).await?;
    }
    store.update(|d| d.settings.forges = ForgeSettings::default())?;
    Ok(true)
}

fn parse(setting: ConfigSetting) -> Result<HostList, AppError> {
    let hosts: Vec<ConfiguredHost> = serde_json::from_value(setting.value)
        .map_err(|_| AppError::new("config_invalid", "ws config: hosts isn't a list of hosts"))?;
    Ok(HostList {
        hosts,
        set: setting.set,
    })
}

fn argument(host: &ConfiguredHost) -> String {
    format!("{}={}", host.name, kind_name(host.kind))
}

fn kind_name(kind: ForgeChoice) -> &'static str {
    match kind {
        ForgeChoice::Auto => "auto",
        ForgeChoice::Github => "github",
        ForgeChoice::Forgejo => "forgejo",
    }
}

/// The implied list with the legacy kinds applied: a listed host takes its
/// kind, and the others follow in key order.
fn merged(
    mut hosts: Vec<ConfiguredHost>,
    legacy: &BTreeMap<String, ForgeHost>,
) -> Vec<ConfiguredHost> {
    for (name, entry) in legacy {
        let name = name.to_lowercase();
        match hosts.iter_mut().find(|h| h.name == name) {
            Some(host) => host.kind = entry.kind,
            None => hosts.push(ConfiguredHost {
                name,
                kind: entry.kind,
            }),
        }
    }
    hosts
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ws_config::tests::{args, fake_ws};

    const CHANGED: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"github.com","kind":"github"},{"name":"git.example.com","kind":"forgejo"}],"default":[{"name":"github.com","kind":"auto"}],"set":true},"changed":true,"warnings":[]}"#;

    const DEFAULT_GET: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"github.com","kind":"auto"}],"default":[{"name":"github.com","kind":"auto"}],"set":false}}"#;

    const SET_GET: &str = r#"{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[{"name":"a.com","kind":"forgejo"}],"default":[{"name":"github.com","kind":"auto"}],"set":true}}"#;

    /// A fake `ws` that answers `config get` with `get` and any other
    /// config call with a change.
    fn fake_config(get: &str) -> (tempfile::TempDir, Runner) {
        fake_ws(&format!(
            "case \"$3\" in get) echo '{get}' ;; *) echo '{CHANGED}' ;; esac"
        ))
    }

    fn host(name: &str, kind: ForgeChoice) -> ConfiguredHost {
        ConfiguredHost {
            name: name.to_string(),
            kind,
        }
    }

    fn store_with_legacy(hosts: &[(&str, ForgeChoice)]) -> Store {
        let store = Store::open(None);
        store
            .update(|d| {
                for (name, kind) in hosts {
                    d.settings
                        .forges
                        .hosts
                        .insert(name.to_string(), ForgeHost { kind: *kind });
                }
            })
            .unwrap();
        store
    }

    #[tokio::test]
    async fn read_parses_the_hosts_setting() {
        let (dir, runner) = fake_config(DEFAULT_GET);
        let list = read(&runner).await.unwrap();
        assert_eq!(args(&dir), "config --json get -- hosts");
        assert_eq!(
            list,
            HostList {
                hosts: vec![host("github.com", ForgeChoice::Auto)],
                set: false,
            }
        );
    }

    #[tokio::test]
    async fn write_passes_name_equals_kind() {
        let (dir, runner) = fake_config(DEFAULT_GET);
        let hosts = [
            host("github.com", ForgeChoice::Github),
            host("git.example.com", ForgeChoice::Forgejo),
        ];
        let list = write(&runner, &hosts).await.unwrap();
        assert_eq!(
            args(&dir),
            "config --json set -- hosts github.com=github git.example.com=forgejo"
        );
        assert!(list.set);
    }

    #[tokio::test]
    async fn write_of_nothing_unsets() {
        let (dir, runner) = fake_config(DEFAULT_GET);
        write(&runner, &[]).await.unwrap();
        assert_eq!(args(&dir), "config --json unset -- hosts");
    }

    #[tokio::test]
    async fn read_keeps_the_ws_error_code() {
        let (_dir, runner) =
            fake_ws(r#"echo '{"error":{"code":"config_invalid","message":"bad"}}' >&2; exit 1"#);
        let err = read(&runner).await.unwrap_err();
        assert_eq!(err.code, "config_invalid");
    }

    #[test]
    fn choice_is_auto_when_unlisted() {
        let hosts = [host("github.com", ForgeChoice::Github)];
        assert_eq!(choice(&hosts, "github.com"), ForgeChoice::Github);
        assert_eq!(choice(&hosts, "git.example.com"), ForgeChoice::Auto);
    }

    #[tokio::test]
    async fn migrate_without_legacy_does_nothing() {
        let store = Store::open(None);
        let (dir, runner) = fake_config(DEFAULT_GET);
        assert!(!migrate(&runner, &store).await.unwrap());
        assert!(!dir.path().join("args").exists());
    }

    #[tokio::test]
    async fn migrate_copies_kinds_after_the_primary_and_clears_desktop_json() {
        let store = store_with_legacy(&[
            ("git.example.com", ForgeChoice::Forgejo),
            ("github.com", ForgeChoice::Github),
        ]);
        let (dir, runner) = fake_config(DEFAULT_GET);
        assert!(migrate(&runner, &store).await.unwrap());
        assert_eq!(
            args(&dir),
            "config --json set -- hosts github.com=github git.example.com=forgejo"
        );
        assert!(store.get().settings.forges.hosts.is_empty());
    }

    #[tokio::test]
    async fn migrate_when_hosts_are_set_only_clears() {
        let store = store_with_legacy(&[("git.example.com", ForgeChoice::Forgejo)]);
        let (dir, runner) = fake_config(SET_GET);
        assert!(migrate(&runner, &store).await.unwrap());
        assert_eq!(args(&dir), "config --json get -- hosts");
        assert!(store.get().settings.forges.hosts.is_empty());
    }

    #[tokio::test]
    async fn migrate_twice_writes_once() {
        let store = store_with_legacy(&[("git.example.com", ForgeChoice::Forgejo)]);
        let (dir, runner) = fake_config(DEFAULT_GET);
        assert!(migrate(&runner, &store).await.unwrap());
        std::fs::remove_file(dir.path().join("args")).unwrap();
        assert!(!migrate(&runner, &store).await.unwrap());
        assert!(!dir.path().join("args").exists());
    }
}
