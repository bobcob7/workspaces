use std::process::Command;

fn main() {
    // The commit the app was built from, as `make build` stamps ws with, so
    // Settings > About can tell whether ws and the app match.
    let describe = Command::new("git")
        .args(["describe", "--tags", "--always", "--dirty"])
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();
    println!("cargo:rustc-env=WS_DESKTOP_COMMIT={describe}");
    println!("cargo:rerun-if-changed=../../.git/HEAD");
    println!("cargo:rerun-if-changed=../../.git/index");
    tauri_build::build()
}
