//! VAPID keys, push subscriptions, payloads and sending (ADR 0053).

use crate::online::wire::PushPayload;
use crate::online::write_private;
use crate::runner::AppError;
use crate::tray::attention::{Attention, Event};
use crate::tray::view::{notices, Notice, SUMMARY_OVER};
use base64ct::{Base64UrlUnpadded, Encoding};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::Duration;
use web_push_native::jwt_simple::algorithms::{ECDSAP256PublicKeyLike, ES256KeyPair};
use web_push_native::WebPushBuilder;

const FILE_VERSION: u32 = 1;
const SEND_TIMEOUT: Duration = Duration::from_secs(10);

/// The server's VAPID key pair, kept in `vapid.json`.
pub struct VapidKeys {
    key: ES256KeyPair,
    public: String,
}

#[derive(Serialize, Deserialize)]
struct VapidFile {
    version: u32,
    private: String,
    public: String,
}

impl VapidKeys {
    /// Reads the file, or generates a pair and writes it (0600).
    pub fn load_or_create(path: &Path) -> std::io::Result<Self> {
        match std::fs::read(path) {
            Ok(bytes) => Self::from_file_bytes(&bytes),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let keys = Self::generate();
                let file = VapidFile {
                    version: FILE_VERSION,
                    private: Base64UrlUnpadded::encode_string(&keys.key.to_bytes()),
                    public: keys.public.clone(),
                };
                let bytes = serde_json::to_vec_pretty(&file).map_err(invalid_data)?;
                write_private(path, &bytes)?;
                Ok(keys)
            }
            Err(e) => Err(e),
        }
    }

    /// base64url of the 65-byte uncompressed point, for applicationServerKey.
    pub fn public_key(&self) -> &str {
        &self.public
    }

    fn generate() -> Self {
        let key = ES256KeyPair::generate();
        let public = encode_public(&key);
        Self { key, public }
    }

    fn from_file_bytes(bytes: &[u8]) -> std::io::Result<Self> {
        let file: VapidFile = serde_json::from_slice(bytes).map_err(invalid_data)?;
        let raw = Base64UrlUnpadded::decode_vec(&file.private).map_err(invalid_data)?;
        let key = ES256KeyPair::from_bytes(&raw).map_err(invalid_data)?;
        let public = encode_public(&key);
        Ok(Self { key, public })
    }
}

fn encode_public(key: &ES256KeyPair) -> String {
    let point = key.public_key().public_key().to_bytes_uncompressed();
    Base64UrlUnpadded::encode_string(&point)
}

fn invalid_data(e: impl std::fmt::Display) -> std::io::Error {
    std::io::Error::new(std::io::ErrorKind::InvalidData, e.to_string())
}

/// One browser's subscription, as stored.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct StoredSubscription {
    pub id: String,
    pub label: String,
    pub created_ms: i64,
    pub subscription: Value,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct SubscriptionFile {
    version: u32,
    subscriptions: Vec<StoredSubscription>,
}

/// The subscriptions, persisted to `subscriptions.json`.
pub struct Subscriptions {
    path: PathBuf,
    entries: Mutex<Vec<StoredSubscription>>,
}

impl Subscriptions {
    /// Missing or unreadable file means no subscriptions.
    pub fn load(path: PathBuf) -> Self {
        let entries = std::fs::read(&path)
            .ok()
            .and_then(|bytes| serde_json::from_slice::<SubscriptionFile>(&bytes).ok())
            .map(|file| file.subscriptions)
            .unwrap_or_default();
        Self {
            path,
            entries: Mutex::new(entries),
        }
    }

    /// Checks the shape, then deserializes a WebPushBuilder; replaces one with the same endpoint.
    pub fn add(&self, subscription: Value, label: &str, now_ms: i64) -> Result<String, AppError> {
        let subscription = unpadded_keys(subscription);
        let endpoint = check_shape(&subscription)?.to_string();
        serde_json::from_value::<WebPushBuilder>(subscription.clone())
            .map_err(|_| bad_subscription())?;
        let id = subscription_id(&endpoint);
        let entry = StoredSubscription {
            id: id.clone(),
            label: label.to_string(),
            created_ms: now_ms,
            subscription,
        };
        let mut entries = self.lock();
        let mut next = entries.clone();
        match next.iter().position(|existing| existing.id == id) {
            Some(index) => next[index] = entry,
            None => next.push(entry),
        }
        self.save(&next).map_err(save_failed)?;
        *entries = next;
        Ok(id)
    }

