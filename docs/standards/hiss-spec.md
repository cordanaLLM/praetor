# The High-Integrity Systems Standard (HISS)

The definitive formal specification for deterministic software engineering and autonomous agent governance in every repository Praetor governs.

The High-Integrity Systems Standard (HISS) defines 21 invariants across four governing families.
The invariants below are policy requirements. Their implemented coverage and
remaining proof gaps are recorded in the [HISS refinement audit](../research/hiss-rule-refinement.md);
a passing scanner does not establish every invariant for every language.

```figure
hiss-taxonomy
```

---

## 1. Mathematical Formalism & Core Invariants

### HISS-01: Acyclic Control Flow (Banned Recursion)

Call graphs must form a Directed Acyclic Graph (DAG):
$$G = (V, E), \quad \forall v \in V, \, (v, v) \notin E^*$$
Direct and mutual recursion are strictly prohibited in production runtimes. All iterative algorithms must use bounded stacks or explicit iteration.

### HISS-02: Bounded Loops & Mandatory I/O Timeouts

Every loop construct must possess a compile-time statically verifiable scalar upper bound:
$$\forall \text{loop} \, L, \quad \exists N_{\max} \in \mathbb{N} \quad \text{s.t.} \quad \text{iterations}(L) \le N_{\max}$$
Unbounded `for {}` or `while (true)` loops without static counter termination are rejected. All network and filesystem I/O operations must accept and enforce explicit `context.Context` deadlines.

