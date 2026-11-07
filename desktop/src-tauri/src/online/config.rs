//! Configuration from the environment, read once at start (ADR 0053).

use std::fmt;
use std::net::SocketAddr;
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};

use argon2::password_hash::phc::PasswordHash;

const DEFAULT_ADDR: ([u8; 4], u16) = ([0, 0, 0, 0], 8080);
const DEFAULT_SESSION_DAYS: u32 = 30;
const DEFAULT_DATA_DIR: &str = "ws-online-data";
const DEFAULT_WEB_DIR: &str = "./dist";
const DEFAULT_VAPID_SUBJECT: &str = "mailto:ws-online@localhost";

/// How the login password is configured.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Password {
    /// The password as configured, compared in constant time.
    Plain(String),
    /// An argon2id PHC string from `hash-password`.
    Hash(String),
}

/// A variable that is missing or invalid.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfigError {
    pub var: &'static str,
    pub message: String,
}

impl fmt::Display for ConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.var, self.message)
    }
}

impl std::error::Error for ConfigError {}

/// The server's settings, read from the environment.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Config {
    pub username: String,
    pub password: Password,
    pub addr: SocketAddr,
    /// The public URL's origin, e.g. "https://ws.example.com" (no default port, no slash).
    pub public_origin: Option<String>,
    pub secure_cookie: bool,
    pub session_days: u32,
    pub trust_proxy: bool,
    pub data_dir: PathBuf,
    pub web_dir: PathBuf,
    pub vapid_subject: String,
}

impl Config {
    /// Reads the settings from the process environment.
    pub fn from_env() -> Result<Self, ConfigError> {
        Self::from_lookup(|key| std::env::var(key).ok())
    }

