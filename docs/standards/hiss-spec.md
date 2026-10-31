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

#### Go Declaration Grammar & Verification Contract

The static scanner cannot identify hot simulation loops without an explicit declaration. Hot paths declare their allocation invariant through their doc comment:

```go
//hiss:hot <reason>
func Step() { ... }
```

- **Marker Grammar:** `//hiss:hot <reason>`. An explanatory reason is mandatory; a bare `//hiss:hot` marker is reported as a rule finding. Markers placed on non-function declarations (`const`, `var`, `type`) are invalid and reported with a stated finding while remaining ignored for test requirements.
- **Verification Test Shape:** For each marked function or method, `_test.go` files in the same package directory (internal or external test package) must contain a call to `testing.AllocsPerRun(n, f)` where $n > 0$ and the function literal calls the marked function (as an identifier or selector).
- **Zero Assertion:** The measurement result must be compared against 0 in an `if` statement (`if allocs != 0` or `if allocs > 0`) whose body fails the test via `t.Fatal`, `t.Fatalf`, `t.Error`, or `t.Errorf`.
- **Language Scope:** Go only. Unmarked code is unchecked. Rust and C implementations are unchecked.

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
- The Go audit scanner checks `var` and `:=` locals declared directly in a function's body. A local with at least one use is reported when every read and write lies inside one nested `if`, `for`, `range`, `switch`, `select`, or plain block. Headers and case clauses count as part of their enclosing statement; deeper uses name the outermost child containing them. Findings identify the function, variable, declaration line, and target block kind and line.
- Only side-effect-free, order-independent initializers are candidates: `var x T` without a value, basic literals (including parentheses and constant unary signs/operators), proven constant identifiers, and composite literals whose elements, keys, and values recursively meet that allow-list. Every initializer in a multi-name declaration must pass. Calls (including conversions), receives, indexes, variable reads, unproven selectors, function literals, and method values are excluded. In particular, `err := f()` followed by `if err != nil` is not a finding; `buf := []byte{}` used only in a loop and `n := 0` used only in an `if` are candidates.
- Any read or write outside that child keeps the declaration, including a later assignment, blank use, or an accumulator read after a loop. Parameters and named results are excluded. Address-taking and closure capture conservatively keep a declaration even inside the child block.
- Coverage is partial: constant proof uses local declarations, package declarations in the same file, and unshadowed `true`/`false`; unresolved names, imported selectors, and unproven field keys are skipped. Initializer proof is bounded to 1,024 expression nodes per declaration. Nested declarations (including shadowing chains), closure-only uses inside a child block, and other languages remain unchecked. Declaration identity reuses the Go scope model (`internal/hiss/go_scope.go`); `.config/hiss/testdata/HISS-05/go/` replay through `praetorctl hiss coverage --verify`. Adopted Go repositories receive this check through `praetorctl audit`; new findings fail the HISS-13 ratchet.

### HISS-06: Bounded Concurrency

Worker pools, concurrent routines, and parallel fan-out must declare an explicit scalar upper bound:

- Goroutine pools and thread spawning must specify a maximum concurrency capacity.
- Unbounded queue workers or unbounded concurrency patterns are banned.
- **Go enforcement (`internal/hiss/go_fanout.go`):** any `go` statement inside a `for` or `range`
  body (directly, or inside a `func` literal invoked in that loop) is reported across production,
  generated, and `_test.go` files unless the enclosing function bounds it by one of five shapes:
  1. **Bounded `errgroup.Group`:** an `errgroup.Group` or `*errgroup.Group` with a `SetLimit` call
     on the same group before the loop (`g.SetLimit(n)` with positive `n`).
  2. **Weighted semaphore:** a `semaphore.Weighted` `Acquire` (or `TryAcquire`) call inside the
     loop body before the `go` statement.
  3. **Buffered token channel:** a send on a buffered channel (`make(chan T, cap)`) inside the loop
     body before the `go` statement with a matching receive (`<-ch`) in the goroutine (deferred or
     at the end of the goroutine body).
  4. **Static loop bound:** a `range` over a fixed-length array type (`[N]T` or `[...]T`) or a
     constant-bounded counter loop (`for i := 0; i < constN; i++` where `constN` is a constant
     expression).
  5. **Allow-listed worker pool (`//hiss:bound`):** a worker pool loop over a constant or an
     explicit `int` variable named in a `//hiss:bound <var><=<bound> <reason>` (or
     `//hiss:bound <var>=<bound> <reason>`) comment immediately above the loop stating the bound
     value and justification.
