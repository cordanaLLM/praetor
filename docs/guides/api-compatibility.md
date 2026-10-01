# Go API compatibility gate

The `api:public-contract` facet, part of the default facet set, adds a hosted check to every
repository whose `go.mod` git tracks. The check compares the exported API of every Go module in
the repository with a base revision. A removed or
changed exported identifier in any module, a nested one included, reaches review as a warning
before the repository's first v1 release and as a failed required check from v1 on. The gate is
one Go program, [`tools/apicompat/gate/main.go`](https://github.com/cordanaLLM/praetor/blob/main/tools/apicompat/gate/main.go),
run by the workflow `.github/workflows/praetor-api.yml` under the status context
**Go API Compatibility**.

## What adoption writes

While the facet is declared and git tracks a `go.mod`, `praetorctl adopt` writes two locked files
and `praetorctl audit` compares them byte for byte, one consistent line-ending style allowed:

| File | Purpose |
| :--- | :--- |
| `tools/apicompat/gate/main.go` | The gate program. Its `//go:build apicompatgate` constraint keeps it out of every `./...` pattern, so the repository's own build, tests, linters and API never include it; `go run` builds it because the command names the file. |
| `.github/workflows/praetor-api.yml` | One job, `Go API Compatibility`, on every pull request and push. It has no condition, so the branch ruleset adoption renders requires it. |

Adoption refuses, even with `--force`, to overwrite a file the repository already had at either
path before the gate was adopted. `adoption.decline` cannot name the `api-compatibility-gate`
step: remove the facet to opt out, which removes both files and, under `--force`, the required
context from the ruleset (`internal/adopt/api_compat.go`,
`TestAdoptionAPICompatibilityGateFacetTransitionConverges` and
`TestAdoptionAPICompatibilityGateRefusals` in `internal/adopt/api_compat_test.go`). The
Renovate ignore rule and the Prettier inventory that adoption maintains list the gate's files
beside the documentation gate's. The actionlint runner labels adoption declares are read from
every workflow it writes (`adoptedWorkflowFiles` in `internal/adopt/harness_ci.go`), so the
runner `praetor-api.yml` runs on is declared exactly while adoption emits the gate
(`TestActionlintLabelsFollowTheAPICompatibilityGate` in `internal/adopt/actionlint_test.go`).

## Repositories without Go

The gate compares Go modules only, so adoption writes it only where git tracks a `go.mod`, at the
root or nested, outside the directories a `./...` pattern skips. That is the module discovery the
gate itself uses (`TrackedModuleFiles` in `tools/apicompat/modules.go`, kept equal to the gate by
`TestGate_Boundary_AdoptionDiscoversTheGatesModules`). The index decides, so a `go.mod` counts once
it is staged or committed, and an untracked one does not.

A repository that declares the facet without such a `go.mod`, a Python or Rust project for
example, gets nothing: no gate program, no workflow, and no **Go API Compatibility** context in
the ruleset adoption renders. The adoption report says no API compatibility checker runs for the
repository's languages, and `praetorctl audit` prints the same as an `[INFO]` line, neither a pass
nor a failure. The facet stays declared and keeps its other policy, including the `Migration:`
footer check.

A repository moves between the two states on the next `praetorctl adopt`. Tracking its first
`go.mod` adds the gate and the required context. Untracking its last one retires them; while the
ruleset still requires the context, an unforced run stops and names the missing `go.mod`, and
`--force` removes the gate and the requirement, as removing the facet does
(`TestAdoptionAPICompatibilityGateFollowsTrackedGoModules` in `internal/adopt/api_compat_test.go`).
Until that run, audit fails on the gate files or the required context left behind and names the
adoption run that retires them.

## Run it locally

From a clean checkout, with `git` and `go` on `PATH`:

```bash
go run tools/apicompat/gate/main.go -base=origin/main
```

The checker checks each revision out in the working tree and restores `HEAD` afterwards, and it
refuses a dirty tree, so commit or stash first. Without `-checker`, the gate installs the pinned
checker into a temporary directory from the Go module proxy; pass `-checker=<path>` to use a
binary you built.

| Flag | Default | Meaning |
| :--- | :--- | :--- |
| `-base` | empty | Base revision. Empty selects the newest root release tag merged into `HEAD`. |
| `-policy` | `auto` | `auto` warns before v1 and rejects from v1; `warn` and `reject` fix the policy. |
| `-checker` | empty | A go-apidiff binary; empty installs the pinned one. |
| `-repo` | `.` | Any directory inside the repository. |

## What it compares

1. **Modules.** Every `go.mod` git tracks at the base and at `HEAD`, outside the directories a
   `./...` pattern skips (`testdata`, `vendor`, and names starting with `.` or `_`). A module path
   with an `internal` element is listed as not public and not compared. More than 512 modules, or
   a revision listing more than 2,097,152 paths, fails the gate rather than comparing a part.
2. **Pairs.** A module present at both revisions, matched by directory, is compared. One only at
   `HEAD` is listed as added. One only at the base is a removed module, every package of it gone,
   and counts as an incompatible change.
3. **New major versions.** A module whose path at `HEAD` is a later major version of its path at
   the base, the same path with a higher `/vN` suffix (`.vN` for `gopkg.in`), is listed as a new
   major version and not compared. Go publishes a new major version under a new module path, so
   importers of the earlier path keep its releases and lose nothing: an in-place bump to
   `example.com/w/v2` passes from v1 on. Any other change of the module path is compared, and the
   checker reads every package of the earlier path as removed (`isNewMajor`;
   `TestGate_Positive_InPlaceMajorVersionIsANewModule`, `TestGate_Boundary_MajorVersionSuffixes`).
4. **Comparison.** For each compared module the gate runs `go list -export ./...` at `HEAD`, so a
   module whose packages do not build fails instead of reading as empty, and then
   `go-apidiff <base> <HEAD> --repo-path=<root>` from that module's directory. go-apidiff loads
   `./...` from its working directory, which is what keeps a nested module from being skipped by
   a run at the root.
5. **Root vendor directory.** go-apidiff loads every module with `-mod=vendor` when the
   repository root holds a `vendor` directory, and with `-mod=readonly` otherwise (`getPackages`
   in its `pkg/diff/run.go`). The go command vendors a nested module from that module's own
   directory, so a nested module with requirements would fail with `inconsistent vendoring`.
   While the base or `HEAD` tracks a root `vendor` entry, the gate compares nested modules in a
   temporary clone whose two commits drop that entry, and removes the clone afterwards; the root
   module is still compared in the checkout (`vendorFreeClone`;
   `TestGate_Positive_ComparesNestedModulesWithoutTheRootVendorDirectory`,
   `TestGate_Boundary_VendorFreeCloneScope`).

The base is `-base` when given; the workflow passes the pull request's base commit. Otherwise it
is the newest root release tag, `vMAJOR.MINOR.PATCH` with no directory prefix and no pre-release
suffix, merged into `HEAD`. A nested module's tag (`lib/v1.2.3`) is never selected. With neither,
there is no published API, and the gate passes saying so; a push builds on that.

## Exit status and policy

| Status | Meaning |
| :--- | :--- |
| `0` | Every compared module is compatible, nothing was there to compare, or incompatible changes were reported under the warn policy. |
| `1` | Incompatible changes under the reject policy. |
| `2` | The comparison did not run: a git or discovery error, a malformed `go.mod`, a module that does not build at `HEAD`, a checker that cannot be installed, misses the canary, exits with a status other than 0 or 1, or prints an error. |

Compatibility policy never relaxes execution policy: status 2 holds under `-policy=warn` too.
`go run` itself exits 1 for any nonzero status of the program, so a workflow log shows the
program's status in the `exit status N` line. On GitHub Actions the verdict is printed as an
`::error::` or `::warning::` workflow command, which renders as an annotation.

## The pinned checker

`checkerModule` in `tools/apicompat/gate/main.go` pins
`github.com/joelanford/go-apidiff@v0.8.4-0.20260910211158-c3e0953fa2fd`, commit `c3e0953fa2fd` of
its main branch. The newest release, v0.8.3, builds against `golang.org/x/tools` v0.33.0, which
cannot decode the export data Go 1.27 writes; go-apidiff ignores the resulting package errors and
reports every module as compatible. The pinned commit changes only dependencies, and its CLI and
exit statuses (0 compatible, 1 incompatible, 2 failed) match the release.

Before comparing anything, the gate runs the checker on a canary repository whose second commit
removes an exported function. A checker that does not report that removal fails the gate with
status 2, so a future toolchain the checker cannot read is a failure, not a silent pass
(`verifyChecker`; `TestGate_Negative_ExecutionFailuresFailWhateverThePolicy` in
`tools/apicompat/gate_test.go`).

## Audit

`praetorctl audit` prints `Locked API compatibility gate verified` once both files hold their
locked texts, git commits them, and the workflow reports `Go API Compatibility` on every pull
request (`forge.RequiredStatusContextsOf`); the branch ruleset audit then requires that context.
In a repository without a tracked `go.mod` it prints the `[INFO]` line from
[Repositories without Go](#repositories-without-go) instead. With the facet removed, or with no
`go.mod` tracked, a Praetor file of the gate left behind, or a ruleset still requiring its
context, fails the audit unless `adoption.decline` names `branch-ruleset`
(`cmd/standardsctl/audit_api_compat.go`, `cmd/standardsctl/audit_api_compat_test.go`).

## Changing the gate

The workflow's action pins move through Renovate's `API compatibility gate actions` group, which
updates `tools/apicompat/assets.go` and this repository's copy together (`renovate.json`,
`TestRenovateUpdatesTemplatePinsWithWorkflowCopy` in `internal/managedasset/renovate_test.go`).
Any change to a managed text fails `TestShippedTextLedger` until
`internal/managedasset/testdata/shipped/api-compatibility.sha256` records the new digest and
`priorDigests` in `tools/apicompat/assets.go` records the outgoing one, so an adopter holding the
earlier text refreshes without `--force`:

```bash
PRAETOR_UPDATE_SHIPPED_TEXTS=1 go test ./internal/managedasset -run 'TestShippedTextLedger$'
```

Praetor lints, vets and scans the gate with its build tag (`make lint`, `make sec`,
`.golangci.yml` `run.build-tags`) and tests it by building the embedded bytes
(`tools/apicompat/gate_test.go`). The gate cannot import `internal/util`, which does not exist in
an adopting repository, so the forbidigo rule and the utility-sprawl check of
`praetorctl dedupe scan` exempt it (`.golangci.yml`, `managedAsset` in
`internal/dedupe/dedupe.go`); each of its commands runs under a deadline of its own.

## Limits

- Go only. For every other language HISS-14 is enforced by the `Migration:` footer check alone
  ([HISS compliance matrix](../wiki/HISS-Matrix.md)), and a repository without a tracked `go.mod`
  gets no gate at all ([Repositories without Go](#repositories-without-go)).
- The base revision is not built separately: the canary covers a checker that cannot read the
  toolchain's packages, and `go list -export` covers `HEAD`, but a base whose packages no longer
  load with the current toolchain is compared as the checker reads it.
- A nested module's own `vendor` directory is not used: go-apidiff loads a nested module with
  `-mod=readonly`, so its requirements come from the module cache or proxy. The vendor-free clone
  copies or hard-links the repository's object store into the temporary directory.
- go-apidiff opens the repository with go-git, which does not read a linked worktree
  (`git worktree add`): it reports every file as staged and exits 2, so the gate fails there
  without touching the tree. Run it in a clone, as the hosted job does.
- The hosted job runs on Linux. The gate itself runs on Linux, macOS and Windows, and its tests run
  on all three.
