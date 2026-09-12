# Project Backlog & Longer-Horizon Workstreams

> Long-horizon architectural goals, deferred feature requests, and discharged milestones.

## Discharged Milestones
- [x] Initial Praetor Governance Adoption

## Future Workstreams

> Ordering per user direction (2026-09-12): the layered config system comes first, because it lets praetor
> tune its own local profile for faster dogfooding and is the enabler for the completeness gate, the sandbox
> rule and the budget gate below.

- [ ] **Change completeness gate (HISS-19) and engineering principles (HISS-20) in the ruleset.** Per user
      direction (2026-09-12): a pull request must carry every part of the system it touches (tests, docs and
      CLI help, config keys with schema/defaults/docs, templates and scaffolds, devcontainer/toolchain entries
      plus `.needs.yaml` and distilled docs for new dependencies, changelog fragment, ADR for architectural
      decisions), derived from the change set and a `completeness:` config section with `dev`/`pr` profiles,
      enforced fail-closed by `praetorctl gate` and CI so nothing incomplete is merged again; and the basic
      engineering principles (single source of truth, do not reinvent, deduplicate and slim, explicit over
      implicit, fail closed, measure before claiming, small reversible steps with round-based checkpoint commits so
      that a crash, reboot or provider limit never loses work) become HISS-20 in `AGENTS.md`.
      Draft text: `~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/harness-principles-draft.md`.
      The AGENTS.md rows land with the wave B agent-surface fix group; the gate needs the config workstream.
- [ ] **Structural optimisation and deduplication first; fleet module library (ADR-0009, design in progress).** Per user
      direction (2026-09-12): before new modules are added, correct the application's structure and remove the
      duplication the audit found (four token resolvers, two GitHub clients, two HISS engines, templates duplicated as
      string literals, 42 persona copies, four config readers, orphan packages), and extend the golusoris demand
      mechanism (ADR-0007, `internal/needs`, `.needs.yaml`) into a module library so that shared capabilities are
      deduplicated across all organisations and repositories by configuration, with backlogs, new features and fixes
      flowing through the same mechanism. Absorbs "Deep AST Deduplication Sweeps". Design fan-out running; the
      refactor itself is sequenced after the in-flight fix waves merge.
- [ ] Deep AST Deduplication Sweeps (absorbed by the item above)
- [ ] **Spec-driven provider integration (ADR-0008, Proposed).** Replace the hand-written forge drivers with a
      declarative provider layer: each provider is a `.config/providers/<name>.yaml` descriptor plus a compiled,
      digest-pinned operation table produced offline by `tools/specc` (a separate Go module holding the only
      OpenAPI dependency; the root `go.mod` stays at `gopkg.in/yaml.v3`). The runtime executor in
      `internal/provider` interprets the table with no generated Go and no new dependency. Capability bindings
      replace `internal/forge/{github,gitlab,gitea}.go`, the remote paths of `project.go` and
      `internal/milestone/forge.go`; a `govern` funnel adds mutation allow-lists, Ed25519 receipts, budget gating
      and injection sanitization to every call; MCP exposure is bound tools plus three meta-tools per provider,
      native MCP consumption deferred to an optional last phase. Pins land in `.standards.lock` under
      `providers:`; docs are distilled per pinned API version. Milestone 1 (about day 15 of ~43 engineer-days):
      GitHub fully supported from its own description with fewer than 150 lines of provider-specific Go, gated by
      a differential test against the legacy driver; v1 covers all three providers (decided Q-003).
      Plan: `~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/provider-integration-plan.md`.
- [ ] **Artifact management and dynamic session state.** Per user direction (2026-09-12): every run
      (audit, verification, fix wave, sandbox execution, gate, receipt, report) should produce first-class
      artifacts with provenance instead of loose files, so that state and session management become fully
      dynamic - the `.workingdir` ledgers (`STATE.md`, `OPEN.md`, `BUGS.md`, `BACKLOG.md`, `AUDIT_RESUME.md`)
      are *rendered from* artifacts rather than hand-maintained, and any session, on any machine, resumes
      from them. Evidence: during the deep audit the workstation rebooted and the entire scratchpad was lost;
      the audit was rebuilt by hand from the workflow journal and agent transcripts (see the recovery script
      in the audit directory), which is exactly the job an artifact store should do automatically. Scope:
      1. A content-addressed artifact store per repository under a persistent location (not `/tmp`), with
         a manifest per artifact: kind, producer (command/agent/workflow + version), inputs (digests),
         commit, timestamp, digest, retention class, and an optional Ed25519 receipt binding.
      2. `praetorctl artifact put|get|list|gc|export` and MCP tools (`standards_artifact_*`), plus
         automatic capture from `audit`, `gate`, `dogfood`, `sandbox run`, and the fleet workflow scripts
         (findings, verdicts, batches, reports, journals, receipts, sandbox logs).
      3. Ledger rendering: `praetorctl state sync` derives `BUGS.md`/`STATE.md`/resume documents from the
         artifact graph (findings artifacts -> bug rows; run artifacts -> state entries); hand edits become
         unnecessary, and the current "hook stages the ledger" side effect disappears.
      4. `praetorctl session resume`: rebuild working context (what was done, what is pending, which branch,
         which artifacts) from the store, so a new session or machine continues without transcript archaeology.
      5. Optional publishing targets for sharing (an Artifact page / static site export / release asset),
         with the same provenance manifest attached.
      Depends on the layered config workstream (store location, retention and publishing are config).
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
      5. Blockers-first scheduling (user direction 2026-09-12): tasks in `OPEN.md`, epics and milestones carry
         `blocks:`/`blocked-by:` relations (the issue reconciler already parses `Depends-On:` for epics); the
         dispatcher computes the unblocked frontier ordered by blocking degree and fans out only on it, holding
         downstream work that a pending blocker would change - this is the primary lever for reducing token use.
      Reference implementation of items 2-3 lives in the deep-audit workflow scripts
      (`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/wf2-fix.mjs`: `A()` breaker,
      `subset` halves, resume-from-branch prologue).
