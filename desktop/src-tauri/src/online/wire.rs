//! Messages shared by the online server's modules (ADR 0053).

use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// A frame the server sends on `/api/ws`.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(tag = "type", rename_all = "camelCase")]
pub enum ServerMsg {
    /// The first frame: the id to send as `X-WS-Connection`.
    Hello { connection: String },
    /// A message on a channel the browser passed as `{"__channel": id}`.
    Channel { id: u64, payload: Value },
    /// A server event, such as `notify`.
    Event { event: String, payload: Value },
    /// The answer to an invoke frame.
    Result {
        id: u64,
        ok: bool,
        #[serde(skip_serializing_if = "Option::is_none")]
        value: Option<Value>,
        #[serde(skip_serializing_if = "Option::is_none")]
        error: Option<AppError>,
    },
}

/// A frame the browser sends on `/api/ws`.
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(tag = "type", rename_all = "camelCase")]
pub enum ClientMsg {
    /// Runs a command, as `POST /api/invoke/<command>` does.
    Invoke {
        id: u64,
        command: String,
        #[serde(default)]
        args: Value,
    },
    /// Keeps the socket alive; no answer.
    Ping,
}

/// One notification, sent by Web Push and as the `notify` event.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct PushPayload {
    pub title: String,
    pub body: String,
    /// `needsInput:<key>`, `done:<key>`, or `agents` for a summary.
    pub tag: String,
    /// The agent's entry key, or `None` for a summary.
    pub key: Option<String>,
    /// Where a click opens: `/?agent=<key>` or `/`.
    pub url: String,
}
