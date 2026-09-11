# Pre-Migration Epic: cordanaLLM/praetor

- **Target Framework**: `github.com/golusoris/golusoris v0.8.0`
- **Current Readiness Score**: `0.0%`
- **Third-Party Dependencies**: `1` total (0 covered, 1 gaps)

## Pre-Migration Tasks

- [ ] **Task 1**: [TASK 1/5] Invariant & Complexity Hygiene: cordanaLLM/praetor
- [ ] **Task 2**: [TASK 2/5] Decoupling & Config Externalization: cordanaLLM/praetor
  - *Prerequisites*: Depends-On: cordanaLLM/praetor#1
- [ ] **Task 3**: [TASK 3/5] Framework Dependency Substitution: cordanaLLM/praetor
  - *Prerequisites*: Depends-On: cordanaLLM/praetor#2
- [ ] **Task 4**: [TASK 4/5] Gated Verification & Ed25519 Receipt: cordanaLLM/praetor
  - *Prerequisites*: Depends-On: cordanaLLM/praetor#3
- [ ] **Task 5**: [TASK 5/5] Full Praetor Activation & Governance Lockdown: cordanaLLM/praetor
  - *Prerequisites*: Depends-On: cordanaLLM/praetor#4

## Execution Directives
1. All changes must pass `make verify-all` with zero warnings.
2. Direct commits to `main` are prohibited; changes must traverse `standardsctl gate run`.
3. Diff-aware CI efficiency: CI runs targeted gates on changed paths; docs-only and state-only changes skip heavy test/fuzz suites.
4. Ed25519 Exit-0 receipts mandatory on all pull requests.


---

## Decomposed Sub-Issue Definitions

### Issue 1: [TASK 1/5] Invariant & Complexity Hygiene: cordanaLLM/praetor

**Labels**: `task, hiss, hygiene`

## Scope
- Enforce NASA JPL Rule 4: refactor all functions to <= 60 LOC.
- Eliminate unhandled panics, unwrap(), and raw fatal exits.
- Add 3D unit tests (positive, negative, boundary) with race detector.

### Issue 2: [TASK 2/5] Decoupling & Config Externalization: cordanaLLM/praetor

**Labels**: `task, architecture, decoupling`
**Depends-On**: `cordanaLLM/praetor#1`

## Scope
- Eliminate in-cluster DNS and hardcoded localhost URLs.
- Externalize secrets and tokens behind environment variables / HashiCorp Vault.
- Decouple monorepo circular import dependencies.

### Issue 3: [TASK 3/5] Framework Dependency Substitution: cordanaLLM/praetor

**Labels**: `task, dependencies, migration`
**Depends-On**: `cordanaLLM/praetor#2`

## Scope
- Swap 0 external dependencies for github.com/golusoris/golusoris v0.8.0 builder kits.
- Apply verified import substitutions.
- Reconcile .needs.yaml capability declarations.

### Issue 4: [TASK 4/5] Gated Verification & Ed25519 Receipt: cordanaLLM/praetor

**Labels**: `task, verification, gating`
**Depends-On**: `cordanaLLM/praetor#3`

## Scope
- Run diff-aware CI verification via `standardsctl ci filter`.
- Run `standardsctl gate run --target=.` in isolated worktree.
- Verify all 5 gates (prefetch, SCA, HISS-16, tests, receipts).
- Sign Ed25519 Exit-0 receipt and submit fast-forward PR.

### Issue 5: [TASK 5/5] Full Praetor Activation & Governance Lockdown: cordanaLLM/praetor

**Labels**: `task, activation, governance`
**Depends-On**: `cordanaLLM/praetor#4`

## Scope
- Reconcile and lock branch protection rulesets via `standardsctl sync`.
- Transition .standards.yaml enforcement level to `strict-zero-debt`.
- Synthesize Paperclip agent harness (`standardsctl paperclip harness`).
- Configure ARC/fleet runner routing policy and enroll into bot gating webhook.

