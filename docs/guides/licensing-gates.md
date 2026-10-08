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

- `.github/workflows/reuse.yml`, whose **REUSE lint** job runs `fsfe/reuse-action` within
  `timeout-minutes: 10` and becomes a required status check of the branch ruleset adoption
  renders. Both `actions/checkout` and `fsfe/reuse-action` are pinned by full commit SHA with a
  version comment (`supplychain.ReuseActionPinnedRef` and the checkout pin from
  `tools/markdownlint/assets.go`, HISS-11), satisfying strict SHA-pinning organization policies.
  It has the hosted gate shape every emitted gate shares
  (`internal/ghworkflow/hostedgate.go`): it runs on pull request activity and on a push to the
  default branch that ruleset protects, its first step fails a draft run by design (on `bash`,
  so it behaves the same on every runner), and every later step runs only when the pull request
  is not a draft. The branch is
  `repository.default_branch` of `.standards.yaml`, else the origin HEAD the checkout records,
  else `main` (`forge.RepositoryDefaultBranch`), written as `branches: ['master']`;
- a `reuse-lint` pre-commit job in `lefthook.yml` that runs `reuse lint` where reuse 6.x is
  installed, skips with the reason where reuse is not installed, and fails, naming the pin, on
  another reuse major.

`praetorctl bump` expects the same tag, and praetor's own Compliance workflow runs it
(`TestReuseActionPinMatchesRegistryAndEngine` in `internal/bump/release_pins_test.go`).

The workflow follows the root on every adoption. Praetor's unedited rendering, for the current
default branch or another one, is refreshed to the current default branch without `--force`.
When the root loses both `REUSE.toml` and `LICENSES/`, adoption removes the unedited workflow,
so the ruleset it renders next stops requiring a check that `reuse lint` would fail on every
pull request, and drops the `reuse-lint` job from an unedited `lefthook.yml`. An edited
`reuse.yml` is kept, `--force` included, and adoption warns that its check now fails
(`TestAdopt_ReuseGateFollowsTheDefaultBranch` and `TestAdopt_ReuseGateRemovedWithTheMarkers` in
`internal/adopt/reuse_gate_test.go`).
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
next character literal (`ReuseShadowedPaths` in
[`internal/supplychain/reuse.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse.go)
and `reuseGlobCheck.includes` in `reuse_glob.go`, the one coverage decision every licensing check
uses). Tests: `TestReuseGlobIncludes_3D` and `TestReuseShadowedPaths_*`, `_Union` among them,
in `internal/supplychain/reuse_shadow_test.go`, and `TestAuditReuseRecords_3D` in
`cmd/standardsctl/audit_reuse_test.go`.

### What the audit reads

Both audit checks read `REUSE.toml` against an allow-list, following what the reuse tool parses
(`ReuseTOML.from_dict` and `AnnotationsItem.from_dict` in `reuse/global_licensing.py`), stepping over
metadata keys and tables while refusing shapes that could define untracked annotations
(`TestReuseOtherKeysAndTablesPositive` in
[`internal/supplychain/reuse_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse_test.go)
and `TestAuditLicensing_Issue896` in
[`cmd/standardsctl/audit_reuse_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/audit_reuse_test.go)):

- every top-level key, `version` included, is stepped over whatever its value holds, except
  `annotations` and dotted keys starting with it, which are refused;
- `[[annotations]]` opens an annotation table, while every other table or array-of-tables header
  opens an ignored table whose keys, `annotations = ...` and `annotations.x` among them, are all
  stepped over and never reach the annotation table before it;
- inside each `[[annotations]]` table, `path` and `SPDX-License-Identifier` are read (each a
  single-line string or an array of them), while `precedence`, `SPDX-FileCopyrightText` and other
  keys are stepped over whatever they hold (`util.TOMLValueScan` in
  [`internal/util/toml.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/util/toml.go)).

Header and key lines are tokenized by one function, `util.TOMLKeyPath`, following the key grammar of
TOML 1.0 (toml.io/en/v1.0.0). A key is one or more segments joined by dots; a segment is a bare key
(`A-Z a-z 0-9 _ -`), a basic string without any backslash, or a literal string. Spaces and tabs are
allowed between tokens only, never inside a bare key or a quoted one. A header is `[ key ]` or
`[[ key ]]` with optional spaces and tabs inside the brackets, then nothing or a comment. The
decoded first segment is compared exactly: `[[annotations\t]]`, `[[ annotations ]]` and
`[["annotations"]]` open an annotation table, as REUSE reads them, while `[["annota tions"]]` and
`[[" annotations"]]` name other keys and open ignored tables, as REUSE ignores them
(`TestReuseHeaderTokenizerPositive`, confirmed with `reuse lint` where the tool is installed and
skipped, stating why, where it is not).