    /// Removes the subscription with `endpoint`; false if there was none.
    pub fn remove(&self, endpoint: &str) -> std::io::Result<bool> {
        let id = subscription_id(endpoint);
        let mut entries = self.lock();
        let mut next = entries.clone();
        let before = next.len();
        next.retain(|existing| existing.id != id);
        if next.len() == before {
            return Ok(false);
        }
        self.save(&next)?;
        *entries = next;
        Ok(true)
    }

    /// A copy of every stored subscription.
    pub fn list(&self) -> Vec<StoredSubscription> {
        self.lock().clone()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, Vec<StoredSubscription>> {
        self.entries.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn save(&self, entries: &[StoredSubscription]) -> std::io::Result<()> {
        let file = SubscriptionFile {
            version: FILE_VERSION,
            subscriptions: entries.to_vec(),
        };
        let bytes = serde_json::to_vec_pretty(&file).map_err(invalid_data)?;
        write_private(&self.path, &bytes)
    }
}

fn bad_subscription() -> AppError {
    AppError::new("bad_subscription", "the push subscription is malformed")
}

/// Checks what WebPushBuilder would panic on: an https endpoint, a 65-byte p256dh point and a 16-byte auth secret.
fn check_shape(subscription: &Value) -> Result<&str, AppError> {
    let endpoint = subscription
        .get("endpoint")
        .and_then(Value::as_str)
        .filter(|endpoint| endpoint.starts_with("https://"))
        .ok_or_else(bad_subscription)?;
    let keys = subscription.get("keys").ok_or_else(bad_subscription)?;
    let p256dh = decode_key(keys, "p256dh")?;
    if p256dh.len() != 65 || p256dh[0] != 0x04 {
        return Err(bad_subscription());
    }
    if decode_key(keys, "auth")?.len() != 16 {
        return Err(bad_subscription());
    }
    Ok(endpoint)
}

/// Strips the `=` padding from the keys, which WebPushBuilder only reads unpadded.
fn unpadded_keys(mut subscription: Value) -> Value {
    let Some(keys) = subscription.get_mut("keys") else {
        return subscription;
    };
    for name in ["p256dh", "auth"] {
        if let Some(text) = keys.get(name).and_then(Value::as_str) {
            let trimmed = text.trim_end_matches('=').to_string();
            keys[name] = Value::from(trimmed);
        }
    }
    subscription
}

/// Decodes a base64url key, padded or not.
fn decode_key(keys: &Value, name: &str) -> Result<Vec<u8>, AppError> {
    let text = keys
        .get(name)
        .and_then(Value::as_str)
        .ok_or_else(bad_subscription)?;
    Base64UrlUnpadded::decode_vec(text.trim_end_matches('=')).map_err(|_| bad_subscription())
}

fn save_failed(e: std::io::Error) -> AppError {
    AppError::new("save_failed", format!("couldn't save subscriptions: {e}"))
}

/// First 16 hex chars of sha256(endpoint).
pub fn subscription_id(endpoint: &str) -> String {
    Sha256::digest(endpoint.as_bytes())
        .iter()
        .take(8)
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

/// The push payloads for one listing's wanted events (design README "Payload").
pub fn payloads(events: &[Event]) -> Vec<PushPayload> {
    let shown: Vec<&Event> = events
        .iter()
        .filter(|event| event.attention != Attention::Busy)
        .collect();
    let texts = notices(events);
    if shown.len() > SUMMARY_OVER {
        return texts.into_iter().map(summary_payload).collect();
    }
    shown
        .into_iter()
        .zip(texts)
        .map(|(event, notice)| event_payload(event, notice))
        .collect()
}

fn event_payload(event: &Event, notice: Notice) -> PushPayload {
    let kind = match event.attention {
        Attention::Done => "done",
        Attention::Blocked | Attention::RunNeedsAttention => "loop",
        _ => "needsInput",
    };
    PushPayload {
        title: notice.title,
        body: notice.body,
        tag: format!("{kind}:{}", event.key),
        key: Some(event.key.clone()),
        url: format!("/?agent={}", percent_encode(&event.key)),
    }
}

fn summary_payload(notice: Notice) -> PushPayload {
    PushPayload {
        title: notice.title,
        body: notice.body,
        tag: "agents".to_string(),
        key: None,
        url: "/".to_string(),
    }
}

fn percent_encode(text: &str) -> String {
    text.bytes()
        .map(|byte| match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                (byte as char).to_string()
            }
            _ => format!("%{byte:02X}"),
        })
        .collect()
}

/// What a push service said.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Delivery {
    /// The push service accepted the message.
    Sent,
    /// The subscription no longer exists and should be dropped.
    Gone,
    /// The message wasn't delivered; the string says why.
    Failed(String),
}

