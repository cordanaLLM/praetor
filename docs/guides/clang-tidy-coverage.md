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

Then run the gate where the lanes' inputs exist, for example in the CI job that has just
configured the build:

```bash
praetorctl ci tidy-coverage --dir=.
```

## When it runs

| Where | Runs when | Otherwise |
| :--- | :--- | :--- |
| `praetorctl audit` | the resolved policy's `linters` name `clang-tidy`, as the `native-gpu-systems` profile does, or `.standards.yaml` declares `clang_tidy` | prints `[SKIP] clang-tidy translation-unit coverage not checked:` with the reason, neither a pass nor a failure |
| `praetorctl ci tidy-coverage [--dir=.]` | always | — |

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
nothing (`TestCheck_Negative_UnreadableLaneFailsClosed`). A compile database is a build output,
so the audit fails in a checkout where it has not been written. For hooks that run before a
build, track a `files` list, such as the units a clang-tidy run last measured, and keep the
compile database lane for the CI job that builds.

## Exceptions

The top-level `exceptions` list is the repository's one declared per-file exception list
(AGENTS.md rule 14). The gate reads the entries whose `rule` is `clang-tidy-coverage`.

| Key | Rule |
| :--- | :--- |
| `rule` | A rule some gate reads from the list; today only `clang-tidy-coverage`. |
| `path` or `glob` | Exactly one. `path` names one clean repository-relative file without glob characters. `glob` follows the `docs_surfaces` glob rules: `*` stays inside one segment and a `**` segment spans any number. |
| `reason` | One line, at most 1024 bytes. |
| `expires` | A `YYYY-MM-DD` date at most 90 days after today, the bound `scripts/npm_audit_gate.py` applies to its own list. The entry holds through that day. |

`LoadManifest` refuses a malformed entry, and the gate applies the same validator,
`config.ValidateExceptions` in `internal/config/exceptions.go`
(`TestValidateExceptionsNegative`, `TestValidateExceptionsBoundary`). An expired entry is still
a valid declaration: the gate treats it as missing and fails on its files. An entry that
excuses no unit every lane leaves unread, because a lane now reads the file or the file is gone,
is stale and fails until it is removed (`TestCheck_Negative_ExpiredAndStaleExceptions`).

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
