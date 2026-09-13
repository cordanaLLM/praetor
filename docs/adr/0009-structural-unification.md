# ADR-0009: Structural Unification Before Further Fix Waves

## Status

Proposed — 2026-09-12. The owner has accepted the sequencing and constraints
below; the module boundaries and milestones are the implementation proposal.

## Context

The deep audit left independently fixed branches that overlap in authentication,
forge transport, source analysis, configuration and generated files. Continuing
the remaining fix waves against those duplicate implementations would multiply
integration work. The owner therefore directed us to finish the first fix batch,
then deduplicate and unify before dispatching the next batch.

The saved design workflow stopped during research. This record completes its
local design synthesis; it does not claim that the refactor has been implemented.
The source observations below refer to the audit branch at `e706904` and must be
checked again after integrating the first batch.

| Concern | Existing implementation | Unification boundary |
| :--- | :--- | :--- |
| Policy | `internal/config` has manifest parsing and a strictness join; subsystem defaults still diverge | Resolve policy once and pass it explicitly to consumers |
| Authentication | `internal/util` has a bounded token resolver; CLI commands also resolve tokens | Reuse the existing resolver before considering a new package |
| Forge operations | `internal/forge` and `internal/milestone` contain remote transport | Keep local milestone persistence separate from remote operations |
| Source analysis | `internal/hiss` and the LSP command implement overlapping checks | Share analysis and policy; retain protocol formatting in the LSP |
| Templates | Files under `templates/` coexist with generator string literals | Embed and migrate one verified asset family at a time |
| Personas | `CompileAgents` already projects `.agents/agents` into vendor directories | Extend that generator and add drift verification |

Repository archetypes and release tracks are different concepts. The similarly
named `internal/flavor` and `internal/flavors` packages are not interchangeable.
The former describes repository structure; the latter manages release tags.
Renaming a package may clarify that distinction, but merging these domains would
obscure it.

## Decision

### Accepted constraints

1. Complete and verify the Lefthook prerequisite, then integrate G01–G10 serially,
   including G07a/G07b. Preserve both sides of overlapping fixes.
2. Hold G11–G22 and the structure-sensitive N04/N05/N06 groups until unification
   and finding reassessment are complete. Dispatch only unblocked tasks.
3. Keep one root Go module and its existing `gopkg.in/yaml.v3` dependency. Use
   explicit constructor arguments rather than a dependency injection framework.
4. Deliver layered configuration in `internal/config` first. A shared library
   extraction follows demonstrated reuse and an explicit migration decision.
5. Preserve ADR-0008's decisions: its parser belongs in a separate `tools/specc`
   module; merge existing forge fixes before superseding them; provider v1 covers
   GitHub, GitLab and Gitea/Forgejo. Provider implementation follows unification.

### Proposed implementation

Resolve configuration from built-in defaults, archetypes/facets, the repository
manifest, environment and flags, in that order. Separate operational settings
from governance constraints: later operational sources override earlier values,
while governance constraints can only become stricter.

Retain and test the strictness join rather than deleting it as currently unused.
For positive complexity limits the join selects `min(a, b)`; mandatory controls
use logical OR; minimum assurance levels use `max(a, b)`; required tool sets use
set union. An absent value is distinct from an invalid zero or negative explicit
limit. Resolution must report invalid input rather than silently relaxing policy.
The resolver must not alias mutable input slices or maps into its output.

Introduce one vertical slice first: resolved complexity limits consumed by audit
and shared source analysis. Add typed verification, provider, routing, receipt and
budget settings incrementally with their actual consumers, schema, defaults and
documentation. Do not publish inert settings that imply unsupported behavior.
Keep the result immutable by convention and inject it through constructors.

Authentication should use `util.ResolveAuthTokenContext` under the caller's
deadline, preserving explicit token → `GITHUB_TOKEN` → `GH_TOKEN` → bounded
`gh auth token` precedence. Preserve cancellation behavior and prevent tokens
from entering diagnostics. Remove duplicate resolvers only after their callers
and tests use the canonical implementation.

