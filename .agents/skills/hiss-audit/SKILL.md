---
name: hiss-audit
description: Audit repository source code, AST, and configurations against High-Integrity Systems Standard (HISS): deterministic execution, bounded complexity, error integrity.
---

# HISS Invariant Audit (`hiss-audit`)

Audit any repository or pull request against High-Integrity Systems Standard (HISS) formal specification.

## Core Directives & Verification Ladder

Invariant set = `AGENTS.md` "Core Directives & Invariants" table; no hardcoded range. Per-language enforcement state (`enforced` | `partial` | `unsupported`) = `.config/hiss/coverage.yaml`; print with `praetorctl hiss coverage`. Invariant without scanner or gate below -> review by hand, report as reviewed, never as scanned.

When invoked, execute audit in strict order of operations:

1. **Verify State**: Read declared configurations (`.standards.yaml`, `.standards-baseline.json`, `.config/hiss/coverage.yaml`).
2. **HISS AST Scan** (`internal/hiss` `Scan()`, run by `standardsctl audit`; these six rules, no others):
   - `HISS-01 (Control Flow)`: Go self-recursion and call cycles, `goto` in Go and C. Recursion prohibited; call graph must be DAG.
   - `HISS-02 (Bounded Loops)`: unbounded `for {}` (Go), native loops (C), `loop {}` (Rust), `while True` (Python). Go HTTP call without `context.Context` deadline = Semgrep rule `hiss-02-go-http-without-context` (`.config/semgrep/hiss-invariants.yml`).
   - `HISS-04 (Complexity Bounds)`: Func LOC $\le 75$. Cyclomatic $\le 10$, cognitive $\le 15$ = `gocyclo`, `gocognit`, `funlen` in golangci-lint (`make lint`), not `Scan()`.
   - `HISS-07 (Checked Errors)`: Go unchecked error assignment (`_ =`) and `panic`, Rust `.unwrap()` / `.expect()`, Python bare `except`.
   - `HISS-08 (Static Determinism)`: `gets`, `strcpy`, `sprintf` (C); `eval` / `exec` (Python). Go = `forbidigo` in golangci-lint.
   - `HISS-09 (Safety Proofs)`: `unsafe` block without preceding `// SAFETY:` justification (Go, Rust).
3. **Gates Outside `Scan()`**:
   - `HISS-10 (Zero-Warning Cascade)`: `make lint` (`go vet ./...` + golangci-lint); zero warnings.
   - `HISS-15 (3D Testing)`: every public function has Positive, Negative, Boundary tests. Touched-file clean rule: `standardsctl audit --base=origin/main`.
   - `HISS-16 (Context Integrity)`: `standardsctl compile-context --verify`; `AGENTS.md` matches vendor agent files (`CLAUDE.md`, Cursor rules, Copilot) and passes caveman lint.
   - `HISS-17 (State Ledger)`: `praetorctl state status`; turn end `praetorctl state sync .`.
   - `HISS-18 (CI Efficiency)`: `standardsctl ci filter --base=origin/main` selects gates from diff.
   - `HISS-19 (Reuse Before Writing)`: `praetorctl dedupe scan .`; report not pass -> fail.
   - `HISS-20 (Replayable Evidence)`: `praetorctl hiss coverage --verify` replays fixtures both directions.
   - `HISS-21 (Platform Neutrality)`: `.github/workflows/portability.yml` matrix (Linux, macOS, Windows); local self-test `make portability-test`.

## Audit Execution Commands

```bash
# Run standardsctl audit sweep
go run ./cmd/standardsctl audit

# Verify context transpilation
go run ./cmd/standardsctl compile-context --verify

# Run test suite with race detector
go test -v -race ./...

# Run static analysis (go vet + golangci-lint)
make lint

# Replay HISS coverage fixtures; scan for clones
go run ./cmd/standardsctl hiss coverage --verify
go run ./cmd/standardsctl dedupe scan .

# Every local gate above in one run
make verify-all
```

## Reporting Guidelines

- When reporting findings, emit **SARIF Diagnostic Distillation** ($\le 1,500$ tokens, $< 60$ lines).
- Highlight top 3 root-cause infractions with `file:line` pointers.
- Provide actionable remediation diffs rather than generic conversational summaries.
