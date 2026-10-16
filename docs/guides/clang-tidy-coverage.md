# clang-tidy translation-unit coverage gate

A clang-tidy run checks only the files its compile database or file list names. A translation
unit no lane reads is never linted, and nothing reports it: an adopter repository found that
dozens of its GPU sources had never been through clang-tidy. The coverage gate closes that gap.
It fails when git tracks a C, C++, CUDA, HIP or Objective-C++ translation unit that no declared
clang-tidy lane reads and no live exception excuses, and it names each such file.

The gate is `tidycoverage.Check` in
[`internal/tidycoverage/coverage.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/tidycoverage/coverage.go).
The audit runs it, and `praetorctl ci tidy-coverage` runs it on its own.

## Quick start

Declare each clang-tidy run as a lane in `.standards.yaml`, and name the units no lane can read
in the top-level `exceptions` list:

```yaml
clang_tidy:
  lanes:
    - name: "cpu"
      compile_database: "build/compile_commands.json"
    - name: "metal"
      files: ".config/clang-tidy/metal-files.txt"

exceptions:
  - rule: "clang-tidy-coverage"
    path: "src/win32/getopt.c"
    reason: "Windows-only unit; no lane configures a Windows build yet"
    expires: "2026-12-31"
```

The audit runs the gate in the pre-commit and pre-push hooks. A `compile_database` lane is a
build output, so the audit judges coverage only where every lane's database is written and
otherwise skips with the lane named ([below](#lanes)). Run the gate that fails closed in the CI
job that has just configured the build:

```bash
praetorctl ci tidy-coverage --dir=.
```

Without that job, a repository with a `compile_database` lane has no run that fails when the
database is missing.

## When it runs

| Where | Runs when | Otherwise | A `compile_database` lane's database is not written |
| :--- | :--- | :--- | :--- |
| `praetorctl audit` | the resolved policy's `linters` name `clang-tidy`, as the `native-gpu-systems` profile does, or `.standards.yaml` declares `clang_tidy` | prints `[SKIP] clang-tidy translation-unit coverage not checked:` with the reason, neither a pass nor a failure | skips with that `[SKIP]` line, naming each such lane |
| `praetorctl ci tidy-coverage [--dir=.]` | always | — | fails closed |

`auditTidyCoverage` in `cmd/standardsctl/tidy_coverage.go` makes the choice
(`TestAuditTidyCoverage_Boundary_EnablementAndSkips`). The audit runs in the pre-commit and
pre-push hooks adoption writes, so every adopter of a profile that declares `clang-tidy` gets
the gate without further wiring. The `linters` key is consumed for this one name only
(`.config/archetype-coverage.yaml`).

A repository that tracks no translation unit is skipped with the reason
`the repository tracks no C, C++, CUDA, HIP or Objective-C++ translation unit`, whatever it
declares (`TestCheck_Boundary_SkipAndEdges`).

## What counts as a translation unit

A path `git ls-files --cached` lists whose suffix is `.c`, `.cc`, `.cpp`, `.cxx`, `.c++`,
`.cppm`, `.cu`, `.hip` or `.mm`, compared without case. Headers are read through the units that
include them and are not counted. An untracked file is not counted until it is staged. A file
named like a unit that clang has no mode for, or that only an include reads, needs an
exceptions entry.

## Lanes

`clang_tidy.lanes` lists 1 to 64 lanes (`config.ValidateClangTidy` in
`internal/config/clang_tidy.go`). Each lane has a unique `name` and exactly one source, a clean
repository-relative path:

| Key | Reads |
| :--- | :--- |
| `compile_database` | A [JSON compilation database](https://clang.llvm.org/docs/JSONCompilationDatabase.html), the `compile_commands.json` CMake and Meson write. Each entry's `file` is resolved against its `directory`; entries outside the repository, such as dependencies built in the same tree, are left out. A repository root reached through a symbolic link is matched in both forms. |
| `files` | A text file with one repository-relative path per line; blank lines and lines starting with `#` are skipped. A line that leaves the repository is an error. |

A lane whose source is missing, unreadable, not a compilation database, empty, or holds an entry
without a `file` fails the gate closed, with the lane named. It never counts as a lane that reads
nothing (`TestCheck_Negative_UnreadableLaneFailsClosed`).

One case differs between the two runs. A compile database is a build output, and the hooks run
the audit in checkouts that have not been built. So when a `compile_database` lane's database
does not exist, the audit skips the gate rather than judge coverage without that lane:

```text
[SKIP] clang-tidy translation-unit coverage not checked: no compile database written in this checkout for lane cpu (build/compile_commands.json); praetorctl ci tidy-coverage judges coverage where a build writes it.
```

The audit still reads the other lanes first and fails on any of their inputs that is unreadable,
and it fails on a compile database that exists but is malformed or empty. A `files` list is
tracked, not built, so a missing one fails the audit too. `praetorctl ci tidy-coverage` fails
closed on a missing compile database as on every other unreadable input
(`tidycoverage.Options.SkipUnbuilt`, `TestCheck_Boundary_SkipUnbuiltCompileDatabase`,
`TestAuditTidyCoverage_Boundary_UnbuiltCompileDatabaseSkips`). A developer who has built a lane
locally gets the full gate from the audit. To judge coverage in the hooks without a build, use a
tracked `files` lane, such as the units a clang-tidy run last measured.

## Exceptions

The top-level `exceptions` list is the repository's one declared per-file exception list
(AGENTS.md rule 14). The gate reads the entries whose `rule` is `clang-tidy-coverage`; the
credits gate of a Praetor checkout reads those whose `rule` is `credits`
([Credits & Acknowledgements](../credits.md#how-this-page-is-kept)).
For a HISS rule decided by the invariant scanner, a live entry removes only matching findings
from the touched-file and new-finding ratchet checks. The audit prints each excused finding
with its reason and expiry. Expired entries excuse nothing, and an entry matching no finding in
its file and function is stale and fails the audit (`applyHISSExceptions` in
`cmd/standardsctl/audit_exceptions.go`; `TestAudit_HISSException*`).

| Key | Rule |
| :--- | :--- |
| `rule` | A rule some gate reads from the list: the HISS registry IDs `HISS-01` through `HISS-21` (`hisscatalog.RuleIDs()`), `clang-tidy-coverage`, `credits` (`config.ExceptionRuleCredits`), `test-timeout` for a workflow that sets a test timeout above the 30-minute per-package bound (`forge.AuditTestTimeouts`, #1381), `root-license-notice` for the [root licence check](licensing-gates.md#one-root-licence), `reuse-annotation-order` for a `REUSE.toml` too large for the [annotation order check](licensing-gates.md#annotation-order), `api-compatibility` (`config.ExceptionRuleAPICompatibility`) for a Go module the API compatibility gate cannot build ([A module that cannot be built even so](api-compatibility.md#a-module-that-cannot-be-built-even-so)), `declared-linters` (`config.ExceptionRuleDeclaredLinters`) for a declared linter no workflow runs yet ([Declared linters](effective-policy.md#declared-linters)), or `mutants` for a tracked mutation-tool config that excludes mutants. HISS-11 names a release workflow with a supply-chain gap ([Declaring a gap](releasing.md#declaring-a-gap)); HISS-10 names a workflow with build lanes that cannot use warnings as errors ([Build-warnings gate](build-warnings.md#exceptions)); HISS-18 names a workflow that must run on every branch push or draft pull request ([Workflow trigger audit](workflow-triggers.md#declaring-a-workflow-that-must-run-everywhere)); HISS-19 names one repository file with duplicate function blocks ([HISS-19: Reuse Before Writing](../standards/hiss-spec.md#hiss-19-reuse-before-writing)). |
| `path` or `glob` | Exactly one, except for `declared-linters`. HISS scanner exceptions use one `path`; no glob. `path` names one clean repository-relative file without glob characters. Other glob entries follow the `docs_surfaces` glob rules: `*` stays inside one segment and a `**` segment spans any number. |
| `function` | Optional for a HISS scanner rule. Matches the scanner finding's function symbol and narrows that file's exception; omitted means the entry covers matching findings anywhere in that file. |
| `tool` | Only and always for `declared-linters`: the linter the entry excuses, as the profile or facet names it (for example `trivy`). A `declared-linters` entry sets neither `path` nor `glob`, and no other rule takes `tool` (`config.ValidateExceptions`). |
| `reason` | One line, at most 1024 bytes. |
| `expires` | A `YYYY-MM-DD` date at most 90 days after today, the bound `scripts/npm_audit_gate.py` applies to its own list. The entry holds through that day. |
| `category` | Optional for existing rules. `A`, `B`, `C` and `D` use their declared interval: A keeps its own expiry and the 90-day cap; B, C and D require expiry exactly 3, 10 and 120 days after `entered`. The `mutants` rule requires a category. |
| `entered` | Required with `category`; a `YYYY-MM-DD` date from which B, C and D intervals are counted. |

The `mutants` rule names one tracked mutation-tool configuration by `path`, such as
`.cargo/mutants.toml`. Its category covers all exclusions in that file. The audit reads cargo-mutants
`exclude_re` and `exclude_globs`; mutmut's `only_mutate`, `do_not_mutate` and
`do_not_mutate_patterns` in
`pyproject.toml` or `setup.cfg`; Stryker JSON config (`mutate` negations, `ignorePatterns` and
`mutator.excludedMutations`); PIT Maven `pom.xml` (`excludedClasses`, `excludedMethods` and
`avoidCallsTo`); Gremlins `.gremlins.yaml` (`unleash.exclude-files`); and go-mutesting configs
named `go-mutesting.yaml`, `.mutesting.yaml` or `mutesting.yaml` (`exclude_dirs`,
`disable_mutators`, `enable_mutators` and `ignore_source_lines`). Nonempty exclusion settings need a live categorized
entry; empty settings need none. Stryker JavaScript/TypeScript config files and PIT Gradle build
scripts are not read because they execute code or use a dynamic DSL; go-mutesting configurations
at arbitrary paths are not discoverable because the tool loads them only when passed by flag.
Source attributes, comments and command-line-only exclusions are outside this file audit. For
example, an exception entered 2026-10-16 in category D expires 2027-02-13:

```yaml
exceptions:
  - rule: mutants
    path: .cargo/mutants.toml
    category: D
    entered: 2026-10-16
    reason: generated code is not yet mutation-tested
    expires: 2027-02-13
```

`LoadManifest` refuses a malformed entry, and the gate applies the same validator,
`config.ValidateExceptions` in `internal/config/exceptions.go`
(`TestValidateExceptionsNegative`, `TestValidateExceptionsBoundary`). An expired entry is still
a valid declaration: the gate treats it as missing and fails on its files. An entry that
excuses no unit every lane leaves unread, because a lane now reads the file or the file is gone,
is stale and fails until it is removed (`TestCheck_Negative_ExpiredAndStaleExceptions`).

The audit also fails every entry whose `path` names a file that does not exist, or whose `glob`
matches none, the same way it fails an expired entry, and the message names the rule, the entry
and the missing path (#1259; `auditExceptionsExpiry` in `cmd/standardsctl/audit_exceptions.go`).
One planned file is the exception: the `HISS-11` entry adoption writes for the adopter's release
workflow `release.yml`, still to be added, passes while no workflow writes provenance. The audit asks the code that writes that entry, and once a provenance workflow
exists the entry fails like any other stale one.

Inspect declared exceptions and their remaining validity with `praetorctl exceptions list`
(`cmd/standardsctl/exceptions.go`, `TestExceptionsList_Positive_ListsEntries`):

```bash
praetorctl exceptions list
```

Filter entries due to expire soon by passing `--expiring-within=<days>`
(`TestExceptionsList_Positive_FiltersByExpiringWithin`, `TestExceptionsList_Boundary_ExactDaysAndZero`):

```bash
praetorctl exceptions list --expiring-within=14
```

The governance audit (`praetorctl audit`) warns 14 days before an exception expires
(`config.ExceptionExpiryWarningDays`, `cmd/standardsctl/audit_exceptions.go`,
`TestAuditExceptionsExpiry_3D`, `TestAudit_ExceptionsExpiryWarning_Integration`), prompting
maintainers to address or renew the debt before the owning gate fails upon expiry:

```text
[WARN] Exceptions: the exceptions entry (rule HISS-11, .github/workflows/rel.yml) expires on 2026-10-24 (14 days left): Level 2 builder
```

## Reading the output

A pass names what each lane read:

```text
[PASS] clang-tidy translation-unit coverage: all 120 tracked translation units read by a lane (cpu: 98, metal: 14) or excused by a live exception (8).
```

A failure lists one line per file or entry before the summary:

```text
  - src/planted.cc: read by no clang-tidy lane and named by no exceptions entry
  - src/win32/getopt.c: read by no clang-tidy lane; its exception expired on 2026-10-05
  - exceptions entry src/gone.c (clang-tidy-coverage): excuses no translation unit that every lane leaves unread; remove the entry
```

A gate that cannot run reports
`[FAIL] clang-tidy translation-unit coverage could not run:` with the cause, such as
`clang-tidy lane cpu: compile database build/compile_commands.json cannot be read`.

## Not yet on this list

Two older exception mechanisms keep their own formats: the npm audit gate reads
`.config/security/npm-audit-exceptions.json` (`scripts/npm_audit_gate.py`), and the HISS-01
cleanup-`goto` exception is declared under `hiss.exceptions` with its own document. Both could
move onto the top-level `exceptions` list as further rules; neither is migrated yet.
Like `.standards.yaml` exceptions, `scripts/npm_audit_gate.py` warns 14 days before an
active npm audit exception expires (`EXCEPTION_EXPIRY_WARNING_DAYS`).