A Go context without a deadline is accepted in four exact shapes that perform no unbounded I/O
with it; the [rule matching reference](hiss-rule-matching.md#go-hiss-02-context-deadlines-and-the-shapes-it-accepts)
lists the evidence and the near misses each one still reports:

- **Lifecycle-owned:** a `context.WithCancel` context used in the start function of a framework
  hook whose stop function cancels it (`fx.Hook` `OnStart` and `OnStop`), as `main.main` owns the
  process lifetime. The cancel function may be stored in a variable or a field before the stop
  function calls it.
- **Sinks:** the `log/slog` functions and `*slog.Logger` methods that take a context only for its
  values, and a `select` whose cases only send to or receive from channels.
- **Callee-bounded:** a function of the same module that derives `WithTimeout` or `WithDeadline`
  from the context before any other use, followed up to four calls deep.
- **Ignored by a third-party constructor:** an allow-listed function, such as the OTLP gRPC
  exporter constructors, at the versions whose source was read, as the module's `go.mod` states.

A new shape is proposed in an issue that names the exact shape, its evidence at a named version,
and a near miss the rule must keep reporting; it lands as one table entry with a negative and a
positive fixture.

### HISS-03: Zero Frame Malloc (Deterministic Memory)

Hot simulation loops and rendering ticks (e.g. 60Hz/120Hz pipelines) must maintain zero dynamic heap allocations:
$$\Delta \text{HeapAlloc}_{\text{tick}} = 0$$
Memory must be pre-allocated during subsystem initialization. Any dynamic heap allocation detected during a frame loop causes immediate test failure.

### HISS-04: Complexity Bounds & Modular Sizing

Functions must remain strictly bounded in complexity and scope:

| Metric | Upper Bound | Enforcement Tool |
| :--- | :--- | :--- |
| **McCabe Cyclomatic Complexity** | $\le 10$ | `gocyclo` / `clippy` / `semgrep` |
| **Cognitive Complexity** | $\le 15$ | `gocognit` / `sonar` |
| **Function Length** | $\le 60$ LOC | AST Scanner |
| **Executable Statements** | $\le 50$ Statements | Compiler AST |

**Function Length** is measured over the body: from the line carrying the opening brace to the line carrying the closing brace, both counted. The signature is not part of the measurement, so a definition written `int f(void)` / `{` on two lines is exactly as long as the same definition written `int f(void) {` on one, and a signature wrapped across a long parameter list -- the ordinary shape of a GPU kernel -- adds nothing to the count. A brace-delimited scanner may still recognise a function from its signature line, which is how it names one whose brace is elsewhere; recognition and measurement are separate. Reformatting must never move a function across the cap.

### HISS-05: Variable Scoping

Identifiers must be declared in the smallest lexical scope that serves them:

- Narrow lexical scoping prevents accidental variable shadowing and state leakage.
- Variables should be declared immediately before their first use.
- Status: advisory until an executable AST scope analyzer is attached.

### HISS-06: Bounded Concurrency

Worker pools, concurrent routines, and parallel fan-out must declare an explicit scalar upper bound:

- Goroutine pools and thread spawning must specify a maximum concurrency capacity.
- Unbounded queue workers or unbounded concurrency patterns are banned.
- Status: advisory; race-detector tests verify absence of data races but do not verify pool bounds.

---

## 2. Memory Safety, Error Handling & Static Verification

### HISS-07: Checked Errors & Zero Unwrap

Production software must never panic or unwrap:

- Total ban on Rust `.unwrap()` and `.expect()` in non-test code.
- Total ban on unchecked Go error returns (`_ = doSomething()`).
- All error flows must handle the error or wrap it with domain context.
- Abort policy: library code returns an error instead of ending the process. The scanner reports
  Go `panic` and `os.Exit`; Rust `panic!`, `todo!`, `unimplemented!`, `unreachable!`,
  `process::exit` and `process::abort`; and Python `sys.exit`. Tests and binary entry points may
  abort: Go `main.main`, a top-level Rust `fn main`, and the Python `if __name__ == "__main__":`
  block or top-level `def main`. The assert family and exit wrappers such as `log.Fatal` are
  recorded as gaps in [`.config/hiss/coverage.yaml`](https://github.com/cordanaLLM/praetor/blob/main/.config/hiss/coverage.yaml), not
  enforced.
- Go `panic(http.ErrAbortHandler)` is allowed anywhere: `net/http` documents it as the way a
  handler aborts its response, and the server recovers it. Only the exact `net/http` sentinel
  counts; [rule matching](hiss-rule-matching.md#go-the-nethttp-abort-sentinel) lists the shapes.

### HISS-08: Static Determinism & Banned Functions

Dynamic runtime code evaluation is strictly banned:

- Total ban on `eval()`, `exec()`, and dynamic string compilation.
- Total ban on insecure C runtime functions (`gets`, `strcpy`, `sprintf`).

### HISS-09: Reference Safety & Mandatory Safety Proofs

Unsafe pointer arithmetic and memory dereferencing require explicit rationale:

- Any `unsafe` block must be preceded by an explanatory `// SAFETY:` comment proving invariants.
- In Rust the comment may stand directly above the statement holding the block, and an `unsafe fn`
  declaration documents its callers' contract in a rustdoc `# Safety` section or a `// SAFETY:`
  comment.
  [Rule matching](hiss-rule-matching.md#rust-where-a-safety-proof-attaches) lists the accepted shapes.
- Missing `// SAFETY:` comments trigger immediate AST check rejection.

### HISS-10: 5-Layer Zero-Warnings Cascade

Warnings are treated as fatal errors across all operational layers:

1. **IDE Layer**: Real-time language server diagnostics (`standards-lsp`).
2. **Pre-Commit**: Fast local Git hooks (`lefthook`).
3. **Pre-Push**: Local test suite and branch audit.
4. **CI Layer**: Multi-platform status checks.
5. **Pre-Apply**: Admission controllers and deployment webhooks.

- `praetorctl audit` and the MCP `standards_audit` read the CI layer's build lanes: every
  workflow command that compiles C, C++, Rust or Go code must carry its toolchain's
  warnings-as-errors form (`-Werror`, `/WX`, `CMAKE_COMPILE_WARNING_AS_ERROR`, `meson setup
  --werror`, `-D warnings` or a `[lints]` deny for Cargo, a `go vet` step and, with cgo files,
  `CGO_CFLAGS: -Werror`), and a lane without it fails the audit
  ([Build-warnings gate](../guides/build-warnings.md)). The gate reads workflow files, the root
  `Cargo.toml`'s `[lints]` and which sources the repository holds; what a make target, script or
  other build file runs is not read.
- A lane that cannot use the form yet is declared in the manifest's exceptions list (rule
  `HISS-10`, the workflow, a reason and an expiry at most 90 days ahead); an expired entry fails
  like a missing one.

---

## 3. Supply Chain, Fleet Governance & Testing

### HISS-11: Hermetic Supply Chain

Every dependency manifest must be cryptographically pinned:

- Pinned lockfiles mandatory (`go.sum`, `Cargo.lock`, `pnpm-lock.yaml`).
- Zero floating tags (e.g. `:latest`) in container deployments.
- SLSA provenance at the declared `supply_chain.slsa_level` and Sigstore Cosign signatures on all
  released binaries.
- `praetorctl audit` and the MCP `standards_audit` measure the SLSA Build level, cosign signing
  and SBOM generation the release workflows can produce, and fail when the policy declares more
  ([How the audit measures the SLSA level](../guides/releasing.md#how-the-audit-measures-the-slsa-level)).
  The measurement reads workflow files only; published attestations are not checked.
- A gap the release workflows cannot close yet is declared in the manifest's exceptions list (rule
  `HISS-11`, the workflow the measurement read, a reason and an expiry at most 90 days ahead): the
  audit prints it with the declared and measured values and passes until the entry expires, and
  an expired entry fails like a missing one
  ([Declaring a gap](../guides/releasing.md#declaring-a-gap)).

### HISS-12: Secret Leak Prevention

Zero credentials in Git history:

- Automated secret detection (`make secrets` / `gitleaks`) scans repository history inside `verify-all`.
- API keys, private certificates, and credentials must never be committed.

### HISS-13: Monotonic Debt Ratchet

Total recorded infractions never grow against the committed baseline:

- An increase in technical debt requires a deliberately recorded rationale.
- Evaluated against `.standards-baseline.json` by `praetorctl baseline --verify` (read-only), `praetorctl audit` and the gate's HISS stage.
- A recorded infraction is identified by its rule, its file and the function that holds it (or, outside a function, the text of its line), never by its line number, so code that only moves stays baselined ([A baseline entry survives a line shift](../guides/adoption-verification.md#a-baseline-entry-survives-a-line-shift)).
- A rejection still fails when a check added after the baseline was recorded finds debt in unchanged code, but it attributes those findings to the check, not to the change, and names the deliberate re-record ([A HISS rejection names the violations](../guides/adoption-verification.md#a-hiss-rejection-names-the-violations)).

### HISS-14: Append-Only ABI & Migration Footers

Public application binary interfaces must evolve safely:

- Public APIs are append-only.
- Any breaking change requires a conventional commit breaking indicator (`!`) and a mandatory `Migration:` footer documenting upgrade instructions.

### HISS-15: 3D Test Discipline

All public methods require three-dimensional test coverage:

1. **Positive Tests**: Assert correct results under valid operational inputs.
2. **Negative Tests**: Assert correct error returns under invalid inputs.
3. **Boundary Tests**: Assert correct handling at numeric, string, and buffer limits ($0, 1, N_{\max}$).
4. **Clean Rule**: Any file modified in a pull request must have all historical debt resolved.

### HISS-16: Agentic Fleet Governance & Server-Side Enforcement

Agent instructions originate from a single canonical source (`AGENTS.md`):

- All vendor harnesses (`CLAUDE.md`, Cursor rules, Copilot) are compiled via `standardsctl compile-context`.
- Every line outside a `## <Vendor>` heading is shared by all targets. A `## <Vendor>` section (`Claude Code`, `Cursor`, `GitHub Copilot`, `Windsurf`, `Gemini`, `Codex`) compiles into that target alone and is removed from the other five, so one agent's guidance never reaches another.
- Authoritative verification executes inside non-root ephemeral sandboxes with cgroup limits and default-deny egress.
- The "## Text Register" block in AGENTS.md is generated from the `register:` section of `.standards.yaml` by `standardsctl compile-context` and verified by `--verify`; hand edits between its markers are reported as drift. Repository conventions such as a changelog fragment lane render only where `register.conventions` states them or the repository keeps a `changelog.d/` directory that holds a file ([Text register](../guides/text-register.md#repository-conventions)).

---

## 4. Agent Operations & Replayable Evidence

### HISS-17: State Ledger Discipline

Every agent turn maintains the local `.workingdir` ledger rather than re-deriving state from scratch:

- Turn start reads `praetorctl state status` (a bounded summary) and the open tasks in `.workingdir/OPEN.md`; the whole `.workingdir/STATE.md` is never read at turn start.
- In-flight work is tracked via `standardsctl state task add` / `complete` / `archive`, never held only in an agent's own working memory.
- Turn end runs `standardsctl state sync .`, which records working-tree status, dirty count, open tasks and a cryptographic state hash into `STATE.md`.
- The whole `.workingdir` directory is private and Git-ignored; reviewed, sanitized material is published under `docs/` instead.

### HISS-18: Diff-Aware CI Efficiency

CI pipelines evaluate the git diff before choosing which gates to run:

- `standardsctl ci filter` classifies a change and exports the gates it requires.
- A docs-only or session-state-only change skips the heavy race detector and security suites; every other change keeps full invariant coverage.
- The filter fails closed. A file kind no classifier recognises (an extensionless script, a Dockerfile, a template) counts as configuration and runs tests, linters and security. A dependency or build manifest (`package.json`, `package-lock.json`, `pnpm-lock.yaml`, `go.mod`, `go.sum`, `Cargo.toml`, `Cargo.lock`, `pom.xml`, a `Makefile`, `tsconfig*.json`, `CMakeLists.txt`, and the pip `requirements*` and `constraints*` files ending in `.txt` or `.in`) is configuration, not documentation, even under `docs/` (`TestBuildManifestTextIsConfiguration`, `TestDependencyManifestUnderDocsIsConfiguration`). An unresolvable base ref or an unreadable manifest runs the full matrix. Every file `compile-context` writes counts as agent text and runs the HISS-16 context check. Agent text also selects the documentation gates (`run_docs`), because the Markdown gate and the credits gate read it (`TestCreditsGateInputsSelectTheCreditsGate`; `internal/cifilter/filter.go`, `internal/cifilter/cifilter_test.go`).
- Code is every extension the HISS scanner reads (`hiss.SupportsExtension`: C, C++ with `.h`, `.hpp` and `.hh` headers, CUDA, HIP, Go, Python, Rust, JavaScript, TypeScript, Svelte and shell scripts with `.sh` or `.bash`), plus Java, Dart, Protocol Buffers, Zig and shading-language sources: GLSL (`.glsl` and the glslang stage suffixes such as `.vert`, `.frag`, `.comp`), HLSL, WGSL and Metal. A code change runs tests, linters and security without context sync. Files under a `test/`, `tests/` or `benches/` directory at any depth, such as a Cargo crate's `tests/`, are tests; the directory names no file kind, so a script or fixture there still fails closed (`internal/cifilter/kinds_test.go`). A file the scanner claims only from its contents (an extensionless script, a systemd unit, an Ansible playbook) is not code here: a diff classifier reads paths, not bytes, so it keeps failing closed or classifying as configuration (`TestUnclassifiedFileKindsRunHeavyGates`).
- `overrides.ci` in `.standards.yaml` governs the filter. Setting `diff_aware_filtering: false` runs the full matrix for every change; setting `skip_heavy_gates_on_docs_or_state: false` runs it for docs-only and state-only changes. A manifest without the block keeps both on; a declared block reads an omitted key as `false`. `ci filter --config` names another manifest (`internal/config/config.go` `EffectiveCI`, `internal/cifilter/changed_files_test.go`).
- A gate that is skipped by classification is distinct from a gate that fails: the filter's decision is itself part of the recorded evidence.
- `praetorctl audit` and the MCP `standards_audit` read the repository's own workflow triggers and report, as `[WARN]` lines naming the file and line, a `push` that runs on every branch, a pull request job that runs its work on a draft, and a job that skips a draft with a job-level condition or through a need held back on the draft. The accepted shape is the hosted gates' (`internal/ghworkflow/hostedgate.go`). Findings warn rather than fail because this rule's failure action rejects nothing. A workflow that must run everywhere is declared in the exceptions list (rule `HISS-18`, its path, a reason and an expiry at most 90 days ahead) ([Workflow trigger audit](../guides/workflow-triggers.md)).

### HISS-19: Reuse Before Writing

One behavior has exactly one implementation:

- Before writing a function, config loader, parser or command, the repository is searched for the capability first; an existing implementation is extended or called rather than reimplemented.
- Configuration formats are held to the same rule: a second config system beside an existing loader is the same defect, because the two silently drift apart.
- `praetorctl dedupe scan .` enforces this with function-level clone and utility-sprawl detection, run by `make dedupe` inside `verify-all`. Any clone *or* sprawl finding fails the scan: a finding the verdict does not carry is a finding nobody resolves.
- The scan reads Go only. It counts the repository's source files in other languages (`util.SourceLanguage` in `internal/util/sourcelang.go`), lists them under `Not Scanned`, and marks the verdict `partial: Go sources only`, because HISS-19 is not measured for those files. A repository with no Go source gets no verdict at all (`Unscanned` and `Partial` in `internal/dedupe/dedupe.go`; `TestScanRepo_Positive_PolyglotVerdictIsPartial` in `internal/dedupe/coverage_test.go`). `--json` prints the same report as JSON.
- The clone key renames a function's parameters, receiver, results and locals by first use before hashing (`cloneKey` in `internal/dedupe/dedupe.go`), so a copy whose locals were renamed still matches, while a body that reads a different local or field does not. Bodies under three statements or five printed lines are not hashed.
- `praetorctl dedupe cadence` makes a sweep due after 20 commits, or once 1,000 Go production lines or 10 Go production files have been added since the recorded sweep, whichever comes first (`--threshold`, `--added-lines`, `--added-files`; `CheckCadence` in `internal/dedupe/cadence.go`).
- Duplication that is genuinely unavoidable (such as toolchain-generated boilerplate that cannot be unified) is declared in the top-level `exceptions` list of `.standards.yaml` with rule `HISS-19`. The entry names one repository file by `path` (a glob is refused), a reason, and an expiry at most 90 days ahead (`config.ExceptionRuleDedupe` in `internal/config/exceptions.go`):

  ```yaml
  exceptions:
    - rule: "HISS-19"
      path: "api/v1alpha1/zz_generated.deepcopy.go"
      reason: "controller-gen produces near-identical DeepCopyInto methods per type"
      expires: "2026-11-01"
  ```

  A clone group whose members all sit in declared, unexpired files does not fail the scan and is reported under `Excepted Duplicate Function Blocks` with each entry's reason and expiry. A group with any member outside excepted files still fails (an entry whose file sits only in mixed groups still fails the scan through the mixed group). An expired entry fails like a missing one, naming the entry; an entry that excuses no duplicate function block is stale and fails until removed. The target must exist and be a regular repository file; no skip is applied on generated-code headers or file names alone.
- Unexcused duplication is justified in the commit body, not left silent.

### HISS-20: Replayable Enforcement Evidence

A claim of coverage is reproducible, never merely asserted:

- Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against a fixture corpus by `praetorctl hiss coverage --verify`, run inside `verify-all`.
- The check runs in both directions: a claim of enforcement must reproduce each of its positive fixtures, and a claim of absence must leave its gap fixtures undetected.
- A rule that silently *gains* coverage fails the gate exactly as one that silently loses it, so the catalog cannot drift in either direction undetected.
- The coverage catalog may only declare evidence for a rule the HISS rule catalog defines ([`internal/hisscatalog/catalog.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/catalog.go)); an unregistered identifier fails `Catalog.Validate` (`TestValidateRefusesUnregisteredRule`). A coverage entry takes its name from that catalog too, and `praetorctl hiss coverage` prints the catalog's name for every rule. `title:` may be omitted; a title that differs from the catalog's is a `[WARN]` line naming the catalog's current text, never a failure (`TestValidateWarnsOnADriftedTitle`, `TestRunHissCoverage_Negative_DriftedTitleWarnsWithTheCatalogText`), so a catalog retitle does not break a repository that repeats the earlier text. `praetorctl hiss coverage --sync-titles` lists each stale title as a dry run, and `--sync-titles --write` rewrites them in place, changing only each title's value (`SyncTitles` in `internal/hisscoverage/titles.go`; `TestRunHissCoverage_Positive_SyncTitlesFixesElevenDriftedTitles`). The HISS rule catalog also feeds `standards_explain_rule`, the generated wiki's HISS matrix and the invariant table of every adopted `AGENTS.md`, so all of them name the same 21 rules.

### HISS-21: Platform Neutrality

A repository's gates, hooks and generated templates run on Linux, macOS and Windows, or declare the platform they require and skip with a stated reason where it is absent:

- A gate has exactly two acceptable states: running, with its result standing as the platform's result; or skipped, with the reason printed and the alternate coverage source named.
- A check that silently does not run and reports success is prohibited — see the [Platform Neutrality invariant](hiss-21-platform-neutrality.md) for the incident history and enforcement detail.
- Enforced by the Platform Neutrality matrix in CI.