    /// Reads the settings through `get`, which returns `None` for unset variables.
    pub fn from_lookup(get: impl Fn(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let username = setting(&get, "WS_ONLINE_USERNAME")
            .ok_or_else(|| invalid("WS_ONLINE_USERNAME", "is required"))?;
        let password = password(&get)?;
        let addr = match setting(&get, "WS_ONLINE_ADDR") {
            Some(addr) => addr
                .parse()
                .map_err(|_| invalid("WS_ONLINE_ADDR", "not a socket address"))?,
            None => SocketAddr::from(DEFAULT_ADDR),
        };
        let public_url = setting(&get, "WS_ONLINE_PUBLIC_URL");
        let public_origin = public_url
            .as_deref()
            .map(|url| {
                origin_of(url).ok_or_else(|| {
                    invalid(
                        "WS_ONLINE_PUBLIC_URL",
                        "must be an http:// or https:// URL with no path",
                    )
                })
            })
            .transpose()?;
        let secure_cookie = secure_cookie(&get, public_origin.as_deref())?;
        let session_days = session_days(&get)?;
        let trust_proxy = flag(&get, "WS_ONLINE_TRUST_PROXY", false)?;
        let data_dir = data_dir(&get);
        let web_dir =
            PathBuf::from(setting(&get, "WS_ONLINE_WEB_DIR").unwrap_or(DEFAULT_WEB_DIR.into()));
        let vapid_subject = vapid_subject(&get, public_url.as_deref())?;

        Ok(Self {
            username,
            password,
            addr,
            public_origin,
            secure_cookie,
            session_days,
            trust_proxy,
            data_dir,
            web_dir,
            vapid_subject,
        })
    }

    /// Creates the data directory (mode 0700) and checks `web_dir/index.html` exists.
    pub fn prepare(&self) -> Result<(), ConfigError> {
        if !self.web_dir.join("index.html").is_file() {
            return Err(invalid(
                "WS_ONLINE_WEB_DIR",
                &format!("no index.html in {}", self.web_dir.display()),
            ));
        }
        create_private_dir(&self.data_dir)
            .map_err(|e| invalid("WS_ONLINE_DATA_DIR", &e.to_string()))
    }
}

/// "scheme://host[:port]" of an http(s) URL, lower-case, default port
/// dropped; None for anything else or a URL with a path other than "/".
pub fn origin_of(url: &str) -> Option<String> {
    let (scheme, rest) = url.split_once("://")?;
    let scheme = scheme.to_ascii_lowercase();
    let default_port = match scheme.as_str() {
        "http" => 80,
        "https" => 443,
        _ => return None,
    };
    let authority = match rest.find('/') {
        Some(slash) if &rest[slash..] == "/" => &rest[..slash],
        Some(_) => return None,
        None => rest,
    };
    if authority.is_empty() || authority.contains(['@', '?', '#', ' ']) {
        return None;
    }
    let (host, port) = match authority.rsplit_once(':') {
        Some((host, port)) if port.bytes().all(|b| b.is_ascii_digit()) && !port.is_empty() => {
            (host, port.parse::<u16>().ok()?)
        }
        Some((_, "")) => return None,
        _ => (authority, default_port),
    };
    if host.is_empty() {
        return None;
    }
    let host = host.to_ascii_lowercase();
    if port == default_port {
        Some(format!("{scheme}://{host}"))
    } else {
        Some(format!("{scheme}://{host}:{port}"))
    }
}

/// The trimmed value of `var`, or None when it is unset or blank.
fn setting(get: &impl Fn(&str) -> Option<String>, var: &str) -> Option<String> {
    get(var)
        .map(|v| v.trim().to_string())
        .filter(|v| !v.is_empty())
}

fn invalid(var: &'static str, message: &str) -> ConfigError {
    ConfigError {
        var,
        message: message.to_string(),
    }
}

fn password(get: &impl Fn(&str) -> Option<String>) -> Result<Password, ConfigError> {
    let plain = get("WS_ONLINE_PASSWORD").filter(|v| !v.is_empty());
    let hash = get("WS_ONLINE_PASSWORD_HASH").filter(|v| !v.is_empty());
    match (plain, hash) {
        (Some(plain), None) => Ok(Password::Plain(plain)),
        (None, Some(hash)) => {
            PasswordHash::new(&hash).map_err(|e| {
                invalid(
                    "WS_ONLINE_PASSWORD_HASH",
                    &format!("not an argon2 hash: {e}"),
                )
            })?;
            Ok(Password::Hash(hash))
        }
        (Some(_), Some(_)) => Err(invalid(
            "WS_ONLINE_PASSWORD",
            "set either WS_ONLINE_PASSWORD or WS_ONLINE_PASSWORD_HASH, not both",
        )),
        (None, None) => Err(invalid(
            "WS_ONLINE_PASSWORD",
            "set WS_ONLINE_PASSWORD or WS_ONLINE_PASSWORD_HASH",
        )),
    }
}

fn secure_cookie(
    get: &impl Fn(&str) -> Option<String>,
    public_origin: Option<&str>,
) -> Result<bool, ConfigError> {
    match setting(get, "WS_ONLINE_SECURE_COOKIE").as_deref() {
        None | Some("auto") => Ok(public_origin.is_some_and(|o| o.starts_with("https://"))),
        Some("true") => Ok(true),
        Some("false") => Ok(false),
        Some(_) => Err(invalid(
            "WS_ONLINE_SECURE_COOKIE",
            "must be auto, true or false",
        )),
    }
}

fn session_days(get: &impl Fn(&str) -> Option<String>) -> Result<u32, ConfigError> {
    let Some(days) = setting(get, "WS_ONLINE_SESSION_DAYS") else {
        return Ok(DEFAULT_SESSION_DAYS);
    };
    days.parse::<u32>()
        .ok()
        .filter(|d| (1..=365).contains(d))
        .ok_or_else(|| {
            invalid(
                "WS_ONLINE_SESSION_DAYS",
                "must be a whole number from 1 to 365",
            )
        })
}

fn flag(
    get: &impl Fn(&str) -> Option<String>,
    var: &'static str,
    default: bool,
) -> Result<bool, ConfigError> {
    match setting(get, var).as_deref() {
        None => Ok(default),
        Some("true") => Ok(true),
        Some("false") => Ok(false),
        Some(_) => Err(invalid(var, "must be true or false")),
    }
}

fn data_dir(get: &impl Fn(&str) -> Option<String>) -> PathBuf {
    if let Some(dir) = setting(get, "WS_ONLINE_DATA_DIR") {
        return PathBuf::from(dir);
    }
    match setting(get, "WS_CONFIG_DIR") {
        Some(dir) => Path::new(&dir).join("online"),
        None => PathBuf::from(DEFAULT_DATA_DIR),
    }
}

fn vapid_subject(
    get: &impl Fn(&str) -> Option<String>,
    public_url: Option<&str>,
) -> Result<String, ConfigError> {
    match setting(get, "WS_ONLINE_VAPID_SUBJECT") {
        Some(subject) if subject.starts_with("mailto:") || subject.starts_with("https://") => {
            Ok(subject)
        }
        Some(_) => Err(invalid(
            "WS_ONLINE_VAPID_SUBJECT",
            "must start with mailto: or https://",
        )),
        None => Ok(public_url
            .filter(|url| url.starts_with("https://"))
            .map(|url| url.trim_end_matches('/').to_string())
            .unwrap_or_else(|| DEFAULT_VAPID_SUBJECT.to_string())),
    }
}

fn create_private_dir(path: &Path) -> std::io::Result<()> {
    std::fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    const MINIMAL: &[(&str, &str)] = &[
        ("WS_ONLINE_USERNAME", "eric"),
        ("WS_ONLINE_PASSWORD", "secret"),
    ];

    fn lookup(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let map: HashMap<String, String> = pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect();
        move |key| map.get(key).cloned()
    }

    fn config_with(extra: &[(&str, &str)]) -> Result<Config, ConfigError> {
        let mut pairs = MINIMAL.to_vec();
        pairs.extend_from_slice(extra);
        Config::from_lookup(lookup(&pairs))
    }

    fn error_var(extra: &[(&str, &str)]) -> &'static str {
        config_with(extra).unwrap_err().var
    }

