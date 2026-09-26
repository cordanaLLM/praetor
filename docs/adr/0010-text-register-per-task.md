# ADR-0010: Text Register per Audience and Task Class

## Status

Accepted — 2026-09-26. Proposed 2026-09-17; amended 2026-09-18 (decisions 9 and 10, the caveman module; decision 11, the context gate); amended 2026-09-21 (decision 12, checker contract parity; decision 13, runtime repair enforcement).

## Context

Praetor text reaches three audiences with three different costs. A maintainer reads issues,
pull-request bodies and review comments on the forge. A newcomer or an expert reads `docs/`.
An agent reads briefs, fan-out prompts and workflow returns, where every token is paid for
again on the next hop. Until this record no configuration named these audiences. The only
register rule was AGENTS.md rule 3 "Lead with Output", which applies one voice to all three,
and the only bounded surface was the SARIF distillation in `internal/lockdown` (58 lines,
1500 tokens).

The operator's direction (paraphrased): three registers — social for the forge, docs for
documentation, internal (terse, telegraphic) for agent-to-agent traffic including research
fan-outs; logs and long evidence travel as files and are fetched back only when needed; the
register is configurable and scaled per task class, never a global hard-coded switch; the
rule applies to this repository's own agent operation and to the product (dispatch,
personas, compiled context, adopted repositories); grunt work goes to cheap providers, the
frontier model keeps judgment.

Constraints that shape the decision:

- The router already declares a task vocabulary: `target_tasks` in
  `.config/models/routing.yaml`, mirrored by `defaultRoutingTiers` in
  `internal/router/sync.go` and matched exactly by `hasRoutingTag` in
  `internal/router/task.go`. A second label set would be a HISS-19 defect.
- `Overrides` is documented as a strictness lattice (`internal/config/config.go`), and
  `ResolvedPolicy` is sealed into `EffectivePolicy.SHA256` (`internal/config/effective.go`,
  `seal`). A register is a choice, not a bound, and must not enter that lattice.
- The compiled context reaches six vendor files through one renderer
  (`internal/agentcontext/render.go`, budget 300 lines). An in-flight change to that
  renderer makes a `## <Vendor>` H2 private to one target and every other line shared.
- AGENTS.md is the hand-edited canonical source (rule 6; `docs/standards/hiss-16-spec.md`).

## Decision

1. **Three registers, one enum.** `social`, `docs`, `internal`, decoded like
   `BranchReviewMode`: a string enum that rejects unknown, empty and non-string values at
   the source boundary.
2. **Manifest-owned, repository-only, outside the lattice.** A top-level `register:`
   section in `.standards.yaml`, a sibling of `adoption:`. It is not part of
   `ResolvedPolicy`, `Join` or `ApplyOverrides`, so fleet and profile layers cannot set it
   and the resolved policy of no repository changes because of it. A repository that writes
   the section changes its own manifest bytes, and with them the `repository` source digest
   inside `EffectivePolicy.SHA256`, exactly as any other edit of that file does.
3. **The surface owns the audience; the task row owns the agent surface.**
   `surfaces.forge` and `surfaces.docs` fix who reads the forge and the docs.
   `tasks.<label>` sets the register of that task's own product (its brief and return, the
   `agent` surface) and an optional `max_tokens` output budget. A `ci_debugging` run
   therefore writes an internal return and a social pull-request body without a second
   label. Keys of `tasks` are the router's `target_tasks` labels; an unknown label in the
   manifest fails `compile-context` and `audit`.
4. **No default budgets.** The `max_tokens` field is bounded (256..8192, the limits a
   provider request accepts) and validated, but the shipped defaults carry a register only
   and the rendered block prints no token numbers for tasks. A budget is a dispatch
   parameter that must come from measured runs, not from an opinion in a default row.
