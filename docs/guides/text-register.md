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
`--verify` and `praetorctl audit`. A repository whose AGENTS.md has no block yet is reported
the same way until `compile-context` runs once; it appends the section at the end of the
file when it finds no markers.

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
```

| Key | Default | Bound | Error when violated |
| :--- | :--- | :--- | :--- |
| `surfaces.<forge\|docs\|agent>` | `social`, `docs`, `internal` | one of the three registers; closed key set | `unsupported text register "<v>"`, `unknown register surface "<k>"` |
| `surfaces.context` | `internal`, whatever `surfaces.agent` says | `internal` only: the caveman gate on AGENTS.md has no opt-out | `register surface "context" is fixed to internal: ...` |
| `surfaces.<mcp\|hooks\|prompts\|ledger>` | unset, resolves as `internal` (`surfaces.agent` does not reach it) | one of the three registers; closed key set | same as the first row |
| `tasks.<label>` | the four rows above, register only | at most 64 rows; label must be a `target_tasks` label | `register tasks exceed 64 rows`, `invalid register task label "<k>"`, `register task "<k>" is not a declared target_tasks label` |
| `tasks.<label>.max_tokens` | none | 256..8192 when written | `register max_tokens for "<k>" must be 256..8192` |
| `evidence.inline_max_lines` | 58 | 1..58, tighten only | `register evidence bound must be 1..58` |
| `evidence.inline_max_tokens` | 1500 | 1..1500, tighten only | `register evidence bound must be 1..1500` |

No default row carries `max_tokens`, and the rendered block prints no token numbers for
tasks. A budget is a dispatch parameter: set one after repair reports show what a task
class actually spends.

### Resolution

`RegisterPolicy.Resolve(surface, task)` decides in this order:

1. The `forge` and `docs` surfaces own their audience. The task never changes who reads the
   forge or the documentation, and no budget applies. The `context` surface is always
   `internal`. Any other emission surface (`mcp`, `hooks`, `prompts`, `ledger`) resolves to
   its own key when the manifest writes it and to `internal` otherwise; neither the task nor
   `surfaces.agent` changes it.
2. On the `agent` surface, or when no surface is named, the task row wins; without a row
   the register is `surfaces.agent`.
3. The result names the winning row (`surfaces.forge`, `tasks.ci_debugging`,
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

`praetorctl dogfood repairs --task <label>` resolves the same row. Each planned job carries
`register` and `max_output_tokens`, and its instructions end with one register sentence. A
`dogfood repairs run` applies the budget as the provider's `max_output_tokens` and records
the register beside provider usage in its report. The budget can only lower the limit of
the run configuration; it never raises it.

Only the rows a manifest writes are checked against the label set. The default rows are
not, so a repository with its own labels is never failed by rows it did not write.

## Evidence

Evidence longer than `inline_max_lines` lines or `inline_max_tokens` estimated tokens never
travels inline. Write it to a file under `.workingdir/evidence/` (private and Git-ignored,
AGENTS.md rule 10) or to the ephemeral directory a gate already uses, and return one
pointer line:

```text
evidence: <path> sha256:<12 hex> lines:<n>
```

The reader fetches the file only when a decision depends on it. Logs, transcripts, full test
output and fetched web pages are always artifacts, whatever their length.
`config.EvidencePointer` produces the line, and the SARIF distillation of
`internal/lockdown` ends its summary with it. The defaults originate there: 58 lines and
1500 tokens are the distillation cap, and `lockdown.MaxDistillLines` and `MaxDistillTokens`
alias the config constants. Tightening the manifest values lowers the numbers the block
prints; it does not yet re-parameterise the distillation cap itself.

## Adopted repositories

`praetorctl adopt` writes the default section into the harness, so a fresh adoption
verifies. When the adoptee later writes its own `register:` section, its next
`compile-context` re-splices the block from that manifest. An adoptee without a
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
(ADR-0010, "Alternatives considered"): nothing measures whether a pull-request body reads
as social prose.

Mechanical: the block in AGENTS.md must match the manifest; AGENTS.md, every canonical
persona under `.agents/agents/` and every canonical skill under `.agents/skills/` must pass
the caveman lint (see "The context gate" below); a configured `max_tokens` bounds the
provider request of a repair run; `praetorctl caveman check --max-words`/`--max-tokens`
makes a per-surface ceiling enforceable on any text a check can read as a file (see
"Ceilings" below). `check` defaults to the strict runtime-message grammar; `--kind=brief`
and `--kind=return` add their documented schema, while `--kind=context` selects the context
gate's compatibility profile. Every result identifies which numbered skill rules remained
advisory.

Repair planning and execution call `config.ValidateEmission` before dispatch or source
mutation. The planner records its brief verdict. The executor records the trusted prompt
prefix as a brief, the separate Responses API instructions as a message, and the proposal
summary as a return. Internal failures stop before the next boundary; `social` and `docs`
record `not_applicable`. Each record names the resolved register, token ceiling, surface,
kind and source; a positive ceiling is enforced for internal text.

Still not mechanically checked: dynamic MCP tool descriptions and results; hook and gate
diagnostics; notebook prompts; Paperclip synthesis (#321); `.workingdir` ledger free text;
popup question text; and native-client chats or hooks (#415). No `internal` label may imply
coverage of those surfaces. Static extraction from non-Markdown source remains #364. A
green repository gate proves tracked context text; repair reports additionally prove only
the runtime repair fields whose validation records are present.

## Surfaces without a register row

Two audiences the schema does not (yet) name a `RegisterSurface` for:

- **Chat replies to the operator.** Always full prose, never `caveman`, per this guide and
  per operator direction recorded outside this repository. Not a `RegisterPolicy` surface:
  adding one is `internal/config/register.go` schema growth, and Q-059's decision covered
  the four emission surfaces only. There is nothing to configure and nothing to opt out of.
- **Popup question text (`AskUserQuestion` and equivalent).** AGENTS.md rule 4 mandates the
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
```