    #[test]
    fn minimal_environment_gives_every_default() {
        let config = config_with(&[]).unwrap();
        assert_eq!(config.username, "eric");
        assert_eq!(config.password, Password::Plain("secret".into()));
        assert_eq!(config.addr, "0.0.0.0:8080".parse().unwrap());
        assert_eq!(config.public_origin, None);
        assert!(!config.secure_cookie);
        assert_eq!(config.session_days, 30);
        assert!(!config.trust_proxy);
        assert_eq!(config.data_dir, PathBuf::from("ws-online-data"));
        assert_eq!(config.web_dir, PathBuf::from("./dist"));
        assert_eq!(config.vapid_subject, "mailto:ws-online@localhost");
    }

    #[test]
    fn data_dir_comes_from_config_dir_when_set() {
        let config = config_with(&[("WS_CONFIG_DIR", "/data/ws")]).unwrap();
        assert_eq!(config.data_dir, PathBuf::from("/data/ws/online"));
    }

    #[test]
    fn blank_values_count_as_unset() {
        let config =
            config_with(&[("WS_ONLINE_ADDR", "  "), ("WS_ONLINE_SESSION_DAYS", "")]).unwrap();
        assert_eq!(config.session_days, 30);
    }

    #[test]
    fn missing_username_names_the_variable() {
        let pairs = [("WS_ONLINE_PASSWORD", "secret")];
        let err = Config::from_lookup(lookup(&pairs)).unwrap_err();
        assert_eq!(err.var, "WS_ONLINE_USERNAME");
    }

    #[test]
    fn blank_username_is_missing() {
        assert_eq!(
            error_var(&[("WS_ONLINE_USERNAME", "   ")]),
            "WS_ONLINE_USERNAME"
        );
    }