5. **One emission block, spliced by the existing pipeline.** `compile-context` renders a
   bounded block from the manifest and splices it into AGENTS.md under `## Text Register`,
   between `<!-- praetor:register:start -->` and `<!-- praetor:register:end -->`;
   `--verify` and `audit` fail on drift. The H2 title is not a vendor name, so all six
   targets share it. For that block only, `.standards.yaml` is the source and AGENTS.md is
   the carrier; the markers make that visible and the constraint below keeps it that way.
6. **Evidence leaves the token path.** Above `register.evidence.inline_max_lines` or
   `inline_max_tokens` (defaults equal the lockdown bound, and can only be tightened),
   evidence is written to a file and referenced with one pointer format,
   `evidence: <path> sha256:<12 hex> lines:<n>`, produced by `config.EvidencePointer`, which
   the lockdown distillation uses as well.
7. **Dispatch consumes the same row.** `models route --task X` reports the register;
   `dogfood repairs --task X` appends one register clause to each job's instructions and
   forwards a configured `max_tokens` as the provider's `max_output_tokens`; `repairrun`
   records the register beside provider `Usage`. Cheap-tier labels default to `internal`, so
   grunt work receives telegraphic prompts and only the frontier tier generates social or
   docs prose. Tier selection is untouched.
8. **At most one skill per register.** `social-text`, derived from `adhd-format` by
   reference, is the social form. `caveman` is the internal form, added on 2026-09-18 after
   the one-line "telegraphic" rule proved too soft for agents to follow; the configuration
   value stays `internal`. The docs rules live in the block and the guide; there is no
   `docs-text` or `internal-brief` skill, no `internal/register` package and no second
   configuration file.

```adr-constraint
id: text-register-has-one-loader-and-one-skill-per-register
kind: forbidden-path
forbids:
  - "internal/register/"
  - ".config/text-register.yaml"
  - ".agents/skills/docs-text/"
  - ".agents/skills/internal-brief/"
rationale: >-
  The register is one section of the existing manifest loader and at most one skill per
  register (social-text, caveman). A second package, a second configuration file or a second
  skill for one register would be the HISS-19 defect this record decides against: two
  implementations of one behaviour that drift apart.
```

The constraint kinds available are `universal-scope` and `forbidden-path`
(`internal/adr/constraint.go`). The runtime half of the checkable part is
`compile-context --verify` (block sync) and the task-label validation; both run inside
`make verify-all`.

### Amendment of 2026-09-18: the caveman module

The operator direction of 2026-09-18 (paraphrased): the internal register is applied by the
engine, not left to advice, and covers everything that does not face a human. Measurement
on this repository shaped the amendment. A hand-written caveman rewrite of AGENTS.md is 22.0%
smaller in bytes and 23.8% in estimated tokens and loses no rule, code span, command, id or
link. A deterministic prose compressor saves 3.2% on the same file, 1.2% across the private
planning notes, and turned "as strictly as" into "as as". Prose is therefore made terse by
hand at the source and kept terse by a lint, never rewritten at run time.

<!-- markdownlint-disable MD029 -- continued decision numbering is semantic -->

9. **One module measures agent-facing text.** `internal/caveman` is a standard-library-only
   leaf package, so every package that emits agent text can import it. It holds four
   functions and reads no configuration:
   - `Check` lints prose: more than 2.0 articles per 100 prose words once 40 words are
     present (AGENTS.md measures 8.8, its caveman rewrite 0.3), filler phrases, hedges,
     terminal noise (ANSI, box drawing, emoji), sentences over 30 words without a `;`, `->`
     or `:` break, and an unclosed `<!-- caveman:off -->` region. Code, inline code, link
     targets, URLs, headings, HTML comments, ledger field rows, hook protocol lines and
     evidence pointers are never read as prose. Table delimiters stay structured; cell text
     is prose.
   - `Floor` is the clarity floor of a rewrite: it fails when a code span, a fenced command,
     an id such as `HISS-17`, a link target or an HTML marker disappears, or when the count
     of `MUST`-type directives, prohibitions or numbered rules falls.
   - `Compress` applies only cleanups that cannot change meaning: ANSI removal, blank
     collapsing in prose lines, blank-line runs, identical consecutive prose lines folded to
     one with a count. It never drops or replaces a word.
   - `EstimateTokens` is the one token estimator (words × 1.3). `internal/lockdown` and
     `internal/docdistill` call it instead of their former copies.

   `praetorctl caveman check` and `praetorctl caveman estimate` expose the module. The
   module is not the `internal/register` package the constraint above forbids: it is not a
   loader and holds no register policy.
