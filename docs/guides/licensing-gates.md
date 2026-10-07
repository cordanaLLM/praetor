# Licensing gates

A repository that declares its licensing the [REUSE](https://reuse.software/spec-3.3/) way, with
`REUSE.toml` or a `LICENSES/` directory at its root, gets three checks from Praetor:

1. [`reuse lint`](#reuse-lint-in-hooks-and-ci) before every commit and in CI, written by
   `praetorctl adopt`;
2. the [annotation order check](#annotation-order) of `praetorctl audit`, which fails an
   override in `REUSE.toml` that never takes effect;
3. the [root licence check](#one-root-licence) of `praetorctl audit`, which holds the root to one
   `LICENSE` with the declared licence's text.

A repository with neither marker gets none of them, and the audit says that it skipped each. No
profile writes a `LICENSE` or `LICENSES/` for the repository, so the checks follow the markers,
whatever the profile.

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

`praetorctl audit` fails when the paths of later annotations together match every file a path
of an annotation names, and names the later annotation that completes the match:

```text
  - REUSE.toml annotation 1 path "vendor/upstream/**" never takes effect: annotation 2 path "**", after it, matches every file it names, and REUSE applies only the last matching annotation, whatever its precedence; move annotation 1 after annotation 2
[FAIL] REUSE.toml annotation order: 1 path(s) resolve to a later annotation, not their own
```

A default spelled as several globs, such as `path = ["*", ".*", "*/**", ".*/**"]`, is listed
with all of them. When no one later annotation matches every file but several do between them,
the finding names the range, such as `annotations 2 to 4, after it, together match every file it
names`, and asks to move the annotation after the last of them.

Put the default table first and every override after it. Later paths that match only some of an
annotation's files, such as `**/*.md` after `docs/**`, leave the rest in effect and are not
reported. Paths compare as REUSE globs: `*` stops at `/`, `**` crosses it, and `\` makes the
next character literal (`ReuseShadowedPaths` and `reuseGlobIncludes` in
[`internal/supplychain/reuse.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse.go)
and `reuse_glob.go`). A `REUSE.toml` the audit cannot read, such as one with a multi-line string
as a path, fails the check rather than passing it unchecked. Tests: `TestReuseGlobIncludes_3D`
and `TestReuseShadowedPaths_*`, `_Union` among them, in
`internal/supplychain/reuse_shadow_test.go`, and `TestAuditReuseRecords_3D` in
`cmd/standardsctl/audit_reuse_test.go`.

## One root licence

Forges and package indexes read the licence from the files at the repository root, and the REUSE
specification exempts `COPYING`, `LICENSE` and `LICENCE`, with any `-` or `.` suffix, from
labelling. A repository that keeps `LICENSES/` therefore states one licence at its root, and
`praetorctl audit` checks it (`CheckRootLicense` in
[`internal/supplychain/root_license.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/root_license.go)):

- **The declared licence** is the one SPDX identifier of the last `REUSE.toml` annotation whose
  paths cover the whole tree, such as `path = "**"` or
  `path = ["*", ".*", "*/**", ".*/**"]`; without such an annotation, the one text
  `LICENSES/` holds. A whole-tree annotation naming an expression, such as `MIT OR Apache-2.0`,
  or a `LICENSES/` with several texts and no whole-tree annotation, declares no single licence,
  and the check skips, saying so.
- **`LICENSE`** must hold the text of `LICENSES/<id>.txt` for that licence. The comparison allows
  one consistent line-ending style, as every other text comparison of the audit does, so a CRLF
  checkout passes; a text with mixed line endings is compared byte for byte, and the finding
  says so.
- **Every other root file named like a licence**, such as `COPYING`, `LICENSE.md`, `LICENCE` or
  `LICENSE-MIT`, compared without case, fails the check unless the manifest keeps it as an
  upstream notice. Directories are not read. A notice under another name, such as `NOTICE`, is
  not a licence file.

To keep an upstream notice, name it in the top-level `exceptions` list of `.standards.yaml`, one
file per entry, by `path` and at the root, with the reason and an expiry at most 90 days ahead,
as for the [clang-tidy coverage gate](clang-tidy-coverage.md#exceptions):

```yaml
exceptions:
  - rule: "root-license-notice"
    path: "COPYING"
    reason: "upstream GPL notice the fork keeps verbatim"
    expires: "2026-12-31"
```

An expired entry keeps nothing, and an entry that keeps no root licence file is stale and fails
until it is removed:

```text
  - COPYING: a second root licence file; its root-license-notice exception expired on 2026-10-06
  - exceptions entry LICENSE-MIT (root-license-notice): keeps no root file named like a licence; remove the entry
[FAIL] root licence: 2 problem(s) with the one root licence of a repository declaring EUPL-1.2
```

Tests: `TestCheckRootLicensePositive`, `TestCheckRootLicenseNegative` and
`TestCheckRootLicenseBoundary` in `internal/supplychain/root_license_test.go`,
`TestValidateExceptionsRootLicenseNotice` in `internal/config/exceptions_test.go`, and
`TestAuditLicensing_3D` in `cmd/standardsctl/audit_reuse_test.go`.