    #[test]
    fn username_is_trimmed() {
        let config = config_with(&[("WS_ONLINE_USERNAME", " eric ")]).unwrap();
        assert_eq!(config.username, "eric");
    }

    #[test]
    fn both_passwords_are_an_error() {
        let pairs = [
            ("WS_ONLINE_USERNAME", "eric"),
            ("WS_ONLINE_PASSWORD", "secret"),
            (
                "WS_ONLINE_PASSWORD_HASH",
                "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
            ),
        ];
        let err = Config::from_lookup(lookup(&pairs)).unwrap_err();
        assert_eq!(err.var, "WS_ONLINE_PASSWORD");
    }

    #[test]
    fn no_password_is_an_error() {
        let pairs = [("WS_ONLINE_USERNAME", "eric")];
        let err = Config::from_lookup(lookup(&pairs)).unwrap_err();
        assert_eq!(err.var, "WS_ONLINE_PASSWORD");
    }

    #[test]
    fn password_hash_is_accepted_when_it_parses() {
        let hash = crate::online::auth::hash_password("secret").unwrap();
        let pairs = [
            ("WS_ONLINE_USERNAME", "eric"),
            ("WS_ONLINE_PASSWORD_HASH", hash.as_str()),
        ];
        let config = Config::from_lookup(lookup(&pairs)).unwrap();
        assert_eq!(config.password, Password::Hash(hash));
    }

    #[test]
    fn bad_password_hash_names_its_variable() {
        let pairs = [
            ("WS_ONLINE_USERNAME", "eric"),
            ("WS_ONLINE_PASSWORD_HASH", "not-a-hash"),
        ];
        let err = Config::from_lookup(lookup(&pairs)).unwrap_err();
        assert_eq!(err.var, "WS_ONLINE_PASSWORD_HASH");
    }

    #[test]
    fn bad_address_names_the_variable() {
        assert_eq!(
            error_var(&[("WS_ONLINE_ADDR", "localhost")]),
            "WS_ONLINE_ADDR"
        );
    }

    #[test]
    fn session_days_must_be_from_one_to_365() {
        assert_eq!(
            config_with(&[("WS_ONLINE_SESSION_DAYS", "1")])
                .unwrap()
                .session_days,
            1
        );
        assert_eq!(
            config_with(&[("WS_ONLINE_SESSION_DAYS", "365")])
                .unwrap()
                .session_days,
            365
        );
        assert_eq!(
            error_var(&[("WS_ONLINE_SESSION_DAYS", "0")]),
            "WS_ONLINE_SESSION_DAYS"
        );
        assert_eq!(
            error_var(&[("WS_ONLINE_SESSION_DAYS", "366")]),
            "WS_ONLINE_SESSION_DAYS"
        );
    }

    #[test]
    fn secure_cookie_rejects_unknown_values() {
        assert_eq!(
            error_var(&[("WS_ONLINE_SECURE_COOKIE", "maybe")]),
            "WS_ONLINE_SECURE_COOKIE"
        );
    }

    #[test]
    fn secure_cookie_auto_is_secure_only_with_https_public_url() {
        let https = config_with(&[("WS_ONLINE_PUBLIC_URL", "https://ws.example.com")]).unwrap();
        let http = config_with(&[("WS_ONLINE_PUBLIC_URL", "http://localhost:8080")]).unwrap();
        let none = config_with(&[]).unwrap();
        assert!(https.secure_cookie);
        assert!(!http.secure_cookie);
        assert!(!none.secure_cookie);
    }

    #[test]
    fn secure_cookie_can_be_forced() {
        let on = config_with(&[("WS_ONLINE_SECURE_COOKIE", "true")]).unwrap();
        let off = config_with(&[
            ("WS_ONLINE_SECURE_COOKIE", "false"),
            ("WS_ONLINE_PUBLIC_URL", "https://ws.example.com"),
        ])
        .unwrap();
        assert!(on.secure_cookie);
        assert!(!off.secure_cookie);
    }

