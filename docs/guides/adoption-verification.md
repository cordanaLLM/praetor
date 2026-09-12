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
bounded to 4,096 entries, six directory levels, 128 metadata files, 64 KiB per
metadata file and 2 MiB in aggregate. Generated dependency/build trees are omitted.
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
[BUG-651 and BUG-438 remain tracked](../../.workingdir/BUGS.md). Archetype catalogs
and these downstream consumers still require separate reconciliation; a C# command
plan does not imply a configured .NET development container or complete C# scanning.