10. **Emission surfaces select where the lint applies.** `register.surfaces` gains
    `context`, `mcp`, `hooks`, `prompts` and `ledger` for text the engine itself writes for
    agents. Each resolves to its own key when written and to `internal` otherwise; neither a
    task label nor `surfaces.agent` changes it, so the lint is on by default for engine text
    (operator decision, 2026-09-18: caveman by default, overridable only by the surface's own
    key). The lint applies where a surface resolves to `internal`
    (`RegisterPolicy.LintEnforced`), so a repository opts a surface out by writing `docs` or
    `social`; `context` is the exception (decision 11). The keys go through the existing strict decoder; they are not rendered in the
    block, which is already at its 15-line budget.

11. **The context gate fails a prose AGENTS.md.** `compile-context --verify`, `audit` and
    their MCP mirrors run `Check` over the canonical AGENTS.md, and any finding fails them
    (`compiler.LintContext`). The gate has no opt-out, no warning mode and no grace period:
    it reads no manifest, and `surfaces.context` is the one emission surface fixed to
    `internal`; the manifest decoder rejects any other register for it. The whole file is
    linted, the adopter's own part below the harness included; the operator chose a red
    adopter gate over a warning for that part. Only the rendered register block is blanked
    first, because its renderer owns its wording and no repository edits it by hand;
    `praetorctl caveman check` applies the same mask, so the command the error names
    reproduces the verdict. AGENTS.md and the adopter harness are rewritten in caveman in
    the same change, and a floor test pins the rewrite against the frozen prose version
    (`internal/compiler/canonical_floor_test.go`). The HISS-17 turn start becomes
    `praetorctl state status` plus the open tasks, never the whole `STATE.md`.
12. **One checker reports its enforcement boundary.** `Check` accepts an explicit message
    kind: `message`, `brief`, `return` or `context`. The CLI defaults to `message`; the Go
    zero value remains `context` for source compatibility, and compiler gates select it
    explicitly. Runtime kinds add C9 lexical grammar drops, including modals and
    straight/curly contractions. Brief and return kinds add C10:
    goal/verdict first, one documented field per line, and every required field present.
    Unsafe Unicode control characters on a line, format controls, other default-ignorable
    code points and variation selectors fail C4. Tabs and normalized LF/CRLF line boundaries
    remain valid, as do ordinary visible combining marks.
    Every report classifies all eight numbered Caveman skill rules as mechanical or
    advisory, so `PASS` cannot imply semantic checks that never ran. Table cells feed the
    same prose checks while delimiters remain byte-stable; code, paths, URLs, quoted errors
    and explicit off regions remain protected. Non-Markdown static extraction remains #364.