    #[test]
    fn trust_proxy_must_be_a_boolean() {
        assert!(
            config_with(&[("WS_ONLINE_TRUST_PROXY", "true")])
                .unwrap()
                .trust_proxy
        );
        assert_eq!(
            error_var(&[("WS_ONLINE_TRUST_PROXY", "yes")]),
            "WS_ONLINE_TRUST_PROXY"
        );
    }

    #[test]
    fn public_url_with_a_path_is_an_error() {
        assert_eq!(
            error_var(&[("WS_ONLINE_PUBLIC_URL", "https://ws.example.com/app")]),
            "WS_ONLINE_PUBLIC_URL"
        );
    }

    #[test]
    fn public_url_sets_origin_and_vapid_subject() {
        let config = config_with(&[("WS_ONLINE_PUBLIC_URL", "https://ws.example.com/")]).unwrap();
        assert_eq!(
            config.public_origin.as_deref(),
            Some("https://ws.example.com")
        );
        assert_eq!(config.vapid_subject, "https://ws.example.com");
    }

    #[test]
    fn http_public_url_leaves_the_default_vapid_subject() {
        let config = config_with(&[("WS_ONLINE_PUBLIC_URL", "http://localhost:8080")]).unwrap();
        assert_eq!(config.vapid_subject, DEFAULT_VAPID_SUBJECT);
    }

    #[test]
    fn vapid_subject_must_be_mailto_or_https() {
        assert_eq!(
            error_var(&[("WS_ONLINE_VAPID_SUBJECT", "ws@x")]),
            "WS_ONLINE_VAPID_SUBJECT"
        );
        let config = config_with(&[("WS_ONLINE_VAPID_SUBJECT", "mailto:ops@example.com")]).unwrap();
        assert_eq!(config.vapid_subject, "mailto:ops@example.com");
    }

    #[test]
    fn origin_of_drops_default_ports_and_keeps_others() {
        assert_eq!(
            origin_of("https://ws.example.com:443").as_deref(),
            Some("https://ws.example.com")
        );
        assert_eq!(
            origin_of("http://ws.example.com:80").as_deref(),
            Some("http://ws.example.com")
        );
        assert_eq!(
            origin_of("https://ws.example.com:8443").as_deref(),
            Some("https://ws.example.com:8443")
        );
    }

    #[test]
    fn origin_of_lower_cases_and_accepts_a_trailing_slash() {
        assert_eq!(
            origin_of("HTTPS://WS.Example.com/").as_deref(),
            Some("https://ws.example.com")
        );
    }

    #[test]
    fn origin_of_rejects_other_schemes_paths_and_empty_hosts() {
        assert_eq!(origin_of("ftp://ws.example.com"), None);
        assert_eq!(origin_of("https://ws.example.com/app"), None);
        assert_eq!(origin_of("https://"), None);
        assert_eq!(origin_of("https://ws.example.com:"), None);
        assert_eq!(origin_of("ws.example.com"), None);
    }

    #[test]
    fn prepare_creates_the_data_dir() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("index.html"), "<html></html>").unwrap();
        let config = config_with(&[
            ("WS_ONLINE_WEB_DIR", dir.path().to_str().unwrap()),
            (
                "WS_ONLINE_DATA_DIR",
                dir.path().join("data/online").to_str().unwrap(),
            ),
        ])
        .unwrap();
        config.prepare().unwrap();
        assert!(config.data_dir.is_dir());
    }

    #[test]
    fn prepare_fails_without_index_html() {
        let dir = tempfile::tempdir().unwrap();
        let config = config_with(&[
            ("WS_ONLINE_WEB_DIR", dir.path().to_str().unwrap()),
            (
                "WS_ONLINE_DATA_DIR",
                dir.path().join("data").to_str().unwrap(),
            ),
        ])
        .unwrap();
        let err = config.prepare().unwrap_err();
        assert_eq!(err.var, "WS_ONLINE_WEB_DIR");
        assert!(!dir.path().join("data").exists());
    }
}