/// 200, 201, 202 → Sent; 404, 410 → Gone; else Failed("status <n>").
pub fn classify(status: u16) -> Delivery {
    match status {
        200..=202 => Delivery::Sent,
        404 | 410 => Delivery::Gone,
        other => Delivery::Failed(format!("status {other}")),
    }
}

/// Sends payloads to every subscription.
pub struct Pusher {
    keys: VapidKeys,
    subject: String,
    subscriptions: Subscriptions,
    client: reqwest::Client,
}

impl Pusher {
    /// Builds the HTTP client with the send timeout.
    pub fn new(keys: VapidKeys, subject: String, subscriptions: Subscriptions) -> Self {
        let client = reqwest::Client::builder()
            .timeout(SEND_TIMEOUT)
            .build()
            .unwrap_or_default();
        Self {
            keys,
            subject,
            subscriptions,
            client,
        }
    }

    /// The public key the browser needs as applicationServerKey.
    pub fn public_key(&self) -> &str {
        self.keys.public_key()
    }

    /// The stored subscriptions this pusher sends to.
    pub fn subscriptions(&self) -> &Subscriptions {
        &self.subscriptions
    }

    /// The encrypted, VAPID-signed request, with `Urgency: high`.
    pub fn request(
        &self,
        subscription: &Value,
        payload: &PushPayload,
    ) -> Result<reqwest::Request, String> {
        let builder: WebPushBuilder =
            serde_json::from_value(subscription.clone()).map_err(|e| e.to_string())?;
        let body = serde_json::to_vec(payload).map_err(|e| e.to_string())?;
        let signed = builder
            .with_vapid(&self.keys.key, &self.subject)
            .build(body)
            .map_err(|e| e.to_string())?;
        let mut request = reqwest::Request::try_from(signed).map_err(|e| e.to_string())?;
        request
            .headers_mut()
            .insert("Urgency", reqwest::header::HeaderValue::from_static("high"));
        Ok(request)
    }

    /// (sent, failed); Gone subscriptions are removed and not counted as failed.
    pub async fn send_all(&self, payload: &PushPayload) -> (usize, usize) {
        let mut sent = 0;
        let mut failed = 0;
        for stored in self.subscriptions.list() {
            match self.deliver(&stored.subscription, payload).await {
                Delivery::Sent => sent += 1,
                Delivery::Gone => self.forget(&stored.subscription),
                Delivery::Failed(reason) => {
                    eprintln!("push: {reason}");
                    failed += 1;
                }
            }
        }
        (sent, failed)
    }

    async fn deliver(&self, subscription: &Value, payload: &PushPayload) -> Delivery {
        let request = match self.request(subscription, payload) {
            Ok(request) => request,
            Err(reason) => return Delivery::Failed(reason),
        };
        match self.client.execute(request).await {
            Ok(response) => classify(response.status().as_u16()),
            Err(e) => Delivery::Failed(e.to_string()),
        }
    }

