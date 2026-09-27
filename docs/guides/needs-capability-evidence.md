# Framework capability evidence

`praetorctl needs report` and `standards_needs_report` report **dependency mapping
availability**, not whether a repository is ready to migrate. The retained Go/JSON
field name `Readiness.Score` remains compatible; its percentage is the dependencies a
target framework maps divided by scanned dependencies. It does not count tests or
prove API compatibility. A dependency-free repository retains the existing 100%
empty-denominator convention; that is not verification evidence.

Praetor ships no framework. A built-in catalog (`internal/needs/catalog.go`) only
**classifies** well-known libraries into capabilities (`github.com/jackc/pgx` is
`db.postgres`, `click` is `clikit.cli`); which framework package replaces, adapts, wraps
or retains a library is declared by the operator's framework, configured under
`framework.targets.<lang>` (see [Framework targets](#framework-targets)). With nothing
configured every command still classifies, and says the framework is not configured
([Not configured](#not-configured)).

Every report states its coverage basis:

- `not-configured`: no framework is selected. Dependencies are classified, every one is
  a gap, and mapping availability renders as `n/a (no target framework configured)`,
  never 0% (`needs.MappingAvailability`).
- `identity-declared`: a module names the framework, but nothing declares its packages.
- `catalog-declared`: a configured capability contract declares the framework's
  packages and the libraries they map. No framework checkout has been inspected. These
  declarations are not verified capabilities.
- `source-observed`: the selected local checkout contains parseable non-test Go
  source with a function, type, variable or constant declaration in the exact
  package the contract names. Package headers, import-only files and
  documentation alone remain unavailable stubs. Directory names
  alone do not imply capabilities. This does not run a build, check build tags,
  resolve imports, compare exported APIs, execute tests, or prove runtime behavior.
  Declarations can still contain TODO or panic stubs; source-observed availability
  is candidate evidence only.
- Executed verification is outside this command. Retain separate build/test
  results for the exact repository tree and configuration before migration.

## Library relationships

A framework's contract also records libraries that should remain part of a
composition. A declared relationship is not an import replacement:

- a `foundations` entry (for example `go.uber.org/fx` or `log/slog`) is retained as
  is: `foundation; retain library`, status `native`;
- a `wraps` entry (for example `github.com/knadh/koanf/v2` wrapped by the framework's
  `config` package) is `wrapped-by` that package;
- a `tooling_for` entry (for example `github.com/ogen-go/ogen` with an integration
  package) is `tooling`. A dependency declaration does not prove generator execution
  or generated-contract parity.

These roles are the configured framework's design decisions; praetor declares none of
them. No package versions or dependency preferences are changed. Configurable
fleet/organization/repository library selection and template options remain
separate work; these contract entries do not activate that policy.

JSON/YAML demands gain optional `relationship` metadata with `kind`,
`framework_package` and `basis`. Related paths identify adapters and are never
written into `framework_replacement`. Each relation has its own evidence basis:
foundations remain `catalog-declared` even when a selected framework's adapter
packages have been inspected. `source-observed` for a wrapper/tooling relation
means only that its exact related package has qualifying declarations under the
selected module, with the same source-inspection limits as other mappings. It
does not establish that the package uses the named library or implements a
compatible API. A wrapper or tooling package the checkout does not provide (missing or
header-only) is not observed, so the library is a plain gap
(`TestLibraryRelationshipTargetsRequireObservedPackages`).

`Readiness.Score` retains its compatibility field and arithmetic: known native
foundations and available mappings count as covered; all external dependencies
remain in the denominator. It can therefore include catalog-declared retained
foundations alongside source-observed adapter availability. Inspect individual
relationship bases rather than treating the aggregate framework basis as proof
for every row. A foundation does not become a gap merely because an empty selected
framework has no replacement for a library that should be retained.

The Go import scan also records selected stdlib `log/slog` imports in the separate
`standard_library_imports` list; the framework's contract decides their role (a
`foundations` entry retains them). Repeated imports are deduplicated. These entries
do not affect third-party dependency counts or migration candidates. This is
syntactic non-test import observation under the existing traversal; it does not
run a build, resolve build tags or establish a complete runtime usage inventory.
Other standard-library imports remain outside this focused catalog selection.

CLI scan/report and MCP report share relationship formatting. Former
“Drop-In Replacement Matrix” wording is replaced by “Library relationships and
migration candidates”; older candidate mappings explicitly remain unverified.
Existing manifests that omit the additive fields continue to decode. A relationship an
older harvested report carries that the selected framework does not declare is marked
`basis=unverified` and counts as a gap
(`TestLibraryRelationshipUnknownFoundationCannotClaimRetention`). Explicit library
relationships are excluded from proposed dependency drops/import rewrites, and
executable migration admission remains closed.

For example, an empty `db/` directory does not provide `db.postgres`. Parseable
library source in the `db/pgx` package the contract declares makes that mapping
available. Other database mappings remain gaps. A fork uses its root `go.mod`
module identity in the reported replacement path. Nested modules, main packages,
and directories containing only test files do not establish parent-module library
availability. Unmapped packages are not assigned capabilities from their names.

A selected checkout may be empty and will report zero available mappings for
nonempty dependency demands. Source packages require an unambiguous root module
identity. Missing selected paths, unreadable or malformed source, symlinks and
exceeded bounds are errors. Inspection has a 30-second deadline, at most 512
contract packages, 128 entries per selected package directory, 1 MiB per read and
8 MiB of aggregate package source. It performs no network access or writes.

CLI and MCP share `needs.ScanRepoWithFramework`; fleet aggregation applies the
same reconciliation to local and harvested demands. JSON readiness entries carry
optional `basis`, framework indexes carry `basis`, and fleet reports carry
`coverage_basis`. Existing numeric coverage fields remain available.

## Capability contract

A framework checkout may publish `capabilities.yaml` at its root (schema version 1).
When the selected checkout has one, it is the package inventory: every declared
package, the Go module the contract says contains it, the capability keys it satisfies
and the third-party modules it `replaces`. A checkout without one is observed against the
configured `framework.targets.go.contract`; a checkout of another module, such as a fork,
is observed at that contract's package paths under its own module. A checkout with
neither observes no package (`TestCheckoutObservedAgainstConfiguredContract_3D` in
`internal/needs/framework_source_test.go`). A contract can also be configured without a
checkout (`framework.targets.<lang>.contract`, see [Framework targets](#framework-targets));
its packages are then declared, not observed.

The contract changes what is declared, not what counts as evidence:

- Every declared package of a checkout is source-observed: parseable non-test Go
  source with a declaration, in the exact declared
  directory, inside the module the contract declares for it. A declared nested module
  (for example `example.com/acme/kit/core`) is honoured instead of being
  rejected as a foreign module, but its `go.mod` must exist and no other module may sit
  between the checkout root and the package. Header-only packages remain unavailable.
- `replaces` maps a consumer's third-party module onto an observed package, ignoring a
  `/vN` major-version suffix on either side. When several packages claim the same
  module, the one declaring the demanded capability wins, then the first in import
  order. A library the catalog does not classify adopts the package's first declared
  capability instead of a `custom.*` gap.
- Library relationships (`foundations`, `wraps`, `tooling_for`) keep their roles; the
  contract never turns a retained library into an import replacement.
- The framework's own modules imported by a consumer (`example.com/acme/kit`,
  `…/core`) are `native` under the `fleet.framework` capability and count as covered.

The report prints `Capability contract: <file>` next to its basis
(`capabilities.yaml` for a checkout's own contract, else the configured file's name);
`FrameworkIndex` carries `contract` and `replacements`.

Version 1 also accepts optional fields; a reader that predates them ignores them:

| Field | Meaning |
| :-- | :-- |
| `ecosystem` | `go` (the default), `npm`, `pypi`, `cargo` or `system`: the grammar every third-party name below is checked against; a configured contract must declare its language's ecosystem |
| `adapts` (per package) | third-party names the package offers an adapter for: `adapter_available`, not a replacement |
| `wraps`, `tooling_for` (per package) | libraries the package wraps or is tooling for; the library is retained (`wrapped-by`, `tooling`) |
| `foundations` (top level) | libraries the framework retains as foundations; a go contract may list standard-library imports here |

Each list holds at most 64 names (`internal/needs/framework_contract.go`). A
contract that fails validation (unsupported version, `framework` not matching the
checkout's `go.mod` module, packages outside the framework, undeclared modules, invalid
capability keys, more than 512 packages or 1 MiB) fails inspection rather than
degrading to heuristics. None of this proves API compatibility, runs a build, or
admits `needs migrate --apply`.

## The committed manifest

`praetorctl needs scan --write` writes `.needs.yaml` scored against the operator's
framework targets (see [Framework targets](#framework-targets)); with none configured it
records the classification only, without a `framework` line, readiness basis
`not-configured`. Nothing else regenerates it,
`compile-context` included. `needs scan --check` scans the same way,
writes nothing, and fails when the committed file differs from what `--write` would write
now, printing the committed lines a fresh scan drops (`- committed:<line>`) and the lines
it adds (`+ generated:<line>`):

```bash
praetorctl needs scan --write   # refresh .needs.yaml
praetorctl needs scan --check   # fail on a stale .needs.yaml
```

The comparison ignores `updated_at`, which every scan stamps, and reads CRLF line
endings as LF. `--write` and `--check` exclude each other. This repository runs the check
as `make needs-check` inside `make verify-all`, with no operator settings selected (empty
`PRAETOR_FLEET_CONFIG` and `PRAETOR_WORKSTATION_CONFIG`, `--manifest=`), so a workstation
that configures framework targets judges the committed manifest the way CI does (this
repository's own `.needs.yaml` therefore names no framework);
`praetorctl audit` does not run the check, so an adopter's manifest written by an older
Praetor is not failed by a newer one. Tests:
`internal/needs/manifest_check_test.go`, `cmd/standardsctl/needs_check_test.go`.

## Selecting the source

```bash
praetorctl needs report --path /path/to/consumer --framework /path/to/framework
praetorctl needs report --path /path/to/consumer --framework=""
```

Without `--framework` the CLI selects `$PRAETOR_FRAMEWORK_DIR`, then
`framework.targets.go.checkout`. Without a checkout, `framework.targets.go.contract`
declares the framework, and without a contract the go target's module names it
(`needs.SelectFrameworkSource`, ADR-0014 §3). No checkout under the dev root is selected
by default any more. If a selected path is absent, the command errors. Use an explicitly
empty `--framework=""` to score against the declaration. The MCP
`standards_needs_report` resolves `framework` the same way; an explicit path stays
confined to the server root under the existing server policy. `needs aggregate` and
`needs requests` scan the dev root (`PRAETOR_DEV_ROOT`, otherwise `PRAETOR_DEV_DIR`,
otherwise `$HOME/dev`) unless `--dev-dir` is given.

## Framework targets

The framework each language is scored against is operator configuration,
`framework.targets.<lang>` in the operator settings
([effective policy](effective-policy.md#framework-forge-and-topology)). Every `needs`
subcommand reads it; so do the MCP `standards_needs_report` and the needs-miner agent,
through the environment and the install manifest.

Praetor has no built-in target: a language is scored against a framework only when the
operator configures one.

- **A language without a target** has no framework. Its demands are classified and are
  gaps, its scan names no framework and its requests are unrouted
  (`TestRegistryScoresEachLanguageAgainstItsTarget` in `internal/needs/targets_test.go`).
  With no target at all, see [Not configured](#not-configured).
- **A configured target** gets its own module, builder kits and routing kit (the first
  builder kit; at most eight). A target with a module and no contract names the framework
  and maps nothing (`TestUnconfiguredScanOmitsFramework_3D`).
- **Contracts.** Each target's `contract` is loaded when a command starts
  (`needs.LoadRegistry`); a scan maps the language's demands onto the packages it
  declares, and a report does so for every language but go, whose framework the report
  selects as above (`TestLoadRegistryDeclaresTargetContracts`,
  `TestAcmeContractsLoadForEveryLanguage`).

### Configuring a framework

A workstation document (placeholder values; a relative `contract` resolves against the
document that sets it):

```yaml
framework:
  targets:
    go:
      module: example.com/acme/kit
      builder_kits: [acme/kit, acme/kit-extras]
      contract: frameworks/kit.capabilities.yaml
      checkout: /home/operator/dev/acme/kit   # optional, workstation layer, go only
    typescript:
      module: example.com/acme/ui
      builder_kits: [acme/ui]
      contract: frameworks/ui.capabilities.yaml
```

Select it with `--workstation-config=<file>` (or `PRAETOR_WORKSTATION_CONFIG`, or the
install manifest) on any `needs` subcommand. The contract format is described in
[Capability contract](#capability-contract); `internal/needs/testdata/contracts/` holds
one placeholder contract per language.

### Exporting a framework as a contract

`praetorctl needs contract export --language=<lang> --out=<file>` writes the framework a
language's target resolves to as a version-1 contract: the selected go framework
(`--framework` selects a checkout; a checkout exports the packages it provides), or
another language's configured contract. A target with a module and no contract exports an
empty contract naming the module, and no target at all is an error
(`set framework.targets.<lang>.module`). Use it to snapshot a framework checkout for CI
runs that have no checkout, or to carry a configured framework to another host. An entry
the contract grammar cannot carry, such as a capability key without a dot, is listed as
`[SKIP]` instead of being dropped silently (`internal/needs/framework_export_test.go`,
`cmd/standardsctl/needs_contract_test.go`).

Earlier releases shipped framework targets and replacement tables of their own. They were
removed (ADR-0014 §6) after being exported with this command, so an operator who relied on
them configures the exported contracts under `framework.targets.<lang>`.

### Not configured

With no `framework.targets` entry, every command still classifies dependencies and says
the framework is not configured instead of inventing one (ADR-0014 §4; tests in
`internal/needs/unconfigured_test.go`):

| Command | Behaviour |
| :-- | :-- |
| `needs scan` | classifies; prints `Target Framework: not configured` and `Coverage basis: not-configured`; `--write` omits `framework` and `builder_kits` |
| `needs report`, MCP `standards_needs_report` | header `Framework: not configured (set framework.targets.<lang>.module and .contract, or pass --framework) \| Mapping availability: n/a (no target framework configured)`; exit 0 |
| `needs aggregate` | `**Target Framework**: not configured (…)`, `**Overall Fleet Target Framework Coverage**: n/a (no target framework configured)` |
| `needs epic` | the preview renders `**Target Framework**: not configured`; `--publish` refuses with `needs.ErrFrameworkNotConfigured` before the forge is contacted |
| `needs migrate` | the dry run lists the blocker `nothing to rewrite: no target framework configured.`; `--apply` refuses with the same text |
| `needs requests` | requests are synthesized for the classified gaps, each with empty `target_builder_kit` and `target_org` and the spec line `Target Builder Kit: unrouted (framework.targets.<lang>.builder_kits not set)`; `needs.UnroutedSummary` counts them as `N of M requests unrouted` |
| harvested rows | classified, readiness basis `not-configured`, no builder kits |

### The `framework_replacement` key

A demand's replacement is written as `framework_replacement`. A `.needs.yaml` or JSON
report written with the former key `golusoris_replacement` still decodes: the old key is
read when the new one is empty, the same value under both keys is accepted, and two
different values are an error naming both. Every row read through the old key carries a
deprecation that `needs scan`, `needs report` and the MCP report print as
`Deprecated input:`; the next `needs scan --write` writes the new key only. The old key is
removed two minor releases after ADR-0014 is accepted (`TestDemandReplacementAliasMatrix`
in `internal/needs/demand_alias_test.go`).

## Repository and fleet discovery

A report scores a **repository** as one row. `needs scan`, `needs report`,
`needs migrate`, `needs epic` and the MCP `standards_needs_report` scan the
repository at `--path` (or `path`); `needs aggregate`, `needs requests` and
`needs epic --dev-dir` first discover every repository below `--dev-dir`. Both
use one walk and one scorer (`discoverFleet`, `discoverRepository` and
`scanRepository` in `internal/needs/discovery.go`), so a fleet epic's readiness
equals the repository's `needs aggregate` row and its single-repository scan.

What counts as a repository:

- **A git checkout starts a repository wherever it sits.** Checkout detection
  is the shared `topology.HasValidGitRepo`: a `.git` directory with a `HEAD`,
  or a gitlink file whose target has one. A submodule or an independent clone
  nested in another checkout is its own row with its own demand. The outer
  checkout's Go import scan stops at it (`TestDiscoverFleetNestedCheckoutsAreOwnRows`).
- **Linked worktrees of one repository are one row.** Checkouts that share a
  git common directory collapse onto the main worktree, or onto the first
  worktree found when the main one is outside the walk. The submodules a
  linked worktree checks out collapse the same way onto the main checkout's
  copy: git keeps them under `.git/worktrees/<name>/modules/`, and
  `topology.ResolveCheckoutRepository` folds that onto `.git/modules/`, reading
  `.git` metadata without running git. Every collapsed checkout is listed under
  "Linked Worktrees Collapsed" in the aggregate report and as a `[SKIP]` line
  by `needs epic --dev-dir` (`TestDiscoverFleetCollapsesLinkedWorktrees`,
  `TestDiscoverFleetCollapsesSubmodulesOfLinkedWorktrees`). Independent clones
  are never collapsed, even when they sit in a worktree.
- **Outside every checkout, a directory holding an analyzer manifest or a
  declaration starts a repository.** Manifests are `go.mod`, `package.json`,
  `pyproject.toml`, `requirements.txt`, `setup.py`, `Cargo.toml`, `meson.build`
  and `CMakeLists.txt`; declarations are `.standards.yaml` and `.needs.yaml`.
  Neither stops the walk: a checkout below such a directory, or below a fleet
  root that is itself a checkout, is still its own row
  (`TestDiscoverFleetDeclarationsNeverSwallowCheckouts`).

Inside a repository, every other directory holding an analyzer manifest is a
**sub-project**. Each is analysed and merged into the repository's row, and the
report lists them ("Nested sub-projects scanned into this report"). When the
root itself holds no manifest, as in a checkout whose only project is
`core/meson.build`, the row is named after the root directory and keeps the
root's declared capabilities. A package several sub-projects demand is one
demand; PyPI names compare after PEP 503 normalisation, so `typing-extensions`
and `typing_extensions` are one package (`TestDemandIdentityNormalisesPyPINames`).

A nested sub-project whose scan fails, such as a template `package.json`
under `examples/`, does not fail the repository. The row keeps the root
project and every other sub-project, and the failure is listed with its error
in the row, in scan output ("Sub-projects that FAILED to scan"), in the
aggregate report ("Sub-projects Failed") and as a pre-migration epic blocker.
The repository fails only when its root project fails, or when no sub-project
scans at all (`TestFailedSubprojectKeepsRepositoryRow`,
`TestFailedSubprojectsUnderNonProjectRoot` in
`internal/needs/discovery_subproject_test.go`).

Sub-projects are scanned at most 5 directories below the repository root. A
deeper manifest is never dropped silently: the row and the aggregate report list
it as not scanned, and the pre-migration epic records it as a blocker
(`TestDiscoverFleetReportsSubprojectsBeyondDepthBound`). A repository whose only
manifests are deeper than that fails its scan with the unscanned paths named.

The walk never enters:

- symlinked directories;
- dot-directories (`.git`, `.claude`, `.workingdir`, `.venv` and the like);
- directories named exactly `vendor`, `node_modules`, `third_party`, `build`,
  `target` or `testdata`. Matching is exact and case-sensitive, so
  first-party trees such as `Build-tools/` or `build_scripts/` are walked;
- `scratch/` and `cache/` directly under the walk root or directly under a
  repository root. Deeper, as in `<repo>/internal/cache/`, they are ordinary sources.

A checkout inside a skipped directory is not discovered. The walk visits at
most 250,000 directories and 64 levels; a tree beyond either bound fails
the command rather than returning a partial fleet (`TestDiscoverFleetBounds`).

In `needs aggregate`, a repository in which no analyzer recognises a project
is listed under "Skipped Repositories". Rows are never merged by name: two
repositories that share a name, such as two clones of one upstream, are
separate rows, told apart by the leaderboard's Location column.

## Migration

An explicitly missing framework path is an error; select a real checkout or
explicitly request the declaration with `--framework=""`.  Lower
source-observed scores and newly exposed gaps are corrected evidence; a 100%
score does not imply verified migration readiness.

`FrameworkIndex.ProvidesCapability` requires exact capability membership;
unknown or broad domain directory names do not imply arbitrary capabilities.
Callers select a framework with `SelectFrameworkSource` and `InspectFramework`, and
score against it with `ScanRepoWithFramework`, passing the analyzer registry built from
the operator's targets (`RegistryFromPolicy`; `nil` configures no target). Human-readable
reports use “Mapping availability” instead of “Readiness Score”. Parse structured
fields and inspect the evidence basis rather than matching the old label.

## Migration candidates and epics

`needs migrate` and `needs epic` share one selected framework analysis with
`ScanRepoWithFramework`. Their availability, basis, gaps and proposed import paths
come from the same reconciled inputs. An observed fork uses its root module name;
a header-only replacement package produces no replacement candidate. Epic
creation propagates inspection/planning errors instead of generating a fallback.

The candidate also names the branch an admitted migration would create:
`framework.migration_branch`, else `refactor/framework-adoption`. An operator with an
open branch under the former built-in name configures that name, because the migration
refuses to reset an existing branch (`TestMigrationBranch_3D` in
`internal/needs/migrate_test.go`).

The existing JSON fields remain. Migration plans
add `coverage_basis`, `mapping_availability`, `framework_version`, `status`, and
`blockers`; epics add version, status and blockers alongside their existing basis.
Current results are `status: "candidate"`, `framework_version: "unverified"`.
`Framework`/`target_framework` identify the module without a fabricated release
suffix. `AddedRequires` stays empty because no verified module version is known.
`Replacements` and `DroppedRequires` describe proposals only, not approved edits.

An empty selection explicitly requests the declaration: the go target's contract, or
nothing on a host that configures no go target ([Not configured](#not-configured)). A
module-shaped
selection such as `example.org/fork` preserves its identity but uses
`identity-declared` basis with no claimed source mappings. To select a relative
checkout unambiguously, prefix its path with `./`. Missing local selections,
invalid source, and inspection failures are errors, matching source reports.
Module names and catalog metadata do not establish a published release.

```bash
# Preview using actual selected source; performs no migration writes.
praetorctl needs migrate --path /path/to/consumer --framework /path/to/framework

# Generate an advisory epic from that same source selection.
praetorctl needs epic --path /path/to/consumer --framework /path/to/framework

# Explicitly request an offline estimate against the declaration.
praetorctl needs migrate --path /path/to/consumer --framework=""
```

`needs epic --publish` resolves the parent epic and each task by title against
the target repository's full issue inventory before it writes anything
(`PublishPreMigrationEpic` in `internal/needs/epic.go`, on the batch from
`forge.PrepareIssueBatch` in `internal/forge/issues.go`). Publishing again reuses
every issue that already exists, leaves it as it is, and creates only the
missing ones, so an interrupted publish resumes and each new task chains onto the
real number of the task before it. A duplicate planned title, two existing
issues sharing a planned title, or an incomplete inventory stops the publish
before the first issue is created. The command prints `created` or
`already published` for each issue.

Publishing creates missing issues; it does not synchronize existing ones. An
issue that already exists keeps its body, labels, dependency references and
state, so an epic republished after its readiness changed still shows the body
it was first published with, and a task the operator closed stays closed.
Identity is the trimmed title, not a machine marker: renaming a published
issue makes the next publish create a new one under the generated title.
`TestPublishPreMigrationEpic_RepublishCreatesNothing` pins that a republish
modifies nothing.

`needs epic --dev-dir` lists every discovered directory it generated no epic
for under `[SKIP]`, with the reason:

- the directory is not a prepared repository: it needs a Git checkout with HEAD
  metadata, a `.standards.yaml`, or a `.needs.yaml`;
- it is a linked worktree, or a submodule checked out in one, collapsed onto
  its repository's checkout;
- it is a checkout that declares no needs and in which no analyzer recognises
  a project, such as a documentation-only repository.

A repository that declares needs (`.standards.yaml` or `.needs.yaml`) but in
which no analyzer recognises a project is a failure, not a skip: the command
exits non-zero and names it (`TestFleetEpicsFailOnDeclarationOnlyRepositories`
in `internal/needs/discovery_test.go`).
`TestPublishPreMigrationEpic_ResumesPartialPublish` and
`TestRegenerateFleetEpics_ReportsSkippedDirectories` in
`internal/needs/epic_test.go` pin publishing and skip reporting.

### Application requires evidence

`ApplyMigration`, `ApplyMigrationWithOptions`, and `needs migrate --apply`
return `*needs.UnverifiedMigrationError` (matching `needs.ErrUnverifiedMigration`
through `errors.Is`) before invoking Git, modifying files, or running module
commands. A plan with no target framework is refused first, with
`needs.ErrFrameworkNotConfigured` (`TestUnconfiguredMigrationRefusesApply_3D`). `Runner`, `SkipTidy`, and caller-supplied plan status/version fields do
not bypass admission. Nil plans and canceled contexts retain explicit errors.

Contract mappings are not evidence, and `go mod tidy` alone does not prove that
the consumer compiles. Use candidate generation to review the actual proposed
changes. Stop automation that treats a dry-run proposal or successful source
inspection as permission to apply it. Handle the typed admission error, and
retain the original dependency until a reviewed replacement has real evidence.

**Remaining work:** executable migration admission requires an immutable module
version bound to the selected source, replacement API compatibility, and isolated
consumer build/test results with an evidence validator that rejects stale or
forged inputs. That system is not implemented by candidate generation. Source
observation can include wrong APIs, absent imported subpackages, or TODO/panic
implementations and cannot supply that evidence. Existing rewrite primitives
retain their direct tests for path confinement, branch safety, dependency edits
and partial failures; these tests do not admit a generated plan for execution.
