use std::process::Command;

fn main() {
    // The commit the app was built from, as `make build` stamps ws with, so
    // Settings > About can tell whether ws and the app match. A build with no
    // .git to describe (the Docker image build) passes it in
    // WS_DESKTOP_COMMIT, set to the release tag; otherwise ask git.
    let given = std::env::var("WS_DESKTOP_COMMIT")
        .ok()
        .filter(|v| !v.is_empty());
    let describe = given.unwrap_or_else(|| {
        Command::new("git")
            .args(["describe", "--tags", "--always", "--dirty"])
            .output()
            .ok()
            .filter(|o| o.status.success())
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
            .unwrap_or_default()
    });
    println!("cargo:rustc-env=WS_DESKTOP_COMMIT={describe}");
    println!("cargo:rerun-if-env-changed=WS_DESKTOP_COMMIT");
    println!("cargo:rerun-if-changed=../../.git/HEAD");
    println!("cargo:rerun-if-changed=../../.git/index");
    #[cfg(feature = "desktop")]
    tauri_build::build();
}
