# HISS refinement audit

Status: audit and scoped repair, 2026-09-13; baseline `eb3f0d8`.
Keep HISS identities and strictness. The supplied “Universal Modernised NASA
Power of Ten (2026 Edition)” is an input proposal, not verified NASA authority.
This audit does not adopt its requirements as a replacement standard.

## Three root causes

1. **Rules lack executable coverage matching their promises.** `internal/hiss/go_ast.go:67`
   checks syntax shapes, not a complete call graph or termination proof. Temporary
   fixtures returned zero violations for direct/mutual recursion, `for true`,
   aliased `unsafe`, and malformed Go; canonical unsafe and conditionless-loop
   controls did trigger. TypeScript was explicitly reported unscanned. These are
   standalone scanner results; compiler and separate Semgrep checks have different
   coverage. `.config/semgrep/hiss-invariants.yml` also combined HTTP Get/Post with
   AND, matching neither. Repairs cover OR, Python eval and Rust test boundaries;
   broader scanner completeness remains open. [Semgrep operator semantics](https://docs.semgrep.dev/writing-rules/rule-syntax).
2. **Declared policy and effective enforcement differ.** HISS-04 documents 75 LOC;
   `internal/config/effective_load.go:207` tightens the audit to 60 through a named
   compatibility layer. CLI/MCP already report 60 and its provenance; documentation
   must agree. Only complexity currently uses the shared layered resolver. CI applies
   a 65% aggregate statement floor, which does not prove positive/negative/boundary
   coverage for every interface. HISS-18 docs-only savings also remain incomplete:
   push conditions force heavy checks. Reuse the existing CI consolidation task.
3. **Universal wording mixes safety goals with particular implementations.** The
   original Power of Ten describes bounded loops, with a scheduler exception, and
   initialization-aware allocation constraints in its safety-critical C context.
   V8's 60% statistic concerns observed Chrome exploits in 2021–2023; it is not a
   universal 2026 statistic. Chromium explicitly recommends copying untrusted
   shared BigBuffer data before parsing, contradicting mandatory zero-copy.
   [Original paper](https://spinroot.com/gerard/pdf/P10.pdf), [V8](https://v8.dev/blog/sandbox), [Mojo guidance](https://chromium.googlesource.com/chromium/src/+/main/docs/security/mojo.md#copy-data-out-of-bigbuffer-before-parsing).

## Refinement disposition

| Proposal area | Targeted disposition |
| --- | --- |
| Subsetting, strict types, schemas | Keep typed trust boundaries; schemas validate structure, not correctness of arbitrary generated code. Select actual language/toolchain profiles. |
| Loops, async, circuit breakers | Retain bounds and deadlines; specify cancellation, retry/output budgets and process isolation for non-cancellable I/O. Circuit breakers depend on operation semantics. |
| Allocation, GC, pointer/JIT tables | Measure allocations in declared hotpaths; scope engine internals and hardware controls to applicable runtimes. Do not mandate V8 mechanisms for Go services. |
| State, messages, contracts | Add explicit lifecycle transitions, ownership, bounded decoding and role checks; distinguish invariant assertions from rejection of untrusted input. Copies versus handles require lifetime analysis. |
| Static checks and exceptions | Keep zero-warning gates; attach tool/version, scope, fixtures and review evidence. Existing scoped justifications need traceability, not blanket suppressions. |
| AI repair/review | Keep bounded repair and independent review evidence; reject forced multi-file edits and bans on early human review. Provider choice stays configurable. Unsourced productivity percentages cannot justify gates. |

## Delivery order and acceptance

First repair existing rule defects with real positive, negative and boundary
fixtures. `make semgrep-test` is part of `make verify-all`; its engine is pinned in
`.config/semgrep/requirements.txt`. It tests matcher behavior, not whole-repository
compliance. Existing Lefthook source scans and all prior gates remain required.
Next add additive per-rule evidence states: enforced, partial, manual, unsupported,
and not-applicable. Preserve legacy report fields; “files read” must never imply
semantic proof. Then extend the existing effective-policy/catalog with versioned
rule scope, provenance, enforcement adapter, fixtures and exception lifecycle;
generate documentation/MCP projections from that record. No second policy engine.
Acceptance requires CLI/MCP parity, unknown-scope refusal, selected-profile tests,
unchanged stricter limits, and replayable evidence. Registry, scanner completeness,
hotpath measurement and semantic test coverage remain planned work.
