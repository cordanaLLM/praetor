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
disabled facet.

Praetor's DevContainer bootstrap snapshot explicitly carries these five embedded
assets. The adopted CLI therefore emits the same locked gate when it is built
inside a generated development container; undeclared `go:embed` inputs remain a
bootstrap error.

The dedicated hosted workflow runs for every push and pull request. The main CI
workflow also selects the gate on documentation-only changes while skipping the
race and security suites; state-only changes under the ignored private ledgers
select no documentation work. Source/configuration changes reach the same gate
through `make verify-all`.
