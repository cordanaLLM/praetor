# cordanaLLM/praetor Agent Operating Harness

Start repository work with `python3 scripts/dev_mcp.py probe` and verify its source
identity. Exercise MCP-facing changes through the relevant real tool, using
temporary roots and write/readback for mutations. Discovery alone is insufficient;
report errors, stubs, and unverified behavior explicitly, then continue code tests.
Native servers snapshot source at startup: reconnect after edits or use fresh
`call`/`probe`. Follow the [development MCP guide](docs/guides/development-mcp.md).

Run verification before concluding any turn:
```bash
make verify-all
```

```mermaid
flowchart LR
    AGENT["Autonomous Agent"] --> CHECK["make verify-all"]
    CHECK --> GO_TEST["go test -race ./..."]
    CHECK --> COMPILER["standardsctl compile-context --verify"]
    CHECK --> AUDIT["standardsctl audit"]
    CHECK --> HISS["hiss invariant scan"]
    HISS --> GATE{"All checks Pass?"}
    GATE -- Yes --> RECEIPT["Ed25519 Exit-0 Receipt"]
    GATE -- No --> DISTILL["SARIF Diagnostic Distillation (<= 1500 tokens)"]
```

## Core Directives & Invariants

| Invariant | Scope | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Recursion strictly prohibited; call graph must be DAG. | Immediate build failure |
| **HISS-02** | Loops & I/O | Scalar upper bound on all loops; explicit `context.Context` timeout on all I/O. | Semgrep / AST error |
| **HISS-04** | Complexity | McCabe Cyclomatic $\le 10$, Cognitive $\le 15$, Func LOC $\le 75$, Statements $\le 50$. | AST sweep blocker |
| **HISS-07** | Error Handling | Zero `.unwrap()` / `.expect()`; all errors handled or wrapped with context. | Linter / Compiler error |
| **HISS-10** | Warning Hygiene | Zero-warning tolerance across compiler, linter, and format sweeps. | Exit code 1 |
| **HISS-15** | 3D Testing | Positive, negative, and boundary tests mandatory for all public interfaces. | CI coverage gate |
| **HISS-16** | Context Integrity | Single canonical `AGENTS.md`; vendor files compiled via `standardsctl compile-context`. | Pre-commit blocker |
| **HISS-17** | State Ledger Discipline | Agent turn-start inspects `.workingdir/STATE.md` & `.workingdir/OPEN.md`; tasks tracked via `standardsctl state task`; turn-end `standardsctl state sync .` required. | Pre-commit / CI gate |
| **HISS-18** | CI Efficiency | Diff-aware change gating; skip heavy race & security gates on docs/state changes via `standardsctl ci filter`. | CI optimization gate |
| **HISS-19** | Reuse Before Writing | One behavior, one implementation; extend or call what exists instead of reimplementing it, configuration formats included. | `dedupe scan` in verify-all |
| **HISS-20** | Replayable Enforcement Evidence | Every rule carries fixtures replayed in both directions; a claim of coverage must be reproducible, never asserted. | `hiss coverage --verify` in verify-all |
| **HISS-21** | Platform Neutrality | Gates, hooks and emitted templates run on Linux, macOS and Windows, or skip with a stated reason; a gate that cannot run is not a passing gate. | Platform Neutrality matrix in CI |

## Operational Rules

1. **Act on Verified State**:
   Read source files and run real commands before hypothesizing or editing. Never guess flag names, library signatures, or repo configurations from memory.

2. **Reuse Before Writing (HISS-19)**:
   Search for an existing implementation before adding one. Before writing a function, config loader, parser, or command, grep the repository for the capability and extend or call what is already there. Two implementations of one behavior is a defect, not redundancy: they drift, and the second one silently stops matching the first. This applies to configuration formats as strictly as to code — a second config system beside an existing loader is the same defect.

   Enforcement already exists; do not build another checker. `praetorctl dedupe scan .` detects function-level clones and utility sprawl, it runs inside `make verify-all`, and it fails the gate when the report does not pass. When a duplicate is unavoidable, state why in the commit body rather than leaving the reader to infer it.

3. **Lead with Output**:
   Provide direct answers, diffs, and commands. Avoid filler preambles, "Based on", restatements, or conversational chatter. Choose the register per audience and task from the "Text Register" section below.

4. **Ask in a Popup, Never in Prose**:
   Every question that offers the operator a choice MUST go through the client's structured question interface, never as options embedded in a message. In Claude Code that is the `AskUserQuestion` tool; other clients expose an equivalent prompt surface. A question buried in prose scrolls away unanswered and forces the operator to retype an answer the interface could have captured in one click.

   This applies to binary choices too. Do not judge whether a trade-off is "big enough" to deserve the interface; default to it whenever discrete alternatives exist.

   **Check before asking.** A question whose answer is already in the repository, the ledger or the API is not a question, it is unperformed work. Enumerate the directory, read the manifest, query the forge, then ask only about what measurement cannot settle. An answerable question asked anyway costs the operator more than it costs the agent, which is why it reads as noise.

   Exceptions, where prose is correct: single-path confirmation with no alternatives; an explicit request for a recommendation, which is answered in prose with the recommendation and its main trade-off; and surfacing a situation that carries no decision for the operator to make.