13. **Runtime repair text is checked, not inferred.** `config.ValidateEmission` accepts the
    resolved register, emission surface, explicit runtime message kind and engine-owned
    text. Internal text delegates to the adversarial `caveman.CheckRuntime` profile;
    `social` and `docs` record
    `not_applicable`. Every validation record contains the register, token ceiling, surface,
    kind, typed source, manifest SHA-256, status, checker contract version and SHA-256 of the
    exact validated text. Source-only Markdown/HTML escapes, unsafe controls and Unicode
    default-ignorables are rejected; quoted and inline prose remains visible. Literal
    recognition accepts complete HTTP(S) URLs with ASCII case-insensitive schemes and
    preserves parentheses within those URL tokens; rooted or extension-bearing slash paths,
    forward- or backslash-drive paths, UNC paths, `:line[:column]` diagnostics, domain-qualified mail
    tokens and valid non-grammar flags. Every other Unicode punctuation or symbol separator
    is joined and split for grammar matching, so punctuation cannot suppress checks. A
    positive resolved ceiling is enforced by the same Caveman token estimator. Failure
    diagnostics expose at most three findings and 768 bytes.

    The repair planner preserves the exact task-row and prompt-surface resolutions and
    validates its generated job brief. The executor revalidates that brief, independently
    validates its static prompt scaffold and the Responses API instructions against
    `surfaces.prompts`, then appends untrusted source and test data. It validates
    `Proposal.Summary` against the task row as a return before `applyProposal`; invalid
    provider prose therefore cannot mutate the candidate tree. For an internal task row the
    prompt scaffold states the return fields from `caveman.SchemaFields`, so the provider
    is asked for the shape the check enforces. Reports retain all four
    validation records with the original `Resolution.Source` values.

    Runtime repair policies require complete task and prompt resolutions bound to one
    manifest digest. Planning resolves from the canonical manifest, scheduling overwrites
    caller tuples from its captured snapshot, and execution compares every field with the
    manifest blob at its immutable source commit. Both runtime boundaries validate register
    task rows against their captured routing vocabulary. Empty, partial or contradictory
    tuples cannot opt out.
    Terminal-state readback
    reconstructs the deterministic job, prompt and request text, rereads the proposal
    summary, reruns the current checker and requires the complete record to match.
    Verified and verification-failed outcomes require all four records; earlier outcomes
    require the contiguous prefix their stage reached. Missing or mismatched proof makes a
    terminal outcome invalid, as do changed bytes, a stale checker contract or a forged
    digest. A pre-enforcement result cannot silently satisfy a new run. A generated planner
    brief that fails validation remains in a blocked plan with its failure record instead
    of disappearing with the returned error.

    The validator lives in the existing `internal/config` package, preserving
    `internal/caveman` as a standard-library-only leaf and avoiding a second register
    loader. Dynamic MCP and hook text remains unverified; native client capture remains
    #415, and Paperclip synthesis remains #321.

<!-- markdownlint-enable MD029 -->

Rewriting the remaining MCP descriptions, hook messages, ledger templates and register
block wording are separate changes that build on this module. Notebook and Paperclip
prompts remain outside this amendment.

## Alternatives considered

- **A global register flag or environment variable.** Rejected: the direction forbids a
  global switch, and one voice cannot serve three audiences.
- **`overrides.register`.** Rejected: `Overrides` is the monotone strictness lattice;
  `CIPolicy` sits there only as a documented gap, not as a precedent, and a `Register` field
  in `ResolvedPolicy` would move the sealed digest of every repository.
- **A top-level `text:` section with a twelve-surface enum, float scale multipliers, three
  skills, a resolve command and proposal-summary rejection.** Rejected as machinery the
  direction did not ask for; rejecting a schema-valid repair over prose length would discard
  paid provider spend.
- **Embedded YAML defaults with an experiment basis, a `register` command group and replay
  fixtures.** Rejected: a second configuration format is the HISS-19 defect, and no
  candidate-to-register mapping exists to replay against. The measurable parts survive as
  `max_tokens` forwarding and the `Register` field of the repair report.
- **Shipping default budgets** (512 tokens for `commit_message_synthesis`, 1024 for
  `waiver_signoff`). Rejected by the operator: those numbers were opinion. The rows ship
  without a budget until repair reports supply data.
- **Emission inside `internal/agentcontext/render.go`.** Rejected to avoid colliding with
  the in-flight renderer change; the splice happens before compilation and the renderer
  stays untouched.
- **Nesting the block under rule 3 as an H3.** Rejected: an unindented heading inside a
  numbered list item ends the list in CommonMark, and an indented one renders as list
  continuation.
