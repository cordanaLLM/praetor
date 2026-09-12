# Contributing to Praetor

Thank you for contributing to Praetor! As the core governance and standardization platform for the CordanaLLM ecosystem, all contributions must adhere to High-Integrity Systems Standards (HISS).

---

## Code of Conduct & Standards

All contributors are expected to uphold deterministic, high-integrity engineering practices:
- Zero-warning tolerance across compiler, linter, and format checks.
- 100% test coverage across public interface dimensions (Positive, Negative, Boundary).
- All commits must include Developer Certificate of Origin (`Signed-off-by: Your Name <email>`).

---

## Local Development & Verification

Run `make hooks` and `make hooks-check` after cloning. The [Git hooks guide](docs/guides/git-hooks.md) describes staged checks, push scope, required tools and the explicit sandbox gate. Commit with a conventional subject and `git commit -s` for DCO. Hooks preserve unstaged work; run `praetorctl state sync .` explicitly at turn end.

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
   - AST complexity limits (McCabe $\le 10$, cognitive $\le 15$, function LOC $\le 75$).
3. **Receipt Generation**:
   - Run `standardsctl gate run --path=.` to verify the ephemeral worktree and generate an Ed25519 Exit-0 receipt.
4. **Pull Request Submission**:
   - Submit PR via GitHub. Direct pushes to `main` are declined by repository rules.
   - Required status checks (`Standards & Invariant Verification Gate`, `DCO 1.1 & REUSE Compliance Gate`, `Go Vulnerability & AST Security Scan`) must pass before merge.