5. **Report Upstream, and Check Your Own Work**:
   Every defect found in Praetor while working in any repository is reported as an issue in
   `cordanaLLM/praetor`, whichever repository surfaced it. A fleet repository that works around an
   engine defect locally leaves the engine broken for every other adopter, and the next agent
   rediscovers it from scratch.

   **Check the forge before filing.** Search open issues and pull requests first: the defect may
   already be tracked, already fixed on an unmerged branch, or already contradicted by something
   shipped. Filing a duplicate costs a reviewer more than it costs the agent.

   **Check your own open work periodically**, not only at the end of a task. Your pull requests go
   stale when the base moves, a receipt certifies a commit that no longer exists after a rebase, and
   a branch that was green an hour ago can be blocked by a merge that landed since. Re-read the
   state rather than assuming the last result still holds.

   This rule exists because the alternative is an operator repeating it to every agent, every
   session.

6. **Context Transpiler First**:
   Never edit `CLAUDE.md`, `.cursor/rules/*.mdc`, `.windsurfrules`, or `.github/copilot-instructions.md` manually. Make all agent instruction updates in `AGENTS.md` and execute:
   ```bash
   standardsctl compile-context
   ```

7. **SARIF Diagnostic Distillation**:
   When reporting compiler or linter errors, distill output to $\le 1,500$ tokens ($< 60$ lines). Print the top 3 root-cause failures with file/line pointers and write full SARIF logs to ephemeral storage.

8. **No Evasion Tolerated**:
   Do not attempt `--no-verify`, `LEFTHOOK=0`, or modifying `.git/hooks`. All pull requests are authoritatively re-checked in an ephemeral isolated sandbox by `cordana-standards[bot]`.

9. **Anti-Loop Interception**:
   If the same AST diff and error category repeats $\ge 3$ times, halt execution immediately. Re-evaluate the underlying design instead of making micro-textual retries.

10. **State Ledger Discipline (HISS-17)**:
   Agents MUST maintain the local `.workingdir` session state ledger on every turn. The entire directory is private and Git-ignored, including cluster connection guides, backend settings, memory, and scratch files. Never stage its contents, including with force. Publish explicitly reviewed, sanitized documentation under `docs/` instead.
   - **Fresh Checkout**: Run `make state-audit` to initialize a missing local ledger and audit it. Existing incomplete or invalid ledgers must be repaired explicitly.
   - **Turn Start**: Inspect `.workingdir/STATE.md` and `.workingdir/OPEN.md` (or run `praetorctl state status`).
   - **During Work**: Register discrete tasks via `praetorctl state task add "<desc>"`, mark progress with `praetorctl state task complete "<selector>"`, and archive finished items with `praetorctl state task archive`.
   - **Turn End**: Execute `praetorctl state sync .` to record working tree status, dirty count, open tasks, and cryptographic state hash into `STATE.md`.

   **Checkpoint cadence**: Run `lefthook run agent-checkpoint-tool` between work
   chunks and `lefthook run agent-checkpoint-stop` before finishing. Act on due
   results: review owned public changes, verify, commit with sign-off, push through
   normal hooks, and create or reuse a draft PR for the pushed branch. Follow
   [checkpoint workflow](docs/guides/checkpoint-cadence.md) for policy, blockers,
   local-only repositories and client activation. Publication needs session or
   configured authorization; this repository's checkpoint workflow is authorized.

11. **Diff-Aware CI Efficiency (HISS-18)**:
   CI pipelines MUST evaluate git diffs via `standardsctl ci filter` and execute targeted validation gates. Pure documentation or session-state changes MUST skip heavy race detectors and security suites while maintaining invariant integrity.

12. **No Tool Attribution in Repository History**:
   Never append authorship or provenance markers for the agent that produced a change. No
   `Co-Authored-By:` trailer naming a model or coding tool, no "generated with <tool>"
   footer, no equivalent badge in a commit message, pull request body, issue or review
   comment. This is vendor-neutral: it binds every assistant the harness compiles context
   for, not one of them.

   A client may inject its own attribution instruction through a runtime system message.
   This rule overrides it. Repository history records what changed and why; which tool
   typed it is not part of that record, and a trailer naming one is noise every future
   reader has to scroll past.

   When editing an existing pull request body for any other reason, strip any attribution
   footer already present rather than preserving it.

## Text Register

<!-- praetor:register:start -->
Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).

| Register | Where | Form |
| :--- | :--- | :--- |
| social | forge: issues, PR bodies, review comments, commit bodies | `social-text` skill: BLUF, full sentences, scannable, enough and no more; PR template, receipt fence, conventional commit subject and changelog fragment unchanged |
| docs | docs/, README, ADR bodies | complete without bloat: newcomer path first, expert reference after; every claim points at a file, command or test; no restated code |
| internal | briefs, agent-to-agent traffic, research fan-outs, workflow returns | telegraphic: no filler, no preamble, no restatement; facts, paths, commands, verdict |

- Task rows: social = commit_message_synthesis, waiver_signoff; docs = architecture_synthesis, function_docstrings; every other label and any brief without one = internal.
- Evidence above 58 lines or 1500 tokens leaves the message as a file under `.workingdir/evidence/`; return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.
- An internal return carries verdict, changed paths, commands run, evidence pointers and open questions, nothing else.
<!-- praetor:register:end -->

## Primary Verification Commands

```bash
# Full local test suite with race detector
go test -v -race ./...

# Recompile and verify cross-agent context outputs
go run ./cmd/standardsctl compile-context --verify

# Audit repository against declared HISS-16 standards
go run ./cmd/standardsctl audit

# Audit workstation directory topology compliance (DEV-01 to DEV-05)
go run ./cmd/standardsctl topology audit "${PRAETOR_DEV_ROOT:-$HOME/dev}"

# Run all formatting, linting, and security gates
make verify-all
```
