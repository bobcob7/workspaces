//! Keeps `ws daemon run` running under the desktop app or ws-online (ADR 0068).

use crate::runner::Runner;
use std::future::Future;
use std::io;
use std::process::{ExitStatus, Stdio};
use std::time::Duration;
use tokio::process::Command;
use tokio::time::Instant;

const MIN_BACKOFF: Duration = Duration::from_secs(1);
const MAX_BACKOFF: Duration = Duration::from_secs(60);
const BACKOFF_RESET_UPTIME: Duration = Duration::from_secs(600);
const ALREADY_RUNNING_WAIT: Duration = Duration::from_secs(60);
const EXIT_DAEMON_RUNNING: i32 = 4;

/// Who hosts the daemon: the desktop app (`--origin app`) or ws-online
/// (`--origin managed`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Host {
    #[cfg_attr(not(feature = "online"), allow(dead_code))]
    App,
    #[cfg_attr(not(feature = "online"), allow(dead_code))]
    Online,
}

/// How one `ws daemon run` ended: its exit code (`None` when it was killed
/// or never started) and how long it ran.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct Exit {
    code: Option<i32>,
    uptime: Duration,
}

/// Keeps `ws daemon run --origin <app|managed>` running for the life of
/// the process (ADR 0068). The child runs with `runner.path()` as PATH,
/// stdin and stdout on /dev/null, stderr to the app's log, and in its own
/// session, so quitting the app doesn't signal it. When it exits, it's
/// started again with `next_backoff`. Exit 4 means another daemon holds the
/// lock, which the app then uses over the socket: wait 60 s before trying again.
pub async fn keep_running(runner: Runner, host: Host) -> ! {
    supervise(move || {
        let runner = runner.clone();
        async move { run_once(&runner, host).await }
    })
    .await
}

async fn run_once(runner: &Runner, host: Host) -> Exit {
    let started = Instant::now();
    let code = match launch(runner, host).await {
        Ok(status) => status.code(),
        Err(err) => {
            eprintln!("daemon: starting ws daemon failed: {err}");
            None
        }
    };
    Exit {
        code,
        uptime: started.elapsed(),
    }
}

/// Runs `launch` again and again, waiting between runs as the exits say.
async fn supervise<F, Fut>(mut launch: F) -> !
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Exit>,
{
    let mut backoff = Duration::ZERO;
    let mut reported_running = false;
    loop {
        let exit = launch().await;
        if exit.code == Some(EXIT_DAEMON_RUNNING) {
            if !reported_running {
                eprintln!("daemon: another ws daemon holds the lock; using it");
                reported_running = true;
            }
            tokio::time::sleep(ALREADY_RUNNING_WAIT).await;
            continue;
        }
        reported_running = false;
        backoff = next_backoff(backoff, exit.uptime);
        eprintln!(
            "daemon: exited ({:?}); restarting in {backoff:?}",
            exit.code
        );
        tokio::time::sleep(backoff).await;
    }
}

/// The wait before the next start: 1 s first, doubling to a 60 s cap, and
/// back to 1 s after a run that lasted 10 min. `prev` is the last wait, zero
/// before the first.
fn next_backoff(prev: Duration, uptime: Duration) -> Duration {
    if uptime >= BACKOFF_RESET_UPTIME || prev.is_zero() {
        return MIN_BACKOFF;
    }
    (prev * 2).min(MAX_BACKOFF)
}

fn daemon_args(host: Host) -> Vec<String> {
    let origin = match host {
        Host::App => "app",
        Host::Online => "managed",
    };
    vec![
        "daemon".to_string(),
        "run".to_string(),
        "--origin".to_string(),
        origin.to_string(),
    ]
}