- Other languages remain unchecked by the scanner; `go test -race` verifies absence of data races
  on executed paths but does not prove pool bounds.

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
- A process signal needs a target bounded above 1 (`killpg`, `kill(-pid)`, Go `syscall.Kill`, `unix.Kill`, Node `process.kill(-pid)`). Signal calls to a process group or process must verify the target exceeds 1 or is guarded earlier in the scope.
  - Python guard shape: `if not isinstance(pgid, int) or isinstance(pgid, bool) or pgid <= 1 or pgid == os.getpgrp(): <refuse>`
  - Go guard shape: `if pid <= 1 || pid == os.Getpid() || pid == syscall.Getpgrp() { <refuse> }`
  - JavaScript / TypeScript guard shape: `if (child.pid <= 1) <refuse>`

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
  `HISS-10`, the workflow, a reason and an expiry within the category interval (A: stated, at most 90 days; B: 3 days, C: 10 days, D: 120 days; uncategorized: 90 days)); an expired entry fails
  like a missing one.

---

## 3. Supply Chain, Fleet Governance & Testing

### HISS-11: Hermetic Supply Chain

Every dependency manifest must be cryptographically pinned:

- Pinned lockfiles mandatory (`go.sum`, `Cargo.lock`, `pnpm-lock.yaml`).
- Zero floating tags (e.g. `:latest`) in container deployments.
- `praetorctl audit` and the MCP `standards_audit` scan `Dockerfile` / `Containerfile` `FROM`,
  compose, Kubernetes and Helm `image:`, GitHub Actions `container:` / `services.*.image` /
  `uses: docker://`, `devcontainer.json` `image`, and fully qualified container image string
  literals in source files (`internal/supplychain.ScanImageRefs`), failing on any resolvable
  reference lacking an `@sha256:` digest pin unless recorded in `.standards-baseline.json`
  (`rule_id: "HISS-11"`, via `praetorctl baseline --record-image-refs --reason "<why>"` or recorded
  once at adoption) or excused by a live `HISS-11` entry in `.standards.yaml`. Unresolvable
  references (template expressions, build arguments, or shell variables) and links that leave the
  repository, which are never followed, are reported via `[INFO]`
  with their count and sample locations and are not judged as blocking violations.
- SLSA provenance at the declared `supply_chain.slsa_level` and Sigstore Cosign signatures on all
  released binaries.
