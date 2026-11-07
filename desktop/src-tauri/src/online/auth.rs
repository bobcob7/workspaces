//! Login, sessions, rate limiting and origin checks (ADR 0053).

use std::collections::{HashMap, VecDeque};
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::{Mutex, MutexGuard, PoisonError};

use argon2::password_hash::phc::PasswordHash;
use argon2::password_hash::{PasswordHasher, PasswordVerifier};
use argon2::Argon2;
use base64ct::{Base64UrlUnpadded, Encoding};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;

use super::config::Password;

/// The name of the session cookie.
pub const COOKIE: &str = "ws_online_session";

const SESSIONS_VERSION: u32 = 1;
const DAY_MS: i64 = 24 * 60 * 60 * 1000;

/// The configured user, compared in constant time.
pub struct Credentials {
    username: String,
    password: Password,
}

impl Credentials {
    pub fn new(username: String, password: Password) -> Self {
        Self { username, password }
    }

    /// Checks both fields fully, whatever the first one gave.
    pub fn verify(&self, username: &str, password: &str) -> bool {
        let user_ok = digest_eq(username, &self.username);
        let password_ok = match &self.password {
            Password::Plain(expected) => digest_eq(password, expected),
            Password::Hash(hash) => verify_hash(password, hash),
        };
        user_ok & password_ok
    }

    /// hex(sha256(username + "\n" + plain password or hash)), stored with sessions.
    pub fn fingerprint(&self) -> String {
        let secret = match &self.password {
            Password::Plain(password) => password,
            Password::Hash(hash) => hash,
        };
        hex(&sha256(&format!("{}\n{secret}", self.username)))
    }
}

/// An argon2id PHC string for `password`, for `ws-online hash-password`.
pub fn hash_password(password: &str) -> Result<String, String> {
    Argon2::default()
        .hash_password(password.as_bytes())
        .map(|hash| hash.to_string())
        .map_err(|e| e.to_string())
}

/// 32 random bytes, base64url without padding.
pub fn new_token() -> String {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes).expect("system random source failed");
    Base64UrlUnpadded::encode_string(&bytes)
}