Refused shapes fail both checks with the line and the allowed shape: a header or key line that does
not tokenize (an unclosed bracket or quote, text after a header, a bare key character outside the
set above, a backslash in a quoted key, whitespace other than space and tab); `annotations` and
`annotations.*` at top level; `[annotations]`, `[annotations.x]`, `["annotations".x]` and
`[[annotations.x]]`; and dotted `path`, `precedence`, `SPDX-FileCopyrightText` or
`SPDX-License-Identifier` keys inside an annotation table (`TestReuseHeaderTokenizerNegative` and
`TestReuseHeaderTokenizerBoundary` in
[`internal/supplychain/reuse_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/reuse_test.go),
`TestTOMLKeyPathNegative` in `internal/util/toml_test.go`).
Annotations written as an inline array of tables, which REUSE accepts, fail this way:

```text
[FAIL] REUSE.toml annotation order not checked: REUSE.toml:2: the top-level key annotations is not one this read follows: write each annotation as a table of its own, opened by a [[annotations]] line and holding one key = value per line, not as an inline array of tables or dotted keys
```

Rewrite each element as an `[[annotations]]` table. Dotted keys, such as `annotations.path`, fail
the same way. A basic string's escape sequences are decoded as TOML defines them, so
`path = "a\\*"`, the escaped star of the REUSE specification, reads as the glob `a\*`, as the
literal string `path = 'a\*'` does. A multi-line string as a `path` or `SPDX-License-Identifier`,
an escape sequence TOML does not define, and a value whose end the read cannot find fail both
checks rather than passing them unchecked (`TestReuseAnnotationTablesPositive` and
`TestReuseAnnotationTablesNegative` in `internal/supplychain/reuse_test.go`, and
`TestAuditLicensing_3D`).

### The comparison bound

Comparing globs explores the product of their automata. Every check spends at most
16,777,216 automaton steps (`maxReuseGlobSteps` in `reuse_glob.go`), one step for each byte one
glob reads, so the bound measures the real cost, not the number of tables: a file listing hundreds
of vendored files, one table each, stays far below it (`TestReuseShadowedPaths_Boundary`). A file
whose comparisons need more is not answered in part: the check fails as not checked, naming the
annotation, the path and the bound (`TestReuseGlobCheck_Budget`,
`TestReuseShadowedPaths_PastTheBound`). Merge its globs into fewer tables, or, while you do, keep
the file unchecked with an exceptions entry in `.standards.yaml`:

```yaml
exceptions:
  - rule: "reuse-annotation-order"
    path: "REUSE.toml"
    reason: "4000 generated vendored file globs; merging them tracked in the backlog"
    expires: "2026-12-31"
```

The audit then prints the order as not checked, with the entry's reason and expiry, and never as a
pass. An expired entry excuses nothing, and an entry for a file the audit checks in full is stale
and fails the check until it is removed (`TestAuditReuseRecords_Bound`).

## One root licence

Forges and package indexes read the licence from the files at the repository root, and the REUSE
specification exempts `COPYING`, `LICENSE` and `LICENCE`, with any `-` or `.` suffix, from
labelling. GitHub's licence detection, Licensee, reads more names as licence files
(`FILENAME_REGEXES` in `lib/licensee/project_files/license_file.rb`). A repository that keeps `LICENSES/` therefore states one licence at its root, and
`praetorctl audit` checks it (`CheckRootLicense` in
[`internal/supplychain/root_license.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/supplychain/root_license.go)):

- **The declared licence** is the one SPDX identifier of the last `REUSE.toml` annotation whose
  paths cover the whole tree, such as `path = "**"` or
  `path = ["*", ".*", "*/**", ".*/**"]`; without such an annotation, the one text
  `LICENSES/` holds. A whole-tree annotation naming an expression, such as `MIT OR Apache-2.0`,
  or a `LICENSES/` with several texts and no whole-tree annotation, declares no single licence,
  and the check skips, saying so.
- **`LICENSE`** must be a regular file holding the text of `LICENSES/<id>.txt` for that licence.
  The comparison allows one consistent line-ending style, as every other text comparison of the
  audit does, so a CRLF checkout passes; a text with mixed line endings is compared byte for
  byte, and the finding says so. A `LICENSE` that is a symbolic link, such as one to
  `LICENSES/<id>.txt`, or a directory is a finding that asks for a regular copy: an archive or a
  forge may not follow the link.
- **Every other root file named like a licence** fails the check unless the manifest keeps it as
  an upstream notice. Names compare without case, and a name counts when REUSE or Licensee reads
  it as a licence (`licenceNameForms`): `COPYING`, `LICENSE.md`, `LICENCE`, `LICENSE-MIT`,
  `LICENSE_MIT`, `UNLICENSE`, `MIT-LICENSE`, `COPYING_x`, `COPYRIGHT`, `OFL` and `PATENTS`, with
  the extensions Licensee accepts after them. Directories are not read. A notice under another
  name, such as `NOTICE`, is not a licence file, and neither is `copyright.go`, whose extension
  Licensee refuses (`TestLicenceNamed_3D`).

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
