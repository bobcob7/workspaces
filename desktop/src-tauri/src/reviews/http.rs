//! HTTPS GET for forge APIs, behind a trait tests replace (ADR 0060).

use crate::runner::AppError;
use std::future::Future;
use std::pin::Pin;
#[cfg(test)]
use std::sync::Mutex;
use std::time::Duration;

const TIMEOUT: Duration = Duration::from_secs(20);
const MAX_REDIRECTS: usize = 3;

/// A boxed future that borrows from `'a`, as a trait method can return.
pub type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// The status and body of an HTTP answer, whatever its status.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HttpResponse {
    pub status: u16,
    pub body: Vec<u8>,
}

/// GETs a URL, so tests can answer without a network.
pub trait Http: Send + Sync {
    /// GET `url` with `Accept: application/json` and, given a token,
    /// `Authorization: token <token>`. A network or TLS failure is
    /// `AppError { code: "forge_failed", message: "<host>: <error>" }`.
    fn get<'a>(
        &'a self,
        url: &'a str,
        token: Option<&'a str>,
    ) -> BoxFuture<'a, Result<HttpResponse, AppError>>;
}

/// The production client: reqwest with rustls, a 20 s timeout per request,
/// and redirects followed only up to three times and only to the first
/// request's host.
pub struct ReqwestHttp {
    client: reqwest::Client,
}

impl ReqwestHttp {
    /// A client with the timeout and redirect policy above.
    pub fn new() -> Self {
        let client = reqwest::Client::builder()
            .timeout(TIMEOUT)
            .redirect(reqwest::redirect::Policy::custom(|attempt| {
                let same_host = attempt.previous().first().and_then(|u| u.host_str())
                    == attempt.url().host_str();
                if attempt.previous().len() > MAX_REDIRECTS || !same_host {
                    attempt.stop()
                } else {
                    attempt.follow()
                }
            }))
            .build()
            .unwrap_or_else(|_| reqwest::Client::new());
        Self { client }
    }
}

impl Default for ReqwestHttp {
    fn default() -> Self {
        Self::new()
    }
}

impl Http for ReqwestHttp {
    fn get<'a>(
        &'a self,
        url: &'a str,
        token: Option<&'a str>,
    ) -> BoxFuture<'a, Result<HttpResponse, AppError>> {
        Box::pin(async move {
            let mut request = self
                .client
                .get(url)
                .header(reqwest::header::ACCEPT, "application/json");
            if let Some(token) = token {
                request = request.header(reqwest::header::AUTHORIZATION, format!("token {token}"));
            }
            let response = request.send().await.map_err(|e| transport_error(url, e))?;
            let status = response.status().as_u16();
            let body = response
                .bytes()
                .await
                .map_err(|e| transport_error(url, e))?;
            Ok(HttpResponse {
                status,
                body: body.to_vec(),
            })
        })
    }
}

/// The error for a request that got no answer. The URL is left out so its
/// query never shows.
fn transport_error(url: &str, error: reqwest::Error) -> AppError {
    AppError::new(
        "forge_failed",
        format!("{}: {}", host_and_port(url), error.without_url()),
    )
}

/// The URL's host, with its port when it has one.
fn host_and_port(url: &str) -> String {
    let Ok(parsed) = reqwest::Url::parse(url) else {
        return String::new();
    };
    let host = parsed.host_str().unwrap_or_default();
    match parsed.port() {
        Some(port) => format!("{host}:{port}"),
        None => host.to_string(),
    }
}

/// Tests: canned answers by exact URL. An unknown URL answers 404 `{}`.
/// A canned answer for a URL; `Err` is a dead host.
#[cfg(test)]
type Route = (String, Result<(u16, String), ()>);

#[cfg(test)]
#[derive(Default)]
pub struct FakeHttp {
    routes: Mutex<Vec<Route>>,
    calls: Mutex<Vec<(String, Option<String>)>>,
}

#[cfg(test)]
impl FakeHttp {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn route(self, url: &str, status: u16, body: &str) -> Self {
        self.routes
            .lock()
            .unwrap()
            .push((url.to_string(), Ok((status, body.to_string()))));
        self
    }

    /// `url` fails as a dead host would: Err(forge_failed "<host>: connection refused").
    pub fn fail(self, url: &str) -> Self {
        self.routes.lock().unwrap().push((url.to_string(), Err(())));
        self
    }

    /// Every (url, token) requested, in order.
    pub fn calls(&self) -> Vec<(String, Option<String>)> {
        self.calls.lock().unwrap().clone()
    }
}

#[cfg(test)]
impl Http for FakeHttp {
    fn get<'a>(
        &'a self,
        url: &'a str,
        token: Option<&'a str>,
    ) -> BoxFuture<'a, Result<HttpResponse, AppError>> {
        self.calls
            .lock()
            .unwrap()
            .push((url.to_string(), token.map(str::to_string)));
        let route = self
            .routes
            .lock()
            .unwrap()
            .iter()
            .find(|(route_url, _)| route_url == url)
            .map(|(_, answer)| answer.clone());
        let answer = match route {
            None => Ok(HttpResponse {
                status: 404,
                body: b"{}".to_vec(),
            }),
            Some(Ok((status, body))) => Ok(HttpResponse {
                status,
                body: body.into_bytes(),
            }),
            Some(Err(())) => Err(AppError::new(
                "forge_failed",
                format!("{}: connection refused", host_and_port(url)),
            )),
        };
        Box::pin(std::future::ready(answer))
    }
}
