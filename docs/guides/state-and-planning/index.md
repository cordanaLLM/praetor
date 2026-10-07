# State ledger and planning

These guides cover the private state Praetor keeps for a repository and the planning built
on it: the `.workingdir/` ledger, planning drafts, the wishes ledger, issue synchronization,
the cleanup of released resources and the research and upstream radar.

Read [State ledger integrity](../state-ledger-integrity.md) first to understand the `.workingdir/` ledger, which the planning and wishes tools rely on. The issue synchronization and garbage collection tools operate independently of it.

- [State ledger integrity](../state-ledger-integrity.md): the private `.workingdir/` ledger,
  its audit and the rules that keep it consistent.
- [Detailed planning drafts](../planning-drafts.md): `praetorctl planning prepare` compiles
  a typed draft into linked proposal artifacts.
- [Wishes and polls](../wishes-and-polls.md): the local, bounded ledger for collecting and
  reviewing ideas.
- [Issue inventory and synchronization integrity](../issue-sync-integrity.md): why issue
  synchronization needs a complete inventory before it changes any issue, and the planning
  sync that ticks parents' boxes and closes finished epics and milestones.
- [Released-resource garbage collection](../garbage-collection.md): `praetorctl gc` dry-runs
  by default and removes only resources their owner released.
- [Research and upstream radar](../radar.md): `praetorctl radar` validates a source registry
  and collects a window of upstream releases and feeds into a neutralised digest.
