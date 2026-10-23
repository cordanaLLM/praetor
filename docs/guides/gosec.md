# gosec findings and dated exceptions

The Security stage rejects gosec findings unless an existing baseline or a live, per-file
exception covers them. Resolve each finding by fixing the code, adding a justified
`#nosec Gxxx -- reason` comment at its line, or declaring a dated exception in `.standards.yaml`.
The scanner configuration stays `.gosec.json`; exceptions do not change its enabled rules.

## Record an existing baseline

`praetorctl baseline --record` refuses a first `.gosec-baseline.json` when findings exist.
It compares the scan with the baseline on the repository's default branch, resolved by
`forge.RepositoryDefaultBranch`: the manifest declaration, the recorded origin HEAD, then
`main`. A present origin tracking ref takes precedence over the local branch. No network fetch
runs; refresh the checkout's refs before recording when they are out of date.

An existing baseline can keep or reduce its count. A hand-raised working-tree baseline cannot
increase the default branch's allowance. The working-tree baseline also cannot grow after it
has shrunk. The flags `--allow-gosec-increase` and `--gosec-reason` remain parseable for command
compatibility, but `--allow-gosec-increase` is refused. HISS baseline increase flags retain their
separate behavior. A clean first scan creates no gosec baseline.

These checks live in `cmd/standardsctl/gosec_baseline_guard.go`; the planted fixture tests are
`TestGosecFirstBaselineGuard*` in `cmd/standardsctl/gosec_baseline_guard_test.go`.
Adoption writes `.gosec.json`, but does not write a gosec baseline.

## Declare one file's exception

```yaml
exceptions:
  - rule: gosec
    path: internal/legacy/command.go
    gosec_rules: [G204]
    reason: Replace the legacy command interface before this exception expires
    category: C
    entered: 2026-10-23
    expires: 2026-11-02
```

The strict manifest schema is `config.Exception` in `internal/config/exceptions.go`.
`gosec_rules` requires distinct IDs, each an uppercase `G` followed by exactly three digits.
The entry requires one clean repository-relative path with forward slashes; globs are refused.
It also requires a reason, explicit category, entry date and expiry. Category B allows at most
3 days from `entered`, C allows 10, D allows 120, and A uses the stated interval up to 90 days. Existing
category and date validation applies (`internal/config/gosec_exceptions.go` and
`tools/secrets/rules`). See the [exception fields](clang-tidy-coverage.md#exceptions).

## Security verdict

Security runs gosec first, then removes findings whose path and rule ID match a live entry
before applying the baseline ratchet. Expiry is inclusive: an entry still applies on its
`expires` day; the following day it excuses nothing. Remaining findings face the same ratchet
as findings without an exception.

The stage message names each applied entry, its path, rule IDs, reason, expiry and count of
excused findings. An entry matching no finding fails as stale and names the entry to remove.
Staleness is checked after all Go modules, so an exception for a nested module is not judged
against only the root module's scan. Removing an entry restores the finding to the ratchet.

`internal/gating/gosec_exceptions.go` implements the filter and stage accounting.
`TestGosecExceptions_FrozenStage` supplies frozen scanner output and checks live, removed,
expired, stale, wrong-path, wrong-rule and expiry-day cases. The runner rejects unexpected
commands, so the test does not depend on downloading or executing security scanners.
