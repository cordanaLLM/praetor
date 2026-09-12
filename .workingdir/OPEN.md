# Open Items & In-Flight Blockers

> Track active, immediate operational blockers and current research spikes.
> Current evidence and repair order: [GAPS_AUDIT.md](GAPS_AUDIT.md). Audit first; ledger integrity before bulk reconciliation, then ADR-0009 unification before adapter expansion.
> Package decisions and migration acceptance: [Go reuse research](../docs/research/go-package-reuse.md). Research complete; prototypes and adoption remain implementation work.

## In-Flight Tasks
- [x] Deep audit WF-1: read-only multi-agent audit of full codebase (Go, CI, hooks, packaging, deploy, templates, config, agent surfaces, editors, docs) (completed: 2026-09-12)
- [x] Deep audit: retain original 716 findings and full evidence report; ledger loss and reconciliation tracked separately (completed: 2026-09-12)
- [ ] Deep audit WF-2: reconcile and fix verified findings on checkpoint/deep-audit-2026-09-12 after ledger integrity and ADR-0009 unification (EUPL-1.2, praetorctl-only, pinned-key receipt, zero gosec exclusions)
- [ ] Deep audit: verification gate, PR to cordanaLLM/praetor
- [ ] Harvest and replay per-workstation coding CLI and agent global rules, skills, state, logs, brains, and memories with source provenance
- [ ] Configure lusoris/praetor as synchronized operational fork with explicit repository and workstation rollout stages
- [ ] Implement capability and evidence based per-task agent routing with cost, latency, and escalation controls
- [ ] Optimize workstation agent and CLI context files with canonical ownership and replay verification
- [ ] Promote scoped dogfood repair candidates through fresh suite replay and explicit repository stages
- [x] Wire Codex PreToolUse through Lefthook and verify activation boundary (completed: 2026-09-12)
- [ ] Trust and verify the repository Codex PreToolUse hook in a native session
- [ ] Enforce verification evidence at agent Stop with bounded failure handling
- [ ] Build NotebookLM source preparation and project plan, specification, and task templates
- [ ] Replace model catalog name heuristics and silent discovery failures with measured capability and provenance records
- [ ] Repair and exercise IDE adapters through shared CLI/MCP services; then integrate notebook preparation and prompt evaluation
- [ ] Run provider prompt candidates on held-out dogfood cases and promote only measured improvements
- [x] Audit current gaps, stubs, disconnected adapters and inaccurate state before further feature implementation (completed: 2026-09-12)
- [ ] Repair bug ledger parsing, persistence and read-error handling; recover four missing findings before reconciliation
- [ ] Implement ADR-0009 shared policy and analysis boundaries before adapter and provider expansion
- [ ] Implement staged forge bot runtime and harden adoption workflows; retain explicit unsupported provider errors
- [x] Research maintained Go packages versus custom implementations and publish a cited reuse and migration decision matrix (completed: 2026-09-12)
