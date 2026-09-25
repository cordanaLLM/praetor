# Adoption verification commands

Adoption prepares governance and declares project commands. It does not execute a
project's scripts or certify that the application builds or passes tests. The
optional `verification` object in the adoption report carries the selected argv,
runtimes, status, and any reasons requiring review.

| Status | Meaning |
| --- | --- |
| `declared-unverified` | Commands follow discovered project metadata. Execution and tool availability still need verification. |
| `unavailable` | A required build/test command is missing or ambiguous. Generated recipes fail explicitly. |
| `preserved-unverified` | Existing custom Makefile ownership is preserved. Review and exercise its `verify-all` contract. |

## README governance is adoption evidence, not certification

When `README.md` exists, adoption owns only the region between
`<!-- praetor:readme-governance:start -->` and
`<!-- praetor:readme-governance:end -->`. It refreshes that region from the
recorded debt baseline and preserves content outside it. The badge says
**HISS Adopted**, never **HISS Compliant**: a baseline records existing scanner
debt, while only a commit-bound signed Exit-0 receipt proves that a particular
verification run passed.

`praetorctl audit` renders the same expected block in memory and fails when the
README block is missing, malformed, duplicated, or stale. Re-run
`praetorctl adopt` to migrate the historical unmarked HISS-16 badge and
governance table. A repository that deliberately keeps README ownership can set
`adoption.decline: [readme]` in `.standards.yaml`; audit reports that explicit
decision instead of silently treating an unchecked README as current.

Adoption refuses unbalanced or duplicate live markers before writing
`README.md`. Marker examples inside fenced code are inert. Human-authored and
custom badges outside the block remain untouched, and a custom HISS badge
prevents the renderer from adding a second badge.

### Branch protection rulesets and adoption decline

Repositories adopting Praetor that manage branch protection externally or decline
generated GitHub rulesets can record `adoption.decline: [branch-ruleset]` in
`.standards.yaml` (accepted by `standardsctl adopt`). When this decline is recorded,
`standardsctl adopt` omits `.github/rulesets/main.json`.

Both `standardsctl audit` and the MCP server's `standards_audit` tool share one
authority path (`adopt.AuditBranchProtection`) to verify branch protection:

- If `branch-ruleset` is explicitly and validly declined in `adoption.decline`,
  audit reports `[PASS] Branch protection ruleset declined by adoption.decline.`
  and passes cleanly without requiring `.github/rulesets/main.json`.
- If `branch-ruleset` is not declined and active policy requires branch protection
  (such as linear history or signed commits), audit fails closed if
  `.github/rulesets/main.json` is missing or invalid.
- An unknown decline item, malformed decline entry, or unreadable `.standards.yaml`
  fails closed, ensuring invalid configuration cannot produce a false pass.


### Stages that do not apply are skipped, not failed

`praetorctl gate run` reports a stage it cannot meaningfully run as skipped, with the reason, rather
than failing the repository for it:

- The module prefetch, Go security scanners and race-detector stages skip where there is no
  `go.mod`. Without this a TypeScript or Python repository failed its own pre-push gate at
  `FAIL ./... [setup failed]`, which reads as a broken repository rather than an inapplicable stage.
- Flavor conformance reports **not applicable** where the repository's declared profile has no
  flavor implementing it -- an OS image forge is not a Go service and should not be measured as one.
- The race-detector stage skips where the race detector cannot build, naming what is missing:

  ```text
  5. [PASS] Race-Detector Tests  (62ms)
     Reason: race detector unavailable (the C compiler "gcc" named by go env is not on PATH):
             race-detector tests skipped; CI runs this leg on Linux with cgo
  ```

  The detector needs cgo and a host C toolchain. Checking `CGO_ENABLED` alone is not enough, and
  the difference is the common case rather than an edge: a stock Windows Go reports
  `CGO_ENABLED=1` and `CC=gcc` while no gcc is installed, so the toolchain claims cgo and every
  race build still fails. The compiler the toolchain names is therefore resolved, not assumed.

  Without this the stage did not report a missing compiler -- it reported `# runtime/cgo` followed
  by every package failing to build, which reads as a repository whose whole tree is broken. On a
  workstation without a C toolchain that was every push, including a push fixing support for that
  platform, so the gate could not be repaired from the platform it was broken on. Set
  `CGO_ENABLED=0` to skip the stage deliberately; install a C toolchain to run it.

A skipped stage prints its reason. That distinction matters: a skipped stage that reads as a pass is
how a gate comes to certify what it never examined.

### A failing flavor stage names the files that cost the score

