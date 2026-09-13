# Praetor Governance

Praetor defines and verifies repository governance across a fleet. Shared policy
and generated tooling keep each repository consistent with its declared needs.

## Language

**Repository archetype**:
A category describing the kind of repository and the structure it requires,
such as an application service or a framework.
_Avoid_: Release track, release channel

**Release track**:
A named release progression with its own version and promotion rules, such as
stable or bleeding.
_Avoid_: Repository archetype, repository flavor

**Resolved policy**:
The effective governance constraints for a repository after its applicable
standards and explicit stricter requirements have been combined.
_Avoid_: Manifest, configuration file

**Demand**:
A repository's declared need for a capability that may already exist, require an
adapter, or remain a gap in the shared framework offering.
_Avoid_: Installed dependency
