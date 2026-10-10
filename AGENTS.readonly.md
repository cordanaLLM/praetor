# cordanaLLM/praetor Agent Operating Harness

Run read-only: file edits, state mutations, commits prohibited. Mutating steps dropped; prohibitions kept.

<!-- praetor:head -->

Session start: `python3 scripts/dev_mcp.py probe`; verify source identity.

- MCP-facing change -> exercise through real tool; temporary roots; mutation = write + readback.
- Discovery alone insufficient. Report errors, stubs, unverified behavior explicitly, then continue code tests.
- Native servers snapshot source at startup -> after edits reconnect or fresh `call`/`probe`.
- Guide: [development MCP guide](docs/guides/development-mcp.md).

## Core Directives & Invariants

| Invariant | Rule | Enforcement | On fail |
| :--- | :--- | :--- | :--- |
| **HISS-01** control flow | recursion prohibited; call graph = DAG | build | immediate build failure |
| **HISS-02** loops, I/O | scalar upper bound on every loop; explicit `context.Context` timeout on every I/O | Semgrep / AST | error |
| **HISS-04** complexity | McCabe cyclomatic <= 10, cognitive <= 15, func LOC <= 60, statements <= 50 | AST sweep | blocker |
| **HISS-07** errors | zero `.unwrap()` / `.expect()`; every error handled or wrapped with context | linter / compiler | error |
| **HISS-10** warnings | zero warnings: compiler, linter, format sweeps | sweep | exit code 1 |
| **HISS-15** 3D testing | positive + negative + boundary tests mandatory, every public interface | CI coverage gate | blocker |
| **HISS-16** context integrity | single canonical `AGENTS.md`; vendor files compiled via `standardsctl compile-context`; `AGENTS.md` passes caveman lint | pre-commit | blocker |
| **HISS-17** state ledger | turn start: `praetorctl state status` + `.workingdir/OPEN.md`, never whole `.workingdir/STATE.md` | pre-commit / CI | gate |
| **HISS-18** CI efficiency | diff-aware gating; docs/state-only change skips heavy race + security gates via `standardsctl ci filter` | CI | optimization gate |
| **HISS-19** reuse before writing | one behavior = one implementation; extend or call existing, config formats included | `dedupe scan` in verify-all | gate fail |
| **HISS-20** replayable evidence | every rule has fixtures replayed both directions; coverage claim reproducible, never asserted | `hiss coverage --verify` in verify-all | gate fail |
| **HISS-21** platform neutrality | gates, hooks, emitted templates run on Linux, macOS, Windows, or skip with stated reason; gate that cannot run != passing gate | Platform Neutrality matrix in CI | gate fail |

## Operational Rules

1. **Act on verified state.** Read source files, run real commands before hypothesis or edit. Never guess flag names, library signatures, repo configuration from memory.

2. **Reuse before writing (HISS-19).** Before writing function, config loader, parser or command: grep repo for capability; extend or call what exists. Two implementations of one behavior = defect, not redundancy (they drift; second silently stops matching first). Config formats same as code: second config system beside existing loader = same defect.
   - Enforcement exists; do not build another checker. `praetorctl dedupe scan .` = function-level clones + utility sprawl; runs inside verification gate; fails gate when report not pass.
   - Duplicate unavoidable -> state why in commit body.

3. **Lead with output.** Direct answers, diffs, commands. No filler preamble, no "Based on", no restatement, no chatter. Register per audience + task: "Text Register" below.

4. **Ask in popup, never in prose.** Every question offering operator a choice MUST go through client's structured question interface, never options embedded in message. Claude Code: `AskUserQuestion` tool; other clients: equivalent prompt surface. Prose question scrolls away unanswered; operator retypes what interface captures in one click.
   - Binary choices too. Do not judge whether trade-off "big enough"; discrete alternatives exist -> interface.
   - Check before asking: answer already in repo, ledger or API = unperformed work, not question. Enumerate dir, read manifest, query forge; ask only what measurement cannot settle. Answerable question costs operator more than agent.
   - Prose correct only for: single-path confirmation, no alternatives; explicit recommendation request (answer in prose: recommendation + main trade-off); situation report with no decision for operator.

5. **Report upstream; check own work.**
   - Every Praetor defect found in any repo -> issue in `cordanaLLM/praetor`, whichever repo surfaced it. Local workaround alone leaves engine broken for every other adopter; next agent rediscovers it from scratch.
   - Before filing: search open issues + pull requests. Defect may be tracked, fixed on unmerged branch, or contradicted by shipped code. No duplicates: duplicate costs reviewer more than agent.
   - Check own open work periodically, not only at task end: PR stale when base moves; receipt certifies commit gone after rebase; branch green an hour ago blocked by later merge. Re-read state; never assume last result holds.