The flavor stage scores required templates and settings, and a setting counts only where the file
is present **and** parses as the shape that setting declares
([onboarding guide](onboarding.md#what-the-flavor-score-measures)). A repository can
therefore fail this stage with every template present: two settings that exist but do not parse put
a go-library repository at 7 of 9 required items, 77.8%, below the 80% bar.

The failure names them, because at that point the push is already blocked:

```text
  4. [FAIL] Flavor Conformance        (3ms)
     Reason: flavor audit failed (score: 77.8%, 0 missing templates, missing or invalid settings: lefthook.yml, .github/rulesets/main.json)
```

Without the file list the whole report was `score: 77.8%, 0 missing templates`, which tells an
operator that something is wrong and nothing about which file to open. `praetorctl flavor audit .`
prints the same files with their names and descriptions under **Missing or Invalid Settings**.

### The race stage's bound, and what firing it means

The race-detector stage is bounded, because an unbounded stage is how a gate hangs instead of
failing (HISS-02). The default is 180 seconds, and `PRAETOR_TEST_STAGE_TIMEOUT` raises it up to a
30-minute ceiling.

The override is clamped rather than trusted. An empty, unparseable, zero, negative or
over-ceiling value falls back to the default or the ceiling and **says which**, so a typo cannot
quietly remove the bound or shrink it to nothing. A raised bound is printed as the stage's reason,
so it appears in the receipt: an override that changed the gate's strictness without showing up in
its output would be an invisible difference between what one operator verified and what every
reviewer reads.

When the bound fires, the stage says so rather than reporting a test failure. These are different
outcomes and used to print identically:

```
5. [FAIL] Race-Detector Tests  (3m0.024s)
   Reason: tests failed in .standards/worktrees/gate-2856-...: ok github.com/... 1.437s
```

That message is the tail of a *successful* run that was cut off, so the first reading is always
"my change broke the tests" — and the suite had not broken at all. The stage now names the bound,
the variable that raises it and the ceiling, so the reader is pointed at the real cause. A suite
that genuinely fails inside the bound still reports as a test failure; the fix must not trade one
wrong diagnosis for another.

The isolated worktree is created under the same bound, so the bound can fire one step earlier
than the tests. On a Windows host with the bound set to 50 ms it did: `git worktree add` outlasted
it, the kill surfaced as a bare `exit status 1`, and the stage reported a repository whose
worktree could not be created — the same misattribution, pointing at the checkout instead of at
the suite. Creation now reports the bound when the deadline is what stopped it, and keeps the
"could not be created" text for every other cause.

Governance profile names no longer select Go or Meson commands. A shared plan
renders both newly generated Makefiles and AGENTS.md. Discovery recognizes:

- Go modules: build and race tests; Cargo projects: locked build and tests.
- Root npm scripts: a declared build, optional check, and a nonempty test script.
  Another declared package manager requires an explicit project contract.
- C# project files: locked restore and Release build with warnings as errors;
  explicit unconditional test projects receive `dotnet test`. Conditional,
  contradictory, or disabled test markers cannot establish a test gate.
- Python: explicit pytest configuration, or tests under `tests/` with an exact
  Python 3.14+ version pin. Unittest commands check the interpreter version before
  discovery. Python 3.14 fails when discovery finds no tests. A separate build
  command remains necessary; a Python-only project without one needs a custom
  contract rather than a generated no-op build.

Mixed projects retain all detected gates; npm build precedes .NET builds for
frontend resources. A solution marker without projects, or Meson/CMake markers
without a selected configured build directory, remains unavailable. Discovery is
bounded to 4,096 entries, 32 directory levels (to cover nested public `src`/test
project layouts), 128 metadata files, 64 KiB per metadata file and 2 MiB in
aggregate. Generated dependency/build trees are omitted.
Exceeding a bound is an error, never a truncated successful plan. Selected metadata
uses bounded reads that refuse symlinks. Command paths with line breaks are
rejected; shell arguments are quoted and Make dollar signs escaped.

The .NET runner selection follows `global.json`: VSTest uses a positional project,
while Microsoft.Testing.Platform uses `--project`. MTP requires a suitable .NET 10+
SDK and compatible test projects. Selection is still unverified until the declared
commands run. See the [VSTest CLI reference](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-test-vstest),
[MTP CLI reference](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-test-mtp),
and [Python 3.14 unittest behavior](https://docs.python.org/3.14/library/unittest.html).

## Migration and activation limits

Re-run adoption to replace byte-exact historical Praetor Makefiles, including the
old echo-only `verify-all` stub. Custom or edited Makefiles are preserved even with
`--force`; includes, generated target names and pattern rules are treated as
ambiguous ownership. A missing target can be appended to a simple existing
Makefile without replacing its recipes. Existing AGENTS.md is preserved by default
with a command-synchronization warning. Review the declared plan; `--force` can
refresh a recognized generated harness boundary while retaining project content.
Malformed or oversized command metadata now fails before any adoption writes.

Run the resulting commands under the intended toolchain and retain actual results
before claiming application verification. Public dogfood governance verification,
HISS language coverage, native tests, and deployment smoke tests remain distinct
pieces of evidence. Repository enrollment and external application execution are
not activated by this plan.

This correction does not repair language-specific devcontainer or editor setup:
BUG-651 and BUG-438 remain open in the private local ledger. Archetype catalogs
and these downstream consumers still require separate reconciliation; a C# command
plan does not imply a configured .NET development container or complete C# scanning.

## Canonical context preparation

Adoption preserves repository-specific `AGENTS.md` instructions when composing
the Praetor harness. The combined canonical content must fit every generated
client projection's 300-line budget. Context compilation is preflighted before
writing the composed canonical file, so a budget failure leaves canonical and
vendor context files unchanged. Other adoption stages may already have run;
this check does not make the entire adoption operation transactional.

An oversized composition remains an explicit preparation failure. Review and
condense the repository's instructions before retrying; adoption does not silently
discard them or raise the limit. A dry-run reports the same compilation failure
without writing context files.
