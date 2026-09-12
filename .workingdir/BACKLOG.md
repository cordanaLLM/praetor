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
