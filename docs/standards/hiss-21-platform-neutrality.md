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

`.github/workflows/portability.yml` runs the matrix on `ubuntu-latest`, `macos-latest` and
`windows-latest` with `fail-fast: false`, compiling, vetting and testing every package and then
running the harness self-tests through `scripts/portability_selftest.py`.

The driver exists because the suites' exit codes are not a sufficient pass condition. It
requires that every suite exited zero **and** that at least `--min-executed` tests actually ran,
printing each skip with its reason. A platform quietly losing coverage then shows up as a number
that moved rather than as an unchanged green check. The floor is a collapse detector, not a
coverage assertion: raise it to a platform's measured figure once a green run reports one, and
never lower it to turn a red run green.

A failing suite must also be readable from the log alone. The driver used to print a failed
suite's last 25 lines, which on a `unittest` run is the trailing summary: the Windows leg said a
suite had failed without naming a single test. It now parses unittest's own block headers and
names every failed and errored case, so the log states which assertions broke on which platform
rather than that something did. Its internal Makefile check compares `Path.as_posix()` against
the slash paths it reads, because a host-shaped string compared against a recorded one is the
separator defect this invariant forbids, reached from inside the gate itself.

## Required status checks for a matrix job

A matrix job reports one check per leg, so `name: Platform Neutrality (${{ matrix.name }})`
becomes three contexts on the forge: `Platform Neutrality (Linux)`, `(macOS)` and `(Windows)`.

The ruleset generator expands the name against `strategy.matrix.include` rather than emitting
the template. This is not cosmetic. A required status check whose context no run ever reports
does not fail a pull request — it leaves it *expected* forever, so a generator that emitted
`${{ matrix.name }}` verbatim would permanently block the branch it believed it was protecting.
An unresolved expression is therefore an error in `forge.RequiredStatusContexts`, never a
literal passed through.

An advisory leg — one carrying `continue-on-error` — is **excluded** from the required
contexts. The forge reports such a job as successful whether or not it passed, so requiring it
would install a check that can never fail: the same unfalsifiable green this invariant forbids,
reached from the other side. Marking a leg advisory is therefore a real statement about what it
can enforce, not a way to keep a red leg in the required set.

`.github/rulesets/main.json` declares the target protection; it is applied to the branch with
`gh api`, not by any workflow. Adding a leg to the matrix changes the declared file
immediately, and the branch only afterwards — so **apply the ruleset once the new legs have
gone green at least once**, never in the same step that introduces them.

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

The shape to copy is `VMAFx/vmafx`'s `libvmaf-build-matrix.yml`, which solved this first across
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

Per rule 12 of `AGENTS.md`, a new invariant earns its row in the directives table when it has a
gate, not when it has a rationale. HISS-21's gate is the workflow above.
