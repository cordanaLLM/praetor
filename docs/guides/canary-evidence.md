# Canary execution and evidence

`praetorctl bump canary <package> --target=<version> --dry-run` returns a plan
without creating a worktree, updating dependencies, or executing tests. Its
`status` is `planned`, `success` is false, and `canary_certified` is false.
A successful plan command may exit zero; that does not establish a passing canary.

An executed canary uses an isolated worktree and the configured test command.
`status: passed` and `success: true` mean that command exited zero after the
dependency update. They do not prove every invariant, compatibility, or readiness
for release. The current runner issues no certification: `canary_certified` stays
false for planned, passing, and failing requests. CLI and train output make this
distinction explicit.

Update and test failures return `bump.ErrCanaryFailed` through wrapping compatible
with `errors.Is`; cancellation remains inspectable through its context error.
Command output is bounded to 64 KiB per stream. Overflow fails the attempt, and
retained output can be partial. Failures expose private SARIF diagnostics through
`diagnostic_path`, with a bounded summary in `distilled_errors`. ANSI output is
encoded as JSON correctly. Failure to retain diagnostics is an error; a file path
is reported only after successful storage.

Diagnostics are stored under `.workingdir/evidence/canary/`. They contain command
output and must remain private. `staged_patch_path` is retained for API compatibility
but stays empty: the runner does not synthesize adaptation patches.

`praetorctl bump apply <package> --version=<version> --patch=<file>` reads a regular,
non-symlink patch of at most one MiB before changing dependencies. Missing,
oversized, invalid-text and invalid-diff inputs return `bump.ErrInvalidPatch`.
Relative patch paths are resolved from the caller's working directory. The command
retains a private snapshot and checks its diff syntax with `git apply --numstat`;
the same captured bytes are applied after updating the dependency. This preserves
patches that intentionally target the updated manifest and avoids rereading a
source patch that changed during the update.

Syntax validation does not establish applicability. If a syntactically valid patch
fails to apply after the dependency update, the command exits nonzero and reports
that the update has already happened. It does not roll back or claim an atomic
update. This command does not admit or verify certification evidence.

The default test command remains `go test -v ./...`; callers using the Go API can
provide an explicit command for another runtime. Commands are split into argv on
whitespace, without shell quoting. Runtime-specific test selection and evidence
admission remain separate work. There is currently no registered MCP canary tool.

## Migration

Do not use old dry-run success flags or the `canary_certified` Boolean as evidence
that tests ran. Use the explicit status and handle non-nil execution errors.
Train failures now propagate a nonzero result. Replace any generated diagnostic
`.patch` files with reviewed actual diffs; those old files are rejected before
dependency updates. Valid patches still apply after dependency updates.