Centralize remote transport independently from authentication. Preserve each
operation's pagination, response-size and retry limits when moving it; differing
validated limits are not interchangeable defaults. Keep HTTP request execution
behind an injected client and retain provider fixtures as a differential oracle.

Move reusable Go analysis into `internal/hiss`, with adapters for filesystem
scans and LSP buffers. CLI and LSP must use the same resolved thresholds and
produce equivalent rule identities and source spans for equivalent input. Keep
LSP framing, URI handling and diagnostic serialization in the command adapter.

Embed templates by family after comparing existing generated output with the
candidate assets. Start with deterministic session-state scaffolds, then adopt,
archetype, editor and harness outputs. A migration must update every active
consumer, remove the replaced literal and prove regeneration is idempotent.
User-owned files retain their documented merge and preservation behavior.

Extend `CompileAgents` for remaining active targets and read-only drift checks.
Check plugin manifests and references before retiring duplicate persona names.
Likewise, remove orphan packages only with their tests, imports, fuzz targets,
documentation and generated references. A zero production-import count alone
does not establish that an exported capability can be retired.

### Checkpoint sequence

| Milestone | Required exit evidence |
| :--- | :--- |
| 0. Hooks and first fix batch | Hook positive/negative tests; integrated build, race tests and explicit remaining gate failures |
| 1. Config foundation | Published schema and typed complexity slice; precedence, invalid-input and strictness tests |
| 2. Authentication and transport | One resolver; preserved operation bounds; cancellation and provider fixture tests |
| 3. Shared source analysis | CLI/LSP parity fixtures at, below and above each policy threshold |
| 4. Assets and persona projections | One owner per migrated family; idempotence, drift and user-content preservation tests |
| 5. Complete retirements | No dangling imports, commands, generated references, docs or fuzz targets |
| 6. Finding reassessment | Each finding linked to surviving code, a verified fix or a justified retirement |

Commit each validated checkpoint. Do not advance a finding to fixed merely
because its old path disappeared, or because a branch claims to fix it. Resume
the held groups only against the reassessed findings. The existing fold script
rebuilds audit data; it does not establish semantic closure after a refactor.

## Consequences

### Positive

- Shared behavior receives one fix and one policy definition, reducing drift.
- Explicit dependency wiring keeps the call graph inspectable under HISS-01.
- Bounded operations and error propagation remain visible under HISS-02/HISS-07.
- Family-sized changes preserve reviewability and recoverable checkpoints.

### Negative / Trade-offs

- First-batch integration still requires resolving overlapping fixes carefully.
- Existing differing defaults require explicit migration tests and documentation.
- Keeping adapters during migration temporarily retains some duplication.

### Neutral

- Config filenames remain `.standards*`; the planned binary rename is separate.
- Cross-repository library ownership, compatibility floors and publication are
  later decisions and do not block the in-repository milestones above.

## Verification & Compliance

Use positive, negative and boundary tests at public interfaces (HISS-15), and
property tests for join commutativity, associativity, idempotence and monotonicity.
Run the relevant build/lint/test gates at each checkpoint and `make verify-all`
before reporting the integrated result. Report failures faithfully; do not add
exclusions to obtain a passing gate. Source comparisons and full-gate execution
belong in isolated checkouts so fixtures cannot change the working repository.

Regenerate vendor context from canonical `AGENTS.md` whenever instructions
change (HISS-16). Record task progress and synchronize the state ledger
(HISS-17). Pure documentation changes retain lightweight invariant checks
without triggering unnecessary heavy source checks (HISS-18).

## References

- Resume state, accepted owner decisions and workstream tracking are retained in
  the private, Git-ignored `.workingdir/` ledger.
- [ADR-0002: Strictness lattice](0002-highest-standard-wins-lattice.md)
- [ADR-0007: Demand deduplication](0007-universal-frameworks-org-and-demand-deduplication.md)
- [ADR-0008: Provider integration](0008-spec-driven-provider-integration.md)