`check` prints one summary line per input, then its findings as `<path>:<line> <rule>:
<excerpt>`, and exits non-zero when any input fails. Line 0 means the whole text. A
directory expands to the Markdown files below it; `-` reads standard input. The text
register block is blanked before the lint, exactly as the context gate does it, and the
summary line counts its lines. It also prints the selected contract plus mechanically
checked and advisory Caveman skill-rule numbers; `PASS` covers only the mechanical rows.
The prose AGENTS.md that the caveman rewrite replaced,
frozen as `internal/compiler/testdata/agents-floor.txt`, fails:

```text
agents-floor.txt: FAIL prose_words=1035 articles=90 density=8.7/100 limit=2.0 off_regions=0 register_block_lines=13 tokens_est=2061 findings=2 contract=context mechanical_rules=none advisory_rules=1,2,3,4,5,6,7,8
agents-floor.txt:0 C1 article-density: 8.7 articles per 100 prose words (90/1035), limit 2.0
agents-floor.txt:76 C5 long-sentence: 39 words: your pull requests go stale when ...
```

The current AGENTS.md passes:

```text
AGENTS.md: PASS prose_words=917 articles=2 density=0.2/100 limit=2.0 off_regions=0 register_block_lines=13 tokens_est=1658 findings=0 contract=context mechanical_rules=none advisory_rules=1,2,3,4,5,6,7,8
```

`praetorctl caveman estimate` puts the rewrite at 10,333 bytes and about 1,911 tokens,
down from 12,308 bytes and about 2,315 tokens; CLAUDE.md went from 168 to 115 lines.

`floor <before> <after>` runs the clarity floor on a rewrite: it exits non-zero when
`<after>` lost a code span, a fenced command, an id, a link target or an HTML marker of
`<before>`, or carries fewer MUST-type directives, prohibitions or numbered rules. Findings
name their line in `<before>` (line 0 for a count). Either input can be `-`, not both. A
rewrite into the internal register is acceptable when `check` passes on it and `floor`
passes from the original to it. Runtime message, brief and return profiles run on demand;
the repository gates call the context profile.

### The context gate

`praetorctl compile-context --verify` and `praetorctl audit` run the lint over the
canonical AGENTS.md, and so do their MCP mirrors (`standards_compile_context` with
`verify_only`, `standards_audit`). Any finding fails the gate; the error quotes the first
five findings and names the fix. A pass prints the counts behind it:

```text
AGENTS.md: caveman lint passed: 917 prose words, 0.2 articles per 100 (limit 2.0), 13 register block lines left to the renderer.
```

