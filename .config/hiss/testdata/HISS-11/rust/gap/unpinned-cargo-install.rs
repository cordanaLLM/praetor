// HISS-11: a build-time dependency is resolved at run time with no version pin, so the
// binary that ends up installed is whatever the registry serves today.
use std::process::Command;

pub fn install_helper() -> std::io::Result<std::process::ExitStatus> {
    Command::new("cargo").args(["install", "cargo-audit"]).status()
}
