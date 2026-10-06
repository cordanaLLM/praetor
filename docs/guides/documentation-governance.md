# Documentation governance

Praetor treats public Markdown as a governed release surface. The locked gate
checks formatting and refuses links from public documentation into private
session artifacts.

Run the same gate locally and in CI:

```bash
make docs-lint
```

`make verify-all` includes this target. `make docs-lint-test` replays the gate's
positive, negative, and boundary fixtures.

## Files the gate checks

The inventory comes from Git, not from an unrestricted filesystem walk. It
contains every tracked and non-ignored untracked file ending in `.md`,
`.markdown`, `.mdx`, `.md.tmpl`, `.markdown.tmpl`, or `.mdx.tmpl`, including the
root `README.md` and Markdown/MDX template sources. That catches a new guide,
component page, or README template before its first commit without reading
ignored scratch output. The private-link rule checks this complete inventory
before any style-only exclusions are applied, so a tracked generated surface
cannot hide a link into private state. Every quoted JSX prop and every static
string or no-substitution template inside a brace-wrapped MDX prop is checked as
a possible destination. Standard URL attributes also reach the embedded-HTML
parser. Static MDX `import` and re-export sources are checked too.
Dynamic `import()` sources are checked in ESM, flow/text expressions, and JSX
property expressions. A dynamic import whose target is not a string literal
fails closed because the gate cannot prove what it ingests.
Known URL/path props backed by non-literal JSX expressions, and JSX spread
attributes that could introduce one, fail closed as unsupported instead of
being silently skipped.