/// hex(sha256(token)).
pub fn token_hash(token: &str) -> String {
    hex(&sha256(token))
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Record {
    hash: String,
    expires_ms: i64,
    cred: String,
}

#[derive(Serialize, Deserialize)]
struct SessionsFile {
    version: u32,
    sessions: Vec<Record>,
}

/// Live sessions, persisted to `sessions.json` (design README).
pub struct Sessions {
    path: PathBuf,
    ttl_ms: i64,
    cred: String,
    records: Mutex<Vec<Record>>,
}

impl Sessions {
    /// Reads the sessions in `path`, keeping those not expired and issued for `cred`.
    /// A missing or unreadable file gives no sessions.
    pub fn load(path: PathBuf, days: u32, cred: String, now_ms: i64) -> Self {
        let records = read_records(&path)
            .into_iter()
            .filter(|r| r.expires_ms > now_ms && r.cred == cred)
            .collect();
        Self {
            path,
            ttl_ms: i64::from(days) * DAY_MS,
            cred,
            records: Mutex::new(records),
        }
    }

    /// Starts a session and returns its token; persists the list.
    pub fn create(&self, now_ms: i64) -> std::io::Result<String> {
        let token = new_token();
        let mut records = self.lock();
        records.retain(|r| r.expires_ms > now_ms);
        records.push(Record {
            hash: token_hash(&token),
            expires_ms: now_ms + self.ttl_ms,
            cred: self.cred.clone(),
        });
        self.persist(&records)?;
        Ok(token)
    }

    /// Whether `token` names a live session at `now_ms`.
    pub fn is_valid(&self, token: &str, now_ms: i64) -> bool {
        let hash = token_hash(token);
        self.lock()
            .iter()
            .any(|r| r.hash == hash && r.expires_ms > now_ms && r.cred == self.cred)
    }

    /// Ends the session for `token`; persists the list.
    pub fn remove(&self, token: &str) -> std::io::Result<()> {
        let hash = token_hash(token);
        let mut records = self.lock();
        records.retain(|r| r.hash != hash);
        self.persist(&records)
    }

    fn lock(&self) -> MutexGuard<'_, Vec<Record>> {
        self.records.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn persist(&self, records: &[Record]) -> std::io::Result<()> {
        let file = SessionsFile {
            version: SESSIONS_VERSION,
            sessions: records.to_vec(),
        };
        let bytes = serde_json::to_vec(&file).map_err(std::io::Error::other)?;
        super::write_private(&self.path, &bytes)
    }
}

fn read_records(path: &std::path::Path) -> Vec<Record> {
    let Ok(bytes) = std::fs::read(path) else {
        return Vec::new();
    };
    match serde_json::from_slice::<SessionsFile>(&bytes) {
        Ok(file) if file.version == SESSIONS_VERSION => file.sessions,
        _ => Vec::new(),
    }
}

/// `Set-Cookie` for a new session.
pub fn session_cookie(token: &str, days: u32, secure: bool) -> String {
    cookie(token, u64::from(days) * 86_400, secure)
}

/// `Set-Cookie` that ends the session.
pub fn clear_cookie(secure: bool) -> String {
    cookie("", 0, secure)
}

fn cookie(value: &str, max_age: u64, secure: bool) -> String {
    let mut out = format!("{COOKIE}={value}; Path=/; HttpOnly; SameSite=Strict; Max-Age={max_age}");
    if secure {
        out.push_str("; Secure");
    }
    out
}

/// The session token in a `Cookie` header, if any.
pub fn cookie_token(cookie_header: &str) -> Option<&str> {
    cookie_header
        .split(';')
        .map(str::trim)
        .find_map(|pair| pair.strip_prefix(COOKIE)?.strip_prefix('='))
        .filter(|token| !token.is_empty())
}

/// Failed logins per client address in a sliding window.
#[derive(Default)]
pub struct RateLimiter {
    failures: Mutex<HashMap<String, VecDeque<i64>>>,
}

impl RateLimiter {
    /// How long a failed login counts against its address.
    pub const WINDOW_MS: i64 = 15 * 60 * 1000;
    /// Failures inside the window after which logins are refused.
    pub const MAX_FAILURES: usize = 10;

    /// Err(seconds to wait) when `client` has MAX_FAILURES failures in the window.
    pub fn check(&self, client: &str, now_ms: i64) -> Result<(), u64> {
        let mut failures = self.lock();
        let Some(recent) = failures.get_mut(client) else {
            return Ok(());
        };
        prune(recent, now_ms);
        if recent.len() < Self::MAX_FAILURES {
            return Ok(());
        }
        let oldest = recent.front().copied().unwrap_or(now_ms);
        let wait_ms = (oldest + Self::WINDOW_MS - now_ms).max(0) as u64;
        Err(wait_ms.div_ceil(1000))
    }

    /// Records a failed login from `client`.
    pub fn fail(&self, client: &str, now_ms: i64) {
        let mut failures = self.lock();
        let recent = failures.entry(client.to_string()).or_default();
        prune(recent, now_ms);
        recent.push_back(now_ms);
    }

    /// Clears the failures of `client` after a good login.
    pub fn succeed(&self, client: &str) {
        self.lock().remove(client);
    }

    fn lock(&self) -> MutexGuard<'_, HashMap<String, VecDeque<i64>>> {
        self.failures.lock().unwrap_or_else(PoisonError::into_inner)
    }
}

fn prune(recent: &mut VecDeque<i64>, now_ms: i64) {
    while recent
        .front()
        .is_some_and(|&failed| now_ms - failed >= RateLimiter::WINDOW_MS)
    {
        recent.pop_front();
    }
}

/// The Origin rule from the design README "Guards".
pub fn origin_allowed(
    origin: Option<&str>,
    host: Option<&str>,
    public_origin: Option<&str>,
) -> bool {
    let Some(origin) = origin else {
        return false;
    };
    match public_origin {
        Some(expected) => origin == expected,
        None => {
            let origin_host = origin.split_once("://").map(|(_, rest)| rest);
            matches!((origin_host, host), (Some(a), Some(b)) if a.eq_ignore_ascii_case(b))
        }
    }
}

/// The rate-limit key: the first X-Forwarded-For entry when trusted and present, else the peer IP.
pub fn client_key(peer: SocketAddr, forwarded_for: Option<&str>, trust_proxy: bool) -> String {
    if trust_proxy {
        let first = forwarded_for
            .and_then(|value| value.split(',').next())
            .map(str::trim)
            .filter(|entry| !entry.is_empty());
        if let Some(entry) = first {
            return entry.to_string();
        }
    }
    peer.ip().to_string()
}

fn sha256(text: &str) -> Vec<u8> {
    Sha256::digest(text.as_bytes()).to_vec()
}

fn digest_eq(given: &str, expected: &str) -> bool {
    sha256(given).ct_eq(&sha256(expected)).into()
}

