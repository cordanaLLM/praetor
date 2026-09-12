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

`needs scan` and generated pre-migration epics also expose their declared-catalog
basis. Epic and import-migration generation still use catalog declarations;
they do not yet consume the selected source index or validate replacement API
compatibility. Their proposed imports and version must be independently checked
before application. A source-observed report does not certify an older migration
plan produced through those separate paths.
