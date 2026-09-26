# Dogfooding

These guides cover running Praetor against repositories it does not own, retaining the
evidence, and turning failures into reviewed repair candidates, by hand or on a schedule.

Read [Public repository dogfooding](../../dogfooding.md) first, then
[Configured dogfood and transcript replay suites](../dogfood-suites.md).

- [Public repository dogfooding](../../dogfooding.md): the retained dogfood loop against
  public repositories, pinned revisions and replay.
- [Configured dogfood and transcript replay suites](../dogfood-suites.md): versioned suite
  configuration that combines public repositories and private transcripts.
- [Capability discovery](../capability-discovery.md): `praetorctl dogfood discover` checks
  whether a capability adapter is available in a repository.
- [Scheduled local dogfood verification](../scheduled-dogfood.md): running configured
  suites on a schedule through the installed `praetorctl`.
- [Local dogfood scheduling](../dogfood-scheduling.md): `praetorctl dogfood schedule run`
  and `status`, one due suite per run.
- [Local dogfood failure triage](../dogfood-repairs.md): `praetorctl dogfood repairs` turns
  a failed suite report into a private review plan.
- [Bounded local repair execution](../dogfood-repair-execution.md): the executor that turns
  one routed failure into a retained candidate patch.
- [Local repair timer](../dogfood-repair-timer.md): a user timer that runs one bounded
  repair attempt per tick.
