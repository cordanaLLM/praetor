---
name: compiler-json
description: "Repair Rust compiler diagnostics from Cargo JSON messages."
---

# Rust Compiler JSON Repair

Use for Rust build, check, Clippy, or test failures.

## Practices

1. Run Cargo with `--message-format json`. Checked version: Cargo 1.98.1;
   Cargo Book: one JSON object per output line.
2. Parse each JSON line. Keep objects where `reason` is `compiler-message`;
   use `message.code`, `message.level`, `message.rendered` for context.
3. Locate diagnostic at span marked `is_primary: true`. Fix file, line, column;
   related spans give context, not edit location.
4. Re-run after each repair. Stop after three rounds, on clean run, or when
   one round makes no progress; report remaining diagnostics.
5. Never disable or downgrade lint. Change code only when diagnostic points to
   real defect or maintainability issue.

```commands
cargo build --message-format json
cargo check --message-format json
cargo clippy --message-format json
cargo test --message-format json
```
