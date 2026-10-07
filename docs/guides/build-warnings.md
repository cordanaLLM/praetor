# Build-warnings gate (HISS-10)

HISS-10 makes every compiler warning fatal. A CI lane that compiles with its toolchain's
defaults prints its warnings and still passes: one CI run of an adopter repository printed
compiler warnings in 39 of its 103 jobs, about 71,600 in each MSVC job, and every job stayed
green. The build-warnings gate fails such a lane. It reads each workflow under
`.github/workflows`, finds every command that compiles C, C++, Rust or Go code, and requires the
warnings-as-errors form that toolchain documents.

The gate is `adopt.AuditBuildWarnings` in
[`internal/adopt/build_warnings_audit.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/build_warnings_audit.go),
over the measurement `forge.MeasureBuildWarnings` in
[`internal/forge/build_warnings.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/forge/build_warnings.go).
`praetorctl audit` and the MCP `standards_audit` both run it.

## Quick start

Give every build lane its toolchain's form:

| Toolchain | Lane the gate reads | Passes with |
| :--- | :--- | :--- |
| gcc, clang, icx, icpx (any target prefix or version suffix) | a command that compiles a source file or runs with `-c` | `-Werror` on the command, not undone by a later `-Wno-error`. `-Werror=<warning>` covers that warning only and does not count ([GCC](https://gcc.gnu.org/onlinedocs/gcc/Warning-Options.html)). |
| MSVC `cl`, `clang-cl` | the same | `/WX` (or `-WX`) on the command or in the `CL` or `_CL_` variable, not undone by a later `/WX-` ([MSVC](https://learn.microsoft.com/en-us/cpp/build/reference/compiler-option-warning-level)) |
| CMake | a configure run (`cmake -S . -B build`, `cmake --preset`) | `-DCMAKE_COMPILE_WARNING_AS_ERROR=ON` (any CMake true constant, CMake 3.24 or later) without `--compile-no-warning-as-error`, or `-Werror` in every `CMAKE_C_FLAGS` and `CMAKE_CXX_FLAGS` it defines ([CMake](https://cmake.org/cmake/help/latest/variable/CMAKE_COMPILE_WARNING_AS_ERROR.html)) |
| Meson | `meson setup` | `--werror` or `-Dwerror=true` ([Meson](https://mesonbuild.com/Builtin-options.html)) |
| Cargo | `cargo build`, `check`, `test`, `run`, `bench`, `clippy`, `rustc`, `nextest` | `-D warnings` in `CARGO_ENCODED_RUSTFLAGS`, else in `RUSTFLAGS`, the first of the two cargo uses; or after `cargo clippy --` and `cargo rustc --` ([Cargo](https://doc.rust-lang.org/cargo/reference/config.html), [Clippy](https://doc.rust-lang.org/clippy/continuous_integration/index.html)) |
| rustc | a command that compiles a `.rs` file | `-D warnings` on the command |
| Go | `go build`, `test` and `install` of the repository | a step of any workflow that runs `go vet` or `go test -vet=all`: the Go compiler reports no warnings, and `go vet` exits non-zero on a finding ([cmd/vet](https://pkg.go.dev/cmd/vet)) |

`-D warnings` may also be written `-Dwarnings`, `--deny warnings`, `--deny=warnings` or with
`-F`/`--forbid`; a later `-W warnings` or `-A warnings` undoes a deny, never a forbid.

```yaml
env:
  RUSTFLAGS: -D warnings

jobs:
  native:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: cmake -S . -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=ON
      - run: cmake --build build
      - run: cargo test --locked
      - run: go vet ./...
```

Praetor's scaffolded CI workflows pass the gate: the Rust workflow
(`templates/rust/ci-rust.yml.tmpl`) sets `RUSTFLAGS: -D warnings` on its job and the Go workflow
runs `go vet ./...` (`TestAuditBuildWarningsPassesTheScaffoldedWorkflows`).

## What the gate reads

- **Commands of `run:` steps.** A script is cut into commands at `;`, `&&`, `||`, `|`, `&` and
  line ends. Words before the program that run nothing of their own (`sudo`, `env`, `time`,
  `ccache`, `sccache`, `if`, `then`, `do`) are passed over. An `echo cargo build` or a command in
  a comment builds nothing.
- **Variables.** A variable is read from the command's own assignments
  (`RUSTFLAGS="-D warnings" cargo build`), an `export` earlier in the script, then the step's,
  the job's and the workflow's `env:`, in that order. A word that is one whole reference
  (`$CMAKE_ARGS`, `${CMAKE_ARGS}`) expands to its value. An `env:` that is one expression, such as
  `${{ fromJSON(vars.CI_ENV) }}`, decides nothing the file shows, so the lane fails.
- **Configure before build.** `cmake --build` and `meson compile`, `test` or `install` are lanes
  only in a job where no earlier `run:` step configures: what configured that tree cannot be
  read, so the lane fails and says so.
- **Go.** Every Go lane of every workflow passes once one binding step runs `go vet` or
  `go test -vet=all`. `go install` of a `module@version` installs a tool, not the repository,
  and `go run` compiles a program only to execute it, such as the API gate
  `tools/apicompat/gate/main.go` adoption writes; neither is a lane.
- **Binding.** A step or job with `continue-on-error` passes whatever its compiler reports, so
  its lane fails, and a `go vet` step with it does not count. A job or step whose `if:` is the
  literal `false` is not read.

## What the gate does not read

The gate reads the flags a `run:` step passes. It does not run a build, and it does not read:

- what a `make` target, a script (`./build.sh`, `bash -c`), an action (`uses:`) or a reusable
  workflow of another repository compiles;
- a preset's `cacheVariables` (`CMakePresets.json`), a Meson machine file or `default_options`,
  `CMakeLists.txt`, `Cargo.toml` `[lints]` or `.cargo/config.toml` `build.rustflags`;
- an assignment in PowerShell or cmd syntax, or a variable a step writes to `$GITHUB_ENV`;
- whether the script lets the failure through (`|| true`, `set +e`);
- which warnings the lane enables (`-Wall`, `-Wextra`, `warning_level`): `-Werror` makes the
  enabled ones fatal;
- other compilers, such as `nvcc`, and which analyzers a golangci-lint configuration runs.

A lane whose form lives in one of these places fails until the form is on the step, or until an
exception declares it. A lane the gate never sees passes unread:
[`gap/build-failure-caught.yml`](https://github.com/cordanaLLM/praetor/blob/main/.config/hiss/testdata/HISS-10/github-actions/gap/build-failure-caught.yml)
and
[`gap/make-hides-the-compiler.yml`](https://github.com/cordanaLLM/praetor/blob/main/.config/hiss/testdata/HISS-10/github-actions/gap/make-hides-the-compiler.yml)
record two such shapes in the HISS-20 coverage table, `.config/hiss/coverage.yaml`.

## Exceptions

A lane that cannot build with warnings as errors yet, such as one compiling a vendored tree,
takes an entry in the top-level `exceptions` list of `.standards.yaml`:

```yaml
exceptions:
  - rule: "HISS-10"
    path: ".github/workflows/ci.yml"
    reason: "vendored codec still warns under MSVC; upstream fix pending"
    expires: "2026-12-31"
```

`path` names one workflow file directly in `.github/workflows`; the entry excuses every failing
lane of that workflow. The audit prints each excused lane under the entry's reason and expiry and
passes. An expired entry fails like a missing one. An entry whose workflow has no failing lane,
or that is left when no workflow builds anything, is stale and fails until it is removed. The
list's general rules, one line of reason and an expiry at most 90 days ahead, are in
[clang-tidy coverage: Exceptions](clang-tidy-coverage.md#exceptions).

## Reading the output

A failure names each lane, what it lacks and the form to add, then the way out:

```text
[FAIL] Build warnings (HISS-10): .github/workflows/ci.yml:14: job native, step "cmake -S . -B build": CMake builds without warnings as errors: the configure command defines no CMAKE_COMPILE_WARNING_AS_ERROR. Add: configure with -DCMAKE_COMPILE_WARNING_AS_ERROR=ON (CMake 3.24 or later) and without --compile-no-warning-as-error.
[FAIL] Build warnings (HISS-10): no live exceptions entry declares the lanes above; build each with its toolchain's warnings-as-errors form (docs/guides/build-warnings.md), or, for a lane that cannot yet, declare its workflow in the exceptions list of .standards.yaml (rule HISS-10, path .github/workflows/<workflow>, a reason, and an expiry at most 90 days ahead).
```

A pass counts the lanes by toolchain, lists the excused ones, and states what was read:

```text
[PASS] Build warnings (HISS-10): 4 build lanes fail on a warning (CMake 1, Cargo 1, Go 2).
Read from the run: steps of the workflow files under .github/workflows; what a make target, a script, an action, a preset or a build file sets was not read, and no build was run.
```

A repository whose workflows build none of these languages skips the gate:

```text
[SKIP] Build warnings (HISS-10) not checked: no run: step under .github/workflows builds C, C++, Rust or Go code, so no lane needs warnings as errors.
```

## Tests

- `internal/forge/build_warnings_test.go`: each toolchain's form, each lane without it, the Go
  vet rule, and the commands that are no lane.
- `internal/adopt/build_warnings_audit_test.go`: the gate per language, exceptions, the skip,
  the scaffolded workflows, and the HISS-10 fixture replay.
- `cmd/standardsctl/audit_build_warnings_test.go` and
  `TestServerAuditRunsTheBuildWarningsGate` in `cmd/standards-mcp/audit_policy_test.go`: the
  CLI and MCP audits run the gate.