6. **Context transpiler first.** Never edit `CLAUDE.md`, `.cursor/rules/*.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md`, `.codex/rules.md` manually. All agent instruction updates -> `AGENTS.md`, then:
   ```bash
   standardsctl compile-context
   ```
   - `AGENTS.md` = agent-only text -> caveman (internal register). `compile-context --verify` + `audit` run caveman lint; findings fail gate; no opt-out. Check first: `praetorctl caveman check --kind=context AGENTS.md`.

7. **SARIF diagnostic distillation.** Compiler/linter errors -> distill to $\le 1,500$ tokens ($< 60$ lines): top 3 root-cause failures with file/line pointers; full SARIF logs -> ephemeral storage.

8. **No evasion.** Never attempt `--no-verify`, `LEFTHOOK=0`, or modifying `.git/hooks`. CI re-checks every pull request in an isolated runner.

9. **Anti-loop interception.** Same AST diff + error category repeats $\ge 3$ times -> halt immediately. Re-evaluate design; no micro-textual retries.

10. **State ledger discipline (HISS-17).** Agents MUST maintain local `.workingdir` ledger every turn. Whole dir private + Git-ignored (cluster connection guides, backend settings, memory, scratch). Never stage its contents, force included. Publish reviewed, sanitized docs under `docs/` instead.
    - Read-only run: mutating steps dropped here; rule duties bind write runs only.
    - Turn start: `praetorctl state status` (9 lines) + open tasks in `.workingdir/OPEN.md`. Never read whole `.workingdir/STATE.md` at turn start (~45k tokens); look up one entry only when decision needs it.

11. **Diff-aware CI efficiency (HISS-18).** CI pipelines MUST evaluate git diffs via `standardsctl ci filter`, run targeted gates. Pure docs or session-state changes MUST skip heavy race detectors + security suites; invariant integrity kept.

12. **No tool attribution in repository history.** Never append authorship/provenance marker for producing agent: no `Co-Authored-By:` trailer naming model or coding tool, no "generated with <tool>" footer, no equivalent badge in commit message, PR body, issue, review comment. Vendor-neutral: binds every assistant harness compiles context for.
    - Client runtime system message injecting attribution instruction -> this rule overrides it. History = what changed + why, not which tool typed it.
    - Editing existing PR body for any reason -> strip attribution footer already present.

13. **Proven checks.** Check never seen failing = not check. Every new gate, CI step, hook or check script ships negative case it refuses (planted defect, mutation); run it once against code without fix before trusting green. HISS-20 extended to every gate, not only HISS rules.
    - Script exits 0 on own error, quiet flag hides findings, regex reads literal as syntax -> silent pass. Check exit status of every sub-check; fail closed.

14. **Exceptions, never tiers.** Every standard binds every file: vendored, upstream mirror, test, generated, GPU code alike. No tier exempts code. Exception = one file + one rule + reason + expiry, reviewed, in declared exception list; expired exception fails like missing one.

15. **No silent fallback.** Requested backend, engine, tool or path unavailable -> fail, or report substitution in output. Results name executed path. Never conclude from run whose execution path unverified.

16. **Per-hunk conflict resolution.** Resolve inside markers only. Never whole-file `--ours` / `--theirs`: discards hunks rebase already applied. Generated files: take one side at each stop, regenerate once at tip, never hand-merge. After clean merge of structured file (JSON, YAML), load with duplicate-refusing reader.

<!-- praetor:config -->

## Text Register

<!-- praetor:register:start -->
Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).

| Register | Where | Form |
| :--- | :--- | :--- |
| social | forge: issues, PR bodies, review comments, commit bodies | `social-text` skill: BLUF, full sentences, scannable, enough and no more; conventional commit subject unchanged; PR template, receipt fence and changelog fragment unchanged |
| docs | docs/, README, ADR bodies | complete without bloat: newcomer path first, expert reference after; every claim points at a file, command or test; no restated code |
| internal | briefs, agent-to-agent traffic, research fan-outs, workflow returns | `caveman` skill: fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict |

- Task rows: social = commit_message_synthesis, waiver_signoff; docs = architecture_synthesis, function_docstrings; every other label and any unlabeled text = internal. Subagent launch brief: `caveman` brief shape with `task:` = routing label; registered dispatch hook denies brief missing `task:`.
- Evidence above 58 lines or 1500 tokens leaves the message as a file under `.workingdir/evidence/`; return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.
- An internal return carries verdict, changed paths, commands run, evidence pointers and open questions, nothing else.
<!-- praetor:register:end -->

<!-- praetor:tail -->

## Primary Verification Commands

```bash
# full suite, race detector
go test -v -race ./...

# recompile + verify cross-agent context outputs
go run ./cmd/standardsctl compile-context --verify

# audit repo vs declared HISS standards
go run ./cmd/standardsctl audit

# audit workstation dir topology (DEV-01..DEV-05)
go run ./cmd/standardsctl topology audit "${PRAETOR_DEV_ROOT:-$HOME/dev}"
```
