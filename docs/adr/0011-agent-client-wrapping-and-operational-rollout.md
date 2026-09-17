# ADR-0011: One Hook Entrypoint, Plugin-First Client Wrapping, and a Governed Operational Rollout

## Status

Proposed — 2026-09-17.

## Context

Per operator direction, Praetor must wrap every agent client and plugin, Antigravity IDE and CLI first, on
Linux, Windows and macOS. Operator settings belong in the private operational fork, workstations follow the
fork automatically (on push now, on release later), nothing may be hard-coded to one operator, one behaviour
has one implementation (HISS-19), and a gate that cannot run must say so (HISS-21).

Measured state, read in this tree at `c5a68eb`. Hook and plugin schema facts come from the Antigravity
documentation fetched on 2026-09-17; host facts were measured with `agy` 1.2.5 on Linux.

| Fact | Evidence |
| :-- | :-- |
| All eleven native hook registrations are `python3 -B "$(git rev-parse --show-toplevel)/…"`: a POSIX substitution plus an interpreter name a stock Windows install lacks | `.claude/settings.json:9,19,31,42`, `.codex/hooks.json:9,21,32`, `.gemini/settings.json:9,19,31,42` |
| The agent-hook path is three process hops (adapter, Lefthook, evaluator) and the Lefthook agent jobs hard-code `python3` | `.config/agent/hooks/command_guard.py:22-25`, `.config/lefthook/praetor.yml:6,10,15,19,23` |
| The command policy names one operator's organisations, and a second copy of it is embedded for adopters | `.config/agent/hooks/block_evasion.py:22-24`, `internal/adopt/hooks.go:18,121,253` |
| Antigravity has no adapter: lifecycle pinned `unsupported`, MCP is argv only, `apply` refuses it | `internal/clientsetup/capabilities.go:38-41`, `capabilities_test.go:27`, `plan.go:54,95`, `cmd/standardsctl/clients.go:113` |
| An Antigravity plugin already ships in the tree (manifest, agents, skills), projection-verified both ways | `.agents/plugins/praetor/`, `cmd/standardsctl/agent_projection.go`, `skill_projection.go` |
| Antigravity hook schema: named top-level key; `PreToolUse`/`PostToolUse` use `{matcher, hooks[]}` groups; `PreInvocation`/`PostInvocation`/`Stop` take a flat handler list; `timeout` in seconds, default 30; stdin `toolCall.name/args`, `workspacePaths`, `conversationId`; PreToolUse decisions `allow deny ask force_ask deny_unless_prior_grant`; PostToolUse returns `{}`; Stop `decision: continue` | vendor documentation; the group form is also on disk in a vendor plugin on the measured host |
| `agy plugin validate` counts components but accepts a malformed `hooks.json` with exit 0 | host probe |
| `clientsetup` is pure by contract; write, backup and readback live in `internal/contextopt` and are composed in `publishClientPlan` | `internal/clientsetup/registry.go:1-4`, `cmd/standardsctl/clients.go:84-107` |
| The MCP registry requires a clean absolute executable path, so any file rendered from it is host data | `internal/clientsetup/registry.go:88` |
| Operational sync allows exactly four owner paths; any other owner-only file fails `plan` and `prepare`; the manifest is deep-compared, so a fork cannot pin its own receipt key | `internal/operationalsync/overlay.go:15,180-186`, `sync.go:266-289`, `prepare.go:121` |
| A fork whose manifest still declares the source identity fails `plan` before that | `internal/operationalsync/identity.go:30-35` |
| The `workstation` policy layer exists, tolerates unknown root keys and owns `complexity` only; nothing in a home directory is discovered for an audit | `internal/config/effective_load.go:184-205`, `effective_yaml.go:93-94`, `docs/guides/effective-policy.md:89-90` |
| No workflow carries a repository guard | #189 |

## Decision

1. **One hook entrypoint.** `praetorctl hook <client> <event>` is the only string a client registration
   contains: one executable call, valid under `sh -c` and `cmd /c`. A table-driven dialect per client decodes
   stdin into one canonical payload and encodes one verdict. The workspace comes from the payload, never from
   the process working directory.
2. **Go-native command policy, no Lefthook hop on the agent path.** The command policy runs in process.
   Built-in evasion patterns stay in the engine; operator-specific patterns move to
   `hooks.command_policy.deny` in operator settings. The checkpoint evaluators stay Python for now and are
   executed directly through a resolved interpreter (`python3`, `python`, `py -3`). Lefthook's agent jobs
   remain as the manual fallback and call the same entrypoint.