The style linter excludes generated agent projections (`AGENTS.md`, `CLAUDE.md`,
and their vendor directories), generated `CHANGELOG.md`, caveman fixtures,
private scratch/worktree trees, and dependency/vendor trees. Those files are not
public author-written documentation: they have a generator, fixture-byte,
caveman, or upstream format contract. Except for ignored untracked content, they
remain in the broader private-link scan. A repository adds its own style
exclusions for partial, generated, or fixture Markdown in `.standards.yaml`
([Repository settings](#repository-settings)). The runner refuses symbolic-link inputs
and paths escaping the repository root; the self-test creates a Markdown symlink
and proves the inventory fails closed. It also reads tracked symlink targets from
the Git index and rejects an alias into either private scratch root, including an
alias consumed by a snippet or image link when the ignored target is absent from
the checkout.

The inventory is bounded to 4,096 files, 1 MiB per file, and 64 MiB total by
default. A repository with a larger documentation tree raises the first two in
`.standards.yaml` ([Repository settings](#repository-settings)); the 64 MiB
aggregate bound is fixed. A bound being exceeded is an error, not a partially
successful check, and the message names the setting that raises it. The Git
index scan is bounded to 65,536 entries and 2,048 symlinks; each symlink target
is bounded to 4,096 bytes.

## Locked Markdown rules

`tools/markdownlint/package-lock.json` pins the complete Node dependency graph.
The runner copies the canonical tool assets to a temporary directory and
executes `npm ci --ignore-scripts --no-audit --no-fund`. It does not use `npx` or
leave a source-tree `node_modules/` directory. The style rules come from the
[markdownlint](https://github.com/DavidAnson/markdownlint) library, which the
runner calls itself rather than through the `markdownlint-cli2` command it ran
before (#736). It starts one child process of its own script per batch of
files, `node tools/markdownlint/verify.mjs --lint <install> <file>...` from the
repository root (`runMarkdownlint` and `lintChild` in
`tools/markdownlint/verify.mjs`). Each batch gets a fresh heap, a time budget
(120 s unless `lint_timeout_seconds` says otherwise,
[Lint time budget](#lint-time-budget)) and a bounded output capture. Before it
starts the first child, the runner checks that
the installed `markdownlint` is the version the lock pins and exposes the
synchronous entry the children import (`markdownlintLibrary`). A mismatch fails
the gate with status 2, also when no file is selected for styling
(`lintEntrySelfTest`). The runner reads that version from the lock, so a pin
update leaves `verify.mjs` unchanged. The temporary installation is removed
after success or failure.

A finding prints as `markdownlint-cli2`'s default formatter printed it, one line
per finding on standard error:

```text
docs/guide.md:1:1 error MD018/no-missing-space-atx No space after hash on atx style heading [Context: "#Guide"]
```

The line holds the path, the line number, the column when the rule reports one,
the severity, the rule's names, its description, and any detail and context.
Findings are ordered by path, line and rule name, as before. On the same files
the output is byte for byte what `markdownlint-cli2` 0.23.3 printed:
`lintOutputSelfTest` replays a fixture set captured from it. The one difference
is that the `markdownlint-cli2 v0.23.3 (markdownlint v0.41.1)` banner line is
no longer printed. Inline `<!-- markdownlint-configure-file ... -->` comments
are parsed as JSONC, TOML or YAML, in that order, as `markdownlint-cli2` parsed
them (`configurationParsers`).

The lock installs no package with a known high or critical advisory, except one
that has no fixed release and a reviewed exception. The
`Go Vulnerability & AST Security Scan` job in `.github/workflows/security.yml`
runs `scripts/npm_audit_gate.py` on it for every pull request and daily. The
script runs `npm audit --package-lock-only --json` and fails on every high or
critical advisory that `.config/security/npm-audit-exceptions.json` does not name
for this lock, with a reason and an expiry date at most 90 days ahead. An
expired exception fails like a missing one. The Markdown gate's lock needs no
exception: it no longer installs braces (GHSA-vfj7-8cjw-p6xm, no fixed release),
which `micromatch` and `markdownlint-cli2` pulled in (#736).
`TestPackageLockInstallsNoBraces` in `tools/markdownlint/assets_test.go` keeps
all three out. `TestMarkdownGateLockClearsFixedAdvisories` in
`internal/supplychain/npm_advisories_test.go` keeps `smol-toml` and `js-yaml` at
or above the versions that fixed their advisories (#643).
Because audit locks the lock byte for byte, an adopter cannot patch it: a fix
ships as a new Praetor text, and a plain `praetorctl adopt` replaces an
unedited earlier lock, `package.json` and `verify.mjs` without `--force`
(`priorDigests` in `tools/markdownlint/assets.go`).

The locked configuration is hermetic. The lint child reads the rules from the
`config` mapping of `tools/markdownlint/markdownlint-cli2.yaml` and hands the
library each file's text as a string, so the library opens no file and finds no
configuration of its own. The file keeps its name and the `noProgress` key
`markdownlint-cli2` read, so adopted copies need no change; any other key is
refused (`lintConfiguration`). `markdownlint-cli2` had no option that turned
configuration discovery off: it read `.markdownlint-cli2.{jsonc,yaml,cjs,mjs}`
and `.markdownlint.{jsonc,json,yaml,yml,cjs,mjs}` from every directory down to
a linted file, a `.markdownlint.*` file replaced the locked rules, and the
`.cjs` and `.mjs` forms ran repository code (#533). Nothing in the gate looks
for those files now. A repository may keep its own `.markdownlint.json` for
other tooling; the gate ignores it whether it loosens or tightens the rules,
and never executes a configuration module. `hermeticConfigSelfTest` proves both
directions with every configuration file form in the fixture tree.

The private-link rule also runs from the temporary installation directory. It
acts only when started as a script, which `invokedAsScript` in
`tools/markdownlint/no-private-scratch-links.mjs` decides by comparing real paths:
Node loads a main module from its real path, and every macOS temporary directory
sits under the `/var` symlink, so comparing spellings let the rule exit 0 there
without reading a file. The self-test runs the rule a second time through a
symlinked path to the temporary directory, so that failure shows on every host.

On Linux and macOS the runner starts `npm` from `PATH`. On Windows it cannot:
Node refuses to spawn the `npm.cmd` batch shim without a shell and fails with
`EINVAL` (CVE-2024-27980), and passing arguments through a shell is deprecated
(DEP0190). The runner therefore starts the `node_modules/npm/bin/npm-cli.js`
that the Windows Node distribution installs beside `node.exe`, through the
running Node binary. If that file is absent, the gate fails and names the path it
expected. `npmInvocation` in `tools/markdownlint/verify.mjs` holds this rule, and
`make docs-lint-test` replays it for both platform families on every host. The
Windows leg of `.github/workflows/portability.yml` runs the same self-test,
including the real locked install.

Praetor pins `tools/markdownlint/*` to LF in its own `.gitattributes` because
those source files are Go-embedded bootstrap inputs. Adopted copies may use
either consistent LF or CRLF checkout text. Audit compares their normalized
canonical content and rejects mixed endings, lone carriage returns, and any
other content drift.

Child-process output is captured rather than inherited without a bound. The
runner emits at most 200 diagnostic lines or 64 KiB, then reports truncation and
returns an infrastructure-error status. The private-link rule reports at most 64
findings and emits `PRAETOR-MD002` at the first additional finding. Destination
fields are shortened in diagnostics, not in comparison logic.

The configuration enables the standard `markdownlint` rules with deliberate
exceptions for line length, repeated sibling headings, front-matter titles, and
the HTML used by the documentation site. Change the canonical configuration and
lock together. Audit compares emitted assets as canonical text under the line
ending policy above; adoption preserves existing files unless `--force`
explicitly refreshes Praetor-owned assets, and reports a preserved asset that
differs from the canonical text as drift with a warning rather than as verified.
One exception needs no `--force`: a file holding exactly a text an earlier
Praetor shipped at that path is Praetor's own unedited output, so plain
`praetorctl adopt` refreshes it in its line-ending style. Audit fails on such a
file and names plain adoption as the repair. The family's `Prior` digests
(`priorDigests` in `tools/markdownlint/assets.go`, read through
`internal/managedasset/family.go`) list every earlier text of each managed
path: the workflow, `package.json`, `package-lock.json`,
`markdownlint-cli2.yaml`, `verify.mjs` and `no-private-scratch-links.mjs`.
`tools/markdownlint/testdata/prior/` holds each
one (`TestPriorDigestsReproduce`,
`TestAdoptionDocumentationGateRefreshesPriorTexts`).

The list stays complete because every text the family ever shipped is recorded
in `internal/managedasset/testdata/shipped/markdown.sha256`, oldest first, one
`<sha256>  <path>` line each. The last line of a path must be its current text,
and every earlier line must be in `priorDigests`, in both directions
(`TestShippedTextLedger`). Changing a managed text therefore takes three steps,
and CI fails until all three are done:

1. Change the text (a Renovate pin update does this for the workflow, and for
   `package.json` and `package-lock.json`).
2. Append its digest:
   `PRAETOR_UPDATE_SHIPPED_TEXTS=1 go test ./internal/managedasset -run 'TestShippedTextLedger$'`.
   The step only appends; it never rewrites or drops a line.
3. Record the outgoing text: add its digest to `priorDigests` and the text to
   `tools/markdownlint/testdata/prior/`. The test failure names the digest and
   the file whose history holds the text.

An npm pin update also moves what records the lock's contents, so a Renovate
pin pull request for the gate fails CI until these follow: the adoption
goldens under `internal/adopt/testdata/managed-family/` (rewrite them with
`PRAETOR_UPDATE_GOLDEN=1 go test ./internal/adopt -run Golden`), the direct pins
in `TestPackageLockPinsEveryInstalledPackage`
(`tools/markdownlint/assets_test.go`), `.needs.yaml`
(`praetorctl needs scan --write`), and the npm table of `THIRD-PARTY-NOTICES.md`
(`praetorctl sbom notices`, which stops on a package without a row or under a
license outside `knownNoticeLicenses` in `internal/supplychain/notices.go`).

## Repository settings

A repository tunes the gate in the `documentation` block of `.standards.yaml`.
Every key is optional, and an absent block keeps the defaults.

```yaml
documentation:
  max_files: 8192
  max_file_bytes: 4194304
  lint_timeout_seconds: 300
  style_exclude:
    - "changelog.d/**"
    - "docs/adr/_index_fragments/*.md"
    - "**/testdata/**"
```

| Key | Default | Accepted | Effect |
| :--- | ---: | :--- | :--- |
| `max_files` | 4,096 | 4,096 to 16,384 | Markdown inventory file-count bound |
| `max_file_bytes` | 1,048,576 | 1,048,576 to 4,194,304 | Per-file size bound |
| `lint_timeout_seconds` | 120 | 1 to 480 | Time budget of each style-lint child, in seconds ([Lint time budget](#lint-time-budget)) |
| `style_exclude` | none | Up to 64 globs of at most 256 bytes | Files the style rules skip |

The bounds can be raised but not removed: HISS-02 requires one, and the
ceilings are fixed in `tools/markdownlint/verify.mjs`. The per-file ceiling is a
memory bound. The markdownlint library lints a 4 MiB file of linked and
code-spanned bullets within a 1.5 GiB heap (`lintMemorySelfTest` replays it),
and an 8 MiB file of linked bullets needed more than 1.5 GiB. That is close to
Node's default heap on a 7 GB hosted runner. `markdownlint-cli2` needed the same,
since it lints through the same library. The private-link rule follows a raised
bound. It accepts inventories up to the file-count ceiling, each file's parse
bound grows with its size, and every parse runs within a fixed memory budget
([Private scratch links](#private-scratch-links)).

`style_exclude` entries are globs over repository-relative paths, matched with
dot files included, so `docs/**` also reaches dot-directories below `docs/`. The
gate matches them itself (`styleExclusionMatcher` in
`tools/markdownlint/verify.mjs`), the way micromatch 4.0.8 matched them before
it left the lock (#736):

| Shape | Matches |
| :--- | :--- |
| `*` | any run of characters inside one path segment, a leading dot included |
| `?` | one character inside one path segment |
| `[abc]`, `[a-z]` | one listed character; `[ab]` also matches the text `[ab]`, as in micromatch, while a class holding `-`, `^`, `{`, `}` or another regular-expression character does not |
| `[^a]` | one character that is not listed, never `/` |
| `{a,b}` | either alternative, inside one path segment; at most 64 alternatives per segment, where each class that also matches its own text (`[ab]`) doubles the count |
| `**` as a whole segment | any number of segments, none included |

A trailing `/**` after a segment that ends in `*` (`docs/*/**`) needs at least
one more segment, and consecutive `**` segments count as one, as in micromatch.
A path spelled exactly as the glob matches it too, so `docs/{a,c}.md` also
excludes a file named `docs/{a,c}.md`: micromatch compares the two before its
pattern. Matching is case-sensitive on every platform; on Windows a backslash
in a path also separates segments. `styleExclusionGrammarSelfTest` replays a
table of micromatch's answers for these shapes.

A glob is refused when it is absolute, drive-lettered, or negated with `!`, when
it contains a backslash or an empty, `.`, or `..` segment, or when it holds no
letter or number, so wildcards alone (`**`, `*/**`) cannot stand for every file.

Its characters come from an allow-list: letters, combining marks and digits of
any script, the space, and only this ASCII punctuation: the grammar's own
`. _ - / * ? [ ] ^ { } ,` and `! # $ % & ' + : ; < = > @ ~` and the backtick,
which micromatch read as themselves. The gate refuses any other character and
names its code point: a double quote, which micromatch read as a quote around
literal text, `( | )`, a tab, a no-break space, an emoji, a joiner. A character
nobody compared with micromatch is therefore refused, never matched differently.
Random differentials against micromatch 4.0.8, drawing globs and paths from
every printable ASCII character, tab and a Unicode sample, found no mismatch
over 112 million glob-path pairs; that is a measurement over that alphabet, not
a proof (the comment above `STYLE_EXCLUSION_TABLE` gives the counts).

The gate also refuses, naming the glob and the reason, every shape it does not
support rather than matching it differently:

<!-- praetor:docs-references:off the list shows illustrative globs and the paths micromatch matched with them; no repository carries them -->

- extglobs and `( | )` groups, POSIX classes such as `[[:alpha:]]`, brace
  ranges such as `{1..3}`, nested brace lists, and `**` inside a segment
  (`docs/**.md`);
- a class that starts with `!`: micromatch read `[!a]` as the characters `!`
  and `a`; write `[^a]`;
- a class range that spans `/`, such as `[ -~]`: micromatch let it match a
  path separator, so `a[ -~]b/x.md` matched `a/b/x.md`;
- a `+` straight after `]`, `{` or `}`: micromatch read it as a
  regular-expression repeat, so `docs/[0-9]+.md` matched `docs/12.md` and not
  `docs/1+.md`;
- a brace alternative of `*` alone: micromatch let it match nothing, so
  `docs/x/**/{*,draft}` matched `docs/x`; list that alternative as a separate
  glob;
- `.*` inside a brace list: micromatch matched it differently from `.*`
  outside one, so `a{x,.*}` did not match `a.`, though `a.*` did;
- a run of `+`, `$` or `^` in a glob of one segment: micromatch escaped only the
  first character of the run there, so `c++.md` matched `c+.md` and `a$$*`
  matched `a$`; the same run in a longer glob, such as `docs/c++/**`, is
  accepted;
- an unmatched bracket or brace, a brace list without a comma, and a brace
  alternative that leaves an empty segment or wildcards alone.

<!-- praetor:docs-references:on -->

Exclusions apply after the built-in style exclusions and before the style rules
run. They narrow the style run only: the private-link rule still reads every
inventory file, excluded or not. When the globs would remove every file the
built-in selection styles, the gate fails instead of styling nothing.

A declared block is reported before linting, with the count each glob removed:

```text
markdown-governance: .standards.yaml documentation bounds 8192 files, 4194304 bytes per file, 300 s per lint child (defaults 4096, 1048576, 120; ceilings 16384, 4194304, 480)
markdown-governance: style exclusion "changelog.d/**" matched 17 files
markdown-governance: style exclusion "docs/adr/_index_fragments/*.md" matched 212 files
markdown-governance: style exclusion "**/testdata/**" matched 30 files
markdown-governance: styled 1402 public Markdown files (259 excluded by documentation.style_exclude); checked 1905 tracked/non-ignored Markdown files for private links
```

`praetorctl audit` validates the block when it loads the manifest
(`internal/config/documentation.go`), with the same ranges and the same
repository-relative glob rules, and records the effective values on their own
line. The unsupported-shape refusals above run in the gate only:

```text
[PASS] Documentation gate settings from .standards.yaml: max_files 8192, max_file_bytes 4194304, lint_timeout_seconds 300, 3 style exclusions (changelog.d/**, docs/adr/_index_fragments/*.md, **/testdata/**).
```

The gate reads the block at run time, so a declaration changes no locked asset.
`TestDocumentationSettingsMirrorConfig` in `tools/markdownlint/assets_test.go`
keeps the gate's constants equal to audit's, and `make docs-lint-test` replays
the settings, raised-bound, style-exclusion, glob-grammar, glob-character,
lint-entry, lint-output, lint-memory, lint-budget, `--only`, and
hermetic-configuration fixtures.

### Lint time budget

Each style-lint child, one batch of paths, must finish within
`lint_timeout_seconds`: 120 s by default, accepted from 1 to 480. A child past
its budget is stopped, and the gate fails with status 2 at once and re-runs
nothing. The report names the batch, its file count and bytes, and its five
largest files as suspects, then the setting that raises the budget (while it is
below the ceiling) and the command that lints the largest suspect alone
(`lintBudgetReport` in `tools/markdownlint/verify.mjs`):

<!-- praetor:docs-references:off the report shows an illustrative adopter batch; no repository here carries these paths -->

```text
markdown-governance: lint batch 2 of 3 (377 files, 3145728 bytes) exceeded its 120 s budget; documentation.lint_timeout_seconds in .standards.yaml raises it up to 480
suspects, the batch's largest files (size is no proof):
  docs/state.md (2894011 bytes)
  docs/guide.md (61440 bytes)
  docs/reference.md (40960 bytes)
  README.md (20480 bytes)
  docs/install.md (10240 bytes)
lint a suspect alone to time it: node tools/markdownlint/verify.mjs --only docs/state.md
```

<!-- praetor:docs-references:on -->

Size is a heuristic, not proof. The slow shape seen so far is a long paragraph
holding an unbalanced `[`: the markdownlint library's GFM autolink-literal
extension then walks back over the paragraph for every later `www.`, `http` or
`@` candidate, so the time grows with the square of the paragraph's length. In
an adopter repository one 2.8 MB file took 140 to 196 s alone and 3.9 s once
the row that broke its backtick pairing was repaired (#784). The lasting fix is
to repair the paragraph: pair the backtick runs and close the bracket. Raising
the budget only buys time.

`node tools/markdownlint/verify.mjs --only <file>...` lints the named files
alone, in one child under the same budget, and prints how long it took. Each
name is resolved from the current directory and must be a file the gate styles;
any other name, an excluded or missing file included, is refused before
linting (`namedStyleFiles`).

The ceiling keeps the budget inside the hosted job. `.github/workflows/praetor-docs.yml`
stops the job after 10 minutes, and 480 s leaves 2 of them for checkout, Node
setup, the locked install and the other steps. A child therefore runs out of
its budget while the job still runs, and the gate names the batch instead of the
runner cancelling the job without one. `TestLintBudgetCeilingFitsHostedJob` in
`tools/markdownlint/assets_test.go` keeps the ceiling and the job limit in step.
`lintBudgetSelfTest` replays a planted child that sleeps past a 1 s budget and
passes under a 10 s one, and `onlyModeSelfTest` covers `--only`.

## Private scratch links

Public Markdown must not link into `.workingdir/` or `.workingdir2/`. These
directories may contain local evidence, cluster details, prompts, or other
session-only data, and adoption keeps both out of Git by default. A repository
that retired the legacy `.workingdir2/` can let Git see it again
([Adoption, audit, and CI](#adoption-audit-and-ci)); the link rule rejects both
roots either way.

The `PRAETOR-MD001` rule parses Markdown and embedded HTML structurally. It
checks inline links, images, reference destinations, autolinks, and HTML URL
attributes including `href`, `src`, `srcset`, `poster`, `data`, `action`, and
`formaction`. `srcset` candidates are parsed separately, including candidates
without whitespace after a comma and `data:` URLs. HTML parsing disables
scripting so links inside `noscript` remain visible. The scan also follows
nested iframe `srcdoc`, meta-refresh URLs, CSS `url(...)` in style attributes
and elements, SVG presentation attributes such as `filter`, `fill`, `stroke`,
`clip-path`, `mask`, and marker attributes, and string or `url(...)` sources in
CSS `image-set()` and `-webkit-image-set()`. String and `url(...)` forms of CSS
`@import` are covered too; CSS escapes are decoded while ordinary CSS string
literals and comments remain prose. Legacy HTML plugin surfaces such as
`applet`/`param` are intentionally outside this public-documentation contract.

Python-Markdown attribute lists are active URL surfaces too: quoted or unquoted
`href`, `src`, `srcset`, and `style` overrides are parsed with or without the
optional colon. Escaped opening braces remain literal. PyMdown Snippets scissors
are checked in single-line and block forms, including line/section selectors.
Because that extension expands directives before Markdown parsing, a scratch
snippet directive inside a fenced block is still rejected; prefix the scissors
with `;` to demonstrate it without including a file.

URL input follows browser preprocessing before WHATWG URL resolution: raw
leading and trailing C0 controls/spaces are removed, and raw tabs, line feeds,
and carriage returns are stripped. Percent-encoded controls are not promoted
into a scheme. Markdown escapes and character references are decoded once;
HTML attributes are consumed as already decoded by the HTML parser, avoiding a
second entity-decoding pass. The resolved path then normalizes `.` and `..`,
query strings, fragments, valid ASCII percent triplets, malformed percent
triplets, file URLs, UNC paths, Windows separators, drive-absolute paths, and
case variants of the private roots.

Embedded HTML traversal is bounded to 65,536 aggregate nodes and eight nested
`srcdoc` documents. Each `srcset` is bounded to 4,096 candidates. Embedded CSS
is bounded to 65,536 tokens and 64 levels of delimiter/function nesting. The
aggregate Markdown and HTML destination inventory is bounded to 262,144
entries. Each file's Markdown parse is bounded to 262,144 events, or to one event
per byte for a larger file, so the bound follows the per-file size bound;
ordinary documentation parses to 0.2 to 0.3 events per byte. MDX syntax-tree
traversal is bounded to 65,536 nodes, 128 levels, and 32 properties per node.
Exceeding any bound fails the gate instead of truncating the scan.

The event bound can only be checked once `micromark` has built the whole event
array. That costs 350 to 1,100 heap bytes per event, and dense Markdown reaches
four events per byte, so a 1 MiB list of one-word items needs more than 3 GB.
The rule therefore scans every file in one worker thread whose V8 old
generation is capped at 1,536 MiB (`PARSE_HEAP_BUDGET_MB` in
`tools/markdownlint/no-private-scratch-links.mjs`). A file that exhausts the
budget stops the worker, not the gate process. The gate then fails with a
message that names the file:

```text
markdown-scratch-links: docs/list.md: Markdown parse exceeds the 1536 MiB parse memory budget
```

Node ends a worker at its heap limit by granting it 16 MB more and then stopping
it. With V8's default young generation, one garbage-collection pass could
promote more than that, and V8 then aborted the whole process instead. The
worker's young generation is therefore capped at 16 MiB
(`SCAN_YOUNG_GENERATION_MB`), and no abort occurred in the repeated runs recorded
beside that constant. Should V8 still abort, `verify.mjs` fails the gate on the
signal.

Some inline constructs parse in superlinear time, so each file's scan also has
a 60-second deadline (`SCAN_DEADLINE_MS`), and the rule refuses a file past the
4 MiB per-file ceiling before reading it. The budget is twice what a 4 MiB file
of linked, code-spanned bullets needs. That is 0.4 events per byte, twice the
density of this repository's documentation. The rule self-test scans such a
file at exactly 4 MiB within the production budget. It also shows that a small
budget and a short deadline each fail with the file's name.

Prose, fenced or inline code, remote URLs, and sibling names such as
`.workingdirectory/` are not links to private artifacts and remain valid. The
snippet preprocessor exception above applies even inside code blocks.

A failure identifies the source line, original destination, normalized path,
and forbidden root:

```text
docs/guide.md:17: error PRAETOR-MD001 private scratch link destination="../.workingdir2/evidence.md" resolved=".workingdir2/evidence.md" forbidden_root=".workingdir2"
```

Move publishable evidence under `docs/` and update the link. Do not unignore or
copy an entire scratch directory to make the diagnostic disappear.

## Adoption, audit, and CI

Repositories declaring the `docs:seo-portal` facet receive the five canonical
assets under `tools/markdownlint/`, the figure engine's 18 files under
`tools/figures/` ([figures guide](figures.md#in-adopting-repositories)),
`docs-lint` and `docs-figures` prerequisites on `verify-all`, a managed block at
the end of `.gitattributes` that keeps the engine, the figure specs and their
outputs at LF, and `.github/workflows/praetor-docs.yml`, whose one job runs the
Markdown gate and then `node tools/figures/build.mjs check` and `sources`. Both
figure commands skip, saying why, in a repository without a figure spec or
output. The marker-owned README block gains an
exact **Documentation Governance** workflow badge and `make docs-lint` gate entry,
using the repository identity declared by the effective manifest. A manifest
without `repository.owner` and `repository.name` leaves the README block
unreconciled and records the skip as a warning. Adoption runs
this workflow step before reconciling the branch ruleset, so the unconditional
**Documentation Governance** job becomes a required status context. It also
owns a canonical tail block in `.gitignore` for the private scratch directories
and the other private adoption artifacts. Audit uses `git check-ignore` to prove
the rules are effective, so a later negation or a visually similar pattern with
leading spaces cannot pass.

The block ignores `.workingdir/` and, by default, the legacy `.workingdir2/`.
Adoption writes the full block into a `.gitignore` that holds none yet, so a
`.workingdir2/` created after adoption is already ignored. A repository that
retired `.workingdir2/` deletes the `/.workingdir2/` line from the block. While
no entry of that name is on disk, adoption keeps the line out, audit no longer
demands it, and Git keeps the path visible
(`TestLegacyScratch_Positive_RetiredRootStaysVisible`,
`TestAuditDocumentationGateLegacyScratchRootIsOptional`). If the directory
reappears, adoption restores the rule and audit fails until it does
(`TestLegacyScratch_Negative_DefaultAndPresentRootAreIgnored`,
`TestAuditDocumentationGateLegacyScratchRootOnDiskIsDemanded`). A retired
block keeps the `!/.config/` negation adoption adds where a Kconfig-style rule
hides `.config/` ([Adoption](../adoption.md#what-adoption-reads-before-it-writes),
`TestLegacyScratch_Boundary_RetiredBlockKeepsConfigNegation`). Adoption
renders the block, and audit checks it and picks the roots it probes, from the
same answer (`keepsLegacyScratch` in `internal/adopt/legacy_scratch.go`, read
through `HasManagedGitIgnoreTail` and `PrivateScratchRoots`).

Existing unambiguous custom Makefile recipes stay intact. Adoption appends one
marked block when `docs-lint` is provably available; includes, generated target
names, `eval`, pattern rules, an operator-owned target collision, or an edited
managed block fail for review.
`praetorctl adopt --force --lock-source-root=<praetor checkout>` repairs an edited
managed block while the facet remains enabled and refreshes the content-locked
assets, but still refuses symbolic links; the report lists that repair as a
replace with its line delta and a backup. The block an earlier Praetor wrote,
with `docs-lint` alone, is recognised exactly: plain `praetorctl adopt`
refreshes it to the current block, reported as a reconcile with no backup, and
a disable removes it (`priorDocumentationMakefileBlocks` in
`internal/adopt/verification_makefile.go`). A refresh or repair that adds a
target, such as `docs-figures`, stops when the rest of the Makefile may already
define it, as a first attachment does. The `.gitattributes` block follows the
same contract: an edited block fails a plain run before its first write, and
`--force` restores it as a replace with a backup, with the facet enabled or
disabled, because the block stays for its DevContainer rule. Only a disable
that would remove the block, in a repository whose `adoption.decline` lists
`dev-container`, refuses an edited one under `--force` too
(`internal/adopt/gitattributes.go`,
`TestAdopt_EditedAttributeBlockWithoutDocumentationFacet`,
`TestPreflightAttributes_Boundary_EditedBlock`). The block of an earlier release, without
the DevContainer rule, is an unedited block and is refreshed by a plain run
(`TestReconcileGitAttributes_Positive_EarlierBlockRefreshedWithoutForce`).

The tool assets and the workflow form the Markdown entry of the managed asset
family registry (`managedasset.Families`, `internal/managedasset/family.go`).
Adoption (`internal/adopt/managed_family.go`), audit
(`cmd/standardsctl/audit_documentation.go`), and the DevContainer bootstrap all
walk every family the facet enables, so a further embedded family joins the
facet as one registry entry; the figure engine is the second. A family may set
`RefuseForeign`: until one of its paths holds the canonical text, a file
already at any of its paths predates adoption, and adoption fails naming it,
even with `--force`, instead of overwriting it. The Markdown entry leaves it
off, so `--force` replaces those files as described above; the figure engine
sets it (`TestManagedFamilyRefusesForeignFilesOnFirstAdopt`,
`TestMarkdownFamilyForeignFilesGolden` and `TestFigureFamilyForeignFilesGolden`
in `internal/adopt`). A family may also declare `.gitattributes` rules
(`Family.Attributes`); adoption writes the rules of every enabled family into
one tail block, after the DevContainer rule that block opens with
([checkout line endings](devcontainer-bootstrap.md#checkout-line-endings)), and
removes the block once nothing declares a rule: no enabled family, and
`dev-container` in `adoption.decline` (`ManagedAttributes` in
`internal/adopt/gitattributes.go`).

Disabling `docs:seo-portal` is a convergent transition. Run
`praetorctl adopt --force --lock-source-root=<praetor checkout>` so the generated branch ruleset can drop its hosted
status context; without that authorization, adoption refuses before deleting
local assets. The transition removes only canonical-equivalent workflow/tool assets, earlier
Praetor texts of them, the exact managed Makefile block and the documentation rules of the
`.gitattributes` attribute block, which keeps its DevContainer rule (the block goes, and a file
that held nothing else is deleted, only where `adoption.decline` lists `dev-container`;
`TestFigureFamilyAdoptionGolden`, `TestDevContainerAttributeAdoptionGolden`), strips the README badge and gate entry, removes
the required status context, and rebuilds the formatter-ignore inventory without
documentation paths; the inventory keeps the files of every family still enabled, such as the
[Go API compatibility gate](api-compatibility.md) (`managedArtifacts` in
`internal/adopt/managed_artifacts.go`). Operator files beside the tool assets and bytes outside
managed blocks are preserved.
Drifted or symbolic-link assets and ambiguous, edited, duplicate, or incomplete
Makefile/formatter markers stop the transition before canonical documentation
assets are deleted; `--force` does not turn an ambiguous deletion into an
authorized one. Re-enabling the facet restores the same canonical surfaces.

`praetorctl audit` verifies the assets, workflow, Makefile attachment, README
badge and gate entry, effective scratch ignore rules, any configured formatter's
inventory, the `.gitattributes` block at the end of the file, and the required
hosted context, and records any declared
[repository settings](#repository-settings). It warns, without failing, when
the repository declares its licensing in `REUSE.toml` but no annotation labels
`tools/figures/third_party/interfig/upstream/**` MIT, or a later table that
also covers those files, such as `**`, relabels them. When the facet is disabled, audit rejects stale Praetor
documentation assets, exact Makefile marker lines, a `.gitattributes` block that holds more than the
DevContainer rule or is not at the end of the file (`TestAuditFigureFamilyGolden`), README contract text,
formatter paths, or a structurally declared hosted status context instead of
silently treating them as active. Operator-owned files at the same paths, prose
that mentions a marker, and unrelated ruleset metadata are not claimed by the
disabled facet. A `.prettierignore` inventory written before this gate existed
differs from the current one only in its comment line and still passes while
the facet is disabled; the next `praetorctl adopt` rewrites the comment.

The hosted workflow (this repository's own `.github/workflows/praetor-docs.yml`)
and the template `adopt.DocumentationWorkflow()` emits to adopters
(`markdownlint.Workflow`, `tools/markdownlint/assets.go`) both pin
`runs-on: ubuntu-26.04` and every action by full commit SHA, with the release as
a trailing comment (`actions/checkout@<sha>  # v7.0.1`). A repository whose
organization requires SHA pinning can run the gate, and it cannot pin the
actions itself because audit locks the file.
`managedasset.Family.Validate` refuses a hosted workflow with a tag-, branch- or
short-SHA-pinned action (`TestWorkflowPinsEveryActionNegative`). Renovate's
github-actions manager reads `tools/markdownlint/assets.go` as well as the
workflow (`renovate.json`), and one grouped branch moves both copies' pins
together (`TestRenovateUpdatesTemplatePinsWithWorkflowCopy` in
`internal/managedasset/renovate_test.go`, which checks every family with a hosted workflow,
the [Go API compatibility gate](api-compatibility.md) included). The Renovate pull
request itself skips the Go tests and the audit
([Renovate pull requests](contributing.md#renovate-pull-requests)), so it is the
takeover pull request that fails CI until someone finishes the update: the
outgoing workflow text must be recorded as described above, and the `sha256`
lines of `internal/adopt/testdata/managed-family/*.golden` regenerated with
`PRAETOR_UPDATE_GOLDEN=1 go test ./internal/adopt`.

Praetor ships every update of the managed files, and a copy an adopter's own
bot bumped first fails the byte lock until their Praetor catches up: Renovate's
`pinDigests` rewrites `praetor-docs.yml`, and its npm manager bumps
`tools/markdownlint/package.json`. The `renovate-ignore` adoption step therefore
tells the adopter's Renovate to leave the managed files alone
(`internal/adopt/renovate.go`):

- It edits only the configuration Renovate itself reads, the first of
  `renovate.json`, `renovate.jsonc`, `renovate.json5`, the same three names
  under `.github/` and `.gitlab/`, `.renovaterc`, `.renovaterc.json`,
  `.renovaterc.jsonc` and `.renovaterc.json5`, in that order. A repository
  without one gets none created. Renovate also filters that list by platform:
  on GitHub it skips the `.gitlab/` names, on GitLab the `.github/` names, and
  on other platforms both, where it reads `.<platform>/renovate.json` after
  `package.json` instead. Adoption does not apply that filter, so keep one
  configuration file per repository, or check that the one adoption edits is
  the one your platform reads.
- It adds one `packageRules` entry, described
  `praetor-managed files: praetorctl adopt ships their updates and praetorctl audit locks them byte for byte`,
  whose `matchFileNames` lists every managed path of the enabled families, then
  the generated DevContainer bundle files, and sets `enabled: false`. It does not use `ignorePaths`: that option is not
  mergeable, so a repository-level list replaces the one `config:recommended`
  contributes and would re-enable updates in test and fixture trees.
- A rule of the adopter's own that already disables Renovate for managed paths
  counts in the entry's place: the entry lists only the paths no such rule
  covers, and is left out, or removed, when every path is covered. A rule
  counts only when it provably has the entry's effect. Its members are
  `description`, `matchFileNames` and `enabled` alone, with `enabled: false`,
  because any other member, a matcher such as `matchManagers` or
  `matchUpdateTypes` included, could narrow it. One of its at most 256
  `matchFileNames` patterns matches the path, read as literal text, `*`, `?`
  and whole `**` segments, case-sensitively; `*` alone matches every file, as
  in Renovate, and a trailing `**` spans one or more segments, as in minimatch.
  A list holding a negation, a regular expression, a class, a brace, a group or
  an escape is not counted. No later rule sets `enabled` to anything but
  `false`, since Renovate applies `packageRules` in order. A rule adoption
  cannot read this way costs a redundant entry, never an unprotected file.
- Every other member and entry keeps its order and value. The file is
  re-indented with two spaces when the entry is added or changed; an entry
  already present, however formatted, leaves the file untouched, so a formatter
  and adoption do not take turns rewriting it.
- A file it cannot read or rewrite without risking the adopter's settings is
  reported with the entry to add by hand and left byte for byte, and adoption
  goes on with its other steps: a symbolic link, a directory or other
  non-regular file, a file over 1 MiB or one it may not read, a `.jsonc` or
  `.json5` name, comments or trailing commas in a `.json` file, mixed line
  endings, a `packageRules` that is not an array, two managed entries, a
  `packageRules` already holding 1024 entries without the managed one, and
  configuration in the `renovate` member of `package.json`.
- The entry lists the managed files of every family an active facet enables:
  the documentation families under `docs:seo-portal` and the
  [Go API compatibility gate](api-compatibility.md) under `api:public-contract`, in a
  repository whose `go.mod` git tracks.
  A family whose source the repository holds (`tools/markdownlint/assets.go`,
  `tools/apicompat/assets.go`, where the managed files are sources its own
  Renovate updates, as in this repository) contributes no path. With no path
  left, no entry is needed and an existing one is removed.
- After the family files the entry lists `.devcontainer/devcontainer.json` and
  `.devcontainer/Dockerfile.praetor` when the DevContainer is one Praetor
  generates: the file records a `customizations.praetor.bootstrap`
  specification, or adoption is about to write it because none exists or
  `--force` replaces it. Audit verifies the bundle against its recorded inputs,
  so a bot's edit fails it, and Renovate reads the digest-only images of
  `Dockerfile.praetor` as `latest`
  ([moving a reviewed default image](devcontainer-bootstrap.md#moving-a-reviewed-default-image)).
  A DevContainer of the adopter's own, which adoption preserves, is not listed
  (`generatedDevContainerPaths`, tests in
  `internal/adopt/renovate_devcontainer_test.go`).

`TestRenovateIgnorePositiveDeclaresManagedFilesOnce`,
`TestRenovateIgnorePositiveAcceptsAnEquivalentAdopterRule`,
`TestRenovateIgnoreNegativeCreatesNoConfiguration`,
`TestRenovateIgnoreNegativeDeclaresOnlyUncoveredPaths`,
`TestRenovateIgnoreNegativeReportsUnreadableConfiguration`, the three
`TestRenovateIgnoreBoundary*` tests, `TestUncoveredRenovatePathsBoundary` and
`TestMergeRenovateRule` in `internal/adopt/renovate_test.go` cover these cases. Dependabot and other
update bots are not configured; keep the managed files out of them by hand.

actionlint rejects a `runs-on` label missing from its built-in table of
GitHub-hosted runners, and v1.7.12, its release at the time of writing, has no
`ubuntu-26.04`. The locked workflow runs on that label, and audit forbids
editing it; so does every CI workflow a flavor scaffolds (the Go, Rust, Node,
JVM and Flutter templates under `templates/`). The `actionlint-labels`
adoption step therefore declares the label to actionlint under
`self-hosted-runner.labels`, the one declaration actionlint accepts for it
(`internal/adopt/actionlint.go`):

- The labels come from the workflows the run writes: the hosted workflow of
  every managed family an active facet enables (this workflow while
  `docs:seo-portal` is enabled, and the
  [Go API compatibility gate](api-compatibility.md)'s while adoption emits it),
  and the CI workflows of the detected flavor unless the flavor step is
  declined. The harness names the same list (`adoptedWorkflowFiles` in
  `internal/adopt/harness_ci.go`). Each workflow's `runs-on` labels are read
  from its body (`forge.WorkflowRunnerLabels`), and a label is declared when
  `actionlintUnknownLabels` lists it as one actionlint does not know. A
  workflow the repository owns, such as a `ci.yml` that differs from the
  flavor's, is not read; with no workflow that needs a label, nothing is
  written.
- It edits the file actionlint reads: `.github/actionlint.yaml`, or
  `.github/actionlint.yml` when the `.yaml` file is absent. With both present,
  actionlint v1.7.12 ignores the `.yml` file, so adoption does too. A
  repository without either gets `.github/actionlint.yaml`, with a header that
  says why it exists. That file passes `yamllint --strict`
  (`scripts/test_emitted_yaml_lint.py` lints its committed rendering,
  `internal/adopt/testdata/emitted/.github/actionlint.yaml`).
- It only adds a missing label, after the adopter's own. A label counts as
  declared when the list holds it, or when one of its patterns matches it under
  Go's `path.Match`, the matching actionlint applies too: `ubuntu-2?.04` declares
  `ubuntu-26.04`, and a brace pattern such as `ubuntu-{26,27}.04` declares nothing,
  in actionlint v1.7.12 as in adoption. Adoption never
  removes a label, including one it added, because the adopter's own workflows
  may run on it too.
- The edit changes only the lines it inserts, and it is kept only when the
  result decodes to the original document plus the new labels. It handles an
  empty file, a file without `self-hosted-runner`, a null or block
  `self-hosted-runner`, and a `labels` value that is null, a block list or a
  one-line flow list such as the `labels: []` that `actionlint -init-config`
  writes. The file keeps its line endings.
- Any other file is reported with the label to declare by hand and left
  untouched, and adoption goes on with its other steps. That covers a symbolic
  link, a directory, a file over 1 MiB or one it may not read, mixed line
  endings, several documents, anchors, aliases, duplicate keys, a flow-style
  mapping, a multi-line flow list and a `labels` value of another kind.
- Once actionlint ships a label, remove it from `actionlintUnknownLabels`, and
  adoption stops adding it. Wherever actionlint is on `PATH`,
  `TestActionlintStillRejectsTheDeclaredLabels` fails as soon as actionlint
  accepts the workflows adoption writes without the declaration, and
  `TestActionlintAcceptsEveryEmittedWorkflow` checks that it accepts each of
  them, every committed rendering of the Node template included, beside the
  file adoption creates for it. Both skip where actionlint is not installed:
  no workflow installs it, and the Git hooks hold the one on `PATH` to a
  [version floor](git-hooks.md#linter-version-floors). `TestActionlintUnknownLabelsBoundary` runs
  everywhere and fails when a listed label is one no emitted workflow runs on.

The `TestActionlintLabels*`, `TestActionlintLabelsOfBoundary` and
`TestMergeActionlintLabelsBoundaryLayouts` tests in
`internal/adopt/actionlint_test.go` cover these cases. This
repository's own `.github/actionlint.yaml` declares the label for all its
workflows, which adoption leaves as it is.

The workflow and `markdownlint-cli2.yaml` pass `yamllint --strict` under its
default rules, which an adopter's own lint may apply to every file. Both open
with a `---` document start, and the workflow quotes its `'on'` key so the truthy
rule does not read it as a boolean. The two pin lines carry a
`# yamllint disable-line rule:line-length` directive, because a 40-hex SHA plus
its release comment runs past 80 columns at step indentation. `make hooks-lint`
checks both files (`scripts/test_emitted_hook_lint.py`).

`DocumentationAssetIsCanonical` compares an adopted repository's workflow file
byte-for-byte, line-ending normalized, against that template, so a workflow
generated before these pins changed fails the exact-content check. Plain
`praetorctl adopt` refreshes an unedited earlier text as described above; an
edited copy needs `praetorctl adopt --force --lock-source-root=<praetor checkout>`.

`adoption.decline: [branch-ruleset]` leaves `.github/rulesets/main.json`
operator-owned, as described in the
[adoption verification guide](adoption-verification.md). Adoption then never
writes the documentation context into it, disabling the facet does not ask
`--force` to rewrite it, and audit still requires the workflow to report
**Documentation Governance** but neither requires nor rejects that context in
the ruleset. With no ruleset to compare, audit reads only the documentation
workflow to find that context (`forge.RequiredStatusContextsOf`), so another
workflow whose check contexts its file cannot show, such as a matrix built from
an expression, does not fail the gate
(`TestAuditDocumentationGate_Positive_DeclinedRulesetReadsOnlyItsWorkflow`).

The other declinable steps behind the gate's surfaces follow the same rule.
Declining `makefile` leaves the `Makefile` to the operator: audit neither
requires the managed `docs-lint` block nor, with the facet disabled, rejects
one. Declining `formatter-ignore` does the same for `.prettierignore`, and
declining `renovate-ignore` leaves the Renovate configuration untouched, which
audit does not read. Declining `actionlint-labels` likewise leaves the
actionlint configuration alone and creates none. Declining
`git-ignore` waives only the managed `.gitignore` tail block; audit still runs
`git check-ignore` and fails until the operator's own rules exclude
`.workingdir/`, and `.workingdir2/` while that directory is on disk or the rules
carry the exact `/.workingdir2/` line. A `.gitignore` that audit cannot read as
text (over 1 MiB, not UTF-8) still applies in Git, so audit then demands both
roots (`TestAuditDocumentationGateDeclinedUnreadableIgnoreProbesEveryRoot`). A
decline list with an unknown or mandatory name fails audit as it fails
adoption, and an undeclined step is still checked in full.
The documentation gate itself cannot be declined:
`adoption.decline: [documentation-gate]` fails adoption and audit, because
removing the `docs:seo-portal` facet is the one switch that converges every
documentation surface.

Praetor's DevContainer bootstrap snapshot explicitly carries the embedded assets
of both families, the five Markdown gate files and the 18 figure engine files.
The adopted CLI therefore emits the same locked gate when it is built
inside a generated development container; undeclared `go:embed` inputs remain a
bootstrap error.

The dedicated hosted workflow runs for every push and pull request. The main CI
workflow also selects the gate on documentation-only changes while skipping the
race and security suites; state-only changes under the ignored private ledgers
select no documentation work. Source/configuration changes reach the same gate
through `make verify-all`. Only Markdown, images, plain-text documents and the
files under `docs/` count as documentation: a dependency manifest ending in
`.txt` (`requirements*.txt`, `CMakeLists.txt`) is configuration even under
`docs/`, and a file kind the filter does not recognise runs the full targeted
matrix rather than the documentation-only path (`internal/cifilter/filter.go`,
`TestBuildManifestTextIsConfiguration` and
`TestUnclassifiedFileKindsRunHeavyGates` in
`internal/cifilter/cifilter_test.go`). `.tsx` and `.jsx` are code extensions,
so a change to a vendored React source file (for example
`tools/figures/third_party/interfig/upstream/src/index.tsx`) is classified as code and runs
the targeted test matrix. Before, such a file fell through as unclassified,
which also ran the tests but reported the change as configuration. A
`.tsx`/`.jsx` file under `docs/` still follows the `docs/` prefix rule
(`TestClassifyChanges_TsxJsxAreCode`,
`TestClassifyChanges_TsxUnderDocsStaysDocsOnly` in
`internal/cifilter/cifilter_test.go`). Every kind the HISS scanner reads comes from its own table
(`hiss.SupportsExtension`), so a Svelte component, the `.mts` and `.cts` TypeScript modules and
`.sh` and `.bash` shell scripts are code too (`TestClassifyChanges_ScannedScriptKindsAreCode`). Zig, the C++ sources and headers
(`.cc`, `.cxx`, `.hpp`, `.hh`), HIP and shading-language sources (GLSL, HLSL,
WGSL, Metal) are code as well, and a shader under `docs/` follows the same
`docs/` prefix rule (`TestSourceKindsHissDoesNotListAreCode` and
`TestSourceKindBoundaries` in `internal/cifilter/kinds_test.go`). The HISS-18
section of the [HISS specification](../standards/hiss-spec.md) lists every
code kind and the test directories.

## Site build and diagrams

The published site is built from the root `mkdocs.yml` by
`.github/workflows/pages.yml` and by the CI **Documentation Integrity Audit**
step, and the adopter preset from `docs/presets/mkdocs/mkdocs.yml` by the CI
`docs-presets` job, in an adopted fixture (below). All of them run `mkdocs build --strict`. A strict build
still passes when a diagram ships as a code listing, so each build is followed
by the diagram check, `site`, a command of the Node figure engine
(`tools/figures/build.mjs`, implemented in `tools/figures/checks.mjs`; Node
22.18 or later, no npm package):

```bash
mkdocs build --strict -d /tmp/site
node tools/figures/build.mjs site --config mkdocs.yml --docs docs --site /tmp/site
node tools/figures/build.mjs sources
```

No step bundles before the build: the figures hook publishes the committed
player from `tools/figures/dist/`.

The configuration decides which diagram kind a build accepts. A `figure`
fence names a spec under `docs/figures/`. The root `mkdocs.yml` and the
preset's `mkdocs.yml` both list `tools/figures/mkdocs_hook.py` under `hooks:`,
which renders each fence as the committed SVGs, a caption and a text
description, and publishes the figure stylesheet and the committed player;
the player loads on top. Neither declares the mermaid custom fence, so figures
are the only diagram kind of both sites
([ADR-0016](../adr/0016-figures-for-adopters.md), section 9). The
[figures guide](figures.md) covers authoring and the build.

The preset's hook entry names the engine `praetorctl adopt` writes to
`tools/figures/` under `docs:seo-portal`, so the preset does not build in
place. The `docs-presets` job adopts a temporary repository with
`docs:seo-portal` and copies the preset into it as the
[preset README's Quickstart](../presets/mkdocs/README.md#quickstart) tells an
adopter: `mkdocs.yml`, `docs/` and `overrides/` into the root, the Python lock
into `docs/`, where the preset's `exclude_docs` keeps it out of the site. There
it installs that lock and runs `build` (which must reproduce the committed
example figure), `make docs-figures`, three strict builds (no
`DOCS_SITE_URL`, a root URL, a URL with a path), `site` on each, a check that
no build published the lock, and the Chromium smoke test on the two builds
with a URL. The job also runs when only the adoption code that sets the
fixture up changes (`internal/adopt`, `internal/managedasset`,
`tools/markdownlint`, `cmd/standardsctl`). The example
figure's spec is `docs/presets/mkdocs/docs/figures/site-build.ts` and its
evidence anchors name the preset's own files. After a change to
`tools/figures/core.mjs` or the vendored render files, rebuild its committed
outputs in such a fixture:

```bash
fixture=$(mktemp -d)
git -C "$fixture" init -q
go run ./cmd/standardsctl adopt --path "$fixture" --facets docs:seo-portal --lock-source-root .
cp -R docs/presets/mkdocs/{mkdocs.yml,docs,overrides} "$fixture"
node "$fixture/tools/figures/build.mjs" build
cp "$fixture"/docs/assets/figures/* docs/presets/mkdocs/docs/assets/figures/
```

The preset's pages live in `docs/presets/mkdocs/docs/`, inside the root
`docs_dir`. The root `mkdocs.yml` lists that directory under `exclude_docs`,
so the root site neither builds nor links the preset's example page; the
preset README stays in the root navigation. The checks read `exclude_docs`
and skip the same pages MkDocs skips. It matches the way MkDocs does through
pathspec's gitignore rules: a leading or middle `/` anchors a pattern at
`docs_dir`, a trailing `/` matches directories only, and `*`, `?` and `[...]`
never match a `/`. It fails on a pattern with a leading `!`, `**` or a
backslash instead of guessing.

`site` fails when a page holds a fence of a kind its configuration does not
enable (on a figures-only site, a Mermaid fence, with a finding that says to draw it
as a `figure` fence), when a built page holds fewer mermaid `<pre>` elements
than its source has `mermaid` fences, or when a `figure` fence did not become
a `figure.praetor-figure` whose images resolve under the site to SVGs that
embed the player's props (`<metadata id="figure-spec">`), on a page that loads
the figure loader with `player.js` beside it. A fence
nested inside a longer fence is source text and is not expected to render. The
mapping from a page to its HTML file assumes the default
`use_directory_urls: true`.

`sources` needs no site and no npm package. It checks the pages of every
site configuration at the repository root (`mkdocs.yml` with `docs/`, an
`astro.config.*` with `src/content/docs/`; `sourceSites` in
`tools/figures/checks.mjs`), or the `--config` and `--docs` it is given, and
fails on a fence of a kind that configuration does
not enable, when a figure's JSON no longer matches its spec, the vendored
engine or its SVGs, when the JSON lacks a positive whole-number size for
either SVG, when its `html` is not the markup `tools/figures/core.mjs` renders
from it, when a spec and its JSON are not both present, when a fence names an
unknown figure or one whose spec has not been rendered yet (naming
`node tools/figures/build.mjs build`), when the README's portable figure block differs from the
renderer, or when an evidence anchor's file or symbol is gone.

`make docs-figures-check` (part of `make verify-all`) runs the figure engine's
tests, among them `tools/figures/checks.test.mjs`, which replays the checks'
fixtures in both directions and asserts the diagram kind each configuration
enables (figures only, at the root and in the preset), so adding the
mermaid fence back to either site or dropping the figures hook fails without
a site build. It also runs the type check and rebuilds the committed player in
`tools/figures/dist/` from the lock and compares it byte for byte within its
size budget (`node tools/figures/bundle.mjs --check`). The managed
`make docs-figures` target, also part of `make verify-all` and the one every
adopting repository receives, rebuilds every figure and compares it byte for
byte with the committed files (`node tools/figures/build.mjs check`, which
needs no npm package) and runs `sources`.
`make docs-diagrams-test` (also part of `make verify-all`) tests the MkDocs
hook, the one Python part of the figure engine, with
`tools/figures/test_mkdocs_hook.py`. The Pages workflow and the `docs-presets`
job also run the Chromium smoke test (`tools/figures/smoke.mjs`), which
fails when a figure does not mount the player, when autoplay does not advance
a figure's active step, or when a figure shows no packet. It blocks every
request to another origin and ignores the console errors those blocked
requests raise, so a theme's repository widget or a web font never decides
the result.

Both sites share one JSON-LD template, `docs/presets/mkdocs/overrides/main.html`,
which reads the author and repository from the rendering site's `mkdocs.yml`
(see the [MkDocs preset README](../presets/mkdocs/README.md)).
`make docs-seo-presets-test` (part of `make verify-all`) runs
`scripts/test_docs_seo_presets.py`: neither preset template may name a project
of its own, and when `mkdocs-material` is installed, one-page builds check what
the template renders for sample configurations. Without it those builds are
skipped with that reason.

Links from a page to a repository file outside `docs/` use the file's GitHub
URL. MkDocs cannot resolve a relative link that leaves `docs/`, and strict
mode turns that warning into a failed build.

## Praetor machine-documentation catalog

Praetor itself adds one repository-owned check to the adopted Markdown gate.
`tools/docsurface/catalog.mjs` is the declarative source for the two
machine-readable endpoints, `docs/llms.txt` and `docs/llms-full.txt`, and for
the `README.md` and `docs/index.md` lines that describe `/llms-full.txt`. The
`Makefile` makes `docs-lint` depend on `docs-surface`, which runs
`node tools/docsurface/verify.mjs`; both files must equal the catalog rendering
byte for byte after line-ending normalization, and each landing line must occur
exactly once.

`/llms-full.txt` is a compatibility and authority map, not a copied policy
snapshot. Its catalog record names `.standards.yaml`, the checked-in ruleset,
`go.mod`, the documentation workflow, the canonical agent harness, and reviewed
public specifications. The rendered file deliberately omits values such as
required status contexts, review counts, and invariant tables: their repository
sources own those values, and the live forge remains authoritative for hosted
enforcement. The verifier rejects a rendering with a line that opens a fenced
code block or a Markdown table row, the two shapes a copied ruleset or
invariant matrix takes, and exact-file parity rejects manual edits to either
endpoint.

Every catalog link names its repository source. The gate derives the published
Pages route from that source, rejects `.md` route leakage, and requires the
source to remain a regular file confined to the repository. It applies `lstat`
to every path component below the canonical repository root, so an
in-repository intermediate symlink cannot give a Pages route to a different
source. Each source path is bounded to 64 components and each read to 1 MiB,
with at most 1,024 read operations. Repository discovery gives
`git rev-parse --show-toplevel` 10 seconds and 4 KiB each for standard output
and error. Catalog arrays are capped at 16 entries, strings must be single-line
and at most 4 KiB, and duplicate section titles, routes, authority labels, or
authority sources fail before rendering.

The catalog does not own funding, badge, or social-link state; those surfaces
in `README.md`, `mkdocs.yml`, `.github/FUNDING.yml`, and the account blocks of
`docs/sponsoring.md` and `docs/monetization.md` render from the operator's
`.config/operator/funding.yaml` through `praetorctl docs funding`
(see [operational configuration](operational-configuration.md#funding-example)).
The `Community & Funding` links render from the catalog
only because `docs/sponsoring.md` and `docs/monetization.md` are published
pages. The pull-request gate performs no network requests. Run
`make docs-lint-test` to replay the valid, drifted, copied-policy, and boundary
fixtures, including synthetic process and file-status fixtures that need no
host symlink support.
