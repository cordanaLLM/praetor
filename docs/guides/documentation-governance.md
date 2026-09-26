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
remain in the broader private-link scan. The runner refuses symbolic-link inputs
and paths escaping the repository root; the self-test creates a Markdown symlink
and proves the inventory fails closed. It also reads tracked symlink targets from
the Git index and rejects an alias into either private scratch root, including an
alias consumed by a snippet or image link when the ignored target is absent from
the checkout.

The inventory is bounded to 4,096 files, 1 MiB per file, and 64 MiB total. A
bound being exceeded is an error, not a partially successful check. The Git
index scan is bounded to 65,536 entries and 2,048 symlinks; each symlink target
is bounded to 4,096 bytes.

## Locked Markdown rules

`tools/markdownlint/package-lock.json` pins the complete Node dependency graph.
The runner copies the canonical tool assets to a temporary directory, executes
`npm ci --ignore-scripts --no-audit --no-fund`, and invokes the installed
`markdownlint-cli2` binary directly. It does not use `npx` or leave a source-tree
`node_modules/` directory. Every subprocess has a timeout, and the temporary
installation is removed after success or failure.

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
explicitly refreshes Praetor-owned assets.

## Private scratch links

Public Markdown must not link into `.workingdir/` or `.workingdir2/`. These
directories may contain local evidence, cluster details, prompts, or other
session-only data and are ignored by Git.

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
entries. MDX syntax-tree traversal is bounded to 65,536 nodes, 128 levels, and
32 properties per node. Exceeding any bound fails the gate instead of truncating
the scan.

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
assets under `tools/markdownlint/`, a `docs-lint` prerequisite on `verify-all`,
and `.github/workflows/praetor-docs.yml`. The marker-owned README block gains an
exact **Documentation Governance** workflow badge and `make docs-lint` gate row,
using the repository identity declared by the effective manifest. Adoption runs
this workflow step before reconciling the branch ruleset, so the unconditional
**Documentation Governance** job becomes a required status context. It also
owns a canonical tail block in `.gitignore` for both private scratch directories
and the other private adoption artifacts. Audit uses `git check-ignore` to prove
the rules are effective, so a later negation or a visually similar pattern with
leading spaces cannot pass.

Existing unambiguous custom Makefile recipes stay intact. Adoption appends one
marked block when `docs-lint` is provably available; includes, generated target
names, `eval`, pattern rules, an operator-owned target collision, or an edited
managed block fail for review. `praetorctl adopt --force` repairs an edited
managed block while the facet remains enabled and refreshes the content-locked
assets, but still refuses symbolic links.

Disabling `docs:seo-portal` is a convergent transition. Run
`praetorctl adopt --force` so the generated branch ruleset can drop its hosted
status context; without that authorization, adoption refuses before deleting
local assets. The transition removes only canonical-equivalent workflow/tool assets
and the exact managed Makefile block, strips the README badge and row, removes
the required status context, and rebuilds the formatter-ignore inventory without
documentation paths. Operator files beside the tool assets and bytes outside
managed blocks are preserved.
Drifted or symbolic-link assets and ambiguous, edited, duplicate, or incomplete
Makefile/formatter markers stop the transition before canonical documentation
assets are deleted; `--force` does not turn an ambiguous deletion into an
authorized one. Re-enabling the facet restores the same canonical surfaces.

`praetorctl audit` verifies the assets, workflow, Makefile attachment, README
badge and row, effective scratch ignore rules, any configured formatter's
inventory, and the required hosted context. When the facet is disabled, audit rejects stale Praetor
documentation assets, exact Makefile marker lines, README contract text,
formatter paths, or a structurally declared hosted status context instead of
silently treating them as active. Operator-owned files at the same paths, prose
that mentions a marker, and unrelated ruleset metadata are not claimed by the
disabled facet. A `.prettierignore` inventory written before this gate existed
differs from the current one only in its comment line and still passes while
the facet is disabled; the next `praetorctl adopt` rewrites the comment.

The hosted workflow (this repository's own `.github/workflows/praetor-docs.yml`)
and the template `adopt.DocumentationWorkflow()` emits to adopters both pin
`runs-on: ubuntu-26.04`. `DocumentationAssetIsCanonical` compares an adopted
repository's workflow file byte-for-byte, line-ending normalized, against that
template, so a workflow generated before this runner pin changed now fails the
exact-content check; `praetorctl adopt` regenerates it onto `ubuntu-26.04`.

`adoption.decline: [branch-ruleset]` leaves `.github/rulesets/main.json`
operator-owned, as described in the
[adoption verification guide](adoption-verification.md). Adoption then never
writes the documentation context into it, disabling the facet does not ask
`--force` to rewrite it, and audit still requires the workflow to report
**Documentation Governance** but neither requires nor rejects that context in
the ruleset.

The other declinable steps behind the gate's surfaces follow the same rule.
Declining `makefile` leaves the `Makefile` to the operator: audit neither
requires the managed `docs-lint` block nor, with the facet disabled, rejects
one. Declining `formatter-ignore` does the same for `.prettierignore`. Declining
`git-ignore` waives only the managed `.gitignore` tail block; audit still runs
`git check-ignore` and fails until the operator's own rules exclude both
private scratch roots. A decline list with an unknown or mandatory name fails
audit as it fails adoption, and an undeclined step is still checked in full.
The documentation gate itself cannot be declined:
`adoption.decline: [documentation-gate]` fails adoption and audit, because
removing the `docs:seo-portal` facet is the one switch that converges every
documentation surface.

Praetor's DevContainer bootstrap snapshot explicitly carries these five embedded
assets. The adopted CLI therefore emits the same locked gate when it is built
inside a generated development container; undeclared `go:embed` inputs remain a
bootstrap error.

The dedicated hosted workflow runs for every push and pull request. The main CI
workflow also selects the gate on documentation-only changes while skipping the
race and security suites; state-only changes under the ignored private ledgers
select no documentation work. Source/configuration changes reach the same gate
through `make verify-all`.

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
stay operator-configured in `README.md`, `mkdocs.yml`, and
`.github/FUNDING.yml`. The `Community & Funding` links render from the catalog
only because `docs/sponsoring.md` and `docs/monetization.md` are published
pages. The pull-request gate performs no network requests. Run
`make docs-lint-test` to replay the valid, drifted, copied-policy, and boundary
fixtures, including synthetic process and file-status fixtures that need no
host symlink support.
