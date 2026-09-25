# Framework capability evidence

`praetorctl needs report` and `standards_needs_report` report **dependency mapping
availability**, not whether a repository is ready to migrate. The retained Go/JSON
field name `Readiness.Score` remains compatible; its percentage is available
catalog mappings divided by scanned dependencies. It does not count tests or
prove API compatibility. A dependency-free repository retains the existing 100%
empty-denominator convention; that is not verification evidence.

Every report states its coverage basis:

- `catalog-declared`: the built-in catalog names expected mappings. No framework
  checkout has been inspected. These declarations are not verified capabilities.
- `source-observed`: the selected local checkout contains parseable non-test Go
  source with a function, type, variable or constant declaration in the exact
  replacement package named by the catalog. Package headers, import-only files and
  documentation alone remain unavailable stubs. Directory names
  alone do not imply capabilities. This does not run a build, check build tags,
  resolve imports, compare exported APIs, execute tests, or prove runtime behavior.
  Declarations can still contain TODO or panic stubs; source-observed availability
  is candidate evidence only.
- Executed verification is outside this command. Retain separate build/test
  results for the exact repository tree and configuration before migration.

## Library relationships

The existing catalog also records libraries that should remain part of a
composition. A known relationship is not an import replacement:

- `go.uber.org/fx` is the `runtime.di` foundation. Retain fx and compose framework
  modules through it; `clikit` does not replace the DI container.
- `github.com/knadh/koanf/v2` is wrapped by the framework's `config` package;
  `github.com/lmittmann/tint` is a slog handler configured by `log`.
- `github.com/ogen-go/ogen` is tooling with related `ogenkit` integration helpers.
  Its module also contains runtime middleware and error packages. A dependency
  declaration does not prove generator execution or generated-contract parity.

These roles preserve Golusoris's decisions documented in ADR-0001 through
ADR-0004. No package versions or dependency preferences are changed. Configurable
fleet/organization/repository library selection and template options remain
separate work; these catalog entries do not activate that policy.

JSON/YAML demands gain optional `relationship` metadata with `kind`,
`framework_package` and `basis`. Related paths identify adapters and are never
written into `golusoris_replacement`. Each relation has its own evidence basis:
foundations remain `catalog-declared` even when a selected framework's adapter
packages have been inspected. `source-observed` for a wrapper/tooling relation
means only that its exact related package has qualifying declarations under the
selected module, with the same source-inspection limits as other mappings. It
does not establish that the package uses the named library or implements a
compatible API. Missing/header-only adapters remain gaps, retaining their
relationship and expected path so the result is explainable.

`Readiness.Score` retains its compatibility field and arithmetic: known native
foundations and available mappings count as covered; all external dependencies
remain in the denominator. It can therefore include catalog-declared retained
foundations alongside source-observed adapter availability. Inspect individual
relationship bases rather than treating the aggregate framework basis as proof
for every row. A foundation does not become a gap merely because an empty selected
framework has no replacement for a library that should be retained.

The Go import scan also records selected stdlib `log/slog` imports in the separate
`standard_library_imports` list. Repeated imports are deduplicated. These entries
do not affect third-party dependency counts or migration candidates. This is
syntactic non-test import observation under the existing traversal; it does not
run a build, resolve build tags or establish a complete runtime usage inventory.
Other standard-library imports remain outside this focused catalog selection.

CLI scan/report and MCP report share relationship formatting. Former
“Drop-In Replacement Matrix” wording is replaced by “Library relationships and
migration candidates”; older candidate mappings explicitly remain unverified.
Existing manifests that omit the additive fields continue to decode. Old
harvested fx mappings are refreshed from the same catalog during reconciliation,
so they cannot reintroduce a clikit replacement. Explicit library relationships
are excluded from proposed dependency drops/import rewrites, and executable
migration admission remains closed.

For example, an empty `db/` directory does not provide `db.postgres`. Parseable
library source under the catalog's `db/pgx` replacement makes that mapping
available. Other database mappings remain gaps. A fork uses its root `go.mod`
module identity in the reported replacement path. Nested modules, main packages,
and directories containing only test files do not establish parent-module library
availability. Unmapped packages are not assigned capabilities from their names.

A selected checkout may be empty and will report zero available mappings for
nonempty dependency demands. Source packages require an unambiguous root module
identity. Missing selected paths, unreadable or malformed source, symlinks and
exceeded bounds are errors. Inspection has a 30-second deadline, at most 512
catalog entries, 128 entries per selected package directory, 1 MiB per read and
8 MiB of aggregate package source. It performs no network access or writes.

CLI and MCP share `needs.ScanRepoWithFramework`; fleet aggregation applies the
same reconciliation to local and harvested demands. JSON readiness entries carry
optional `basis`, framework indexes carry `basis`, and fleet reports carry
`coverage_basis`. Existing numeric coverage fields remain available.

## Capability contract

A framework checkout may publish `capabilities.yaml` at its root (golusoris
`core/capabilities`, schema version 1). When the selected checkout has one, it is the
package inventory: every declared package, the Go module the contract says contains it,
the capability keys it satisfies and the third-party modules it `replaces`. The static
catalog's directory candidates are not consulted for such a checkout.

