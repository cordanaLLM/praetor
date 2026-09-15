// Pinned by exact version and locked to the committed Cargo.lock resolution.
use std::process::Command;

pub fn install_helper() -> std::io::Result<std::process::ExitStatus> {
    Command::new("cargo")
        .args(["install", "cargo-audit", "--version", "0.21.0", "--locked"])
        .status()
}
