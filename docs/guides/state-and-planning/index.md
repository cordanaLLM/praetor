# State ledger and planning

These guides cover the private state Praetor keeps for a repository and the planning built
on it: the `.workingdir/` ledger, planning drafts, the wishes ledger, issue synchronization
and the cleanup of released resources.

Read [Bug ledger integrity](../state-ledger-integrity.md) first: every other page here reads
or writes that ledger.

- [Bug ledger integrity](../state-ledger-integrity.md): the private `.workingdir/` ledger,
  its audit and the rules that keep it consistent.
- [Detailed planning drafts](../planning-drafts.md): `praetorctl planning prepare` compiles
  a typed draft into linked proposal artifacts.
- [Wishes and polls](../wishes-and-polls.md): the local, bounded ledger for collecting and
  reviewing ideas.
- [Issue inventory and synchronization integrity](../issue-sync-integrity.md): why issue
  synchronization needs a complete inventory before it changes any issue.
- [Released-resource garbage collection](../garbage-collection.md): `praetorctl gc` dry-runs
  by default and removes only resources their owner released.
