//! Helpers shared by unit tests.

/// Runs the script once, until the kernel stops answering "text file busy".
/// A `fork` on another test thread can hold the script's write descriptor
/// open until its child execs, so the first `exec` of a freshly written script
/// may fail with ETXTBSY. Once it succeeds no later fork can inherit the write
/// end. The script must be harmless to run with a `probe` argument.
pub fn wait_until_executable(path: &std::path::Path) {
    for _ in 0..500 {
        match std::process::Command::new(path).arg("probe").output() {
            Err(e) if e.raw_os_error() == Some(libc::ETXTBSY) => {
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            _ => return,
        }
    }
}
