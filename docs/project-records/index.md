# Project records

These are historical design records: plans and research written while features were being
designed. Each page states its status and the date or commit it was reviewed against, and
none is maintained as current documentation. For how Praetor behaves today, read the
[Guides](../guides/index.md), the [HISS specification](../standards/hiss-spec.md) and the
[architecture decisions](../adr/README.md).

## Plans

- [Bidirectional module contracts](../plans/bidirectional-contracts.md): audited design
  proposal for contracts between generated configuration, observations and repairs.
- [Golusoris core migration](../plans/golusoris-migration-plan.md): status of moving
  Praetor's code onto golusoris core.
- [IDE-driven agent setup](../plans/ide-agent-setup.md): the goal of IDE extensions that set
  up and reconcile every agent effective policy selects.
- [Infrastructure development environments](../plans/infrastructure-development-environments.md):
  researched strategy for VM and cluster backends, not implemented.
- [Shared lifecycle management](../plans/lifecycle-management.md): staged plan for agent
  session, workspace and state lifecycles.
- [Package development pipeline](../plans/package-development-pipeline.md): proposed
  configurable package development and reuse pipeline.
- [Shared planning and reconciliation](../plans/shared-planning-and-reconciliation.md):
  incremental plan from the planning draft compiler to graph reconciliation.
- [Upstream contribution workflow](../plans/upstream-contribution-workflow.md): proposed
  upstream contributions within the package pipeline.
- [Wish platform integration](../plans/wish-platform-integration.md): plan for forge and
  Discord projections of the wishes ledger.

## Research

- [Compact management data](../research/compact-management-data.md): locally benchmarked
  research on compact, configurable management data.
- [Public dogfood capability discovery](../research/dogfood-capability-discovery.md): the
  discovery run over 100 public repositories and what it found.
- [Go package reuse](../research/go-package-reuse.md): which mature Go packages to reuse
  instead of maintaining custom code.
- [HISS refinement audit](../research/hiss-rule-refinement.md): audit and scoped repair of
  the HISS rules.
- [IDE agent bootstrap](../research/ide-agent-bootstrap.md): VS Code agent bootstrap and
  safe `praetorctl` execution.
- [Infrastructure sandboxes](../research/infrastructure-sandboxes.md): isolation tiers for
  template-driven development.
- [Wish voting platforms](../research/wish-voting-platforms.md): ballots and votes across
  forges and Discord.