The contract changes what is declared, not what counts as evidence:

- Every declared package is still source-observed under the same rules as catalog
  candidates: parseable non-test Go source with a declaration, in the exact declared
  directory, inside the module the contract declares for it. A declared nested module
  (for example `github.com/golusoris/golusoris/core`) is honoured instead of being
  rejected as a foreign module, but its `go.mod` must exist and no other module may sit
  between the checkout root and the package. Header-only packages remain unavailable.
- `replaces` maps a consumer's third-party module onto an observed package, ignoring a
  `/vN` major-version suffix on either side. When several packages claim the same
  module, the one declaring the demanded capability wins, then the first in import
  order. A library the catalog does not know adopts the package's first declared
  capability instead of a `custom.*` gap. A catalog mapping whose replacement path
  predates the framework's layout is corrected by the contract.
- Catalog library relationships (retained foundations, wrappers, tooling) keep their
  roles; the contract never turns a retained library into an import replacement.
- The framework's own modules imported by a consumer (`github.com/golusoris/golusoris`,
  `…/core`) are `native` under the `fleet.framework` capability and count as covered.

The report prints `Capability contract: capabilities.yaml` next to its basis, which
stays `source-observed`; `FrameworkIndex` carries `contract` and `replacements`. A
contract that fails validation (unsupported version, `framework` not matching the
checkout's `go.mod` module, packages outside the framework, undeclared modules, invalid
capability keys, more than 512 packages or 1 MiB) fails inspection rather than
degrading to heuristics. None of this proves API compatibility, runs a build, or
admits `needs migrate --apply`.

## Selecting the source

```bash
praetorctl needs report --path /path/to/consumer --framework /path/to/framework
praetorctl needs report --path /path/to/consumer --framework=""
```

The CLI keeps its existing default selection: `PRAETOR_FRAMEWORK_DIR`, otherwise
`PRAETOR_DEV_DIR/golusoris/golusoris`, otherwise `$HOME/dev/golusoris/golusoris`.
If that selected path is absent it now errors. Use an explicitly empty
`--framework=""` for a declared-catalog estimate. MCP keeps its existing default
of a declared catalog when `framework` is omitted; explicit paths remain confined
to the server root under the existing server policy.

## Migration

Previously an explicitly missing framework silently used the built-in catalog,
and CLI/MCP report scores ignored the framework index altogether. Missing paths
now fail; select a real checkout or explicitly request the declared catalog.
Consumers must treat lower source-observed scores and newly exposed gaps as
corrected evidence, and must not infer verified migration readiness from 100%.

`FrameworkIndex.ProvidesCapability` now requires exact capability membership;
unknown or broad domain directory names no longer imply arbitrary capabilities.
All existing exported signatures remain; callers can use the additive
`ScanRepoWithFramework` function when selecting a framework. Human-readable
reports use “Mapping availability” instead of “Readiness Score”. Parse structured
fields and inspect the evidence basis rather than matching the old label.

## Migration candidates and epics

`needs migrate` and `needs epic` now share one selected framework analysis with
`ScanRepoWithFramework`. Their availability, basis, gaps and proposed import paths
come from the same reconciled inputs. An observed fork uses its root module name;
a header-only replacement package produces no replacement candidate. Epic
creation propagates inspection/planning errors instead of generating a fallback.

The exported function signatures and existing JSON fields remain. Migration plans
add `coverage_basis`, `mapping_availability`, `framework_version`, `status`, and
`blockers`; epics add version, status and blockers alongside their existing basis.
Current results are `status: "candidate"`, `framework_version: "unverified"`.
`Framework`/`target_framework` identify the module without a fabricated release
suffix. `AddedRequires` stays empty because no verified module version is known.
`Replacements` and `DroppedRequires` describe proposals only, not approved edits.

An empty selection explicitly requests the declared catalog. A module-shaped
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

# Explicitly request an offline catalog estimate.
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
for under `[SKIP]`, with the reason: a directory needs a Git checkout with HEAD
metadata, a `.standards.yaml`, or a `.needs.yaml` to count as a prepared
repository. `TestPublishPreMigrationEpic_ResumesPartialPublish` and
`TestRegenerateFleetEpics_ReportsSkippedDirectories` in
`internal/needs/epic_test.go` pin both behaviors.

### Breaking migration: application requires evidence

`ApplyMigration`, `ApplyMigrationWithOptions`, and `needs migrate --apply` now
return `*needs.UnverifiedMigrationError` (matching `needs.ErrUnverifiedMigration`
through `errors.Is`) before invoking Git, modifying files, or running module
commands. `Runner`, `SkipTidy`, and caller-supplied plan status/version fields do
not bypass admission. Nil plans and canceled contexts retain explicit errors.

Previously these paths could rewrite imports and claim success from catalog
mappings and an invented `v0.8.0`; `go mod tidy` alone did not prove that the
consumer compiled. Use candidate generation to review the actual proposed
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