The whole file is linted, including what a repository wrote below the praetor harness. Only
the text register block is left out: `compile-context` renders it, nobody edits it by hand,
and its wording belongs to its renderer (`compiler.MaskRegisterBlock`). Compiling without
`--verify` never lints, so a prose edit still compiles and fails on the next verify.

The gate has no opt-out, no warning mode and no grace period, in praetor and in every
adopted repository. It reads no manifest, so neither `surfaces.agent` nor any task row can
switch it off. `register.surfaces.context` is fixed to `internal`: writing `docs` or
`social` there is rejected when the manifest loads (`config.ContextRegister`,
`internal/config/register_test.go`).

A repository adopted before this gate carries the old prose harness and fails after the
upgrade. `praetorctl adopt --force` rewrites the harness in caveman and keeps everything
below its end marker; `praetorctl caveman check --kind=context AGENTS.md` then lists what is left to
rewrite in the repository's own part.

A rewrite of praetor's own AGENTS.md must keep every fact of the prose version:
`internal/compiler/canonical_floor_test.go` runs the clarity floor below against the frozen
fixture and fails when a rule id, a `MUST`, a prohibition, a numbered rule, a command or a
link disappears. The lint and gate code live in `internal/compiler/caveman_lint.go`.

### The persona and skill gate

`SurfaceContext` covers more than AGENTS.md by its own doc comment: "AGENTS.md, the
compiled vendor files, personas and skills" (`internal/config/register.go`). The CLI's
`compile-context --verify` and `audit` (`cmd/standardsctl`) therefore also run the caveman
lint, plus a 600-prose-word ceiling (`compiler.AgentTextCeiling`), over every file under
`.agents/agents/*.md` and every `.agents/skills/*/SKILL.md` (`compiler.LintAgentText`,
`cmd/standardsctl/caveman_gate.go`). No opt-out, same as AGENTS.md itself: personas and
skills are agent-only text under `SurfaceContext`, not an emission surface a manifest can
turn off. A pass prints:

```text
6 personas and 13 skills passed the caveman lint (<= 600 prose words each).
```

