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
| `internal` | another agent | `verdict: pass. changed: internal/compiler/register.go. ran: go test ./internal/compiler/. evidence: .workingdir/evidence/run.log sha256:0123456789ab lines:412. open: none.` |

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
    # Emission surfaces: unset means "same as agent". See "Caveman lint" below.
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
| `surfaces.<mcp\|hooks\|prompts\|ledger>` | unset, resolves as `surfaces.agent` | one of the three registers; closed key set | same as the first row |
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
   its own key when the manifest writes it and to `surfaces.agent` otherwise; the task never
   changes it either.
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

Register compliance on human-typed surfaces is advisory: nothing measures whether a
pull-request body reads as social prose, and nothing measures whether an agent return
follows `caveman`. Three things are mechanical: the block in AGENTS.md must match the
manifest, AGENTS.md must pass the caveman lint (see "The context gate" below), and a
configured `max_tokens` bounds the provider request of a repair run.

## Caveman lint and token estimate

The internal register is measurable. `internal/caveman` lints agent-facing text, proves a
rewrite lost nothing, and estimates tokens; `praetorctl caveman` runs it on files:

```bash
praetorctl caveman check AGENTS.md .agents/agents/
praetorctl caveman estimate AGENTS.md
```

`check` prints one summary line per input, then its findings as `<path>:<line> <rule>:
<excerpt>`, and exits non-zero when any input fails. Line 0 means the whole text. A
directory expands to the Markdown files below it; `-` reads standard input. The text
register block is blanked before the lint, exactly as the context gate does it, and the
summary line counts its lines. The prose AGENTS.md that the caveman rewrite replaced,
frozen as `internal/compiler/testdata/agents-floor.txt`, fails:

```text
agents-floor.txt: FAIL prose_words=1035 articles=90 density=8.7/100 limit=2.0 off_regions=0 register_block_lines=13 findings=2
agents-floor.txt:0 C1 article-density: 8.7 articles per 100 prose words (90/1035), limit 2.0
agents-floor.txt:76 C5 long-sentence: 39 words: your pull requests go stale when ...
```

The current AGENTS.md passes:

```text
AGENTS.md: PASS prose_words=721 articles=2 density=0.3/100 limit=2.0 off_regions=0 register_block_lines=13 findings=0
```

`praetorctl caveman estimate` puts the rewrite at 10,333 bytes and about 1,911 tokens,
down from 12,308 bytes and about 2,315 tokens; CLAUDE.md went from 168 to 115 lines.

### The context gate

`praetorctl compile-context --verify` and `praetorctl audit` run the lint over the
canonical AGENTS.md, and so do their MCP mirrors (`standards_compile_context` with
`verify_only`, `standards_audit`). Any finding fails the gate; the error quotes the first
five findings and names the fix. A pass prints the counts behind it:

```text
AGENTS.md: caveman lint passed: 721 prose words, 0.3 articles per 100 (limit 2.0), 13 register block lines left to the renderer.
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
below its end marker; `praetorctl caveman check AGENTS.md` then lists what is left to
rewrite in the repository's own part.

A rewrite of praetor's own AGENTS.md must keep every fact of the prose version:
`internal/compiler/canonical_floor_test.go` runs the clarity floor below against the frozen
fixture and fails when a rule id, a `MUST`, a prohibition, a numbered rule, a command or a
link disappears. The lint and gate code live in `internal/compiler/caveman_lint.go`.

### Check rules

| Rule | Fires when |
| :--- | :--- |
| `C1 article-density` | more than 2.0 `a`/`an`/`the` per 100 prose words, judged once the text holds 40 prose words |
| `C2 filler` | "based on", "I think", "note that", "it is important", "in order to", "as requested", "let me", "please" |
| `C3 hedge` | "probably", "seems", "might", "basically", "simply", "just", "really", "actually" |
| `C4 terminal-noise` | an ANSI escape anywhere; box drawing (U+2500-257F) or emoji outside code |
| `C5 long-sentence` | a sentence over 30 prose words without a `;`, `->` or `:` break |
| `C6 unclosed-off-region` | `<!-- caveman:off -->` without a later `<!-- caveman:on -->` |

The 2.0 threshold is measured, not chosen: the prose AGENTS.md read 8.7 articles per 100
prose words, its hand-written caveman rewrite reads 0.3. C2 and C3 ignore quoted text, so a rule that names a
banned phrase in quotes does not trip itself.

Not prose, and never linted as prose: fenced code (C4 still reports ANSI there), inline
code, link targets, URLs, headings, table rows, HTML comments, ledger field rows such as
`- **Tasks**: 3 open | **Open Bugs**: 0`, hook protocol lines (`PRAETOR_*`), evidence
pointers, and anything between `<!-- caveman:off -->` and `<!-- caveman:on -->`. The
summary line counts the off regions, so an escape stays visible.

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
follows `surfaces.agent`, except `context`, which is always `internal`. An unknown surface name is an error, never a silent fallback.
The decision is recorded in [ADR-0010](../adr/0010-text-register-per-task.md) (decisions
9 and 10); tests and fixtures are in `internal/caveman` and
`cmd/standardsctl/caveman_test.go`.
