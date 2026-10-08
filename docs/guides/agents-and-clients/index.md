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
- [Shared client configuration](../client-bootstrap.md): `praetorctl clients` projects the
  MCP server registry into client settings.
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