fn verify_hash(password: &str, hash: &str) -> bool {
    PasswordHash::new(hash).is_ok_and(|parsed| {
        Argon2::default()
            .verify_password(password.as_bytes(), &parsed)
            .is_ok()
    })
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::Ipv4Addr;

    fn plain(user: &str, password: &str) -> Credentials {
        Credentials::new(user.into(), Password::Plain(password.into()))
    }

    #[test]
    fn plain_credentials_accept_only_the_right_pair() {
        let creds = plain("eric", "secret");
        assert!(creds.verify("eric", "secret"));
        assert!(!creds.verify("erik", "secret"));
        assert!(!creds.verify("eric", "wrong"));
        assert!(!creds.verify("", ""));
    }

    #[test]
    fn hashed_credentials_accept_only_the_right_pair() {
        let hash = hash_password("secret").unwrap();
        let creds = Credentials::new("eric".into(), Password::Hash(hash));
        assert!(creds.verify("eric", "secret"));
        assert!(!creds.verify("eric", "wrong"));
        assert!(!creds.verify("erik", "secret"));
    }

    #[test]
    fn hash_password_output_verifies() {
        let hash = hash_password("correct horse").unwrap();
        assert!(hash.starts_with("$argon2id$"));
        assert!(verify_hash("correct horse", &hash));
        assert!(!verify_hash("battery", &hash));
    }

    #[test]
    fn an_unparseable_hash_rejects_every_password() {
        let creds = Credentials::new("eric".into(), Password::Hash("nope".into()));
        assert!(!creds.verify("eric", "nope"));
    }

    #[test]
    fn fingerprint_changes_with_the_password() {
        let first = plain("eric", "secret").fingerprint();
        let second = plain("eric", "other").fingerprint();
        assert_eq!(first.len(), 64);
        assert_ne!(first, second);
        assert_eq!(first, plain("eric", "secret").fingerprint());
    }

    #[test]
    fn new_tokens_are_unique_base64url() {
        let a = new_token();
        let b = new_token();
        assert_ne!(a, b);
        assert_eq!(a.len(), 43);
        assert!(a
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_'));
    }

    #[test]
    fn token_hash_is_sha256_hex_and_not_the_token() {
        let hash = token_hash("abc");
        assert_eq!(
            hash,
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        );
    }

    fn sessions_path(dir: &tempfile::TempDir) -> PathBuf {
        dir.path().join("sessions.json")
    }

    #[test]
    fn sessions_survive_a_reload_of_the_same_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = sessions_path(&dir);
        let first = Sessions::load(path.clone(), 30, "fp".into(), 0);
        let token = first.create(1_000).unwrap();
        let second = Sessions::load(path, 30, "fp".into(), 2_000);
        assert!(second.is_valid(&token, 2_000));
    }

    #[test]
    fn sessions_expire_after_their_days() {
        let dir = tempfile::tempdir().unwrap();
        let sessions = Sessions::load(sessions_path(&dir), 1, "fp".into(), 0);
        let token = sessions.create(0).unwrap();
        assert!(sessions.is_valid(&token, DAY_MS - 1));
        assert!(!sessions.is_valid(&token, DAY_MS));
    }

    #[test]
    fn sessions_are_dropped_when_the_fingerprint_differs() {
        let dir = tempfile::tempdir().unwrap();
        let path = sessions_path(&dir);
        let token = Sessions::load(path.clone(), 30, "old".into(), 0)
            .create(0)
            .unwrap();
        let reloaded = Sessions::load(path, 30, "new".into(), 0);
        assert!(!reloaded.is_valid(&token, 1));
    }

    #[test]
    fn remove_ends_only_that_session() {
        let dir = tempfile::tempdir().unwrap();
        let sessions = Sessions::load(sessions_path(&dir), 30, "fp".into(), 0);
        let ended = sessions.create(0).unwrap();
        let kept = sessions.create(0).unwrap();
        sessions.remove(&ended).unwrap();
        assert!(!sessions.is_valid(&ended, 1));
        assert!(sessions.is_valid(&kept, 1));
    }

    #[test]
    fn sessions_file_is_private_and_holds_no_token() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let path = sessions_path(&dir);
        let sessions = Sessions::load(path.clone(), 30, "fp".into(), 0);
        let token = sessions.create(0).unwrap();
        let mode = std::fs::metadata(&path).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
        let contents = std::fs::read_to_string(&path).unwrap();
        assert!(!contents.contains(&token));
        assert!(contents.contains(&token_hash(&token)));
    }

    #[test]
    fn a_bad_sessions_file_gives_no_sessions() {
        let dir = tempfile::tempdir().unwrap();
        let path = sessions_path(&dir);
        std::fs::write(&path, "{not json").unwrap();
        let sessions = Sessions::load(path, 30, "fp".into(), 0);
        assert!(!sessions.is_valid("anything", 0));
    }

    #[test]
    fn session_cookies_match_the_design() {
        assert_eq!(
            session_cookie("tok", 30, false),
            "ws_online_session=tok; Path=/; HttpOnly; SameSite=Strict; Max-Age=2592000"
        );
        assert_eq!(
            session_cookie("tok", 1, true),
            "ws_online_session=tok; Path=/; HttpOnly; SameSite=Strict; Max-Age=86400; Secure"
        );
        assert_eq!(
            clear_cookie(false),
            "ws_online_session=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0"
        );
        assert_eq!(
            clear_cookie(true),
            "ws_online_session=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0; Secure"
        );
    }

    #[test]
    fn cookie_token_finds_the_session_among_others() {
        assert_eq!(cookie_token("a=1; ws_online_session=tok; b=2"), Some("tok"));
        assert_eq!(cookie_token("ws_online_session=tok"), Some("tok"));
    }

    #[test]
    fn cookie_token_ignores_similar_names_and_empty_values() {
        assert_eq!(cookie_token("ws_online_session_x=tok"), None);
        assert_eq!(cookie_token("ws_online_session="), None);
        assert_eq!(cookie_token("a=1"), None);
    }

    fn ip() -> String {
        "203.0.113.5".to_string()
    }

    #[test]
    fn the_eleventh_failure_inside_the_window_is_limited() {
        let limiter = RateLimiter::default();
        for _ in 0..RateLimiter::MAX_FAILURES {
            assert!(limiter.check(&ip(), 0).is_ok());
            limiter.fail(&ip(), 0);
        }
        assert_eq!(limiter.check(&ip(), 0), Err(900));
        assert_eq!(limiter.check(&ip(), 1_000), Err(899));
    }

    #[test]
    fn an_old_failure_leaves_the_window() {
        let limiter = RateLimiter::default();
        limiter.fail(&ip(), 0);
        for _ in 1..RateLimiter::MAX_FAILURES {
            limiter.fail(&ip(), 60_000);
        }
        assert!(limiter.check(&ip(), 60_000).is_err());
        assert!(limiter.check(&ip(), RateLimiter::WINDOW_MS).is_ok());
    }

    #[test]
    fn a_success_clears_the_address() {
        let limiter = RateLimiter::default();
        for _ in 0..RateLimiter::MAX_FAILURES {
            limiter.fail(&ip(), 0);
        }
        limiter.succeed(&ip());
        assert!(limiter.check(&ip(), 0).is_ok());
    }

    #[test]
    fn other_addresses_are_not_limited() {
        let limiter = RateLimiter::default();
        for _ in 0..RateLimiter::MAX_FAILURES {
            limiter.fail(&ip(), 0);
        }
        assert!(limiter.check("198.51.100.1", 0).is_ok());
    }

    #[test]
    fn origin_is_required() {
        assert!(!origin_allowed(None, Some("ws.local:8080"), None));
    }

    #[test]
    fn origin_matching_host_is_allowed() {
        assert!(origin_allowed(
            Some("http://ws.local:8080"),
            Some("ws.local:8080"),
            None
        ));
    }

    #[test]
    fn origin_with_another_port_is_refused() {
        assert!(!origin_allowed(
            Some("http://ws.local:9090"),
            Some("ws.local:8080"),
            None
        ));
    }

    #[test]
    fn public_origin_must_match_when_set() {
        let public = Some("https://ws.example.com");
        assert!(origin_allowed(
            Some("https://ws.example.com"),
            Some("proxy.internal"),
            public
        ));
        assert!(!origin_allowed(
            Some("https://ws.local"),
            Some("ws.local"),
            public
        ));
    }

    #[test]
    fn client_key_is_the_peer_without_trust() {
        let peer = SocketAddr::new(Ipv4Addr::new(10, 0, 0, 9).into(), 5000);
        assert_eq!(client_key(peer, Some("198.51.100.7"), false), "10.0.0.9");
    }

    #[test]
    fn client_key_is_the_first_forwarded_entry_with_trust() {
        let peer = SocketAddr::new(Ipv4Addr::new(10, 0, 0, 9).into(), 5000);
        assert_eq!(
            client_key(peer, Some("198.51.100.7, 10.0.0.1"), true),
            "198.51.100.7"
        );
        assert_eq!(client_key(peer, None, true), "10.0.0.9");
    }
}
