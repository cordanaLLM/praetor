---
name: hiss-audit
description: Audit repository source code, AST, and configurations against the High-Integrity Systems Standard (HISS) to ensure deterministic execution, bounded complexity, and error integrity.
---

# HISS Invariant Audit (`hiss-audit`)

Audit any repository or pull request against the High-Integrity Systems Standard (HISS) formal specification.

## Core Directives & Verification Ladder

When invoked, execute the audit in strict order of operations:

1. **Verify State**: Read declared configurations (`.standards.yaml`, `.standards-baseline.json`).
2. **Execute Static AST Scanners**:
   - `HISS-01 (Control Flow)`: Detect recursion or cyclic call graphs. Recursion is strictly prohibited; call graph must be a DAG.
   - `HISS-02 (Bounded Loops)`: Ensure all `for`/`while` loops contain static scalar upper bounds (`MaxIterations`, `MaxURLLimit`, etc.). Verify every I/O operation consumes a `context.Context` with deadline/timeout.
   - `HISS-03 (Zero Frame Malloc)`: Inspect hot-path simulation/render loops to assert zero dynamic allocations.
   - `HISS-04 (Complexity Bounds)`: Assert McCabe Cyclomatic $\le 10$, Cognitive $\le 15$, Func LOC $\le 75$, Statements $\le 50$.
   - `HISS-07 (Checked Errors)`: Verify zero unchecked error returns (`_ =`) and zero `.unwrap()` / `.expect()`.
   - `HISS-08 (Static Determinism)`: Ban `eval()`, dynamic code loading, and vulnerable libc calls (`gets`, `strcpy`, `sprintf`).
   - `HISS-09 (Safety Proofs)`: Confirm any `unsafe` block is preceded by a `// SAFETY:` explanatory justification.
   - `HISS-10 (Zero-Warning Cascade)`: Run `go vet ./...` and linters; ensure zero warnings.
3. **Audit 3D Test Coverage (HISS-15)**:
   - Ensure every public function has Positive, Negative, and Boundary unit tests.
   - Verify the Touched-File Clean Rule: no pre-existing debt permitted in newly modified files.
4. **Context Integrity Check (HISS-16)**:
   - Run `standardsctl compile-context --verify` to verify that `AGENTS.md` matches vendor agent files (`CLAUDE.md`, Cursor rules, Copilot).

## Audit Execution Commands

```bash
# Run standardsctl audit sweep
go run ./cmd/standardsctl audit

# Verify context transpilation
go run ./cmd/standardsctl compile-context --verify

# Run test suite with race detector
go test -v -race ./...

# Run static analysis
go vet ./...
```

## Reporting Guidelines

- When reporting findings, emit a **SARIF Diagnostic Distillation** ($\le 1,500$ tokens, $< 60$ lines).
- Highlight the top 3 root-cause infractions with `file:line` pointers.
- Provide actionable remediation diffs rather than generic conversational summaries.
