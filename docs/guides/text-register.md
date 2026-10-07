# Text register

Praetor text has three readers: a maintainer on the forge, a reader of the documentation,
and an agent that pays for every token again on the next hop. The `register:` section of
`.standards.yaml` names the voice for each of them and for each task class, and
`praetorctl compile-context` renders that section into AGENTS.md and the six vendor files.
One command exercises the whole path:

```bash
praetorctl compile-context --verify
```

It fails when the block in AGENTS.md differs from what the manifest renders. The decision
and its alternatives are recorded in [ADR-0010](../adr/0010-text-register-per-task.md).

## Start here

| Register | Reader | Example |
| :--- | :--- | :--- |
| `social` | a person on the forge | "`compile-context --verify` now fails on a stale register block. Run `praetorctl compile-context` once per repository. Verified by `go test ./internal/compiler/`." |
| `docs` | a newcomer, then an expert | This page: what it is and the one command first, the reference tables after, every claim pointing at a file, command or test. |
| `internal` | another agent | `<code>verdict: pass</code><br><code>changed: internal/compiler/register.go</code><br><code>ran: go test ./internal/compiler/ = pass</code><br><code>evidence: none</code><br><code>open: none</code>` |

The social register is the `social-text` skill (`.agents/skills/social-text/SKILL.md`),
derived from `adhd-format`. The internal register is the `caveman` skill
(`.agents/skills/caveman/SKILL.md`, see [Caveman](#caveman-the-internal-form)). The docs
register has no skill; the rendered block and this page are its definition.

The block appears under `## Text Register` in AGENTS.md, between
`<!-- praetor:register:start -->` and `<!-- praetor:register:end -->`, and from there in
`CLAUDE.md`, the Cursor rules, the Copilot instructions, `.windsurfrules`, `GEMINI.md` and
the Codex rules. Between the markers the manifest is the source and AGENTS.md is only the
carrier: a hand edit there is overwritten by the next `compile-context` and reported by
`--verify` and `praetorctl audit`. A repository whose AGENTS.md has no block yet fails the
same way, with its own message, until `compile-context` runs once; it appends the section at
the end of the file when it finds no markers.

The failure names where the block renders from: `.standards.yaml` when that file declares a
`register` section, otherwise the default register, with the reason (no `.standards.yaml`, or
one without a `register` section). A missing block reads
`AGENTS.md has no text register block; run 'praetorctl compile-context' to render it from the default register (no .standards.yaml)`,
an edited one `AGENTS.md text register block is out of sync with .standards.yaml`
(`RegisterAuthority.PolicyOrigin` in `internal/config/register_authority.go`,
`TestSyncRegisterBlockDriftNamesTheOrigin` in `internal/compiler/register_test.go`).

## Reference

### Schema

Every key is optional. An omitted section equals the defaults shown. The section is decoded
strictly, so a misspelled key is an error (`internal/config/register.go`,
`internal/config/register_test.go`).

```yaml
register:
  surfaces:
    forge: social      # issues, PR bodies, review comments, commit bodies
    docs: docs         # docs/, README, ADR bodies, notebook documents
    agent: internal    # briefs, fan-out prompts, workflow returns; also the fallback
    operator: docs     # chat replies to the operator; fixed, no other value
    # Emission surfaces: unset means internal, whatever agent says. See "Caveman lint" below.
    # context: internal  # AGENTS.md, compiled vendor files, personas, skills; fixed, no other value
    # mcp: internal      # MCP tool and property descriptions, MCP text results
    # hooks: internal    # agent hook decisions and denials, hook script messages
    # prompts: internal  # provider, repair, notebook and harness prompts
    # ledger: internal   # free text in the .workingdir ledger files
  tasks:
    architecture_synthesis: docs
    function_docstrings: docs
    commit_message_synthesis: social
    waiver_signoff: social
    # scalar shorthand equals {register: <value>}; a budget is opt-in:
    # ci_debugging: {register: internal, max_tokens: 512}
  evidence:
    inline_max_lines: 58
    inline_max_tokens: 1500
  conventions:
    # Repository-specific clauses, rendered after the register's universal form.
    social: PR template, receipt fence and changelog fragment unchanged
```

| Key | Default | Bound | Error when violated |
| :--- | :--- | :--- | :--- |
| `surfaces.<forge\|docs\|agent>` | `social`, `docs`, `internal` | one of the three registers; closed key set | `unsupported text register "<v>"`, `unknown register surface "<k>"` |
| `surfaces.operator` | `docs`, whatever `surfaces.agent` says | `docs` only: a reply to the operator is never caveman (`internal/config/register.go`) | `register surface "operator" is fixed to docs: ...` |
| `surfaces.context` | `internal`, whatever `surfaces.agent` says | `internal` only: the caveman gate on AGENTS.md has no opt-out | `register surface "context" is fixed to internal: ...` |
| `surfaces.<mcp\|hooks\|prompts\|ledger>` | unset, resolves as `internal` (`surfaces.agent` does not reach it) | one of the three registers; closed key set | same as the first row |
| `tasks.<label>` | the four rows above, register only | at most 64 rows; label must be a `target_tasks` label | `register tasks exceed 64 rows`, `invalid register task label "<k>"`, `register task "<k>" is not a declared target_tasks label` |
| `tasks.<label>.max_tokens` | none | 256..8192 when written | `register max_tokens for "<k>" must be 256..8192` |
| `evidence.inline_max_lines` | 58 | 1..58, tighten only | `register evidence bound must be 1..58` |
| `evidence.inline_max_tokens` | 1500 | 1..1500, tighten only | `register evidence bound must be 1..1500` |
| `conventions.<social\|docs\|internal>` | none; `social` is detected (see [Repository conventions](#repository-conventions)) | one line of UTF-8, at most 160 bytes, no control character, no `\|` | `register conventions: unsupported text register "<k>"`, `register conventions.<k> exceeds 160 bytes`, `register conventions.<k> must be one line of UTF-8 text without control characters or '\|'` |
| `sources.expected` / `sources.not_applicable` / `sources.sha256` | none | 1..16384 applicable values; 0..16384 explicitly classified exclusions; full lowercase SHA-256 | applicable count, exclusion count, or digest mismatch |
| `sources.inputs[]` | none | 1..64 tracked shell, Python, Go, JSON, or YAML scopes | strict field, path, parser, selector, surface, and kind errors |

No default row carries `max_tokens`, and the rendered block prints no token numbers for
tasks. A budget is a dispatch parameter: set one after repair reports show what a task
class actually spends.

### Repository conventions

The Form column holds two parts. The engine renders the universal form of each register,
the same in every repository: for `social`, the `social-text` skill, BLUF, full sentences,
and a conventional commit subject left unchanged. A clause about how one repository
publishes (its pull-request template, a receipt fence, a changelog fragment lane) follows
it only when that repository states it, because an adopter without that lane would
otherwise be told to keep it, and `compile-context` would restore the claim on every run
(#328).

The social clause comes from, in order:

1. `register.conventions.social` in `.standards.yaml`, as written. An empty value (`""`)
   states that the repository has none, whatever else the tree holds.
2. Otherwise, `changelog fragment unchanged` (`config.FragmentConvention`) when the
   repository keeps a `changelog.d/` directory that holds at least one regular file: a
   fragment, the `.gitkeep` placeholder or any other file (`changelog.FragmentDirPresent`).
   An empty directory does not count, because git keeps no empty directory and a fresh
   clone of the same commit would not have it. A file or a symbolic link named
   `changelog.d` does not count either.
3. Otherwise, nothing: the row ends after the universal form.

`docs` and `internal` take a clause from `conventions` only. Praetor states its own social
conventions in its `.standards.yaml`, so its row keeps the pull-request template, the
receipt fence and the fragment lane. Prompt directives (`config.RegisterDirective`, used by
`dogfood repairs`, the repair runner, notebooks and the Paperclip harness) carry the
universal form only.

A release render (`praetorctl release`, `changelog.RenderReleaseContext`) removes the
rendered fragments and leaves an empty `.gitkeep` (`changelog.FragmentPlaceholder`) in
`changelog.d/` in their place, and leaves an existing one unchanged. Commit it with the rendered
`CHANGELOG.md`: the directory then survives the release, and the checkout and every fresh
clone render the same block. Removing every file from `changelog.d/` changes the block, so
`compile-context --verify` reports drift until `praetorctl compile-context` runs again.
Tests: `internal/config/register_conventions_test.go`,
`internal/compiler/register_conventions_test.go` (`TestRegisterBlockAgreesWithFreshClone`
covers the release render and the clone), `internal/changelog/fragment_dir_test.go`,
`internal/changelog/placeholder_test.go` and
`TestAdoptRegisterBlockFollowsFragmentDirectory` in
`internal/adopt/harness_register_test.go`.

### Resolution

`RegisterPolicy.Resolve(surface, task)` decides in this order (`internal/config/register.go`):

1. The `context` surface is always `internal` (`ContextRegister`), with source `surfaces.context`.
   The `operator` surface is always `docs` (`OperatorRegister`), with source `surfaces.operator`;
   the task never changes it, and no budget applies (`TestOperatorSurfaceIsFixed` in
   `internal/config/register_test.go`).
2. The `forge` and `docs` surfaces own their audience (`p.surfaceRegister(surface)`). The task
   never changes who reads the forge or the documentation, and no budget applies. Any other
   emission surface (`mcp`, `hooks`, `prompts`, `ledger`) resolves to its own key when the
   manifest writes it and to `internal` (`EmissionDefaultRegister`) otherwise; neither the task
   nor `surfaces.agent` changes it.
3. On the `agent` surface, or when no surface is named, the task row wins; without a row
   the register is `surfaces.agent`.
4. The result names the winning row (`surfaces.operator`, `surfaces.forge`, `tasks.ci_debugging`,
   `surfaces.agent`) for reports.

A `ci_debugging` run therefore writes an internal return and a social pull-request body
with one label and no second vocabulary.

### One label set for tier and register

Task keys are the router's `target_tasks` labels (`.config/models/routing.yaml`, or the
router defaults when that file is absent). The same label selects the cost tier and the
register, and the register never changes the tier: `models route` selects tier and model
exactly as before and reports the register beside them.

```bash
praetorctl models route --task commit_message_synthesis --input-tokens 400
```

The report gains `register`, `register_source` and, when the row has one, `max_tokens`.
When `--output-tokens` is absent and the row has a budget, the budget becomes the output
estimate and `limitations` says so.

`praetorctl dogfood repairs --task <label>` resolves that task row and the independent
`prompts` surface. Each planned job carries `register`, `register_source`,
`max_output_tokens`, `prompt_register` and `prompt_register_source`; its task instructions
end with one register sentence. A `dogfood repairs run` uses the prompt row for its static
prompt and Responses API instructions, uses the task row for the job brief and proposal
summary, and records both resolutions beside provider usage. The task budget can only
lower the limit of the run configuration; it never raises it.

Only the rows a manifest writes are checked against the label set. The default rows are
not, so a repository with its own labels is never failed by rows it did not write.

## Evidence

Evidence longer than `inline_max_lines` lines or `inline_max_tokens` estimated tokens never
travels inline. Write it to a file under `.workingdir/evidence/` (`config.EvidenceDir`) or to
the ephemeral directory a gate already uses, and return one pointer line:

```text
evidence: <path> sha256:<12 hex> lines:<n>
```

The reader fetches the file only when a decision depends on it. Logs, transcripts, full test
output and fetched web pages are always artifacts, whatever their length.

Every rendered block names that directory, so the repository has to keep it out of Git:

- `praetorctl compile-context`, `praetorctl init` and the `standards_compile_context` write
  make Git ignore it before they render the rule. All three run one write
  (`adopt.CompileAgentContext`), which reconciles the ignore rule through the writer
  `praetorctl state init` uses (`adopt.EnsureEvidenceIgnore`). A repository whose rules already exclude it is left byte for
  byte alone. Otherwise the Praetor private-artifact block is merged into `.gitignore` and the
  run prints `Added the Praetor private-artifact block to .gitignore`. With `git-ignore` in
  `adoption.decline`, it prints the warning `state` prints and leaves `.gitignore` alone.
- `praetorctl compile-context --verify`, `praetorctl audit` and their MCP mirrors fail with
  `git does not ignore .workingdir/evidence/` while Git does not ignore it, whichever facets
  the manifest enables and whether or not `git-ignore` is declined
  (`compiler.CheckEvidenceIgnored`).

Only the repository's own ignore rules count; a personal excludes file does not. The probe asks
about a file inside the directory, so it answers before the directory exists. `.workingdir/*`
followed by `!.workingdir/evidence/` reads as not ignored. `/.workingdir/` followed by the same
negation still reads as ignored, because Git cannot re-include anything under an excluded
directory. A directory outside any Git work tree passes, since no commit can publish it.
`internal/compiler/evidence_ignore_test.go`, `internal/adopt/private_ignore_test.go`,
`internal/adopt/context_write_test.go`, `cmd/standardsctl/compile_context_evidence_test.go`
and `cmd/standardsctl/init_evidence_test.go` cover each case.
`config.EvidencePointer` produces the line, and the SARIF distillation of
`internal/lockdown` ends its summary with it. The defaults originate there: 58 lines and
1500 tokens are the distillation cap, and `lockdown.MaxDistillLines` and `MaxDistillTokens`
alias the config constants. Tightening the manifest values lowers the numbers the block
prints; it does not yet re-parameterise the distillation cap itself.

## Adopted repositories

`praetorctl adopt` writes into the harness the section `compile-context` renders from the
repository's manifest (`compiler.LoadRegisterBlock`): the default section without a
`register:` section, the manifest's rows with one. A fresh adoption and an
`adopt --force` refresh therefore verify without a `compile-context` run first
(`TestAdoptForceHarnessCarriesManifestRegisterBlock` in
`internal/adopt/harness_register_test.go`). When the adoptee later changes its `register:`
section, its next `compile-context` re-splices the block from that manifest, and so does its next
plain `praetorctl adopt`, which keeps the rest of the harness as written and backs up the prior
bytes (`TestAdoptKeptHarnessSplicesRegisterBlock`). An adoptee without a
`routing.yaml` is validated against the router defaults. If the repository instructions
kept across a harness refresh already hold a section (because `compile-context` appended
one earlier), the merge keeps a single copy. `praetorctl init` and harvester onboarding
splice the block before their first compile for the same reason.

## Internal briefs and returns

A brief to another agent states the goal, the inputs (paths, not pasted content), the
return shape, where evidence goes and the task label. The return carries a verdict, changed
paths, commands run, evidence pointers and open questions, and nothing else. A research
fan-out saves each fetched page under `.workingdir/evidence/` and returns pointers with a
one-line finding per page; the orchestrator opens a page only when a decision needs it.

The shared checker makes both shapes explicit. Brief fields are `goal`, `inputs`, `return`,
`evidence`, `task`, with `goal` first. Return fields are `verdict`, `changed`, `ran`,
`evidence`, `open`, with `verdict` first. Put one field on each line, using `none` when a
field has no value:

```bash
praetorctl caveman check --kind=brief candidate-brief.md
praetorctl caveman check --kind=return candidate-return.md
```

A subagent launch brief has no fallback. Text without a task label resolves to
`surfaces.agent`, but where the native dispatch hook is registered
(`praetorctl hook <client> pre-dispatch`), a Claude `Agent`, Codex `spawn_agent`, Gemini
`invoke_agent` or AGY `invoke_subagent` brief without a `task:` field is denied, and so is a
label the routing vocabulary does not declare. The hook resolves the register from that
label ([subagent text register gate](agent-hooks.md#subagent-text-register-gate)).

The rendered block states the brief shape on its task-row line (`subagentBriefRule` in
`internal/config/register_render.go`) and adds "registered dispatch hook denies brief
missing `task:`" only where the repository registers that hook.
`agenthook.DispatchGateRegistered` decides it from the repository's client hook files
(`.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json`), recognising a
registration the way `praetorctl adopt` does: the engine call or the skew guard, under a
matcher that covers the dispatch tool. A file that is absent or does not parse as strict
JSON proves no registration. `compile-context` and `adopt` both read it, so the block an
adopted repository receives verifies without a manual edit
(`TestLoadRegisterBlockFollowsDispatchHook`, `TestAdoptRegisterBlockFollowsDispatchHook`).
Adoption registers only the pre-tool row. The dispatch gate also binds dispatch receipts and
returns through their own rows, so an operator registers it deliberately
([agent hooks](agent-hooks.md)); until then, the block does not claim it.

## Caveman: the internal form

Operators call the internal register "caveman", and the `caveman` skill
(`.agents/skills/caveman/SKILL.md`) makes that form concrete. The
configuration value stays `internal`, so no manifest changes; the internal row of the
rendered block names the skill, and so does the one register sentence that repair jobs and
the Paperclip harness receive (`config.RegisterDirective`).

The skill replaces an adjective ("telegraphic") with rules an agent can apply line by line:
drop articles, pronouns, copulas, hedges and framing; write fragments, one fact per line;
use `->`, `=`, `x2`, `!` and `?` for the connective phrases; copy code, paths, commands,
errors, ids and numbers verbatim. Its clarity floor keeps any fact the reader needs, so the
line gets shorter without losing information:

```text
push rejected x2: state stale (gate run wrote receipt after sync). fix: resync, push. no bypass.
```

Caveman covers text another agent reads: briefs, agent and workflow returns, research
fan-outs and tool-call notes. Forge text stays `social-text`, documentation stays in the
docs register, and a reply to a person is full prose.

## What is not enforced

Register compliance on human-typed surfaces (issues, PR bodies, review comments, commit
bodies, ADRs, docs pages, changelog titles) is advisory, by the operator's own decision
(ADR-0010, "Consequences"): nothing measures whether a pull-request body reads
as social prose. The docs and social registers have no lint of their own, and the command
never reports that absence as a pass: `praetorctl caveman check --surface=docs <file>` reads
the file, so a missing path fails as it does on every surface, and then exits non-zero with
`surfaces.docs = docs has no Caveman verdict; input NOT checked` (see "Surfaces" below;
`TestCavemanCheckSurfaceWithoutLint` in `cmd/standardsctl/caveman_test.go`).

Mechanical: the block in AGENTS.md must match the manifest; AGENTS.md, every canonical
persona under `.agents/agents/` and every canonical skill under `.agents/skills/` must pass
the caveman lint (see "The context gate" below); a configured `max_tokens` bounds the
provider request of a repair run; `praetorctl caveman check --max-words`/`--max-tokens`
makes a per-surface ceiling enforceable on any text a check can read as a file (see
"Ceilings" below). Without `--kind`, `check` judges AGENTS.md and the compiled vendor
files under the context gate's profile and every other input under the strict
runtime-message grammar (see "Caveman lint and token estimate" below); `--kind=brief`
and `--kind=return` add their documented schema, while `--kind=context` selects the context
gate's compatibility profile. Every result identifies which numbered skill rules remained
advisory.

Repair planning and execution call `config.ValidateEmission` before dispatch or source
mutation. The planner records its task-brief verdict. The executor rechecks that brief,
checks its own prompt scaffold against `surfaces.prompts`, records the separate Responses
API instructions as a prompt-surface message, and checks the proposal summary against the
task row as a return. Internal failures stop before the next boundary; `social` and `docs`
record `not_applicable`. Each record names the exact resolved register, token ceiling,
surface, kind, typed source and manifest SHA-256, plus the checker contract version and
SHA-256 of the exact text; a positive task ceiling is enforced for internal task text and
provider output.

Runtime emission text is adversarial input, not a trusted Markdown source. Its strict
profile rejects off regions, comments, fences, headings, tables and ledger rows; quoted and
inline-code prose remains visible to grammar checks. Unsafe Unicode control characters on a
line, format controls, `Other_Default_Ignorable_Code_Point` values such as U+034F COMBINING
GRAPHEME JOINER, and variation selectors are rejected. Tabs and normalized LF/CRLF line
boundaries remain valid, as do ordinary visible combining marks. HTML entities/tags,
Markdown links and malformed punctuation cannot split or hide banned grammar. Literal
recognition is narrow: complete HTTP(S) URLs, rooted or extension-bearing slash paths,
forward- or backslash-drive paths, UNC paths, `:line[:column]` diagnostics, domain-qualified
mail tokens and syntactically valid non-grammar flags retain source spelling. One-letter
technical flags and path-valued attached forms such as `-I/usr/include` and
`-I=/usr/include` are literals. MSVC and clang-cl attached macro definitions matching
`/Dmacro[=value]` are literals; other slash-prefixed prose remains lintable. URL schemes are
ASCII case-insensitive; parentheses within complete URLs remain part of the literal, and
recognition removes at most four terminal
layers of sentence punctuation, balanced wrappers or unmatched closing wrappers. Commands
and field-like diagnostics remain usable.

Grammar matching applies a bounded compatibility fold to fullwidth ASCII, circled and
parenthesized Latin, mathematical Latin ranges and legacy symbols, small-capital, modifier
and subscript Latin letters, ideographic space and an explicit apostrophe-confusable set.
Literal recognition always examines the original spelling first: compatibility
punctuation such as the slash, full stop or at sign cannot manufacture a protected path or
mail token. Every other Unicode punctuation or symbol separator is joined and split for
grammar matching. An
evidence pointer suppresses nothing unless the complete line has the canonical path,
12-hex digest and line-count form. Initial validation and terminal replay call this same
profile.

Tracked shell and Python output templates, selected JSON/YAML values, and MCP
descriptions and result callsites are checked through `register.sources`,
described under [Tracked runtime sources](#tracked-runtime-sources). Dynamic
template fields are normalized to placeholders so the agent-owned surrounding
text is linted. Entirely runtime-owned values and wire formats require a narrow,
explicit classification; they remain in the count and digest as
`not_applicable` instead of disappearing. A green source gate therefore proves
the declared roots, semantic selectors, full applicable inventory, and full
classified-exclusion inventory. It does not prove the runtime value substituted
into a placeholder; that still needs producer validation.

The Paperclip harness is checked twice: `register.sources` lints its `harness.json` rows, and
`praetorctl audit` requires `.paperclip/rules.md` to be their rendering and lints it with the
context profile personas and skills pass
([what the generated harness claims](adoption-verification.md#what-the-generated-harness-claims)).

Still not mechanically checked: runtime values substituted into MCP and hook
templates; notebook prompts; `.workingdir` ledger free
text; popup question text; and native-client chats or hooks (#415). No `internal`
label may imply coverage of those surfaces. A green repository gate proves tracked
context text. Repair terminal readback additionally
reconstructs deterministic owned text, rereads the retained proposal summary and replays
the current checker. It rejects missing records, changed bytes, stale checker contracts,
forged digests and mismatched register provenance; it does not make unlisted runtime
surfaces verified.

## Surfaces without a register row

Two interactive surfaces do not carry a row in the rendered register block:

- **Chat replies to the operator.** Governed by `SurfaceOperator` (`"operator"` in
  `internal/config/register.go`), which resolves strictly to `OperatorRegister` (`docs`).
  It has no opt-out: writing `internal` or `social` in `.standards.yaml` is rejected by
  `validate` with `register surface "operator" is fixed to docs: a reply to the operator is never caveman`
  (`TestOperatorSurfaceIsFixed` in `internal/config/register_test.go`). Replies to a person
  are always full prose, never `caveman`. The Caveman lint does not apply to it: `LintEnforced` is true only
  where a surface resolves to `internal`, and `operator` resolves to `docs`
  (`internal/config/register.go`).
- **Popup question text (`AskUserQuestion` and equivalent).** Outside `RegisterSurface`:
  the schema defines no surface for interactive popups. AGENTS.md rule 4 mandates the
  mechanism (a structured popup, never options embedded in prose); the text inside that
  popup follows caveman's own clarity-floor discipline (drop filler and hedges, never
  compress past the point where an option or its consequence needs a follow-up to
  understand), a scoped carve-out rather than full prose or full caveman. Neither is
  mechanically checked: both are composed at conversation time.

## Caveman lint and token estimate

The internal register is measurable. `internal/caveman` lints agent-facing text, proves a
rewrite lost nothing, and estimates tokens; `praetorctl caveman` runs it on files:

```bash
praetorctl caveman check --kind=context AGENTS.md .agents/agents/
praetorctl caveman check --kind=message candidate-note.md
praetorctl caveman floor AGENTS.md AGENTS.caveman.md
praetorctl caveman estimate AGENTS.md
praetorctl caveman estimate --base=origin/main AGENTS.md .agents/
```

`check` prints one summary line per input, then its findings as `<path>:<line> <rule>:
<excerpt>`, and exits non-zero when any input fails. Line 0 means the whole text. A
directory expands to the Markdown files below it; `-` reads standard input.

Without `--kind`, `check` judges a context file the way the context gate does: the
canonical `AGENTS.md` and each vendor file compile-context writes (`CLAUDE.md`,
`.cursor/rules/hiss-invariants.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`,
`.gemini/GEMINI.md`, `.codex/rules.md`) get `--kind=context`, so `praetorctl caveman check
AGENTS.md`, the command the compiled harness names, reproduces the gate's verdict. Every
other input gets `--kind=message`. The file list is `agentcontext.ContextFiles`, built from
the targets compile-context writes; workstation discovery reads the same list. Each path is
resolved against `--root` (default `.`) and compared exactly, so the rule has these edges:

- an `AGENTS.md` in a subdirectory (`nested/AGENTS.md`), a persona directory entry
  (`.agents/agents/<name>/AGENTS.md`) and a `claude.md` in `docs/` are not context files
  and get `message`;
- a context file outside `--root` gets `message`; pass `--root` to check another
  repository's files;
- names are case-sensitive on every platform: on a case-insensitive file system
  `agents.md` opens `AGENTS.md` but gets `message`; type the name as compile-context
  writes it;
- an explicit `--kind` always wins.

`.windsurfrules` and `.cursor/rules/hiss-invariants.mdc` are read as prose at their place
below `--root` although they are not Markdown; elsewhere a file without the `.md` extension
is a tracked runtime source and needs `--surface` (see "Tracked runtime sources" below).
The tests are `TestCavemanCheckContextKindInference_*` in `cmd/standardsctl/caveman_test.go`
and `TestIsContextPath_*` in `internal/agentcontext/render_test.go`.

The text register block is blanked before the lint, exactly as the context gate does it, and the
summary line counts its lines; `front_matter_lines` counts the lines of YAML front matter
left out of the prose rules (see "Not prose" below). It also prints the selected contract plus mechanically
checked and advisory Caveman skill-rule numbers; `PASS` covers only the mechanical rows.
The prose AGENTS.md that the caveman rewrite replaced,
frozen as `internal/compiler/testdata/agents-floor.txt`, fails:

```text
agents-floor.txt: FAIL prose_words=1253 articles=94 density=7.5/100 limit=2.0 off_regions=0 register_block_lines=13 front_matter_lines=0 tokens_est=2059 findings=2 contract=context mechanical_rules=none advisory_rules=1,2,3,4,5,6,7,8
agents-floor.txt:0 C1 article-density: 7.5 articles per 100 prose words (94/1253), limit 2.0
agents-floor.txt:76 C5 long-sentence: 39 words: your pull requests go stale when ...
```

The current AGENTS.md passes:

```text
AGENTS.md: PASS prose_words=918 articles=3 density=0.3/100 limit=2.0 off_regions=0 register_block_lines=13 front_matter_lines=0 tokens_est=1661 findings=0 contract=context mechanical_rules=none advisory_rules=1,2,3,4,5,6,7,8
```

`praetorctl caveman estimate` puts the rewrite at 10,359 bytes and about 1,912 tokens,
down from 12,308 bytes and about 2,315 tokens; CLAUDE.md went from 168 to 115 lines.

`floor <before> <after>` runs the clarity floor on a rewrite: it exits non-zero when
`<after>` lost a code span, a shell command, an id, a link target, an HTML marker or a
number of `<before>`, or carries fewer MUST-type directives, prohibitions or numbered rules
(the "Clarity floor" table below). Findings
name their line in `<before>` (line 0 for a count). Either input can be `-`, not both. A
rewrite into the internal register is acceptable when `check` passes on it and `floor`
passes from the original to it. The clarity `floor` command (`praetorctl caveman floor`,
`internal/caveman/floor.go`) runs on demand during rewrites. Repository gates actively
exercise the `context` profile: `praetorctl compile-context --verify`
(`cmd/standardsctl/compile_context.go`) and `praetorctl audit` (`cmd/standardsctl/audit.go`)
gate `AGENTS.md` via `compiler.LintContext` (`internal/compiler/caveman_lint.go`), plus
canonical personas and skills via `compiler.LintAgentText` (`internal/compiler/caveman_gate.go`,
`internal/compiler/projection.go`). In addition, `audit` gates the `message`, `brief` and
`return` profiles on declared non-Markdown sources via `auditCavemanConfiguredSources`
(`cmd/standardsctl/audit.go`, `cavemanSourceInputs` in `cmd/standardsctl/caveman.go`), where
`register.sources` inputs are restricted to `message|brief|return`
(`internal/config/register_sources.go`). At runtime, the `brief` and `return` profiles apply
automatically rather than on demand: the native pre-dispatch hook enforces them on subagent
traffic (`internal/agenthook/agent_traffic.go`), while automated repair planning
(`internal/dogfood/repair.go`) and execution (`internal/repairrun/run_execute.go`) validate
briefs, summaries and provider instructions.

### The context gate

`praetorctl compile-context`, `praetorctl compile-context --verify` and `praetorctl audit` run
the lint over the canonical AGENTS.md, and so do their MCP mirrors (`standards_compile_context`
with and without `verify_only`, `standards_audit`). Each tracked nested AGENTS.md goes through
the same lint ([nested AGENTS.md files](#nested-agentsmd-files)). Any finding fails the gate;
the error quotes the first five findings and names the fix. A pass prints the counts behind it:

```text
AGENTS.md: caveman lint passed: 918 prose words, 0.3 articles per 100 (limit 2.0), 13 register block lines left to the renderer.
```

The whole file is linted, including what a repository wrote below the praetor harness. Only
the text register block is left out: `compile-context` renders it, nobody edits it by hand,
and its wording belongs to its renderer (`compiler.MaskRegisterBlock`).

Compiling without `--verify`, and `praetorctl init`, write every target first and lint
afterwards, AGENTS.md, every tracked nested AGENTS.md and every canonical persona and skill: a
prose edit still compiles, and
the run then exits non-zero with `context written, but compile-context --verify will fail:`
and the findings instead of printing success. `--verify` runs every check, register block, evidence ignore rule, vendor
files, lint and projections, and reports every failure together, so a missing register block no
longer hides a lint failure behind it (`VerifyCompiledContext` and `lintAgentText` in
`internal/compiler/projection.go`; `TestVerifyCompiledContext_Negative_ReportsEveryFailure` and
`TestCompileContextProjections_Negative_LintsTheWrittenSource` in
`internal/compiler/evidence_ignore_test.go`).

`praetorctl adopt` lints the AGENTS.md it merged, dry run included
(`compiler.LintContextText`). The text it keeps from the repository is never rewritten, so a
finding is recorded as a warning on the `agent-harness` step and the Universal Harness pillar
prints `[warned: 1 warning(s)]` instead of a success mark
(`internal/adopt/harness_lint_test.go`).

Article density is a ratio over the text it reads, so a long, article-free harness would dilute
a paragraph of prose written below it until the whole file passed. The gate therefore also
judges the text after the harness end marker (`<!-- praetor:harness:end -->`) on its own, and
reports a failure there as a `C1 article-density` finding at the first line after the marker,
prefixed `repository text below the harness:`. A file without the marker is judged whole, as
before. `compiler.CheckContextText` holds the rule, `praetorctl caveman check --kind=context`
runs the same function on every file it is given, and `TestCheckContextTextJudgesTailAlone` in
`internal/compiler/caveman_lint_test.go` and `TestCavemanCheckJudgesHarnessTailLikeTheGate` in
`cmd/standardsctl/caveman_test.go` cover it.

This tail check is a breaking change for adopted repositories. Every harness praetor writes
ends with the marker, refreshed or not, so an `AGENTS.md` whose own text below the harness
passed only because the harness diluted it now fails `compile-context --verify`, `audit` and
the lefthook pre-commit context check, with no configuration change. To find the text, run:

```bash
praetorctl caveman check --kind=context AGENTS.md
```

Then fix the part below `<!-- praetor:harness:end -->` in one of two ways:

- Rewrite it in the internal register: fragments, no articles, commands and paths verbatim.
  `praetorctl caveman floor <before> <after>` proves the rewrite kept every rule id, command
  and link.
- Keep text that must stay in full sentences, such as a quoted policy or a legal notice, and
  wrap it in `<!-- caveman:off -->` and `<!-- caveman:on -->`. The lint reads nothing between
  the two markers.

`TestCheckContextTextMigrationPaths` in `internal/compiler/caveman_lint_test.go` holds both
paths.

The gate has no opt-out, no warning mode and no grace period, in praetor and in every
adopted repository. It reads no manifest, so neither `surfaces.agent` nor any task row can
switch it off. `register.surfaces.context` is fixed to `internal`: writing `docs` or
`social` there is rejected when the manifest loads (`config.ContextRegister`,
`internal/config/register_test.go`).

A repository adopted before this gate carries the old prose harness and fails after the
upgrade. `praetorctl adopt --force --lock-source-root=<praetor checkout>` rewrites the harness in caveman and keeps everything
below its end marker; `praetorctl caveman check --kind=context AGENTS.md` then lists what is left to
rewrite in the repository's own part.

A rewrite of praetor's own AGENTS.md must keep every fact of the prose version:
`internal/compiler/canonical_floor_test.go` runs the clarity floor below against the frozen
fixture and fails when a rule id, a `MUST`, a prohibition, a numbered rule, a command, a
link or a number disappears. The fixture changes only with a deliberate correction of
AGENTS.md, such as the HISS-04 function length that #539 set to 60. The lint and gate code
live in `internal/compiler/caveman_lint.go`.

### Nested AGENTS.md files

An `AGENTS.md` below the repository root is agent context as much as the root file, so the
same gate judges it the same way: kind context, register block masked, harness tail judged
alone, no word ceiling (`compiler.CheckContextText`). To reproduce a verdict, run:

```bash
praetorctl caveman check --kind=context path/to/AGENTS.md
```

`compiler.ListNestedContextFiles` in `internal/compiler/caveman_gate.go` is the one place that
finds these files. It reads only what Git tracks:

- It lists the index with `git ls-files` (`util.RunGitProbe`, bounded by the probe deadline).
  An ignored or untracked file is never read, and Git enters neither a nested repository nor
  a submodule. Outside a Git work tree there are no nested files.
- More than 1024 files (`compiler.MaxNestedContextFiles`) fails with the count and the cap.
- A tracked file missing from the working tree, as a sparse checkout or an unstaged deletion
  leaves it, is skipped and not counted. A symlinked one is refused.
- The canonical AGENTS.md the context gate already lints (`--source`, `--agents`) is skipped,
  so every file is linted once.

`compile-context`, `compile-context --verify`, `audit`, `standards_compile_context` and
`standards_audit` all run it. Every nested file is read before the verdict, so one run names
each failure: the error starts `nested AGENTS.md fails the caveman lint: <failed> of <linted>
files`, quotes the findings of the first five failing files and counts the rest.

Adoption never rewrites a nested AGENTS.md. When one fails after the chain, adoption records
a warning and the run still applies (`internal/adopt/context_verify.go`).

An adopted repository whose nested files are prose fails `compile-context --verify` and
`audit` after the upgrade. To list them, run `git ls-files '**/AGENTS.md'`. Then rewrite each
in caveman, or wrap prose that must stay in `<!-- caveman:off -->` and `<!-- caveman:on -->`.

Tests: `internal/compiler/caveman_nested_test.go` (enumerator bounds), the nested cases in
`cmd/standardsctl/caveman_gate_test.go` (CLI gates),
`cmd/standards-mcp/audit_nested_context_test.go` (MCP tools) and
`TestAdopt_Boundary_NestedContextProseIsWarned` in `internal/adopt/context_verify_test.go`.

### The persona and skill gate

`SurfaceContext` covers more than AGENTS.md by its own doc comment: "AGENTS.md, the
compiled vendor files, personas and skills" (`internal/config/register.go`). The CLI's
`compile-context --verify` and `audit` (`cmd/standardsctl`) therefore also run the caveman
lint, plus a 600-prose-word ceiling (`compiler.AgentTextCeiling`), over every file under
`.agents/agents/*.md` and every `.agents/skills/*/SKILL.md` (`compiler.LintAgentText`,
`internal/compiler/caveman_gate.go`). No opt-out, same as AGENTS.md itself: personas and
skills are agent-only text under `SurfaceContext`, not an emission surface a manifest can
turn off. The gate shares one run with the nested AGENTS.md lint above, and a pass prints
both counts:

```text
0 nested AGENTS.md, 6 personas and 13 skills passed the caveman lint (personas and skills <= 600 prose words each).
```

The MCP `standards_compile_context` tool runs the same code as the CLI: `verify_only` calls
`compiler.VerifyCompiledContext` and a write calls `compiler.CompileContextProjections`
(`internal/compiler/projection.go`, called from `compileContext` in
`cmd/standards-mcp/server.go`). Its verify therefore lints every persona and skill with
`LintAgentText` and checks every persona and plugin skill projection, and prints the same
lines as `compile-context --verify`. `TestMCPVerifyLintsPersonasAndSkills` and
`TestMCPVerifyFailsOnPersonaDrift` in `cmd/standards-mcp/server_projection_test.go` pin it.

`praetorctl audit` and `standards_audit` run one function for this lint,
`compiler.AuditAgentSources`, so both give the same verdict: the pass line above prefixed
`[PASS]`, or a `[FAIL] Agent source caveman lint:` error that joins every failed surface.
Projections are a
different matter: `standards_audit` still verifies no persona or skill projection, so use
`praetorctl audit` or `standards_compile_context` with `verify_only` for those.

600 words was chosen when the persona/skill gate was introduced: at the time it sat between
two skills failing the lint at 532-561 prose words (`caveman`, `social-text`, since pared
down and passing at 412 and 400 words) and the shortest passing skill in the directory at
185 words -- low enough to force a caveman rewrite, high enough not to force splitting a
skill with real rule tables.
An illustrative "bad" prose sample inside a skill (`adhd-format/SKILL.md`'s anti-pattern
block, `caveman/SKILL.md`'s before/after section) is not this file's own prose and is
wrapped in `<!-- caveman:off -->`/`<!-- caveman:on -->`, the same mechanism the package doc
comment names for exactly this case.

### Check rules

| Rule | Fires when |
| :--- | :--- |
| `C1 article-density` | more than 2.0 `a`/`an`/`the` per 100 prose words, judged once the text holds 40 prose words |
| `C2 filler` | "based on", "I think", "note that", "it is important", "it looks like", "in order to", "as requested", "let me", "please" |
| `C3 hedge` | "probably", "seems", "might", "basically", "simply", "just", "really", "actually" |
| `C4 terminal-noise` | an ANSI escape, unsafe control character other than tab or Unicode default-ignorable anywhere; box drawing (U+2500-257F) or emoji outside code |
| `C5 long-sentence` | a sentence over 30 prose words without a `;`, `->` or `:` break; a sentence ends at `.`, `!`, `?` or `:` before a blank, and closing brackets, quotes or emphasis delimiters may sit in between (`**Done.** Next.` holds two sentences) |
| `C6 unclosed-off-region` | `<!-- caveman:off -->` without a later `<!-- caveman:on -->` |
| `C7 word-ceiling` | `Options.MaxProseWords` is set (opt-in, 0 means no ceiling) and `Report.ProseWords` exceeds it |
| `C8 token-ceiling` | `Options.MaxTokens` is set (opt-in, 0 means no ceiling) and `Report.EstimatedTokens` (the whole input, not prose alone) exceeds it |
| `C9 grammar` | `message`, `brief` or `return` text contains a listed article, personal pronoun, copula, auxiliary, modal or politeness token, including straight/curly contractions; `as is` stays permitted by the clarity floor |
| `C10 message-shape` | a brief lacks `goal`/`inputs`/`return`/`evidence`/`task`, a return lacks `verdict`/`changed`/`ran`/`evidence`/`open`, the answer field is not first, or known fields share a line |
| `C11 runtime-source-escape` | `CheckRuntime` only (`internal/caveman/runtime.go`): a line carries a source-only construct that could hide prose, such as an off region, a fence line, a setext underline, structured text, an HTML entity or tag, or a Markdown link |
| `C12 runtime-evidence-pointer` | `CheckRuntime` only: an `evidence:` field carries pointer markers (`sha256`, `lines:`) but is not the complete canonical `evidence: <path> sha256:<12 hex> lines:<n>` form (`evidenceRe`, `internal/caveman/scan.go`) |
| `C13 unclosed-fence` | a fenced code block is still open when the text ends; everything after its opening fence would otherwise count as code and escape every other rule. Only a bare delimiter at least as long as the opener closes it, so a template holding inner fences needs a longer outer fence. A backtick line whose info string holds a backtick (`` ```foo``` flag ``) is an inline code span under CommonMark and opens no fence; a tilde fence's info string may hold backticks |

The 2.0 threshold is measured, not chosen: the prose AGENTS.md read 7.5 articles per 100
prose words, its hand-written caveman rewrite reads 0.2. Source-document `Check` masks quoted
text so a rule that names a banned phrase does not trip itself; adversarial `CheckRuntime`
keeps quoted text visible. C7 and C8 are opt-in, unlike C1-C6 and C13: a ceiling is a
property of one surface (600 prose words for a persona or a skill; the evidence bound,
1500 tokens, for anything checked against it), not of caveman prose everywhere, so
`AgentTextCeiling` is passed explicitly by the persona/skill gate rather than living in
`Check`'s defaults. `Options.Kind` has a zero-value `context` profile for source
compatibility; the CLI defaults to `message` for every input but the context files it
judges as `context` (see "Caveman lint and token estimate" above).

The summary's rule numbers refer to the eight numbered rules in the Caveman skill, not the
`C1`-`C13` finding identifiers. Classification is intentionally conservative:

| Skill rule | Summary classification | Implemented boundary |
| :--- | :--- | :--- |
| 1. Cut grammar | mechanical for `message`, `brief`, `return`; advisory for `context` | runtime kinds use C2, C3, C9; context uses C1-C3 heuristics so policy text can name grammar tokens |
| 2. Fragments | advisory | C10 separates known fields; semantic “one fact” judgment remains |
| 3. Symbols | advisory | whether `->`, `=`, `xN`, `!`, `?` preserve meaning requires judgment |
| 4. Verbatim tokens | advisory in `check` | `floor <before> <after>` performs the mechanical comparison |
| 5. Start with answer | mechanical for `brief` and `return`; advisory for `message` and `context` | C10 requires goal/verdict first |
| 6. Tables only when useful | advisory | cell prose receives C1-C5 and C9; usefulness still requires comparison intent |
| 7. Return shape | mechanical for `return`; advisory for other kinds | C10 requires all return fields |
| 8. Evidence bound | advisory | C8 enforces a supplied token ceiling; line count, artifact placement and producer metadata remain external |

Not prose, and never linted as prose: fenced code (C4 still reports ANSI, unsafe controls and
default-ignorable Unicode there), inline
code, link targets, URLs, headings, HTML comments, ledger field rows such as
`- **Tasks**: 3 open | **Open Bugs**: 0`, hook protocol lines (`PRAETOR_*`), evidence
pointers, and anything between `<!-- caveman:off -->` and `<!-- caveman:on -->`. The
summary line counts the off regions, so an escape stays visible. YAML front matter is not
prose either: a `---` first line through the next `---` line, when the block between them
decodes as a YAML mapping or is written in its shape, is a contract with the harness that
loads the file (a skill's `description:` decides when the skill fires), so `Check` leaves
it out of every prose rule and the summary counts its lines as `front_matter_lines`. The
shape is at least one top-level `key:` entry, and otherwise only indented lines, `-` items
and `#` comments, so a `description:` holding an unquoted colon and blank, which skill
loaders read but YAML does not decode, still counts (`TestCheckFrontMatterUnquotedColon` in
`internal/caveman/check_test.go`). A block with a line of another shape, or one that does
not start on line one, stays prose, so a thematic break cannot hide a paragraph.
`CheckRuntime` reads front matter as prose, its wrapped lines one paragraph, and counts no
`front_matter_lines`. Code spans follow CommonMark: a span closes at
the next backtick run of the same length, a run that never closes is literal backticks,
and a span may wrap across the lines of one paragraph. A fence opens inside a blockquote
(`> ```sh`) and closes when the blockquote ends. Markdown table delimiters
stay structured, but each cell is prose and receives the same phrase, density, sentence and
strict-grammar checks. Lexically complete paths, URLs, flags and diagnostic tokens stay
protected from C9; URL schemes are ASCII case-insensitive. Every other Unicode punctuation
or symbol separator is joined and split during grammar matching, so punctuation alone does
not create a literal. Straight-single,
curly-single, straight-double and curly-double quoted error text stays protected from C2,
C3 and C9; apostrophes inside contractions remain lintable.

### Clarity floor

`caveman.Floor(before, after)` fails a rewrite that lost a fact. The items below must all
survive verbatim, anywhere in the new text, and the counts must not fall:

| Rule | Must survive |
| :--- | :--- |
| `F1` | every inline code span with its backticks; a span that wraps across a line break is one fact, joined with one space, so wrapping or unwrapping it loses nothing, and a span of only blanks is no fact |
| `F2` | every command of a shell fence: each line of a script fence (`bash`, `sh`, `shell`, `zsh`, `ksh`, `csh`, `fish`, `powershell`, `pwsh`, `ps1` and the other names of the table) or of a fence without a language, except blanks and `#` comments; each line of a Windows batch fence (`cmd`, `bat`, `batch`) except `REM` and `::` comments; in a `console`, `shell-session` or `terminal` fence only a line after the `$` prompt and its blank, compared without them; a line continuing a command that ends in `\` counts too. Lines of other fences (Go, JSON, YAML, `text`) are examples, not commands, and the output lines of a session are what its commands printed: `F9` and `F10` hold both. A fence inside a blockquote compares without its `>` markers |
| `F3` to `F5` | every id such as `HISS-17` or `ADR-0010`, link target, and HTML marker |
| `F6` | the count of `MUST`, `SHALL` and `REQUIRED` |
| `F7` | the count of prohibitions: never, do not, don't, must not, no |
| `F8` | the count of numbered bold rules (`1. **...**`) |
| `F9` | every number of a line outside fenced code, table cells and front matter included, of the code of a source or data fence and of the output of a terminal session (see `F10`). A word that holds a digit is one number, whole: `557.3` turning into `557`, `v8.6.0` into `v8.7.0`, `p99.94` into `p99.9` or `-5` into `5` fails, and the digits of `utf-8` are no number of their own. A number followed by a unit and no further digit is the number alone (`24 h` equals `24h`). It may move anywhere a reader still finds it: prose, a code span, a command, fenced code, a list marker or a heading, though not into a comment. Labels of Markdown syntax are no number facts: ordered list markers, quoted ones too, heading section numbers such as `## 3.`, the labels of reference links, reference definitions and footnotes, and HTML entities; nor are numbers inside a code span, id, link target or URL of the original |
| `F10` | every identifier, key or word of the code of a fence whose language is neither a shell nor `mermaid` (Go, JSON, YAML, `text`) and of the output lines of a terminal session, its comments stripped by the syntax of its language (`commentGroups` in `internal/caveman/fence_comments.go`, by Linguist name or alias, cut by `util.StripComments`): line comments, block comments such as `/* */` and `<!-- -->` across lines, and never a marker inside a quoted string. Correcting a comment or reformatting passes; renaming a call, changing a key or a quoted value or commenting out an entry fails. It may move into any fenced code or code span |

The shell fence table is `util.MarkdownShellFence` (`internal/util/markdown_syntax.go`),
the one the documentation reference check reads commands from. The fixtures under
`internal/caveman/testdata/floor/<case>/` replay each rule in both directions
(`TestFloorFixturesReplayBothWays` in `internal/caveman/fixtures_test.go`).

The floor reads both texts with ANSI escape sequences removed (`stripANSI` in
`internal/caveman/ansi.go`, the reader `caveman.Compress` uses), so a colour code is no fact
and a coloured id, directive, number or code word is the one it shows, while an escape left
unterminated on its line stays text with its digits and words
(`TestFloorANSIEscapesEveryRule` and `TestFloorOSCEndsOnItsLine` in
`internal/caveman/floor_test.go`).

### Safe compression

`caveman.Compress` removes ANSI escapes (an escape left unterminated on its line stays text,
and so do the lines below it), turns CRLF into LF, trims and collapses blanks in
prose lines outside code spans, collapses blank-line runs and folds identical consecutive
prose lines into one line ending in `(xN)`. Fenced code (blockquoted fences included), YAML
front matter, structured lines and off regions keep their bytes, and so does a code span
that wraps onto the next line. It never drops or replaces a word: automatic prose compression saved 1-3%
on real inputs and inverted one sentence's meaning.

### One token estimator

`caveman.EstimateTokens` (words × 1.3, `caveman.TokensPerWord`) is the only token
estimator. The SARIF distillation in `internal/lockdown` and the package-docs distiller in
`internal/docdistill` call it, and `praetorctl caveman estimate` prints it per input and in
total.

#### Measuring a rewrite against a git revision

`estimate --base=<rev>` answers what a rewrite saved: it measures each path at the revision
and in the working tree and prints before, after and the signed difference per file and in
total, under the keys of the plain line. The fixture of `TestCavemanEstimateBasePositive` in
`cmd/standardsctl/caveman_baseline_test.go` reports:

```text
base: main = <40-digit commit>
docs/edited.md: bytes=23->11 (-12) lines=1->1 (+0) tokens_est=5->2 (-3)
docs/gone.md: bytes=30->0 (-30) lines=2->0 (-2) tokens_est=7->0 (-7) worktree=absent
docs/kept.md: bytes=14->14 (+0) lines=1->1 (+0) tokens_est=3->3 (+0)
docs/new.md: bytes=0->10 (+10) lines=0->1 (+1) tokens_est=0->2 (+2) base=absent
docs/old/x.md: bytes=4->0 (-4) lines=1->0 (-1) tokens_est=2->0 (-2) worktree=absent
total: inputs=5 bytes=71->35 (-36) tokens_est=17->7 (-10) base_absent=1 worktree_absent=2 worktree_ignored=0
```

| Input | Behaviour |
| :--- | :--- |
| `<rev>` | any revision that names a commit: a branch, a tag, `HEAD~1`, an object id. It is resolved once, in the repository of the first path, and the first line prints the commit, so every read sees one tree. A revision with a shell metacharacter (`HEAD@{1}`) or a leading `-` is refused (`util.ValidateExecArg`) |
| a file | measured on both sides. On one side only it is a row ending in `base=absent` or `worktree=absent`, the missing side counted as zero; on neither side it is an error, so a typo is never a row. A file whose directory was deleted or renamed since the revision is such a `worktree=absent` row too. A file git ignores is measured all the same, because it was asked for by name, and its row ends in `worktree=ignored` |
| a directory | the union of the Markdown files below it at the revision and of those git tracks or would track in the working tree, in lexical order, so a file or a whole subdirectory deleted since the revision is still reported. A directory the working tree no longer holds is its Markdown files at the revision, each `worktree=absent`; one with no such file on either side is an error. A symlink named as the directory is refused |
| `-` | an error: standard input has no revision |

A renamed file is two rows, the old path `worktree=absent` and the new one `base=absent`.
Git runs in the deepest directory of each path that the working tree still holds
(`util.SplitAtExistingDir`) and is given the rest as a literal pathspec, so a path below a
removed or renamed directory is read like any other deleted file, and the paths
`git diff --name-only <rev>` prints at the repository root can be handed over as they are;
`TestCavemanEstimateBaseDeletedDirectory` pins it.

Both sides of a directory describe one set. The working-tree side is read off
`git ls-files --cached --others --exclude-standard` run in that directory: tracked files, and
untracked files that no ignore rule of the repository hides. The tree itself is never
walked. Git does not list a file it ignores, and it lists a nested repository as one entry
without looking inside, so a run at the repository root neither reports nor counts ledgers,
dependencies or other checkouts, however many Markdown files they hold
(`baselineWorktreeFiles` in `cmd/standardsctl/caveman_baseline_worktree.go`,
`TestCavemanEstimateBaseDirectoryCountsWhatGitTracks`,
`TestCavemanEstimateBaseLeftAloneTreeAboveBound`). From the listing the command keeps the
entries that end in `.md` and are regular files; an index entry whose file is gone, a
symlink and a submodule are none (`TestBaselineWorktreeMarkdown`). The listing goes through
`util.RunGitProbe`, which reads no per-user git configuration, so a personal ignore file
does not change the answer between machines. A file the revision holds stays a row whatever
git does with it now: still on disk but ignored since, it is measured on both sides and
marked `worktree=ignored`. The total counts the three marks as `base_absent`,
`worktree_absent` and `worktree_ignored`.

Two bounds apply, and each error says what was counted:

| Bound | Error |
| :--- | :--- |
| 4096 Markdown files that git tracks or would track below one directory (`maxCavemanFiles`) | `directory <path> holds more than 4096 Markdown files that git tracks or would track; name its subdirectories or files` (`TestBaselineWorktreeFilesBound`) |
| 4096 files in one run, files deleted since the revision included | `more than 4096 input files` |
| 4 MiB for one git listing of a directory | the listing fails with `command output exceeds 4194304 bytes per stream`; it is never cut |

A file reached twice is measured twice: named two times, or named beside its directory, its
row is printed two times and added to the total two times, as the plain `estimate` does.
Name each path once.

No row reports a deletion that did not happen:

| Situation | Result | Pinned by |
| :--- | :--- | :--- |
| the directory named is a symlink | the error the reader gives a file below such a link, `confinement root must be a directory, never a symlink`; the reader does not read through the link while git lists its target, which would read every file as deleted. A path may reach the directory through a symlink, the directory itself may not be one | `TestCavemanEstimateBaseRefusesSymlinkedDirectory` |
| a tracked directory was replaced by a symlink | its files are refused by the reader, not counted as deleted | `TestCavemanEstimateBaseSymlinkBoundary` |
| a path is named in another letter case than git tracks it under, on a file system that ignores case (the default on macOS and Windows) | an error naming the tracked spelling relative to the top of the repository, `<path> is tracked as <tracked spelling>`, for a file and for a directory. Git matches a path exactly, so the base side would read as absent. The tracked spellings come from one bounded listing at the top of the repository (`git ls-files --cached --with-tree=<commit>` with an `icase` pathspec), and a spelling counts only when it is the same file (`os.SameFile`). Where case is distinguished, `readme.md` beside a tracked `README.md` is its own file and a `base=absent` row; only a hard link to the tracked file under that second spelling is refused like the spelling itself, because it is the same file | `TestCavemanEstimateBaseTrackedSpelling`, `TestCavemanEstimateBaseCaseInsensitiveFileSystem` |

The base side is read from git as stored (`git ls-tree`, `git cat-file blob`) through
`util.RunGitProbe`, each call under its 5 s bound and the command under the 30 s bound of
`contextopt.MaxDuration`; a blob above 1 MiB or one that is not UTF-8 is an error, as it is
for a working-tree file. On a checkout that converts line ends, the working-tree bytes include
the carriage returns the stored blob lacks; the token estimate counts words and is unaffected.
The implementation is `cavemanEstimateBase` in `cmd/standardsctl/caveman_baseline.go`.

### Surfaces

`--surface=<name>` makes `check` resolve that surface from the repository at `--root`
(default `.`) through the same loader `compile-context` uses. The inputs are read first, so
a missing or oversized input is an error whichever surface is named. When the surface
resolves to `docs` or `social`, the command then returns an error naming the deciding row
and stating that the input was not checked
(`surfaces.docs = docs has no Caveman verdict; input NOT checked`,
`surfaces.operator = docs has no Caveman verdict; input NOT checked`;
`TestCavemanCheckSurfaceWithoutLint` in `cmd/standardsctl/caveman_test.go`); a surface without a
Caveman verdict never produces a green skip:

```bash
praetorctl caveman check --surface=mcp descriptions.md
```

The emission surfaces are `context`, `mcp`, `hooks`, `prompts` and `ledger`; an unset one
is `internal`, so the lint is on by default, and `surfaces.agent` does not reach it. Only the
surface's own key opts it out, and `context` accepts no value but `internal`. An unknown surface name is an error, never a silent fallback.
The decision is recorded in [ADR-0010](../adr/0010-text-register-per-task.md) (decisions
9 and 10); tests and fixtures are in `internal/caveman` and
`cmd/standardsctl/caveman_test.go`.

### Tracked runtime sources

The canonical declaration sits beside `register.surfaces` in
`.standards.yaml`; there is no second source-manifest format. Every selected
file must be Git-tracked below `--root`. `expected`, `not_applicable`, and the
aggregate `sha256` bind the sorted extraction inventory, including path,
semantic selector, surface, kind, parser, decoded-text digest, and exclusion
class. The reported physical line is diagnostic only, so inserting comments
does not churn the digest. Removing a selector, matched value, or classified
exclusion is therefore a gate failure, not an invisible reduction in coverage.

```yaml
register:
  sources:
    expected: 247
    not_applicable: 124
    sha256: "sha256:b722a1c97f22157cbe134d137a1f6e1c5199a061bc2b07c29de46278e6161e4d"
    inputs:
      - path: ".paperclip/harness.json"
        surface: prompts
        kind: message
        format: json
        selector: "operating_contract.*"
      - path: ".paperclip/harness.json"
        surface: prompts
        kind: message
        format: json
        selector: "invariants.*"
      - path: ".config/semgrep/hiss-invariants.yml"
        surface: hooks
        kind: message
        format: yaml
        selector: "rules.*.message"
      - path: ".config/agent/hooks"
        surface: hooks
        kind: message
        format: python
      - path: ".config/lefthook/scripts"
        surface: hooks
        kind: message
        format: python
      - path: "cmd/standards-mcp"
        surface: mcp
        kind: message
        format: go
        selector: "mcp.descriptions"
      - path: "cmd/standards-mcp"
        surface: mcp
        kind: message
        format: go
        selector: "mcp.outputs"
```

`praetorctl caveman check --configured-sources --root=.` verifies coverage,
then lints every decoded value. `praetorctl audit` runs the same checker
(`configuredCavemanInputs` in `cmd/standardsctl/caveman.go`), so the two cannot
disagree about which values are linted: classified exclusions are skipped
before the surface verdict is checked, in both
(`TestConfiguredSourceGatesShareOneChecker`). `make caveman-sources` and
`make verify-all` (which CI runs) call it for Praetor. New adoption writes the
same contract for its generated Paperclip harness and adds the source target to
the generated Makefile. Both `audit` and the dedicated command fail closed when
`register.sources` is absent.

#### A repository with no agent-facing text outside Markdown

A repository that has none says so explicitly, with `expected: 0` and a one-line reason, and
no `inputs`, `not_applicable` or `sha256`:

```yaml
register:
  sources:
    expected: 0
    reason: "no hook, prompt or MCP text outside Markdown"
```

`praetorctl audit` then passes the gate with a line naming the declaration and its reason:

```text
[PASS] Caveman non-Markdown source coverage: register.sources declares no agent-facing text (reason: no hook, prompt or MCP text outside Markdown).
```

`praetorctl caveman check --configured-sources` prints the same declaration followed by
`nothing to check`. The manifest
decoder refuses `expected: 0` without a reason, or with inputs or pins, and a reason on any
other contract; binding a first input means setting `expected` and `sha256` to the values it
extracts, as for any contract (`config.RegisterSources.DeclaresNone`, tests in
`internal/config/register_sources_test.go`). The declaration holds only while
`.paperclip/harness.json`, the text adoption binds, is absent: with a harness on disk both
checks fail and name it. Adoption keeps the declaration byte for byte, `--force` included,
while `paperclip` stays declined; a run that would write a harness refuses before its first
write (`TestAuditDeclinedPaperclipNoSources_Positive`,
`TestAuditDeclinedPaperclipNoSources_Negative` and
`TestAdoptDeclinedPaperclipKeepsNoSources_Boundary` in
`cmd/standardsctl/audit_decline_contract_test.go`). `standards_audit` runs no
`register.sources` gate.

#### Upgrading an adopted repository

Run `praetorctl adopt`. It adds `register.sources` to an existing manifest
without replacing operator fields, and changes only the lines of that block:
comments, blank lines and indentation elsewhere stay as written, including an
indented comment right above the next `register` key on a re-bind. A null
`register` (`register:` with no children, only commented-out children, or
`register: ~`) and a null `sources` value load as undeclared, so adoption fills
them in place instead of failing
(`TestAdoptBindsSourcesUnderNullRegister`,
`TestSetManifestSourcesFillsNullRegisterAndSources`). A flow-style root or
`register` mapping, and an explicitly tagged null (`register: !!null`), is
re-encoded instead, with every value kept
(`internal/adopt/manifest_text.go`). The Paperclip harness it binds depends on
who wrote it:

- A harness byte-identical to what an earlier release synthesized for this
  repository (`harness.json`, and `rules.md` when present) is refreshed to the
  current contract text, which passes the lint, and the contract binds the
  refreshed bytes. A consistent CRLF checkout (`core.autocrlf=true` on Windows)
  counts as those bytes; mixed line endings count as an edit. A `rules.md` that
  was removed stays removed. The recognized earlier texts, including both push
  protocols a release prescribed (the single AGit push, and the AGit push plus
  the review-branch push since #458), are pinned in
  `internal/paperclip/harness.go` (`PriorGenerated`). A `rules.md` counts in
  either layout a release wrote: unwrapped items before #477, or the
  markdownlint-clean layout since
  (`TestPriorGeneratedAcceptsBothRulesLayoutsOfItsHarness`).
- Any other harness is operator-owned. Adoption keeps its bytes, under
  `--force` too. A manifest without `register.sources` gets a contract bound
  to the harness's decoded values. A declared contract must already match the
  harness: adoption never re-binds it to an edit, under `--force` either, so
  after editing the harness recompute the pins. Set `expected`,
  `not_applicable` and `sha256` to the extracted values the adoption error
  reports, all in one run, or read them from
  `praetorctl caveman check --configured-sources --root=.` once the harness is
  staged; or delete `.paperclip/harness.json` and rerun `praetorctl adopt` to
  regenerate it, except with the paperclip step declined or the repository
  identity unresolved, where adoption writes no harness and the error names
  restoring the bound bytes instead (`TestAdoptEditedHarnessFailsBeforeWritingWithRemedy` and
  `TestAdoptForceKeepsReboundEditedOperatingContract` in
  `internal/adopt/adopt_test.go`,
  `TestAdoptForceEditedHarnessNeedsRecomputedPins` in
  `cmd/standardsctl/audit_paperclip_force_test.go`). `--force` changes one
  value: a `platform` naming another repository than the identity, which
  audit rejects, is set to the identity in place, and every other byte of the
  file stays. A `Platform` key in another case is that member, as the loader
  reads it (`paperclip.PatchPlatform`,
  `TestAdoptForcePatchesOnlyHarnessPlatform`,
  `TestAdoptCaseVariantPlatformKey`). When the kept values fail the lint, edit
  them and recompute the pins, or regenerate the harness.
- With `adoption.decline: [paperclip]`, adoption never writes a harness, in
  either mode. An existing one stays byte for byte and the contract binds it.
  A declined step never writes, so `--force` does not refresh it: when a kept
  harness fails the lint (a released one flags `I/O`, `is` and `must`), either
  drop `paperclip` from `adoption.decline` and rerun `praetorctl adopt`, which
  refreshes released output and re-binds the contract, or edit the failing
  values by hand and set `expected`, `not_applicable` and `sha256` from
  `praetorctl caveman check --configured-sources --root=.`. With no harness on
  disk, adoption adds no `register.sources` and reports `preserved without
  register.sources`; declare the contract for the repository's own agent-facing
  text, or [declare that it has none](#a-repository-with-no-agent-facing-text-outside-markdown),
  or audit keeps failing. Audit passes the absent harness itself, naming the
  decline ([declined steps](adoption-verification.md#what-audit-does-with-a-declined-step)).
- Without a repository identity (no `repository.owner` and `repository.name`
  in `.standards.yaml` and no origin remote), adoption cannot name the harness
  platform, so it writes no harness, in either mode (BUG-852). An existing
  harness stays byte for byte and the contract binds it. With none on disk,
  adoption adds no `register.sources` and reports `repository identity is
  unresolved`, both for an existing manifest and for the one a first adoption
  scaffolds; set the identity or add the remote and rerun
  (`TestAdoptUnresolvedIdentityBindsOnlyAnExistingHarness` and
  `TestAdoptFreshManifestReportsUnboundSources` in
  `internal/adopt/harness_plan_test.go`).

When adoption writes the harness, a refresh of earlier output or the `--force`
platform patch, it re-binds an existing contract to the written bytes. It keeps
every declared input, including rows an operator added, and recomputes only
`expected`, `not_applicable` and `sha256`
(`TestAdoptForcePlatformPatchRebindsExtendedContract`). A harness adoption
keeps leaves the contract as declared, under `--force` too
(`TestAdoptForceKeepsOperatorHarnessAndExtendedContract`). Adoption
never re-blesses drift it did not cause: in both modes, a declared contract that
fails its own gate before the run stops adoption, before its first write, with
`existing register.sources fails its configured gate`. The error carries every
differing value and names the remedy: set the pins to those values, or, when
the drift is an edited harness, delete it and rerun `praetorctl adopt`.

A harness adoption writes where none existed is Praetor output, not drift.
When every declared input selects `.paperclip/harness.json`, adoption binds
the pins to the bytes it writes without holding them to the old values first,
and reports `Re-bound register.sources to the Paperclip harness this run
writes where none existed`. Pins bound to a harness an earlier release wrote
therefore follow the regenerated one
(`TestAdoptDeletedHarnessRebindsPinsAcrossReleases`). A contract that also
selects another file keeps its gate, because its one digest cannot tell that
file's drift from the new harness; the error then reports values that cover
the harness the run would write
(`TestAdoptAbsentHarnessKeepsGateOfMixedContract`).

Migration (#502): `--force` used to regenerate an edited harness and re-bind
the contract to the regenerated bytes. It now keeps the edit and stops with
that remedy until the pins match it.

The fixtures, including the harness the 462e3f3a release wrote, are in
`internal/adopt/manifest_sources_test.go`,
`internal/adopt/absent_harness_rebind_test.go` and
`internal/paperclip/prior_test.go`.

`shell` extracts static quoted `echo`, bounded `%s` `printf`, here-document
output, and exact trailing `>&2` or `1>&2` redirection. Shell expansion and
interpolated here-documents fail as unverified instead of disappearing.

`python` recognizes `print`, `agent_message`, `sys.stderr.write`,
`sys.stdout.write`, `sys.stdout.buffer.write`, and `stream.write`. It decodes
Python escapes, joins adjacent or `+`-concatenated text, and replaces f-string
expressions and dynamic concatenation terms with `{value}`. A template must
contain agent-owned static text. An entirely computed value requires one exact
adjacent classification comment: `structured-protocol`,
`untrusted-passthrough`, or `protocol-marker`; unsupported arguments and broad
classifications fail closed.

Generic `go` extraction selects one named static `[]string` or `[...]string`
table through `table.*` or `table.<index>`. The `mcp.descriptions` selector
censuses tool and property descriptions. `mcp.outputs` censuses tool results,
governed builders, `http.Error`, and transport writer output. Static templates
are linted; governed composition and the fixed `structured-json`,
`untrusted-passthrough`, `protocol`, and `shared-source` classes are count- and
digest-bound as not applicable. `shared-source` (`mcpTextShared`) is text
another praetor package authors once and also prints elsewhere: the
`standards_explain_rule` explanation that `internal/hisscatalog` also renders
into the wiki, the `standards_compile_context` report that `internal/compiler`
writes for the CLI too, the adopt pillar line and the `standards_plan` drift
verdict (`adopt.FormatPlanStatus`) the CLI prints. The owning package stays the
text's one source (HISS-19), and `http.Error` accepts only `protocol` or
`untrusted-passthrough` (`TestExtractGoMCPRuntimeClassifiesSharedSourceText` in
`internal/cavemansource/extract_test.go`). Dynamic text without one of those
narrow wrappers, raw builder storage access, and helper implementations that
differ from the fixed contract fail closed.

`json` and `yaml` use dotted selectors; `*` selects every mapping value or
sequence element, and a numeric segment selects one sequence index. Escapes are
decoded before linting, so checked static text matches the runtime string.

For an ad-hoc directory check, exact `--surface=hooks` selects `.sh,.py` unless
`--ext` is explicit. Checks without that surface retain the historical `.md`
default:

```bash
praetorctl caveman check --root=. --surface=hooks scripts
praetorctl caveman check --root=. --surface=prompts \
  --selector='agent.*.prompt' config/agents.json
```

A directory matching no requested files, an extractor producing no values, or
a non-internal surface returns an error rather than a green no-op. One contract
is bounded to 64 inputs and 64 discovered files. One selector's match set or one
Go string table yields at most 256 values; the whole contract extracts at most
16,384 applicable values and 16,384 explicitly classified exclusions, one full
table per input (`config.MaxRegisterSourceOutputs`). Each file is at most 1 MiB, each value at
most 64 KiB and at most 1,024 logical lines; all selected text is at most 1
MiB. Positive, negative, exact-limit, and +1 fixtures live in
`internal/cavemansource` and
`cmd/standardsctl/caveman_source_test.go`.

### Ceilings

`--max-words=N` (C7) and `--max-tokens=N` (C8) add an opt-in ceiling to `check`, on top of
whatever C1-C6 and C11 already judge; 0 (the default) means no ceiling. A negative value is
refused before any input is read, with an error naming the flag, so a typo such as
`--max-tokens=-1` cannot switch a ceiling off (`validateCavemanCeilings` in
`cmd/standardsctl/caveman.go`, `TestCavemanCheckRefusesNegativeCeilings`):

```bash
praetorctl caveman check --kind=context --max-words=600 .agents/skills/<skill>/SKILL.md
praetorctl caveman check --kind=return --max-tokens=1500 .workingdir/evidence/candidate-return.md
```

The persona/skill gate calls the same `caveman.Options.MaxProseWords` field programmatically
(`compiler.AgentTextCeiling`, 600); the flags exist so any other surface can be capped the
moment its text is a file, including decoded runtime sources and a return or a brief a
dispatch path writes out before sending it, and the evidence-pointer bound
(`register.evidence`, default 1500 tokens) the same way. Repair planning and execution call
the shared checker directly on their owned runtime fields, without first writing them to a
file. Runtime values substituted into MCP and hook templates, notebook and Paperclip
prompts, ledger text, popup questions and native-client traffic still need their own
producer or capture wiring and remain unverified; see "What is not enforced".
