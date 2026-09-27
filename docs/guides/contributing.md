# Contributing to Praetor

Thank you for contributing to Praetor, an open governance engine for any repository fleet. Every contribution must adhere to the [High-Integrity Systems Standard (HISS)](../standards/hiss-spec.md).

---

## Code of Conduct & Standards

All contributors are expected to uphold deterministic, high-integrity engineering practices:

- Zero-warning tolerance across compiler, linter, and format checks.
- HISS-15 requires a positive, a negative, and a boundary test for every public interface;
  CI additionally enforces a 65% total-statement-coverage floor
  (`COVERAGE_FLOOR` in `.github/workflows/ci.yml`), not 100% coverage.
- All commits must include Developer Certificate of Origin (`Signed-off-by: Your Name <email>`).

---

## Local Development & Verification

Before submitting any Pull Request, ensure local verification passes completely:

```bash
# 1. Run race-detected unit tests
go test -v -race ./...

# 2. Verify agent context synchronization
go run ./cmd/standardsctl compile-context --verify

# 3. Audit repository against declared HISS standards
go run ./cmd/standardsctl audit

# 4. Run all verification gates
make verify-all
```

---

## Pull Request Lifecycle

1. **Feature Branch**: Create a branch off `main` (e.g. `feat/feature-name` or `fix/bug-name`).
2. **Deterministic Checks**:
   - `gofmt` code formatting.
   - `REUSE 3.3` licensing compliance.
   - AST complexity limits (McCabe $\le 10$, cognitive $\le 15$, function LOC $\le 60$
     -- the audit-compatibility ceiling `config.AuditMaxFuncLOC` always tightens a looser
     repository override, per `internal/hiss/hiss.go` and `internal/config/effective.go`).
3. **Receipt Generation**:
   - Commit first, then run `standardsctl gate run --path=.` to verify the ephemeral worktree and generate an Ed25519 Exit-0 receipt. The gate refuses a tree with uncommitted or untracked changes ([details](adoption-verification.md#a-receipt-certifies-only-a-working-tree-that-matches-head)).
4. **Pull Request Submission**:
   - Submit PR via GitHub. Direct pushes to `main` are declined by repository rules.
   - All 8 required status checks in `.github/rulesets/main.json` must pass before merge:
     `Release & Bot Configuration Validation`, `Standards & Invariant Verification Gate`,
     `DCO 1.1 & REUSE Compliance Gate`, `Platform Neutrality (Linux)`,
     `Platform Neutrality (macOS)`, `Platform Neutrality (Windows)`,
     `Documentation Governance`, and `Go Vulnerability & AST Security Scan`.