The MCP mirrors (`standards_compile_context`, `standards_audit`, `cmd/standards-mcp`) are a
separate, smaller implementation that already did not verify persona/skill projection sync
before this change (`standards_audit`'s own comment: "Run 'praetorctl audit' for the full
CLI gate set"); they still only run `compiler.LintContext` over AGENTS.md and do not yet
call `LintAgentText`. Bringing them to parity is unclaimed follow-up work, not part of this
change.

600 words was chosen because it sits between the two skills that failed the lint at
532-561 prose words (`caveman`, `social-text`) and the shortest passing skill in the
directory at 185 words (`.workingdir/planning/register-gaps-20260919.md`): low enough to
force a caveman rewrite, high enough not to force splitting a skill with real rule tables.
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
| `C4 terminal-noise` | an ANSI escape anywhere; box drawing (U+2500-257F) or emoji outside code |
| `C5 long-sentence` | a sentence over 30 prose words without a `;`, `->` or `:` break |
| `C6 unclosed-off-region` | `<!-- caveman:off -->` without a later `<!-- caveman:on -->` |
| `C7 word-ceiling` | `Options.MaxProseWords` is set (opt-in, 0 means no ceiling) and `Report.ProseWords` exceeds it |
| `C8 token-ceiling` | `Options.MaxTokens` is set (opt-in, 0 means no ceiling) and `Report.EstimatedTokens` (the whole input, not prose alone) exceeds it |
| `C9 grammar` | `message`, `brief` or `return` text contains a listed article, personal pronoun, copula, auxiliary, modal or politeness token, including straight/curly contractions; `as is` stays permitted by the clarity floor |
| `C10 message-shape` | a brief lacks `goal`/`inputs`/`return`/`evidence`/`task`, a return lacks `verdict`/`changed`/`ran`/`evidence`/`open`, the answer field is not first, or known fields share a line |

The 2.0 threshold is measured, not chosen: the prose AGENTS.md read 8.7 articles per 100
prose words, its hand-written caveman rewrite reads 0.3. C2, C3 and C9 ignore quoted text,
so a rule that names a banned phrase in quotes does not trip itself. C7 and C8 are opt-in,
unlike C1-C6: a ceiling is a property of one surface (600 prose words for a persona or a skill; the evidence bound,
1500 tokens, for anything checked against it), not of caveman prose everywhere, so
`AgentTextCeiling` is passed explicitly by the persona/skill gate rather than living in
`Check`'s defaults. `Options.Kind` has a zero-value `context` profile for source
compatibility; the CLI explicitly defaults to `message`.

The summary's rule numbers refer to the eight numbered rules in the Caveman skill, not the
`C1`-`C10` finding identifiers. Classification is intentionally conservative:

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

Not prose, and never linted as prose: fenced code (C4 still reports ANSI there), inline
code, link targets, URLs, headings, HTML comments, ledger field rows such as
`- **Tasks**: 3 open | **Open Bugs**: 0`, hook protocol lines (`PRAETOR_*`), evidence
pointers, and anything between `<!-- caveman:off -->` and `<!-- caveman:on -->`. The
summary line counts the off regions, so an escape stays visible. Markdown table delimiters
stay structured, but each cell is prose and receives the same phrase, density, sentence and
strict-grammar checks. Bare paths and URLs stay protected from C9. Straight-single,
curly-single, straight-double and curly-double quoted error text stays protected from C2,
C3 and C9; apostrophes inside contractions remain lintable.

### Clarity floor

`caveman.Floor(before, after)` fails a rewrite that lost a fact. The items below must all
survive verbatim, anywhere in the new text, and the counts must not fall:

| Rule | Must survive |
| :--- | :--- |
| `F1` to `F5` | every inline code span, fenced command line (not diagrams, blanks or `#` comments), id such as `HISS-17` or `ADR-0010`, link target, and HTML marker |
| `F6` | the count of `MUST`, `SHALL` and `REQUIRED` |
| `F7` | the count of prohibitions: never, do not, don't, must not, no |
| `F8` | the count of numbered bold rules (`1. **...**`) |

### Safe compression

`caveman.Compress` removes ANSI escapes, turns CRLF into LF, trims and collapses blanks in
prose lines outside code spans, collapses blank-line runs and folds identical consecutive
prose lines into one line ending in `(xN)`. Fenced code, structured lines and off regions
keep their bytes. It never drops or replaces a word: automatic prose compression saved 1-3%
on real inputs and inverted one sentence's meaning.

### One token estimator

`caveman.EstimateTokens` (words × 1.3, `caveman.TokensPerWord`) is the only token
estimator. The SARIF distillation in `internal/lockdown` and the package-docs distiller in
`internal/docdistill` call it, and `praetorctl caveman estimate` prints it per input and in
total.

### Surfaces

`--surface=<name>` makes `check` resolve that surface from the repository at `--root`
(default `.`) through the same loader `compile-context` uses. When the surface resolves to
`docs` or `social`, the command prints which row decided it and skips the lint:

```bash
praetorctl caveman check --surface=mcp descriptions.md
```

The emission surfaces are `context`, `mcp`, `hooks`, `prompts` and `ledger`; an unset one
is `internal`, so the lint is on by default, and `surfaces.agent` does not reach it. Only the
surface's own key opts it out, and `context` accepts no value but `internal`. An unknown surface name is an error, never a silent fallback.
The decision is recorded in [ADR-0010](../adr/0010-text-register-per-task.md) (decisions
9 and 10); tests and fixtures are in `internal/caveman` and
`cmd/standardsctl/caveman_test.go`.

### Ceilings

`--max-words=N` (C7) and `--max-tokens=N` (C8) add an opt-in ceiling to `check`, on top of
whatever C1-C6 already judge; 0 (the default) means no ceiling:

```bash
praetorctl caveman check --kind=context --max-words=600 .agents/skills/example/SKILL.md
praetorctl caveman check --kind=return --max-tokens=1500 .workingdir/evidence/candidate-return.md
```

The persona/skill gate calls the same `caveman.Options.MaxProseWords` field programmatically
(`compiler.AgentTextCeiling`, 600); the flags exist so any other surface can be capped the
moment its text is a file, including a return or a brief a dispatch path writes out before
sending it, and the evidence-pointer bound (`register.evidence`, default 1500 tokens) the
same way. Repair planning and execution call the shared checker directly on their owned
runtime fields, without first writing them to a file. Dynamic MCP and hook text, notebook
and Paperclip prompts, ledger text, popup questions and native-client traffic still need
their own producer or capture wiring and remain unverified; see "What is not enforced".