/// Starts one `ws daemon run` and waits for it to exit.
async fn launch(runner: &Runner, host: Host) -> io::Result<ExitStatus> {
    let mut command = Command::new("ws");
    command
        .args(daemon_args(host))
        .env("PATH", runner.path())
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::inherit());
    // SAFETY: `setsid` is async-signal-safe and touches no shared state; the
    // closure runs in the forked child before `exec`.
    unsafe {
        command.pre_exec(|| {
            if libc::setsid() == -1 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    child.wait().await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    use std::sync::{Arc, Mutex};

    /// A directory holding a fake `ws` script, for PATH.
    fn fake_ws(body: &str) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        dir
    }

    /// The PATH the fake script runs with: its own directory, then the system's.
    fn path_with(dir: &tempfile::TempDir) -> String {
        let system = std::env::var("PATH").unwrap_or_default();
        format!("{}:{system}", dir.path().display())
    }

    fn gaps(starts: &[Instant]) -> Vec<Duration> {
        starts.windows(2).map(|pair| pair[1] - pair[0]).collect()
    }

    #[test]
    fn next_backoff_doubles_to_the_cap() {
        let short = Duration::from_secs(1);
        let mut wait = next_backoff(Duration::ZERO, short);
        let mut waits = vec![wait.as_secs()];
        for _ in 0..7 {
            wait = next_backoff(wait, short);
            waits.push(wait.as_secs());
        }
        assert_eq!(waits, [1, 2, 4, 8, 16, 32, 60, 60]);
    }

    #[test]
    fn next_backoff_resets_after_ten_minutes_of_uptime() {
        let long = Duration::from_secs(600);
        assert_eq!(next_backoff(Duration::from_secs(60), long), MIN_BACKOFF);
    }

    #[test]
    fn daemon_args_ends_with_the_host_origin() {
        assert_eq!(daemon_args(Host::App), ["daemon", "run", "--origin", "app"]);
        assert_eq!(
            daemon_args(Host::Online),
            ["daemon", "run", "--origin", "managed"]
        );
    }

    #[tokio::test(start_paused = true)]
    async fn supervise_restarts_with_backoff() {
        let starts = Arc::new(Mutex::new(Vec::new()));
        let seen = Arc::clone(&starts);
        let task = tokio::spawn(supervise(move || {
            seen.lock().unwrap().push(Instant::now());
            async {
                Exit {
                    code: Some(1),
                    uptime: Duration::ZERO,
                }
            }
        }));
        tokio::time::sleep(Duration::from_secs(200)).await;
        task.abort();
        let starts = starts.lock().unwrap().clone();
        assert_eq!(
            gaps(&starts),
            [1, 2, 4, 8, 16, 32, 60, 60].map(Duration::from_secs)
        );
    }

    #[tokio::test(start_paused = true)]
    async fn supervise_waits_a_minute_when_another_daemon_runs() {
        let starts = Arc::new(Mutex::new(Vec::new()));
        let seen = Arc::clone(&starts);
        let task = tokio::spawn(supervise(move || {
            seen.lock().unwrap().push(Instant::now());
            async {
                Exit {
                    code: Some(EXIT_DAEMON_RUNNING),
                    uptime: Duration::ZERO,
                }
            }
        }));
        tokio::time::sleep(Duration::from_secs(130)).await;
        task.abort();
        let starts = starts.lock().unwrap().clone();
        assert_eq!(
            gaps(&starts),
            [Duration::from_secs(60), Duration::from_secs(60)]
        );
    }

    #[tokio::test]
    async fn launch_runs_the_daemon_with_its_origin() {
        let out = tempfile::tempdir().unwrap();
        let args_file = out.path().join("args");
        let script = format!("echo \"$@\" > '{}'", args_file.display());
        let dir = fake_ws(&script);
        let runner = Runner::new(path_with(&dir));

        let status = launch(&runner, Host::App).await.unwrap();

        assert!(status.success());
        let args = std::fs::read_to_string(&args_file).unwrap();
        assert_eq!(args.trim(), "daemon run --origin app");
    }

    #[tokio::test]
    async fn launch_detaches_the_daemon_into_its_own_session() {
        let out = tempfile::tempdir().unwrap();
        let sid_file = out.path().join("sid");
        let script = format!("ps -o sid= -p $$ > '{}'", sid_file.display());
        let dir = fake_ws(&script);
        let runner = Runner::new(path_with(&dir));

        launch(&runner, Host::Online).await.unwrap();

        let child_sid: i32 = std::fs::read_to_string(&sid_file)
            .unwrap()
            .trim()
            .parse()
            .unwrap();
        // SAFETY: `getsid(0)` only reads the calling process's session id.
        let own_sid = unsafe { libc::getsid(0) };
        assert_ne!(child_sid, own_sid);
    }
}
