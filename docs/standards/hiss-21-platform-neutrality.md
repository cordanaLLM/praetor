<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: EUPL-1.2
-->

# HISS-21 — Platform Neutrality

> A repository's gates, hooks and generated templates must run on Linux, macOS and Windows,
> or declare the platform they require and **skip with a stated reason** where it is absent.
> A gate that cannot run is not a passing gate.

## Why this is an invariant and not a checklist

Six portability defects reached `main` across the governed fleet before anything was watching
for the class:

| Defect | What broke | How it surfaced |
| :--- | :--- | :--- |
| [#84](https://github.com/cordanaLLM/praetor/issues/84) | `git commit` impossible on Windows — four stacked layers, each hidden behind the one before it | somebody tried Windows |
| #84 layer 5 | the harness self-tests needed cgo, `os.killpg` and `os.getuid`, so they could never pass on Windows — the gate could not be repaired from the platform it was broken on | same |
| [#81](https://github.com/cordanaLLM/praetor/issues/81) | the race stage hardcoded `go test ./...`, which cannot build a cgo project from a bare worktree | a cgo repository |
| [#78](https://github.com/cordanaLLM/praetor/issues/78) | the same stage failed every non-Go repository at `FAIL ./... [setup failed]` | adopting a TypeScript repository |
| BUG-948 | baseline fingerprints embedded OS path separators, so a baseline recorded on Windows silently stopped suppressing on Linux — and the touched-file rule failed open | a Windows-written baseline |
| BUG-933 | CRLF was predicted to silence the line matchers; measured, it does not — but the property holds by accident | deliberate measurement |

Five were latent for an unknown time. Each was fixed where it surfaced, and nothing prevented
the sixth. Enumerating the known members catches the members; an invariant catches the class.

## The rule has exactly two acceptable states

A gate on a given platform is either:

1. **Running**, and its result is the platform's result; or
2. **Skipped, with the reason printed**, naming what is absent and where the coverage is
   obtained instead.

The third state — a check that silently does not run and reports success — is prohibited.
This is the same defect the repository has now removed from the flavor audit, `plan`, the
sentinel, `dedupe scan` and the coverage claims that made HISS-20 necessary:
**a value nothing measured, presented as one something did.** Universal portability is not
always achievable — `go test -race` genuinely needs a C toolchain — and pretending otherwise
produces worse code than admitting it. What is never acceptable is not knowing.

`race_detector_available()` in `.config/lefthook/scripts/test_hooks.py` is the reference shape:
it returns `(False, reason)` rather than a bare boolean, and every skip built on it names the
reason and states where the coverage is recovered.

## What it forbids, concretely

- A path comparison that embeds the host separator (BUG-948). Normalise with
  `baseline.NormalizePath`; `filepath.ToSlash` is a no-op on Linux and will not catch it.
- A hook or script calling a POSIX-only API without a guard — `os.killpg`, `os.getuid`,
  directory `fsync`, `chmod` semantics.
- A binary path built without a host-appropriate extension (`EXE_SUFFIX` in the `Makefile`).
- A gate stage assuming a language toolchain is present rather than detecting it (#78, #81).
- An assertion comparing LF bytes against output that may be CRLF.
- A template emitted by `adopt` that only builds on the platform that generated it.

## Enforcement in this repository

`.github/workflows/portability.yml` runs the matrix on `ubuntu-26.04`, `macos-26` and
`windows-2025` with `fail-fast: false`, compiling, vetting and testing every package and then
running the harness self-tests through `scripts/portability_selftest.py`.

Every label is an explicit image, never a `-latest` alias. An alias retargets the matrix the day
GitHub promotes the next image, which changes what the platform-neutrality claim was measured
against without changing a line of this repository — the same argument
`internal/config/hierarchy.go` makes for the Darwin runner defaults.

Every leg then runs `node tools/markdownlint/verify.mjs --self-test`. That runner is a
Go-embedded asset which adoption copies into every `docs:seo-portal` repository's `verify-all`,
so it is a generated template under this invariant. The documentation workflows run it on
Linux only. Its Windows-specific path is described in
[documentation governance](../guides/documentation-governance.md#locked-markdown-rules).
`TestPortabilityReplaysMarkdownGateSelfTestOnEveryLeg` in
`internal/forge/workflow_guard_test.go` fails if the step is removed, moved before Node is set
up, or given a condition that could skip it on some legs.

Every leg also runs the figure build check, the commands `make docs-figures-check` and the
managed `make docs-figures` run, called directly rather than through `make`: the locked
`npm ci --prefix tools/figures --ignore-scripts`, the engine's tests and type check,
`node tools/figures/build.mjs check`, `node tools/figures/bundle.mjs --check` and
`node tools/figures/build.mjs sources`. `check` and `sources` are the commands every adopting
repository runs, and they skip with a stated reason where it has no figure. esbuild and
TypeScript arrive as per-platform npm optional dependencies, so the leg proves that the install
works without install scripts on each OS, `check` proves that a rebuild there is byte-identical to
the committed SVG and JSON, and `bundle.mjs --check` proves that the platform's esbuild rebuilds
the committed player in `tools/figures/dist/` byte for byte from the lock, within its size budget.
The hashed files and the player are pinned to LF by the managed block at the end of
`.gitattributes`, which adoption writes in every adopting repository too, so a Windows checkout
reads the same bytes. The
Chromium smoke test needs a built site and runs in `pages.yml` on Linux only
([figures guide](../guides/figures.md#checks)).

Every leg also runs the documentation reference gate (`praetorctl docs references --path=.`,
see [references that stop resolving](../guides/documentation-drift.md#references-that-stop-resolving))
through the binary the job builds, not through its Makefile target. The gate reads
Markdown the Windows image checks out with CRLF, lists the tree through git and reads Go source
by slash-separated repository path, which are the three places a platform difference would make
it answer differently. `TestPortabilityRunsDocumentationReferencesOnEveryLeg` in
`internal/forge/docs_references_guard_test.go` fails if the step is removed, runs before the
build, or is given a condition that could skip it on some legs.

Every leg also runs the Interfig Verify Gate, the commands of `make interfig-verify`:
`python3 -B scripts/test_sync_interfig.py`, `python3 -B scripts/sync_interfig.py verify` and
`node --test 'tools/figures/third_party/interfig/upstream/src/*.test.ts'`. The script resolves every path from
its own location through `pathlib`, so it answers the same from any working directory; the
vendored files are `-text` in `.gitattributes`, so a Windows checkout hashes the same bytes as
`vendor.json` records; and the test glob is quoted, so `node` expands it rather than the shell.
The gate therefore runs, rather than skips, on every leg. The online `update` subcommand is not
a gate, but it resolves `node` and `npm` through `shutil.which`, which finds `npm.cmd` on
Windows where a bare `npm` would not start.

The matrix pins the tools it installs, not only the platforms it runs on. The legs share one
`actions/setup-python` version (3.13) and install yamllint through the interpreter path that
action reports, rather than by name, from the hash-locked `.config/hook-lint/requirements.txt`
(`pip install --require-hashes -r`), the one yamllint pin `make hooks-lint` also installs.
Both pins serve this invariant directly: the job's only output is whether a result differs
across platforms, so a tool free to resolve to a different version on a different leg makes an
upstream release indistinguishable from the portability defect the job exists to report. A
linter that is pinned on one leg and floating on another is not a narrower gate, it is a gate
whose red runs cannot be attributed. The `gosec` the security-scope
suite needs is installed on every leg from `tools/go/go.mod`
(`go install -modfile=tools/go/go.mod github.com/securego/gosec/v2/cmd/gosec`), the one version
source the Makefile and the CI and security workflows also read. That step runs under bash on
every leg: the Windows default shell, PowerShell, splits an unquoted argument that starts with a
dash at its period ([PowerShell#6291](https://github.com/PowerShell/PowerShell/issues/6291)), so
go received the module file name without its `.mod` extension, refused it, and the Windows leg
ran without gosec (#558).
`TestPortabilityPassesNoDottedDashArgumentThroughPowerShell`
(`internal/forge/powershell_argument_guard_test.go`) refuses such an argument in any workflow
step that runs under PowerShell on a Windows leg.

The driver exists because the suites' exit codes are not a sufficient pass condition. It
requires that every suite exited zero **and** that at least `--min-executed` tests actually ran,
printing each skip with its reason. A platform quietly losing coverage then shows up as a number
that moved rather than as an unchanged green check. The floor is a collapse detector, not a
coverage assertion: raise it to a platform's measured figure once a green run reports one, and
never lower it to turn a red run green.

A failing suite must also be readable from the log alone. The driver used to print a failed
suite's last 25 lines, which on a `unittest` run is the trailing summary: the Windows leg said a
suite had failed without naming a single test. It now parses unittest's own block headers, names
every failed and errored case, and prints each case's own block rather than the tail of the run —
a macOS run that failed four cases published one traceback and left the other three to be
attributed by reading the code. Both are bounded, so a noisy suite cannot bury the log, and the
tail is still printed when nothing parses as a block. Its internal Makefile check compares
`Path.as_posix()` against the slash paths it reads, because a host-shaped string compared against
a recorded one is the separator defect this invariant forbids, reached from inside the gate
itself.

A child process that a signal ends is started once more, and the crash is still reported. A
macOS leg once failed `scripts/test_checkpoint_hooks.py` because the Python launcher of one
scope-bridge call died by SIGSEGV with no output, and the same suite then passed on every
rerun (#809). That suite starts every child through `run_child` in
`scripts/portability_selftest.py`, and `ChildSuites` in `scripts/test_portability_selftest.py`
fails when it starts one any other way. The rules are:

- A child that exits is returned at once, whatever its exit code. Only a signal exit is retried.
- After a signal exit, the helper prints a report between two marker lines. The report names
  the test, the child's kind (Python launcher, `sh`, `lefthook` and so on), its resolved path
  and the version it states, and each attempt's signal or exit code with its output.
- When a signal also ends the retry, the test fails with `ChildCrashed` and the same report. A
  crash is never a pass.
- The driver repeats every report under the suite that printed it, whether the suite passed
  or failed, and adds a warning annotation to the run. A pass after a retry therefore shows in
  the log.
- Windows has no signal exits: a crash there leaves an exit code, which is returned like any
  other exit code. The signal cases of `ChildRetry` skip there and say why.

The driver runs the same suites as the `Makefile`'s `hooks-test` target, and
`test_suites_match_the_makefile` in `scripts/test_portability_selftest.py` fails when the two
lists differ, so a suite added to one list only turns `make portability-test` red instead of
leaving the other two platforms untested. Besides the Git hook and checkpoint suites, the list
includes `scripts/test_praetor_hook.py`, the tests of the skew guard that every tracked
agent-client hook row calls (see
[engine skew never blocks a client](../guides/agent-hooks.md#rollout-engine-skew-never-blocks-a-client)).
Its in-process cases run on every leg. Its `TrackedRegistrations` class runs the tracked row
strings through `sh`, so it skips on Windows and states why: the strings are written for a POSIX
shell, and on Windows Codex and AGY run hooks through `cmd.exe` and Gemini CLI through
PowerShell. An `sh` run on the leg's Git Bash would pass without showing what those clients do.
The Windows leg therefore executes fewer tests than the other two.

### The hook toolchain is declared, and the matrix asserts it

Which tools the hooks depend on is declared by the hook policy, not on this page.
`.config/lefthook/scripts/toolchain.py` declares the two programs it resolves from fixed
candidates and proves before use (`RESOLVED`) and the programs it starts by name (`BY_NAME`);
`.config/lefthook/tool-floors.txt` declares the linters and their version floors. Before the
self-tests, every leg runs the Assert Hook Toolchain step,
`.config/lefthook/scripts/toolchain.py --require "$REQUIRED"`, which prints one line per
declared tool and fails the leg when a required one is unusable. The table is rendered from
those declarations and the step's `REQUIRED` list:

<!-- praetor:hook-toolchain:start -->
| Tool | The hook policy requires | Platform Neutrality job |
| :--- | :--- | :--- |
| `python` | the first of `python3`, `python`, `py -3` on `PATH` that states Python 3.10 or newer | asserted on every leg |
| `make` | the first of `make`, `gmake`, `mingw32-make` on `PATH` that states GNU Make | asserted on every leg |
| `git` | found on `PATH` under this name | asserted on every leg |
| `go` | found on `PATH` under this name | asserted on every leg |
| `gofmt` | found on `PATH` under this name | asserted on every leg |
| `gosec` | found on `PATH` under this name | asserted on every leg |
| `govulncheck` | found on `PATH` under this name | reported, not asserted |
| `lefthook` | found on `PATH` under this name | asserted on every leg |
| `semgrep` | found on `PATH` under this name | reported, not asserted |
| `sh` | found on `PATH` under this name | asserted on every leg |
| `actionlint` | version 1.7.12 or newer | reported, not asserted |
| `hadolint` | installed; no version floor | reported, not asserted |
| `shellcheck` | version 0.11.0 or newer | asserted on every leg |
| `yamllint` | version 1.38.0 or newer | asserted on every leg |
<!-- praetor:hook-toolchain:end -->

`test_hook_toolchain_table_matches_the_declarations` in `scripts/test_portability_selftest.py`
fails when the table differs from its rendering; regenerate it with
`PRAETOR_UPDATE_HOOK_TOOLCHAIN_TABLE=1 python3 -B scripts/test_portability_selftest.py`.

The declarations cannot fall behind the scripts. The `DeclaredPrograms` cases in
`.config/lefthook/scripts/test_hooks.py` read every command `checks.py` and `hooks.py` start
and every job of `praetor.yml`, and fail when a program started by name is missing from
`BY_NAME` or the floors file, or is declared and no longer started. `sh` is declared because
every job starts through it. A program added to a command table therefore fails the hook
self-tests until it is declared, and then appears in this table. The cases read those two
files and the policy's jobs; `checkpoint.py`, `sandbox.py` and `common.py` start `gh`,
`docker` and `taskkill` on paths of their own, which the table does not cover.

Asserted means the step checked the tool on that leg:

- `python` and `make` are resolved from their candidates and proven by what they state, the
  way the hooks resolve them
  ([the interpreter the hooks run](../guides/git-hooks.md#the-interpreter-the-hooks-run),
  [the make the hooks run](../guides/git-hooks.md#the-make-the-hooks-run)). No variable
  selects either, so the step sets none; the interpreter `actions/setup-python` installs is
  first on the leg's `PATH`.
- A program started by name is looked up on `PATH`.
- A linter with a floor is asked its `--version` and held to the floor.

A leg that fails here fails as a missing dependency instead of later as a hook rejecting a
commit. `TestPortabilityAssertsTheHookToolchainBeforeTheSelfTests` in
`internal/forge/hook_toolchain_guard_test.go` fails when the step is removed, moved after the
self-tests, guarded by a condition, or stops requiring `python`, `make` or `sh`.

Reported, not asserted means the job does not install the tool, so the matrix does not depend
on it; the step still prints its line, with the message a hook would give. A program the
hooks start by name that is missing refuses the commit or push with the program named
(`run` in `.config/lefthook/scripts/common.py`).

No leg installs `make`; each runs the one its image carries. On the Windows image
(`windows-2025-vs2026`, measured at version 20260925.250) that is a copy of `mingw32-make`
which the image's build script (`Install-Mingw64.ps1` in `actions/runner-images`) writes to
`C:\mingw64\bin\make.exe` and the image's software list does not name, which is why earlier
versions of this page and of the workflow called it absent (#341). The step makes the
dependency a stated one: an image that drops it fails the leg at the assertion, naming the
candidates it tried.

## Required status checks for a matrix job

A matrix job reports one check per leg, so `name: Platform Neutrality (${{ matrix.name }})`
becomes three contexts on the forge: `Platform Neutrality (Linux)`, `(macOS)` and `(Windows)`.

The ruleset generator names each leg the way GitHub reports it rather than emitting the
template (`internal/forge/workflow_matrix.go`). It builds every combination of the matrix axes,
drops the ones an `exclude` entry matches, then applies each `include` entry by the workflow
syntax rules. A name holding `${{ matrix.<variable> }}` is evaluated against each leg and gets
nothing appended. Any other name, or a job without one (reported under its id), gets the leg's
axis values appended in declaration order with empty values left out, as
`Test (ubuntu-latest, 1.22)`; a value an `include` entry merges into an axis combination is not
appended, while an `include` entry that forms its own leg appends all of its values. Legs whose
names coincide share one context, listed once: `Build (${{ matrix.os }})` over the axes `os` and
`go` requires one `Build (<os>)` per runner image
(`TestMatrixContexts_Positive_NamesEveryLegAsGitHubReportsIt`). A YAML alias stands for its
anchored node anywhere in the matrix, as GitHub Actions reads it
(`TestMatrixContexts_Positive_ResolvesAnchorsAndAliases`).

This is not cosmetic. A required status check whose context no run ever reports does not fail a
pull request — it leaves it *expected* forever, so a generator that emitted `${{ matrix.name }}`
verbatim, or a constant name without its leg values, would permanently block the branch it
believed it was protecting. `forge.RequiredStatusContexts` therefore refuses, with the shape
named, every matrix whose contexts the file cannot show: an unresolved expression, a matrix,
axis or `include` list that is itself an expression, a value whose printed form differs from its
spelling (an unquoted `3.10` is the number 3.1), a name that is empty or begins with
whitespace once evaluated (`${{ matrix.prefix }} Build` with an empty prefix), and the YAML merge
key `<<`, which GitHub Actions does not read. None is ever passed through as a literal
(`TestMatrixContexts_Negative_RefusesWhatTheFileCannotShow`). Whitespace an empty value leaves at
the end of an evaluated name is dropped, as GitHub drops it: `Build ${{ matrix.suffix }}` with an
empty suffix reports `Build`, the way tokio's `features exclude ${{ matrix.name }}` reports
`features exclude`.

An advisory leg — one carrying `continue-on-error` — is **excluded** from the required
contexts. The forge reports such a job as successful whether or not it passed, so requiring it
would install a check that can never fail: the same unfalsifiable green this invariant forbids,
reached from the other side. Marking a leg advisory is therefore a real statement about what it
can enforce, not a way to keep a red leg in the required set.

`.github/rulesets/main.json` declares the target protection, and `praetorctl sync --remote`
writes it to GitHub ([writing the ruleset with `sync --remote`](../guides/adoption-verification.md#writing-the-ruleset-labels-and-repository-metadata-to-github-with-sync-remote));
no workflow applies it. Adding a leg to the matrix changes the declared file immediately, and
the branch only afterwards — so **apply the ruleset once the new legs have gone green at least
once**, never in the same step that introduces them.

All three legs of this repository are in the declared ruleset, and every pull request of the
canonical repository waits for them. They became required once all three passed on `main`
(#558 fixed the last red legs; the pre-merge run of #588 was green on Linux, macOS and Windows).
`TestCanonicalRulesetRequiresEveryPlatformNeutralityLeg` in
`internal/forge/required_contexts_in_test.go` fails when the rendered or the committed ruleset
loses a leg.

## Outside the canonical repository the matrix is opt-in, and says so

A private operational fork receives many commits per sync, and its macOS and Windows minutes
are billed at a multiple. There the matrix runs only when the repository variable
`PRAETOR_FORK_PORTABILITY` is `enabled`. The job condition is the repository guard described in
[operational fork synchronization](../guides/operational-sync.md#which-workflows-run-where)
or that variable.

The matrix also skips, in every repository, a pull request the Renovate app (`renovate[bot]`)
opened from a branch that starts with `renovate/`. Such a pull request is taken over on a
signed-off branch whose own pull request runs the three legs
([Renovate pull requests](../guides/contributing.md#renovate-pull-requests)), so the condition
leads with
`!(startsWith(github.head_ref, 'renovate/') && github.event.pull_request.user.login == 'renovate[bot]') && (...)`.
A person's pull request from a branch named `renovate/...` runs the legs.

A job skipped by its condition reports nothing, which is the silent skip this invariant forbids.
The workflow therefore carries a second job with the exact negation of that condition. It runs
on one Linux runner, writes a notice and a step summary naming the reason, the Renovate pull
request or the repository and the variable, and states that no platform was verified. Exactly one of the
two jobs runs for any repository and branch (`TestPortabilityFollowsTheRepositoryVariable` in
`internal/forge/workflow_guard_test.go`).

A job condition normally removes a job from the required contexts, because its check may never
report. A condition that is only a disjunction containing the repository guard for the
repository's own `.standards.yaml` identity is the exception: it is always true there, so the
three legs stay required in the canonical repository. A leading Renovate term does not
change that: the rest of the condition decides (`withoutRenovateBranchSkip` in
`internal/forge/workflow_guard.go`). The skip job's condition is a negated group, which never
counts, so it is required nowhere: a check that says no platform was verified must not satisfy
a ruleset.

`sync --remote` never adds to a fork's GitHub ruleset a check its runs will not report:

| Where | Committed ruleset | Checks `sync --remote` adds on GitHub |
| :--- | :--- | :--- |
| canonical repository | the three legs | the three legs |
| operational fork (`repository.source` names the canonical repository) | the three legs, so the fork's audit accepts the file it carries unchanged (#255) | no leg; a leg an earlier sync added stays required until it is removed by hand (below) |
| adopter | no leg: `portability.yml` is not emitted to adopters | no leg |

A fork's legs are skipped before their matrix expands, so GitHub never reports
`Platform Neutrality (Linux)` there (actions/runner#952), and requiring it would leave every
pull request waiting. `sync --remote` therefore adds only the checks whose jobs report in the
repository it writes to (`forge.RequiredStatusContextsIn`), reads the live ruleset back, and
names each check it left off (`TestSync_Remote_RequiresOnlyChecksThatReportInTheRepository` in
`cmd/standardsctl/sync_remote_guard_test.go`):

- `[INFO] Not required on GitHub for <owner>/<name> ...`: the live ruleset does not require it.
- `[WARN] Still required on GitHub for <owner>/<name> ...`: the live ruleset still requires it,
  and unless the repository enables the job (for example with `PRAETOR_FORK_PORTABILITY`), every
  pull request waits for it
  (`TestSync_Remote_WarnsAboutLeftOffChecksTheLiveRulesetStillRequires`).

The warning is the migration path for forks that ran `sync --remote` before this check existed.
Since #258 a fork renders the legs, and those syncs wrote them to GitHub. The merge never removes
a live required check (`mergeRuleset` in `internal/forge/ruleset_merge.go`), so a later sync
leaves them in place. Remove the checks the warning names from the `praetor-main-protection`
ruleset in the fork's repository settings (Settings, Rules, Rulesets). A fork that sets
`PRAETOR_FORK_PORTABILITY` and wants the legs required adds them to that ruleset the same way,
and `sync --remote` keeps them. The warning still names them, because the guard skips the jobs
by default; that fork keeps them regardless.

## Templating: the matrix shape is per language

A single three-OS matrix is correct for Go and wrong for almost everything else. Each archetype
gets the shape its toolchain actually needs, and the shapes differ in *what can run*, not only in
which commands are typed.

| Archetype | Runners | Per-leg work | Legs that cannot run their tests |
| :--- | :--- | :--- | :--- |
| `framework`, `library-client` (Go) | ubuntu, macos, windows | `go build ./...`, `go vet ./...`, `go test ./...` | race legs where cgo is unavailable — skip with reason, recovered on the Linux gate |
| `app-service` (Python) | ubuntu, macos, windows | install, `pytest` | native wheels absent for a platform — skip naming the wheel |
| `app-service`, `web-package` (JS/TS) | ubuntu, macos, windows | install, build, test | none expected; path-separator assertions are the usual defect |
| `container-image`, `gitops-infra` | ubuntu | build and scan | macOS and Windows runners have no Linux container daemon — declare the single runner, do not pretend to a matrix |
| `native-gpu-systems` (C/C++/CUDA/HIP/SYCL/Metal/Vulkan) | see below | compile, link, and run what the runner can | every GPU backend — see below |
| `os-image`, `planning-artifacts`, `org-health`, `pages-site` | ubuntu | lint and render | not applicable; no compiled artifact |

### GPU archetypes need compile-only legs, and must say so

`native-gpu-systems` is the case that breaks a naive matrix: no hosted runner has a GPU, so a
CUDA, HIP, SYCL, Metal or Vulkan backend can be **compiled and linked** but not **executed**.
Dropping the test step silently is precisely the third state this invariant prohibits.

The shape to copy is a downstream adopter's build matrix, which solved this first across
eighteen cells and states the omission in the workflow itself:

> Build-only legs — no GPU on the `windows-2025` runner, so the test step is intentionally
> omitted. The point is verifying that the CUDA / SYCL backend code compiles & links on the
> MSVC toolchain, closing the platform-coverage hole left by the Linux-only GPU matrix entries.

Three further patterns from that workflow generalise:

- **`continue-on-error: ${{ matrix.experimental == true }}`** — an advisory leg is marked as
  advisory in the matrix data, so a leg that cannot block is visibly distinguished from one that
  can. It is not quietly excluded from the required set.
- **`fail-fast: false`** — which platforms are broken is the job's entire output; cancelling the
  survivors discards it.
- **Cross-compiled legs report `SKIP`, not `PASS`** — meson marks cross-built tests skipped
  regardless of whether the host could have run them, and the workflow documents that rather
  than reading the skip as success.

A GPU archetype's matrix therefore carries three kinds of leg, and the kind is data, not a
comment: legs that build *and* test, legs that build only because no runner has the hardware,
and legs that are advisory because their toolchain is not pinned. A backend with no leg of any
kind is an uncovered backend.

## The matrix caches per platform, and the cache step is itself platform-neutral

The matrix restores its Go caches through `.github/actions/go-cache` like every other job, and
two details of that action exist because this gate runs on three runners.

The cache keys carry `runner.os` and `runner.arch`, so the Windows leg cannot restore objects a
Linux leg compiled. Without that the legs would silently share one entry and a green Windows leg
would prove nothing about Windows.

The action resolves its paths by asking the toolchain rather than naming them:

```yaml
echo "modules=$(go env GOMODCACHE)" >> "$GITHUB_OUTPUT"
echo "build=$(go env GOCACHE)" >> "$GITHUB_OUTPUT"
```

A hardcoded `~/.cache/go-build` is correct on Linux, wrong on Windows, and -- because it is a
path that merely fails to exist rather than an error -- would produce a cache step that reports
success while caching nothing. That is the shape HISS-21 exists to catch: a gate that cannot do
its job on a platform but does not say so. `go env` answers per host, so the step is decided by
the toolchain rather than by the author's machine.

## Relationship to the other invariants

HISS-21 constrains where a gate runs; [HISS-20](https://github.com/cordanaLLM/praetor/issues/88)
constrains whether its enforcement is replayable. They meet at the fixture corpus: extending it
with an OS axis, so a fixture asserting cross-platform behaviour is replayed on each runner
rather than assumed, is the strongest available form of both. That was already the plan for
BUG-932 before any of the defects above surfaced.

A new invariant earns its row in the directives table when it has a gate, not when it has a
rationale. HISS-21's gate is the workflow above.

## What the macOS and Windows legs cover after #135

The gate itself was red on `main` for nine consecutive runs and was not a required check
(#135), which is the same failure the rest of this document describes, seen from the outside:
a signal nobody could trust was present but carried no information.

**macOS.** Every failure traced to one root cause repeated across five packages
(`operationalsync`, `repairrun`, `dogfood`, `harvester`, `util`): code that walked a path's
full ancestry from the filesystem root — or compared it against `filepath.EvalSymlinks` of
the whole string — and rejected the first symlink found. macOS ships `/var` and `/tmp` as
symlinks to `/private/var` and `/private/tmp`, so anything under the platform's own `TMPDIR`,
including every `t.TempDir()` fixture these packages' own tests use, failed before the code
under test ever ran. This is `contextopt.openDirectory`'s exact shape from #109/#151, present
independently in four other packages; the fix in each case either narrows the check to the
confinement boundary the caller actually names (leaf and immediate parent, or every component
below an explicit root) or delegates to `contextopt.OpenDirectoryIn` / `EnsureDirectory`
directly. A separate defect compared `git rev-parse --show-toplevel`'s output against an
unresolved path by string; it now compares by `os.SameFile` (device and inode), which does not
care which symlink either spelling was reached through. All packages that failed on the macOS
leg pass locally as of this fix; the leg itself still needs a green CI run to confirm it on the
real runner.

**Windows.** One cause accounted for the largest single block of failures: `.standards.lock`
pins a `sha256` of each archetype YAML computed from the LF blobs committed here, and
`windows-latest`'s default `core.autocrlf=true` checked those files out as CRLF, changing the
hash before `internal/config/lockdigest.go` ever compared it. `.gitattributes` now pins those
files to `eol=lf`, the same fix already applied to `*.go` for the identical reason. The
remaining fixes address independent, narrower causes: a Windows-only executable resolution that
returned the PATHEXT extension's configured case rather than the on-disk name (functionally
correct, string-unequal); a test payload that spliced a Windows path into hand-written JSON
without escaping its backslashes; `os.FileInfo.Mode()` reporting no POSIX permission bits on
Windows, which two independent checks (a test assertion and a production install-permission
guard) treated as "permission denied" rather than "not applicable here"; `git apply` following
the runner's ambient `core.autocrlf` when replaying an adaptation patch; and a test that
labelled a real, host-produced temporary path with a hardcoded `GOOS: "linux"`. Each is fixed
where found and confirmed only on Linux, since this work has no Windows host to verify against;
the Windows leg carried substantially more failing packages than macOS did, and this pass does
not claim to have closed all of them — see the PR for what remains open under #132.

**Third pass.** What remained after the two passes above was, on both legs, one question asked
in several places: *is this the same directory?* A directory has many spellings — an ancestor
symlink on macOS, an 8.3 short name and a drive-letter case on Windows — and each site compared
spellings as strings. `internal/harvester` published `GitCommonDir` unresolved, so a checkout and
its linked worktree stopped matching; `internal/state` hashed the repository path unresolved, so
a hook and the tool it invokes derived different state bindings and every commit and push was
refused as stale. Both now go through `internal/util.ResolveExistingPath`, and the two
"same directory?" predicates that had grown in `internal/harvester` and `internal/adopt` collapse
onto one `internal/util.SameDirectory` that answers by inode rather than by string (HISS-19).
The condition is forced portably in tests by an aliased ancestor, so Linux runs the same case.

The remaining failures were fixtures asserting what a platform cannot express, and each is now
guarded by the predicate that already existed for it rather than by a new one: a rooted path
without a volume is not absolute on Windows (the volume-aware fixture helpers from the second
pass, applied to the call sites its sweep missed); `os.FileInfo.Mode()` carries no POSIX
permission on NTFS (`util.ModeIsProtection`); repair execution needs Linux file isolation
(`requireRepairIsolation`, which one case in `internal/repairrun` never asked for, so a nil
result panicked and aborted the package's whole test binary). One fixture could not exist at all
on a case-insensitive filesystem and was rebuilt so that it need not. `common.py` also learned to
reap a bounded command's descendants on the platform with no process group.

**After the legs became required (#624).** The first Windows run on `main` after
Platform Neutrality became a required check failed two tests that pass on Linux and macOS.
`internal/paperclip/testdata` holds harness files an earlier release wrote, and
`TestPatchPlatform_Positive_ReleasedHarnessChangesOneLine` compares them line by line; under
`text=auto` the Windows checkout added a carriage return to every line, so `.gitattributes` now
pins that directory to `eol=lf`. The owner-overlay identity suite runs a nested `go test` over
config, forge and adopt, and adopt alone takes close to three minutes on `windows-latest`; its
deadline went from four to eight minutes, and `portability.yml` passes `go test -timeout 30m`
because the package was already near the 10-minute default per package there. The next Windows
run passed every test and was still cancelled by the job's own 45-minute limit during the figure
build check (go test took 27 minutes, the harness self-tests 13), so the job limit went to 75
minutes. By October 2026 the Windows leg took 55 to 80 minutes (go test 41 to 63, the harness
self-tests 12 to 13), and a slow runner was cancelled at 75 minutes in its last step with every
step green, so the job limit is now 120 minutes (#729).

**A deadline from the outer timeout (#831).** The eight minutes then ran out on macOS, where
adopt alone took 341 to 557 s, so the identity suite timed out at random. A fixed count only
moves that cliff, so the nested run's deadline now comes from the outer `go test -timeout`
(`t.Deadline()`) less a one-minute margin for stopping the nested run and reporting it, and
`go test -timeout 0` keeps an explicit 30-minute bound. The nested `go test` passes that bound
on as its own `-timeout`, in whole seconds at or above what remains: without it every nested
package binary keeps the 10-minute default, the same fixed count one slow macOS runner away.
Each binary starts after that value is computed, so the deadline, not a binary's own alarm,
stops the run. A window under 15 seconds fails at once, and a run stopped
by its deadline, or by a binary's `panic: test timed out`, fails naming both deadlines and
`go test -timeout`, so a slow runner does not read as an identity regression. The nested run
keeps all of adopt: a name or file selection would be the one-at-a-time list #263 found
incomplete. `deriveSuiteDeadline` and `nestedGoTestArgs` in
`internal/operationalsync/identity_suite_test.go` derive the deadline and the nested
`-timeout`; `internal/operationalsync/identity_suite_deadline_test.go` tests both at their
boundaries and the timeout-panic wording, and its
`TestOverlaySuiteRefusesAPlantedIdentityRegression` proves the guard still fails a test that
asserts the canonical owner, and that the derived `-timeout` reaches the nested package binary.
