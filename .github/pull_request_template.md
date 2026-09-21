<!-- cordanaLLM/praetor Pull Request Verification Gate -->

## Summary of Changes

<!-- Provide a concise description of what this PR introduces, refactors, or fixes. -->

---

## 1. High-Integrity Systems Standard (HISS) Checklist

All PRs must strictly adhere to the declared HISS invariants before merging:

### Static & Runtime Determinism
- [ ] **HISS-01 (Control Flow)**: Call graph is strictly a Directed Acyclic Graph (DAG); zero recursion.
- [ ] **HISS-02 (Bounded Loops & I/O)**: All loops declare a static compile-time scalar upper bound; all network/disk I/O operations enforce explicit `context.Context` timeouts.
- [ ] **HISS-03 (Zero Frame Malloc)**: Hot-path loops maintain zero dynamic heap allocations ($\Delta \text{HeapAlloc}_{\text{tick}} = 0$).
- [ ] **HISS-04 (Complexity Bounds)**: McCabe Cyclomatic $\le 10$, Cognitive $\le 15$, Func LOC $\le 60$, Statements $\le 50$.

### Memory & Error Integrity
- [ ] **HISS-07 (Checked Errors)**: Zero `.unwrap()` / `.expect()`; zero unchecked error returns (`_ = err`); all errors handled or wrapped with domain context.
- [ ] **HISS-08 (Static Determinism)**: Zero `eval()`, zero dynamic evaluation, zero banned unsafe C functions (`gets`, `strcpy`, `sprintf`).
- [ ] **HISS-09 (Safety Proofs)**: Any `unsafe` block or operation is preceded by an explanatory `// SAFETY:` rationale proving invariant maintenance.
- [ ] **HISS-10 (Zero-Warning Cascade)**: Zero warnings across compiler, `go vet`, and all configured linters.

### Supply Chain & API Governance
- [ ] **HISS-11 (Hermetic Supply Chain)**: All dependencies and lockfiles (`go.sum`) pinned; zero unpinned floating container tags.
- [ ] **HISS-14 (Append-Only ABI)**: Public APIs are append-only. Any breaking change includes `!` in the conventional commit title and a mandatory `Migration:` footer.

---

## 2. Three-Dimensional (3D) Testing Verification (HISS-15)

Every public method and modified component must include three dimensions of automated testing:
- [ ] **HISS-15 Dimension 1: Positive Tests**: Validated expected outputs against normal and golden operational inputs.
- [ ] **HISS-15 Dimension 2: Negative Tests**: Validated deterministic error propagation on corrupted, unauthorized, or invalid inputs.
- [ ] **HISS-15 Dimension 3: Boundary Tests**: Validated limits ($0$, $1$, $N_{\max}$, empty buffers, max field limits).
- [ ] **HISS-13 Whole-Tree Debt Ratchet**: The full supported source tree was scanned; no finding is exempt because of file origin, language, or ownership, and debt may only decrease.

---

## 3. Context Integrity & Agent Governance (HISS-16)

- [ ] **Canonical Single Source of Truth**: All agent rule updates were made directly in `AGENTS.md`.
- [ ] **Context Transpilation Verification**: Ran `standardsctl compile-context --verify` with 0 diffs.
- [ ] **Zero Manual Evasion**: No manual edits to `CLAUDE.md`, `.cursor/rules/*.mdc`, `.windsurfrules`, or `.github/copilot-instructions.md`.
- [ ] **No Bypass Flags**: Did NOT use `--no-verify`, `LEFTHOOK=0`, or mutate `.git/hooks`.

---

## 4. Ed25519 Exit-0 Verification Receipt

Paste the **signed receipt JSON** below, inside the fence labelled `receipt`. The label is what
the validator looks for: an unlabelled or `text` fence is treated as a code snippet in a
description and never read.

`praetorctl gate run` writes it to `.standards-receipt.json`, and the pre-push hook keeps a copy
at `.git/praetor-receipts/<sha>.json`. It must certify the pull request's head commit; a rebase
invalidates it, so re-mint after one.

```receipt
<!-- Paste the contents of .standards-receipt.json here, replacing this line. -->
```

- **Local Race Detector Status**: `go test -v -race ./...` (PASS)