    fn forget(&self, subscription: &Value) {
        let Some(endpoint) = subscription.get("endpoint").and_then(Value::as_str) else {
            return;
        };
        if let Err(e) = self.subscriptions.remove(endpoint) {
            eprintln!("push: couldn't remove a subscription: {e}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    const SUBSCRIPTION: &str = r#"{"endpoint":"https://push.example.com/abc","expirationTime":null,"keys":{"p256dh":"BLn9b-VR0ca83knDNZ32dCHGyjJp-1riX9ZTN40MqV8K_LpQmLqxC_DoHvqvFXO_nGdAB4W9dogZb_sM-uV4JbY","auth":"_ordMnz7uTCmrpBTeUV4Bw"}}"#;

    fn sample_subscription() -> Value {
        serde_json::from_str(SUBSCRIPTION).unwrap()
    }

    fn event(key: &str, attention: Attention) -> Event {
        Event {
            key: key.to_string(),
            name: key.to_string(),
            snippet: String::new(),
            attention,
        }
    }

    fn sample_payload() -> PushPayload {
        PushPayload {
            title: "fix-login needs input".to_string(),
            body: "Which branch?".to_string(),
            tag: "needsInput:fix-login".to_string(),
            key: Some("fix-login".to_string()),
            url: "/?agent=fix-login".to_string(),
        }
    }

    #[test]
    fn load_or_create_writes_private_file_and_keeps_the_key() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("vapid.json");
        let first = VapidKeys::load_or_create(&path).unwrap();
        let mode = std::fs::metadata(&path).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
        let second = VapidKeys::load_or_create(&path).unwrap();
        assert_eq!(first.public_key(), second.public_key());
        let point = Base64UrlUnpadded::decode_vec(first.public_key()).unwrap();
        assert_eq!(point.len(), 65);
        assert_eq!(point[0], 0x04);
    }

    #[test]
    fn load_or_create_rejects_a_damaged_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("vapid.json");
        std::fs::write(&path, b"not json").unwrap();
        assert!(VapidKeys::load_or_create(&path).is_err());
    }

    #[test]
    fn add_stores_and_persists_the_subscription() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("subscriptions.json");
        let subscriptions = Subscriptions::load(path.clone());
        let id = subscriptions
            .add(sample_subscription(), "laptop", 1_000)
            .unwrap();
        assert_eq!(id, subscription_id("https://push.example.com/abc"));
        let reloaded = Subscriptions::load(path);
        assert_eq!(reloaded.list(), subscriptions.list());
        assert_eq!(reloaded.list()[0].label, "laptop");
        assert_eq!(reloaded.list()[0].created_ms, 1_000);
    }

