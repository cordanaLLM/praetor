# Licensing gates

A repository that declares its licensing the [REUSE](https://reuse.software/spec-3.3/) way, with
`REUSE.toml` or a `LICENSES/` directory at its root, gets two checks from Praetor:

1. [`reuse lint`](#reuse-lint-in-hooks-and-ci) before every commit and in CI, written by
   `praetorctl adopt`;
2. the [annotation order check](#annotation-order) of `praetorctl audit`, which fails an
   override in `REUSE.toml` that never takes effect.

A repository with neither marker gets neither, and the audit says that it skipped the check.

## `reuse lint` in hooks and CI

Adoption writes two jobs, both at the one REUSE pin, `supplychain.ReuseActionVersion` in
[`internal/supplychain/reuse_lint.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse_lint.go):

- `.github/workflows/reuse.yml`, whose **REUSE lint** job runs `fsfe/reuse-action@v6` and
  becomes a required status check of the branch ruleset adoption renders;
- a `reuse-lint` pre-commit job in `lefthook.yml` that runs `reuse lint` where reuse 6.x is
  installed, skips with the reason where reuse is not installed, and fails, naming the pin, on
  another reuse major.

`praetorctl bump` expects the same tag, and praetor's own Compliance workflow runs it
(`TestReuseActionPinMatchesRegistryAndEngine` in `internal/bump/release_pins_test.go`).
[Fast adoption](../adoption.md#what-adoption-scaffolds-automatically) lists how adoption
treats an existing workflow and how to decline it; [git hooks](git-hooks.md) lists the other
pre-commit jobs.

## Annotation order

REUSE 3.3 applies to a file only the last `[[annotations]]` table of a `REUSE.toml` whose
`path` matches it, "exclusively the last matching table in the file", whatever the table's
`precedence`. `precedence = "override"` and `"aggregate"` only decide between a table and the
licensing information inside the file or in another `REUSE.toml`. An override placed before a
whole-tree default table is therefore relabelled by the default, and `reuse lint` still passes,
because every file still carries licensing information.

`praetorctl audit` fails when a path of an annotation is matched whole by the path of a later
annotation, and names both:

```text
  - REUSE.toml annotation 1 path "vendor/upstream/**" never takes effect: annotation 2 path "**", after it, matches every file it names, and REUSE applies only the last matching annotation, whatever its precedence; move annotation 1 after annotation 2
[FAIL] REUSE.toml annotation order: 1 path(s) resolve to a later annotation, not their own
```

Put the default table first and every override after it. A later path that matches only some
of an annotation's files, such as `**/*.md` after `docs/**`, leaves the rest in effect and is
not reported. Paths compare as REUSE globs: `*` stops at `/`, `**` crosses it, and `\` makes the
next character literal (`ReuseShadowedPaths` and `reuseGlobIncludes` in
[`internal/supplychain/reuse.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse.go)
and `reuse_glob.go`). A `REUSE.toml` the audit cannot read, such as one with a multi-line string
as a path, fails the check rather than passing it unchecked. Tests: `TestReuseShadowedPaths_*` in
`internal/supplychain/reuse_shadow_test.go` and `TestAuditReuseRecords_3D` in
`cmd/standardsctl/audit_reuse_test.go`.
