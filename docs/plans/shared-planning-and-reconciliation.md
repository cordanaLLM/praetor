# Shared planning and graph reconciliation

Status: incremental architecture plan. The first implementation is a structural
draft compiler; the later reconciliation and automation stages below remain open.

Praetor should represent relationships consistently across sources, requirements,
repositories, packages, templates, environments, agents, tasks, milestones and
evidence. Settings select desired capabilities, constraints, permissions and
objectives. Reusable graph templates describe supported arrangements. Existing
Go services observe the world, generate artifacts and perform authorized changes.

## One model, separate kinds of authority

| View | Authority | Meaning |
| --- | --- | --- |
| Desired | Explicitly selected configuration and accepted requirements | What a scoped project should become |
| Observed | Versioned source, API or runtime observations | What was actually examined, including omissions and age |
| Proposed | Comparison plus supported transformation rules | Candidate work; it grants no execution or publication authority |
| Evidenced | Results bound to exact inputs, policy and execution | Which outcomes were tested or observed at which scope |

Each entity needs a stable logical ID distinct from its revision or content hash.
Typed edges describe relations such as requires, provides, generated-from,
depends-on, implements, constrained-by and verified-by. Dependency edges used to
schedule work must be acyclic. Other relationships may contain cycles and must
be handled with bounded traversal rather than pretending the entire ecosystem is
a DAG. Schema and adapter versions travel with snapshots.

Use bounded graph slices for the active decision. There is no requirement for a
graph database or for loading the whole fleet into every agent's context. Machine
artifacts can be canonical compact JSON with separately generated human views;
compression must retain identity, source provenance and unknown states.

## From wishes to corrections

1. Import a selected research result, wish, planning artifact, finding or manager
   demand with source identity, revision, privacy and coverage. Keep proposed
   claims distinct from accepted requirements.
2. Normalize it into a desired capability and explicit constraints. Preserve
   conflicting requirements and unanswered questions. Do not guess deadlines,
   owners, prices, compatibility or acceptance evidence.
3. Observe the selected project and its current policy. Record both the requested
   and examined scope; missing data is an unknown, not an absent capability.
4. Compare typed capabilities and constraints. Classify differences as missing,
   incompatible, outdated, conflicting, orphaned, satisfied or unverified. Name
   the rule and source evidence responsible for every classification.
5. Resolve supported transformations from maintained adapters and versioned
   templates. Generate detailed actions, dependencies, outputs and acceptance
   checks. An unsupported transformation becomes an explicit capability request.
6. Project the same plan into TODOs, roadmap items and milestones, preserving
   stable IDs and existing human edits. Obtain any execution authority required
   by the effective policy.
7. Execute through existing services, observe the result, and compare again.
   Backfeed updates evidence and may propose follow-up work. It cannot turn a
   successful local command into fleet acceptance or silently authorize a retry.

Modernization follows these steps against a newer supported package or template.
Adoption follows them against a selected governance profile. Wish triage follows
them against a requested capability. A common comparison contract should replace
duplicated reasoning only after the corresponding adapter proves equivalent
behavior with actual consumer fixtures.

## Decisions and settings

“Best” means best under a stated objective and constraints. Cost, latency,
reliability, capability, license, platform and privacy may conflict. Keep hard
constraints separate from preferences; preserve units, observation time and
uncertainty in measured data. Unknown cost must not silently mean free.

Configuration resolution should expose the selected organization, group,
repository, environment and task layers, their authority, permitted overrides
and effective digest. Existing policy rules continue to govern their current
domains. This plan does not claim that a general RBAC or global graph policy
engine already exists. Review exceptions require an explicit setting and a
recorded owner decision, rather than being inferred from a failed gate.

Repeated comparison of unchanged snapshots must return the same plan. Every
write needs an expected prior revision/hash and a retained outcome. Partial
failure, concurrent edits, permission changes and missing adapters must remain
visible. Reconciliation needs bounded attempts and convergence checks to prevent
endless alternating corrections.

## Implementation roadmap

| Milestone | Ordered work | Exit evidence |
| --- | --- | --- |
| PG1: typed planning drafts | Define stable IDs and bounded schema; validate links, details and dependencies; render draft TODO, roadmap and milestone views; expose the same compiler through CLI and MCP | Deterministic output; positive, negative and boundary tests; real private write/readback; explicit unverified source provenance |
| PG2: grounded inputs | Adapt Notebook snapshots, revisioned wishes and needs reports; verify bytes/citations; preserve conflicts, omissions and source dispositions | Two distinct input adapters share one contract; changed sources invalidate dependent drafts; no fabricated decomposition or semantic approval |
| PG3: project reconciliation | Observe existing tasks and milestones; plan managed changes; apply through compare-and-swap; preserve human content and existing completion state | Repeat is a no-op; concurrent edits conflict; partial failures recover; exact project TODO/roadmap/milestone IDs read back |
| PG4: typed differences | Define desired/observed capability slices and transformation rules; add adoption and modernization adapters; explain every difference | Missing versus unexamined distinguished; version/API constraints tested; unchanged input yields no work; unsupported changes create bounded requests |
| PG5: evidence and decisions | Add verified backfeed, stale-input invalidation, objective-based ranking and agent budgets | Failed or partial execution cannot complete a plan; unknown costs stay unknown; held-out cases show claimed gains without quality loss |
| PG6: ecosystem adapters | Connect package/framework/template managers, IDEs, bots, runtime environments and enrolled repositories through their existing owners | Two real consumers per reused contract; private/public boundary tests; API/MCP parity; bounded convergence and rollback evidence |

PG1 prepares a **new private draft artifact directory**. It does not modify
existing project ledgers, reconcile a live roadmap, invoke a model, execute plan
instructions, verify source claims or publish forge objects. Those operations
belong to the subsequent milestones and must not be reported as completed by
structural validation alone.

Keep Notebook's existing wire contract compatible while introducing typed plans.
Use `internal/contextopt` for bounded snapshots and artifact writes, and existing
state, milestone, wishes, needs, forge and operational-sync services as adapters.
Retire duplicated implementations only after parity tests and an explicit
compatibility migration. The package pipeline, lifecycle and bidirectional
contract plans remain related authorities, not competing engines.