    #[test]
    fn add_with_the_same_endpoint_replaces_the_entry() {
        let dir = tempfile::tempdir().unwrap();
        let subscriptions = Subscriptions::load(dir.path().join("subscriptions.json"));
        subscriptions
            .add(sample_subscription(), "old", 1_000)
            .unwrap();
        subscriptions
            .add(sample_subscription(), "new", 2_000)
            .unwrap();
        let list = subscriptions.list();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].label, "new");
    }

    #[test]
    fn add_rejects_a_subscription_without_auth() {
        let dir = tempfile::tempdir().unwrap();
        let subscriptions = Subscriptions::load(dir.path().join("subscriptions.json"));
        let mut subscription = sample_subscription();
        subscription["keys"].as_object_mut().unwrap().remove("auth");
        let error = subscriptions
            .add(subscription, "laptop", 1_000)
            .unwrap_err();
        assert_eq!(error.code, "bad_subscription");
        assert!(subscriptions.list().is_empty());
    }

    fn add_with_edited_keys(edit: impl FnOnce(&mut Value)) -> Result<String, AppError> {
        let dir = tempfile::tempdir().unwrap();
        let subscriptions = Subscriptions::load(dir.path().join("subscriptions.json"));
        let mut subscription = sample_subscription();
        edit(&mut subscription);
        subscriptions.add(subscription, "laptop", 1_000)
    }

    #[test]
    fn add_rejects_a_short_p256dh_key() {
        let error = add_with_edited_keys(|subscription| {
            let short = subscription["keys"]["p256dh"].as_str().unwrap()[..40].to_string();
            subscription["keys"]["p256dh"] = Value::from(short);
        })
        .unwrap_err();
        assert_eq!(error.code, "bad_subscription");
    }

    #[test]
    fn add_rejects_a_short_auth_secret() {
        let error = add_with_edited_keys(|subscription| {
            subscription["keys"]["auth"] = Value::from("_ordMnz7uTCmrpBTeUV4");
        })
        .unwrap_err();
        assert_eq!(error.code, "bad_subscription");
    }

    #[test]
    fn add_rejects_keys_that_are_not_base64url() {
        let error = add_with_edited_keys(|subscription| {
            subscription["keys"]["auth"] = Value::from("_ordMnz7uTCmrpBTeUV4B+w");
        })
        .unwrap_err();
        assert_eq!(error.code, "bad_subscription");
    }

    #[test]
    fn add_rejects_an_http_endpoint() {
        let error = add_with_edited_keys(|subscription| {
            subscription["endpoint"] = Value::from("http://push.example.com/abc");
        })
        .unwrap_err();
        assert_eq!(error.code, "bad_subscription");
    }

    #[test]
    fn add_accepts_padded_keys() {
        let result = add_with_edited_keys(|subscription| {
            subscription["keys"]["auth"] = Value::from("_ordMnz7uTCmrpBTeUV4Bw==");
        });
        assert!(result.is_ok());
    }

    #[test]
    fn remove_returns_true_then_false() {
        let dir = tempfile::tempdir().unwrap();
        let subscriptions = Subscriptions::load(dir.path().join("subscriptions.json"));
        subscriptions
            .add(sample_subscription(), "laptop", 1_000)
            .unwrap();
        let endpoint = "https://push.example.com/abc";
        assert!(subscriptions.remove(endpoint).unwrap());
        assert!(!subscriptions.remove(endpoint).unwrap());
        assert!(subscriptions.list().is_empty());
    }

    #[test]
    fn load_of_a_missing_or_bad_file_is_empty() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("subscriptions.json");
        assert!(Subscriptions::load(path.clone()).list().is_empty());
        std::fs::write(&path, b"{").unwrap();
        assert!(Subscriptions::load(path).list().is_empty());
    }

    #[test]
    fn subscription_id_is_16_hex_chars() {
        let id = subscription_id("https://push.example.com/abc");
        assert_eq!(id.len(), 16);
        assert!(id.chars().all(|c| c.is_ascii_hexdigit()));
    }

    #[test]
    fn payloads_for_one_needs_input_event_name_the_agent() {
        let mut needs = event("fix-login", Attention::NeedsInput);
        needs.snippet = "Which branch?".to_string();
        let found = payloads(&[needs]);
        assert_eq!(
            found,
            [PushPayload {
                title: "fix-login needs input".to_string(),
                body: "Which branch?".to_string(),
                tag: "needsInput:fix-login".to_string(),
                key: Some("fix-login".to_string()),
                url: "/?agent=fix-login".to_string(),
            }]
        );
    }

    #[test]
    fn payloads_for_a_done_event_use_the_done_tag() {
        let found = payloads(&[event("fix-login", Attention::Done)]);
        assert_eq!(found[0].title, "fix-login is done");
        assert_eq!(found[0].tag, "done:fix-login");
    }

    #[test]
    fn payload_url_percent_encodes_the_key_but_not_the_tag() {
        let found = payloads(&[event("fix login/a", Attention::NeedsInput)]);
        assert_eq!(found[0].url, "/?agent=fix%20login%2Fa");
        assert_eq!(found[0].tag, "needsInput:fix login/a");
        assert_eq!(found[0].key.as_deref(), Some("fix login/a"));
    }

    #[test]
    fn payloads_for_a_blocked_loop_run_use_the_loop_kind() {
        let found = payloads(&[event("fix-login", Attention::Blocked)]);
        assert_eq!(found[0].tag, "loop:fix-login");
        assert_eq!(found[0].url, "/?agent=fix-login");
        assert_eq!(found[0].title, "fix-login is blocked");
    }

    #[test]
    fn four_events_give_one_summary_payload() {
        let events = [
            event("a", Attention::NeedsInput),
            event("b", Attention::Done),
            event("c", Attention::NeedsInput),
            event("d", Attention::Done),
        ];
        let found = payloads(&events);
        assert_eq!(found.len(), 1);
        assert_eq!(found[0].tag, "agents");
        assert_eq!(found[0].key, None);
        assert_eq!(found[0].url, "/");
    }

    #[test]
    fn busy_events_are_dropped() {
        assert!(payloads(&[event("a", Attention::Busy)]).is_empty());
    }

    #[test]
    fn request_targets_the_endpoint_with_web_push_headers() {
        let dir = tempfile::tempdir().unwrap();
        let keys = VapidKeys::load_or_create(&dir.path().join("vapid.json")).unwrap();
        let pusher = Pusher::new(
            keys,
            "mailto:ops@example.com".to_string(),
            Subscriptions::load(dir.path().join("subscriptions.json")),
        );
        let request = pusher
            .request(&sample_subscription(), &sample_payload())
            .unwrap();
        assert_eq!(request.url().as_str(), "https://push.example.com/abc");
        let headers = request.headers();
        assert_eq!(headers["content-encoding"], "aes128gcm");
        assert!(headers.contains_key("ttl"));
        assert_eq!(headers["urgency"], "high");
        assert!(headers["authorization"]
            .to_str()
            .unwrap()
            .starts_with("vapid t="));
    }

    #[test]
    fn classify_covers_the_push_service_statuses() {
        assert_eq!(classify(201), Delivery::Sent);
        assert_eq!(classify(404), Delivery::Gone);
        assert_eq!(classify(410), Delivery::Gone);
        assert_eq!(classify(500), Delivery::Failed("status 500".to_string()));
    }
}
