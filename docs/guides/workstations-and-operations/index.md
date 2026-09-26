# Workstations, devcontainers and operations

These guides are for the operator: installing the engine on a workstation, building
devcontainers, moving work between machines, and supplying the operational configuration
the engine acts on.

Read [Workstation install and status](../workstation-update.md) first.

- [Workstation install and status](../workstation-update.md): `praetorctl workstation
  install` and `status` on every operating system.
- [Portable DevContainer bootstrap](../devcontainer-bootstrap.md): building a devcontainer
  from an explicitly selected Praetor source snapshot.
- [Local repository observations](../workstation-inventory.md): the workstation harvester's
  private report of local Git repositories.
- [Workstation sync with devsync](../devsync.md): moving project folders between
  workstations through rclone and Google Drive.
- [Operational configuration](../operational-configuration.md): which settings belong to
  the engine and which the operator supplies.
- [Operational fork synchronization](../operational-sync.md): the `init`, `plan` and
  `prepare` stages of `praetorctl operational sync`.