- `praetorctl audit` and the MCP `standards_audit` measure the SLSA Build level, cosign signing
  and SBOM generation the release workflows can produce, and fail when the policy declares more
  ([How the audit measures the SLSA level](../guides/releasing.md#how-the-audit-measures-the-slsa-level)).
  The measurement reads workflow files only; published attestations are not checked.
- A gap the release workflows cannot close yet is declared in the manifest's exceptions list (rule
  `HISS-11`, the workflow the measurement read, a reason and an expiry within the category interval (A: stated, at most 90 days; B: 3 days, C: 10 days, D: 120 days; uncategorized: 90 days)): the
  audit prints it with the declared and measured values and passes until the entry expires, and
  an expired entry fails like a missing one
  ([Declaring a gap](../guides/releasing.md#declaring-a-gap)).

### HISS-12: Secret Leak Prevention

Zero credentials in Git history:

- Automated secret detection (`make secrets` / `praetorctl security secrets`) scans full repository history inside `verify-all`.
- History scans must inspect merge commits using `--log-opts="--full-history --branches --tags --remotes=origin -m"` because default `git log -p` omits patches for merge commits (the repository's own branches, tags and `origin` remote refs; history fetched from any other remote belongs to that repository's own scan).
- API keys, private certificates, and credentials must never be committed.
- The scaffolded `.gitleaks.toml` extends the gitleaks defaults, which name GitHub and GitLab tokens, with rules for the forge token shapes the defaults lack: a Gitea or Forgejo access token (40 lowercase hex) assigned to a name that says so, sent in an `Authorization: token` header, or written as the password of an https URL (`templates/native/.gitleaks.toml.tmpl`, `TestScaffoldedGitleaksConfigDetectsForgeTokens`). The generic rule's entropy threshold misses a low-entropy token, and a 40-hex git object id stays quiet.
- Secret scan exemptions must be declared in `.standards.yaml` with `rule: HISS-12`, target `path`, `tool` (the gitleaks rule id, e.g. `generic-api-key`), optional finding `fingerprint`, mandatory ISO-8601 `expires` date, and rationale in `reason`.
- A HISS-12 `path` may name a file that exists only in history (a secret removed from the tree but not from the log). The audit's exceptions check does not require it in the tree: the secret gate judges the entry against the scanned history and fails an entry no finding uses (`cmd/standardsctl/audit_exceptions.go`, `TestAuditExceptionTargets_SecretsHistoryPath`).
- An expired exemption excuses nothing; an exemption that excuses no findings fails the gate ("excuses no finding: remove it"). Undated exemption files such as `.gitleaksignore` are prohibited.

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
- Code is every extension the HISS scanner reads (`hiss.SupportsExtension`: C, C++ with `.h`, `.hpp` and `.hh` headers, Objective-C++ `.mm` read by the C/C++ rules, CUDA, HIP, Go, Python, Rust, JavaScript, TypeScript, Svelte and shell scripts with `.sh` or `.bash`; `.m` is listed as unscanned objective-c where a marker shows the repository is Objective-C), plus Java, Dart, Protocol Buffers, Zig and shading-language sources: GLSL (`.glsl` and the glslang stage suffixes such as `.vert`, `.frag`, `.comp`), HLSL, WGSL and Metal. A code change runs tests, linters and security without context sync. Files under a `test/`, `tests/` or `benches/` directory at any depth, such as a Cargo crate's `tests/`, are tests; the directory names no file kind, so a script or fixture there still fails closed (`internal/cifilter/kinds_test.go`). A file the scanner claims only from its contents (an extensionless script, a systemd unit, an Ansible playbook) is not code here: a diff classifier reads paths, not bytes, so it keeps failing closed or classifying as configuration (`TestUnclassifiedFileKindsRunHeavyGates`).
- `overrides.ci` in `.standards.yaml` governs the filter. Setting `diff_aware_filtering: false` runs the full matrix for every change; setting `skip_heavy_gates_on_docs_or_state: false` runs it for docs-only and state-only changes. A manifest without the block keeps both on; a declared block reads an omitted key as `false`. `ci filter --config` names another manifest (`internal/config/config.go` `EffectiveCI`, `internal/cifilter/changed_files_test.go`).
- Platform Neutrality pull requests select affected steps and Go packages through the hook selector; protected-branch pushes and manual dispatch retain the full three-platform run. Every scoped skip states its reason and the log counts changed paths ([Platform Neutrality scope](../guides/workflow-triggers.md#platform-neutrality-pull-request-scope)).
- A gate that is skipped by classification is distinct from a gate that fails: the filter's decision is itself part of the recorded evidence.
- `praetorctl audit` and the MCP `standards_audit` read the repository's own workflow triggers and report, as `[WARN]` lines naming the file and line, a `push` that runs on every branch, a pull request job that runs its work on a draft, and a job that skips a draft with a job-level condition or through a need held back on the draft. The accepted shape is the hosted gates' (`internal/ghworkflow/hostedgate.go`). Findings warn rather than fail because this rule's failure action rejects nothing. A workflow that must run everywhere is declared in the exceptions list (rule `HISS-18`, its path, a reason and an expiry within the category interval (A: stated, at most 90 days; B: 3 days, C: 10 days, D: 120 days; uncategorized: 90 days)) ([Workflow trigger audit](../guides/workflow-triggers.md)).

### HISS-19: Reuse Before Writing

One behavior has exactly one implementation:

- Before writing a function, config loader, parser or command, the repository is searched for the capability first; an existing implementation is extended or called rather than reimplemented.
- Configuration formats are held to the same rule: a second config system beside an existing loader is the same defect, because the two silently drift apart.
- `praetorctl dedupe scan .` enforces this with function-level clone and utility-sprawl detection, run by `make dedupe` inside `verify-all`. Any clone *or* sprawl finding fails the scan: a finding the verdict does not carry is a finding nobody resolves. In an adopted repository the sprawl check requires one git helper per repository and names that helper; inside the engine it names the audited internal/util helpers.
- The native AST scan reads Go only. Polyglot code clone detection across other languages (C, C++, CUDA, Python, Rust, shell, YAML workflows, and other languages listed in `Unscanned`) is delegated to the pinned clone detector `jscpd` 5.4.2 (`tools/jscpd/package.json`). The detector binary is resolved from `PRAETOR_JSCPD_BIN` when set, else `jscpd` on `PATH`, verified under a 10s context timeout to output exactly `jscpd 5.4.2`; if unavailable or mismatched when foreign files are present, the foreign scan is skipped with the reason recorded in `ForeignSkipped` and the gate fails closed (`Passed: false`, CLI exits nonzero for both human text and `--json`; order `engine-dedupe-jscpd-skip-fails-closed-20261027`). Install pinned jscpd via `npm ci --prefix tools/jscpd --ignore-scripts --no-audit --no-fund` or run `make dedupe`, which provisions pinned jscpd automatically. Cross-root clone detection is supported via `--across DIR` (repeatable), passing all roots to one `jscpd` run and retaining groups spanning multiple roots plus groups in the main root. Languages scanned by `jscpd` enter `ForeignScanned` (reported, not gated); `Not Scanned` (`Unscanned`) keeps naming every language the gate does not judge, and `partial: Go sources only` remains set while `Unscanned` is non-empty. Foreign duplicate groups are reported under `Foreign Duplicate Blocks` (reported, not gated), but do not alter `Passed` or `CleanlinessScore` in this change (gating them requires a baseline); contiguous clone spans within a file are merged so one contiguous span counts as one copy, while distinct repeat occurrences remain separate locations. A repository with no Go source gets no verdict at all (`Unscanned` and `Partial` in `internal/dedupe/dedupe.go`; `TestScanRepo_Positive_PolyglotVerdictIsPartial` in `internal/dedupe/coverage_test.go`). `--json` prints the same report as JSON.
- The clone key renames a function's parameters, receiver, results and locals by first use before hashing (`cloneKey` in `internal/dedupe/dedupe.go`), so a copy whose locals were renamed still matches, while a body that reads a different local or field does not. Bodies under three statements or five printed lines are not hashed.
- `praetorctl dedupe cadence` makes a sweep due after 20 commits, or once 1,000 Go production lines or 10 Go production files have been added since the recorded sweep, whichever comes first (`--threshold`, `--added-lines`, `--added-files`; `CheckCadence` in `internal/dedupe/cadence.go`).
- Duplication that is genuinely unavoidable (such as toolchain-generated boilerplate that cannot be unified) is declared in the top-level `exceptions` list of `.standards.yaml` with rule `HISS-19`. The entry names one repository file by `path` (a glob is refused), a reason, and an expiry within the category interval (A: stated, at most 90 days; B: 3 days, C: 10 days, D: 120 days; uncategorized: 90 days) (`config.ExceptionRuleDedupe` in `internal/config/exceptions.go`):

  ```yaml
  exceptions:
    - rule: "HISS-19"
      path: "api/v1alpha1/zz_generated.deepcopy.go"
      reason: "controller-gen produces near-identical DeepCopyInto methods per type"
      expires: "2026-11-01"
  ```

  The `category` field takes precedence over a case-insensitive `category D` (or A, B, C) reason prefix. Caps run from `entered` when present, otherwise today; an earlier expiry is allowed. A requires its own explicit expiry without a further cap. See the [exception fields](../guides/clang-tidy-coverage.md#exceptions) and `TestValidateExceptionsCategoryCaps`.

  To excuse one clone group in that file, set `fingerprint` to an 8–64 hexadecimal character prefix of the group's reported `hash` (`dedupe scan --json`). Matching is case-insensitive and accepts odd-length prefixes. To limit locations by line, set `lines: "START-END"`: positive, 1-based, inclusive bounds on the location's **start** line. The block need not end inside the range. Both scopes must match when both are present; omitting both preserves whole-path matching. For example, this entry excuses only starts on lines 10 through 16 in groups whose hash starts with `abcdef12`:

  ```yaml
  exceptions:
    - rule: "HISS-19"
      path: "tests/common/mod.rs"
      fingerprint: "abcdef12"
      lines: "10-16"
      reason: "shared fixture setup awaiting extraction"
      expires: "2026-11-01"
  ```

  A clone group is fully excused only when every location matches a live entry, including its scopes. Native groups are reported under `Excepted Duplicate Function Blocks`; foreign groups use `ForeignExcepted` (`foreign_excepted` in JSON). A native group with any unexcused member still fails. An expired scoped entry excuses nothing and annotates only matching locations. An entry is used only when its path and scopes match a duplicate location, even when another location keeps the group reported; a scoped entry that matches nothing remains stale. An unscoped entry keeps the existing whole-path behavior. Distinct scopes may name the same file; identical entries are refused. `TestScanRepo_ScopedExceptions` and `TestLoadManifestDedupeScopeSchemaRoundTrip` cover these contracts. The target must exist and be a regular repository file; generated files bearing the canonical `// Code generated ... DO NOT EDIT.` header and directories skipped by the HISS scanner (such as build and target trees) are excluded from the scan automatically.
- Unexcused duplication is justified in the commit body, not left silent.

### HISS-20: Replayable Enforcement Evidence

A claim of coverage is reproducible, never merely asserted:

- Every enforcement claim in `.config/hiss/coverage.yaml` is replayed against a fixture corpus by `praetorctl hiss coverage --verify`, run inside `verify-all`.
- For adopted repositories that pin the engine binary without carrying the engine's fixture tree, `praetorctl hiss coverage --verify --embedded` replays every claim against the binary's own compiled-in coverage catalog and corpus, proving on the adopter's host that the pinned engine version detects what it claims.
- The check runs in both directions: a claim of enforcement must reproduce each of its positive fixtures, and a claim of absence must leave its gap fixtures undetected.
- A rule that silently *gains* coverage fails the gate exactly as one that silently loses it, so the catalog cannot drift in either direction undetected.
- The coverage catalog may only declare evidence for a rule the HISS rule catalog defines ([`internal/hisscatalog/catalog.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/catalog.go)); an unregistered identifier fails `Catalog.Validate` (`TestValidateRefusesUnregisteredRule`). A coverage entry takes its name from that catalog too, and `praetorctl hiss coverage` prints the catalog's name for every rule. `title:` may be omitted; a title that differs from the catalog's is a `[WARN]` line naming the catalog's current text, never a failure (`TestValidateWarnsOnADriftedTitle`, `TestRunHissCoverage_Negative_DriftedTitleWarnsWithTheCatalogText`), so a catalog retitle does not break a repository that repeats the earlier text. `praetorctl hiss coverage --sync-titles` lists each stale title as a dry run, and `--sync-titles --write` rewrites them in place, changing only each title's value (`SyncTitles` in `internal/hisscoverage/titles.go`; `TestRunHissCoverage_Positive_SyncTitlesFixesElevenDriftedTitles`). The HISS rule catalog also feeds `standards_explain_rule`, the generated wiki's HISS matrix and the invariant table of every adopted `AGENTS.md`, so all of them name the same 21 rules.

### HISS-21: Platform Neutrality

A repository's gates, hooks and generated templates run on Linux, macOS and Windows, or declare the platform they require and skip with a stated reason where it is absent:

- A gate has exactly two acceptable states: running, with its result standing as the platform's result; or skipped, with the reason printed and the alternate coverage source named.
- A check that silently does not run and reports success is prohibited — see the [Platform Neutrality invariant](hiss-21-platform-neutrality.md) for the incident history and enforcement detail.
- Enforced by the Platform Neutrality matrix in CI.