- [ ] **Hermetic dynamic verification as a fleet-wide, configurable harness rule.** Per user direction
      (2026-09-12): agents must be able to spin up tiny ephemeral clones inside the repository's
      devcontainer and test against real running code, instead of inferring behaviour by reading, for
      every repository that adopts praetor, not just this one. Motivation: the deep audit had to stay
      read-only because the mutating gates (`make verify-all`, `standardsctl audit/gate/state sync`,
      lefthook) touch the tree and `$HOME`; inside a container none of that matters, and dynamic checks
      cost far fewer tokens than reasoning. Scope:
      1. Extend ADR-0004's ephemeral-worktree gating with a container backend: `praetorctl sandbox run
         [--image auto] -- <cmd>` clones the current worktree (or a branch) into a throwaway directory,
         mounts it into the repo's devcontainer image (built from `.devcontainer`/`docker/dev/Dockerfile`,
         cached by digest), runs the command as the invoking uid with `HOME`/`GOCACHE`/`GOPATH` inside
         the clone, and returns exit code + trimmed output; clone and container are removed afterwards.
      2. Expose it over MCP (`standards_sandbox_run`, mutating=false from the repo's point of view) so any
         agent, editor or workflow can request a real run; wire the paperclip harness and the fleet
         workflow scripts to prefer it for tests, gates, fuzz batteries and reproductions.
      3. Make it configurable in `.standards.yaml` (`verification: {sandbox: devcontainer|docker|podman|off,
         image: <ref|auto>, mode: required|preferred, resources: {cpus, memory, timeout}}`) with a
         praetor fleet default of `preferred`, and enforce `required` for adopted repos that declare the
         `agent:sandboxed` facet (this repo already declares it, and it currently means nothing).
      4. Scaffold the devcontainer during `adopt` for repos that lack one (the generator exists in
         `internal/devcontainer`), so the rule can hold across the fleet.
      Prerequisite: the layered config workstream below (the `verification:` block needs schema + typed access).
- [ ] **Application config layer and explicit dependency wiring.** Per user question (2026-09-12) about
      whether the Go application has a real config system or DI framework: it has neither. `internal/config`
      parses only the governance manifest `.standards.yaml` (metadata, profiles, facets, three override
      blocks) with no defaults/file/env/flag layering, no schema validation (the `# Schema:` URL is
      unreachable), no typed subsystem sections, and the advertised `.config/archetypes/**` are never
      loaded; complexity caps are parsed but enforced by nothing. No DI container is used. Decision
      recorded here: do NOT adopt `go.uber.org/fx` (new dependency, reflection-based graph, at odds with
      the single-dependency policy and HISS's explicit acyclic wiring); instead implement the
      `config.yaml` capability the repo already declares as *required* toward golusoris (`.needs.yaml`,
      status `gap`) - layered sources (built-in defaults -> `.config/archetypes` + facets -> `.standards.yaml`
      -> environment -> flags), a published JSON schema that `audit` validates against, typed sections per
      subsystem (`verification`, `providers`, `routing`, `receipts`, `budget`), and constructor injection
      of resolved config into subsystems (kill the remaining package-level registries). Deliver in
      `internal/config` first, upstream to golusoris when its `config.yaml` capability lands.

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

### Discharged Tasks [2026-09-12 16:19:17 UTC, commit `local`]
- [x] Continue Claude: implement and verify the staged lefthook gates before fix-wave integration (completed: 2026-09-12)
- [x] Continue Claude: recover G09 work and integrate wave A half 1 while preserving all fixes (completed: 2026-09-12)
- [x] Continue Claude: synthesize ADR-0009 and preserve accepted config-first consolidation decisions (completed: 2026-09-12)
- [x] Continue Claude: integrate verified secure-write permission and pre-truncation failure fix (completed: 2026-09-12)
- [x] Development MCP: connect agents to this checkout and verify real tool behavior before implementation (completed: 2026-09-12)
- [x] Integrate committed G09 core with G01-G08 and verify fail-closed scan consumers (completed: 2026-09-12)
- [x] Integrate full G10 preserving G01-G08 safety and truthful harvester outcomes (completed: 2026-09-12)
- [x] Publish verified WIP checkpoints through the approved checkpoint branch policy (completed: 2026-09-12)
- [x] Run automatic dogfooding against public non-owned repositories with replayable evidence (completed: 2026-09-12)
- [x] Discover and replay retained lusoris/praetor harvesting and memory ingestion data (completed: 2026-09-12)
- [x] Run and close a bounded public non-owned repository dogfood loop (completed: 2026-09-12)

### Discharged Tasks [2026-09-12 16:55:49 UTC, commit `local`]
- [x] Activate fresh local Praetor binaries and verify development MCP consumers (completed: 2026-09-12)
- [x] Implement bounded context-file optimization through CLI and MCP with provenance and behavior checks (completed: 2026-09-12)
- [x] Wire declared-task and capability routing to a deterministic cost-aware CLI selector (completed: 2026-09-12)
- [x] Prepare owner-fork updates from reviewed upstream commits while preserving configuration overlay (completed: 2026-09-12)
