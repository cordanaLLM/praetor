# Project Backlog & Longer-Horizon Workstreams

> Long-horizon architectural goals, deferred feature requests, and discharged milestones.

## Discharged Milestones
- [x] Initial Praetor Governance Adoption

## Future Workstreams
- [ ] Deep AST Deduplication Sweeps
- [ ] **Budget-aware agent dispatch (limit pre-checks as a harness primitive).** Per user direction
      (2026-09-12): every multi-agent fan-out must check remaining provider budget *before* dispatch and
      pace itself, because hitting a rolling-window limit mid-wave kills all in-flight agents and discards
      their warm context. Today `internal/router` models only per-model RPM/TPM headroom (arbiter,
      `LimitTracker`, 429 cooldown, `docs/standards/model-routing-and-fanout.md`) and no dispatcher calls
      it (`.config/agent/hooks/pre_agent_dispatch.py` is unreferenced). Scope:
      1. Model account-window budgets (rolling 5 h / weekly / monthly, per provider and per model tier)
         next to the RPM/TPM limits in `.config/models/routing.yaml`; track spend per window in
         `LimitTracker`; expose `RemainingWindowBudget(tier)`.
      2. A pre-dispatch gate: estimate a wave's spend from agent count x effort tier x observed
         tokens-per-agent, refuse or shrink the wave when it would breach 80 % of the remaining window,
         and prefer the cheaper tier for grunt work (the "route grunt off the scarce provider" rule).
      3. Checkpoint/resume semantics for agent work units: commit every N findings on a per-unit branch,
         resume from that branch on relaunch, and a circuit breaker that stops launching after K
         consecutive empty results so a limit hit costs a handful of agents, not hundreds.
      4. Wire `praetorctl agent` / the paperclip harness and the fleet workflow scripts through the gate,
         and surface the decision (estimated spend, remaining budget, chosen wave size) in the run log.
      Reference implementation of items 2-3 lives in the deep-audit workflow scripts
      (`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/wf2-fix.mjs`: `A()` breaker,
      `subset` halves, resume-from-branch prologue).

## Active Milestones

| # | Title | State | Due Date | Progress |
| :- | :--- | :--- | :--- | :--- |
| 1 | **v1.0.0-GA Fleet Adoption & Sentinel** | 🟢 Open | 2026-10-01 | 0% |
| 2 | **v1.1.0 Multi-Arch Container & Forge Scaling** | 🟢 Open | 2026-11-15 | 0% |


### Discharged Tasks [2026-09-11 20:05:39 UTC, commit `local`]
- [x] Task 1: Ongoing execution (completed: 2026-09-11)
- [x] Dogfooding battery execution against public open-source repositories (completed: 2026-09-11)
- [x] WorkingDir tracking system overhaul and task lifecycle commands (completed: 2026-09-11)
- [x] Milestone subsystem with local JSON cache and BACKLOG.md sync (completed: 2026-09-11)
- [x] GitHub Projects v2 board manager and tracking commands (completed: 2026-09-11)
- [x] Enforce Invariant HISS-17 (State Ledger Discipline) in AGENTS.md (completed: 2026-09-11)

### Discharged Tasks [2026-09-11 20:07:51 UTC, commit `local`]
- [x] Execute full verification suite (make verify-all) (completed: 2026-09-11)
- [x] Mint cryptographic Ed25519 Exit-0 receipt via standardsctl gate (completed: 2026-09-11)

### Discharged Tasks [2026-09-11 20:13:24 UTC, commit `local`]
- [x] Open Pull Request on cordanaLLM/praetor and mirror to lusoris/praetor (completed: 2026-09-11)

### Discharged Tasks [2026-09-11 20:18:30 UTC, commit `local`]
- [x] Define and register 4 new archetypes: rust-systems, typescript-node, jvm-service, mobile-flutter in internal/flavor (completed: 2026-09-11)
- [x] Update archetype autodetection in internal/adopt/adopt.go (completed: 2026-09-11)
- [x] Implement comprehensive tests for new flavors in internal/flavor/flavor_test.go (completed: 2026-09-11)
- [x] Expand remote dogfooding benchmarks in internal/dogfood/remote.go and run dogfooding simulation (completed: 2026-09-11)
- [x] Verify full test suite with race detector and standards audit (completed: 2026-09-11)

### Discharged Tasks [2026-09-11 20:35:29 UTC, commit `local`]
- [x] Implement internal/cifilter module for diff-aware change detection and CI job filtering (completed: 2026-09-11)
- [x] Add standardsctl ci filter CLI subcommand supporting JSON and GitHub Actions output modes (completed: 2026-09-11)
- [x] Update .github/workflows/ci.yml and flavor CI templates with diff-aware gating (completed: 2026-09-11)
- [x] Add batch/fleet pre-migration epic generation and rerun epics across all prepared repos (completed: 2026-09-11)
- [x] Verify full test suite with race detector and execute standards audit (completed: 2026-09-11)