- **Lifting `dropMarkedBlock` out of `internal/milestone`.** Rejected for this change: that
  function only drops a span and reads to the end of the file on a missing end marker.
  Replace-or-append with an unterminated-marker error is new behaviour, not a lift, and
  routing BACKLOG.md through it would change `SyncToBacklog`. The new helper lives in
  `internal/util`; migrating milestone is a follow-up once tests prove equivalence.
- **Deterministic prose compression of agent text at compile or run time** (dropping
  articles, hedges and filler phrases). Rejected on measurement: 1-3% savings against 22%
  for a hand rewrite, and one meaning inversion in 12 KB. `Compress` keeps only the
  transforms that cannot change meaning.
- **A per-register lint package or a second estimator next to the existing ones.** Rejected
  as HISS-19: the two existing words × 1.3 estimators move into `internal/caveman` instead,
  and the lint thresholds live in that package, not in a configuration file.

## Consequences

Positive: one label set drives cost tier and text register; every agent context (six vendor
files, adopted harnesses, repair prompts, notebook and paperclip prompts) carries the same
rendered rule; drift is caught by the gates that already run; logs stop entering the token
path by rule and by bound.

Negative and trade-offs: AGENTS.md gains a tool-written region (precedents: the adopt
harness end marker, the milestone block in BACKLOG.md). Hand edits between the markers are
overwritten on the next `compile-context` and reported by `--verify`. A repository whose
AGENTS.md has no block yet fails `--verify` and `audit` until it runs `compile-context`
once. Register compliance on human-driven surfaces is advisory; only the evidence bound and
a configured provider budget are machine-enforced, and the guide says so. A renamed
`target_tasks` label orphans its manifest row until `compile-context` or `audit` runs.
`lockdown.MaxDistillLines` and `MaxDistillTokens` become aliases of the config defaults,
which ties the SARIF cap to the evidence bound; tightening the manifest values lowers the
numbers the block prints but does not yet re-parameterise the distillation cap.
`internal/config` now imports `internal/router` for the label shape and the row bound, and
`internal/lockdown` imports `internal/config`; both edges are acyclic.

Amendment (2026-09-18): every token figure now comes from one estimator, and the internal
register becomes measurable (`praetorctl caveman check`). The thresholds are heuristics fixed
from two measured inputs (8.8 and 0.3 articles per 100 prose words); fixtures under
`internal/caveman/testdata` replay them in both directions, and a change of threshold has to
keep both sides passing. The context gate (decision 11) breaks adopted repositories on
upgrade: the harness praetor wrote before it is prose. `praetorctl adopt --force` rewrites
the harness and keeps the repository's own part, which the repository then rewrites in
caveman; there is no setting that keeps it in prose.

Amendment (2026-09-21): the checker now distinguishes runtime messages, briefs, returns and
context policy text. Strict runtime text rejects the grammar classes named by the skill;
briefs and returns enforce their field contracts; table-cell prose is no longer hidden by
Markdown structure. Reports expose mechanical and advisory skill-rule sets. Repair planning
and execution now validate their owned briefs, provider instructions and summary returns
and retain the verdicts. Dynamic MCP and hook text, Paperclip synthesis and native-client
chat remain explicitly unverified rather than inheriting that claim.

Neutral: `models route` and `dogfood repairs` JSON gain additive fields (HISS-14,
append-only); plans written before this change decode with an empty register and keep their
instructions byte-identical.

## References

- Operator direction of 2026-09-17 (paraphrased above; verbatim in the private
  `.workingdir/` ledger).
- Operator direction and decisions of 2026-09-18 on the caveman module (paraphrased in the
  amendment; the design note and its measurements are in the private `.workingdir/` ledger).
- [ADR-0001: Universal context transpiler](0001-universal-context-transpiler.md)
- [ADR-0002: Strictness lattice](0002-highest-standard-wins-lattice.md)
- [ADR-0009: Structural unification](0009-structural-unification.md)
- `docs/standards/model-routing-and-fanout.md`, `docs/guides/text-register.md`
