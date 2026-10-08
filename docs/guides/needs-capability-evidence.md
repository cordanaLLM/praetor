# Framework capability evidence

`praetorctl needs report` and `standards_needs_report` report **dependency mapping
availability**, not whether a repository is ready to migrate. The retained Go/JSON
field name `Readiness.Score` remains compatible; its percentage is the dependencies a
target framework maps or declares a [non-goal](#non-goals) divided by scanned
dependencies. It does not count tests or
prove API compatibility. With a framework configured, a dependency-free repository
retains the existing 100% empty-denominator convention; that is not verification
evidence. With none configured it renders as `n/a`, like every other row.

Praetor ships no framework. A built-in catalog (`internal/needs/catalog.go`) only
**classifies** well-known libraries into capabilities (`github.com/jackc/pgx` is
`db.postgres`, `click` is `clikit.cli`); which framework package replaces, adapts, wraps
or retains a library is declared by the operator's framework, configured under
`framework.targets.<lang>` (see [Framework targets](#framework-targets)). With nothing
configured every command still classifies, and says the framework is not configured
([Not configured](#not-configured)). A Rust crate that binds a capability the native
catalog lists takes the native key, so the `ash` Vulkan bindings and
`find_package(Vulkan)` are both `gpu.vulkan`; embedded WebAssembly runtimes such as
`wasmtime` are `runtime.wasm` (`TestRustCatalogSystemsAndGraphics_3D`). A library the
catalog does not list is classified under its language's external prefix, such as
`rust.external.<crate>`.

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

## Non-goals

A framework that has decided not to provide a capability, such as a terminal UI toolkit or
a hardware SDK that belongs in the application, declares it a non-goal in the
`non_goals` list of its own `.needs.yaml`. Without the declaration every demand of that
capability stays a gap and lowers mapping availability on every run.

```yaml
non_goals:
  - capability: clikit.tui
    rationale: Terminal user interfaces belong to the application
    alternative: github.com/charmbracelet/bubbletea used directly
```

| Field | Rule |
| :--- | :--- |
| `capability` | the capability key a scan reports for the dependency (`needs report` prints `capability=`); each key once |
| `rationale` | why the capability is a non-goal; must not be empty |
| `alternative` | what a repository uses instead; must not be empty |

The list holds at most 128 entries. Each entry is decoded strictly: a key it does not
declare, such as a misspelled `rationale`, fails the read. A capability listed under
`capabilities.required` or `capabilities.optional` and under `non_goals` is refused,
because a capability is either needed or a non-goal. Every error names the entry as
`non_goals[<index>] (<capability>)` (`internal/needs/non_goals.go`;
`TestNonGoalDeclarationValidation_3D`).

When a report or a fleet aggregation selects a framework checkout, the checkout's
`.needs.yaml` supplies the framework's non-goals. A demand the framework does not map and
whose capability it declares a non-goal gets the status `non_goal`, with the rationale and
the alternative in its note. Mapping availability counts it as mapped; the row's
`readiness.non_goal_deps` keeps the number visible. `needs report` prints a
`Framework non-goals:` header line and marks such demands `○`. `needs aggregate` counts
them as covered in the fleet coverage, leaves them out of the gap table, lists each
declared non-goal under "Framework Non-goals" and adds a Non-goals column to the
leaderboard (`TestFrameworkNonGoalLiftsReadiness_3D`,
`TestAggregateReportsFrameworkNonGoals_3D`). A framework selected by its contract alone,
without a checkout, declares no non-goals.

A declaration the code contradicts is false and fails:

- `needs scan --check` and `needs scan --write` fail with `ErrNonGoalContradicted` when
  the scanned repository's own dependencies or selected standard-library imports demand a
  capability it declares a non-goal, naming the entry and up to eight packages that use
  it. A framework that imports such a capability fails the check in its own repository
  (`TestDeclaredNonGoalUsedByCode_3D`, `TestNeedsScanDeclaredNonGoals_3D`).
- Inspecting a framework checkout fails when it provides a package for a capability its
  `.needs.yaml` declares a non-goal, naming both (`TestFrameworkNonGoalContradiction_3D`).

`needs scan --write` carries the list over from the committed manifest, the way it
carries declared capabilities. A manifest without non-goals is written byte for byte as
before.

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
  `TestAcmeContractsLoadForEveryLanguage`). A target with a contract and no module takes
  the framework its contract declares as its module, so `needs scan` names that framework
  instead of `not configured` (`TestLoadRegistryContractOnlyTarget_3D`,
  `TestNeedsScanContractOnlyTarget_3D` in `cmd/standardsctl/needs_unconfigured_test.go`).
  The CLI and the MCP `standards_needs_report` select the operator settings and load these
  contracts through one loader, `needs.SelectRegistry` (`TestSelectRegistry_3D`).
- **The framework a row names.** A report, a fleet row, a migration plan and an epic name
  the framework the row's own language is reconciled against when that one is configured,
  else the first configured framework another of its languages is reconciled against, as
  `needs scan` does (`needs.RowFramework`). A host that configures only a python target
  therefore reports a python repository against the python contract, with a percentage,
  and a mixed go and python repository against the python contract too; only a row none
  of whose languages has a target is not configured. The `needs aggregate` header names
  the selected go framework when one is configured, then every other framework a row was
  scored against (`internal/needs/row_framework_test.go`,
  `TestNeedsPythonOnlyHost_3D` in `cmd/standardsctl/needs_unconfigured_test.go`).

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
`cmd/standardsctl/needs_contract_test.go`). The file opens with the `---` document start
and indents every level by two spaces, so it passes yamllint's default rules and the YAML
lint of praetor's own pre-commit hook when committed (`TestContractExportPassesYAMLLint_3D`
in `internal/needs/framework_export_test.go`).

Earlier releases shipped framework targets and replacement tables of their own. They were
removed (ADR-0014 §6) after being exported with this command, so an operator who relied on
them configures the exported contracts under `framework.targets.<lang>`.

### Not configured

With no `framework.targets` entry, every command still classifies dependencies and says
the framework is not configured instead of inventing one. The same holds for a single row
none of whose languages has a target, on a host that configures other languages
([The framework a row names](#framework-targets)). Every mapping availability or
readiness figure renders as `n/a (no target framework configured)`, never 0% and never
the empty-denominator 100% (`needs.MappingAvailability`; ADR-0014 §4; tests in
`internal/needs/unconfigured_test.go` and `cmd/standardsctl/needs_unconfigured_test.go`):

| Command | Behaviour |
| :-- | :-- |
| `needs scan` | classifies; prints `Target Framework: not configured`, `Mapping availability: n/a (no target framework configured) (…)` and `Coverage basis: not-configured`; `--write` omits `framework` and `builder_kits` |
| `needs report`, MCP `standards_needs_report` | header `Framework: not configured (set framework.targets.<lang>.module and .contract, or pass --framework) \| Mapping availability: n/a (no target framework configured)`; exit 0 |
| `needs aggregate` | `**Target Framework**: not configured (…)`, `**Overall Fleet Target Framework Coverage**: n/a (no target framework configured)`; every leaderboard row's readiness is `n/a (…)` |
| `needs epic` | the preview renders `**Target Framework**: not configured` and omits the substitution task with `nothing to rewrite: no target framework configured`; `needs epic --dev-dir` lists each repository with `Readiness: n/a (…)`; `--publish` refuses with `needs.ErrFrameworkNotConfigured` before the forge is contacted |
| `needs migrate` | the dry run prints `Mapping availability: n/a (…)` and the blocker `nothing to rewrite: no target framework configured.`; `--apply` refuses with `needs.ErrFrameworkNotConfigured` |
| `needs requests` | requests are synthesized for the classified gaps, each with empty `target_builder_kit` and `target_org` and the spec line `Target Builder Kit: unrouted (framework.targets.<lang>.builder_kits not set)`; the command prints `N of M requests unrouted` below its heading (`needs.UnroutedSummary`) |
| `agent run praetor-needs-miner` | prints `Readiness n/a (…)` |
| harvested rows | classified, readiness basis `not-configured`, no builder kits |

### The `framework_replacement` key

A demand's replacement is written as `framework_replacement`. A `.needs.yaml` or JSON
report written with the key earlier releases used (`legacyReplacementKey` in
`internal/needs/demand_alias.go`) still decodes: the old key is read when the new one is empty, the same value under both keys is accepted, and two
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
  `pyproject.toml`, `requirements.txt`, `setup.py`, `Cargo.toml`, `meson.build`,
  `CMakeLists.txt`, `build.zig` and `build.zig.zon`; declarations are
  `.standards.yaml` and `.needs.yaml`.
  Neither stops the walk: a checkout below such a directory, or below a fleet
  root that is itself a checkout, is still its own row
  (`TestDiscoverFleetDeclarationsNeverSwallowCheckouts`).

Inside a repository, every other directory holding an analyzer manifest is a
**sub-project**. Each is analysed and merged into the repository's row, and the
report lists them ("Nested sub-projects scanned into this report"). When the
root itself holds no manifest, as in a checkout whose only project is
`core/meson.build`, the row is named as the repository (steps 2 to 4 below), not
after the sub-project, and keeps the root's declared capabilities. A package several sub-projects demand is one
demand; PyPI names compare after PEP 503 normalisation, so `typing-extensions`
and `typing_extensions` are one package (`TestDemandIdentityNormalisesPyPINames`).

A row is named in this order (`nameRepository` in `internal/needs/discovery.go`):

1. the project's own manifest, where it names the project: the Go module path, or
   the `package.json` name. A manifest naming its project `unknown` names nothing;
2. `repository.name` in the root's `.standards.yaml`;
3. the origin remote of a checkout (`config.ResolveRepositoryName`, the resolver
   `needs epic --publish` takes its forge coordinates from). A directory that is no
   checkout skips this step: git would answer with the remote of a checkout around it;
4. the root directory, resolved to an absolute path first, so `--path .`,
   `--path ./` and an absolute path give the same name.

The name is the same in scan output, `.needs.yaml` and every pre-migration epic
title. Steps 2 and 3 keep it the same in every clone, linked worktree and CI
workspace, so `needs scan --check` passes for one commit under any directory name
(`TestRepositoryNameFollowsIdentity_Positive`, `TestRepositoryNameIdentity_Boundary`
in `internal/needs/repository_name_test.go`). A directory name changes with the
checkout, so a row named by step 4 says so and why: `needs scan`, `needs scan
--check`, `needs report` and `standards_needs_report` print a `Repository name:` line,
the epic checklist a `**Repository Name**` line, and JSON output carries
`repository_fallback`; `.needs.yaml` never does. Step 4 also names a row whose
`.standards.yaml` does not load, whose origin remote git cannot read, or whose
remote gives an invalid name such as the `.` of a URL ending in `/.`
(`TestRepositoryNameFallbackIsNamed_Negative`,
`TestNeedsScanNamesRepositoryFallback_3D` in `cmd/standardsctl/needs_check_test.go`).

A Cargo dependency declared with `path`, in an inline table, a
`[dependencies.<crate>]` sub-table or a dotted key (`core.path = "../core"`), is a
crate of the repository: a workspace member or a sibling crate. It is never a
third-party demand, even when it also names a `version` for publishing. A
dependency inherited with `workspace = true` takes the entry of the nearest
`Cargo.toml` above the crate with a `[workspace]` table, the file Cargo itself
searches for: an inherited path entry is first-party, and an inherited registry
entry carries the workspace's version. An inherited crate that no workspace root
declares stays a third-party demand without a version
(`TestCargoPathDependenciesAreFirstParty_3D`,
`TestCargoWorkspaceMembersAreFirstParty_3D` in
`internal/needs/analyzer_rust_test.go`).

A Zig build is a native project: the native analyzer detects `build.zig` or
`build.zig.zon` beside `meson.build` and `CMakeLists.txt`, and reports the
language `zig` for it (`c`, `cpp` and `cuda` only for meson or CMake). A Cargo
workspace with a `build.zig` at its root is one row carrying the crates and the
Zig packages, Rust and Zig (`TestScanRepoZigBesideCargoWorkspace_Positive`). The
demands come from the `.dependencies` of `build.zig.zon`, read in the format of
Zig 0.16.0 (`doc/build.zig.zon.md` in the Zig source): each entry names a
package fetched by `url` (with its `hash`) or found in the tree by `path`. A url
package is a third-party demand. A path package is one only when it sits under a
directory discovery prunes (`vendor/`, `third_party/`, `zig-pkg/` and the other names below),
where it is vendored; any other path package is the repository's own, scanned as
a sub-project when it holds a `build.zig`, like a Cargo path crate. Demands take
the native catalog's capability or `native.external.<name>`. `build.zig` itself
is a program, so libraries it compiles from vendored sources without a
`build.zig.zon` entry are not demands.

A malformed `build.zig.zon` fails the scan with the line and the reason: text
that is not ZON, a file that is not one struct literal, a `.dependencies` or
dependency entry that is not a struct, a `url` or `path` that is not a string,
and a dependency that sets both `url` and `path` or neither. The parser reads
nothing else of the manifest and nests at most 64 literals deep
(`internal/needs/zon.go`; `TestParseZon_Negative_MalformedManifestRefused`,
`TestParseZon_Boundary` in `internal/needs/zon_test.go`). The pre-migration epic
phrases no C/C++ step for a Zig build without C/C++ markers.

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
- the trees Zig writes inside a checkout, named exactly `zig-pkg`, `zig-out` or
  `.zig-cache` (`util.IsToolchainTreeDir`, the list every repository walker
  shares). Zig 0.16 copies each package it fetches into
  `zig-pkg/<name>-<version>-<hash>/` with the package's own `build.zig` and
  `build.zig.zon`, so a built checkout would otherwise list every fetched
  package as a sub-project and its url dependencies as the repository's demands
  (`TestScanRepoZigToolchainTreesPruned_Positive`,
  `TestDiscoverFleetZigToolchainTreesPruned_Negative`,
  `TestScanRepoZigToolchainTreeLookalikes_Boundary`);
- `scratch/` and `cache/` directly under the walk root or directly under a
  repository root. Deeper, as in `<repo>/internal/cache/`, they are ordinary sources.

A checkout inside a skipped directory is not discovered. The walk visits at
most 250,000 directories and 64 levels; a tree beyond either bound fails
the command rather than returning a partial fleet (`TestDiscoverFleetBounds`).

Inside a Go project, the import scan applies the skips the go command applies
when it expands `./...` (`go help packages`): `vendor/` and `testdata/`,
directories and files whose names begin with `_` or `.`, directories that
hold their own `go.mod`, and directories the module's `go.mod` `ignore`
directives (Go 1.25+) name: a `./`-prefixed path below the module root only, any
other path at every depth, each with everything inside it
(`TestScanASTImportsSkipsGoToolIgnoredSources`,
`TestScanASTImportsStopsAtNestedModules`,
`TestGoAnalysisHonoursGoModIgnoreDirectives` in `internal/needs`;
`gomanifest.IgnoreSet` applies the go command's matching rule). Praetor adds
skips of its own: a nested checkout, which is a fleet repository with its own
demand even without a `go.mod`, `node_modules/`, and `scratch/` and `cache/`
directly under the scan root. It visits at most 1,000,000 entries and fails beyond that rather than returning a
partial import set (`TestScanASTImportsBoundaryEntryLimit`). `go.mod` is read
through `internal/gomanifest`: a trailing comment never becomes part of the
module path or Go version, any white space may follow the `module` or `go`
keyword, a quoted require path or version is unquoted, and a requirement is
indirect only when its comment is the go command's `indirect` marker
(`TestParseGoModReadsDirectivesLikeTheGoCommand`,
`TestParseGoModQuotedRequirementsAndTabbedGoDirective`). A UTF-8 byte-order mark
at the start of `go.mod` is dropped before the lines are read, so the module
directive after it still names the module (`TestGoModByteOrderMark_3D`). The go
command itself refuses such a file; the scan reads it rather than count the
module's own packages as third-party demand.

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

A target that is the selected framework's own module is refused before it is
scanned. That covers the framework's own directory, a symlink to it, another
checkout whose `go.mod` declares the framework's module, and a module-path
selection of that module (`--framework=example.org/fork` on the checkout that
declares it). Scored against itself, a framework would be offered its own packages
as replacements for its own dependencies: self-import cycles, and package paths
nothing declares. The error is `needs.SelfTargetMigrationError`
(`errors.Is(err, needs.ErrSelfTargetMigration)`) and names both the target and the
framework. A nested module that only shares the framework's path prefix, such as
`example.org/fork/tools` or `example.org/fork/v2`, is a different module and is
analysed. `needs epic --dev-dir` lists the framework's own checkout under
`[SKIP]` instead of failing the run (`TestMigrationRefusesSelfTarget_3D` and
`TestRegenerateFleetEpics_SkipsSelectedFramework` in
`internal/needs/migration_self_target_test.go`).

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

Once every task exists, the parent's body names each one in a task-list line,
`- [ ] #N`, ticked when the task is already closed (`linkEpicChildren`). A slot
line of the checklist the parent was first published with
(`- [ ] **Task n**: ...`) becomes the line naming its task, keeping its tick; a
task the body names nowhere is appended under a `## Child Issues` heading. The
planning sync of `praetorctl issue reconcile` then ticks a box when its task
closes and closes the epic once every task is closed
([planning sync](issue-sync-integrity.md#planning-sync-parents-epics-and-milestones)).
A parent that already names every task is not edited; an existing parent that
gained its lines is printed as `already published (updated)`
(`TestPublishPreMigrationEpic_Positive_ParentNamesChildIssues` and
`TestPublishPreMigrationEpic_Boundary_AppendsUnnamedChildren` in
`internal/needs/epic_children_test.go`).

The epic, its parent and every task carry the repository's active milestone:
the open milestone in `.workingdir/milestones.json` that is published to the
forge and due first, an undated one after every dated one
(`milestone.ActiveMilestone`). A repository without one publishes no milestone,
and an unreadable store fails the generation.

Apart from those child lines, publishing creates missing issues and does not
synchronize existing ones. An issue that already exists keeps its labels,
dependency references, state and the rest of its body, so an epic republished
after its readiness changed still shows the text it was first published with,
and a task the operator closed stays closed. Identity is the trimmed title, not
a machine marker: renaming a published issue makes the next publish create a
new one under the generated title.
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

### Epic task scope

An epic has five task slots. Each is scoped by what the scan found, so a
repository is not handed work that does not fit it (`createChildTasks` in
`internal/needs/epic.go`):

| Fact | Read from | Effect on the tasks |
| :--- | :--- | :--- |
| Detected languages | the row's `language` and `languages` | task 1 phrases its error-handling audit and 3D test run per language: `go test -race ./...` for Go, `cargo test --workspace` for Rust, the project's test runner for Python, the package's `test` script for TypeScript, the build system's test target under ThreadSanitizer for C/C++. Task 2 asks to break circular module dependencies only for Python, TypeScript and C/C++; the Go compiler and Cargo already reject such cycles |
| Source languages no analyzer detects | the source inventory `praetorctl dedupe scan` reads (`dedupe.SourceLanguageCounts`: Git-listed or walked files by extension, test fixtures excluded), less the detected languages; JavaScript and Vue count as covered by the Node analyzer | the checklist lists them with file counts under `**Unanalyzed Languages**`, such as `shell (9 files)`, and tasks 1 and 4 name them as unverified: no audit or test runner is phrased for them and the gate checks none of them. A failed source listing is named instead of listing nothing |
| Go module at the repository root | `go.mod` | the gate's prefetch, security and race-test stages check only that module, so task 4 names every other detected language as tested and audited outside the gate |
| Kubernetes manifests | `Chart.yaml`, `kustomization.yaml` or `helmfile.yaml` at the root, as the `infra-k8s` flavor detects them | task 2 adds replacing in-cluster DNS names with configured endpoints |
| Declared runner routing | the fleet and repository tiers `config.LoadCascadingRunnerConfigContext` merges; the organisation tier is keyed by the forge owner, which the scan does not resolve | routing that differs from `DefaultRunnerPolicy` adds a check through `praetorctl audit` to task 5; routing that does not load adds repair work instead of failing the epic |
| Target framework and proposed substitutions | the migration plan | task 3 is omitted when no framework is configured, or when the scan proposes no substitution against the configured one |

An omitted task keeps its slot. The checklist names it with its reason and no
open item, the next planned task depends on the planned task before it, and
`--publish` creates no issue for it. `PreMigrationEpic.OmittedTasks`
(`omitted_tasks` in JSON) lists the omitted slots. A written epic still carries
all five `[TASK n/5]` markers that `praetorctl audit` checks. The diff-aware CI
directive states intent: the epic reads no workflow, so it never claims that
`praetorctl ci filter` already runs. Task 5 names `praetorctl sync --remote`,
because `praetorctl sync` without `--remote` leaves the forge untouched.

`internal/needs/epic_scope_test.go` pins the scope:
`TestGeneratePreMigrationEpic_Negative_RustWorkspaceGetsNoForeignWork` (a Cargo
workspace with no framework, cluster or runner signal),
`TestGeneratePreMigrationEpic_Positive_SignalsBringTheirSteps`,
`TestGeneratePreMigrationEpic_Positive_UnanalyzedLanguagesAreNamed` (shell scripts
beside a Python package) and the `Negative` and `Boundary` cases. `TestEpicCommands_Positive_ParseAgainstTheCLI` in
`cmd/standardsctl/needs_epic_commands_test.go` parses every `praetorctl`
command an epic names against the real flag sets.

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
