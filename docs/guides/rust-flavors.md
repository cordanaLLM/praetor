# Rust flavors and declared compiler minima

The `rust-systems` and `rust-library` flavors use the same `rust-msrv` repository audit gate.
For every discovered `Cargo.toml`, a declared `package.rust-version` or
`workspace.package.rust-version` needs one of these forms of evidence:

- The nearest `rust-toolchain` or `rust-toolchain.toml` pins the declared major and minor
  version. For example, `rust-version = "1.80"` is satisfied by channel `1.80` or `1.80.3`.
  A declaration including a patch requires that exact version.
- One workflow job installs the declared version and subsequently runs `cargo build`,
  `cargo check` or `cargo test` with it.

For example, this job supplies workflow evidence for `rust-version = "1.80"`:

```yaml
jobs:
  msrv:
    runs-on: ubuntu-latest
    steps:
      - uses: dtolnay/rust-toolchain@1.80
      - run: cargo check
```

The gate recognizes the version in the `dtolnay/rust-toolchain` action reference or the
`toolchain` input of a supported Rust setup action. A direct
`rustup toolchain install 1.80` followed by `cargo +1.80 check` also supplies evidence.
Installing a version alone does not build the crate. A floating `stable` pin alone does
not prove a declared minimum. Commands in different jobs, echoed commands and steps
disabled with `if: false` do not supply that evidence. Dynamic matrices and shell scripts
whose compiler selection cannot be read require an explicit minimum-version job.

Workspace members are discovered under the same bounded metadata walk as adoption;
`rust-version.workspace = true` resolves through the containing workspace declaration.
Discovery errors, malformed TOML and exceeded metadata bounds fail the gate. No
`rust-version` declaration prints a stated `[PASS]`; no `Cargo.toml` prints `[SKIP]`.
The verdict names the crate, declared version and pin or workflow evidence. The gate reads
configuration; it does not run Rust compilation or certify a successful workflow run.

Implementation: `internal/adopt/rust_msrv.go`, `internal/adopt/rust_msrv_workflow.go`,
`internal/util/toml.go` and `internal/ghworkflow/ghworkflow.go`. Planted and boundary cases:
`internal/adopt/repository_checks_test.go` and `internal/adopt/rust_msrv_test.go`.
Cargo documents the declaration in [Rust version](https://doc.rust-lang.org/cargo/reference/rust-version.html);
rustup documents pin selection in [Overrides](https://rust-lang.github.io/rustup/overrides.html).
