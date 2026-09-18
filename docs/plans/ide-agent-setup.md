# IDE-driven setup and enforcement for coding agents

The goal is that installing a Praetor IDE extension provides the setup and
reconciliation entry point for **every agent selected by effective policy**.
The same service must work from a CLI, development container and private GitOps
configuration. Setup covers wrappers, shared tools, instructions, skills,
lifecycle checks, model routing, budgets and private memory bindings. Users
should configure the desired result once and receive concrete repair actions
when observed state differs.

## One shared implementation

Keep policy resolution and setup operations in Go. Extend `internal/clientsetup`
and the `praetorctl clients` command family; reuse existing adoption, context
compilation and snapshot replacement services. IDE extensions provide workspace
selection, review, native host integration and result presentation. They do not
copy another client registry, implement independent installers, or invent
successful enforcement states.

The first implemented connection uses `clients capabilities` and
`standards_client_capabilities` to expose a versioned inventory derived from the
existing adapter registry. Its lifecycle definitions are explicitly unverified
at runtime. The VS Code family setup command uses that inventory to select a
client and invoke the existing configuration preparation/application pipeline.
This delivers the MCP configuration stage; complete wrapper installation and
native activation remain open work.

## Completion contract

| Stage | Required evidence | Incomplete outcome |
| --- | --- | --- |
| Resolve | Effective policy, selected agents, scope, execution host, versions and destinations | Conflict or unsupported selection |
| Prepare | Bounded plan with source hashes, explicit operations, preserved user settings and required privileges | Missing dependency or review required |
| Install | Versioned wrapper/tool artifacts, ownership manifest, backup and exact readback | Failed or partially applied operation |
| Configure | Merged context, skills, tool connections, hooks, routing and memory references | Conflict, stale plan or unsupported adapter |
| Activate | Native client discovery, permission/trust completion and session reload acknowledgement | Restart, trust or native action required |
| Verify | Named harmless tool call, isolated rejected-before-execution control and checkpoint event from that native session | Configured but enforcement unverified |
| Reconcile | Fresh evidence bound to binary, configuration, policy and session identity | Drift or stale activation |
| Repair/remove | Reviewed bounded repair or removal of owned artifacts only, with rollback/readback | Partial cleanup, retained user changes |

Installation or an exit-zero configuration command cannot substitute for the
activation and verification stages. Required-agent failures must block Praetor's
managed launch in strict mode. A documented advisory mode may expose the gap;
it must never display the same passing state. Git and server-side verification
continue to protect repository changes regardless of the editor used.

## Configuration and ownership

- Resolve explicit workstation, organization, repository, container and private
  GitOps settings through the existing policy system. Preserve source ownership
  and report conflicts rather than choosing an undocumented precedence.
- Select the agents actually used. Do not install every provider CLI, model or
  skill bundle. Reuse the template matrix and capability registry.
- Bind each installation to its actual execution host. A remote extension host
  configures its remote environment; a local desktop, container and cloud agent
  do not share a home directory or loopback endpoint by implication.
- Keep credentials in external secret references. Plans and receipts should
  contain identities, hashes and observations, with sensitive paths retained
  privately. Never publish workstation inventories automatically.
- Preserve incumbent hooks and unmanaged client settings. Changes need
  conflict detection, durable backups, bounded retries and verifiable readback.
- Keep managed ownership, lease/session expiry and garbage collection aligned
  with the existing lifecycle work. An abandoned session must not leave a
  permanent green enforcement indicator.

## Next implementation stages

1. Add `clients doctor` and a versioned setup profile with explicit selected
   clients and destinations. Observe binaries, configurations and evidence;
   report missing, unsupported, conflicted and stale states separately.
2. Add wrapper and lifecycle reconciliation to the existing preparation/apply
   service. First qualify the existing Codex, Claude Code and Gemini definitions
   using the actual installed versions. Resolve competing configuration scopes
   before writing them. Generate shared wrappers from one owned artifact source.
3. Add native activation adapters and private session receipts. Support native
   approval workflows instead of changing another application's trust database.
   Verify tool discovery, a named call, guard rejection and checkpoint events.
4. Extend the adapter matrix to independent clients and IDE families, with
   fixtures and real host qualification for each supported version. Publish
   tested extension artifacts before claiming marketplace or installation
   readiness. JetBrains/Neovim configuration generation alone is not equivalent
   to an installed plugin.
5. Add drift repair, upgrades, interrupted-operation recovery and uninstall.
   Test multiple workspaces and concurrent IDEs, offline operation, read-only
   homes, changed schemas, native reload requirements and competing editors.

### How ADR-0011 maps onto these stages

[ADR-0011](../adr/0011-agent-client-wrapping-and-operational-rollout.md) decides
how the stages are built. It does not reorder them, and nothing below is
implemented by the record itself.

| Stage above | ADR-0011 decision | What it contributes |
| --- | --- | --- |
| 1. Observe, setup profile | 5, 6 | Explicit client selection in the `clients` policy section; an install manifest written only by an explicit operator command; `clients verify` reporting states separately |
| 2. Wrappers, lifecycle reconciliation | 1, 2, 3, 4 | One `praetorctl hook <client> <event>` registration string, a Go command policy, the plugin tree as the single owned wrapper source, one per-OS root helper, MCP through the existing merge adapter |
| 3. Native activation, session receipts | 5 | States derived by `clients verify`; only `observed`, a native session that ran the hook after install, counts as enforcing |
| 4. Adapter matrix | 9 | Each further client is a dialect row, a registration row, a root row and a merge adapter, with recorded fixtures; a launcher only where no native hook surface exists |
| 5. Drift repair, upgrades, uninstall | 7, 8 | Owned-tree removal, pull-based workstation update with pin and rollback, operator files carried by operational sync |

The completion contract is unchanged: `installed` and `discovered` are the
Configure stage, and they are never displayed as the Verify stage.

The [lifecycle coverage guide](../guides/agent-lifecycle.md) records current
adapter coverage. The [VS Code research](../research/ide-agent-bootstrap.md)
records host constraints and test requirements. These boundaries make the goal
testable; the inventory and setup entry point do not complete it by themselves.
