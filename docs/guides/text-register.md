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
   forge or the documentation, and no budget applies.
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

Operators call the internal register "caveman", and the
[`caveman` skill](../../.agents/skills/caveman/SKILL.md) makes that form concrete. The
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
follows `caveman`. Two things are mechanical: the block in AGENTS.md
must match the manifest, and a configured `max_tokens` bounds the provider request of a
repair run.
