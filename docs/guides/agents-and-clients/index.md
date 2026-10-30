# Agents, clients and model routing

These guides cover how coding agents and their clients are wired to a governed repository:
lifecycle hooks, client and editor configuration, the context and transcript tools, and how
work is routed to a model.

Read [Agent lifecycle coverage](../agent-lifecycle.md) first: it separates the shared
repository policy from the client-specific adapters the other pages configure.

- [Agent lifecycle coverage](../agent-lifecycle.md): the shared Lefthook jobs and
  `AGENTS.md` context versus each client's native lifecycle adapter.
- [Context cache bands](../context-cache-bands.md): the head, config and tail markers in
  `AGENTS.md` that keep the compiled files a stable prompt prefix, and `--verify-stable`.
- [Agent hooks](../agent-hooks.md): the single `praetorctl hook <client> <event>` entrypoint
  and the native hook coverage of each client.
- [Issue claims](../issue-claims.md): `praetorctl issue claim`, `status` and `release` report
  which session works an issue and at which stage; the dispatch gate refuses an issue another
  live session holds.
- [Shared client configuration](../client-bootstrap.md): `praetorctl clients` projects the
  MCP server registry into client settings.
- [Client schemas](../client-schemas.md): the pinned upstream schemas every rendered client
  config and hook fixture is checked against, the types generated from them and the formats
  that have none.
- [Configurable backend connections](../client-connections.md): `praetorctl clients connect`
  applies one private deployment profile to that registry.
- [Repository-aware editor generation](../editor-capabilities.md): `praetorctl editors
  generate` and `verify`, and the repository capabilities both resolve.
- [Local context review candidates](../../context-optimizer.md): `standardsctl
  context-optimize` proposes smaller rules, skills and memory files for review.
- [Local transcript ingestion](../transcript-ingestion.md): `praetorctl harvest transcript`
  caches Antigravity and Claude Code transcript events privately.
- [Declared-task model routing](../../standards/model-routing-and-fanout.md): offline model
  selection with `praetorctl models route` from `.config/models/routing.yaml`.
- [Tribunus data sync](../../tribunus/data-sync.md): the data the Tribunus graph router
  needs before it can route work to a model.

## Keep tracked context within the selected clients

The `agent_clients` list in `.standards.yaml` selects the context projections to generate
and verify. The `editor-selection` gate in `praetorctl audit` refuses a Git-tracked context
file belonging to an unselected client. Delete the tracked file, or add that client to
`agent_clients`. An untracked local copy passes. Omitting `agent_clients` selects every
supported client; an explicit empty list selects none. A client's size cap (for example
`.windsurfrules` at 12000 characters) applies only to a selected client; see
[editor capabilities](../editor-capabilities.md#selecting-agent-clients).

Adoption reports each tracked excluded context file as an action requiring deletion and
leaves the file available for review. The authoritative paths come from
`internal/agentcontext/render.go` (`VendorTargets`); the gate and adoption share
`internal/agentcontext/selection.go`. `TestRepositoryChecksEditorSelection` and
`TestExcludedContextAdoptionAction` in `internal/adopt/` exercise these cases.

The separate `editors` list selects IDE settings and does not select agent-context files.
