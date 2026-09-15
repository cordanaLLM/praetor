use std::process::Command;

// Spawning a shell around a runtime-built string is dynamic execution. Nothing in
// internal/hiss, .golangci.yml or .config/semgrep/hiss-invariants.yml has a Rust
// HISS-08 rule, so this is silent.
fn run(script: &str) -> std::io::Result<std::process::ExitStatus> {
    Command::new("sh").arg("-c").arg(script).status()
}