3. **Plugin-first wrapping.** The in-tree Antigravity plugin gains `hooks.json` and `rules/AGENTS.md` through
   the existing projection. The global wrap is the same tree installed under the resolved global customization
   root. MCP never enters the tracked plugin; it goes through the existing merge adapter into the user's
   `mcp_config.json` with snapshot, compare-and-swap, backup and readback.
4. **One per-OS root helper**, pure, with the environment injected, used by client setup and the harvester.
5. **Lifecycle states are derived, not asserted.** `Capabilities()` stays static. `clients verify` performs the
   I/O and reports `installed`, `discovered`, `observed`, `drifted`, `stale`. Only `observed` (a native
   session ran the hook after install) may be presented as enforcing.
6. **Operator settings extend the existing layered policy documents** with three owned sections (`clients`,
   `hooks`, `update`). Selection stays explicit. The one persisted selection is the install manifest written by
   an explicit operator command; `audit` and `gate` never read it.
7. **Operational sync learns owner-only operator paths and an `init` stage.** Prefixes are engine schema and
   must be git-ignored upstream; a path qualifies only when it is absent at base and source. Fork receipts
   travel as git notes and are verified against a key held in operator settings.
8. **Auto-update is pull-based at both hops.** The fork polls upstream and promotes a verified candidate by
   fast-forward; each workstation runs one engine command from its native scheduler. A pin holds a
   workstation; rollback restores the previous install and sets the pin.
9. **Every other client uses the same four seams** (dialect row, registration row, root row, merge adapter).
   A launcher (`praetorctl clients launch`) exists only for clients without a native hook surface.

## Alternatives considered

- **A lifecycle state machine with a new executor package, `clients setup` and a settings pointer file
  first.** Better long-term map, but three new packages and eight serial changes before Antigravity is
  wrapped, a second operator path to the same MCP write, and a home-directory pointer that contradicts the
  effective-policy guide. Its cross-OS and configurability pieces are adopted; its machinery is not.
- **`praetorctl hook <event>` without a client argument.** Deny encodings differ per client (exit 2 plus
  stderr versus a stdout decision document), so one argument cannot select a dialect.
- **A tracked `mcp_config.json` inside the plugin.** Would commit one host's absolute paths.
- **Release-per-push with signed artifacts as the first channel.** Needs a fork credential on every workstation
  before anything works; kept as the later `release` channel.
- **Trusting `agy plugin validate` as shape validation.** Measured to pass malformed hooks.

## Consequences

- Positive: registrations become host-neutral; Windows and macOS stop depending on a POSIX shell and a fixed
  interpreter name for the command guard; the operator's names leave the engine; Antigravity is wrapped in six
  small changes; adding a client is rows plus fixtures.
- Positive: the fork becomes checkable by the engine's own `plan`, including its operator files.
- Negative: the checkpoint evaluators still need Python until they are ported; a host without any interpreter
  gets a stated skip on `post-tool` and a block-once on `stop`.
- Negative: a changed registration string changes the Codex trust hash; re-trust is a named native action.
- Negative: `installed` and `discovered` are configuration, not enforcement; operators will see
  "configured, enforcement unverified" until a real session leaves a receipt.

## Checkable clauses

This record declares no machine-checked clause yet, on purpose. Its one clause forbids the Python hook
adapters from returning, and those files still exist: declaring it now would make the record fail its own
replay under `standardsctl adr verify`. The change that deletes the adapters adds the clause in the same
commit, as an `adr-constraint` block with this content:

```yaml
id: agent-hook-adapters-stay-deleted
kind: forbidden-path
forbids:
  - ".config/agent/hooks/command_guard.py"
  - ".config/agent/hooks/codex_pre_tool.py"
  - ".config/agent/hooks/checkpoint.py"
  - ".config/agent/hooks/checkpoint_scope.py"
  - ".config/agent/hooks/block_evasion.py"
rationale: >-
  One behaviour, one implementation. The Go entrypoint replaced these adapters; a returning copy would be a
  second command policy that drifts from the first and re-introduces operator names into the engine.
```

## References

- Issues #167, #169, #170, #172, #176, #188, #189, #107, #56, #66, #72, #135.
- `docs/plans/ide-agent-setup.md` (completion contract), `docs/guides/effective-policy.md`,
  `docs/guides/operational-sync.md`, ADR-0003.
- `https://antigravity.google/docs/hooks`, `https://antigravity.google/docs/plugins` (fetched 2026-09-17).
